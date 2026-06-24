// breaker_test.go — Tests del CircuitBreaker.
//
// Estrategia: usar FakeClock (storage_clock.go) para todo lo temporal.
// Cero time.Sleep — los tests son deterministas y rápidos.

package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

var errProbeFailed = errors.New("probe failed")

// recordingPersist captura llamadas al callback de persistencia para
// verificarlas en los tests. Thread-safe.
type recordingPersist struct {
	mu    sync.Mutex
	calls []persistCall
}

type persistCall struct {
	Name      string
	State     CircuitState
	NextRetry *time.Time
}

func (r *recordingPersist) fn(name string, state CircuitState, nextRetry *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, persistCall{Name: name, State: state, NextRetry: nextRetry})
	return nil
}

func (r *recordingPersist) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingPersist) last() (persistCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return persistCall{}, false
	}
	return r.calls[len(r.calls)-1], true
}

// newTestBreaker construye un breaker con thresholds bajos para tests
// rápidos: 3 fallos consecutivos → open, cooldown 1 minuto.
func newTestBreaker(clock Clock) (*CircuitBreaker, *recordingPersist) {
	rec := &recordingPersist{}
	cfg := BreakerConfig{
		Name:             "test",
		FailureThreshold: 3,
		CooldownDuration: 1 * time.Minute,
		Clock:            clock,
		Persist:          rec.fn,
	}
	return NewCircuitBreaker(cfg), rec
}

// callOK ejecuta una llamada exitosa.
func callOK(b *CircuitBreaker) error {
	return b.Call(func() error { return nil })
}

// callKO ejecuta una llamada que falla.
func callKO(b *CircuitBreaker) error {
	return b.Call(func() error { return errProbeFailed })
}

// ─────────────────────────────────────────────────────────────────────────────
// Construction
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_NewWithDefaults(t *testing.T) {
	b := NewCircuitBreaker(BreakerConfig{Name: "x"})
	if b.cfg.FailureThreshold != 5 {
		t.Errorf("default FailureThreshold = %d, want 5", b.cfg.FailureThreshold)
	}
	if b.cfg.CooldownDuration != 5*time.Minute {
		t.Errorf("default CooldownDuration = %v, want 5m", b.cfg.CooldownDuration)
	}
	if b.cfg.Clock == nil {
		t.Error("default Clock is nil")
	}
	if b.GetState() != CircuitClosed {
		t.Errorf("initial state = %s, want closed", b.GetState())
	}
}

func TestCircuitBreaker_NewWithCustomConfig(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	b, _ := newTestBreaker(clock)
	if b.cfg.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", b.cfg.FailureThreshold)
	}
}

func TestCircuitBreaker_NewWithState_OpenFutureRetry(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	future := now.Add(2 * time.Minute)

	cfg := DefaultBreakerConfig("test")
	cfg.Clock = clock
	b := NewCircuitBreakerWithState(cfg, CircuitOpen, &future)

	if b.GetState() != CircuitOpen {
		t.Errorf("state = %s, want open (next_retry is future)", b.GetState())
	}
	snap := b.Snapshot()
	if snap.NextRetry == nil || !snap.NextRetry.Equal(future) {
		t.Errorf("NextRetry = %v, want %v", snap.NextRetry, future)
	}
}

func TestCircuitBreaker_NewWithState_OpenExpiredRetry(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	past := now.Add(-1 * time.Minute)

	cfg := DefaultBreakerConfig("test")
	cfg.Clock = clock
	b := NewCircuitBreakerWithState(cfg, CircuitOpen, &past)

	if b.GetState() != CircuitHalfOpen {
		t.Errorf("state = %s, want half_open (cooldown expired during downtime)", b.GetState())
	}
}

func TestCircuitBreaker_NewWithState_HalfOpenRestoresToClosed(t *testing.T) {
	cfg := DefaultBreakerConfig("test")
	cfg.Clock = NewFakeClock(time.Now())
	b := NewCircuitBreakerWithState(cfg, CircuitHalfOpen, nil)

	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s, want closed (half_open restores to closed)", b.GetState())
	}
}

func TestCircuitBreaker_NewWithState_InvalidStateFallsBackToClosed(t *testing.T) {
	cfg := DefaultBreakerConfig("test")
	cfg.Clock = NewFakeClock(time.Now())
	b := NewCircuitBreakerWithState(cfg, CircuitState("garbage"), nil)

	if b.GetState() != CircuitClosed {
		t.Errorf("invalid state should fall back to closed, got %s", b.GetState())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CLOSED state behavior
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_ClosedStaysClosedOnSuccess(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, rec := newTestBreaker(clock)

	for i := 0; i < 10; i++ {
		if err := callOK(b); err != nil {
			t.Fatalf("call %d returned error: %v", i, err)
		}
	}
	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s after 10 successes, want closed", b.GetState())
	}
	if rec.count() != 0 {
		t.Errorf("Persist called %d times after pure-success run, want 0", rec.count())
	}
}

func TestCircuitBreaker_ClosedAccumulatesFailuresUnderThreshold(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, rec := newTestBreaker(clock) // threshold=3

	// 2 fallos: aún CLOSED, sin transición
	for i := 0; i < 2; i++ {
		if err := callKO(b); !errors.Is(err, errProbeFailed) {
			t.Fatalf("call %d: got err=%v, want errProbeFailed", i, err)
		}
	}
	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s after 2 failures (threshold=3), want closed", b.GetState())
	}
	if rec.count() != 0 {
		t.Errorf("Persist called %d times without transition, want 0", rec.count())
	}
}

func TestCircuitBreaker_ClosedTransitionsToOpenAtThreshold(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, rec := newTestBreaker(clock) // threshold=3, cooldown=1m

	// 3 fallos consecutivos → OPEN
	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}

	if b.GetState() != CircuitOpen {
		t.Fatalf("state = %s after %d failures, want open", b.GetState(), 3)
	}
	if rec.count() != 1 {
		t.Fatalf("Persist called %d times, want 1 (single transition)", rec.count())
	}
	last, _ := rec.last()
	if last.State != CircuitOpen {
		t.Errorf("persisted state = %s, want open", last.State)
	}
	if last.NextRetry == nil {
		t.Fatal("persisted NextRetry is nil, want set to now + cooldown")
	}
	expected := now.Add(1 * time.Minute)
	if !last.NextRetry.Equal(expected) {
		t.Errorf("persisted NextRetry = %v, want %v", last.NextRetry, expected)
	}
}

func TestCircuitBreaker_ClosedSuccessResetsFailureCounter(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, _ := newTestBreaker(clock) // threshold=3

	// 2 fallos, luego 1 éxito → contador reset → otros 2 fallos no abren
	_ = callKO(b)
	_ = callKO(b)
	_ = callOK(b)
	_ = callKO(b)
	_ = callKO(b)

	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s, want closed (counter should have reset)", b.GetState())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// OPEN state behavior
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_OpenRejectsCallsWithinCooldown(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, rec := newTestBreaker(clock)

	// Llevar a OPEN
	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	initialPersistCount := rec.count()

	// El fn() debe NO ejecutarse: usamos un counter para detectar invocaciones
	var fnInvocations atomic.Int32
	err := b.Call(func() error {
		fnInvocations.Add(1)
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Call returned %v, want ErrCircuitOpen", err)
	}
	if fnInvocations.Load() != 0 {
		t.Error("fn() was invoked while breaker is open within cooldown")
	}
	if rec.count() != initialPersistCount {
		t.Errorf("Persist called %d times during rejection, want %d (no transition)",
			rec.count(), initialPersistCount)
	}
}

func TestCircuitBreaker_OpenTransitionsToHalfOpenAfterCooldown(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, rec := newTestBreaker(clock) // cooldown=1m

	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	if b.GetState() != CircuitOpen {
		t.Fatalf("setup: state = %s, want open", b.GetState())
	}

	// Avanzar más allá del cooldown
	clock.Advance(2 * time.Minute)

	// Próximo Call() permite ejecución y deja el breaker en half_open
	// ANTES de ejecutar fn (lo verificamos viendo el snapshot post-call).
	var fnInvoked atomic.Bool
	err := b.Call(func() error {
		fnInvoked.Store(true)
		return errProbeFailed // hacemos que falle para que post-call no sea CLOSED
	})
	if !errors.Is(err, errProbeFailed) {
		t.Errorf("Call returned %v, want errProbeFailed", err)
	}
	if !fnInvoked.Load() {
		t.Error("fn() was not invoked after cooldown expired")
	}

	// Después del fail en half_open, debería volver a OPEN
	if b.GetState() != CircuitOpen {
		t.Errorf("state = %s after half_open + fail, want open", b.GetState())
	}

	// Persist debería haberse llamado 3 veces:
	//   1. closed → open (3er fallo)
	//   2. open → half_open (cooldown expirado)
	//   3. half_open → open (fallo del probe)
	if rec.count() != 3 {
		t.Errorf("Persist called %d times, want 3 (close→open, open→half_open, half_open→open)",
			rec.count())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HALF_OPEN state behavior
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_HalfOpenSuccessReturnsToClosed(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, rec := newTestBreaker(clock)

	// Forzar a half_open vía recuperación de estado.
	cfg := b.cfg
	bRestored := NewCircuitBreakerWithState(cfg, CircuitHalfOpen, nil)
	_ = bRestored // ojo: ese empieza closed, no half_open

	// Mejor: ir vía la ruta natural (closed → open → cooldown → half_open)
	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	clock.Advance(2 * time.Minute)

	// Una llamada exitosa: half_open → closed
	if err := callOK(b); err != nil {
		t.Fatalf("Call returned %v, want nil", err)
	}

	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s after half_open + success, want closed", b.GetState())
	}

	// Persist:
	//   1. closed → open
	//   2. open → half_open
	//   3. half_open → closed
	if rec.count() != 3 {
		t.Errorf("Persist called %d times, want 3", rec.count())
	}
	last, _ := rec.last()
	if last.State != CircuitClosed {
		t.Errorf("last persisted state = %s, want closed", last.State)
	}
	if last.NextRetry != nil {
		t.Errorf("last persisted NextRetry = %v, want nil (closed has no retry)", last.NextRetry)
	}
}

func TestCircuitBreaker_HalfOpenFailureRestartsCooldown(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, _ := newTestBreaker(clock)

	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	clock.Advance(2 * time.Minute) // cooldown expirado

	// Llamada que falla en half_open → vuelta a open con cooldown reseteado
	_ = callKO(b)

	if b.GetState() != CircuitOpen {
		t.Fatalf("state = %s after half_open + fail, want open", b.GetState())
	}

	// El nuevo cooldown empieza desde el "now" del FakeClock tras el Advance
	snap := b.Snapshot()
	expected := clock.Now().Add(1 * time.Minute)
	if snap.NextRetry == nil || !snap.NextRetry.Equal(expected) {
		t.Errorf("NextRetry = %v, want %v (cooldown restarted from current time)",
			snap.NextRetry, expected)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reset
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_ResetFromOpenReturnsToClosed(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, rec := newTestBreaker(clock)

	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	if b.GetState() != CircuitOpen {
		t.Fatalf("setup: state = %s, want open", b.GetState())
	}

	b.Reset()

	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s after Reset, want closed", b.GetState())
	}

	// Persist: 1 (transición a open) + 1 (Reset)
	if rec.count() != 2 {
		t.Errorf("Persist count = %d, want 2 (open transition + reset)", rec.count())
	}
	last, _ := rec.last()
	if last.State != CircuitClosed || last.NextRetry != nil {
		t.Errorf("last persisted = %+v, want closed with nil NextRetry", last)
	}
}

func TestCircuitBreaker_ResetFromCleanClosedIsNoOp(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, rec := newTestBreaker(clock)

	b.Reset() // sin haber hecho nada antes

	if rec.count() != 0 {
		t.Errorf("Persist called %d times on no-op Reset, want 0", rec.count())
	}
}

func TestCircuitBreaker_ResetClearsFailureCounter(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, _ := newTestBreaker(clock) // threshold=3

	_ = callKO(b)
	_ = callKO(b) // 2 fallos, contador=2

	b.Reset()

	// Tras reset, contador a 0. 2 fallos más NO deben abrir.
	_ = callKO(b)
	_ = callKO(b)
	if b.GetState() != CircuitClosed {
		t.Errorf("state = %s after Reset + 2 fails, want closed (counter should have cleared)",
			b.GetState())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Persist callback behavior
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_PersistOnlyCalledOnTransition(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, rec := newTestBreaker(clock)

	// 100 successes en CLOSED — cero transiciones, cero persists.
	for i := 0; i < 100; i++ {
		_ = callOK(b)
	}
	if rec.count() != 0 {
		t.Errorf("Persist called %d times on 100 successes (no transition), want 0", rec.count())
	}
}

func TestCircuitBreaker_PersistErrorDoesNotBreakCall(t *testing.T) {
	clock := NewFakeClock(time.Now())
	cfg := DefaultBreakerConfig("test")
	cfg.Clock = clock
	cfg.FailureThreshold = 3
	cfg.CooldownDuration = 1 * time.Minute
	persistErr := errors.New("disk full")
	cfg.Persist = func(name string, state CircuitState, nextRetry *time.Time) error {
		return persistErr
	}
	b := NewCircuitBreaker(cfg)

	// Hacemos 3 fallos para forzar transición → persist devuelve error,
	// pero el Call retorna el error original del fn, no el del persist.
	var lastErr error
	for i := 0; i < 3; i++ {
		lastErr = callKO(b)
	}
	if !errors.Is(lastErr, errProbeFailed) {
		t.Errorf("last Call returned %v, want errProbeFailed (persist error must not propagate)", lastErr)
	}
	if b.GetState() != CircuitOpen {
		t.Errorf("state = %s, want open (transition must succeed even if persist failed)",
			b.GetState())
	}
}

func TestCircuitBreaker_PersistNilCallbackIsAllowed(t *testing.T) {
	clock := NewFakeClock(time.Now())
	cfg := BreakerConfig{
		Name:             "no-persist",
		FailureThreshold: 3,
		CooldownDuration: 1 * time.Minute,
		Clock:            clock,
		// Persist: nil
	}
	b := NewCircuitBreaker(cfg)

	// No debe panic con Persist=nil
	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	if b.GetState() != CircuitOpen {
		t.Errorf("state = %s, want open", b.GetState())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Snapshot
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitBreaker_SnapshotInClosed(t *testing.T) {
	clock := NewFakeClock(time.Now())
	b, _ := newTestBreaker(clock)

	snap := b.Snapshot()
	if snap.Name != "test" {
		t.Errorf("snap.Name = %s, want test", snap.Name)
	}
	if snap.State != CircuitClosed {
		t.Errorf("snap.State = %s, want closed", snap.State)
	}
	if snap.NextRetry != nil {
		t.Errorf("snap.NextRetry = %v, want nil in closed", snap.NextRetry)
	}
}

func TestCircuitBreaker_SnapshotInOpen(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(now)
	b, _ := newTestBreaker(clock)

	for i := 0; i < 3; i++ {
		_ = callKO(b)
	}
	snap := b.Snapshot()
	if snap.State != CircuitOpen {
		t.Errorf("snap.State = %s, want open", snap.State)
	}
	if snap.NextRetry == nil {
		t.Fatal("snap.NextRetry is nil in open state")
	}
	expected := now.Add(1 * time.Minute)
	if !snap.NextRetry.Equal(expected) {
		t.Errorf("snap.NextRetry = %v, want %v", snap.NextRetry, expected)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CircuitState.IsValid
// ─────────────────────────────────────────────────────────────────────────────

func TestCircuitState_IsValid(t *testing.T) {
	valid := []CircuitState{CircuitClosed, CircuitOpen, CircuitHalfOpen}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("IsValid(%s) = false, want true", s)
		}
	}
	invalid := []CircuitState{"", "garbage", "half-open", "CLOSED"}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", s)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrency
// ─────────────────────────────────────────────────────────────────────────────

// TestCircuitBreaker_ConcurrentCallsRespectInvariants verifica que
// llamadas concurrentes no rompen la máquina de estado. No verificamos
// el contador exacto de failures (depende del orden), pero sí que el
// state final es consistente con los resultados observados.
func TestCircuitBreaker_ConcurrentCallsRespectInvariants(t *testing.T) {
	clock := NewFakeClock(time.Now())
	cfg := BreakerConfig{
		Name:             "concurrent",
		FailureThreshold: 5,
		CooldownDuration: 1 * time.Minute,
		Clock:            clock,
		// Persist: nil para reducir ruido
	}
	b := NewCircuitBreaker(cfg)

	const goroutines = 50
	const callsPerGoroutine = 20

	var wg sync.WaitGroup
	var successes atomic.Int32
	var failures atomic.Int32
	var rejections atomic.Int32

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < callsPerGoroutine; i++ {
				err := b.Call(func() error {
					// La mitad pares falla, la otra mitad OK
					if (id+i)%2 == 0 {
						return errProbeFailed
					}
					return nil
				})
				switch {
				case err == nil:
					successes.Add(1)
				case errors.Is(err, ErrCircuitOpen):
					rejections.Add(1)
				case errors.Is(err, errProbeFailed):
					failures.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	total := successes.Load() + failures.Load() + rejections.Load()
	want := int32(goroutines * callsPerGoroutine)
	if total != want {
		t.Errorf("total calls = %d, want %d", total, want)
	}

	// State final debe ser uno de los tres válidos.
	finalState := b.GetState()
	if !finalState.IsValid() {
		t.Errorf("final state is not valid: %s", finalState)
	}
}
