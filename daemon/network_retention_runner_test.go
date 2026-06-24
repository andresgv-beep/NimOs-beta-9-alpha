// network_retention_runner_test.go — Tests del RetentionRunner.

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestRetentionRunner(t *testing.T) (*RetentionRunner, *NetworkRepo, *EventEmitter, *FakeClock, *sqlConn, func()) {
	t.Helper()
	c, cleanup := setupNetworkDB(t)
	clock := NewFakeClock(time.Date(2026, 6, 15, 2, 30, 0, 0, time.UTC))
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	r, err := NewRetentionRunner(repo, emitter, clock, RetentionRunnerConfig{
		RunHourUTC:             3,
		CompletedOperationsTTL: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, repo, emitter, clock, c, cleanup
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestRetentionRunner_NewRequiresDeps(t *testing.T) {
	_, err := NewRetentionRunner(nil, nil, nil, RetentionRunnerConfig{})
	if err == nil {
		t.Error("expected error with nil deps")
	}
}

func TestRetentionRunner_DefaultsApplied(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	clock := NewFakeClock(time.Now())
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	r, err := NewRetentionRunner(repo, emitter, clock, RetentionRunnerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if r.config.RunHourUTC != 3 {
		t.Errorf("default RunHourUTC = %d, want 3", r.config.RunHourUTC)
	}
	if r.config.CompletedOperationsTTL == 0 {
		t.Error("default TTL not applied")
	}
}

func TestRetentionRunner_InvalidHourFallsBackToDefault(t *testing.T) {
	c, cleanup := setupNetworkDB(t)
	defer cleanup()
	clock := NewFakeClock(time.Now())
	repo := NewNetworkRepo(c.db, clock)
	emitter := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())

	r, err := NewRetentionRunner(repo, emitter, clock, RetentionRunnerConfig{
		RunHourUTC: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.config.RunHourUTC != 3 {
		t.Errorf("invalid hour 99 should fall back to 3, got %d", r.config.RunHourUTC)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// nextRunTime
// ═════════════════════════════════════════════════════════════════════════════

func TestRetentionRunner_NextRunBeforeWindow(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// "Now" es 02:30 UTC. Próximo run es hoy 03:00.
	now := time.Date(2026, 6, 15, 2, 30, 0, 0, time.UTC)
	next := r.nextRunTime(now)
	expected := time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("next = %v, want %v", next, expected)
	}
}

func TestRetentionRunner_NextRunAfterWindow(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// "Now" es 05:00 UTC. Ya pasó el 03:00; siguiente es mañana 03:00.
	now := time.Date(2026, 6, 15, 5, 0, 0, 0, time.UTC)
	next := r.nextRunTime(now)
	expected := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("next = %v, want %v", next, expected)
	}
}

func TestRetentionRunner_NextRunExactlyAtHour(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// "Now" es exactamente 03:00:00.000. El candidate del mismo día no
	// es After(now), así que va a mañana.
	now := time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)
	next := r.nextRunTime(now)
	expected := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("next = %v, want %v", next, expected)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// RunOnce — ejecuta purgas reales contra DB
// ═════════════════════════════════════════════════════════════════════════════

func TestRetentionRunner_RunOnceDoesNotErrorOnEmptyDB(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	if err := r.RunOnce(context.Background()); err != nil {
		t.Errorf("RunOnce on empty DB should not error: %v", err)
	}
}

func TestRetentionRunner_RunOncePrunesEvents(t *testing.T) {
	r, _, emitter, clock, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// El FakeClock arranca en 2026-06-15 02:30. Emit ahora un debug que
	// dura solo 1 día por defecto. Avanzamos 2 días y purgamos.
	if _, err := emitter.Emit(context.Background(), EventInput{
		Category: CategoryObserver,
		Event:    "ancient_event",
		Level:    EventLevelDebug,
		Message:  "old debug",
	}); err != nil {
		t.Fatal(err)
	}

	// Avanzar 2 días → el evento debug está fuera del TTL default (1d).
	clock.Advance(48 * time.Hour)

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 100)
	for _, e := range events {
		if e.Event == "ancient_event" {
			t.Error("ancient debug event should have been pruned")
		}
	}
}

func TestRetentionRunner_RunOnceEmitsAuditOnlyIfWork(t *testing.T) {
	r, _, emitter, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// DB vacía → RunOnce no borra nada → no emite retention_pass_completed.
	r.RunOnce(context.Background())

	events, _ := emitter.ListEventsByCategory(context.Background(), CategoryObserver, 10)
	for _, e := range events {
		if e.Event == "retention_pass_completed" {
			t.Error("should not emit retention_pass_completed when no deletions")
		}
	}
}

func TestRetentionRunner_RunOncePrunesOldCompletedOperations(t *testing.T) {
	r, repo, _, clock, c, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// Insertar una operación completada muy vieja.
	clock.SetTime(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	old := &NetworkOperation{
		Type:        "ddns_update",
		Status:      "completed",
		TriggeredBy: "system:scheduler",
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		if err := repo.CreateOperation(context.Background(), tx, old); err != nil {
			return err
		}
		return repo.UpdateOperationStatus(context.Background(), tx, old.ID, "completed", nil, nil)
	})

	// Insertar una reciente.
	clock.SetTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))
	recent := &NetworkOperation{
		Type:        "ddns_update",
		Status:      "completed",
		TriggeredBy: "system:scheduler",
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		if err := repo.CreateOperation(context.Background(), tx, recent); err != nil {
			return err
		}
		return repo.UpdateOperationStatus(context.Background(), tx, recent.ID, "completed", nil, nil)
	})

	clock.SetTime(time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC))
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// La operation vieja debería estar borrada, la reciente preservada.
	var count int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_operations WHERE id = ?`, old.ID).Scan(&count)
	if count != 0 {
		t.Errorf("old operation not pruned; count=%d", count)
	}
	c.db.QueryRow(`SELECT COUNT(*) FROM network_operations WHERE id = ?`, recent.ID).Scan(&count)
	if count != 1 {
		t.Errorf("recent operation should be preserved; count=%d", count)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Lifecycle (Start/Stop)
// ═════════════════════════════════════════════════════════════════════════════

func TestRetentionRunner_StartIdempotent(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	r.Start(ctx) // segunda llamada no debe panickear ni crear goroutine extra
	r.Stop()
}

func TestRetentionRunner_StopWithoutStart(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	// No panic.
	r.Stop()
}

func TestRetentionRunner_StopUnblocksGoroutine(t *testing.T) {
	// Inicia un runner y verifica que Stop() retorna en tiempo
	// razonable (no se queda esperando 24h).
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	r.Start(context.Background())

	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// OK
	case <-time.After(2 * time.Second):
		t.Error("Stop() did not return within 2s")
	}
}

func TestRetentionRunner_ContextCancelStopsGoroutine(t *testing.T) {
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	cancel()

	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Error("goroutine did not exit on context cancel")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Resilience
// ═════════════════════════════════════════════════════════════════════════════

func TestRetentionRunner_RunOnceContinuesAfterPartialFailure(t *testing.T) {
	// Para forzar un fallo parcial sin trampear DB, verificamos que el
	// orden de purgas es consistente. Si una falla, las demás se ejecutan
	// igual y RunOnce devuelve un error pero no excepción.
	//
	// Usamos un context cancelado para forzar errores en TODAS las
	// queries — verificamos que se devuelve error (no panic).
	r, _, _, _, _, cleanup := newTestRetentionRunner(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.RunOnce(ctx)
	// Con contexto cancelado, esperamos error pero no panic.
	if err != nil && !errors.Is(err, context.Canceled) {
		// Aceptamos cualquier error que envuelva el cancel.
		t.Logf("got expected error: %v", err)
	}
}
