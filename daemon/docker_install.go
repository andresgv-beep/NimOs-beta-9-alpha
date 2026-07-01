// docker_install.go — Install/uninstall workflow del módulo Docker (Beta 8.1)
//
// Endpoints:
//   GET  /api/docker/apps              · lista apps registradas (cross BD ↔ ps)
//   POST /api/docker/install           · instala Docker engine + share docker-apps
//     ?async=true devuelve operationId y trabaja en goroutine
//   POST /api/docker/uninstall         · elimina Docker engine completo
//   POST /api/docker/uninstall-config  · solo borra docker.json (rescue)
//
// Worker async:
//   runDockerInstallWork()  · 7 fases (checks, share, install, start, registry,
//                              cleanup, finalize) con reporting de progreso al
//                              operationsRepo. Documentación detallada en el
//                              cuerpo de la función.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

func dockerInstalledApps(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	// APP-010 · marcar deprecación en cada respuesta.
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/services>; rel="successor-version"`)

	if !isDockerInstalledGo() {
		jsonOk(w, map[string]interface{}{"apps": []interface{}{}})
		return
	}

	registeredApps, err := appsRepo.ListDockerApps(r.Context())
	if err != nil {
		logMsg("docker: ListDockerApps failed: %v", err)
		registeredApps = nil
	}

	// docker ps (running only · este endpoint legacy mantiene la semántica
	// histórica de "solo containers running con ports expuestos").
	out, _ := runSafe("docker", "ps", "--format", "{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}|{{.Labels}}")
	containers := map[string]dockerContainer{}
	if out != "" {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			parts := strings.SplitN(line, "|", 5)
			if len(parts) < 4 {
				continue
			}
			labels := ""
			if len(parts) >= 5 {
				labels = parts[4]
			}
			containers[parts[0]] = dockerContainer{
				Name:   parts[0],
				Image:  parts[1],
				Status: parts[2],
				Ports:  parts[3],
				AppID:  parseAppIDLabel(labels),
			}
		}
	}

	// Helper local · extrae el primer port host del raw "0.0.0.0:8096->8096/tcp"
	extractFirstHostPort := func(rawPorts string) interface{} {
		if rawPorts == "" {
			return nil
		}
		re := regexp.MustCompile(`0\.0\.0\.0:(\d+)`)
		if m := re.FindStringSubmatch(rawPorts); m != nil {
			return parseIntDefault(m[1], 0)
		}
		return nil
	}

	var apps []interface{}
	matchedContainers := map[string]bool{}

	for _, reg := range registeredApps {
		// APP-017 · matching delegado al helper compartido.
		containerName, container := matchContainerForAppID(reg.ID, containers)

		containerStatus := "unknown"
		if container != nil {
			matchedContainers[containerName] = true
			if strings.Contains(container.Status, "Up") {
				containerStatus = "running"
			} else {
				containerStatus = "stopped"
			}
		}

		entry := map[string]interface{}{
			"id": reg.ID, "name": reg.Name, "icon": reg.Icon,
			"color": reg.Color, "port": reg.Port, "image": reg.Image,
			"status": containerStatus, "category": "installed",
			"isStack":    reg.Type == "stack",
			"external":   reg.OpenMode == "external",
			"accessMode": reg.AccessMode,
		}
		// SHIELD-P2 · con el puerto directo cerrado, el launcher/iframe
		// necesita la URL Caddy para llegar a la app.
		if reg.AccessMode == "caddy_only" {
			if url := externalURLForApp(r.Context(), reg.ID); url != "" {
				entry["externalUrl"] = url
			}
		}
		apps = append(apps, entry)
	}

	// Containers running no registrados + con port expuesto · "orphans con UI".
	// APP-017 · usa isPossibleStackSubContainer para filtrar subcontainers.
	for name, c := range containers {
		if matchedContainers[name] {
			continue
		}
		port := extractFirstHostPort(c.Ports)
		if port == nil {
			continue
		}
		if isPossibleStackSubContainer(name, matchedContainers) {
			continue
		}

		dispName, icon, color := getAppMeta(c.Image, name)
		apps = append(apps, map[string]interface{}{
			"id": name, "name": dispName, "icon": icon, "color": color,
			"port": port, "image": c.Image, "status": "running", "category": "installed",
		})
	}

	if apps == nil {
		apps = []interface{}{}
	}
	jsonOk(w, map[string]interface{}{"apps": apps})
}

// dockerInstall · POST /api/docker/install
//
// Wrapper sync/async sobre runDockerInstallWork.
//
// Modo sync (default · sin query param):
//   - Bloquea hasta completar (~30s-5min según si Docker ya está instalado).
//   - Responde 200 OK con resultado, o jsonError con el código apropiado.
//
// Modo async (con ?async=true):
//   - Crea una operation en operationsRepo (type="docker.install").
//   - Lanza goroutine que ejecuta el trabajo y reporta progreso.
//   - Responde 202 Accepted con {operationId, pollUrl} para polling.
//   - El cliente debe GET /api/operations/{id} para ver estado.
func dockerInstall(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)

	if isAsyncRequested(r) {
		// Async path · APP-014
		op, err := operationsRepo.Create(r.Context(), "docker.install", session.Username)
		if err != nil {
			jsonError(w, 500, "Failed to create operation: "+err.Error())
			return
		}
		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return runDockerInstallWork(ctx, body, op.ID)
		})
		writeAsyncAccepted(w, op)
		return
	}

	// Sync path (legacy · compat 100%)
	result, err := runDockerInstallWork(r.Context(), body, "")
	if err != nil {
		writeWorkerError(w, err)
		return
	}
	jsonOk(w, result)
}

// dockerInstallMu protege contra instalaciones de Docker concurrentes.
//
// BUG FIX (12/06/2026): la UI puede lanzar varias peticiones de instalación
// seguidas (reintentos del frontend, doble click, polling agresivo). Sin este
// guard, cada petición lanzaba el script de Docker en paralelo · dos `apt-get`
// a la vez → lock de dpkg → exit 100 → instalación a medias (estado iU) →
// fallo. Visto en producción: 5 intentos en ~17s pisándose mutuamente.
//
// Con este mutex (try-lock), solo UNA instalación corre a la vez. Las demás
// peticiones reciben un error claro "ya en progreso" en vez de pisar el apt.
var dockerInstallMu sync.Mutex

// runDockerInstallWork · trabajo real de instalación de Docker engine en el pool.
//
// Función pura sin acceso a HTTP. Reusada por el wrapper sync y por el
// trabajo async (runWorkerAsync). Si opID != "" reporta progreso a
// operationsRepo en pasos clave.
//
// Returns:
//   - map: payload de éxito (mismo contrato que el endpoint sync previo)
//   - error: si es *httpStatusError, el wrapper sync usa Code; si no, 500
//
// Pasos (con % de progreso reportado en modo async):
//
//	 0% · validaciones (paso 0)
//	10% · ubicar pool destino (paso 1-2)
//	20% · crear directorios + daemon.json (pasos 3-4)
//	30% · instalar Docker engine (paso 5 · ~60% del tiempo en el peor caso)
//	80% · arrancar Docker + permisos (pasos 6-8)
//	90% · crear share docker-apps (paso 9)
//	95% · guardar config + registrar (pasos 10-11)
//
// El caller ya verificó admin · este worker no re-autoriza.
func runDockerInstallWork(ctx context.Context, body map[string]interface{}, opID string) (map[string]interface{}, error) {
	// Guard contra instalaciones concurrentes · si otra ya está corriendo,
	// no lanzamos un segundo apt-get (evita el lock de dpkg → exit 100).
	if !dockerInstallMu.TryLock() {
		return nil, asHTTPError(409, "Docker installation already in progress. Please wait.")
	}
	defer dockerInstallMu.Unlock()

	updateOpProgressSafe(ctx, opID, 0, "Checking environment")

	// ── 0. APP-063 · proteger data Docker pre-existente ──
	// Si NimOS no había instalado Docker antes pero /var/lib/docker tiene
	// data, probablemente es de un Docker instalado manualmente por el user.
	// El paso 6 más abajo hace `rm -rf /var/lib/docker` para limpiar al
	// reapuntar data-root al pool · sin este check, borraría data ajena.
	prevConf := getDockerConfigGo()
	prevInstalled, _ := prevConf["installed"].(bool)
	if !prevInstalled {
		hasData, checkErr := dockerVarLibHasData()
		if checkErr != nil {
			return nil, asHTTPError(500, "Failed to check /var/lib/docker: %v", checkErr)
		}
		if hasData {
			logMsg("docker: install aborted · /var/lib/docker has pre-existing data, NimOS hadn't installed Docker previously")
			return nil, asHTTPError(409,
				"/var/lib/docker contains existing data not managed by NimOS. "+
					"To prevent accidental data loss, NimOS won't overwrite it automatically. "+
					"Either move the data elsewhere or remove the directory manually, "+
					"then retry installation.")
		}
	}

	updateOpProgressSafe(ctx, opID, 10, "Locating storage pool")

	// ── 1. Find the target pool (Beta 8.1: usa service v2) ──
	if storageService == nil {
		return nil, asHTTPError(500, "Storage service not initialized")
	}
	pools, err := storageService.ListPools(ctx)
	if err != nil {
		return nil, asHTTPError(500, "listing pools: %v", err)
	}
	if len(pools) == 0 {
		return nil, asHTTPError(400, "No storage pools available. Create a pool in Storage Manager first.")
	}

	poolName := bodyStr(body, "pool")
	var targetPool *Pool
	for _, p := range pools {
		if poolName != "" && p.Name == poolName {
			targetPool = p
			break
		}
		if p.IsPrimary {
			targetPool = p
		}
	}
	if targetPool == nil {
		targetPool = pools[0]
	}

	mountPoint := targetPool.MountPoint
	if mountPoint == "" {
		return nil, asHTTPError(400, "Pool has no mount point configured")
	}

	// ── 2. Verify pool is REALLY mounted ──
	mountSrc, _ := runSafe("findmnt", "-n", "-o", "SOURCE", mountPoint)
	rootSrc, _ := runSafe("findmnt", "-n", "-o", "SOURCE", "/")
	if strings.TrimSpace(mountSrc) == "" || strings.TrimSpace(mountSrc) == strings.TrimSpace(rootSrc) {
		return nil, asHTTPError(400, "Storage pool is not mounted. Check Storage Manager.")
	}

	dockerPath := filepath.Join(mountPoint, "docker")
	dockerDataPath := filepath.Join(dockerPath, "data")
	// Beta 8.2 · containerd guarda las capas descomprimidas de las imágenes.
	// Por defecto vive en /var/lib/containerd (disco del sistema · en un Pi,
	// la microSD lenta). Si no se mueve al pool, las capas saturan la SD y
	// las instalaciones se vuelven lentísimas (descompresión en SD) y
	// congelan el sistema (toda la I/O compite por la SD). Lo movemos al
	// pool SSD igual que el data-root de Docker.
	containerdPath := filepath.Join(dockerPath, "containerd")

	updateOpProgressSafe(ctx, opID, 20, "Preparing directories")

	// ── 3. Create ALL directories on the pool FIRST ──
	for _, dir := range []string{"data", "containers", "stacks", "volumes", "containerd"} {
		if err := os.MkdirAll(filepath.Join(dockerPath, dir), 0755); err != nil {
			return nil, asHTTPError(500, "Failed to create directory: %v", err)
		}
	}
	log.Printf("Docker directories created at %s", dockerPath)

	// ── 4. Create daemon.json BEFORE Docker ever starts ──
	os.MkdirAll("/etc/docker", 0755)
	// data-root en el pool SSD + pool de direcciones de red amplio.
	//
	// default-address-pools: por defecto Docker solo puede crear ~31 redes
	// (172.17.0.0/12 en /16 + 192.168.0.0/16 en /20). Cada app NimOS es un
	// stack de compose = una red `<app>_default` = una subred. Con ~31 apps el
	// pool se agota → "all predefined address pools have been fully subnetted"
	// y no se pueden instalar más apps. Ampliamos a /24 sobre 172.17.0.0/12 =
	// 4096 redes posibles. Excluimos 192.168.0.0/16 a propósito: su /20 por
	// defecto (192.168.0.0/20) solapa con LANs domésticas típicas (p.ej. la
	// propia .131 del NAS) y con macvlan del Nivel-2 → colisión de enrutado.
	// Ver NETWORK-POOL-DESIGN.md.
	daemonConf := map[string]interface{}{
		"data-root":             dockerDataPath,
		"default-address-pools": dockerAddressPools(),
	}
	daemonData, _ := json.MarshalIndent(daemonConf, "", "  ")
	if err := os.WriteFile("/etc/docker/daemon.json", daemonData, 0644); err != nil {
		return nil, asHTTPError(500, "Failed to write daemon.json: %v", err)
	}
	log.Printf("Docker daemon.json → data-root=%s", dockerDataPath)

	// ── 4b. Configure containerd root on the pool ──
	// Docker delega en containerd el almacenamiento de capas (snapshots
	// overlayfs). Su root por defecto es /var/lib/containerd (SD lenta).
	// Generamos /etc/containerd/config.toml apuntando al pool SSD.
	//
	// Formato config.toml v2 (containerd 1.x/2.x):
	//   version = 2
	//   root = "<pool>/docker/containerd"
	//   state = "/run/containerd"   (state es efímero · runtime · se queda en /run)
	os.MkdirAll("/etc/containerd", 0755)
	containerdConf := fmt.Sprintf("version = 2\nroot = %q\nstate = \"/run/containerd\"\n", containerdPath)
	if err := os.WriteFile("/etc/containerd/config.toml", []byte(containerdConf), 0644); err != nil {
		return nil, asHTTPError(500, "Failed to write containerd config.toml: %v", err)
	}
	log.Printf("containerd config.toml → root=%s", containerdPath)

	// ── 5. Install Docker if not present ──
	dockerAvailable := isDockerInstalledGo()
	if !dockerAvailable {
		updateOpProgressSafe(ctx, opID, 30, "Installing Docker engine (this can take several minutes)")
		log.Println("Docker not found, installing...")
		runSafe("systemctl", "stop", "docker.socket", "docker", "containerd")

		installCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		// SECURITY: Download Docker install script to file first, then execute
		// (avoids pipe-to-shell which can't be verified)
		scriptPath := "/tmp/docker-install.sh"
		if _, ok := runSafe("curl", "-fsSL", "https://get.docker.com", "-o", scriptPath); !ok {
			return nil, asHTTPError(500, "Failed to download Docker install script")
		}
		// Verify script was downloaded and is non-empty
		if info, err := os.Stat(scriptPath); err != nil || info.Size() < 1000 {
			os.Remove(scriptPath)
			return nil, asHTTPError(500, "Docker install script is invalid or empty")
		}
		defer os.Remove(scriptPath)

		// Esperar a que apt/dpkg estén libres antes de instalar. Ubuntu lanza
		// actualizaciones automáticas (unattended-upgrades) que toman el lock
		// de dpkg al arrancar el sistema · si el script de Docker corre a la
		// vez, falla con "Could not get lock" (exit 100). Esperamos hasta 120s.
		if !waitForAptLock(installCtx, 120*time.Second) {
			return nil, asHTTPError(500, "apt/dpkg is busy (another package operation is running). Please wait and retry.")
		}

		// dpkg conffile guard · Docker 29 / containerd 2.x empaqueta
		// /etc/containerd/config.toml como conffile gestionado. Como NimOS lo
		// escribe ANTES (paso 4b · root de containerd en el pool), al instalar
		// el paquete dpkg ve el fichero modificado y, en modo interactivo,
		// PREGUNTA "¿mantener tu versión o la del paquete?". Sin TTY que
		// responda, el proceso se cuelga PARA SIEMPRE → Docker queda a medio
		// configurar → "Dependency failed" al arrancar el servicio (bug de
		// instalación en limpio, 23/06/2026). Forzamos conffile = confold (keep
		// existing → conserva la config de NimOS) + confdef, para que NUNCA
		// pregunte. Drop-in temporal en apt.conf.d (cubre todas las llamadas
		// apt internas de get.docker.com, no solo una).
		const aptConfPath = "/etc/apt/apt.conf.d/99nimos-docker-confold"
		if err := os.WriteFile(aptConfPath,
			[]byte("Dpkg::Options { \"--force-confdef\"; \"--force-confold\"; };\n"), 0644); err != nil {
			log.Printf("warning: no se pudo escribir %s: %v", aptConfPath, err)
		}
		defer os.Remove(aptConfPath)

		cmd := exec.CommandContext(installCtx, "bash", scriptPath)
		// noninteractive + listchanges off + needrestart automático: las tres
		// vías por las que un apt-get puede quedarse esperando input y colgar.
		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"APT_LISTCHANGES_FRONTEND=none",
			"NEEDRESTART_MODE=a",
		)
		installOut, err := cmd.CombinedOutput()
		os.Remove(aptConfPath)
		if err != nil {
			log.Printf("Docker install failed: %v\nOutput: %s", err, string(installOut))
			return nil, asHTTPError(500, "Docker installation failed. Check system logs.")
		}
		log.Println("Docker engine installed")
		runSafe("usermod", "-aG", "docker", "nimos")
		runSafe("usermod", "-aG", "docker", "nimos")
		dockerAvailable = true
		// Stop whatever the installer started — we restart with our config
		runSafe("systemctl", "stop", "docker.socket", "docker", "containerd")
	} else {
		// Docker exists — stop to reconfigure
		runSafe("systemctl", "stop", "docker.socket", "docker", "containerd")
	}

	updateOpProgressSafe(ctx, opID, 80, "Starting Docker service")

	// ── 6. Kill /var/lib/docker y /var/lib/containerd — pool only ──
	// Seguro: si NimOS no había instalado Docker antes, el check APP-063 en
	// paso 0 ya verificó que /var/lib/docker está vacío o no existe. Si NimOS
	// SÍ había instalado Docker antes, data-root ya estaba en el pool desde
	// el primer install · /var/lib/docker no debería tener data nuestra.
	//
	// containerd: su root viejo en la SD se limpia · las capas se
	// recrearán en el pool al instalar apps. Sin esto, quedaría 5GB+ de
	// capas huérfanas saturando la SD (bug detectado 29/05/2026).
	runSafe("rm", "-rf", "/var/lib/docker")
	runSafe("rm", "-rf", "/var/lib/containerd")

	// ── 7. Start Docker with correct config ──
	if dockerAvailable {
		// containerd primero · debe arrancar con el nuevo config.toml (root
		// en el pool) ANTES que Docker, porque Docker depende de él. Si
		// containerd ya estaba corriendo con la config vieja, este restart
		// lo recarga apuntando al pool.
		runSafe("systemctl", "enable", "containerd")
		runSafe("systemctl", "restart", "containerd")
		time.Sleep(1 * time.Second)

		runSafe("systemctl", "enable", "docker.service", "docker.socket")
		runSafe("systemctl", "start", "docker")
		time.Sleep(2 * time.Second)

		// Verify Docker root
		rootDir, _ := runSafe("docker", "info", "--format", "{{.DockerRootDir}}")
		rootDir = strings.TrimSpace(rootDir)
		if rootDir != "" && rootDir != dockerDataPath {
			log.Printf("WARNING: Docker Root Dir=%s expected=%s", rootDir, dockerDataPath)
		} else {
			log.Printf("Docker Root Dir confirmed: %s", dockerDataPath)
		}

		// Verify containerd root está en el pool (no en la SD)
		cdRoot, _ := runSafe("containerd", "config", "dump")
		if strings.Contains(cdRoot, containerdPath) {
			log.Printf("containerd root confirmed: %s", containerdPath)
		} else {
			log.Printf("WARNING: containerd root may not be on pool · expected=%s", containerdPath)
		}

		// ── 8. Permissions for FileManager ──
		runSafe("chmod", "755", dockerPath)
		runSafe("chmod", "755", filepath.Join(dockerPath, "containers"))
		runSafe("chmod", "755", filepath.Join(dockerPath, "stacks"))
		runSafe("chmod", "755", filepath.Join(dockerPath, "volumes"))

		updateOpProgressSafe(ctx, opID, 90, "Creating docker-apps share")

		// ── 9. (Refactor permisos Fase 3) ──
		// ELIMINADO: el share "docker-apps" + grupo compartido
		// nimos-share-docker-apps. Las apps Docker son cajas confinadas,
		// gestionables SOLO por la UI de NimOS (el FileManager corre como root
		// y navega/edita todo). NO se exponen por SMB/NFS. Cada app es dueña de
		// su volumen con su UID único (ver app_uids.go · applyAppPermissions).
		// El directorio containers/ no necesita grupo ni share humano.
	}

	updateOpProgressSafe(ctx, opID, 95, "Registering Docker engine")

	// ── 10. Save config ──
	conf := getDockerConfigGo()
	conf["installed"] = true
	conf["dockerAvailable"] = dockerAvailable
	conf["path"] = dockerPath
	if perms, ok := body["permissions"].([]interface{}); ok {
		conf["permissions"] = perms
	}
	conf["installedAt"] = time.Now().UTC().Format(time.RFC3339)
	saveDockerConfigGo(conf)

	// ── 11. APP-013 · registro vía único punto canónico ──
	// Antes de Beta 8.1.1 había un dbServiceRegister hardcodeado aquí con
	// Status="running"/Health="healthy" sin verificar nada, paralelo al
	// detector. Si los IDs divergían (findPoolFromPath vs targetPool.Name),
	// quedaban dos rows.
	//
	// Flujo actual:
	//   1. saveDockerConfigGo deja installed=true en docker.json
	//   2. runAutoRegister invoca detectDockerEngine que inserta la instance
	//      con Status="unknown"
	//   3. reconcileServices verifica systemctl is-active docker y corrige
	//      el status real inmediatamente · sin esperar al tick (≤30s)
	//   4. ForceDockerCacheRefresh prepara la cache para que /api/services
	//      pueda servir Docker engine + children sin lag perceptible
	runAutoRegister(ctx)
	reconcileServices()
	ForceDockerCacheRefresh(ctx)

	return map[string]interface{}{"ok": true, "path": dockerPath, "dockerAvailable": dockerAvailable}, nil
}

func dockerUninstall(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	runShellStatic("docker stop $(docker ps -aq) 2>/dev/null || true")
	runShellStatic("docker rm $(docker ps -aq) 2>/dev/null || true")
	runSafe("systemctl", "stop", "docker")
	runSafe("systemctl", "disable", "docker")
	runShellStatic("apt-get purge -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin 2>/dev/null || true")
	runSafe("rm", "-f", "/etc/docker/daemon.json")

	// Deregister from service registry
	conf := getDockerConfigGo()
	if p, _ := conf["path"].(string); strings.HasPrefix(p, nimosPoolsDir+"/") {
		parts := strings.Split(strings.TrimPrefix(p, nimosPoolsDir+"/"), "/")
		if len(parts) > 0 {
			dbServiceDelete("docker@" + parts[0])
		}
	}

	conf["installed"] = false
	conf["dockerAvailable"] = false
	conf["path"] = nil
	conf["permissions"] = []interface{}{}
	conf["installedAt"] = nil
	saveDockerConfigGo(conf)
	jsonOk(w, map[string]interface{}{"ok": true})
}

func dockerUninstallConfig(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	conf := getDockerConfigGo()
	conf["installed"] = false
	conf["path"] = nil
	conf["permissions"] = []interface{}{}
	conf["installedAt"] = nil
	saveDockerConfigGo(conf)
	jsonOk(w, map[string]interface{}{"ok": true})
}

// waitForAptLock espera hasta que el lock de dpkg/apt esté libre, o hasta que
// se agote el timeout. Devuelve true si el lock quedó libre, false si expiró.
//
// BUG FIX (12/06/2026): en una instalación NimOS desde cero, Ubuntu suele estar
// corriendo unattended-upgrades (actualizaciones automáticas) que toman el lock
// de dpkg. Si el script de Docker corre a la vez, apt falla con "Could not get
// lock /var/lib/dpkg/lock-frontend" (exit 100). Esperar a que se libere evita
// ese fallo · el caso típico es que las actualizaciones de arranque terminen
// en segundos o pocos minutos.
//
// Comprueba el lock con `fuser` sobre los archivos de lock de dpkg/apt. Si
// fuser no está disponible, asume libre (no bloquea la instalación).
func waitForAptLock(ctx context.Context, timeout time.Duration) bool {
	lockFiles := []string{
		"/var/lib/dpkg/lock-frontend",
		"/var/lib/dpkg/lock",
		"/var/lib/apt/lists/lock",
	}

	deadline := time.Now().Add(timeout)
	for {
		if aptLockFree(lockFiles) {
			return true
		}
		if time.Now().After(deadline) {
			log.Println("waitForAptLock: timeout esperando el lock de apt/dpkg")
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(3 * time.Second):
			log.Println("waitForAptLock: apt/dpkg ocupado, esperando...")
		}
	}
}

// aptLockFree indica si NINGÚN proceso tiene abiertos los archivos de lock.
// Usa `fuser <file>` · exit 0 significa que algún proceso lo tiene (ocupado),
// exit !=0 significa libre. Si fuser no existe, asume libre.
func aptLockFree(lockFiles []string) bool {
	for _, lf := range lockFiles {
		if _, err := os.Stat(lf); err != nil {
			continue // el lock file no existe · nada que comprobar
		}
		// fuser devuelve 0 si hay procesos usando el archivo
		if _, busy := runSafe("fuser", lf); busy {
			return false // algún proceso tiene el lock
		}
	}
	return true
}
