// network_router_http.go — Endpoint `/api/v4/network/router`.
//
// Contract:
//
//   GET /api/v4/network/router
//
//   → Devuelve status del router (UPnP):
//     - available: si upnpc está instalado.
//     - detected:  si hay router UPnP respondiendo.
//     - local_ip / external_ip si detected.
//     - mappings: lista de port forwardings actuales.
//
// Diseño:
//
//   - Solo GET. No POST/PUT/DELETE — los mappings se gestionan vía
//     `network_ports` (POST /api/v4/network/ports), no aquí. Este
//     endpoint es read-only: "muéstrame qué hay en el router".
//
//   - Si el router no responde, devolvemos 200 con detected=false y
//     un mensaje legible — NO 503. La razón: el router caído no es un
//     error del backend, es un dato útil para el frontend.
//
//   - Wrapped en breaker via el provider — si UPnP cuelga por 10s,
//     devolvemos detected=false en lugar de bloquear el handler.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipos
// ─────────────────────────────────────────────────────────────────────────────

// RouterStatusResponse es la respuesta combinada del endpoint:
// status (de Detect) + mappings (de ListMappings si Detected).
type RouterStatusResponse struct {
	Available  bool                `json:"available"`
	Detected   bool                `json:"detected"`
	LocalIP    string              `json:"local_ip,omitempty"`
	ExternalIP string              `json:"external_ip,omitempty"`
	Desc       string              `json:"description,omitempty"`
	Message    string              `json:"message,omitempty"` // hint legible para el usuario
	Mappings   []RouterPortMapping `json:"mappings"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Routing
// ─────────────────────────────────────────────────────────────────────────────

func handleNetworkRouterRoutes(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v4/network/router" {
		jsonError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	routerGetHTTP(w, r)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/router
// ─────────────────────────────────────────────────────────────────────────────

func routerGetHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRouterProvider == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	// Timeout duro para que el handler no se quede esperando si el
	// router cuelga (algunos routers responden a SSDP pero se cuelgan
	// en RPC). Si el provider tiene su propio timeout, este es un
	// cinturón adicional.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	status, err := networkRouterProvider.Detect(ctx)
	if err != nil {
		// Detect rara vez devuelve error fatal. Si lo hace, devolvemos
		// 503 — algo grave en el cliente UPnP.
		jsonError(w, http.StatusServiceUnavailable,
			"Router detection failed: "+err.Error())
		return
	}

	res := RouterStatusResponse{
		Available:  status.Available,
		Detected:   status.Detected,
		LocalIP:    status.LocalIP,
		ExternalIP: status.ExternalIP,
		Desc:       status.Desc,
		Mappings:   []RouterPortMapping{}, // siempre array, no null
	}

	if !status.Available {
		res.Message = "UPnP client (upnpc) is not installed. Install it with: apt install miniupnpc"
	} else if !status.Detected {
		res.Message = "No UPnP router detected. Verify that UPnP is enabled in your router admin (some ISPs disable it by default)"
	}

	// Si el router responde, intentar listar mappings. Si falla, NO es
	// fatal — devolvemos lo que tengamos.
	if status.Detected {
		mappings, err := networkRouterProvider.ListMappings(ctx)
		if err != nil {
			if errors.Is(err, ErrRouterUnavailable) {
				res.Message = "Router stopped responding while listing port mappings"
			} else if errors.Is(err, ErrRouterTransient) {
				res.Message = "Transient error listing port mappings; try again"
			} else {
				res.Message = "Could not list port mappings: " + err.Error()
			}
		} else {
			res.Mappings = mappings
		}
	}

	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(res)
}
