// network_probe.go — Acceso a la "realidad" del sistema para el observer.
//
// El NetworkObserver no toca syscalls ni archivos directamente: todo
// pasa por NetworkProbe. Esto permite:
//
//   - Tests sin abrir sockets reales.
//   - Substitución del probe en boot/recovery sin tocar el observer.
//   - Aislamiento de detalles de OS (rutas /proc).
//
// Lo que el probe debe responder:
//
//   - ¿Qué ports HTTP/HTTPS está escuchando el daemon AHORA?
//     (no qué dice la DB que debería escuchar — qué escucha realmente)
//
// Lo que NO hace este probe (deferred / delegado):
//
//   - DDNS IP real (HTTP call al provider) → reconciler DDNS.
//   - TLS / certs → delegado a Caddy (control plane los observa vía
//     network_exposure, no el probe).
//   - UPnP port mapping check → router reconciler.
//   - Capability detection runtime → vive en nimos_capabilities.go.

package main

import (
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipos públicos
// ─────────────────────────────────────────────────────────────────────────────

// ProbedPort describe el estado real de un listener HTTP/HTTPS del
// daemon. ID coincide con el ID en la tabla network_ports ("http" o
// "https").
//
// Listening=false significa que el probe no detectó listener para ese
// ID, sea porque está desactivado o porque crashó.
type ProbedPort struct {
	ID          string // "http" o "https"
	Listening   bool
	Port        int    // puerto real escuchando (0 si !Listening)
	BindAddress string // "0.0.0.0", "127.0.0.1", etc (vacío si !Listening)
}

// ProbeResult es lo que el probe devuelve en una pasada completa.
// El observer compara este snapshot con la DB para detectar divergencias.
type ProbeResult struct {
	ProbedAt time.Time
	Ports    []ProbedPort
}

// ─────────────────────────────────────────────────────────────────────────────
// Interfaz
// ─────────────────────────────────────────────────────────────────────────────

// NetworkProbe abstrae el acceso a la realidad. La implementación real
// vive en este mismo archivo (RealNetworkProbe). Los tests usan un mock.
//
// El método Probe DEBE ser idempotente y rápido — el observer lo llama
// cada N segundos. No abrir conexiones de red ni operaciones largas.
type NetworkProbe interface {
	Probe(portInputs []PortProbeInput) ProbeResult
}

// PortProbeInput le dice al probe qué ports evaluar (típicamente
// 'http' y 'https'). El observer lo rellena desde network_ports.
type PortProbeInput struct {
	ID string // "http" o "https"
}

// ─────────────────────────────────────────────────────────────────────────────
// Implementación real
// ─────────────────────────────────────────────────────────────────────────────

// RealNetworkProbe lee del sistema real. Es stateless — se puede crear
// una instancia compartida.
//
// HTTPListener y HTTPSListener son funciones inyectables que devuelven
// el estado actual del listener. En el daemon real, esto lo expone el
// HTTP server. Si no están configuradas, el probe devuelve
// Listening=false (forzando al observer a marcar drift, lo que es seguro).
type RealNetworkProbe struct {
	clock Clock

	// Inyectables. Si nil, el probe asume "no listening".
	HTTPListener  ListenerStateFn
	HTTPSListener ListenerStateFn
}

// ListenerStateFn devuelve el estado actual de un listener. Lo
// proveerá el HTTP server cuando esté integrado.
type ListenerStateFn func() (port int, bindAddress string, ok bool)

// NewRealNetworkProbe construye un probe real. clock nil → RealClock.
func NewRealNetworkProbe(clock Clock) *RealNetworkProbe {
	if clock == nil {
		clock = NewRealClock()
	}
	return &RealNetworkProbe{clock: clock}
}

// Probe ejecuta una pasada: examina cada port pedido.
func (p *RealNetworkProbe) Probe(portInputs []PortProbeInput) ProbeResult {
	res := ProbeResult{
		ProbedAt: p.clock.Now().UTC(),
		Ports:    make([]ProbedPort, 0, len(portInputs)),
	}
	for _, pi := range portInputs {
		res.Ports = append(res.Ports, p.probePort(pi))
	}
	return res
}

// probePort consulta el listener inyectable para el ID dado.
func (p *RealNetworkProbe) probePort(pi PortProbeInput) ProbedPort {
	out := ProbedPort{ID: pi.ID}
	var fn ListenerStateFn
	switch pi.ID {
	case "http":
		fn = p.HTTPListener
	case "https":
		fn = p.HTTPSListener
	default:
		return out // listening=false
	}
	if fn == nil {
		return out
	}
	port, bind, ok := fn()
	if !ok {
		return out
	}
	out.Listening = true
	out.Port = port
	out.BindAddress = bind
	return out
}
