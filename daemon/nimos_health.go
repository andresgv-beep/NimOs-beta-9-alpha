// nimos_health.go — Tipo global HealthStatus.
//
// Antes vivía duplicado: storage usaba constantes string sueltas
// (HealthHealthy, HealthIncomplete, ...) y network había definido un
// tipo HealthStatus local con prefijo Net*. Esta duplicación se
// detectó tras F-002 (Observer) y se resolvió antes de F-006 para
// evitar que un tercer consumidor amplificara el coste del refactor
// (ver TECH_DEBT_NETWORK_V4.md · D-001).
//
// Diseño:
//
//   - `HealthStatus` es un tipo definido sobre `string` — admite
//     asignación directa desde literales y conversión a string sin
//     coste. Eso permite que los structs antiguos que tenían
//     `Status string` sigan funcionando: las constantes nuevas se
//     asignan transparentemente.
//
//   - Las constantes cubren TODOS los valores ya usados por ambos
//     módulos:
//       storage: healthy, incomplete, degraded, partial, unknown
//       network: healthy, degraded, failed
//       network_observed CHECK: healthy, degraded, failed, partial,
//                               unknown, stale
//
//   - "stale" no aparece en el código vivo todavía, pero está en el
//     schema CHECK porque se reserva para el caso de un observer cuyo
//     último snapshot es demasiado antiguo para confiar.
//
// Nombre de constantes: `HealthStatus*` (no `HealthStatusHealthy` que
// sería redundante; usamos `HealthHealthy` directamente, manteniendo
// los nombres que storage ya tenía). Esto preserva la compatibilidad
// con todos los consumidores actuales sin rewriting masivo.

package main

// HealthStatus es el estado de salud agregado de un módulo o entidad.
//
// Tipo definido (no alias) sobre string. Esto da type-safety: solo se
// pueden asignar constantes Health* o conversiones explícitas. Los
// structs que antes tenían `Status string` se cambian a
// `Status HealthStatus`. La serialización a JSON sigue produciendo
// strings, así que la API externa no cambia.
type HealthStatus string

// Constantes globales de health. Reemplazan tanto las constantes
// untyped de storage_observe_types.go (que se borran en este refactor)
// como las NetHealth* de network_observer.go.
const (
	HealthHealthy    HealthStatus = "healthy"
	HealthDegraded   HealthStatus = "degraded"
	HealthFailed     HealthStatus = "failed"
	HealthPartial    HealthStatus = "partial"
	HealthIncomplete HealthStatus = "incomplete"
	HealthUnknown    HealthStatus = "unknown"
	HealthStale      HealthStatus = "stale"
)
