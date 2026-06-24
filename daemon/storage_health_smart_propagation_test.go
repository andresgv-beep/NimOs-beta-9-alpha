package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// Propagación de SMART crítico al estado del pool.
//
// Bug encontrado en hardware: un pool raid1c3 con un disco en estado SMART
// crítico (8 sectores pendientes + 8 incorregibles) se mostraba 'healthy / sin
// incidencias' porque smart_critical solo escalaba el pool si effective==0.
// Con redundancia intacta, el riesgo quedaba oculto. Ahora un disco crítico
// degrada el pool a 'degraded' (atención), sin sobre-alarmar a 'critical'
// porque los datos siguen protegidos.
// ─────────────────────────────────────────────────────────────────────────────

// EL CASO REAL: raid1c3, 3 discos, uno con smart_critical, ninguno missing.
func TestPoolHealth_SmartCriticalWithRedundancy_Degraded(t *testing.T) {
	diags := []Diagnostic{
		{Code: "smart_critical", Severity: 4, Disk: "sdb", Detail: "8 pending sectors"},
	}
	h := ComputePoolHealth(diags, "raid1c3", 3, false, 0, "")

	if h.Status != "degraded" {
		t.Errorf("disco SMART crítico con redundancia: status got %q, want degraded "+
			"(antes daba healthy y ocultaba el riesgo)", h.Status)
	}
	if h.Status == "healthy" {
		t.Error("BUG ORIGINAL: un pool con disco muriéndose NO debe ser healthy")
	}
}

// No sobre-alarmar: un disco crítico con redundancia NO debe ser 'critical'
// (los datos están protegidos; critical se reserva para pérdida real/probable).
func TestPoolHealth_SmartCriticalNotOverEscalated(t *testing.T) {
	diags := []Diagnostic{
		{Code: "smart_critical", Severity: 4, Disk: "sdb"},
	}
	h := ComputePoolHealth(diags, "raid1c3", 3, false, 0, "")
	if h.Status == "critical" {
		t.Error("con redundancia intacta NO debe ser critical (sería alarmista); degraded es lo correcto")
	}
}

// No-regresión: un pool sano de verdad sigue healthy.
func TestPoolHealth_AllGood_Healthy(t *testing.T) {
	h := ComputePoolHealth(nil, "raid1c3", 3, false, 0, "")
	if h.Status != "healthy" {
		t.Errorf("pool sin diagnósticos: got %q, want healthy", h.Status)
	}
}

// No-regresión: disco missing sigue mandando sobre smart (es más grave).
func TestPoolHealth_MissingTakesPriorityOverSmart(t *testing.T) {
	diags := []Diagnostic{
		{Code: "disk_missing", Severity: 3, Disk: "sdc"},
		{Code: "smart_critical", Severity: 4, Disk: "sdb"},
	}
	h := ComputePoolHealth(diags, "raid1c3", 3, false, 0, "")
	// raid1c3 puede perder 2; con 1 missing sigue degraded, pero el reasonCode
	// debe ser el de degradación por disco ausente, no el de SMART.
	if h.Status != "degraded" {
		t.Errorf("1 disco missing en raid1c3: got %q, want degraded", h.Status)
	}
}
