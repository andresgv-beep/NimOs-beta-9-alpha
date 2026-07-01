// network_ddns_reconciler_test.go — Tests del DDNSReconciler.
//
// Estrategia:
//   - mockDDNSProvider que registra llamadas y devuelve lo que se le diga.
//   - DB real con schemas aplicados (no estamos testeando el repo aquí
//     pero el reconciler hace muchos updates en una pasada — DB real es
//     más fiable que un mock del repo).
//   - SecretsStore real con su master key efímera.
//   - FakeClock para controlar tiempo de needsUpdate.

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock provider
// ─────────────────────────────────────────────────────────────────────────────

type mockDDNSProvider struct {
	name string

	mu      sync.Mutex
	calls   []mockDDNSCall
	result  *DDNSUpdateResult
	callErr error
}

type mockDDNSCall struct {
	Domain string
	Secret string
}

func (m *mockDDNSProvider) Name() string { return m.name }

func (m *mockDDNSProvider) Update(_ context.Context, domain, secret string) (*DDNSUpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockDDNSCall{Domain: domain, Secret: secret})
	return m.result, m.callErr
}

func (m *mockDDNSProvider) Calls() []mockDDNSCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockDDNSCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup
// ─────────────────────────────────────────────────────────────────────────────

func newTestDDNSReconciler(t *testing.T) (*DDNSReconciler, *mockDDNSProvider, *NetworkRepo, *SecretsStore, *EventEmitter, *FakeClock, *sqlConn, func()) {
	t.Helper()

	c, cleanup := setupNetworkDB(t)
	clock := NewFakeClock(time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC))
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	// SecretsStore con master key efímera (igual que en otros tests).
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretsStoreWithKey(c.db, key, clock)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := NewDDNSReconciler(repo, store, emitter, clock, DDNSReconcilerConfig{
		Interval: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	provider := &mockDDNSProvider{name: "duckdns"}
	rec.RegisterProvider(provider)

	return rec, provider, repo, store, emitter, clock, c, cleanup
}

// seedDDNS crea una entrada DDNS con secret asociado y la marca como aplicada.
// Devuelve la entrada para uso en el test.
func seedDDNS(t *testing.T, db *sql.DB, repo *NetworkRepo, store *SecretsStore, domain string, enabled, autoUpdate bool, interval int) *NetworkDdns {
	t.Helper()
	secretID, err := store.CreateSecret("ddns_token", "tok-"+domain, []byte("plaintext-token-"+domain))
	if err != nil {
		t.Fatal(err)
	}

	d := &NetworkDdns{
		Provider:       "duckdns",
		Domain:         domain,
		TokenSecretID:  string(secretID),
		Enabled:        enabled,
		AutoUpdate:     autoUpdate,
		UpdateInterval: interval,
	}
	withNetTx(t, db, func(tx *sql.Tx) error {
		if err := repo.CreateDdns(context.Background(), tx, d); err != nil {
			return err
		}
		return repo.MarkDdnsApplied(context.Background(), tx, d.ID)
	})
	return d
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestDDNSReconciler_NewRequiresDeps(t *testing.T) {
	_, err := NewDDNSReconciler(nil, nil, nil, nil, DDNSReconcilerConfig{})
	if err == nil {
		t.Error("expected error with nil deps")
	}
}

func TestDDNSReconciler_ImplementsReconciler(t *testing.T) {
	rec, _, _, _, _, _, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	if rec.Name() != "ddns_updater" {
		t.Errorf("Name = %q", rec.Name())
	}
	if rec.Tier() != TierMedium {
		t.Errorf("Tier = %v", rec.Tier())
	}
	if rec.Interval() != 60*time.Second {
		t.Errorf("Interval = %v", rec.Interval())
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// needsUpdate decision
// ═════════════════════════════════════════════════════════════════════════════

func TestDDNSReconciler_NeedsUpdate_Pending(t *testing.T) {
	rec, _, _, _, _, _, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	d := &NetworkDdns{
		AutoUpdate:  false,
		Convergence: Convergence{Desired: 2, Applied: 1},
	}
	if !rec.needsUpdate(d) {
		t.Error("pending should always trigger update")
	}
}

func TestDDNSReconciler_NeedsUpdate_OneShotAppliedSkips(t *testing.T) {
	rec, _, _, _, _, _, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	d := &NetworkDdns{
		AutoUpdate:  false,
		Convergence: Convergence{Desired: 1, Applied: 1},
	}
	if rec.needsUpdate(d) {
		t.Error("converged one-shot should NOT trigger")
	}
}

func TestDDNSReconciler_NeedsUpdate_AutoFirstRun(t *testing.T) {
	rec, _, _, _, _, _, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	d := &NetworkDdns{
		AutoUpdate:  true,
		LastRunAt:   nil,
		Convergence: Convergence{Desired: 1, Applied: 1},
	}
	if !rec.needsUpdate(d) {
		t.Error("auto + LastRunAt nil should trigger (first run)")
	}
}

func TestDDNSReconciler_NeedsUpdate_AutoIntervalElapsed(t *testing.T) {
	rec, _, _, _, _, clock, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	pastTime := clock.Now().Add(-20 * time.Minute)
	d := &NetworkDdns{
		AutoUpdate:     true,
		UpdateInterval: 900, // 15min
		LastRunAt:      &pastTime,
		Convergence:    Convergence{Desired: 1, Applied: 1},
	}
	if !rec.needsUpdate(d) {
		t.Error("interval elapsed should trigger")
	}
}

func TestDDNSReconciler_NeedsUpdate_AutoIntervalNotElapsed(t *testing.T) {
	rec, _, _, _, _, clock, _, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	recentTime := clock.Now().Add(-1 * time.Minute)
	d := &NetworkDdns{
		AutoUpdate:     true,
		UpdateInterval: 900,
		LastRunAt:      &recentTime,
		Convergence:    Convergence{Desired: 1, Applied: 1},
	}
	if rec.needsUpdate(d) {
		t.Error("interval not elapsed should NOT trigger")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Reconcile flow
// ═════════════════════════════════════════════════════════════════════════════

func TestDDNSReconciler_SkipsDisabledEntries(t *testing.T) {
	rec, provider, repo, store, _, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	seedDDNS(t, c.db, repo, store, "disabled.duckdns.org", false, true, 900)

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.Calls()) != 0 {
		t.Errorf("disabled entry should not be processed; calls=%d", len(provider.Calls()))
	}
}

func TestDDNSReconciler_HappyPath(t *testing.T) {
	rec, provider, repo, store, emitter, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	d := seedDDNS(t, c.db, repo, store, "test.duckdns.org", true, true, 900)
	provider.result = &DDNSUpdateResult{RawResponse: "OK"}

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	calls := provider.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Domain != "test.duckdns.org" {
		t.Errorf("domain = %q", calls[0].Domain)
	}
	if calls[0].Secret != "plaintext-token-test.duckdns.org" {
		t.Errorf("secret not passed correctly: %q", calls[0].Secret)
	}

	// Verificar persistencia: last_run_result=success
	updated, _ := repo.GetDdns(context.Background(), d.ID)
	if updated.LastRunResult == nil || *updated.LastRunResult != "success" {
		t.Errorf("LastRunResult = %v", updated.LastRunResult)
	}
	if updated.LastRunAt == nil {
		t.Error("LastRunAt should be set")
	}

	// Verificar evento (primer run → info)
	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	foundFirstRun := false
	for _, e := range events {
		if e.Event == "first_update_succeeded" {
			foundFirstRun = true
			if e.Level != string(EventLevelInfo) {
				t.Errorf("first run event level = %q, want info", e.Level)
			}
		}
	}
	if !foundFirstRun {
		t.Error("expected first_update_succeeded event")
	}

	// Verificar operation auditable
	ops, _ := repo.ListOperationsByTriggeredBy(context.Background(), "reconciler:ddns_updater", 10)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	if ops[0].Status != "completed" {
		t.Errorf("op status = %q, want completed", ops[0].Status)
	}
	if ops[0].CompletedAt == nil {
		t.Error("op completed_at should be set")
	}
}

func TestDDNSReconciler_SecondRunIsDebugNotInfo(t *testing.T) {
	rec, provider, repo, store, emitter, clock, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	seedDDNS(t, c.db, repo, store, "test.duckdns.org", true, true, 900)
	provider.result = &DDNSUpdateResult{RawResponse: "OK"}

	// Primera pasada → first_update_succeeded (info)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Avanzar tiempo > 15min para que toque otra vez.
	clock.Advance(20 * time.Minute)

	// Segunda pasada → update_succeeded (debug)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryDdns, 20)
	var debugCount, infoCount int
	for _, e := range events {
		if e.Event == "update_succeeded" && e.Level == string(EventLevelDebug) {
			debugCount++
		}
		if e.Event == "first_update_succeeded" && e.Level == string(EventLevelInfo) {
			infoCount++
		}
	}
	if infoCount != 1 {
		t.Errorf("first run info events = %d, want 1", infoCount)
	}
	if debugCount != 1 {
		t.Errorf("subsequent run debug events = %d, want 1", debugCount)
	}
}

func TestDDNSReconciler_AuthFailed(t *testing.T) {
	rec, provider, repo, store, emitter, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	d := seedDDNS(t, c.db, repo, store, "test.duckdns.org", true, true, 900)
	provider.callErr = ErrDDNSAuthFailed

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	updated, _ := repo.GetDdns(context.Background(), d.ID)
	if updated.LastRunResult == nil || *updated.LastRunResult != "failed" {
		t.Errorf("LastRunResult = %v", updated.LastRunResult)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	foundAuth := false
	for _, e := range events {
		if e.Event == "auth_failed" {
			foundAuth = true
			if e.Level != string(EventLevelError) {
				t.Errorf("auth_failed event level = %q, want error", e.Level)
			}
		}
	}
	if !foundAuth {
		t.Error("expected auth_failed event")
	}

	ops, _ := repo.ListOperationsByTriggeredBy(context.Background(), "reconciler:ddns_updater", 10)
	if len(ops) != 1 || ops[0].Status != "failed" {
		t.Errorf("op status = %q, want failed", ops[0].Status)
	}
	if ops[0].ErrorCode == nil || *ops[0].ErrorCode != "AUTH" {
		t.Errorf("op error_code = %v", ops[0].ErrorCode)
	}
}

func TestDDNSReconciler_TransientFailure(t *testing.T) {
	rec, provider, repo, store, emitter, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	seedDDNS(t, c.db, repo, store, "test.duckdns.org", true, true, 900)
	provider.callErr = ErrDDNSTransient

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	foundTransient := false
	for _, e := range events {
		if e.Event == "transient_failure" {
			foundTransient = true
			if e.Level != string(EventLevelWarn) {
				t.Errorf("level = %q, want warn", e.Level)
			}
		}
	}
	if !foundTransient {
		t.Error("expected transient_failure event")
	}
}

func TestDDNSReconciler_UnknownProvider(t *testing.T) {
	// Crear DDNS con un provider VÁLIDO según el schema (noip está en el
	// CHECK) pero que el reconciler NO tiene registrado. Esto emula el
	// caso de que añadamos un provider al schema pero olvidemos
	// registrarlo en el código.
	rec, _, _, _, emitter, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	store := rec.secrets
	secretID, _ := store.CreateSecret("ddns_token", "x", []byte("x"))
	d := &NetworkDdns{
		Provider:      "noip", // válido en schema, NO registrado en reconciler
		Domain:        "test.example.com",
		TokenSecretID: string(secretID),
		Enabled:       true,
		AutoUpdate:    true,
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		if err := rec.repo.CreateDdns(context.Background(), tx, d); err != nil {
			return err
		}
		return rec.repo.MarkDdnsApplied(context.Background(), tx, d.ID)
	})

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	foundUnknown := false
	for _, e := range events {
		if e.Event == "provider_unknown" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Error("expected provider_unknown event")
	}
}

func TestDDNSReconciler_ContextCancellation(t *testing.T) {
	rec, provider, repo, store, _, _, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	// Crear varias entradas
	for i := 0; i < 5; i++ {
		seedDDNS(t, c.db, repo, store,
			"test"+string(rune('a'+i))+".duckdns.org", true, true, 900)
	}
	provider.result = &DDNSUpdateResult{RawResponse: "OK"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ya cancelado al entrar

	err := rec.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestDDNSReconciler_IdempotencyAcrossPasses(t *testing.T) {
	// Si el interval no ha pasado, una segunda pasada no debe llamar al provider.
	rec, provider, repo, store, _, clock, c, cleanup := newTestDDNSReconciler(t)
	defer cleanup()

	seedDDNS(t, c.db, repo, store, "test.duckdns.org", true, true, 900)
	provider.result = &DDNSUpdateResult{RawResponse: "OK"}

	// Pasada 1
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Avanzar 30s (mucho menos que update_interval=900s)
	clock.Advance(30 * time.Second)
	// Pasada 2
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	if calls := provider.Calls(); len(calls) != 1 {
		t.Errorf("calls = %d, want 1 (interval not elapsed)", len(calls))
	}
}
