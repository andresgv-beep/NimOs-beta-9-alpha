// storage_repo_ops_test.go — Tests de Operations, Events, Capabilities.
//
// Ejecutar:
//   go test -run TestStorageRepoOps -v
//
// Reutilizan setupTestDB de storage_repo_test.go.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// OPERATIONS
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRepoOpsCreate(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool primero (op tendrá FK al pool)
	poolID := "pool-1"
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileRaid1, MountPoint: "/m",
		})
	})

	// Crear operación
	op := &Operation{
		ID:     "op-1",
		Type:   OpTypeCreatePool,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data:   json.RawMessage(`{"name":"data","profile":"raid1"}`),
	}
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreateOperation(ctx, tx, op)
	})

	// Recuperar y verificar
	got, err := repo.GetOperation(ctx, "op-1")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if got == nil {
		t.Fatal("operation not found")
	}
	if got.Type != OpTypeCreatePool {
		t.Errorf("Type: got %q", got.Type)
	}
	if got.Status != OpStatusInProgress {
		t.Errorf("Status: got %q", got.Status)
	}
	if got.PoolID == nil || *got.PoolID != poolID {
		t.Errorf("PoolID: got %v", got.PoolID)
	}
	if string(got.Data) != `{"name":"data","profile":"raid1"}` {
		t.Errorf("Data: got %s", string(got.Data))
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set automatically")
	}
	if got.CompletedAt != nil {
		t.Error("CompletedAt should be nil for in_progress op")
	}
}

func TestStorageRepoOpsUpdateStatus(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: "p1", Name: "a", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		poolID := "p1"
		return repo.CreateOperation(ctx, tx, &Operation{
			ID:     "op-update",
			Type:   OpTypeAddDevice,
			PoolID: &poolID,
			Status: OpStatusInProgress,
		})
	})

	// Update a completed
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.UpdateOperationStatus(ctx, tx, "op-update", OpStatusCompleted, nil, nil)
	})

	got, _ := repo.GetOperation(ctx, "op-update")
	if got.Status != OpStatusCompleted {
		t.Errorf("Status: got %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set for terminal status")
	}

	// Update a failed con error
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-fail", Type: OpTypeAddDevice, Status: OpStatusInProgress,
		}); err != nil {
			return err
		}
		errMsg := "device not found"
		errCode := ErrCodeDeviceMissing
		return repo.UpdateOperationStatus(ctx, tx, "op-fail", OpStatusFailed, &errMsg, &errCode)
	})

	got, _ = repo.GetOperation(ctx, "op-fail")
	if got.Status != OpStatusFailed {
		t.Errorf("Status: got %q", got.Status)
	}
	if got.Error == nil || *got.Error != "device not found" {
		t.Errorf("Error: got %v", got.Error)
	}
	if got.ErrorCode == nil || *got.ErrorCode != ErrCodeDeviceMissing {
		t.Errorf("ErrorCode: got %v", got.ErrorCode)
	}
}

func TestStorageRepoOpsListPending(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		// 1 completed, 1 in_progress, 1 pending, 1 failed
		if err := repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-done", Type: OpTypeRenamePool, PoolID: &poolID,
			Status: OpStatusCompleted,
		}); err != nil {
			return err
		}
		if err := repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-running", Type: OpTypeStartScrub, PoolID: &poolID,
			Status: OpStatusInProgress,
		}); err != nil {
			return err
		}
		// Otro pool para el pending (porque INV-1/INV-2 nos restringen)
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: "p2", Name: "other", BtrfsUUID: "u2",
			Profile: ProfileSingle, MountPoint: "/m2",
		}); err != nil {
			return err
		}
		poolID2 := "p2"
		if err := repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-pending", Type: OpTypeAddDevice, PoolID: &poolID2,
			Status: OpStatusPending,
		}); err != nil {
			return err
		}
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-failed", Type: OpTypeReplaceDevice,
			Status: OpStatusFailed,
		})
	})

	pending, err := repo.ListPendingOperations(ctx)
	if err != nil {
		t.Fatalf("ListPendingOperations: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending count: got %d, want 2", len(pending))
		for _, p := range pending {
			t.Logf("  - %s: %s", p.ID, p.Status)
		}
	}
}

func TestStorageRepoOpsINV1LayoutExclusion(t *testing.T) {
	// Verifica la invariante del schema (UNIQUE parcial idx_one_layout_op_per_pool):
	// solo una layout-op activa por pool a la vez.
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileRaid1, MountPoint: "/m",
		}); err != nil {
			return err
		}
		// Primera layout-op (add_device) in_progress
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-1", Type: OpTypeAddDevice, PoolID: &poolID,
			Status: OpStatusInProgress,
		})
	})

	// Segunda layout-op (replace_device) en el mismo pool debe fallar
	tx, _ := conn.BeginTx(ctx, nil)
	err := repo.CreateOperation(ctx, tx, &Operation{
		ID: "op-2", Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusPending,
	})
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected UNIQUE constraint violation for concurrent layout-ops on same pool")
	}

	// Pero create_snapshot SÍ debe permitirse (no es layout)
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-snap", Type: OpTypeCreateSnapshot, PoolID: &poolID,
			Status: OpStatusInProgress,
		})
	})
}

func TestStorageRepoOpsINV2ScrubExclusion(t *testing.T) {
	// Verifica idx_one_scrub_per_pool.
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileRaid1, MountPoint: "/m",
		}); err != nil {
			return err
		}
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "scrub-1", Type: OpTypeStartScrub, PoolID: &poolID,
			Status: OpStatusInProgress,
		})
	})

	// Segundo scrub en el mismo pool debe fallar
	tx, _ := conn.BeginTx(ctx, nil)
	err := repo.CreateOperation(ctx, tx, &Operation{
		ID: "scrub-2", Type: OpTypeStartScrub, PoolID: &poolID,
		Status: OpStatusPending,
	})
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected UNIQUE constraint violation for concurrent scrubs on same pool")
	}

	// Scrub en OTRO pool sí debe permitirse
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: "p2", Name: "other", BtrfsUUID: "u2",
			Profile: ProfileSingle, MountPoint: "/m2",
		}); err != nil {
			return err
		}
		poolID2 := "p2"
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "scrub-3", Type: OpTypeStartScrub, PoolID: &poolID2,
			Status: OpStatusInProgress,
		})
	})
}

func TestStorageRepoOpsHistoryPreservedOnPoolDelete(t *testing.T) {
	// Cuando se borra un pool, las operations deben conservarse con
	// pool_id NULL (ON DELETE SET NULL).
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-historic", Type: OpTypeCreatePool, PoolID: &poolID,
			Status: OpStatusCompleted,
		})
	})

	// Borrar pool
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.DeletePool(ctx, tx, poolID)
	})

	// La operación sigue existiendo con pool_id = NULL
	got, _ := repo.GetOperation(ctx, "op-historic")
	if got == nil {
		t.Fatal("Operation should still exist after pool delete")
	}
	if got.PoolID != nil {
		t.Errorf("PoolID should be NULL after pool delete, got %v", *got.PoolID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EVENTS
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRepoEvents(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool + op
	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		return repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-with-events", Type: OpTypeCreatePool, PoolID: &poolID,
			Status: OpStatusInProgress,
		})
	})

	// Añadir 3 eventos
	withTx(t, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		events := []*Event{
			{ID: "e1", OperationID: "op-with-events",
				Timestamp: now, Level: EventLevelInfo,
				Message: "Operation started"},
			{ID: "e2", OperationID: "op-with-events",
				Timestamp: now.Add(time.Second), Level: EventLevelInfo,
				Message: "Wiping disk /dev/sdb"},
			{ID: "e3", OperationID: "op-with-events",
				Timestamp: now.Add(2 * time.Second), Level: EventLevelInfo,
				Message: "mkfs.btrfs completed"},
		}
		for _, e := range events {
			if err := repo.AppendEvent(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	})

	// Recuperar y verificar orden cronológico
	events, err := repo.ListEvents(ctx, "op-with-events")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events: got %d, want 3", len(events))
	}
	if events[0].Message != "Operation started" {
		t.Errorf("first event: got %q", events[0].Message)
	}
	if events[2].Message != "mkfs.btrfs completed" {
		t.Errorf("last event: got %q", events[2].Message)
	}
}

func TestStorageRepoEventsCascadeOnOpDelete(t *testing.T) {
	// Verifica que borrar una Operation borra sus events (CASCADE).
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreateOperation(ctx, tx, &Operation{
			ID: "op-doomed", Type: OpTypeRenamePool, Status: OpStatusCompleted,
		}); err != nil {
			return err
		}
		return repo.AppendEvent(ctx, tx, &Event{
			ID: "e-doomed", OperationID: "op-doomed",
			Level: EventLevelInfo, Message: "doomed event",
		})
	})

	// Verificar que existe
	events, _ := repo.ListEvents(ctx, "op-doomed")
	if len(events) != 1 {
		t.Fatalf("setup: events should be 1, got %d", len(events))
	}

	// Borrar la operation manualmente (CASCADE debe limpiar events)
	tx, _ := conn.BeginTx(ctx, nil)
	tx.ExecContext(ctx, "DELETE FROM storage_operations WHERE id = ?", "op-doomed")
	tx.Commit()

	events, _ = repo.ListEvents(ctx, "op-doomed")
	if len(events) != 0 {
		t.Errorf("events after op delete: got %d, want 0 (CASCADE)", len(events))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CAPABILITIES
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRepoCapabilities(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileRaid1, MountPoint: "/m",
		})
	})

	// Sin capabilities al inicio
	caps, err := repo.GetPoolCapabilities(ctx, poolID)
	if err != nil {
		t.Fatalf("GetPoolCapabilities: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("initial caps: got %d, want 0", len(caps))
	}

	// Set capabilities por defecto
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.SetPoolCapabilities(ctx, tx, poolID, DefaultBtrfsManagedCapabilities())
	})

	caps, _ = repo.GetPoolCapabilities(ctx, poolID)
	if len(caps) != 8 {
		t.Errorf("after set: got %d caps, want 8 (full BTRFS managed set)", len(caps))
	}

	// HasCapability funciona
	has, err := repo.HasCapability(ctx, poolID, "snapshots")
	if err != nil {
		t.Fatalf("HasCapability: %v", err)
	}
	if !has {
		t.Error("snapshots should be supported")
	}

	has, _ = repo.HasCapability(ctx, poolID, "nonexistent")
	if has {
		t.Error("nonexistent should NOT be supported")
	}

	// SetPoolCapabilities reemplaza (no añade): solo 2 caps ahora
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.SetPoolCapabilities(ctx, tx, poolID, []string{"scrub", "compression"})
	})

	caps, _ = repo.GetPoolCapabilities(ctx, poolID)
	if len(caps) != 2 {
		t.Errorf("after replace: got %d, want 2", len(caps))
	}

	has, _ = repo.HasCapability(ctx, poolID, "snapshots")
	if has {
		t.Error("snapshots should NOT be supported after replace")
	}
}

func TestStorageRepoCapabilitiesCascade(t *testing.T) {
	// Borrar pool debe limpiar sus capabilities (CASCADE).
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	poolID := "p1"
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: poolID, Name: "data", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		return repo.SetPoolCapabilities(ctx, tx, poolID, DefaultBtrfsManagedCapabilities())
	})

	caps, _ := repo.GetPoolCapabilities(ctx, poolID)
	if len(caps) != 8 {
		t.Fatalf("setup: got %d caps", len(caps))
	}

	// Borrar pool
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.DeletePool(ctx, tx, poolID)
	})

	caps, _ = repo.GetPoolCapabilities(ctx, poolID)
	if len(caps) != 0 {
		t.Errorf("after pool delete: got %d caps, want 0 (CASCADE)", len(caps))
	}
}
