package main

// storage_btrfs_import_test.go — Tests para importPoolBtrfs (Bloque C3.1).
//
// Cubre:
//   · Validación de inputs (uuid, name, formato)
//   · Error cuando observer no está disponible
//   · Error cuando UUID no aparece en observed state
//   · Error cuando FS ya está managed
//   · Error cuando FS tiene devices missing
//   · Error cuando nombre ya está en uso
//   · isValidPoolName

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setupImportTest crea un observer mockeado con FSes conocidos.
// Devuelve fn de cleanup.
func setupImportTest(t *testing.T, filesystems []ObservedBtrfs) func() {
	t.Helper()
	savedObs := globalObserver

	o := NewStorageObserver(1 * time.Hour)
	o.snapshot.Store(&ObservedSnapshot{
		Generation:  1,
		Timestamp:   time.Now().UTC(),
		Filesystems: filesystems,
	})
	o.generation.Store(1)
	globalObserver = o

	return func() {
		globalObserver = savedObs
	}
}

// ─── Validación de inputs ──────────────────────────────────────────────────

func TestImportPoolBtrfs_RequiresUUID(t *testing.T) {
	cleanup := setupImportTest(t, nil)
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"name": "test",
	})
	if _, ok := result["error"]; !ok {
		t.Error("expected error for missing uuid")
	}
}

func TestImportPoolBtrfs_RequiresName(t *testing.T) {
	cleanup := setupImportTest(t, nil)
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "some-uuid",
	})
	if _, ok := result["error"]; !ok {
		t.Error("expected error for missing name")
	}
}

func TestImportPoolBtrfs_RejectsInvalidName(t *testing.T) {
	cleanup := setupImportTest(t, nil)
	defer cleanup()

	cases := []string{
		"",                                     // vacío
		"pool with spaces",                     // espacios
		"pool/with/slashes",                    // slashes
		"system",                               // reservado
		"thisistoolongtobeapoolnamebecausefxs", // >32
	}
	for _, name := range cases {
		result := importPoolBtrfs(map[string]interface{}{
			"uuid": "some-uuid",
			"name": name,
		})
		if _, ok := result["error"]; !ok {
			t.Errorf("expected error for name %q", name)
		}
	}
}

// ─── Observer disponibilidad ───────────────────────────────────────────────

func TestImportPoolBtrfs_RequiresObserver(t *testing.T) {
	savedObs := globalObserver
	defer func() { globalObserver = savedObs }()
	globalObserver = nil

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "some-uuid",
		"name": "test",
	})
	if _, ok := result["error"]; !ok {
		t.Error("expected error when observer not running")
	}
}

// ─── FS no observado ───────────────────────────────────────────────────────

func TestImportPoolBtrfs_FSNotObserved(t *testing.T) {
	cleanup := setupImportTest(t, []ObservedBtrfs{
		{UUID: "uuid-A", Label: "A"},
	})
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "uuid-NOT-EXISTING",
		"name": "test",
	})
	if result["code"] != "FS_NOT_OBSERVED" {
		t.Errorf("expected FS_NOT_OBSERVED code, got %v", result["code"])
	}
}

// ─── FS ya managed ─────────────────────────────────────────────────────────

func TestImportPoolBtrfs_AlreadyManaged(t *testing.T) {
	cleanup := setupImportTest(t, []ObservedBtrfs{
		{
			UUID:            "managed-uuid",
			IsManaged:       true,
			ManagedPoolName: "existing-pool",
		},
	})
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "managed-uuid",
		"name": "test",
	})
	if result["code"] != "ALREADY_MANAGED" {
		t.Errorf("expected ALREADY_MANAGED code, got %v", result["code"])
	}
	if result["pool_name"] != "existing-pool" {
		t.Errorf("expected pool_name=existing-pool, got %v", result["pool_name"])
	}
}

// ─── FS incompleto ─────────────────────────────────────────────────────────

func TestImportPoolBtrfs_FSIncomplete(t *testing.T) {
	// Un filesystem DEGRADADO (1/2 discos) ahora SÍ se importa (en modo
	// degradado,ro) para permitir recuperación y reparación. Antes se
	// rechazaba con FS_INCOMPLETE, creando un bucle sin salida. El test del
	// montaje real degradado se hace en hardware; aquí validamos que NO se
	// rechaza con el código viejo.
	cleanup := setupImportTest(t, []ObservedBtrfs{
		{
			UUID:            "incomplete-uuid",
			DevicesExpected: 2,
			DevicesOnline:   1,
			DevicesMissing:  1,
			IsManaged:       false,
		},
	})
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "incomplete-uuid",
		"name": "test",
	})
	// Ya NO debe rechazarse con FS_INCOMPLETE (eso era el bucle).
	if result["code"] == "FS_INCOMPLETE" {
		t.Errorf("un FS degradado (1/2) ya NO debe rechazarse con FS_INCOMPLETE — debe importarse degradado")
	}
}

func TestImportPoolBtrfs_NoDevicesOnline(t *testing.T) {
	// Con 0 discos online NO hay nada que montar → sí se rechaza.
	cleanup := setupImportTest(t, []ObservedBtrfs{
		{
			UUID:            "dead-uuid",
			DevicesExpected: 2,
			DevicesOnline:   0,
			DevicesMissing:  2,
			IsManaged:       false,
		},
	})
	defer cleanup()

	result := importPoolBtrfs(map[string]interface{}{
		"uuid": "dead-uuid",
		"name": "test",
	})
	if result["code"] != "FS_NO_DEVICES" {
		t.Errorf("0 discos online debe rechazarse con FS_NO_DEVICES, got %v", result["code"])
	}
}

// ─── isValidPoolName ───────────────────────────────────────────────────────

func TestIsValidPoolName(t *testing.T) {
	valid := []string{"data", "datos1", "my-pool", "POOL-2026", "a"}
	for _, n := range valid {
		if !isValidPoolName(n) {
			t.Errorf("%q should be valid", n)
		}
	}

	invalid := []string{
		"",
		"pool with spaces",
		"pool/slash",
		"pool.dot",
		"system",
		"BOOT",
		"thisnameistoolongtobeavalidpoolname-1234567890",
	}
	for _, n := range invalid {
		if isValidPoolName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

// ─── Concurrencia: observer + import ───────────────────────────────────────

// El test usa atomic.Int64 implícitamente vía mockProbe.calls — confirma que
// dos lecturas de snapshot durante import no producen race.
func TestImportPoolBtrfs_ConcurrentSnapshotReads(t *testing.T) {
	cleanup := setupImportTest(t, []ObservedBtrfs{
		{
			UUID:            "test-uuid-concurrent",
			IsManaged:       false,
			DevicesExpected: 1,
			DevicesOnline:   1,
		},
	})
	defer cleanup()

	var reads atomic.Int64
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Muchos lectores paralelos
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					obs := globalObserver
					if obs == nil {
						continue
					}
					snap := obs.Snapshot()
					if snap != nil {
						reads.Add(1)
					}
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// importPoolBtrfs llamará al snapshot también — verifica que no hay race
	_ = importPoolBtrfs(map[string]interface{}{
		"uuid": "test-uuid-concurrent",
		"name": "test-concurrent",
	})
	// El test puede fallar por otras razones (sin storageService o sin lock real
	// en producción), pero lo importante es que no haya race.

	close(done)
	wg.Wait() // esperar a que las 20 goroutines salgan antes del defer cleanup()

	if reads.Load() == 0 {
		t.Error("no concurrent reads happened")
	}
}
