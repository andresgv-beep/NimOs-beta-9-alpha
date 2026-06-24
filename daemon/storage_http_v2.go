// storage_http_v2.go — Handlers HTTP del stack storage (Beta 8).
//
// Expone el StorageService vía REST en /api/storage/v2/...
// Único stack HTTP de storage tras Sesión 4 (el legacy /api/storage/* fue
// eliminado completamente, junto con storage_http.go y storage_disk_mgmt.go).
//
// Convenciones:
//   - Éxito:    HTTP 200, body { "data": ... }
//   - Error:    HTTP 4xx/5xx, body { "error": { "code": "...", "message": "..." } }
//
// Códigos HTTP por code semántico:
//   - pool_not_found, device_not_found             → 404
//   - bad_request, profile_invalid                 → 400
//   - pool_observed, capability_missing            → 403
//   - pool_name_taken, device_in_use,
//     operation_in_progress, min_disks_reached,
//     device_not_eligible, insufficient_disks      → 409
//   - btrfs_command_failed, mount_failed,
//     internal                                     → 500
//
// see docs/storage_http_api.md

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// HTTP response helpers
// ─────────────────────────────────────────────────────────────────────────────

// apiResponse es el wrapper estándar de toda respuesta exitosa.
type apiResponse struct {
	Data interface{} `json:"data"`
}

// apiErrorResponse es el wrapper estándar de toda respuesta de error.
type apiErrorResponse struct {
	Error apiError `json:"error"`
}

// apiError es el cuerpo del campo "error" en respuestas de fallo.
//
// Details es opcional (omitempty) y se usa cuando el error tiene contexto
// estructurado relevante para la UI, por ejemplo ErrDiskHasFilesystem
// (Bloque C3.4 — wizard de doble intención necesita IsManaged, PoolName,
// ObservationHealth, etc. para renderizar la elección "importar vs
// destruir"). En errores genéricos Details no se incluye.
type apiError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// writeJSON serializa data y la escribe como respuesta. status es el
// código HTTP. Si la serialización falla, escribe un error 500 sin body
// para no enviar JSON inválido.
func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		// No podemos garantizar JSON limpio si falló Marshal.
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeData es atajo para writeJSON con apiResponse.
func writeData(w http.ResponseWriter, status int, data interface{}) {
	writeJSON(w, status, apiResponse{Data: data})
}

// writeError serializa una respuesta de error con código y mensaje.
// El status HTTP se calcula desde el code semántico.
func writeError(w http.ResponseWriter, code, message string) {
	status := httpStatusForCode(code)
	writeJSON(w, status, apiErrorResponse{
		Error: apiError{Code: code, Message: message},
	})
}

// writeErrorWithDetails es como writeError pero adjunta un payload estructurado
// en error.details. Útil para errores tipados que la UI necesita renderizar
// con contexto rico (Bloque C3.4: ErrDiskHasFilesystem expone IsManaged,
// PoolName, ObservationHealth, etc.).
func writeErrorWithDetails(w http.ResponseWriter, code, message string, details interface{}) {
	status := httpStatusForCode(code)
	writeJSON(w, status, apiErrorResponse{
		Error: apiError{Code: code, Message: message, Details: details},
	})
}

// writeServiceError extrae el code de un ServiceError y delega en writeError.
// Reconoce también tipos de error específicos con payload estructurado
// (ErrDiskHasFilesystem) y los enriquece con error.details.
// Si el error no es de los tipos conocidos, devuelve internal 500.
func writeServiceError(w http.ResponseWriter, err error) {
	// ErrDiskHasFilesystem — Bloque C3.4. La UI necesita los campos
	// estructurados para el wizard de doble intención.
	if fsErr, ok := err.(*ErrDiskHasFilesystem); ok {
		// Status 409 Conflict: el recurso no se puede crear porque hay
		// algo gestionable ya presente. Reutilizamos ErrCodeDeviceInUse
		// para que httpStatusForCode devuelva 409. El code semántico real
		// va tal cual (fsErr.Code() = "DISK_HAS_FILESYSTEM").
		//
		// DEUDA-ARQUI-OBSERVABLE-ENTITY: cuando se generalice el tipo a
		// ErrStorageEntityPresent, este case sirve de plantilla.
		writeJSON(w, http.StatusConflict, apiErrorResponse{
			Error: apiError{
				Code:    fsErr.Code(),
				Message: fsErr.Error(),
				Details: fsErr,
			},
		})
		return
	}
	if se, ok := err.(*ServiceError); ok {
		writeError(w, se.Code, se.Msg)
		return
	}
	logMsg("storage HTTP: unexpected error: %v", err)
	writeError(w, ErrCodeInternal, err.Error())
}

// httpStatusForCode mapea el code semántico a HTTP status.
func httpStatusForCode(code string) int {
	switch code {
	case ErrCodePoolNotFound, ErrCodeDeviceNotFound:
		return http.StatusNotFound
	case ErrCodeBadRequest, ErrCodeProfileInvalid:
		return http.StatusBadRequest
	case ErrCodePoolObserved, ErrCodeCapabilityMissing:
		return http.StatusForbidden
	case ErrCodePoolNameTaken,
		ErrCodeDeviceInUse,
		ErrCodeOperationInProgress,
		ErrCodeMinDisksReached,
		ErrCodeDeviceNotEligible,
		ErrCodeInsufficientDisks,
		ErrCodeTransitionNotPermitted,
		ErrCodePoolRecovery:
		return http.StatusConflict
	case ErrCodeBtrfsCommandFailed, ErrCodeMountFailed, ErrCodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// methodNotAllowed responde 405 con el header Allow correcto.
func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	if len(allowed) > 0 {
		allowStr := allowed[0]
		for i := 1; i < len(allowed); i++ {
			allowStr += ", " + allowed[i]
		}
		w.Header().Set("Allow", allowStr)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMethodNotAllowed)
	body, _ := json.Marshal(apiErrorResponse{
		Error: apiError{Code: ErrCodeBadRequest, Message: "method not allowed"},
	})
	_, _ = w.Write(body)
}

// ─────────────────────────────────────────────────────────────────────────────
// StorageHTTPHandler — agrupa los handlers
// ─────────────────────────────────────────────────────────────────────────────

// StorageHTTPHandler agrupa los handlers HTTP del módulo storage Beta 8.
// Inyectamos el service para que los tests puedan usar un service
// con DB temporal y mock executor.
type StorageHTTPHandler struct {
	service *StorageService
}

// storageHTTPHandler es la instancia global. Inicializada por initStorageModule()
// y consumida por startHTTPServer() para registrar las rutas v2.
var storageHTTPHandler *StorageHTTPHandler

// NewStorageHTTPHandler crea el handler con el service inyectado.
func NewStorageHTTPHandler(service *StorageService) *StorageHTTPHandler {
	return &StorageHTTPHandler{service: service}
}

// Register registra todas las rutas en el mux dado.
// El path base es /api/storage/v2.
//
// Todas las rutas pasan por requireAdmin (mismo patrón que Beta 7):
// si la sesión no es admin → 401 Unauthorized sin tocar el service.
func (h *StorageHTTPHandler) Register(mux *http.ServeMux) {
	// Recursos principales (Bloque 1 anterior)
	mux.HandleFunc("/api/storage/v2/pools", h.requireAdmin(h.handlePools))
	mux.HandleFunc("/api/storage/v2/pools/", h.requireAdmin(h.handlePoolByID))
	mux.HandleFunc("/api/storage/v2/devices", h.requireAdmin(h.handleDevices))
	mux.HandleFunc("/api/storage/v2/operations", h.requireAdmin(h.handleOperations))
	mux.HandleFunc("/api/storage/v2/generation", h.requireAdmin(h.handleGeneration))
	mux.HandleFunc("/api/storage/v2/scan", h.requireAdmin(h.handleScan))

	// Endpoints añadidos en Fase 7 (Bloque 1) para que la UI pudiera migrar
	// del /api/storage/* original al stack v2 completo.
	mux.HandleFunc("/api/storage/v2/capabilities", h.requireAdmin(h.handleCapabilities))
	mux.HandleFunc("/api/storage/v2/status", h.requireAdmin(h.handleStatus))
	mux.HandleFunc("/api/storage/v2/alerts", h.requireAdmin(h.handleAlerts))
	mux.HandleFunc("/api/storage/v2/disks", h.requireAdmin(h.handleDisksCategorized))
	mux.HandleFunc("/api/storage/v2/wipe", h.requireAdmin(h.handleWipe))
	mux.HandleFunc("/api/storage/v2/scrub", h.requireAdmin(h.handleScrubAction))
	mux.HandleFunc("/api/storage/v2/scrub/status", h.requireAdmin(h.handleScrubStatus))
	mux.HandleFunc("/api/storage/v2/snapshots", h.requireAdmin(h.handleSnapshots))
	mux.HandleFunc("/api/storage/v2/pool/export", h.requireAdmin(h.handlePoolExport))
	mux.HandleFunc("/api/storage/v2/pool/destroy", h.requireAdmin(h.handlePoolDestroyByName))

	// Endpoints añadidos en Fase 7 (Sesión 2) que cerraron los 4 gaps
	// funcionales restantes para que la UI pudiera salir del stack /api/storage/*.
	mux.HandleFunc("/api/storage/v2/observed", h.requireAdmin(h.handleObserved))
	mux.HandleFunc("/api/storage/v2/pools/import", h.requireAdmin(h.handleImport))
	mux.HandleFunc("/api/storage/v2/snapshots/rollback", h.requireAdmin(h.handleSnapshotRollback))
	// /v2/snapshots POST (crear) ya cubierto por handleSnapshots (dispatch GET+POST)
}

// requireAdmin envuelve un handler con verificación de sesión admin.
// Si requireAdmin (definida en sessions.go) devuelve nil, ya ha escrito
// el 401 en w, así que solo retornamos.
func (h *StorageHTTPHandler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAdmin(w, r) == nil {
			return
		}
		next(w, r)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /api/storage/v2/pools — GET (list) | POST (create)
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handlePools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listPools(w, r)
	case http.MethodPost:
		h.createPool(w, r)
	default:
		methodNotAllowed(w, "GET", "POST")
	}
}

// listPools — GET /api/storage/v2/pools
func (h *StorageHTTPHandler) listPools(w http.ResponseWriter, r *http.Request) {
	pools, err := h.service.ListPools(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, pools)
}

// createPool — POST /api/storage/v2/pools
//
// Body:
//   {"name": "data", "profile": "raid1",
//    "device_ids": ["d1", "d2"],
//    "compression": "zstd", "wipe_first": false}
func (h *StorageHTTPHandler) createPool(w http.ResponseWriter, r *http.Request) {
	var req CreatePoolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}

	op, err := h.service.CreatePool(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// 200 OK + Operation. El frontend mira op.Status y op.PoolID
	// para decidir cómo seguir.
	writeData(w, http.StatusOK, op)
}

// ─────────────────────────────────────────────────────────────────────────────
// /api/storage/v2/pools/{id}[/subresource...]
//
// Multiplexa según path y método:
//   GET    /pools/{id}                            → detalle
//   DELETE /pools/{id}                            → destroy
//   POST   /pools/{id}/rename                     → rename
//   POST   /pools/{id}/set-compression            → set compression
//   POST   /pools/{id}/convert-profile            → convert profile
//   POST   /pools/{id}/devices                    → add device
//   DELETE /pools/{id}/devices/{deviceID}         → remove device
//   POST   /pools/{id}/devices/{deviceID}/replace → replace device
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handlePoolByID(w http.ResponseWriter, r *http.Request) {
	id, rest := splitPoolIDPath(r.URL.Path)
	if id == "" {
		writeError(w, ErrCodeBadRequest, "missing pool id in path")
		return
	}

	// Caso 1: /pools/{id}     (sin subrecurso)
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			h.getPool(w, r, id)
		case http.MethodDelete:
			h.destroyPool(w, r, id)
		default:
			methodNotAllowed(w, "GET", "DELETE")
		}
		return
	}

	// Caso 2: /pools/{id}/{subresource...}
	switch {
	case rest == "rename":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.renamePool(w, r, id)

	case rest == "set-compression":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.setCompression(w, r, id)

	case rest == "convert-profile":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.convertProfile(w, r, id)

	case rest == "recovery":
		// STOR-01-C: resolución asistida de un pool en estado recovery.
		// Body: {"action": "accept"} o {"action": "resume"}.
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.resolvePoolRecovery(w, r, id)

	case rest == "balance-status":
		// Progreso del balance en curso (para el polling del frontend
		// durante una conversión de profile async).
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		h.poolBalanceStatus(w, r, id)

	case rest == "devices":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		h.addDevice(w, r, id)

	case strings.HasPrefix(rest, "devices/"):
		// /pools/{id}/devices/{deviceID}[/replace]
		deviceTail := rest[len("devices/"):]
		deviceID, deviceRest := splitFirstSegment(deviceTail)
		if deviceID == "" {
			writeError(w, ErrCodeBadRequest, "missing device id in path")
			return
		}
		switch {
		case deviceRest == "":
			if r.Method != http.MethodDelete {
				methodNotAllowed(w, "DELETE")
				return
			}
			h.removeDevice(w, r, id, deviceID)
		case deviceRest == "replace":
			if r.Method != http.MethodPost {
				methodNotAllowed(w, "POST")
				return
			}
			h.replaceDevice(w, r, id, deviceID)
		default:
			writeError(w, ErrCodeBadRequest, "unknown device subresource")
		}

	default:
		writeError(w, ErrCodeBadRequest, "unknown pool subresource")
	}
}

// ─── /pools/{id} handlers ─────────────────────────────────────────────────────

func (h *StorageHTTPHandler) getPool(w http.ResponseWriter, r *http.Request, id string) {
	pool, err := h.service.GetPool(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, pool)
}

func (h *StorageHTTPHandler) destroyPool(w http.ResponseWriter, r *http.Request, id string) {
	op, err := h.service.DestroyPool(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/rename ───────────────────────────────────────────────────────

type renamePoolRequest struct {
	Name string `json:"name"`
}

func (h *StorageHTTPHandler) renamePool(w http.ResponseWriter, r *http.Request, id string) {
	var req renamePoolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, ErrCodeBadRequest, "name is required")
		return
	}
	op, err := h.service.RenamePool(r.Context(), id, req.Name)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/set-compression ──────────────────────────────────────────────

type setCompressionRequest struct {
	Algorithm string `json:"algorithm"`
}

func (h *StorageHTTPHandler) setCompression(w http.ResponseWriter, r *http.Request, id string) {
	var req setCompressionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Algorithm == "" {
		writeError(w, ErrCodeBadRequest, "algorithm is required")
		return
	}
	op, err := h.service.SetPoolCompression(r.Context(), id, req.Algorithm)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/convert-profile ──────────────────────────────────────────────

type convertProfileBody struct {
	NewProfile Profile `json:"new_profile"`
}

func (h *StorageHTTPHandler) convertProfile(w http.ResponseWriter, r *http.Request, id string) {
	var body convertProfileBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	op, err := h.service.ConvertProfile(r.Context(), ConvertProfileRequest{
		PoolID:     id,
		NewProfile: body.NewProfile,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/recovery ────────────────────────────────────────────────────
// STOR-01-C: resolución asistida de un pool en estado recovery.

type recoveryActionBody struct {
	Action string `json:"action"` // "accept" | "resume"
}

func (h *StorageHTTPHandler) resolvePoolRecovery(w http.ResponseWriter, r *http.Request, id string) {
	var body recoveryActionBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}

	var err error
	switch body.Action {
	case "accept":
		err = resolvePoolRecoveryAccept(r.Context(), id)
	case "resume":
		err = resolvePoolRecoveryResume(r.Context(), id)
	default:
		writeError(w, ErrCodeBadRequest, `action debe ser "accept" o "resume"`)
		return
	}

	if err != nil {
		writeError(w, ErrCodeInternal, err.Error())
		return
	}
	writeData(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"action": body.Action,
	})
}

// ─── /pools/{id}/balance-status ──────────────────────────────────────────────
// Progreso del balance en curso (conversión de profile async).

func (h *StorageHTTPHandler) poolBalanceStatus(w http.ResponseWriter, r *http.Request, id string) {
	pool, err := h.service.GetPool(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, readBalanceStatus(pool.MountPoint))
}

// ─── /pools/{id}/devices ──────────────────────────────────────────────────────

type addDeviceBody struct {
	DeviceID   string `json:"device_id"`
	DevicePath string `json:"device_path,omitempty"`
	WipeFirst  bool   `json:"wipe_first,omitempty"`
}

func (h *StorageHTTPHandler) addDevice(w http.ResponseWriter, r *http.Request, poolID string) {
	var body addDeviceBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if body.DeviceID == "" && body.DevicePath == "" {
		writeError(w, ErrCodeBadRequest, "device_id or device_path is required")
		return
	}
	op, err := h.service.AddDevice(r.Context(), AddDeviceRequest{
		PoolID:     poolID,
		DeviceID:   body.DeviceID,
		DevicePath: body.DevicePath,
		WipeFirst:  body.WipeFirst,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Si la operación terminó en failed (p.ej. el btrfs device add físico
	// falló), NO devolver 200: el cliente debe enterarse para no encadenar
	// un convert sobre un pool que no llegó a crecer.
	if op != nil && op.Status == OpStatusFailed {
		msg := "add device failed"
		if op.Error != nil && *op.Error != "" {
			msg = *op.Error
		}
		writeError(w, ErrCodeBtrfsCommandFailed, msg)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/devices/{deviceID} ───────────────────────────────────────────

func (h *StorageHTTPHandler) removeDevice(w http.ResponseWriter, r *http.Request, poolID, deviceID string) {
	op, err := h.service.RemoveDevice(r.Context(), RemoveDeviceRequest{
		PoolID:   poolID,
		DeviceID: deviceID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─── /pools/{id}/devices/{deviceID}/replace ───────────────────────────────────

type replaceDeviceBody struct {
	NewDeviceID string `json:"new_device_id"`
}

func (h *StorageHTTPHandler) replaceDevice(w http.ResponseWriter, r *http.Request, poolID, oldDeviceID string) {
	var body replaceDeviceBody
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if body.NewDeviceID == "" {
		writeError(w, ErrCodeBadRequest, "new_device_id is required")
		return
	}
	op, err := h.service.ReplaceDevice(r.Context(), ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: oldDeviceID,
		NewDeviceID: body.NewDeviceID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

// ─────────────────────────────────────────────────────────────────────────────
// /api/storage/v2/scan — POST → ScanDevices
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	result, err := h.service.ScanDevices(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/devices[?available=true]
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	ctx := r.Context()
	availableOnly := r.URL.Query().Get("available") == "true"

	var (
		devices []*Device
		err     error
	)
	if availableOnly {
		devices, err = h.service.ListAvailableDevices(ctx)
	} else {
		devices, err = h.service.ListDevices(ctx)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, devices)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/operations[?pool_id=X&status=Y&limit=N]
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handleOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	q := r.URL.Query()
	filter := OperationFilter{}

	if poolID := q.Get("pool_id"); poolID != "" {
		filter.PoolID = &poolID
	}
	if statusStr := q.Get("status"); statusStr != "" {
		s := OperationStatus(statusStr)
		filter.Status = &s
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 0 {
			writeError(w, ErrCodeBadRequest,
				"invalid limit: must be a non-negative integer")
			return
		}
		filter.Limit = n
	}

	ops, err := h.service.ListOperations(r.Context(), filter)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, ops)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/generation
// ─────────────────────────────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handleGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	gen, err := h.service.GetGeneration(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]int64{"generation": gen})
}

// ─────────────────────────────────────────────────────────────────────────────
// Path parsing
// ─────────────────────────────────────────────────────────────────────────────

// splitPoolIDPath extrae el pool ID de un path tipo
// "/api/storage/v2/pools/{id}" o "/api/storage/v2/pools/{id}/subresource/...".
// Devuelve (id, rest) donde rest puede ser vacío o "subresource/...".
// Si el path no comienza con el prefijo esperado, devuelve ("", "").
func splitPoolIDPath(urlPath string) (id, rest string) {
	const prefix = "/api/storage/v2/pools/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", ""
	}
	after := urlPath[len(prefix):]
	if after == "" {
		return "", ""
	}
	return splitFirstSegment(after)
}

// splitFirstSegment toma una cadena tipo "abc/def/ghi" y la divide en
// el primer segmento y el resto: ("abc", "def/ghi"). Si no hay "/",
// devuelve (cadena, "").
func splitFirstSegment(s string) (first, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// decodeJSONBody decodifica el body de la petición en dest. Limita
// el tamaño del body a 64 KB para evitar abuso. Rechaza JSON malformado.
//
// El caller debe validar los CAMPOS de dest (longitudes, no-vacíos, etc.).
// Esta función solo valida que el JSON parsea.
func decodeJSONBody(r *http.Request, dest interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	// 64 KB es de sobra para nuestros payloads (pool name + 4 device IDs)
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // rechaza campos extra → ayuda a detectar typos del cliente
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("invalid JSON body: %v", err)
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Handlers v2 que delegan en funciones BTRFS nativas
// ─────────────────────────────────────────────────────────────────────────
// Estos handlers delegan en funciones BTRFS nativas (startScrub,
// listSnapshots, createSnapshot, rollbackSnapshot, detectStorageDisksGo)
// que viven fuera del Service v2 — en storage_btrfs_features.go y
// storage_startup.go.
//
// Se mantienen así porque no hay problema concreto que justifique
// moverlas al Service. NIMOS_DISCIPLINE: no refactorizar "por consistencia".
// ═══════════════════════════════════════════════════════════════════════════

// ─── GET /v2/capabilities ─────────────────────────────────────────────────
// Devuelve qué backends de storage están disponibles + info del sistema.

func (h *StorageHTTPHandler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeData(w, http.StatusOK, map[string]interface{}{
		"zfs":   false, // Beta 8: ZFS no longer supported
		"btrfs": hasBtrfs,
		"arch":  systemArch,
		"ramGB": systemRamGB,
	})
}

// ─── GET /v2/status ───────────────────────────────────────────────────────
// Stats agregadas del módulo storage. NO duplica /v2/pools.
//
// Respuesta:
//   {
//     "has_pool":       bool,   // ¿hay al menos 1 pool managed?
//     "total_pools":    int,    // pools registrados en SQLite
//     "mounted_pools":  int,    // pools actualmente montados
//     "alerts":         [...]   // alertas activas
//   }
//
// Si el cliente necesita la lista de pools → /v2/pools.
// Si necesita filesystems observados (incluyendo orphans) → /v2/observed.

func (h *StorageHTTPHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	pools, err := h.service.ListPools(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}

	mountedCount := 0
	for _, p := range pools {
		if p.Mounted {
			mountedCount++
		}
	}

	storageAlertsMu.RLock()
	currentAlerts := storageAlertsGo
	storageAlertsMu.RUnlock()

	writeData(w, http.StatusOK, map[string]interface{}{
		"has_pool":      len(pools) > 0,
		"total_pools":   len(pools),
		"mounted_pools": mountedCount,
		"alerts":        currentAlerts,
	})
}

// ─── GET /v2/alerts ───────────────────────────────────────────────────────

func (h *StorageHTTPHandler) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	storageAlertsMu.RLock()
	currentAlerts := storageAlertsGo
	storageAlertsMu.RUnlock()
	writeData(w, http.StatusOK, map[string]interface{}{"alerts": currentAlerts})
}

// ─── GET /v2/disks ────────────────────────────────────────────────────────
// Devuelve discos físicos del sistema en formato CATEGORIZADO:
//
//   {
//     "eligible":    [...discos libres elegibles para crear pool],
//     "nvme":        [...discos NVMe (subset por tecnología)],
//     "usb":         [...discos USB (subset por bus)],
//     "provisioned": [...discos ya asignados a pools]
//   }
//
// Diferencia con /v2/devices:
//   · /v2/devices  → array plano de Device structs (canónico, interno)
//   · /v2/disks    → categorización por estado/tecnología para UI humana
//
// Delega en detectStorageDisks() (storage_disks.go, tipado). La función
// tiene la lógica de categorización: filtrar root disk, marcar los
// que están en pool, separar USB/NVMe, detectar orphan filesystems.
func (h *StorageHTTPHandler) handleDisksCategorized(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	// detectStorageDisks devuelve {eligible, nvme, usb, provisioned}
	writeData(w, http.StatusOK, detectStorageDisks())
}

// ─── POST /v2/wipe ────────────────────────────────────────────────────────
// Body: { "disk": "/dev/sdb" }

type wipeRequest struct {
	Disk  string `json:"disk"`
	Force bool   `json:"force"` // permite wipe de orphans (NO managed pools)
}

func (h *StorageHTTPHandler) handleWipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req wipeRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Disk == "" {
		writeError(w, ErrCodeBadRequest, "disk path is required")
		return
	}
	var result map[string]interface{}
	if req.Force {
		result = wipeDiskForce(req.Disk)
	} else {
		result = wipeDiskGo(req.Disk)
	}
	// wipeDiskGo devuelve {"ok": bool, "error"?: string}
	if errStr, _ := result["error"].(string); errStr != "" {
		writeError(w, ErrCodeBtrfsCommandFailed, errStr)
		return
	}
	writeData(w, http.StatusOK, result)
}

// ─── POST /v2/scrub ───────────────────────────────────────────────────────
// Body: { "pool": "data" }

type scrubRequest struct {
	Pool string `json:"pool"`
}

func (h *StorageHTTPHandler) handleScrubAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req scrubRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Pool == "" {
		writeError(w, ErrCodeBadRequest, "pool is required")
		return
	}
	result := startScrub(map[string]interface{}{"pool": req.Pool})
	if errStr, _ := result["error"].(string); errStr != "" {
		writeError(w, ErrCodeBtrfsCommandFailed, errStr)
		return
	}
	writeData(w, http.StatusOK, result)
}

// ─── GET /v2/scrub/status?pool=NAME ───────────────────────────────────────

func (h *StorageHTTPHandler) handleScrubStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	pool := r.URL.Query().Get("pool")
	if pool == "" {
		writeError(w, ErrCodeBadRequest, "pool query parameter is required")
		return
	}
	writeData(w, http.StatusOK, getScrubStatus(pool))
}

// ─── /v2/snapshots ────────────────────────────────────────────────────────
// GET  ?pool=NAME   → lista snapshots (delegado a listSnapshots, BTRFS nativo)
// POST {pool,name}  → crea snapshot (delegado a createSnapshot, BTRFS nativo)
//
// Snapshots implementados sobre subvolúmenes BTRFS read-only en
// <mountpoint>/.snapshots/. Funciones en storage_btrfs_features.go.

func (h *StorageHTTPHandler) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pool := r.URL.Query().Get("pool")
		if pool == "" {
			writeError(w, ErrCodeBadRequest, "pool query parameter is required")
			return
		}
		writeData(w, http.StatusOK, listSnapshots(pool))
	case http.MethodPost:
		var body map[string]interface{}
		if err := decodeJSONBody(r, &body); err != nil {
			writeError(w, ErrCodeBadRequest, err.Error())
			return
		}
		result := createSnapshot(body)
		if errStr, _ := result["error"].(string); errStr != "" {
			writeError(w, ErrCodeBtrfsCommandFailed, errStr)
			return
		}
		writeData(w, http.StatusOK, result)
	default:
		methodNotAllowed(w, "GET", "POST")
	}
}

// ─── POST /v2/pool/export ─────────────────────────────────────────────────
// Body: { "name": "data" }
// "Export" = unmount + remove from config, conservando datos en discos.

type poolByNameRequest struct {
	Name string `json:"name"`
}

func (h *StorageHTTPHandler) handlePoolExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req poolByNameRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, ErrCodeBadRequest, "pool name is required")
		return
	}
	result := exportPoolBtrfs(req.Name)
	if errStr, _ := result["error"].(string); errStr != "" {
		writeError(w, ErrCodeBtrfsCommandFailed, errStr)
		return
	}
	writeData(w, http.StatusOK, result)
}

// ─── POST /v2/pool/destroy ────────────────────────────────────────────────
// Variante by-name (la UI vieja lo llama así). Destruye el pool y borra los datos.
// La variante by-id (DELETE /v2/pools/{id}) ya existe arriba.

func (h *StorageHTTPHandler) handlePoolDestroyByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req poolByNameRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, ErrCodeBadRequest, "pool name is required")
		return
	}
	result := destroyPoolBtrfs(req.Name)
	if errStr, _ := result["error"].(string); errStr != "" {
		msg := errStr
		if m, ok := result["message"].(string); ok && m != "" {
			msg = m
		}
		writeError(w, errStr, msg)
		return
	}
	writeData(w, http.StatusOK, result)
}

// ─── GET /v2/observed ─────────────────────────────────────────────────────
// Fase 7 Bloque C1 · Observed state del storage.
// Devuelve el snapshot del observer (BTRFS detectados, devices físicos,
// divergencias vs managed).
//
// Query param: ?refresh=true fuerza re-scan inmediato (escape hatch).
//
// Status codes:
//   200 OK            snapshot devuelto en {data: {...}}
//   503 Unavailable   observer no inicializado (boot incompleto)

func (h *StorageHTTPHandler) handleObserved(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if globalObserver == nil {
		// 503 no tiene mapping en httpStatusForCode (no es ErrCode semántico).
		// Escribimos directamente con writeJSON.
		writeJSON(w, http.StatusServiceUnavailable, apiErrorResponse{
			Error: apiError{Code: ErrCodeInternal, Message: "storage observer not running"},
		})
		return
	}
	if r.URL.Query().Get("refresh") == "true" {
		globalObserver.InvalidateNow()
		// Espera breve para que el scan se complete (best-effort).
		// El cliente puede hacer otra GET sin ?refresh para tener la
		// versión nueva si esta llega antes de tiempo.
		time.Sleep(200 * time.Millisecond)
	}
	writeData(w, http.StatusOK, globalObserver.Snapshot())
}

// ─── POST /v2/pools/import ────────────────────────────────────────────────
// Fase 7 Bloque C3.1 · Importar filesystem BTRFS detectado por el observer
// como pool gestionado.
//
// Body: {"uuid": "<btrfs-uuid>", "name": "<pool-name>"}
//
// Pre-requisito: el filesystem debe aparecer en /v2/observed con
// is_managed=false (orphan) y devices completos.
//
// Códigos devueltos por importPoolBtrfs (storage_btrfs_import.go):
//   sin code, solo error → validación inputs (400)
//   FS_NOT_OBSERVED      → pool_not_found (404)
//   ALREADY_MANAGED      → pool_name_taken (409)
//   FS_INCOMPLETE        → device_missing (409)
//   NAME_TAKEN           → pool_name_taken (409)
//   sin code (mount/btrfs)→ btrfs_command_failed (500)

func (h *StorageHTTPHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]interface{}
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	result := importPoolBtrfs(body)
	errMsg, hasErr := result["error"].(string)
	if !hasErr || errMsg == "" {
		writeData(w, http.StatusOK, result)
		return
	}
	// Hay error. Si trae code semántico, lo mapeamos. Si no, es error
	// de validación de inputs (uuid/name vacíos, nombre inválido) → 400.
	code, _ := result["code"].(string)
	switch code {
	case "FS_NOT_OBSERVED":
		writeError(w, ErrCodePoolNotFound, errMsg)
	case "ALREADY_MANAGED", "NAME_TAKEN":
		writeError(w, ErrCodePoolNameTaken, errMsg)
	case "FS_INCOMPLETE":
		writeError(w, ErrCodeDeviceMissing, errMsg)
	case "":
		// Sin code: errores de validación de inputs o BTRFS subyacente.
		// Los de validación tienen mensajes específicos; los demás son fallos
		// de comando (mount, btrfs). Distinguimos por prefijo heurístico.
		if strings.Contains(errMsg, "is required") ||
			strings.Contains(errMsg, "Invalid pool name") ||
			strings.Contains(errMsg, "observer") {
			writeError(w, ErrCodeBadRequest, errMsg)
		} else {
			writeError(w, ErrCodeBtrfsCommandFailed, errMsg)
		}
	default:
		writeError(w, ErrCodeBtrfsCommandFailed, errMsg)
	}
}

// ─── POST /v2/snapshots/rollback ──────────────────────────────────────────
// Rollback un snapshot a su pool de origen.
// Body: {"pool": "name", "snapshot": "snapshot-name"}
//
// Delegado a rollbackSnapshot (BTRFS nativo, storage_btrfs_features.go).

func (h *StorageHTTPHandler) handleSnapshotRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]interface{}
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, ErrCodeBadRequest, err.Error())
		return
	}
	result := rollbackSnapshot(body)
	if errStr, _ := result["error"].(string); errStr != "" {
		writeError(w, ErrCodeBtrfsCommandFailed, errStr)
		return
	}
	writeData(w, http.StatusOK, result)
}
