// shares_health_test.go
//
// Tests del cálculo de salud de un Share.
// La función ComputeShareHealth es PURA → tests rápidos y exhaustivos.

package main

import (
	"testing"
)

// ─── Helpers ───────────────────────────────────────────────────────────

// healthyShareView devuelve un ShareView "sano" base.
// Útil como punto de partida para tests que modifican un campo a la vez.
func healthyShareView() ShareView {
	return ShareView{
		DBShare: DBShare{
			Name:   "fotos",
			Path:   "/pool/shares/fotos",
			Pool:   "pool",
			Volume: "pool",
		},
		PoolType:   "btrfs",
		MountPoint: "/pool/shares/fotos",
		Quota:      10 * 1024 * 1024 * 1024, // 10 GiB
		Used:       1 * 1024 * 1024 * 1024,  // 1 GiB (10%)
		Available:  9 * 1024 * 1024 * 1024,
	}
}

// ─── Happy path ────────────────────────────────────────────────────────

func TestComputeShareHealth_Healthy(t *testing.T) {
	v := healthyShareView()
	h := ComputeShareHealth(v)

	if h.Status != HealthHealthy {
		t.Errorf("Status = %q, want %q", h.Status, HealthHealthy)
	}
	if h.Reason != "" {
		t.Errorf("Reason = %q, want empty", h.Reason)
	}
	if h.UsagePercent != 10 {
		t.Errorf("UsagePercent = %d, want 10", h.UsagePercent)
	}
}

func TestComputeShareHealth_HealthyNoQuota(t *testing.T) {
	// Share sin quota configurada (Quota == 0) debería ser healthy
	v := healthyShareView()
	v.Quota = 0
	v.Used = 5 * 1024 * 1024 * 1024
	h := ComputeShareHealth(v)

	if h.Status != HealthHealthy {
		t.Errorf("Status = %q, want %q (no quota = unlimited)", h.Status, HealthHealthy)
	}
	if h.UsagePercent != 0 {
		t.Errorf("UsagePercent = %d, want 0 (no quota)", h.UsagePercent)
	}
}

// ─── Quota issues ──────────────────────────────────────────────────────

func TestComputeShareHealth_NearQuota_AtThreshold(t *testing.T) {
	// Justo en el límite (85% por default)
	v := healthyShareView()
	v.Quota = 100
	v.Used = 85
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want %q (85%% = at threshold)", h.Status, HealthDegraded)
	}
	if h.Reason != "near_quota" {
		t.Errorf("Reason = %q, want 'near_quota'", h.Reason)
	}
	if h.UsagePercent != 85 {
		t.Errorf("UsagePercent = %d, want 85", h.UsagePercent)
	}
}

func TestComputeShareHealth_NearQuota_AboveThreshold(t *testing.T) {
	v := healthyShareView()
	v.Quota = 100
	v.Used = 95
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (95%%)", h.Status)
	}
	if h.Reason != "near_quota" {
		t.Errorf("Reason = %q, want 'near_quota'", h.Reason)
	}
}

func TestComputeShareHealth_NearQuota_BelowThreshold(t *testing.T) {
	// 84% NO está cerca de quota (umbral 85)
	v := healthyShareView()
	v.Quota = 100
	v.Used = 84
	h := ComputeShareHealth(v)

	if h.Status != HealthHealthy {
		t.Errorf("Status = %q, want healthy (84%% < 85%% threshold)", h.Status)
	}
}

func TestComputeShareHealth_OverQuota_Exactly100(t *testing.T) {
	v := healthyShareView()
	v.Quota = 100
	v.Used = 100
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (100%%)", h.Status)
	}
	if h.Reason != "over_quota" {
		t.Errorf("Reason = %q, want 'over_quota'", h.Reason)
	}
}

func TestComputeShareHealth_OverQuota_Excess(t *testing.T) {
	v := healthyShareView()
	v.Quota = 100
	v.Used = 150
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (150%%)", h.Status)
	}
	if h.Reason != "over_quota" {
		t.Errorf("Reason = %q, want 'over_quota'", h.Reason)
	}
	if h.UsagePercent != 150 {
		t.Errorf("UsagePercent = %d, want 150 (can exceed 100)", h.UsagePercent)
	}
}

// ─── Failure cases ─────────────────────────────────────────────────────

func TestComputeShareHealth_OrphanPool(t *testing.T) {
	// Pool desaparecido: PoolType vacío
	v := healthyShareView()
	v.PoolType = ""
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (orphan_pool)", h.Status)
	}
	if h.Reason != "orphan_pool" {
		t.Errorf("Reason = %q, want 'orphan_pool'", h.Reason)
	}
}

func TestComputeShareHealth_NotMounted(t *testing.T) {
	// PoolType OK pero MountPoint vacío
	v := healthyShareView()
	v.MountPoint = ""
	h := ComputeShareHealth(v)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (not_mounted)", h.Status)
	}
	if h.Reason != "not_mounted" {
		t.Errorf("Reason = %q, want 'not_mounted'", h.Reason)
	}
}

// ─── Precedencia ───────────────────────────────────────────────────────
// orphan_pool DEBE tener prioridad sobre over_quota, not_mounted, etc.

func TestComputeShareHealth_OrphanPoolBeatsOverQuota(t *testing.T) {
	v := healthyShareView()
	v.PoolType = "" // orphan
	v.Quota = 100
	v.Used = 200 // over quota TAMBIÉN, pero orphan tiene prioridad
	h := ComputeShareHealth(v)

	if h.Reason != "orphan_pool" {
		t.Errorf("Reason = %q, want 'orphan_pool' (prioridad sobre over_quota)", h.Reason)
	}
}

func TestComputeShareHealth_NotMountedBeatsOverQuota(t *testing.T) {
	v := healthyShareView()
	v.MountPoint = "" // not mounted
	v.Quota = 100
	v.Used = 200
	h := ComputeShareHealth(v)

	if h.Reason != "not_mounted" {
		t.Errorf("Reason = %q, want 'not_mounted' (prioridad sobre over_quota)", h.Reason)
	}
}

// ─── Custom thresholds ─────────────────────────────────────────────────

func TestComputeShareHealth_CustomThreshold(t *testing.T) {
	// Threshold 50% → 60% debería ser near_quota
	v := healthyShareView()
	v.Quota = 100
	v.Used = 60

	th := SharesHealthThresholds{NearQuotaPercent: 50}
	h := ComputeShareHealthWithThresholds(v, th)

	if h.Status != HealthDegraded {
		t.Errorf("Status = %q, want degraded (60%% >= 50%% custom)", h.Status)
	}
	if h.Reason != "near_quota" {
		t.Errorf("Reason = %q, want 'near_quota'", h.Reason)
	}
}

func TestComputeShareHealth_StrictThreshold(t *testing.T) {
	// Threshold 99% → 95% NO debería ser near_quota
	v := healthyShareView()
	v.Quota = 100
	v.Used = 95

	th := SharesHealthThresholds{NearQuotaPercent: 99}
	h := ComputeShareHealthWithThresholds(v, th)

	if h.Status != HealthHealthy {
		t.Errorf("Status = %q, want healthy (95%% < 99%% custom threshold)", h.Status)
	}
}

// ─── EnrichShareViewWithHealth ─────────────────────────────────────────

func TestEnrichShareViewWithHealth_AddsHealthField(t *testing.T) {
	v := healthyShareView()
	m := map[string]interface{}{"name": "fotos"}
	EnrichShareViewWithHealth(m, v)

	healthIface, exists := m["health"]
	if !exists {
		t.Fatal("health field not added to map")
	}

	healthMap, ok := healthIface.(map[string]interface{})
	if !ok {
		t.Fatalf("health is not a map, got %T", healthIface)
	}

	if healthMap["status"] != HealthHealthy {
		t.Errorf("health.status = %v, want %q", healthMap["status"], HealthHealthy)
	}
	if healthMap["usagePercent"] != 10 {
		t.Errorf("health.usagePercent = %v, want 10", healthMap["usagePercent"])
	}
}

func TestEnrichShareViewWithHealth_PreservesOtherFields(t *testing.T) {
	// Enrich no debe sobrescribir otros campos del map
	v := healthyShareView()
	m := map[string]interface{}{
		"name":     "fotos",
		"poolType": "btrfs",
	}
	EnrichShareViewWithHealth(m, v)

	if m["name"] != "fotos" {
		t.Errorf("name overwritten: %v", m["name"])
	}
	if m["poolType"] != "btrfs" {
		t.Errorf("poolType overwritten: %v", m["poolType"])
	}
	if _, hasHealth := m["health"]; !hasHealth {
		t.Error("health field not added")
	}
}
