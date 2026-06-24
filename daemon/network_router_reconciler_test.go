// network_router_reconciler_test.go — Tests del RouterReconciler.

package main

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock RouterProvider
// ─────────────────────────────────────────────────────────────────────────────

type mockRouterProvider struct {
	mu sync.Mutex

	detectStatus  *RouterStatus
	detectErr     error
	listMappings  []RouterPortMapping
	listErr       error
	addErr        error
	removeErr     error

	addedCalls   []RouterPortMapping
	removedCalls []struct {
		Protocol     string
		ExternalPort int
	}
}

func (m *mockRouterProvider) Name() string { return "mock-router" }

func (m *mockRouterProvider) Detect(_ context.Context) (*RouterStatus, error) {
	return m.detectStatus, m.detectErr
}

func (m *mockRouterProvider) ListMappings(_ context.Context) ([]RouterPortMapping, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listMappings, nil
}

func (m *mockRouterProvider) AddMapping(_ context.Context, mp RouterPortMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedCalls = append(m.addedCalls, mp)
	return m.addErr
}

func (m *mockRouterProvider) RemoveMapping(_ context.Context, proto string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removedCalls = append(m.removedCalls, struct {
		Protocol     string
		ExternalPort int
	}{proto, port})
	return m.removeErr
}

func (m *mockRouterProvider) AddedCalls() []RouterPortMapping {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RouterPortMapping, len(m.addedCalls))
	copy(out, m.addedCalls)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup
// ─────────────────────────────────────────────────────────────────────────────

func newTestRouterReconciler(t *testing.T) (*RouterReconciler, *mockRouterProvider, *NetworkRepo, *EventEmitter, *FakeClock, *sqlConn, func()) {
	t.Helper()
	c, cleanup := setupNetworkDB(t)
	clock := NewFakeClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())
	provider := &mockRouterProvider{}

	rec, err := NewRouterReconciler(repo, emitter, clock, provider,
		RouterReconcilerConfig{
			Interval: 3600 * time.Second,
			LocalIP:  "192.168.1.50", // fijo para tests, sin detectLocalIP()
		})
	if err != nil {
		t.Fatal(err)
	}
	return rec, provider, repo, emitter, clock, c, cleanup
}

// seedPortForRouter inserta una entrada network_ports en DB. Nombre
// específico para no chocar con seedPort de network_ports_http_test.go.
func seedPortForRouter(t *testing.T, db *sql.DB, repo *NetworkRepo, id string, port int, enabled bool) {
	t.Helper()
	p := &NetworkPort{
		ID:          id,
		Port:        port,
		BindAddress: "0.0.0.0",
		Enabled:     enabled,
	}
	withNetTx(t, db, func(tx *sql.Tx) error {
		if err := repo.CreatePort(context.Background(), tx, p); err != nil {
			return err
		}
		return repo.MarkPortApplied(context.Background(), tx, p.ID)
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterReconciler_NewRequiresDeps(t *testing.T) {
	_, err := NewRouterReconciler(nil, nil, nil, nil, RouterReconcilerConfig{})
	if err == nil {
		t.Error("expected error with nil deps")
	}
}

func TestRouterReconciler_ImplementsReconciler(t *testing.T) {
	rec, _, _, _, _, _, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	if rec.Name() != "router_upnp" {
		t.Errorf("Name = %q", rec.Name())
	}
	if rec.Tier() != TierLow {
		t.Errorf("Tier = %v, want Low", rec.Tier())
	}
	if rec.Interval() != 3600*time.Second {
		t.Errorf("Interval = %v", rec.Interval())
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Router unavailable / down handling
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterReconciler_BinaryMissingEmitsWarn(t *testing.T) {
	rec, provider, _, emitter, _, _, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: false}

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	found := false
	for _, e := range events {
		if e.Event == "router_unavailable" && e.Level == string(EventLevelWarn) {
			found = true
		}
	}
	if !found {
		t.Error("expected router_unavailable warn event")
	}
}

func TestRouterReconciler_RouterNotRespondingEmitsWarn(t *testing.T) {
	rec, provider, _, emitter, _, _, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: false}

	rec.Reconcile(context.Background())

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	found := false
	for _, e := range events {
		if e.Event == "router_unavailable" {
			found = true
		}
	}
	if !found {
		t.Error("expected router_unavailable event")
	}
}

func TestRouterReconciler_UnavailableEmittedOnceUntilRecovery(t *testing.T) {
	rec, provider, _, emitter, _, _, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	// Pasada 1: router caído → emit
	provider.detectStatus = &RouterStatus{Available: false}
	rec.Reconcile(context.Background())

	// Pasada 2: sigue caído → NO debe emitir de nuevo
	rec.Reconcile(context.Background())

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	count := 0
	for _, e := range events {
		if e.Event == "router_unavailable" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("router_unavailable emitted %d times, want 1", count)
	}
}

func TestRouterReconciler_RecoveryEmitted(t *testing.T) {
	rec, provider, _, emitter, _, _, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	// Pasada 1: caído.
	provider.detectStatus = &RouterStatus{Available: false}
	rec.Reconcile(context.Background())

	// Pasada 2: vuelve.
	provider.detectStatus = &RouterStatus{
		Available:  true,
		Detected:   true,
		LocalIP:    "192.168.1.50",
		ExternalIP: "1.2.3.4",
	}
	rec.Reconcile(context.Background())

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 20)
	foundRecovery := false
	for _, e := range events {
		if e.Event == "router_recovered" {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Error("expected router_recovered event")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Happy path: mappings creados
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterReconciler_CreatesMissingMappings(t *testing.T) {
	rec, provider, repo, _, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{
		Available:  true,
		Detected:   true,
		LocalIP:    "192.168.1.50",
		ExternalIP: "1.2.3.4",
	}
	provider.listMappings = []RouterPortMapping{} // router vacío

	seedPortForRouter(t, c.db, repo, "http", 8080, true)
	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	added := provider.AddedCalls()
	if len(added) != 2 {
		t.Fatalf("expected 2 AddMapping calls, got %d", len(added))
	}

	ports := map[int]bool{8080: true, 8443: true}
	for _, m := range added {
		if !ports[m.ExternalPort] {
			t.Errorf("unexpected port %d", m.ExternalPort)
		}
		if m.InternalIP != "192.168.1.50" {
			t.Errorf("internal IP = %q", m.InternalIP)
		}
		if m.Protocol != "TCP" {
			t.Errorf("protocol = %q, want TCP", m.Protocol)
		}
		// Descripción debe llevar el prefijo NimOS-.
		if len(m.Description) < len(RouterMappingDescPrefix) ||
			m.Description[:len(RouterMappingDescPrefix)] != RouterMappingDescPrefix {
			t.Errorf("description %q missing prefix", m.Description)
		}
	}
}

func TestRouterReconciler_SkipsDisabledPorts(t *testing.T) {
	rec, provider, repo, _, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}

	seedPortForRouter(t, c.db, repo, "http", 8080, false) // disabled
	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	added := provider.AddedCalls()
	if len(added) != 1 {
		t.Fatalf("expected 1 AddMapping call (only enabled), got %d", len(added))
	}
	if added[0].ExternalPort != 8443 {
		t.Errorf("wrong port mapped: %d", added[0].ExternalPort)
	}
}

func TestRouterReconciler_NoActionWhenMappingExists(t *testing.T) {
	rec, provider, repo, _, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}
	provider.listMappings = []RouterPortMapping{
		{Protocol: "TCP", ExternalPort: 8443, InternalIP: "192.168.1.50", InternalPort: 8443, Description: "NimOS-https"},
	}

	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	added := provider.AddedCalls()
	if len(added) != 0 {
		t.Errorf("expected 0 calls (mapping already exists), got %d: %+v", len(added), added)
	}
}

func TestRouterReconciler_RespectsForeignMapping(t *testing.T) {
	rec, provider, repo, emitter, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}
	// Otro dispositivo tiene el puerto 8443 mapeado.
	provider.listMappings = []RouterPortMapping{
		{Protocol: "TCP", ExternalPort: 8443, InternalIP: "192.168.1.99", InternalPort: 8443, Description: "OtherDevice"},
	}

	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	added := provider.AddedCalls()
	if len(added) != 0 {
		t.Errorf("should not override foreign mapping; got %d AddMapping calls", len(added))
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	foundConflict := false
	for _, e := range events {
		if e.Event == "router_mapping_conflict" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Error("expected router_mapping_conflict event")
	}
}

func TestRouterReconciler_RefreshesStaleOwnMapping(t *testing.T) {
	// Mapping nuestro pero apuntando a IP vieja → re-mapear.
	rec, provider, repo, _, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}
	provider.listMappings = []RouterPortMapping{
		{Protocol: "TCP", ExternalPort: 8443, InternalIP: "192.168.1.99", InternalPort: 8443, Description: "NimOS-https"},
	}

	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	added := provider.AddedCalls()
	if len(added) != 1 {
		t.Errorf("expected 1 refresh AddMapping, got %d", len(added))
	}
	if added[0].InternalIP != "192.168.1.50" {
		t.Errorf("internal IP = %q, want 192.168.1.50", added[0].InternalIP)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Conflict handling on AddMapping
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterReconciler_HandlesAddConflict(t *testing.T) {
	rec, provider, repo, emitter, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}
	provider.addErr = ErrRouterConflict

	seedPortForRouter(t, c.db, repo, "https", 8443, true)

	rec.Reconcile(context.Background())

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	found := false
	for _, e := range events {
		if e.Event == "router_mapping_conflict" {
			found = true
		}
	}
	if !found {
		t.Error("expected router_mapping_conflict event")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Context cancellation
// ═════════════════════════════════════════════════════════════════════════════

func TestRouterReconciler_ContextCancelled(t *testing.T) {
	rec, provider, repo, _, _, c, cleanup := newTestRouterReconciler(t)
	defer cleanup()

	provider.detectStatus = &RouterStatus{Available: true, Detected: true, LocalIP: "192.168.1.50"}
	seedPortForRouter(t, c.db, repo, "http", 80, true)
	seedPortForRouter(t, c.db, repo, "https", 443, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rec.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
