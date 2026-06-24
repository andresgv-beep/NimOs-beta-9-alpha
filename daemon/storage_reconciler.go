// storage_reconciler.go — Reconciliación periódica del estado físico
// de devices contra la DB.
//
// Dos operaciones:
//
//   1. ReconcileDevicesAtBoot — UNA VEZ al arranque del daemon.
//      Ejecuta ScanDevices y deja last_seen_at actualizado.
//      Útil para detectar discos nuevos enchufados durante el reboot.
//
//   2. StartDeviceReconcilerLoop — loop EN BACKGROUND.
//      Cada N segundos (default 30s):
//        a) Ejecuta ScanDevices (actualiza last_seen_at de los presentes)
//        b) Marca como "missing" los devices que llevan M ciclos sin verse
//        c) Marca como "detected" los devices missing que vuelven a aparecer
//
// El estado "missing" es PROYECTADO, no una columna. Se calcula a partir
// de last_seen_at vs now. Si now - last_seen_at > threshold → missing.
// Esto evita race conditions de "marcar/desmarcar missing".
//
// see docs/storage_state_machines.md §5 (Device lifecycle)

package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// ReconcilerConfig controla el comportamiento del loop.
type ReconcilerConfig struct {
	// Interval entre ciclos de scan. Default 30s.
	Interval time.Duration
	// MissingThreshold es el tiempo sin ver un device antes de
	// considerarlo missing. Default 3x Interval (3 ciclos perdidos).
	MissingThreshold time.Duration
}

// DefaultReconcilerConfig devuelve los defaults razonables.
func DefaultReconcilerConfig() ReconcilerConfig {
	return ReconcilerConfig{
		Interval:         30 * time.Second,
		MissingThreshold: 90 * time.Second,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler
// ─────────────────────────────────────────────────────────────────────────────

// DeviceReconciler gestiona el loop de reconciliación. Su lifecycle:
//   - NewDeviceReconciler(...)
//   - Start(ctx)         → arranca el loop en background
//   - Stop()             → para el loop, espera a que el ciclo en curso termine
type DeviceReconciler struct {
	service *StorageService
	clock   Clock
	config  ReconcilerConfig

	mu      sync.Mutex
	running bool
	stopCh  chan struct{} // cerrado por Stop() para sacar al loop del select
	done    chan struct{} // cerrado por el loop al salir
	// onCycleComplete (si != nil) se llama al final de cada ciclo. Solo
	// para tests, permite al test esperar a que un ciclo termine sin
	// time.Sleep.
	onCycleComplete func()

	// P2 — detección de reaparición de devices (missing→present).
	// prevMissing guarda los serials que estaban missing al final del ciclo
	// anterior. Cuando un serial sale de ese set (reaparece tras spin-up USB),
	// se dispara onDeviceReappear para remontar el pool. nil hasta el primer
	// ciclo (no disparamos en el arranque en frío).
	prevMissing map[string]bool
	// onDeviceReappear (si != nil) se llama cuando ≥1 device pasa de
	// missing→present. Inyectable para tests; en producción remonta pools.
	onDeviceReappear func(ctx context.Context, reappeared []*Device)
}

// NewDeviceReconciler crea el reconciler con sus dependencias.
func NewDeviceReconciler(service *StorageService, clock Clock, config ReconcilerConfig) *DeviceReconciler {
	if config.Interval == 0 {
		config = DefaultReconcilerConfig()
	}
	if config.MissingThreshold == 0 {
		config.MissingThreshold = config.Interval * 3
	}
	return &DeviceReconciler{
		service: service,
		clock:   clock,
		config:  config,
	}
}

// Start arranca el loop. Idempotente: una segunda llamada es no-op.
// Devuelve cuando el loop ha registrado su ticker (los Advance del
// FakeClock posteriores ya verán este ticker).
func (r *DeviceReconciler) Start(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.done = make(chan struct{})
	stopCh := r.stopCh
	done := r.done
	r.mu.Unlock()

	// Esperamos a que el loop confirme que ha creado su ticker.
	// Esto evita una race en tests: si Advance() corre antes de que
	// el ticker exista, el tick se pierde.
	tickerReady := make(chan struct{})
	go r.loop(ctx, stopCh, done, tickerReady)
	<-tickerReady
}

// Stop para el loop. Bloquea hasta que el ciclo en curso (si lo hay)
// termine. Idempotente.
func (r *DeviceReconciler) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	stopCh := r.stopCh
	done := r.done
	r.stopCh = nil
	r.done = nil
	r.mu.Unlock()

	// Señalizar al loop que pare
	close(stopCh)
	// Esperar a que confirme su salida
	<-done
}

// loop es el corazón del reconciler. Ejecuta ciclos a intervalos
// regulares hasta que el contexto se cancele, Stop() se llame, o
// el canal stopCh se cierre.
func (r *DeviceReconciler) loop(ctx context.Context, stopCh <-chan struct{}, done chan<- struct{}, tickerReady chan<- struct{}) {
	ticker := r.clock.NewTicker(r.config.Interval)
	defer ticker.Stop()
	defer close(done)

	// Señalizar a Start que el ticker está registrado
	close(tickerReady)

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-ticker.C():
			if err := r.runCycle(ctx); err != nil {
				logMsg("DeviceReconciler: cycle error: %v", err)
			}

			r.mu.Lock()
			cb := r.onCycleComplete
			r.mu.Unlock()
			if cb != nil {
				cb()
			}
		}
	}
}

// runCycle ejecuta UN ciclo de reconciliación. Llamable también desde
// tests para forzar un ciclo sin esperar al ticker.
func (r *DeviceReconciler) runCycle(ctx context.Context) error {
	_, err := r.service.ScanDevices(ctx)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	// P2 — detectar devices que pasan de missing→present (reaparición).
	// El escenario real: corte de luz → la Pi bootea antes de que el HDD USB
	// termine su spin-up → el mount inicial falla → el disco aparece 40s
	// después. Aquí lo vemos reaparecer y remontamos el pool, en vez de
	// dejarlo desmontado para siempre.
	missing, mErr := r.MissingDevices(ctx)
	if mErr != nil {
		// No es fatal para el ciclo; el scan ya actualizó last_seen_at.
		logMsg("DeviceReconciler: MissingDevices error: %v", mErr)
		return nil
	}

	currMissing := make(map[string]bool, len(missing))
	for _, d := range missing {
		if d.Serial != "" {
			currMissing[d.Serial] = true
		}
	}

	// Reaparecidos = estaban en prevMissing y ya no están en currMissing.
	// prevMissing nil = primer ciclo: solo registramos la base, sin disparar.
	if r.prevMissing != nil {
		reappearedSerials := diffReappeared(r.prevMissing, currMissing)
		if len(reappearedSerials) > 0 && r.onDeviceReappear != nil {
			reappeared := r.devicesBySerial(ctx, reappearedSerials)
			logMsg("DeviceReconciler: %d device(s) reaparecidos tras estar missing → remontando", len(reappeared))
			r.onDeviceReappear(ctx, reappeared)
		}
	}
	r.prevMissing = currMissing

	return nil
}

// diffReappeared devuelve los serials que estaban en prev (missing) y ya no
// están en curr (vuelven a estar presentes). Función pura para test.
func diffReappeared(prev, curr map[string]bool) []string {
	var out []string
	for serial := range prev {
		if !curr[serial] {
			out = append(out, serial)
		}
	}
	return out
}

// devicesBySerial resuelve serials a *Device leyendo la lista actual.
func (r *DeviceReconciler) devicesBySerial(ctx context.Context, serials []string) []*Device {
	want := make(map[string]bool, len(serials))
	for _, s := range serials {
		want[s] = true
	}
	all, err := r.service.ListDevices(ctx)
	if err != nil {
		return nil
	}
	var out []*Device
	for _, d := range all {
		if want[d.Serial] {
			out = append(out, d)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// MissingDevices — proyección del estado missing
// ─────────────────────────────────────────────────────────────────────────────

// MissingDevices devuelve los devices que no se han visto en más de
// config.MissingThreshold tiempo. Estado proyectado, no consulta nueva
// al hardware.
//
// Esta función es PURA respecto a hardware (no escanea), solo lee la DB.
func (r *DeviceReconciler) MissingDevices(ctx context.Context) ([]*Device, error) {
	all, err := r.service.ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("MissingDevices: %w", err)
	}
	now := r.clock.Now()
	threshold := r.config.MissingThreshold

	var missing []*Device
	for _, d := range all {
		if d.LastSeenAt.IsZero() {
			// No tiene last_seen_at registrado → no podemos clasificar
			continue
		}
		if now.Sub(d.LastSeenAt) > threshold {
			missing = append(missing, d)
		}
	}
	return missing, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Boot reconciliation — una vez al arranque
// ─────────────────────────────────────────────────────────────────────────────

// ReconcileDevicesAtBoot ejecuta un scan inicial al arrancar el daemon.
// Idempotente. Si falla, loggea el error y permite al caller decidir si
// abortar o continuar.
//
// Razón para ejecutar al boot: entre apagar y encender el sistema pueden
// haber enchufado/desenchufado discos. Sin este boot reconciliation, el
// daemon no sabe del nuevo hardware hasta el primer ciclo del loop
// (default 30s después).
func (s *StorageService) ReconcileDevicesAtBoot(ctx context.Context) error {
	logMsg("Boot reconciliation: scanning devices...")
	result, err := s.ScanDevices(ctx)
	if err != nil {
		return fmt.Errorf("ReconcileDevicesAtBoot: %w", err)
	}
	logMsg("Boot reconciliation: total=%d inserted=%d updated=%d",
		result.Total, result.Inserted, result.Updated)
	return nil
}
