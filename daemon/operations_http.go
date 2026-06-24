// operations_http.go — Handler HTTP para /api/operations (Beta 8.1.x · APP-012).
//
// Rutas:
//
//   GET /api/operations/{id}    · obtener estado de una op (polling)
//   GET /api/operations         · listar (admin only · query params: type, status, limit)
//
// Sin POST ni DELETE por HTTP · las operations se crean dentro del backend
// vía operationsRepo.Create() desde el handler que lance la op async.
//
// Autorización:
//   GET /api/operations/{id}: el creador (createdBy) o un admin.
//   GET /api/operations: solo admin.
//
// Garbage collection · ver db_operations.go::DeleteExpired. No se invoca
// desde este handler.

package main

import (
	"net/http"
	"regexp"
	"strconv"
)

// operationIDRegex valida el formato de id `op_<digits>_<hex8>`.
// Defensivo: previene inyección de paths raros vía URL.
var operationIDRegex = regexp.MustCompile(`^op_[0-9]+_[a-f0-9]{8}$`)

// handleOperationsRoutes responde a /api/operations y /api/operations/{id}.
func handleOperationsRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// GET /api/operations · listar (admin)
	if path == "/api/operations" && method == "GET" {
		session := requireAdmin(w, r)
		if session == nil {
			return
		}
		listOperationsHandler(w, r)
		return
	}

	// GET /api/operations/{id} · obtener una
	getOpRegex := regexp.MustCompile(`^/api/operations/(op_[0-9]+_[a-f0-9]{8})$`)
	if matches := getOpRegex.FindStringSubmatch(path); matches != nil && method == "GET" {
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		getOperationHandler(w, r, matches[1], session)
		return
	}

	jsonError(w, 404, "Not found")
}

// listOperationsHandler · GET /api/operations · admin · listado con filtros.
//
// Query params (todos opcionales):
//
//	type=docker.install        · filtrar por tipo
//	status=running             · filtrar por status
//	createdBy=alice            · filtrar por usuario (solo admin lo puede usar)
//	limit=50                   · cap del resultado (default 100, max 500)
func listOperationsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opType := q.Get("type")
	status := q.Get("status")
	createdBy := q.Get("createdBy")

	limit := 100
	if lStr := q.Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	ops, err := operationsRepo.List(r.Context(), opType, status, createdBy, limit)
	if err != nil {
		logMsg("operations: list failed: %v", err)
		jsonError(w, 500, "Failed to list operations")
		return
	}

	result := make([]map[string]interface{}, 0, len(ops))
	for _, op := range ops {
		result = append(result, op.ToMap())
	}
	jsonOk(w, map[string]interface{}{"operations": result, "count": len(result)})
}

// getOperationHandler · GET /api/operations/{id} · creador o admin.
//
// Devuelve 404 si la op no existe, 403 si el caller no es creador ni admin.
func getOperationHandler(w http.ResponseWriter, r *http.Request, id string, session *DBSession) {
	// Defensa adicional · ya validado por regex en handleOperationsRoutes
	// pero por si llega por otra ruta.
	if !operationIDRegex.MatchString(id) {
		jsonError(w, 400, "Invalid operation id")
		return
	}

	op, err := operationsRepo.Get(r.Context(), id)
	if err != nil {
		logMsg("operations: get %q failed: %v", id, err)
		jsonError(w, 500, "Failed to retrieve operation")
		return
	}
	if op == nil {
		jsonError(w, 404, "Operation not found")
		return
	}

	// Autorización · creador o admin (patrón consistente con resto del proyecto)
	if op.CreatedBy != session.Username && session.Role != "admin" {
		jsonError(w, 403, "Not authorized to view this operation")
		return
	}

	jsonOk(w, op.ToMap())
}
