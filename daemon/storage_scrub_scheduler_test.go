// storage_scrub_scheduler_test.go — scrub programado.
//
// Antes nada creaba filas en scrub_schedule y el scheduler exigía el minuto
// exacto: los scrubs programados no corrían nunca, y una máquina apagada a
// esa hora se los saltaba. Estos tests fijan el comportamiento nuevo.

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type scrubRow struct {
	enabled          int
	lastRun, nextRun sql.NullString
}

func readScrubRow(t *testing.T, conn *sql.DB, pool string) (scrubRow, bool) {
	t.Helper()
	var r scrubRow
	err := conn.QueryRow(`SELECT enabled, last_run, next_run FROM scrub_schedule WHERE pool_name = ?`, pool).
		Scan(&r.enabled, &r.lastRun, &r.nextRun)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatalf("readScrubRow: %v", err)
	}
	return r, true
}

func setScrubNextRun(t *testing.T, conn *sql.DB, pool string, next time.Time) {
	t.Helper()
	if _, err := conn.Exec(`INSERT OR REPLACE INTO scrub_schedule
		(pool_name, enabled, frequency, day_of_week, day_of_month, hour, minute, next_run)
		VALUES (?, 1, 'monthly', 0, 1, 3, 0, ?)`, pool, next.Format(time.RFC3339)); err != nil {
		t.Fatalf("setScrubNextRun: %v", err)
	}
}

// stubScheduledScrub sustituye el lanzamiento real y cuenta las llamadas.
func stubScheduledScrub(t *testing.T, ok bool) *[]string {
	t.Helper()
	var calls []string
	orig := runScheduledScrubFn
	runScheduledScrubFn = func(pool string) map[string]interface{} {
		calls = append(calls, pool)
		if ok {
			return map[string]interface{}{"ok": true}
		}
		return map[string]interface{}{"ok": false, "error": "pool degradado"}
	}
	t.Cleanup(func() {
		runScheduledScrubFn = orig
		scrubFailMu.Lock()
		scrubFailNotified = map[string]bool{}
		scrubFailMu.Unlock()
	})
	return &calls
}

func TestScrubScheduler_CreatesDefaultScheduleWithoutRunning(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	calls := stubScheduledScrub(t, true)
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local)

	runScheduledScrubIfDue(context.Background(), service.db, "data", now)

	row, found := readScrubRow(t, service.db, "data")
	if !found || row.enabled != 1 {
		t.Fatalf("debía crear una programación activa: found=%v enabled=%d", found, row.enabled)
	}
	if want := "2026-12-01T03:00:00"; row.nextRun.String[:19] != want {
		t.Errorf("next_run = %s, want %s (mensual, día 1, 03:00)", row.nextRun.String, want)
	}
	if len(*calls) != 0 {
		t.Errorf("un pool recién programado no debe escanearse ya: %v", *calls)
	}
}

func TestScrubScheduler_CatchesUpMissedRun(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	calls := stubScheduledScrub(t, true)
	// Tocaba el día 1 a las 03:00 pero la máquina estaba apagada: hoy es día 5.
	setScrubNextRun(t, service.db, "data", time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local))
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local)

	runScheduledScrubIfDue(context.Background(), service.db, "data", now)

	if len(*calls) != 1 {
		t.Fatalf("el scrub perdido debía correr ahora: llamadas=%v", *calls)
	}
	row, _ := readScrubRow(t, service.db, "data")
	if row.lastRun.String != now.Format(time.RFC3339) {
		t.Errorf("last_run = %q, want %q", row.lastRun.String, now.Format(time.RFC3339))
	}
	if row.nextRun.String[:19] != "2026-12-01T03:00:00" {
		t.Errorf("next_run = %s, want el próximo día 1", row.nextRun.String)
	}
}

func TestScrubScheduler_NotDueDoesNothing(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	calls := stubScheduledScrub(t, true)
	setScrubNextRun(t, service.db, "data", time.Date(2026, 12, 1, 3, 0, 0, 0, time.Local))

	runScheduledScrubIfDue(context.Background(), service.db, "data", time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local))

	if len(*calls) != 0 {
		t.Errorf("no tocaba todavía: %v", *calls)
	}
}

func TestScrubScheduler_FailureRetriesHourlyAndNotifiesOnce(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	calls := stubScheduledScrub(t, false)
	setScrubNextRun(t, service.db, "data", time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local))
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local)
	ctx := context.Background()

	runScheduledScrubIfDue(ctx, service.db, "data", now)

	row, _ := readScrubRow(t, service.db, "data")
	if row.lastRun.Valid {
		t.Errorf("un fallo no debe marcar last_run: %v", row.lastRun.String)
	}
	if want := now.Add(scrubRetryDelay).Format(time.RFC3339); row.nextRun.String != want {
		t.Errorf("next_run = %s, want reintento %s", row.nextRun.String, want)
	}
	if !scrubFailNotified["data"] {
		t.Error("la primera vez de la racha debe notificar")
	}

	// Al minuto siguiente NO reintenta (espera la hora): sin bucle de avisos.
	runScheduledScrubIfDue(ctx, service.db, "data", now.Add(time.Minute))
	if len(*calls) != 1 {
		t.Errorf("no debe reintentar antes de scrubRetryDelay: llamadas=%d", len(*calls))
	}

	// Una hora después reintenta; ahora funciona y la racha se cierra.
	runScheduledScrubFn = func(string) map[string]interface{} { return map[string]interface{}{"ok": true} }
	runScheduledScrubIfDue(ctx, service.db, "data", now.Add(scrubRetryDelay))
	if scrubFailNotified["data"] {
		t.Error("un scrub bueno debe cerrar la racha de fallos")
	}
}

func TestScrubScheduler_DisabledScheduleIsRespected(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	calls := stubScheduledScrub(t, true)
	setScrubNextRun(t, service.db, "data", time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local))
	if _, err := service.db.Exec(`UPDATE scrub_schedule SET enabled = 0 WHERE pool_name = 'data'`); err != nil {
		t.Fatal(err)
	}

	runScheduledScrubIfDue(context.Background(), service.db, "data", time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local))

	if len(*calls) != 0 {
		t.Errorf("una programación desactivada a mano no debe correr: %v", *calls)
	}
}

func TestScrubScheduler_OnlyManagedMountedPools(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	orig := storageService
	storageService = service
	defer func() { storageService = orig }()
	origMounted := isPoolMounted
	isPoolMounted = func(mp string) bool { return mp == "/nimos/pools/data" || mp == "/nimos/pools/ajeno" }
	defer func() { isPoolMounted = origMounted }()
	calls := stubScheduledScrub(t, true)

	insertTestPool(t, service, "data", ControlStateManaged)                  // managed + montado
	insertTestPool(t, service, "apagado", ControlStateManaged)               // managed, sin montar
	insertTestPool(t, service, "ajeno", ControlStateObserved)                // montado pero no es de NimOS
	setScrubNextRun(t, service.db, "renombrado", time.Now().Add(-time.Hour)) // fila huérfana

	checkAndRunScheduledScrubs()

	// La fila huérfana (pool que ya no existe) vencida no debe dispararse.
	if len(*calls) != 0 {
		t.Errorf("no debía lanzar ningún scrub: %v", *calls)
	}
	if _, ok := readScrubRow(t, service.db, "data"); !ok {
		t.Error("el pool managed y montado debía recibir programación")
	}
	for _, p := range []string{"apagado", "ajeno"} {
		if _, ok := readScrubRow(t, service.db, p); ok {
			t.Errorf("%s no debía recibir programación", p)
		}
	}
}

func TestNextScrubRun(t *testing.T) {
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.Local) // jueves
	cases := []struct {
		freq          string
		h, m, dow, dm int
		want          string
	}{
		{"daily", 3, 0, 0, 0, "2026-11-06T03:00:00"},
		{"daily", 20, 30, 0, 0, "2026-11-05T20:30:00"},
		{"weekly", 3, 0, 0, 0, "2026-11-08T03:00:00"}, // domingo
		{"monthly", 3, 0, 0, 1, "2026-12-01T03:00:00"},
		{"monthly", 3, 0, 0, 20, "2026-11-20T03:00:00"},
	}
	for _, c := range cases {
		got, ok := nextScrubRun(now, c.freq, c.h, c.m, c.dow, c.dm)
		if !ok || got.Format(time.RFC3339)[:19] != c.want {
			t.Errorf("nextScrubRun(%s %02d:%02d dow=%d dom=%d) = %v, want %s", c.freq, c.h, c.m, c.dow, c.dm, got, c.want)
		}
	}
	if _, ok := nextScrubRun(now, "nunca", 3, 0, 0, 1); ok {
		t.Error("frecuencia desconocida debe devolver ok=false")
	}
}
