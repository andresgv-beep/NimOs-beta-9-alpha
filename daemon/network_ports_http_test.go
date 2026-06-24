// network_ports_http_test.go — Tests de los handlers HTTP.
//
// Estrategia:
//
//   - Inicializamos networkRepo / networkEventEmitter como globals
//     porque el handler los lee directamente (igual que el resto del
//     daemon). Cada test setea su propio entorno y limpia.
//
//   - La auth (requireAdmin) consulta la DB de sesiones. Para tests
//     creamos un DBSession admin directamente vía SQL y mandamos el
//     Bearer token correspondiente.
//
// Cubre:
//   - GET list / detail (con auth).
//   - PUT update happy path + validaciones (port range, bind_address
//     inválido, JSON inválido, body incompleto).
//   - 401 sin auth, 403 sin admin.
//   - 404 puerto inválido.
//   - Tras update: desired incrementado, response refleja nuevo estado.
//   - Evento auditado.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Setup
// ─────────────────────────────────────────────────────────────────────────────

// httpTestMu serializa los tests que mutan singletons globales. Sin
// esto, el go test runner podría correr varios tests en paralelo y
// pisarse las globals (networkRepo, db).
var httpTestMu sync.Mutex

// setupPortsHTTPTest monta DB con schemas + globals + un usuario admin.
// Devuelve el bearer token y una cleanup func.
func setupPortsHTTPTest(t *testing.T) (token string, c *sqlConn, cleanup func()) {
	t.Helper()

	httpTestMu.Lock()

	// Guardar globals previos para restaurarlos.
	prevDB := db
	prevRepo := networkRepo
	prevEmitter := networkEventEmitter

	c, dbCleanup := setupNetworkDB(t)
	db = c.db

	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	networkRepo = NewNetworkRepo(c.db, clock)
	networkEventEmitter = NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	// Crear tabla de sesiones que matchea el schema real de db.go.
	// La auth real consulta `sessions` (no `db_sessions`) y busca por el
	// SHA-256 del bearer token, no por el token raw.
	if _, err := c.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			ip TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}

	// Sembrar un admin. El token que entregamos al cliente es el raw;
	// lo que persistimos es su SHA-256 (igual que auth.go).
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	token = hex.EncodeToString(tokenBytes)
	hashed := sha256Hex(token)
	nowMs := time.Now().UnixMilli()
	expiresMs := time.Now().Add(time.Hour).UnixMilli()
	if _, err := c.db.Exec(`
		INSERT INTO sessions (token, username, role, created_at, expires_at, ip)
		VALUES (?, ?, 'admin', ?, ?, '127.0.0.1')
	`, hashed, "test-admin", nowMs, expiresMs); err != nil {
		t.Fatal(err)
	}

	cleanup = func() {
		networkRepo = prevRepo
		networkEventEmitter = prevEmitter
		db = prevDB
		dbCleanup()
		httpTestMu.Unlock()
	}
	return token, c, cleanup
}

// seedPort crea un port y lo marca como applied — estado realista
// tras un boot exitoso.
func seedPort(t *testing.T, id string, port int, bind string, enabled bool) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := networkRepo.CreatePort(context.Background(), tx, &NetworkPort{
		ID: id, Port: port, BindAddress: bind, Enabled: enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := networkRepo.MarkPortApplied(context.Background(), tx, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// doReq construye una request HTTP y la pasa al dispatcher. Devuelve
// el ResponseRecorder ya con el resultado.
func doReq(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleNetworkPortsRoutes(rr, req)
	return rr
}

// decodeBody lee el body del recorder como map.
func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v (raw=%q)", err, rr.Body.String())
	}
	return out
}

// ═════════════════════════════════════════════════════════════════════════════
// Validación interna
// ═════════════════════════════════════════════════════════════════════════════

func TestValidatePortUpdate_Cases(t *testing.T) {
	intPtr := func(i int) *int { return &i }
	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name    string
		req     portUpdateRequest
		wantErr bool
	}{
		{"happy", portUpdateRequest{intPtr(8080), strPtr("0.0.0.0"), boolPtr(true)}, false},
		{"v6 wildcard", portUpdateRequest{intPtr(443), strPtr("::"), boolPtr(true)}, false},
		{"loopback", portUpdateRequest{intPtr(443), strPtr("127.0.0.1"), boolPtr(false)}, false},
		{"missing port", portUpdateRequest{nil, strPtr("0.0.0.0"), boolPtr(true)}, true},
		{"missing bind", portUpdateRequest{intPtr(80), nil, boolPtr(true)}, true},
		{"missing enabled", portUpdateRequest{intPtr(80), strPtr("0.0.0.0"), nil}, true},
		{"port 0", portUpdateRequest{intPtr(0), strPtr("0.0.0.0"), boolPtr(true)}, true},
		{"port 65536", portUpdateRequest{intPtr(65536), strPtr("0.0.0.0"), boolPtr(true)}, true},
		{"port negative", portUpdateRequest{intPtr(-1), strPtr("0.0.0.0"), boolPtr(true)}, true},
		{"bind hostname", portUpdateRequest{intPtr(80), strPtr("example.com"), boolPtr(true)}, true},
		{"bind empty", portUpdateRequest{intPtr(80), strPtr(""), boolPtr(true)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePortUpdate(&c.req)
			if (err != nil) != c.wantErr {
				t.Errorf("err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /api/v4/network/ports
// ═════════════════════════════════════════════════════════════════════════════

func TestPortsHTTP_ListReturnsTwoPorts(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	seedPort(t, "http", 80, "0.0.0.0", true)
	seedPort(t, "https", 443, "0.0.0.0", true)

	rr := doReq(t, token, "GET", "/api/v4/network/ports", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	ports, ok := body["ports"].([]interface{})
	if !ok || len(ports) != 2 {
		t.Fatalf("expected 2 ports in body, got %v", body)
	}
}

func TestPortsHTTP_ListRequiresAuth(t *testing.T) {
	_, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	rr := doReq(t, "", "GET", "/api/v4/network/ports", "")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 401 or 403", rr.Code)
	}
}

func TestPortsHTTP_ListRequiresAdmin(t *testing.T) {
	_, c, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	// Crear sesión con role=user (no admin).
	userTokenRaw := make([]byte, 16)
	rand.Read(userTokenRaw)
	tokStr := hex.EncodeToString(userTokenRaw)
	hashed := sha256Hex(tokStr)
	c.db.Exec(`INSERT INTO sessions (token, username, role, created_at, expires_at, ip)
		VALUES (?, 'joe', 'user', ?, ?, '127.0.0.1')`,
		hashed, time.Now().UnixMilli(), time.Now().Add(time.Hour).UnixMilli())

	rr := doReq(t, tokStr, "GET", "/api/v4/network/ports", "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestPortsHTTP_ListEmpty(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	rr := doReq(t, token, "GET", "/api/v4/network/ports", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := decodeBody(t, rr)
	if ports, ok := body["ports"].([]interface{}); !ok || len(ports) != 0 {
		t.Errorf("expected empty list, got %v", body["ports"])
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /api/v4/network/ports/:id
// ═════════════════════════════════════════════════════════════════════════════

func TestPortsHTTP_GetReturnsView(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	seedPort(t, "http", 8080, "127.0.0.1", true)

	rr := doReq(t, token, "GET", "/api/v4/network/ports/http", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["id"] != "http" || body["port"].(float64) != 8080 || body["bind_address"] != "127.0.0.1" {
		t.Errorf("body=%v", body)
	}
	// Tras seedPort hicimos MarkApplied → status=converged.
	if body["status"] != "converged" {
		t.Errorf("status=%v, want converged", body["status"])
	}
}

func TestPortsHTTP_GetNotFound(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	rr := doReq(t, token, "GET", "/api/v4/network/ports/http", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestPortsHTTP_GetInvalidID(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	rr := doReq(t, token, "GET", "/api/v4/network/ports/ftp", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// PUT /api/v4/network/ports/:id
// ═════════════════════════════════════════════════════════════════════════════

func TestPortsHTTP_UpdateHappy(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	seedPort(t, "http", 80, "0.0.0.0", true)

	body := `{"port": 8080, "bind_address": "127.0.0.1", "enabled": false}`
	rr := doReq(t, token, "PUT", "/api/v4/network/ports/http", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	out := decodeBody(t, rr)
	if out["port"].(float64) != 8080 || out["bind_address"] != "127.0.0.1" || out["enabled"].(bool) {
		t.Errorf("response body did not reflect update: %v", out)
	}

	// Tras update, desired=2 (1 inicial + 1 update) y applied sigue en 1.
	// La columna `desired_generation` debe reflejar el incremento.
	if out["desired_generation"].(float64) != 2 {
		t.Errorf("desired_generation = %v, want 2", out["desired_generation"])
	}
	if out["status"] != "pending" {
		t.Errorf("status = %v, want pending (applied=1 < desired=2)", out["status"])
	}
}

func TestPortsHTTP_UpdateNonExistentReturns404(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	body := `{"port": 8080, "bind_address": "0.0.0.0", "enabled": true}`
	rr := doReq(t, token, "PUT", "/api/v4/network/ports/http", body)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestPortsHTTP_UpdateInvalidJSON(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()
	seedPort(t, "http", 80, "0.0.0.0", true)

	rr := doReq(t, token, "PUT", "/api/v4/network/ports/http", "not json{")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPortsHTTP_UpdateValidationErrors(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()
	seedPort(t, "http", 80, "0.0.0.0", true)

	cases := []struct {
		name string
		body string
	}{
		{"missing fields", `{"port": 80}`},
		{"port out of range", `{"port": 70000, "bind_address": "0.0.0.0", "enabled": true}`},
		{"port zero", `{"port": 0, "bind_address": "0.0.0.0", "enabled": true}`},
		{"bind hostname", `{"port": 80, "bind_address": "example.com", "enabled": true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, token, "PUT", "/api/v4/network/ports/http", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPortsHTTP_UpdateEmitsAuditEvent(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()
	seedPort(t, "http", 80, "0.0.0.0", true)

	body := `{"port": 8080, "bind_address": "0.0.0.0", "enabled": true}`
	rr := doReq(t, token, "PUT", "/api/v4/network/ports/http", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}

	events, err := networkEventEmitter.ListEventsByCategory(context.Background(), CategoryPort, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Event != "config_updated" {
		t.Errorf("event = %q, want config_updated", events[0].Event)
	}
	if events[0].TargetID == nil || *events[0].TargetID != "http" {
		t.Errorf("target_id = %v, want http", events[0].TargetID)
	}
	if events[0].Level != string(EventLevelInfo) {
		t.Errorf("level = %q, want info", events[0].Level)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Method routing
// ═════════════════════════════════════════════════════════════════════════════

func TestPortsHTTP_MethodNotAllowed(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()
	seedPort(t, "http", 80, "0.0.0.0", true)

	cases := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v4/network/ports"},
		{"DELETE", "/api/v4/network/ports/http"},
		{"POST", "/api/v4/network/ports/http"},
	}
	for _, c := range cases {
		rr := doReq(t, token, c.method, c.path, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status=%d, want 405", c.method, c.path, rr.Code)
		}
	}
}

func TestPortsHTTP_UnknownPathReturns404(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	rr := doReq(t, token, "GET", "/api/v4/network/ports/something/weird", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Service unavailable cuando networkRepo es nil
// ═════════════════════════════════════════════════════════════════════════════

func TestPortsHTTP_ServiceUnavailableWhenRepoNil(t *testing.T) {
	token, _, cleanup := setupPortsHTTPTest(t)
	defer cleanup()

	// Forzar nil para simular "módulo no inicializado".
	prev := networkRepo
	networkRepo = nil
	defer func() { networkRepo = prev }()

	rr := doReq(t, token, "GET", "/api/v4/network/ports", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}

	// Reset body reader para próximo request
	_ = bytes.NewBuffer(nil)

	rr = doReq(t, token, "PUT", "/api/v4/network/ports/http",
		`{"port": 80, "bind_address": "0.0.0.0", "enabled": true}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT status = %d, want 503", rr.Code)
	}
}
