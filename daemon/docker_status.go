// docker_status.go — GET /api/docker/status (Beta 8.1)
//
// Devuelve el estado runtime de Docker + containers visibles. Usado por:
//   · Dashboard NimHealth (sumario rápido)
//   · AppStore capabilities (¿hay Docker?)
//   · Frontend en arranque (decidir qué pantalla mostrar)

package main

import (
	"net/http"
	"time"
)

func dockerStatus(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	// Allow admin or users with Docker permission
	role := session.Role
	hasPerm := hasDockerPermission(session)
	if role != "admin" && !hasPerm {
		jsonError(w, 403, "No permission")
		return
	}
	conf := getDockerConfigGo()
	dockerRunning := isDockerInstalledGo()

	if dockerRunning && conf["installed"] != true {
		conf["installed"] = true
		conf["installedAt"] = time.Now().UTC().Format(time.RFC3339)
		saveDockerConfigGo(conf)
	}

	var containers []map[string]interface{}
	if dockerRunning && hasPerm {
		containers = getRealContainersGo()
	} else {
		containers = []map[string]interface{}{}
	}

	jsonOk(w, map[string]interface{}{
		"installed":     conf["installed"],
		"path":          conf["path"],
		"hasPermission": hasPerm,
		"installedAt":   conf["installedAt"],
		"containers":    containers,
		"dockerRunning": dockerRunning,
	})
}
