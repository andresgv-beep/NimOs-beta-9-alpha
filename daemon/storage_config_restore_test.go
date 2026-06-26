// storage_config_restore_test.go — Auto-restore de config al arrancar.
//
// Verifica la regla de oro: restaurar SOLO sobre BD ausente/vacía, nunca pisar
// una BD viva; elegir el backup más nuevo y válido; rechazar backups corruptos.

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeBackupDB crea un fichero SQLite en path. Si valid, con las tablas críticas
// y una fila-sentinela en shares; si no, con un esquema que no valida.
func makeBackupDB(t *testing.T, path, sentinel string, valid bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if valid {
		d.Exec("CREATE TABLE users (username TEXT)")
		d.Exec("CREATE TABLE pools (name TEXT)")
		d.Exec("CREATE TABLE shares (name TEXT)")
		if _, err := d.Exec("INSERT INTO shares (name) VALUES (?)", sentinel); err != nil {
			t.Fatal(err)
		}
	} else {
		d.Exec("CREATE TABLE only_this (x TEXT)") // no contiene users/pools/shares
	}
}

// readSentinel abre la BD restaurada y devuelve la fila de shares.
func readSentinel(t *testing.T, path string) string {
	t.Helper()
	d, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var s string
	if err := d.QueryRow("SELECT name FROM shares LIMIT 1").Scan(&s); err != nil {
		t.Fatalf("leyendo sentinela: %v", err)
	}
	return s
}

func poolBackupPath(poolsDir, pool, base string) string {
	return filepath.Join(poolsDir, pool, "system-backup", "config", base)
}

// Caso central: BD viva ausente + dos backups válidos → restaura el MÁS NUEVO.
func TestRestore_RestoresNewestValidBackup(t *testing.T) {
	dir := t.TempDir()
	poolsDir := filepath.Join(dir, "pools")
	live := filepath.Join(dir, "live", "nimos.db") // no existe aún

	makeBackupDB(t, poolBackupPath(poolsDir, "data8", "nimos.db"), "NUEVO", true)
	makeBackupDB(t, poolBackupPath(poolsDir, "data9", "nimos.db"), "VIEJO", true)
	// Hacer "data9" más viejo que "data8".
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(poolBackupPath(poolsDir, "data9", "nimos.db"), old, old)

	restored, err := maybeRestoreConfigOnBootWith(live, poolsDir)
	if err != nil {
		t.Fatalf("restore err: %v", err)
	}
	if !restored {
		t.Fatal("debía restaurar (BD viva ausente + backup válido)")
	}
	if got := readSentinel(t, live); got != "NUEVO" {
		t.Errorf("restauró el backup equivocado: got %q, want NUEVO", got)
	}
}

// NUNCA pisar una BD viva con contenido.
func TestRestore_SkipsWhenLiveNonEmpty(t *testing.T) {
	dir := t.TempDir()
	poolsDir := filepath.Join(dir, "pools")
	live := filepath.Join(dir, "live", "nimos.db")

	os.MkdirAll(filepath.Dir(live), 0755)
	os.WriteFile(live, []byte("BD VIVA CON DATOS"), 0600)
	makeBackupDB(t, poolBackupPath(poolsDir, "data8", "nimos.db"), "BACKUP", true)

	restored, err := maybeRestoreConfigOnBootWith(live, poolsDir)
	if err != nil {
		t.Fatalf("restore err: %v", err)
	}
	if restored {
		t.Error("NO debía restaurar sobre una BD viva con contenido")
	}
	data, _ := os.ReadFile(live)
	if string(data) != "BD VIVA CON DATOS" {
		t.Error("pisó una BD viva — eso jamás debe pasar")
	}
}

// Sin backups → no restaura, sin error.
func TestRestore_NoBackups(t *testing.T) {
	dir := t.TempDir()
	restored, err := maybeRestoreConfigOnBootWith(
		filepath.Join(dir, "live", "nimos.db"), filepath.Join(dir, "pools"))
	if err != nil {
		t.Fatalf("restore err: %v", err)
	}
	if restored {
		t.Error("no había backups; no debía restaurar")
	}
}

// Un backup inválido (sin tablas críticas) se rechaza.
func TestRestore_SkipsInvalidBackup(t *testing.T) {
	dir := t.TempDir()
	poolsDir := filepath.Join(dir, "pools")
	live := filepath.Join(dir, "live", "nimos.db")

	makeBackupDB(t, poolBackupPath(poolsDir, "data8", "nimos.db"), "", false) // inválido

	restored, err := maybeRestoreConfigOnBootWith(live, poolsDir)
	if err != nil {
		t.Fatalf("restore err: %v", err)
	}
	if restored {
		t.Error("un backup sin tablas críticas no debe restaurarse")
	}
}

// Entre un válido y uno inválido, restaura el válido aunque el inválido sea más nuevo.
func TestRestore_PrefersValidOverNewerInvalid(t *testing.T) {
	dir := t.TempDir()
	poolsDir := filepath.Join(dir, "pools")
	live := filepath.Join(dir, "live", "nimos.db")

	makeBackupDB(t, poolBackupPath(poolsDir, "data8", "nimos.db"), "BUENO", true)
	makeBackupDB(t, poolBackupPath(poolsDir, "data9", "nimos.db"), "", false)
	// El inválido, más nuevo.
	future := time.Now().Add(2 * time.Hour)
	os.Chtimes(poolBackupPath(poolsDir, "data9", "nimos.db"), future, future)

	restored, err := maybeRestoreConfigOnBootWith(live, poolsDir)
	if err != nil {
		t.Fatalf("restore err: %v", err)
	}
	if !restored || readSentinel(t, live) != "BUENO" {
		t.Error("debía restaurar el válido aunque el inválido fuera más nuevo")
	}
}

func TestLiveDBIsEmpty(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.db")
	if !liveDBIsEmpty(missing) {
		t.Error("un fichero ausente debe contar como vacío")
	}
	empty := filepath.Join(dir, "empty.db")
	os.WriteFile(empty, nil, 0600)
	if !liveDBIsEmpty(empty) {
		t.Error("un fichero de tamaño 0 debe contar como vacío")
	}
	full := filepath.Join(dir, "full.db")
	os.WriteFile(full, []byte("x"), 0600)
	if liveDBIsEmpty(full) {
		t.Error("un fichero con contenido NO está vacío")
	}
}
