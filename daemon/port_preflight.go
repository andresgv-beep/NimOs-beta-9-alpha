// port_preflight.go — Preflight de conflictos de puerto fijo (Beta 8.x)
//
// El Port Allocator (port_allocator.go / port_bindings.go) reubica
// automáticamente los puertos host FLOTANTES para evitar colisiones. Pero hay
// puertos que NO se pueden mover sin romper el servicio: los "fijos por
// naturaleza", donde el consumidor espera exactamente ese puerto (DNS :53,
// DHCP :67/:68, NTP :123...). Cambiarlos a un puerto del pool dejaría el
// servicio inalcanzable, así que el allocator los deja intactos.
//
// Consecuencia: dos apps que necesiten EL MISMO puerto fijo (p.ej. Pi-hole y
// AdGuard, ambos en :53) chocan de forma inevitable. Antes, NimOS intentaba el
// deploy igualmente y docker fallaba a medias dejando red/containers/registros
// rotos. Este preflight detecta ese choque ANTES de crear nada, para poder
// cancelar limpio con un mensaje claro.
//
// Nota de eje: el chequeo es GENÉRICO — no busca el puerto 53 en concreto, sino
// cualquier binding que el allocator no pudo reubicar y que otra app ya tiene.
// Así cubre el :53 y cualquier choque de puerto fijo futuro sin listas extra.
//
// Todo aquí es PURO (sin I/O ni http) → testeable de forma directa. La capa http
// (docker_stacks.go) solo invoca y responde.

package main

import "fmt"

// PortConflict describe un puerto host que la app intenta usar pero que ya está
// ocupado por OTRA app y que el allocator no pudo reubicar.
type PortConflict struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	HeldBy   string `json:"held_by"`
}

// occupiedHostPortsBy mapea cada puerto host ocupado al ID de la app que lo
// retiene. Gemela de occupiedHostPorts (port_reserved.go) pero conservando el
// dueño, para poder explicar al usuario qué app bloquea un puerto fijo.
//
// `apps` debe excluir a la app que se está desplegando (igual que para
// occupiedHostPorts), para no marcar su propio puerto previo como conflicto.
// Si dos apps declarasen el mismo host (estado inconsistente que no debería
// ocurrir con el allocator), gana la primera; el preflight lo bloquea igual.
func occupiedHostPortsBy(apps []*DBDockerApp) map[int]string {
	owner := make(map[int]string)
	for _, a := range apps {
		if a == nil {
			continue
		}
		for _, b := range a.parsedPorts() {
			if b.Host > 0 {
				if _, exists := owner[b.Host]; !exists {
					owner[b.Host] = a.ID
				}
			}
		}
	}
	return owner
}

// detectFixedConflicts devuelve los puertos host de `bindings` que SIGUEN
// cayendo sobre un puerto ocupado por otra app (occupiedBy: puerto host → appID),
// deduplicados por puerto (53/tcp + 53/udp → un único conflicto).
//
// Se llama DESPUÉS del allocator: como éste ya reubicó todo lo movible, cualquier
// binding que todavía aterrice sobre un puerto ocupado es un puerto que no se
// pudo mover (fijo por naturaleza, o pool agotado) → conflicto real e inevitable.
//
// Resultado vacío = seguro para desplegar.
func detectFixedConflicts(bindings []PortBinding, occupiedBy map[int]string) []PortConflict {
	if len(bindings) == 0 || len(occupiedBy) == 0 {
		return nil
	}
	var conflicts []PortConflict
	seen := make(map[int]bool) // un conflicto por puerto host (no duplicar tcp+udp)
	for _, b := range bindings {
		if b.Host <= 0 || seen[b.Host] {
			continue
		}
		if holder, taken := occupiedBy[b.Host]; taken {
			seen[b.Host] = true
			conflicts = append(conflicts, PortConflict{
				Port:     b.Host,
				Protocol: b.Protocol,
				HeldBy:   holder,
			})
		}
	}
	return conflicts
}

// portConflictMessage compone un mensaje legible para el usuario a partir de los
// conflictos detectados. Pensado para el primer (y casi siempre único) choque.
func portConflictMessage(conflicts []PortConflict) string {
	if len(conflicts) == 0 {
		return ""
	}
	c := conflicts[0]
	return fmt.Sprintf(
		"El puerto %d ya está en uso por la app «%s». Solo una app puede usar ese puerto a la vez.",
		c.Port, c.HeldBy)
}
