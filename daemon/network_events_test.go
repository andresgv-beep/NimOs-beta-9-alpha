// network_events_test.go — Tests del EventEmitter.
//
// Cubre:
//   - Emit insertion (fila nueva).
//   - Dedupe runtime (incrementa occurrences).
//   - Rate limit (drop después de N/min).
//   - Validación básica de input.
//   - List queries (since, by category, by operation).
//   - Retention por nivel.
//   - Aggregation nocturna.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newTestEmitter(t *testing.T) (*EventEmitter, *FakeClock, *sqlConn, func()) {
	t.Helper()
	c, cleanup := setupNetworkDB(t)
	clock := NewFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	em := NewEventEmitter(c.db, clock, DefaultEventEmitterConfig())
	return em, clock, c, cleanup
}

// ═════════════════════════════════════════════════════════════════════════════
// Emit
// ═════════════════════════════════════════════════════════════════════════════

func TestEmit_InsertsRowFirstTime(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	in := EventInput{
		Category: CategoryDdns,
		Event:    "update_started",
		Level:    EventLevelInfo,
		Message:  "DDNS update beginning for nimosbarraca",
	}
	emitted, err := em.Emit(context.Background(), in)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !emitted {
		t.Error("first emit: emitted=false, want true")
	}

	n, _ := em.CountEvents(context.Background())
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestEmit_RejectsEmptyFields(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	cases := []EventInput{
		{Event: "x", Level: EventLevelInfo, Message: "m"},             // no category
		{Category: CategoryDdns, Level: EventLevelInfo, Message: "m"}, // no event
		{Category: CategoryDdns, Event: "x", Message: "m"},            // no level
		{Category: CategoryDdns, Event: "x", Level: EventLevelInfo},   // no message
	}
	for i, in := range cases {
		_, err := em.Emit(context.Background(), in)
		if err == nil {
			t.Errorf("case %d: expected error for missing field, got nil", i)
		}
	}
}

func TestEmit_DedupeIncrementsOccurrences(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	target := "ddns-abc"
	in := EventInput{
		Category: CategoryDdns,
		Event:    "update_no_change",
		TargetID: &target,
		Level:    EventLevelInfo,
		Message:  "DDNS IP unchanged",
	}
	emitted, err := em.Emit(context.Background(), in)
	if err != nil || !emitted {
		t.Fatalf("first emit: emitted=%v, err=%v", emitted, err)
	}

	// Mismo evento → debe dedupe.
	for i := 0; i < 3; i++ {
		emitted, err := em.Emit(context.Background(), in)
		if err != nil {
			t.Fatalf("emit #%d: %v", i+2, err)
		}
		if emitted {
			t.Errorf("emit #%d: emitted=true, want false (dedupe)", i+2)
		}
	}

	// Solo 1 fila pero occurrences=4
	n, _ := em.CountEvents(context.Background())
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	events, _ := em.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	if len(events) != 1 || events[0].Occurrences != 4 {
		t.Errorf("occurrences = %d, want 4", events[0].Occurrences)
	}
}

func TestEmit_DedupeRespectsTargetID(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	t1 := "ddns-a"
	t2 := "ddns-b"
	in1 := EventInput{Category: CategoryDdns, Event: "x", TargetID: &t1, Level: EventLevelInfo, Message: "m"}
	in2 := EventInput{Category: CategoryDdns, Event: "x", TargetID: &t2, Level: EventLevelInfo, Message: "m"}

	em.Emit(context.Background(), in1)
	em.Emit(context.Background(), in2)
	em.Emit(context.Background(), in1)
	em.Emit(context.Background(), in2)

	n, _ := em.CountEvents(context.Background())
	if n != 2 {
		t.Errorf("count = %d, want 2 (different targets, no dedupe across)", n)
	}
}

func TestEmit_DedupeNullTargetTreatedAsSameKey(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	in := EventInput{Category: CategoryObserver, Event: "scan_started", Level: EventLevelDebug, Message: "scanning"}
	em.Emit(context.Background(), in)
	em.Emit(context.Background(), in)
	em.Emit(context.Background(), in)

	n, _ := em.CountEvents(context.Background())
	if n != 1 {
		t.Errorf("count = %d, want 1 (target NULL dedupe-able)", n)
	}
}

func TestEmit_DedupeExpiresAfterWindow(t *testing.T) {
	em, clock, _, cleanup := newTestEmitter(t)
	defer cleanup()

	in := EventInput{Category: CategoryDdns, Event: "x", Level: EventLevelInfo, Message: "m"}
	em.Emit(context.Background(), in)

	// Avanzar más allá de la ventana de dedupe (5 min).
	clock.Advance(6 * time.Minute)

	emitted, _ := em.Emit(context.Background(), in)
	if !emitted {
		t.Error("after dedupe window: emitted=false, want true (new row)")
	}

	n, _ := em.CountEvents(context.Background())
	if n != 2 {
		t.Errorf("count = %d, want 2 (one per window)", n)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Rate limit
// ═════════════════════════════════════════════════════════════════════════════

func TestEmit_RateLimitDropsAfterThreshold(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	// Default rate limit = 10/min/category. Variar event para evitar dedupe.
	for i := 0; i < 10; i++ {
		in := EventInput{
			Category: CategoryDdns,
			Event:    "evt_" + string(rune('a'+i)),
			Level:    EventLevelInfo,
			Message:  "m",
		}
		_, err := em.Emit(context.Background(), in)
		if err != nil {
			t.Fatalf("emit #%d: %v", i, err)
		}
	}

	// El emit #11 debe ser rate-limited.
	in := EventInput{Category: CategoryDdns, Event: "evt_overflow", Level: EventLevelInfo, Message: "m"}
	emitted, err := em.Emit(context.Background(), in)
	if !errors.Is(err, ErrEventRateLimited) {
		t.Errorf("emit #11: err = %v, want ErrEventRateLimited", err)
	}
	if emitted {
		t.Error("emit #11: emitted=true, want false")
	}

	if em.DropsForCategory(CategoryDdns) != 1 {
		t.Errorf("drops = %d, want 1", em.DropsForCategory(CategoryDdns))
	}
}

func TestEmit_RateLimitResetsAfterWindow(t *testing.T) {
	em, clock, _, cleanup := newTestEmitter(t)
	defer cleanup()

	// Saturar
	for i := 0; i < 10; i++ {
		em.Emit(context.Background(), EventInput{
			Category: CategoryDdns, Event: "evt_" + string(rune('a'+i)),
			Level: EventLevelInfo, Message: "m",
		})
	}
	// Drop esperado
	in := EventInput{Category: CategoryDdns, Event: "drop_me", Level: EventLevelInfo, Message: "m"}
	_, err := em.Emit(context.Background(), in)
	if !errors.Is(err, ErrEventRateLimited) {
		t.Fatal("expected rate-limit on 11th")
	}

	// Avanzar más allá de la ventana
	clock.Advance(2 * time.Minute)

	in2 := EventInput{Category: CategoryDdns, Event: "after_window", Level: EventLevelInfo, Message: "m"}
	emitted, err := em.Emit(context.Background(), in2)
	if err != nil {
		t.Errorf("after window: err = %v, want nil", err)
	}
	if !emitted {
		t.Error("after window: emitted=false, want true")
	}
}

func TestEmit_RateLimitPerCategoryIndependent(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	// Saturar DDNS
	for i := 0; i < 10; i++ {
		em.Emit(context.Background(), EventInput{
			Category: CategoryDdns, Event: "evt_" + string(rune('a'+i)),
			Level: EventLevelInfo, Message: "m",
		})
	}
	// Otra category debe estar libre
	emitted, err := em.Emit(context.Background(), EventInput{
		Category: CategoryCert, Event: "issue", Level: EventLevelInfo, Message: "m",
	})
	if err != nil {
		t.Errorf("cert: err = %v", err)
	}
	if !emitted {
		t.Error("cert: emitted=false, want true (independent category)")
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Queries
// ═════════════════════════════════════════════════════════════════════════════

func TestList_ByCategory(t *testing.T) {
	em, _, _, cleanup := newTestEmitter(t)
	defer cleanup()

	em.Emit(context.Background(), EventInput{Category: CategoryDdns, Event: "x", Level: EventLevelInfo, Message: "m"})
	em.Emit(context.Background(), EventInput{Category: CategoryCert, Event: "x", Level: EventLevelInfo, Message: "m"})
	em.Emit(context.Background(), EventInput{Category: CategoryDdns, Event: "y", Level: EventLevelInfo, Message: "m"})

	ddns, err := em.ListEventsByCategory(context.Background(), CategoryDdns, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ddns) != 2 {
		t.Errorf("ddns count = %d, want 2", len(ddns))
	}
}

func TestList_ByOperation(t *testing.T) {
	em, _, c, cleanup := newTestEmitter(t)
	defer cleanup()

	// Crear una operation que sirva como FK
	opID := "op-123"
	_, err := c.db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at)
		VALUES (?, 'x', 'completed', 'user:admin', '2026-05-21T12:00:00Z')
	`, opID)
	if err != nil {
		t.Fatal(err)
	}

	em.Emit(context.Background(), EventInput{
		OperationID: &opID, Category: CategoryDdns, Event: "step1",
		Level: EventLevelInfo, Message: "m",
	})
	em.Emit(context.Background(), EventInput{
		OperationID: &opID, Category: CategoryDdns, Event: "step2",
		Level: EventLevelInfo, Message: "m",
	})
	em.Emit(context.Background(), EventInput{
		Category: CategoryDdns, Event: "unrelated",
		Level: EventLevelInfo, Message: "m",
	})

	got, err := em.ListEventsByOperation(context.Background(), opID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("op events = %d, want 2", len(got))
	}
}

func TestList_Since(t *testing.T) {
	em, clock, _, cleanup := newTestEmitter(t)
	defer cleanup()

	t0 := clock.Now().UTC()
	em.Emit(context.Background(), EventInput{Category: CategoryDdns, Event: "early", Level: EventLevelInfo, Message: "m"})

	clock.Advance(10 * time.Minute)
	em.Emit(context.Background(), EventInput{Category: CategoryDdns, Event: "later", Level: EventLevelInfo, Message: "m"})

	got, err := em.ListEventsSince(context.Background(), t0.Add(5*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event != "later" {
		t.Errorf("since = %v, want only [later]", eventEvents(got))
	}
}

func eventEvents(es []*NetworkEvent) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Event
	}
	return out
}

// ═════════════════════════════════════════════════════════════════════════════
// Retention por nivel
// ═════════════════════════════════════════════════════════════════════════════

// seedEventAt inserta un evento con timestamp exacto, bypassando Emit.
func seedEventAt(t *testing.T, db *sql.DB, id, level, category, event string, at time.Time) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at)
		VALUES (?, ?, ?, ?, ?, 'm', ?)
	`, id, at.UTC().Format(time.RFC3339), category, event, level, at.UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seedEventAt: %v", err)
	}
}

func TestPruneByRetention_AppliesPerLevel(t *testing.T) {
	em, clock, c, cleanup := newTestEmitter(t)
	defer cleanup()

	now := clock.Now().UTC()

	// Fabricar eventos en distintos timestamps y niveles
	type seed struct {
		id    string
		level string
		ago   time.Duration
	}
	seeds := []seed{
		// errors: retention 90d
		{"err-recent", "error", 30 * 24 * time.Hour},
		{"err-old", "error", 100 * 24 * time.Hour},

		// warns: retention 30d
		{"warn-recent", "warn", 15 * 24 * time.Hour},
		{"warn-old", "warn", 40 * 24 * time.Hour},

		// info: retention 7d
		{"info-recent", "info", 5 * 24 * time.Hour},
		{"info-old", "info", 10 * 24 * time.Hour},

		// debug: retention 24h
		{"debug-recent", "debug", 12 * time.Hour},
		{"debug-old", "debug", 36 * time.Hour},
	}
	for _, s := range seeds {
		seedEventAt(t, c.db, s.id, s.level, "ddns", "evt", now.Add(-s.ago))
	}

	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = em.PruneEventsByRetention(context.Background(), tx, now)
		return err
	})

	// Esperamos que se borren los 4 "old"
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4 (one *-old per level)", deleted)
	}

	// Verificar supervivientes
	for _, id := range []string{"err-recent", "warn-recent", "info-recent", "debug-recent"} {
		var cnt int
		c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id = ?`, id).Scan(&cnt)
		if cnt != 1 {
			t.Errorf("%s should survive, got count=%d", id, cnt)
		}
	}
	for _, id := range []string{"err-old", "warn-old", "info-old", "debug-old"} {
		var cnt int
		c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id = ?`, id).Scan(&cnt)
		if cnt != 0 {
			t.Errorf("%s should be deleted, got count=%d", id, cnt)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Aggregation nocturna
// ═════════════════════════════════════════════════════════════════════════════

func TestAggregate_CompactsRoutineEvents(t *testing.T) {
	em, clock, c, cleanup := newTestEmitter(t)
	defer cleanup()

	now := clock.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	morning := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 9, 0, 0, 0, time.UTC)
	afternoon := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 15, 0, 0, 0, time.UTC)

	// 4 eventos info de ayer con misma (category, event, target=NULL).
	for i, ts := range []time.Time{morning, morning.Add(time.Hour), afternoon, afternoon.Add(time.Hour)} {
		seedEventAt(t, c.db, "i"+string(rune('a'+i)), "info", "observer", "scan_complete", ts)
	}

	// 1 evento error de ayer — NO se debe agregar (errors+warns intactos).
	seedEventAt(t, c.db, "err-stays", "error", "observer", "scan_failed", morning)

	// 1 evento info de HOY — NO se debe agregar (no es el día especificado).
	seedEventAt(t, c.db, "today-info", "info", "observer", "scan_complete", now)

	var compacted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		compacted, err = em.AggregateRoutineEventsForDay(context.Background(), tx, yesterday)
		return err
	})

	if compacted != 4 {
		t.Errorf("compacted = %d, want 4", compacted)
	}

	// Verificar: el error sobrevive, el de hoy sobrevive, los 4 viejos info se han fundido en 1 sintético.
	var cnt int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id = 'err-stays'`).Scan(&cnt)
	if cnt != 1 {
		t.Error("error event should survive aggregation")
	}
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id = 'today-info'`).Scan(&cnt)
	if cnt != 1 {
		t.Error("today's info event should survive aggregation")
	}

	// Buscar el sintético
	rows, _ := c.db.Query(`SELECT id, message, occurrences, details FROM network_events WHERE message LIKE 'Aggregated %'`)
	var found int
	for rows.Next() {
		var id, msg, details string
		var occ int64
		rows.Scan(&id, &msg, &occ, &details)
		found++
		if !strings.HasPrefix(msg, "Aggregated 4 events") {
			t.Errorf("synthetic message = %q, want 'Aggregated 4 events'", msg)
		}
		if occ != 4 {
			t.Errorf("synthetic occurrences = %d, want 4", occ)
		}
		// details debe ser JSON parseable
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(details), &parsed); err != nil {
			t.Errorf("synthetic details not valid JSON: %v", err)
		}
	}
	rows.Close()
	if found != 1 {
		t.Errorf("synthetic events created = %d, want 1", found)
	}
}

func TestAggregate_IgnoresAlreadyAggregated(t *testing.T) {
	em, clock, c, cleanup := newTestEmitter(t)
	defer cleanup()

	now := clock.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	startOfYesterday := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)

	// Insertar un sintético ya existente del día anterior + 3 originales agregables.
	_, _ = c.db.Exec(`
		INSERT INTO network_events (id, timestamp, category, event, level, message, last_seen_at, occurrences)
		VALUES ('synthetic', ?, 'observer', 'scan_complete', 'info', 'Aggregated 5 events', ?, 5)
	`, startOfYesterday.Format(time.RFC3339), yesterday.Format(time.RFC3339))

	for i := 0; i < 3; i++ {
		seedEventAt(t, c.db, "fresh-"+string(rune('a'+i)), "info", "observer", "scan_complete",
			yesterday.Add(time.Duration(i)*time.Hour))
	}

	var compacted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		compacted, err = em.AggregateRoutineEventsForDay(context.Background(), tx, yesterday)
		return err
	})

	// El sintético existente NO debe contarse. Solo se compactan los 3 frescos.
	if compacted != 3 {
		t.Errorf("compacted = %d, want 3 (synthetic should be skipped)", compacted)
	}

	var existsSynthetic int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE id = 'synthetic'`).Scan(&existsSynthetic)
	if existsSynthetic != 1 {
		t.Error("existing synthetic should survive (not re-aggregated)")
	}
}

func TestAggregate_NoopWhenNothingToCompact(t *testing.T) {
	em, clock, _, cleanup := newTestEmitter(t)
	defer cleanup()

	yesterday := clock.Now().AddDate(0, 0, -1)
	var compacted int64
	withNetTx(t, em.db, func(tx *sql.Tx) error {
		var err error
		compacted, err = em.AggregateRoutineEventsForDay(context.Background(), tx, yesterday)
		return err
	})
	if compacted != 0 {
		t.Errorf("empty: compacted = %d, want 0", compacted)
	}
}
