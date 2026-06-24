// network_events_migration_test.go — Test de la migración del CHECK de category
// (15/06/2026 · añadir 'exposure' a BDs existentes).

package main

import (
	"os"
	"testing"
)

// TestMigrateNetworkEventsCategoryCheck simula una DB con el CHECK VIEJO (sin
// 'exposure'), corre la migración, y verifica que: (1) ahora acepta 'exposure',
// (2) preserva los datos existentes, (3) es idempotente.
func TestMigrateNetworkEventsCategoryCheck(t *testing.T) {
	tmpDB := "/tmp/nimos_evt_migration_test.db"
	_ = os.Remove(tmpDB)
	defer os.Remove(tmpDB)

	c, err := openTestSQLite(tmpDB)
	if err != nil {
		t.Fatalf("openTestSQLite: %v", err)
	}
	defer c.db.Close()

	if err := initNimosCoreSchema(c.db); err != nil {
		t.Fatalf("initNimosCoreSchema: %v", err)
	}

	// Crear network_operations (FK target) y la tabla network_events con el
	// CHECK VIEJO (sin 'exposure'), simulando una DB pre-migración.
	c.db.Exec(`CREATE TABLE network_operations (id TEXT PRIMARY KEY)`)
	_, err = c.db.Exec(`CREATE TABLE network_events (
		id           TEXT    PRIMARY KEY,
		operation_id TEXT,
		timestamp    TEXT    NOT NULL,
		category     TEXT    NOT NULL
			CHECK(category IN ('ddns', 'cert', 'port', 'upnp', 'breaker', 'observer', 'capability')),
		event        TEXT    NOT NULL,
		target_id    TEXT,
		level        TEXT    NOT NULL CHECK(level IN ('debug', 'info', 'warn', 'error')),
		message      TEXT    NOT NULL,
		details      TEXT,
		occurrences  INTEGER NOT NULL DEFAULT 1 CHECK(occurrences >= 1),
		last_seen_at TEXT    NOT NULL,
		FOREIGN KEY (operation_id) REFERENCES network_operations(id) ON DELETE CASCADE
	)`)
	if err != nil {
		t.Fatalf("crear tabla vieja: %v", err)
	}

	// Insertar un evento existente (categoría válida en el CHECK viejo)
	_, err = c.db.Exec(`INSERT INTO network_events
		(id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-1', '2026-06-15T12:00:00Z', 'ddns', 'test', 'info', 'msg', '2026-06-15T12:00:00Z')`)
	if err != nil {
		t.Fatalf("insert evento viejo: %v", err)
	}

	// Confirmar que ANTES de migrar, 'exposure' es rechazado
	_, err = c.db.Exec(`INSERT INTO network_events
		(id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-exp-pre', '2026-06-15T12:00:00Z', 'exposure', 'test', 'info', 'msg', '2026-06-15T12:00:00Z')`)
	if err == nil {
		t.Fatal("antes de migrar, 'exposure' debería ser RECHAZADO por el CHECK viejo")
	}

	// Correr la migración
	migrateNetworkEventsCategoryCheck(c.db)

	// DESPUÉS de migrar: 'exposure' debe aceptarse
	_, err = c.db.Exec(`INSERT INTO network_events
		(id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-exp', '2026-06-15T12:00:00Z', 'exposure', 'test', 'info', 'msg', '2026-06-15T12:00:00Z')`)
	if err != nil {
		t.Errorf("tras migrar, 'exposure' debería aceptarse, pero falló: %v", err)
	}

	// El dato viejo debe seguir ahí (migración preserva datos)
	var count int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id='evt-1'`).Scan(&count)
	if count != 1 {
		t.Errorf("el evento viejo 'evt-1' debería preservarse tras la migración, count=%d", count)
	}

	// Idempotencia: correr de nuevo no debe romper nada
	migrateNetworkEventsCategoryCheck(c.db)
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id='evt-1'`).Scan(&count)
	if count != 1 {
		t.Errorf("tras segunda migración (idempotente), 'evt-1' debería seguir, count=%d", count)
	}
}
