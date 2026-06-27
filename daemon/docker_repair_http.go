// docker_repair_http.go — Reparación de contenedores rotos.
//
// Cuando un disco del pool se cae a media operación (o tras un susto de
// almacenamiento), Docker puede PERDER la capa/imagen de un contenedor: el
// contenedor existe pero no arranca ("RWLayer is unexpectedly nil") y su imagen
// ya no está en `docker images`. La CONFIG de la app (bind-mounts en el pool)
// SOBREVIVE. Estos endpoints distinguen ese estado y lo arreglan:
//
//	GET  /api/docker/app/<id>/broken  · ¿el contenedor está ROTO (no solo parado)?
//	POST /api/docker/app/<id>/repair  · recrear: compose down + up -d --pull always --force-recreate
//
// "Roto" ≠ "parado": un parado se arranca con start; uno roto necesita re-pull +
// recreate porque le falta la capa/imagen. Por eso la UI ofrece "Reparar
// contenedor" en vez de "Abrir" deshabilitado.

package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// appComposePath resuelve el docker-compose.yml de una app stack. "" si no hay
// Docker configurado o el id es inválido.
func appComposePath(appID string) string {
	safeID := sanitizeDockerNameGo(appID)
	if safeID == "" {
		return ""
	}
	conf := getDockerConfigGo()
	dockerPath, _ := conf["path"].(string)
	if dockerPath == "" {
		dp, err := getDockerPath()
		if err != nil {
			return ""
		}
		dockerPath = dp
	}
	return filepath.Join(dockerPath, "stacks", safeID, "docker-compose.yml")
}

// imagePresentLocally indica si una imagen (por ID sha256 o por ref/tag) está
// presente en el almacén local de Docker.
func imagePresentLocally(imageRef string) bool {
	if imageRef == "" {
		return false
	}
	out, ok := runSafe("docker", "image", "inspect", "--format", "{{.Id}}", imageRef)
	return ok && strings.TrimSpace(out) != ""
}

// appBrokenStatus inspecciona los contenedores del stack de una app y decide si
// alguno está ROTO (no solo parado). Roto = NO corriendo Y (State.Error != ""
// —p.ej. "RWLayer is unexpectedly nil"— O su imagen ya no existe localmente).
// Devuelve (broken, reason).
func appBrokenStatus(appID string) (bool, string) {
	safeID := sanitizeDockerNameGo(appID)
	if safeID == "" {
		return false, ""
	}
	// Contenedores del stack vía la label de compose-project (cubre stacks
	// multi-servicio). Fallback al nombre = appID para deploys de un contenedor.
	names := []string{}
	if out, ok := runSafe("docker", "ps", "-a",
		"--filter", "label=com.docker.compose.project="+safeID,
		"--format", "{{.Names}}"); ok {
		for _, n := range strings.Split(strings.TrimSpace(out), "\n") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		names = []string{safeID}
	}

	for _, name := range names {
		insp, ok := runSafe("docker", "inspect", "-f",
			"{{.State.Status}}|{{.State.Error}}|{{.Image}}|{{.Config.Image}}", name)
		if !ok {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(insp), "|", 4)
		if len(parts) < 4 {
			continue
		}
		status, stateErr, imageID, configImage := parts[0], parts[1], parts[2], parts[3]
		if status == "running" {
			continue
		}
		if stateErr != "" {
			return true, "El contenedor no puede arrancar: " + stateErr
		}
		// No corriendo y su imagen ya no está → necesita re-pull + recreate.
		if !imagePresentLocally(imageID) && !imagePresentLocally(configImage) {
			return true, "La imagen del contenedor se ha perdido (se recreará al reparar)"
		}
	}
	return false, ""
}

// dockerAppBroken · GET /api/docker/app/<id>/broken
func dockerAppBroken(w http.ResponseWriter, r *http.Request, appID string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}
	broken, reason := appBrokenStatus(appID)
	jsonOk(w, map[string]interface{}{"ok": true, "broken": broken, "reason": reason})
}

// dockerAppRepair · POST /api/docker/app/<id>/repair
//
// Recrea el stack: `down` (limpia los contenedores rotos, conserva config y
// volúmenes) + `up -d --pull always --force-recreate` (re-pull de la imagen
// perdida + recrea reusando la config bind-montada en el pool).
//
// Solo apps tipo 'stack' (con docker-compose.yml). Síncrono · puede tardar
// minutos por la descarga de imagen.
func dockerAppRepair(w http.ResponseWriter, r *http.Request, appID string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission to manage Docker")
		return
	}

	composePath := appComposePath(appID)
	if composePath == "" {
		jsonError(w, 400, "Docker not configured or invalid app id")
		return
	}
	if _, err := os.Stat(composePath); err != nil {
		jsonError(w, 404, "No compose file for app (solo las apps tipo stack se pueden reparar)")
		return
	}
	stackPath := filepath.Dir(composePath)

	// 1. down · quita el/los contenedor(es) roto(s). SIN -v: conserva los
	// volúmenes; la config bind-montada vive en el pool y no se toca.
	// --remove-orphans limpia restos. Best-effort: si el contenedor está tan roto
	// que `down` falla, seguimos al `up` (--force-recreate intentará reemplazarlo).
	downCtx, cancel := context.WithTimeout(commitContext(), 5*time.Minute)
	defer cancel()
	downCmd := exec.CommandContext(downCtx, "docker", "compose", "-f", composePath, "down", "--remove-orphans")
	downCmd.Dir = stackPath
	if out, err := downCmd.CombinedOutput(); err != nil {
		logMsg("docker: repair down (best-effort) warning para %s: %v (output: %s)", appID, err, string(out))
	}

	// 2. up -d --pull always --force-recreate · re-pull de la imagen perdida y
	// recrea el contenedor. Timeout 15 min (imágenes grandes / red doméstica).
	// commitContext(): no matar el subprocess si el cliente se desconecta.
	upCtx, cancel2 := context.WithTimeout(commitContext(), 15*time.Minute)
	defer cancel2()
	upCmd := exec.CommandContext(upCtx, "docker", "compose", "-f", composePath, "up", "-d", "--pull", "always", "--force-recreate")
	upCmd.Dir = stackPath
	if out, err := upCmd.CombinedOutput(); err != nil {
		logMsg("docker: repair up -d falló para %s: %v (output: %s)", appID, err, string(out))
		jsonError(w, 500, "Repair failed: "+string(out))
		return
	}

	// Invalidación inmediata de la caché de estado (igual que deploy/update): sin
	// esto, /api/services no refleja el contenedor recreado hasta el siguiente
	// tick (≤30s) y la UI deja "Abrir" bloqueado hasta salir y volver a entrar.
	// commitContext() · no atar el refresco a la conexión del cliente.
	ForceDockerCacheRefresh(commitContext())

	logMsg("docker: app %s reparada (recreada desde compose)", appID)
	addNotification("info", "system", "Contenedor reparado",
		"Se recreó el contenedor de "+appID+" reusando su configuración.")
	jsonOk(w, map[string]interface{}{"ok": true, "appId": appID})
}
