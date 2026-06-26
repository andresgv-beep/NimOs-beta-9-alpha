// storage_health_reconcile_test.go — T12 · La tarjeta no puede mentir.
//
// Reproduce el incidente data8: la caché cree que el pool está "healthy" pero el
// observer (btrfs real) reporta un disco MISSING. La tarjeta NO puede seguir
// diciendo healthy. Sin reconcileHealthWithDivergences, este test FALLA (el
// status se queda en "healthy"); con el fix, pasa.

package main

import "testing"

func newHealthyPool(id, uuid string) *Pool {
	return &Pool{
		ID:        id,
		BtrfsUUID: uuid,
		Health: &PoolHealth{
			Version: 1,
			Status:  "healthy",
		},
	}
}

// T12 — caso central del incidente: divergencia pool_missing_device sobre el
// pool → la tarjeta debe dejar de ser "healthy".
func TestReconcile_MissingDevice_DowngradesFromHealthy(t *testing.T) {
	pool := newHealthyPool("pool-1", "uuid-data8")
	divs := []Divergence{
		{
			Type:     DivPoolMissingDevice,
			Severity: SeverityWarning,
			PoolID:   "pool-1",
			PoolName: "data8",
			Detail:   "Falta 1 disco del pool 'data8'.",
		},
	}

	reconcileHealthWithDivergences(pool, divs)

	if pool.Health.Status == "healthy" {
		t.Fatalf("BUG split-brain: la tarjeta sigue 'healthy' con un disco MISSING real")
	}
	if pool.Health.Status != "degraded" {
		t.Errorf("status = %q; quería 'degraded' (severity warning)", pool.Health.Status)
	}
	if pool.Health.Reason.Primary != "reality_divergence" {
		t.Errorf("reason.Primary = %q; quería 'reality_divergence'", pool.Health.Reason.Primary)
	}
	if len(pool.Health.Diagnostics) == 0 {
		t.Error("se esperaba al menos un diagnostic 'observer_divergence' para el banner")
	}
}

// FIX-5 — pool entero no detectado → estado MISSING (distinto de degraded).
func TestReconcile_PoolNotDetected_Missing(t *testing.T) {
	pool := newHealthyPool("p", "u")
	divs := []Divergence{{
		Type:     DivPoolNotDetected,
		Severity: SeverityCritical,
		PoolID:   "p",
		Detail:   "Pool 'p' está registrado pero no se detecta físicamente.",
	}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "missing" {
		t.Errorf("un pool no detectado debe ser 'missing', no %q", pool.Health.Status)
	}
}

// MISSING gana sobre degraded (es más severo de mostrar).
func TestReconcile_Missing_BeatsDegraded(t *testing.T) {
	pool := newHealthyPool("p", "u")
	pool.Health.Status = "degraded"
	divs := []Divergence{{Type: DivPoolNotDetected, Severity: SeverityCritical, PoolID: "p", Detail: "x"}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "missing" {
		t.Errorf("missing debe sobrescribir degraded; got %q", pool.Health.Status)
	}
}

// Un disco que falta (pool presente) sigue siendo degraded, NO missing.
func TestReconcile_DeviceMissing_StaysDegraded(t *testing.T) {
	pool := newHealthyPool("p", "u")
	divs := []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityWarning, PoolID: "p", Detail: "falta 1 disco"}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "degraded" {
		t.Errorf("falta de un disco (pool presente) es degraded, no %q", pool.Health.Status)
	}
}

// Crítico (tipo no-NotDetected) → critical.
func TestReconcile_CriticalDivergence_Critical(t *testing.T) {
	pool := newHealthyPool("p", "u")
	divs := []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityCritical, PoolID: "p", Detail: "Pool no detectado."}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "critical" {
		t.Errorf("status = %q; quería 'critical'", pool.Health.Status)
	}
}

// Pool no montado (severity info) tampoco puede ser healthy.
func TestReconcile_Unmounted_NotHealthy(t *testing.T) {
	pool := newHealthyPool("p", "u")
	divs := []Divergence{{Type: DivPoolUnmounted, Severity: SeverityInfo, PoolID: "p", Detail: "Pool no montado."}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status == "healthy" {
		t.Errorf("un pool no montado no puede mostrarse 'healthy'")
	}
}

// Empareja por UUID cuando la divergencia no trae PoolID.
func TestReconcile_MatchByUUID(t *testing.T) {
	pool := newHealthyPool("p", "uuid-x")
	divs := []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityWarning, FSUUID: "uuid-x", Detail: "Falta disco."}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "degraded" {
		t.Errorf("debía emparejar por UUID y degradar; status = %q", pool.Health.Status)
	}
}

// Una divergencia de OTRO pool no afecta a este.
func TestReconcile_OtherPool_Untouched(t *testing.T) {
	pool := newHealthyPool("pool-1", "uuid-1")
	divs := []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityCritical, PoolID: "pool-2", Detail: "otro"}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "healthy" {
		t.Errorf("una divergencia de otro pool no debe tocar este; status = %q", pool.Health.Status)
	}
}

// orphan_filesystem NO afecta a la salud de un pool managed (es territorio FIX-3).
func TestReconcile_OrphanFilesystem_DoesNotAffectManaged(t *testing.T) {
	pool := newHealthyPool("p", "u")
	divs := []Divergence{{Type: DivOrphanFilesystem, Severity: SeverityInfo, FSUUID: "u", Detail: "huérfano"}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "healthy" {
		t.Errorf("orphan_filesystem no debe degradar un pool managed; status = %q", pool.Health.Status)
	}
}

// La realidad solo EMPEORA: una divergencia warning no "mejora" un pool ya critical.
func TestReconcile_NeverImproves(t *testing.T) {
	pool := newHealthyPool("p", "u")
	pool.Health.Status = "critical"
	divs := []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityWarning, PoolID: "p", Detail: "warn"}}
	reconcileHealthWithDivergences(pool, divs)
	if pool.Health.Status != "critical" {
		t.Errorf("no debe mejorar de critical a degraded; status = %q", pool.Health.Status)
	}
}

// No-panic con Health nil.
func TestReconcile_NilHealth_NoPanic(t *testing.T) {
	pool := &Pool{ID: "p"}
	reconcileHealthWithDivergences(pool, []Divergence{{Type: DivPoolMissingDevice, Severity: SeverityCritical, PoolID: "p"}})
}
