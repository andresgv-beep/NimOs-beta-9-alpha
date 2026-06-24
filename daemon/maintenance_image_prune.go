// maintenance_image_prune.go — Tarea: poda de imágenes Docker huérfanas (dangling).
//
// Item 7 del backlog AppStore (GC). Borra SOLO imágenes "dangling" (sin tag,
// `<none>:<none>`) que se acumulan al actualizar/reconstruir apps: al bajar una
// versión nueva, la imagen vieja se queda sin tag pero ocupa disco.
//
// `docker image prune -f` (SIN `-a`) es seguro por construcción:
//   - Docker NUNCA borra una imagen en uso por un container (ni parado).
//   - NUNCA borra imágenes ETIQUETADAS (las que referencia una app instalada).
//   → solo restos sin tag que no referencia nadie.
//
// Las imágenes etiquetadas-sin-uso (riesgo: bases compartidas entre apps) NO se
// tocan aquí · es un nivel aparte, más delicado, fuera del alcance de esta tarea.
//
// Cumple el contrato de mantenimiento:
//   1. refuse-if-uncertain → si Docker no está instalado, SE SALTA (no error).
//   2. skip-known          → `prune` (sin -a) jamás toca imágenes en uso/tag.
//   3. grace-period        → no aplica (Docker ya protege lo en uso).
//   4. log-everything      → registra cuántas imágenes y cuánto disco liberó.

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type dockerImagePruneTask struct{}

func (t *dockerImagePruneTask) ID() string   { return "docker_image_prune" }
func (t *dockerImagePruneTask) Name() string { return "Limpieza de imágenes Docker huérfanas" }
func (t *dockerImagePruneTask) Category() string { return MaintCategoryDocker }
func (t *dockerImagePruneTask) Description() string {
	return "Borra imágenes Docker 'dangling' (sin etiqueta, restos de actualizaciones de apps) que no usa ningún container. Recupera disco. No toca imágenes etiquetadas ni en uso."
}

func (t *dockerImagePruneTask) DefaultSchedule() Schedule {
	// Semanal: las imágenes dangling se acumulan con las actualizaciones, así
	// que reclamar disco periódicamente es justo el valor de la tarea. El
	// borrado es seguro (Docker protege lo en uso/etiquetado), a diferencia del
	// sweep de directorios. El usuario puede cambiar el schedule.
	return Schedule{Kind: ScheduleWeekly, AtWeekday: 0, AtHour: 4, AtMinute: 0}
}

func (t *dockerImagePruneTask) Run(ctx context.Context) MaintenanceResult {
	// 1. refuse-if-uncertain · si Docker no está, no es fallo: no aplica.
	if !isDockerInstalledGo() {
		return MaintenanceResult{Skipped: true, SkipReason: "Docker no instalado"}
	}

	// 2/3. dangling-only · Docker no borra imágenes en uso ni etiquetadas.
	out, ok := runSafe("docker", "image", "prune", "-f")
	if !ok {
		return MaintenanceResult{Err: fmt.Errorf("docker image prune falló")}
	}

	deleted, bytesFreed := parseDockerPruneOutput(out)

	// 4. log-everything.
	logMsg("maintenance: docker_image_prune · %d imagen(es) dangling borrada(s), %d bytes liberados", deleted, bytesFreed)

	return MaintenanceResult{ItemsRemoved: int64(deleted), BytesFreed: bytesFreed}
}

// ── Lógica pura (testeable) ──────────────────────────────────────────────────

// parseDockerPruneOutput extrae (nº de imágenes borradas, bytes liberados) de la
// salida de `docker image prune -f`. PURA.
//
// Formato típico:
//
//	Deleted Images:
//	deleted: sha256:aaaa...
//	deleted: sha256:bbbb...
//	untagged: foo:latest
//
//	Total reclaimed space: 1.234GB
func parseDockerPruneOutput(out string) (deleted int, bytesFreed int64) {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "deleted:"):
			deleted++
		case strings.HasPrefix(l, "Total reclaimed space:"):
			size := strings.TrimSpace(strings.TrimPrefix(l, "Total reclaimed space:"))
			bytesFreed = parseDockerSize(size)
		}
	}
	return deleted, bytesFreed
}

// parseDockerSize convierte un tamaño legible de Docker ("1.234GB", "500MB",
// "0B") a bytes. Docker usa unidades SI decimales (1kB = 1000 B). PURA.
// Devuelve 0 si no se puede parsear.
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(strings.TrimSpace(s[:i]), 64)
	if err != nil {
		return 0
	}
	unit := strings.TrimSpace(s[i:])
	mult := map[string]float64{
		"B":  1,
		"kB": 1e3, "KB": 1e3,
		"MB": 1e6,
		"GB": 1e9,
		"TB": 1e12,
		"PB": 1e15,
	}
	m, ok := mult[unit]
	if !ok {
		return 0
	}
	return int64(num * m)
}
