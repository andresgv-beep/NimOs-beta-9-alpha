// docker_stacks.go — Stacks docker-compose (Beta 8.1)
//
// Stacks = aplicaciones multi-container con docker-compose.yml.
// Usado principalmente por el AppStore para apps del catálogo (Jellyfin,
// Immich, VS Code Server, etc).
//
// Endpoints:
//   POST   /api/docker/stack        · deploy nuevo stack (compose + .env)
//   DELETE /api/docker/stack/<id>   · elimina stack completo
//
// Variables canónicas inyectadas automáticamente en .env de cada stack:
//   CONFIG_PATH · {dockerPath}/containers/{stackId}
//   HOST_IP     · IP local del NAS (getStackHostIP en docker_async.go)
//   TZ          · timezone del host (getStackTimezone en docker_async.go)
//
// Tras escribir .env se expanden referencias ${VAR} recursivamente con
// expandStackEnvRefs (máximo 4 pasadas anti-loop). Esto permite al catálogo
// definir vars compuestas como PROJECTS_PATH=${CONFIG_PATH}/projects.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func dockerStackDeploy(w http.ResponseWriter, r *http.Request) {
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
	id := sanitizeDockerNameGo(bodyStr(body, "id"))
	compose := bodyStr(body, "compose")
	if id == "" || compose == "" {
		jsonError(w, 400, "Missing stack info")
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
	stackPath := filepath.Join(dockerPath, "stacks", id)
	os.MkdirAll(stackPath, 0755)

	// Create container config directory (used by CONFIG_PATH in compose)
	containerPath := filepath.Join(dockerPath, "containers", id)
	ensureContainerSubvolume(containerPath, 0775)
	// Set permissions so admin can read/write configs
	runSafe("chmod", "-R", "775", containerPath)

	// Write compose file
	composePath := filepath.Join(stackPath, "docker-compose.yml")

	// Fase 2 (Beta 8.2) · inyectar labels com.nimos.* en cada servicio del
	// compose ANTES de escribirlo a disco. Los labels de un container Docker
	// son inmutables tras `docker create`, por lo que la única vía robusta
	// es modificar el YAML antes del `compose up -d`.
	//
	// Permite identificación robusta de containers gestionados por NimOS
	// (Fase 3 reconciler) sin depender de matching por nombre.
	//
	// Si la inyección falla (compose mal formado, etc.) el deploy continúa
	// con el compose original · NO bloqueante para no romper installs por
	// problemas en el catálogo.
	stackLabels := NewNimOSLabels(id, bodyStr(body, "appVersion"), session.Username, true)
	composeWithLabels, lerr := injectNimOSLabelsIntoCompose(compose, stackLabels)
	if lerr != nil {
		logMsg("docker: stack %s · inyección de labels falló (%v) · usando compose original", id, lerr)
		composeWithLabels = compose
	} else {
		logMsg("docker: stack %s · labels com.nimos.* inyectados en compose", id)
	}

	// ─── Fase 4 · Port Allocator ──────────────────────────────────────────────
	// Asigna el puerto host del servicio PRINCIPAL evitando colisiones con los
	// reservados de NimOS (5000 daemon, 9091 NimTorrent, 2019/http/https Caddy) y
	// con otras apps instaladas. NO BLOQUEANTE: ante cualquier problema (sin puerto,
	// host ${VAR}, parse/alloc error) cae al comportamiento previo (puerto declarado,
	// compose sin reescribir) → nunca rompe un install que hoy funciona.
	// Ver PORT-ALLOCATOR-DESIGN. Alcance: solo el principal (flotante TCP); los
	// secundarios se registran pero no se reasignan (Fase 6).
	allocatedMainPort := 0
	if p, ok := body["port"].(float64); ok {
		allocatedMainPort = int(p)
	}
	stackPortsJSON := ""
	if allocatedMainPort > 0 {
		// Ocupación de las OTRAS apps (se excluye la actual para que su propio
		// puerto previo no se bloquee a sí mismo en el sticky). occupiedBy guarda
		// además QUÉ app retiene cada puerto, para nombrarla en el preflight de
		// conflictos. El set bool que consume el allocator se deriva de ahí.
		occupiedBy := map[int]string{}
		if apps, lerr := appsRepo.ListDockerApps(context.Background()); lerr == nil {
			others := make([]*DBDockerApp, 0, len(apps))
			for _, a := range apps {
				if a != nil && a.ID != id {
					others = append(others, a)
				}
			}
			occupiedBy = occupiedHostPortsBy(others)
		}
		occupied := make(map[int]bool, len(occupiedBy))
		for p := range occupiedBy {
			occupied[p] = true
		}
		// Puertos de Caddy (dinámicos) para los reservados duros.
		caddyHTTP, caddyHTTPS := 80, 443
		if networkRepo != nil {
			if cfg, cerr := networkRepo.GetExposureConfig(context.Background()); cerr == nil {
				if cfg.HTTPPort > 0 {
					caddyHTTP = cfg.HTTPPort
				}
				if cfg.HTTPSPort > 0 {
					caddyHTTPS = cfg.HTTPSPort
				}
			}
		}
		// Sticky por puerto: bindings de la instalación previa si la app ya existía.
		var prevBindings []PortBinding
		if prev, gerr := appsRepo.GetDockerApp(context.Background(), id); gerr == nil && prev != nil {
			prevBindings = prev.parsedPorts()
		}
		outYAML, mainHost, pj, rerr := resolveStackHostPorts(
			composeWithLabels, allocatedMainPort, prevBindings,
			occupied, reservedHard(caddyHTTP, caddyHTTPS), reservedSoft())
		if rerr == nil {
			// PREFLIGHT · puertos fijos por naturaleza (DNS :53, DHCP...) que el
			// allocator NO pudo reubicar y que otra app ya retiene. Es un conflicto
			// inevitable: se cancela AQUÍ, antes de escribir el compose o llamar a
			// docker → no se crea red, container ni registro, ni se sobrescribe un
			// compose previo. Cero basura que limpiar. Responde 409 estructurado
			// para que el frontend muestre un aviso claro en vez del error crudo.
			finalBindings := (&DBDockerApp{ID: id, PortsJSON: pj}).parsedPorts()
			if conflicts := detectFixedConflicts(finalBindings, occupiedBy); len(conflicts) > 0 {
				logMsg("docker: stack %s · deploy cancelado (preflight) · %s",
					id, portConflictMessage(conflicts))
				jsonResponse(w, http.StatusConflict, map[string]interface{}{
					"error": map[string]interface{}{
						"code":    "port_in_use",
						"message": portConflictMessage(conflicts),
						"details": map[string]interface{}{"conflicts": conflicts},
					},
				})
				return
			}
			if mainHost != allocatedMainPort {
				logMsg("docker: stack %s · puerto host %d ocupado/reservado → reasignado a %d",
					id, allocatedMainPort, mainHost)
			}
			composeWithLabels = outYAML
			allocatedMainPort = mainHost
			stackPortsJSON = pj
		} else {
			logMsg("docker: stack %s · port allocator omitido (%v) · se usa el puerto declarado", id, rerr)
		}
	}

	os.WriteFile(composePath, []byte(composeWithLabels), 0644)

	// APP-064 · Inyección automática de variables canónicas en .env del stack.
	//
	// Los composes del catálogo NimOS Appstore usan placeholders estándar
	// que el backend conoce pero el frontend no:
	//
	//   CONFIG_PATH · ruta del directorio de configuración del container
	//                 (montado como volumen para persistencia de configs).
	//                 = {dockerPath}/containers/{stackId}
	//
	//   HOST_IP     · IP local del NAS · usada por apps que generan URLs
	//                 absolutas (e.g. Jellyfin PublishedServerUrl).
	//
	//   TZ          · timezone del host (e.g. "Europe/Madrid"). Apps con
	//                 cron interno o logs timestamp lo necesitan. Si no
	//                 se puede determinar, queda "UTC".
	//
	// Estas vars se inyectan SIEMPRE antes de escribir .env. Si el body del
	// frontend manda también values en body.env, esos prevalecen (override).
	//
	// Tras el merge, expandimos referencias ${OTRA_VAR} dentro de los values
	// recursivamente · permite que el catálogo defina vars compuestas como
	// `PROJECTS_PATH = ${CONFIG_PATH}/projects` y que se resuelvan al path
	// completo antes de que docker-compose lo lea. Sin esta expansión, las
	// vars del .env no se interpolan entre sí (limitación de docker-compose).
	autoEnv := map[string]interface{}{
		"CONFIG_PATH": containerPath,
		"HOST_IP":     getStackHostIP(),
		"TZ":          getStackTimezone(),
	}
	// Merge body.env encima · permitir override desde el catálogo si hace falta
	if env, ok := body["env"].(map[string]interface{}); ok {
		for k, v := range env {
			autoEnv[k] = v
		}
	}

	// Refactor permisos · runtimeIdentity (ver PERMISOS-DESIGN addendum).
	//
	// Si el catálogo declara las env vars de identidad (uidEnv/gidEnv), inyectamos
	// el UID/GID ÚNICO asignado a la app para que el proceso corra como el DUEÑO
	// de su volumen (el que chowna applyAppPermissions más abajo). Sin esto, apps
	// que se bajan a un UID interno fijo (gitea→USER_UID, linuxserver→PUID,
	// synapse→UID) corren como p.ej. 1000, pero su /data es del UID asignado
	// (100xxx, modo 0750) → no pueden ni ATRAVESAR su propia carpeta → crash
	// (confirmado en hierro con Gitea: "stat .../app.ini: permission denied").
	//
	// Se hace DESPUÉS del merge de body.env → el catálogo NO decide el UID
	// (principio nº3 de PERMISOS-DESIGN), lo decide NimOS y nadie lo pisa.
	// assignAppUID es idempotente → devuelve el MISMO UID que usará
	// applyAppPermissions, así proceso y volumen coinciden.
	if rt := parseRuntimeIdentity(body); rt != nil {
		if au, err := assignAppUID(db, id, time.Now().UTC().Format(time.RFC3339)); err == nil {
			for k, v := range runtimeIdentityEnv(rt, au.UID, au.GID) {
				autoEnv[k] = v
			}
			logMsg("docker: stack %s · runtimeIdentity → %s=%d (UID asignado, coincide con el volumen)", id, rt.UIDEnv, au.UID)
		} else {
			logMsg("docker: stack %s · runtimeIdentity: no se pudo asignar UID: %v (se omite · la app podría no arrancar)", id, err)
		}
	}
	// Expandir referencias ${KEY} dentro de values · max 4 pasadas para evitar
	// loops infinitos en caso de referencia circular (raro, pero defensivo).
	autoEnv = expandStackEnvRefs(autoEnv, 4)

	// APP-066 · Resolver placeholders {RANDOM} con persistencia idempotente.
	//
	// Algunos catálogos declaran credenciales internas del stack como literal
	// "{RANDOM}" (ejemplo: Immich · DB_PASSWORD entre immich-server y su
	// Postgres interno). El user nunca ve esos valores · son comunicación
	// máquina-a-máquina dentro de la red Docker del stack.
	//
	// Sin resolución, el valor llega literal a docker-compose y Postgres se
	// inicializa con la cadena "{RANDOM}" como password · funcional pero
	// inseguro (todos los Immich del mundo tendrían misma pass).
	//
	// La función es IDEMPOTENTE por construcción · lee el .env previo si
	// existe y reusa valores ya generados. Esto significa:
	//   · Primera instalación · genera 24 chars aleatorios
	//   · Reinstalación con .env previo · mantiene valor previo (no rompe
	//     Postgres data dir que tiene el hash del valor anterior)
	//
	// El user nunca tiene que tocar esto · totalmente transparente.
	envFilePath := filepath.Join(stackPath, ".env")
	autoEnv = resolveRandomPlaceholders(autoEnv, envFilePath)

	// APP-067 · Default seguro para variables ${VAR} no resueltas.
	//
	// Bug navidrome (descubierto 28/05, bloqueante 31/05): el compose usa
	// ${MUSIC_PATH}:/music:ro pero MUSIC_PATH no es canónica (CONFIG_PATH,
	// HOST_IP, TZ) ni viene en body.env. Queda sin definir → docker-compose
	// la expande a "" → spec ":/music:ro" → "empty section between colons"
	// → deploy FALLA con 500.
	//
	// Sin este fix, CUALQUIER app cuyo compose use una variable de path que
	// NimOS no conozca (MUSIC_PATH, PHOTOS_PATH, MEDIA_PATH, ...) rompería el
	// deploy con un error críptico.
	//
	// Solución: escanear el compose por ${VAR} (y ${VAR:-default}), y para
	// cada una que NO esté ya en autoEnv NI tenga default en el propio
	// compose, asignar un default seguro bajo CONFIG_PATH:
	//   MUSIC_PATH → {containerPath}/music
	//
	// La app SIEMPRE arranca con una carpeta vacía bajo su config. El usuario
	// puede luego apuntar la variable a su biblioteca real (editar .env y
	// recrear, o reinstalar con el valor en body.env · que tiene prioridad).
	//
	// NO toca variables que YA tienen default en el compose (${VAR:-/x}) ·
	// docker-compose las resuelve solo. NO toca las canónicas ni las de
	// body.env (ya están en autoEnv).
	autoEnv = fillUnresolvedPathVars(compose, autoEnv, containerPath)

	if err := writeEnvFile(envFilePath, autoEnv); err != nil {
		logMsg("docker: no se pudo escribir %s: %v", envFilePath, err)
	}

	// Sembrado de ficheros de config (seedFiles) · para apps cuya imagen no
	// autogenera la config desde el env y la necesita PRESENTE al arrancar, o
	// cuyo ajuste no tiene env (qBittorrent: contraseña del WebUI como hash
	// PBKDF2 en qBittorrent.conf). Se escribe ANTES de applyAppPermissions (que
	// chowns el volumen al UID de la app) y del `up`. Los placeholders se
	// sustituyen con autoEnv (incluye los valores de los configFields del modal).
	if seeds := parseSeedFiles(body["seedFiles"]); len(seeds) > 0 {
		seedVals := make(map[string]string, len(autoEnv))
		for k, v := range autoEnv {
			// Los valores de usuario llegan escapados ($→$$) para el .env de
			// docker-compose. Para el sembrado (p.ej. el hash de la contraseña)
			// se necesita el valor REAL, así que des-escapamos $$→$.
			seedVals[k] = strings.ReplaceAll(fmt.Sprintf("%v", v), "$$", "$")
		}
		writeSeedFiles(containerPath, seeds, seedVals)
	}

	// APP-068 · Pull de imágenes ANTES del up · necesario para poder
	// inspeccionar el UID de cada imagen y aplicar permisos correctos a los
	// volúmenes de apps con UID propio (Grafana=472, Postgres=999, ...).
	pullCmd := exec.Command("docker", "compose", "-f", composePath, "pull")
	pullCmd.Dir = stackPath
	if out, err := pullCmd.CombinedOutput(); err != nil {
		// El pull puede fallar por imágenes que no soportan pull (build local)
		// o problemas de red · no abortamos, el up reintentará el pull.
		logMsg("docker: compose pull para %s devolvió: %s (continuando)", id, string(out))
	}

	// APP-068 + Refactor permisos Fase 2 · Aplicar permisos por volumen ANTES
	// del up, usando el UID ÚNICO asignado a la app (app_uids.go):
	//   - App flexible → chown al UID asignado, chmod 0750, confinada
	//   - App UID fijo (postgres/synapse) → su UID de imagen, confinada por vol
	//   - BD → 0700 exclusivo
	// Todos los volúmenes tratados se devuelven para EXCLUIRLOS del modelo de
	// shares posterior (no pisarlos). Las apps escriben al arrancar.
	dbVols := applyAppPermissions(id, compose, autoEnv, time.Now().UTC().Format(time.RFC3339))

	// Deploy
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	cmd.Dir = stackPath
	if out, err := cmd.CombinedOutput(); err != nil {
		jsonError(w, 500, fmt.Sprintf("Failed to deploy stack: %s", string(out)))
		return
	}

	// Refactor permisos Fase 3 · Finalizar permisos del container SIN grupo
	// compartido. Las apps son cajas confinadas, gestionadas solo por la UI de
	// NimOS (FileManager = root, navega/edita todo). NO se exponen por SMB.
	// Los volúmenes ya tratados por la Fase 2 (incl. BD 0700) se respetan; la
	// raíz y carpetas nuevas creadas por Docker en el up reciben UID+0750.
	finalizeAppContainerPerms(id, containerPath, dbVols)
	runSafe("chmod", "-R", "775", stackPath)
	// El chmod -R 775 anterior PISA los permisos del .env (lo deja 775, legible
	// por todos). El .env puede contener secretos (passwords del modal), así que
	// hay que RE-protegerlo a 0600 DESPUÉS del chmod recursivo. Sin esto, el
	// 0600 que puso writeEnvFile quedaría anulado. (El compose y demás ficheros
	// del stack sí quedan 775 · solo el .env necesita protección.)
	reprotectEnvFile(envFilePath)

	// Register stack
	// Fase 4 · el puerto host efectivo ya lo decidió el Port Allocator más arriba
	// (== body["port"] si no hubo reasignación; reasignado si chocaba).
	stackPort := allocatedMainPort
	// openMode: el catálogo puede declarar "internal" | "external" | "game".
	// Prioridad: el campo openMode explícito del body (catálogo) manda. Si no
	// viene, caemos al flag legacy "external" (bool) por compatibilidad.
	openMode := bodyStr(body, "openMode")
	if openMode == "" {
		openMode = "internal"
		if ext, ok := body["external"].(bool); ok && ext {
			openMode = "external"
		}
	}
	if openMode != "internal" && openMode != "external" && openMode != "game" {
		openMode = "internal" // valor desconocido → seguro por defecto
	}
	app := &DBDockerApp{
		ID:          id,
		Name:        bodyStr(body, "name"),
		Icon:        bodyStr(body, "icon"),
		Image:       "stack",
		Color:       bodyStr(body, "color"),
		Type:        "stack",
		OpenMode:    openMode,
		Port:        stackPort,
		PortsJSON:   stackPortsJSON,
		Config:      buildAppConfigJSONWithGame(bodyStr(body, "landingPath"), bodyStr(body, "game")),
		InstalledBy: session.Username,
	}
	// BUG-FIX (Nextcloud install · 26/05/2026): usar context.Background() en lugar
	// de r.Context() porque el frontend puede dar timeout durante descargas pesadas
	// (Nextcloud ~1GB, Immich varios GB · 5-15min). Si el HTTP context se cancela,
	// `compose up` (subprocess externo) continúa hasta terminar PERO el INSERT a
	// docker_apps usaría un contexto cancelado y abortaría silenciosamente · resultado:
	// container vivo pero NimOS sin registro = invisible en AppStore/NimShield.
	//
	// Síntoma del bug original: Nextcloud apareció en docker ps + en docker_app_images
	// (poblada por goroutine con context.Background()) pero NO en docker_apps.
	if err := appsRepo.CreateOrUpdateDockerApp(context.Background(), app); err != nil {
		logMsg("docker: stack install register failed for %s: %v", id, err)
	}

	// Capa 2 · postInstall (async). Si la app declara acciones postInstall
	// (ej. Matrix · crear el primer admin), se ejecutan en goroutine tras el
	// arranque · esperan a que el container esté healthy y corren el comando
	// (register_new_matrix_user, etc.). No bloquea la respuesta del install
	// porque Synapse tarda en estar listo la 1ª vez. Ver docker_postinstall.go.
	maybeRunPostInstall(id, body)

	// Sprint Updates · poblar docker_app_images con los servicios del stack.
	// No bloqueante: si falla, se logea pero el deploy se considera OK.
	// Update-check posterior puede refrescar lo que falte.
	go populateAppImagesAfterDeploy(context.Background(), id, composePath, stackPath)

	// APP-034 · invalidación inmediata de cache de NimHealth (sync, ~150ms en Pi).
	// Sin esto, la app no aparece en /api/services hasta el siguiente tick (≤30s).
	// También usamos context.Background() · misma razón.
	ForceDockerCacheRefresh(context.Background())

	jsonOk(w, map[string]interface{}{"ok": true, "stack": id, "path": stackPath})
}

// dockerStackDelete · DELETE /api/docker/stack/<id>[?wipe=true]
//
// Dos modos de operación según query param `wipe`:
//
//	wipe=false (default · recomendado para el user) · "DESINSTALACIÓN SUAVE"
//	  · docker compose down --remove-orphans · containers fuera
//	  · NO se borran volúmenes Docker (-v) · datos en containers/{id} intactos
//	  · NO se borra stackPath ni containers/{id} · compose YAML, .env y datos
//	    de la app se conservan
//	  · Resultado: si reinstalas la app más tarde, todo vuelve donde estaba.
//	    Postgres encuentra su data dir, Immich su BD, Jellyfin su biblioteca.
//
//	wipe=true · "DESINSTALACIÓN COMPLETA · DESTRUCTIVA"
//	  · docker compose down -v --remove-orphans · containers + volúmenes Docker
//	  · Borra stackPath (docker-compose.yml + .env)
//	  · Borra containers/{id} (uploads, postgres data, configs de la app)
//	  · NO se puede deshacer.
//
// En ambos casos: la row en docker_apps se elimina · la app deja de aparecer
// como instalada en el AppStore.
func dockerStackDelete(w http.ResponseWriter, r *http.Request, id string) {
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
		jsonError(w, 400, "Invalid stack ID")
		return
	}

	// Detectar modo · default es suave (preservar datos)
	wipe := r.URL.Query().Get("wipe") == "true"

	// APP-031 · race-free uninstall (mismo flujo que dockerContainerDelete).
	// commitContext() · debe persistir aunque cliente se desconecte (el cleanup
	// en goroutine ya está lanzado, la BD debe quedar consistente).
	if err := appsRepo.MarkDockerAppDeleting(commitContext(), safeId); err != nil {
		logMsg("docker: stack uninstall mark-deleting failed for %s: %v", safeId, err)
	}

	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		if dp, err := getDockerPath(); err == nil {
			dockerPath = dp
		} else {
			// Sin path · borramos la row directamente, no hay nada de stack que limpiar.
			// commitContext() · borrado final debe persistir.
			if delErr := appsRepo.DeleteDockerApp(commitContext(), safeId); delErr != nil {
				logMsg("docker: stack uninstall row delete failed for %s: %v", safeId, delErr)
			}
			jsonOk(w, map[string]interface{}{"ok": true})
			return
		}
	}
	stackPath := filepath.Join(dockerPath, "stacks", safeId)
	composePath := filepath.Join(stackPath, "docker-compose.yml")

	// Cleanup en background + DELETE final tras compose down.
	idCapture := safeId
	wipeCapture := wipe
	dockerPathCapture := dockerPath
	go func() {
		// En modo wipe · capturar las imágenes del stack ANTES del down
		// (necesita los containers vivos para listarlas). Se borran después.
		// Usa el label com.nimos.app_id · inmune a variables sin definir en
		// el compose (bug navidrome/MUSIC_PATH).
		var stackImages []string
		if wipeCapture {
			stackImages = getStackImages(context.Background(), idCapture)
		}

		if _, err := os.Stat(composePath); err == nil {
			// Argumentos de compose down · siempre --remove-orphans, solo añade -v
			// en modo wipe (destruir volúmenes Docker)
			downArgs := []string{"compose", "-f", composePath, "down", "--remove-orphans"}
			if wipeCapture {
				downArgs = append(downArgs[:len(downArgs)-1], "-v", "--remove-orphans")
			}
			cmd := exec.Command("docker", downArgs...)
			cmd.Dir = stackPath
			cmd.Run()
		}

		// En modo wipe · borrar stack path (compose YAML + .env) y datos
		if wipeCapture {
			os.RemoveAll(stackPath)
			// El dir de datos puede ser un subvolumen BTRFS (apps nuevas) o un
			// dir plano (legacy) · removeContainerPath trata cada caso bien.
			removeContainerPath(filepath.Join(dockerPathCapture, "containers", idCapture))
			logMsg("docker: stack %s uninstalled in WIPE mode · all data removed", idCapture)

			// Borrar las imágenes del stack (capturadas antes del down).
			// docker rmi SIN -f · seguro entre apps: si otra app usa una imagen
			// compartida, Docker la protege y no se borra.
			if len(stackImages) > 0 {
				n := removeAppImages(context.Background(), stackImages)
				logMsg("docker: stack %s · %d/%d imágenes borradas (wipe)", idCapture, n, len(stackImages))
			}
		} else {
			logMsg("docker: stack %s uninstalled in SOFT mode · data preserved at %s/containers/%s", idCapture, dockerPathCapture, idCapture)
		}

		// DELETE final libera la row de BD (en ambos modos)
		if err := appsRepo.DeleteDockerApp(context.Background(), idCapture); err != nil {
			logMsg("docker: stack uninstall final DB delete failed for %s: %v", idCapture, err)
		}
		// Sprint Updates · limpiar también las imágenes tracked.
		// Tanto modo soft como wipe: la app deja de aparecer instalada · sus
		// imágenes no necesitan tracking. Si reinstala, populateAppImagesAfterDeploy
		// las recreará con los digests actuales.
		if appImagesRepo != nil {
			if err := appImagesRepo.DeleteByApp(context.Background(), idCapture); err != nil {
				logMsg("docker: app images cleanup failed for %s: %v", idCapture, err)
			}
		}
		// APP-034 · refresh cache tras cleanup completo.
		ForceDockerCacheRefresh(context.Background())
	}()

	jsonOk(w, map[string]interface{}{"ok": true, "wipe": wipe})
}

// dockerPull · GET /api/docker/pull/{image}
//
// Wrapper sync/async sobre runDockerPullWork.

// composeVarPattern captura referencias a variables en un compose:
//
//	${VAR}        → grupo 1 = "VAR", grupo 2 = ""        (sin default)
//	${VAR:-foo}   → grupo 1 = "VAR", grupo 2 = ":-foo"   (con default · NO tocar)
//	${VAR-foo}    → grupo 1 = "VAR", grupo 2 = "-foo"    (con default · NO tocar)
//	$VAR          → grupo 1 = "VAR", grupo 2 = ""        (forma sin llaves)
var composeVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:?-[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// volumeHostVarPattern captura SOLO variables usadas en el lado-host de un
// bind mount de volumen. El patrón busca ${VAR} (o $VAR) seguido de ":/"
// (separador host:container con ruta absoluta en el container).
//
// Ejemplos que captura:
//   - ${MUSIC_PATH}:/music         → MUSIC_PATH
//   - ${CONFIG_PATH}/data:/var/lib → (CONFIG_PATH ya definida, se ignora luego)
//   - ${UPLOAD_LOCATION}:/usr/src  → UPLOAD_LOCATION
//
// Lo que NO captura (y antes rompía Immich):
//   - POSTGRES_USER: ${DB_USERNAME}   (environment · no es :/)
//   - command: '...$$user...'         (command · no es :/)
//   - ${DB_PASSWORD}                   (environment)
//
// El sufijo (:?/...) exige que tras la variable venga ":" + "/" (inicio de
// la ruta del container) o "/...:/ " (subruta + separador). Esto restringe
// las coincidencias al contexto de volumen.
var volumeHostVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:?-[^}]*)?\}(?:/[^:\s]*)?:/`)

// fillUnresolvedPathVars escanea SOLO los volúmenes del compose buscando
// variables ${VAR} en el lado-host que NO estén ya definidas en autoEnv y
// que NO tengan default inline. Para cada una, asigna un default seguro
// bajo containerPath.
//
// IMPORTANTE (fix 01/06): antes esta función escaneaba TODO el compose con un
// patrón amplio que capturaba ${VAR} en cualquier contexto · incluido
// environment: (POSTGRES_USER, DB_NAME) y command: ($$user de postgres). Eso
// convertía esas variables en rutas absurdas y rompía Immich (postgres no
// arrancaba con usuario "/nimos/pools/.../postgres_user"). Ahora SOLO mira
// variables en el lado-host de volúmenes (${VAR}:/...), que son las únicas
// que de verdad necesitan un path por defecto.
//
// Reglas:
//   - Solo variables en contexto de volumen (${VAR}:/container/path)
//   - Si la var YA está en autoEnv → no se toca
//   - Si tiene default inline ${VAR:-x} → no se toca (compose lo resuelve)
//
// Devuelve autoEnv modificado (mismo mapa).
func fillUnresolvedPathVars(compose string, autoEnv map[string]interface{}, containerPath string) map[string]interface{} {
	matches := volumeHostVarPattern.FindAllStringSubmatch(compose, -1)
	for _, m := range matches {
		varName := m[1]
		hasInlineDefault := m[2] != ""
		if varName == "" {
			continue
		}
		// Ya definida → no tocar
		if _, exists := autoEnv[varName]; exists {
			continue
		}
		// Tiene default inline en el compose → docker-compose lo resuelve solo
		if hasInlineDefault {
			continue
		}
		// Variable de path sin resolver · asignar default seguro bajo containerPath
		dirName := defaultDirNameForVar(varName)
		defaultPath := filepath.Join(containerPath, dirName)
		os.MkdirAll(defaultPath, 0775)
		autoEnv[varName] = defaultPath
		logMsg("docker: stack · variable de volumen %q sin definir, default → %s "+
			"(el usuario puede cambiarla luego)", varName, defaultPath)
	}
	return autoEnv
}

// defaultDirNameForVar deriva un nombre de directorio limpio del nombre de una
// variable: minúsculas y sin sufijos _PATH/_DIR/_LOCATION.
//
//	MUSIC_PATH → "music"  ·  PHOTOS_DIR → "photos"  ·  MEDIA → "media"
func defaultDirNameForVar(varName string) string {
	n := strings.ToLower(varName)
	for _, suffix := range []string{"_path", "_dir", "_location", "_folder"} {
		n = strings.TrimSuffix(n, suffix)
	}
	if n == "" {
		n = strings.ToLower(varName)
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────
// Permisos de volúmenes para apps · structs y helpers compartidos
// ─────────────────────────────────────────────────────────────────────────
//
// MODELO ACTUAL (Refactor permisos Fases 1-3 · ver app_uids.go y
// PERMISOS-DESIGN.md): cada app tiene un UID único asignado por NimOS y es
// dueña de su volumen (chmod 0750, SIN grupo compartido). Las apps son cajas
// confinadas, gestionables solo por la UI de NimOS (FileManager = root). La
// lógica de decisión y aplicación de permisos vive en app_uids.go
// (decideVolumePlan / applyAppPermissions / finalizeAppContainerPerms).
//
// Los structs y helpers de abajo (composeForPerms, dbContainerPaths,
// isDBContainerPath, imageUID, resolveVolumeHostPath, ...) los usa app_uids.go
// para parsear el compose y detectar volúmenes de BD.

// composeForPerms es una vista mínima del compose para extraer servicios,
// sus imágenes y sus volúmenes (bind mounts).
type composeForPerms struct {
	Services map[string]struct {
		Image   string   `yaml:"image"`
		Volumes []string `yaml:"volumes"`
	} `yaml:"services"`
}

// dbContainerPaths son rutas DENTRO del container donde las bases de datos
// guardan sus datos. Un volumen que monte a una de estas rutas es un volumen
// de BD y necesita permisos 0700 exclusivos (NO el modelo de shares de NimOS).
//
// PostgreSQL, MariaDB/MySQL, MongoDB y similares EXIGEN que su directorio de
// datos sea 0700, propiedad de su UID, SIN setgid/grupo/ACL. El modelo normal
// de NimOS (chmod 2775 + grupo + ACL para que el FileManager navegue) ROMPE
// estas bases de datos (postgres entra en PANIC). Por eso se detectan y se les
// da trato especial.
var dbContainerPaths = []string{
	"/var/lib/postgresql/data", // PostgreSQL
	"/var/lib/mysql",           // MySQL / MariaDB
	"/var/lib/mariadb",         // MariaDB (alternativa)
	"/data/db",                 // MongoDB
	"/bitnami/postgresql",      // imágenes bitnami postgres
	"/bitnami/mariadb",         // imágenes bitnami mariadb
}

// isDBContainerPath indica si una ruta-container corresponde al directorio de
// datos de una base de datos (que necesita 0700 exclusivo).
func isDBContainerPath(containerPath string) bool {
	cp := strings.TrimRight(strings.TrimSpace(containerPath), "/")
	for _, dbPath := range dbContainerPaths {
		if cp == dbPath {
			return true
		}
	}
	return false
}

// imageUID devuelve el UID NUMÉRICO que la imagen declara en Config.User.
// Vacío si corre como root o no se puede determinar.
//
// Config.User puede venir como número ("472", "472:472") o como NOMBRE
// ("grafana", "node", "postgres"). Si es un nombre, NO podemos usarlo
// directamente para chown (necesitamos el UID numérico). En ese caso
// resolvemos el UID real ejecutando `id -u <nombre>` dentro de la imagen.
//
// BUG que arregla (n8n): n8n declara Config.User="node" (UID 1000). Antes
// imageUID devolvía "node" (texto), decideVolumePlan hacía strconv.Atoi("node")
// que falla → trataba el volumen como "flexible" → le ponía el UID único de
// NimOS (100000+) → pero n8n corre como node(1000) → EACCES al escribir.
// Ahora imageUID devuelve "1000" y decideVolumePlan respeta el UID correcto.
func imageUID(image string) string {
	out, ok := runSafe("docker", "inspect", image, "--format", "{{.Config.User}}")
	if !ok {
		return ""
	}
	user := strings.TrimSpace(out)
	if user == "" || user == "root" {
		return ""
	}
	// Quedarnos con la parte antes de ":" (user, no group).
	if idx := strings.IndexByte(user, ':'); idx >= 0 {
		user = user[:idx]
	}
	if user == "" {
		return ""
	}
	// ¿Ya es numérico? → devolverlo tal cual.
	if _, err := strconv.Atoi(user); err == nil {
		return user
	}
	// Es un NOMBRE (ej. "node", "grafana") · resolver su UID real preguntando
	// a la propia imagen. `id -u <nombre>` devuelve el UID numérico.
	if uidOut, ok := runSafe("docker", "run", "--rm", "--entrypoint", "id", image, "-u", user); ok {
		uid := strings.TrimSpace(uidOut)
		if _, err := strconv.Atoi(uid); err == nil {
			return uid
		}
	}
	// No se pudo resolver · devolver el nombre (comportamiento anterior, mejor
	// que nada · decideVolumePlan lo tratará como flexible).
	return user
}

// volumeContainerPath extrae el lado-container de un volumen
// "host:container[:opts]". Devuelve "" si no se puede determinar.
//
//	"${DB_DATA}:/var/lib/postgresql/data"     → "/var/lib/postgresql/data"
//	"${CONFIG}/data:/var/lib/grafana"          → "/var/lib/grafana"
//	"${UPLOAD}:/usr/src/app/upload:rw"         → "/usr/src/app/upload"
func volumeContainerPath(vol string) string {
	parts := strings.Split(vol, ":")
	if len(parts) < 2 {
		return ""
	}
	// parts[0] = host · parts[1] = container · parts[2..] = opciones (ro, rw)
	return strings.TrimSpace(parts[1])
}

// resolveVolumeHostPath extrae el lado host de un volumen "host:container[:opts]"
// y expande las variables ${VAR} usando envVars. Devuelve "" si es un volumen
// con nombre (no bind mount) o no se puede resolver.
func resolveVolumeHostPath(vol string, envVars map[string]interface{}) string {
	parts := strings.SplitN(vol, ":", 2)
	if len(parts) < 2 {
		return "" // volumen con nombre (ej. "model-cache:/cache") · no bind mount
	}
	host := strings.TrimSpace(parts[0])
	// Expandir ${VAR} y $VAR con envVars
	host = expandComposeVars(host, envVars)
	// Si tras expandir sigue teniendo $ o está vacío, no es resoluble
	if host == "" || strings.Contains(host, "$") {
		return ""
	}
	// Solo rutas absolutas (bind mounts), no volúmenes con nombre
	if !strings.HasPrefix(host, "/") {
		return ""
	}
	return host
}

// expandComposeVars sustituye ${VAR} y $VAR en s usando envVars.
func expandComposeVars(s string, envVars map[string]interface{}) string {
	return composeVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		m := composeVarPattern.FindStringSubmatch(match)
		name := m[1]
		if name == "" {
			name = m[3]
		}
		if val, ok := envVars[name]; ok {
			return fmt.Sprintf("%v", val)
		}
		return match // no resuelta · dejar tal cual (se filtrará luego)
	})
}

// nimosPoolsRoot devuelve el prefijo de los pools para validar que un volumen
// está dentro del área gestionada por NimOS.
func nimosPoolsRoot() string {
	return "/nimos/pools/"
}
