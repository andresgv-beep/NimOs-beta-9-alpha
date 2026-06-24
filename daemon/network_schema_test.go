// network_schema_test.go — Tests del schema network (Beta 8 v4).
//
// Verificamos:
//   · El schema aplica sin error y es idempotente.
//   · Las 6 tablas y sus índices existen.
//   · CHECKs rechazan valores inválidos a nivel DB (no solo en Go).
//   · UNIQUE constraints funcionan.
//   · FK CASCADE funciona (network_ddns ← nimos_secrets).
//   · FK ON DELETE SET NULL funciona (network_operations.parent_operation).
//   · La precondición "core schema first" se detecta y rechaza.
//
// Los CHECKs a nivel DB son críticos porque son la última línea de
// defensa contra bugs en Go que querrían insertar valores corruptos.

package main

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// setupNetworkDB monta DB con core + network schema aplicados. Es la
// base para los tests de network_* (repo, reconciler, etc.) — no solo
// del schema.
func setupNetworkDB(t *testing.T) (*sqlConn, func()) {
	t.Helper()
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_network_test_" + safeName + ".db"
	_ = os.Remove(tmpDB)

	c, err := openTestSQLite(tmpDB)
	if err != nil {
		t.Fatalf("openTestSQLite: %v", err)
	}
	if err := initNimosCoreSchema(c.db); err != nil {
		c.db.Close()
		_ = os.Remove(tmpDB)
		t.Fatalf("initNimosCoreSchema: %v", err)
	}
	if err := initNetworkSchema(c.db); err != nil {
		c.db.Close()
		_ = os.Remove(tmpDB)
		t.Fatalf("initNetworkSchema: %v", err)
	}
	cleanup := func() {
		c.db.Close()
		_ = os.Remove(tmpDB)
	}
	return c, cleanup
}

// insertSecret crea un secret mínimo para usarlo como FK en network_ddns.
// Usa los bajos niveles directamente (sin pasar por SecretsStore) porque
// estos tests verifican el schema, no la API de secrets.
func insertSecret(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO nimos_secrets (id, category, label, ciphertext, nonce, key_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, "ddns_token", "test-"+id, []byte("ct"), []byte("nn"), 1, "2026-05-21T12:00:00Z")
	if err != nil {
		t.Fatalf("insertSecret: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Schema sanity
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_IsIdempotent(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// Re-aplicar el schema un par de veces.
	if err := initNetworkSchema(c.db); err != nil {
		t.Errorf("second initNetworkSchema: %v", err)
	}
	if err := initNetworkSchema(c.db); err != nil {
		t.Errorf("third initNetworkSchema: %v", err)
	}
}

func TestNetworkSchema_AllTablesExist(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	wantTables := []string{
		"network_ports",
		"network_ddns",
		"network_observed",
		"network_operations",
		"network_events",
		"network_exposed_apps",
		"network_exposure_config",
	}
	for _, table := range wantTables {
		var name string
		err := c.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestNetworkSchema_IndexesExist(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// Solo los críticos para queries del reconciler/observer.
	wantIndexes := []string{
		"idx_network_ddns_provider",
		"idx_network_observed_at",
		"idx_network_observed_health",
		"idx_network_ops_status",
		"idx_network_events_category",
		"idx_network_events_dedupe",
	}
	for _, idx := range wantIndexes {
		var name string
		err := c.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
			idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %s missing: %v", idx, err)
		}
	}
}

func TestNetworkSchema_RequiresCoreSchemaFirst(t *testing.T) {
	// DB nueva SIN aplicar nimos_core_schema. initNetworkSchema debe fallar.
	safeName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_network_no_core_" + safeName + ".db"
	_ = os.Remove(tmpDB)
	defer os.Remove(tmpDB)

	c, err := openTestSQLite(tmpDB)
	if err != nil {
		t.Fatalf("openTestSQLite: %v", err)
	}
	defer c.db.Close()

	err = initNetworkSchema(c.db)
	if err == nil {
		t.Fatal("initNetworkSchema succeeded without core schema, should fail")
	}
	if !strings.Contains(err.Error(), "nimos_secrets") {
		t.Errorf("error doesn't mention nimos_secrets: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// network_ports CHECKs
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_PortsAcceptsValid(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_ports (id, port, bind_address, updated_at)
		VALUES ('http', 8080, '0.0.0.0', '2026-05-21T12:00:00Z')
	`)
	if err != nil {
		t.Errorf("valid insert failed: %v", err)
	}
}

func TestNetworkSchema_PortsRejectsInvalidID(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_ports (id, port, bind_address, updated_at)
		VALUES ('ftp', 21, '0.0.0.0', '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK on id should reject 'ftp' (not in 'http','https')")
	}
}

func TestNetworkSchema_PortsRejectsOutOfRangePort(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	cases := []int{0, -1, 65536, 99999}
	for _, port := range cases {
		_, err := c.db.Exec(`
			INSERT INTO network_ports (id, port, bind_address, updated_at)
			VALUES ('http', ?, '0.0.0.0', '2026-05-21T12:00:00Z')
		`, port)
		if err == nil {
			t.Errorf("CHECK should reject port=%d", port)
		}
		// Limpiar para el siguiente intento
		_, _ = c.db.Exec(`DELETE FROM network_ports WHERE id = 'http'`)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// network_ddns CHECKs + FK
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_DdnsAcceptsValid(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	insertSecret(t, c.db, "sec-1")

	_, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-1', 'duckdns', 'nimosbarraca.duckdns.org', 'sec-1')
	`)
	if err != nil {
		t.Errorf("valid insert failed: %v", err)
	}
}

func TestNetworkSchema_DdnsRejectsUnknownProvider(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	insertSecret(t, c.db, "sec-1")

	_, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-1', 'godaddy', 'example.com', 'sec-1')
	`)
	if err == nil {
		t.Error("CHECK should reject unknown provider 'godaddy'")
	}
}

func TestNetworkSchema_DdnsRejectsMissingSecretFK(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	// NO insertamos secret.

	_, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-1', 'duckdns', 'example.com', 'does-not-exist')
	`)
	if err == nil {
		t.Error("FK should reject token_secret_id that does not exist in nimos_secrets")
	}
}

func TestNetworkSchema_DdnsCascadeOnSecretDelete(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	insertSecret(t, c.db, "sec-1")

	if _, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-1', 'duckdns', 'example.com', 'sec-1')
	`); err != nil {
		t.Fatalf("setup ddns insert: %v", err)
	}

	// Borrar el secret debe disparar CASCADE en network_ddns.
	if _, err := c.db.Exec(`DELETE FROM nimos_secrets WHERE id = 'sec-1'`); err != nil {
		t.Fatalf("delete secret: %v", err)
	}

	var count int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_ddns WHERE id = 'ddns-1'`).Scan(&count)
	if count != 0 {
		t.Errorf("CASCADE didn't fire: ddns row still exists after secret delete")
	}
}

func TestNetworkSchema_DdnsUniqueDomain(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	insertSecret(t, c.db, "sec-1")
	insertSecret(t, c.db, "sec-2")

	_, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-1', 'duckdns', 'shared.duckdns.org', 'sec-1')
	`)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err = c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id)
		VALUES ('ddns-2', 'noip', 'shared.duckdns.org', 'sec-2')
	`)
	if err == nil {
		t.Error("UNIQUE(domain) should reject second entry for same domain")
	}
}

func TestNetworkSchema_DdnsUpdateIntervalMinimum(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	insertSecret(t, c.db, "sec-1")

	_, err := c.db.Exec(`
		INSERT INTO network_ddns (id, provider, domain, token_secret_id, update_interval)
		VALUES ('ddns-1', 'duckdns', 'x.duckdns.org', 'sec-1', 30)
	`)
	if err == nil {
		t.Error("CHECK should reject update_interval < 60s (would hammer provider)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// network_observed CHECKs
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_ObservedAcceptsAllValidTypes(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	for i, snapshotType := range []string{"periodic", "event", "boot", "manual"} {
		_, err := c.db.Exec(`
			INSERT INTO network_observed (
				id, generation, snapshot_at, snapshot_type,
				snapshot_data, overall_health
			) VALUES (?, ?, ?, ?, '{}', 'healthy')
		`, "obs-"+snapshotType, i+1, "2026-05-21T12:00:00Z", snapshotType)
		if err != nil {
			t.Errorf("type %q rejected: %v", snapshotType, err)
		}
	}
}

func TestNetworkSchema_ObservedRejectsBadType(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_observed (
			id, generation, snapshot_at, snapshot_type,
			snapshot_data, overall_health
		) VALUES ('o1', 1, '2026-05-21T12:00:00Z', 'random', '{}', 'healthy')
	`)
	if err == nil {
		t.Error("CHECK should reject snapshot_type='random'")
	}
}

func TestNetworkSchema_ObservedRejectsBadHealth(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_observed (
			id, generation, snapshot_at, snapshot_type,
			snapshot_data, overall_health
		) VALUES ('o1', 1, '2026-05-21T12:00:00Z', 'periodic', '{}', 'totally-broken')
	`)
	if err == nil {
		t.Error("CHECK should reject overall_health='totally-broken'")
	}
}

func TestNetworkSchema_ObservedGenerationMustBePositive(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_observed (
			id, generation, snapshot_at, snapshot_type,
			snapshot_data, overall_health
		) VALUES ('o1', 0, '2026-05-21T12:00:00Z', 'periodic', '{}', 'healthy')
	`)
	if err == nil {
		t.Error("CHECK should reject generation=0 (must be > 0)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// network_operations CHECKs + FK SET NULL
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_OperationsTriggeredByPatterns(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	validCases := []string{
		"user:admin",
		"user:andres",
		"reconciler:ddns_updater",
		"reconciler:cert_renewer",
		"system:boot",
		"system:scheduler",
	}
	for _, tb := range validCases {
		_, err := c.db.Exec(`
			INSERT INTO network_operations (id, type, status, triggered_by, started_at)
			VALUES (?, 'ddns_update', 'pending', ?, '2026-05-21T12:00:00Z')
		`, "op-"+tb, tb)
		if err != nil {
			t.Errorf("valid triggered_by %q rejected: %v", tb, err)
		}
	}
}

func TestNetworkSchema_OperationsRejectsAdHocTriggeredBy(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	invalidCases := []string{
		"random",                  // sin prefijo
		"cli:something",            // categoría no permitida
		"webhook:incoming",         // categoría no permitida
		"reconciler",               // sin nombre
		"system:custom",            // system: solo 'boot' o 'scheduler'
		"",                         // vacío
	}
	for _, tb := range invalidCases {
		_, err := c.db.Exec(`
			INSERT INTO network_operations (id, type, status, triggered_by, started_at)
			VALUES (?, 'x', 'pending', ?, '2026-05-21T12:00:00Z')
		`, "op-bad", tb)
		if err == nil {
			t.Errorf("CHECK should reject triggered_by=%q", tb)
		}
	}
}

func TestNetworkSchema_OperationsParentSetNullOnDelete(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// Padre + hijo
	if _, err := c.db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at)
		VALUES ('parent', 'batch', 'completed', 'user:admin', '2026-05-21T12:00:00Z')
	`); err != nil {
		t.Fatalf("parent: %v", err)
	}
	if _, err := c.db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at, parent_operation)
		VALUES ('child', 'cert_issue', 'completed', 'reconciler:cert_renewer', '2026-05-21T12:01:00Z', 'parent')
	`); err != nil {
		t.Fatalf("child: %v", err)
	}

	// Borrar el padre debe dejar al hijo con parent_operation = NULL
	// (NO borrar el hijo — auditoría histórica sobrevive).
	if _, err := c.db.Exec(`DELETE FROM network_operations WHERE id = 'parent'`); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	var (
		exists int
		parent sql.NullString
	)
	if err := c.db.QueryRow(`
		SELECT COUNT(*) FROM network_operations WHERE id = 'child'
	`).Scan(&exists); err != nil {
		t.Fatalf("check child: %v", err)
	}
	if exists != 1 {
		t.Error("ON DELETE SET NULL deleted the child (wrong)")
	}
	c.db.QueryRow(`SELECT parent_operation FROM network_operations WHERE id = 'child'`).Scan(&parent)
	if parent.Valid {
		t.Errorf("child parent_operation = %v, want NULL", parent.String)
	}
}

func TestNetworkSchema_OperationsStatusCheck(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at)
		VALUES ('op-1', 'x', 'whatever', 'user:admin', '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK should reject status='whatever'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// network_events CHECKs + FK CASCADE
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_EventsAcceptsAllCategories(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	for _, cat := range []string{"ddns", "cert", "port", "upnp", "breaker", "observer", "capability", "exposure"} {
		_, err := c.db.Exec(`
			INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at)
			VALUES (?, '2026-05-21T12:00:00Z', ?, 'test', 'info', 'msg', '2026-05-21T12:00:00Z')
		`, "evt-"+cat, cat)
		if err != nil {
			t.Errorf("category %q rejected: %v", cat, err)
		}
	}
}

func TestNetworkSchema_EventsRejectsBadCategory(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-1', '2026-05-21T12:00:00Z', 'random_cat', 'x', 'info', 'm', '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK should reject category='random_cat'")
	}
}

func TestNetworkSchema_EventsAcceptsAllLevels(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	for _, level := range []string{"debug", "info", "warn", "error"} {
		_, err := c.db.Exec(`
			INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at)
			VALUES (?, '2026-05-21T12:00:00Z', 'ddns', 'x', ?, 'm', '2026-05-21T12:00:00Z')
		`, "evt-"+level, level)
		if err != nil {
			t.Errorf("level %q rejected: %v", level, err)
		}
	}
}

func TestNetworkSchema_EventsRejectsBadLevel(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-1', '2026-05-21T12:00:00Z', 'ddns', 'x', 'trace', 'm', '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK should reject level='trace' (only debug/info/warn/error)")
	}
}

func TestNetworkSchema_EventsOccurrencesMustBePositive(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	_, err := c.db.Exec(`
		INSERT INTO network_events (id, timestamp, category, event, level, message, occurrences, last_seen_at)
		VALUES ('evt-1', '2026-05-21T12:00:00Z', 'ddns', 'x', 'info', 'm', 0, '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK should reject occurrences=0 (must be >= 1)")
	}
}

func TestNetworkSchema_EventsCascadeFromOperation(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// Operation + 3 eventos linkados
	_, err := c.db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at)
		VALUES ('op-1', 'ddns_update', 'completed', 'reconciler:ddns_updater', '2026-05-21T12:00:00Z')
	`)
	if err != nil {
		t.Fatalf("operation: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err := c.db.Exec(`
			INSERT INTO network_events (id, operation_id, timestamp, category, event, level, message, last_seen_at)
			VALUES (?, 'op-1', '2026-05-21T12:00:00Z', 'ddns', 'step', 'info', 'm', '2026-05-21T12:00:00Z')
		`, "evt-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}

	// Borrar la op debe disparar CASCADE en events.
	_, err = c.db.Exec(`DELETE FROM network_operations WHERE id = 'op-1'`)
	if err != nil {
		t.Fatalf("delete op: %v", err)
	}

	var count int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE operation_id = 'op-1'`).Scan(&count)
	if count != 0 {
		t.Errorf("CASCADE didn't fire: %d events still link to op-1", count)
	}
}

func TestNetworkSchema_EventsAllowNullOperation(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// Observer detecta cambio sin operación asociada → operation_id=NULL.
	_, err := c.db.Exec(`
		INSERT INTO network_events (id, operation_id, timestamp, category, event, level, message, last_seen_at)
		VALUES ('evt-1', NULL, '2026-05-21T12:00:00Z', 'observer', 'public_ip_changed', 'info', 'IP cambió', '2026-05-21T12:00:00Z')
	`)
	if err != nil {
		t.Errorf("event with NULL operation_id rejected: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Triple generation invariants
// ─────────────────────────────────────────────────────────────────────────────

func TestNetworkSchema_GenerationsCannotBeNegative(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()

	// network_ports: desired_generation = -1
	_, err := c.db.Exec(`
		INSERT INTO network_ports (id, port, desired_generation, updated_at)
		VALUES ('http', 8080, -1, '2026-05-21T12:00:00Z')
	`)
	if err == nil {
		t.Error("CHECK should reject negative desired_generation on network_ports")
	}
}
