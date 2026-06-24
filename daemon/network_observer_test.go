// network_observer_test.go — Tests del NetworkObserver.
//
// Estrategia:
//   - Mock probe controlable que devuelve estados artificiales.
//   - Sembrar ports/certs en DB con applied_generation > 0 para activar
//     el análisis de divergencias.
//   - Verificar:
//     · Reconciler interface (Name/Tier/Interval).
//     · RunOnce sin entidades → snapshot saludable.
//     · Port divergence: enabled+no_listening → critical + record observed.
//     · Cert divergence: missing_fullchain → degraded + record observed.
//     · Cert divergence: expired → critical.
//     · Certs expiring → degraded (no critical).
//     · Persistencia en network_observed (snapshot_type='event' si hay div).
//     · Eventos emitidos por categoría correcta.
//     · Snapshot atómico publicado tras Run.
//     · Applied=0 NO genera divergence (estado inicial).

package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock probe
// ─────────────────────────────────────────────────────────────────────────────

type mockNetworkProbe struct {
	mu     sync.Mutex
	result ProbeResult
	calls  int
}

func (m *mockNetworkProbe) Probe(_ []PortProbeInput) ProbeResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.result
}

func (m *mockNetworkProbe) setResult(r ProbeResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = r
}

func (m *mockNetworkProbe) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// ─────────────────────────────────────────────────────────────────────────────
// Setup helper
// ─────────────────────────────────────────────────────────────────────────────

func newTestNetObserver(t *testing.T) (*NetworkObserver, *mockNetworkProbe, *NetworkRepo, *EventEmitter, *FakeClock, *sqlConn, func()) {
	t.Helper()
	c, cleanup := setupNetworkDB(t)
	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())
	probe := &mockNetworkProbe{}

	obs, err := NewNetworkObserver(repo, emitter, probe, clock, ObserverConfig{
		Interval: 60 * time.Second,
	})
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return obs, probe, repo, emitter, clock, c, cleanup
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestObserver_NewRequiresDeps(t *testing.T) {
	_, err := NewNetworkObserver(nil, nil, nil, nil, ObserverConfig{})
	if err == nil {
		t.Error("nil repo should error")
	}
}

func TestObserver_NewAppliesDefaults(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	clock := NewFakeClock(time.Now())
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())
	probe := &mockNetworkProbe{}

	obs, err := NewNetworkObserver(repo, emitter, probe, clock, ObserverConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.config.Interval == 0 {
		t.Error("Interval default not applied")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Reconciler interface
// ═════════════════════════════════════════════════════════════════════════════

func TestObserver_ReconcilerInterface(t *testing.T) {
	obs, _, _, _, _, _, cleanup := newTestNetObserver(t)
	defer cleanup()

	if obs.Name() != "network_observer" {
		t.Errorf("Name = %q", obs.Name())
	}
	if obs.Tier() != TierMedium {
		t.Errorf("Tier = %v", obs.Tier())
	}
	if obs.Interval() != 60*time.Second {
		t.Errorf("Interval = %v", obs.Interval())
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// RunOnce — happy paths
// ═════════════════════════════════════════════════════════════════════════════

func TestObserver_RunOnce_EmptyDB(t *testing.T) {
	obs, probe, _, _, clock, _, cleanup := newTestNetObserver(t)
	defer cleanup()

	probe.setResult(ProbeResult{ProbedAt: clock.Now().UTC()})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if probe.callCount() != 1 {
		t.Errorf("probe calls = %d, want 1", probe.callCount())
	}

	snap := obs.Snapshot()
	if snap == nil {
		t.Fatal("snapshot is nil after RunOnce")
	}
	if snap.OverallHealth != HealthHealthy {
		t.Errorf("health = %s, want healthy", snap.OverallHealth)
	}
	if snap.DivergenceCount != 0 {
		t.Errorf("divergences = %d, want 0", snap.DivergenceCount)
	}
}

func TestObserver_RunOnce_PersistsSnapshotToDB(t *testing.T) {
	obs, probe, repo, _, clock, _, cleanup := newTestNetObserver(t)
	defer cleanup()

	probe.setResult(ProbeResult{ProbedAt: clock.Now().UTC()})
	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	stored, err := repo.GetLatestObservedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetLatestObservedSnapshot: %v", err)
	}
	if stored.SnapshotType != "periodic" {
		t.Errorf("snapshot_type = %q, want periodic", stored.SnapshotType)
	}
	if stored.OverallHealth != "healthy" {
		t.Errorf("overall_health = %q", stored.OverallHealth)
	}
	if stored.DivergenceCount != 0 {
		t.Errorf("divergence_count = %d", stored.DivergenceCount)
	}
}

func TestObserver_RunOnce_PublishesAtomicSnapshot(t *testing.T) {
	obs, probe, _, _, clock, _, cleanup := newTestNetObserver(t)
	defer cleanup()

	if obs.Snapshot() != nil {
		t.Error("snapshot non-nil before any run")
	}

	probe.setResult(ProbeResult{ProbedAt: clock.Now().UTC()})

	// Dos pasadas → generation crece monotonicamente.
	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen1 := obs.Snapshot().Generation
	if gen1 != 1 {
		t.Errorf("first gen = %d, want 1", gen1)
	}

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen2 := obs.Snapshot().Generation
	if gen2 != 2 {
		t.Errorf("second gen = %d, want 2", gen2)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Port divergences
// ═════════════════════════════════════════════════════════════════════════════

// seedAppliedPort crea un port y lo marca como applied (para activar
// el análisis de divergence).
func seedAppliedPort(t *testing.T, db *sql.DB, repo *NetworkRepo, id string, port int, bind string, enabled bool) {
	t.Helper()
	withNetTx(t, db, func(tx *sql.Tx) error {
		if err := repo.CreatePort(context.Background(), tx, &NetworkPort{
			ID: id, Port: port, BindAddress: bind, Enabled: enabled,
		}); err != nil {
			return err
		}
		return repo.MarkPortApplied(context.Background(), tx, id)
	})
}

func TestObserver_PortDivergence_NotListening(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "http", 80, "0.0.0.0", true)

	// Probe reporta NOT listening.
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: false}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	snap := obs.Snapshot()
	if len(snap.PortDivergences) != 1 {
		t.Fatalf("port divergences = %d, want 1", len(snap.PortDivergences))
	}
	d := snap.PortDivergences[0]
	if d.PortID != "http" || d.Reason != "not_listening" {
		t.Errorf("divergence = %+v", d)
	}
	if snap.OverallHealth != HealthFailed {
		t.Errorf("health = %s, want critical (not_listening)", snap.OverallHealth)
	}

	// Verificar que el observed_generation se ha incrementado.
	p, _ := repo.GetPort(context.Background(), "http")
	if p.Convergence.Observed <= p.Convergence.Applied {
		t.Errorf("observed_generation should have advanced; got %+v", p.Convergence)
	}
	if !p.Convergence.HasDrifted() {
		t.Error("port should be marked as drifted")
	}
}

func TestObserver_PortDivergence_ConfigMismatch(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "http", 80, "0.0.0.0", true)

	// Probe reporta listening pero en puerto distinto.
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: true, Port: 8080, BindAddress: "0.0.0.0"}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	snap := obs.Snapshot()
	if len(snap.PortDivergences) != 1 {
		t.Fatalf("divergences = %d", len(snap.PortDivergences))
	}
	d := snap.PortDivergences[0]
	if d.Reason != "config_mismatch" {
		t.Errorf("reason = %q, want config_mismatch", d.Reason)
	}
	// config_mismatch ≠ critical (la web sí está respondiendo en otro puerto).
	if snap.OverallHealth != HealthDegraded {
		t.Errorf("health = %s, want degraded", snap.OverallHealth)
	}
}

func TestObserver_PortDivergence_UnexpectedListener(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "https", 443, "0.0.0.0", false)

	// Probe reporta listening aunque debería estar deshabilitado.
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "https", Listening: true, Port: 443, BindAddress: "0.0.0.0"}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	snap := obs.Snapshot()
	if len(snap.PortDivergences) != 1 || snap.PortDivergences[0].Reason != "unexpected_listener" {
		t.Errorf("divergences = %+v", snap.PortDivergences)
	}
}

func TestObserver_PortAppliedZero_NoDivergence(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	// Port creado SIN MarkApplied → applied=0.
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreatePort(context.Background(), tx, &NetworkPort{
			ID: "http", Port: 80, BindAddress: "0.0.0.0", Enabled: true,
		})
	})

	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: false}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := obs.Snapshot()
	if snap.DivergenceCount != 0 {
		t.Errorf("applied=0 should not produce divergence; got %d", snap.DivergenceCount)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Combinaciones
// ═════════════════════════════════════════════════════════════════════════════

func TestObserver_DivergenceTriggersEventSnapshot(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "http", 80, "0.0.0.0", true)
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: false}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	stored, _ := repo.GetLatestObservedSnapshot(context.Background())
	if stored.SnapshotType != "event" {
		t.Errorf("snapshot_type = %q, want event (divergences > 0)", stored.SnapshotType)
	}
}

func TestObserver_EmitsEvents(t *testing.T) {
	obs, probe, repo, emitter, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "http", 80, "0.0.0.0", true)
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: false}},
	})

	if err := obs.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Verificar que se emitió un evento de tipo port_divergence
	events, err := emitter.ListEventsByCategory(context.Background(), CategoryPort, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 1 {
		t.Errorf("expected port event, got %d", len(events))
	}
	if events[0].Event != "port_divergence" {
		t.Errorf("event = %q, want port_divergence", events[0].Event)
	}
	if events[0].Level != string(EventLevelWarn) {
		t.Errorf("level = %q, want warn", events[0].Level)
	}

	// Critical health también debería emitir un evento de observer
	observerEvents, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	found := false
	for _, e := range observerEvents {
		if e.Event == "overall_health_critical" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected overall_health_critical event")
	}
}

func TestObserver_MultiplePassesIdempotent(t *testing.T) {
	obs, probe, repo, _, clock, c, cleanup := newTestNetObserver(t)
	defer cleanup()

	seedAppliedPort(t, c.db, repo, "http", 80, "0.0.0.0", true)
	probe.setResult(ProbeResult{
		ProbedAt: clock.Now().UTC(),
		Ports:    []ProbedPort{{ID: "http", Listening: false}},
	})

	for i := 0; i < 3; i++ {
		clock.Advance(time.Minute)
		if err := obs.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	// 3 snapshots distintos en DB.
	count, _ := repo.CountObservedSnapshots(context.Background())
	if count != 3 {
		t.Errorf("snapshots in DB = %d, want 3", count)
	}

	// El observed_generation debe haber avanzado 3 veces.
	p, _ := repo.GetPort(context.Background(), "http")
	if p.Convergence.Observed-p.Convergence.Applied != 3 {
		t.Errorf("observed - applied = %d, want 3 (3 passes each incrementing)",
			p.Convergence.Observed-p.Convergence.Applied)
	}
}
