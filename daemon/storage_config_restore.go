// storage_config_restore.go — Auto-restore de config al arrancar (Capa 2).
//
// PROBLEMA (incidente data8, junio 2026):
// El backup de config a pool ya es WAL-safe (storage_config_backup.go), pero no
// había forma de RECUPERARLO solo. Si la BD viva se borra/vacía (muerte de SD,
// reinstalación, corrupción total), NimOS arrancaba con una BD vacía y el usuario
// tenía que restaurar a mano — justo lo que pasó.
//
// SOLUCIÓN:
// Antes de abrir la BD, si la BD viva NO EXISTE o está VACÍA, buscar el backup
// más nuevo y VÁLIDO en los pools montados (`<pool>/system-backup/config/`) y
// restaurarlo. Así, muerte de SD = reinstalas y NimOS se reconstruye solo.
//
// SEGURIDAD (Regla 16, sin destruir): SOLO se restaura sobre una BD ausente o
// vacía — JAMÁS se pisa una BD viva con contenido. Y solo se acepta un backup que
// valida (contiene las tablas críticas). Ante la duda, no se restaura.
//
// ALCANCE v1: cubre el caso "la BD se vació/borró pero los pools están montados"
// (los pools en fstab los monta el sistema antes de arrancar el daemon). La
// recuperación completa tras reinstalar la SD con los pools SIN montar todavía
// requiere montar los pools antes (familia FIX-3) y va en su propio frente.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// restoreCriticalTables son las tablas que un backup válido DEBE contener para
// aceptarse. Evita restaurar un fichero corrupto o de otro formato.
var restoreCriticalTables = []string{"users", "pools", "shares"}

// maybeRestoreConfigOnBoot restaura la BD desde el backup de un pool si la BD
// viva no existe o está vacía. Devuelve true si restauró algo. Pensada para
// llamarse ANTES de openDB().
func maybeRestoreConfigOnBoot() (bool, error) {
	return maybeRestoreConfigOnBootWith(dbPath, nimosPoolsDir)
}

func maybeRestoreConfigOnBootWith(livePath, poolsDir string) (bool, error) {
	// 1. NUNCA pisar una BD viva con contenido.
	if !liveDBIsEmpty(livePath) {
		return false, nil
	}

	// 2. Buscar candidatos en los pools montados y quedarnos con el más nuevo
	//    que VALIDE.
	base := filepath.Base(livePath)
	var best string
	var bestMod time.Time
	for _, cand := range findPoolBackups(poolsDir, base) {
		if !validateBackupDB(cand) {
			continue
		}
		fi, err := os.Stat(cand)
		if err != nil {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = cand, fi.ModTime()
		}
	}
	if best == "" {
		return false, nil
	}

	// 3. Restaurar (copia atómica) y limpiar WAL/SHM viejos del destino.
	if err := copyFileAtomic(best, livePath); err != nil {
		return false, fmt.Errorf("config restore: copia %s → %s: %w", best, livePath, err)
	}
	_ = os.Remove(livePath + "-wal")
	_ = os.Remove(livePath + "-shm")

	logMsg("config restore: BD restaurada desde %s (backup del %s)",
		best, bestMod.Format(time.RFC3339))
	return true, nil
}

// liveDBIsEmpty indica si la BD viva está ausente o vacía (tamaño 0). Solo en
// ese caso es seguro restaurar (no hay datos vivos que pisar).
func liveDBIsEmpty(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return true // no existe → tratar como vacía
	}
	return fi.Size() == 0
}

// findPoolBackups devuelve los ficheros de backup `<pool>/system-backup/config/<base>`
// presentes en los pools montados bajo poolsDir.
func findPoolBackups(poolsDir, base string) []string {
	entries, err := os.ReadDir(poolsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(poolsDir, e.Name(), "system-backup", "config", base)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Size() > 0 {
			out = append(out, cand)
		}
	}
	return out
}

// validateBackupDB abre el backup en solo-lectura y comprueba que contiene las
// tablas críticas. Rechaza ficheros corruptos, vacíos o de otro formato.
func validateBackupDB(path string) bool {
	d, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		return false
	}
	defer d.Close()
	for _, tbl := range restoreCriticalTables {
		var n int
		if err := d.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&n); err != nil || n != 1 {
			return false
		}
	}
	return true
}

// copyFileAtomic copia src a dst de forma atómica (tmp + rename) con permisos
// 0600 (la BD contiene datos sensibles).
func copyFileAtomic(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	tmp := dst + ".restore.tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
