// docker_reconciler.go — Reconciler de apps Docker (Beta 8.2 · Fase 3).
//
// Origen: bug Nextcloud (26/05/2026). Un container quedó vivo en Docker pero
// sin row en docker_apps (el INSERT se abortó por r.Context() cancelado).
// Resultado: app invisible para AppStore/NimShield/Updates. Fase 1
// (commitContext) previene la causa; este reconciler es la red de seguridad
// que REPARA inconsistencias que escapen al patrón.
//
// ─────────────────────────────────────────────────────────────────────────
// QUÉ HACE (Nivel 2 · auto-import de huérfanos)
// ─────────────────────────────────────────────────────────────────────────
//
// Cada 5 min (y 1 vez al arranque vía RunOnce desde main), compara:
//
//   Fuente de verdad A · containers Docker con label com.nimos.managed=true
//                        (listNimOSContainers · docker_labels.go)
//   Fuente de verdad B · rows en docker_apps (AppsRepo)
//
// Detección y acción:
//
//   Container con label, SIN row en docker_apps → HUÉRFANO
//      → reconstruye la row usando los labels com.nimos.* del container
//      → emite log + (futuro) evento
//
// Lo que este reconciler NO hace (decisión consciente · DISCIPLINE §1):
//
//   - NO marca apps como "stopped" si su container desapareció. Durante el
//     arranque del daemon, Docker puede tardar en levantar containers y un
//     barrido prematuro daría falsos positivos. Eso es Nivel 3, se añade si
//     aparece el caso de uso real.
//   - NO fuerza cleanup de rows deleting=1 viejas. Idem · Nivel 3.
//   - NO toca containers SIN label com.nimos.managed (containers ajenos que
//     el usuario lanzó por su cuenta vía SSH · no son de NimOS).
//
// ─────────────────────────────────────────────────────────────────────────
// POR QUÉ LABELS Y NO MATCHING POR NOMBRE
// ─────────────────────────────────────────────────────────────────────────
//
// NimHealth (nimhealth_docker.go) detecta orphans por heurística de nombres
// (immich → immich_server, etc). Eso es frágil. Este reconciler usa los
// labels com.nimos.app_id que son fuente de verdad explícita: el container
// DICE a qué app pertenece, no lo adivinamos. Por eso el reconciler puede
// reconstruir la row con certeza.

package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// dockerAppScheduler · ReconcilerScheduler global del módulo Docker apps.
// Declarado aquí (su módulo natural), registrado y arrancado en main.go,
// mismo patrón que nimhealthScheduler.
var dockerAppScheduler *ReconcilerScheduler

// ─────────────────────────────────────────────────────────────────────────
// DockerAppReconciler
// ─────────────────────────────────────────────────────────────────────────

// DockerAppReconciler implementa Reconciler. Detecta containers gestionados
// por NimOS (label com.nimos.managed=true) que no tienen row en docker_apps
// y los reimporta.
type DockerAppReconciler struct {
	repo  *AppsRepo
	clock Clock

	// listContainers permite inyectar un fake en tests. En producción es
	// listNimOSContainers (docker_labels.go). Si es nil, usa el real.
	listContainers func(ctx context.Context) ([]NimOSContainer, error)

	// inspectContainer permite inyectar un fake en tests para obtener los
	// labels completos de un container. En producción es
	// getNimOSContainerLabels. Si es nil, usa el real.
	inspectContainer func(ctx context.Context, containerID string) (map[string]string, error)
}

// NewDockerAppReconciler crea el reconciler. Si clock es nil usa RealClock.
func NewDockerAppReconciler(repo *AppsRepo, clock Clock) *DockerAppReconciler {
	if clock == nil {
		clock = NewRealClock()
	}
	return &DockerAppReconciler{
		repo:             repo,
		clock:            clock,
		listContainers:   listNimOSContainers,
		inspectContainer: getNimOSContainerLabels,
	}
}

// Name implementa Reconciler.
func (r *DockerAppReconciler) Name() string { return "docker_apps" }

// Tier implementa Reconciler. Medium: si falla, las apps huérfanas no se
// reimportan automáticamente, pero el sistema sigue funcionando y el usuario
// puede reinstalar manualmente. No es Critical (el daemon arranca igual).
func (r *DockerAppReconciler) Tier() ReconcilerTier { return TierMedium }

// Interval implementa Reconciler. 5 min es suficiente para auto-recuperación
// sin presión sobre el daemon Docker (cada tick hace 1 docker ps + N inspect
// solo de huérfanos detectados, que normalmente son 0).
func (r *DockerAppReconciler) Interval() time.Duration { return 5 * time.Minute }

// Reconcile implementa Reconciler. Detecta huérfanos y los reimporta.
func (r *DockerAppReconciler) Reconcile(ctx context.Context) error {
	// 1. Containers gestionados por NimOS (label com.nimos.managed=true)
	containers, err := r.listContainers(ctx)
	if err != nil {
		return fmt.Errorf("list nimos containers: %w", err)
	}
	if len(containers) == 0 {
		return nil // nada que reconciliar
	}

	// 2. Apps registradas en BD (incluyendo las que están deleting, para no
	//    reimportar algo que el usuario está desinstalando ahora mismo).
	apps, err := r.repo.ListDockerAppsIncludingDeleting(ctx)
	if err != nil {
		return fmt.Errorf("list docker apps: %w", err)
	}

	// Set de app_ids ya registrados para lookup O(1).
	registered := make(map[string]bool, len(apps))
	for _, a := range apps {
		registered[a.ID] = true
	}

	// 3. Detectar huérfanos: containers con app_id que NO está en docker_apps.
	//
	//    Agrupamos por app_id porque un stack tiene varios containers con el
	//    mismo com.nimos.app_id; solo necesitamos UNA row por app, no una por
	//    container.
	seen := make(map[string]bool)
	imported := 0
	for _, c := range containers {
		if c.AppID == "" {
			logMsg("docker_reconciler: container %s (%s) tiene managed=true pero app_id vacío, omitido",
				c.Name, c.ID)
			continue
		}
		if registered[c.AppID] {
			continue // ya tiene row, todo bien
		}
		if seen[c.AppID] {
			continue // ya importamos este app_id en este ciclo (otro container del mismo stack)
		}
		seen[c.AppID] = true

		// HUÉRFANO detectado · reimportar.
		if err := r.reimportOrphan(ctx, c); err != nil {
			logMsg("docker_reconciler: failed to reimport orphan %q: %v", c.AppID, err)
			continue
		}
		imported++
		logMsg("docker_reconciler: reimported orphan app %q (container %s) · "+
			"era un huérfano (container vivo sin row en docker_apps)", c.AppID, c.Name)
	}

	if imported > 0 {
		logMsg("docker_reconciler: %d app(s) huérfana(s) reimportada(s)", imported)
	}
	return nil
}

// reimportOrphan reconstruye la row de docker_apps usando los labels
// com.nimos.* del container huérfano.
func (r *DockerAppReconciler) reimportOrphan(ctx context.Context, c NimOSContainer) error {
	// Obtener los labels completos del container para reconstruir la row con
	// la máxima fidelidad posible (installed_by, installed_at, etc).
	labels, err := r.inspectContainer(ctx, c.ID)
	if err != nil {
		// Si no podemos inspeccionar, reconstruimos con lo mínimo del listado.
		logMsg("docker_reconciler: inspect %s falló (%v), reimport con datos mínimos", c.ID, err)
		labels = map[string]string{}
	}

	appType := "container"
	if c.IsStack {
		appType = "stack"
	}

	installedBy := labels[LabelInstalledBy]
	if installedBy == "" {
		installedBy = "system-recovery" // marca que fue auto-importado
	}
	installedAt := labels[LabelInstalledAt]
	if installedAt == "" {
		installedAt = r.clock.Now().UTC().Format(time.RFC3339)
	}

	app := &DBDockerApp{
		ID:          c.AppID,
		Name:        c.AppID, // mejor esfuerzo · el nombre bonito se perdió, usamos el id
		Image:       "",      // desconocido desde labels · se completará en el próximo update-check
		Type:        appType,
		OpenMode:    "internal",
		InstalledBy: installedBy,
		InstalledAt: installedAt,
	}

	// commitContext() · el reimport debe persistir sí o sí. Es exactamente
	// el patrón Fase 1: esta operación de recovery NO debe abortarse porque
	// un context de fondo se cancele.
	if err := r.repo.CreateOrUpdateDockerApp(commitContext(), app); err != nil {
		return fmt.Errorf("create row: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────
// Endpoint admin · disparo manual
// ─────────────────────────────────────────────────────────────────────────

// handleReconcileApps · POST /api/admin/reconcile-apps
//
// Dispara el reconciler de Docker apps on-demand, sin esperar al siguiente
// tick de 5 min. Útil tras detectar una inconsistencia o tras un crash.
// Admin only.
func handleReconcileApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed")
		return
	}
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	if dockerAppScheduler == nil {
		jsonError(w, 503, "Docker reconciler not initialized")
		return
	}

	// RunOnce usa el context del request · aquí está bien porque es una
	// acción interactiva: si el admin cancela, abortar es correcto. El
	// reimport interno usa commitContext() de todas formas.
	if err := dockerAppScheduler.RunOnce(r.Context(), "docker_apps"); err != nil {
		logMsg("docker_reconciler: manual reconcile failed: %v", err)
		jsonError(w, 500, "Reconcile failed: "+err.Error())
		return
	}

	jsonOk(w, map[string]interface{}{
		"ok":      true,
		"message": "Reconcile completado",
	})
}
