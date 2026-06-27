// docker_routes.go — Router central del módulo Docker (Beta 8.1)
//
// Dos dispatchers:
//   handleDockerRoutes      · URLs exactas (case strings sin regex)
//   handleDockerRegexRoutes · URLs con identificadores (regex matching)
//
// Registrados desde http.go::setupMux para los prefijos:
//   /api/docker/
//   /api/permissions/
//   /api/firewall/* (legacy; ver firewall.go)
//
// Aviso histórico: el router de Docker SOLÍA dispatchear también URLs de
// hardware (/api/hardware/install-driver, /api/hardware/driver-log/) pero
// esos cases eran código muerto · mux.HandleFunc("/api/hardware/") en
// http.go enruta esos paths a handleHardwareRoutes (en hardware_system.go tras
// la modularización de Beta 8.2). Las
// funciones hardwareInstallDriver y hardwareDriverLog se eliminaron durante
// el sprint post-cierre (mayo 2026).

package main

import (
	"net/http"
	"regexp"
	"strings"
)

func handleDockerRoutes(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	method := r.Method

	switch {
	case urlPath == "/api/docker/status" && method == "GET":
		dockerStatus(w, r)
	case urlPath == "/api/docker/permissions" && method == "GET":
		dockerPermissionsGet(w, r)
	case urlPath == "/api/docker/permissions" && method == "PUT":
		dockerPermissionsSet(w, r)
	case urlPath == "/api/docker/app-permissions" && method == "GET":
		dockerAppPermissions(w, r)
	case urlPath == "/api/docker/containers" && method == "GET":
		dockerContainersList(w, r)
	case urlPath == "/api/docker/installed-apps" && method == "GET":
		dockerInstalledApps(w, r)
	case urlPath == "/api/docker/install" && method == "POST":
		dockerInstall(w, r)
	case urlPath == "/api/docker/uninstall" && method == "POST":
		dockerUninstall(w, r)
	case urlPath == "/api/docker/uninstall" && method == "DELETE":
		dockerUninstallConfig(w, r)
	case urlPath == "/api/docker/container" && method == "POST":
		dockerContainerCreate(w, r)
	case urlPath == "/api/docker/stack" && method == "POST":
		dockerStackDeploy(w, r)
	case urlPath == "/api/docker/updates-summary" && method == "GET":
		dockerUpdatesSummary(w, r)
	case urlPath == "/api/docker/network-pool" && method == "GET":
		dockerNetworkPoolStatus(w, r)
	case urlPath == "/api/docker/network-pool/restart" && method == "POST":
		dockerNetworkPoolRestart(w, r)
	case urlPath == "/api/permissions/matrix" && method == "GET":
		permissionsMatrix(w, r)
	case urlPath == "/api/firewall/add-rule" && method == "POST":
		firewallAddRule(w, r)
	case urlPath == "/api/firewall/remove-rule" && method == "POST":
		firewallRemoveRule(w, r)
	case urlPath == "/api/firewall/toggle" && method == "POST":
		firewallToggle(w, r)
	default:
		// Regex routes
		if handleDockerRegexRoutes(w, r) {
			return
		}
		jsonError(w, 404, "Not found")
	}
}

func handleDockerRegexRoutes(w http.ResponseWriter, r *http.Request) bool {
	urlPath := r.URL.Path
	method := r.Method

	// PUT /api/docker/app-permissions/:appId
	reAppPerm := regexp.MustCompile(`^/api/docker/app-permissions/([a-zA-Z0-9_-]+)$`)
	if m := reAppPerm.FindStringSubmatch(urlPath); m != nil && method == "PUT" {
		dockerAppPermUpdate(w, r, m[1])
		return true
	}

	// GET /api/docker/app-access/:appId
	reAppAccess := regexp.MustCompile(`^/api/docker/app-access/([a-zA-Z0-9_-]+)$`)
	if m := reAppAccess.FindStringSubmatch(urlPath); m != nil && method == "GET" {
		dockerAppAccess(w, r, m[1])
		return true
	}

	// GET /api/docker/app-folders/:appId
	reAppFolders := regexp.MustCompile(`^/api/docker/app-folders/([a-zA-Z0-9_-]+)$`)
	if m := reAppFolders.FindStringSubmatch(urlPath); m != nil && method == "GET" {
		dockerAppFolders(w, r, m[1])
		return true
	}

	// POST /api/docker/container/:id/:action
	reAction := regexp.MustCompile(`^/api/docker/container/([a-zA-Z0-9_.-]+)/(start|stop|restart)$`)
	if m := reAction.FindStringSubmatch(urlPath); m != nil && method == "POST" {
		dockerContainerAction(w, r, m[1], m[2])
		return true
	}

	// DELETE /api/docker/container/:id
	reDelete := regexp.MustCompile(`^/api/docker/container/([a-zA-Z0-9_.-]+)$`)
	if m := reDelete.FindStringSubmatch(urlPath); m != nil && method == "DELETE" {
		dockerContainerDelete(w, r, m[1])
		return true
	}

	// GET /api/docker/container/:id/mounts
	reMounts := regexp.MustCompile(`^/api/docker/container/([a-zA-Z0-9_-]+)/mounts$`)
	if m := reMounts.FindStringSubmatch(urlPath); m != nil && method == "GET" {
		dockerContainerMounts(w, r, m[1])
		return true
	}

	// POST /api/docker/container/:id/rebuild
	reRebuild := regexp.MustCompile(`^/api/docker/container/([a-zA-Z0-9_-]+)/rebuild$`)
	if m := reRebuild.FindStringSubmatch(urlPath); m != nil && method == "POST" {
		dockerContainerRebuild(w, r, m[1])
		return true
	}

	// DELETE /api/docker/stack/:id
	reStack := regexp.MustCompile(`^/api/docker/stack/([a-zA-Z0-9_-]+)$`)
	if m := reStack.FindStringSubmatch(urlPath); m != nil && method == "DELETE" {
		dockerStackDelete(w, r, m[1])
		return true
	}

	// GET /api/docker/app/:id/update-check · sprint Updates 25/05/2026
	reUpdateCheck := regexp.MustCompile(`^/api/docker/app/([a-zA-Z0-9_-]+)/update-check$`)
	if m := reUpdateCheck.FindStringSubmatch(urlPath); m != nil && method == "GET" {
		dockerUpdateCheck(w, r, m[1])
		return true
	}

	// POST /api/docker/app/:id/update · sprint Updates 25/05/2026
	reAppUpdate := regexp.MustCompile(`^/api/docker/app/([a-zA-Z0-9_-]+)/update$`)
	if m := reAppUpdate.FindStringSubmatch(urlPath); m != nil && method == "POST" {
		dockerAppUpdate(w, r, m[1])
		return true
	}

	// GET /api/docker/app/:id/broken · ¿contenedor roto (necesita reparar)?
	reAppBroken := regexp.MustCompile(`^/api/docker/app/([a-zA-Z0-9_-]+)/broken$`)
	if m := reAppBroken.FindStringSubmatch(urlPath); m != nil && method == "GET" {
		dockerAppBroken(w, r, m[1])
		return true
	}

	// POST /api/docker/app/:id/repair · recrear contenedor roto (re-pull + recreate)
	reAppRepair := regexp.MustCompile(`^/api/docker/app/([a-zA-Z0-9_-]+)/repair$`)
	if m := reAppRepair.FindStringSubmatch(urlPath); m != nil && method == "POST" {
		dockerAppRepair(w, r, m[1])
		return true
	}

	// GET /api/docker/pull/:image
	if strings.HasPrefix(urlPath, "/api/docker/pull/") && method == "GET" {
		dockerPull(w, r)
		return true
	}

	return false
}

// ═══════════════════════════════════
// Handlers
// ═══════════════════════════════════
