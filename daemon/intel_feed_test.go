package main

import (
	"os"
	"testing"
)

// Helper: monta DB de test + tablas intel, devuelve cleanup.
func setupIntelTest(t *testing.T) func() {
	t.Helper()
	prevDB := db
	c, dbCleanup := setupNetworkDB(t)
	db = c.db
	dbIntelInit()
	// resetea el estado activo entre tests
	intelActive = &IntelState{trie: newIntelTrie(), source: "none"}
	return func() {
		db = prevDB
		dbCleanup()
	}
}

// Carga el feed real descargado (si está en /tmp/intelcheck), si no, skip.
// Reutiliza los bytes ya firmados de producción para tests fieles.
func loadRealFeed(t *testing.T) (manifest, sig []byte, files map[string][]byte) {
	t.Helper()
	dir := "/tmp/intelcheck/"
	m, err := os.ReadFile(dir + "manifest.json")
	if err != nil {
		t.Skip("feed real no disponible en /tmp/intelcheck, saltando")
	}
	s, _ := os.ReadFile(dir + "manifest.json.sig")
	v4, _ := os.ReadFile(dir + "blocklist_ipv4.txt")
	v6, _ := os.ReadFile(dir + "blocklist_ipv6.txt")
	return m, s, map[string][]byte{"blocklist_ipv4.txt": v4, "blocklist_ipv6.txt": v6}
}

// applyFeed con el feed real → debe activar el trie y marcar observe.
func TestIntelB_ApplyRealFeed(t *testing.T) {
	defer setupIntelTest(t)()
	man, sig, files := loadRealFeed(t)

	loader := func(name string) ([]byte, error) { return files[name], nil }
	v, err := applyFeed(man, sig, loader, "network", true)
	if err != nil {
		t.Fatalf("applyFeed: %v", err)
	}
	if v != 1 {
		t.Errorf("feed_version=%d, want 1", v)
	}
	if intelActive.trie.size() == 0 {
		t.Error("trie vacío tras aplicar")
	}
	if !intelActive.observeOnly {
		t.Error("el feed real es observe → observeOnly debería ser true")
	}
}

// Cache round-trip: guardar y recuperar una versión.
func TestIntelB_CacheRoundTrip(t *testing.T) {
	defer setupIntelTest(t)()
	files := map[string][]byte{
		"manifest.json":      []byte(`{"feed_version":5}`),
		"blocklist_ipv4.txt": []byte("1.2.3.4\n"),
	}
	if err := dbIntelStore(5, files); err != nil {
		t.Fatal(err)
	}
	dbIntelSetCurrentVersion(5)
	if dbIntelCurrentVersion() != 5 {
		t.Error("current_version no persistió")
	}
	got, err := dbIntelLoadVersion(5)
	if err != nil {
		t.Fatal(err)
	}
	if string(got["blocklist_ipv4.txt"]) != "1.2.3.4\n" {
		t.Error("contenido cacheado no coincide")
	}
}

// Anti-replay: un feed_version menor que el vigente debe rechazarse.
func TestIntelB_AntiReplay(t *testing.T) {
	defer setupIntelTest(t)()
	man, sig, files := loadRealFeed(t)
	loader := func(name string) ([]byte, error) { return files[name], nil }

	// fijamos la versión vigente a 99 (más nueva que el feed real v1)
	dbIntelSetCurrentVersion(99)

	_, err := applyFeed(man, sig, loader, "network", true)
	if err == nil {
		t.Fatal("debería rechazar un feed v1 cuando la vigente es v99 (anti-replay)")
	}
}

// Poda: tras guardar 4 versiones, solo quedan las 3 más recientes.
func TestIntelB_PruneKeeps3(t *testing.T) {
	defer setupIntelTest(t)()
	for _, v := range []int{10, 11, 12, 13} {
		dbIntelStore(v, map[string][]byte{"manifest.json": []byte("x")})
	}
	for _, gone := range []int{10} {
		if _, err := dbIntelLoadVersion(gone); err == nil {
			t.Errorf("v%d debería haberse podado", gone)
		}
	}
	for _, kept := range []int{11, 12, 13} {
		if _, err := dbIntelLoadVersion(kept); err != nil {
			t.Errorf("v%d debería conservarse: %v", kept, err)
		}
	}
}

// Firma corrupta → applyFeed rechaza y NO toca el trie vigente.
func TestIntelB_TamperedRejected(t *testing.T) {
	defer setupIntelTest(t)()
	man, sig, files := loadRealFeed(t)
	loader := func(name string) ([]byte, error) { return files[name], nil }

	// manipulamos el manifest (cambia un byte) → la firma ya no valida
	bad := make([]byte, len(man))
	copy(bad, man)
	bad[len(bad)/2] ^= 0xFF

	sizeBefore := intelActive.trie.size()
	if _, err := applyFeed(bad, sig, loader, "network", false); err == nil {
		t.Fatal("manifest manipulado debería rechazarse")
	}
	if intelActive.trie.size() != sizeBefore {
		t.Error("el trie vigente NO debe cambiar si el feed nuevo es rechazado")
	}
}
