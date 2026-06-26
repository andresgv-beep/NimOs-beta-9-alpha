package main

// ═══════════════════════════════════════════════════════════════════════
// SHARES HTTP HANDLERS · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Esta capa SOLO traduce HTTP ↔ service.
// Sus responsabilidades:
//   · Routing (handleSharesRoutes)
//   · Auth (requireAdmin, requireAuth)
//   · Parseo del body de la request
//   · Llamada al service correspondiente
//   · Serialización JSON de la respuesta
//   · Mapeo de errores del service a códigos HTTP
//
// NO contiene lógica de negocio. Si necesitas validar nombres,
// orquestar BTRFS, o consultar SQLite → eso vive en shares_service.go.
//
// Endpoint contract:
//   GET    /api/shares             — lista shares (filtrado por permisos)
//   POST   /api/shares             — crea (body: {name, description, pool, quotaBytes})
//   PUT    /api/shares/:name       — actualiza (description, recycleBin, quota, permissions, appPermissions)
//   DELETE /api/shares/:name       — elimina + destruye subvolumen
//
// ═══════════════════════════════════════════════════════════════════════

import (
	"net/http"
	"regexp"
	"strings"
)

// sharePathRegex matchea /api/shares/:name con :name en grupo 1.
var sharePathRegex = regexp.MustCompile(`^/api/shares/([a-zA-Z0-9_-]+)$`)

// handleSharesRoutes despacha las requests del módulo Shares.
// Punto de entrada único registrado en el router HTTP principal.
func handleSharesRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Managed folders (Fase 3): /api/shares/{share}/folders[/{id}]
	// Se intercepta antes del routing de shares para no chocar con el regex
	// de recurso simple. Si la atiende, terminamos aquí.
	if tryHandleFolderRoutes(w, r) {
		return
	}

	// Colección · GET /api/shares · POST /api/shares
	if path == "/api/shares" {
		switch method {
		case "GET":
			sharesListHTTP(w, r)
		case "POST":
			sharesCreateHTTP(w, r)
		default:
			jsonError(w, 405, "Method not allowed")
		}
		return
	}

	// Huérfanos (FIX-3) · deben ir ANTES del regex de recurso, porque "orphans"
	// matchearía como nombre de share.
	if path == "/api/shares/orphans" && method == "GET" {
		sharesOrphansListHTTP(w, r)
		return
	}
	if path == "/api/shares/orphans/readopt" && method == "POST" {
		sharesOrphansReadoptHTTP(w, r)
		return
	}

	// Recurso · /api/shares/:name
	matches := sharePathRegex.FindStringSubmatch(path)
	if matches == nil {
		jsonError(w, 404, "Not found")
		return
	}
	target := matches[1]

	switch method {
	case "PUT":
		sharesUpdateHTTP(w, r, target)
	case "DELETE":
		sharesDeleteHTTP(w, r, target)
	default:
		jsonError(w, 405, "Method not allowed")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// GET /api/shares
// ═══════════════════════════════════════════════════════════════════════

func sharesListHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	// Admin ve todos; usuarios normales solo los suyos
	filterUser := ""
	if session.Role != "admin" {
		filterUser = session.Username
	}

	views, err := ListShares(r.Context(), filterUser)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	// Para no-admin: añadir myPermission en cada item (UX-friendly)
	if session.Role != "admin" {
		filtered := make([]map[string]interface{}, 0, len(views))
		for _, v := range views {
			m := v.ToMap()
			if perm, ok := v.Permissions[session.Username]; ok {
				m["myPermission"] = perm
			}
			EnrichShareViewWithHealth(m, v)
			filtered = append(filtered, m)
		}
		jsonOk(w, filtered)
		return
	}

	// Admin: devolver todos los views
	result := make([]map[string]interface{}, len(views))
	for i, v := range views {
		m := v.ToMap()
		EnrichShareViewWithHealth(m, v)
		result[i] = m
	}
	jsonOk(w, result)
}

// ═══════════════════════════════════════════════════════════════════════
// POST /api/shares
// ═══════════════════════════════════════════════════════════════════════

func sharesCreateHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	input := CreateShareInput{
		Name:        strings.TrimSpace(bodyStr(body, "name")),
		Description: bodyStr(body, "description"),
		PoolName:    bodyStr(body, "pool"),
		CreatedBy:   session.Username,
	}
	if qb, ok := body["quotaBytes"].(float64); ok {
		input.QuotaBytes = int64(qb)
	}

	result, err := CreateShare(r.Context(), input)
	if err != nil {
		// Service devuelve errores user-friendly · mapeamos a status razonables
		status := mapShareErrorToStatus(err)
		jsonError(w, status, err.Error())
		return
	}

	jsonOk(w, map[string]interface{}{
		"ok":   true,
		"name": result.Name,
		"path": result.Path,
		"pool": result.Pool,
	})
}

// ═══════════════════════════════════════════════════════════════════════
// PUT /api/shares/:name
// ═══════════════════════════════════════════════════════════════════════

func sharesUpdateHTTP(w http.ResponseWriter, r *http.Request, target string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)

	input := UpdateShareInput{}

	if desc, ok := body["description"].(string); ok {
		input.Description = strPtr(desc)
	}
	if rb, ok := body["recycleBin"].(bool); ok {
		input.RecycleBin = boolPtr(rb)
	}
	if quotaRaw, ok := body["quota"]; ok {
		var qb int64
		if f, ok := quotaRaw.(float64); ok {
			qb = int64(f)
		}
		input.Quota = &qb
	}
	if permsRaw, ok := body["permissions"]; ok {
		if newPermsMap, ok := permsRaw.(map[string]interface{}); ok {
			input.Permissions = make(map[string]string, len(newPermsMap))
			for u, v := range newPermsMap {
				if s, ok := v.(string); ok {
					input.Permissions[u] = s
				}
			}
		}
	}
	if appsRaw, ok := body["appPermissions"]; ok {
		if appsList, ok := appsRaw.([]interface{}); ok {
			input.AppPermissions = make([]map[string]interface{}, 0, len(appsList))
			for _, a := range appsList {
				if am, ok := a.(map[string]interface{}); ok {
					input.AppPermissions = append(input.AppPermissions, am)
				}
			}
		}
	}

	if err := UpdateShare(r.Context(), target, input); err != nil {
		status := mapShareErrorToStatus(err)
		jsonError(w, status, err.Error())
		return
	}

	jsonOk(w, map[string]interface{}{"ok": true})
}

// ═══════════════════════════════════════════════════════════════════════
// DELETE /api/shares/:name
// ═══════════════════════════════════════════════════════════════════════

func sharesDeleteHTTP(w http.ResponseWriter, r *http.Request, target string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	if err := DeleteShare(r.Context(), target); err != nil {
		status := mapShareErrorToStatus(err)
		jsonError(w, status, err.Error())
		return
	}

	jsonOk(w, map[string]interface{}{"ok": true})
}

// ═══════════════════════════════════════════════════════════════════════
// ERROR MAPPING · service errors → HTTP status codes
// ═══════════════════════════════════════════════════════════════════════

// mapShareErrorToStatus mapea errores user-friendly del service a HTTP status.
// Beta 8.1 usa strings; cuando reemplacemos por ErrCode semántico, este
// helper se sustituye por un switch sobre el code.
func mapShareErrorToStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return 404
	case strings.Contains(msg, "already exists"):
		return 400
	case strings.Contains(msg, "not mounted"):
		return 503
	case strings.Contains(msg, "required") ||
		strings.Contains(msg, "too long") ||
		strings.Contains(msg, "can only contain") ||
		strings.Contains(msg, "Invalid"):
		return 400
	default:
		return 500
	}
}
