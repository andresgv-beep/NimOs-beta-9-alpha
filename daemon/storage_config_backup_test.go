// storage_config_backup_test.go — El backup WAL-safe captura las escrituras
// recientes que viven en el WAL. Reproduce el bug que vació las shares: copiar
// el .db a pelo las habría perdido; VACUUM INTO no.

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Abre una BD en modo WAL (como producción), inserta filas que quedan en el WAL
// (no checkpointed) y verifica que el snapshot las contiene.
func TestBackupDBConsistent_CapturesWALWrites(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	dsn := srcPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

	src, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer src.Close()

	if _, err := src.Exec("CREATE TABLE shares (name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Escrituras recientes → viven en el WAL.
	for _, n := range []string{"data11", "multimedia", "musica"} {
		if _, err := src.Exec("INSERT INTO shares (name) VALUES (?)", n); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	snap := filepath.Join(dir, "snapshot.db")
	if err := backupDBConsistentFrom(src, snap); err != nil {
		t.Fatalf("backupDBConsistentFrom: %v", err)
	}

	// El snapshot debe contener las 3 shares. Copiar el .db a pelo sin el WAL
	// las habría perdido — exactamente el bug del incidente.
	dst, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer dst.Close()

	var count int
	if err := dst.QueryRow("SELECT COUNT(*) FROM shares").Scan(&count); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if count != 3 {
		t.Errorf("snapshot tiene %d shares; quería 3 (el WAL-safe debe capturar las escrituras recientes)", count)
	}
}

// Un temporal residual de un intento previo no debe romper el backup, y el
// destino final debe quedar escrito.
func TestBackupDBConsistent_HandlesStaleTmp(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")

	src, err := sql.Open("sqlite", srcPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer src.Close()
	if _, err := src.Exec("CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	snap := filepath.Join(dir, "snap.db")
	// Temporal residual de un intento anterior.
	if err := os.WriteFile(snap+".tmp", []byte("garbage"), 0600); err != nil {
		t.Fatalf("write stale tmp: %v", err)
	}

	if err := backupDBConsistentFrom(src, snap); err != nil {
		t.Fatalf("debería limpiar el .tmp residual y funcionar; err=%v", err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Errorf("el snapshot final no existe: %v", err)
	}
	// El permiso debe ser 0600 (datos sensibles).
	if fi, err := os.Stat(snap); err == nil && fi.Mode().Perm() != 0600 {
		t.Errorf("permiso del snapshot = %v; quería 0600", fi.Mode().Perm())
	}
}

// Con db nil debe devolver error, no panic.
func TestBackupDBConsistent_NilDB(t *testing.T) {
	if err := backupDBConsistentFrom(nil, filepath.Join(t.TempDir(), "x.db")); err == nil {
		t.Error("db nil debe devolver error")
	}
}
