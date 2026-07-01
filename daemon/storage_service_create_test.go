// storage_service_create_test.go — Tests de CreatePool y DestroyPool.
//
// Usan MockBtrfsExecutor para no requerir BTRFS real ni sudo.
// Verifican:
//   - Validaciones (nombre vacío, profile inválido, discos insuficientes)
//   - Que el executor recibe los argumentos correctos
//   - Que el pool y devices se persisten en DB tras éxito
//   - Que la operation se marca completed/failed según el resultado
//   - Que un fallo del executor deja la operation en failed con código

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper: registrar devices en la DB para usar en CreatePool
// ─────────────────────────────────────────────────────────────────────────────

// testDeviceCounter genera IDs únicos para test devices.
// Cada llamada a registerTestDevices reserva un bloque de IDs nuevo,
// permitiendo crear múltiples pools en el mismo test sin colisiones.
var testDeviceCounter int

func registerTestDevices(t *testing.T, service *StorageService, count int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, count)

	tx, _ := service.db.BeginTx(ctx, nil)
	for i := 0; i < count; i++ {
		testDeviceCounter++
		id := fmt.Sprintf("dev-%d", testDeviceCounter)
		_, err := service.repo.UpsertDevice(ctx, tx, &Device{
			ID:          id,
			Serial:      fmt.Sprintf("TEST-SERIAL-%d", testDeviceCounter),
			ByIDPath:    fmt.Sprintf("/dev/disk/by-id/test-%d", testDeviceCounter),
			CurrentPath: fmt.Sprintf("/dev/loop%d", testDeviceCounter),
			SizeBytes:   1e12,
		})
		if err != nil {
			tx.Rollback()
			t.Fatalf("registerTestDevices: %v", err)
		}
		ids[i] = id
	}
	tx.Commit()
	return ids
}

// ─────────────────────────────────────────────────────────────────────────────
// CreatePool — caminos felices
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceCreatePoolHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name:      "multimedia",
		Profile:   ProfileRaid1,
		DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	if op == nil {
		t.Fatal("operation should not be nil")
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q, want completed", op.Status)
	}
	if op.PoolID == nil {
		t.Error("op.PoolID should be set after success")
	}

	// Verificar que el executor recibió los argumentos correctos
	if len(mock.CreateFilesystemCalls) != 1 {
		t.Fatalf("CreateFilesystem calls: got %d, want 1", len(mock.CreateFilesystemCalls))
	}
	call := mock.CreateFilesystemCalls[0]
	if call.Label != "multimedia" {
		t.Errorf("Label: got %q, want multimedia", call.Label)
	}
	if call.Profile != ProfileRaid1 {
		t.Errorf("Profile: got %q", call.Profile)
	}
	if len(call.ByIDPaths) != 2 {
		t.Errorf("ByIDPaths: got %d, want 2", len(call.ByIDPaths))
	}

	// Mount se llamó una vez con el mountpoint correcto
	if len(mock.MountFilesystemCalls) != 1 {
		t.Fatalf("Mount calls: got %d, want 1", len(mock.MountFilesystemCalls))
	}
	if mock.MountFilesystemCalls[0].MountPoint != "/nimos/pools/multimedia" {
		t.Errorf("mount point: got %q", mock.MountFilesystemCalls[0].MountPoint)
	}

	// El pool se persistió en DB con los datos correctos
	pool, _ := service.GetPool(ctx, *op.PoolID)
	if pool == nil {
		t.Fatal("pool not found in DB after CreatePool")
	}
	if pool.Name != "multimedia" {
		t.Errorf("pool.Name: got %q", pool.Name)
	}
	if pool.MountPoint != "/nimos/pools/multimedia" {
		t.Errorf("pool.MountPoint: got %q", pool.MountPoint)
	}
	if pool.ControlState != ControlStateManaged {
		t.Errorf("pool.ControlState: got %q", pool.ControlState)
	}
	if len(pool.Devices) != 2 {
		t.Errorf("pool.Devices: got %d", len(pool.Devices))
	}
	if len(pool.Capabilities) != 8 {
		t.Errorf("pool.Capabilities: got %d (expected full BTRFS managed set)", len(pool.Capabilities))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CreatePool — validaciones
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceCreatePoolEmptyName(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name: "", Profile: ProfileRaid1, DeviceIDs: []string{"x", "y"},
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	assertErrorCode(t, err, ErrCodeBadRequest)
}

func TestStorageServiceCreatePoolInvalidProfile(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name: "validname", Profile: "raid5", DeviceIDs: []string{"a"},
	})
	if err == nil {
		t.Fatal("expected error for invalid profile")
	}
	assertErrorCode(t, err, ErrCodeProfileInvalid)
}

func TestStorageServiceCreatePoolInsufficientDisks(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name: "validname", Profile: ProfileRaid10, DeviceIDs: []string{"a", "b"}, // raid10 necesita 4
	})
	if err == nil {
		t.Fatal("expected error for insufficient disks")
	}
	assertErrorCode(t, err, ErrCodeInsufficientDisks)
}

func TestStorageServiceCreatePoolNameTaken(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Crear un pool primero
	deviceIDs := registerTestDevices(t, service, 4)
	_, err := service.CreatePool(ctx, CreatePoolRequest{
		Name: "data", Profile: ProfileRaid1, DeviceIDs: deviceIDs[:2],
	})
	if err != nil {
		t.Fatalf("setup CreatePool: %v", err)
	}

	// Intentar crear otro con el mismo nombre
	_, err = service.CreatePool(ctx, CreatePoolRequest{
		Name: "data", Profile: ProfileRaid1, DeviceIDs: deviceIDs[2:],
	})
	if err == nil {
		t.Fatal("expected pool_name_taken")
	}
	assertErrorCode(t, err, ErrCodePoolNameTaken)
}

func TestStorageServiceCreatePoolDeviceNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.CreatePool(context.Background(), CreatePoolRequest{
		Name: "data", Profile: ProfileRaid1,
		DeviceIDs: []string{"nonexistent-1", "nonexistent-2"},
	})
	if err == nil {
		t.Fatal("expected device_not_found")
	}
	assertErrorCode(t, err, ErrCodeDeviceNotFound)
}

func TestStorageServiceCreatePoolDeviceInUse(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	// Crear primer pool con esos devices
	_, err := service.CreatePool(ctx, CreatePoolRequest{
		Name: "first", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Intentar crear otro con los mismos devices
	_, err = service.CreatePool(ctx, CreatePoolRequest{
		Name: "second", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})
	if err == nil {
		t.Fatal("expected device_in_use")
	}
	assertErrorCode(t, err, ErrCodeDeviceInUse)
}

// ─────────────────────────────────────────────────────────────────────────────
// CreatePool — fallos del executor
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceCreatePoolMkfsFails(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	// Inyectar fallo en CreateFilesystem
	mock.CreateFilesystemFn = func(ctx context.Context, req CreateFilesystemRequest) (*FilesystemInfo, error) {
		return nil, fmt.Errorf("mkfs.btrfs failed: device busy")
	}

	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name: "fail", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("CreatePool should not return error (operation captures it): %v", err)
	}
	if op.Status != OpStatusFailed {
		t.Errorf("op.Status: got %q, want failed", op.Status)
	}
	if op.ErrorCode == nil || *op.ErrorCode != ErrCodeBtrfsCommandFailed {
		t.Errorf("op.ErrorCode: got %v, want %q", op.ErrorCode, ErrCodeBtrfsCommandFailed)
	}

	// El pool NO debe estar en DB
	pool, _ := service.repo.GetPoolByName(ctx, "fail")
	if pool != nil {
		t.Error("pool should not exist in DB after mkfs failure")
	}
}

func TestStorageServiceCreatePoolMountFails(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	// CreateFilesystem ok, pero mount falla
	mock.MountFilesystemFn = func(ctx context.Context, byIDPath, mountPoint string) error {
		return fmt.Errorf("mount: filesystem not recognized")
	}

	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name: "mountfail", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	if op.Status != OpStatusFailed {
		t.Errorf("op.Status: got %q, want failed", op.Status)
	}
	if op.ErrorCode == nil || *op.ErrorCode != ErrCodeMountFailed {
		t.Errorf("op.ErrorCode: got %v, want %q", op.ErrorCode, ErrCodeMountFailed)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DestroyPool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceDestroyPoolHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	// Crear pool primero
	op1, err := service.CreatePool(ctx, CreatePoolRequest{
		Name: "tokill", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("setup CreatePool: %v", err)
	}
	poolID := *op1.PoolID

	mock.Reset() // limpiar registros para verificar solo destroy

	// Destruir
	op2, err := service.DestroyPool(ctx, poolID)
	if err != nil {
		t.Fatalf("DestroyPool: %v", err)
	}
	if op2.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op2.Status)
	}

	// Executor recibió DestroyFilesystem
	if len(mock.DestroyFilesystemCalls) != 1 {
		t.Fatalf("DestroyFilesystem calls: got %d, want 1", len(mock.DestroyFilesystemCalls))
	}
	call := mock.DestroyFilesystemCalls[0]
	if call.MountPoint != "/nimos/pools/tokill" {
		t.Errorf("MountPoint: got %q", call.MountPoint)
	}
	if len(call.ByIDPaths) != 2 {
		t.Errorf("ByIDPaths: got %d, want 2", len(call.ByIDPaths))
	}

	// Pool no debe existir en DB
	pool, _ := service.repo.GetPool(ctx, poolID)
	if pool != nil {
		t.Error("pool should be deleted from DB")
	}

	// Devices deben seguir existiendo y estar libres
	for _, did := range deviceIDs {
		d, _ := service.repo.GetDevice(ctx, did)
		if d == nil {
			t.Errorf("device %s should still exist after DestroyPool", did)
		}
	}
	available, _ := service.repo.ListAvailableDevices(ctx)
	if len(available) != 2 {
		t.Errorf("available devices after destroy: got %d, want 2", len(available))
	}

	// La operation original (CreatePool) se conserva con pool_id NULL (auditoría)
	origOp, _ := service.repo.GetOperation(ctx, op1.ID)
	if origOp == nil {
		t.Fatal("original CreatePool operation should be preserved")
	}
	if origOp.PoolID != nil {
		t.Errorf("original op.PoolID after destroy: got %v, want nil (SET NULL)", *origOp.PoolID)
	}
}

func TestStorageServiceDestroyPoolNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.DestroyPool(context.Background(), "nonexistent-pool")
	if err == nil {
		t.Fatal("expected error")
	}
	assertErrorCode(t, err, ErrCodePoolNotFound)
}

func TestStorageServiceDestroyPoolObservedRejected(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool en estado observed (saltándonos CreatePool porque
	// CreatePool fuerza managed)
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "obs-1", Name: "observed", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
		ControlState: ControlStateObserved,
	})
	tx.Commit()

	_, err := service.DestroyPool(ctx, "obs-1")
	if err == nil {
		t.Fatal("expected pool_observed error")
	}
	assertErrorCode(t, err, ErrCodePoolObserved)
}

func TestStorageServiceDestroyPoolExecutorFails(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)
	op1, _ := service.CreatePool(ctx, CreatePoolRequest{
		Name: "doomed", Profile: ProfileRaid1, DeviceIDs: deviceIDs,
	})

	// Inyectar fallo en DestroyFilesystem
	mock.DestroyFilesystemFn = func(ctx context.Context, req DestroyFilesystemRequest) error {
		return fmt.Errorf("umount: target is busy")
	}

	op2, _ := service.DestroyPool(ctx, *op1.PoolID)
	if op2.Status != OpStatusFailed {
		t.Errorf("op.Status: got %q, want failed", op2.Status)
	}
	if op2.ErrorCode == nil || *op2.ErrorCode != ErrCodeBtrfsCommandFailed {
		t.Errorf("op.ErrorCode: got %v", op2.ErrorCode)
	}

	// El pool DEBE seguir en DB (no se borró porque destroy físico falló)
	pool, _ := service.repo.GetPool(ctx, *op1.PoolID)
	if pool == nil {
		t.Error("pool should still exist after failed destroy")
	}
}

// TestCreatePool_Concurrent: N goroutines llaman a CreatePool sobre los
// mismos devices. Verifica que no hay panic/deadlock y que la DB queda
// consistente (exactamente 1 pool). Ejecutar con `-race` activa también
// el detector de data races sobre el flujo concurrente.
func TestCreatePool_Concurrent(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	deviceIDs := registerTestDevices(t, service, 2)

	const N = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(N)

	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, _ = service.CreatePool(ctx, CreatePoolRequest{
				Name:      fmt.Sprintf("p-%d", idx),
				Profile:   ProfileRaid1,
				DeviceIDs: deviceIDs,
			})
		}(i)
	}
	close(start)
	wg.Wait()

	pools, err := service.ListPools(ctx)
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("estado final: esperaba 1 pool, hay %d", len(pools))
	}
}

func assertErrorCode(t *testing.T, err error, expectedCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", expectedCode)
	}
	var se *ServiceError
	if !errors.As(err, &se) {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
	if se.Code != expectedCode {
		t.Errorf("error code: got %q, want %q (msg: %s)", se.Code, expectedCode, se.Msg)
	}
}
