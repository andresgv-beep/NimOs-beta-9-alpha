// storage_config_backup.go — Backup de config WAL-safe.
//
// PROBLEMA (incidente data8, junio 2026):
// El backup de config a pool copiaba `nimos.db` con `os.ReadFile`. Pero la BD usa
// `journal_mode(WAL)`: las escrituras recientes viven en `nimos.db-wal` hasta el
// checkpoint. Copiar solo el `.db` capturaba un estado VIEJO, sin esos cambios.
// Resultado probable del incidente: las shares creadas estaban en el WAL, el
// backup las perdió, y una restauración posterior trajo la tabla vacía.
//
// SOLUCIÓN:
// `VACUUM INTO` produce un snapshot CONSISTENTE de un solo fichero que incluye
// todo el contenido (también lo que está en el WAL), de forma transaccional y
// segura sobre una BD viva. Es el método recomendado por SQLite para backup en
// caliente. Escribimos a un temporal (VACUUM INTO exige que el destino NO exista)
// y renombramos atómicamente sobre el destino final.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// backupDBConsistent escribe un snapshot consistente de la BD de NimOS en
// dstPath, usando VACUUM INTO sobre la conexión global. Incluye lo que esté en
// el WAL — a diferencia de copiar el .db a pelo.
func backupDBConsistent(dstPath string) error {
	return backupDBConsistentFrom(db, dstPath)
}

// backupDBConsistentFrom es la variante con *sql.DB explícito (para tests).
func backupDBConsistentFrom(src *sql.DB, dstPath string) error {
	if src == nil {
		return fmt.Errorf("backupDBConsistent: db nil")
	}

	// VACUUM INTO falla si el destino ya existe → escribimos a un temporal y
	// renombramos. Limpiamos cualquier temporal residual de un intento previo.
	tmp := dstPath + ".tmp"
	_ = os.Remove(tmp)

	// El path es interno y controlado; escapamos comillas simples por seguridad
	// (VACUUM INTO toma un literal de cadena, no admite bind del nombre).
	escaped := strings.ReplaceAll(tmp, "'", "''")
	if _, err := src.Exec("VACUUM INTO '" + escaped + "'"); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("VACUUM INTO: %w", err)
	}

	// La BD puede contener datos sensibles (sesiones, security) → 0600.
	if err := os.Chmod(tmp, 0600); err != nil {
		// no es fatal; seguimos con el rename
		logMsg("backupDBConsistent: chmod %s: %v", tmp, err)
	}

	// Rename atómico (tmp y dst están en el mismo directorio → misma fs).
	if err := os.Rename(tmp, dstPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename snapshot %s → %s: %w", tmp, dstPath, err)
	}

	return nil
}
