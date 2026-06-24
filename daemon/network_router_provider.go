// network_router_provider.go — Interfaz RouterProvider para gestión
// de port forwarding en el router.
//
// F-007 entrega:
//   - Interfaz RouterProvider con UPnP como única implementación inicial.
//   - UPnPRouterProvider via `upnpc` CLI (network_router_upnp.go).
//   - Reconciler que sincroniza network_ports → mappings UPnP.
//
// Diseño:
//
//   - El RouterProvider devuelve si el router responde (Detect) y
//     gestiona mappings individuales (List/Add/Remove).
//
//   - Provider concreto puede no estar disponible (sin `upnpc` instalado,
//     sin router UPnP en la red, etc.). Esto NO es un error fatal:
//     el reconciler emite warn y sigue. NimOS funciona aunque el router
//     no coopere.
//
//   - Errores se clasifican: ErrRouterUnavailable (router no responde),
//     ErrRouterConflict (port ya mapeado por otro), ErrRouterTransient
//     (red caída, retry).

package main

import (
	"context"
	"errors"
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors sentinels
// ─────────────────────────────────────────────────────────────────────────────

// ErrRouterUnavailable indica que el router no es alcanzable o no soporta
// el protocolo (UPnP no habilitado, no hay router con UPnP en la red).
// El reconciler emite warn pero sigue funcionando — los puertos NO se
// mapean automáticamente.
var ErrRouterUnavailable = errors.New("router unavailable (no UPnP response)")

// ErrRouterConflict indica que el mapping no se pudo crear porque otro
// dispositivo tiene reservado ese puerto en el router. El usuario debe
// resolverlo manualmente (apagar otro dispositivo, cambiar puerto en NimOS).
var ErrRouterConflict = errors.New("port mapping conflict on router")

// ErrRouterTransient marca fallos recuperables. El breaker los cuenta.
var ErrRouterTransient = errors.New("router operation transient failure")

// ─────────────────────────────────────────────────────────────────────────────
// Tipos
// ─────────────────────────────────────────────────────────────────────────────

// RouterStatus es la información que devuelve el router cuando responde.
//
// Available: el cliente (e.g. `upnpc`) está instalado y configurado.
// Detected:  un router UPnP fue encontrado y respondió.
// LocalIP / ExternalIP: lo que el router reporta sobre la red.
//
// Si Available=false, los demás campos son irrelevantes.
// Si Available=true pero Detected=false, hay cliente pero no router.
type RouterStatus struct {
	Available  bool   `json:"available"`
	Detected   bool   `json:"detected"`
	LocalIP    string `json:"local_ip,omitempty"`
	ExternalIP string `json:"external_ip,omitempty"`
	Desc       string `json:"description,omitempty"`
	Raw        string `json:"raw,omitempty"` // output crudo para debug
}

// RouterPortMapping representa una regla de port forwarding existente
// en el router.
type RouterPortMapping struct {
	Protocol     string `json:"protocol"`      // "TCP" | "UDP"
	ExternalPort int    `json:"external_port"`
	InternalIP   string `json:"internal_ip"`
	InternalPort int    `json:"internal_port"`
	Description  string `json:"description,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// RouterProvider
// ─────────────────────────────────────────────────────────────────────────────

// RouterProvider abstrae la gestión del router. La única implementación
// inicial es UPnP, pero la interfaz permite añadir otros (e.g. MikroTik
// API, OPNsense, etc.) cuando aparezca necesidad real.
type RouterProvider interface {
	// Name devuelve el identificador estable ("upnp", futuramente "mikrotik", etc).
	Name() string

	// Detect comprueba si el router está disponible y devuelve su status.
	// NUNCA devuelve error fatal — si el router no responde, devuelve
	// un RouterStatus con Detected=false. Solo devuelve error para
	// problemas inesperados del cliente (e.g. binario corrupto).
	Detect(ctx context.Context) (*RouterStatus, error)

	// ListMappings devuelve las reglas de port forwarding actuales.
	// Si el router no responde, devuelve ErrRouterUnavailable.
	ListMappings(ctx context.Context) ([]RouterPortMapping, error)

	// AddMapping añade una regla. Idempotente: si ya existe con los
	// mismos parámetros, no error. Si hay conflicto (otro mapping en
	// ese puerto), devuelve ErrRouterConflict.
	AddMapping(ctx context.Context, m RouterPortMapping) error

	// RemoveMapping borra una regla por (protocol, externalPort).
	// Idempotente: si no existe, no error.
	RemoveMapping(ctx context.Context, protocol string, externalPort int) error
}
