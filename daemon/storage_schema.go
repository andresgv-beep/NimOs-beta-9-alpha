// storage_schema.go — Schema SQLite del módulo de storage (Beta 8).
//
// El schema se embebe en el binario con //go:embed para que no haya que
// distribuir archivos externos. El daemon lo aplica al arranque dentro de
// initStorageSchema() llamado desde openDB().
//
// El schema es idempotente (todos los CREATE usan IF NOT EXISTS), así que
// se puede aplicar en cada arranque sin problemas.
//
// Para inspeccionar el schema fuera de Go, ver docs/schema.sql.
// Para validar el schema, ver scripts/validate_schema.py.

package main

import (
	_ "embed"
	"fmt"
)

//go:embed storage_schema.sql
var storageSchemaSQL string

// initStorageSchema aplica el schema del módulo storage a la base de datos.
// Es idempotente: se puede llamar en cada arranque sin efectos secundarios.
//
// Si el schema falla al aplicarse, devuelve error y el daemon NO debe
// continuar (sin schema no puede operar).
//
// see docs/storage_invariants.md#5
func initStorageSchema() error {
	if db == nil {
		return fmt.Errorf("initStorageSchema: db is nil, call openDB first")
	}

	// Verificación defensiva: foreign keys deben estar activadas.
	// El schema usa CASCADE y RESTRICT que sin esto son decorativos.
	var fkEnabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		return fmt.Errorf("cannot read foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		return fmt.Errorf("foreign_keys is OFF (%d). Must be ON. See storage_invariants.md#5.1", fkEnabled)
	}

	// Aplicar el script completo del schema.
	if _, err := db.Exec(storageSchemaSQL); err != nil {
		return fmt.Errorf("cannot apply storage schema: %v", err)
	}

	return nil
}
