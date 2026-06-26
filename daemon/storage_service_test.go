// storage_service_test.go — Tests de la capa de orquestación StorageService.
//
// Estos tests verifican:
//   - Queries síncronas (ListPools con hidratación, GetPool, etc.)
//   - Mutaciones síncronas (RenamePool, SetPoolCompression)
//   - Validación de policy antes de mutar
//   - Que Operation se persiste correctamente
//   - Que el error code se propaga correctamente
//
// Tests de mutaciones async (CreatePool, AddDevice, etc.) llegarán en
// Bloque 2 cuando BtrfsExecutor exista.

package main

import (
	"context"
	"errors"
	"testing"
)

// setupTestService crea un StorageService apoyado en una DB SQLite temporal
// y un MockBtrfsExecutor que registra llamadas. El scanner se devuelve
// aparte para que los tests que necesitan controlar discos lo modifiquen.
func setupTestService(t *testing.T) (*StorageService, *MockBtrfsExecutor, func()) {
	t.Helper()

	conn, _, cleanupDB := setupTestDB(t)
	repo := NewStorageRepo(conn)
	policy := NewPolicyChecker()
	mock := NewMockBtrfsExecutor()
	scanner := NewMockDeviceScanner(nil) // sin discos por defecto
	service := NewStorageService(conn, repo, policy, mock, scanner)
	service.deviceChecker = noopDeviceChecker // tests no requieren preflight real

	// Los tests usan devices ficticios cuyos paths no existen en /dev. La
	// verificación real de existencia (devicePathExists) los rechazaría, así
	// que la hacemos permisiva durante el test y la restauramos al limpiar.
	origPathExists := devicePathExists
	devicePathExists = func(string) bool { return true }
	// El rename físico (btrfs label + fstab + mount) no es ejecutable en
	// tests; lo sustituimos por un stub que solo simula éxito.
	origRename := applyPoolRenamePhysicalFn
	applyPoolRenamePhysicalFn = func(*StorageService, context.Context, *Pool, string, string, string) error {
		return nil
	}
	// El executor mock no monta de verdad; simulamos que la verificación de
	// montaje post-creación tiene éxito.
	origVerifyMount := verifyPoolMountedFn
	verifyPoolMountedFn = func(string) bool { return true }
	// FIX-1: el gate de montaje de las ops de layout usa checks reales del
	// sistema; en tests los pools no son montajes reales, así que simulamos
	// "montado y rw" (lo que estos tests ya asumían implícitamente).
	origWritableChecks := defaultPoolWritableChecks
	defaultPoolWritableChecks = poolWritableChecks{
		mountedPool: func(string) bool { return true },
		readOnly:    func(string) bool { return false },
	}
	wrappedCleanup := func() {
		devicePathExists = origPathExists
		applyPoolRenamePhysicalFn = origRename
		verifyPoolMountedFn = origVerifyMount
		defaultPoolWritableChecks = origWritableChecks
		cleanupDB()
	}

	return service, mock, wrappedCleanup
}

// setupTestServiceWithScanner es como setupTestService pero también
// devuelve el scanner para que tests de ScanDevices puedan configurarlo.
func setupTestServiceWithScanner(t *testing.T) (*StorageService, *MockBtrfsExecutor, *MockDeviceScanner, func()) {
	t.Helper()

	conn, _, cleanupDB := setupTestDB(t)
	repo := NewStorageRepo(conn)
	policy := NewPolicyChecker()
	mock := NewMockBtrfsExecutor()
	scanner := NewMockDeviceScanner(nil)
	service := NewStorageService(conn, repo, policy, mock, scanner)
	service.deviceChecker = noopDeviceChecker // tests no requieren preflight real

	return service, mock, scanner, cleanupDB
}

// ─────────────────────────────────────────────────────────────────────────────
// Queries
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceListPoolsEmpty(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	pools, err := service.ListPools(context.Background())
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if len(pools) != 0 {
		t.Errorf("empty list: got %d pools, want 0", len(pools))
	}
}

func TestStorageServiceListPoolsHydrated(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool con devices y capabilities a mano (porque CreatePool
	// aún no está implementado — Bloque 2)
	poolID := "p1"

	// Insertar directo vía repo dentro de una transacción
	tx, _ := service.db.BeginTx(ctx, nil)
	err1 := service.repo.CreatePool(ctx, tx, &Pool{
		ID: poolID, Name: "data", BtrfsUUID: "u1",
		Profile: ProfileRaid1, MountPoint: "/nimos/pools/data",
	})
	_, err2 := service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1",
		CurrentPath: "/dev/sdb", SizeBytes: 1e12,
	})
	err3 := service.repo.AssignDeviceToPool(ctx, tx, poolID, "d1")
	err4 := service.repo.SetPoolCapabilities(ctx, tx, poolID, []string{"snapshots", "scrub"})
	if err := firstError(err1, err2, err3, err4); err != nil {
		tx.Rollback()
		t.Fatalf("setup: %v", err)
	}
	tx.Commit()

	pools, err := service.ListPools(ctx)
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("got %d pools, want 1", len(pools))
	}
	p := pools[0]
	if len(p.Devices) != 1 {
		t.Errorf("hydrated devices: got %d, want 1", len(p.Devices))
	}
	if len(p.Devices) > 0 && p.Devices[0].Serial != "S1" {
		t.Errorf("device serial: got %q, want S1", p.Devices[0].Serial)
	}
	if len(p.Capabilities) != 2 {
		t.Errorf("hydrated caps: got %d, want 2", len(p.Capabilities))
	}
}

func TestStorageServiceGetPoolNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.GetPool(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pool")
	}
	var se *ServiceError
	if !errors.As(err, &se) {
		t.Fatalf("error type: got %T", err)
	}
	if se.Code != ErrCodePoolNotFound {
		t.Errorf("error code: got %q, want %q", se.Code, ErrCodePoolNotFound)
	}
}

func TestStorageServiceGetGeneration(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	gen0, err := service.GetGeneration(ctx)
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if gen0 != 0 {
		t.Errorf("initial generation: got %d, want 0", gen0)
	}

	// Una mutación incrementa
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "a", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	tx.Commit()

	gen1, _ := service.GetGeneration(ctx)
	if gen1 <= gen0 {
		t.Errorf("after CreatePool: got %d, want > %d", gen1, gen0)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Mutaciones síncronas — RenamePool
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceRenamePoolHappy(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Setup: crear pool managed
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "old", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	tx.Commit()

	// Renombrar
	op, err := service.RenamePool(ctx, "p1", "new")
	if err != nil {
		t.Fatalf("RenamePool: %v", err)
	}
	if op == nil {
		t.Fatal("operation should not be nil")
	}
	if op.Type != OpTypeRenamePool {
		t.Errorf("op.Type: got %q", op.Type)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q, want completed", op.Status)
	}
	if op.CompletedAt == nil {
		t.Error("completed_at should be set")
	}

	// Verificar que el pool tiene el nuevo nombre
	pool, _ := service.GetPool(ctx, "p1")
	if pool.Name != "new" {
		t.Errorf("pool.Name: got %q, want new", pool.Name)
	}
}

func TestStorageServiceRenamePoolNotFound(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	_, err := service.RenamePool(context.Background(), "nonexistent", "whatever")
	if err == nil {
		t.Fatal("expected error")
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Code != ErrCodePoolNotFound {
		t.Errorf("expected pool_not_found, got %v", err)
	}
}

func TestStorageServiceRenamePoolObservedRejected(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool en estado observed
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
		ControlState: ControlStateObserved,
	})
	tx.Commit()

	_, err := service.RenamePool(ctx, "p1", "whatever")
	if err == nil {
		t.Fatal("expected policy rejection")
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Code != ErrCodePoolObserved {
		t.Errorf("expected pool_observed, got %v", err)
	}
}

func TestStorageServiceRenamePoolNameTaken(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Dos pools, intentar renombrar el primero al nombre del segundo
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m1",
	})
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p2", Name: "backups", BtrfsUUID: "u2",
		Profile: ProfileSingle, MountPoint: "/m2",
	})
	tx.Commit()

	_, err := service.RenamePool(ctx, "p1", "backups")
	if err == nil {
		t.Fatal("expected name_taken error")
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Code != ErrCodePoolNameTaken {
		t.Errorf("expected pool_name_taken, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Mutaciones síncronas — SetPoolCompression
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageServiceSetCompressionHappy(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool managed con capability compression
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	service.repo.SetPoolCapabilities(ctx, tx, "p1", []string{"compression"})
	tx.Commit()

	op, err := service.SetPoolCompression(ctx, "p1", "zstd:3")
	if err != nil {
		t.Fatalf("SetPoolCompression: %v", err)
	}
	if op.Status != OpStatusCompleted {
		t.Errorf("op.Status: got %q", op.Status)
	}

	pool, _ := service.GetPool(ctx, "p1")
	if pool.Compression != "zstd:3" {
		t.Errorf("compression: got %q", pool.Compression)
	}
}

func TestStorageServiceSetCompressionRequiresCapability(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool sin capability "compression"
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.CreatePool(ctx, tx, &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "u1",
		Profile: ProfileSingle, MountPoint: "/m",
	})
	tx.Commit()

	_, err := service.SetPoolCompression(ctx, "p1", "zstd:3")
	if err == nil {
		t.Fatal("expected capability_missing error")
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Code != ErrCodeCapabilityMissing {
		t.Errorf("expected capability_missing, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func firstError(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
