// storage_share_adopt_test.go — Detección de shares huérfanas.
//
// Reproduce el incidente: subvolúmenes en disco bajo shares/ sin fila en la BD.
// Verifica que el scanner los detecta, ignora los ya registrados, los que no son
// subvolúmenes, y los ocultos.

package main

import "testing"

func known(pairs map[string][]string) map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for pool, names := range pairs {
		m[pool] = map[string]bool{}
		for _, n := range names {
			m[pool][n] = true
		}
	}
	return m
}

// Caso central: 3 carpetas en disco, 0 en BD → las 3 son huérfanas (data8).
func TestFindOrphans_AllUnregistered(t *testing.T) {
	pools := []*Pool{{Name: "data8", MountPoint: "/nimos/pools/data8"}}
	list := func(string) ([]string, error) {
		return []string{"data11", "multimedia", "musica"}, nil
	}
	isSub := func(string) bool { return true }

	got := findOrphanSharesIn(pools, known(nil), list, isSub)
	if len(got) != 3 {
		t.Fatalf("esperaba 3 huérfanas, got %d: %+v", len(got), got)
	}
	if got[0].Pool != "data8" || got[0].Path != "/nimos/pools/data8/shares/data11" {
		t.Errorf("campos mal poblados: %+v", got[0])
	}
}

// Las ya registradas en BD se ignoran.
func TestFindOrphans_SkipsRegistered(t *testing.T) {
	pools := []*Pool{{Name: "data8", MountPoint: "/nimos/pools/data8"}}
	list := func(string) ([]string, error) {
		return []string{"data11", "multimedia", "musica"}, nil
	}
	isSub := func(string) bool { return true }

	got := findOrphanSharesIn(pools, known(map[string][]string{"data8": {"multimedia", "musica"}}), list, isSub)
	if len(got) != 1 || got[0].Name != "data11" {
		t.Fatalf("solo data11 debía ser huérfana; got %+v", got)
	}
}

// Un dir que NO es subvolumen se ignora (carpeta suelta, no una share real).
func TestFindOrphans_SkipsNonSubvolume(t *testing.T) {
	pools := []*Pool{{Name: "data8", MountPoint: "/nimos/pools/data8"}}
	list := func(string) ([]string, error) { return []string{"realshare", "carpeta_suelta"}, nil }
	isSub := func(path string) bool { return path == "/nimos/pools/data8/shares/realshare" }

	got := findOrphanSharesIn(pools, known(nil), list, isSub)
	if len(got) != 1 || got[0].Name != "realshare" {
		t.Fatalf("solo el subvolumen debía contar; got %+v", got)
	}
}

// Los ocultos (.papelera, etc.) se ignoran.
func TestFindOrphans_SkipsHidden(t *testing.T) {
	pools := []*Pool{{Name: "data8", MountPoint: "/nimos/pools/data8"}}
	list := func(string) ([]string, error) { return []string{".papelera", "musica"}, nil }
	isSub := func(string) bool { return true }

	got := findOrphanSharesIn(pools, known(nil), list, isSub)
	if len(got) != 1 || got[0].Name != "musica" {
		t.Fatalf("los ocultos deben ignorarse; got %+v", got)
	}
}

// Multi-pool: detecta huérfanos en cada pool, salta pools sin mountpoint.
func TestFindOrphans_MultiPool(t *testing.T) {
	pools := []*Pool{
		{Name: "data8", MountPoint: "/nimos/pools/data8"},
		{Name: "data9", MountPoint: ""}, // no montado → se salta
	}
	list := func(dir string) ([]string, error) {
		if dir == "/nimos/pools/data8/shares" {
			return []string{"a"}, nil
		}
		return nil, nil
	}
	isSub := func(string) bool { return true }

	got := findOrphanSharesIn(pools, known(nil), list, isSub)
	if len(got) != 1 || got[0].Pool != "data8" {
		t.Fatalf("esperaba 1 huérfano en data8; got %+v", got)
	}
}

// Pool cuya carpeta shares/ no existe (lister error) → sin huérfanos, sin panic.
func TestFindOrphans_SharesDirMissing(t *testing.T) {
	pools := []*Pool{{Name: "data8", MountPoint: "/nimos/pools/data8"}}
	list := func(string) ([]string, error) { return nil, errMissingDir }
	isSub := func(string) bool { return true }

	got := findOrphanSharesIn(pools, known(nil), list, isSub)
	if len(got) != 0 {
		t.Fatalf("sin carpeta shares/ no debe haber huérfanos; got %+v", got)
	}
}

var errMissingDir = &mockErr{"no such dir"}

type mockErr struct{ s string }

func (e *mockErr) Error() string { return e.s }
