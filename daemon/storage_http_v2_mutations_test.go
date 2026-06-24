// storage_http_v2_mutations_test.go — Tests de los handlers HTTP de
// mutación (POST/DELETE) añadidos en Bloque 4.
//
// Cubrimos:
//   - POST   /pools                          → CreatePool
//   - DELETE /pools/{id}                     → DestroyPool
//   - POST   /pools/{id}/rename              → RenamePool
//   - POST   /pools/{id}/set-compression     → SetPoolCompression
//   - POST   /pools/{id}/devices             → AddDevice
//   - DELETE /pools/{id}/devices/{deviceID}  → RemoveDevice
//   - POST   /pools/{id}/devices/{id}/replace → ReplaceDevice
//   - POST   /pools/{id}/convert-profile     → ConvertProfile
//   - POST   /scan                           → ScanDevices

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper para registrar devices y crear un pool listo para mutar
// ─────────────────────────────────────────────────────────────────────────────

func setupPoolForMutations(t *testing.T, service *StorageService, numDevices int) (poolID string, deviceIDs []string) {
	t.Helper()
	deviceIDs = registerTestDevices(t, service, numDevices)
	op, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name:      "mutbase",
		Profile:   ProfileRaid1,
		DeviceIDs: deviceIDs[:2], // pool inicial con 2 discos
	})
	if err != nil {
		t.Fatalf("setupPoolForMutations: %v", err)
	}
	poolID = *op.PoolID
	return
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools — CreatePool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPCreatePoolHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	devIDs := registerTestDevices(t, service, 2)

	body := `{"name":"data","profile":"raid1","device_ids":["` + devIDs[0] + `","` + devIDs[1] + `"]}`
	rec := doRequest(handler.handlePools, "POST", "/api/storage/v2/pools", body)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var op Operation
	decodeData(t, rec, &op)
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}
	if op.PoolID == nil {
		t.Error("op.PoolID should be set")
	}
}

func TestStorageHTTPCreatePoolBadJSON(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePools, "POST", "/api/storage/v2/pools", `{not valid`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeBadRequest {
		t.Errorf("code: got %q", code)
	}
}

func TestStorageHTTPCreatePoolUnknownField(t *testing.T) {
	// DisallowUnknownFields detecta typos en el cliente
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	body := `{"name":"data","profile":"raid1","device_ids":["a","b"],"unknown":"x"}`
	rec := doRequest(handler.handlePools, "POST", "/api/storage/v2/pools", body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestStorageHTTPCreatePoolInvalidProfile(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	devIDs := registerTestDevices(t, service, 2)
	body := `{"name":"data","profile":"raid5","device_ids":["` + devIDs[0] + `","` + devIDs[1] + `"]}`
	rec := doRequest(handler.handlePools, "POST", "/api/storage/v2/pools", body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 for invalid profile", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeProfileInvalid {
		t.Errorf("code: got %q, want %q", code, ErrCodeProfileInvalid)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/storage/v2/pools/{id} — DestroyPool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPDestroyPoolHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "DELETE",
		"/api/storage/v2/pools/"+poolID, "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var op Operation
	decodeData(t, rec, &op)
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}
}

func TestStorageHTTPDestroyPoolNotFound(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "DELETE",
		"/api/storage/v2/pools/nonexistent", "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools/{id}/rename — RenamePool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPRenamePoolHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/rename",
		`{"name":"newname"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	pool, _ := service.GetPool(context.Background(), poolID)
	if pool.Name != "newname" {
		t.Errorf("pool name: got %q, want newname", pool.Name)
	}
}

func TestStorageHTTPRenamePoolMissingName(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/rename", `{}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools/{id}/set-compression
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPSetCompressionHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/set-compression",
		`{"algorithm":"zstd:3"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	pool, _ := service.GetPool(context.Background(), poolID)
	if pool.Compression != "zstd:3" {
		t.Errorf("compression: got %q", pool.Compression)
	}
}

func TestStorageHTTPSetCompressionMissingAlgorithm(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/set-compression", `{}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools/{id}/devices — AddDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPAddDeviceHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, _ := setupPoolForMutations(t, service, 2)
	// Disco extra
	extraIDs := registerTestDevices(t, service, 1)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/devices",
		`{"device_id":"`+extraIDs[0]+`"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	pool, _ := service.GetPool(context.Background(), poolID)
	if len(pool.Devices) != 3 {
		t.Errorf("devices in pool: got %d, want 3", len(pool.Devices))
	}
}

func TestStorageHTTPAddDeviceMissingDeviceID(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/devices", `{}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/storage/v2/pools/{id}/devices/{deviceID} — RemoveDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPRemoveDeviceHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Crear pool con 3 discos para poder quitar 1
	devIDs := registerTestDevices(t, service, 3)
	op, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name:      "rmtest",
		Profile:   ProfileRaid1,
		DeviceIDs: devIDs,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	poolID := *op.PoolID

	rec := doRequest(handler.handlePoolByID, "DELETE",
		"/api/storage/v2/pools/"+poolID+"/devices/"+devIDs[0], "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	pool, _ := service.GetPool(context.Background(), poolID)
	if len(pool.Devices) != 2 {
		t.Errorf("devices: got %d, want 2", len(pool.Devices))
	}
}

func TestStorageHTTPRemoveDeviceMinDisksReached(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, devIDs := setupPoolForMutations(t, service, 2) // raid1 con 2 discos

	rec := doRequest(handler.handlePoolByID, "DELETE",
		"/api/storage/v2/pools/"+poolID+"/devices/"+devIDs[0], "")

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeMinDisksReached {
		t.Errorf("code: got %q, want %q", code, ErrCodeMinDisksReached)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools/{id}/devices/{deviceID}/replace — ReplaceDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPReplaceDeviceHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	poolID, devIDs := setupPoolForMutations(t, service, 2)
	// Disco nuevo (más grande o igual que los del pool)
	newIDs := registerTestDevices(t, service, 1)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/devices/"+devIDs[0]+"/replace",
		`{"new_device_id":"`+newIDs[0]+`"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestStorageHTTPReplaceDeviceMissingNewDeviceID(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	poolID, devIDs := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/devices/"+devIDs[0]+"/replace",
		`{}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/pools/{id}/convert-profile — ConvertProfile
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPConvertProfileHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Crear single con 2 discos, convertir a raid1
	devIDs := registerTestDevices(t, service, 2)
	op, _ := service.CreatePool(context.Background(), CreatePoolRequest{
		Name:      "convtest",
		Profile:   ProfileSingle,
		DeviceIDs: devIDs,
	})
	poolID := *op.PoolID

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/convert-profile",
		`{"new_profile":"raid1"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Modelo ASYNC: el endpoint devuelve la op in_progress; el balance corre
	// en background. Esperar a que complete antes de verificar el profile.
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Data.ID == "" {
		t.Fatalf("no se pudo extraer op.ID de la respuesta: %v (body: %s)", err, rec.Body.String())
	}
	waitForOperation(t, service, context.Background(), resp.Data.ID, 3*time.Second)

	pool, _ := service.GetPool(context.Background(), poolID)
	if pool.Profile != ProfileRaid1 {
		t.Errorf("profile: got %q", pool.Profile)
	}
}

func TestStorageHTTPConvertProfileSameProfile(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()
	poolID, _ := setupPoolForMutations(t, service, 2)

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/"+poolID+"/convert-profile",
		`{"new_profile":"raid1"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (same profile)", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/storage/v2/scan — ScanDevices
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPScanDevicesHappy(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	// Configurar scanner para que reporte 1 disco
	scanner, ok := service.scanner.(*MockDeviceScanner)
	if !ok {
		t.Fatal("expected MockDeviceScanner in test setup")
	}
	scanner.Devices = []ScannedDevice{
		{Name: "sdb", DevicePath: "/dev/sdb", ByIDPath: "/dev/disk/by-id/x",
			Serial: "ABC", SizeBytes: 1e12},
	}

	rec := doRequest(handler.handleScan, "POST", "/api/storage/v2/scan", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var result ScanResult
	decodeData(t, rec, &result)
	if result.Inserted != 1 {
		t.Errorf("inserted: got %d, want 1", result.Inserted)
	}
}

func TestStorageHTTPScanDevicesWrongMethod(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handleScan, "GET", "/api/storage/v2/scan", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Path parsing avanzado
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPPoolByIDUnknownSubresource(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "POST",
		"/api/storage/v2/pools/p1/nonsense", `{}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (unknown subresource)", rec.Code)
	}
}

func TestStorageHTTPDeviceSubresourceMissingID(t *testing.T) {
	handler, _, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	rec := doRequest(handler.handlePoolByID, "DELETE",
		"/api/storage/v2/pools/p1/devices/", "")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (missing device id)", rec.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Service error propagation: el code semántico debe convertirse en HTTP
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageHTTPCreatePoolDeviceInUseReturns409(t *testing.T) {
	handler, service, _, cleanup := setupTestHTTP(t)
	defer cleanup()

	devIDs := registerTestDevices(t, service, 2)
	// Crear primer pool con esos discos
	_, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name: "first", Profile: ProfileRaid1, DeviceIDs: devIDs,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Intentar crear otro pool con los mismos discos → device_in_use
	body := `{"name":"second","profile":"raid1","device_ids":["` + devIDs[0] + `","` + devIDs[1] + `"]}`
	rec := doRequest(handler.handlePools, "POST", "/api/storage/v2/pools", body)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", rec.Code)
	}
	code, _ := decodeError(t, rec)
	if code != ErrCodeDeviceInUse {
		t.Errorf("code: got %q, want %q", code, ErrCodeDeviceInUse)
	}
}
