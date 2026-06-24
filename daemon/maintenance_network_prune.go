// maintenance_network_prune.go — Tarea: poda de redes Docker huérfanas.
//
// Backlog GC (item "orphan app/network GC") + raíz del error
// "all predefined address pools have been fully subnetted": cada stack de
// compose crea una red `<app>_default` que consume una subred del pool de
// Docker. Un install fallido (o una app borrada a lo bruto) puede dejar la red
// sin contenedores → huérfana, ocupando subred. Cuando se acumulan, el pool se
// agota y no se pueden crear más redes (ni instalar más apps).
//
// `docker network prune -f` es SEGURO por construcción:
//   - Docker NUNCA borra una red con ≥1 container conectado (vivo o parado).
//   - NUNCA borra las redes built-in `bridge` / `host` / `none`.
//   → solo cae lo realmente huérfano (sin ningún container).
//
// Hermana de docker_image_prune (maintenance_image_prune.go). Misma forma, pero
// las redes no liberan disco de forma relevante → BytesFreed = 0; la métrica
// útil es cuántas redes se recuperaron (= subredes liberadas).
//
// Cumple el contrato de mantenimiento:
//   1. refuse-if-uncertain → si Docker no está instalado, SE SALTA (no error).
//   2. skip-known          → `prune` jamás toca redes en uso ni built-in.
//   3. grace-period        → no aplica (Docker ya protege lo en uso).
//   4. log-everything      → registra cuántas redes huérfanas borró.

package main

import (
	"context"
	"fmt"
	"strings"
)

type dockerNetworkPruneTask struct{}

func (t *dockerNetworkPruneTask) ID() string       { return "docker_network_prune" }
func (t *dockerNetworkPruneTask) Name() string     { return "Limpieza de redes Docker huérfanas" }
func (t *dockerNetworkPruneTask) Category() string { return MaintCategoryDocker }
func (t *dockerNetworkPruneTask) Description() string {
	return "Borra redes Docker huérfanas (sin ningún contenedor conectado, restos de instalaciones fallidas o apps eliminadas). Cada red ocupa una subred del pool de Docker; recuperarlas evita el error 'all predefined address pools have been fully subnetted' al instalar más apps. No toca redes en uso ni las del sistema (bridge/host/none)."
}

func (t *dockerNetworkPruneTask) DefaultSchedule() Schedule {
	// Semanal, como la poda de imágenes. Las redes huérfanas se acumulan con
	// installs fallidos / desinstalaciones; reclamar subredes periódicamente
	// mantiene sano el pool. El borrado es seguro (Docker protege lo en uso).
	// El usuario puede cambiar el schedule.
	return Schedule{Kind: ScheduleWeekly, AtWeekday: 0, AtHour: 4, AtMinute: 30}
}

func (t *dockerNetworkPruneTask) Run(ctx context.Context) MaintenanceResult {
	// 1. refuse-if-uncertain · si Docker no está, no es fallo: no aplica.
	if !isDockerInstalledGo() {
		return MaintenanceResult{Skipped: true, SkipReason: "Docker no instalado"}
	}

	// 2/3. solo-huérfanas · Docker no borra redes en uso ni built-in.
	out, ok := runSafe("docker", "network", "prune", "-f")
	if !ok {
		return MaintenanceResult{Err: fmt.Errorf("docker network prune falló")}
	}

	removed := parseDockerNetworkPruneOutput(out)

	// 4. log-everything.
	logMsg("maintenance: docker_network_prune · %d red(es) huérfana(s) borrada(s) (subredes liberadas)", removed)

	return MaintenanceResult{ItemsRemoved: int64(removed), BytesFreed: 0}
}

// ── Lógica pura (testeable) ──────────────────────────────────────────────────

// parseDockerNetworkPruneOutput cuenta las redes borradas en la salida de
// `docker network prune -f`. PURA.
//
// Formato típico:
//
//	Deleted Networks:
//	mealie_default
//	lidarr_default
//
// Si no se borró nada, Docker no imprime la cabecera (salida vacía) → 0.
func parseDockerNetworkPruneOutput(out string) (removed int) {
	seenHeader := false
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "Deleted Networks:") {
			seenHeader = true
			continue
		}
		if seenHeader {
			// Cada línea no vacía tras la cabecera es el nombre de una red.
			removed++
		}
	}
	return removed
}
