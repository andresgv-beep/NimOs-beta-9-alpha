// docker_containers.go — Lifecycle de containers individuales (Beta 8.1)
//
// Endpoints:
//   GET    /api/docker/containers              · lista todos los containers
//   POST   /api/docker/container               · crea un container nuevo
//   POST   /api/docker/container/<id>/<action> · start | stop | restart | pause
//   DELETE /api/docker/container/<id>          · elimina container
//   GET    /api/docker/container/<id>/mounts   · inspecciona volúmenes
//   POST   /api/docker/container/<id>/rebuild  · recrea container con compose
//
// Los stacks (multi-container con docker-compose.yml) viven en docker_stacks.go.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func dockerContainersList(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !isDockerInstalledGo() {
		jsonOk(w, map[string]interface{}{"installed": false, "containers": []interface{}{}})
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}
	jsonOk(w, map[string]interface{}{"installed": true, "containers": getRealContainersGo()})
}

// dockerInstalledApps · GET /api/docker/installed-apps
//
// APP-010 · DEPRECATED desde Beta 8.1.x.
//
// Este endpoint queda mantenido para compat con clientes pre-Beta-8 que
// consumían el formato legacy {apps: [{id, name, port, status, isStack,
// external, category}]}. El frontend nuevo debe leer /api/services y
// filtrar por type="docker-app" o consumir /api/services?app=docker.
//
// Headers de deprecación según RFC 8594:
//   Deprecation: true
//   Sunset: ... (fecha estimada de retirada, una vez completado el port frontend)
//   Link: </api/services>; rel="successor-version"
//
// APP-017 · refactorizado para usar matchContainerForAppID (single source
func dockerContainerCreate(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}
	if !isDockerInstalledGo() {
		jsonError(w, 400, "Docker not installed")
		return
	}

	body, _ := readBody(r)
	rawId := bodyStr(body, "id")
	rawName := bodyStr(body, "name")
	rawImage := bodyStr(body, "image")
	id := sanitizeDockerNameGo(rawId)
	name := sanitizeDockerNameGo(rawName)
	image := sanitizeDockerNameGo(rawImage)
	if id == "" || name == "" || image == "" {
		jsonError(w, 400, "Missing container info")
		return
	}
	// Reject if sanitization changed the input — means malicious chars were present
	if id != rawId || name != rawName || image != rawImage {
		jsonError(w, 400, "Container name, id, or image contains invalid characters")
		return
	}

	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		dp, err := getDockerPath()
		if err != nil {
			jsonError(w, 400, "Docker not configured — install Docker from App Store first")
			return
		}
		dockerPath = dp
	}

	// Build docker args as a slice — no shell interpolation
	dockerArgs := []string{"run", "-d", "--name", id, "--restart", "unless-stopped"}

	// Ports — validate strictly
	if ports, ok := body["ports"].(map[string]interface{}); ok {
		portRegex := regexp.MustCompile(`^\d{1,5}$`)
		for host, container := range ports {
			containerStr := fmt.Sprintf("%v", container)
			if !portRegex.MatchString(host) || !portRegex.MatchString(containerStr) {
				jsonError(w, 400, "Invalid port mapping (must be numeric)")
				return
			}
			dockerArgs = append(dockerArgs, "-p", host+":"+containerStr)
		}
	}

	// Config volume · Refactor permisos Fase 3 · sin grupo compartido.
	// La app es dueña de su volumen (gestionable solo por la UI de NimOS, que
	// corre como root). Asignamos el UID único de la app si lo tiene.
	containerDataPath := filepath.Join(dockerPath, "containers", id)
	os.MkdirAll(containerDataPath, 0750)
	if au, err := getAppUID(db, id); err == nil && au != nil {
		runSafe("chown", "-R", fmt.Sprintf("%d:%d", au.UID, au.GID), containerDataPath)
	}
	runSafe("chmod", "0750", containerDataPath)
	dockerArgs = append(dockerArgs, "-v", containerDataPath+":/config")

	// Shared folder mounts
	sharesForMount, _ := dbSharesListRaw()
	var mountedShares []string
	for _, s := range sharesForMount {
		for _, ap := range s.AppPermissions {
			if ap.AppId == id {
				if s.Path != "" {
					dockerArgs = append(dockerArgs, "-v", s.Path+":/media/"+s.Name+":ro")
					mountedShares = append(mountedShares, s.Name)
				}
				break
			}
		}
	}

	// Env vars — SECURITY: passed as separate args, no shell escaping needed
	if env, ok := body["env"].(map[string]interface{}); ok {
		for key, val := range env {
			valStr := fmt.Sprintf("%v", val)
			if matched, _ := regexp.MatchString(`^[a-zA-Z_][a-zA-Z0-9_]*$`, key); matched {
				dockerArgs = append(dockerArgs, "-e", key+"="+valStr)
			}
		}
	}

	// Fase 2 (Beta 8.2) · labels com.nimos.* para identificación robusta.
	// Permite que el reconciler Docker (Fase 3) detecte containers gestionados
	// sin depender del nombre.
	containerLabels := NewNimOSLabels(id, bodyStr(body, "appVersion"), session.Username, false)
	dockerArgs = append(dockerArgs, containerLabels.ToDockerLabelArgs()...)

	dockerArgs = append(dockerArgs, image)

	out, ok := runSafe("docker", dockerArgs...)
	if !ok {
		jsonError(w, 500, "Failed to create container")
		return
	}

	// Register app · CreateOrUpdate es UPSERT idempotente, no necesita pre-filtrar
	//
	// APP-033 · construir array completo de PortBinding desde body.ports.
	// El primer port se duplica en Port (legacy) para clientes viejos que
	// leen ese campo. El array completo va en PortsJSON · canonical multi-port.
	var portBindings []PortBinding
	appPort := 0
	if ports, ok := body["ports"].(map[string]interface{}); ok {
		for host, container := range ports {
			hostInt := parseIntDefault(host, 0)
			containerInt := parseIntDefault(fmt.Sprintf("%v", container), 0)
			if hostInt == 0 || containerInt == 0 {
				continue
			}
			portBindings = append(portBindings, PortBinding{
				Host:     hostInt,
				Declared: containerInt,
				Protocol: "tcp", // body no trae proto · asumimos tcp (99% apps web)
			})
			if appPort == 0 {
				appPort = hostInt
			}
		}
	}
	portsJSON := "[]"
	if len(portBindings) > 0 {
		if data, jerr := json.Marshal(portBindings); jerr == nil {
			portsJSON = string(data)
		}
	}

	app := &DBDockerApp{
		ID:          id,
		Name:        name,
		Icon:        bodyStr(body, "icon"),
		Image:       image,
		Color:       bodyStr(body, "color"),
		Type:        "container",
		Port:        appPort,
		PortsJSON:   portsJSON,
		Config:      buildAppConfigJSON(bodyStr(body, "landingPath")),
		InstalledBy: session.Username,
	}
	// BUG-FIX (26/05/2026) · misma razón que dockerStackDeploy: usar
	// context.Background() porque pulls largos pueden cancelar r.Context().
	if err := appsRepo.CreateOrUpdateDockerApp(context.Background(), app); err != nil {
		logMsg("docker: install register failed for %s: %v", id, err)
	}

	// Sprint Updates · poblar docker_app_images (1 row · single container).
	go populateAppImagesForContainer(context.Background(), id, image)

	// APP-034 · invalidación inmediata de cache de NimHealth (sync, ~150ms en Pi).
	// context.Background() · misma razón que arriba.
	ForceDockerCacheRefresh(context.Background())

	jsonOk(w, map[string]interface{}{
		"ok": true, "containerId": strings.TrimSpace(out),
		"container":     map[string]interface{}{"id": id, "name": name, "image": image, "status": "running"},
		"mountedShares": mountedShares,
	})
}

func dockerContainerAction(w http.ResponseWriter, r *http.Request, id, action string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}
	safeId := sanitizeDockerNameGo(id)
	if safeId == "" {
		jsonError(w, 400, "Invalid container ID")
		return
	}
	if _, ok := runSafe("docker", action, safeId); !ok {
		jsonError(w, 500, fmt.Sprintf("Failed to %s container", action))
		return
	}
	// APP-034 · refresh async para no añadir latencia a la respuesta del action.
	// El user clicó start/stop/restart, la operación ya completó · la cache se
	// pondrá al día antes de la próxima vista de NimHealth.
	go ForceDockerCacheRefresh(context.Background())

	jsonOk(w, map[string]interface{}{"ok": true, "action": action, "containerId": safeId})
}

// dockerContainerDelete · DELETE /api/docker/container/<id>[?wipe=true]
//
// Para apps mono-container, el efecto práctico de wipe es menor que en stacks
// (los containers individuales no suelen tener volúmenes nombrados asociados),
// pero mantenemos el flag por consistencia API · permite que el frontend use
// el mismo dialog para mono-container y stacks.
func dockerContainerDelete(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}
	safeId := sanitizeDockerNameGo(id)
	if safeId == "" {
		jsonError(w, 400, "Invalid container ID")
		return
	}

	wipe := r.URL.Query().Get("wipe") == "true"

	// APP-031 · race-free uninstall:
	//   1. MarkDockerAppDeleting · síncrono. Observer ya no la lista activa,
	//      container no se cuenta como orphan durante el cleanup.
	//   2. Goroutine: docker stop/rm + DeleteDockerApp final.
	//
	// Sustituye al flujo legacy (DELETE row síncrono + container stop async)
	// que generaba flicker en orphanCount durante la ventana stop/rm.
	// commitContext() · debe persistir aunque cliente se desconecte.
	if err := appsRepo.MarkDockerAppDeleting(commitContext(), safeId); err != nil {
		logMsg("docker: uninstall mark-deleting failed for %s: %v", safeId, err)
		// Continuamos: el cleanup de Docker es lo importante. El observer puede
		// generar un orphan transitorio pero no es peor que el flujo legacy.
	}

	// Capturar id para la goroutine (Context del request muere al return).
	idCapture := safeId
	wipeCapture := wipe
	go func() {
		// En modo wipe · capturar la imagen ANTES de borrar el container.
		var containerImage string
		if wipeCapture {
			containerImage = getContainerImage(context.Background(), idCapture)
		}

		runSafe("docker", "stop", idCapture)
		// En modo wipe usamos -v (borra volúmenes anónimos asociados).
		// En modo soft, los volúmenes anónimos también se borran porque
		// `docker rm` sin -v solo preserva volúmenes con nombre (named volumes)
		// que en mono-container no se crean por defecto · efecto similar.
		if wipeCapture {
			if _, ok := runSafe("docker", "rm", "-v", idCapture); !ok {
				runSafe("docker", "rm", "-f", "-v", idCapture)
			}
		} else {
			if _, ok := runSafe("docker", "rm", idCapture); !ok {
				runSafe("docker", "rm", "-f", idCapture)
			}
		}
		if wipeCapture {
			logMsg("docker: container %s uninstalled in WIPE mode", idCapture)
			// Borrar la imagen del container (capturada antes del rm).
			// docker rmi SIN -f · seguro entre apps: si otra app usa la misma
			// imagen, Docker la protege y no se borra.
			if containerImage != "" {
				n := removeAppImages(context.Background(), []string{containerImage})
				logMsg("docker: container %s · %d imagen(es) borrada(s) (wipe)", idCapture, n)
			}
		} else {
			logMsg("docker: container %s uninstalled in SOFT mode", idCapture)
		}
		// DELETE final libera la row. Usamos Background ctx porque el request
		// ya terminó y la operación debe completarse independientemente.
		if err := appsRepo.DeleteDockerApp(context.Background(), idCapture); err != nil {
			logMsg("docker: uninstall final DB delete failed for %s: %v", idCapture, err)
		}
		// Refactor permisos Fase 4 · marcar el UID de la app como liberado.
		// NO libera el número (no se reusa · ver app_uids.go). Solo registra
		// que la app se desinstaló, para que el reconciler de higiene sepa
		// distinguir apps activas de desinstaladas. La limpieza del usuario de
		// sistema (userdel) la hace el reconciler, solo si NO quedan datos.
		if err := releaseAppUID(db, idCapture, time.Now().UTC().Format(time.RFC3339)); err != nil {
			logMsg("docker: uninstall releaseAppUID failed for %s: %v", idCapture, err)
		}
		// Sprint Updates · limpiar también las imágenes tracked.
		if appImagesRepo != nil {
			if err := appImagesRepo.DeleteByApp(context.Background(), idCapture); err != nil {
				logMsg("docker: app images cleanup failed for %s: %v", idCapture, err)
			}
		}
		// APP-034 · refresh cache tras cleanup completo.
		ForceDockerCacheRefresh(context.Background())
	}()

	jsonOk(w, map[string]interface{}{"ok": true, "containerId": safeId, "wipe": wipe})
}

func dockerContainerMounts(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission")
		return
	}
	safeId := sanitizeDockerNameGo(id)
	if safeId == "" {
		jsonError(w, 400, "Invalid container ID")
		return
	}
	out, ok := runSafe("docker", "inspect", safeId, "--format", `{{range .Mounts}}{{.Source}}|{{.Destination}}|{{.Mode}}{{println}}{{end}}`)
	if !ok {
		jsonError(w, 500, "Failed to get mounts")
		return
	}
	var mounts []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) >= 2 {
			mode := "rw"
			if len(parts) >= 3 {
				mode = parts[2]
			}
			mounts = append(mounts, map[string]interface{}{"source": parts[0], "destination": parts[1], "mode": mode})
		}
	}
	if mounts == nil {
		mounts = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"containerId": safeId, "mounts": mounts})
}

// dockerContainerRebuild · reconstruye una app instalada preservando su config.
//
// Política Beta 8.1 (post-APP-001):
//
//	type='stack'     → docker compose -f {stack}/docker-compose.yml up -d --force-recreate
//	                   El compose file ES la fuente de verdad: preserva volumes, env,
//	                   networks, ports, labels, restart policy — todo lo declarado.
//	type='container' → 501 Not Implemented. Rebuild de container suelto requiere
//	                   reconstruir flags desde `docker inspect` (ticket APP-001-B).
//
// CONTEXTO HISTÓRICO (Beta 7 y anteriores):
// La implementación previa hacía `docker stop && rm && run -d --name X image` lo cual
// PERDÍA volumes, env vars, port mappings y network attachments. Una app con datos
// (Jellyfin biblioteca, Immich DB, Vaultwarden vault) quedaba inutilizable tras un
// click en "Rebuild". Esta versión bloquea el path peligroso devolviendo 501 hasta
// que la implementación correcta esté lista.
func dockerContainerRebuild(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission")
		return
	}
	safeId := sanitizeDockerNameGo(id)
	if safeId == "" {
		jsonError(w, 400, "Invalid container ID")
		return
	}

	// Lookup app type en docker_apps. Sin registro no podemos garantizar rebuild seguro.
	app, err := appsRepo.GetDockerApp(r.Context(), safeId)
	if err != nil {
		jsonError(w, 500, fmt.Sprintf("Failed to lookup app: %v", err))
		return
	}
	if app == nil {
		jsonError(w, 404, "App not found in registry · rebuild requires a registered app")
		return
	}

	switch app.Type {
	case "stack":
		dockerPath, err := getDockerPath()
		if err != nil {
			jsonError(w, 400, "Docker path not configured")
			return
		}
		stackDir := filepath.Join(dockerPath, "stacks", safeId)
		composePath := filepath.Join(stackDir, "docker-compose.yml")
		if _, err := os.Stat(composePath); err != nil {
			jsonError(w, 404, fmt.Sprintf("Compose file not found at %s", composePath))
			return
		}
		// `docker compose up -d --force-recreate` reusa el compose existente y
		// recrea containers preservando TODO lo declarado (volumes, env, ports,
		// networks, labels). Único cambio: containers nuevos con IDs nuevos.
		cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d", "--force-recreate")
		cmd.Dir = stackDir
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			logMsg("docker: stack rebuild %s failed: %v · output: %s", safeId, runErr, string(out))
			jsonError(w, 500, fmt.Sprintf("Rebuild failed: %s", string(out)))
			return
		}
		logMsg("docker: stack rebuild %s ok", safeId)
		jsonOk(w, map[string]interface{}{
			"ok":          true,
			"containerId": safeId,
			"type":        "stack",
			"method":      "compose_force_recreate",
		})
		return

	case "container", "":
		// Rebuild de container suelto (no stack) está deshabilitado hasta tener
		// implementación correcta basada en `docker inspect` + reconstrucción de
		// flags. Devolver 501 explícito previene que la UI lo invoque silenciosamente.
		//
		// Workaround para el usuario: desinstalar y reinstalar la app desde AppStore.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{` +
			`"ok":false,` +
			`"error":"container_rebuild_not_implemented",` +
			`"message":"Rebuild for standalone containers is temporarily disabled. ` +
			`The previous implementation lost volumes, environment variables, port mappings and ` +
			`network attachments. To rebuild this app, uninstall and reinstall it.",` +
			`"ticket":"APP-001-B"` +
			`}`))
		logMsg("docker: rebuild %s rejected (type=container, APP-001-B pending)", safeId)
		return

	default:
		jsonError(w, 500, fmt.Sprintf("Unknown app type %q for rebuild", app.Type))
		return
	}
}
