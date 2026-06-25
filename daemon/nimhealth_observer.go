// nimhealth_observer.go — Implementación del Reconciler para NimHealth.
//
// Patrón Beta 8.1 (Network module v4): el observer implementa la interfaz
// Reconciler y se registra en un ReconcilerScheduler propio. Cero
// goroutines ad-hoc, cero sleep mágicos, cero Start/Stop manuales.
//
// El observer hace UNA pasada cada Interval() segundos:
//
//   1. runAutoRegister(ctx)        · detectores auto-registran instances
//   2. reconcileAllInstances(ctx)  · syncs status/health de cada instance
//   3. refreshDockerCache(ctx)     · pobla la cache para el handler HTTP
//
// La cache de Docker (`dockerCache`) es global porque la lee el handler
// HTTP en /api/services. Usa `sync.RWMutex` para concurrent access.
// (Decisión §4.1: NO atomic.Pointer porque permite mutación parcial
// más natural que el snapshot inmutable.)

package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// DockerAppCache · snapshot en memoria del último docker ps -a
//
// Vive solo en memoria · NO se persiste en SQLite (disciplina §2:
// recomputable trivialmente en ~150ms al boot).
// ═══════════════════════════════════════════════════════════════════════

type DockerAppCache struct {
	mu          sync.RWMutex
	statuses    []DockerAppStatus
	orphans     int
	aggHealth   HealthStatus // healthy/degraded/failed/partial/unknown/stale
	updatedAt   time.Time
	initialized bool
}

// dockerCache · única instancia global. Léase desde handler, escríbase
// desde refreshDockerCache (llamada por el observer).
var dockerCache DockerAppCache

// nimhealthScheduler · ReconcilerScheduler global del módulo NimHealth.
// Se inicializa en main.go durante el boot. Es independiente del
// networkReconcilers (disciplina §1: no acoplamiento entre módulos).
var nimhealthScheduler *ReconcilerScheduler

// ═══════════════════════════════════════════════════════════════════════
// NimHealthObserver · implementa Reconciler (interfaz F-004)
// ═══════════════════════════════════════════════════════════════════════

// NimHealthObserverConfig agrupa la configuración inyectable.
type NimHealthObserverConfig struct {
	// Interval entre ticks del observer. Default: 30s.
	Interval time.Duration
}

// DefaultNimHealthConfig devuelve config con valores por defecto.
func DefaultNimHealthConfig() NimHealthObserverConfig {
	return NimHealthObserverConfig{
		Interval: 30 * time.Second,
	}
}

// NimHealthObserver observa estado de services + Docker periódicamente.
//
// Implementa la interfaz Reconciler de network_reconciler.go:
//
//	Name() string
//	Tier() ReconcilerTier
//	Interval() time.Duration
//	Reconcile(ctx context.Context) error
//
// Se registra en un ReconcilerScheduler propio (nimhealthScheduler) que
// vive en main.go · NO acopla con networkReconcilers.
type NimHealthObserver struct {
	clock  Clock
	config NimHealthObserverConfig
}

// NewNimHealthObserver crea un observer. Clock inyectable para tests
// con FakeClock.
func NewNimHealthObserver(clock Clock, config NimHealthObserverConfig) *NimHealthObserver {
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = 30 * time.Second
	}
	return &NimHealthObserver{
		clock:  clock,
		config: config,
	}
}

// ── Reconciler interface ────────────────────────────────────────────────

func (o *NimHealthObserver) Name() string            { return "nimhealth_observer" }
func (o *NimHealthObserver) Tier() ReconcilerTier    { return TierMedium }
func (o *NimHealthObserver) Interval() time.Duration { return o.config.Interval }

// Reconcile ejecuta una pasada completa del observer. Llamada por
// ReconcilerScheduler periódicamente.
//
// Si alguna sub-pasada falla, se loggea y continúa con las demás.
// Razón: queremos que un fallo de Docker NO impida reconciliar
// servicios systemd, y viceversa.
//
// La función NUNCA panics · errores se devuelven o se loggean.
func (o *NimHealthObserver) Reconcile(ctx context.Context) error {
	// 1. Auto-registro de servicios detectados (no presentes aún en DB)
	runAutoRegister(ctx)

	// 2. Reconciliación de status/health de cada instance.
	//    Esta función vive en services.go (reconcileServices) y se
	//    mantiene compatible para no romper callers existentes.
	reconcileServices()

	// 3. NORMA 1 de Docker · reconciliar su seguridad cada ciclo.
	//    - Si el pool de Docker NO está montado → detener Docker (no escribir
	//      en el disco de sistema).
	//    - Si el pool VOLVIÓ a estar disponible y Docker está parado →
	//      rearrancarlo. Esto es lo que hace que, tras corregir el pool, Docker
	//      "vuelva a encenderse" solo sin intervención manual.
	if isDockerInstalledGo() {
		if !ensureDockerSafeOrStop() {
			// No es seguro: ya se detuvo. Nada más que hacer este ciclo.
		} else {
			// Es seguro. Si Docker estaba parado (lo paramos antes), arrancarlo.
			ensureDockerStartedIfSafe()
		}
	}

	// 4. Refresh de cache Docker (lo lee el handler /api/services)
	refreshDockerCache(ctx)

	return nil
}

// ═══════════════════════════════════════════════════════════════════════
// refreshDockerCache · ejecuta docker ps -a una vez y escribe la cache.
//
// Coste medido en Pi 4: ~80-150ms · CONSTANTE con N apps (no hace inspect
// por container).
// ═══════════════════════════════════════════════════════════════════════

func refreshDockerCache(_ context.Context) {
	if !isDockerInstalledGo() {
		// Docker no disponible · cache vacía pero marcada inicializada
		dockerCache.mu.Lock()
		dockerCache.statuses = []DockerAppStatus{}
		dockerCache.orphans = 0
		dockerCache.aggHealth = HealthUnknown
		dockerCache.updatedAt = time.Now()
		dockerCache.initialized = true
		dockerCache.mu.Unlock()
		return
	}

	// Localizar la instance de Docker (única con AppID="containers")
	instances, err := dbServiceListWithTimeout("")
	if err != nil {
		logMsg("nimhealth: refreshDockerCache list failed: %v", err)
		return
	}
	var dockerInstanceID string
	for _, inst := range instances {
		if inst.AppID == "containers" {
			dockerInstanceID = inst.ID
			break
		}
	}
	if dockerInstanceID == "" {
		// No hay instance Docker registrada · nada que cachear todavía.
		// (Puede ser que Docker engine no esté en un pool o no instalado)
		return
	}

	statuses, orphans := getDockerAppStatuses(dockerInstanceID)
	aggHealth := ComputeDockerAggregateHealth(statuses)

	dockerCache.mu.Lock()
	dockerCache.statuses = statuses
	dockerCache.orphans = orphans
	dockerCache.aggHealth = aggHealth
	dockerCache.updatedAt = time.Now()
	dockerCache.initialized = true
	dockerCache.mu.Unlock()
}

// ForceDockerCacheRefresh · APP-034 · invalidación de cache post-operación.
//
// Wrapper sobre refreshDockerCache pensado para ser llamado desde handlers
// HTTP del módulo Docker tras operaciones que modifican el estado real:
//
//   - dockerStackDeploy / dockerContainerCreate (tras OK)
//   - dockerContainerDelete / dockerStackDelete (tras cleanup)
//   - dockerContainerAction (start/stop/restart)
//
// Sin esto, el observer espera hasta 30s para reflejar el cambio en /api/services,
// lo cual produce ventana visible de inconsistencia para el usuario.
//
// Es safe llamarlo concurrentemente con el tick natural del observer · el
// RWMutex de dockerCache serializa las escrituras.
//
// La llamada es BLOQUEANTE (~80-150ms en Pi 4). Si el handler necesita devolver
// respuesta HTTP rápida y la frescura de cache no es crítica, invocar en goroutine:
//
//	go ForceDockerCacheRefresh(context.Background())
func ForceDockerCacheRefresh(ctx context.Context) {
	logMsg("nimhealth: forced cache refresh requested")
	refreshDockerCache(ctx)
}

// ═══════════════════════════════════════════════════════════════════════
// enrichDockerInstance · helper que añade children + reasonCode al map
// del Docker engine en el response de /api/services.
//
// Llamado desde nimhealth.go::handleServiceRoutes.
// ═══════════════════════════════════════════════════════════════════════

func enrichDockerInstance(result map[string]interface{}, dockerInstalled, inGrace bool) {
	if !dockerInstalled {
		result["children"] = []map[string]interface{}{}
		result["orphanCount"] = 0
		result["health"] = string(HealthUnknown)
		result["reasonCode"] = ReasonDockerUnavailable
		return
	}

	dockerCache.mu.RLock()
	cacheInit := dockerCache.initialized
	cacheUpdated := dockerCache.updatedAt
	cacheStatuses := dockerCache.statuses
	cacheOrphans := dockerCache.orphans
	cacheAggHealth := dockerCache.aggHealth
	dockerCache.mu.RUnlock()

	if !cacheInit {
		// Cache aún no poblada · primer tick del observer pendiente.
		reason := ReasonInitializing
		if inGrace {
			reason = ReasonBootGracePeriod
		}
		result["children"] = []map[string]interface{}{}
		result["orphanCount"] = 0
		result["health"] = string(HealthUnknown)
		result["reasonCode"] = reason
		return
	}

	// Stale detection · si última observación > 5×interval (~150s)
	// reportamos como stale para que la UI muestre advertencia.
	stale := time.Since(cacheUpdated) > 5*30*time.Second

	childrenMaps := make([]map[string]interface{}, len(cacheStatuses))
	for j, c := range cacheStatuses {
		childrenMaps[j] = c.ToMap()
	}
	result["children"] = childrenMaps
	result["orphanCount"] = cacheOrphans

	effectiveHealth := cacheAggHealth
	if stale {
		effectiveHealth = HealthStale
	}
	result["health"] = string(effectiveHealth)

	// Adjuntar reasonCode solo si el estado no es completamente OK
	switch {
	case stale:
		result["reasonCode"] = ReasonStale
	case inGrace && cacheAggHealth == HealthDegraded:
		// Durante grace period, suprimimos el degraded reportando
		// boot_grace_period. El frontend renderizará gris en lugar
		// de amarillo de degraded.
		result["health"] = string(HealthUnknown)
		result["reasonCode"] = ReasonBootGracePeriod
	case cacheAggHealth == HealthDegraded:
		result["reasonCode"] = ReasonDegradedChildren
	}
	_ = fmt.Sprintf // keep fmt import alive if compiler asks
}
