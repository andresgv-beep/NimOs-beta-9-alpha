// network_schema.go — Schema SQLite del módulo network (Beta 8 v4).
//
// El schema se embebe en el binario con //go:embed para que no haya que
// distribuir archivos externos. El daemon lo aplica al arranque dentro
// de initNetworkSchema(), llamado desde main.go DESPUÉS de
// initNimosCoreSchema() (las tablas nimos_secrets y nimos_breakers son
// dependencia: network_ddns tiene FK a nimos_secrets).
//
// El schema es idempotente (todos los CREATE usan IF NOT EXISTS). Se
// puede aplicar en cada arranque sin efectos secundarios.
//
// Tablas que contiene:
//   · network_ports       — puertos del daemon (triple generation)
//   · network_ddns        — DDNS configs (FK a nimos_secrets)
//   · network_observed    — snapshots históricos del observer
//   · network_operations  — operaciones auditables (triggered_by + request_id)
//   · network_events      — log auditable con dedupe + categorías
//
// Para inspeccionar fuera de Go: ver el archivo .sql.

package main

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed network_schema.sql
var networkSchemaSQL string

// initNetworkSchema aplica el schema del módulo network a la base de
// datos. Es idempotente.
//
// PRECONDICIÓN: nimos_core_schema debe estar ya aplicado, porque
// network_ddns tiene FK a nimos_secrets(id). Si se llama antes, la
// FK fallará en CREATE TABLE.
//
// El parámetro conn permite pasar tanto el `db` global como una conexión
// temporal para tests. En producción se llama con `db`.
//
// Si el schema falla al aplicarse, devuelve error y el daemon NO debe
// continuar (sin tablas network el módulo no funciona).
func initNetworkSchema(conn *sql.DB) error {
	if conn == nil {
		return fmt.Errorf("initNetworkSchema: conn is nil")
	}

	// Verificación defensiva: foreign keys deben estar activadas.
	// Sin esto, la FK network_ddns → nimos_secrets es decorativa.
	var fkEnabled int
	if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		return fmt.Errorf("cannot read foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		return fmt.Errorf("foreign_keys is OFF (%d). Must be ON before applying network schema", fkEnabled)
	}

	// Verificación defensiva: nimos_secrets debe existir (precondición).
	// Mejor mensaje de error que un constraint failure críptico.
	var hasSecrets int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'nimos_secrets'
	`).Scan(&hasSecrets); err != nil {
		return fmt.Errorf("cannot check nimos_secrets existence: %v", err)
	}
	if hasSecrets == 0 {
		return fmt.Errorf("nimos_secrets table missing — call initNimosCoreSchema first")
	}

	if _, err := conn.Exec(networkSchemaSQL); err != nil {
		return fmt.Errorf("cannot apply network schema: %v", err)
	}
	// Migraciones columnares idempotentes (patrón del proyecto: SQLite
	// devuelve "duplicate column" si ya existe — se ignora). Para DBs
	// creadas antes de que la config de exposición tuviera puertos.
	conn.Exec(`ALTER TABLE network_exposure_config ADD COLUMN http_port INTEGER NOT NULL DEFAULT 80`)
	conn.Exec(`ALTER TABLE network_exposure_config ADD COLUMN https_port INTEGER NOT NULL DEFAULT 443`)
	conn.Exec(`ALTER TABLE network_exposure_config ADD COLUMN fw_managed_ports TEXT NOT NULL DEFAULT '[]'`)

	// Migración del CHECK de category en network_events (15/06/2026):
	// se añadió la categoría 'exposure' (la usa el reconciler de exposición),
	// pero las DBs creadas antes tienen el CHECK viejo sin ella → el reconciler
	// fallaba en bucle con "CHECK constraint failed: category IN (...)".
	//
	// SQLite no permite modificar un CHECK con ALTER. Hay que recrear la tabla.
	// Detectamos si el CHECK viejo está presente (buscando 'exposure' en el DDL
	// almacenado) y solo migramos si falta. Idempotente.
	migrateNetworkEventsCategoryCheck(conn)

	return nil
}

// migrateNetworkEventsCategoryCheck recrea network_events con el CHECK de
// category actualizado (incluyendo 'exposure') si la tabla existente tiene el
// CHECK viejo. Idempotente: si ya incluye 'exposure', no hace nada.
func migrateNetworkEventsCategoryCheck(conn *sql.DB) {
	var ddl string
	err := conn.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'network_events'
	`).Scan(&ddl)
	if err != nil {
		return // tabla no existe aún (DB nueva usa el schema correcto) o error
	}
	if strings.Contains(ddl, "'exposure'") {
		return // ya migrada · el CHECK incluye exposure
	}

	// Recrear la tabla con el CHECK nuevo, preservando los datos.
	// SQLite: crear tabla nueva, copiar, borrar vieja, renombrar.
	tx, err := conn.Begin()
	if err != nil {
		logMsg("migrateNetworkEventsCategoryCheck: begin tx: %v", err)
		return
	}
	defer tx.Rollback()

	// El nuevo DDL debe coincidir con el de network_schema.sql (con 'exposure').
	// Construimos la tabla _new con el CHECK actualizado y copiamos columnas.
	stmts := []string{
		`ALTER TABLE network_events RENAME TO network_events_old`,
		`CREATE TABLE network_events (
			id           TEXT    PRIMARY KEY,
			operation_id TEXT,
			timestamp    TEXT    NOT NULL,
			category     TEXT    NOT NULL
				CHECK(category IN ('ddns', 'cert', 'port', 'upnp', 'breaker', 'observer', 'capability', 'exposure')),
			event        TEXT    NOT NULL,
			target_id    TEXT,
			level        TEXT    NOT NULL
				CHECK(level IN ('debug', 'info', 'warn', 'error')),
			message      TEXT    NOT NULL,
			details      TEXT,
			occurrences  INTEGER NOT NULL DEFAULT 1 CHECK(occurrences >= 1),
			last_seen_at TEXT    NOT NULL,
			FOREIGN KEY (operation_id) REFERENCES network_operations(id) ON DELETE CASCADE
		)`,
		`INSERT INTO network_events (id, operation_id, timestamp, category, event, target_id, level, message, details, occurrences, last_seen_at)
			SELECT id, operation_id, timestamp, category, event, target_id, level, message, details, occurrences, last_seen_at
			FROM network_events_old`,
		`DROP TABLE network_events_old`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			logMsg("migrateNetworkEventsCategoryCheck: stmt falló (%v) · se aborta migración", err)
			return // rollback por el defer
		}
	}
	if err := tx.Commit(); err != nil {
		logMsg("migrateNetworkEventsCategoryCheck: commit: %v", err)
		return
	}
	logMsg("migrateNetworkEventsCategoryCheck: network_events migrada · CHECK ahora incluye 'exposure'")
}
