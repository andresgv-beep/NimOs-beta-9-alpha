package main

// ═══════════════════════════════════════════════════════════════════════
// docker_compose_services.go · Inspección de servicios en docker-compose
// ───────────────────────────────────────────────────────────────────────
// Sprint Updates · 25/05/2026
//
// Para poblar docker_app_images tras un compose up -d, necesitamos saber
// qué servicios e imágenes hay declarados en el compose file.
//
// En lugar de parsear YAML a mano (libs externas, edge cases, escapes),
// usamos `docker compose config --format json` que normaliza el compose
// y devuelve JSON estructurado · más robusto y oficial.
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// composeConfigResult representa la salida JSON de `docker compose config`.
// Solo extraemos lo que necesitamos: nombres de servicio + imagen.
type composeConfigResult struct {
	Services map[string]struct {
		Image string `json:"image"`
	} `json:"services"`
}

// ComposeService representa un servicio declarado en docker-compose con su
// imagen asociada. Es la unidad básica que pasamos a docker_app_images.
type ComposeService struct {
	Name  string // nombre del servicio (clave en compose: "redis", "immich-server")
	Image string // imagen Docker (incluye tag: "ghcr.io/.../foo:latest")
}

// getComposeServices ejecuta `docker compose -f <path> config --format json`
// y devuelve la lista de servicios con sus imágenes.
//
// El comando expande variables, valida sintaxis y normaliza el formato ·
// más robusto que parsear el YAML manualmente.
//
// Timeout 10s · `compose config` es local, no hace network. Si tarda más,
// algo va mal (corruption, daemon Docker bloqueado).
func getComposeServices(ctx context.Context, composePath, stackDir string) ([]ComposeService, error) {
	if composePath == "" {
		return nil, fmt.Errorf("getComposeServices: composePath vacío")
	}

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "docker", "compose", "-f", composePath, "config", "--format", "json")
	// Importante: usar stackDir como cwd para que `compose config` resuelva
	// .env files correctamente (el .env se busca en el directorio del compose).
	if stackDir != "" {
		cmd.Dir = stackDir
	}

	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("docker compose config %s: %w (stderr: %s)", composePath, err, stderr)
	}

	var result composeConfigResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse compose config JSON: %w", err)
	}

	var services []ComposeService
	for name, svc := range result.Services {
		if svc.Image == "" {
			// Servicios construidos con `build:` en lugar de `image:` no tienen
			// digest tracking (no provienen de un registry). Los saltamos.
			continue
		}
		services = append(services, ComposeService{
			Name:  name,
			Image: svc.Image,
		})
	}

	return services, nil
}

// populateAppImagesAfterDeploy se llama tras un `compose up -d` exitoso para
// inyectar las rows iniciales en docker_app_images.
//
// Para cada servicio del compose:
//  1. Extrae imagen del compose
//  2. Obtiene digest local con docker image inspect
//  3. Hace UPSERT en docker_app_images con local == remote
//     (acabamos de descargar lo último, asumimos al día)
//
// No es bloqueante: si algo falla para un servicio individual, se logea
// pero no aborta el deploy. La tabla puede actualizarse después con
// refreshRemoteDigestsForApp.
func populateAppImagesAfterDeploy(ctx context.Context, appID, composePath, stackDir string) {
	if appImagesRepo == nil {
		logMsg("docker: populateAppImagesAfterDeploy(%s) skipped · appImagesRepo nil", appID)
		return
	}

	services, err := getComposeServices(ctx, composePath, stackDir)
	if err != nil {
		logMsg("docker: getComposeServices(%s) failed: %v", appID, err)
		return
	}

	if len(services) == 0 {
		logMsg("docker: populateAppImagesAfterDeploy(%s) · no services with image:", appID)
		return
	}

	for _, svc := range services {
		digest, err := getLocalImageDigest(ctx, svc.Image)
		if err != nil {
			logMsg("docker: getLocalImageDigest(%s/%s/%s) failed: %v", appID, svc.Name, svc.Image, err)
			// Intentamos persistir con digest vacío · al menos guardamos el
			// hecho de que el servicio existe. Update-check intentará refrescar
			// digest remoto y eventualmente compararlo.
		}
		if err := appImagesRepo.UpsertLocalDigest(ctx, appID, svc.Name, svc.Image, digest); err != nil {
			logMsg("docker: UpsertLocalDigest(%s/%s) failed: %v", appID, svc.Name, err)
		}
	}
}

// populateAppImagesForContainer es la variante simple para apps single-container
// (dockerInstall, no stack). El servicio es el propio container, una sola fila.
func populateAppImagesForContainer(ctx context.Context, appID, image string) {
	if appImagesRepo == nil {
		logMsg("docker: populateAppImagesForContainer(%s) skipped · appImagesRepo nil", appID)
		return
	}
	if image == "" {
		return
	}
	digest, err := getLocalImageDigest(ctx, image)
	if err != nil {
		logMsg("docker: getLocalImageDigest(%s/%s) failed: %v", appID, image, err)
	}
	if err := appImagesRepo.UpsertLocalDigest(ctx, appID, appID, image, digest); err != nil {
		logMsg("docker: UpsertLocalDigest(%s) failed: %v", appID, err)
	}
}
