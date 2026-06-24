// storage_http_v2_session2_test.go — Tests de los 4 endpoints nuevos
// añadidos en Sesión 2 (Fase 7) para cerrar paridad v2 ↔ legacy:
//
//   1. GET  /api/storage/v2/observed
//   2. POST /api/storage/v2/pools/import
//   3. POST /api/storage/v2/snapshots
//   4. POST /api/storage/v2/snapshots/rollback
//
// Y tests de la integración C3.4 en createPool v2 (V2.2):
//   · ErrDiskHasFilesystem se mapea a 409 + error.details estructurado

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ═════════════════════════════════════════════════════════════════════════════
// V2.1.1 · GET /api/storage/v2/observed
// ═════════════════════════════════════════════════════════════════════════════

func TestStorageHTTPObservedNoObserver(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Garantizar que el observer global está nil
	savedObs := globalObserver
	globalObserver = nil
	defer func() { globalObserver = savedObs }()

	rec := doRequest(handler.handleObserved, "GET", "/api/storage/v2/observed", "")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestStorageHTTPObservedHappy(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Mockear observer con un snapshot mínimo
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()

	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{
		Generation:  42,
		Timestamp:   time.Now().UTC(),
		Filesystems: []ObservedBtrfs{},
	})
	o.generation.Store(42)
	globalObserver = o

	rec := doRequest(handler.handleObserved, "GET", "/api/storage/v2/observed", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}

	// Verificar que viene envuelto en {data: ...} con generation correcto
	var wrapper struct {
		Data struct {
			Generation uint64 `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrapper.Data.Generation != 42 {
		t.Errorf("generation: got %d, want 42", wrapper.Data.Generation)
	}
}

func TestStorageHTTPObservedMethodNotAllowed(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleObserved, "POST", "/api/storage/v2/observed", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET" {
		t.Errorf("Allow header: got %q, want GET", allow)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// V2.1.2 · POST /api/storage/v2/pools/import
// ═════════════════════════════════════════════════════════════════════════════

func TestStorageHTTPImportMissingUUID(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Asegurar observer no-nil para que importPoolBtrfs no aborte por eso
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()
	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{Filesystems: []ObservedBtrfs{}})
	globalObserver = o

	rec := doRequest(handler.handleImport, "POST", "/api/storage/v2/pools/import",
		`{"name":"test"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeBadRequest {
		t.Errorf("error.code: got %q, want %q", code, ErrCodeBadRequest)
	}
}

func TestStorageHTTPImportFSNotObserved(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Observer activo pero sin el FS solicitado
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()
	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{Filesystems: []ObservedBtrfs{}})
	globalObserver = o

	rec := doRequest(handler.handleImport, "POST", "/api/storage/v2/pools/import",
		`{"uuid":"00000000-0000-0000-0000-000000000000","name":"newpool"}`)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodePoolNotFound {
		t.Errorf("error.code: got %q, want %q", code, ErrCodePoolNotFound)
	}
}

func TestStorageHTTPImportMethodNotAllowed(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleImport, "GET", "/api/storage/v2/pools/import", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// V2.1.3 · POST /api/storage/v2/snapshots
// (GET sobre la misma ruta sigue funcionando — dispatch interno)
// ═════════════════════════════════════════════════════════════════════════════

func TestStorageHTTPSnapshotsGETStillWorks(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshots, "GET",
		"/api/storage/v2/snapshots?pool=test", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

func TestStorageHTTPSnapshotsGETMissingPool(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshots, "GET",
		"/api/storage/v2/snapshots", "")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestStorageHTTPSnapshotsPOSTBadJSON(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshots, "POST",
		"/api/storage/v2/snapshots", `not-json`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestStorageHTTPSnapshotsMethodNotAllowed(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshots, "DELETE",
		"/api/storage/v2/snapshots", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Errorf("Allow header: got %q, want GET, POST", allow)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// V2.1.4 · POST /api/storage/v2/snapshots/rollback
// ═════════════════════════════════════════════════════════════════════════════

func TestStorageHTTPSnapshotRollbackMethodNotAllowed(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshotRollback, "GET",
		"/api/storage/v2/snapshots/rollback", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

func TestStorageHTTPSnapshotRollbackBadJSON(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleSnapshotRollback, "POST",
		"/api/storage/v2/snapshots/rollback", `{broken`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// V2.2 · Integración C3.4 — writeServiceError reconoce ErrDiskHasFilesystem
// ═════════════════════════════════════════════════════════════════════════════

func TestWriteServiceError_ErrDiskHasFilesystem_ManagedPool(t *testing.T) {
	rec := httptest.NewRecorder()
	fsErr := &ErrDiskHasFilesystem{
		Disk:              "/dev/sda",
		FSType:            "btrfs",
		FSUUID:            "abc-uuid",
		FSLabel:           "data",
		IsManaged:         true,
		PoolID:            "pool-id-123",
		PoolName:          "data",
		ObservationHealth: "healthy",
	}

	writeServiceError(rec, fsErr)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}

	// Decodificar payload completo incluyendo details
	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Disk      string `json:"disk"`
				FSType    string `json:"fs_type"`
				IsManaged bool   `json:"is_managed"`
				PoolName  string `json:"pool_name"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if wrapper.Error.Code != "DISK_HAS_FILESYSTEM" {
		t.Errorf("code: got %q, want DISK_HAS_FILESYSTEM", wrapper.Error.Code)
	}
	if wrapper.Error.Details.Disk != "/dev/sda" {
		t.Errorf("details.disk: got %q, want /dev/sda", wrapper.Error.Details.Disk)
	}
	if !wrapper.Error.Details.IsManaged {
		t.Errorf("details.is_managed: got false, want true")
	}
	if wrapper.Error.Details.PoolName != "data" {
		t.Errorf("details.pool_name: got %q, want 'data'", wrapper.Error.Details.PoolName)
	}
}

func TestWriteServiceError_ErrDiskHasFilesystem_Orphan(t *testing.T) {
	rec := httptest.NewRecorder()
	fsErr := &ErrDiskHasFilesystem{
		Disk:      "/dev/sdb",
		FSType:    "btrfs",
		FSUUID:    "orphan-uuid",
		FSLabel:   "old-data",
		IsManaged: false,
	}

	writeServiceError(rec, fsErr)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}

	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				IsManaged bool `json:"is_managed"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrapper.Error.Code != "DISK_HAS_FILESYSTEM" {
		t.Errorf("code: got %q, want DISK_HAS_FILESYSTEM", wrapper.Error.Code)
	}
	if wrapper.Error.Details.IsManaged {
		t.Errorf("details.is_managed: got true, want false (orphan)")
	}
}

// TestCreatePool_PreflightBlocksDataLoss verifica que CreatePool aborta cuando
// el deviceChecker reporta ErrDiskHasFilesystem. El error fluye intacto a
// writeServiceError → 409 + details estructurados.
func TestCreatePool_PreflightBlocksDataLoss(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Inyectar checker que SIEMPRE reporta filesystem managed
	service.deviceChecker = func(devices []*Device) error {
		if len(devices) == 0 {
			return nil
		}
		return &ErrDiskHasFilesystem{
			Disk:      devices[0].CurrentPath,
			FSType:    "btrfs",
			FSUUID:    "preexisting-uuid",
			FSLabel:   "old-pool",
			IsManaged: true,
			PoolName:  "old-pool",
		}
	}

	// Preparar devices en DB (sin entrar en demasiados detalles — esto solo
	// debe llegar hasta el checkDevicesAvailable y abortar)
	ctx := context.Background()
	tx, _ := service.db.BeginTx(ctx, nil)
	dev := &Device{
		ID: "dev-test-pf-1", Serial: "PF-SERIAL-1",
		ByIDPath: "/dev/disk/by-id/test-pf-1", CurrentPath: "/dev/loopX",
		Model: "test", SizeBytes: 1e10,
	}
	if _, err := service.repo.UpsertDevice(ctx, tx, dev); err != nil {
		t.Fatalf("create device 1: %v", err)
	}
	dev2 := &Device{
		ID: "dev-test-pf-2", Serial: "PF-SERIAL-2",
		ByIDPath: "/dev/disk/by-id/test-pf-2", CurrentPath: "/dev/loopY",
		Model: "test", SizeBytes: 1e10,
	}
	if _, err := service.repo.UpsertDevice(ctx, tx, dev2); err != nil {
		t.Fatalf("create device 2: %v", err)
	}
	tx.Commit()

	body := `{"name":"test-pf","profile":"raid1","device_ids":["dev-test-pf-1","dev-test-pf-2"]}`
	rec := doRequest(handler.createPool, "POST", "/api/storage/v2/pools", body)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				IsManaged bool   `json:"is_managed"`
				PoolName  string `json:"pool_name"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrapper.Error.Code != "DISK_HAS_FILESYSTEM" {
		t.Errorf("code: got %q, want DISK_HAS_FILESYSTEM", wrapper.Error.Code)
	}
	if !wrapper.Error.Details.IsManaged {
		t.Errorf("details.is_managed: got false, want true")
	}
	if wrapper.Error.Details.PoolName != "old-pool" {
		t.Errorf("details.pool_name: got %q, want old-pool", wrapper.Error.Details.PoolName)
	}
}
