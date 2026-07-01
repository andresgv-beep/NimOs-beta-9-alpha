// maintenance_orphan_dirs.go — Tarea: limpieza de directorios huérfanos en
// /nimos/pools/.
//
// Conecta cleanOrphanPoolDirs (que antes solo corría al boot y tras destruir un
// pool) al subsistema de mantenimiento, para poder lanzarla desde el Control
// Panel sin reiniciar el daemon.
//
// Huérfano = directorio en /nimos/pools/ que (1) no corresponde a ningún pool
// conocido en la BD, (2) no tiene nada montado encima, y (3) no es reciente
// (grace period). Son restos de pools destruidos o renombrados.
//
// Cumple las 4 reglas del contrato de mantenimiento, heredadas de
// cleanOrphanPoolDirsResult:
//   1. refuse-if-uncertain → si la config no tiene pools conocidos, SE NIEGA a
//      limpiar (un config corrupto borraría todo) → Skipped, no error.
//   2. skip-known          → nunca toca un mount point de un pool de la BD.
//   3. grace-period        → no borra directorios creados hace <5min (race con
//      CreatePool).
//   4. log-everything      → registra cada borrado y la decisión.

package main

import "context"

type orphanDirSweepTask struct{}

func (t *orphanDirSweepTask) ID() string       { return "orphan_dir_sweep" }
func (t *orphanDirSweepTask) Name() string     { return "Limpieza de directorios huérfanos" }
func (t *orphanDirSweepTask) Category() string { return MaintCategoryStorage }
func (t *orphanDirSweepTask) Description() string {
	return "Borra carpetas en /nimos/pools/ que quedaron de pools destruidos o renombrados (no asociadas a ningún pool actual y sin nada montado)."
}

func (t *orphanDirSweepTask) DefaultSchedule() Schedule {
	// at_boot: mantiene el comportamiento histórico (cleanOrphanPoolDirs ya
	// corría al arranque). Además, al estar registrada como tarea, se puede
	// lanzar manualmente desde el Control Panel (botón "ejecutar") sin
	// reiniciar — que es justo lo que faltaba. No la ponemos en interval/daily
	// porque es destructiva (borra dirs con contenido); mejor a conciencia.
	return Schedule{Kind: ScheduleAtBoot}
}

func (t *orphanDirSweepTask) Run(ctx context.Context) MaintenanceResult {
	// Usa la variante GUARDED: se abstiene si hay operaciones de storage en
	// curso (un pool podría estar en transición, p.ej. cambiando un disco).
	removed, bytesFreed, skipped, skipReason := cleanOrphanPoolDirsGuarded()
	if skipped {
		return MaintenanceResult{Skipped: true, SkipReason: skipReason}
	}
	return MaintenanceResult{ItemsRemoved: removed, BytesFreed: bytesFreed}
}
