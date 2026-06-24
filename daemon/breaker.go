// breaker.go — Circuit breaker pattern (NimOS core, módulo global).
//
// Vive en /daemon/breaker.go porque NO es específico de network. Casos
// de uso esperados, esparcidos por todos los módulos de NimOS:
//
//   · network/ddns:     duckdns, noip, dynu...
//   · network/certs:    letsencrypt, zerossl...
//   · network/public_ip: ifconfig.me, ipify...
//   · network/router:   upnp.router
//   · backup (futuro):  s3, b2
//   · apps:             docker hub
//   · notify (futuro):  pushover, telegram
//
// Diseño (NIMOS_DISCIPLINE.md §3 v2):
//
//   - El breaker NO conoce SQLite. La persistencia se inyecta vía callback.
//   - El breaker NO expone HealthStatus, solo CircuitState (DISCIPLINE §8).
//     El observer del servicio que use este breaker traduce State → Health.
//   - Persistencia lazy: el callback Persist se llama SOLO cuando el state
//     cambia (transición), nunca en cada Call(). En operación normal, ~10
//     transiciones al día por breaker.
//   - El callback se invoca FUERA del lock, así un SQLite lento no
//     serializa Calls de la misma instancia.
//
// Máquina de estado:
//
//                         failures < threshold
//                          ────────────────────
//                         │                    │
//                         ▼                    │
//                ┌────────────────┐  fail   ┌──┴────────┐
//                │     CLOSED     │────────▶│   OPEN    │
//                │ (funcionando)  │         │ (cooldown)│
//                └────────────────┘         └──┬────────┘
//                         ▲                    │
//                         │              cooldown
//                  success│              expired
//                         │                    │
//                         │                    ▼
//                ┌────────┴─────────────────────────┐
//                │            HALF_OPEN             │
//                │  (probando, fn() puede ejecutar) │
//                └──────────────────────────────────┘
//
// Recuperación al boot (Persist asistido por Restore en boot del daemon):
//   - state=open  y next_retry > now  → respetar cooldown
//   - state=open  y next_retry <= now → arrancar half_open
//   - state=half_open                 → arrancar closed (asumimos que
//                                       durante el downtime se resolvió)

package main

import (
	"errors"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// CircuitState es el estado interno del breaker. NO se expone como
// HealthStatus a la UI (NIMOS_DISCIPLINE.md §8) — esa traducción la hace
// el observer del servicio que use este breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// IsValid devuelve true si el string es un CircuitState conocido.
// Útil al deserializar desde SQLite.
func (s CircuitState) IsValid() bool {
	switch s {
	case CircuitClosed, CircuitOpen, CircuitHalfOpen:
		return true
	}
	return false
}

// ErrCircuitOpen se devuelve cuando Call() es invocado pero el breaker
// está open y el cooldown no ha expirado. El caller NO debería reintentar
// inmediatamente — debería esperar al menos hasta el next_retry_at del
// snapshot. La idea es que el reconciler que usa el breaker se saltará
// el ciclo cuando vea este error.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// PersistFunc es el callback de persistencia lazy. Se llama SOLO cuando
// el state del breaker cambia. nextRetry es non-nil solo si state=open.
//
// El callback puede devolver error pero el breaker NO lo propaga al
// caller de Call() — un fallo de persistencia no debe romper la lógica
// del circuit breaker. Si el daemon crashea entre la transición y la
// persistencia, al reboot el breaker volverá a CLOSED (best-effort) y
// re-aprenderá del estado real del provider externo.
type PersistFunc func(name string, state CircuitState, nextRetry *time.Time) error

// BreakerConfig controla el comportamiento del breaker.
type BreakerConfig struct {
	// Nombre único del breaker. Sirve como clave en el registry global y
	// en la tabla nimos_breakers. Convención: "servicio" o "categoría.servicio"
	// (ej: "duckdns", "backup.s3", "notify.pushover").
	Name string

	// Número de fallos consecutivos antes de pasar a OPEN. Default 5.
	FailureThreshold int

	// Duración del cooldown antes de pasar de OPEN a HALF_OPEN. Default 5min.
	CooldownDuration time.Duration

	// Reloj inyectable (para tests). Default RealClock.
	Clock Clock

	// Callback de persistencia. Si nil, el breaker funciona pero su estado
	// no sobrevive a reinicios del daemon (válido para tests).
	Persist PersistFunc
}

// DefaultBreakerConfig devuelve los defaults razonables para un breaker.
// El caller solo necesita rellenar Name (obligatorio) y Persist (opcional).
func DefaultBreakerConfig(name string) BreakerConfig {
	return BreakerConfig{
		Name:             name,
		FailureThreshold: 5,
		CooldownDuration: 5 * time.Minute,
		Clock:            NewRealClock(),
	}
}

// CircuitBreaker es la implementación. Thread-safe. Un breaker se puede
// compartir entre múltiples goroutines sin protección externa.
//
// Limitación conocida: en estado HALF_OPEN, múltiples Calls concurrentes
// pueden colarse antes de que el primero termine. En NimOS esto es
// aceptable porque los breakers se invocan típicamente desde un único
// reconciler (DDNS, Cert) y la concurrencia real es rara. Si en el
// futuro algún servicio necesita HALF_OPEN estricto (1 call max),
// añadiremos un semaphore separado — pero no antes de tener el caso.
type CircuitBreaker struct {
	cfg BreakerConfig

	mu        sync.Mutex
	state     CircuitState
	failures  int
	nextRetry time.Time // válido solo si state == CircuitOpen
}

// NewCircuitBreaker crea un breaker en estado CLOSED con defaults
// aplicados a campos no rellenos.
//
// Para restaurar el state desde persistencia al boot, usar
// NewCircuitBreakerWithState.
func NewCircuitBreaker(cfg BreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.CooldownDuration <= 0 {
		cfg.CooldownDuration = 5 * time.Minute
	}
	if cfg.Clock == nil {
		cfg.Clock = NewRealClock()
	}
	return &CircuitBreaker{
		cfg:   cfg,
		state: CircuitClosed,
	}
}

// NewCircuitBreakerWithState crea un breaker con un estado inicial
// restaurado desde persistencia. Aplica las reglas de recuperación
// definidas en NIMOS_DISCIPLINE.md §3 v2:
//
//   - savedState=open, savedNextRetry > now  → mantener open
//   - savedState=open, savedNextRetry <= now → arrancar half_open
//   - savedState=half_open                   → arrancar closed
//   - savedState=closed                      → arrancar closed
//
// Si savedState no es un CircuitState válido, arranca closed como
// fallback defensivo (no debería pasar si la tabla tiene su CHECK).
func NewCircuitBreakerWithState(cfg BreakerConfig, savedState CircuitState, savedNextRetry *time.Time) *CircuitBreaker {
	b := NewCircuitBreaker(cfg)
	now := b.cfg.Clock.Now()

	switch savedState {
	case CircuitOpen:
		if savedNextRetry != nil && savedNextRetry.After(now) {
			b.state = CircuitOpen
			b.nextRetry = *savedNextRetry
		} else {
			// Cooldown expirado durante el downtime del daemon.
			// Empezamos en half_open para probar el provider.
			b.state = CircuitHalfOpen
		}
	case CircuitHalfOpen:
		// El daemon se cayó mientras probaba el recovery. Asumimos
		// que el provider pudo recuperarse durante el downtime y
		// empezamos limpio.
		b.state = CircuitClosed
	default:
		// CircuitClosed o estado inválido → closed.
		b.state = CircuitClosed
	}
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

// Call ejecuta fn() respetando el estado del breaker.
//
//   - state CLOSED:    ejecuta fn(), contabiliza el resultado.
//   - state OPEN:      rechaza con ErrCircuitOpen sin invocar fn(), salvo
//                      que el cooldown haya expirado (en cuyo caso pasa
//                      a HALF_OPEN y prueba).
//   - state HALF_OPEN: ejecuta fn(). Si OK → CLOSED. Si KO → OPEN con
//                      cooldown reseteado.
//
// El callback Persist se invoca fuera del lock, así que un SQLite lento
// no serializa Calls. El error del callback se descarta (best-effort).
func (b *CircuitBreaker) Call(fn func() error) error {
	allowed, transitionPre := b.beforeCall()
	if transitionPre != nil {
		b.persist(*transitionPre)
	}
	if !allowed {
		return ErrCircuitOpen
	}

	err := fn()

	transitionPost := b.afterCall(err)
	if transitionPost != nil {
		b.persist(*transitionPost)
	}
	return err
}

// GetState devuelve el state actual del breaker. NO devuelve HealthStatus
// (eso lo hace el observer del servicio que usa el breaker).
func (b *CircuitBreaker) GetState() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Snapshot devuelve una vista inmutable del estado actual del breaker.
// Útil para observers (que luego traducen a HealthStatus) y para
// persistencia inicial al registrar el breaker en el registry global.
func (b *CircuitBreaker) Snapshot() BreakerSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

// Reset fuerza el breaker a CLOSED, descartando el state actual. Útil
// para operaciones manuales del admin ("el provider está de vuelta,
// no esperes al cooldown"). Persistencia: se invoca si hubo transición.
func (b *CircuitBreaker) Reset() {
	b.mu.Lock()
	if b.state == CircuitClosed && b.failures == 0 {
		b.mu.Unlock()
		return
	}
	b.state = CircuitClosed
	b.failures = 0
	b.nextRetry = time.Time{}
	snap := b.snapshotLocked()
	b.mu.Unlock()

	b.persist(snap)
}

// BreakerSnapshot es una vista inmutable del estado del breaker.
// El JSON tag está pensado para serializar al endpoint
// /api/v4/network/observed dentro del campo Breakers[].
type BreakerSnapshot struct {
	Name      string       `json:"name"`
	State     CircuitState `json:"state"`
	NextRetry *time.Time   `json:"next_retry,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal (todos los *Locked deben llamarse con b.mu held)
// ─────────────────────────────────────────────────────────────────────────────

// beforeCall decide si la llamada puede proceder. Si state=OPEN y el
// cooldown ya expiró, transiciona a HALF_OPEN aquí mismo.
//
// Devuelve (allowed, transitionSnapshotIfAny).
func (b *CircuitBreaker) beforeCall() (bool, *BreakerSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case CircuitClosed, CircuitHalfOpen:
		return true, nil

	case CircuitOpen:
		if b.cfg.Clock.Now().Before(b.nextRetry) {
			return false, nil
		}
		// Cooldown expirado, transición a half_open para probar.
		b.state = CircuitHalfOpen
		b.nextRetry = time.Time{}
		snap := b.snapshotLocked()
		return true, &snap
	}
	return false, nil
}

// afterCall actualiza el estado tras un Call(). Devuelve un snapshot
// si hubo transición, nil si el state no cambió.
func (b *CircuitBreaker) afterCall(callErr error) *BreakerSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	if callErr != nil {
		return b.recordFailureLocked()
	}
	return b.recordSuccessLocked()
}

func (b *CircuitBreaker) recordFailureLocked() *BreakerSnapshot {
	b.failures++
	switch b.state {
	case CircuitHalfOpen:
		// El probe falló → cooldown reseteado, vuelta a open.
		b.nextRetry = b.cfg.Clock.Now().Add(b.cfg.CooldownDuration)
		b.state = CircuitOpen
		snap := b.snapshotLocked()
		return &snap

	case CircuitClosed:
		if b.failures >= b.cfg.FailureThreshold {
			b.nextRetry = b.cfg.Clock.Now().Add(b.cfg.CooldownDuration)
			b.state = CircuitOpen
			snap := b.snapshotLocked()
			return &snap
		}
	}
	return nil
}

func (b *CircuitBreaker) recordSuccessLocked() *BreakerSnapshot {
	switch b.state {
	case CircuitHalfOpen:
		// Recuperación confirmada.
		b.failures = 0
		b.state = CircuitClosed
		snap := b.snapshotLocked()
		return &snap

	case CircuitClosed:
		// Resetear contador de fallos consecutivos. No es transición.
		b.failures = 0
	}
	return nil
}

func (b *CircuitBreaker) snapshotLocked() BreakerSnapshot {
	snap := BreakerSnapshot{
		Name:  b.cfg.Name,
		State: b.state,
	}
	if b.state == CircuitOpen && !b.nextRetry.IsZero() {
		nr := b.nextRetry
		snap.NextRetry = &nr
	}
	return snap
}

// persist invoca el callback de persistencia si está configurado.
// Errores se descartan (best-effort, ver doc de PersistFunc).
func (b *CircuitBreaker) persist(snap BreakerSnapshot) {
	if b.cfg.Persist == nil {
		return
	}
	_ = b.cfg.Persist(snap.Name, snap.State, snap.NextRetry)
}
