// storage_clock.go — Abstracción del reloj para hacer testeables los
// loops temporales (reconciler, schedulers).
//
// En producción usamos RealClock que delega en el time package.
// En tests usamos FakeClock que permite avanzar el tiempo manualmente,
// evitando time.Sleep() y tests flaky.
//
// Patrón estándar en Go. Inspirado en clockwork (jonboulle/clockwork)
// y k8s.io/utils/clock, simplificado a lo que necesitamos.

package main

import (
	"sync"
	"time"
)

// Clock abstrae el acceso al tiempo. Permite que el código que depende
// de "ahora" o de tickers sea testeable sin esperar segundos reales.
type Clock interface {
	// Now devuelve el momento actual.
	Now() time.Time

	// NewTicker devuelve un ticker que dispara con la frecuencia dada.
	// El caller debe llamar a Stop() para liberar recursos.
	NewTicker(d time.Duration) Ticker
}

// Ticker abstrae time.Ticker.
type Ticker interface {
	// C es el canal por el que llegan los tics.
	C() <-chan time.Time
	// Stop libera recursos. Idempotente.
	Stop()
}

// ─────────────────────────────────────────────────────────────────────────────
// RealClock — implementación de producción
// ─────────────────────────────────────────────────────────────────────────────

// RealClock usa el time package estándar.
type RealClock struct{}

// NewRealClock crea un reloj real.
func NewRealClock() *RealClock { return &RealClock{} }

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct {
	t *time.Ticker
}

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// ─────────────────────────────────────────────────────────────────────────────
// FakeClock — implementación para tests
// ─────────────────────────────────────────────────────────────────────────────

// FakeClock controla manualmente el tiempo. Los tests llaman a Advance()
// para hacer pasar el tiempo y disparar los tickers.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

// NewFakeClock crea un reloj falso con el momento inicial dado.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *FakeClock) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{
		ch:       make(chan time.Time, 1),
		interval: d,
		next:     f.now.Add(d),
	}
	f.tickers = append(f.tickers, t)
	return t
}

// Advance hace que el reloj avance por la duración dada. Si esto
// cruza el próximo tick de algún ticker, ese ticker dispara.
//
// El test llama a Advance(30s) y luego espera a que el loop procese
// el tic. Para sincronizar, el test puede usar un canal que el
// callback del loop señaliza tras cada iteración.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	tickers := append([]*fakeTicker(nil), f.tickers...)
	now := f.now
	f.mu.Unlock()

	// Disparar tickers que tocaba disparar. Bloqueo fuera del Lock para
	// que el receptor del tic pueda interactuar con FakeClock sin
	// deadlock.
	for _, t := range tickers {
		t.mu.Lock()
		shouldFire := !t.stopped && !t.next.After(now)
		if shouldFire {
			t.next = t.next.Add(t.interval)
		}
		t.mu.Unlock()
		if shouldFire {
			// Send no-bloqueante: si el canal está lleno (el caller no
			// drenó el tic anterior), perdemos este tic. Coherente con
			// time.Ticker estándar.
			select {
			case t.ch <- now:
			default:
			}
		}
	}
}

// SetTime fija el reloj a un momento absoluto. Útil para tests que
// quieren simular distintos puntos del calendario sin calcular deltas.
// NO dispara tickers — solo cambia el "now". Si necesitas que los
// tickers reaccionen, usa Advance.
func (f *FakeClock) SetTime(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

type fakeTicker struct {
	mu       sync.Mutex
	ch       chan time.Time
	interval time.Duration
	next     time.Time
	stopped  bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}
