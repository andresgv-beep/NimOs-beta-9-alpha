// app_uids_test.go — Tests duros del módulo de asignación de UIDs (Fase 1).
//
// Cubre la lógica de allocator (la parte crítica de SEGURIDAD): que los UIDs
// nunca se reusen entre apps distintas, que reinstalar reuse el propio, que el
// contador solo suba, idempotencia, concurrencia, y los límites del rango.
//
// NO testea ensureAppSystemUser (toca el sistema · useradd); eso se valida en
// el Pi/Z370 a mano. Aquí testeamos toda la lógica pura de BD.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// setupTestUIDsDB · BD temporal con el schema de apps + app_uids aplicado.
func setupTestUIDsDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_uids_test_" + safeName + ".db"
	os.Remove(tmpDB)

	conn, err := sql.Open("sqlite", tmpDB+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := initAppsSchema(conn); err != nil {
		t.Fatalf("initAppsSchema: %v", err)
	}
	if err := initAppUIDsSchema(conn); err != nil {
		t.Fatalf("initAppUIDsSchema: %v", err)
	}
	cleanup := func() {
		conn.Close()
		os.Remove(tmpDB)
	}
	return conn, cleanup
}

// ── Asignación básica ────────────────────────────────────────────────

func TestAssignAppUID_FirstAppGetsBase(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a, err := assignAppUID(db, "jellyfin", "2026-06-18T10:00:00Z")
	if err != nil {
		t.Fatalf("assignAppUID: %v", err)
	}
	if a.UID != appUIDBase {
		t.Errorf("primera app debería tener UID %d, got %d", appUIDBase, a.UID)
	}
	if a.GID != appUIDBase {
		t.Errorf("GID debería igualar UID (%d), got %d", appUIDBase, a.GID)
	}
	if a.ReleasedAt != "" {
		t.Errorf("app nueva no debería estar released, got %q", a.ReleasedAt)
	}
}

func TestAssignAppUID_SecondAppGetsNext(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "jellyfin", "2026-06-18T10:00:00Z")
	a2, err := assignAppUID(db, "immich", "2026-06-18T10:01:00Z")
	if err != nil {
		t.Fatalf("assignAppUID immich: %v", err)
	}
	if a2.UID != a1.UID+1 {
		t.Errorf("segunda app debería tener UID %d, got %d", a1.UID+1, a2.UID)
	}
	if a1.UID == a2.UID {
		t.Fatal("CRÍTICO: dos apps distintas NO pueden compartir UID")
	}
}

func TestAssignAppUID_ManyAppsUniqueUIDs(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	seen := map[int]string{}
	for i := 0; i < 50; i++ {
		appID := fmt.Sprintf("app-%d", i)
		a, err := assignAppUID(db, appID, "2026-06-18T10:00:00Z")
		if err != nil {
			t.Fatalf("assignAppUID %s: %v", appID, err)
		}
		if prev, dup := seen[a.UID]; dup {
			t.Fatalf("CRÍTICO: UID %d asignado a %s Y a %s (colisión)", a.UID, prev, appID)
		}
		seen[a.UID] = appID
	}
	if len(seen) != 50 {
		t.Errorf("esperaba 50 UIDs únicos, got %d", len(seen))
	}
}

// ── Reinstalación: reusa el PROPIO UID ───────────────────────────────

func TestAssignAppUID_ReinstallReusesSameUID(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "matrix-synapse", "2026-06-18T10:00:00Z")
	// Desinstalar (marca released)
	if err := releaseAppUID(db, "matrix-synapse", "2026-06-18T11:00:00Z"); err != nil {
		t.Fatalf("releaseAppUID: %v", err)
	}
	// Reinstalar la MISMA app
	a2, err := assignAppUID(db, "matrix-synapse", "2026-06-18T12:00:00Z")
	if err != nil {
		t.Fatalf("assignAppUID reinstall: %v", err)
	}
	if a2.UID != a1.UID {
		t.Errorf("reinstalar misma app debería reusar SU UID %d, got %d", a1.UID, a2.UID)
	}
	// Tras reinstalar, debe estar activa (released_at limpio)
	got, _ := getAppUID(db, "matrix-synapse")
	if got.ReleasedAt != "" {
		t.Errorf("tras reinstalar, released_at debería estar limpio, got %q", got.ReleasedAt)
	}
}

// CRÍTICO · el UID de una app liberada NO se reusa para OTRA app distinta.
func TestAssignAppUID_ReleasedUIDNotReusedByOtherApp(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "appvieja", "2026-06-18T10:00:00Z")
	releaseAppUID(db, "appvieja", "2026-06-18T11:00:00Z")

	// Instalar OTRA app distinta · NO debe heredar el UID de appvieja
	a2, err := assignAppUID(db, "appnueva", "2026-06-18T12:00:00Z")
	if err != nil {
		t.Fatalf("assignAppUID appnueva: %v", err)
	}
	if a2.UID == a1.UID {
		t.Fatalf("CRÍTICO (ataque reciclaje UID): appnueva heredó el UID %d de appvieja", a1.UID)
	}
	if a2.UID != a1.UID+1 {
		t.Errorf("appnueva debería tener el siguiente UID %d, got %d", a1.UID+1, a2.UID)
	}
}

// ── Idempotencia y contador ──────────────────────────────────────────

func TestAssignAppUID_IdempotentForActiveApp(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "jellyfin", "2026-06-18T10:00:00Z")
	// Llamar de nuevo sobre una app ACTIVA → mismo UID, sin avanzar contador
	a2, err := assignAppUID(db, "jellyfin", "2026-06-18T10:05:00Z")
	if err != nil {
		t.Fatalf("assignAppUID repeat: %v", err)
	}
	if a2.UID != a1.UID {
		t.Errorf("re-asignar app activa debería dar el mismo UID %d, got %d", a1.UID, a2.UID)
	}
	// El contador no debió avanzar (la siguiente app nueva coge base+1)
	a3, _ := assignAppUID(db, "otra", "2026-06-18T10:06:00Z")
	if a3.UID != appUIDBase+1 {
		t.Errorf("el contador no debió malgastarse · esperaba %d, got %d", appUIDBase+1, a3.UID)
	}
}

func TestAssignAppUID_CounterNeverGoesBack(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "a", "t")
	releaseAppUID(db, "a", "t")
	a2, _ := assignAppUID(db, "b", "t")
	releaseAppUID(db, "b", "t")
	a3, _ := assignAppUID(db, "c", "t")

	if !(a1.UID < a2.UID && a2.UID < a3.UID) {
		t.Errorf("el contador debe subir siempre: a=%d b=%d c=%d", a1.UID, a2.UID, a3.UID)
	}
}

func TestReleaseAppUID_Idempotent(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	assignAppUID(db, "jellyfin", "2026-06-18T10:00:00Z")
	if err := releaseAppUID(db, "jellyfin", "2026-06-18T11:00:00Z"); err != nil {
		t.Fatalf("release 1: %v", err)
	}
	// Segunda llamada · no debe romper ni cambiar el released_at original
	if err := releaseAppUID(db, "jellyfin", "2026-06-18T12:00:00Z"); err != nil {
		t.Fatalf("release 2: %v", err)
	}
	got, _ := getAppUID(db, "jellyfin")
	if got.ReleasedAt != "2026-06-18T11:00:00Z" {
		t.Errorf("released_at debería mantener el primer valor, got %q", got.ReleasedAt)
	}
}

func TestReleaseAppUID_NonexistentApp(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()
	// Liberar una app que no existe · no debe romper
	if err := releaseAppUID(db, "fantasma", "2026-06-18T10:00:00Z"); err != nil {
		t.Errorf("release de app inexistente no debería fallar, got %v", err)
	}
}

// ── getAppUID ─────────────────────────────────────────────────────────

func TestGetAppUID_NotFound(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()
	a, err := getAppUID(db, "noexiste")
	if err != nil {
		t.Errorf("getAppUID inexistente no debería dar error, got %v", err)
	}
	if a != nil {
		t.Errorf("getAppUID inexistente debería dar nil, got %+v", a)
	}
}

// ── Validación de entrada ────────────────────────────────────────────

func TestAssignAppUID_EmptyAppID(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()
	if _, err := assignAppUID(db, "", "t"); err == nil {
		t.Error("assignAppUID con appID vacío debería fallar")
	}
	if _, err := assignAppUID(db, "   ", "t"); err == nil {
		t.Error("assignAppUID con appID en blanco debería fallar")
	}
}

// ── Persistencia entre arranques (idempotencia del schema) ───────────

func TestInitAppUIDsSchema_PreservesCounter(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	assignAppUID(db, "app1", "t") // coge base, contador → base+1

	// Simular reinicio del daemon: re-aplicar el schema (idempotente)
	if err := initAppUIDsSchema(db); err != nil {
		t.Fatalf("re-init schema: %v", err)
	}
	// El contador NO debe resetearse · la siguiente app coge base+1
	a, _ := assignAppUID(db, "app2", "t")
	if a.UID != appUIDBase+1 {
		t.Errorf("el contador debe sobrevivir al re-init · esperaba %d, got %d", appUIDBase+1, a.UID)
	}
}

// ── Concurrencia · sin colisiones bajo carga paralela ────────────────

func TestAssignAppUID_ConcurrentNoCollision(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	const n = 30
	var wg sync.WaitGroup
	results := make([]int, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			a, err := assignAppUID(db, fmt.Sprintf("concapp-%d", idx), "t")
			if err != nil {
				errs[idx] = err
				return
			}
			results[idx] = a.UID
		}(i)
	}
	wg.Wait()

	seen := map[int]int{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d falló: %v", i, errs[i])
		}
		if prev, dup := seen[results[i]]; dup {
			t.Fatalf("CRÍTICO: UID %d asignado a goroutine %d Y %d (colisión concurrente)",
				results[i], prev, i)
		}
		seen[results[i]] = i
	}
	if len(seen) != n {
		t.Errorf("esperaba %d UIDs únicos bajo concurrencia, got %d", n, len(seen))
	}
}

// ── sanitizeAppUserName ──────────────────────────────────────────────

func TestSanitizeAppUserName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"jellyfin", "nimos-app-jellyfin"},
		{"matrix-synapse", "nimos-app-matrix-synapse"},
		{"matrix_synapse", "nimos-app-matrix-synapse"}, // _ → -
		{"AppCaps", "nimos-app-appcaps"},               // minúsculas
		{"weird@name!", "nimos-app-weird-name"},        // chars raros → -
	}
	for _, c := range cases {
		got := sanitizeAppUserName(c.in)
		if got != c.want {
			t.Errorf("sanitizeAppUserName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeAppUserName_LongTruncated(t *testing.T) {
	long := strings.Repeat("x", 50)
	got := sanitizeAppUserName(long)
	if len(got) > 32 {
		t.Errorf("nombre debería truncarse a ≤32 chars, got %d (%q)", len(got), got)
	}
}

// ─── Fase 4 · Reconciler de higiene ──────────────────────────────────

// TestReconcileDecision · la lógica pura de decisión.
func TestReconcileDecision(t *testing.T) {
	cases := []struct {
		name             string
		isActive         bool
		hasFiles         bool
		want             string
	}{
		{"activa con datos", true, true, "preserve"},
		{"activa sin datos", true, false, "preserve"},   // activa → nunca tocar
		{"inactiva con datos (desinstalada-normal)", false, true, "preserve"}, // CRÍTICO
		{"inactiva sin datos (desinstalada-total)", false, false, "clean"},
	}
	for _, c := range cases {
		got := reconcileDecision(c.isActive, c.hasFiles)
		if got != c.want {
			t.Errorf("%s: reconcileDecision(active=%v,files=%v)=%q, want %q",
				c.name, c.isActive, c.hasFiles, got, c.want)
		}
	}
}

// TestReconcileAppUIDs_PreservesAppWithData · CRÍTICO · una app desinstalada
// que CONSERVA datos (normal) NO debe limpiarse (sus datos la necesitan).
func TestReconcileAppUIDs_PreservesAppWithData(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	// App con datos conservados (desinstalada-normal)
	a, _ := assignAppUID(db, "jellyfin", "t")
	releaseAppUID(db, "jellyfin", "t2")

	// hasFilesFn dice que SÍ tiene archivos (datos conservados)
	hasFiles := func(uid int) bool { return uid == a.UID } // jellyfin tiene datos

	rep := reconcileAppUIDs(db, "/fake", map[string]bool{}, hasFiles)

	// Debe CONSERVAR jellyfin (tiene datos), NO limpiarlo
	if len(rep.Cleaned) != 0 {
		t.Errorf("CRÍTICO: jellyfin con datos NO debe limpiarse, cleaned=%v", rep.Cleaned)
	}
	if len(rep.Preserved) != 1 || rep.Preserved[0] != "jellyfin" {
		t.Errorf("jellyfin con datos debe conservarse, preserved=%v", rep.Preserved)
	}
}

// TestReconcileAppUIDs_CleansAppWithoutData · una app desinstalada-total (sin
// datos) SÍ se limpia (su usuario de sistema sobra).
func TestReconcileAppUIDs_CleansAppWithoutData(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	assignAppUID(db, "tempapp", "t")
	releaseAppUID(db, "tempapp", "t2")

	// hasFilesFn dice que NO tiene archivos (datos borrados, total)
	hasFiles := func(uid int) bool { return false }

	rep := reconcileAppUIDs(db, "/fake", map[string]bool{}, hasFiles)

	if len(rep.Cleaned) != 1 || rep.Cleaned[0] != "tempapp" {
		t.Errorf("tempapp sin datos debe limpiarse, cleaned=%v", rep.Cleaned)
	}
}

// TestReconcileAppUIDs_NeverTouchesActiveApp · una app activa NO se toca aunque
// figure como released por error.
func TestReconcileAppUIDs_NeverTouchesActiveApp(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	assignAppUID(db, "immich", "t")
	releaseAppUID(db, "immich", "t2") // marcada released por error

	// immich está ACTIVA y sin datos · aún así NO debe tocarse
	hasFiles := func(uid int) bool { return false }
	active := map[string]bool{"immich": true}

	rep := reconcileAppUIDs(db, "/fake", active, hasFiles)

	if len(rep.Cleaned) != 0 {
		t.Errorf("CRÍTICO: app activa NUNCA debe limpiarse, cleaned=%v", rep.Cleaned)
	}
}

// TestReconcileAppUIDs_DoesNotReuseUID · tras limpiar un usuario, el UID NO se
// reusa · una app nueva sigue cogiendo el siguiente número.
func TestReconcileAppUIDs_DoesNotReuseUID(t *testing.T) {
	db, cleanup := setupTestUIDsDB(t)
	defer cleanup()

	a1, _ := assignAppUID(db, "vieja", "t")
	releaseAppUID(db, "vieja", "t2")

	// Reconcile limpia el usuario (sin datos)
	reconcileAppUIDs(db, "/fake", map[string]bool{}, func(uid int) bool { return false })

	// Una app nueva NO debe coger el UID de "vieja"
	a2, _ := assignAppUID(db, "nueva", "t3")
	if a2.UID == a1.UID {
		t.Fatalf("CRÍTICO: el UID %d de 'vieja' se reusó para 'nueva' tras reconcile", a1.UID)
	}
	if a2.UID != a1.UID+1 {
		t.Errorf("nueva debe coger el siguiente UID %d, got %d", a1.UID+1, a2.UID)
	}
}
