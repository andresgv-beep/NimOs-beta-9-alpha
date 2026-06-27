// storage_service_devices_test.go — Tests de AddDevice, RemoveDevice,
// ReplaceDevice y ConvertProfile.
//
// Patrón: usar MockBtrfsExecutor, helpers compartidos (registerTestDevices,
// assertErrorCode) y la suite ya existente.

package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Helper: crea pool con N devices y devuelve poolID + deviceIDs.
func createTestPool(t *testing.T, service *StorageService, ctx context.Context,
	name string, profile Profile, numDevices int) (string, []string) {
	t.Helper()
	deviceIDs := registerTestDevices(t, service, numDevices)
	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name:      name,
		Profile:   profile,
		DeviceIDs: deviceIDs,
	})
	if err != nil {
		t.Fatalf("createTestPool: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Fatalf("createTestPool: op status %q", op.Status)
	}
	return *op.PoolID, deviceIDs
}

// ─────────────────────────────────────────────────────────────────────────────
// AddDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceAddDeviceHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool inicial con 2 discos en raid1
	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Registrar un disco extra
	tx, _ := service.db.BeginTx(ctx, nil)
	extraID := "extra-1"
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: extraID, Serial: "EXTRA-1",
		ByIDPath: "/dev/disk/by-id/extra-1", CurrentPath: "/dev/sdz",
		SizeBytes: 1e12,
	})
	tx.Commit()

	mock.Reset()

	op, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID: poolID, DeviceID: extraID,
	})
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}

	// Executor recibió AddDevice
	if len(mock.AddDeviceCalls) != 1 {
		t.Fatalf("AddDevice calls: got %d, want 1", len(mock.AddDeviceCalls))
	}
	if mock.AddDeviceCalls[0].ByIDPath != "/dev/disk/by-id/extra-1" {
		t.Errorf("ByIDPath: got %q", mock.AddDeviceCalls[0].ByIDPath)
	}

	// El pool ahora tiene 3 devices
	pool, _ := service.GetPool(ctx, poolID)
	if len(pool.Devices) != 3 {
		t.Errorf("devices in pool: got %d, want 3", len(pool.Devices))
	}
}

func TestStorageServiceAddDevicePoolNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.AddDevice(context.Background(), AddDeviceRequest{
		PoolID: "nonexistent", DeviceID: "x",
	})
	assertErrorCode(t, err, ErrCodePoolNotFound)
}

func TestStorageServiceAddDeviceDeviceInUse(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Intentar añadir un disco que ya está en el pool
	_, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID: poolID, DeviceID: deviceIDs[0],
	})
	assertErrorCode(t, err, ErrCodeDeviceInUse)
}

func TestStorageServiceAddDeviceObservedRejected(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool observado
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "obs-1", Name: "observed", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
		ControlState: ControlStateObserved,
	})
	tx.Commit()

	_, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID: "obs-1", DeviceID: "any",
	})
	assertErrorCode(t, err, ErrCodePoolObserved)
}

func TestStorageServiceAddDeviceBtrfsFails(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Registrar disco extra
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "extra", Serial: "E1",
		ByIDPath: "/dev/disk/by-id/e1", CurrentPath: "/dev/sde",
		SizeBytes: 1e12,
	})
	tx.Commit()

	// Inyectar fallo
	mock.AddDeviceFn = func(ctx context.Context, mp, p string) error {
		return fmt.Errorf("btrfs device add: no space left")
	}

	op, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID: poolID, DeviceID: "extra",
	})
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if op.Status != OpStatusFailed {
		t.Errorf("op.Status: got %q, want failed", op.Status)
	}
	if op.ErrorCode == nil || *op.ErrorCode != ErrCodeBtrfsCommandFailed {
		t.Errorf("op.ErrorCode: got %v", op.ErrorCode)
	}

	// El device NO debe estar asignado al pool
	pool, _ := service.GetPool(ctx, poolID)
	if len(pool.Devices) != 2 {
		t.Errorf("devices: got %d, want 2 (rollback)", len(pool.Devices))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RemoveDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceRemoveDeviceHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// raid1 con 3 discos para poder quitar uno
	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 3)
	mock.Reset()

	op, err := service.RemoveDevice(ctx, RemoveDeviceRequest{
		PoolID: poolID, DeviceID: deviceIDs[0],
	})
	if err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}

	if len(mock.RemoveDeviceCalls) != 1 {
		t.Fatalf("RemoveDevice calls: got %d", len(mock.RemoveDeviceCalls))
	}

	// El pool ahora tiene 2 devices
	pool, _ := service.GetPool(ctx, poolID)
	if len(pool.Devices) != 2 {
		t.Errorf("devices: got %d, want 2", len(pool.Devices))
	}

	// El device sigue existiendo pero ya no está en el pool
	d, _ := service.repo.GetDevice(ctx, deviceIDs[0])
	if d == nil {
		t.Error("device should still exist after remove")
	}
}

func TestStorageServiceRemoveDeviceMinDisksReached(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// raid1 con exactamente el mínimo (2). Quitar uno bajaría a 1.
	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	_, err := service.RemoveDevice(ctx, RemoveDeviceRequest{
		PoolID: poolID, DeviceID: deviceIDs[0],
	})
	assertErrorCode(t, err, ErrCodeMinDisksReached)
}

func TestStorageServiceRemoveDeviceNotInPool(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 3)

	// Registrar disco que NO está en el pool
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "outsider", Serial: "OUT",
		ByIDPath: "/dev/disk/by-id/out", CurrentPath: "/dev/sdo",
		SizeBytes: 1e12,
	})
	tx.Commit()

	_, err := service.RemoveDevice(ctx, RemoveDeviceRequest{
		PoolID: poolID, DeviceID: "outsider",
	})
	assertErrorCode(t, err, ErrCodeDeviceNotFound)
}

// ─────────────────────────────────────────────────────────────────────────────
// ReplaceDevice
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceReplaceDeviceHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Registrar disco nuevo
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12, // mayor que el viejo
	})
	tx.Commit()
	mock.Reset()

	// Aislar el auto-scrub post-replace (no shell-out a btrfs en unit test).
	origScrub := startScrubOnPool
	defer func() { startScrubOnPool = origScrub }()
	startScrubOnPool = func(mp, name string) error { return nil }

	op, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: deviceIDs[0],
		NewDeviceID: "new-1",
	})
	if err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}

	if len(mock.ReplaceDeviceCalls) != 1 {
		t.Fatalf("ReplaceDevice calls: got %d", len(mock.ReplaceDeviceCalls))
	}

	// El pool tiene los mismos 2 devices, pero ahora new-1 en lugar del viejo
	pool, _ := service.GetPool(ctx, poolID)
	if len(pool.Devices) != 2 {
		t.Errorf("devices count: got %d", len(pool.Devices))
	}
	found := false
	for _, d := range pool.Devices {
		if d.ID == "new-1" {
			found = true
		}
		if d.ID == deviceIDs[0] {
			t.Errorf("old device %q still in pool", deviceIDs[0])
		}
	}
	if !found {
		t.Error("new device should be in pool")
	}
}

// TestStorageServiceReplaceDeviceRevertsToReadOnlyOnFailure — si el pool estaba
// en read-only (degradado) y lo remontamos rw SOLO para reparar, un replace que
// falla NO debe dejar el pool en escritura sin redundancia: debe revertirse a ro.
// Además la membresía no debe cambiar (el viejo sigue, el nuevo no entra) y la
// operación queda FAILED.
func TestStorageServiceReplaceDeviceRevertsToReadOnlyOnFailure(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Disco nuevo válido (más grande que el viejo).
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()
	mock.Reset()

	// Inyectar: pool en ro, captar remounts, y forzar fallo del replace.
	origRO, origRW, origToRO := poolMountIsReadOnly, remountPoolReadWriteDegraded, remountPoolReadOnlyDegraded
	defer func() {
		poolMountIsReadOnly = origRO
		remountPoolReadWriteDegraded = origRW
		remountPoolReadOnlyDegraded = origToRO
	}()
	poolMountIsReadOnly = func(string) bool { return true }
	rwCalled := false
	remountPoolReadWriteDegraded = func(string) error { rwCalled = true; return nil }
	roReverted := false
	remountPoolReadOnlyDegraded = func(string) error { roReverted = true; return nil }
	mock.ReplaceDeviceFn = func(ctx context.Context, mp, oldP, newP string) error {
		return fmt.Errorf("boom: target write error")
	}

	op, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: deviceIDs[0],
		NewDeviceID: "new-1",
	})
	if err != nil {
		t.Fatalf("esperaba op fallida sin error de transporte, got %v", err)
	}
	if op == nil || op.Status != OpStatusFailed {
		t.Fatalf("op debería estar FAILED, got %+v", op)
	}
	if !rwCalled {
		t.Error("debió remontar rw para intentar la reparación")
	}
	if !roReverted {
		t.Error("tras el fallo del replace debió revertir el pool a ro (no dejarlo rw degradado)")
	}

	// La membresía NO cambió: el viejo sigue, el nuevo no entró.
	pool, _ := service.GetPool(ctx, poolID)
	stillOld := false
	for _, d := range pool.Devices {
		if d.ID == deviceIDs[0] {
			stillOld = true
		}
		if d.ID == "new-1" {
			t.Error("new-1 no debería estar en el pool tras un fallo")
		}
	}
	if !stillOld {
		t.Error("el disco viejo debería seguir en el pool tras un fallo")
	}
}

// TestStorageServiceReplaceDeviceLaunchesScrubOnSuccess — tras un replace
// exitoso debe lanzarse un scrub de verificación sobre el pool reparado.
func TestStorageServiceReplaceDeviceLaunchesScrubOnSuccess(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()
	mock.Reset()

	origScrub := startScrubOnPool
	defer func() { startScrubOnPool = origScrub }()
	scrubbedMount := ""
	startScrubOnPool = func(mp, name string) error { scrubbedMount = mp; return nil }

	op, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: deviceIDs[0],
		NewDeviceID: "new-1",
	})
	if err != nil || op == nil || op.Status != OpStatusCompleted {
		t.Fatalf("el replace debería completar, got op=%+v err=%v", op, err)
	}
	if scrubbedMount == "" {
		t.Error("tras un replace exitoso debió lanzarse un scrub de verificación")
	}
}

func TestStorageServiceReplaceDeviceNewSmaller(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Disco nuevo MÁS PEQUEÑO que el viejo
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "smaller", Serial: "SM",
		ByIDPath: "/dev/disk/by-id/sm", CurrentPath: "/dev/sds",
		SizeBytes: 1e11, // 100GB vs 1TB del viejo
	})
	tx.Commit()

	_, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: deviceIDs[0],
		NewDeviceID: "smaller",
	})
	assertErrorCode(t, err, ErrCodeDeviceNotEligible)
}

func TestStorageServiceReplaceDeviceNewInUse(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Dos pools
	pool1ID, dev1IDs := createTestPool(t, service, ctx, "first", ProfileRaid1, 2)
	_, dev2IDs := createTestPool(t, service, ctx, "second", ProfileRaid1, 2)

	// Intentar reemplazar uno de pool1 con uno de pool2
	_, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      pool1ID,
		OldDeviceID: dev1IDs[0],
		NewDeviceID: dev2IDs[0],
	})
	assertErrorCode(t, err, ErrCodeDeviceInUse)
}

// ─────────────────────────────────────────────────────────────────────────────
// ConvertProfile
// ─────────────────────────────────────────────────────────────────────────────

// waitForOperation espera (polling corto) a que una operation alcance estado
// terminal. Para tests del modelo async (ConvertProfile corre en goroutine).
func waitForOperation(t *testing.T, service *StorageService, ctx context.Context, opID string, timeout time.Duration) *Operation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		op, err := service.repo.GetOperation(ctx, opID)
		if err != nil {
			t.Fatalf("waitForOperation: %v", err)
		}
		switch op.Status {
		case OpStatusCompleted, OpStatusFailed, OpStatusRolledBack, OpStatusCancelled:
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitForOperation: la op %s no terminó en %v", opID, timeout)
	return nil
}

func TestStorageServiceConvertProfileHappy(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Single con 2 discos → convertir a raid1
	poolID, _ := createTestPool(t, service, ctx, "data", ProfileSingle, 2)
	mock.Reset()

	op, err := service.ConvertProfile(ctx, ConvertProfileRequest{
		PoolID:     poolID,
		NewProfile: ProfileRaid1,
	})
	if err != nil {
		t.Fatalf("ConvertProfile: %v", err)
	}
	// Modelo ASYNC: la op vuelve in_progress (el balance corre en background).
	if op.Status != OpStatusInProgress {
		t.Errorf("op.Status inmediato: got %q, want in_progress (async)", op.Status)
	}

	// Esperar a que el background complete (el mock es instantáneo).
	final := waitForOperation(t, service, ctx, op.ID, 3*time.Second)
	if final.Status != OpStatusCompleted {
		t.Errorf("op.Status final: got %q, want completed", final.Status)
	}

	if len(mock.ConvertProfileCalls) != 1 {
		t.Fatalf("ConvertProfile calls: got %d", len(mock.ConvertProfileCalls))
	}
	if mock.ConvertProfileCalls[0].NewProfile != ProfileRaid1 {
		t.Errorf("NewProfile: got %q", mock.ConvertProfileCalls[0].NewProfile)
	}

	// El pool ahora tiene profile raid1
	pool, _ := service.GetPool(ctx, poolID)
	if pool.Profile != ProfileRaid1 {
		t.Errorf("pool.Profile: got %q, want raid1", pool.Profile)
	}
}

func TestStorageServiceConvertProfileSameProfile(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	_, err := service.ConvertProfile(ctx, ConvertProfileRequest{
		PoolID:     poolID,
		NewProfile: ProfileRaid1, // mismo
	})
	assertErrorCode(t, err, ErrCodeBadRequest)
}

func TestStorageServiceConvertProfileInsufficientDisks(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// raid1 con 2 discos → intentar convertir a raid10 (necesita 4)
	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	_, err := service.ConvertProfile(ctx, ConvertProfileRequest{
		PoolID:     poolID,
		NewProfile: ProfileRaid10,
	})
	assertErrorCode(t, err, ErrCodeInsufficientDisks)
}

func TestStorageServiceConvertProfileInvalid(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	_, err := service.ConvertProfile(ctx, ConvertProfileRequest{
		PoolID:     poolID,
		NewProfile: "raid5",
	})
	assertErrorCode(t, err, ErrCodeProfileInvalid)
}

// ─────────────────────────────────────────────────────────────────────────────
// INV-1 desde el service: 2 operaciones layout concurrentes en mismo pool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceLayoutOpsAreMutuallyExclusive(t *testing.T) {
	// El schema rechaza dos layout-ops activas en el mismo pool.
	// Verificamos que el service traduce ese error en operation_in_progress.
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Registrar disco extra
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "extra", Serial: "X1",
		ByIDPath: "/dev/disk/by-id/x1", CurrentPath: "/dev/sdz",
		SizeBytes: 1e12,
	})
	tx.Commit()

	// Hacer que AddDevice quede colgado a media (mock no termina nunca, pero
	// vamos a simular que ya hay una op pendiente metiéndola a mano)
	mock.Reset()
	// Insertar manualmente una op in_progress
	tx2, _ := service.db.BeginTx(ctx, nil)
	stuckOp := &Operation{
		ID: "stuck-op", Type: OpTypeAddDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
	}
	if err := service.repo.CreateOperation(ctx, tx2, stuckOp); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tx2.Commit()

	// Intentar otra AddDevice: debe rechazarse por la UNIQUE parcial del schema
	_, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID: poolID, DeviceID: "extra",
	})
	assertErrorCode(t, err, ErrCodeOperationInProgress)
}
