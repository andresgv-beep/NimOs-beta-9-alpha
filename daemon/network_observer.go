// network_observer.go — Observer del módulo network.
//
// Cada N segundos:
//
//   1. Lee config "applied" desde DB (puertos + ddns + certs).
//   2. Llama al probe para conocer la realidad.
//   3. Compara realidad vs applied → calcula divergence_count.
//   4. Para cada divergence, llama Record*Observed (incrementa
//      observed_generation → triple-gen marca drift).
//   5. Inserta un network_observed (snapshot_type='periodic', o
//      'event' si hay divergencias nuevas detectadas).
//   6. Emite eventos a través del EventEmitter.
//   7. Mantiene un snapshot atómico para lecturas lock-free desde
//      handlers HTTP (Snapshot()).
//
// Lo que el observer NO hace en F-002:
//
//   - No verifica DDNS IP real (eso es del reconciler DDNS, F-004).
//     observed_generation de ddns se mueve cuando el reconciler vea
//     que la IP ha cambiado, no aquí.
//   - No re-aplica nada: solo observa y registra.
//   - No persiste el snapshot atómico — la DB ya guarda histórico.
//
// El observer implementa la interfaz Reconciler para integrarse en
// el ReconcilerScheduler de F-001. Tier=TierMedium: si falla, perdemos
// visibilidad pero el sistema sigue funcionando.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipos públicos
// ─────────────────────────────────────────────────────────────────────────────

// Health del módulo network: usamos las constantes globales definidas
// en nimos_health.go (HealthHealthy, HealthDegraded, HealthFailed).
// El schema CHECK de network_observed acepta estos valores + 'partial',
// 'unknown', 'stale'.

// PortDivergence reporta una diferencia entre un port en DB y el estado real.
//
// Reason es uno de:
//   - "not_listening": DB dice enabled=true pero el daemon no escucha.
//   - "config_mismatch": el daemon escucha pero en puerto/bind distinto.
//   - "unexpected_listener": DB dice enabled=false pero el daemon escucha.
type PortDivergence struct {
	PortID        string `json:"port_id"`
	Reason        string `json:"reason"`
	DesiredPort   int    `json:"desired_port"`
	DesiredBind   string `json:"desired_bind_address"`
	DesiredEnable bool   `json:"desired_enabled"`
	ActualPort    int    `json:"actual_port"`
	ActualBind    string `json:"actual_bind_address"`
	ActualListen  bool   `json:"actual_listening"`
}

// ObserverSnapshot es el último resultado completo del observer.
// Inmutable una vez creado; el observer publica nuevas instancias
// atomicamente con atomic.Pointer.
type ObserverSnapshot struct {
	ObservedAt       time.Time         `json:"observed_at"`
	Generation       int64             `json:"generation"` // monotónico, +1 cada Probe completo
	OverallHealth    HealthStatus      `json:"overall_health"`
	DivergenceCount  int               `json:"divergence_count"`
	PortDivergences  []PortDivergence  `json:"port_divergences"`
	ScanDurationMs   int64             `json:"scan_duration_ms"`
	ProbeResult      ProbeResult       `json:"-"` // raw del probe; no serializar a JSON externo
}

// ─────────────────────────────────────────────────────────────────────────────
// Observer
// ─────────────────────────────────────────────────────────────────────────────

// ObserverConfig agrupa parámetros tunables. Si Interval=0, se usa
// el default.
type ObserverConfig struct {
	Interval time.Duration
}

// DefaultObserverConfig devuelve la configuración de producción.
func DefaultObserverConfig() ObserverConfig {
	return ObserverConfig{
		Interval: 60 * time.Second,
	}
}

// NetworkObserver es el singleton del módulo network. Implementa la
// interfaz Reconciler — se registra en el ReconcilerScheduler durante
// initNetworkModule cuando esté wireado (lo haremos en su sub-paso).
type NetworkObserver struct {
	repo    *NetworkRepo
	emitter *EventEmitter
	probe   NetworkProbe
	clock   Clock
	config  ObserverConfig

	// Generación monotónica del observer (independiente de la columna
	// generation de network_observed; esa es para snapshots en DB).
	gen atomic.Int64

	// Último snapshot publicado. Lecturas lock-free vía Load.
	last atomic.Pointer[ObserverSnapshot]
}

// NewNetworkObserver construye el observer. Repo, emitter y probe son
// obligatorios. Clock nil → RealClock. Config zero → defaults.
func NewNetworkObserver(repo *NetworkRepo, emitter *EventEmitter, probe NetworkProbe, clock Clock, config ObserverConfig) (*NetworkObserver, error) {
	if repo == nil {
		return nil, fmt.Errorf("NewNetworkObserver: repo is nil")
	}
	if emitter == nil {
		return nil, fmt.Errorf("NewNetworkObserver: emitter is nil")
	}
	if probe == nil {
		return nil, fmt.Errorf("NewNetworkObserver: probe is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = DefaultObserverConfig().Interval
	}
	return &NetworkObserver{
		repo:    repo,
		emitter: emitter,
		probe:   probe,
		clock:   clock,
		config:  config,
	}, nil
}

// Snapshot devuelve el último resultado del observer, o nil si nunca
// se ha ejecutado. Lecturas lock-free — apto para handlers HTTP.
func (o *NetworkObserver) Snapshot() *ObserverSnapshot {
	return o.last.Load()
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler interface impl
// ─────────────────────────────────────────────────────────────────────────────

// Name implementa Reconciler.
func (o *NetworkObserver) Name() string { return "network_observer" }

// Tier implementa Reconciler.
func (o *NetworkObserver) Tier() ReconcilerTier { return TierMedium }

// Interval implementa Reconciler.
func (o *NetworkObserver) Interval() time.Duration { return o.config.Interval }

// Reconcile implementa Reconciler — una pasada completa.
func (o *NetworkObserver) Reconcile(ctx context.Context) error {
	return o.RunOnce(ctx)
}

// ─────────────────────────────────────────────────────────────────────────────
// RunOnce (función principal, también accesible para tests)
// ─────────────────────────────────────────────────────────────────────────────

// RunOnce ejecuta una pasada del observer:
//   - Lee config "applied" de DB.
//   - Probe del sistema.
//   - Calcula divergencias.
//   - Persiste snapshot y record-observed para cada divergencia.
//   - Emite eventos.
//   - Actualiza el atomic.Pointer.
//
// Devuelve error si DB falla; nunca paniquea con datos sucios.
func (o *NetworkObserver) RunOnce(ctx context.Context) error {
	start := o.clock.Now()

	// 1) Leer "applied" config.
	ports, err := o.repo.ListPorts(ctx)
	if err != nil {
		return fmt.Errorf("observer: list ports: %w", err)
	}

	// 2) Construir inputs del probe.
	portInputs := make([]PortProbeInput, 0, len(ports))
	for _, p := range ports {
		portInputs = append(portInputs, PortProbeInput{ID: p.ID})
	}

	probeRes := o.probe.Probe(portInputs)

	// 3) Analizar divergencias.
	portDivs := analyzePortDivergences(ports, probeRes.Ports)

	// 4) Computar health + métricas agregadas.
	divergenceCount := len(portDivs)
	health := computeHealth(portDivs)

	// 5) Detectar drift y persistir Record*Observed para cada divergencia.
	if err := o.recordDrifts(ctx, portDivs); err != nil {
		// Si Record* falla, NO abortamos la pasada — todavía queremos
		// publicar el snapshot. Log y seguimos.
		logMsg("observer: record drifts: %v", err)
	}

	scanMs := o.clock.Now().Sub(start).Milliseconds()

	// 6) Persistir snapshot en network_observed.
	snapshotType := "periodic"
	if divergenceCount > 0 {
		snapshotType = "event"
	}
	if err := o.persistSnapshot(ctx, probeRes, portDivs,
		health, divergenceCount, scanMs, snapshotType); err != nil {
		// Log y continuamos — el snapshot atómico en memoria sí se publica.
		logMsg("observer: persist snapshot: %v", err)
	}

	// 7) Emitir eventos para nuevas divergencias.
	o.emitDivergenceEvents(ctx, portDivs, health)

	// 8) Publicar snapshot atómico.
	o.gen.Add(1)
	snap := &ObserverSnapshot{
		ObservedAt:      probeRes.ProbedAt,
		Generation:      o.gen.Load(),
		OverallHealth:   health,
		DivergenceCount: divergenceCount,
		PortDivergences: portDivs,
		ScanDurationMs:  scanMs,
		ProbeResult:     probeRes,
	}
	o.last.Store(snap)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Análisis de divergencias
// ─────────────────────────────────────────────────────────────────────────────

// analyzePortDivergences compara cada port en DB (con applied_generation)
// contra lo que el probe vio.
//
// IMPORTANTE: solo evaluamos ports que tienen applied_generation > 0.
// Si applied=0 significa que el reconciler de F-004+ aún no ha aplicado
// nada — no es divergence, es estado inicial. (Hoy F-002 no tiene
// reconciler de ports todavía, así que en práctica todos los ports
// estarán applied=0 y NO generarán divergence. Es lo correcto.)
func analyzePortDivergences(ports []*NetworkPort, probed []ProbedPort) []PortDivergence {
	probedByID := make(map[string]ProbedPort, len(probed))
	for _, pp := range probed {
		probedByID[pp.ID] = pp
	}

	out := make([]PortDivergence, 0)
	for _, p := range ports {
		if p.Convergence.Applied == 0 {
			// Nunca se ha aplicado — no hay nada que divergir.
			continue
		}
		pp, found := probedByID[p.ID]
		actualListen := found && pp.Listening

		if p.Enabled && !actualListen {
			out = append(out, PortDivergence{
				PortID:        p.ID,
				Reason:        "not_listening",
				DesiredPort:   p.Port,
				DesiredBind:   p.BindAddress,
				DesiredEnable: true,
				ActualListen:  false,
			})
			continue
		}
		if !p.Enabled && actualListen {
			out = append(out, PortDivergence{
				PortID:        p.ID,
				Reason:        "unexpected_listener",
				DesiredEnable: false,
				ActualPort:    pp.Port,
				ActualBind:    pp.BindAddress,
				ActualListen:  true,
			})
			continue
		}
		if p.Enabled && actualListen {
			if pp.Port != p.Port || pp.BindAddress != p.BindAddress {
				out = append(out, PortDivergence{
					PortID:        p.ID,
					Reason:        "config_mismatch",
					DesiredPort:   p.Port,
					DesiredBind:   p.BindAddress,
					DesiredEnable: true,
					ActualPort:    pp.Port,
					ActualBind:    pp.BindAddress,
					ActualListen:  true,
				})
			}
		}
	}
	return out
}

// computeHealth deriva el estado global. Reglas mínimas (DISCIPLINE §1
// — sin anticipación):
//
//   - critical si hay cualquier port divergence con reason='not_listening'
//     (la web no carga).
//   - degraded si hay otras divergencias.
//   - healthy si nada de lo anterior.
//
// Estas reglas se ajustarán cuando F-006 (diagnose API) defina las suyas.
func computeHealth(portDivs []PortDivergence) HealthStatus {
	for _, d := range portDivs {
		if d.Reason == "not_listening" {
			return HealthFailed
		}
	}
	if len(portDivs) > 0 {
		return HealthDegraded
	}
	return HealthHealthy
}

// ─────────────────────────────────────────────────────────────────────────────
// Persistencia
// ─────────────────────────────────────────────────────────────────────────────

// recordDrifts incrementa observed_generation para cada entidad
// divergente. Una sola transacción.
func (o *NetworkObserver) recordDrifts(ctx context.Context, portDivs []PortDivergence) error {
	if len(portDivs) == 0 {
		return nil
	}
	tx, err := o.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, d := range portDivs {
		if err = o.repo.RecordPortObserved(ctx, tx, d.PortID); err != nil {
			return fmt.Errorf("RecordPortObserved %s: %w", d.PortID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// persistSnapshot escribe una fila en network_observed con los
// agregados de esta pasada. Una sola transacción.
func (o *NetworkObserver) persistSnapshot(
	ctx context.Context,
	probeRes ProbeResult,
	portDivs []PortDivergence,
	health HealthStatus, divergenceCount int,
	scanMs int64, snapshotType string,
) error {
	data, err := json.Marshal(map[string]interface{}{
		"ports":            probeRes.Ports,
		"port_divergences": portDivs,
	})
	if err != nil {
		return fmt.Errorf("marshal snapshot data: %w", err)
	}

	tx, err := o.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	snap := &NetworkObservedSnapshot{
		SnapshotAt:      probeRes.ProbedAt,
		SnapshotType:    snapshotType,
		SnapshotData:    data,
		OverallHealth:   string(health),
		DivergenceCount: divergenceCount,
		ScanDurationMs:  scanMs,
	}
	if err = o.repo.CreateObservedSnapshot(ctx, tx, snap); err != nil {
		return fmt.Errorf("CreateObservedSnapshot: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// emitDivergenceEvents emite un evento por cada divergence + un evento
// resumen de salud global. Best-effort: si Emit falla por rate limit
// o por DB, lo registramos pero no abortamos.
func (o *NetworkObserver) emitDivergenceEvents(ctx context.Context, portDivs []PortDivergence, health HealthStatus) {
	for _, d := range portDivs {
		_, err := o.emitter.Emit(ctx, EventInput{
			Category: CategoryPort,
			Event:    "port_divergence",
			TargetID: &d.PortID,
			Level:    EventLevelWarn,
			Message:  fmt.Sprintf("Port %s divergence: %s", d.PortID, d.Reason),
		})
		if err != nil {
			logMsg("observer: emit port divergence: %v", err)
		}
	}

	// Resumen de salud: solo si cambió o si es crítica (info en healthy
	// es ruido — dejamos que la dedupe lo absorba con la ventana de 5min).
	if health == HealthFailed {
		_, err := o.emitter.Emit(ctx, EventInput{
			Category: CategoryObserver,
			Event:    "overall_health_critical",
			Level:    EventLevelError,
			Message:  "Network module health is critical",
		})
		if err != nil {
			logMsg("observer: emit health critical: %v", err)
		}
	}
}
