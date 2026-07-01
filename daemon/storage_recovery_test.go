// storage_recovery_test.go — Tests de RecoverPendingOperations.
//
// Cubrimos los escenarios de la matriz de recovery:
//
//   Operation type    BTRFS state                    → Outcome esperado
//   ──────────────────────────────────────────────────────────────────
//   create_pool       FS no existe en disco          → rolled_back
//   create_pool       FS existe pero pool no en DB   → inconclusive
//   destroy_pool      FS no existe (destruido)       → completed
//   destroy_pool      FS sigue existiendo            → inconclusive
//   add_device etc.   cualquier estado               → inconclusive
//   sin huérfanas     —                              → no-op (Inspected=0)
//
// Cada test usa MockBtrfsExecutor para forzar el estado BTRFS deseado
// e inspecciona la DB tras recovery para verificar el outcome.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper para inyectar una operation huérfana en la DB
// ─────────────────────────────────────────────────────────────────────────────

// injectOrphanOp inserta una Operation con el status y data dados,
// saltándose el StorageService (que no permitiría status arbitrario).
// Simula el estado post-crash: una op que quedó in_progress.
func injectOrphanOp(t *testing.T, service *StorageService, op *Operation) {
	t.Helper()
	ctx := context.Background()
	if op.ID == "" {
		op.ID = newUUID()
	}
	if op.Status == "" {
		op.Status = OpStatusInProgress
	}
	tx, _ := service.db.BeginTx(ctx, nil)
	if err := service.repo.CreateOperation(ctx, tx, op); err != nil {
		tx.Rollback()
		t.Fatalf("injectOrphanOp: %v", err)
	}
	tx.Commit()
}

// ─────────────────────────────────────────────────────────────────────────────
// Sin operations huérfanas
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryNoOrphans(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()

	result, err := service.RecoverPendingOperations(context.Background())
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.Inspected != 0 {
		t.Errorf("Inspected: got %d, want 0", result.Inspected)
	}
	if result.Completed+result.RolledBack+result.Inconclusive != 0 {
		t.Errorf("expected no actions, got: %+v", result)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// create_pool — caso A: BTRFS no ve el FS → rolled_back
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryCreatePoolRolledBack(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Mock: cualquier UUID/label → no existe en BTRFS
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return false, nil
	}

	op := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "interrupted-pool"}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}

	if result.Inspected != 1 {
		t.Errorf("Inspected: got %d, want 1", result.Inspected)
	}
	if result.RolledBack != 1 {
		t.Errorf("RolledBack: got %d, want 1", result.RolledBack)
	}

	// La operation en DB tiene status failed con código rolled_back
	updated, _ := service.repo.GetOperation(ctx, op.ID)
	if updated.Status != OpStatusFailed {
		t.Errorf("status: got %q, want failed", updated.Status)
	}
	if updated.ErrorCode == nil || *updated.ErrorCode != ErrCodeRecoveryRolledBack {
		t.Errorf("error_code: got %v, want %q", updated.ErrorCode, ErrCodeRecoveryRolledBack)
	}

	// El BTRFS executor fue consultado con el nombre del pool
	if len(mock.FilesystemExistsByUUIDCalls) != 1 {
		t.Fatalf("FilesystemExistsByUUID calls: got %d, want 1",
			len(mock.FilesystemExistsByUUIDCalls))
	}
	if mock.FilesystemExistsByUUIDCalls[0] != "interrupted-pool" {
		t.Errorf("queried UUID: got %q", mock.FilesystemExistsByUUIDCalls[0])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// create_pool — caso B: BTRFS sí ve el FS pero pool no en DB → inconclusive
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryCreatePoolInconclusiveLeftFilesystem(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Mock: el FS sí existe (mkfs ejecutó antes del crash)
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return true, nil
	}

	op := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "leftover-pool"}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.Inconclusive != 1 {
		t.Errorf("Inconclusive: got %d, want 1", result.Inconclusive)
	}

	updated, _ := service.repo.GetOperation(ctx, op.ID)
	if updated.ErrorCode == nil || *updated.ErrorCode != ErrCodeRecoveryInconclusive {
		t.Errorf("error_code: got %v, want %q", updated.ErrorCode, ErrCodeRecoveryInconclusive)
	}
	// El mensaje debe mencionar el manual cleanup
	if updated.Error == nil {
		t.Fatal("error message should be set")
	}
	// Verificamos que el mensaje explica el problema (sin atarnos al texto exacto)
	if !contains(*updated.Error, "filesystem") {
		t.Errorf("error message should mention filesystem state: %q", *updated.Error)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// destroy_pool — caso A: FS destruido → completed
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryDestroyPoolCompleted(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Crear primero el pool en DB (simulando el estado antes del destroy)
	tx, _ := service.db.BeginTx(ctx, nil)
	poolID := newUUID()
	service.repo.CreatePool(ctx, tx, &Pool{
		ID:         poolID,
		Name:       "doomed",
		BtrfsUUID:  "uuid-to-destroy",
		Profile:    ProfileRaid1,
		MountPoint: "/nimos/pools/doomed",
	})
	tx.Commit()

	// Mock: el FS ya no existe (el destroy físico se completó antes del crash)
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return false, nil
	}

	op := &Operation{
		Type:   OpTypeDestroyPool,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       "doomed",
			"btrfs_uuid": "uuid-to-destroy",
		}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.Completed != 1 {
		t.Errorf("Completed: got %d, want 1 (result: %+v)", result.Completed, result)
	}

	// El pool YA no debe estar en DB (recovery lo limpia)
	pool, _ := service.repo.GetPool(ctx, poolID)
	if pool != nil {
		t.Error("pool should be deleted from DB after destroy recovery")
	}

	// La operation está marcada completed
	updated, _ := service.repo.GetOperation(ctx, op.ID)
	if updated.Status != OpStatusCompleted {
		t.Errorf("status: got %q, want completed", updated.Status)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// destroy_pool — caso B: FS sigue existiendo → inconclusive
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryDestroyPoolInconclusive(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tx, _ := service.db.BeginTx(ctx, nil)
	poolID := newUUID()
	service.repo.CreatePool(ctx, tx, &Pool{
		ID:         poolID,
		Name:       "halfdestroyed",
		BtrfsUUID:  "uuid-halfdestroyed",
		Profile:    ProfileRaid1,
		MountPoint: "/nimos/pools/halfdestroyed",
	})
	tx.Commit()

	// Mock: el FS aún existe (umount o wipefs no ejecutaron)
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return true, nil
	}

	op := &Operation{
		Type:   OpTypeDestroyPool,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       "halfdestroyed",
			"btrfs_uuid": "uuid-halfdestroyed",
		}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.Inconclusive != 1 {
		t.Errorf("Inconclusive: got %d, want 1 (result: %+v)", result.Inconclusive, result)
	}

	// IMPORTANTE: el pool DEBE seguir en DB (no borramos ante inconclusive)
	pool, _ := service.repo.GetPool(ctx, poolID)
	if pool == nil {
		t.Error("pool should NOT be deleted when destroy is inconclusive")
	}

	updated, _ := service.repo.GetOperation(ctx, op.ID)
	if updated.ErrorCode == nil || *updated.ErrorCode != ErrCodeRecoveryInconclusive {
		t.Errorf("error_code: got %v, want %q", updated.ErrorCode, ErrCodeRecoveryInconclusive)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// add_device, remove_device, replace_device, convert_profile → inconclusive
// ─────────────────────────────────────────────────────────────────────────────

// Sin balance activo, las layout ops huérfanas son inconclusive (no podemos
// saber si terminaron). Con balance activo se re-adoptan (ver test siguiente).
func TestStorageRecoveryLayoutOpsInconclusiveWhenNoBalance(t *testing.T) {
	cases := []OperationType{
		OpTypeAddDevice,
		OpTypeRemoveDevice,
		OpTypeReplaceDevice,
		OpTypeConvertProfile,
	}
	// Inyectar: no hay balance activo en ningún pool.
	prev := readBalanceStatusFn
	readBalanceStatusFn = func(mp string) BalanceStatus { return BalanceStatus{Active: false} }
	defer func() { readBalanceStatusFn = prev }()

	for _, opType := range cases {
		t.Run(string(opType), func(t *testing.T) {
			service, _, cleanup := setupTestService(t)
			defer cleanup()
			ctx := context.Background()

			// La op requiere pool existente por FK
			tx, _ := service.db.BeginTx(ctx, nil)
			poolID := newUUID()
			service.repo.CreatePool(ctx, tx, &Pool{
				ID:         poolID,
				Name:       fmt.Sprintf("layout-%s", opType),
				BtrfsUUID:  fmt.Sprintf("uuid-%s", opType),
				Profile:    ProfileRaid1,
				MountPoint: "/nimos/pools/x",
			})
			tx.Commit()

			op := &Operation{
				Type:   opType,
				PoolID: &poolID,
				Status: OpStatusInProgress,
				Data:   rawJSON(map[string]interface{}{}),
			}
			injectOrphanOp(t, service, op)

			result, _ := service.RecoverPendingOperations(ctx)
			if result.Inconclusive != 1 {
				t.Errorf("%s: Inconclusive got %d, want 1", opType, result.Inconclusive)
			}
			if result.Readopted != 0 {
				t.Errorf("%s: Readopted got %d, want 0 (sin balance activo)", opType, result.Readopted)
			}
		})
	}
}

// P3: con balance BTRFS vivo, la op de layout huérfana se RE-ADOPTA (se mantiene
// in_progress, el lock se conserva) en vez de marcarse failed.
func TestStorageRecoveryLayoutOpReadoptedWhenBalanceActive(t *testing.T) {
	prev := readBalanceStatusFn
	readBalanceStatusFn = func(mp string) BalanceStatus {
		return BalanceStatus{Active: true, PercentDone: 42}
	}
	defer func() { readBalanceStatusFn = prev }()

	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tx, _ := service.db.BeginTx(ctx, nil)
	poolID := newUUID()
	service.repo.CreatePool(ctx, tx, &Pool{
		ID:         poolID,
		Name:       "readopt-pool",
		BtrfsUUID:  "uuid-readopt",
		Profile:    ProfileRaid1,
		MountPoint: "/nimos/pools/readopt",
	})
	tx.Commit()

	op := &Operation{
		Type:   OpTypeConvertProfile,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{}),
	}
	injectOrphanOp(t, service, op)

	result, _ := service.RecoverPendingOperations(ctx)
	if result.Readopted != 1 {
		t.Errorf("Readopted got %d, want 1 (balance activo)", result.Readopted)
	}
	if result.Inconclusive != 0 {
		t.Errorf("Inconclusive got %d, want 0", result.Inconclusive)
	}

	// La op debe seguir in_progress (lock conservado), no failed.
	got, err := service.repo.GetOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if got.Status != OpStatusInProgress {
		t.Errorf("op re-adoptada: status got %s, want in_progress", got.Status)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// import_pool — recovery (STOR-01 R4)
// ─────────────────────────────────────────────────────────────────────────────

// Caso A: el pool quedó registrado en la BD → import completó → completed.
func TestStorageRecoveryImportPoolCompleted(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// El pool SÍ está en la BD (el import llegó a persistir antes del crash).
	tx, _ := service.db.BeginTx(ctx, nil)
	poolID := newUUID()
	service.repo.CreatePool(ctx, tx, &Pool{
		ID:         poolID,
		Name:       "imported-ok",
		BtrfsUUID:  "uuid-import-done",
		Profile:    ProfileSingle,
		MountPoint: "/nimos/pools/imported-ok",
	})
	tx.Commit()

	op := &Operation{
		Type:   OpTypeImportPool,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       "imported-ok",
			"btrfs_uuid": "uuid-import-done",
		}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.Completed != 1 {
		t.Errorf("Completed: got %d, want 1 (result: %+v)", result.Completed, result)
	}

	// El pool sigue en la BD (import completó, no se revierte)
	pool, _ := service.repo.GetPool(ctx, poolID)
	if pool == nil {
		t.Error("pool should remain in DB after completed import recovery")
	}
}

// Caso B: el pool NO está en la BD → import no completó → rolled_back.
// Seguro porque import no daña el FS de origen (sigue intacto en disco).
func TestStorageRecoveryImportPoolRolledBack(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// El pool NO está en la BD (el import murió antes de persistir).
	op := &Operation{
		Type:   OpTypeImportPool,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       "imported-fail",
			"btrfs_uuid": "uuid-import-never-persisted",
		}),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}
	if result.RolledBack != 1 {
		t.Errorf("RolledBack: got %d, want 1 (result: %+v)", result.RolledBack, result)
	}

	// El recovery marca la op como failed + ErrorCode=recovery_rolled_back
	// (el contador RolledBack se deriva de ese código, no de un estado propio).
	updated, _ := service.repo.GetOperation(ctx, op.ID)
	if updated.Status != OpStatusFailed {
		t.Errorf("status: got %q, want failed (rolled_back se marca vía ErrorCode)", updated.Status)
	}
	if updated.ErrorCode == nil || *updated.ErrorCode != ErrCodeRecoveryRolledBack {
		t.Errorf("ErrorCode: got %v, want %q", updated.ErrorCode, ErrCodeRecoveryRolledBack)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multiple operations en un solo recovery
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryMultipleOrphans(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Inyectar 3 operations huérfanas de distintos tipos
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		// El pool "exists-pool" sí existe en BTRFS, los demás no
		return uuid == "exists-pool", nil
	}

	// 1. create_pool rolled_back (no existe en BTRFS)
	op1 := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "rolled-back-pool"}),
	}
	injectOrphanOp(t, service, op1)

	// 2. create_pool inconclusive (sí existe en BTRFS pero no en DB)
	op2 := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "exists-pool"}),
	}
	injectOrphanOp(t, service, op2)

	// 3. add_device → inconclusive siempre (requiere pool existente por FK)
	tx, _ := service.db.BeginTx(ctx, nil)
	poolID := newUUID()
	service.repo.CreatePool(ctx, tx, &Pool{
		ID:         poolID,
		Name:       "pool-3",
		BtrfsUUID:  "pool-3-uuid",
		Profile:    ProfileRaid1,
		MountPoint: "/nimos/pools/pool-3",
	})
	tx.Commit()

	op3 := &Operation{
		Type:   OpTypeAddDevice,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{}),
	}
	injectOrphanOp(t, service, op3)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations: %v", err)
	}

	if result.Inspected != 3 {
		t.Errorf("Inspected: got %d, want 3", result.Inspected)
	}
	if result.RolledBack != 1 {
		t.Errorf("RolledBack: got %d, want 1 (result: %+v)", result.RolledBack, result)
	}
	if result.Inconclusive != 2 {
		t.Errorf("Inconclusive: got %d, want 2 (result: %+v)", result.Inconclusive, result)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Idempotencia: ejecutar recovery dos veces es seguro
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryIdempotent(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return false, nil
	}

	op := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "x"}),
	}
	injectOrphanOp(t, service, op)

	// Primera ejecución resuelve la op
	r1, _ := service.RecoverPendingOperations(ctx)
	if r1.Inspected != 1 {
		t.Errorf("first run: Inspected got %d, want 1", r1.Inspected)
	}

	// Segunda ejecución no encuentra nada (la op está failed, no in_progress)
	r2, _ := service.RecoverPendingOperations(ctx)
	if r2.Inspected != 0 {
		t.Errorf("second run: Inspected got %d, want 0 (op already resolved)", r2.Inspected)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error de BTRFS (no podemos determinar) → inconclusive
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryBtrfsErrorMeansInconclusive(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Mock: BTRFS falla al consultar (kernel sin btrfs, permisos, etc.)
	mock.FilesystemExistsByUUIDFn = func(ctx context.Context, uuid string) (bool, error) {
		return false, fmt.Errorf("btrfs command not found")
	}

	op := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]interface{}{"name": "unknown-state"}),
	}
	injectOrphanOp(t, service, op)

	result, _ := service.RecoverPendingOperations(ctx)
	if result.Inconclusive != 1 {
		t.Errorf("Inconclusive: got %d, want 1 (ante error BTRFS, inconclusive)",
			result.Inconclusive)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Malformed Data en la operation → inconclusive (sin crashear)
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRecoveryMalformedDataInconclusive(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Insertar op con Data malformada (JSON inválido)
	op := &Operation{
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data:   json.RawMessage(`{not valid json`),
	}
	injectOrphanOp(t, service, op)

	result, err := service.RecoverPendingOperations(ctx)
	if err != nil {
		t.Fatalf("RecoverPendingOperations should not error on bad data: %v", err)
	}
	if result.Inconclusive != 1 {
		t.Errorf("Inconclusive: got %d, want 1", result.Inconclusive)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// contains es un strings.Contains pero sin import duplicado
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
