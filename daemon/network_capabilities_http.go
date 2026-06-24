// network_capabilities_http.go — Handlers HTTP para SystemCapabilities.
//
// Endpoint contract:
//
//   GET  /api/v4/network/capabilities          — devuelve cache (refresh lazy si > 1h)
//   POST /api/v4/network/capabilities/refresh  — fuerza refresh ahora
//
// Diseño:
//
//   - El store ya gestiona el TTL interno: GET(1h) devuelve cached si
//     no expiró, redetecta si sí. El handler NO duplica esa lógica.
//
//   - El refresh fuerza re-detección (puede tardar segundos si hay que
//     hacer LookPath de N binaries + ejecutar `certbot --version`).
//     Admin-only por eso.
//
//   - Tanto GET como POST devuelven el mismo formato — el frontend no
//     diferencia entre cached y fresh, solo recibe el snapshot actual.

package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// MaxCapabilitiesAge es la edad máxima admitida en GET antes de
// refrescar automáticamente. Coincide con el plan v4 ("refresh lazy
// si > 1h").
const MaxCapabilitiesAge = time.Hour

// handleNetworkCapabilitiesRoutes despachador para /api/v4/network/capabilities[/refresh].
func handleNetworkCapabilitiesRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	switch {
	case path == "/api/v4/network/capabilities":
		if method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		capabilitiesGetHTTP(w, r)
	case path == "/api/v4/network/capabilities/refresh":
		if method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		capabilitiesRefreshHTTP(w, r)
	default:
		jsonError(w, http.StatusNotFound, "Not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/capabilities
// ─────────────────────────────────────────────────────────────────────────────

func capabilitiesGetHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkCapabilities == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	caps, err := networkCapabilities.Get(MaxCapabilitiesAge)
	if err != nil {
		jsonError(w, http.StatusInternalServerError,
			"Failed to load capabilities: "+err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(caps)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v4/network/capabilities/refresh
// ─────────────────────────────────────────────────────────────────────────────

func capabilitiesRefreshHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkCapabilities == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	caps, err := networkCapabilities.ForceRefresh()
	if err != nil {
		jsonError(w, http.StatusInternalServerError,
			"Failed to refresh capabilities: "+err.Error())
		return
	}

	// Audit event — la detección puede ejecutar subprocesos, queremos
	// saber quién la dispara para debug futuro.
	if networkEventEmitter != nil {
		_, _ = networkEventEmitter.Emit(r.Context(), EventInput{
			Category: CategoryObserver,
			Event:    "capabilities_refreshed",
			Level:    EventLevelInfo,
			Message:  "SystemCapabilities refreshed by user:" + session.Username,
		})
	}

	json.NewEncoder(w).Encode(caps)
}
