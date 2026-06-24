// network_ddns_http_test.go — Tests de los handlers HTTP de DDNS.
//
// Estrategia: igual que network_ports_http_test.go.
//   - setupDdnsHTTPTest serializa globals y crea sessions table fake.
//   - Inyecta networkRepo + networkEventEmitter + networkSecretsStore.
//   - Helpers doDdnsReq / decodeDdnsBody.
//
// Cubre:
//   - GET list / detail con auth.
//   - POST create happy path + validaciones (token/domain/provider/interval).
//   - PUT update y rotación de token.
//   - DELETE con y sin delete_secret.
//   - 401/403/404/409 según corresponda.
//   - Token NUNCA aparece en respuestas.

package main

import (
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

// ddnsHTTPTestMu serializa los tests que mutan singletons globales.
var ddnsHTTPTestMu sync.Mutex

func setupDdnsHTTPTest(t *testing.T) (token string, c *sqlConn, cleanup func()) {
	t.Helper()
	ddnsHTTPTestMu.Lock()

	prevDB := db
	prevRepo := networkRepo
	prevEmitter := networkEventEmitter
	prevSecrets := networkSecretsStore

	c, dbCleanup := setupNetworkDB(t)
	db = c.db

	clock := NewFakeClock(time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC))
	networkRepo = NewNetworkRepo(c.db, clock)
	networkEventEmitter = NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	// Master key efímera para no tocar disco.
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	secrets, err := NewSecretsStoreWithKey(c.db, key, clock)
	if err != nil {
		t.Fatal(err)
	}
	networkSecretsStore = secrets

	// Tabla de sesiones real (matchea schema de db.go).
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

	// Sembrar admin.
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	token = hex.EncodeToString(tokenBytes)
	hashed := sha256Hex(token)
	c.db.Exec(`INSERT INTO sessions (token, username, role, created_at, expires_at, ip)
		VALUES (?, 'test-admin', 'admin', ?, ?, '127.0.0.1')`,
		hashed, time.Now().UnixMilli(), time.Now().Add(time.Hour).UnixMilli())

	cleanup = func() {
		networkSecretsStore = prevSecrets
		networkEventEmitter = prevEmitter
		networkRepo = prevRepo
		db = prevDB
		dbCleanup()
		ddnsHTTPTestMu.Unlock()
	}
	return token, c, cleanup
}

func doDdnsReq(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
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
	handleNetworkDdnsRoutes(rr, req)
	return rr
}

func decodeDdnsBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v (raw=%q)", err, rr.Body.String())
	}
	return out
}

// ═════════════════════════════════════════════════════════════════════════════
// Validation helpers
// ═════════════════════════════════════════════════════════════════════════════

func TestValidateDomain_Cases(t *testing.T) {
	cases := []struct {
		domain  string
		wantErr bool
	}{
		{"example.com", false},
		{"my-host.duckdns.org", false},
		{"sub.dom.example.net", false},
		{"", true},
		{"   ", true},
		{"a", true},                  // too short - failed by regex
		{"has space.com", true},
		{"-leadinghyphen.com", true},
		{"trailinghyphen-.com", true},
		{"has/slash.com", true},
		{strings.Repeat("a", 300) + ".com", true}, // too long
	}
	for _, c := range cases {
		t.Run(c.domain, func(t *testing.T) {
			err := validateDomain(c.domain)
			if (err != nil) != c.wantErr {
				t.Errorf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestValidateUpdateInterval_Cases(t *testing.T) {
	cases := []struct {
		secs    int
		wantErr bool
	}{
		{60, false},
		{900, false},
		{86400, false},
		{59, true},
		{0, true},
		{86401, true},
	}
	for _, c := range cases {
		err := validateUpdateInterval(c.secs)
		if (err != nil) != c.wantErr {
			t.Errorf("secs=%d: err=%v wantErr=%v", c.secs, err, c.wantErr)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /api/v4/network/ddns
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_ListEmpty(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rr := doDdnsReq(t, token, "GET", "/api/v4/network/ddns", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := decodeDdnsBody(t, rr)
	if list, ok := body["ddns"].([]interface{}); !ok || len(list) != 0 {
		t.Errorf("expected empty list, got %v", body["ddns"])
	}
}

func TestDdnsHTTP_ListRequiresAuth(t *testing.T) {
	_, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rr := doDdnsReq(t, "", "GET", "/api/v4/network/ddns", "")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 401/403", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// POST /api/v4/network/ddns
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_CreateHappyPath(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	body := `{
		"provider": "duckdns",
		"domain": "test.duckdns.org",
		"token": "secret-tok-x",
		"enabled": true,
		"auto_update": true,
		"update_interval": 900
	}`
	rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	out := decodeDdnsBody(t, rr)

	// Token NUNCA en respuesta.
	for k, v := range out {
		if k == "token" {
			t.Errorf("response leaks 'token' key")
		}
		if s, ok := v.(string); ok && strings.Contains(s, "secret-tok-x") {
			t.Errorf("response leaks token in field %s", k)
		}
	}
	// has_token=true.
	if out["has_token"] != true {
		t.Error("has_token should be true")
	}
	if out["status"] != "pending" {
		t.Errorf("status=%v, want pending", out["status"])
	}
}

func TestDdnsHTTP_CreateDefaultsApplied(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	// Mínimo posible: solo provider+domain+token.
	body := `{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`
	rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	out := decodeDdnsBody(t, rr)
	if out["enabled"] != true {
		t.Errorf("default enabled should be true, got %v", out["enabled"])
	}
	if out["auto_update"] != true {
		t.Errorf("default auto_update should be true, got %v", out["auto_update"])
	}
	if out["update_interval"].(float64) != 900 {
		t.Errorf("default update_interval = %v, want 900", out["update_interval"])
	}
}

func TestDdnsHTTP_CreateValidationErrors(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	cases := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"no domain", `{"provider": "duckdns", "token": "t"}`},
		{"no token", `{"provider": "duckdns", "domain": "test.duckdns.org"}`},
		{"no provider", `{"domain": "test.duckdns.org", "token": "t"}`},
		{"invalid domain", `{"provider": "duckdns", "domain": "has space", "token": "t"}`},
		{"interval too low", `{"provider": "duckdns", "domain": "test.duckdns.org", "token": "t", "update_interval": 30}`},
		{"invalid JSON", `not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", c.body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestDdnsHTTP_CreateInvalidProviderReturns400(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	body := `{"provider": "notexist", "domain": "test.example.com", "token": "x"}`
	rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDdnsHTTP_CreateDuplicateDomainConflict(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	body := `{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`
	rr1 := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", body)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first POST status=%d", rr1.Code)
	}
	rr2 := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", body)
	if rr2.Code != http.StatusConflict {
		t.Errorf("second POST status=%d, want 409", rr2.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /api/v4/network/ddns/:id
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_GetReturnsView(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	// Crear primero.
	createBody := `{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`
	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns", createBody)
	created := decodeDdnsBody(t, rrCreate)
	id := created["id"].(string)

	rr := doDdnsReq(t, token, "GET", "/api/v4/network/ddns/"+id, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	out := decodeDdnsBody(t, rr)
	if out["id"] != id || out["domain"] != "test.duckdns.org" {
		t.Errorf("body=%v", out)
	}
}

func TestDdnsHTTP_GetNotFound(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rr := doDdnsReq(t, token, "GET", "/api/v4/network/ddns/does-not-exist", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// PUT /api/v4/network/ddns/:id
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_UpdateConfig(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	updateBody := `{"enabled": false, "auto_update": false, "update_interval": 1800}`
	rr := doDdnsReq(t, token, "PUT", "/api/v4/network/ddns/"+id, updateBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	out := decodeDdnsBody(t, rr)
	if out["enabled"] != false {
		t.Errorf("enabled = %v, want false", out["enabled"])
	}
	if out["auto_update"] != false {
		t.Errorf("auto_update = %v, want false", out["auto_update"])
	}
	if out["update_interval"].(float64) != 1800 {
		t.Errorf("update_interval = %v, want 1800", out["update_interval"])
	}
	// desired debe haber subido a 2.
	if out["desired_generation"].(float64) != 2 {
		t.Errorf("desired_generation = %v, want 2", out["desired_generation"])
	}
}

func TestDdnsHTTP_UpdateValidationErrors(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	cases := []string{
		`{"enabled": true}`, // missing fields
		`{"enabled": true, "auto_update": true, "update_interval": 30}`, // interval too low
	}
	for _, body := range cases {
		rr := doDdnsReq(t, token, "PUT", "/api/v4/network/ddns/"+id, body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body=%s status=%d, want 400", body, rr.Code)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// DELETE /api/v4/network/ddns/:id
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_Delete(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	rr := doDdnsReq(t, token, "DELETE", "/api/v4/network/ddns/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rr.Code)
	}

	rrGet := doDdnsReq(t, token, "GET", "/api/v4/network/ddns/"+id, "")
	if rrGet.Code != http.StatusNotFound {
		t.Errorf("after delete: status=%d, want 404", rrGet.Code)
	}
}

func TestDdnsHTTP_DeleteWithSecret(t *testing.T) {
	token, c, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	created := decodeDdnsBody(t, rrCreate)
	id := created["id"].(string)

	// El secret_id no se expone, pero lo podemos leer de la DB para verificación.
	var secretID string
	c.db.QueryRow(`SELECT token_secret_id FROM network_ddns WHERE id = ?`, id).Scan(&secretID)
	if secretID == "" {
		t.Fatal("could not find secret_id")
	}

	rr := doDdnsReq(t, token, "DELETE", "/api/v4/network/ddns/"+id+"?delete_secret=true", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rr.Code)
	}

	// El secret debe haber desaparecido.
	var cnt int
	c.db.QueryRow(`SELECT COUNT(*) FROM nimos_secrets WHERE id = ?`, secretID).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("delete_secret=true did not remove the secret; cnt=%d", cnt)
	}
}

func TestDdnsHTTP_DeletePreservesSecretByDefault(t *testing.T) {
	token, c, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	var secretID string
	c.db.QueryRow(`SELECT token_secret_id FROM network_ddns WHERE id = ?`, id).Scan(&secretID)

	rr := doDdnsReq(t, token, "DELETE", "/api/v4/network/ddns/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rr.Code)
	}

	// CASCADE en network_ddns→nimos_secrets: el FK ON DELETE CASCADE
	// borra el ddns cuando se borra el secret, pero NO al revés. Verifico
	// que el secret SIGUE existiendo.
	var cnt int
	c.db.QueryRow(`SELECT COUNT(*) FROM nimos_secrets WHERE id = ?`, secretID).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("default delete should preserve secret; cnt=%d", cnt)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// POST /api/v4/network/ddns/:id/token (rotación)
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_RotateToken(t *testing.T) {
	token, c, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "original-token"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	var secretID string
	c.db.QueryRow(`SELECT token_secret_id FROM network_ddns WHERE id = ?`, id).Scan(&secretID)

	rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns/"+id+"/token",
		`{"token": "new-rotated-token"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Verificar que el secret se actualizó (descifrando).
	s, err := networkSecretsStore.GetSecret(SecretID(secretID))
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Plaintext) != "new-rotated-token" {
		t.Errorf("secret plaintext = %q, want new-rotated-token", string(s.Plaintext))
	}
}

func TestDdnsHTTP_RotateTokenValidationErrors(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rrCreate := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	id := decodeDdnsBody(t, rrCreate)["id"].(string)

	for _, body := range []string{`{}`, `{"token": ""}`, `not json`} {
		rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns/"+id+"/token", body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body=%s status=%d, want 400", body, rr.Code)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Method routing
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_MethodNotAllowed(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	cases := []struct {
		method, path string
	}{
		{"PATCH", "/api/v4/network/ddns"},
		{"PUT", "/api/v4/network/ddns"},
		{"DELETE", "/api/v4/network/ddns"},
		{"GET", "/api/v4/network/ddns/any/token"},
		{"DELETE", "/api/v4/network/ddns/any/token"},
	}
	for _, c := range cases {
		rr := doDdnsReq(t, token, c.method, c.path, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status=%d, want 405", c.method, c.path, rr.Code)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Audit
// ═════════════════════════════════════════════════════════════════════════════

func TestDdnsHTTP_CreateEmitsAuditEvent(t *testing.T) {
	token, _, cleanup := setupDdnsHTTPTest(t)
	defer cleanup()

	rr := doDdnsReq(t, token, "POST", "/api/v4/network/ddns",
		`{"provider": "duckdns", "domain": "test.duckdns.org", "token": "x"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d", rr.Code)
	}

	events, _ := networkEventEmitter.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	found := false
	for _, e := range events {
		if e.Event == "created" && e.Level == string(EventLevelInfo) {
			found = true
		}
	}
	if !found {
		t.Error("expected 'created' event of level info")
	}
}
