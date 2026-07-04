// storage_reconciler_membership_test.go — F5: reconstrucción de membresía
// pool→device desde la verdad de btrfs (reconcilePoolMembership).

package main

import (
	"context"
	"testing"
	"time"
)

// TestReconcilePoolMembershipAddsObservedDevice — F5: si btrfs reporta un device
// online en el FS de un pool managed que la BD no tiene en storage_pool_devices
// (caso típico: `btrfs replace`/`device add` hecho por CLI, fuera del daemon),
// el reconciliador lo asigna al pool. Solo añade; idempotente.
func TestReconcilePoolMembershipAddsObservedDevice(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	pool, err := service.GetPool(ctx, poolID)
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if len(pool.Devices) != 2 {
		t.Fatalf("setup: el pool debería tener 2 devices, tiene %d", len(pool.Devices))
	}

	// Registrar un disco EXTRA en la BD (aún sin membresía) — simula el disco
	// nuevo que un `btrfs replace` por CLI metió en el FS.
	const extraByID = "/dev/disk/by-id/f5-extra"
	tx, _ := service.db.BeginTx(ctx, nil)
	if _, err := service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "f5-extra", Serial: "F5-EXTRA",
		ByIDPath: extraByID, CurrentPath: "/dev/sdx",
		SizeBytes: 2e12,
	}); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	tx.Commit()

	// btrfs (observer) ahora reporta ese disco extra como miembro online del FS
	// del pool, pero la BD todavía no lo tiene asignado.
	orig := observerFilesystemsFn
	defer func() { observerFilesystemsFn = orig }()
	observerFilesystemsFn = func() []ObservedBtrfs {
		return []ObservedBtrfs{{
			UUID: pool.BtrfsUUID,
			Devices: []ObservedDevice{
				{ByIDPath: extraByID, Path: "/dev/sdx", Present: true, InFS: pool.BtrfsUUID},
			},
		}}
	}

	r := NewDeviceReconciler(service, NewFakeClock(time.Now()), DefaultReconcilerConfig())
	if err := r.reconcilePoolMembership(ctx); err != nil {
		t.Fatalf("reconcilePoolMembership: %v", err)
	}

	// El disco extra debe estar ahora asignado al pool.
	got, _ := service.repo.ListDevicesInPool(ctx, poolID)
	found := false
	for _, d := range got {
		if d.ID == "f5-extra" {
			found = true
		}
	}
	if !found {
		t.Errorf("F5: el device observado por btrfs NO se asignó al pool (miembros=%d)", len(got))
	}

	// Idempotencia: un segundo ciclo no duplica ni falla.
	if err := r.reconcilePoolMembership(ctx); err != nil {
		t.Fatalf("segundo ciclo: %v", err)
	}
	got2, _ := service.repo.ListDevicesInPool(ctx, poolID)
	if len(got2) != len(got) {
		t.Errorf("idempotencia: los miembros cambiaron en el 2º ciclo: %d → %d", len(got), len(got2))
	}
}

// TestReconcilePoolMembershipIgnoresUnobservedPool — si btrfs no reporta el FS
// del pool (p.ej. desmontado), el reconciliador NO toca su membresía: es
// conservador y no infiere ausencia como una razón para desasignar.
func TestReconcilePoolMembershipIgnoresUnobservedPool(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	before, _ := service.repo.ListDevicesInPool(ctx, poolID)

	orig := observerFilesystemsFn
	defer func() { observerFilesystemsFn = orig }()
	observerFilesystemsFn = func() []ObservedBtrfs {
		return []ObservedBtrfs{{UUID: "uuid-de-otro-fs-que-no-es-el-pool"}}
	}

	r := NewDeviceReconciler(service, NewFakeClock(time.Now()), DefaultReconcilerConfig())
	if err := r.reconcilePoolMembership(ctx); err != nil {
		t.Fatalf("reconcilePoolMembership: %v", err)
	}
	after, _ := service.repo.ListDevicesInPool(ctx, poolID)
	if len(after) != len(before) {
		t.Errorf("la membresía cambió para un pool no observado: %d → %d", len(before), len(after))
	}
}
