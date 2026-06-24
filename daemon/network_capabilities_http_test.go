// network_capabilities_http_test.go — Tests de los handlers /capabilities.

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
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Setup
// ─────────────────────────────────────────────────────────────────────────────

var capsHTTPTestMu sync.Mutex

func setupCapsHTTPTest(t *testing.T, detect DetectFunc) (token string, c *sqlConn, cleanup func()) {
	t.Helper()
	capsHTTPTestMu.Lock()

	prevDB := db
	prevCaps := networkCapabilities
	prevEmitter := networkEventEmitter

	c, dbCleanup := setupNetworkDB(t)
	db = c.db

	clock := NewFakeClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	networkEventEmitter = NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	store, err := NewCapabilitiesStore(c.db, clock, detect)
	if err != nil {
		t.Fatal(err)
	}
	networkCapabilities = store

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
		networkEventEmitter = prevEmitter
		networkCapabilities = prevCaps
		db = prevDB
		dbCleanup()
		capsHTTPTestMu.Unlock()
	}
	return token, c, cleanup
}

func doCapsReq(t *testing.T, token, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handleNetworkCapabilitiesRoutes(rr, req)
	return rr
}

// fixedDetect devuelve siempre el mismo struct, contando llamadas.
func fixedDetect(counter *int64, caps SystemCapabilities) DetectFunc {
	return func() SystemCapabilities {
		atomic.AddInt64(counter, 1)
		caps.DetectedAt = time.Now()
		return caps
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /api/v4/network/capabilities
// ═════════════════════════════════════════════════════════════════════════════

func TestCapsHTTP_GetReturnsJSON(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{
		OpenSSLInstalled: true,
		DigInstalled:     true,
	}))
	defer cleanup()

	rr := doCapsReq(t, token, "GET", "/api/v4/network/capabilities")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var out SystemCapabilities
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OpenSSLInstalled {
		t.Error("OpenSSLInstalled should be true")
	}
	if !out.DigInstalled {
		t.Error("DigInstalled should be true")
	}
}

func TestCapsHTTP_GetRefreshesIfMissing(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	// Primera GET: no hay nada en DB → detect()
	doCapsReq(t, token, "GET", "/api/v4/network/capabilities")
	if atomic.LoadInt64(&calls) != 1 {
		t.Errorf("first GET should detect; calls=%d", calls)
	}

	// Segunda GET inmediata: usa cache.
	doCapsReq(t, token, "GET", "/api/v4/network/capabilities")
	if atomic.LoadInt64(&calls) != 1 {
		t.Errorf("second GET should NOT re-detect; calls=%d", calls)
	}
}

func TestCapsHTTP_GetRequiresAuth(t *testing.T) {
	var calls int64
	_, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	rr := doCapsReq(t, "", "GET", "/api/v4/network/capabilities")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 401/403", rr.Code)
	}
}

func TestCapsHTTP_GetServiceUnavailableWhenNotInit(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	prev := networkCapabilities
	networkCapabilities = nil
	defer func() { networkCapabilities = prev }()

	rr := doCapsReq(t, token, "GET", "/api/v4/network/capabilities")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rr.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// POST /api/v4/network/capabilities/refresh
// ═════════════════════════════════════════════════════════════════════════════

func TestCapsHTTP_PostForcesRefresh(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{
		CertbotInstalled: true,
	}))
	defer cleanup()

	// GET inicial → detect 1 vez.
	doCapsReq(t, token, "GET", "/api/v4/network/capabilities")
	if atomic.LoadInt64(&calls) != 1 {
		t.Fatalf("initial calls=%d", calls)
	}

	// POST refresh → detect otra vez.
	rr := doCapsReq(t, token, "POST", "/api/v4/network/capabilities/refresh")
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh status=%d", rr.Code)
	}
	if atomic.LoadInt64(&calls) != 2 {
		t.Errorf("refresh should redetect; calls=%d", calls)
	}
}

func TestCapsHTTP_PostRefreshEmitsAudit(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	doCapsReq(t, token, "POST", "/api/v4/network/capabilities/refresh")

	events, _ := networkEventEmitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	found := false
	for _, e := range events {
		if e.Event == "capabilities_refreshed" {
			found = true
		}
	}
	if !found {
		t.Error("expected capabilities_refreshed event")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Method routing
// ═════════════════════════════════════════════════════════════════════════════

func TestCapsHTTP_MethodNotAllowed(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	cases := []struct {
		method, path string
	}{
		{"POST", "/api/v4/network/capabilities"},
		{"PUT", "/api/v4/network/capabilities"},
		{"DELETE", "/api/v4/network/capabilities"},
		{"GET", "/api/v4/network/capabilities/refresh"},
		{"PUT", "/api/v4/network/capabilities/refresh"},
	}
	for _, c := range cases {
		rr := doCapsReq(t, token, c.method, c.path)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status=%d, want 405", c.method, c.path, rr.Code)
		}
	}
}

func TestCapsHTTP_UnknownPathReturns404(t *testing.T) {
	var calls int64
	token, _, cleanup := setupCapsHTTPTest(t, fixedDetect(&calls, SystemCapabilities{}))
	defer cleanup()

	rr := doCapsReq(t, token, "GET", "/api/v4/network/capabilities/something")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rr.Code)
	}
}

// Compilation guard: io.Discard usage to silence unused import warning
// if the test ever stops using strings.NewReader.
var _ = io.Discard
