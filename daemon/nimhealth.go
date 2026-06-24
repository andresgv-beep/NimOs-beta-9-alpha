// nimhealth.go — NimHealth backend module · HTTP layer.
//
// HOME PRINCIPAL del módulo NimHealth. Este archivo contiene:
//
//   · HTTP handlers de /api/services (lista, dependencies, acciones, logs)
//   · Reason codes (enum cerrado de 9 valores)
//   · Boot grace period helpers (hostUptime, inBootGracePeriod)
//   · DB query timeout wrappers (defensa local, ver §4.5 doc)
//
// El observer (Reconciler), la cache Docker, los detectores y el parsing
// de docker ps viven en archivos hermanos:
//
//   · nimhealth_observer.go    · Reconciler implementation + cache
//   · nimhealth_detectors.go   · 7 detectores + helpers
//   · nimhealth_docker.go      · getDockerAppStatuses + parsing
//
// REGLA CENTRAL (frontera sagrada, ver INTEGRATION-v1.2 §6):
//
//   SALUD es BINARIO/ENUM (HealthStatus del enum global)
//   MÉTRICAS son NUMÉRICAS (% CPU, MB RAM, MB/s)
//
//   NimHealth hace lo PRIMERO. NimMonitor hace lo SEGUNDO.

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// Reason codes · enum cerrado de 9 valores
//
// Campo opcional en la respuesta de /api/services. Presente solo si
// status=unknown o health no es healthy. Ayuda a la UI a explicar al
// usuario el porqué de un estado ambiguo.
//
// Reglas:
//   · ENUM CERRADO: si necesitas un 10º código, se discute antes de añadirlo.
//   · RUNTIME-ONLY: NUNCA se persiste en service_instances. Es derivado del
//     estado actual y de timestamps; el observer/handler lo computa al
//     construir el response.
// ═══════════════════════════════════════════════════════════════════════

const (
	// initializing · primer tick del observer aún no ha corrido al boot.
	ReasonInitializing = "initializing"

	// boot_grace_period · host arrancó hace < dockerBootGracePeriod;
	// containers en transición no se marcan como failed todavía.
	ReasonBootGracePeriod = "boot_grace_period"

	// observer_timeout · docker existe y corre, pero docker ps -a no
	// respondió dentro del timeout (2s).
	ReasonObserverTimeout = "observer_timeout"

	// docker_unavailable · daemon parado, socket /var/run/docker.sock
	// ausente, o docker no instalado.
	ReasonDockerUnavailable = "docker_unavailable"

	// db_timeout · query a SQLite excedió 2s.
	ReasonDbTimeout = "db_timeout"

	// paused · usuario detuvo el servicio explícitamente.
	ReasonPaused = "paused"

	// degraded_children · Docker engine OK pero al menos un container
	// está en error. Acompaña a health=degraded.
	ReasonDegradedChildren = "degraded_children"

	// stale · última observación es de hace > 5×interval (~150s).
	ReasonStale = "stale"

	// not_yet_observed · servicio recién registrado, observer no llegó.
	ReasonNotYetObserved = "not_yet_observed"
)

// ═══════════════════════════════════════════════════════════════════════
// Boot grace period
//
// Durante los primeros dockerBootGracePeriod segundos tras el arranque
// del HOST (no del daemon), containers Docker en transición se reportan
// como starting/unknown con reason=boot_grace_period en lugar de marcarse
// como error/failed prematuramente.
//
// IMPORTANTE: usamos host uptime (/proc/uptime), NO daemon uptime. Si el
// daemon se reinicia solo (crash, update), los containers ya llevan horas
// corriendo · NO queremos otros 90s de gracia inmerecida.
// ═══════════════════════════════════════════════════════════════════════

const dockerBootGracePeriod = 90 * time.Second

// hostUptime devuelve el tiempo desde el último boot del host.
// Si no se puede leer /proc/uptime devuelve un valor grande (99h) para
// que inBootGracePeriod() devuelva false (no aplicar gracia indebida).
func hostUptime() time.Duration {
	data, err := osReadFile("/proc/uptime")
	if err != nil {
		return 99 * time.Hour
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 99 * time.Hour
	}
	var secs float64
	if _, err := fmt.Sscanf(parts[0], "%f", &secs); err != nil {
		return 99 * time.Hour
	}
	return time.Duration(secs * float64(time.Second))
}

// inBootGracePeriod devuelve true si el host arrancó hace menos de
// dockerBootGracePeriod. Usado para suprimir reportes de error
// prematuros en containers Docker durante el arranque.
func inBootGracePeriod() bool {
	return hostUptime() < dockerBootGracePeriod
}

// osReadFile · indirección para poder mockear /proc/uptime en tests.
var osReadFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ═══════════════════════════════════════════════════════════════════════
// DB query timeouts · defensa local (ver INTEGRATION-v1.2 §4.5)
//
// Los wrappers envuelven dbServiceList/Get/Dependencies en una goroutine
// con timeout de 2s. Si SQLite se atasca, NimHealth NO se cuelga · falla
// con error y se reintenta en el siguiente tick.
//
// Esto es defensa LOCAL · no cambia firmas de funciones porque eso
// afectaría a 30+ callsites del repo. Cuando NimOS adopte el patrón
// Repo (ver D-002 en §9 del documento) estos wrappers se eliminan.
// ═══════════════════════════════════════════════════════════════════════

const dbQueryTimeout = 2 * time.Second

func dbServiceListWithTimeout(poolFilter string) ([]ServiceInstance, error) {
	type result struct {
		list []ServiceInstance
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		list, err := dbServiceList(poolFilter)
		ch <- result{list, err}
	}()
	select {
	case r := <-ch:
		return r.list, r.err
	case <-time.After(dbQueryTimeout):
		return []ServiceInstance{}, fmt.Errorf("db_timeout: dbServiceList exceeded %v", dbQueryTimeout)
	}
}

func dbServiceDependenciesWithTimeout(instanceID string) ([]ServiceDependency, error) {
	type result struct {
		list []ServiceDependency
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		list, err := dbServiceDependencies(instanceID)
		ch <- result{list, err}
	}()
	select {
	case r := <-ch:
		return r.list, r.err
	case <-time.After(dbQueryTimeout):
		return []ServiceDependency{}, fmt.Errorf("db_timeout: dbServiceDependencies exceeded %v", dbQueryTimeout)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// HTTP Handlers
// ═══════════════════════════════════════════════════════════════════════

func handleServiceRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	path := r.URL.Path
	method := r.Method

	// GET /api/services — list all services (lectura desde cache + DB)
	if path == "/api/services" && method == "GET" {
		poolFilter := r.URL.Query().Get("pool")
		instances, err := dbServiceListWithTimeout(poolFilter)
		if err != nil {
			// db_timeout o error real · servimos lista vacía con razón.
			jsonOk(w, map[string]interface{}{
				"services":   []map[string]interface{}{},
				"reasonCode": ReasonDbTimeout,
			})
			return
		}

		dockerInstalled := isDockerInstalledGo()
		inGrace := dockerInstalled && inBootGracePeriod()

		result := make([]map[string]interface{}, len(instances))
		for i, inst := range instances {
			var appName string
			db.QueryRow(`SELECT name FROM app_registry WHERE id = ?`, inst.AppID).Scan(&appName)
			deps, _ := dbServiceDependenciesWithTimeout(inst.ID)
			result[i] = inst.ToMap()
			result[i]["appName"] = appName
			depsMap := make([]map[string]interface{}, len(deps))
			for j, d := range deps {
				depsMap[j] = d.ToMap()
			}
			result[i]["dependencies"] = depsMap

			// Docker engine: leer cache (poblada por observer)
			if inst.AppID == "containers" {
				enrichDockerInstance(result[i], dockerInstalled, inGrace)
			}
		}
		jsonOk(w, map[string]interface{}{"services": result})
		return
	}

	// GET /api/services/dependencies?pool=X — check pool dependencies
	if path == "/api/services/dependencies" && method == "GET" {
		poolName := r.URL.Query().Get("pool")
		if poolName == "" {
			jsonError(w, 400, "pool parameter required")
			return
		}
		deps, canDestroy, canForce, err := canDestroyPool(poolName)
		if err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		depsMap := make([]map[string]interface{}, len(deps))
		for i, d := range deps {
			depsMap[i] = d.ToMap()
		}
		jsonOk(w, map[string]interface{}{
			"pool":         poolName,
			"dependencies": depsMap,
			"canDestroy":   canDestroy,
			"canForce":     canForce,
		})
		return
	}

	// POST /api/services/{id}/stop|start|restart
	// GET  /api/services/{id}/logs
	if strings.HasPrefix(path, "/api/services/") {
		handleServiceInstanceRoute(w, r, session, path, method)
		return
	}

	jsonError(w, 404, "Not found")
}

// handleServiceInstanceRoute · maneja /api/services/{id}/{action}
// para acciones puntuales de start/stop/restart/logs en services y
// containers Docker. Separado del handler raíz por claridad.
func handleServiceInstanceRoute(w http.ResponseWriter, r *http.Request, session *DBSession, path, method string) {
	trimmed := strings.TrimPrefix(path, "/api/services/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		jsonError(w, 404, "Not found")
		return
	}
	instanceID := parts[0]
	if decoded, err := url.PathUnescape(instanceID); err == nil {
		instanceID = decoded
	}
	action := parts[1]

	registeredSvc, _ := dbServiceGet(instanceID)
	isDockerApp := registeredSvc == nil && isDockerInstalledGo()

	if registeredSvc == nil && !isDockerApp {
		jsonError(w, 404, "Service not found")
		return
	}

	containerName := instanceID
	var containerNames []string
	if isDockerApp && appsRepo != nil {
		app, _ := appsRepo.GetDockerApp(r.Context(), instanceID)
		if app != nil {
			containerName = app.ID
			// BUG FIX (15/06/2026): buscar los containers reales por label
			// com.nimos.app_id en vez de asumir nombre = app_id. Apps
			// multi-servicio (matrix → matrix_synapse + matrix_element) tienen
			// nombres propios. Si no hay containers con label (apps viejas
			// pre-Fase 2), caemos al nombre = app_id.
			containerNames = getStackContainerNames(r.Context(), app.ID)
		}
	}
	if len(containerNames) == 0 {
		containerNames = []string{containerName} // fallback · nombre = app_id
	}

	// GET /api/services/{id}/logs
	if method == "GET" && action == "logs" {
		n := 50
		if nStr := r.URL.Query().Get("n"); nStr != "" {
			if parsed := parseIntDefault(nStr, 50); parsed > 0 && parsed <= 200 {
				n = parsed
			}
		}

		if isDockerApp {
			handleDockerLogs(w, containerNames, n)
		} else {
			lines, err := getServiceLogs(instanceID, n)
			if err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"logs": lines})
		}
		return
	}

	// POST actions require admin
	if method == "POST" {
		if session.Role != "admin" {
			jsonError(w, 403, "Admin required")
			return
		}

		if isDockerApp {
			handleDockerAction(w, containerNames, action)
		} else {
			handleSystemdAction(w, instanceID, action)
		}
		return
	}

	jsonError(w, 404, "Not found")
}

// handleDockerLogs · pull tail logs de los containers de una app on-demand.
// Para apps multi-container, concatena los logs de todos con un encabezado
// por container.
func handleDockerLogs(w http.ResponseWriter, containerNames []string, n int) {
	var lines []map[string]interface{}
	multi := len(containerNames) > 1
	for _, containerName := range containerNames {
		out, _ := runSafe("docker", "logs", "--tail", fmt.Sprintf("%d", n), "--timestamps", containerName)
		if multi {
			// Encabezado por container para distinguir en apps multi-servicio
			lines = append(lines, map[string]interface{}{
				"timestamp": "",
				"message":   "── " + containerName + " ──",
			})
		}
		if out != "" {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				ts := ""
				msg := line
				if len(line) > 30 && line[4] == '-' {
					if idx := strings.Index(line, " "); idx > 0 && idx < 35 {
						ts = line[:idx]
						msg = line[idx+1:]
					}
				}
				lines = append(lines, map[string]interface{}{"timestamp": ts, "message": msg})
			}
		}
	}
	if lines == nil {
		lines = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"logs": lines})
}

// handleDockerAction · stop/start/restart de los containers de una app.
// Para apps multi-container (matrix → synapse + element), aplica la acción a
// todos. El orden importa en start (dependencias) pero Docker maneja restart
// policies · aquí aplicamos a todos y reportamos si alguno falla.
func handleDockerAction(w http.ResponseWriter, containerNames []string, action string) {
	if action != "stop" && action != "start" && action != "restart" {
		jsonError(w, 404, "Unknown action")
		return
	}

	var failed []string
	for _, containerName := range containerNames {
		_, ok := runSafe("docker", action, containerName)
		if !ok {
			failed = append(failed, containerName)
		}
	}

	if len(failed) > 0 {
		jsonError(w, 500, fmt.Sprintf("failed to %s container(s): %s",
			action, strings.Join(failed, ", ")))
		return
	}
	jsonOk(w, map[string]interface{}{
		"ok":         true,
		"action":     action,
		"containers": containerNames,
	})
}

// handleSystemdAction · stop/start/restart de un service registrado.
func handleSystemdAction(w http.ResponseWriter, instanceID, action string) {
	switch action {
	case "stop":
		if err := serviceStop(instanceID); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "status": "stopping"})
	case "start":
		if err := serviceStart(instanceID); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "status": "starting"})
	case "restart":
		serviceStop(instanceID)
		time.Sleep(1 * time.Second)
		if err := serviceStart(instanceID); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "status": "starting"})
	default:
		jsonError(w, 404, "Unknown action")
	}
}

// enrichDockerInstance · helper para añadir children + health agregada
// + reasonCode al map del Docker engine en el response de /api/services.
// La implementación real está en nimhealth_observer.go (donde vive la
// cache). Este nombre actúa como contrato.
//
// Implementado en nimhealth_observer.go.
