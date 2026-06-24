package main

// port_reserved.go — Fase 1 del Port Allocator (PORT-ALLOCATOR-DESIGN v1.1).
//
// Funciones PURAS: no tocan DB ni red. Definen los puertos reservados de NimOS
// (duros y blandos), construyen el mapa de ocupación a partir del PortsJSON
// canónico de las apps instaladas, y exponen el predicado base isPortFree.
//
// El wiring que carga los puertos de Caddy (NetworkRepo.GetExposureConfig) y la
// lista de apps instaladas se conecta más adelante (Fase 4). Aquí todo entra por
// argumento para que sea testeable sin estado.

const (
	// Reservados DUROS estáticos · NimOS los posee, nunca disponibles
	// (ni flotante ni fijo).
	portDaemonHTTP     = 5000 // http.go:20  · servidor HTTP del daemon NimOS
	portNimTorrent     = 9091 // apps.go:86  · nimos-torrentd / NimTorrent (web)
	portNimTorrentPeer = 6881 // nimos-torrentd · puerto peer BitTorrent (default libtorrent, confirmado por ss)
	portCaddyAdmin     = 2019 // caddy admin API (caddy_admin_url default :2019)

	// Pool flotante para reasignación de puertos en conflicto (decisión v1.1).
	floatPoolMin = 30000
	floatPoolMax = 59999
)

// reservedHard devuelve el conjunto de puertos reservados DUROS: los nativos de
// NimOS (estáticos) más los de Caddy (dinámicos). Un puerto duro NUNCA se asigna,
// ni a flotantes ni a fijos.
//
// caddyHTTP / caddyHTTPS provienen de NetworkRepo.GetExposureConfig (default
// 80 / 443; en producción 444). Se pasan como argumento para mantener la función
// pura. Valores <= 0 se ignoran (no contaminan el set).
func reservedHard(caddyHTTP, caddyHTTPS int) map[int]bool {
	set := map[int]bool{
		portDaemonHTTP:     true,
		portNimTorrent:     true,
		portNimTorrentPeer: true,
		portCaddyAdmin:     true,
	}
	if caddyHTTP > 0 {
		set[caddyHTTP] = true
	}
	if caddyHTTPS > 0 {
		set[caddyHTTPS] = true
	}
	return set
}

// reservedSoft devuelve los puertos well-known del sistema. NO se auto-asignan a
// puertos FLOTANTES, pero una app que los pida explícitamente como FIJO puede
// reclamarlos si el host los tiene libres (ej. Pi-hole / AdGuard en :53). La
// política de reclamación blanda vive en el allocator (Fase 2); aquí solo se
// declara el conjunto. Ver §3.2 del diseño.
func reservedSoft() map[int]bool {
	return map[int]bool{
		22:  true, // SSH
		53:  true, // DNS
		67:  true, // DHCP (server)
		68:  true, // DHCP (client)
		123: true, // NTP
	}
}

// occupiedHostPorts construye el conjunto de puertos HOST en uso por las apps
// instaladas, leyendo el PortsJSON canónico de cada una (única fuente de verdad,
// §4). Recorre todos los bindings (multi-puerto, tcp y udp) y toma el Host.
//
// Es la base del mapa de ocupación. Coste O(apps × bindings): microsegundos
// incluso con cientos de apps — por eso NO hace falta tabla port_allocations.
// Apps nil se ignoran; Host <= 0 se ignora.
func occupiedHostPorts(apps []*DBDockerApp) map[int]bool {
	set := make(map[int]bool)
	for _, a := range apps {
		if a == nil {
			continue
		}
		for _, b := range a.parsedPorts() {
			if b.Host > 0 {
				set[b.Host] = true
			}
		}
	}
	return set
}

// isPortFree es el predicado BASE: un puerto está libre si está en rango válido,
// no lo ocupa otra app, y no es reservado-duro. Los reservados BLANDOS NO se
// comprueban aquí a propósito: un puerto blando puede estar "free" para un fijo
// que lo reclame; esa política se aplica en el allocator (Fase 2).
func isPortFree(port int, occupied, hard map[int]bool) bool {
	if port < 1 || port > 65535 {
		return false
	}
	if occupied[port] {
		return false
	}
	if hard[port] {
		return false
	}
	return true
}

// inFloatPool indica si un puerto cae dentro del pool flotante de NimOS
// (30000–59999). Lo usará el allocator (Fase 2) para recorrer el pool.
func inFloatPool(port int) bool {
	return port >= floatPoolMin && port <= floatPoolMax
}
