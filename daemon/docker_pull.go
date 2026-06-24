// docker_pull.go — Docker pull async (Beta 8.1)
//
// Endpoint:
//   POST /api/docker/pull/<image>[?async=true]
//
// El pull se ejecuta como operación async (operationsRepo) reportando
// progreso cuando se solicita con ?async=true. Útil para imágenes grandes
// (300MB-2GB) donde queremos mostrar barra de progreso real al usuario.
//
// Sin ?async=true funciona síncrono · backward compat con clientes antiguos.

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

func dockerPull(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	if !hasDockerPermission(session) {
		jsonError(w, 403, "No permission")
		return
	}
	rawImage := strings.TrimPrefix(r.URL.Path, "/api/docker/pull/")
	decoded, _ := url.PathUnescape(rawImage)
	image := sanitizeDockerNameGo(decoded)
	if image == "" || image != decoded {
		jsonError(w, 400, "Invalid image name")
		return
	}

	if isAsyncRequested(r) {
		// Async path · APP-053
		op, err := operationsRepo.Create(r.Context(), "docker.pull", session.Username)
		if err != nil {
			jsonError(w, 500, "Failed to create operation: "+err.Error())
			return
		}
		runWorkerAsync(op.ID, func(ctx context.Context) (map[string]interface{}, error) {
			return runDockerPullWork(ctx, image, op.ID)
		})
		writeAsyncAccepted(w, op)
		return
	}

	// Sync path (legacy)
	result, err := runDockerPullWork(r.Context(), image, "")
	if err != nil {
		writeWorkerError(w, err)
		return
	}
	jsonOk(w, result)
}

// runDockerPullWork · trabajo real de `docker pull <image>`.
//
// Función pura sin acceso a HTTP. Si opID != "" reporta progreso a
// operationsRepo. Como docker pull es una sola operación de duración variable
// (10s-2min) y no expone progreso real, solo reporta start (5%) y end (100%).
//
// Returns:
//   - map: {"ok": true, "image": image}
//   - error: *httpStatusError(500) si docker pull falla
func runDockerPullWork(ctx context.Context, image, opID string) (map[string]interface{}, error) {
	updateOpProgressSafe(ctx, opID, 5, "Pulling image "+image)

	if _, ok := runSafe("docker", "pull", image); !ok {
		return nil, asHTTPError(500, "Failed to pull image")
	}

	return map[string]interface{}{"ok": true, "image": image}, nil
}
