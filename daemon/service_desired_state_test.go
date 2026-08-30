package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateToV5PersistsManualDockerStop(t *testing.T) {
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "service-intent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Exec(`
		CREATE TABLE app_registry (id TEXT PRIMARY KEY, managed_by TEXT NOT NULL);
		CREATE TABLE service_instances (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO app_registry (id, managed_by) VALUES ('containers', 'docker');
		INSERT INTO service_instances (id, app_id, updated_at) VALUES ('containers@test5', 'containers', 'now');
	`); err != nil {
		t.Fatal(err)
	}

	previous := db
	db = conn
	defer func() { db = previous }()

	if err := migrateToV5(); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV5(); err != nil {
		t.Fatalf("segunda migracion debe ser no-op: %v", err)
	}
	if !dockerAutoStartAllowed() {
		t.Fatal("el estado por defecto debe permitir el auto-arranque")
	}
	if err := dbServiceSetDesiredStatus("containers@test5", "stopped"); err != nil {
		t.Fatal(err)
	}
	if dockerAutoStartAllowed() {
		t.Fatal("una parada manual debe bloquear el auto-arranque de Docker")
	}
	if err := dbServiceSetDesiredStatus("containers@test5", "running"); err != nil {
		t.Fatal(err)
	}
	if !dockerAutoStartAllowed() {
		t.Fatal("un arranque manual debe volver a permitir el auto-arranque")
	}
}
