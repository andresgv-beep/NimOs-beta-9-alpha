package main

// ═══════════════════════════════════════════════════════════════════════
// docker_updates_http.go · Endpoints HTTP del feature Updates
// ───────────────────────────────────────────────────────────────────────
// Sprint Updates · 25/05/2026
//
// Endpoints:
//   GET  /api/docker/updates-summary           · count + lista para sidebar
//   GET  /api/docker/app/<id>/update-check     · estado de update de 1 app
//   POST /api/docker/app/<id>/update           · ejecutar update de 1 app
//
// Patrón TTL · update-check evita llamar al registry si remote_checked_at
// es reciente (< 6h). El frontend puede forzar con ?force=true (ej. tras
// pulsar "Comprobar ahora" en algún botón futuro).
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"
)

// updateCheckTTL · tiempo mínimo entre llamadas al registry remoto por imagen.
// Si la última comprobación fue hace menos que esto, se usa el cache de BD.
// 6h es el sweet spot: bajo para detectar updates en el día, alto para no
// abusar de Docker Hub anonymous (rate limit 100/6h por IP).
const updateCheckTTL = 6 * time.Hour

// dockerUpdatesSummary · GET /api/docker/updates-summary
//
// Devuelve un sumario de las apps con updates disponibles. El sidebar lo
// usa para mostrar el icono junto a "Instaladas" y el contador.
//
// Esta query NO llama al registry · solo lee BD. Es barata (un GROUP BY
// sobre docker_app_images con índice). Se puede llamar cada vez que el
// frontend lo necesite.
//
// Response:
//
//	{
//	  "ok": true,
//	  "count": 3,
//	  "apps": [
//	    { "appId": "immich", "servicesTotal": 4, "servicesWithUpdate": 2, "oldestCheckAt": "..." },
//	    { "appId": "jellyfin", "servicesTotal": 1, "servicesWithUpdate": 1, "oldestCheckAt": "..." }
//	  ]
//	}
func dockerUpdatesSummary(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if appImagesRepo == nil {
		jsonError(w, 500, "App images repo not initialized")
		return
	}

	apps, err := appImagesRepo.ListAppsWithUpdates(r.Context())
	if err != nil {
		logMsg("docker: updates-summary failed: %v", err)
		jsonError(w, 500, "Failed to query updates summary")
		return
	}

	// Asegurar slice no-nil para que el JSON sea [] y no null
	if apps == nil {
		apps = []AppUpdateSummary{}
	}

	jsonOk(w, map[string]interface{}{
		"ok":    true,
		"count": len(apps),
		"apps":  apps,
	})
}

// dockerUpdateCheck · GET /api/docker/app/<id>/update-check
//
// Devuelve el estado de actualización de una app concreta. Si el cache de
// la BD está caducado (>TTL), llama al registry para refrescar.
//
// Query params:
//   - force=true · ignora el TTL, fuerza refresh desde registry
//
// Response:
//
//	{
//	  "ok": true,
//	  "updateAvailable": true,
//	  "services": [
//	    {
//	      "name": "immich-server",
//	      "image": "ghcr.io/immich-app/immich-server:release",
//	      "updateAvailable": true,
//	      "localDigest": "sha256:abc",
//	      "remoteDigest": "sha256:xyz",
//	      "remoteCheckedAt": "2026-05-25T...",
//	      "checkStatus": "ok"
//	    },
//	    ...
//	  ]
//	}
//
// Si la app no tiene servicios tracked (ej. instalada antes del sprint),
// devuelve services=[] y updateAvailable=false. El frontend graceful.
func dockerUpdateCheck(w http.ResponseWriter, r *http.Request, appID string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if appImagesRepo == nil {
		jsonError(w, 500, "App images repo not initialized")
		return
	}

	safeID := sanitizeDockerNameGo(appID)
	if safeID == "" {
		jsonError(w, 400, "Invalid app id")
		return
	}

	force := r.URL.Query().Get("force") == "true"

	// 1. Leer estado actual de BD
	images, err := appImagesRepo.GetByApp(r.Context(), safeID)
	if err != nil {
		logMsg("docker: update-check GetByApp(%s) failed: %v", safeID, err)
		jsonError(w, 500, "Failed to query app images")
		return
	}

	// 2. Decidir si refrescamos desde registry
	needsRefresh := force
	if !needsRefresh {
		for _, img := range images {
			if img.NeedsRemoteCheck(updateCheckTTL) {
				needsRefresh = true
				break
			}
		}
	}

	// 3. Si necesita refresh · llamar al registry en paralelo y actualizar BD
	if needsRefresh && len(images) > 0 {
		// Context con timeout amplio · 5 imagenes * 15s timeout cada una = ~75s peor caso.
		// Usamos un timeout total más conservador.
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		_, err := refreshRemoteDigestsForApp(ctx, appImagesRepo, safeID)
		if err != nil {
			logMsg("docker: refresh remote digests for %s: %v", safeID, err)
			// No abortamos · devolvemos lo que haya en BD
		}

		// Releer las imágenes actualizadas
		images, err = appImagesRepo.GetByApp(r.Context(), safeID)
		if err != nil {
			logMsg("docker: re-read after refresh failed for %s: %v", safeID, err)
		}
	}

	// 4. Construir response
	services := make([]map[string]interface{}, 0, len(images))
	anyUpdate := false
	for _, img := range images {
		hasUpdate := img.HasUpdate()
		if hasUpdate {
			anyUpdate = true
		}
		services = append(services, map[string]interface{}{
			"name":            img.ServiceName,
			"image":           img.Image,
			"updateAvailable": hasUpdate,
			"localDigest":     img.LocalDigest,
			"remoteDigest":    img.RemoteDigest,
			"remoteCheckedAt": img.RemoteCheckedAt,
			"checkStatus":     img.CheckStatus,
		})
	}

	jsonOk(w, map[string]interface{}{
		"ok":              true,
		"updateAvailable": anyUpdate,
		"services":        services,
	})
}

// dockerAppUpdate · POST /api/docker/app/<id>/update
//
// Ejecuta el update de una app: `docker compose pull && docker compose up -d`
// Tras éxito, actualiza local_digest en BD (igual a remote_digest, app al día).
//
// Con ?async=true (recomendado · UPD-ASYNC): responde 202 {operationId,
// pollUrl} al instante y ejecuta el trabajo en background reportando
// progreso por fases (10/50/85/95/100). Esto elimina el 502 del proxy en
// updates largos (Immich en Pi: 5-15 min > timeout de Caddy) donde el
// trabajo terminaba bien pero la respuesta HTTP se perdía y la UI
// mostraba "invalid JSON response (status 502)".
//
// Sin ?async=true funciona síncrono · backward compat (30s-2min en apps
// pequeñas; en grandes puede chocar con el timeout del proxy).
//
// Response sync:
//
//	{ "ok": true, "appId": "immich" }
//
// Solo funciona para apps tipo 'stack' (las que tienen compose YAML).
// Para single-container, hay que re-pull + re-create · diferente flujo.
// De momento solo soportamos stacks; el frontend lo gestiona.
func dockerAppUpdate(w http.ResponseWriter, r *http.Request, appID string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}

	safeID := sanitizeDockerNameGo(appID)
	if safeID == "" {
		jsonError(w, 400, "Invalid app id")
		return
	}

	// Localizar el stack path · el compose vive en {dockerPath}/stacks/{id}
	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		dp, err := getDockerPath()
		if err != nil {
			jsonError(w, 400, "Docker not configured")
			return
		}
		dockerPath = dp
	}
	stackPath := filepath.Join(dockerPath, "stacks", safeID)
	composePath := filepath.Join(stackPath, "docker-compose.yml")

	if isAsyncRequested(r) {
		// Async path · UPD-ASYNC. Mismo patrón que dockerPull/dockerInstall.
		op, err := operationsRepo.Create(r.Context(), "docker.app_update", session.Username)
		if err != nil {
			jsonError(w, 500, "Failed to create operation: "+err.Error())
			return
		}
		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return runAppUpdateWork(ctx, safeID, stackPath, composePath, op.ID)
		})
		writeAsyncAccepted(w, op)
		return
	}

	// Sync path (legacy)
	result, err := runAppUpdateWork(r.Context(), safeID, stackPath, composePath, "")
	if err != nil {
		writeWorkerError(w, err)
		return
	}
	jsonOk(w, result)
}

// runAppUpdateWork · trabajo real del update de una app stack.
//
// Función pura sin acceso a HTTP (patrón runDockerPullWork). Si opID != ""
// reporta progreso por fases a operationsRepo · tramos de la barra en UI:
//
//	10  Descargando imágenes      (compose pull)
//	50  Recreando contenedores    (compose up -d --pull always --force-recreate)
//	85  Verificando servicios     (refresh de digests locales en BD)
//	95  Actualizando registro     (invalidar cache NimHealth)
//	100 succeeded                 (lo marca runWorkerAsync)
//
// Los subprocess usan commitContext() + timeout 15 min: si el cliente se
// desconecta a mitad NO se mata el docker compose (dejaría imágenes a medias
// y containers inconsistentes).
func runAppUpdateWork(ctx context.Context, safeID, stackPath, composePath, opID string) (map[string]interface{}, error) {
	updateOpProgressSafe(ctx, opID, 10, "Descargando imágenes…")

	// 1. compose pull · descarga las imágenes nuevas (NO toca containers vivos).
	// Timeout 15 min · stacks pesados como Immich tienen 4+ imágenes (varios GB
	// total) y en Pi 4 con red doméstica pueden tardar fácil 5-10 min. Mejor
	// pasarse de tiempo que matar el pull a medias y dejar imágenes corruptas.
	//
	// NOTA · este pull suele ser idempotente (si el digest local coincide con
	// el del registry, no descarga). El paso 2 con --pull always es el que
	// FUERZA realmente la descarga al recrear · el pull aquí solo "calienta"
	// el cache para que el up -d sea rápido.
	//
	// commitContext() · si el cliente se desconecta a mitad, NO queremos
	// matar el subprocess Docker (dejaría imágenes a medias y containers
	// inconsistentes). El timeout sigue siendo 15min absolutos.
	pullCtx, cancel := context.WithTimeout(commitContext(), 15*time.Minute)
	defer cancel()
	pullCmd := exec.CommandContext(pullCtx, "docker", "compose", "-f", composePath, "pull")
	pullCmd.Dir = stackPath
	if out, err := pullCmd.CombinedOutput(); err != nil {
		logMsg("docker: app update pull failed for %s: %v (output: %s)", safeID, err, string(out))
		return nil, asHTTPError(500, "Pull failed: %s", string(out))
	}

	updateOpProgressSafe(ctx, opID, 50, "Recreando contenedores…")

	// 2. compose up -d --pull always --force-recreate
	//
	// IMPORTANTE: la combinación CORRECTA tras debugging real (Pi, Immich):
	//
	//   --pull always        · obliga a Docker a contactar el registry y traer
	//                          la imagen aunque el TAG ya exista local. Sin esto,
	//                          compose ve que tiene `:release` local y no descarga
	//                          aunque el digest haya cambiado · update fantasma
	//                          (containers se recrean pero con imagen vieja).
	//
	//   --force-recreate     · obliga a recrear containers incluso si la
	//                          config compose no cambió. Sin esto, los
	//                          containers nuevos no se generarían aunque
	//                          la imagen ya estuviera actualizada.
	//
	// La combinación de las dos garantiza que se descarga lo nuevo Y se
	// activa en containers vivos · es el equivalente a "watchtower update"
	// que hace Portainer/TrueNAS/Synology bajo el capó.
	//
	// Timeout 15 min · pull + recreate + healthchecks de 4 containers Immich
	// pueden tardar fácil 5-10 min.
	//
	// commitContext() · idem que arriba · no matar el recreate si cliente se va.
	upCtx, cancel2 := context.WithTimeout(commitContext(), 15*time.Minute)
	defer cancel2()
	upCmd := exec.CommandContext(upCtx, "docker", "compose", "-f", composePath, "up", "-d", "--pull", "always", "--force-recreate")
	upCmd.Dir = stackPath
	if out, err := upCmd.CombinedOutput(); err != nil {
		logMsg("docker: app update up -d failed for %s: %v (output: %s)", safeID, err, string(out))
		return nil, asHTTPError(500, "Restart failed: %s", string(out))
	}

	updateOpProgressSafe(ctx, opID, 85, "Verificando servicios…")

	// 3. Refrescar digests locales (ahora son los nuevos) en BD.
	// Inline (antes goroutine): en el worker async no bloquea ninguna
	// respuesta HTTP y así el progreso refleja el paso real. Si falla,
	// update-check los refrescará al abrir el detail (no es fatal).
	refreshLocalDigestsAfterUpdate(commitContext(), safeID, composePath, stackPath)

	updateOpProgressSafe(ctx, opID, 95, "Actualizando registro…")

	// 4. Invalidar cache de NimHealth.
	// commitContext() · refresh debe completarse aunque cliente se haya ido.
	// Otros consumidores (UI, observers) leerán esta cache.
	ForceDockerCacheRefresh(commitContext())

	logMsg("docker: app %s updated successfully", safeID)
	return map[string]interface{}{
		"ok":    true,
		"appId": safeID,
	}, nil
}

// refreshLocalDigestsAfterUpdate actualiza local_digest en BD para todos los
// servicios de una app tras `compose pull && up -d`. Las imágenes nuevas
// están descargadas; sus digests son ahora los "remotos" que veíamos antes.
//
// Se llama inline desde runAppUpdateWork (fase 85%) · en async no bloquea
// ninguna respuesta HTTP y el progreso refleja el paso real.
func refreshLocalDigestsAfterUpdate(ctx context.Context, appID, composePath, stackDir string) {
	if appImagesRepo == nil {
		return
	}
	services, err := getComposeServices(ctx, composePath, stackDir)
	if err != nil {
		logMsg("docker: refreshLocalDigestsAfterUpdate(%s): %v", appID, err)
		return
	}
	for _, svc := range services {
		digest, err := getLocalImageDigest(ctx, svc.Image)
		if err != nil {
			logMsg("docker: post-update digest %s/%s: %v", appID, svc.Name, err)
			continue
		}
		if err := appImagesRepo.UpdateLocalDigest(ctx, appID, svc.Name, digest); err != nil {
			logMsg("docker: UpdateLocalDigest post-update %s/%s: %v", appID, svc.Name, err)
		}
	}
}
