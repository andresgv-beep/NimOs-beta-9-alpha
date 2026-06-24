// network_router_http_test.go — Tests del endpoint /router.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

var routerHTTPTestMu sync.Mutex

func setupRouterHTTPTest(t *testing.T, provider RouterProvider) (token string, cleanup func()) {
	t.Helper()
	routerHTTPTestMu.Lock()

	prevDB := db
	prevProvider := networkRouterProvider

	c, dbCleanup := setupNetworkDB(t)
	db = c.db
	networkRouterProvider = provider

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
		networkRouterProvider = prevProvider
		db = prevDB
		dbCleanup()
		routerHTTPTestMu.Unlock()
	}
	return token, cleanup
}

func doRouterReq(t *testing.T, token, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handleNetworkRouterRoutes(rr, req)
	return rr
}

func decodeRouterBody(t *testing.T, rr *httptest.ResponseRecorder) RouterStatusResponse {
	t.Helper()
	var out RouterStatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v raw=%q", err, rr.Body.String())
	}
	return out
}

// ═════════════════════════════════════════════════════════════════════════════
// Auth & routing
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterHTTP_RequiresAuth(t *testing.T) {
	_, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{Available: true, Detected: true},
	})
	defer cleanup()

	rr := doRouterReq(t, "", "GET", "/api/v4/network/router")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 401/403", rr.Code)
	}
}

func TestRouterHTTP_MethodNotAllowed(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{Available: true, Detected: true},
	})
	defer cleanup()

	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rr := doRouterReq(t, token, m, "/api/v4/network/router")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status=%d, want 405", m, rr.Code)
		}
	}
}

func TestRouterHTTP_NotFoundForOtherPath(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router/extra")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rr.Code)
	}
}

func TestRouterHTTP_ServiceUnavailableWhenNotInit(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{})
	defer cleanup()

	prev := networkRouterProvider
	networkRouterProvider = nil
	defer func() { networkRouterProvider = prev }()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Detect outcomes
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterHTTP_BinaryMissing(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{Available: false},
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := decodeRouterBody(t, rr)
	if body.Available {
		t.Error("Available should be false")
	}
	if body.Message == "" {
		t.Error("expected actionable Message")
	}
	if body.Mappings == nil {
		t.Error("Mappings should be empty array, not nil")
	}
}

func TestRouterHTTP_NoRouter(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{Available: true, Detected: false},
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	body := decodeRouterBody(t, rr)

	if !body.Available || body.Detected {
		t.Errorf("Available=%v Detected=%v", body.Available, body.Detected)
	}
	if !strings.Contains(strings.ToLower(body.Message), "upnp") {
		t.Errorf("Message should mention UPnP: %q", body.Message)
	}
}

func TestRouterHTTP_RouterDetectedWithMappings(t *testing.T) {
	mappings := []RouterPortMapping{
		{Protocol: "TCP", ExternalPort: 8080, InternalIP: "192.168.1.50", InternalPort: 8080, Description: "NimOS-http"},
		{Protocol: "TCP", ExternalPort: 8443, InternalIP: "192.168.1.50", InternalPort: 8443, Description: "NimOS-https"},
	}
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{
			Available:  true,
			Detected:   true,
			LocalIP:    "192.168.1.50",
			ExternalIP: "1.2.3.4",
		},
		listMappings: mappings,
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := decodeRouterBody(t, rr)
	if !body.Detected {
		t.Error("Detected should be true")
	}
	if body.ExternalIP != "1.2.3.4" {
		t.Errorf("ExternalIP = %q", body.ExternalIP)
	}
	if len(body.Mappings) != 2 {
		t.Errorf("Mappings count = %d, want 2", len(body.Mappings))
	}
}

func TestRouterHTTP_DetectErrorReturns503(t *testing.T) {
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectErr: context.DeadlineExceeded,
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rr.Code)
	}
}

func TestRouterHTTP_ListMappingsErrorStillReturns200(t *testing.T) {
	// Si Detect funciona pero ListMappings falla, devolvemos lo que tengamos.
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{
			Available: true,
			Detected:  true,
			LocalIP:   "192.168.1.50",
		},
		listErr: ErrRouterTransient,
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := decodeRouterBody(t, rr)
	if body.Message == "" {
		t.Error("expected Message indicating mappings could not be listed")
	}
}

func TestRouterHTTP_MappingsIsEmptyArrayNotNull(t *testing.T) {
	// El frontend espera array, no null. Verificamos que [] aparece
	// en el JSON cuando no hay mappings.
	token, cleanup := setupRouterHTTPTest(t, &mockRouterProvider{
		detectStatus: &RouterStatus{Available: false},
	})
	defer cleanup()

	rr := doRouterReq(t, token, "GET", "/api/v4/network/router")
	raw := rr.Body.String()
	if !strings.Contains(raw, `"mappings":[]`) {
		t.Errorf("JSON should contain \"mappings\":[], got: %s", raw)
	}
}
