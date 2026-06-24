// network_exposure_http_test.go — Tests de /api/v4/network/exposure.

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var exposureHTTPTestMu sync.Mutex

func setupExposureHTTPTest(t *testing.T) (token string, cleanup func()) {
	t.Helper()
	exposureHTTPTestMu.Lock()

	prevDB := db
	prevRepo := networkRepo
	prevObs := networkExposureObserver

	c, dbCleanup := setupNetworkDB(t)
	db = c.db
	networkRepo = NewNetworkRepo(c.db, NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)))
	networkExposureObserver = nil

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
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	token = hex.EncodeToString(tokenBytes)
	hashed := sha256Hex(token)
	c.db.Exec(`INSERT INTO sessions (token, username, role, created_at, expires_at, ip)
		VALUES (?, 'test-admin', 'admin', ?, ?, '127.0.0.1')`,
		hashed, time.Now().UnixMilli(), time.Now().Add(time.Hour).UnixMilli())

	cleanup = func() {
		networkExposureObserver = prevObs
		networkRepo = prevRepo
		db = prevDB
		dbCleanup()
		exposureHTTPTestMu.Unlock()
	}
	return token, cleanup
}

// doExposureReqIfMatch — como doExposureReq pero con header If-Match
// (candado optimista CRIT-1 para mutaciones).
func doExposureReqIfMatch(t *testing.T, token, method, path, body string, gen int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("If-Match", strconv.FormatInt(gen, 10))
	rr := httptest.NewRecorder()
	handleNetworkExposureRoutes(rr, req)
	return rr
}

func doExposureReq(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleNetworkExposureRoutes(rr, req)
	return rr
}

func TestExposureHTTP_RequiresAuth(t *testing.T) {
	_, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	rr := doExposureReq(t, "", "GET", "/api/v4/network/exposure", "")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 401/403", rr.Code)
	}
}

func TestExposureHTTP_ConfigGetDefault(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	rr := doExposureReq(t, token, "GET", "/api/v4/network/exposure/config", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Config NetworkExposureConfig `json:"config"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Config.Enabled {
		t.Error("default config should be disabled")
	}
}

func TestExposureHTTP_ConfigPutAndGet(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"base_domain":"nimosbarraca1.duckdns.org","enabled":true}`
	rr := doExposureReq(t, token, "PUT", "/api/v4/network/exposure/config", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/config", "")
	var resp struct {
		Config NetworkExposureConfig `json:"config"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Config.BaseDomain != "nimosbarraca1.duckdns.org" || !resp.Config.Enabled {
		t.Errorf("config not persisted: %+v", resp.Config)
	}
}

func TestExposureHTTP_ConfigEnableWithoutDomainRejected(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"enabled":true}`
	rr := doExposureReq(t, token, "PUT", "/api/v4/network/exposure/config", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (enable without domain)", rr.Code)
	}
}

func TestExposureHTTP_CreateAndList(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"app_id":"immich","display_name":"Immich","subdomain":"immich","upstream_host":"127.0.0.1","upstream_port":2283}`
	rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d", rr.Code)
	}
	var resp struct {
		Apps []*NetworkExposedApp `json:"apps"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Apps) != 1 || resp.Apps[0].AppID != "immich" {
		t.Errorf("list mismatch: %+v", resp.Apps)
	}
}

func TestExposureHTTP_CreateValidations(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	cases := []struct {
		name string
		body string
	}{
		{"no app_id", `{"subdomain":"x","upstream_host":"127.0.0.1","upstream_port":80}`},
		{"no route", `{"app_id":"x","upstream_host":"127.0.0.1","upstream_port":80}`},
		{"no host", `{"app_id":"x","subdomain":"x","upstream_port":80}`},
		{"bad port", `{"app_id":"x","subdomain":"x","upstream_host":"127.0.0.1","upstream_port":0}`},
	}
	for _, tc := range cases {
		rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", tc.body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", tc.name, rr.Code)
		}
	}
}

func TestExposureHTTP_DuplicateConflict(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"app_id":"immich","subdomain":"immich","upstream_host":"127.0.0.1","upstream_port":2283}`
	doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	if rr.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409 (duplicate)", rr.Code)
	}
}

func TestExposureHTTP_GetUpdateDelete(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	// Create.
	body := `{"app_id":"immich","subdomain":"immich","upstream_host":"127.0.0.1","upstream_port":2283}`
	rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	var created struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&created)
	id := created.App.ID

	// Get.
	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/"+id, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d", rr.Code)
	}

	// Update (cambiar puerto + disable) — con If-Match de la generación leída.
	upd := `{"upstream_port":2284,"enabled":false}`
	rr = doExposureReqIfMatch(t, token, "PUT", "/api/v4/network/exposure/"+id, upd,
		created.App.Convergence.Desired)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/"+id, "")
	var got struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&got)
	if got.App.UpstreamPort != 2284 || got.App.Enabled {
		t.Errorf("update not applied: %+v", got.App)
	}

	// Delete — con la generación FRESCA (el update la subió).
	rr = doExposureReqIfMatch(t, token, "DELETE", "/api/v4/network/exposure/"+id, "",
		got.App.Convergence.Desired)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/"+id, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("after delete GET status=%d, want 404", rr.Code)
	}
}

func TestExposureHTTP_GetNotFound(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	rr := doExposureReq(t, token, "GET", "/api/v4/network/exposure/no-existe", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rr.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CRIT-1 · Contrato If-Match en HTTP
// ─────────────────────────────────────────────────────────────────────────────

func TestExposureHTTP_MutationWithoutIfMatchIs428(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"app_id":"immich","subdomain":"immich","upstream_host":"127.0.0.1","upstream_port":2283}`
	rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	var created struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&created)
	id := created.App.ID

	// PUT sin If-Match → 428 Precondition Required.
	rr = doExposureReq(t, token, "PUT", "/api/v4/network/exposure/"+id, `{"upstream_port":9999}`)
	if rr.Code != http.StatusPreconditionRequired {
		t.Errorf("PUT without If-Match status=%d, want 428", rr.Code)
	}
	// DELETE sin If-Match → 428.
	rr = doExposureReq(t, token, "DELETE", "/api/v4/network/exposure/"+id, "")
	if rr.Code != http.StatusPreconditionRequired {
		t.Errorf("DELETE without If-Match status=%d, want 428", rr.Code)
	}
	// Y la app sigue intacta.
	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/"+id, "")
	if rr.Code != http.StatusOK {
		t.Errorf("app should be untouched, GET=%d", rr.Code)
	}
}

func TestExposureHTTP_StaleIfMatchIs412WithCurrentState(t *testing.T) {
	token, cleanup := setupExposureHTTPTest(t)
	defer cleanup()

	body := `{"app_id":"immich","subdomain":"immich","upstream_host":"127.0.0.1","upstream_port":2283}`
	rr := doExposureReq(t, token, "POST", "/api/v4/network/exposure", body)
	var created struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&created)
	id := created.App.ID
	gen := created.App.Convergence.Desired

	// Cliente A actualiza con la generación correcta → 200, y el body trae
	// el token fresco (gen+1) para no necesitar otra petición.
	rr = doExposureReqIfMatch(t, token, "PUT", "/api/v4/network/exposure/"+id,
		`{"display_name":"A"}`, gen)
	if rr.Code != http.StatusOK {
		t.Fatalf("fresh PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	var afterA struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&afterA)
	if afterA.App.Convergence.Desired != gen+1 {
		t.Errorf("response generation = %d, want %d (fresh token)", afterA.App.Convergence.Desired, gen+1)
	}

	// Cliente B llega con la generación VIEJA → 412 + estado actual en body.
	rr = doExposureReqIfMatch(t, token, "PUT", "/api/v4/network/exposure/"+id,
		`{"display_name":"B"}`, gen)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale PUT status=%d, want 412", rr.Code)
	}
	var conflict struct {
		App *NetworkExposedApp `json:"app"`
	}
	json.NewDecoder(rr.Body).Decode(&conflict)
	if conflict.App == nil || conflict.App.DisplayName != "A" {
		t.Errorf("412 body should carry current state (A): %+v", conflict.App)
	}

	// DELETE con generación vieja → 412 y la app sobrevive.
	rr = doExposureReqIfMatch(t, token, "DELETE", "/api/v4/network/exposure/"+id, "", gen)
	if rr.Code != http.StatusPreconditionFailed {
		t.Errorf("stale DELETE status=%d, want 412", rr.Code)
	}
	rr = doExposureReq(t, token, "GET", "/api/v4/network/exposure/"+id, "")
	if rr.Code != http.StatusOK {
		t.Errorf("app must survive a conflicted delete, GET=%d", rr.Code)
	}
}
