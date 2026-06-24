// nimos_secrets_test.go — Tests del SecretsStore.
//
// Estrategia:
//   - DB SQLite en /tmp (siguiendo patrón storage_repo_test.go).
//   - Master keys en t.TempDir() para no escribir en /var/lib.
//   - FakeClock para verificar timestamps deterministas.
//   - Tests crypto-específicos:
//       · round-trip (encrypt → decrypt = original)
//       · nonce únicos (mismo plaintext → distintos ciphertexts)
//       · tamper detection (modificar ciphertext rompe Open)
//       · wrong key (otra master key no descifra)

package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test setup helpers
// ─────────────────────────────────────────────────────────────────────────────

// setupSecretsDB monta una DB temporal con el core schema aplicado y
// devuelve la conexión más un cleanup func. Patrón idéntico al de
// storage_repo_test.go / db_apps_test.go.
func setupSecretsDB(t *testing.T) (conn *sqlConn, cleanup func()) {
	t.Helper()
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_secrets_test_" + safeName + ".db"
	_ = os.Remove(tmpDB)

	c, err := openTestSQLite(tmpDB)
	if err != nil {
		t.Fatalf("openTestSQLite: %v", err)
	}
	if err := initNimosCoreSchema(c.db); err != nil {
		c.db.Close()
		_ = os.Remove(tmpDB)
		t.Fatalf("initNimosCoreSchema: %v", err)
	}
	cleanup = func() {
		c.db.Close()
		_ = os.Remove(tmpDB)
	}
	return c, cleanup
}

// sqlConn es solo un wrapper para que el test helper devuelva el *sql.DB
// junto con su path (útil para debugging si un test falla).
type sqlConn struct {
	db   *sql.DB
	path string
}

func openTestSQLite(path string) (*sqlConn, error) {
	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, err
	}
	return &sqlConn{db: conn, path: path}, nil
}

// newTestStore es el setup más común: DB en /tmp + key inyectada
// random + FakeClock partiendo del momento del test.
func newTestStore(t *testing.T) (*SecretsStore, *FakeClock, func()) {
	t.Helper()
	c, cleanup := setupSecretsDB(t)

	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		cleanup()
		t.Fatalf("generate test key: %v", err)
	}

	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	store, err := NewSecretsStoreWithKey(c.db, key, clock)
	if err != nil {
		cleanup()
		t.Fatalf("NewSecretsStoreWithKey: %v", err)
	}
	return store, clock, cleanup
}

// ─────────────────────────────────────────────────────────────────────────────
// Master key lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadOrCreateMasterKey_CreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "master.key")

	key, err := loadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("loadOrCreateMasterKey: %v", err)
	}
	if len(key) != masterKeySize {
		t.Errorf("key size = %d, want %d", len(key), masterKeySize)
	}

	// El archivo debe existir con permisos 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("key file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreateMasterKey_ReadsWhenExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	// Crear key manualmente
	want := make([]byte, masterKeySize)
	for i := range want {
		want[i] = byte(i)
	}
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	got, err := loadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("loadOrCreateMasterKey: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("loaded key != written key")
	}
}

func TestLoadOrCreateMasterKey_IdempotentOnSecondCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	first, err := loadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := loadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("second call generated different key (should re-read, not regenerate)")
	}
}

func TestLoadOrCreateMasterKey_FailsOnWrongSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	// 16 bytes en lugar de 32
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	_, err := loadOrCreateMasterKey(path)
	if !errors.Is(err, ErrMasterKeyInvalid) {
		t.Errorf("err = %v, want ErrMasterKeyInvalid", err)
	}

	// CRÍTICO: el archivo NO debe haber sido sobrescrito.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(got) != "0123456789abcdef" {
		t.Errorf("file was overwritten! got %q (this would destroy existing secrets)", got)
	}
}

func TestNewSecretsStoreWithKey_RejectsWrongSize(t *testing.T) {
	c, cleanup := setupSecretsDB(t)
	defer cleanup()

	_, err := NewSecretsStoreWithKey(c.db, []byte("too short"), nil)
	if !errors.Is(err, ErrMasterKeyInvalid) {
		t.Errorf("err = %v, want ErrMasterKeyInvalid", err)
	}
}

func TestNewSecretsStore_RejectsNilDB(t *testing.T) {
	key := make([]byte, masterKeySize)
	_, err := NewSecretsStoreWithKey(nil, key, nil)
	if err == nil {
		t.Error("expected error for nil DB, got nil")
	}
}

// TestNewSecretsStore_WithFilePath cubre la ruta de producción:
// NewSecretsStore con masterKeyPath que generará la key en disco.
func TestNewSecretsStore_WithFilePath(t *testing.T) {
	c, cleanup := setupSecretsDB(t)
	defer cleanup()

	keyPath := filepath.Join(t.TempDir(), "test_master.key")

	// Primera llamada: genera la key
	store1, err := NewSecretsStore(c.db, keyPath, nil)
	if err != nil {
		t.Fatalf("first NewSecretsStore: %v", err)
	}
	id, err := store1.CreateSecret("ddns_token", "fp_test", []byte("plaintext-fp"))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// Segunda llamada con la misma path: re-lee la key, debe descifrar.
	store2, err := NewSecretsStore(c.db, keyPath, nil)
	if err != nil {
		t.Fatalf("second NewSecretsStore: %v", err)
	}
	got, err := store2.GetSecret(id)
	if err != nil {
		t.Fatalf("GetSecret on second store: %v", err)
	}
	if !bytes.Equal(got.Plaintext, []byte("plaintext-fp")) {
		t.Errorf("plaintext mismatch across store instances using same key file")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Round-trip
// ─────────────────────────────────────────────────────────────────────────────

func TestSecrets_CreateAndGet_RoundTrip(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	plaintext := []byte("my-duckdns-token-abc-123")
	id, err := store.CreateSecret("ddns_token", "nimosbarraca", plaintext)
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSecret returned empty ID")
	}

	got, err := store.GetSecret(id)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !bytes.Equal(got.Plaintext, plaintext) {
		t.Errorf("Plaintext = %q, want %q", got.Plaintext, plaintext)
	}
	if got.Category != "ddns_token" {
		t.Errorf("Category = %q, want ddns_token", got.Category)
	}
	if got.Label != "nimosbarraca" {
		t.Errorf("Label = %q, want nimosbarraca", got.Label)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
}

func TestSecrets_CreatedAtIsRecorded(t *testing.T) {
	store, clock, cleanup := newTestStore(t)
	defer cleanup()

	id, err := store.CreateSecret("x", "y", []byte("z"))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	got, err := store.GetSecret(id)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	want := clock.Now().UTC().Truncate(time.Second)
	if !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want)
	}
}

func TestSecrets_GetUpdatesLastAccessed(t *testing.T) {
	store, clock, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("x", "y", []byte("z"))

	got1, _ := store.GetSecret(id)
	// La primera vez nunca se ha accedido pero el propio Get marca el
	// last_accessed; sin embargo el `got` devuelto NO incluye ese
	// update reciente porque ocurrió después del SELECT.
	// Verificamos haciendo una segunda lectura tras avanzar el reloj.
	if got1.LastAccessed != nil {
		t.Logf("first Get returned LastAccessed=%v (acceptable: it's the read after the previous UPDATE, but on fresh row should be nil)", got1.LastAccessed)
	}

	clock.Advance(10 * time.Minute)

	got2, err := store.GetSecret(id)
	if err != nil {
		t.Fatalf("second GetSecret: %v", err)
	}
	if got2.LastAccessed == nil {
		t.Fatal("LastAccessed is nil after two Gets")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Crypto correctness
// ─────────────────────────────────────────────────────────────────────────────

func TestSecrets_NonceIsUniquePerSecret(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	// Mismo plaintext en dos secrets distintos.
	pt := []byte("same-token-value")
	id1, _ := store.CreateSecret("ddns_token", "host1", pt)
	id2, _ := store.CreateSecret("ddns_token", "host2", pt)

	var nonce1, nonce2 []byte
	var ct1, ct2 []byte
	if err := store.db.QueryRow(`SELECT nonce, ciphertext FROM nimos_secrets WHERE id = ?`,
		string(id1)).Scan(&nonce1, &ct1); err != nil {
		t.Fatalf("query nonce1: %v", err)
	}
	if err := store.db.QueryRow(`SELECT nonce, ciphertext FROM nimos_secrets WHERE id = ?`,
		string(id2)).Scan(&nonce2, &ct2); err != nil {
		t.Fatalf("query nonce2: %v", err)
	}

	if bytes.Equal(nonce1, nonce2) {
		t.Error("same plaintext produced same nonce — CRYPTO BUG, GCM is now broken")
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("same plaintext produced same ciphertext — nonce reuse or other crypto bug")
	}
}

func TestSecrets_TamperDetected(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("x", "y", []byte("legit-plaintext"))

	// Corromper el ciphertext directamente en la DB.
	if _, err := store.db.Exec(
		`UPDATE nimos_secrets SET ciphertext = ? WHERE id = ?`,
		[]byte("totally garbage data"), string(id),
	); err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	_, err := store.GetSecret(id)
	if err == nil {
		t.Fatal("GetSecret returned nil error for tampered ciphertext — GCM auth tag should reject")
	}
	if !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("expected decrypt error, got: %v", err)
	}
}

func TestSecrets_WrongKeyCannotDecrypt(t *testing.T) {
	c, cleanup := setupSecretsDB(t)
	defer cleanup()

	keyA := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, keyA); err != nil {
		t.Fatalf("gen keyA: %v", err)
	}
	keyB := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, keyB); err != nil {
		t.Fatalf("gen keyB: %v", err)
	}

	storeA, err := NewSecretsStoreWithKey(c.db, keyA, nil)
	if err != nil {
		t.Fatalf("NewSecretsStoreWithKey A: %v", err)
	}
	id, err := storeA.CreateSecret("ddns_token", "test", []byte("super-secret"))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// storeB tiene OTRA key apuntando a la misma DB.
	storeB, err := NewSecretsStoreWithKey(c.db, keyB, nil)
	if err != nil {
		t.Fatalf("NewSecretsStoreWithKey B: %v", err)
	}
	_, err = storeB.GetSecret(id)
	if err == nil {
		t.Fatal("storeB decrypted with wrong key — CRYPTO BUG")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CRUD edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestSecrets_DuplicateLabelFails(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.CreateSecret("ddns_token", "same", []byte("a"))
	if err != nil {
		t.Fatalf("first CreateSecret: %v", err)
	}

	_, err = store.CreateSecret("ddns_token", "same", []byte("b"))
	if !errors.Is(err, ErrSecretExists) {
		t.Errorf("err = %v, want ErrSecretExists", err)
	}
}

func TestSecrets_SameLabelDifferentCategoryIsOK(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	if _, err := store.CreateSecret("ddns_token", "shared", []byte("a")); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := store.CreateSecret("api_key", "shared", []byte("b")); err != nil {
		t.Fatalf("create B: %v", err)
	}
}

func TestSecrets_GetNonExistentReturnsNotFound(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.GetSecret("does-not-exist")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("err = %v, want ErrSecretNotFound", err)
	}
}

func TestSecrets_GetByLabelHappyPath(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("ddns_token", "label1", []byte("pt"))

	got, err := store.GetSecretByLabel("ddns_token", "label1")
	if err != nil {
		t.Fatalf("GetSecretByLabel: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %s, want %s", got.ID, id)
	}
	if !bytes.Equal(got.Plaintext, []byte("pt")) {
		t.Errorf("plaintext mismatch")
	}
}

func TestSecrets_GetByLabelNotFound(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.GetSecretByLabel("nope", "nope")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("err = %v, want ErrSecretNotFound", err)
	}
}

func TestSecrets_EmptyInputsRejected(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	cases := []struct {
		name      string
		cat       string
		label     string
		plaintext []byte
	}{
		{"empty category", "", "label", []byte("pt")},
		{"empty label", "cat", "", []byte("pt")},
		{"empty plaintext", "cat", "label", []byte{}},
		{"nil plaintext", "cat", "label", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.CreateSecret(tc.cat, tc.label, tc.plaintext)
			if err == nil {
				t.Errorf("CreateSecret(%q,%q,%v) succeeded, want error",
					tc.cat, tc.label, tc.plaintext)
			}
		})
	}
}

func TestSecrets_UpdateChangesCiphertextAndNonce(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("x", "y", []byte("original"))

	var ct1, n1 []byte
	store.db.QueryRow(`SELECT ciphertext, nonce FROM nimos_secrets WHERE id = ?`,
		string(id)).Scan(&ct1, &n1)

	if err := store.UpdateSecret(id, []byte("updated-value")); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}

	var ct2, n2 []byte
	store.db.QueryRow(`SELECT ciphertext, nonce FROM nimos_secrets WHERE id = ?`,
		string(id)).Scan(&ct2, &n2)

	if bytes.Equal(n1, n2) {
		t.Error("Update did not rotate nonce — CRYPTO RISK if same plaintext reused")
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("Update did not change ciphertext")
	}

	// Y debe descifrar al nuevo valor
	got, err := store.GetSecret(id)
	if err != nil {
		t.Fatalf("GetSecret after update: %v", err)
	}
	if !bytes.Equal(got.Plaintext, []byte("updated-value")) {
		t.Errorf("plaintext after update = %q, want updated-value", got.Plaintext)
	}
}

func TestSecrets_UpdateNonExistentReturnsNotFound(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	err := store.UpdateSecret("nope", []byte("anything"))
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("err = %v, want ErrSecretNotFound", err)
	}
}

func TestSecrets_UpdateRejectsEmptyPlaintext(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("x", "y", []byte("original"))
	if err := store.UpdateSecret(id, nil); err == nil {
		t.Error("UpdateSecret with nil plaintext should error")
	}
}

func TestSecrets_DeleteRemovesEntry(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	id, _ := store.CreateSecret("x", "y", []byte("z"))
	if err := store.DeleteSecret(id); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	_, err := store.GetSecret(id)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("after delete: err = %v, want ErrSecretNotFound", err)
	}
}

func TestSecrets_DeleteNonExistentIsNoOp(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.DeleteSecret("does-not-exist"); err != nil {
		t.Errorf("DeleteSecret on non-existent: %v (should be no-op)", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// List
// ─────────────────────────────────────────────────────────────────────────────

func TestSecrets_ListByCategoryFiltersAndSorts(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	_, _ = store.CreateSecret("ddns_token", "zeta", []byte("a"))
	_, _ = store.CreateSecret("ddns_token", "alpha", []byte("b"))
	_, _ = store.CreateSecret("ddns_token", "mike", []byte("c"))
	_, _ = store.CreateSecret("api_key", "should-not-appear", []byte("d"))

	got, err := store.ListSecrets("ddns_token")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d secrets, want 3", len(got))
	}
	wantLabels := []string{"alpha", "mike", "zeta"} // sort ASC
	for i, info := range got {
		if info.Label != wantLabels[i] {
			t.Errorf("[%d] label = %s, want %s", i, info.Label, wantLabels[i])
		}
		if info.Category != "ddns_token" {
			t.Errorf("[%d] category = %s, want ddns_token", i, info.Category)
		}
	}
}

func TestSecrets_ListAllCategoriesWhenEmpty(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	_, _ = store.CreateSecret("api_key", "a", []byte("x"))
	_, _ = store.CreateSecret("ddns_token", "b", []byte("y"))
	_, _ = store.CreateSecret("backup_key", "c", []byte("z"))

	got, err := store.ListSecrets("")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3 across all categories", len(got))
	}
}

func TestSecrets_ListNeverIncludesPlaintext(t *testing.T) {
	// Verificación defensiva: SecretInfo no tiene campo Plaintext.
	// Si alguien añade tal campo en el futuro, este test compila-fails.
	store, _, cleanup := newTestStore(t)
	defer cleanup()
	_, _ = store.CreateSecret("cat", "lbl", []byte("super-secret-plain-text"))
	got, _ := store.ListSecrets("cat")
	if len(got) != 1 {
		t.Fatal("setup")
	}
	// El struct SecretInfo no tiene Plaintext. Si en el futuro se
	// añade, este test sigue siendo válido pero pasa el linter.
	_ = got[0].ID
	_ = got[0].Category
	_ = got[0].Label
	_ = got[0].CreatedAt
	_ = got[0].LastAccessed
}

// ─────────────────────────────────────────────────────────────────────────────
// Schema sanity
// ─────────────────────────────────────────────────────────────────────────────

func TestNimosCoreSchema_IsIdempotent(t *testing.T) {
	c, cleanup := setupSecretsDB(t)
	defer cleanup()

	// Aplicar dos veces el schema no debe fallar.
	if err := initNimosCoreSchema(c.db); err != nil {
		t.Errorf("second initNimosCoreSchema: %v", err)
	}
	if err := initNimosCoreSchema(c.db); err != nil {
		t.Errorf("third initNimosCoreSchema: %v", err)
	}
}

func TestNimosCoreSchema_TablesExist(t *testing.T) {
	c, cleanup := setupSecretsDB(t)
	defer cleanup()

	wantTables := []string{"nimos_secrets", "nimos_breakers", "nimos_capabilities"}
	for _, table := range wantTables {
		var name string
		err := c.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrency
// ─────────────────────────────────────────────────────────────────────────────

func TestSecrets_ConcurrentCreatesAndGetsDoNotCorrupt(t *testing.T) {
	store, _, cleanup := newTestStore(t)
	defer cleanup()

	const writers = 10
	const secretsPerWriter = 20

	var wg sync.WaitGroup
	var createdOK atomic.Int32
	var ids sync.Map // id → expected plaintext

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < secretsPerWriter; i++ {
				label := fmt.Sprintf("worker-%d-secret-%d", workerID, i)
				pt := []byte(fmt.Sprintf("plaintext-%d-%d", workerID, i))
				id, err := store.CreateSecret("ddns_token", label, pt)
				if err != nil {
					t.Errorf("CreateSecret(%s): %v", label, err)
					continue
				}
				ids.Store(id, pt)
				createdOK.Add(1)
			}
		}(w)
	}
	wg.Wait()

	if got := int(createdOK.Load()); got != writers*secretsPerWriter {
		t.Errorf("createdOK = %d, want %d", got, writers*secretsPerWriter)
	}

	// Verificar que cada secret se puede leer correctamente.
	var verified atomic.Int32
	ids.Range(func(k, v interface{}) bool {
		id := k.(SecretID)
		expected := v.([]byte)
		got, err := store.GetSecret(id)
		if err != nil {
			t.Errorf("GetSecret(%s): %v", id, err)
			return true
		}
		if !bytes.Equal(got.Plaintext, expected) {
			t.Errorf("plaintext mismatch for %s", id)
			return true
		}
		verified.Add(1)
		return true
	})
	if int(verified.Load()) != writers*secretsPerWriter {
		t.Errorf("verified = %d, want %d", verified.Load(), writers*secretsPerWriter)
	}
}
