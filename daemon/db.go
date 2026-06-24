package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ═══════════════════════════════════
// Database
// ═══════════════════════════════════

var db *sql.DB

const dbPath = "/var/lib/nimos/config/nimos.db"

func openDB() error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create db directory: %v", err)
	}

	var err error
	// IMPORTANTE: usamos _pragma= (sintaxis de modernc.org/sqlite) en vez de
	// _busy_timeout= (sintaxis del driver CGO mattn, que este driver IGNORA).
	// Cada _pragma se ejecuta en CADA conexión nueva del pool · crítico para
	// que busy_timeout aplique a las 8 conexiones, no solo a una. Sin esto,
	// las conexiones sin timeout fallan con "database is locked" al instante.
	//
	//   journal_mode(WAL)    · lectores concurrentes + un escritor
	//   busy_timeout(10000)  · esperar 10s si hay contención, en vez de fallar
	//   foreign_keys(1)      · CASCADE/RESTRICT en tablas storage_*
	//   synchronous(NORMAL)  · seguro en WAL, escrituras más rápidas (menos lock)
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("cannot open database: %v", err)
	}

	// ── Pool de conexiones · concurrencia con WAL ──
	//
	// Beta 8.2 (01/06/2026) · fix del cuello de botella de BD.
	//
	// PROBLEMA RESUELTO: antes había SetMaxOpenConns(1) · UNA sola conexión
	// para todo el daemon. Eso serializaba TODAS las operaciones de BD ·
	// lecturas incluidas. Durante operaciones largas (instalar app, scan de
	// devices cada 30s), NimHealth y los reconcilers se bloqueaban esperando
	// la única conexión. Síntomas en producción: "NimHealth muerto durante
	// instalaciones", apps colgadas en "Instalando...", ListPools con
	// "context canceled", y el daemon llegando a caerse bajo carga.
	//
	// SOLUCIÓN: WAL permite MÚLTIPLES LECTORES concurrentes + UN escritor.
	// Subimos MaxOpenConns para aprovecharlo · las lecturas (la mayoría de las
	// ops) corren en paralelo. Las escrituras las serializa SQLite (un
	// escritor) y busy_timeout=10s hace que esperen en vez de fallar.
	//
	// Los PRAGMAs van en el DSN (_pragma=) para que se apliquen a CADA
	// conexión del pool, no solo a una.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0) // conexiones a archivo local · no reciclar

	// Force foreign_keys ON explicitly. The query string `?_foreign_keys=ON`
	// is not honored by modernc.org/sqlite, so we set it via PRAGMA after
	// opening. Required for CASCADE/RESTRICT in storage_* tables to work.
	// see docs/storage_invariants.md#5.1
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("cannot enable foreign_keys: %v", err)
	}

	if err := createTables(); err != nil {
		return fmt.Errorf("cannot create tables: %v", err)
	}

	return nil
}

func createTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		username     TEXT PRIMARY KEY,
		password     TEXT NOT NULL,
		role         TEXT NOT NULL DEFAULT 'user',
		description  TEXT DEFAULT '',
		totp_secret  TEXT DEFAULT '',
		totp_enabled INTEGER DEFAULT 0,
		backup_codes TEXT DEFAULT '',
		created_at   TEXT NOT NULL,
		updated_at   TEXT
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token        TEXT PRIMARY KEY,
		username     TEXT NOT NULL,
		role         TEXT NOT NULL,
		created_at   INTEGER NOT NULL,
		expires_at   INTEGER NOT NULL,
		ip           TEXT DEFAULT '',
		FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS shares (
		name         TEXT PRIMARY KEY,
		display_name TEXT NOT NULL,
		description  TEXT DEFAULT '',
		path         TEXT NOT NULL UNIQUE,
		volume       TEXT NOT NULL,
		pool         TEXT NOT NULL,
		recycle_bin  INTEGER DEFAULT 1,
		created_by   TEXT NOT NULL,
		created_at   TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS share_permissions (
		share_name   TEXT NOT NULL,
		username     TEXT NOT NULL,
		permission   TEXT NOT NULL,
		PRIMARY KEY (share_name, username),
		FOREIGN KEY (share_name) REFERENCES shares(name) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS app_permissions (
		share_name   TEXT NOT NULL,
		app_id       TEXT NOT NULL,
		uid          INTEGER NOT NULL,
		permission   TEXT NOT NULL,
		PRIMARY KEY (share_name, app_id),
		FOREIGN KEY (share_name) REFERENCES shares(name) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS user_app_access (
		username     TEXT NOT NULL,
		app_id       TEXT NOT NULL,
		permission   TEXT NOT NULL DEFAULT 'use',
		granted_by   TEXT NOT NULL,
		granted_at   TEXT NOT NULL,
		PRIMARY KEY (username, app_id),
		FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS managed_folders (
		id            TEXT PRIMARY KEY,
		share_name    TEXT NOT NULL,
		rel_path      TEXT NOT NULL,
		quota_bytes   INTEGER NOT NULL DEFAULT 0,
		generation    INTEGER NOT NULL DEFAULT 0,
		control_state TEXT NOT NULL DEFAULT 'active',
		owner_user    TEXT NOT NULL,
		created_by    TEXT NOT NULL,
		created_at    TEXT NOT NULL,
		UNIQUE (share_name, rel_path),
		FOREIGN KEY (share_name) REFERENCES shares(name) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS managed_folder_permissions (
		folder_id    TEXT NOT NULL,
		username     TEXT NOT NULL,
		permission   TEXT NOT NULL,
		PRIMARY KEY (folder_id, username),
		FOREIGN KEY (folder_id) REFERENCES managed_folders(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS preferences (
		username     TEXT NOT NULL,
		key          TEXT NOT NULL,
		value        TEXT NOT NULL,
		PRIMARY KEY (username, key)
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_username ON sessions(username);
	CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
	CREATE INDEX IF NOT EXISTS idx_share_perms_user ON share_permissions(username);
	CREATE INDEX IF NOT EXISTS idx_preferences_user ON preferences(username);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Migration: add backup_codes column if it doesn't exist
	db.Exec(`ALTER TABLE users ADD COLUMN backup_codes TEXT DEFAULT ''`)

	// Migration: create backup tables
	if err := createBackupTables(); err != nil {
		return fmt.Errorf("backup tables: %v", err)
	}

	// Create notification table
	if err := createNotificationTable(); err != nil {
		return fmt.Errorf("notification table: %v", err)
	}

	// Create app registry table
	if err := createAppRegistryTable(); err != nil {
		return fmt.Errorf("app registry table: %v", err)
	}

	// Create service registry tables
	if err := createServiceRegistryTables(); err != nil {
		return fmt.Errorf("service registry tables: %v", err)
	}

	// Create download tokens table (CRIT-008)
	if err := createDownloadTokensTable(); err != nil {
		return fmt.Errorf("download tokens table: %v", err)
	}

	// ── Apps module (Beta 8.1) ─────────────────────────────
	// Schema: docker_apps + native_apps (separado de app_registry).
	// Repo: AppsRepo gestiona CRUD; vive en db_apps.go.
	// Tests: db_apps_test.go valida el contrato del repo.
	if err := initAppsSchema(db); err != nil {
		return fmt.Errorf("apps schema: %v", err)
	}
	// ── App UIDs (Refactor permisos · Fase 1) ──────────────
	// Asignación de UID único por app Docker. Tablas app_uids + uid_allocator.
	// Debe ir TRAS initAppsSchema. Ver app_uids.go y PERMISOS-DESIGN.md.
	if err := initAppUIDsSchema(db); err != nil {
		return fmt.Errorf("app_uids schema: %v", err)
	}
	appsRepo = NewAppsRepo(db)
	// AppImagesRepo · sprint Updates (25/05/2026)
	// Tracking de digests de imágenes Docker · detección de actualizaciones.
	// Schema en apps_schema.sql · tabla docker_app_images.
	appImagesRepo = NewAppImagesRepo(db)

	// ── Operations module (Beta 8.1.x · APP-012) ─────────────
	// Schema: nimos_operations · async ops tracking.
	// Repo: OperationsRepo gestiona CRUD + state machine; vive en db_operations.go.
	// Sin consumidores hasta Fase 2 Batch 3 (dockerInstall async, dockerPull async).
	if err := initOperationsSchema(db); err != nil {
		return fmt.Errorf("operations schema: %v", err)
	}
	operationsRepo = NewOperationsRepo(db)

	// ── Schema migrations (versioned) ──
	runSchemaMigrations()

	return nil
}

// runSchemaMigrations applies versioned migrations.
// Each migration runs once and bumps user_version.
func runSchemaMigrations() {
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)

	if version < 1 {
		// v1: Extend app_registry with type and managed_by columns
		db.Exec(`ALTER TABLE app_registry ADD COLUMN type TEXT DEFAULT 'ui'`)
		db.Exec(`ALTER TABLE app_registry ADD COLUMN managed_by TEXT DEFAULT 'none'`)

		// Update existing apps with correct type and managed_by
		updates := []struct {
			id, appType, managedBy string
		}{
			{"nimsettings", "ui", "none"},
			{"storage", "system", "internal"},
			{"network", "system", "internal"},
			{"nimtorrent", "daemon", "systemd"},
			{"appstore", "ui", "none"},
			{"files", "ui", "none"},
			{"mediaplayer", "ui", "none"},
			{"terminal", "ui", "none"},
			{"containers", "docker", "docker"},
			{"monitor", "ui", "none"},
			{"vms", "system", "internal"},
			{"texteditor", "ui", "none"},
		}
		for _, u := range updates {
			db.Exec(`UPDATE app_registry SET type = ?, managed_by = ? WHERE id = ?`,
				u.appType, u.managedBy, u.id)
		}

		// Add nimbackup if not present
		db.Exec(`INSERT OR IGNORE INTO app_registry (id, name, category, admin_only, public, type, managed_by)
			VALUES ('nimbackup', 'NimBackup', 'system', 0, 0, 'daemon', 'internal')`)

		db.Exec("PRAGMA user_version = 1")
		logMsg("schema: migrated to version 1 (app_registry extended, service registry)")
	}

	if version < 2 {
		// v2: Add NimHealth app to registry
		db.Exec(`INSERT OR IGNORE INTO app_registry (id, name, category, admin_only, public, type, managed_by)
			VALUES ('nimhealth', 'NimHealth', 'system', 0, 0, 'ui', 'none')`)
		db.Exec("PRAGMA user_version = 2")
		logMsg("schema: migrated to version 2 (nimhealth app)")
	}

	if version < 3 {
		// v3: Normalize HealthStatus vocabulary to the 6 official states
		// (disciplina §6). Adds CHECK constraints on status+health, adds
		// last_observed_at, drops unused 'optional' dependency level.
		//
		// Mapeo old→new:
		//   status:  failed → error
		//   health:  unreachable → failed
		//            unhealthy   → failed
		//            idle        → healthy  (engine OK, just no containers)
		//
		// TRANSACCIONAL: o se aplica entera o nada. Si falla por la mitad,
		// rollback automático y service_instances queda intacta.
		if err := migrateToV3(); err != nil {
			logMsg("schema: ERROR migrating to v3: %v · service_instances unchanged", err)
		} else {
			db.Exec("PRAGMA user_version = 3")
			logMsg("schema: migrated to version 3 (HealthStatus normalized)")
		}
	}

	// Future migrations go here:
	// if version < 4 { ... db.Exec("PRAGMA user_version = 4") }
}

// migrateToV3 ejecuta la migración v3 en una transacción.
// Devuelve error si cualquier paso falla; SQLite hace rollback automático.
func migrateToV3() error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() // no-op si Commit() tuvo éxito

	stmts := []string{
		// 1. Nueva tabla con CHECK constraints + last_observed_at
		`CREATE TABLE service_instances_v3 (
			id               TEXT PRIMARY KEY,
			app_id           TEXT NOT NULL,
			pool_name        TEXT NOT NULL,
			path             TEXT NOT NULL,
			status           TEXT CHECK (status IN
			                   ('running','stopped','starting','stopping','error','unknown'))
			                   DEFAULT 'unknown',
			health           TEXT CHECK (health IN
			                   ('healthy','degraded','failed','partial','unknown','stale'))
			                   DEFAULT 'unknown',
			owner            TEXT DEFAULT 'system',
			config           TEXT DEFAULT '{}',
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			last_observed_at TEXT,
			FOREIGN KEY (app_id) REFERENCES app_registry(id)
		)`,

		// 2. Copiar datos con mapeo CASE
		`INSERT INTO service_instances_v3
			(id, app_id, pool_name, path, status, health, owner, config,
			 created_at, updated_at, last_observed_at)
		SELECT
			id, app_id, pool_name, path,
			CASE status
				WHEN 'running'  THEN 'running'
				WHEN 'stopped'  THEN 'stopped'
				WHEN 'starting' THEN 'starting'
				WHEN 'stopping' THEN 'stopping'
				WHEN 'failed'   THEN 'error'
				WHEN 'error'    THEN 'error'
				ELSE 'unknown'
			END AS status,
			CASE health
				WHEN 'healthy'     THEN 'healthy'
				WHEN 'degraded'    THEN 'degraded'
				WHEN 'unreachable' THEN 'failed'
				WHEN 'unhealthy'   THEN 'failed'
				WHEN 'idle'        THEN 'healthy'
				WHEN 'failed'      THEN 'failed'
				WHEN 'partial'     THEN 'partial'
				WHEN 'stale'       THEN 'stale'
				WHEN 'incomplete'  THEN 'unknown'  -- NimHealth no usa este valor
				ELSE 'unknown'
			END AS health,
			owner, config, created_at, updated_at, NULL
		FROM service_instances`,

		// 3. Swap tablas
		`DROP TABLE service_instances`,
		`ALTER TABLE service_instances_v3 RENAME TO service_instances`,

		// 4. Recrear índices (perdidos al hacer DROP/RENAME)
		`CREATE INDEX IF NOT EXISTS idx_si_pool ON service_instances(pool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_si_status ON service_instances(status)`,

		// 5. service_dependencies: 'optional' → 'soft' (nivel no usado en código)
		`UPDATE service_dependencies SET required='soft' WHERE required='optional'`,
	}

	for i, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}

	return tx.Commit()
}

// ═══════════════════════════════════
// Migration from JSON files
// ═══════════════════════════════════

type jsonUser struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	Description string `json:"description"`
	TotpSecret  string `json:"totpSecret"`
	TotpEnabled bool   `json:"totpEnabled"`
	Created     string `json:"created"`
}

type jsonShare struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"displayName"`
	Description    string            `json:"description"`
	Path           string            `json:"path"`
	Volume         string            `json:"volume"`
	Pool           string            `json:"pool"`
	RecycleBin     bool              `json:"recycleBin"`
	CreatedBy      string            `json:"createdBy"`
	Created        string            `json:"created"`
	Permissions    map[string]string `json:"permissions"`
	AppPermissions []json.RawMessage `json:"appPermissions"`
}

type jsonSession struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Created  int64  `json:"created"`
}

func migrateFromJSON() {
	migratedAny := false

	// Migrate users
	if data, err := os.ReadFile(usersFile); err == nil {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
		if count == 0 {
			var users []jsonUser
			if err := json.Unmarshal(data, &users); err == nil {
				tx, _ := db.Begin()
				for _, u := range users {
					totpEnabled := 0
					if u.TotpEnabled {
						totpEnabled = 1
					}
					tx.Exec(`INSERT OR IGNORE INTO users (username, password, role, description, totp_secret, totp_enabled, created_at)
						VALUES (?, ?, ?, ?, ?, ?, ?)`,
						u.Username, u.Password, u.Role, u.Description, u.TotpSecret, totpEnabled, u.Created)
				}
				tx.Commit()
				logMsg("  migration: imported %d users from JSON", len(users))
				migratedAny = true
			}
		}
	}

	// Migrate shares
	if data, err := os.ReadFile(sharesFile); err == nil {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM shares").Scan(&count)
		if count == 0 {
			var shares []jsonShare
			if err := json.Unmarshal(data, &shares); err == nil {
				tx, _ := db.Begin()
				for _, s := range shares {
					recycleBin := 0
					if s.RecycleBin {
						recycleBin = 1
					}
					tx.Exec(`INSERT OR IGNORE INTO shares (name, display_name, description, path, volume, pool, recycle_bin, created_by, created_at)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
						s.Name, s.DisplayName, s.Description, s.Path, s.Volume, s.Pool, recycleBin, s.CreatedBy, s.Created)

					for username, perm := range s.Permissions {
						tx.Exec(`INSERT OR IGNORE INTO share_permissions (share_name, username, permission)
							VALUES (?, ?, ?)`, s.Name, username, perm)
					}
				}
				tx.Commit()
				logMsg("  migration: imported %d shares from JSON", len(shares))
				migratedAny = true
			}
		}
	}

	// Migrate sessions
	sessionsFile := filepath.Join(filepath.Dir(usersFile), "sessions.json")
	if data, err := os.ReadFile(sessionsFile); err == nil {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
		if count == 0 {
			var sessions map[string]jsonSession
			if err := json.Unmarshal(data, &sessions); err == nil {
				tx, _ := db.Begin()
				imported := 0
				now := time.Now().UnixMilli()
				for token, s := range sessions {
					expiresAt := s.Created + sessionExpiryMs
					if expiresAt > now {
						tx.Exec(`INSERT OR IGNORE INTO sessions (token, username, role, created_at, expires_at)
							VALUES (?, ?, ?, ?, ?)`, token, s.Username, s.Role, s.Created, expiresAt)
						imported++
					}
				}
				tx.Commit()
				logMsg("  migration: imported %d active sessions from JSON", imported)
				migratedAny = true
			}
		}
	}

	// Rename old JSON files — Node.js now reads from SQLite via daemon
	if migratedAny {
		for _, f := range []string{usersFile, sharesFile, sessionsFile} {
			if _, err := os.Stat(f); err == nil {
				os.Rename(f, f+".migrated")
			}
		}
		logMsg("  migration: JSON files renamed to .migrated")
	}
}

// ═══════════════════════════════════
// User operations
// ═══════════════════════════════════

// ═══════════════════════════════════
// Session operations
// ═══════════════════════════════════

const sessionExpiryMs int64 = 24 * 60 * 60 * 1000 // 24 hours (sliding — renewed on each request)

// ═══════════════════════════════════
// Download tokens (CRIT-008: short-lived, one-time-use)
// ═══════════════════════════════════

const downloadTokenExpiryMs int64 = 60 * 1000 // 60 seconds

// ═══════════════════════════════════
// Share operations (data layer)
// ═══════════════════════════════════

// ═══════════════════════════════════
// Preferences operations
// ═══════════════════════════════════

// ═══════════════════════════════════
// App Registry — stored in DB, not hardcoded
// ═══════════════════════════════════

// ═══════════════════════════════════
// Helpers
// ═══════════════════════════════════

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
