package main

// ═══════════════════════════════════════════════════════════════════════
// docker_updates.go · Helpers para detección de actualizaciones Docker
// ───────────────────────────────────────────────────────────────────────
// Sprint Updates · 25/05/2026
//
// Funciones para:
//   - Obtener digest LOCAL de una imagen instalada (docker image inspect)
//   - Obtener digest REMOTO desde un registry (docker manifest inspect)
//   - Detectar updates comparando ambos
//
// Estrategia:
//   - Comandos Docker con timeout (5s local, 15s remoto)
//   - Si manifest inspect falla por auth/rate-limit, lo reportamos como
//     status especial · el frontend graceful (oculta botón Actualizar)
//   - NO se descargan imágenes nuevas aquí · solo metadatos
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// localInspectTimeout · `docker image inspect` debería ser instantáneo
	// (es metadata local), pero damos margen por si el daemon Docker está
	// ocupado con otra operación.
	localInspectTimeout = 5 * time.Second

	// remoteInspectTimeout · `docker manifest inspect` hace HTTPS round-trip
	// al registry (Docker Hub, ghcr.io...). 15s cubre redes lentas + DNS.
	remoteInspectTimeout = 15 * time.Second
)

// getLocalImageDigest devuelve el digest sha256 de una imagen tal y como está
// instalada en este sistema. Si la imagen no está, devuelve string vacío.
//
// Ejemplo:
//
//	getLocalImageDigest("jellyfin/jellyfin:latest")
//	→ "sha256:abc123..." (si está) o "" (si no está descargada)
//
// El comando es:
//
//	docker image inspect <image> --format '{{index .RepoDigests 0}}'
//
// El output viene como "image@sha256:abc..." · extraemos la parte tras el @.
func getLocalImageDigest(ctx context.Context, image string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("getLocalImageDigest: image vacío")
	}

	cctx, cancel := context.WithTimeout(ctx, localInspectTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "docker", "image", "inspect", image,
		"--format", "{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}")
	out, err := cmd.Output()
	if err != nil {
		// "No such image" es un caso esperado · imagen no descargada todavía.
		// No es un error técnico · simplemente no hay digest.
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		if strings.Contains(stderr, "No such image") || strings.Contains(stderr, "Error: No such") {
			return "", nil
		}
		return "", fmt.Errorf("docker image inspect %s: %w (stderr: %s)", image, err, stderr)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		// La imagen existe pero no tiene RepoDigests · pasa con imágenes
		// construidas localmente sin push. Sin digest, no hay tracking.
		return "", nil
	}

	// Output esperado: "ghcr.io/immich/server@sha256:abc123..."
	// Queremos solo la parte sha256:abc123...
	idx := strings.Index(raw, "@")
	if idx == -1 {
		// Formato inesperado · log y devolvemos raw como fallback
		logMsg("docker: digest local sin @ separator para %s: %q", image, raw)
		return raw, nil
	}
	return raw[idx+1:], nil
}

// RemoteCheckOutcome encapsula el resultado de comprobar un registry remoto.
// Incluye el status para que el caller (endpoint update-check) decida si
// guardarlo en BD como 'ok', 'unauthorized', 'rate_limited', etc.
type RemoteCheckOutcome struct {
	Digest string // sha256:... si OK, vacío si falló
	Status string // 'ok' | 'rate_limited' | 'unauthorized' | 'unsupported' | 'error'
	Err    error  // detalles del fallo · útil para logs
}

// getRemoteImageDigest consulta el registry remoto para obtener el digest
// actual del tag. NO descarga la imagen · solo metadatos.
//
// Ejemplos:
//
//	getRemoteImageDigest("jellyfin/jellyfin:latest")  → "sha256:..."
//	getRemoteImageDigest("private/repo:v1")           → "", status='unauthorized'
//	getRemoteImageDigest("typo/imagen:latest")        → "", status='unsupported'
//
// IMPORTANTE · qué digest comparamos:
//
// Para imágenes multi-arch (la mayoría hoy día), hay DOS niveles de digests:
//
//  1. INDEX MANIFEST DIGEST · hash del JSON del manifest list completo.
//     Es lo que `docker image inspect --format '{{index .RepoDigests 0}}'`
//     devuelve para el local · es estable entre arquitecturas porque
//     todas las arqs ven el MISMO index del registry.
//
//  2. ARCH-SPECIFIC MANIFEST DIGEST · hash del manifest dentro del index
//     para una arch concreta (amd64, arm64). Este SÍ es distinto por arq.
//
// El comando `docker image inspect` devuelve el INDEX digest (nivel 1).
// El comando `docker manifest inspect` devuelve el INDEX JSON con manifests
// dentro · pero NO directamente el digest del index.
//
// Para obtener el INDEX digest del remoto, calculamos SHA256 del manifest
// JSON raw. Eso es exactamente lo que Docker hace internamente · el "digest
// del manifest" es literalmente sha256(manifest_json).
//
// Necesita que el daemon Docker esté corriendo y que `experimental` esté
// habilitado en config (en Docker moderno está por defecto).
func getRemoteImageDigest(ctx context.Context, image string) RemoteCheckOutcome {
	if image == "" {
		return RemoteCheckOutcome{Status: "error", Err: fmt.Errorf("image vacío")}
	}

	cctx, cancel := context.WithTimeout(ctx, remoteInspectTimeout)
	defer cancel()

	// `docker buildx imagetools inspect --raw` devuelve el manifest JSON
	// raw exactamente como lo sirve el registry · sin re-serialización que
	// alteraría el hash. Es lo único que devuelve el INDEX digest comparable
	// con lo que tenemos local en .RepoDigests.
	//
	// Si buildx no está disponible (Docker viejo), fallback a manifest inspect
	// pero ese tiene la limitación de no devolver el index digest directamente.
	cmd := exec.CommandContext(cctx, "docker", "buildx", "imagetools", "inspect", image, "--raw")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}

		// Clasificar el error
		switch {
		case strings.Contains(stderr, "toomanyrequests") || strings.Contains(stderr, "rate limit"):
			return RemoteCheckOutcome{Status: "rate_limited", Err: fmt.Errorf("registry rate limit: %s", stderr)}
		case strings.Contains(stderr, "unauthorized") || strings.Contains(stderr, "denied") || strings.Contains(stderr, "authentication required"):
			return RemoteCheckOutcome{Status: "unauthorized", Err: fmt.Errorf("registry requires auth: %s", stderr)}
		case strings.Contains(stderr, "not found") || strings.Contains(stderr, "manifest unknown"):
			return RemoteCheckOutcome{Status: "unsupported", Err: fmt.Errorf("image/tag not in registry: %s", stderr)}
		case cctx.Err() == context.DeadlineExceeded:
			return RemoteCheckOutcome{Status: "error", Err: fmt.Errorf("registry timeout (%s)", remoteInspectTimeout)}
		default:
			return RemoteCheckOutcome{Status: "error", Err: fmt.Errorf("docker buildx imagetools inspect %s: %w (stderr: %s)", image, err, stderr)}
		}
	}

	// El INDEX digest es literalmente sha256(manifest_raw_json).
	// Docker calcula el digest así internamente · al hacer pull, guarda
	// .RepoDigests con este mismo hash.
	hash := sha256.Sum256(out)
	digest := "sha256:" + hex.EncodeToString(hash[:])

	return RemoteCheckOutcome{Digest: digest, Status: "ok"}
}

// runtimeArch · helper que mapea runtime.GOARCH a la nomenclatura Docker.
// Mantenido como referencia para futuras necesidades de detección de arch,
// aunque ya no se usa en getRemoteImageDigest (la nueva implementación
// compara el index digest, que es agnóstico a arch).
func runtimeArch() string {
	switch runtime.GOARCH {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "arm":
		return "arm"
	case "386":
		return "386"
	default:
		return runtime.GOARCH
	}
}

// refreshRemoteDigestsForApp consulta el registry para todas las imágenes de
// una app y actualiza la BD con los digests obtenidos. Se llama desde el
// endpoint update-check cuando el TTL del cache ha expirado.
//
// Cada servicio se consulta en paralelo (limit razonable · típicamente 1-5
// imágenes por app, no necesita pool global). Si una falla, se registra su
// status en BD pero las otras siguen.
//
// Devuelve el número de servicios actualizados con éxito.
func refreshRemoteDigestsForApp(ctx context.Context, repo *AppImagesRepo, appID string) (int, error) {
	images, err := repo.GetByApp(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("GetByApp %s: %w", appID, err)
	}
	if len(images) == 0 {
		return 0, nil
	}

	// Canal para recoger resultados
	type result struct {
		serviceName string
		outcome     RemoteCheckOutcome
	}
	results := make(chan result, len(images))

	for _, img := range images {
		go func(img AppImage) {
			outcome := getRemoteImageDigest(ctx, img.Image)
			results <- result{serviceName: img.ServiceName, outcome: outcome}
		}(img)
	}

	updated := 0
	for i := 0; i < len(images); i++ {
		r := <-results
		if r.outcome.Err != nil {
			logMsg("docker: remote check failed for %s/%s: %v (status=%s)", appID, r.serviceName, r.outcome.Err, r.outcome.Status)
		}
		// Aun si falló, persistimos el status (sirve para que el frontend
		// sepa que esa imagen no es comprobable y oculte el botón).
		if err := repo.UpdateRemoteDigest(ctx, appID, r.serviceName, r.outcome.Digest, r.outcome.Status); err != nil {
			logMsg("docker: UpdateRemoteDigest %s/%s: %v", appID, r.serviceName, err)
			continue
		}
		if r.outcome.Status == "ok" {
			updated++
		}
	}

	return updated, nil
}
