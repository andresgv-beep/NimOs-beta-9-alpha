package main

// storage_observer.go — StorageObserver: runtime truth cache + divergence analysis.
//
// Diseño completo en docs/storage_observer_design.md.
//
// Resumen del flujo:
//
//                                     ┌────────────────────┐
//                                     │  Periodic 60s      │
//                                     └─────────┬──────────┘
//                                               │
//   notifyStorageChanged() ──┐                  │
//                            ▼                  ▼
//   refresh button ──────► triggerCh ─────► tryReconcile()
//                          (buf=1)               │
//                                                ▼
//                                       computeFingerprint()
//                                                │
//                              ┌─────────────────┴─────────────────┐
//                              ▼                                   ▼
//                       same as last                          changed
//                              │                                   │
//                          (skip)                          probeBtrfsFilesystems()
//                                                                  │
//                                                          buildSnapshot()
//                                                                  │
//                                                          atomic.Store()
//                                                                  │
//                                                          generation++
//
// Lecturas del endpoint hacen atomic.Load → lock-free, miles/seg sin contención.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// StorageObserver mantiene el observed state del sistema y lo actualiza
// periódicamente o por triggers internos.
type StorageObserver struct {
	// Configuración
	periodicInterval time.Duration // 60s baseline (config via constructor)

	// Estado
	snapshot   atomic.Pointer[ObservedSnapshot]
	generation atomic.Uint64

	// Control
	triggerCh chan struct{} // buffered=1, drop si lleno (anti-thundering)
	stopCh    chan struct{}
	wg        sync.WaitGroup

	// Single-flight para reconcile (no spawn 2 scans paralelos)
	mu sync.Mutex

	// forceNext: una invalidación EXPLÍCITA (destroy/export/wipe/import) marca
	// esto. Garantiza que el próximo reconcile (a) no se salte por fingerprint
	// y (b) se re-encole si llega mientras otro reconcile ya está en curso —
	// si no, el trigger se perdía por TryLock y el huérfano tardaba hasta 60s
	// en desaparecer de la UI. (bug 13/06: card fantasma 15-20s tras destruir)
	forceMu   sync.Mutex
	forceNext bool

	// Fingerprint last seen (protegido por mu, solo escrito durante reconcile)
	lastFingerprint [32]byte

	// Hook para tests / monitoring: si != nil, se llama tras cada snapshot.
	// La firma incluye 'changed' para que tests puedan verificar fingerprint skip.
	onSnapshot func(snap *ObservedSnapshot, changed bool)

	// Override de probe para tests. Si != nil, se usa en lugar de
	// probeBtrfsFilesystems() real (que ejecuta `btrfs` shell).
	probeFn func() (filesystems []ObservedBtrfs, looseDevices []ObservedDevice, ok bool)

	// Override de fingerprint para tests. Si != nil, se usa en lugar de
	// computeFingerprint() real.
	fingerprintFn func() [32]byte
}

// globalObserver es la instancia única usada en producción.
// Wireup en main.go al boot. Lectores: handlers HTTP.
var globalObserver *StorageObserver

// NewStorageObserver crea un observer no-arrancado. Llamar Start() para
// iniciar el loop.
func NewStorageObserver(periodicInterval time.Duration) *StorageObserver {
	if periodicInterval <= 0 {
		periodicInterval = 60 * time.Second
	}
	o := &StorageObserver{
		periodicInterval: periodicInterval,
		triggerCh:        make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
	}
	// Estado inicial: snapshot vacío con generation=0
	o.snapshot.Store(&ObservedSnapshot{
		Generation:  0,
		Timestamp:   time.Now().UTC(),
		Filesystems: []ObservedBtrfs{},
		Divergences: []Divergence{},
	})
	return o
}

// Start arranca el loop en una goroutine. Idempotente: si ya está corriendo,
// no hace nada.
func (o *StorageObserver) Start() {
	o.wg.Add(1)
	go o.loop()
	// Trigger inicial — primer snapshot al arrancar
	o.InvalidateNow()
}

// Stop detiene el loop y espera a que termine. Idempotente.
func (o *StorageObserver) Stop() {
	select {
	case <-o.stopCh:
		return // ya detenido
	default:
		close(o.stopCh)
	}
	o.wg.Wait()
}

// Snapshot devuelve el snapshot actual. Lock-free (atomic.Load).
// El snapshot retornado es inmutable — no mutarlo.
func (o *StorageObserver) Snapshot() *ObservedSnapshot {
	return o.snapshot.Load()
}

// InvalidateNow señala al observer que debe re-scan ASAP.
//
// Non-blocking: si ya hay un trigger pendiente, este se descarta
// (1 scan cubrirá ambos eventos).
//
// Llamar desde:
//
//	· createPoolBtrfs tras mkfs+mount
//	· destroyPoolBtrfs tras unmount+wipe
//	· exportPoolBtrfs tras unmount
//	· wipeDiskGo tras wipefs
//	· Storage scheduler reconciler si detecta cambio
//	· Endpoint /api/storage/observed?refresh=true
func (o *StorageObserver) InvalidateNow() {
	// Marca que el próximo reconcile es forzado: no se salta por fingerprint
	// y, si llega tarde (otro reconcile ya corriendo), se re-encola al terminar.
	o.forceMu.Lock()
	o.forceNext = true
	o.forceMu.Unlock()

	select {
	case o.triggerCh <- struct{}{}:
	default:
		// Ya hay un trigger pendiente, no añadimos otro.
	}
}

// Generation devuelve la generation actual del snapshot.
func (o *StorageObserver) Generation() uint64 {
	return o.generation.Load()
}

// ─────────────────────────────────────────────────────────────────────────────
// Loop interno
// ─────────────────────────────────────────────────────────────────────────────

func (o *StorageObserver) loop() {
	defer o.wg.Done()

	ticker := time.NewTicker(o.periodicInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			return
		case <-ticker.C:
			o.tryReconcile()
		case <-o.triggerCh:
			o.tryReconcile()
		}
	}
}

// tryReconcile ejecuta un reconcile si no hay otro en curso.
// Si lo hay, sale silenciosamente (el otro cubrirá).
func (o *StorageObserver) tryReconcile() {
	if !o.mu.TryLock() {
		// Otro reconcile en curso. Si esta llamada venía de una invalidación
		// EXPLÍCITA (forceNext), no podemos descartarla: el reconcile en curso
		// pudo arrancar ANTES del cambio y captaría estado viejo. Re-encolamos
		// para que se ejecute en cuanto el actual libere el lock.
		o.forceMu.Lock()
		pending := o.forceNext
		o.forceMu.Unlock()
		if pending {
			select {
			case o.triggerCh <- struct{}{}:
			default:
			}
		}
		return
	}
	defer o.mu.Unlock()

	// Consumir la bandera de forzado para ESTE reconcile.
	o.forceMu.Lock()
	forced := o.forceNext
	o.forceNext = false
	o.forceMu.Unlock()

	start := time.Now()

	// 1. Fingerprint barato
	var fp [32]byte
	if o.fingerprintFn != nil {
		fp = o.fingerprintFn()
	} else {
		fp = computeFingerprint()
	}

	// Si el fingerprint no cambió, skip del scan caro — SALVO que sea un
	// reconcile forzado (destroy/export/wipe), donde el caller ya sabe que
	// el estado cambió aunque el fingerprint barato no lo capte todavía.
	if !forced && fp == o.lastFingerprint && o.generation.Load() > 0 {
		// generation > 0 evita skip del primer scan (lastFingerprint zero-valued)
		if o.onSnapshot != nil {
			o.onSnapshot(o.snapshot.Load(), false)
		}
		return
	}

	// 2. Scan completo
	var (
		filesystems  []ObservedBtrfs
		looseDevices []ObservedDevice
		ok           bool
	)
	if o.probeFn != nil {
		filesystems, looseDevices, ok = o.probeFn()
	} else {
		filesystems, ok = probeBtrfsFilesystems()
		if ok {
			// Build fsByDevice map para excluir discos en uso
			fsByDevice := map[string]string{}
			for _, fs := range filesystems {
				for _, d := range fs.Devices {
					fsByDevice[d.Path] = fs.UUID
				}
			}
			looseDevices = probeLooseDevices(fsByDevice)
		}
	}

	if !ok {
		// probe falló — mantener el snapshot anterior y registrar
		logMsg("StorageObserver: probe failed (btrfs not responding?)")
		return
	}

	// 3. Cruzar con managed state (SQLite) para marcar IsManaged
	enrichWithManagedState(filesystems)

	// 4. Análisis de divergencia
	divergences := analyzeDivergences(filesystems)

	// 5. Construir snapshot
	newSnap := &ObservedSnapshot{
		Generation:      o.generation.Add(1),
		Timestamp:       time.Now().UTC(),
		Filesystems:     filesystems,
		LooseDevices:    looseDevices,
		Divergences:     divergences,
		ScanDurationMs:  time.Since(start).Milliseconds(),
		FingerprintHash: fp,
	}

	o.lastFingerprint = fp
	o.snapshot.Store(newSnap)

	if o.onSnapshot != nil {
		o.onSnapshot(newSnap, true)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cruce con managed state (SQLite)
// ─────────────────────────────────────────────────────────────────────────────

// enrichWithManagedState marca cada ObservedBtrfs con IsManaged/ManagedPool*
// según lo que haya en storage_pools (SQLite). Modifica filesystems in-place.
func enrichWithManagedState(filesystems []ObservedBtrfs) {
	if storageService == nil {
		return
	}
	ctx := context.Background()
	pools, err := storageService.repo.ListPools(ctx)
	if err != nil {
		return
	}
	// Mapa UUID BTRFS → pool managed
	byUUID := make(map[string]*Pool, len(pools))
	for _, p := range pools {
		byUUID[p.BtrfsUUID] = p
	}
	for i := range filesystems {
		if p, ok := byUUID[filesystems[i].UUID]; ok {
			filesystems[i].IsManaged = true
			filesystems[i].ManagedPoolID = p.ID
			filesystems[i].ManagedPoolName = p.Name
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Análisis de divergencias
// ─────────────────────────────────────────────────────────────────────────────

// analyzeDivergences compara managed (SQLite) vs observed (filesystems)
// y produce divergencias. Función pura sobre los datos pasados.
func analyzeDivergences(filesystems []ObservedBtrfs) []Divergence {
	var divs []Divergence

	// 1. Filesystems observed sin pool managed → orphan_filesystem
	for _, fs := range filesystems {
		if fs.IsManaged {
			continue
		}
		divs = append(divs, Divergence{
			Type:     DivOrphanFilesystem,
			Severity: SeverityInfo,
			FSUUID:   fs.UUID,
			Detail: "Filesystem BTRFS detectado (" + fs.Label +
				") no registrado en NimOS. Puede importarse o eliminarse.",
			Hint: "Usa 'Importar' para añadirlo o 'Destruir' para liberar los discos.",
		})
	}

	// 2. Filesystems managed con devices missing → pool_missing_device
	for _, fs := range filesystems {
		if !fs.IsManaged {
			continue
		}
		if fs.DevicesMissing > 0 {
			sev := SeverityWarning
			if fs.DevicesOnline == 0 {
				sev = SeverityCritical
			}
			divs = append(divs, Divergence{
				Type:     DivPoolMissingDevice,
				Severity: sev,
				PoolID:   fs.ManagedPoolID,
				PoolName: fs.ManagedPoolName,
				FSUUID:   fs.UUID,
				Detail: pluralize("Falta %d disco", fs.DevicesMissing) +
					" del pool '" + fs.ManagedPoolName + "'.",
				Hint: "Verifica conexiones físicas o reemplaza el disco ausente.",
			})
		}
		if fs.IOErrorCount > 0 {
			divs = append(divs, Divergence{
				Type:     DivUnexpectedIOErrors,
				Severity: SeverityWarning,
				PoolID:   fs.ManagedPoolID,
				PoolName: fs.ManagedPoolName,
				FSUUID:   fs.UUID,
				Detail:   "Errores de I/O detectados en el pool '" + fs.ManagedPoolName + "'.",
				Hint:     "Ejecuta un scrub y revisa SMART de los discos.",
			})
		}
		if !fs.IsMounted && fs.HasMountPoint {
			divs = append(divs, Divergence{
				Type:     DivPoolUnmounted,
				Severity: SeverityInfo,
				PoolID:   fs.ManagedPoolID,
				PoolName: fs.ManagedPoolName,
				FSUUID:   fs.UUID,
				Detail:   "Pool '" + fs.ManagedPoolName + "' no está montado.",
				Hint:     "Reinicia el daemon o monta manualmente.",
			})
		}
	}

	// 3. Pools managed que NO aparecen en observed → critical
	// (Significa: pool registrado pero todos sus discos ausentes Y filesystem
	// no detectado por btrfs filesystem show — situación grave)
	if storageService != nil {
		ctx := context.Background()
		pools, err := storageService.repo.ListPools(ctx)
		if err == nil {
			observedUUIDs := map[string]bool{}
			for _, fs := range filesystems {
				observedUUIDs[fs.UUID] = true
			}
			for _, p := range pools {
				if !observedUUIDs[p.BtrfsUUID] {
					divs = append(divs, Divergence{
						Type:     DivPoolNotDetected,
						Severity: SeverityCritical,
						PoolID:   p.ID,
						PoolName: p.Name,
						Detail:   "Pool '" + p.Name + "' está registrado pero no se detecta físicamente.",
						Hint:     "Verifica conexiones de discos. Si no aparecen, posible fallo de hardware.",
					})
				}
			}
		}
	}

	return divs
}

func pluralize(format string, n int) string {
	// Helper menor: añade 's' si n != 1
	plural := ""
	if n != 1 {
		plural = "s"
	}
	// Substituir %d
	out := ""
	written := false
	for i := 0; i < len(format); i++ {
		if !written && i+1 < len(format) && format[i] == '%' && format[i+1] == 'd' {
			out += intToStr(n)
			i++
			written = true
		} else {
			out += string(format[i])
		}
	}
	return out + plural
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// ─────────────────────────────────────────────────────────────────────────────
// Hook global para que otros módulos invaliden el observer
// ─────────────────────────────────────────────────────────────────────────────

// notifyStorageChanged es un hook que cualquier módulo de storage puede llamar
// para forzar un re-scan del observer.
//
// Si globalObserver no está inicializado (ej: tests, boot temprano), no-op.
// Es seguro llamar desde cualquier goroutine.
func notifyStorageChanged() {
	if globalObserver != nil {
		globalObserver.InvalidateNow()
	}
}
