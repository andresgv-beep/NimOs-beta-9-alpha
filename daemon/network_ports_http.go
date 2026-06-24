// network_ports_http.go — HTTP handlers para configuración de puertos.
//
// Endpoint contract:
//   GET  /api/v4/network/ports        — lista puertos (http + https)
//   GET  /api/v4/network/ports/:id    — detalle de un puerto
//   PUT  /api/v4/network/ports/:id    — actualiza config (port, bind, enabled)
//
// IDs válidos: 'http', 'https' (el schema CHECK los limita).
//
// Lo que estos handlers NO hacen (deferred):
//
//   - Aplicar el cambio al kernel (rebindar listeners). El handler solo
//     actualiza la DB: incrementa desired_generation y el reconciler de
//     ports (F-004+) verá pending y aplicará.
//
//   - POST/DELETE. Los ports son singletons del daemon — siempre existen
//     dos filas (http+https) que el boot crea si no existen. No tiene
//     sentido crearlos ni borrarlos vía API.
//
// Validación:
//   - port range: 1..65535 (puertos privilegiados <1024 permitidos pero
//     el reconciler fallará si el daemon no corre como root).
//   - bind_address: regex permisivo (IP v4, IP v6, '0.0.0.0', '::').
//   - enabled: boolean.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
)

// portPathRegex matchea /api/v4/network/ports/:id con :id ∈ {http, https}.
var portPathRegex = regexp.MustCompile(`^/api/v4/network/ports/(http|https)$`)

// handleNetworkPortsRoutes es el dispatcher. Registrado en http.go.
func handleNetworkPortsRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Colección · GET /api/v4/network/ports
	if path == "/api/v4/network/ports" {
		if method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		portsListHTTP(w, r)
		return
	}

	// Recurso · /api/v4/network/ports/:id
	matches := portPathRegex.FindStringSubmatch(path)
	if matches == nil {
		jsonError(w, http.StatusNotFound, "Not found")
		return
	}
	id := matches[1]

	switch method {
	case http.MethodGet:
		portsGetHTTP(w, r, id)
	case http.MethodPut:
		portsUpdateHTTP(w, r, id)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/ports
// ─────────────────────────────────────────────────────────────────────────────

// portView es lo que devuelve la API. Se diseña explícitamente — no
// reusamos NetworkPort para evitar exponer detalles internos (e.g.
// updated_at en formato exacto).
type portView struct {
	ID          string `json:"id"`
	Port        int    `json:"port"`
	BindAddress string `json:"bind_address"`
	Enabled     bool   `json:"enabled"`

	// Estado de convergencia derivado.
	Status      string `json:"status"` // converged | pending | drifted
	Desired     int64  `json:"desired_generation"`
	Observed    int64  `json:"observed_generation"`
	Applied     int64  `json:"applied_generation"`

	UpdatedAt string `json:"updated_at"` // RFC3339
}

// portToView convierte un NetworkPort en su representación API.
func portToView(p *NetworkPort) portView {
	status := "converged"
	if p.Convergence.IsPending() {
		status = "pending"
	} else if p.Convergence.HasDrifted() {
		status = "drifted"
	}
	return portView{
		ID:          p.ID,
		Port:        p.Port,
		BindAddress: p.BindAddress,
		Enabled:     p.Enabled,
		Status:      status,
		Desired:     p.Convergence.Desired,
		Observed:    p.Convergence.Observed,
		Applied:     p.Convergence.Applied,
		UpdatedAt:   p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func portsListHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	ports, err := networkRepo.ListPorts(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to list ports: "+err.Error())
		return
	}

	views := make([]portView, 0, len(ports))
	for _, p := range ports {
		views = append(views, portToView(p))
	}
	jsonOk(w, map[string]interface{}{"ports": views})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/ports/:id
// ─────────────────────────────────────────────────────────────────────────────

func portsGetHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	p, err := networkRepo.GetPort(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrPortNotFound) {
			jsonError(w, http.StatusNotFound, "Port not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to get port: "+err.Error())
		return
	}
	jsonOk(w, portToView(p))
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /api/v4/network/ports/:id
// ─────────────────────────────────────────────────────────────────────────────

// portUpdateRequest es el cuerpo de la request. Todos los campos
// requeridos — no soportamos PATCH parcial todavía (anti-explosión §1).
type portUpdateRequest struct {
	Port        *int    `json:"port"`
	BindAddress *string `json:"bind_address"`
	Enabled     *bool   `json:"enabled"`
}

// validatePortUpdate aplica las reglas de validación.
// Devuelve nil si todo OK; error con mensaje user-facing si algo falla.
func validatePortUpdate(req *portUpdateRequest) error {
	if req.Port == nil || req.BindAddress == nil || req.Enabled == nil {
		return fmt.Errorf("missing required fields: port, bind_address, enabled")
	}
	if *req.Port < 1 || *req.Port > 65535 {
		return fmt.Errorf("port must be in range 1..65535, got %d", *req.Port)
	}
	if err := validateBindAddress(*req.BindAddress); err != nil {
		return fmt.Errorf("bind_address invalid: %w", err)
	}
	return nil
}

// validateBindAddress valida que sea "0.0.0.0", "::", o una IP parseable.
// Hostnames NO se aceptan — bind quiere una IP, no DNS lookup.
func validateBindAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("empty")
	}
	// IPv4/IPv6 wildcard explícitos primero (net.ParseIP los acepta también).
	if addr == "0.0.0.0" || addr == "::" {
		return nil
	}
	if ip := net.ParseIP(addr); ip == nil {
		return fmt.Errorf("not a valid IP literal: %q", addr)
	}
	return nil
}

func portsUpdateHTTP(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	// Parse body.
	var req portUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if err := validatePortUpdate(&req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Update. Usamos una transacción para que la lectura inmediata
	// posterior (para el response) vea el estado consistente.
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to begin tx: "+err.Error())
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	err = networkRepo.UpdatePortConfig(r.Context(), tx, id, *req.Port, *req.BindAddress, *req.Enabled)
	if err != nil {
		if errors.Is(err, ErrPortNotFound) {
			jsonError(w, http.StatusNotFound, "Port not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to update port: "+err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to commit: "+err.Error())
		return
	}

	// Audit log (best-effort, no abortamos si falla).
	if networkEventEmitter != nil {
		targetID := id
		_, emitErr := networkEventEmitter.Emit(r.Context(), EventInput{
			Category: CategoryPort,
			Event:    "config_updated",
			TargetID: &targetID,
			Level:    EventLevelInfo,
			Message:  fmt.Sprintf("Port %s config updated by user:%s", id, session.Username),
		})
		if emitErr != nil {
			logMsg("ports update: emit event: %v", emitErr)
		}
	}

	// Releer y devolver el nuevo estado.
	p, err := networkRepo.GetPort(r.Context(), id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to read back: "+err.Error())
		return
	}
	jsonOk(w, portToView(p))
}
