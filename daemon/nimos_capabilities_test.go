// nimos_capabilities_test.go — Tests del CapabilitiesStore + RealDetect.
//
// Estrategia:
//   - Tests del Store con MockDetect (controlable, sin tocar el sistema).
//   - Tests de RealDetect verifican que NO panique y rellena DetectedAt,
//     pero NO asumen valores concretos (depende del sistema host).
//   - FakeClock para determinismo temporal.

package main

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// setupCapsDB monta una DB temporal con el core schema. Reutiliza el
// patrón de setupSecretsDB (mismo wrapper sqlConn).
func setupCapsDB(t *testing.T) (*sqlConn, func()) {
	t.Helper()
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_caps_test_" + safeName + ".db"
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
	cleanup := func() {
		c.db.Close()
		_ = os.Remove(tmpDB)
	}
	return c, cleanup
}

// countingDetect devuelve un DetectFunc que devuelve un struct fijo
// y cuenta cuántas veces se ha llamado. Útil para verificar que el
// cache funciona (no llama detect cuando es fresco).
type countingDetect struct {
	calls atomic.Int32
	caps  SystemCapabilities
}

func (c *countingDetect) fn() SystemCapabilities {
	c.calls.Add(1)
	// Devolvemos una copia para evitar que el store mute el original.
	return c.caps
}

// mockCaps devuelve un struct fijo realista.
func mockCaps() SystemCapabilities {
	return SystemCapabilities{
		CertbotInstalled: true,
		CertbotVersion:   "2.10.0",
		OpenSSLInstalled: true,
		UPnPClient:       false,
		NFTBackend:       true,
		IPTablesBackend:  true,
		UFWInstalled:     false,
		DigInstalled:     true,
		HostInstalled:    false,
		SystemdAvailable: true,
	}
}

// newTestStore (capabilities edition) monta un store con detect mockeable
// y FakeClock. Devuelve también el counter para asserts.
func newTestCapsStore(t *testing.T) (*CapabilitiesStore, *countingDetect, *FakeClock, func()) {
	t.Helper()
	c, cleanup := setupCapsDB(t)
	cnt := &countingDetect{caps: mockCaps()}
	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	store, err := NewCapabilitiesStore(c.db, clock, cnt.fn)
	if err != nil {
		cleanup()
		t.Fatalf("NewCapabilitiesStore: %v", err)
	}
	return store, cnt, clock, cleanup
}

// ─────────────────────────────────────────────────────────────────────────────
// Construction
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_NewRejectsNilDB(t *testing.T) {
	_, err := NewCapabilitiesStore(nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil DB")
	}
}

func TestCaps_NewAcceptsNilDetectAndClock(t *testing.T) {
	c, cleanup := setupCapsDB(t)
	defer cleanup()

	store, err := NewCapabilitiesStore(c.db, nil, nil)
	if err != nil {
		t.Fatalf("NewCapabilitiesStore: %v", err)
	}
	if store.clock == nil {
		t.Error("clock not defaulted to RealClock")
	}
	if store.detect == nil {
		t.Error("detect not defaulted to RealDetect")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Get / ForceRefresh
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_GetOnEmptyDBWithMaxAgeForcesRefresh(t *testing.T) {
	store, cnt, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	caps, err := store.Get(1 * time.Hour)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cnt.calls.Load() != 1 {
		t.Errorf("detect called %d times, want 1 (empty DB should trigger refresh)", cnt.calls.Load())
	}
	if !caps.CertbotInstalled || caps.CertbotVersion != "2.10.0" {
		t.Errorf("unexpected caps: %+v", caps)
	}
}

func TestCaps_GetOnEmptyDBWithZeroMaxAgeReturnsNotPersisted(t *testing.T) {
	store, cnt, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	_, err := store.Get(0)
	if !errors.Is(err, ErrCapabilitiesNotPersisted) {
		t.Errorf("err = %v, want ErrCapabilitiesNotPersisted", err)
	}
	if cnt.calls.Load() != 0 {
		t.Errorf("detect called %d times, want 0 (maxAge=0 must not refresh)", cnt.calls.Load())
	}
}

func TestCaps_FreshCacheDoesNotRefresh(t *testing.T) {
	store, cnt, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	// Primer Get → detecta y persiste.
	_, err := store.Get(1 * time.Hour)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if cnt.calls.Load() != 1 {
		t.Fatalf("setup: calls = %d, want 1", cnt.calls.Load())
	}

	// Avanzar SOLO 10 min — sigue fresca (1h threshold).
	clock.Advance(10 * time.Minute)

	caps2, err := store.Get(1 * time.Hour)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if cnt.calls.Load() != 1 {
		t.Errorf("detect called %d times, want still 1 (cache still fresh)", cnt.calls.Load())
	}
	if !caps2.CertbotInstalled {
		t.Error("second Get returned different caps")
	}
}

func TestCaps_StaleCacheTriggersRefresh(t *testing.T) {
	store, cnt, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	_, _ = store.Get(1 * time.Hour) // 1ª detect
	if cnt.calls.Load() != 1 {
		t.Fatalf("setup: calls = %d, want 1", cnt.calls.Load())
	}

	// Avanzar 2 horas — stale para threshold de 1h.
	clock.Advance(2 * time.Hour)

	_, err := store.Get(1 * time.Hour)
	if err != nil {
		t.Fatalf("Get after stale: %v", err)
	}
	if cnt.calls.Load() != 2 {
		t.Errorf("detect called %d times, want 2 (stale should refresh)", cnt.calls.Load())
	}
}

func TestCaps_ZeroMaxAgeReturnsCachedWithoutRefresh(t *testing.T) {
	store, cnt, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	// Crear persistencia.
	_, _ = store.Get(1 * time.Hour)

	// Avanzar mucho, pero pedir con maxAge=0 → no debe refrescar.
	clock.Advance(99 * time.Hour)

	caps, err := store.Get(0)
	if err != nil {
		t.Fatalf("Get(0): %v", err)
	}
	if cnt.calls.Load() != 1 {
		t.Errorf("detect called %d times, want 1 (maxAge=0 must use cache)", cnt.calls.Load())
	}
	// El DetectedAt debe ser el ORIGINAL, no el current.
	originalTime := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	if !caps.DetectedAt.Equal(originalTime) {
		t.Errorf("DetectedAt = %v, want %v (original)", caps.DetectedAt, originalTime)
	}
}

func TestCaps_ForceRefreshAlwaysDetects(t *testing.T) {
	store, cnt, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		_, err := store.ForceRefresh()
		if err != nil {
			t.Fatalf("ForceRefresh %d: %v", i, err)
		}
	}
	if cnt.calls.Load() != 5 {
		t.Errorf("detect called %d times, want 5", cnt.calls.Load())
	}
}

func TestCaps_ForceRefreshUpdatesDetectedAt(t *testing.T) {
	store, _, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	caps1, _ := store.ForceRefresh()
	clock.Advance(3 * time.Hour)
	caps2, _ := store.ForceRefresh()

	if !caps2.DetectedAt.After(caps1.DetectedAt) {
		t.Errorf("second refresh DetectedAt (%v) not after first (%v)",
			caps2.DetectedAt, caps1.DetectedAt)
	}
}

func TestCaps_ForceRefreshUsesClockNotRealNow(t *testing.T) {
	store, cnt, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	// El mockDetect siempre devuelve DetectedAt=zero. El store debe
	// sobrescribirlo con clock.Now().
	cnt.caps.DetectedAt = time.Time{} // explícito

	caps, err := store.ForceRefresh()
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	want := clock.Now().UTC()
	if !caps.DetectedAt.Equal(want) {
		t.Errorf("DetectedAt = %v, want %v (clock-driven)", caps.DetectedAt, want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Persistence (cross-instance)
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_PersistsAcrossStoreInstances(t *testing.T) {
	c, cleanup := setupCapsDB(t)
	defer cleanup()

	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	cnt := &countingDetect{caps: mockCaps()}

	store1, err := NewCapabilitiesStore(c.db, clock, cnt.fn)
	if err != nil {
		t.Fatal(err)
	}
	caps1, err := store1.ForceRefresh()
	if err != nil {
		t.Fatal(err)
	}

	// Nuevo store con OTRO detect que sería distinguible si se llamase.
	otherCaps := mockCaps()
	otherCaps.CertbotVersion = "9.99.9-DIFFERENT"
	otherCnt := &countingDetect{caps: otherCaps}
	store2, err := NewCapabilitiesStore(c.db, clock, otherCnt.fn)
	if err != nil {
		t.Fatal(err)
	}

	// Get fresca (sin avanzar reloj) → debe leer de DB, no llamar a otherCnt.
	caps2, err := store2.Get(1 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if otherCnt.calls.Load() != 0 {
		t.Errorf("otherDetect called %d times, want 0 (should read from DB)", otherCnt.calls.Load())
	}
	if caps2.CertbotVersion != caps1.CertbotVersion {
		t.Errorf("CertbotVersion across stores = %s, want %s", caps2.CertbotVersion, caps1.CertbotVersion)
	}
}

func TestCaps_JSONRoundTrip(t *testing.T) {
	store, _, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	want := SystemCapabilities{
		CertbotInstalled: true,
		CertbotVersion:   "2.10.0",
		OpenSSLInstalled: true,
		NFTBackend:       true,
		IPTablesBackend:  false,
		DigInstalled:     true,
		SystemdAvailable: true,
		DetectedAt:       time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}

	if err := store.writeToDB(&want); err != nil {
		t.Fatalf("writeToDB: %v", err)
	}
	got, err := store.readFromDB()
	if err != nil {
		t.Fatalf("readFromDB: %v", err)
	}

	if got.CertbotInstalled != want.CertbotInstalled ||
		got.CertbotVersion != want.CertbotVersion ||
		got.OpenSSLInstalled != want.OpenSSLInstalled ||
		got.NFTBackend != want.NFTBackend ||
		got.IPTablesBackend != want.IPTablesBackend ||
		got.DigInstalled != want.DigInstalled ||
		got.SystemdAvailable != want.SystemdAvailable {
		t.Errorf("round-trip mismatch:\n  want: %+v\n  got:  %+v", want, *got)
	}
	if !got.DetectedAt.Equal(want.DetectedAt) {
		t.Errorf("DetectedAt mismatch: got %v want %v", got.DetectedAt, want.DetectedAt)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LastDetected
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_LastDetectedReturnsTimestamp(t *testing.T) {
	store, _, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	_, _ = store.ForceRefresh()
	got, err := store.LastDetected()
	if err != nil {
		t.Fatalf("LastDetected: %v", err)
	}
	if !got.Equal(clock.Now().UTC()) {
		t.Errorf("LastDetected = %v, want %v", got, clock.Now().UTC())
	}
}

func TestCaps_LastDetectedOnEmptyDBReturnsError(t *testing.T) {
	store, _, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	_, err := store.LastDetected()
	if !errors.Is(err, ErrCapabilitiesNotPersisted) {
		t.Errorf("err = %v, want ErrCapabilitiesNotPersisted", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper methods on SystemCapabilities
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_HasAnyFirewallBackend(t *testing.T) {
	cases := []struct {
		name string
		c    SystemCapabilities
		want bool
	}{
		{"only nft", SystemCapabilities{NFTBackend: true}, true},
		{"only iptables", SystemCapabilities{IPTablesBackend: true}, true},
		{"both", SystemCapabilities{NFTBackend: true, IPTablesBackend: true}, true},
		{"neither", SystemCapabilities{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.HasAnyFirewallBackend(); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestCaps_SupportsDNS01(t *testing.T) {
	cases := []struct {
		name string
		c    SystemCapabilities
		want bool
	}{
		{"both", SystemCapabilities{CertbotInstalled: true, DigInstalled: true}, true},
		{"only certbot", SystemCapabilities{CertbotInstalled: true}, false},
		{"only dig", SystemCapabilities{DigInstalled: true}, false},
		{"neither", SystemCapabilities{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.SupportsDNS01(); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrency (single-flight)
// ─────────────────────────────────────────────────────────────────────────────

func TestCaps_ConcurrentForceRefreshSerializes(t *testing.T) {
	store, cnt, _, cleanup := newTestCapsStore(t)
	defer cleanup()

	const concurrent = 20
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ForceRefresh()
			if err != nil {
				t.Errorf("ForceRefresh: %v", err)
			}
		}()
	}
	wg.Wait()

	// Cada ForceRefresh hace su propio detect. La protección del mutex
	// asegura que NO se mezclan, no que se deduplique.
	if int(cnt.calls.Load()) != concurrent {
		t.Errorf("detect calls = %d, want %d (each ForceRefresh detects once)",
			cnt.calls.Load(), concurrent)
	}
}

func TestCaps_ConcurrentGetRefreshesOnlyWhenStale(t *testing.T) {
	store, cnt, clock, cleanup := newTestCapsStore(t)
	defer cleanup()

	// Primer Get genera persistencia fresca.
	_, _ = store.Get(1 * time.Hour)
	if cnt.calls.Load() != 1 {
		t.Fatalf("setup: calls = %d, want 1", cnt.calls.Load())
	}

	// Avanzar 30min — sigue fresca. Múltiples Gets concurrentes NO deben
	// llamar a detect.
	clock.Advance(30 * time.Minute)

	const concurrent = 50
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Get(1 * time.Hour)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	wg.Wait()

	if cnt.calls.Load() != 1 {
		t.Errorf("detect calls = %d, want still 1 (fresh cache should not refresh)",
			cnt.calls.Load())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RealDetect — sanity tests (no asserts on specific values)
// ─────────────────────────────────────────────────────────────────────────────

func TestRealDetect_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RealDetect panicked: %v", r)
		}
	}()
	caps := RealDetect()
	if caps.DetectedAt.IsZero() {
		t.Error("RealDetect did not set DetectedAt")
	}
}

func TestRealDetect_DetectedAtIsRecent(t *testing.T) {
	before := time.Now().UTC()
	caps := RealDetect()
	after := time.Now().UTC()

	if caps.DetectedAt.Before(before) || caps.DetectedAt.After(after) {
		t.Errorf("DetectedAt = %v, expected between %v and %v",
			caps.DetectedAt, before, after)
	}
}

func TestRealDetect_BooleansAreActuallyBooleans(t *testing.T) {
	// Test trivial pero verifica que el struct se rellena sin errores
	// silenciosos. En Go los bool no tienen este problema, pero el test
	// sirve como smoke check.
	caps := RealDetect()
	_ = caps.CertbotInstalled
	_ = caps.OpenSSLInstalled
	_ = caps.UPnPClient
	_ = caps.NFTBackend
	_ = caps.IPTablesBackend
	_ = caps.UFWInstalled
	_ = caps.DigInstalled
	_ = caps.HostInstalled
	_ = caps.SystemdAvailable
}

// ─────────────────────────────────────────────────────────────────────────────
// detectCertbotVersion — parsing
// ─────────────────────────────────────────────────────────────────────────────

// Estos tests no pueden ejecutar certbot real, pero verifican el
// comportamiento defensivo cuando el path no existe.

func TestDetectCertbotVersion_NonExistentBinaryReturnsEmpty(t *testing.T) {
	got := detectCertbotVersion("/nonexistent/path/certbot")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
