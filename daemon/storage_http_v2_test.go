// storage_http_v2_test.go — Tests de los handlers HTTP de queries.
//
// Usamos httptest.NewRecorder para capturar las respuestas sin levantar
// un servidor real. Cada test:
//   1. Crea un StorageService con DB temporal y mock executor
//   2. Pre-puebla la DB con el estado que quiere testear
//   3. Construye un http.Request con httptest.NewRequest
//   4. Llama al handler directamente
//   5. Inspecciona el ResponseRecorder

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper: setup completo de handler + service
// ─────────────────────────────────────────────────────────────────────────────

func setupTestHTTP(t *testing.T) (*StorageHTTPHandler, *StorageService, *MockBtrfsExecutor, func()) {
	t.Helper()
	service, mock, cleanup := setupTestService(t)
	handler := NewStorageHTTPHandler(service)
	return handler, service, mock, cleanup
}

// doRequest ejecuta un request contra el handler dado y devuelve la respuesta.
func doRequest(handler http.HandlerFunc, method, path string, body string) *httptest.ResponseRecorder {
	var bodyReader = strings.NewReader(body)
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// decodeData extrae el campo "data" de una respuesta exitosa.
func decodeData(t *testing.T, rec *httptest.ResponseRecorder, dest interface{}) {
	t.Helper()
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if err := json.Unmarshal(wrapper.Data, dest); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

// decodeError extrae el code/message de una respuesta de error.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wrapper); err != nil {
		t.Fatalf("decode error response: %v (body: %s)", err, rec.Body.String())
	}
	return wrapper.Error.Code, wrapper.Error.Message
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/pools
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPListPoolsEmpty(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePools, "GET", "/api/storage/v2/pools", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}

	var pools []*Pool
	decodeData(t, rec, &pools)
	if len(pools) != 0 {
		t.Errorf("got %d pools, want 0", len(pools))
	}
}

func TestStorageHTTPListPoolsWithData(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	// Pre-poblar
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileRaid1, MountPoint: "/m",
	})
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p2", Name: "backups", BtrfsUUID: "u2",
		Profile: ProfileSingle, MountPoint: "/b",
	})
	tx.Commit()

	rec := doRequest(handler.handlePools, "GET", "/api/storage/v2/pools", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d", rec.Code)
	}

	var pools []*Pool
	decodeData(t, rec, &pools)
	if len(pools) != 2 {
		t.Errorf("got %d pools, want 2", len(pools))
	}
}

func TestStorageHTTPListPoolsMethodNotAllowed(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePools, "PUT", "/api/storage/v2/pools", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
	// El handler ahora acepta GET y POST
	if got := rec.Header().Get("Allow"); got != "GET, POST" {
		t.Errorf("Allow header: got %q, want %q", got, "GET, POST")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/pools/{id}
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPGetPoolFound(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileRaid1, MountPoint: "/m",
	})
	tx.Commit()

	rec := doRequest(handler.handlePoolByID, "GET", "/api/storage/v2/pools/p1", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var pool Pool
	decodeData(t, rec, &pool)
	if pool.ID != "p1" || pool.Name != "data" {
		t.Errorf("unexpected pool: %+v", pool)
	}
}

func TestStorageHTTPGetPoolNotFound(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "GET", "/api/storage/v2/pools/nonexistent", "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodePoolNotFound {
		t.Errorf("error code: got %q, want %q", code, ErrCodePoolNotFound)
	}
}

func TestStorageHTTPGetPoolMissingIDInPath(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "GET", "/api/storage/v2/pools/", "")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestStorageHTTPGetPoolSubresourceWithGETRejected(t *testing.T) {
	// /pools/{id}/devices con GET no está permitido (devices es POST add)
	// → debe devolver 405 method not allowed, no 400
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "GET",
		"/api/storage/v2/pools/p1/devices", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/devices
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPListDevicesAll(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1",
		CurrentPath: "/dev/sda", SizeBytes: 1e12,
	})
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d2", Serial: "S2", ByIDPath: "/dev/disk/by-id/s2",
		CurrentPath: "/dev/sdb", SizeBytes: 1e12,
	})
	tx.Commit()

	rec := doRequest(handler.handleDevices, "GET", "/api/storage/v2/devices", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d", rec.Code)
	}
	var devs []*Device
	decodeData(t, rec, &devs)
	if len(devs) != 2 {
		t.Errorf("got %d devices, want 2", len(devs))
	}
}

func TestStorageHTTPListDevicesAvailableOnly(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	// 2 devices, 1 asignado a un pool
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1",
		CurrentPath: "/dev/sda", SizeBytes: 1e12,
	})
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d2", Serial: "S2", ByIDPath: "/dev/disk/by-id/s2",
		CurrentPath: "/dev/sdb", SizeBytes: 1e12,
	})
	service.repo.AssignDeviceToPool(ctx, tx, "p1", "d1")
	tx.Commit()

	// available=true → solo d2
	rec := doRequest(handler.handleDevices, "GET",
		"/api/storage/v2/devices?available=true", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d", rec.Code)
	}
	var devs []*Device
	decodeData(t, rec, &devs)
	if len(devs) != 1 {
		t.Errorf("got %d devices, want 1 (d2 only)", len(devs))
	}
	if len(devs) > 0 && devs[0].ID != "d2" {
		t.Errorf("got device %q, want d2", devs[0].ID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/operations
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPListOperations(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	// Crear un pool y luego renombrarlo → genera 1 operation completed
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "old", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	tx.Commit()

	_, err := service.RenamePool(ctx, "p1", "new")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	rec := doRequest(handler.handleOperations, "GET", "/api/storage/v2/operations", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d", rec.Code)
	}
	var ops []*Operation
	decodeData(t, rec, &ops)
	if len(ops) != 1 {
		t.Errorf("got %d operations, want 1", len(ops))
	}
	if len(ops) > 0 && ops[0].Type != OpTypeRenamePool {
		t.Errorf("op type: got %q", ops[0].Type)
	}
}

func TestStorageHTTPListOperationsFilterByStatus(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	// Inyectar 2 ops con statuses distintos
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "x", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	poolID := "p1"
	service.repo.CreateOperation(ctx, tx, &Operation{
		ID: "op-1", Type: OpTypeAddDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
	})
	tx.Commit()

	// Sin filtro: 1 op
	rec := doRequest(handler.handleOperations, "GET",
		"/api/storage/v2/operations", "")
	var all []*Operation
	decodeData(t, rec, &all)
	if len(all) != 1 {
		t.Errorf("no filter: got %d ops, want 1", len(all))
	}

	// Filtro status=completed: 0 ops
	rec2 := doRequest(handler.handleOperations, "GET",
		"/api/storage/v2/operations?status=completed", "")
	var completed []*Operation
	decodeData(t, rec2, &completed)
	if len(completed) != 0 {
		t.Errorf("filter completed: got %d ops, want 0", len(completed))
	}

	// Filtro status=in_progress: 1 op
	rec3 := doRequest(handler.handleOperations, "GET",
		"/api/storage/v2/operations?status=in_progress", "")
	var inProgress []*Operation
	decodeData(t, rec3, &inProgress)
	if len(inProgress) != 1 {
		t.Errorf("filter in_progress: got %d ops, want 1", len(inProgress))
	}
}

func TestStorageHTTPListOperationsInvalidLimit(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleOperations, "GET",
		"/api/storage/v2/operations?limit=abc", "")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeBadRequest {
		t.Errorf("code: got %q", code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/storage/v2/generation
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPGetGeneration(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleGeneration, "GET",
		"/api/storage/v2/generation", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d", rec.Code)
	}
	var resp map[string]int64
	decodeData(t, rec, &resp)
	if _, ok := resp["generation"]; !ok {
		t.Errorf("response missing 'generation' field: %+v", resp)
	}
}

func TestStorageHTTPGetGenerationIncrementsOnMutation(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	ctx := context.Background()

	rec1 := doRequest(handler.handleGeneration, "GET",
		"/api/storage/v2/generation", "")
	var resp1 map[string]int64
	decodeData(t, rec1, &resp1)
	gen0 := resp1["generation"]

	// Mutar algo
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "x", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	tx.Commit()

	rec2 := doRequest(handler.handleGeneration, "GET",
		"/api/storage/v2/generation", "")
	var resp2 map[string]int64
	decodeData(t, rec2, &resp2)
	gen1 := resp2["generation"]

	if gen1 <= gen0 {
		t.Errorf("generation should increase after mutation: %d → %d", gen0, gen1)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP status mapping
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPStatusForCode(t *testing.T) {
	cases := []struct {
		code     string
		expected int
	}{
		{ErrCodePoolNotFound, 404},
		{ErrCodeDeviceNotFound, 404},
		{ErrCodeBadRequest, 400},
		{ErrCodeProfileInvalid, 400},
		{ErrCodePoolObserved, 403},
		{ErrCodeCapabilityMissing, 403},
		{ErrCodePoolNameTaken, 409},
		{ErrCodeDeviceInUse, 409},
		{ErrCodeOperationInProgress, 409},
		{ErrCodeMinDisksReached, 409},
		{ErrCodeDeviceNotEligible, 409},
		{ErrCodeInsufficientDisks, 409},
		{ErrCodeBtrfsCommandFailed, 500},
		{ErrCodeMountFailed, 500},
		{ErrCodeInternal, 500},
		{"unknown_code", 500}, // default
	}
	for _, c := range cases {
		got := httpStatusForCode(c.code)
		if got != c.expected {
			t.Errorf("httpStatusForCode(%q): got %d, want %d",
				c.code, got, c.expected)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Path parsing
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPSplitPoolIDPath(t *testing.T) {
	cases := []struct {
		input    string
		wantID   string
		wantRest string
	}{
		{"/api/storage/v2/pools/abc-123", "abc-123", ""},
		{"/api/storage/v2/pools/abc-123/devices", "abc-123", "devices"},
		{"/api/storage/v2/pools/abc-123/devices/d1", "abc-123", "devices/d1"},
		{"/api/storage/v2/pools/", "", ""},
		{"/api/storage/v2/pools", "", ""},
		{"/api/storage/v2/something/else", "", ""},
	}
	for _, c := range cases {
		gotID, gotRest := splitPoolIDPath(c.input)
		if gotID != c.wantID || gotRest != c.wantRest {
			t.Errorf("splitPoolIDPath(%q) = (%q,%q), want (%q,%q)",
				c.input, gotID, gotRest, c.wantID, c.wantRest)
		}
	}
}
