package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════════
// NimShield — Application Security Module
// Phase 1: Collector, Honeypots, Blocklist, Middleware
// ═══════════════════════════════════════════════════════════════════════════════

// ── Shield Event ─────────────────────────────────────────────────────────────

type ShieldEvent struct {
	Timestamp time.Time
	Category  string // auth, traversal, injection, scan, docker, system, honeypot
	Severity  string // low, medium, high, critical
	SourceIP  string
	UserAgent string
	Endpoint  string
	Username  string
	Method    string
	Status    int
	Details   map[string]interface{}
}

var shieldEvents = make(chan ShieldEvent, 2000)

// shieldEmit sends an event to the shield engine. Non-blocking — drops if full.
func shieldEmit(event ShieldEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	select {
	case shieldEvents <- event:
	default:
		// Channel full — drop event rather than block the request
	}
}

// ── Blocklist ────────────────────────────────────────────────────────────────

type BlockEntry struct {
	IP        string
	Reason    string
	Rule      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

var (
	shieldBlocklist = map[string]*BlockEntry{} // IP → entry
	shieldBlockMu   sync.RWMutex
	// shieldEnabled is read from request goroutines and written from the HTTP
	// toggle handler. It MUST be atomic to avoid a data race. Persisted in the
	// shield_settings table; loaded at startup (see loadShieldEnabled).
	shieldEnabled atomic.Bool
)

func init() {
	// Default: shield on. Overridden by persisted state at startup if present.
	shieldEnabled.Store(true)
}

func shieldBlockIP(ip string, duration time.Duration, reason, rule string) {
	shieldBlockMu.Lock()
	shieldBlocklist[ip] = &BlockEntry{
		IP:        ip,
		Reason:    reason,
		Rule:      rule,
		ExpiresAt: time.Now().Add(duration),
		CreatedAt: time.Now(),
	}
	shieldBlockMu.Unlock()

	logMsg("shield BLOCK: %s for %v — %s [%s]", ip, duration, reason, rule)

	// Store in DB for persistence across restarts
	dbShieldBlockInsert(ip, duration, reason, rule)

	// Emit notification
	addNotification("warning", "system",
		fmt.Sprintf("IP bloqueada: %s", ip),
		fmt.Sprintf("NimShield bloqueó %s por %v. Motivo: %s", ip, duration, reason))
}

func shieldUnblockIP(ip string) {
	shieldBlockMu.Lock()
	delete(shieldBlocklist, ip)
	shieldBlockMu.Unlock()

	dbShieldBlockDelete(ip)
	logMsg("shield UNBLOCK: %s", ip)
}

func shieldIsBlocked(ip string) (bool, string) {
	shieldBlockMu.RLock()
	entry, exists := shieldBlocklist[ip]
	shieldBlockMu.RUnlock()

	if !exists {
		return false, ""
	}

	// Check expiry
	if time.Now().After(entry.ExpiresAt) {
		shieldUnblockIP(ip)
		return false, ""
	}

	return true, entry.Reason
}

// ── Whitelist ────────────────────────────────────────────────────────────────

var shieldWhitelist = map[string]bool{
	"127.0.0.1": true,
	"::1":       true,
}

func shieldIsWhitelisted(ip string) bool {
	shieldBlockMu.RLock()
	ok := shieldWhitelist[ip]
	shieldBlockMu.RUnlock()
	return ok
	// Nota: la LAN NO se whitelistea por defecto (solo loopback y las IPs que
	// el admin añada explícitamente). El bloque de CIDRs privados anterior era
	// lógica muerta (calculaba Contains y siempre devolvía false); eliminado.
}

// ── Honeypots ────────────────────────────────────────────────────────────────
// Endpoints that don't exist in NimOS. Any request = 100% malicious.
// Zero false positives. Instant block.

var honeypotPaths = map[string]string{
	"/.env":            "HONEY-001",
	"/.git/config":     "HONEY-002",
	"/.git/HEAD":       "HONEY-002",
	"/wp-login.php":    "HONEY-003",
	"/wp-admin":        "HONEY-004",
	"/wp-admin/":       "HONEY-004",
	"/phpmyadmin":      "HONEY-005",
	"/phpmyadmin/":     "HONEY-005",
	"/admin":           "HONEY-006",
	"/api/admin/debug": "HONEY-007",
	"/config.json":     "HONEY-008",
	"/api/v1/exec":     "HONEY-009",
	"/shell":           "HONEY-010",
	"/console":         "HONEY-011",
	"/actuator":        "HONEY-012",
	"/actuator/health": "HONEY-012",
	"/server-status":   "HONEY-013",
	"/xmlrpc.php":      "HONEY-015",
	"/cgi-bin/":        "HONEY-016",
	"/manager/html":    "HONEY-017",
	"/solr/":           "HONEY-018",
	"/api/jsonws":      "HONEY-019",
	"/vendor/phpunit":  "HONEY-020",
}

// honeypotPrefixes are the ONLY honeypot paths allowed to match by prefix
// (i.e. "/cgi-bin/anything" triggers). These are third-party software roots
// that NimOS itself never serves, so a prefix match cannot collide with a
// legitimate NimOS/AppStore route. Every other honeypot requires an EXACT
// path match (N4: a broad prefix match was auto-blocking legitimate LAN IPs
// when an app registered a path under a honeypot prefix).
var honeypotPrefixes = map[string]string{
	"/cgi-bin/":    "HONEY-016",
	"/solr/":       "HONEY-018",
	"/phpmyadmin/": "HONEY-005",
	"/wp-admin/":   "HONEY-004",
}

func checkHoneypot(r *http.Request) bool {
	path := strings.ToLower(r.URL.Path)
	rule, isHoneypot := honeypotPaths[path]
	if !isHoneypot {
		// Prefix match ONLY against the curated, attack-only prefixes, and
		// never against NimOS app routes (which live under /app/).
		if !strings.HasPrefix(path, "/app/") {
			for hPath, hRule := range honeypotPrefixes {
				if strings.HasPrefix(path, hPath) {
					rule = hRule
					isHoneypot = true
					break
				}
			}
		}
	}

	if !isHoneypot {
		return false
	}

	ip := clientIP(r)

	shieldEmit(ShieldEvent{
		Category:  "honeypot",
		Severity:  "critical",
		SourceIP:  ip,
		UserAgent: r.UserAgent(),
		Endpoint:  r.URL.Path,
		Method:    r.Method,
		Details:   map[string]interface{}{"rule": rule},
	})

	// Instant block — 24h, no scoring needed
	if !shieldIsWhitelisted(ip) {
		shieldBlockIP(ip, 24*time.Hour, fmt.Sprintf("Honeypot: %s → %s", r.URL.Path, rule), rule)
	}

	return true
}

// ── XSS/Injection Detection in Requests (CSP Compensation) ──────────────────

var xssPatterns = []string{
	"<script", "</script", "javascript:", "onerror=", "onload=",
	"onfocus=", "onmouseover=", "onclick=", "eval(", "alert(",
	"document.cookie", "document.write", "window.location",
}

var sqliPatterns = []string{
	"' OR ", "' or ", "'; DROP", "'; drop", "UNION SELECT", "union select",
	"1=1", "' AND '", "' and '", "-- ", "/*", "*/",
}

var cmdPatterns = []string{
	"; rm ", "; cat ", "| nc ", "$(", "`", "&& curl", "&& wget",
	"/etc/passwd", "/etc/shadow", "| bash", "| sh",
}

func checkRequestPayload(r *http.Request) string {
	// Check URL query string. We inspect BOTH the raw query and a
	// percent-decoded form: an attacker can encode the payload (e.g.
	// %3Cscript%3E) to slip past a raw-only match. Decoding closes that bypass.
	rawQuery := r.URL.RawQuery
	queries := collectQueryForms(rawQuery)
	for _, query := range queries {
		if matchesPatterns(query, xssPatterns) {
			return "CSP-001"
		}
		if matchesPatterns(query, sqliPatterns) {
			return "INJ-001"
		}
		if matchesPatterns(query, cmdPatterns) {
			return "INJ-002"
		}
	}

	// Check URL path for traversal/injection. Decode first so encoded
	// traversal (%2e%2e) is caught too — both on Path and on the raw,
	// un-normalized RawPath (N3).
	path := r.URL.Path
	decodedPath := path
	if dp, err := url.QueryUnescape(path); err == nil {
		decodedPath = dp
	}
	rawPath := r.URL.RawPath
	decodedRawPath := rawPath
	if rawPath != "" {
		if dp, err := url.QueryUnescape(rawPath); err == nil {
			decodedRawPath = dp
		}
	}
	if strings.Contains(path, "..") ||
		strings.Contains(decodedPath, "..") ||
		strings.Contains(decodedRawPath, "..") {
		return "TRAV-001"
	}

	// Check request body (N2). API attacks (SQLi/XSS in JSON fields) travel in
	// POST/PUT bodies, which the query/path checks never see. We read the body
	// under a size cap (DoS guard) and RE-INJECT it so downstream handlers can
	// still read it. Without re-injection every POST handler would see an empty
	// body. GET/HEAD and bodyless requests skip this entirely.
	if rule := checkRequestBody(r); rule != "" {
		return rule
	}

	return ""
}

// shieldMaxBodyInspect caps how many bytes of a request body NimShield reads
// for inspection. Bodies larger than this are NOT inspected (and NOT truncated
// for the handler — the original stream is preserved). 1 MiB covers any
// legitimate JSON API payload while bounding memory use per request.
const shieldMaxBodyInspect = 1 << 20

// shieldIsUploadRequest detecta si una petición es una SUBIDA DE ARCHIVO, en
// cuyo caso su cuerpo no debe inspeccionarse por inyección (es binario/
// arbitrario por diseño).
//
// BLINDAJE: el skip se concede SOLO en rutas de subida CONOCIDAS. El
// Content-Type binario por sí solo NO basta — si bastara, un atacante podría
// mandar un payload de inyección a un endpoint de API normal con
// "Content-Type: application/octet-stream" y saltarse la inspección. Por eso
// el Content-Type binario solo se acepta como señal DENTRO de una ruta de
// upload conocida (refuerzo, no llave maestra).
//
// Rutas de subida conocidas (verificadas en el código, jun 2026):
//
//	· /api/files/upload, /api/files/upload-chunk  (Files)
//	· /api/user/wallpaper                          (galería de fondos)
//	· /api/torrent/upload                          (ficheros .torrent)
//
// Más la señal de subida por chunks (headers X-Filename / X-Chunk-Index), que
// va sin Content-Type y es intrínsecamente de upload.
func shieldIsUploadRequest(r *http.Request) bool {
	p := r.URL.Path

	// Señal 2 (independiente de ruta): subida por chunks. El chunk crudo viaja
	// sin Content-Type, identificado por estos headers → es upload por diseño.
	if r.Header.Get("X-Filename") != "" || r.Header.Get("X-Chunk-Index") != "" {
		return true
	}

	// Señal 1: ¿es una ruta de subida conocida?
	knownUploadPath := strings.HasPrefix(p, "/api/files/upload") || // /upload y /upload-chunk
		p == "/api/user/wallpaper" ||
		p == "/api/torrent/upload"
	if !knownUploadPath {
		// Fuera de rutas de upload conocidas NO se concede skip, aunque el
		// Content-Type sea binario. Aquí se cierra el hueco teórico.
		return false
	}

	// Señal 3 (solo dentro de una ruta de upload conocida): el Content-Type
	// binario/multipart confirma la subida. Si no lo es (p. ej. wallpaper por
	// base64-en-JSON), igualmente es una ruta de upload conocida → skip.
	return true
}

// checkRequestBody inspects the request body for injection patterns, then
// restores r.Body so downstream handlers read the same bytes. Returns a rule
// id on match, "" otherwise. Safe to call on any method/request.
func checkRequestBody(r *http.Request) string {
	if r.Body == nil || r.ContentLength <= 0 {
		return ""
	}
	if r.ContentLength >= shieldMaxBodyInspect {
		// Too large to inspect safely — leave the body untouched for the handler.
		return ""
	}

	// Las SUBIDAS DE ARCHIVOS no se inspeccionan: su cuerpo es contenido
	// arbitrario por naturaleza (un .zip con código Go dentro contiene strings
	// SQL, "exec.Command" o bytes que casan con los patrones por casualidad).
	// Escanearlas buscando "inyección" garantiza falsos positivos —bloquear al
	// dueño subiendo un fichero legítimo—. Un ataque de inyección real viaja en
	// el JSON/form de una llamada de API, no en el payload de un upload.
	//
	// La señal fiable es la RUTA de subida (el chunk va sin Content-Type, con
	// headers X-Filename/X-Chunk-Index) y, como refuerzo, los Content-Type
	// binarios típicos de upload.
	if shieldIsUploadRequest(r) {
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, shieldMaxBodyInspect))
	r.Body.Close()
	// ALWAYS re-inject what we read, even on a read error, so the handler is
	// never handed a consumed/empty body.
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return ""
	}

	body := string(data)
	// Inspect raw + percent-decoded forms (same rationale as the query string).
	for _, form := range collectQueryForms(body) {
		if matchesPatterns(form, xssPatterns) {
			return "CSP-001"
		}
		if matchesPatterns(form, sqliPatterns) {
			return "INJ-001"
		}
		if matchesPatterns(form, cmdPatterns) {
			return "INJ-002"
		}
	}
	return ""
}

// collectQueryForms returns the distinct strings to scan for a raw query: the
// raw form, a once-decoded form, and a twice-decoded form (to catch double
// encoding like %253C → %3C → <). Empty/duplicate forms are skipped.
func collectQueryForms(raw string) []string {
	if raw == "" {
		return nil
	}
	forms := []string{raw}
	seen := map[string]bool{raw: true}
	cur := raw
	for i := 0; i < 2; i++ {
		dec, err := url.QueryUnescape(cur)
		if err != nil || dec == cur || seen[dec] {
			break
		}
		forms = append(forms, dec)
		seen[dec] = true
		cur = dec
	}
	return forms
}

func matchesPatterns(input string, patterns []string) bool {
	// N5: match against several forms to defeat trivial evasions:
	//   · raw lowercased
	//   · inline SQL comments replaced by a SPACE then whitespace collapsed
	//     (defeats "union/**/select" → "union select")
	//   · inline SQL comments removed entirely then whitespace collapsed
	//     (defeats "un/**/ion" → "union")
	// This is short-term hardening; cumulative scoring is the proper long-term
	// answer (chasing a perfect pattern list is a losing race).
	lower := strings.ToLower(input)
	forms := []string{
		lower,
		collapseWS(stripSQLComments(lower, " ")),
		collapseWS(stripSQLComments(lower, "")),
	}
	for _, p := range patterns {
		lp := strings.ToLower(p)
		for _, f := range forms {
			if strings.Contains(f, lp) {
				return true
			}
		}
	}
	return false
}

// stripSQLComments replaces inline SQL block comments /* ... */ with repl.
func stripSQLComments(s, repl string) string {
	for {
		start := strings.Index(s, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "*/")
		if end < 0 {
			break
		}
		s = s[:start] + repl + s[start+2+end+2:]
	}
	return s
}

// collapseWS collapses any run of whitespace to a single space.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ── Scanner User-Agent Detection ─────────────────────────────────────────────

var scannerUAs = []string{
	"nikto", "sqlmap", "nmap", "masscan", "dirbuster", "gobuster",
	"wfuzz", "ffuf", "nuclei", "burpsuite", "zaproxy", "acunetix",
	"nessus", "openvas", "w3af", "skipfish", "arachni",
}

func isScannerUA(ua string) bool {
	lower := strings.ToLower(ua)
	for _, s := range scannerUAs {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// ── Shield Middleware ─────────────────────────────────────────────────────────
// Inserted at the very beginning of the HTTP handler chain.
// Returns true if the request was handled (blocked/honeypot) — caller should stop.

func shieldMiddleware(w http.ResponseWriter, r *http.Request) bool {
	if !shieldEnabled.Load() {
		return false
	}

	ip := clientIP(r)

	// 1. Check if IP is blocked
	if blocked, reason := shieldIsBlocked(ip); blocked {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(403)
		// N7: build the JSON via Marshal so a reason with quotes/newlines can
		// never break the response shape.
		resp, _ := json.Marshal(map[string]string{
			"error": "Blocked by NimShield: " + reason,
		})
		w.Write(resp)
		return true
	}

	// 1.5. NimShield Intelligence — ¿la IP está en el threat feed firmado?
	// En modo observación solo registra (no corta); en bloqueo activo, corta.
	// Respeta la whitelist internamente.
	if shieldIntelCheck(r) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(403)
		resp, _ := json.Marshal(map[string]string{
			"error": "Blocked by NimShield Intelligence threat feed",
		})
		w.Write(resp)
		return true
	}

	// 2. Check honeypots (instant detection, zero false positives)
	if checkHoneypot(r) {
		http.NotFound(w, r)
		return true
	}

	// 3. Check for known scanner user-agents
	if isScannerUA(r.UserAgent()) && !shieldIsWhitelisted(ip) {
		shieldEmit(ShieldEvent{
			Category:  "scan",
			Severity:  "high",
			SourceIP:  ip,
			UserAgent: r.UserAgent(),
			Endpoint:  r.URL.Path,
			Method:    r.Method,
			Details:   map[string]interface{}{"rule": "SCAN-003", "type": "scanner_ua"},
		})
		shieldBlockIP(ip, 24*time.Hour, "Vulnerability scanner: "+r.UserAgent(), "SCAN-003")
		http.NotFound(w, r)
		return true
	}

	// 4. Check request payload for XSS/SQLi/CMDi (CSP compensation)
	if rule := checkRequestPayload(r); rule != "" && !shieldIsWhitelisted(ip) {
		shieldEmit(ShieldEvent{
			Category:  categoryForRule(rule),
			Severity:  severityForRule(rule),
			SourceIP:  ip,
			UserAgent: r.UserAgent(),
			Endpoint:  r.URL.Path,
			Method:    r.Method,
			Details:   map[string]interface{}{"rule": rule, "query": r.URL.RawQuery},
		})
		// Don't block on first offense for injection — let the rule engine accumulate
		// But DO block immediately for command injection (INJ-002)
		if rule == "INJ-002" {
			shieldBlockIP(ip, 24*time.Hour, "Command injection attempt", rule)
			http.NotFound(w, r)
			return true
		}
	}

	return false
}

func categoryForRule(rule string) string {
	switch {
	case strings.HasPrefix(rule, "AUTH"):
		return "auth"
	case strings.HasPrefix(rule, "TRAV"):
		return "traversal"
	case strings.HasPrefix(rule, "INJ"), strings.HasPrefix(rule, "CSP"):
		return "injection"
	case strings.HasPrefix(rule, "SCAN"):
		return "scan"
	case strings.HasPrefix(rule, "HONEY"):
		return "honeypot"
	default:
		return "system"
	}
}

func severityForRule(rule string) string {
	switch rule {
	case "INJ-002", "HONEY-001":
		return "critical"
	case "INJ-001", "CSP-001", "TRAV-001":
		return "high"
	default:
		return "medium"
	}
}

// ── Shield Engine (background goroutine) ─────────────────────────────────────

func startShieldEngine() {
	// Init DB tables FIRST so we can read persisted state.
	dbShieldInit()

	// Load persisted enabled-state (overrides the init() default).
	loadShieldEnabled()

	if !shieldEnabled.Load() {
		logMsg("shield: disabled (persisted state)")
		return
	}

	// Load persisted blocks
	loadPersistedBlocks()
	loadPersistedWhitelist()

	logMsg("shield: engine started (honeypots: %d, scanner UAs: %d, XSS patterns: %d)",
		len(honeypotPaths), len(scannerUAs), len(xssPatterns))

	// Process events
	go shieldEventLoop()

	// NimShield Intelligence: carga el feed y refresca cada 2 días (no bloquea).
	startIntel()
}

func shieldEventLoop() {
	for event := range shieldEvents {
		// Store event in DB
		dbShieldEventInsert(event)

		// Run rule engine
		processRules(event)
	}
}

// ── Blocklist persistence ────────────────────────────────────────────────────

func loadPersistedBlocks() {
	blocks := dbShieldBlocksGetActive()
	shieldBlockMu.Lock()
	for _, b := range blocks {
		if b.ExpiresAt.After(time.Now()) {
			shieldBlocklist[b.IP] = &b
		}
	}
	shieldBlockMu.Unlock()
	if len(blocks) > 0 {
		logMsg("shield: loaded %d persisted blocks", len(blocks))
	}
}

// ── Cleanup expired blocks (runs every 5 min) ───────────────────────────────

func startShieldCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		now := time.Now()
		shieldBlockMu.Lock()
		for ip, entry := range shieldBlocklist {
			if now.After(entry.ExpiresAt) {
				delete(shieldBlocklist, ip)
				logMsg("shield: expired block for %s", ip)
			}
		}
		shieldBlockMu.Unlock()

		// Also clean old events from DB (keep 7 days)
		dbShieldEventsCleanup(7 * 24 * time.Hour)
	}
}
