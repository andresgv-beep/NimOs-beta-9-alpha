package main

// storage_observer_test.go — Tests del Storage Observer.
//
// Cubre:
//   · Estado inicial: snapshot vacío con generation=0
//   · Start ejecuta primer scan
//   · InvalidateNow dispara scan
//   · Fingerprint skip: si fingerprint no cambia, no rebuild snapshot
//   · Concurrency: lecturas paralelas son seguras (atomic.Pointer)
//   · No spawn 2 reconciles en paralelo (single-flight via mu)
//   · Stop es idempotente y rápido
//   · computeObservationHealth — todos los casos
//   · analyzeDivergences — orphan, missing device, IO errors

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Helpers ──────────────────────────────────────────────────────────────

// newTestObserver crea un observer con probe y fingerprint mockeables.
// Devuelve el observer y un control para inyectar resultados de probe.
type mockProbe struct {
	filesystems  []ObservedBtrfs
	looseDevices []ObservedDevice
	ok           bool
	calls        atomic.Int64
}

func newTestObserver(t *testing.T, interval time.Duration) (*StorageObserver, *mockProbe, [32]byte) {
	t.Helper()
	mp := &mockProbe{ok: true}
	fp := [32]byte{1, 2, 3, 4}

	o := NewStorageObserver(interval)
	o.probeFn = func() ([]ObservedBtrfs, []ObservedDevice, bool) {
		mp.calls.Add(1)
		return mp.filesystems, mp.looseDevices, mp.ok
	}
	o.fingerprintFn = func() [32]byte {
		return fp
	}
	return o, mp, fp
}

// ─── Estado inicial ───────────────────────────────────────────────────────

func TestObserver_InitialState(t *testing.T) {
	o := NewStorageObserver(60 * time.Second)
	snap := o.Snapshot()
	if snap == nil {
		t.Fatal("initial snapshot is nil")
	}
	if snap.Generation != 0 {
		t.Errorf("initial generation = %d, want 0", snap.Generation)
	}
	if len(snap.Filesystems) != 0 {
		t.Errorf("initial filesystems = %d, want 0", len(snap.Filesystems))
	}
}

// ─── Start ejecuta primer scan ────────────────────────────────────────────

func TestObserver_StartTriggersInitialScan(t *testing.T) {
	o, mp, _ := newTestObserver(t, 10*time.Second)

	// Setup snapshot esperado
	mp.filesystems = []ObservedBtrfs{
		{UUID: "test-uuid", CanProbe: true, ObservationHealth: HealthHealthy},
	}

	o.Start()
	defer o.Stop()

	// Esperar el primer scan (InvalidateNow lo dispara desde Start)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mp.calls.Load() > 0 && o.Generation() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if mp.calls.Load() == 0 {
		t.Fatal("Start did not trigger probe")
	}
	if o.Generation() == 0 {
		t.Fatal("generation not incremented after initial scan")
	}
	snap := o.Snapshot()
	if len(snap.Filesystems) != 1 {
		t.Errorf("snapshot has %d filesystems, want 1", len(snap.Filesystems))
	}
}

// ─── InvalidateNow dispara scan ────────────────────────────────────────────

func TestObserver_InvalidateNowTriggersScan(t *testing.T) {
	o, mp, _ := newTestObserver(t, 1*time.Hour) // periodic muy lento

	// Fingerprint que cambia cada llamada (counter atómico)
	// Setear ANTES de Start para evitar races con tryReconcile.
	var fpCounter atomic.Int64
	o.fingerprintFn = func() [32]byte {
		var fp [32]byte
		n := fpCounter.Add(1)
		fp[0] = byte(n)
		return fp
	}

	o.Start()
	defer o.Stop()

	// Esperar el primer scan inicial
	waitForCalls(t, mp, 1, 2*time.Second)
	initialCalls := mp.calls.Load()

	// Invalidar — fingerprint cambia, no skipea
	o.InvalidateNow()

	// Esperar nuevo scan
	waitForCalls(t, mp, int(initialCalls)+1, 2*time.Second)
}

// ─── Fingerprint skip ──────────────────────────────────────────────────────

func TestObserver_FingerprintSkipsRedundantScans(t *testing.T) {
	o, mp, _ := newTestObserver(t, 50*time.Millisecond)

	skipped := atomic.Int32{}
	o.onSnapshot = func(snap *ObservedSnapshot, changed bool) {
		if !changed {
			skipped.Add(1)
		}
	}

	o.Start()
	defer o.Stop()

	// Esperar primer scan
	waitForCalls(t, mp, 1, 2*time.Second)

	// Dejar correr varios TICKS PERIÓDICOS con el mismo fingerprint.
	// El tick periódico ("¿cambió algo solo?") SÍ debe skipear por fingerprint
	// para no re-escanear discos cada 50ms sin motivo.
	time.Sleep(300 * time.Millisecond)

	// Generation no incrementa: los ticks periódicos con misma huella skipean.
	if o.Generation() > 1 {
		t.Errorf("generation = %d, want 1 (periodic same-fp scans should skip)", o.Generation())
	}
	if skipped.Load() == 0 {
		t.Error("expected at least 1 fingerprint-skip but got 0")
	}
}

// TestObserver_InvalidateNowBypassesFingerprint blinda el fix del bug 13/06:
// una invalidación EXPLÍCITA (destroy/export/wipe) NO debe saltarse por
// fingerprint. El caller ya sabe que el estado cambió; skipear dejaba la card
// huérfana fantasma en la UI hasta el siguiente tick de 60s.
func TestObserver_InvalidateNowBypassesFingerprint(t *testing.T) {
	o, mp, _ := newTestObserver(t, 1*time.Hour)

	o.Start()
	defer o.Stop()
	waitForCalls(t, mp, 1, 2*time.Second)

	genBefore := o.Generation()

	// InvalidateNow explícito con MISMO fingerprint → debe forzar scan igual.
	o.InvalidateNow()
	time.Sleep(200 * time.Millisecond)

	if o.Generation() <= genBefore {
		t.Errorf("generation = %d, want > %d (forced invalidation must re-scan despite same fingerprint)",
			o.Generation(), genBefore)
	}
}

// TestObserver_ForcedInvalidationNotLostUnderContention blinda la otra mitad
// del bug: si una invalidación llega mientras OTRO reconcile ya corre, el
// TryLock la descartaba y el cambio tardaba hasta 60s en verse. Ahora se
// re-encola. Verificamos que tras invalidaciones concurrentes el observer
// acaba ejecutando un scan que refleja el estado nuevo.
func TestObserver_ForcedInvalidationNotLostUnderContention(t *testing.T) {
	o, mp, _ := newTestObserver(t, 1*time.Hour)

	// probe lento para forzar solape: mientras un reconcile escanea, llegan más.
	o.probeFn = func() ([]ObservedBtrfs, []ObservedDevice, bool) {
		mp.calls.Add(1)
		time.Sleep(80 * time.Millisecond)
		return mp.filesystems, mp.looseDevices, mp.ok
	}

	o.Start()
	defer o.Stop()
	waitForCalls(t, mp, 1, 2*time.Second)

	callsBefore := mp.calls.Load()

	// Ráfaga de invalidaciones explícitas mientras el probe es lento.
	for i := 0; i < 5; i++ {
		o.InvalidateNow()
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)

	// Debe haberse ejecutado al menos un scan adicional tras la ráfaga;
	// ninguna invalidación forzada se pierde silenciosamente.
	if mp.calls.Load() <= callsBefore {
		t.Errorf("probe calls = %d, want > %d (forced invalidations must not be dropped under contention)",
			mp.calls.Load(), callsBefore)
	}
}

// ─── Concurrency ──────────────────────────────────────────────────────────

func TestObserver_ConcurrentReads(t *testing.T) {
	o, mp, _ := newTestObserver(t, 50*time.Millisecond)
	mp.filesystems = []ObservedBtrfs{{UUID: "x", CanProbe: true}}
	o.Start()
	defer o.Stop()

	// Muchos lectores paralelos durante 200ms
	var wg sync.WaitGroup
	var readErrors atomic.Int64
	stop := make(chan struct{})

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := o.Snapshot()
					if snap == nil {
						readErrors.Add(1)
					}
					// Acceder al contenido para que -race lo detecte si hay shared mutable
					_ = snap.Generation
					_ = len(snap.Filesystems)
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	if readErrors.Load() > 0 {
		t.Errorf("%d concurrent reads returned nil", readErrors.Load())
	}
}

// ─── Single-flight reconcile ───────────────────────────────────────────────

func TestObserver_NoParallelReconciles(t *testing.T) {
	o, mp, _ := newTestObserver(t, 1*time.Hour)

	var concurrent atomic.Int32
	var maxSeen atomic.Int32
	o.probeFn = func() ([]ObservedBtrfs, []ObservedDevice, bool) {
		cur := concurrent.Add(1)
		// Track máximo paralelismo observado
		for {
			m := maxSeen.Load()
			if cur <= m || maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		mp.calls.Add(1)
		return nil, nil, true
	}

	// Fingerprint atómico para poder cambiarlo sin race
	var fpCounter atomic.Int64
	o.fingerprintFn = func() [32]byte {
		var fp [32]byte
		n := fpCounter.Add(1)
		fp[0] = byte(n)
		fp[1] = byte(n >> 8)
		return fp
	}

	o.Start()
	defer o.Stop()

	// Lluvia de invalidations — cada scan verá un fingerprint distinto
	// gracias al atomic counter, así que ninguno skipea.
	for i := 0; i < 20; i++ {
		o.InvalidateNow()
	}
	time.Sleep(500 * time.Millisecond)

	if maxSeen.Load() > 1 {
		t.Errorf("observed %d concurrent reconciles, want max 1", maxSeen.Load())
	}
}

// ─── Stop idempotente ──────────────────────────────────────────────────────

func TestObserver_StopIdempotent(t *testing.T) {
	o, _, _ := newTestObserver(t, 1*time.Hour)
	o.Start()

	// Llamar Stop varias veces no debe panic
	o.Stop()
	o.Stop()
	o.Stop()
}

// ─── computeObservationHealth ──────────────────────────────────────────────

func TestComputeObservationHealth(t *testing.T) {
	cases := []struct {
		name string
		fs   ObservedBtrfs
		want HealthStatus
	}{
		{
			"healthy",
			ObservedBtrfs{CanProbe: true, DevicesExpected: 2, DevicesOnline: 2, IsMounted: true, IOErrorCount: 0},
			HealthHealthy,
		},
		{
			"incomplete missing 1 of 2",
			ObservedBtrfs{CanProbe: true, DevicesExpected: 2, DevicesOnline: 1, IsMounted: true},
			HealthIncomplete,
		},
		{
			"degraded with io errors",
			ObservedBtrfs{CanProbe: true, DevicesExpected: 2, DevicesOnline: 2, IsMounted: true, IOErrorCount: 5},
			HealthDegraded,
		},
		{
			"partial unmounted but devices ok",
			ObservedBtrfs{CanProbe: true, DevicesExpected: 1, DevicesOnline: 1, IsMounted: false},
			HealthPartial,
		},
		{
			"unknown when cannot probe",
			ObservedBtrfs{CanProbe: false},
			HealthUnknown,
		},
	}
	for _, tc := range cases {
		got := computeObservationHealth(&tc.fs)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ─── analyzeDivergences ────────────────────────────────────────────────────

func TestAnalyzeDivergences_OrphanFilesystem(t *testing.T) {
	fs := []ObservedBtrfs{
		{UUID: "orphan-uuid", Label: "loose", IsManaged: false, CanProbe: true},
	}
	divs := analyzeDivergences(fs)
	if len(divs) == 0 {
		t.Fatal("expected at least 1 divergence for orphan filesystem")
	}
	found := false
	for _, d := range divs {
		if d.Type == DivOrphanFilesystem && d.FSUUID == "orphan-uuid" {
			found = true
			if d.Severity != SeverityInfo {
				t.Errorf("orphan severity = %q, want info", d.Severity)
			}
		}
	}
	if !found {
		t.Error("orphan_filesystem divergence not found")
	}
}

func TestAnalyzeDivergences_MissingDevice(t *testing.T) {
	fs := []ObservedBtrfs{
		{
			UUID: "managed-uuid", IsManaged: true,
			ManagedPoolID: "p1", ManagedPoolName: "data",
			DevicesExpected: 2, DevicesOnline: 1, DevicesMissing: 1,
			CanProbe: true,
		},
	}
	divs := analyzeDivergences(fs)
	found := false
	for _, d := range divs {
		if d.Type == DivPoolMissingDevice && d.PoolName == "data" {
			found = true
			if d.Severity != SeverityWarning {
				t.Errorf("missing device severity = %q, want warning", d.Severity)
			}
		}
	}
	if !found {
		t.Error("pool_missing_device divergence not found")
	}
}

func TestAnalyzeDivergences_AllMissingCritical(t *testing.T) {
	fs := []ObservedBtrfs{
		{
			UUID: "managed-uuid", IsManaged: true,
			ManagedPoolName: "data",
			DevicesExpected: 2, DevicesOnline: 0, DevicesMissing: 2,
			CanProbe: true,
		},
	}
	divs := analyzeDivergences(fs)
	for _, d := range divs {
		if d.Type == DivPoolMissingDevice {
			if d.Severity != SeverityCritical {
				t.Errorf("all-missing severity = %q, want critical", d.Severity)
			}
			return
		}
	}
	t.Error("expected pool_missing_device divergence")
}

func TestAnalyzeDivergences_IOErrors(t *testing.T) {
	fs := []ObservedBtrfs{
		{
			UUID: "managed-uuid", IsManaged: true, ManagedPoolName: "data",
			DevicesExpected: 2, DevicesOnline: 2,
			IOErrorCount: 42,
			CanProbe:     true, IsMounted: true,
		},
	}
	divs := analyzeDivergences(fs)
	found := false
	for _, d := range divs {
		if d.Type == DivUnexpectedIOErrors {
			found = true
		}
	}
	if !found {
		t.Error("expected unexpected_io_errors divergence")
	}
}

// ─── Helpers de tests ──────────────────────────────────────────────────────

func waitForCalls(t *testing.T, mp *mockProbe, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if mp.calls.Load() >= int64(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d probe calls (got %d)", want, mp.calls.Load())
}
