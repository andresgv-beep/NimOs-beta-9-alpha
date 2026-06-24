// storage_reconciler_test.go — Tests del DeviceReconciler.
//
// Cubrimos:
//   - ReconcileDevicesAtBoot: scan inicial inserta devices
//   - MissingDevices: proyección correcta según last_seen_at
//   - Loop: ciclos consecutivos actualizan last_seen_at
//   - Loop: device que desaparece pasa a missing tras el threshold
//   - Loop: device que reaparece deja de ser missing
//   - Start/Stop: lifecycle correcto, idempotente
//
// Sin time.Sleep. Sin tiempos reales. FakeClock controla todo.

package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper: crear reconciler con FakeClock listo
// ─────────────────────────────────────────────────────────────────────────────

type reconcilerHarness struct {
	service     *StorageService
	scanner     *MockDeviceScanner
	clock       *FakeClock
	reconciler  *DeviceReconciler
	cleanupFunc func()
	// cycleSignal recibe un struct vacío cada vez que el loop completa
	// un ciclo. El test puede esperar en este canal en vez de Sleep.
	cycleSignal chan struct{}
}

func newReconcilerHarness(t *testing.T, cfg ReconcilerConfig) *reconcilerHarness {
	t.Helper()
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)

	start, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	clock := NewFakeClock(start)
	// El service también usa el clock (para que UpsertDevice setee
	// last_seen_at con el reloj fake, no el real)
	service.SetClock(clock)

	reconciler := NewDeviceReconciler(service, clock, cfg)
	cycleSignal := make(chan struct{}, 100)
	reconciler.onCycleComplete = func() {
		select {
		case cycleSignal <- struct{}{}:
		default:
		}
	}

	return &reconcilerHarness{
		service:     service,
		scanner:     scanner,
		clock:       clock,
		reconciler:  reconciler,
		cleanupFunc: cleanup,
		cycleSignal: cycleSignal,
	}
}

// advanceAndWait avanza el reloj y espera a que el loop complete UN ciclo.
// Si no completa en 1s, falla el test (deadlock o bug).
func (h *reconcilerHarness) advanceAndWait(t *testing.T, d time.Duration) {
	t.Helper()
	h.clock.Advance(d)
	select {
	case <-h.cycleSignal:
		// ciclo completado, listo
	case <-time.After(1 * time.Second):
		t.Fatal("reconciler did not complete cycle within 1s real time")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ReconcileDevicesAtBoot
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageReconcileAtBootInsertsDevices(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
			Serial: "ABC-1", SizeBytes: 1e12},
		{Name: "sdc", DevicePath: "/dev/sdc", ByIDPath: "/dev/disk/by-id/y",
			Serial: "ABC-2", SizeBytes: 1e12},
	}

	err := service.ReconcileDevicesAtBoot(ctx)
	if err != nil {
		t.Fatalf("ReconcileDevicesAtBoot: %v", err)
	}

	devs, _ := service.ListDevices(ctx)
	if len(devs) != 2 {
		t.Errorf("devices in DB: got %d, want 2", len(devs))
	}
}

func TestStorageReconcileAtBootIdempotent(t *testing.T) {
	service, _, scanner, cleanup := setupTestServiceWithScanner(t)
	defer cleanup()
	ctx := context.Background()

	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
			Serial: "ABC", SizeBytes: 1e12},
	}

	for i := 0; i < 3; i++ {
		if err := service.ReconcileDevicesAtBoot(ctx); err != nil {
			t.Fatalf("ReconcileDevicesAtBoot run %d: %v", i+1, err)
		}
	}

	devs, _ := service.ListDevices(ctx)
	if len(devs) != 1 {
		t.Errorf("after 3 boots: got %d devices, want 1", len(devs))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MissingDevices — proyección desde last_seen_at
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageReconcilerMissingDevicesNone(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx := context.Background()

	// Disco visto justo ahora
	tx, _ := h.service.db.BeginTx(ctx, nil)
	h.service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/x",
		CurrentPath: "/dev/sda", SizeBytes: 1e12,
		LastSeenAt: h.clock.Now(),
	})
	tx.Commit()

	missing, err := h.reconciler.MissingDevices(ctx)
	if err != nil {
		t.Fatalf("MissingDevices: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d missing, want 0 (just seen)", len(missing))
	}
}

func TestStorageReconcilerMissingDevicesCrossesThreshold(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx := context.Background()

	// Disco visto al t=0
	tx, _ := h.service.db.BeginTx(ctx, nil)
	h.service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/x",
		CurrentPath: "/dev/sda", SizeBytes: 1e12,
		LastSeenAt: h.clock.Now(),
	})
	tx.Commit()

	// Avanzar el reloj 25s: aún dentro del threshold (30s)
	h.clock.Advance(25 * time.Second)
	missing, _ := h.reconciler.MissingDevices(ctx)
	if len(missing) != 0 {
		t.Errorf("at t=25s: got %d missing, want 0", len(missing))
	}

	// Avanzar a t=31s: AHORA sí es missing
	h.clock.Advance(6 * time.Second)
	missing, _ = h.reconciler.MissingDevices(ctx)
	if len(missing) != 1 {
		t.Errorf("at t=31s: got %d missing, want 1", len(missing))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Loop lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageReconcilerStartStop(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.reconciler.Start(ctx)
	// Start es idempotente
	h.reconciler.Start(ctx)

	// Stop espera al ciclo en curso
	h.reconciler.Stop()
	// Stop es idempotente
	h.reconciler.Stop()
}

func TestStorageReconcilerLoopRunsCycles(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// El scanner reporta 1 disco
	h.scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
			Serial: "S1", SizeBytes: 1e12},
	}

	h.reconciler.Start(ctx)
	defer h.reconciler.Stop()

	// Disparar 3 ciclos avanzando el reloj
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)

	// El disco se insertó en el 1er ciclo y se actualizó en los siguientes
	devs, _ := h.service.ListDevices(ctx)
	if len(devs) != 1 {
		t.Errorf("got %d devices, want 1", len(devs))
	}
}

func TestStorageReconcilerLoopMarksDeviceMissingAfterDisappear(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second, // 3 ciclos
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ciclo 1: scanner ve el disco
	h.scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
			Serial: "S1", SizeBytes: 1e12},
	}

	h.reconciler.Start(ctx)
	defer h.reconciler.Stop()

	h.advanceAndWait(t, 10*time.Second) // ciclo 1: disco visto

	// El disco desaparece físicamente
	h.scanner.Devices = []ScannedDevice{}

	// Tras suficientes ciclos sin verlo, debe aparecer en MissingDevices.
	// Threshold es 30s; el disco se vio al t=10s. Necesitamos llegar a
	// t > 40s estrictamente.
	h.advanceAndWait(t, 10*time.Second) // ciclo 2: t=20s, sin verlo
	h.advanceAndWait(t, 10*time.Second) // ciclo 3: t=30s, sin verlo
	h.advanceAndWait(t, 10*time.Second) // ciclo 4: t=40s, exactamente al borde (diff=30)
	h.advanceAndWait(t, 10*time.Second) // ciclo 5: t=50s, diff=40 > 30 → missing

	missing, _ := h.reconciler.MissingDevices(ctx)
	if len(missing) != 1 {
		t.Errorf("after device disappeared: got %d missing, want 1", len(missing))
	}
}

func TestStorageReconcilerLoopDeviceReappearsLeavesMissing(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dev := ScannedDevice{
		Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
		Serial: "S1", SizeBytes: 1e12,
	}
	h.scanner.Devices = []ScannedDevice{dev}

	h.reconciler.Start(ctx)
	defer h.reconciler.Stop()

	h.advanceAndWait(t, 10*time.Second) // visto

	// Desaparece
	h.scanner.Devices = []ScannedDevice{}
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)

	missing, _ := h.reconciler.MissingDevices(ctx)
	if len(missing) != 1 {
		t.Fatalf("step 1: got %d missing, want 1", len(missing))
	}

	// Vuelve a aparecer
	h.scanner.Devices = []ScannedDevice{dev}
	h.advanceAndWait(t, 10*time.Second)

	missing, _ = h.reconciler.MissingDevices(ctx)
	if len(missing) != 0 {
		t.Errorf("after reappear: got %d missing, want 0", len(missing))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Loop sobrevive errores del scanner
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageReconcilerLoopSurvivesScannerError(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Scanner siempre falla
	h.scanner.Err = errReconcileFake

	h.reconciler.Start(ctx)
	defer h.reconciler.Stop()

	// Avanzar 3 ciclos: cada uno con error, pero el loop NO debe pararse
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)

	// El loop sigue corriendo (si no, advanceAndWait habría fallado por timeout)
}

// errReconcileFake es un error usado en tests
var errReconcileFake = &reconcilerFakeError{msg: "scanner failed"}

type reconcilerFakeError struct{ msg string }

func (e *reconcilerFakeError) Error() string { return e.msg }

// ─────────────────────────────────────────────────────────────────────────────
// Race detection (-race en CI) verifica que el reconciler no tiene races
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageReconcilerConcurrentStartStop(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.reconciler.Start(ctx)
		}()
	}
	wg.Wait()

	// Stop después de los Start concurrentes
	h.reconciler.Stop()
}
