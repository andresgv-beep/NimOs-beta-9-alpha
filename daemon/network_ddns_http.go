// network_ddns_http.go — HTTP handlers para /api/v4/network/ddns.
//
// Endpoint contract:
//
//   GET    /api/v4/network/ddns         — lista todas las entradas DDNS
//   POST   /api/v4/network/ddns         — crea una nueva entrada
//   GET    /api/v4/network/ddns/:id     — detalle de una entrada
//   PUT    /api/v4/network/ddns/:id     — actualiza config (no token)
//   DELETE /api/v4/network/ddns/:id     — elimina (y opcionalmente el secret)
//   POST   /api/v4/network/ddns/:id/token — rota el token sin recrear la entrada
//
// Diseño:
//
//   - Los handlers NUNCA devuelven el token en la respuesta. Solo el ID
//     del secret asociado.
//   - La rotación del token es un endpoint aparte (no parte de PUT) para
//     que sea explícito y auditable. PUT toca config, NO credenciales.
//   - DELETE acepta query ?delete_secret=true para borrar el secret
//     junto con la entrada. Default es false (preserva el secret).
//
// Validación:
//
//   - provider: debe estar en la lista permitida por el schema
//     (delegamos al CHECK de la DB; preferimos error útil).
//   - domain: regex permisivo (alfanuméricos, guiones, puntos).
//   - token: string no vacío, longitud razonable.
//   - update_interval: entre 60s y 86400s (24h).

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Routing
// ─────────────────────────────────────────────────────────────────────────────

// ddnsPathRegex matchea /api/v4/network/ddns/:id y /api/v4/network/ddns/:id/token
var ddnsItemRegex = regexp.MustCompile(`^/api/v4/network/ddns/([^/]+)(/token|/update)?$`)

// handleNetworkDdnsRoutes es el dispatcher.
func handleNetworkDdnsRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Colección /api/v4/network/ddns
	if path == "/api/v4/network/ddns" {
		switch method {
		case http.MethodGet:
			ddnsListHTTP(w, r)
		case http.MethodPost:
			ddnsCreateHTTP(w, r)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	// Recurso individual
	matches := ddnsItemRegex.FindStringSubmatch(path)
	if matches == nil {
		jsonError(w, http.StatusNotFound, "Not found")
		return
	}
	id := matches[1]
	isTokenSubpath := matches[2] == "/token"
	isUpdateSubpath := matches[2] == "/update"

	if isTokenSubpath {
		if method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		ddnsRotateTokenHTTP(w, r, id)
		return
	}

	if isUpdateSubpath {
		if method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		ddnsForceUpdateHTTP(w, r, id)
		return
	}

	switch method {
	case http.MethodGet:
		ddnsGetHTTP(w, r, id)
	case http.MethodPut:
		ddnsUpdateHTTP(w, r, id)
	case http.MethodDelete:
		ddnsDeleteHTTP(w, r, id)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// View types (API contract)
// ─────────────────────────────────────────────────────────────────────────────

// ddnsView es lo que devuelve la API. No incluye token ni secret_id
// expuesto — solo "has_token" para que el frontend sepa si existe.
type ddnsView struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Domain         string `json:"domain"`
	HasToken       bool   `json:"has_token"`
	Enabled        bool   `json:"enabled"`
	AutoUpdate     bool   `json:"auto_update"`
	UpdateInterval int    `json:"update_interval"`

	LastRunAt     *string `json:"last_run_at,omitempty"`
	LastRunResult *string `json:"last_run_result,omitempty"`
	LastIP        *string `json:"last_ip,omitempty"`

	Status   string `json:"status"`
	Desired  int64  `json:"desired_generation"`
	Observed int64  `json:"observed_generation"`
	Applied  int64  `json:"applied_generation"`
}

func ddnsToView(d *NetworkDdns) ddnsView {
	status := "converged"
	if d.Convergence.IsPending() {
		status = "pending"
	} else if d.Convergence.HasDrifted() {
		status = "drifted"
	}
	var lastRunAtStr *string
	if d.LastRunAt != nil {
		s := d.LastRunAt.UTC().Format("2006-01-02T15:04:05Z")
		lastRunAtStr = &s
	}
	return ddnsView{
		ID:             d.ID,
		Provider:       d.Provider,
		Domain:         d.Domain,
		HasToken:       d.TokenSecretID != "",
		Enabled:        d.Enabled,
		AutoUpdate:     d.AutoUpdate,
		UpdateInterval: d.UpdateInterval,
		LastRunAt:      lastRunAtStr,
		LastRunResult:  d.LastRunResult,
		LastIP:         d.LastIP,
		Status:         status,
		Desired:        d.Convergence.Desired,
		Observed:       d.Convergence.Observed,
		Applied:        d.Convergence.Applied,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Validation helpers
// ─────────────────────────────────────────────────────────────────────────────

// domainRegex valida un nombre de dominio simple: alfanuméricos, guiones,
// puntos. NO acepta espacios ni caracteres especiales. Acepta nombres
// internacionalizados solo si vienen como ASCII (xn--...).
//
// Cada label (segmento entre puntos) debe:
//   - empezar con alfanumérico
//   - terminar con alfanumérico
//   - el medio puede tener alfanuméricos y guiones
//
// Esto rechaza casos como "-bad.com", "bad-.com", "good.-bad.com".
var domainLabelRegex = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

func validateDomain(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return errors.New("domain is required")
	}
	if len(d) > 253 {
		return errors.New("domain too long")
	}
	// Debe tener al menos un punto (un dominio "single label" como "x"
	// se rechaza para evitar typos del usuario).
	if !strings.Contains(d, ".") {
		return errors.New("domain must contain at least one dot")
	}
	// Cada label debe ser válida por separado.
	for _, label := range strings.Split(d, ".") {
		if label == "" {
			return errors.New("domain has empty label")
		}
		if !domainLabelRegex.MatchString(label) {
			return fmt.Errorf("domain label %q has invalid characters or format", label)
		}
	}
	return nil
}

func validateToken(t string) error {
	if t == "" {
		return errors.New("token is required")
	}
	if len(t) > 1024 {
		return errors.New("token too long")
	}
	return nil
}

func validateUpdateInterval(seconds int) error {
	if seconds < 60 {
		return fmt.Errorf("update_interval too low (%d), minimum is 60 seconds", seconds)
	}
	if seconds > 86400 {
		return fmt.Errorf("update_interval too high (%d), maximum is 86400 seconds (24h)", seconds)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/ddns
// ─────────────────────────────────────────────────────────────────────────────

func ddnsListHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	entries, err := networkRepo.ListDdns(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to list ddns: "+err.Error())
		return
	}
	views := make([]ddnsView, 0, len(entries))
	for _, d := range entries {
		views = append(views, ddnsToView(d))
	}
	jsonOk(w, map[string]interface{}{"ddns": views})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v4/network/ddns
// ─────────────────────────────────────────────────────────────────────────────

type ddnsCreateRequest struct {
	Provider       string `json:"provider"`
	Domain         string `json:"domain"`
	Token          string `json:"token"`
	Enabled        *bool  `json:"enabled"`
	AutoUpdate     *bool  `json:"auto_update"`
	UpdateInterval *int   `json:"update_interval"`
}

func ddnsCreateHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil || networkSecretsStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	var req ddnsCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if req.Provider == "" {
		jsonError(w, http.StatusBadRequest, "provider is required")
		return
	}
	if err := validateDomain(req.Domain); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateToken(req.Token); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Defaults
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	autoUpdate := true
	if req.AutoUpdate != nil {
		autoUpdate = *req.AutoUpdate
	}
	updateInterval := 900
	if req.UpdateInterval != nil {
		if err := validateUpdateInterval(*req.UpdateInterval); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		updateInterval = *req.UpdateInterval
	}

	// Crear secret + ddns en una sola operación atómica conceptual.
	// El secret se crea fuera de tx (la API de SecretsStore no acepta tx);
	// si la creación del ddns falla, borramos el secret (best-effort).
	secretID, err := networkSecretsStore.CreateSecret("ddns_token",
		req.Provider+":"+req.Domain, []byte(req.Token))
	if err != nil {
		// "secret already exists" → mapear a 409 (el caller probablemente
		// está intentando crear un duplicado por dominio).
		if strings.Contains(err.Error(), "already exists") {
			jsonError(w, http.StatusConflict,
				"DDNS for this provider+domain already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to store token: "+err.Error())
		return
	}

	d := &NetworkDdns{
		Provider:       req.Provider,
		Domain:         strings.TrimSpace(req.Domain),
		TokenSecretID:  string(secretID),
		Enabled:        enabled,
		AutoUpdate:     autoUpdate,
		UpdateInterval: updateInterval,
	}

	err = ddnsWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.CreateDdns(r.Context(), tx, d)
	})
	if err != nil {
		// Rollback del secret huérfano.
		_ = networkSecretsStore.DeleteSecret(secretID)
		// Detectar UNIQUE(domain) conflict.
		if strings.Contains(err.Error(), "already exists") {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		// CHECK constraint del provider.
		if strings.Contains(err.Error(), "CHECK constraint failed") {
			jsonError(w, http.StatusBadRequest, "provider not allowed: "+req.Provider)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to create ddns: "+err.Error())
		return
	}

	// Audit event
	ddnsEmitAudit(r.Context(), d.ID, "created",
		fmt.Sprintf("DDNS %s created for %s by user:%s", d.Provider, d.Domain, session.Username))

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ddnsToView(d))
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v4/network/ddns/:id
// ─────────────────────────────────────────────────────────────────────────────

func ddnsGetHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	d, err := networkRepo.GetDdns(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrDdnsNotFound) {
			jsonError(w, http.StatusNotFound, "DDNS not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to get ddns: "+err.Error())
		return
	}
	jsonOk(w, ddnsToView(d))
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /api/v4/network/ddns/:id
// ─────────────────────────────────────────────────────────────────────────────

type ddnsUpdateRequest struct {
	Enabled        *bool `json:"enabled"`
	AutoUpdate     *bool `json:"auto_update"`
	UpdateInterval *int  `json:"update_interval"`
}

func ddnsUpdateHTTP(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	var req ddnsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if req.Enabled == nil || req.AutoUpdate == nil || req.UpdateInterval == nil {
		jsonError(w, http.StatusBadRequest,
			"missing required fields: enabled, auto_update, update_interval")
		return
	}
	if err := validateUpdateInterval(*req.UpdateInterval); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	err := ddnsWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.UpdateDdnsConfig(r.Context(), tx, id,
			*req.Enabled, *req.AutoUpdate, *req.UpdateInterval)
	})
	if err != nil {
		if errors.Is(err, ErrDdnsNotFound) {
			jsonError(w, http.StatusNotFound, "DDNS not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to update: "+err.Error())
		return
	}

	ddnsEmitAudit(r.Context(), id, "config_updated",
		fmt.Sprintf("DDNS %s config updated by user:%s", id, session.Username))

	d, _ := networkRepo.GetDdns(r.Context(), id)
	jsonOk(w, ddnsToView(d))
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/v4/network/ddns/:id
// ─────────────────────────────────────────────────────────────────────────────

func ddnsDeleteHTTP(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	// ?delete_secret=true para también borrar el secret asociado.
	deleteSecret := false
	if v := r.URL.Query().Get("delete_secret"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			deleteSecret = b
		}
	}

	// Leer primero para conocer el secret_id.
	d, err := networkRepo.GetDdns(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrDdnsNotFound) {
			jsonError(w, http.StatusNotFound, "DDNS not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to read: "+err.Error())
		return
	}
	secretID := d.TokenSecretID

	err = ddnsWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.DeleteDdns(r.Context(), tx, id)
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to delete: "+err.Error())
		return
	}

	if deleteSecret && secretID != "" && networkSecretsStore != nil {
		// best-effort: si falla, lo logueamos pero ya devolvimos OK.
		if err := networkSecretsStore.DeleteSecret(SecretID(secretID)); err != nil {
			logMsg("ddns delete: also delete secret %s: %v", secretID, err)
		}
	}

	ddnsEmitAudit(r.Context(), id, "deleted",
		fmt.Sprintf("DDNS %s deleted by user:%s (delete_secret=%v)",
			id, session.Username, deleteSecret))

	w.WriteHeader(http.StatusNoContent)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v4/network/ddns/:id/token
// ─────────────────────────────────────────────────────────────────────────────

type ddnsRotateTokenRequest struct {
	Token string `json:"token"`
}

// ddnsForceUpdateHTTP · POST /api/v4/network/ddns/:id/update
// Dispara una actualización inmediata fuera del ciclo periódico ("Actualizar ahora").
func ddnsForceUpdateHTTP(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil || networkDDNSReconciler == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	// Verificar que existe antes de forzar.
	if _, err := networkRepo.GetDdns(r.Context(), id); err != nil {
		if errors.Is(err, ErrDdnsNotFound) {
			jsonError(w, http.StatusNotFound, "DDNS not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to read: "+err.Error())
		return
	}

	if err := networkDDNSReconciler.ForceUpdate(r.Context(), id); err != nil {
		jsonError(w, http.StatusBadGateway, "Update failed: "+err.Error())
		return
	}

	ddnsEmitAudit(r.Context(), id, "force_update",
		fmt.Sprintf("DDNS %s manually updated by user:%s", id, session.Username))

	// Devolver el estado actualizado.
	d, err := networkRepo.GetDdns(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	jsonOk(w, ddnsToView(d))
}

func ddnsRotateTokenHTTP(w http.ResponseWriter, r *http.Request, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if networkRepo == nil || networkSecretsStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}

	var req ddnsRotateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if err := validateToken(req.Token); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	d, err := networkRepo.GetDdns(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrDdnsNotFound) {
			jsonError(w, http.StatusNotFound, "DDNS not found: "+id)
			return
		}
		jsonError(w, http.StatusInternalServerError, "Failed to read: "+err.Error())
		return
	}

	// Rotar el plaintext del secret existente (mantiene mismo ID).
	if err := networkSecretsStore.UpdateSecret(SecretID(d.TokenSecretID), []byte(req.Token)); err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to rotate token: "+err.Error())
		return
	}

	ddnsEmitAudit(r.Context(), id, "token_rotated",
		fmt.Sprintf("DDNS %s token rotated by user:%s", id, session.Username))

	w.WriteHeader(http.StatusNoContent)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// ddnsWithTx ejecuta fn dentro de una tx sobre `db`. Helper local para
// no depender del tx helper del reconciler (que es privado).
func ddnsWithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ddnsEmitAudit emite un evento de auditoría con categoría DDNS. No
// propaga errores — best-effort.
func ddnsEmitAudit(ctx context.Context, id, event, message string) {
	if networkEventEmitter == nil {
		return
	}
	targetID := id
	if _, err := networkEventEmitter.Emit(ctx, EventInput{
		Category: CategoryDdns,
		Event:    event,
		TargetID: &targetID,
		Level:    EventLevelInfo,
		Message:  message,
	}); err != nil {
		logMsg("ddns audit emit %s: %v", event, err)
	}
}
