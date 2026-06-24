// db_concurrency_test.go — Tests de concurrencia de la BD (Beta 8.2).
//
// Valida el fix del cuello de botella: antes SetMaxOpenConns(1) serializaba
// todo y bloqueaba lecturas durante operaciones largas. Ahora MaxOpenConns(8)
// + WAL permite lectores concurrentes sin "database is locked".
//
// Estos tests simulan el escenario real del bug:
//   - Muchas lecturas concurrentes (NimHealth, ListPools, reconcilers)
//   - Mientras hay escrituras en curso (instalación de app, ScanDevices)
//
// Si el pool estuviera mal configurado, veríamos "database is locked" o
// timeouts. El test falla si eso ocurre.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openConcurrencyTestDB abre una BD temporal con la MISMA config que
// producción (db.go): WAL, busy_timeout, MaxOpenConns(8), PRAGMAs.
func openConcurrencyTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "concurrency-test.db")

	conn, err := sql.Open("sqlite", dbFile+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	// Misma config que producción
	conn.SetMaxOpenConns(8)
	conn.SetMaxIdleConns(4)
	conn.SetConnMaxLifetime(0)

	// Tabla de prueba
	if _, err := conn.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, val INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	cleanup := func() {
		conn.Close()
		os.RemoveAll(tmpDir)
	}
	return conn, cleanup
}

// TestDBConcurrency_ReadsNotBlockedByWrites · el corazón del fix.
//
// Simula el bug real: lecturas (NimHealth, ListPools) que NO deben bloquearse
// mientras hay escrituras largas (instalación). Con SetMaxOpenConns(1) las
// lecturas se encolaban detrás de las escrituras. Con MaxOpenConns(8)+WAL
// corren en paralelo.
//
// El test lanza escritores y lectores a la vez. Si alguno falla con
// "database is locked" o el test tarda demasiado, el fix no funciona.
func TestDBConcurrency_ReadsNotBlockedByWrites(t *testing.T) {
	conn, cleanup := openConcurrencyTestDB(t)
	defer cleanup()

	// Sembrar datos
	for i := 0; i < 100; i++ {
		if _, err := conn.Exec("INSERT INTO items (name, val) VALUES (?, ?)", fmt.Sprintf("item-%d", i), i); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	const numReaders = 20
	const numWriters = 4
	const opsPerWorker = 50

	var wg sync.WaitGroup
	errCh := make(chan error, (numReaders+numWriters)*opsPerWorker)

	start := time.Now()

	// Lectores concurrentes (simulan NimHealth, ListPools, reconcilers)
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				var count int
				if err := conn.QueryRow("SELECT COUNT(*) FROM items WHERE val > ?", i%50).Scan(&count); err != nil {
					errCh <- fmt.Errorf("reader: %w", err)
					return
				}
			}
		}()
	}

	// Escritores concurrentes (simulan instalación, ScanDevices)
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				_, err := conn.Exec("INSERT INTO items (name, val) VALUES (?, ?)",
					fmt.Sprintf("w%d-%d", id, i), id*1000+i)
				if err != nil {
					errCh <- fmt.Errorf("writer %d: %w", id, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	elapsed := time.Since(start)

	// Verificar que NADIE falló (especialmente con "database is locked")
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		t.Fatalf("hubo %d errores en concurrencia (el fix no funciona). Primero: %v", len(errs), errs[0])
	}

	// Sanity: con 20 lectores + 4 escritores × 50 ops, esto debe ser RÁPIDO
	// (segundos, no minutos). Si tardara mucho, indicaría serialización/locks.
	if elapsed > 30*time.Second {
		t.Errorf("concurrencia tardó %v · demasiado, posible serialización", elapsed)
	}
	t.Logf("Concurrencia OK: %d lectores + %d escritores × %d ops en %v",
		numReaders, numWriters, opsPerWorker, elapsed)
}

// TestDBConcurrency_NoLockedErrors · estrés de escrituras concurrentes.
// SQLite solo permite un escritor, pero busy_timeout debe hacer que esperen
// en vez de fallar con "database is locked".
func TestDBConcurrency_NoLockedErrors(t *testing.T) {
	conn, cleanup := openConcurrencyTestDB(t)
	defer cleanup()

	const numWriters = 8
	const opsPerWorker = 30

	var wg sync.WaitGroup
	var lockedCount int
	var mu sync.Mutex

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				_, err := conn.Exec("INSERT INTO items (name, val) VALUES (?, ?)",
					fmt.Sprintf("s%d-%d", id, i), id)
				if err != nil {
					mu.Lock()
					lockedCount++
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	if lockedCount > 0 {
		t.Errorf("%d escrituras fallaron (probable 'database is locked') · "+
			"busy_timeout debería hacerlas esperar, no fallar", lockedCount)
	}

	// Confirmar que TODAS las escrituras llegaron
	var total int
	conn.QueryRow("SELECT COUNT(*) FROM items").Scan(&total)
	want := numWriters * opsPerWorker
	if total != want {
		t.Errorf("se escribieron %d items, esperados %d (se perdieron escrituras)", total, want)
	}
}
