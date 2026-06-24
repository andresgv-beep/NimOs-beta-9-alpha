package main

import (
	"testing"
)

// ─── snapshotsToPrune — P4: retention de snapshots ────────────────────────────

func TestSnapshotsToPrune_UnderLimit(t *testing.T) {
	names := []string{
		"nimbackup-20260101-100000",
		"nimbackup-20260102-100000",
	}
	if got := snapshotsToPrune(names, 10); got != nil {
		t.Errorf("bajo el límite: no debe podar nada, got %v", got)
	}
}

func TestSnapshotsToPrune_AtLimit(t *testing.T) {
	names := []string{
		"nimbackup-20260101-100000",
		"nimbackup-20260102-100000",
		"nimbackup-20260103-100000",
	}
	if got := snapshotsToPrune(names, 3); got != nil {
		t.Errorf("justo en el límite: no poda, got %v", got)
	}
}

func TestSnapshotsToPrune_OverLimitRemovesOldest(t *testing.T) {
	// 5 snapshots, máximo 3 → borrar los 2 más viejos.
	names := []string{
		"nimbackup-20260103-100000",
		"nimbackup-20260101-100000", // más viejo
		"nimbackup-20260105-100000",
		"nimbackup-20260102-100000", // 2º más viejo
		"nimbackup-20260104-100000",
	}
	got := snapshotsToPrune(names, 3)
	if len(got) != 2 {
		t.Fatalf("debe podar 2, got %d: %v", len(got), got)
	}
	// Deben ser los dos más viejos.
	want := map[string]bool{
		"nimbackup-20260101-100000": true,
		"nimbackup-20260102-100000": true,
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("podó %s, que no es de los más viejos", n)
		}
	}
}

func TestSnapshotsToPrune_KeepsNewest(t *testing.T) {
	names := []string{
		"nimbackup-20260101-100000",
		"nimbackup-20260102-100000",
		"nimbackup-20260103-100000",
		"nimbackup-20260104-100000",
	}
	got := snapshotsToPrune(names, 1)
	// Debe podar 3, conservando solo el más nuevo (04).
	if len(got) != 3 {
		t.Fatalf("debe podar 3, got %d", len(got))
	}
	for _, n := range got {
		if n == "nimbackup-20260104-100000" {
			t.Error("no debe podar el más nuevo")
		}
	}
}

func TestSnapshotsToPrune_ZeroMaxNoOp(t *testing.T) {
	// maxKeep <= 0 es defensivo: no podar (evita borrar todo por un bug de config).
	names := []string{"nimbackup-20260101-100000"}
	if got := snapshotsToPrune(names, 0); got != nil {
		t.Errorf("maxKeep=0 no debe podar (defensivo), got %v", got)
	}
}

func TestSnapshotsToPrune_Empty(t *testing.T) {
	if got := snapshotsToPrune(nil, 5); got != nil {
		t.Errorf("lista vacía: got %v", got)
	}
}
