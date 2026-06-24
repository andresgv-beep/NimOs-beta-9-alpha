// storage_integration_test.go — Test de integración del módulo storage
// contra BTRFS REAL usando discos loopback.
//
// REQUIERE:
//   - Kernel con soporte BTRFS (cat /proc/filesystems | grep btrfs)
//   - btrfs-progs instalado (mkfs.btrfs, btrfs)
//   - losetup, mount, wipefs, blkid
//   - Privilegios de root (loopback, mount)
//
// EJECUTAR:
//   sudo go test -tags integration -run TestStorageIntegration -v
//
// Por defecto NO se compila ni se ejecuta — solo cuando se pasa el tag.
// Esto evita que la suite normal (go test -run TestStorage) intente tocar
// BTRFS, que requiere sudo.

//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Loopback helpers
// ─────────────────────────────────────────────────────────────────────────────

// loopbackDisk representa un disco virtual respaldado por un archivo.
type loopbackDisk struct {
	BackingFile string // /tmp/nimos-test-disk-N.img
	DevicePath  string // /dev/loop15
}

// createLoopbackDisks crea N discos loopback de sizeMB cada uno.
// Devuelve los discos creados y una función de cleanup que los destruye.
func createLoopbackDisks(t *testing.T, n int, sizeMB int) ([]*loopbackDisk, func()) {
	t.Helper()
	disks := make([]*loopbackDisk, 0, n)

	cleanup := func() {
		for _, d := range disks {
			if d.DevicePath != "" {
				_ = exec.Command("losetup", "-d", d.DevicePath).Run()
			}
			if d.BackingFile != "" {
				_ = os.Remove(d.BackingFile)
			}
		}
	}

	for i := 0; i < n; i++ {
		backing := fmt.Sprintf("/tmp/nimos-test-disk-%d-%d.img", os.Getpid(), i)

		// dd if=/dev/zero of=<backing> bs=1M count=<sizeMB>
		if err := exec.Command("dd",
			"if=/dev/zero", "of="+backing,
			fmt.Sprintf("bs=1M"),
			fmt.Sprintf("count=%d", sizeMB),
			"status=none",
		).Run(); err != nil {
			cleanup()
			t.Fatalf("dd failed for %s: %v", backing, err)
		}

		// losetup --find --show <backing>
		out, err := exec.Command("losetup", "--find", "--show", backing).Output()
		if err != nil {
			os.Remove(backing)
			cleanup()
			t.Fatalf("losetup failed: %v", err)
		}
		devicePath := strings.TrimSpace(string(out))

		disks = append(disks, &loopbackDisk{
			BackingFile: backing,
			DevicePath:  devicePath,
		})
	}

	return disks, cleanup
}

// requireBtrfs salta el test si el kernel no soporta BTRFS o si no
// tenemos los binarios necesarios.
func requireBtrfs(t *testing.T) {
	t.Helper()

	// 1. Kernel debe tener btrfs
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		t.Skip("cannot read /proc/filesystems")
	}
	if !strings.Contains(string(data), "btrfs") {
		// Intentar modprobe
		if err := exec.Command("modprobe", "btrfs").Run(); err != nil {
			t.Skip("BTRFS kernel module not available (modprobe btrfs failed)")
		}
		data, _ = os.ReadFile("/proc/filesystems")
		if !strings.Contains(string(data), "btrfs") {
			t.Skip("BTRFS not in /proc/filesystems even after modprobe")
		}
	}

	// 2. Binarios obligatorios
	for _, bin := range []string{"btrfs", "mkfs.btrfs", "losetup", "mount", "wipefs", "blkid"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("required binary not found: %s", bin)
		}
	}

	// 3. Privilegios de root
	if os.Geteuid() != 0 {
		t.Skip("integration tests require root (sudo go test -tags integration)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test principal: ciclo end-to-end con BTRFS real
// ─────────────────────────────────────────────────────────────────────────────

// TestStorageIntegrationCreateAndDestroyPool ejecuta el ciclo completo:
//   1. Setup: 2 discos loopback de 256MB
//   2. Crear pool raid1 sobre ellos
//   3. Verificar que el pool está montado y la DB está actualizada
//   4. Destruir el pool
//   5. Verificar que el filesystem está desmontado y los discos limpios
func TestStorageIntegrationCreateAndDestroyPool(t *testing.T) {
	requireBtrfs(t)

	// Setup loopback disks
	disks, cleanup := createLoopbackDisks(t, 2, 256)
	defer cleanup()
	t.Logf("Created loopback disks: %s, %s", disks[0].DevicePath, disks[1].DevicePath)

	// Setup DB + componentes
	conn, _, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	repo := NewStorageRepo(conn)
	policy := NewPolicyChecker()
	executor := NewRealBtrfsExecutor()
	executor.CmdTimeout = 60 * time.Second // tests más rápidos
	scanner := NewMockDeviceScanner(nil)   // no usamos scan automático aquí
	service := NewStorageService(conn, repo, policy, executor, scanner)
	service.deviceChecker = noopDeviceChecker // tests no requieren preflight real

	ctx := context.Background()

	// Registrar discos en DB manualmente (como haría scanAndRegisterDevices
	// en un sistema con udev funcionando)
	tx, _ := conn.BeginTx(ctx, nil)
	deviceIDs := []string{}
	for i, d := range disks {
		id := fmt.Sprintf("integ-dev-%d", i)
		_, err := repo.UpsertDevice(ctx, tx, &Device{
			ID:          id,
			Serial:      fmt.Sprintf("LOOPBACK-%d", i), // sintético
			ByIDPath:    d.DevicePath,                  // usar device path directo (no hay by-id real)
			CurrentPath: d.DevicePath,
			Model:       "loopback",
			SizeBytes:   256 * 1024 * 1024,
		})
		if err != nil {
			tx.Rollback()
			t.Fatalf("UpsertDevice: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}
	tx.Commit()

	// ─── CreatePool real ───────────────────────────────────────────────
	t.Log("CreatePool: creating raid1 pool on 2 loopback disks")
	mountPoint := "/tmp/nimos-integration-test-pool"
	_ = os.RemoveAll(mountPoint)
	defer os.RemoveAll(mountPoint)

	// El service hardcodea /nimos/pools/<name>, así que para el test
	// vamos a usar /nimos/pools/ realmente
	defer exec.Command("umount", "-l", "/nimos/pools/integration-test").Run()
	defer os.RemoveAll("/nimos/pools/integration-test")

	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name:      "integration-test",
		Profile:   ProfileRaid1,
		DeviceIDs: deviceIDs,
		WipeFirst: true,
	})
	if err != nil {
		t.Fatalf("CreatePool returned error: %v", err)
	}
	if op == nil {
		t.Fatal("CreatePool returned nil operation")
	}
	if op.Status != OpStatusCompleted {
		errMsg := ""
		if op.Error != nil {
			errMsg = *op.Error
		}
		t.Fatalf("CreatePool failed: status=%s error=%s", op.Status, errMsg)
	}
	t.Logf("Pool created: ID=%s", *op.PoolID)

	// ─── Verificar estado ──────────────────────────────────────────────

	// El pool existe en DB
	pool, err := service.GetPool(ctx, *op.PoolID)
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if pool == nil {
		t.Fatal("pool not in DB")
	}
	if pool.Profile != ProfileRaid1 {
		t.Errorf("profile: got %q", pool.Profile)
	}
	if len(pool.Devices) != 2 {
		t.Errorf("devices in pool: got %d, want 2", len(pool.Devices))
	}
	if pool.BtrfsUUID == "" {
		t.Error("BtrfsUUID should not be empty")
	}
	t.Logf("Pool state: name=%s uuid=%s devices=%d", pool.Name, pool.BtrfsUUID, len(pool.Devices))

	// El filesystem está montado
	mounts, _ := os.ReadFile("/proc/mounts")
	if !strings.Contains(string(mounts), pool.MountPoint) {
		t.Errorf("pool mountpoint %s not in /proc/mounts", pool.MountPoint)
	}

	// btrfs filesystem show debe ver el filesystem
	out, err := exec.Command("btrfs", "filesystem", "show", pool.MountPoint).CombinedOutput()
	if err != nil {
		t.Errorf("btrfs filesystem show failed: %v: %s", err, out)
	} else {
		t.Logf("BTRFS reports: %s", strings.TrimSpace(string(out)))
	}

	// ─── DestroyPool real ──────────────────────────────────────────────
	t.Log("DestroyPool: destroying the pool")
	op2, err := service.DestroyPool(ctx, *op.PoolID)
	if err != nil {
		t.Fatalf("DestroyPool: %v", err)
	}
	if op2.Status != OpStatusCompleted {
		errMsg := ""
		if op2.Error != nil {
			errMsg = *op2.Error
		}
		t.Fatalf("DestroyPool failed: status=%s error=%s", op2.Status, errMsg)
	}
	t.Log("Pool destroyed successfully")

	// El pool ya no existe en DB
	gone, _ := service.repo.GetPool(ctx, *op.PoolID)
	if gone != nil {
		t.Error("pool should be removed from DB")
	}

	// El filesystem ya no está montado
	mountsAfter, _ := os.ReadFile("/proc/mounts")
	if strings.Contains(string(mountsAfter), pool.MountPoint) {
		t.Errorf("pool mountpoint %s still in /proc/mounts after destroy", pool.MountPoint)
	}

	// Los devices vuelven a estar libres (siguen en DB)
	availableAfter, _ := service.ListAvailableDevices(ctx)
	if len(availableAfter) != 2 {
		t.Errorf("available devices after destroy: got %d, want 2", len(availableAfter))
	}

	// La operation original (create) sigue en histórico con pool_id NULL
	origOp, _ := service.repo.GetOperation(ctx, op.ID)
	if origOp == nil {
		t.Fatal("original CreatePool operation should be preserved")
	}
	if origOp.PoolID != nil {
		t.Errorf("orig op pool_id: got %v, want nil", *origOp.PoolID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: AddDevice y RemoveDevice con BTRFS real
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageIntegrationAddRemoveDevice(t *testing.T) {
	requireBtrfs(t)

	// 3 loopbacks: 2 para crear pool inicial, 1 para añadir
	disks, cleanup := createLoopbackDisks(t, 3, 256)
	defer cleanup()

	conn, _, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	repo := NewStorageRepo(conn)
	policy := NewPolicyChecker()
	executor := NewRealBtrfsExecutor()
	executor.CmdTimeout = 60 * time.Second
	scanner := NewMockDeviceScanner(nil)
	service := NewStorageService(conn, repo, policy, executor, scanner)
	service.deviceChecker = noopDeviceChecker // tests no requieren preflight real
	ctx := context.Background()

	// Registrar los 3 discos
	tx, _ := conn.BeginTx(ctx, nil)
	devIDs := []string{}
	for i, d := range disks {
		id := fmt.Sprintf("addrm-dev-%d", i)
		_, err := repo.UpsertDevice(ctx, tx, &Device{
			ID: id, Serial: fmt.Sprintf("ADDRM-%d", i),
			ByIDPath: d.DevicePath, CurrentPath: d.DevicePath,
			SizeBytes: 256 * 1024 * 1024,
		})
		if err != nil {
			tx.Rollback()
			t.Fatalf("UpsertDevice: %v", err)
		}
		devIDs = append(devIDs, id)
	}
	tx.Commit()

	defer exec.Command("umount", "-l", "/nimos/pools/addrm-test").Run()
	defer os.RemoveAll("/nimos/pools/addrm-test")

	// Crear pool con 2 discos
	t.Log("Creating raid1 pool with 2 disks")
	op, err := service.CreatePool(ctx, CreatePoolRequest{
		Name:      "addrm-test",
		Profile:   ProfileRaid1,
		DeviceIDs: devIDs[:2],
		WipeFirst: true,
	})
	if err != nil || op.Status != OpStatusCompleted {
		t.Fatalf("CreatePool: %v / status=%s", err, op.Status)
	}
	poolID := *op.PoolID
	defer service.DestroyPool(ctx, poolID)

	// Añadir el tercer disco
	t.Log("Adding 3rd disk to pool")
	op2, err := service.AddDevice(ctx, AddDeviceRequest{
		PoolID:   poolID,
		DeviceID: devIDs[2],
	})
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if op2.Status != OpStatusCompleted {
		errMsg := ""
		if op2.Error != nil {
			errMsg = *op2.Error
		}
		t.Fatalf("AddDevice failed: %s", errMsg)
	}

	// Pool ahora tiene 3
	pool, _ := service.GetPool(ctx, poolID)
	if len(pool.Devices) != 3 {
		t.Errorf("after AddDevice: got %d devices, want 3", len(pool.Devices))
	}
	t.Logf("Pool has %d devices after AddDevice", len(pool.Devices))

	// Verificar con btrfs filesystem show
	out, _ := exec.Command("btrfs", "filesystem", "show", pool.MountPoint).CombinedOutput()
	t.Logf("BTRFS view: %s", strings.TrimSpace(string(out)))

	// Quitar uno (de los 3, podemos bajar a 2 que es el mínimo de raid1)
	t.Log("Removing 1st disk from pool")
	op3, err := service.RemoveDevice(ctx, RemoveDeviceRequest{
		PoolID:   poolID,
		DeviceID: devIDs[0],
	})
	if err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}
	if op3.Status != OpStatusCompleted {
		errMsg := ""
		if op3.Error != nil {
			errMsg = *op3.Error
		}
		t.Fatalf("RemoveDevice failed: %s", errMsg)
	}

	pool2, _ := service.GetPool(ctx, poolID)
	if len(pool2.Devices) != 2 {
		t.Errorf("after RemoveDevice: got %d devices, want 2", len(pool2.Devices))
	}

	// El disco quitado está libre
	available, _ := service.ListAvailableDevices(ctx)
	found := false
	for _, d := range available {
		if d.ID == devIDs[0] {
			found = true
		}
	}
	if !found {
		t.Errorf("removed device %s should be in available list", devIDs[0])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Mostrar guard de WipeDevice
// ─────────────────────────────────────────────────────────────────────────────

// TestStorageIntegrationWipeDeviceRejectsBootDisk verifica que el guard
// de WipeDevice rechaza wipear el disco de boot del sistema.
//
// El test verifica DOS cosas:
//   1. Se rechaza (err != nil)
//   2. Se rechaza por la razón CORRECTA ("refusing to wipe boot disk"),
//      no por un error secundario como "cannot resolve symlink".
//      Esto detecta regresiones en parentDeviceOf / isBootDisk.
func TestStorageIntegrationWipeDeviceRejectsBootDisk(t *testing.T) {
	requireBtrfs(t)

	executor := NewRealBtrfsExecutor()
	ctx := context.Background()

	// Encontrar el boot disk
	mountsData, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Fatalf("read /proc/mounts: %v", err)
	}
	var rootDev string
	for _, line := range strings.Split(string(mountsData), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "/" {
			rootDev = fields[0]
			break
		}
	}
	if rootDev == "" {
		t.Skip("cannot determine root device")
	}

	// Resolver a su disco padre usando la MISMA función que usa el executor.
	// Esto verifica que parentDeviceOf maneja NVMe (nvme0n1p2 → nvme0n1)
	// además de sd*, vd*, hd*.
	realRoot, _ := filepath.EvalSymlinks(rootDev)
	parent := parentDeviceOf(realRoot)
	t.Logf("Boot partition: %s → parent disk: %s", realRoot, parent)

	// Sanity check: el parent debe existir como path real
	if _, statErr := os.Stat(parent); statErr != nil {
		t.Fatalf("parentDeviceOf produced invalid path %q: %v", parent, statErr)
	}

	// Intentar wipear el disco padre del boot
	err = executor.WipeDevice(ctx, parent)
	if err == nil {
		t.Fatalf("WipeDevice should REFUSE to wipe boot disk %s", parent)
	}

	// CRÍTICO: debe rechazarse por "refusing to wipe boot disk", no por
	// otra razón (símbolo no resoluble, etc.). El mensaje correcto
	// confirma que el guard de boot ejecutó correctamente.
	if !strings.Contains(err.Error(), "refusing to wipe boot disk") {
		t.Errorf("expected error to mention 'refusing to wipe boot disk', got: %v", err)
	}
	t.Logf("Correctly refused for the right reason: %v", err)
}
