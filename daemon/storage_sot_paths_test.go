package main

import (
	"context"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fase C · SOT-04: paths verificados en operaciones de device.
// resolveDevicePath prefiere by-id si existe, cae a current_path, y devuelve ""
// si ninguno vive. Remove/Replace/Wipe deben fallar limpio cuando "" en vez de
// pasar un path muerto a btrfs.
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveDevicePath_PrefersByID_WhenExists(t *testing.T) {
	orig := devicePathExists
	defer func() { devicePathExists = orig }()
	devicePathExists = func(p string) bool { return true } // todo existe

	d := &Device{ByIDPath: "/dev/disk/by-id/ata-X", CurrentPath: "/dev/sdb"}
	if got := resolveDevicePath(d); got != "/dev/disk/by-id/ata-X" {
		t.Errorf("got %q, want el by-id (preferido cuando existe)", got)
	}
}

func TestResolveDevicePath_FallsBackToCurrent_WhenByIDDead(t *testing.T) {
	orig := devicePathExists
	defer func() { devicePathExists = orig }()
	// by-id muerto, current vivo (el caso real del ST320LT020 de hoy)
	devicePathExists = func(p string) bool { return p == "/dev/sdb" }

	d := &Device{ByIDPath: "/dev/disk/by-id/ata-OBSOLETO", CurrentPath: "/dev/sdb"}
	if got := resolveDevicePath(d); got != "/dev/sdb" {
		t.Errorf("got %q, want /dev/sdb (fallback porque el by-id está muerto)", got)
	}
}

func TestResolveDevicePath_Empty_WhenNoneExist(t *testing.T) {
	orig := devicePathExists
	defer func() { devicePathExists = orig }()
	devicePathExists = func(p string) bool { return false } // nada existe

	d := &Device{ByIDPath: "/dev/disk/by-id/ata-X", CurrentPath: "/dev/sdb"}
	if got := resolveDevicePath(d); got != "" {
		t.Errorf("got %q, want \"\" (ningún path vive)", got)
	}
}

func TestResolveDevicePath_NilSafe(t *testing.T) {
	if got := resolveDevicePath(nil); got != "" {
		t.Errorf("got %q, want \"\" para device nil", got)
	}
}

// RemoveDevice debe fallar limpio (sin tocar el executor) si el path está muerto.
func TestRemoveDevice_FailsClean_WhenPathDead(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Pool raid1 con 3 discos: quitar 1 deja 2, que sigue cumpliendo raid1
	// (MinDisks=2). Así la validación pasa y llegamos a la resolución de path.
	poolID, _ := createTestPool(t, service, ctx, "data", ProfileRaid1, 3)

	// El device a quitar: forzamos que su path NO exista
	pool, _ := service.GetPool(ctx, poolID)
	target := pool.Devices[0]

	mock.Reset()
	// Sobrescribir SOLO para esta operación: ningún path vive
	orig := devicePathExists
	devicePathExists = func(string) bool { return false }
	defer func() { devicePathExists = orig }()

	op, err := service.RemoveDevice(ctx, RemoveDeviceRequest{PoolID: poolID, DeviceID: target.ID})
	if err != nil {
		t.Fatalf("RemoveDevice devolvió error de servicio inesperado: %v", err)
	}
	if op.Status != OpStatusFailed {
		t.Errorf("op.Status: got %q, want failed (path muerto)", op.Status)
	}
	// Lo crítico: NO se llamó al executor con un path muerto
	if len(mock.RemoveDeviceCalls) != 0 {
		t.Errorf("executor RemoveDevice fue llamado %d veces; no debe llamarse con path muerto", len(mock.RemoveDeviceCalls))
	}
}
