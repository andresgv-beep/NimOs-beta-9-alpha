// nimhealth_docker.go — Lógica de parsing Docker para NimHealth.
//
// Aislado de los handlers HTTP y del observer para que el archivo de
// docker parsing tenga responsabilidad única: cruzar docker_apps
// (config persistente) con docker ps -a (runtime) y devolver una lista
// de DockerAppStatus enriquecida.
//
// El observer (nimhealth_observer.go) llama a getDockerAppStatuses
// UNA vez por tick (~30s) y guarda el resultado en dockerCache.
// El handler HTTP NUNCA llama estas funciones directamente · lee cache.
//
// REGLA: cero docker stats, cero docker inspect periódico. Solo
// docker ps -a. Si se necesita inspect, es on-demand desde un endpoint
// /detail (futuro, fuera de scope actual).

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// dockerContainer · línea parseada de docker ps -a
type dockerContainer struct {
	Name   string
	Image  string
	Status string // raw: "Up 3 hours", "Exited (0) 2h ago", ...
	Ports  string // raw: "0.0.0.0:8096->8096/tcp, ..."
	AppID  string // valor del label com.nimos.app_id · "" si legacy/no-NimOS
}

// ═══════════════════════════════════════════════════════════════════════
// Stack-matching heuristics · APP-017 · single source of truth
//
// Cuando una app es de tipo "stack" (docker-compose), su container principal
// suele tener un sufijo derivado del nombre del servicio en compose:
//
//	immich      → immich_server, immich-server
//	nextcloud   → nextcloud_app, nextcloud-app
//	calibre-web → calibre-web (sin sufijo, exact match)
//
// stackContainerSuffixes lista los sufijos en orden de preferencia.
// El sufijo vacío "" debe ir PRIMERO (exact match prevalece).
//
// stackSubKeywords lista substrings que identifican subcontainers internos
// de stacks (redis, postgres, ml, db, etc) · NO se cuentan como orphans
// cuando hay un container "principal" con prefijo compartido.
// ═══════════════════════════════════════════════════════════════════════

var (
	stackContainerSuffixes = []string{"", "_server", "-server", "_app", "-app"}
	stackSubKeywords       = []string{"_redis", "_postgres", "_ml", "_machine", "_db", "_cache"}
)

// matchContainerForAppID busca el container que corresponde a un app ID.
//
// Estrategia (orden):
//
//  1. Exact match + sufijos de stack (immich, immich_server, immich-server, ...).
//  2. Fallback prefix-match (cualquier name que empiece por "{id}_" o "{id}-").
//
// Devuelve (containerName, *container) si encuentra, ("", nil) si no.
// El caller marca matchedContainers para luego filtrar orphans correctamente.
//
// NOTA: el map containers se ITERA en el fallback, así que el primer match
// gana. Es no-determinístico si hay múltiples candidatos con el mismo prefijo
// (improbable en práctica: cada stack tiene container principal único).
func matchContainerForAppID(appID string, containers map[string]dockerContainer) (string, *dockerContainer) {
	// 0. Match ROBUSTO por label com.nimos.app_id (item 6 backlog) · gana sobre
	//    el matching por nombre, que es frágil (adivina por sufijos/separadores).
	//    En stacks multi-servicio el MISMO app_id va en todos los servicios
	//    (immich_server/_postgres/_redis/...), así que entre los del label
	//    desempatamos con la MISMA preferencia por nombre (matchByName acotado al
	//    subconjunto) · nunca peor que el match por nombre y sin cruzar con otra
	//    app. Los containers legacy/no-NimOS (sin label) caen al fallback por
	//    nombre sobre el conjunto completo.
	labelled := map[string]dockerContainer{}
	for name, c := range containers {
		if c.AppID != "" && c.AppID == appID {
			labelled[name] = c
		}
	}
	if len(labelled) > 0 {
		if name, c := matchByName(appID, labelled); c != nil {
			return name, c
		}
		// El label coincide pero ningún patrón de nombre encaja · es de esta app
		// con certeza → devolver uno DETERMINISTA (nombre menor), no aleatorio.
		pick := ""
		for name := range labelled {
			if pick == "" || name < pick {
				pick = name
			}
		}
		c := labelled[pick]
		return pick, &c
	}
	return matchByName(appID, containers)
}

// matchByName · heurística de matching container↔app por NOMBRE (sufijo en orden
// de preferencia, prefijo, y normalización guion↔guion_bajo). Fallback cuando no
// hay label com.nimos.app_id fiable (containers legacy o no-NimOS), y también el
// desempate dentro del conjunto ya filtrado por label.
func matchByName(appID string, containers map[string]dockerContainer) (string, *dockerContainer) {
	// 1. Sufijos en orden de preferencia (exact match primero).
	for _, suffix := range stackContainerSuffixes {
		candidate := appID + suffix
		if c, ok := containers[candidate]; ok {
			cc := c // capturar por valor antes de devolver puntero
			return candidate, &cc
		}
	}
	// 2. Fallback: prefix-match (immich_server cuando appID="immich").
	for name, c := range containers {
		if strings.HasPrefix(name, appID+"_") || strings.HasPrefix(name, appID+"-") {
			cc := c
			return name, &cc
		}
	}
	// 3. Fallback guion ↔ guion_bajo · Docker Compose y los container_name
	//    custom mezclan '-' y '_' libremente (proyecto "matrix-synapse" con
	//    container_name "matrix_synapse"). El matching por nombre falla porque
	//    appID usa '-' y el container usa '_'. Normalizamos ambos a '-' y
	//    comparamos exacto o por prefijo del separador. Esto evita que un
	//    container legítimo aparezca como "huérfano" (app fantasma) en el
	//    launcher cuando su nombre difiere solo en el tipo de separador.
	norm := func(s string) string { return strings.ReplaceAll(s, "_", "-") }
	normID := norm(appID)
	for name, c := range containers {
		nn := norm(name)
		if nn == normID || strings.HasPrefix(nn, normID+"-") {
			cc := c
			return name, &cc
		}
	}
	return "", nil
}

// isPossibleStackSubContainer · APP-017 · ¿el container `name` es un
// subcomponente interno de algún stack que ya está representado?
//
// Devuelve true si:
//   - El nombre contiene un keyword conocido de stack-sub (redis, postgres...)
//   - O su prefijo coincide con algún ID que YA fue matcheado (matchedIDs).
//
// matchedIDs debe ser el conjunto de container names que ya fueron asociados
// a una app registered (la salida de matchContainerForAppID). El prefijo se
// extrae del primer segmento separado por "_".
//
// Uso: contar orphans excluyendo subcontainers de stacks legítimos.
func isPossibleStackSubContainer(name string, matchedIDs map[string]bool) bool {
	// 1. Keywords directos (redis, postgres, ml, machine, db, cache).
	for _, sub := range stackSubKeywords {
		if strings.Contains(name, sub) {
			return true
		}
	}
	// 2. Prefijo coincide con algún match previo (immich_microservices cuando
	//    ya está matched immich_server).
	for matched := range matchedIDs {
		prefix := strings.SplitN(matched, "_", 2)[0]
		if strings.HasPrefix(name, prefix+"_") || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}

// ═══════════════════════════════════════════════════════════════════════
// Aggregate health · Docker engine como agregado de sus children
//
// Vocabulario oficial HealthStatus (7 constantes en nimos_health.go).
// NimHealth usa 6 de esas 7 (NO usa HealthIncomplete · ver §4.7 doc).
//
// Reglas:
//
//	no children                                     → HealthHealthy (engine OK vacío)
//	all containers stopped (engine OK)              → HealthHealthy (no es failure)
//	any child status=error  OR  health=failed       → HealthDegraded
//	any child stopped + otros running               → HealthDegraded (mix)
//	all running+healthy                              → HealthHealthy
// ═══════════════════════════════════════════════════════════════════════

func ComputeDockerAggregateHealth(children []DockerAppStatus) HealthStatus {
	if len(children) == 0 {
		return HealthHealthy
	}
	allStopped := true
	hasError := false
	for _, c := range children {
		if c.Status != "stopped" {
			allStopped = false
		}
		if c.Status == "error" || c.Health == string(HealthFailed) {
			hasError = true
		}
	}
	if hasError {
		return HealthDegraded
	}
	if allStopped {
		// Engine arriba, containers todos parados · no es failure
		return HealthHealthy
	}
	// Hay al menos uno running · si alguno está stopped es mix → degraded
	for _, c := range children {
		if c.Status == "stopped" {
			return HealthDegraded
		}
	}
	return HealthHealthy
}

// ═══════════════════════════════════════════════════════════════════════
// getDockerAppStatuses · construye DockerAppStatus para cada app
// registrada cruzando docker_apps (SQLite) con docker ps -a.
//
// Devuelve la lista y el conteo de containers huérfanos.
// ═══════════════════════════════════════════════════════════════════════

func getDockerAppStatuses(dockerServiceID string) ([]DockerAppStatus, int) {
	ctx := context.Background()

	// 1. Apps registradas en la DB · incluyendo las marcadas deleting=1.
	//    Las activas se procesan normalmente; las deleting se usan SOLO para
	//    filtrarlas del orphan count (su container puede seguir vivo durante
	//    el cleanup async pero no es un orphan, es uninstall in progress).
	allRegistered, err := appsRepo.ListDockerAppsIncludingDeleting(ctx)
	if err != nil {
		logMsg("apps: getDockerAppStatuses list failed: %v", err)
		return []DockerAppStatus{}, 0
	}
	var registered []*DBDockerApp
	deletingIDs := map[string]bool{}
	for _, a := range allRegistered {
		if a.Deleting {
			deletingIDs[a.ID] = true
		} else {
			registered = append(registered, a)
		}
	}

	// 2. docker ps -a (ALL containers, not just running)
	out, _ := runSafe("docker", "ps", "-a", "--format", "{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}|{{.Labels}}")
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

	// 3. Cross: para cada app registrada, encontrar su container
	var statuses []DockerAppStatus
	matchedContainers := map[string]bool{}

	for _, reg := range registered {
		// APP-017 · matching delegado a helper compartido con dockerInstalledApps.
		containerName, found := matchContainerForAppID(reg.ID, containers)
		if found != nil {
			matchedContainers[containerName] = true
		}

		// Construir status base
		// Default: stopped/healthy · stopped no es failure por sí mismo,
		// el campo status ya transmite la inactividad.
		status := DockerAppStatus{
			ServiceBase: ServiceBase{
				ID:     reg.ID,
				Type:   "docker-app",
				Parent: dockerServiceID,
				Name:   reg.Name,
				Status: "stopped",
				Health: string(HealthHealthy),
			},
			Image:         reg.Image,
			Icon:          reg.Icon,
			ContainerName: containerName,
			OpenMode:      reg.OpenMode,
		}

		if found != nil {
			status.Status = NormalizeDockerStatus(found.Status)
			if status.Status == "running" {
				status.Health = string(HealthHealthy)
			} else if status.Status == "error" {
				status.Health = string(HealthFailed)
			}
			status.Uptime = extractUptime(found.Status)
			status.Ports = parsePorts(found.Ports, reg)
		} else {
			// Registrada pero sin container · usar PortsJSON canónico (APP-033).
			// reg.parsedPorts() devuelve el array completo desde JSON, con
			// fallback a reg.Port legacy si JSON está vacío.
			status.Status = "stopped"
			status.Health = string(HealthHealthy)
			status.Ports = reg.parsedPorts()
		}

		statuses = append(statuses, status)
	}

	// 4. Contar orphans (en docker ps pero no en docker_apps activos)
	//    APP-031 · filtrar containers cuyo appId está en deleting=1: NO son
	//    orphans, son uninstall in progress.
	//    APP-017 · subcontainers de stacks excluidos vía isPossibleStackSubContainer.
	orphanCount := 0
	for name := range containers {
		if matchedContainers[name] {
			continue
		}
		// APP-031 · si el container o su prefijo coincide con una app en deleting,
		// no contarlo como orphan (su row se borrará en cuanto el cleanup termine).
		if deletingIDs[name] {
			continue
		}
		isUninstalling := false
		for delID := range deletingIDs {
			if strings.HasPrefix(name, delID+"_") || strings.HasPrefix(name, delID+"-") {
				isUninstalling = true
				break
			}
		}
		if isUninstalling {
			continue
		}
		if isPossibleStackSubContainer(name, matchedContainers) {
			continue
		}
		orphanCount++
	}

	if statuses == nil {
		statuses = []DockerAppStatus{}
	}
	return statuses, orphanCount
}

// extractUptime · de docker ps STATUS, e.g. "Up 3 hours" → "3h"
func extractUptime(rawStatus string) string {
	lower := strings.ToLower(rawStatus)
	if !strings.Contains(lower, "up") {
		return ""
	}
	upRegex := regexp.MustCompile(`(?i)up\s+([^,(]+)`)
	matches := upRegex.FindStringSubmatch(rawStatus)
	if len(matches) < 2 {
		return ""
	}
	dur := strings.TrimSpace(matches[1])
	dur = strings.ReplaceAll(dur, " hours", "h")
	dur = strings.ReplaceAll(dur, " hour", "h")
	dur = strings.ReplaceAll(dur, " minutes", "m")
	dur = strings.ReplaceAll(dur, " minute", "m")
	dur = strings.ReplaceAll(dur, " seconds", "s")
	dur = strings.ReplaceAll(dur, " second", "s")
	dur = strings.ReplaceAll(dur, " days", "d")
	dur = strings.ReplaceAll(dur, " day", "d")
	dur = strings.ReplaceAll(dur, " weeks", "w")
	dur = strings.ReplaceAll(dur, " week", "w")
	dur = strings.ReplaceAll(dur, "About a ", "1")
	dur = strings.ReplaceAll(dur, "About an ", "1")
	return strings.TrimSpace(dur)
}

// parsePorts · extrae bindings de docker ps PORTS, mergeando con config.
func parsePorts(rawPorts string, config *DBDockerApp) []PortBinding {
	if rawPorts == "" {
		if config != nil && config.Port > 0 {
			return []PortBinding{{Declared: config.Port, Host: config.Port}}
		}
		return []PortBinding{}
	}

	var bindings []PortBinding
	portRegex := regexp.MustCompile(`(\d+):(\d+)/`)
	matches := portRegex.FindAllStringSubmatch(rawPorts, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		key := m[1] + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		var host, declared int
		fmt.Sscanf(m[1], "%d", &host)
		fmt.Sscanf(m[2], "%d", &declared)
		bindings = append(bindings, PortBinding{Host: host, Declared: declared})
	}
	if bindings == nil {
		bindings = []PortBinding{}
	}
	return bindings
}
