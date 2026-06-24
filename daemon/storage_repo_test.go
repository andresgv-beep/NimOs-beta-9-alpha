// storage_repo_test.go — Tests del StorageRepo contra una DB SQLite real.
//
// Estos tests usan un archivo SQLite en /tmp, aplican el schema completo,
// y verifican las queries y mutaciones del repo de Pools y Devices.
//
// Ejecutar:
//   cd daemon/
//   go test -run TestStorageRepo -v
//
// No requieren BTRFS ni hardware especial — solo archivo en disco.

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB crea una DB SQLite en /tmp, aplica el schema, devuelve la
// conexión y la función de cleanup.
func setupTestDB(t *testing.T) (*sql.DB, *StorageRepo, func()) {
	t.Helper()

	// Sanitizar el nombre: los subtests t.Run incluyen "/" que rompe el path.
	// Sustituimos cualquier carácter problemático por "_".
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_repo_test_" + safeName + ".db"
	os.Remove(tmpDB)

	conn, err := sql.Open("sqlite", tmpDB+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	// PRAGMA explícito (modernc.org/sqlite no honra el query string)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}

	// Aplicar el schema embebido (mismo que usa el daemon)
	if _, err := conn.Exec(storageSchemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	repo := NewStorageRepo(conn)

	cleanup := func() {
		conn.Close()
		os.Remove(tmpDB)
	}
	return conn, repo, cleanup
}

// helper: ejecuta fn dentro de una transacción.
func withTx(t *testing.T, conn *sql.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		t.Fatalf("tx fn: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// POOLS
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRepoPoolCRUD(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// 1. HasAnyPool: false al inicio
	has, err := repo.HasAnyPool(ctx)
	if err != nil {
		t.Fatalf("HasAnyPool: %v", err)
	}
	if has {
		t.Fatal("HasAnyPool should be false on empty DB")
	}

	// 2. CreatePool
	pool := &Pool{
		ID:         "pool-test-1",
		Name:       "multimedia",
		BtrfsUUID:  "btrfs-uuid-aaa",
		Profile:    ProfileRaid1,
		MountPoint: "/nimos/pools/multimedia",
	}
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, pool)
	})

	// 3. GetPool
	got, err := repo.GetPool(ctx, "pool-test-1")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if got == nil {
		t.Fatal("GetPool returned nil")
	}
	if got.Name != "multimedia" {
		t.Errorf("Name: got %q, want %q", got.Name, "multimedia")
	}
	if got.Profile != ProfileRaid1 {
		t.Errorf("Profile: got %q, want %q", got.Profile, ProfileRaid1)
	}
	if got.Role != RoleData {
		t.Errorf("Role default: got %q, want %q", got.Role, RoleData)
	}
	if got.ControlState != ControlStateManaged {
		t.Errorf("ControlState default: got %q, want %q", got.ControlState, ControlStateManaged)
	}
	if got.Compression != "none" {
		t.Errorf("Compression default: got %q, want %q", got.Compression, "none")
	}
	if got.Generation != 0 {
		t.Errorf("Generation initial: got %d, want 0", got.Generation)
	}

	// 4. HasAnyPool: true ahora
	has, _ = repo.HasAnyPool(ctx)
	if !has {
		t.Fatal("HasAnyPool should be true after CreatePool")
	}

	// 5. GetPoolByName
	got2, err := repo.GetPoolByName(ctx, "multimedia")
	if err != nil || got2 == nil || got2.ID != pool.ID {
		t.Fatal("GetPoolByName mismatch")
	}

	// 6. RenamePool
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.RenamePool(ctx, tx, pool.ID, "MisMultimedia")
	})
	got, _ = repo.GetPool(ctx, pool.ID)
	if got.Name != "MisMultimedia" {
		t.Errorf("Rename failed: got %q", got.Name)
	}
	if got.Generation != 1 {
		t.Errorf("Generation after rename: got %d, want 1", got.Generation)
	}

	// 7. SetPoolControlState
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.SetPoolControlState(ctx, tx, pool.ID, ControlStateObserved)
	})
	got, _ = repo.GetPool(ctx, pool.ID)
	if got.ControlState != ControlStateObserved {
		t.Errorf("ControlState: got %q", got.ControlState)
	}

	// 8. SetPoolCompression
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.SetPoolCompression(ctx, tx, pool.ID, "zstd:3")
	})
	got, _ = repo.GetPool(ctx, pool.ID)
	if got.Compression != "zstd:3" {
		t.Errorf("Compression: got %q", got.Compression)
	}

	// 9. Generation se ha incrementado en cada mutación
	if got.Generation != 3 {
		t.Errorf("Generation after 3 mutations: got %d, want 3", got.Generation)
	}

	// 10. DeletePool
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.DeletePool(ctx, tx, pool.ID)
	})
	got, _ = repo.GetPool(ctx, pool.ID)
	if got != nil {
		t.Fatal("Pool not deleted")
	}
}

func TestStorageRepoPoolUniqueConstraints(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	p1 := &Pool{
		ID: "p1", Name: "data", BtrfsUUID: "uuid-1",
		Profile: ProfileSingle, MountPoint: "/nimos/pools/data",
	}
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, p1)
	})

	// Name duplicado debe fallar
	p2 := &Pool{
		ID: "p2", Name: "data", BtrfsUUID: "uuid-2",
		Profile: ProfileSingle, MountPoint: "/nimos/pools/data2",
	}
	tx, _ := conn.BeginTx(ctx, nil)
	err := repo.CreatePool(ctx, tx, p2)
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected error on duplicate name")
	}

	// btrfs_uuid duplicado debe fallar
	p3 := &Pool{
		ID: "p3", Name: "other", BtrfsUUID: "uuid-1",
		Profile: ProfileSingle, MountPoint: "/nimos/pools/other",
	}
	tx, _ = conn.BeginTx(ctx, nil)
	err = repo.CreatePool(ctx, tx, p3)
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected error on duplicate btrfs_uuid")
	}
}

func TestStorageRepoListPoolsByControlState(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	pools := []*Pool{
		{ID: "p1", Name: "a", BtrfsUUID: "u1", Profile: ProfileSingle,
			MountPoint: "/nimos/pools/a", ControlState: ControlStateManaged},
		{ID: "p2", Name: "b", BtrfsUUID: "u2", Profile: ProfileRaid1,
			MountPoint: "/nimos/pools/b", ControlState: ControlStateObserved},
		{ID: "p3", Name: "c", BtrfsUUID: "u3", Profile: ProfileRaid1,
			MountPoint: "/nimos/pools/c", ControlState: ControlStateManaged},
	}
	for _, p := range pools {
		withTx(t, conn, func(tx *sql.Tx) error {
			return repo.CreatePool(ctx, tx, p)
		})
	}

	managed, err := repo.ListPoolsByControlState(ctx, ControlStateManaged)
	if err != nil {
		t.Fatalf("ListPoolsByControlState: %v", err)
	}
	if len(managed) != 2 {
		t.Errorf("managed pools: got %d, want 2", len(managed))
	}

	observed, _ := repo.ListPoolsByControlState(ctx, ControlStateObserved)
	if len(observed) != 1 {
		t.Errorf("observed pools: got %d, want 1", len(observed))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DEVICES
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageRepoDeviceUpsert(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	dev := &Device{
		ID:          "dev-1",
		Serial:      "WD-WCC4N1234567",
		ByIDPath:    "/dev/disk/by-id/ata-WDC_WD40EFRX",
		CurrentPath: "/dev/sdb",
		Model:       "WDC WD40EFRX-68N32N0",
		SizeBytes:   4000000000000,
	}

	// Insert nuevo
	withTx(t, conn, func(tx *sql.Tx) error {
		_, err := repo.UpsertDevice(ctx, tx, dev); return err
	})

	got, err := repo.GetDeviceBySerial(ctx, "WD-WCC4N1234567")
	if err != nil {
		t.Fatalf("GetDeviceBySerial: %v", err)
	}
	if got == nil {
		t.Fatal("device not found after upsert")
	}
	if got.CurrentPath != "/dev/sdb" {
		t.Errorf("CurrentPath: got %q", got.CurrentPath)
	}

	// Upsert con mismo serial pero current_path distinto (simula reboot)
	dev2 := &Device{
		ID:          "dev-1", // mismo ID lógico, pero UpsertDevice usa serial
		Serial:      "WD-WCC4N1234567",
		ByIDPath:    "/dev/disk/by-id/ata-WDC_WD40EFRX", // by_id_path estable
		CurrentPath: "/dev/sdc",                          // cambió la letra
		Model:       "WDC WD40EFRX-68N32N0",
		SizeBytes:   4000000000000,
	}
	withTx(t, conn, func(tx *sql.Tx) error {
		_, err := repo.UpsertDevice(ctx, tx, dev2); return err
	})

	got, _ = repo.GetDeviceBySerial(ctx, "WD-WCC4N1234567")
	if got.CurrentPath != "/dev/sdc" {
		t.Errorf("CurrentPath after upsert: got %q, want /dev/sdc", got.CurrentPath)
	}
	if got.Generation != 1 {
		t.Errorf("Generation after upsert: got %d, want 1", got.Generation)
	}

	// Serial vacío debe fallar
	devBad := &Device{
		ID: "dev-bad", Serial: "", ByIDPath: "/dev/disk/by-id/x",
		CurrentPath: "/dev/sdx",
	}
	tx, _ := conn.BeginTx(ctx, nil)
	_, err = repo.UpsertDevice(ctx, tx, devBad)
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected error for empty serial")
	}
}

func TestStorageRepoListAvailableDevices(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool
	pool := &Pool{
		ID: "pool-1", Name: "data", BtrfsUUID: "uuid-1",
		Profile: ProfileRaid1, MountPoint: "/nimos/pools/data",
	}
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, pool)
	})

	// Crear 3 devices
	devs := []*Device{
		{ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1", CurrentPath: "/dev/sda", SizeBytes: 1e12},
		{ID: "d2", Serial: "S2", ByIDPath: "/dev/disk/by-id/s2", CurrentPath: "/dev/sdb", SizeBytes: 1e12},
		{ID: "d3", Serial: "S3", ByIDPath: "/dev/disk/by-id/s3", CurrentPath: "/dev/sdc", SizeBytes: 1e12},
	}
	for _, d := range devs {
		withTx(t, conn, func(tx *sql.Tx) error {
			_, err := repo.UpsertDevice(ctx, tx, d); return err
		})
	}

	// Asignar d1 y d2 al pool
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.AssignDeviceToPool(ctx, tx, "pool-1", "d1"); err != nil {
			return err
		}
		return repo.AssignDeviceToPool(ctx, tx, "pool-1", "d2")
	})

	// Solo d3 debería estar disponible
	available, err := repo.ListAvailableDevices(ctx)
	if err != nil {
		t.Fatalf("ListAvailableDevices: %v", err)
	}
	if len(available) != 1 {
		t.Errorf("available devices: got %d, want 1", len(available))
	}
	if len(available) > 0 && available[0].ID != "d3" {
		t.Errorf("available[0]: got %q, want d3", available[0].ID)
	}

	// ListDevicesInPool devuelve d1 y d2
	inPool, err := repo.ListDevicesInPool(ctx, "pool-1")
	if err != nil {
		t.Fatalf("ListDevicesInPool: %v", err)
	}
	if len(inPool) != 2 {
		t.Errorf("devices in pool: got %d, want 2", len(inPool))
	}
}

func TestStorageRepoFKCascadeOnPoolDelete(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Crear pool + device + asignación
	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: "p1", Name: "test", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		if _, err := repo.UpsertDevice(ctx, tx, &Device{
			ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1",
			CurrentPath: "/dev/sda", SizeBytes: 1e12,
		}); err != nil {
			return err
		}
		return repo.AssignDeviceToPool(ctx, tx, "p1", "d1")
	})

	// Borrar pool: el CASCADE debe limpiar pool_devices
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.DeletePool(ctx, tx, "p1")
	})

	// El device sigue existiendo (no se borra en CASCADE)
	d, _ := repo.GetDevice(ctx, "d1")
	if d == nil {
		t.Fatal("Device should still exist after pool delete")
	}

	// Y ahora debería estar available (sin pool)
	available, _ := repo.ListAvailableDevices(ctx)
	if len(available) != 1 {
		t.Errorf("device should be available after pool delete: got %d available", len(available))
	}
}

func TestStorageRepoFKRestrictOnDeviceDelete(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	withTx(t, conn, func(tx *sql.Tx) error {
		if err := repo.CreatePool(ctx, tx, &Pool{
			ID: "p1", Name: "test", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m",
		}); err != nil {
			return err
		}
		if _, err := repo.UpsertDevice(ctx, tx, &Device{
			ID: "d1", Serial: "S1", ByIDPath: "/dev/disk/by-id/s1",
			CurrentPath: "/dev/sda", SizeBytes: 1e12,
		}); err != nil {
			return err
		}
		return repo.AssignDeviceToPool(ctx, tx, "p1", "d1")
	})

	// Intentar borrar device en uso debe fallar (RESTRICT)
	tx, _ := conn.BeginTx(ctx, nil)
	_, err := tx.ExecContext(ctx, "DELETE FROM storage_devices WHERE id = ?", "d1")
	tx.Rollback()
	if err == nil {
		t.Fatal("Expected FK RESTRICT error when deleting device in use")
	}
}

func TestStorageRepoGlobalGeneration(t *testing.T) {
	conn, repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	gen0, _ := repo.GetGlobalGeneration(ctx)
	if gen0 != 0 {
		t.Errorf("initial generation: got %d, want 0", gen0)
	}

	// Cada mutación debe incrementar global_generation
	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.CreatePool(ctx, tx, &Pool{
			ID: "p1", Name: "a", BtrfsUUID: "u1",
			Profile: ProfileSingle, MountPoint: "/m1",
		})
	})
	gen1, _ := repo.GetGlobalGeneration(ctx)
	if gen1 != 1 {
		t.Errorf("after CreatePool: got %d, want 1", gen1)
	}

	withTx(t, conn, func(tx *sql.Tx) error {
		return repo.RenamePool(ctx, tx, "p1", "b")
	})
	gen2, _ := repo.GetGlobalGeneration(ctx)
	if gen2 != 2 {
		t.Errorf("after RenamePool: got %d, want 2", gen2)
	}
}

// Suprime warning de unused import
var _ = time.Now
