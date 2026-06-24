package main

import "time"

func createAppRegistryTable() error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS app_registry (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		category    TEXT NOT NULL DEFAULT 'app',
		admin_only  INTEGER DEFAULT 0,
		public      INTEGER DEFAULT 0
	);`)
	if err != nil {
		return err
	}

	// Seed default apps if table is empty
	var count int
	db.QueryRow("SELECT COUNT(*) FROM app_registry").Scan(&count)
	if count == 0 {
		tx, _ := db.Begin()
		seedApps := []struct {
			id, name, category string
			adminOnly, public  int
		}{
			{"nimsettings", "NimSettings", "system", 0, 0},
			{"storage", "Storage", "system", 1, 0},
			{"network", "Network", "system", 1, 0},
			{"nimtorrent", "NimTorrent", "app", 0, 0},
			{"nimshield", "NimShield", "system", 0, 0},
			{"controlpanel", "Panel de Control", "system", 1, 0},
			{"appstore", "App Store", "system", 0, 0},
			{"files", "Files", "app", 0, 1},
			{"mediaplayer", "Media Player", "app", 0, 1},
			{"terminal", "Terminal", "system", 0, 0},
			{"containers", "Containers", "system", 0, 0},
			{"monitor", "System Monitor", "system", 0, 0},
			{"vms", "Virtual Machines", "system", 0, 0},
			{"texteditor", "Text Editor", "app", 0, 0},
		}
		for _, a := range seedApps {
			tx.Exec(`INSERT OR IGNORE INTO app_registry (id, name, category, admin_only, public) VALUES (?, ?, ?, ?, ?)`,
				a.id, a.name, a.category, a.adminOnly, a.public)
		}
		tx.Commit()
		logMsg("app_registry: seeded %d default apps", len(seedApps))
	}

	// Migración idempotente: registra apps añadidas en versiones posteriores
	// al seed inicial. En instalaciones existentes (app_registry ya poblada)
	// el bloque de arriba no corre, así que las apps nuevas se insertan aquí.
	// INSERT OR IGNORE respeta las filas existentes (no pisa configuración).
	laterApps := []struct {
		id, name, category string
		adminOnly, public  int
	}{
		{"nimshield", "NimShield", "system", 0, 0},           // concedible por el admin, no admin-only
		{"controlpanel", "Panel de Control", "system", 1, 0}, // admin-only: administración del sistema
	}
	for _, a := range laterApps {
		db.Exec(`INSERT OR IGNORE INTO app_registry (id, name, category, admin_only, public) VALUES (?, ?, ?, ?, ?)`,
			a.id, a.name, a.category, a.adminOnly, a.public)
	}
	return nil
}

// isPublicApp checks if an app is accessible to all authenticated users
func isPublicApp(appId string) bool {
	var pub int
	err := db.QueryRow(`SELECT public FROM app_registry WHERE id = ?`, appId).Scan(&pub)
	if err != nil {
		return false
	}
	return pub == 1
}

// isAdminOnlyApp checks if an app is restricted to admin users
func isAdminOnlyApp(appId string) bool {
	var adminOnly int
	err := db.QueryRow(`SELECT admin_only FROM app_registry WHERE id = ?`, appId).Scan(&adminOnly)
	if err != nil {
		return false
	}
	return adminOnly == 1
}

// dbListAppRegistry returns all registered apps for the admin panel
func dbListAppRegistry() ([]DBAppRegistryEntry, error) {
	rows, err := db.Query(`SELECT id, name, category, admin_only, public FROM app_registry ORDER BY category, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DBAppRegistryEntry
	for rows.Next() {
		var a DBAppRegistryEntry
		var adminOnly, public int
		rows.Scan(&a.Id, &a.Name, &a.Category, &adminOnly, &public)
		a.AdminOnly = adminOnly == 1
		a.Public = public == 1
		result = append(result, a)
	}
	if result == nil {
		result = []DBAppRegistryEntry{}
	}
	return result, nil
}

// Check if a user has access to an app
// Admin always has access. Public apps are always accessible.
// For everything else, check user_app_access table.
func dbUserHasAppAccess(username, role, appId string) bool {
	if role == "admin" {
		return true
	}
	if isPublicApp(appId) {
		return true
	}
	if isAdminOnlyApp(appId) {
		return false
	}
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM user_app_access WHERE username = ? AND app_id = ?`, username, appId).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// Get permission level for a user+app ('use', 'manage', or ”)
func dbUserGetAppPermission(username, role, appId string) string {
	if role == "admin" {
		return "manage"
	}
	if isPublicApp(appId) {
		return "use"
	}
	if isAdminOnlyApp(appId) {
		return ""
	}
	var perm string
	err := db.QueryRow(`SELECT permission FROM user_app_access WHERE username = ? AND app_id = ?`, username, appId).Scan(&perm)
	if err != nil {
		return ""
	}
	return perm
}

// List all app access for a user
func dbUserListAppAccess(username string) ([]DBAppGrant, error) {
	rows, err := db.Query(`SELECT app_id, permission, granted_by, granted_at FROM user_app_access WHERE username = ? ORDER BY app_id`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DBAppGrant
	for rows.Next() {
		g := DBAppGrant{Username: username}
		rows.Scan(&g.AppId, &g.Permission, &g.GrantedBy, &g.GrantedAt)
		result = append(result, g)
	}
	if result == nil {
		result = []DBAppGrant{}
	}
	return result, nil
}

// List all app access entries (for admin panel)
func dbAppAccessListAll() ([]DBAppGrant, error) {
	rows, err := db.Query(`SELECT username, app_id, permission, granted_by, granted_at FROM user_app_access ORDER BY username, app_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DBAppGrant
	for rows.Next() {
		var g DBAppGrant
		rows.Scan(&g.Username, &g.AppId, &g.Permission, &g.GrantedBy, &g.GrantedAt)
		result = append(result, g)
	}
	if result == nil {
		result = []DBAppGrant{}
	}
	return result, nil
}

// Grant app access
func dbAppAccessGrant(username, appId, permission, grantedBy string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO user_app_access (username, app_id, permission, granted_by, granted_at) VALUES (?, ?, ?, ?, ?)`,
		username, appId, permission, grantedBy, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Revoke app access
func dbAppAccessRevoke(username, appId string) error {
	_, err := db.Exec(`DELETE FROM user_app_access WHERE username = ? AND app_id = ?`, username, appId)
	return err
}

// Revoke all app access for a user
func dbAppAccessRevokeAll(username string) error {
	_, err := db.Exec(`DELETE FROM user_app_access WHERE username = ?`, username)
	return err
}
