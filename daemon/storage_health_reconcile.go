// storage_health_reconcile.go — FIX-2 · Cierre del SPLIT-BRAIN.
//
// PROBLEMA (incidente data8, junio 2026):
// La tarjeta del pool deriva su salud de la CACHÉ (SQLite + enrichPool), mientras
// el observer (storage_observer.go) calcula la VERDAD desde `btrfs filesystem
// show` y la expone como divergencias. Las dos fuentes no estaban reconciliadas:
// la tarjeta podía mostrar "healthy" mientras el observer ya veía un disco
// MISSING. Esa contradicción —"single · HEALTHY" con un devid ausente real— es
// el bug que más confusión causó durante la reparación.
//
// REGLA 16 (External Systems Own Their Facts):
// La realidad del kernel/btrfs es la ÚNICA autoridad; la BD es una caché. Cuando
// difieren, la realidad gana — pero SOLO para empeorar la salud, nunca para
// afirmar "sano" sobre lo que la caché creía roto. Una divergencia que afecta a
// un pool jamás puede dejarlo en "healthy".
//
// Esta es la primera mitad de FIX-2 (consumo de divergencias, downgrade-only).
// La alineación de membresía (corregir el "DEGRADED falso" por discos fantasma
// en la BD) es el módulo hermano y va aparte para mantener este pequeño y puro.

package main

import "strings"

// healthStatusRank ordena los estados de salud de mejor (0) a peor (4).
// Permite comparar severidades sin encadenar switches: la realidad solo puede
// mover el estado hacia un rank MAYOR (peor), nunca menor.
func healthStatusRank(status string) int {
	switch status {
	case "healthy":
		return 0
	case "at_risk":
		return 1
	case "unstable":
		return 2
	case "degraded":
		return 3
	case "critical":
		return 4
	case "missing":
		// El pool entero no se detecta: estado propio y el más severo de mostrar
		// (no es solo "degradado": faltan TODOS sus discos / no lo ve btrfs).
		return 5
	default:
		// Estado desconocido: lo tratamos como "healthy" (rank 0) para que
		// cualquier divergencia real pueda empeorarlo.
		return 0
	}
}

// divergenceAffectsManagedPool indica si una divergencia del observer se refiere
// a ESTE pool managed. Se empareja por PoolID (preferido) o por UUID de
// filesystem. Las divergencias orphan_filesystem NO afectan a un pool existente
// (son filesystems SIN fila en BD; eso lo cubre FIX-3, no la salud de la tarjeta).
func divergenceAffectsManagedPool(d Divergence, pool *Pool) bool {
	if pool == nil {
		return false
	}
	if d.Type == DivOrphanFilesystem {
		return false
	}
	if d.PoolID != "" && d.PoolID == pool.ID {
		return true
	}
	if d.FSUUID != "" && pool.BtrfsUUID != "" && d.FSUUID == pool.BtrfsUUID {
		return true
	}
	return false
}

// divergenceToHealthStatus traduce la severidad de una divergencia al estado de
// salud MÍNIMO que implica. Incluso una divergencia "info" (p.ej. pool no
// montado) impide afirmar "healthy": un pool no montado no está sano.
func divergenceToHealthStatus(d Divergence) string {
	// El pool entero no detectado → estado MISSING propio (FIX-5), sea cual sea
	// la severidad. No es "degradado": el pool no está físicamente.
	if d.Type == DivPoolNotDetected {
		return "missing"
	}
	switch d.Severity {
	case SeverityCritical:
		return "critical"
	case SeverityWarning:
		return "degraded"
	case SeverityInfo:
		return "at_risk"
	default:
		return ""
	}
}

// severityToDiagnosticInt mapea la severidad textual de la divergencia al int
// del tipo Diagnostic (1=info, 2=warning, 3=error, 4=critical).
func severityToDiagnosticInt(sev string) int {
	switch sev {
	case SeverityCritical:
		return 4
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 1
	}
}

// reconcileHealthWithDivergences es el CIERRE del split-brain (FIX-2).
//
// Tras enrichPool (que computa la salud desde la caché), esta función cruza el
// pool con las divergencias que el observer ya calculó desde btrfs. Si alguna
// divergencia afecta al pool y la realidad implica un estado PEOR que el de la
// caché, la realidad manda: se baja el status, se anota el motivo (para el banner
// de la UI) y se añaden los diagnósticos correspondientes.
//
// GARANTÍA: la salud solo puede empeorar aquí, nunca mejorar. Esto cierra el
// "HEALTHY falso" (peligroso: afirma "sano" sobre algo roto) sin riesgo de que
// un observer con un bug silencie un problema real marcando algo como sano.
//
// Función PURA sobre los datos pasados → testeable sin observer real.
func reconcileHealthWithDivergences(pool *Pool, divergences []Divergence) {
	if pool == nil || pool.Health == nil {
		return
	}

	worst := ""
	var details []string
	var codes []string

	for _, d := range divergences {
		if !divergenceAffectsManagedPool(d, pool) {
			continue
		}
		implied := divergenceToHealthStatus(d)
		if implied == "" {
			continue
		}
		if healthStatusRank(implied) > healthStatusRank(worst) {
			worst = implied
		}
		details = append(details, d.Detail)
		codes = append(codes, d.Type)

		// Adjuntar como diagnostic para que la UI tenga el texto del banner.
		pool.Health.Diagnostics = append(pool.Health.Diagnostics, Diagnostic{
			Code:     "observer_divergence",
			Severity: severityToDiagnosticInt(d.Severity),
			Detail:   d.Detail,
		})
	}

	// Solo EMPEORAR: si la realidad implica un estado peor que el de la caché,
	// la realidad gana. Si la caché ya era igual o peor, no tocamos el status.
	if worst != "" && healthStatusRank(worst) > healthStatusRank(pool.Health.Status) {
		pool.Health.Status = worst
		pool.Health.Reason = PoolHealthReason{
			Primary:   "reality_divergence",
			Message:   strings.Join(details, "; "),
			Secondary: codes,
		}
	}
}

// observerDivergencesFn devuelve las divergencias del snapshot actual del
// observer. Es var (no func) para que los tests puedan inyectar divergencias
// sin un observer real. En producción lee globalObserver de forma nil-safe
// (puede ser nil en boot temprano o en tests sin observer → sin reconciliación).
var observerDivergencesFn = func() []Divergence {
	if globalObserver == nil {
		return nil
	}
	snap := globalObserver.Snapshot()
	if snap == nil {
		return nil
	}
	return snap.Divergences
}
