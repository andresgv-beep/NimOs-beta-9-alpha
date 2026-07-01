package main

// ═══════════════════════════════════════════════════════════════════════
// SHARES HEALTH · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Calcula la salud de un Share. Usa las CONSTANTES HealthHealthy,
// HealthDegraded, etc. ya definidas en storage_observe_types.go
// (compartidas a nivel main package para no duplicar).
//
// Aplica el patrón Health SOLO donde aporta valor concreto:
//
//   · over_quota   → usando >=100% de quota (degraded)
//   · near_quota   → usando >=85% de quota (degraded preventivo)
//   · orphan_pool  → pool ya no existe (failed → mapeado a "degraded"
//                    porque "failed" no está en el enum storage)
//   · not_mounted  → pool existe pero no montado (degraded)
//   · healthy      → todo bien
//
// NO se aplica Convergence (Desired/Observed/Applied):
//   Discipline #3 — solo donde hay reconciliación real.
//   Los shares son config estática que no se drift.
//
// NO se persisten snapshots:
//   Discipline #2 — se calcula en runtime, frontend lo refresca.
//
// USO:
//   health := ComputeShareHealth(view)
//   if health.Status != HealthHealthy { ... }
// ═══════════════════════════════════════════════════════════════════════

// ShareHealth describe la salud de un share en un momento dado.
// Es un VALOR calculado en runtime, NO se persiste en SQLite.
type ShareHealth struct {
	Status HealthStatus `json:"status"` // "healthy" | "degraded" | "partial" | "unknown"
	Reason string       `json:"reason"` // "over_quota" | "near_quota" | "orphan_pool" | "not_mounted" | ""
	Detail string       `json:"detail"` // descripción legible (UI-friendly)

	// Campos opcionales para enriquecer la UI:
	UsagePercent int `json:"usagePercent,omitempty"` // 0-100+ (puede pasarse de 100 si over)
}

// ─── Thresholds ────────────────────────────────────────────────────────

// SharesHealthThresholds son los umbrales que deciden cuándo un share
// pasa de healthy a degraded.
//
// Centralizados aquí para que sean ajustables y visibles.
// Beta 8.1: valores fijos. Beta 9+: posiblemente configurables por usuario.
type SharesHealthThresholds struct {
	NearQuotaPercent int // % a partir del cual avisamos (default: 85)
}

// DefaultSharesHealthThresholds son los valores por defecto.
var DefaultSharesHealthThresholds = SharesHealthThresholds{
	NearQuotaPercent: 85,
}

// ═══════════════════════════════════════════════════════════════════════
// ComputeShareHealth — el cálculo principal
// ═══════════════════════════════════════════════════════════════════════

// ComputeShareHealth evalúa la salud de un ShareView.
//
// Orden de evaluación (primero = más prioritario):
//  1. orphan_pool  (pool no existe → degraded crítico)
//  2. not_mounted  (pool existe pero no montado → degraded crítico)
//  3. over_quota   (usando >=100% del límite → degraded)
//  4. near_quota   (usando >=NearQuotaPercent% → degraded preventivo)
//  5. healthy      (nada de lo anterior aplica)
//
// La función es PURA: dado el ShareView, siempre devuelve lo mismo.
// No tiene efectos secundarios. Testeable sin BTRFS ni SQLite.
func ComputeShareHealth(v ShareView) ShareHealth {
	return ComputeShareHealthWithThresholds(v, DefaultSharesHealthThresholds)
}

// ComputeShareHealthWithThresholds permite inyectar thresholds custom.
// Útil para tests y para futuras features de configuración usuario.
func ComputeShareHealthWithThresholds(v ShareView, th SharesHealthThresholds) ShareHealth {
	// CHECK 1: pool huérfano (PoolType vacío = resolveSharePool falló al enrich)
	if v.PoolType == "" {
		return ShareHealth{
			Status: HealthDegraded,
			Reason: "orphan_pool",
			Detail: "El pool de almacenamiento ya no existe",
		}
	}

	// CHECK 2: pool existe pero MountPoint vacío
	if v.MountPoint == "" {
		return ShareHealth{
			Status: HealthDegraded,
			Reason: "not_mounted",
			Detail: "El pool no está montado",
		}
	}

	// CHECKs 3-4: quota (solo si hay quota configurada)
	if v.Quota > 0 {
		usagePercent := int((v.Used * 100) / v.Quota)

		if v.Used >= v.Quota {
			return ShareHealth{
				Status:       HealthDegraded,
				Reason:       "over_quota",
				Detail:       "El share ha excedido su cuota. No se podrán escribir más archivos.",
				UsagePercent: usagePercent,
			}
		}

		if usagePercent >= th.NearQuotaPercent {
			return ShareHealth{
				Status:       HealthDegraded,
				Reason:       "near_quota",
				Detail:       "El share está cerca de su cuota. Considera ampliarla o liberar espacio.",
				UsagePercent: usagePercent,
			}
		}
	}

	// CHECK 5: todo bien
	usagePercent := 0
	if v.Quota > 0 {
		usagePercent = int((v.Used * 100) / v.Quota)
	}
	return ShareHealth{
		Status:       HealthHealthy,
		Reason:       "",
		Detail:       "",
		UsagePercent: usagePercent,
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Integración con ShareView
// ═══════════════════════════════════════════════════════════════════════

// EnrichWithHealth añade los campos de salud al map serializado.
// Llamado desde shares_http.go al construir la respuesta JSON.
//
// Mantenemos la integración EXPLÍCITA (no automática) para que el
// frontend reciba health solo donde se quiere (Beta 8.1: siempre, pero
// podría suprimirse en endpoints públicos en el futuro).
func EnrichShareViewWithHealth(view map[string]interface{}, v ShareView) {
	health := ComputeShareHealth(v)
	view["health"] = map[string]interface{}{
		"status":       health.Status,
		"reason":       health.Reason,
		"detail":       health.Detail,
		"usagePercent": health.UsagePercent,
	}
}
