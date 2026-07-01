// maintenance_torrent.go — Tarea: barrido de .torrent temporales.
//
// Barre <pool>/.nimos-tmp/torrents/ borrando ficheros más viejos que el grace
// period. Criterio de seguridad CONFIRMADO: torrentd hace
// make_shared<lt::torrent_info>(path) — lee el .torrent síncronamente dentro del
// POST. Al volver, el temp ya no se usa. Cualquier temp más viejo que el grace
// es basura segura.
//
// Cumple las 4 reglas del contrato:
//   1. refuse-if-uncertain → sin pool montado, Skipped (no inventa rutas).
//   2. skip-known          → no aplica (temps de un solo uso), pero respeta grace.
//   3. grace-period        → no borra ficheros recién creados (default 15 min).
//   4. log-everything      → registra cada borrado y el resultado.

package main

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

type torrentTmpSweepTask struct{}

func (t *torrentTmpSweepTask) ID() string       { return "torrent_tmp_sweep" }
func (t *torrentTmpSweepTask) Name() string     { return "Limpieza de temporales de torrent" }
func (t *torrentTmpSweepTask) Category() string { return MaintCategoryStorage }
func (t *torrentTmpSweepTask) Description() string {
	return "Borra ficheros .torrent temporales que ya fueron procesados por el motor de descargas."
}

func (t *torrentTmpSweepTask) DefaultSchedule() Schedule {
	return Schedule{Kind: ScheduleInterval, IntervalMinutes: 360, GraceMinutes: 15}
}

func (t *torrentTmpSweepTask) Run(ctx context.Context) MaintenanceResult {
	// Regla 1 (refuse-if-uncertain): sin pool montado no hay nada que barrer,
	// y NO caemos a una ruta del sistema. Skip, no error.
	mount, err := firstMountedPoolPath()
	if err != nil {
		return MaintenanceResult{Skipped: true, SkipReason: "no hay pool montado"}
	}
	tmpDir := filepath.Join(mount, ".nimos-tmp", "torrents")

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		// El directorio puede no existir aún (nunca se subió un torrent). No es error.
		if os.IsNotExist(err) {
			return MaintenanceResult{Skipped: true, SkipReason: "no hay directorio de temporales"}
		}
		return MaintenanceResult{Err: err}
	}

	// Grace period configurable (regla 3). Default 15 min si no hay override.
	grace := 15 * time.Minute
	if cfg := dbMaintenanceGetConfig(t.ID()); cfg.Schedule.GraceMinutes > 0 {
		grace = time.Duration(cfg.Schedule.GraceMinutes) * time.Minute
	}
	cutoff := time.Now().Add(-grace)

	var removed, bytes int64
	for _, e := range entries {
		if ctx.Err() != nil { // timeout/cancelación
			break
		}
		if e.IsDir() {
			continue // los temps son ficheros sueltos; no tocamos subdirectorios
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Regla 3 (grace-period): saltar ficheros recientes.
		if info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(tmpDir, e.Name())
		size := info.Size()
		if err := os.Remove(path); err != nil {
			logMsg("maintenance/torrent_tmp_sweep: no se pudo borrar %s: %v", path, err)
			continue
		}
		// Regla 4 (log-everything).
		logMsg("maintenance/torrent_tmp_sweep: borrado temporal %s (%d bytes)", e.Name(), size)
		removed++
		bytes += size
	}

	return MaintenanceResult{ItemsRemoved: removed, BytesFreed: bytes}
}
