package main

import (
	"net/http"
	"regexp"
	"strings"
)

// ═══════════════════════════════════════════════════════════════════════
// MANAGED FOLDERS · HTTP · NimOS Beta 8.1 (Fase 3, paso 4)
// ═══════════════════════════════════════════════════════════════════════
//
// Rutas (bajo el prefijo /api/shares/ que ya maneja handleSharesRoutes):
//   GET    /api/shares/{share}/folders        listar
//   POST   /api/shares/{share}/folders        crear
//   PATCH  /api/shares/{share}/folders/{id}   editar quota/permisos
//   DELETE /api/shares/{share}/folders/{id}   borrar
//
// Autorización: crear/editar/borrar → solo admin. Listar → autenticado.
//
// El dispatch se engancha desde handleSharesRoutes (shares_http.go), que es
// quien recibe todo lo que cuelga de /api/shares/. tryHandleFolderRoutes
// devuelve true si la request era de folders (y ya la atendió), false si no.
// ═══════════════════════════════════════════════════════════════════════

// /api/shares/{share}/folders            → grupo 1 = share
var folderCollectionRegex = regexp.MustCompile(`^/api/shares/([a-zA-Z0-9_-]+)/folders/?$`)

// /api/shares/{share}/filetypes          → grupo 1 = share
var shareFiletypesRegex = regexp.MustCompile(`^/api/shares/([a-zA-Z0-9_-]+)/filetypes/?$`)

// /api/shares/{share}/folders/{id}       → grupo 1 = share, grupo 2 = id (uuid)
var folderResourceRegex = regexp.MustCompile(`^/api/shares/([a-zA-Z0-9_-]+)/folders/([a-zA-Z0-9-]+)$`)

// tryHandleFolderRoutes intenta atender una ruta de managed folders.
// Devuelve true si la atendió, false si el path no es de folders.
func tryHandleFolderRoutes(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path

	// Distribución por tipo: /api/shares/{share}/filetypes
	if m := shareFiletypesRegex.FindStringSubmatch(path); m != nil {
		if r.Method == "GET" {
			shareFiletypesHTTP(w, r, m[1])
		} else {
			jsonError(w, 405, "Method not allowed")
		}
		return true
	}

	// Colección: /api/shares/{share}/folders
	if m := folderCollectionRegex.FindStringSubmatch(path); m != nil {
		share := m[1]
		switch r.Method {
		case "GET":
			foldersListHTTP(w, r, share)
		case "POST":
			foldersCreateHTTP(w, r, share)
		default:
			jsonError(w, 405, "Method not allowed")
		}
		return true
	}

	// Recurso: /api/shares/{share}/folders/{id}
	if m := folderResourceRegex.FindStringSubmatch(path); m != nil {
		share := m[1]
		id := m[2]
		switch r.Method {
		case "PATCH":
			foldersUpdateHTTP(w, r, share, id)
		case "DELETE":
			foldersDeleteHTTP(w, r, share, id)
		default:
			jsonError(w, 405, "Method not allowed")
		}
		return true
	}

	return false
}

// ─── GET /api/shares/{share}/folders ───
func foldersListHTTP(w http.ResponseWriter, r *http.Request, share string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	views, err := ListManagedFolders(r.Context(), share)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, views)
}

// ─── POST /api/shares/{share}/folders ───
func foldersCreateHTTP(w http.ResponseWriter, r *http.Request, share string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	in := CreateManagedFolderInput{
		ShareName: share,
		RelPath:   strings.TrimSpace(bodyStr(body, "relPath")),
		OwnerUser: strings.TrimSpace(bodyStr(body, "ownerUser")),
		CreatedBy: session.Username,
	}
	if qb, ok := body["quotaBytes"].(float64); ok {
		in.QuotaBytes = int64(qb)
	}
	in.Permissions = parsePermissionsMap(body["permissions"])

	view, err := CreateManagedFolder(r.Context(), in)
	if err != nil {
		jsonError(w, mapFolderErrorToStatus(err), err.Error())
		return
	}
	jsonOk(w, view)
}

// ─── PATCH /api/shares/{share}/folders/{id} ───
func foldersUpdateHTTP(w http.ResponseWriter, r *http.Request, share, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	var quotaPtr *int64
	if qb, ok := body["quotaBytes"].(float64); ok {
		q := int64(qb)
		quotaPtr = &q
	}

	var perms map[string]string
	if _, present := body["permissions"]; present {
		perms = parsePermissionsMap(body["permissions"])
	}

	view, err := UpdateManagedFolder(r.Context(), id, quotaPtr, perms)
	if err != nil {
		jsonError(w, mapFolderErrorToStatus(err), err.Error())
		return
	}
	jsonOk(w, view)
}

// ─── DELETE /api/shares/{share}/folders/{id} ───
func foldersDeleteHTTP(w http.ResponseWriter, r *http.Request, share, id string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	if err := DeleteManagedFolder(r.Context(), id); err != nil {
		jsonError(w, mapFolderErrorToStatus(err), err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}

// parsePermissionsMap convierte el campo JSON "permissions" (objeto
// username->permission) en map[string]string, ignorando valores no-string.
func parsePermissionsMap(raw interface{}) map[string]string {
	out := map[string]string{}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// mapFolderErrorToStatus traduce errores del service a códigos HTTP.
func mapFolderErrorToStatus(err error) int {
	if err == nil {
		return 200
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return 404
	case strings.Contains(msg, "already exists"):
		return 409
	case msg == "folder_not_empty":
		return 409
	case strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "required") ||
		strings.Contains(msg, "top-level") ||
		strings.Contains(msg, "too long"):
		return 400
	default:
		return 500
	}
}

// ─── GET /api/shares/{share}/filetypes ───
// Distribución por tipo de fichero. Escaneo en vivo (timeout interno 3s).
func shareFiletypesHTTP(w http.ResponseWriter, r *http.Request, share string) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	dist, err := scanShareFileTypes(share)
	if err != nil {
		jsonError(w, mapFolderErrorToStatus(err), err.Error())
		return
	}
	jsonOk(w, dist)
}
