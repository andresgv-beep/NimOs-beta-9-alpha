package main

import (
	"context"
	"testing"
	"time"
)

// ─── diffReappeared — pieza pura de P2 ────────────────────────────────────────

func TestDiffReappeared_OneCameBack(t *testing.T) {
	prev := map[string]bool{"S1": true, "S2": true}
	curr := map[string]bool{"S2": true} // S1 reapareció (ya no está missing)
	got := diffReappeared(prev, curr)
	if len(got) != 1 || got[0] != "S1" {
		t.Errorf("got %v, want [S1]", got)
	}
}

func TestDiffReappeared_NoneCameBack(t *testing.T) {
	prev := map[string]bool{"S1": true}
	curr := map[string]bool{"S1": true} // sigue missing
	if got := diffReappeared(prev, curr); len(got) != 0 {
		t.Errorf("sigue missing: got %v, want []", got)
	}
}

func TestDiffReappeared_NewlyMissingNotReappeared(t *testing.T) {
	prev := map[string]bool{}
	curr := map[string]bool{"S1": true} // S1 ACABA de desaparecer, no reapareció
	if got := diffReappeared(prev, curr); len(got) != 0 {
		t.Errorf("recién missing no es reaparición: got %v, want []", got)
	}
}

func TestDiffReappeared_EmptyBoth(t *testing.T) {
	if got := diffReappeared(map[string]bool{}, map[string]bool{}); len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

// ─── Hook onDeviceReappear — integración con el loop (P2) ──────────────────────
//
// Verifica el escenario real: un device se va missing y al reaparecer el loop
// dispara onDeviceReappear (en producción → reconcileMountState para remontar).
// Clave: NO debe dispararse en el primer ciclo (arranque en frío).

func TestReconcilerFiresReappearHook(t *testing.T) {
	h := newReconcilerHarness(t, ReconcilerConfig{
		Interval:         10 * time.Second,
		MissingThreshold: 30 * time.Second,
	})
	defer h.cleanupFunc()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reappearCalls int
	var lastReappeared []string
	h.reconciler.onDeviceReappear = func(ctx context.Context, devs []*Device) {
		reappearCalls++
		lastReappeared = nil
		for _, d := range devs {
			lastReappeared = append(lastReappeared, d.Serial)
		}
	}

	dev := ScannedDevice{
		Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
		Serial: "S1", SizeBytes: 1e12,
	}
	h.scanner.Devices = []ScannedDevice{dev}

	h.reconciler.Start(ctx)
	defer h.reconciler.Stop()

	// Primer ciclo: device presente. NO debe disparar el hook.
	h.advanceAndWait(t, 10*time.Second)
	if reappearCalls != 0 {
		t.Fatalf("arranque en frío no debe disparar reappear: got %d", reappearCalls)
	}

	// Desaparece y cruza el threshold (missing).
	h.scanner.Devices = []ScannedDevice{}
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)
	h.advanceAndWait(t, 10*time.Second)

	missing, _ := h.reconciler.MissingDevices(ctx)
	if len(missing) != 1 {
		t.Fatalf("device debería estar missing: got %d", len(missing))
	}
	callsBeforeReappear := reappearCalls

	// Reaparece → debe disparar el hook exactamente con S1.
	h.scanner.Devices = []ScannedDevice{dev}
	h.advanceAndWait(t, 10*time.Second)

	if reappearCalls != callsBeforeReappear+1 {
		t.Errorf("reaparición debe disparar el hook una vez: got %d calls totales", reappearCalls)
	}
	if len(lastReappeared) != 1 || lastReappeared[0] != "S1" {
		t.Errorf("hook debe recibir S1: got %v", lastReappeared)
	}
}
