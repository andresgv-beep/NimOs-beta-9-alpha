package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"
)

func TestBalanceStatusCommandFailures(t *testing.T) {
	for _, tc := range []struct {
		name, output  string
		code          int
		known, active bool
	}{
		{"idle", "No balance found on '/pool'", 0, true, false},
		{"running", "Balance on '/pool' is running", 0, true, true},
		{"running exit one", "Balance on '/pool' is running", 1, true, true},
		{"paused", "Balance on '/pool' is paused", 0, true, true},
		{"empty", "", 0, false, false},
		{"permission", "ERROR: permission denied", 1, false, false},
		{"unmounted", "ERROR: not a btrfs filesystem", 1, false, false},
		{"unexpected", "unknown future format", 0, false, false},
		{"error after status", "No balance found on '/pool'\nERROR: I/O error", 0, false, false},
		{"failed idle command", "No balance found on '/pool'", 1, false, false},
		{"killed", "Balance on '/pool' is running", -1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := balanceStatusFromCommand(tc.output, tc.code)
			if got.Known != tc.known || got.Active != tc.active {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func setupRecoveryBalance(t *testing.T) (*StorageService, *Operation, string) {
	t.Helper()
	service, _, cleanup := setupTestService(t)
	t.Cleanup(cleanup)
	poolID := newUUID()
	err := service.runInTx(context.Background(), func(tx *sql.Tx) error {
		return service.repo.CreatePool(context.Background(), tx, &Pool{
			ID: poolID, Name: "reliability", BtrfsUUID: newUUID(), Profile: ProfileSingle,
			ControlState: ControlStateManaged, MountPoint: "/nimos/pools/reliability",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	op := &Operation{Type: OpTypeConvertProfile, PoolID: &poolID, Status: OpStatusInProgress,
		Data: rawJSON(map[string]string{"to_profile": "raid1"})}
	injectOrphanOp(t, service, op)
	return service, op, poolID
}

func assertQuarantined(t *testing.T, service *StorageService, op *Operation, poolID string) {
	t.Helper()
	ctx := context.Background()
	got, err := service.repo.GetOperation(ctx, op.ID)
	if err != nil || got == nil {
		t.Fatalf("operation: %v", err)
	}
	if got.Status != OpStatusFailed || got.ErrorCode == nil || *got.ErrorCode != ErrCodeRecoveryInconclusive {
		t.Fatalf("operation falsely resolved: %+v", got)
	}
	pool, err := service.repo.GetPool(ctx, poolID)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ControlState != ControlStateRecovery || pool.Profile != ProfileSingle {
		t.Fatalf("pool: %+v", pool)
	}
	for _, kind := range []OperationType{OpTypeAddDevice, OpTypeRemoveDevice, OpTypeReplaceDevice, OpTypeConvertProfile} {
		if NewPolicyChecker().Allows(pool, kind) {
			t.Fatalf("unsafe operation %s allowed", kind)
		}
	}
}

func TestRecoveredBalanceNeverInfersSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   BalanceStatus
		maxWait time.Duration
	}{
		{"query failed", BalanceStatus{}, time.Hour},
		{"ended or cancelled", BalanceStatus{Known: true}, time.Hour},
		{"timeout while running", BalanceStatus{Known: true, Active: true}, -time.Second},
		{"timeout while paused", BalanceStatus{Known: true, Active: true, Paused: true}, -time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, op, poolID := setupRecoveryBalance(t)
			service.watchRecoveredBalance(context.Background(), op.ID, poolID, "/pool", func(string) BalanceStatus { return tc.state }, time.Millisecond, tc.maxWait)
			assertQuarantined(t, service, op, poolID)
		})
	}
}

func TestRecoveredBalanceShutdownRetainsLock(t *testing.T) {
	service, op, poolID := setupRecoveryBalance(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.watchRecoveredBalance(ctx, op.ID, poolID, "/pool", func(string) BalanceStatus { return BalanceStatus{Known: true, Active: true} }, time.Hour, time.Hour)
	got, err := service.repo.GetOperation(context.Background(), op.ID)
	if err != nil || got.Status != OpStatusInProgress {
		t.Fatalf("lock lost: %+v %v", got, err)
	}
}

func TestRecoveryUnknownBalanceQuarantinesPool(t *testing.T) {
	service, op, poolID := setupRecoveryBalance(t)
	prev := readBalanceStatusFn
	readBalanceStatusFn = func(string) BalanceStatus { return BalanceStatus{} }
	defer func() { readBalanceStatusFn = prev }()
	if _, err := service.RecoverPendingOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertQuarantined(t, service, op, poolID)
	// The UI must be able to query this exact operation, not a newer one.
	ops, err := service.repo.ListOperations(context.Background(), OperationFilter{ID: &op.ID, PoolID: &poolID})
	if err != nil || len(ops) != 1 || ops[0].ID != op.ID {
		t.Fatalf("exact operation lookup: %+v %v", ops, err)
	}
	missing := "nonexistent"
	ops, err = service.repo.ListOperations(context.Background(), OperationFilter{ID: &missing})
	if err != nil || len(ops) != 0 {
		t.Fatalf("missing id returned unrelated operations: %+v %v", ops, err)
	}
}

func TestRecoveryQuarantineFailureRetainsLock(t *testing.T) {
	service, op, poolID := setupRecoveryBalance(t)
	_, err := service.db.Exec(`CREATE TRIGGER fail_recovery BEFORE UPDATE OF control_state ON storage_pools BEGIN SELECT RAISE(ABORT, 'injected persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	service.quarantineLayoutOperation(context.Background(), op.ID, poolID, "injected query error")
	got, err := service.repo.GetOperation(context.Background(), op.ID)
	if err != nil || got.Status != OpStatusInProgress {
		t.Fatalf("lock lost after persistence failure: %+v %v", got, err)
	}
}

// Opt-in real kernel check, invoked only inside the isolated loop-device harness.
func TestBalanceStatusVirtualBtrfs(t *testing.T) {
	mountPoint := os.Getenv("NIMOS_BTRFS_TEST_MOUNT")
	if mountPoint == "" {
		t.Skip("requires tests/storage/btrfs_virtual.sh")
	}
	st := readBalanceStatus(mountPoint)
	if !st.Known || st.Active {
		t.Fatalf("real idle Btrfs status: %+v", st)
	}
	st = readBalanceStatus(mountPoint + "/nonexistent")
	if st.Known {
		t.Fatalf("inaccessible filesystem incorrectly known: %+v", st)
	}
}

func TestStorageConvertFailureQuarantinesPartialLayout(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()
	poolID, _ := createTestPool(t, service, ctx, "partial", ProfileSingle, 2)
	mock.ConvertProfileFn = func(context.Context, string, Profile) error { return errors.New("ENOSPC after partial conversion") }
	op, err := service.ConvertProfile(ctx, ConvertProfileRequest{PoolID: poolID, NewProfile: ProfileRaid1})
	if err != nil {
		t.Fatal(err)
	}
	waitForOperation(t, service, ctx, op.ID, 3*time.Second)
	assertQuarantined(t, service, op, poolID)
	if _, err = service.ConvertProfile(ctx, ConvertProfileRequest{PoolID: poolID, NewProfile: ProfileRaid1}); err == nil {
		t.Fatal("second conversion accepted on partial layout")
	}
}

func TestStorageOperationHTTPFiltersExactID(t *testing.T) {
	service, op, poolID := setupRecoveryBalance(t)
	handler := NewStorageHTTPHandler(service)
	for _, tc := range []struct {
		id    string
		count int
	}{{op.ID, 1}, {"nonexistent", 0}} {
		rec := doRequest(handler.handleOperations, "GET", "/api/storage/v2/operations?id="+tc.id+"&pool_id="+poolID, "")
		if rec.Code != 200 {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		var ops []Operation
		decodeData(t, rec, &ops)
		if len(ops) != tc.count {
			t.Fatalf("got %d operations, want %d", len(ops), tc.count)
		}
		if len(ops) > 0 && ops[0].ID != op.ID {
			t.Fatal("returned a different operation")
		}
	}
}
