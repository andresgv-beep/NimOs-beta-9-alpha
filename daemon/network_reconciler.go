// network_reconciler.go — Interface Reconciler + scheduler.
//
// Modelo (NIMOS_DISCIPLINE v2 §1 actualizado):
//
//   Tier (Critical/Medium/Low)  — categoría de criticidad operativa.
//   Interval (free duration)    — frecuencia de ejecución.
//
// Los dos son ORTOGONALES: un reconciler 'Critical' puede correr cada
// 60s y otro 'Critical' cada 5 min. El tier influye en la respuesta del
// daemon a fallos (en el futuro: Critical → fail boot, Medium → degraded
// mode, Low → log & continue), pero NO en la frecuencia.
//
// Modelo de ejecución:
//
//   - Cada reconciler tiene su propia goroutine con un time.Ticker.
//   - Reconcile() recibe context.Context: si el scheduler para, lo
//     cancela.
//   - Errores se loguean (vía logMsg) pero no detienen la goroutine —
//     el siguiente tick reintentará.
//   - El scheduler expone RunOnce para tests deterministas.
//
// Lo que NO incluye este scheduler (lo dejamos para iteraciones
// posteriores cuando aparezca el caso de uso):
//
//   - Backoff exponencial tras N fallos seguidos (F-004+).
//   - Fail-boot si reconciler Critical falla en arranque (F-004+).
//   - Métricas detalladas de ejecuciones (Beta 9+).
//   - Concurrency limit a través de tiers (Beta 9+).
//
// Estos son anti-patrón de abstracción anticipada (DISCIPLINE §1). Se
// añaden cuando aparezca la necesidad real, no antes.

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// ReconcilerTier
// ─────────────────────────────────────────────────────────────────────────────

// ReconcilerTier expresa la criticidad operativa del reconciler.
// Es independiente del Interval.
type ReconcilerTier int

const (
	// TierCritical: si falla, el daemon NO está operativo (ej: puertos HTTP).
	// En el futuro, fallos repetidos en Critical disparan alarmas o
	// abortan el boot.
	TierCritical ReconcilerTier = iota

	// TierMedium: si falla, hay degradación notable pero el sistema
	// sigue funcionando (ej: DDNS no actualiza, certs no renuevan).
	TierMedium

	// TierLow: si falla, el impacto es indirecto y autorrecuperable
	// (ej: UPnP, cleanup, métricas).
	TierLow
)

// String devuelve el nombre del tier para logs.
func (t ReconcilerTier) String() string {
	switch t {
	case TierCritical:
		return "critical"
	case TierMedium:
		return "medium"
	case TierLow:
		return "low"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler interface
// ─────────────────────────────────────────────────────────────────────────────

// Reconciler representa una tarea periódica que mantiene una parte del
// sistema convergiendo hacia su estado deseado.
//
// Contract:
//   - Name() es estable y único.
//   - Tier() es la criticidad operativa.
//   - Interval() es el periodo deseado entre ejecuciones. Debe ser >= 1s.
//   - Reconcile() recibe contexto cancelable. Si el contexto se cancela
//     a mitad de operación, el reconciler debe abortar limpiamente
//     (rollback parcial OK, pero no debe colgar).
//   - Devolver error es informativo: el scheduler loguea pero no para.
type Reconciler interface {
	Name() string
	Tier() ReconcilerTier
	Interval() time.Duration
	Reconcile(ctx context.Context) error
}

// ─────────────────────────────────────────────────────────────────────────────
// ReconcilerScheduler
// ─────────────────────────────────────────────────────────────────────────────

// ReconcilerScheduler gestiona el ciclo de vida de un conjunto de
// reconcilers. No es thread-safe contra Register concurrente con Start;
// el daemon registra todos los reconcilers en boot, luego llama Start
// una vez.
type ReconcilerScheduler struct {
	clock Clock

	// settleDelay es la espera antes de la PASADA INICIAL de cada
	// reconciler al arrancar (default 3s: margen para que servicios
	// vecinos como Caddy/Docker terminen de levantar). Los tests lo
	// reducen para no esperar.
	settleDelay time.Duration

	mu          sync.Mutex
	reconcilers map[string]Reconciler
	running     bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewReconcilerScheduler crea un scheduler vacío. Si clock es nil,
// usa RealClock.
func NewReconcilerScheduler(clock Clock) *ReconcilerScheduler {
	if clock == nil {
		clock = NewRealClock()
	}
	return &ReconcilerScheduler{
		clock:       clock,
		settleDelay: 3 * time.Second,
		reconcilers: make(map[string]Reconciler),
	}
}

// ErrReconcilerAlreadyRegistered se devuelve si Register se llama dos
// veces con el mismo Name().
var ErrReconcilerAlreadyRegistered = errors.New("reconciler already registered")

// ErrSchedulerAlreadyRunning se devuelve si Start se llama dos veces
// sin Stop intermedio.
var ErrSchedulerAlreadyRunning = errors.New("scheduler already running")

// ErrSchedulerNotRunning se devuelve si Stop se llama sin Start previo.
var ErrSchedulerNotRunning = errors.New("scheduler not running")

// ErrReconcilerNotFound se devuelve por RunOnce si Name() no existe.
var ErrReconcilerNotFound = errors.New("reconciler not found")

// Register añade un reconciler. Falla si ya hay uno con el mismo Name()
// o si el scheduler ya está corriendo (registrar tras Start no es
// soportado — todos los reconcilers se montan en boot).
func (s *ReconcilerScheduler) Register(r Reconciler) error {
	if r == nil {
		return fmt.Errorf("Register: reconciler is nil")
	}
	if r.Name() == "" {
		return fmt.Errorf("Register: Name() must be non-empty")
	}
	if r.Interval() < time.Second {
		return fmt.Errorf("Register: Interval() must be >= 1s, got %v", r.Interval())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("Register: cannot register %q after Start", r.Name())
	}
	if _, exists := s.reconcilers[r.Name()]; exists {
		return fmt.Errorf("%w: %s", ErrReconcilerAlreadyRegistered, r.Name())
	}
	s.reconcilers[r.Name()] = r
	return nil
}

// ListReconcilers devuelve los reconcilers registrados ordenados por
// nombre. Snapshot — devuelve copia, modificarla no afecta al scheduler.
func (s *ReconcilerScheduler) ListReconcilers() []Reconciler {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Reconciler, 0, len(s.reconcilers))
	for _, r := range s.reconcilers {
		out = append(out, r)
	}
	// Ordenar por nombre para determinismo en tests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name() > out[j].Name(); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Start arranca una goroutine por cada reconciler registrado. Cada
// goroutine corre Reconcile() con un time.Ticker de Interval().
//
// El ctx pasado se propaga a todas las goroutines: si se cancela, todas
// salen limpiamente. Stop() también propaga cancelación.
//
// Idempotencia: llamar Start dos veces sin Stop devuelve
// ErrSchedulerAlreadyRunning.
func (s *ReconcilerScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrSchedulerAlreadyRunning
	}
	innerCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true

	// Snapshot del map para evitar race con Register (que ya rechazaría,
	// pero defensive).
	snapshot := make([]Reconciler, 0, len(s.reconcilers))
	for _, r := range s.reconcilers {
		snapshot = append(snapshot, r)
	}
	s.mu.Unlock()

	for _, r := range snapshot {
		s.wg.Add(1)
		go s.runReconciler(innerCtx, r)
	}
	return nil
}

// Stop cancela las goroutines y espera a que todas terminen.
// Devuelve ErrSchedulerNotRunning si Start no fue llamado.
func (s *ReconcilerScheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return ErrSchedulerNotRunning
	}
	cancel := s.cancel
	s.running = false
	s.cancel = nil
	s.mu.Unlock()

	cancel()
	s.wg.Wait()
	return nil
}

// IsRunning devuelve true si el scheduler está activo.
func (s *ReconcilerScheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// RunOnce ejecuta Reconcile de un reconciler concreto, sin iniciar el
// scheduler. Útil para tests y para tareas manuales triggered desde un
// endpoint admin.
//
// El contexto es del caller — no hay timeout implícito.
func (s *ReconcilerScheduler) RunOnce(ctx context.Context, name string) error {
	s.mu.Lock()
	r, ok := s.reconcilers[name]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrReconcilerNotFound, name)
	}
	return r.Reconcile(ctx)
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal: per-reconciler goroutine
// ─────────────────────────────────────────────────────────────────────────────

// runReconciler es el loop de una goroutine. Corre Reconcile en cada
// tick del ticker. Si el contexto se cancela, sale limpiamente. Si
// Reconcile devuelve error, lo loguea y continúa.
func (s *ReconcilerScheduler) runReconciler(ctx context.Context, r Reconciler) {
	defer s.wg.Done()
	logMsg("Reconciler %q (%s) started, interval=%v",
		r.Name(), r.Tier(), r.Interval())

	// Pasada INICIAL tras un settle corto: un reconciler declarativo debe
	// converger cuanto antes, no tras su primer intervalo completo. Sin
	// esto, el observer de certs (tier de 5 min) dejaba 5 minutos de
	// "desconocido" en la UI tras CADA reinicio del daemon. El settle de
	// 3s da margen a que los servicios vecinos (Caddy, Docker) terminen de
	// arrancar; si aún no están, el reconciler degrada con gracia y el
	// siguiente tick lo recoge.
	select {
	case <-ctx.Done():
		logMsg("Reconciler %q stopping", r.Name())
		return
	case <-time.After(s.settleDelay):
		s.runReconcileWithRecover(ctx, r)
	}

	ticker := time.NewTicker(r.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logMsg("Reconciler %q stopping", r.Name())
			return
		case <-ticker.C:
			s.runReconcileWithRecover(ctx, r)
		}
	}
}

// runReconcileWithRecover invoca Reconcile protegido contra panics —
// queremos que un panic en un reconciler no derribe la goroutine y
// detenga al resto del scheduler.
func (s *ReconcilerScheduler) runReconcileWithRecover(ctx context.Context, r Reconciler) {
	defer func() {
		if p := recover(); p != nil {
			logMsg("Reconciler %q panicked: %v", r.Name(), p)
		}
	}()
	if err := r.Reconcile(ctx); err != nil {
		logMsg("Reconciler %q failed: %v", r.Name(), err)
	}
}
