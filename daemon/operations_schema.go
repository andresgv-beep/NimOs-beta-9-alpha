// operations_schema.go — Schema SQLite del módulo de operations async (Beta 8.1.x).
//
// APP-012 · infraestructura para operaciones asíncronas (install, pull,
// snapshot...) que tardan más que un request HTTP razonable. El handler
// devuelve un operationId; el cliente hace polling al endpoint /api/operations/{id}.
//
// El schema se embebe en el binario con //go:embed para que no haya que
// distribuir archivos externos. El daemon lo aplica al arranque dentro de
// initOperationsSchema() llamado desde openDB().
//
// Idempotente: todos los CREATE usan IF NOT EXISTS. Safe en cada arranque.
//
// Diseñado para HORIZONTALIDAD: cualquier módulo puede crear operations
// (type es un string libre). El módulo es responsable de definir su propio
// payload de result_json y de leer/serializar consistentemente.

package main

import (
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed operations_schema.sql
var operationsSchemaSQL string

// initOperationsSchema aplica el schema de nimos_operations a la base de datos.
// Es idempotente: se puede llamar en cada arranque sin efectos secundarios.
//
// Si el schema falla al aplicarse, devuelve error y el daemon NO debe
// arrancar (es un estado inconsistente).
func initOperationsSchema(db *sql.DB) error {
	if _, err := db.Exec(operationsSchemaSQL); err != nil {
		return fmt.Errorf("apply operations schema: %w", err)
	}
	return nil
}
