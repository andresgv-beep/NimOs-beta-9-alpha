// docker_helpers_path_test.go — getDockerPath: Docker atado a SU pool.
//
// Antes, si el pool de Docker no montaba (disco lento al arrancar, pool
// degradado), getDockerPath saltaba a otro pool montado y reescribía
// docker.json: las apps arrancaban vacías y, al volver el pool original,
// NimOS seguía en el nuevo. Estos tests fijan que eso ya no pasa.

package main

import (
	"strings"
	"testing"
)

// dockerPathHarness inyecta config, montajes y guardado de docker.json.
type dockerPathHarness struct {
	conf    map[string]interface{}
	mounted map[string]bool
	saves   int
}

func newDockerPathHarness(t *testing.T, conf map[string]interface{}, mounted ...string) *dockerPathHarness {
	t.Helper()
	h := &dockerPathHarness{conf: conf, mounted: map[string]bool{}}
	for _, m := range mounted {
		h.mounted[m] = true
	}
	origGet, origSave, origMounted := getDockerConfigGo, saveDockerConfigGo, isPoolMounted
	getDockerConfigGo = func() map[string]interface{} { return h.conf }
	saveDockerConfigGo = func(c map[string]interface{}) { h.conf = c; h.saves++ }
	isPoolMounted = func(mp string) bool { return h.mounted[mp] }
	t.Cleanup(func() {
		getDockerConfigGo, saveDockerConfigGo, isPoolMounted = origGet, origSave, origMounted
	})
	return h
}

func withStorageService(t *testing.T) *StorageService {
	t.Helper()
	service, _, cleanup := setupTestService(t)
	orig := storageService
	storageService = service
	t.Cleanup(func() { storageService = orig; cleanup() })
	return service
}

func TestGetDockerPath_ConfiguredPoolMounted(t *testing.T) {
	svc := withStorageService(t)
	id := insertTestPool(t, svc, "data", ControlStateManaged)
	h := newDockerPathHarness(t, map[string]interface{}{"path": "/nimos/pools/data/docker"}, "/nimos/pools/data")

	got, err := getDockerPath()
	if err != nil || got != "/nimos/pools/data/docker" {
		t.Fatalf("got (%q, %v), want /nimos/pools/data/docker", got, err)
	}
	// docker.json antiguo sin poolId → se anota para poder seguir un rename.
	if h.conf["poolId"] != id {
		t.Errorf("poolId no anotado: %v", h.conf["poolId"])
	}
}

func TestGetDockerPath_PoolNotMounted_DoesNotJumpToOtherPool(t *testing.T) {
	svc := withStorageService(t)
	insertTestPool(t, svc, "data", ControlStateManaged)
	other := insertTestPool(t, svc, "backup", ControlStateManaged)
	setPrimaryPool(t, svc, other)

	for _, tc := range []struct {
		name string
		conf map[string]interface{}
	}{
		{"legacy sin poolId", map[string]interface{}{"path": "/nimos/pools/data/docker"}},
		{"con poolId", map[string]interface{}{"path": "/nimos/pools/data/docker", "poolId": "pool-id-data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Solo el OTRO pool está montado: el de Docker no.
			h := newDockerPathHarness(t, tc.conf, "/nimos/pools/backup")
			got, err := getDockerPath()
			if err == nil {
				t.Fatalf("debía fallar, devolvió %q", got)
			}
			if !strings.Contains(err.Error(), "/nimos/pools/data") {
				t.Errorf("el error debe nombrar el pool de Docker: %v", err)
			}
			if h.saves != 0 || h.conf["path"] != "/nimos/pools/data/docker" {
				t.Errorf("docker.json no debe tocarse: saves=%d path=%v", h.saves, h.conf["path"])
			}
		})
	}
}

func TestGetDockerPath_FollowsRenameByPoolID(t *testing.T) {
	svc := withStorageService(t)
	// El pool se llamaba data6 y ahora es data9: mismo ID, otro mount point.
	id := insertTestPool(t, svc, "data9", ControlStateManaged)
	h := newDockerPathHarness(t,
		map[string]interface{}{"path": "/nimos/pools/data6/docker", "poolId": id},
		"/nimos/pools/data9")

	got, err := getDockerPath()
	if err != nil || got != "/nimos/pools/data9/docker" {
		t.Fatalf("got (%q, %v), want /nimos/pools/data9/docker", got, err)
	}
	if h.conf["path"] != "/nimos/pools/data9/docker" {
		t.Errorf("docker.json no actualizado tras rename: %v", h.conf["path"])
	}
}

func TestGetDockerPath_FreshInstallPicksPrimaryAndRecordsID(t *testing.T) {
	svc := withStorageService(t)
	insertTestPool(t, svc, "data", ControlStateManaged)
	primary := insertTestPool(t, svc, "fast", ControlStateManaged)
	setPrimaryPool(t, svc, primary)
	h := newDockerPathHarness(t, map[string]interface{}{"installed": false, "path": nil},
		"/nimos/pools/data", "/nimos/pools/fast")

	got, err := getDockerPath()
	if err != nil || got != "/nimos/pools/fast/docker" {
		t.Fatalf("got (%q, %v), want /nimos/pools/fast/docker", got, err)
	}
	if h.conf["poolId"] != primary {
		t.Errorf("poolId: got %v, want %s", h.conf["poolId"], primary)
	}
}
