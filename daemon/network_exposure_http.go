// network_exposure_http.go — HTTP handlers para /api/v4/network/exposure.
//
// Endpoint contract:
//
//   Config global (singleton):
//   GET  /api/v4/network/exposure/config   — lee config (dominio, enabled)
//   PUT  /api/v4/network/exposure/config   — actualiza config
//
//   Apps expuestas:
//   GET    /api/v4/network/exposure        — lista apps + estado de certs
//   POST   /api/v4/network/exposure        — registra/expone una app
//   GET    /api/v4/network/exposure/:id    — detalle de una app
//   PUT    /api/v4/network/exposure/:id    — actualiza config de la app
//   DELETE /api/v4/network/exposure/:id    — deja de exponer (borra)
//
// El GET de colección incluye el snapshot de certs del observer para que
// la UI pinte el estado HTTPS de cada app junto a su fila, sin una segunda
// llamada.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// networkExposureObserver es el observer global de certs, enchufado en boot.
// Puede ser nil si el módulo no se ha inicializado.
var networkExposureObserver *NetworkExposureObserver

var exposureItemRegex = regexp.MustCompile(`^/api/v4/network/exposure/([^/]+)$`)

// handleNetworkExposureRoutes es el dispatcher del subsistema de exposición.
func handleNetworkExposureRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Config singleton.
	if path == "/api/v4/network/exposure/config" {
		switch method {
		case http.MethodGet:
			exposureConfigGetHTTP(w, r)
		case http.MethodPut:
			exposureConfigPutHTTP(w, r)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	// Colección.
	if path == "/api/v4/network/exposure" {
		switch method {
		case http.MethodGet:
			exposureListHTTP(w, r)
		case http.MethodPost:
			exposureCreateHTTP(w, r)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	// Recurso individual.
	if m := exposureItemRegex.FindStringSubmatch(path); m != nil {
		id := m[1]
		// "config" ya se manejó arriba; aquí id es un UUID de app.
		switch method {
		case http.MethodGet:
			exposureGetHTTP(w, r, id)
		case http.MethodPut:
			exposureUpdateHTTP(w, r, id)
		case http.MethodDelete:
			exposureDeleteHTTP(w, r, id)
		default:
			jsonError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	jsonError(w, http.StatusNotFound, "Not found")
}

// ─────────────────────────────────────────────────────────────────────────────
// Config singleton
// ─────────────────────────────────────────────────────────────────────────────

func exposureConfigGetHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	cfg, err := networkRepo.GetExposureConfig(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"config": cfg})
}

type exposureConfigRequest struct {
	BaseDomain    *string `json:"base_domain"`
	CaddyAdminURL *string `json:"caddy_admin_url"`
	Enabled       *bool   `json:"enabled"`
	HTTPPort      *int    `json:"http_port"`
	HTTPSPort     *int    `json:"https_port"`
}

func exposureConfigPutHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	var req exposureConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	// Partimos de la config actual y aplicamos los campos presentes.
	cfg, err := networkRepo.GetExposureConfig(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.BaseDomain != nil {
		cfg.BaseDomain = strings.TrimSpace(*req.BaseDomain)
	}
	if req.CaddyAdminURL != nil {
		cfg.CaddyAdminURL = strings.TrimSpace(*req.CaddyAdminURL)
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.HTTPPort != nil {
		cfg.HTTPPort = *req.HTTPPort
	}
	if req.HTTPSPort != nil {
		cfg.HTTPSPort = *req.HTTPSPort
	}

	err = exposureWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.SaveExposureConfig(r.Context(), tx, cfg)
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"config": cfg})
}

// ─────────────────────────────────────────────────────────────────────────────
// Apps
// ─────────────────────────────────────────────────────────────────────────────

func exposureListHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	apps, err := networkRepo.ListExposedApps(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Enriquecer el DTO con metadatos persistidos en docker_apps.config (read
	// model / API projection). El repo de Network da lo SUYO; aquí componemos la
	// vista cruzando con docker_apps. Un solo query (anti N+1) vía el mapa.
	// Extensible: hoy landing_path; mañana health_url, supports_iframe, etc.
	enrichExposureDTO(r.Context(), apps)

	resp := map[string]interface{}{"apps": apps}
	// Adjuntar snapshot de certs si el observer está disponible.
	if networkExposureObserver != nil {
		if snap := networkExposureObserver.Snapshot(); snap != nil {
			resp["certs"] = snap
		}
	}
	jsonOk(w, resp)
}

type exposureCreateRequest struct {
	AppID        string `json:"app_id"`
	DisplayName  string `json:"display_name"`
	Subdomain    string `json:"subdomain"`
	Path         string `json:"path"`
	UpstreamHost string `json:"upstream_host"`
	UpstreamPort int    `json:"upstream_port"`
	Enabled      *bool  `json:"enabled"`
}

func exposureCreateHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	var req exposureCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		jsonError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	if req.Subdomain == "" && req.Path == "" {
		jsonError(w, http.StatusBadRequest, "subdomain or path is required")
		return
	}
	if req.UpstreamHost == "" {
		jsonError(w, http.StatusBadRequest, "upstream_host is required")
		return
	}
	if req.UpstreamPort <= 0 || req.UpstreamPort > 65535 {
		jsonError(w, http.StatusBadRequest, "upstream_port must be 1-65535")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	app := &NetworkExposedApp{
		AppID:        req.AppID,
		DisplayName:  req.DisplayName,
		Subdomain:    strings.TrimSpace(req.Subdomain),
		Path:         strings.TrimSpace(req.Path),
		UpstreamHost: req.UpstreamHost,
		UpstreamPort: req.UpstreamPort,
		Enabled:      enabled,
	}
	err := exposureWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.CreateExposedApp(r.Context(), tx, app)
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonError(w, http.StatusConflict, "App already exposed (app_id or route in use)")
			return
		}
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"app": app})
}

func exposureGetHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	app, err := networkRepo.GetExposedApp(r.Context(), id)
	if errors.Is(err, ErrExposedAppNotFound) {
		jsonError(w, http.StatusNotFound, "Exposed app not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"app": app})
}

func exposureUpdateHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	// Candado optimista (CRIT-1): toda mutación exige If-Match con la
	// desired_generation que el cliente vio. Sin él no hay forma de saber
	// si está editando sobre una vista vieja.
	expectedGen, hasGen := parseIfMatch(r)
	if !hasGen {
		jsonError(w, http.StatusPreconditionRequired,
			"Missing If-Match header (send the desired_generation you last read)")
		return
	}

	// Cargar existente para componer los campos a actualizar.
	app, err := networkRepo.GetExposedApp(r.Context(), id)
	if errors.Is(err, ErrExposedAppNotFound) {
		jsonError(w, http.StatusNotFound, "Exposed app not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req exposureCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if req.DisplayName != "" {
		app.DisplayName = req.DisplayName
	}
	if req.Subdomain != "" || req.Path != "" {
		app.Subdomain = strings.TrimSpace(req.Subdomain)
		app.Path = strings.TrimSpace(req.Path)
	}
	if req.UpstreamHost != "" {
		app.UpstreamHost = req.UpstreamHost
	}
	if req.UpstreamPort != 0 {
		app.UpstreamPort = req.UpstreamPort
	}
	if req.Enabled != nil {
		app.Enabled = *req.Enabled
	}

	err = exposureWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.UpdateExposedAppConfig(r.Context(), tx, app, expectedGen)
	})
	if errors.Is(err, ErrExposedAppConflict) {
		exposureConflictResponse(w, r, id)
		return
	}
	if errors.Is(err, ErrExposedAppNotFound) {
		jsonError(w, http.StatusNotFound, "Exposed app not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Releer: el UPDATE incrementó desired_generation; devolver el estado
	// real para que el cliente tenga el token fresco sin otra petición.
	if fresh, gerr := networkRepo.GetExposedApp(r.Context(), id); gerr == nil {
		app = fresh
	}
	jsonOk(w, map[string]interface{}{"app": app})
}

func exposureDeleteHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if requireAdmin(w, r) == nil {
		return
	}
	if networkRepo == nil {
		jsonError(w, http.StatusServiceUnavailable, "Network module not initialized")
		return
	}
	expectedGen, hasGen := parseIfMatch(r)
	if !hasGen {
		jsonError(w, http.StatusPreconditionRequired,
			"Missing If-Match header (send the desired_generation you last read)")
		return
	}
	err := exposureWithTx(r.Context(), func(tx *sql.Tx) error {
		return networkRepo.DeleteExposedApp(r.Context(), tx, id, expectedGen)
	})
	if errors.Is(err, ErrExposedAppConflict) {
		exposureConflictResponse(w, r, id)
		return
	}
	if errors.Is(err, ErrExposedAppNotFound) {
		jsonError(w, http.StatusNotFound, "Exposed app not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"deleted": id})
}

// parseIfMatch extrae la generación esperada del header If-Match (candado
// optimista CRIT-1). Acepta el entero a pelo ("3") o entre comillas estilo
// ETag ("\"3\""). ok=false si falta o no es un entero.
func parseIfMatch(r *http.Request) (int64, bool) {
	raw := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	if raw == "" {
		return 0, false
	}
	gen, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// exposureConflictResponse responde 412 con el estado ACTUAL de la app en el
// body, para que el cliente pueda refrescar su vista y decidir.
func exposureConflictResponse(w http.ResponseWriter, r *http.Request, id string) {
	payload := map[string]interface{}{
		"error": "Exposed app was modified by another client (stale If-Match generation)",
	}
	if current, err := networkRepo.GetExposedApp(r.Context(), id); err == nil {
		payload["app"] = current
	}
	jsonResponse(w, http.StatusPreconditionFailed, payload)
}

// exposureWithTx ejecuta fn dentro de una tx sobre la conexión global `db`.
func exposureWithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
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

// enrichExposureDTO compone la vista de las apps expuestas añadiendo metadatos
// que NO viven en las tablas de Network, sino en docker_apps.config (read model
// / API projection). El repo de Network da los datos de SU dominio; aquí se
// enriquece el DTO cruzando con docker_apps.
//
// ANTI N+1: un solo query (GetInstalledAppsConfigMap) trae todos los configs,
// luego se cruza en memoria · NO un SELECT por app.
//
// EXTENSIBLE: hoy añade landing_path. El día de mañana, health_url,
// supports_iframe, app_version, etc. · todos enriquecimientos del mismo DTO,
// se añaden aquí sin tocar el repo de Network.
//
// Si algo falla (no hay appsRepo, error en el query), se degrada silencioso ·
// las apps salen sin el enriquecimiento (no rompe la respuesta).
func enrichExposureDTO(ctx context.Context, apps []*NetworkExposedApp) {
	if len(apps) == 0 || appsRepo == nil {
		return
	}
	configMap, err := appsRepo.GetInstalledAppsConfigMap(ctx)
	if err != nil {
		logMsg("enrichExposureDTO: no se pudo leer configs de docker_apps: %v", err)
		return
	}
	for _, app := range apps {
		cfg, ok := configMap[app.AppID]
		if !ok {
			continue
		}
		// landing_path · ruta del panel (Pi-hole "/admin").
		if lp := landingPathFromConfig(cfg); lp != "" {
			app.LandingPath = lp
		}
		// (futuro) health_url, supports_iframe, app_version... van aquí.
	}
}
