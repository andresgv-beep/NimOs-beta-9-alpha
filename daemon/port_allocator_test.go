package main

import "testing"

func mustAlloc(t *testing.T, preferred int, fixed bool, sticky int, occupied, hard, soft map[int]bool) int {
	t.Helper()
	p, err := allocatePort(preferred, fixed, sticky, occupied, hard, soft)
	if err != nil {
		t.Fatalf("allocatePort error inesperado: %v", err)
	}
	return p
}

func TestAllocate_FloatingPreferredFree(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	got := mustAlloc(t, 8081, false, 0, map[int]bool{}, hard, soft)
	if got != 8081 {
		t.Errorf("preferido libre debería respetarse, got %d", got)
	}
}

func TestAllocate_FloatingPreferredOccupied_GoesToPool(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	occupied := map[int]bool{9091: true} // como transmission vs torrentd (9091 también es duro)
	got := mustAlloc(t, 9091, false, 0, occupied, hard, soft)
	if got != floatPoolMin {
		t.Errorf("preferido ocupado → primer libre del pool (%d), got %d", floatPoolMin, got)
	}
}

func TestAllocate_FloatingPreferredIsSoft_GoesToPool(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// Un flotante NO debe quedarse en un well-known blando (53).
	got := mustAlloc(t, 53, false, 0, map[int]bool{}, hard, soft)
	if got != floatPoolMin {
		t.Errorf("flotante sobre blando → pool (%d), got %d", floatPoolMin, got)
	}
}

func TestAllocate_StickyKept(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// preferido sería 8081 pero la app ya tenía 30124 → se conserva.
	got := mustAlloc(t, 8081, false, 30124, map[int]bool{}, hard, soft)
	if got != 30124 {
		t.Errorf("sticky libre debe conservarse, got %d", got)
	}
}

func TestAllocate_StickyTakenByAnother_Reallocates(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// el puerto previo 30124 ahora lo tiene otra app → no se puede conservar.
	occupied := map[int]bool{30124: true}
	got := mustAlloc(t, 40000, false, 30124, occupied, hard, soft)
	if got != 40000 {
		t.Errorf("sticky ocupado → cae al preferido libre, got %d", got)
	}
}

func TestAllocate_FixedPreferredFree_ClaimsSoft(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// Pi-hole pide el 53 como fijo y está libre → lo reclama.
	got := mustAlloc(t, 53, true, 0, map[int]bool{}, hard, soft)
	if got != 53 {
		t.Errorf("fijo debe poder reclamar el blando 53 si está libre, got %d", got)
	}
}

func TestAllocate_FixedOccupied_Errors(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// AdGuard pide el 53 pero Pi-hole ya lo tiene → error "elige una".
	occupied := map[int]bool{53: true}
	if _, err := allocatePort(53, true, 0, occupied, hard, soft); err == nil {
		t.Errorf("fijo ocupado debería dar error (elige una)")
	}
}

func TestAllocate_FixedOnHardReserved_Errors(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// nadie puede reclamar un duro, ni un fijo.
	if _, err := allocatePort(9091, true, 0, map[int]bool{}, hard, soft); err == nil {
		t.Errorf("fijo sobre reservado duro debería dar error")
	}
}

func TestAllocate_MultipleFloating_NoCollision(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	occupied := map[int]bool{}
	// patrón del caller: marcar cada asignado como occupied antes de la siguiente.
	a := mustAlloc(t, 9091, false, 0, occupied, hard, soft) // preferido duro → pool 30000
	occupied[a] = true
	b := mustAlloc(t, 9091, false, 0, occupied, hard, soft) // siguiente libre → 30001
	occupied[b] = true
	if a == b {
		t.Fatalf("dos flotantes no deben colisionar: a=%d b=%d", a, b)
	}
	if a != floatPoolMin || b != floatPoolMin+1 {
		t.Errorf("esperaba %d y %d, got %d y %d", floatPoolMin, floatPoolMin+1, a, b)
	}
}

func TestAllocate_PoolExhausted_Errors(t *testing.T) {
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	occupied := make(map[int]bool, floatPoolMax-floatPoolMin+1)
	for p := floatPoolMin; p <= floatPoolMax; p++ {
		occupied[p] = true
	}
	// preferido también ocupado → no queda nada.
	occupied[8081] = true
	if _, err := allocatePort(8081, false, 0, occupied, hard, soft); err == nil {
		t.Errorf("pool agotado debería dar error")
	}
}
