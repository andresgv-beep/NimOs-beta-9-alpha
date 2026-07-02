package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════════
// NimShield — Database + HTTP API
// ═══════════════════════════════════════════════════════════════════════════════

// ── DB Init ──────────────────────────────────────────────────────────────────

func dbShieldInit() {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS shield_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			source_ip TEXT,
			user_agent TEXT,
			endpoint TEXT,
			username TEXT,
			method TEXT,
			status INTEGER,
			rule TEXT,
			details TEXT,
			net_key TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_shield_events_ip ON shield_events(source_ip);
		CREATE INDEX IF NOT EXISTS idx_shield_events_ts ON shield_events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_shield_events_cat ON shield_events(category);

		CREATE TABLE IF NOT EXISTS shield_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ip TEXT NOT NULL,
			reason TEXT,
			rule TEXT,
			expires_at TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_shield_blocks_ip ON shield_blocks(ip);

		CREATE TABLE IF NOT EXISTS shield_whitelist (
			ip TEXT PRIMARY KEY,
			note TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS shield_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	if err != nil {
		logMsg("shield DB init error: %v", err)
	}

	// net_key: la clave de red del evento (IPv6 → /64, ver shieldNetKey), para
	// que la correlación agregue por red y no por IP exacta. source_ip conserva
	// la IP real (forense). ALTER idempotente para BDs existentes (falla en
	// silencio si la columna ya existe, mismo patrón que shield_reputation).
	db.Exec(`ALTER TABLE shield_events ADD COLUMN net_key TEXT`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_shield_events_netkey ON shield_events(net_key)`)

	dbShieldReputationInit()
	dbShieldConfigInit()
	dbIntelInit()
}

// ── Enabled-state persistence ────────────────────────────────────────────────
// NimShield's on/off state survives restarts via the shield_settings table.
// The hot source of truth is the atomic shieldEnabled (shield.go); the DB backs
// it up. Default (no row) is "enabled".

func dbShieldSetEnabled(enabled bool) {
	v := "0"
	if enabled {
		v = "1"
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO shield_settings (key, value) VALUES ('enabled', ?)`, v); err != nil {
		logMsg("shield: failed to persist enabled state: %v", err)
	}
}

// loadShieldEnabled reads the persisted on/off state into the atomic flag.
// If no row exists yet (fresh install), the init() default of true stands.
func loadShieldEnabled() {
	var v string
	err := db.QueryRow(`SELECT value FROM shield_settings WHERE key = 'enabled'`).Scan(&v)
	if err != nil {
		// No persisted state — keep the default.
		return
	}
	shieldEnabled.Store(v == "1")
	if v != "1" {
		logMsg("shield: persisted state is disabled")
	}
}

// ── Whitelist persistence ────────────────────────────────────────────────────
// IPs de confianza que NimShield NUNCA bloquea (p.ej. la IP de auditoría del
// admin). Persisten en BD y se cargan en memoria al arrancar. La fuente de
// verdad en caliente es el mapa shieldWhitelist (shield.go); la BD lo respalda.

func dbShieldWhitelistGetAll() []map[string]string {
	rows, err := db.Query(`SELECT ip, note, created_at FROM shield_whitelist ORDER BY created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var ip, note, created string
		if err := rows.Scan(&ip, &note, &created); err == nil {
			out = append(out, map[string]string{"ip": ip, "note": note, "created_at": created})
		}
	}
	return out
}

func dbShieldWhitelistAdd(ip, note string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO shield_whitelist (ip, note) VALUES (?, ?)`, ip, note)
	return err
}

func dbShieldWhitelistRemove(ip string) error {
	_, err := db.Exec(`DELETE FROM shield_whitelist WHERE ip = ?`, ip)
	return err
}

// loadPersistedWhitelist carga las entradas de confianza de BD al estado en
// caliente (IPs exactas al mapa, CIDRs a la lista de prefijos). Se llama al
// arrancar el shield, junto a loadPersistedBlocks.
func loadPersistedWhitelist() {
	entries := dbShieldWhitelistGetAll()
	for _, e := range entries {
		shieldWhitelistApplyLive(e["ip"])
	}
	if len(entries) > 0 {
		logMsg("shield: loaded %d whitelisted entries", len(entries))
	}
}

func dbShieldEventInsert(event ShieldEvent) {
	rule := ""
	if r, ok := event.Details["rule"].(string); ok {
		rule = r
	}
	detailsJSON := "{}"
	if event.Details != nil {
		if data, err := json.Marshal(event.Details); err == nil {
			detailsJSON = string(data)
		}
	}

	db.Exec(`INSERT INTO shield_events (timestamp, category, severity, source_ip, user_agent, endpoint, username, method, status, rule, details, net_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.Timestamp.UTC().Format(time.RFC3339),
		event.Category, event.Severity, event.SourceIP,
		event.UserAgent, event.Endpoint, event.Username,
		event.Method, event.Status, rule, detailsJSON,
		shieldNetKey(event.SourceIP))
}

func dbShieldEventsCleanup(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge).UTC().Format(time.RFC3339)
	db.Exec(`DELETE FROM shield_events WHERE timestamp < ?`, cutoff)
}

func dbShieldEventsRecent(limit int) []map[string]interface{} {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, timestamp, category, severity, source_ip, endpoint, method, rule, details
		FROM shield_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id int
		var ts, cat, sev, ip, endpoint, method, rule, details string
		rows.Scan(&id, &ts, &cat, &sev, &ip, &endpoint, &method, &rule, &details)
		events = append(events, map[string]interface{}{
			"id": id, "timestamp": ts, "category": cat, "severity": sev,
			"sourceIP": ip, "endpoint": endpoint, "method": method, "rule": rule,
		})
	}
	if events == nil {
		events = []map[string]interface{}{}
	}
	return events
}

// ── Block Storage ────────────────────────────────────────────────────────────

func dbShieldBlockInsert(ip string, duration time.Duration, reason, rule string) {
	expiresAt := time.Now().Add(duration).UTC().Format(time.RFC3339)
	// Upsert — replace if IP already blocked
	db.Exec(`DELETE FROM shield_blocks WHERE ip = ?`, ip)
	db.Exec(`INSERT INTO shield_blocks (ip, reason, rule, expires_at) VALUES (?, ?, ?, ?)`,
		ip, reason, rule, expiresAt)
}

func dbShieldBlockDelete(ip string) {
	db.Exec(`DELETE FROM shield_blocks WHERE ip = ?`, ip)
}

func dbShieldBlocksGetActive() []BlockEntry {
	rows, err := db.Query(`SELECT ip, reason, rule, expires_at, created_at FROM shield_blocks WHERE expires_at > datetime('now')`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var blocks []BlockEntry
	for rows.Next() {
		var b BlockEntry
		var expiresStr, createdStr string
		rows.Scan(&b.IP, &b.Reason, &b.Rule, &expiresStr, &createdStr)
		b.ExpiresAt, _ = time.Parse(time.RFC3339, expiresStr)
		b.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		blocks = append(blocks, b)
	}
	return blocks
}

// ── HTTP API ─────────────────────────────────────────────────────────────────

func handleShieldRoutes(w http.ResponseWriter, r *http.Request) {
	// Control de acceso por APP (no por rol): el admin concede acceso a
	// NimShield desde la gestión de usuarios; quien lo tenga, entra. Mismo
	// modelo que el resto de apps (p.ej. nimtorrent). El loopback y la
	// Lectura (status/events/blocks/whitelist GET): requireAppAccess basta.
	// Mutaciones (unblock/toggle/whitelist POST): exigen rol admin dentro de
	// cada case — apagar el escudo o whitelistar al atacante no puede estar
	// al alcance de cualquier usuario con acceso a la app.
	session := requireAppAccess(w, r, "nimshield")
	if session == nil {
		return
	}

	path := r.URL.Path
	method := r.Method

	switch {

	// GET /api/shield/status
	case path == "/api/shield/status" && method == "GET":
		shieldBlockMu.RLock()
		blockedCount := len(shieldBlocklist)
		shieldBlockMu.RUnlock()

		jsonOk(w, map[string]interface{}{
			"enabled":            shieldEnabled.Load(),
			"blockedIPs":         blockedCount,
			"honeypots":          len(honeypotPaths),
			"rules":              22,
			"xssPatterns":        len(xssPatterns),
			"scannerUAs":         len(scannerUAs),
			"firewallEscalation": shieldFWEnabled.Load(),
			"firewallEntries":    shieldFWCount(),
			"droppedEvents":      shieldEventsDropped.Load(),
		})

	// GET /api/shield/events?limit=50
	case path == "/api/shield/events" && method == "GET":
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			fmt.Sscanf(l, "%d", &limit)
		}
		jsonOk(w, map[string]interface{}{"events": dbShieldEventsRecent(limit)})

	// GET /api/shield/blocks
	case path == "/api/shield/blocks" && method == "GET":
		shieldBlockMu.RLock()
		blocks := make([]map[string]interface{}, 0, len(shieldBlocklist))
		for ip, entry := range shieldBlocklist {
			blocks = append(blocks, map[string]interface{}{
				"ip":        ip,
				"reason":    entry.Reason,
				"rule":      entry.Rule,
				"expiresAt": entry.ExpiresAt.UTC().Format(time.RFC3339),
				"createdAt": entry.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		shieldBlockMu.RUnlock()
		jsonOk(w, map[string]interface{}{"blocks": blocks})

	// POST /api/shield/unblock — body: {"ip": "1.2.3.4"}
	case path == "/api/shield/unblock" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		ip := bodyStr(body, "ip")
		if ip == "" {
			jsonError(w, 400, "IP required")
			return
		}
		shieldUnblockIP(ip)
		jsonOk(w, map[string]interface{}{"ok": true, "ip": ip})

	// POST /api/shield/toggle — enable/disable
	case path == "/api/shield/toggle" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		newState := !shieldEnabled.Load()
		shieldEnabled.Store(newState)
		dbShieldSetEnabled(newState)
		if newState {
			// Si el daemon arrancó con el shield desactivado, el motor (event
			// loop, whitelist/bloqueos persistidos, feed intel) nunca llegó a
			// arrancar: hay que levantarlo ahora. Idempotente — si ya corre,
			// no hace nada.
			shieldEnsureEngine()
			// Si el escalado a firewall estaba armado, re-crear la tabla y
			// re-escalar reincidentes (el apagado anterior la desmontó).
			if shieldFWEnabled.Load() {
				if err := shieldFWInit(); err != nil {
					logMsg("shield FW: no pude re-crear la tabla al re-activar: %v", err)
				} else {
					shieldFWResync()
				}
			}
		} else {
			// Shield apagado = NADA bloquea, tampoco el kernel: desmontar la
			// tabla nftables (los DROP no deben sobrevivir al apagado). El
			// event loop en marcha es inocuo con el middleware desactivado.
			shieldFWTeardown()
		}
		logMsg("shield: %s by %s", map[bool]string{true: "enabled", false: "disabled"}[newState], session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "enabled": newState})

	// POST /api/shield/firewall — armar/desarmar el escalado a kernel (admin)
	// body: {"enable": true|false}
	case path == "/api/shield/firewall" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		on, _ := bodyBool(body, "enable")
		if err := shieldFWSetEnabled(on); err != nil {
			jsonError(w, 500, "No se pudo configurar el firewall: "+err.Error())
			return
		}
		logMsg("shield FW: escalation %s by %s", map[bool]string{true: "ENABLED", false: "disabled"}[on], session.Username)
		jsonOk(w, map[string]interface{}{
			"ok": true, "firewallEscalation": shieldFWEnabled.Load(), "firewallEntries": shieldFWCount(),
		})

	// GET /api/shield/config — política actual
	case path == "/api/shield/config" && method == "GET":
		jsonOk(w, map[string]interface{}{"ok": true, "config": getShieldConfig(), "defaults": defaultShieldConfig()})

	// PUT /api/shield/config — actualizar política (admin)
	case path == "/api/shield/config" && method == "PUT":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		var newCfg ShieldConfig
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			jsonError(w, 400, "JSON inválido")
			return
		}
		if err := setShieldConfig(newCfg); err != nil {
			jsonError(w, 500, "No se pudo guardar la configuración")
			return
		}
		logMsg("shield: config updated by %s", session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "config": getShieldConfig()})

	// GET /api/shield/reputation — IPs conocidas con su nivel
	// GET /api/shield/intel — estado del threat feed
	case path == "/api/shield/intel" && method == "GET":
		jsonOk(w, map[string]interface{}{"ok": true, "intel": intelStatus()})

	// POST /api/shield/intel/enforce — activar/desactivar bloqueo en duro (admin)
	case path == "/api/shield/intel/enforce" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		on, _ := bodyBool(body, "enforce")
		intelSetEnforce(on)
		jsonOk(w, map[string]interface{}{"ok": true, "intel": intelStatus()})

	// POST /api/shield/intel/refresh — forzar descarga del feed ahora (admin)
	case path == "/api/shield/intel/refresh" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		v, err := intelRefresh()
		if err != nil {
			jsonError(w, 502, "No se pudo actualizar el feed: "+err.Error())
			return
		}
		logMsg("shield: intel feed refreshed to v%d by %s", v, session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "intel": intelStatus()})

	// POST /api/shield/intel/rollback — volver a la versión previa (admin)
	case path == "/api/shield/intel/rollback" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		v, err := intelRollback()
		if err != nil {
			jsonError(w, 400, "No se pudo hacer rollback: "+err.Error())
			return
		}
		logMsg("shield: intel rolled back to v%d by %s", v, session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "intel": intelStatus()})

	case path == "/api/shield/reputation" && method == "GET":
		jsonOk(w, map[string]interface{}{"ok": true, "reputation": shieldRepList()})

	// POST /api/shield/reputation/forget — degradar IP a desconocida (admin)
	case path == "/api/shield/reputation/forget" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		ip := bodyStr(body, "ip")
		if ip == "" {
			jsonError(w, 400, "IP required")
			return
		}
		if err := shieldRepForget(ip); err != nil {
			jsonError(w, 500, "No se pudo olvidar la IP")
			return
		}
		logMsg("shield: reputation forgotten for %s by %s", ip, session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "ip": ip})

	// GET /api/shield/whitelist — lista IPs de confianza
	case path == "/api/shield/whitelist" && method == "GET":
		jsonOk(w, map[string]interface{}{"ok": true, "whitelist": dbShieldWhitelistGetAll()})

	// POST /api/shield/whitelist — body: {"ip": "1.2.3.4" | "10.0.0.0/24", "note": "auditoría"}
	case path == "/api/shield/whitelist" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		ip := bodyStr(body, "ip")
		note := bodyStr(body, "note")
		// Validar: IP exacta o rango CIDR — nada de basura arbitraria.
		if net.ParseIP(ip) == nil {
			if _, err := netip.ParsePrefix(ip); err != nil {
				jsonError(w, 400, "IP o CIDR inválido (ej: 192.168.1.100 o 192.168.1.0/24)")
				return
			}
		}
		if err := dbShieldWhitelistAdd(ip, note); err != nil {
			jsonError(w, 500, "No se pudo guardar")
			return
		}
		// Aplicar en caliente y liberar todo bloqueo activo que la entrada
		// cubra (incluidos los escalados al kernel).
		shieldWhitelistApplyLive(ip)
		shieldUnblockCovered(ip)
		logMsg("shield: whitelisted %s by %s", ip, session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "ip": ip})

	// POST /api/shield/whitelist/remove — body: {"ip": "1.2.3.4"}
	case path == "/api/shield/whitelist/remove" && method == "POST":
		if session.Role != "admin" {
			jsonError(w, 403, "Solo un administrador puede modificar NimShield")
			return
		}
		body, _ := readBody(r)
		ip := bodyStr(body, "ip")
		if ip == "" {
			jsonError(w, 400, "IP required")
			return
		}
		// No permitir quitar loopback (rompería el acceso local de Caddy).
		if ip == "127.0.0.1" || ip == "::1" {
			jsonError(w, 400, "No se puede quitar loopback de la whitelist")
			return
		}
		if err := dbShieldWhitelistRemove(ip); err != nil {
			jsonError(w, 500, "No se pudo quitar")
			return
		}
		shieldWhitelistRemoveLive(ip)
		logMsg("shield: un-whitelisted %s by %s", ip, session.Username)
		jsonOk(w, map[string]interface{}{"ok": true, "ip": ip})

	default:
		jsonError(w, 404, "Not found")
	}
}
