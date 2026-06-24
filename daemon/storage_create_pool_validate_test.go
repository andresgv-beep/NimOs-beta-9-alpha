// storage_create_pool_validate_test.go
//
// Tests del formato dual de entrada en CreatePoolRequest (Disks vs DeviceIDs).
//
// Cubre:
//   · Happy path con DeviceIDs (formato canónico)
//   · Happy path con Disks (paths, normalización a DeviceIDs)
//   · Error: ambos campos presentes
//   · Error: ninguno presente
//   · Error: path inexistente en repo
//   · Error: name vacío
//   · Error: profile inválido
//   · Error: insufficient disks por debajo de MinDisks
//   · Estado canónico tras Validate (Disks queda vacío, DeviceIDs poblado)
//   · Regresión: tests existentes con DeviceIDs siguen pasando (validamos
//     a nivel servicio en TestCreatePoolWithDeviceIDs_BackwardCompat)

package main

import (
	"context"
	"strings"
	"testing"
)

// ─── Helpers ──────────────────────────────────────────────────────────────

// registerTwoDevicesForValidate inserta 2 devices con paths conocidos
// y devuelve ([id1, id2], [path1, path2]).
func registerTwoDevicesForValidate(t *testing.T, service *StorageService) ([]string, []string) {
	t.Helper()
	ids := registerTestDevices(t, service, 2)
	if len(ids) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(ids))
	}
	// Resolver paths actuales (los que escribió MockDeviceScanner)
	ctx := context.Background()
	devs, err := service.repo.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	paths := make([]string, len(ids))
	for i, id := range ids {
		for _, d := range devs {
			if d.ID == id {
				paths[i] = d.CurrentPath
				break
			}
		}
		if paths[i] == "" {
			t.Fatalf("could not resolve path for device %s", id)
		}
	}
	return ids, paths
}

// ─── Happy paths ──────────────────────────────────────────────────────────

func TestValidate_HappyPathWithDeviceIDs(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, _ := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:      "data",
		Profile:   ProfileRaid1,
		DeviceIDs: ids,
	}
	if err := req.Validate(context.Background(), service.repo); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(req.DeviceIDs) != 2 {
		t.Errorf("DeviceIDs len = %d, want 2", len(req.DeviceIDs))
	}
	if len(req.Disks) != 0 {
		t.Errorf("Disks should be empty after Validate, got %v", req.Disks)
	}
}

func TestValidate_HappyPathWithDisks(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	_, paths := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:    "data",
		Profile: ProfileRaid1,
		Disks:   paths,
	}
	if err := req.Validate(context.Background(), service.repo); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Tras Validate: forma canónica
	if len(req.DeviceIDs) != 2 {
		t.Errorf("DeviceIDs should be populated, got %v", req.DeviceIDs)
	}
	if len(req.Disks) != 0 {
		t.Errorf("Disks should be cleared after Validate, got %v", req.Disks)
	}
	// Verificar que los IDs resueltos son válidos en el repo
	for _, id := range req.DeviceIDs {
		d, err := service.repo.GetDevice(context.Background(), id)
		if err != nil || d == nil {
			t.Errorf("resolved ID %q is not a valid device", id)
		}
	}
}

// ─── Errores del formato dual ─────────────────────────────────────────────

func TestValidate_ErrorBothFormats(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, paths := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:      "data",
		Profile:   ProfileRaid1,
		DeviceIDs: ids,
		Disks:     paths,
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error when both Disks and DeviceIDs present")
	}
	if code := getServiceErrorCode(err); code != ErrCodeBadRequest {
		t.Errorf("error code = %q, want %q", code, ErrCodeBadRequest)
	}
	if !strings.Contains(err.Error(), "EITHER") {
		t.Errorf("error message should explain mutual exclusion, got: %v", err)
	}
}

func TestValidate_ErrorNeitherFormat(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	_, _ = registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:    "data",
		Profile: ProfileRaid1,
		// Sin DeviceIDs ni Disks
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error when no devices specified")
	}
	if code := getServiceErrorCode(err); code != ErrCodeBadRequest {
		t.Errorf("error code = %q, want %q", code, ErrCodeBadRequest)
	}
	if !strings.Contains(err.Error(), "no devices") {
		t.Errorf("error message should mention 'no devices', got: %v", err)
	}
}

func TestValidate_ErrorPathNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	_, _ = registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:    "data",
		Profile: ProfileRaid1,
		Disks:   []string{"/dev/sd-doesnotexist"},
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if code := getServiceErrorCode(err); code != ErrCodeDeviceNotFound {
		t.Errorf("error code = %q, want %q", code, ErrCodeDeviceNotFound)
	}
	if !strings.Contains(err.Error(), "/dev/sd-doesnotexist") {
		t.Errorf("error should mention the invalid path, got: %v", err)
	}
}

// ─── Validaciones básicas no relacionadas con el formato dual ─────────────

func TestValidate_ErrorEmptyName(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, _ := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:      "",
		Profile:   ProfileRaid1,
		DeviceIDs: ids,
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if code := getServiceErrorCode(err); code != ErrCodeBadRequest {
		t.Errorf("error code = %q, want %q", code, ErrCodeBadRequest)
	}
}

func TestValidate_ErrorInvalidProfile(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, _ := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:      "data",
		Profile:   "raid5", // no soportado en Beta 8
		DeviceIDs: ids,
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error for invalid profile")
	}
	if code := getServiceErrorCode(err); code != ErrCodeProfileInvalid {
		t.Errorf("error code = %q, want %q", code, ErrCodeProfileInvalid)
	}
}

func TestValidate_ErrorInsufficientDisksWithIDs(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, _ := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:      "data",
		Profile:   ProfileRaid1,
		DeviceIDs: []string{ids[0]}, // raid1 needs 2, dando 1
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error for insufficient disks")
	}
	if code := getServiceErrorCode(err); code != ErrCodeInsufficientDisks {
		t.Errorf("error code = %q, want %q", code, ErrCodeInsufficientDisks)
	}
}

func TestValidate_ErrorInsufficientDisksWithPaths(t *testing.T) {
	// El mismo error pero entrando por Disks.
	// Verifica que la normalización Disks→IDs ocurre ANTES del check
	// de MinDisks (orden correcto de validaciones).
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	_, paths := registerTwoDevicesForValidate(t, service)

	req := CreatePoolRequest{
		Name:    "data",
		Profile: ProfileRaid1,
		Disks:   []string{paths[0]}, // raid1 needs 2, dando 1 path
	}
	err := req.Validate(context.Background(), service.repo)
	if err == nil {
		t.Fatal("expected error for insufficient disks (path form)")
	}
	if code := getServiceErrorCode(err); code != ErrCodeInsufficientDisks {
		t.Errorf("error code = %q, want %q", code, ErrCodeInsufficientDisks)
	}
}

// ─── Integración end-to-end ───────────────────────────────────────────────

// TestCreatePool_E2EWithDisks verifica el flujo completo CreatePool con
// formato Disks. Equivale al test ya existente con DeviceIDs pero usando
// paths. Es regression test del contrato dual a nivel servicio.
func TestCreatePool_E2EWithDisks(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	_, paths := registerTwoDevicesForValidate(t, service)

	op, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name:    "data",
		Profile: ProfileRaid1,
		Disks:   paths,
	})
	if err != nil {
		t.Fatalf("CreatePool with Disks: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status = %q, want completed", op.Status)
	}
	if op.PoolID == nil {
		t.Fatal("op.PoolID is nil")
	}
	// Verificar que el pool existe con los devices correctos
	pool, err := service.repo.GetPool(context.Background(), *op.PoolID)
	if err != nil || pool == nil {
		t.Fatalf("pool not in repo: %v", err)
	}
	if pool.Name != "data" {
		t.Errorf("pool name = %q, want data", pool.Name)
	}
}

// TestCreatePool_BackwardCompatWithDeviceIDs verifica que el formato
// canónico DeviceIDs sigue funcionando igual que antes del refactor.
// Es el contrato existente: clientes que ya usaban DeviceIDs no deben
// notar nada.
func TestCreatePool_BackwardCompatWithDeviceIDs(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ids, _ := registerTwoDevicesForValidate(t, service)

	op, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name:      "data",
		Profile:   ProfileRaid1,
		DeviceIDs: ids,
	})
	if err != nil {
		t.Fatalf("CreatePool with DeviceIDs (backward compat): %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status = %q, want completed", op.Status)
	}
}

// ─── Helpers de error extraction ──────────────────────────────────────────

// getServiceErrorCode extrae el código de un error del service.
// Si el error no es del tipo *ServiceError, devuelve "".
func getServiceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if se, ok := err.(*ServiceError); ok {
		return se.Code
	}
	return ""
}
