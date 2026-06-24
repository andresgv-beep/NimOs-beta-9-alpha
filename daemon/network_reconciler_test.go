// network_reconciler_test.go — Tests del Reconciler interface + scheduler.
//
// Cubre:
//   - Register: rechaza nil, name vacío, interval < 1s, duplicados.
//   - Register tras Start: rechazado.
//   - Start/Stop lifecycle (idempotencia inversa).
//   - RunOnce: ejecuta y devuelve el error del reconciler.
//   - Goroutine loop: tick periódico ejecuta Reconcile.
//   - Cancelación del contexto detiene la goroutine.
//   - Panic en Reconcile no derriba el scheduler.
//   - Tier.String() para los 3 valores.

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fake reconciler para tests
// ─────────────────────────────────────────────────────────────────────────────

type fakeReconciler struct {
	name     string
	tier     ReconcilerTier
	interval time.Duration

	mu       sync.Mutex
	calls    int64
	lastErr  error
	doPanic  bool
	doErr    error
	blockCh  chan struct{} // si !nil, Reconcile bloquea hasta que se cierra
}

func (f *fakeReconciler) Name() string             { return f.name }
func (f *fakeReconciler) Tier() ReconcilerTier     { return f.tier }
func (f *fakeReconciler) Interval() time.Duration  { return f.interval }

func (f *fakeReconciler) Reconcile(ctx context.Context) error {
	atomic.AddInt64(&f.calls, 1)

	if f.blockCh != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.blockCh:
		}
	}

	f.mu.Lock()
	doPanic := f.doPanic
	doErr := f.doErr
	f.mu.Unlock()

	if doPanic {
		panic("intentional panic in test")
	}
	return doErr
}

func (f *fakeReconciler) Calls() int64 {
	return atomic.LoadInt64(&f.calls)
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier.String
// ─────────────────────────────────────────────────────────────────────────────

func TestReconciler_TierString(t *testing.T) {
	cases := []struct {
		tier ReconcilerTier
		want string
	}{
		{TierCritical, "critical"},
		{TierMedium, "medium"},
		{TierLow, "low"},
		{ReconcilerTier(99), "unknown(99)"},
	}
	for _, c := range cases {
		if got := c.tier.String(); got != c.want {
			t.Errorf("%d: String() = %q, want %q", c.tier, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Register
// ─────────────────────────────────────────────────────────────────────────────

func TestRegister_RejectsNil(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	if err := s.Register(nil); err == nil {
		t.Error("Register(nil) should error")
	}
}

func TestRegister_RejectsEmptyName(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "", tier: TierMedium, interval: time.Second}
	if err := s.Register(r); err == nil {
		t.Error("Register with empty Name should error")
	}
}

func TestRegister_RejectsShortInterval(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "x", tier: TierMedium, interval: 500 * time.Millisecond}
	if err := s.Register(r); err == nil {
		t.Error("Register with interval<1s should error")
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r1 := &fakeReconciler{name: "dup", tier: TierMedium, interval: time.Second}
	r2 := &fakeReconciler{name: "dup", tier: TierLow, interval: time.Minute}

	if err := s.Register(r1); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := s.Register(r2)
	if !errors.Is(err, ErrReconcilerAlreadyRegistered) {
		t.Errorf("second Register: err = %v, want ErrReconcilerAlreadyRegistered", err)
	}
}

func TestRegister_RejectsAfterStart(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "first", tier: TierMedium, interval: time.Second}
	if err := s.Register(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	r2 := &fakeReconciler{name: "late", tier: TierMedium, interval: time.Second}
	if err := s.Register(r2); err == nil {
		t.Error("Register after Start should error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ListReconcilers
// ─────────────────────────────────────────────────────────────────────────────

func TestListReconcilers_SortedByName(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	names := []string{"zeta", "alpha", "mike"}
	for _, n := range names {
		s.Register(&fakeReconciler{name: n, tier: TierMedium, interval: time.Second})
	}

	got := s.ListReconcilers()
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	wantOrder := []string{"alpha", "mike", "zeta"}
	for i, r := range got {
		if r.Name() != wantOrder[i] {
			t.Errorf("[%d] = %s, want %s", i, r.Name(), wantOrder[i])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Start / Stop lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func TestStart_ErrorsIfAlreadyRunning(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	err := s.Start(context.Background())
	if !errors.Is(err, ErrSchedulerAlreadyRunning) {
		t.Errorf("second Start: err = %v, want ErrSchedulerAlreadyRunning", err)
	}
}

func TestStop_ErrorsIfNotRunning(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	err := s.Stop()
	if !errors.Is(err, ErrSchedulerNotRunning) {
		t.Errorf("Stop without Start: err = %v, want ErrSchedulerNotRunning", err)
	}
}

func TestIsRunning_TrueDuringStart(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	if s.IsRunning() {
		t.Error("before Start: IsRunning=true, want false")
	}
	s.Start(context.Background())
	if !s.IsRunning() {
		t.Error("after Start: IsRunning=false, want true")
	}
	s.Stop()
	if s.IsRunning() {
		t.Error("after Stop: IsRunning=true, want false")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RunOnce
// ─────────────────────────────────────────────────────────────────────────────

func TestRunOnce_ExecutesAndReturnsError(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "x", tier: TierMedium, interval: time.Second}
	s.Register(r)

	if err := s.RunOnce(context.Background(), "x"); err != nil {
		t.Errorf("RunOnce: %v", err)
	}
	if r.Calls() != 1 {
		t.Errorf("calls = %d, want 1", r.Calls())
	}

	// Reconciler retorna error
	r.mu.Lock()
	r.doErr = fmt.Errorf("boom")
	r.mu.Unlock()

	err := s.RunOnce(context.Background(), "x")
	if err == nil || err.Error() != "boom" {
		t.Errorf("RunOnce with error: got %v, want 'boom'", err)
	}
}

func TestRunOnce_NotFound(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	err := s.RunOnce(context.Background(), "missing")
	if !errors.Is(err, ErrReconcilerNotFound) {
		t.Errorf("err = %v, want ErrReconcilerNotFound", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Goroutine loop
// ─────────────────────────────────────────────────────────────────────────────

func TestSchedulerLoop_TicksReconcile(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "ticker", tier: TierMedium, interval: time.Second}
	s.Register(r)

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Esperar a que tick al menos 2 veces (2 * 1s + margen).
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if r.Calls() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.Calls() < 2 {
		t.Errorf("after ~2.5s with interval=1s: calls = %d, want >= 2", r.Calls())
	}
}

func TestSchedulerLoop_ContextCancellationStops(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	r := &fakeReconciler{name: "x", tier: TierMedium, interval: time.Second}
	s.Register(r)

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop terminó porque la goroutine ya estaba acabando.
	case <-time.After(2 * time.Second):
		t.Error("Stop did not return within 2s after context cancel")
	}
}

func TestSchedulerLoop_PanicDoesNotKillScheduler(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	rBad := &fakeReconciler{name: "panicky", tier: TierMedium, interval: time.Second, doPanic: true}
	rGood := &fakeReconciler{name: "good", tier: TierMedium, interval: time.Second}
	s.Register(rBad)
	s.Register(rGood)

	s.Start(context.Background())
	defer s.Stop()

	// El malo panica en cada tick; el bueno debe seguir ejecutándose.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rGood.Calls() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rGood.Calls() < 2 {
		t.Errorf("good reconciler should keep running despite peer panicking: calls = %d", rGood.Calls())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrency
// ─────────────────────────────────────────────────────────────────────────────

func TestConcurrent_MultipleReconcilersRunIndependently(t *testing.T) {
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	const N = 5
	recs := make([]*fakeReconciler, N)
	for i := 0; i < N; i++ {
		recs[i] = &fakeReconciler{
			name:     fmt.Sprintf("r%d", i),
			tier:     TierMedium,
			interval: time.Second,
		}
		s.Register(recs[i])
	}

	s.Start(context.Background())
	defer s.Stop()

	// Esperar a que todos ticken al menos una vez.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allTicked := true
		for _, r := range recs {
			if r.Calls() == 0 {
				allTicked = false
				break
			}
		}
		if allTicked {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, r := range recs {
		if r.Calls() == 0 {
			t.Errorf("reconciler %s never ticked", r.Name())
		}
	}
}

func TestSchedulerLoop_InitialRunBeforeFirstInterval(t *testing.T) {
	// CONTRATO: cada reconciler hace una pasada INICIAL tras el settle,
	// sin esperar su primer intervalo. Sin esto, el observer de certs
	// (intervalo largo) dejaba minutos de "desconocido" en la UI tras
	// cada reinicio del daemon.
	s := NewReconcilerScheduler(nil)
	s.settleDelay = 10 * time.Millisecond
	// Intervalo ENORME: si hay una llamada, solo puede ser la inicial.
	r := &fakeReconciler{name: "boot", tier: TierLow, interval: time.Hour}
	s.Register(r)

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if r.Calls() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r.Calls() < 1 {
		t.Error("initial run should happen right after settle, not after the first interval")
	}
}
