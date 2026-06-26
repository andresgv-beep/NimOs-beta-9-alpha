// storage_layout_guard_test.go — T11 · El gate de montaje de las ops de layout.
//
// Reproduce el incidente data8: una op de layout sobre un pool NO montado debe
// dar un error claro (mount_missing), no dejar que btrfs escupa el críptico
// "not a btrfs filesystem". Sin assertLayoutOpAllowed, las ops tocaban btrfs a
// ciegas; con el gate, fallan limpio antes.

package main

import (
	"errors"
	"testing"
)

func layoutChecks(mounted, readOnly bool) poolWritableChecks {
	return poolWritableChecks{
		mountedPool: func(string) bool { return mounted },
		readOnly:    func(string) bool { return readOnly },
	}
}

// Pool montado y rw → la op está permitida.
func TestLayoutGuard_MountedRW_Allowed(t *testing.T) {
	pool := &Pool{ID: "p", MountPoint: nimosPoolsDir + "/data8"}
	if err := assertLayoutOpAllowedWith(pool, layoutChecks(true, false)); err != nil {
		t.Fatalf("pool montado rw debería permitir la op; err=%v", err)
	}
}

// T11 central — pool NO montado → bloqueo con mount_missing (no "not a btrfs fs").
func TestLayoutGuard_NotMounted_BlocksMountMissing(t *testing.T) {
	pool := &Pool{ID: "p", MountPoint: nimosPoolsDir + "/data8"}
	err := assertLayoutOpAllowedWith(pool, layoutChecks(false, false))
	if err == nil {
		t.Fatal("pool NO montado debe bloquear la op de layout")
	}
	var pwe *PoolWritableError
	if !errors.As(err, &pwe) || pwe.Code != "mount_missing" {
		t.Errorf("se esperaba mount_missing; err=%v", err)
	}
}

// Pool read-only → bloqueo con read_only (btrfs detectó daño).
func TestLayoutGuard_ReadOnly_Blocks(t *testing.T) {
	pool := &Pool{ID: "p", MountPoint: nimosPoolsDir + "/data8"}
	err := assertLayoutOpAllowedWith(pool, layoutChecks(true, true))
	var pwe *PoolWritableError
	if !errors.As(err, &pwe) || pwe.Code != "read_only" {
		t.Errorf("pool ro debe bloquear con read_only; err=%v", err)
	}
}

// Mountpoint vacío → mount_missing (no se asume nada).
func TestLayoutGuard_EmptyMount_Blocks(t *testing.T) {
	pool := &Pool{ID: "p", MountPoint: ""}
	err := assertLayoutOpAllowedWith(pool, layoutChecks(true, false))
	var pwe *PoolWritableError
	if !errors.As(err, &pwe) || pwe.Code != "mount_missing" {
		t.Errorf("mountpoint vacío debe dar mount_missing; err=%v", err)
	}
}

// Pool nil → error, sin panic.
func TestLayoutGuard_NilPool_NoPanic(t *testing.T) {
	if err := assertLayoutOpAllowedWith(nil, layoutChecks(true, false)); err == nil {
		t.Error("pool nil debe devolver error")
	}
}
