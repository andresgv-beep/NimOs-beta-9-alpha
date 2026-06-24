// context_helpers_test.go — Tests del patrón commitContext.
//
// Bug Nextcloud regression test (26/05/2026):
//
// El bug original era que `r.Context()` se cancela cuando el cliente cierra
// la conexión HTTP. Si esa cancelación llega mientras una operación de
// persistencia (INSERT a docker_apps) está en curso, la query SQL aborta
// y deja el sistema en estado inconsistente.
//
// commitContext() devuelve context.Background() que NUNCA se cancela. Estos
// tests garantizan que:
//
//   1. commitContext() es estructuralmente diferente de un request context
//   2. commitContext() NO se cancela cuando el request context se cancela
//   3. Una query SQL con commitContext() completa aunque el cliente se vaya
//
// Si alguien refactoriza commitContext() en el futuro y rompe estas
// garantías (ej. lo deriva de r.Context()), estos tests fallarán y
// prevendrán el regreso del bug Nextcloud.

package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestCommitContext_NotCancellable verifica que commitContext() no tiene Done channel
// activo · es un context.Background() puro.
func TestCommitContext_NotCancellable(t *testing.T) {
	ctx := commitContext()

	// Background context.Done() devuelve un canal cerrado nunca · select default.
	select {
	case <-ctx.Done():
		t.Fatal("commitContext() debería ser context.Background() · NO cancelable. " +
			"Si esto falla, probablemente alguien lo derivó de r.Context() · " +
			"ver bug Nextcloud (26/05/2026)")
	default:
		// OK · no se cancela
	}

	if ctx.Err() != nil {
		t.Errorf("commitContext() devuelve Err() no nil: %v · debe ser nil siempre", ctx.Err())
	}
}

// TestCommitContext_SurvivesRequestCancellation simula el escenario exacto del
// bug Nextcloud: cliente HTTP se desconecta a mitad de operación, pero el
// código usa commitContext() para la persistencia.
//
// Verifica que:
//   - El request context SÍ se cancela
//   - commitContext() sigue válido
//   - Una query SQL con commitContext() completa con éxito
func TestCommitContext_SurvivesRequestCancellation(t *testing.T) {
	// Setup: BD SQLite en memoria con una tabla cualquiera.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE persist_test (id TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Simular un request HTTP cancelable.
	req := httptest.NewRequest("POST", "/api/test/persist", nil)
	reqCtx, reqCancel := context.WithCancel(req.Context())
	req = req.WithContext(reqCtx)

	// Operación de "subprocess" simulada · tarda 50ms.
	// Mientras "corre", el cliente HTTP se desconecta (reqCancel()).
	// Después, el handler ejecuta el INSERT a la BD usando commitContext().

	// Lanzamos cancelación del request a los 20ms
	go func() {
		time.Sleep(20 * time.Millisecond)
		reqCancel()
	}()

	// "subprocess" externo (no controlado por reqCtx · simula compose up -d)
	time.Sleep(50 * time.Millisecond)

	// Verificar que el request context efectivamente se canceló
	if reqCtx.Err() == nil {
		t.Fatal("setup: request context debería estar cancelado tras 50ms")
	}

	// AQUÍ ESTÁ LA PRUEBA: el INSERT con commitContext() debe funcionar
	// aunque reqCtx esté cancelado.
	_, err = db.ExecContext(commitContext(),
		`INSERT INTO persist_test (id, value) VALUES (?, ?)`,
		"nextcloud", "installed")
	if err != nil {
		t.Fatalf("INSERT con commitContext() falló pese a tener cliente desconectado: %v · "+
			"Esto es el bug Nextcloud · commitContext() NO está desacoplado de r.Context()", err)
	}

	// Verificar que la row se persistió.
	var value string
	err = db.QueryRowContext(commitContext(),
		`SELECT value FROM persist_test WHERE id = ?`, "nextcloud").Scan(&value)
	if err != nil {
		t.Fatalf("SELECT post-INSERT falló: %v", err)
	}
	if value != "installed" {
		t.Errorf("row persistida con valor inesperado: got %q, want 'installed'", value)
	}
}

// TestCommitContext_RequestContextBehavesCorrectly · sanity check:
// verifica que un INSERT con r.Context() SÍ falla cuando el request se cancela.
// Esto demuestra que el bug existe sin commitContext() y que commitContext()
// es la solución correcta.
func TestCommitContext_RequestContextFailsWhenCancelled(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE persist_test (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/test/persist", nil)
	reqCtx, reqCancel := context.WithCancel(req.Context())
	req = req.WithContext(reqCtx)

	// Cancelar inmediatamente
	reqCancel()

	// INSERT con r.Context() ya cancelado · DEBE fallar
	_, err = db.ExecContext(req.Context(),
		`INSERT INTO persist_test (id) VALUES (?)`, "test")
	if err == nil {
		t.Fatal("INSERT con context cancelado debería fallar · si no falla, " +
			"el driver SQLite no respeta cancelación y nuestra suposición sobre " +
			"el bug Nextcloud es incorrecta")
	}

	// El error debe ser de context (cancellation), no otro tipo
	if err != context.Canceled && err.Error() != "context canceled" {
		t.Logf("nota: error de cancelación tiene formato %q · OK si es semánticamente correcto", err)
	}
}
