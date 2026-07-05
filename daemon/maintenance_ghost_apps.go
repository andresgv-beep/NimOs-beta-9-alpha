// maintenance_ghost_apps.go — Tarea: limpieza de apps fantasma del registro.
//
// ORIGEN (caso real, Pi 2026-07-05): el config-backup del pool restaura la BD
// de NimOS en una instalación fresca, y con ella el registro de apps
// (docker_apps)… pero los contenedores y stacks de esas apps viven en el
// data-root de Docker de la instalación ANTERIOR. Resultado: el cajón de apps
// se llena de "apps instaladas" sin ningún respaldo real en Docker — iconos
// fantasma que muestran "Detenida" para siempre y no se pueden arrancar.
//
// FANTASMA = fila de docker_apps que cumple TODO esto:
//   - sin ningún contenedor asociado (ni por label com.nimos.app_id — el
//     matching robusto —, ni por nombre legacy, ni por proyecto compose),
//   - sin directorio de stack en <dockerPath>/stacks/<id> (si el stack
//     existe, la app es reparable/re-arrancable: NO es fantasma),
//   - instalada hace más de 1h (grace-period: una instalación en curso
//     todavía no tiene contenedor y NO es un fantasma).
//
// La acción es EXACTAMENTE la del desinstalador (DELETE /api/installed-apps/:id
// → appsRepo.DeleteDockerApp): borrar la fila del registro. JAMÁS toca Docker
// ni datos del pool (LÍNEA ROJA del subsistema) — si no hay contenedor ni
// stack, no hay nada más que borrar.
//
// Cumple el contrato de mantenimiento:
//  1. refuse-if-uncertain → se SALTA (no borra nada) si Docker no está
//     instalado, si su daemon no responde, o si el data-root no está sobre un
//     pool montado (checkDockerDataRoot): en cualquiera de esos estados una
//     app sana parecería fantasma. En la duda, ni un DELETE.
//  2. skip-known → contenedor O stack presentes = app real, no se toca.
//  3. grace-period → 1h desde installed_at antes de considerar fantasma.
//  4. log-everything → registra cada fila borrada y el porqué.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ghostAppSweepTask struct{}

func (t *ghostAppSweepTask) ID() string       { return "ghost_app_sweep" }
func (t *ghostAppSweepTask) Name() string     { return "Limpieza de apps fantasma" }
func (t *ghostAppSweepTask) Category() string { return MaintCategoryDocker }
func (t *ghostAppSweepTask) Description() string {
	return "Quita del registro (y del cajón de apps) las apps que ya no existen de verdad: sin contenedor Docker y sin stack en el pool. Suele pasar tras restaurar la configuración desde un pool en una instalación nueva. No toca Docker ni datos del pool: solo borra el registro, igual que el botón Desinstalar. Si Docker está caído o el pool desmontado, la tarea se salta (no borra nada en la duda)."
}

func (t *ghostAppSweepTask) DefaultSchedule() Schedule {
	// Semanal, domingo de madrugada como sus hermanas Docker. Los fantasmas
	// aparecen en eventos raros (restore, borrados a mano); no urge más.
	return Schedule{Kind: ScheduleWeekly, AtWeekday: 0, AtHour: 5, AtMinute: 0}
}

// ghostAppGrace — margen desde installed_at antes de poder ser fantasma.
const ghostAppGrace = time.Hour

func (t *ghostAppSweepTask) Run(ctx context.Context) MaintenanceResult {
	// 1. refuse-if-uncertain · sin Docker sano y con su storage montado, una
	// app real es indistinguible de un fantasma → no se borra NADA.
	if !isDockerInstalledGo() {
		return MaintenanceResult{Skipped: true, SkipReason: "Docker no instalado"}
	}
	if st := checkDockerDataRoot(); !st.Safe {
		return MaintenanceResult{Skipped: true, SkipReason: "storage de Docker no disponible: " + st.Reason}
	}
	if _, ok := runSafe("docker", "info", "--format", "{{.ServerVersion}}"); !ok {
		return MaintenanceResult{Skipped: true, SkipReason: "el daemon de Docker no responde"}
	}

	// ListDockerApps ya excluye deleting=1 (desinstalación en curso).
	apps, err := appsRepo.ListDockerApps(ctx)
	if err != nil {
		return MaintenanceResult{Err: fmt.Errorf("listando apps: %w", err)}
	}

	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)

	now := time.Now()
	var removed int64
	for _, app := range apps {
		// 3. grace-period · instalaciones recientes no son fantasmas todavía.
		if withinGhostGrace(app.InstalledAt, now) {
			continue
		}
		// 2. skip-known · contenedor o stack presentes = app real.
		if appHasAnyContainer(app.ID) {
			continue
		}
		if appHasStackDir(dockerPath, app.ID) {
			continue
		}

		// Fantasma confirmado: misma acción que el desinstalador.
		if err := appsRepo.DeleteDockerApp(ctx, app.ID); err != nil {
			logMsg("maintenance: ghost_app_sweep · error borrando %q: %v", app.ID, err)
			continue
		}
		// 4. log-everything.
		logMsg("maintenance: ghost_app_sweep · %q eliminada del registro (sin contenedor ni stack — fantasma)", app.ID)
		removed++
	}

	if removed > 0 {
		logMsg("maintenance: ghost_app_sweep · %d app(s) fantasma limpiadas del registro", removed)
	}
	return MaintenanceResult{ItemsRemoved: removed, BytesFreed: 0}
}

// ── Detección (helpers finos sobre exec/fs + decisión pura) ─────────────────

// appHasAnyContainer comprueba si existe ALGÚN contenedor (vivo o parado)
// asociado a la app, por las tres vías conocidas, de más robusta a legacy:
//  1. label com.nimos.app_id=<id>   (matching robusto, item 6 backlog)
//  2. label com.docker.compose.project=<safeId>  (stacks compose)
//  3. nombre exacto del contenedor  (apps 'container' legacy)
func appHasAnyContainer(appID string) bool {
	safe := sanitizeDockerNameGo(appID)
	if out, ok := runSafe("docker", "ps", "-a", "--filter", "label="+LabelAppID+"="+appID, "--format", "{{.Names}}"); ok && strings.TrimSpace(out) != "" {
		return true
	}
	if out, ok := runSafe("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+safe, "--format", "{{.Names}}"); ok && strings.TrimSpace(out) != "" {
		return true
	}
	if _, ok := runSafe("docker", "inspect", "--format", "{{.Id}}", safe); ok {
		return true
	}
	return false
}

// appHasStackDir comprueba si la app conserva su directorio de stack
// (<dockerPath>/stacks/<safeId>). Si existe, la app es reparable → no fantasma.
func appHasStackDir(dockerPath, appID string) bool {
	if dockerPath == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(dockerPath, "stacks", sanitizeDockerNameGo(appID)))
	return err == nil && fi.IsDir()
}

// withinGhostGrace decide si una app está aún en periodo de gracia según su
// installed_at (RFC3339). PURA. Fecha vacía o ilegible → CON gracia
// (refuse-if-uncertain también aquí: una fila con fecha corrupta no debe
// borrarse por un defecto de parseo).
func withinGhostGrace(installedAt string, now time.Time) bool {
	if installedAt == "" {
		return true // sin fecha → duda → no tocar
	}
	t, err := time.Parse(time.RFC3339, installedAt)
	if err != nil {
		return true // fecha ilegible → duda → no tocar
	}
	return now.Sub(t) < ghostAppGrace
}
