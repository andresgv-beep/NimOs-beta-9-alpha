// nimos_core_schema.go — Schema SQLite del core de NimOS (tablas globales).
//
// El schema se embebe en el binario con //go:embed para que no haya que
// distribuir archivos externos. El daemon lo aplica al arranque dentro
// de initNimosCoreSchema() llamado desde main.go, después de openDB() y
// antes de initStorageSchema().
//
// El schema es idempotente (todos los CREATE usan IF NOT EXISTS), así
// que se puede aplicar en cada arranque sin problemas.
//
// Tablas que contiene (todas globales, no de un módulo concreto):
//   · nimos_secrets        — AES-GCM secrets store
//   · nimos_breakers       — CircuitBreaker state (persistencia mínima)
//   · nimos_capabilities   — SystemCapabilities cache
//
// Para inspeccionar el schema fuera de Go, ver el archivo .sql.

package main

import (
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed nimos_core_schema.sql
var nimosCoreSchemaSQL string

// initNimosCoreSchema aplica el schema de tablas globales (nimos_*) a
// la base de datos. Es idempotente: se puede llamar en cada arranque
// sin efectos secundarios.
//
// El parámetro conn permite pasar tanto el `db` global como una conexión
// temporal para tests. En producción se llama con `db`.
//
// Si el schema falla al aplicarse, devuelve error y el daemon NO debe
// continuar (sin tablas globales el resto de módulos no funciona).
func initNimosCoreSchema(conn *sql.DB) error {
	if conn == nil {
		return fmt.Errorf("initNimosCoreSchema: conn is nil")
	}

	// Verificación defensiva: foreign keys deben estar activadas.
	var fkEnabled int
	if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		return fmt.Errorf("cannot read foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		return fmt.Errorf("foreign_keys is OFF (%d). Must be ON", fkEnabled)
	}

	if _, err := conn.Exec(nimosCoreSchemaSQL); err != nil {
		return fmt.Errorf("cannot apply nimos core schema: %v", err)
	}
	return nil
}
