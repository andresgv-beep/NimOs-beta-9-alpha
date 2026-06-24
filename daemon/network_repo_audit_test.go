// network_repo_audit_test.go — Tests del NetworkRepo (audit tables).
//
// Cubre:
//   - network_observed CRUD: round-trip, generation autoincrement,
//     GetLatest, ListSince, ListByType, Count.
//   - network_observed retention: las 3 reglas de poda (100 top,
//     1/hora último día, 1/día último mes) sobre datasets fabricados.
//   - network_operations CRUD: round-trip, status transitions,
//     triggered_by validation (delegada a DB), FK SET NULL del padre.
//   - network_operations retention: solo borra terminales antiguas.
//
// FakeClock se inyecta para tests deterministas — fundamental para la
// retention donde necesito controlar "now" con precisión.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ═════════════════════════════════════════════════════════════════════════════
// network_observed — CRUD
// ═════════════════════════════════════════════════════════════════════════════

func TestObserved_CreateAndGet_RoundTrip(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	ip := "1.2.3.4"
	synced := true
	o := &NetworkObservedSnapshot{
		SnapshotType:    "periodic",
		SnapshotData:    json.RawMessage(`{"key":"value"}`),
		OverallHealth:   "healthy",
		PublicIP:        &ip,
		DdnsSynced:      &synced,
		DivergenceCount: 0,
		ScanDurationMs:  152,
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateObservedSnapshot(context.Background(), tx, o)
	})

	if o.ID == "" {
		t.Error("ID not generated")
	}
	if o.Generation != 1 {
		t.Errorf("first generation = %d, want 1", o.Generation)
	}
	if !o.SnapshotAt.Equal(clock.Now().UTC()) {
		t.Errorf("SnapshotAt = %v, want %v", o.SnapshotAt, clock.Now().UTC())
	}

	got, err := repo.GetObservedSnapshot(context.Background(), o.ID)
	if err != nil {
		t.Fatalf("GetObservedSnapshot: %v", err)
	}
	if got.OverallHealth != "healthy" {
		t.Errorf("fields not preserved: %+v", got)
	}
	if got.PublicIP == nil || *got.PublicIP != "1.2.3.4" {
		t.Errorf("PublicIP = %v, want 1.2.3.4", got.PublicIP)
	}
	if got.DdnsSynced == nil || !*got.DdnsSynced {
		t.Errorf("DdnsSynced = %v, want true", got.DdnsSynced)
	}
	if string(got.SnapshotData) != `{"key":"value"}` {
		t.Errorf("SnapshotData = %s, want preserved JSON", string(got.SnapshotData))
	}
}

func TestObserved_GenerationAutoincrement(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	// Crear 3 snapshots sin generation explícito → 1, 2, 3.
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		for i := 0; i < 3; i++ {
			o := &NetworkObservedSnapshot{
				SnapshotType:  "periodic",
				OverallHealth: "healthy",
			}
			if err := repo.CreateObservedSnapshot(context.Background(), tx, o); err != nil {
				return err
			}
			if int64(i+1) != o.Generation {
				return fmt.Errorf("snapshot %d: generation = %d, want %d", i, o.Generation, i+1)
			}
		}
		return nil
	})
}

func TestObserved_GetLatest(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	// Vacío → ErrObservedNotFound
	_, err := repo.GetLatestObservedSnapshot(context.Background())
	if !errors.Is(err, ErrObservedNotFound) {
		t.Errorf("empty: err = %v, want ErrObservedNotFound", err)
	}

	// Insertar 3 snapshots con timestamps crecientes
	for i := 0; i < 3; i++ {
		clock.Advance(time.Minute)
		o := &NetworkObservedSnapshot{
			SnapshotType:  "periodic",
			OverallHealth: "healthy",
		}
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateObservedSnapshot(context.Background(), tx, o)
		})
	}

	latest, err := repo.GetLatestObservedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetLatestObservedSnapshot: %v", err)
	}
	if latest.Generation != 3 {
		t.Errorf("latest generation = %d, want 3", latest.Generation)
	}
}

func TestObserved_ListSince(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	t0 := clock.Now().UTC()

	// 3 snapshots en t0, t0+5min, t0+10min
	for i := 0; i < 3; i++ {
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateObservedSnapshot(context.Background(), tx,
				&NetworkObservedSnapshot{SnapshotType: "periodic", OverallHealth: "healthy"})
		})
		clock.Advance(5 * time.Minute)
	}

	// since = t0+3min → debe devolver 2 (los de t0+5min y t0+10min)
	got, err := repo.ListObservedSince(context.Background(), t0.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ListObservedSince: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
	// Order DESC: generations [3, 2]
	if got[0].Generation != 3 || got[1].Generation != 2 {
		t.Errorf("order = [%d,%d], want [3,2]", got[0].Generation, got[1].Generation)
	}
}

func TestObserved_ListByType(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	types := []string{"periodic", "event", "periodic", "boot", "manual", "event"}
	for _, st := range types {
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateObservedSnapshot(context.Background(), tx,
				&NetworkObservedSnapshot{SnapshotType: st, OverallHealth: "healthy"})
		})
	}

	events, err := repo.ListObservedByType(context.Background(), "event", 10)
	if err != nil {
		t.Fatalf("ListObservedByType: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.SnapshotType != "event" {
			t.Errorf("expected only events, got %q", e.SnapshotType)
		}
	}
}

func TestObserved_Count(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	n, _ := repo.CountObservedSnapshots(context.Background())
	if n != 0 {
		t.Errorf("empty: count = %d, want 0", n)
	}

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		for i := 0; i < 5; i++ {
			if err := repo.CreateObservedSnapshot(context.Background(), tx,
				&NetworkObservedSnapshot{SnapshotType: "periodic", OverallHealth: "healthy"}); err != nil {
				return err
			}
		}
		return nil
	})

	n, _ = repo.CountObservedSnapshots(context.Background())
	if n != 5 {
		t.Errorf("after 5 inserts: count = %d, want 5", n)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// network_observed — Retention
// ═════════════════════════════════════════════════════════════════════════════

// seedObservedAt inserta un snapshot con snapshot_at exacto + tipo dado.
// Bypassa el clock para construir datasets temporales arbitrarios.
// gen se usa como parte del ID para garantizar unicidad incluso cuando
// dos timestamps caen en el mismo segundo Unix.
func seedObservedAt(t *testing.T, db *sql.DB, at time.Time, snapshotType string, gen int64) string {
	t.Helper()
	id := fmt.Sprintf("obs-g%d-%d-%s", gen, at.Unix(), snapshotType)
	_, err := db.Exec(`
		INSERT INTO network_observed (
			id, generation, snapshot_at, snapshot_type, snapshot_data,
			overall_health, divergence_count,
			scan_duration_ms
		) VALUES (?, ?, ?, ?, '{}', 'healthy', 0, 0)
	`, id, gen, at.UTC().Format(time.RFC3339), snapshotType)
	if err != nil {
		t.Fatalf("seedObservedAt: %v", err)
	}
	return id
}

func TestObserved_Retention_KeepsTop100(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	// Insertar 150 snapshots espaciados 1 minuto (todos dentro del último día,
	// pero solo nos interesa que la regla "top 100" funcione).
	for i := 0; i < 150; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*time.Minute), "periodic", int64(i+1))
	}

	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	// Hay 150 filas. Las reglas:
	//   - top 100: 100 sobreviven por esta regla (los más recientes, -0..-99min).
	//   - 1/hora último día: hay 3 horas tocadas (-0..-59=h0, -60..-119=h-1,
	//     -120..-149=h-2). De esos 3 representantes, 2 ya están en top-100
	//     (h0, h-1); el de h-2 (snapshot a -120min) está FUERA del top-100
	//     y la regla lo preserva como extra.
	//   - 1/día último mes: 1 día tocado (hoy), ya en top.
	//
	// Resultado esperado: 101 supervivientes (100 + 1 extra de regla 2).
	remaining, _ := repo.CountObservedSnapshots(context.Background())
	if remaining != 101 {
		t.Errorf("remaining = %d, want 101 (100 top + 1 extra from hour-rule)", remaining)
	}
	if deleted != 49 {
		t.Errorf("deleted = %d, want 49", deleted)
	}
}

func TestObserved_Retention_KeepsOnePerHourLastDay(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	// Crear 3 snapshots por hora durante las últimas 6 horas (18 total).
	// Top 100 rule: TODOS sobreviven (solo 18 < 100). Necesito que haya
	// >100 entradas TOTALES con más antiguas que las reglas 2 y 3 pasen.
	//
	// Plan: 200 snapshots dispersos hasta hace 7 días, con muchos por hora
	// y por día. La regla "top 100" filtra los más recientes, las otras
	// reglas preservan representatividad temporal.

	// Insertar:
	//   - 200 snapshots de los últimos 30 minutos (muchos por hora):
	//     todos en la hora actual.
	//   - 50 snapshots dispersos en las últimas 24 horas (uno cada ~30 min)
	//   - 30 snapshots dispersos en los últimos 30 días (uno cada ~24 horas)

	gen := int64(1)
	// Bloque 1: 200 en los últimos 30 min (todos misma hora)
	for i := 0; i < 200; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*9*time.Second), "periodic", gen)
		gen++
	}
	// Bloque 2: 50 dispersos en las últimas 24h
	for i := 0; i < 50; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*30*time.Minute), "periodic", gen)
		gen++
	}

	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	// Verificación: deben sobrevivir
	//   - 100 más recientes (todos del bloque 1, los primeros 100)
	//   - 1 por hora durante el último día: hay ~24-25 horas distintas en bloque 2
	//   - 1 por día durante el último mes: hoy ya está cubierto
	//
	// Lower bound: 100 + algunos del bloque 2. Upper bound: 100 + 25.
	remaining, _ := repo.CountObservedSnapshots(context.Background())
	if remaining < 100 || remaining > 130 {
		t.Errorf("remaining = %d, want between 100 and 130", remaining)
	}
	if deleted < 100 {
		t.Errorf("deleted = %d, want at least 100 (started with 250)", deleted)
	}
}

func TestObserved_Retention_KeepsOnePerDayLastMonth(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	gen := int64(1)

	// Crear 1 snapshot por día durante los últimos 60 días.
	for i := 0; i < 60; i++ {
		seedObservedAt(t, c.db, now.AddDate(0, 0, -i), "periodic", gen)
		gen++
	}

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		_, err := repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	// Reglas aplicadas:
	//   - 100 más recientes: hay 60, sobreviven los 60.
	//   - 1/hora último día: el de hoy.
	//   - 1/día último mes (30 días): los últimos 30 (≤30 días atrás).
	// Como solo hay 60 filas y la regla top-100 las cubre todas, las 60
	// sobreviven.
	remaining, _ := repo.CountObservedSnapshots(context.Background())
	if remaining != 60 {
		t.Errorf("remaining = %d, want 60 (all preserved by top-100 rule)", remaining)
	}

	// Ahora añadimos otros 200 hoy (todos misma fecha) para forzar la
	// poda. Top-100 cogerá los 100 de hoy. Las 60 viejas (que cubren
	// días -0..-59) caen — pero la regla 3 (1/día último mes, 30 días)
	// debe preservar 1 por día de los últimos 30.
	for i := 0; i < 200; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*5*time.Second), "periodic", gen)
		gen++
	}

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		_, err := repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	// Esperamos: 100 (top) + algunos del mes. Hay snapshots para días
	// -0..-59. La regla 3 cubre días -0..-29 (mes ≈ 30 días). De esos,
	// los más recientes (-0 hasta los que estén en top-100) ya están
	// contados. Sobreviven los días viejos de regla 3 que no están en top.
	//
	// Lower bound: 100. Upper bound: 100 + 30 = 130. Days -30..-59
	// (30 días) no deben sobrevivir.
	remaining, _ = repo.CountObservedSnapshots(context.Background())
	if remaining < 100 || remaining > 135 {
		t.Errorf("remaining = %d, want between 100 and 135", remaining)
	}

	// Día -45 NO debe sobrevivir (fuera del último mes).
	day45ago := now.AddDate(0, 0, -45).Format(time.RFC3339)
	var cnt int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_observed WHERE snapshot_at = ?`, day45ago).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("day -45 should be deleted, found %d", cnt)
	}
}

func TestObserved_Retention_EventsPreferredOverPeriodic(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	// Crear 2 snapshots en la misma hora (60 min atrás): uno periodic y uno event.
	// Regla 2 debería preservar el event sobre el periodic.

	// Pero primero necesitamos forzar la poda — agregar muchos snapshots
	// recientes para que los 2 de hace 60min no entren en top-100.

	gen := int64(1)
	// 150 snapshots en los últimos 5 minutos
	for i := 0; i < 150; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*2*time.Second), "periodic", gen)
		gen++
	}
	// 2 snapshots a la misma hora (hace 60 minutos)
	// Ordeno los timestamps para que el periodic sea más reciente
	// y verificar que aún así el event gana.
	hourAgo := now.Add(-60 * time.Minute)
	seedObservedAt(t, c.db, hourAgo.Add(-30*time.Second), "event", gen) // event ANTES en el tiempo
	gen++
	periodicID := seedObservedAt(t, c.db, hourAgo, "periodic", gen) // periodic más reciente
	gen++

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		_, err := repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	// El periodic de hace 60min NO debe sobrevivir (el event en la
	// misma hora le gana por la regla CASE).
	var cnt int
	c.db.QueryRow(`SELECT COUNT(*) FROM network_observed WHERE id = ?`, periodicID).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("periodic at -60min should have been deleted in favor of event in same hour; found %d", cnt)
	}
}

func TestObserved_Retention_BelowThresholdIsNoop(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	for i := 0; i < 50; i++ {
		seedObservedAt(t, c.db, now.Add(-time.Duration(i)*time.Minute), "periodic", int64(i+1))
	}

	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = repo.PruneObservedSnapshots(context.Background(), tx, now)
		return err
	})

	if deleted != 0 {
		t.Errorf("with only 50 rows: deleted = %d, want 0", deleted)
	}
	n, _ := repo.CountObservedSnapshots(context.Background())
	if n != 50 {
		t.Errorf("remaining = %d, want 50", n)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// network_operations — CRUD
// ═════════════════════════════════════════════════════════════════════════════

func TestOps_CreateAndGet_RoundTrip(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	target := "ddns-abc"
	requestID := "req-xyz"
	op := &NetworkOperation{
		Type:        "ddns_update",
		TargetID:    &target,
		TriggeredBy: "reconciler:ddns_updater",
		RequestID:   &requestID,
		Data:        json.RawMessage(`{"ip":"1.2.3.4"}`),
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, op)
	})

	if op.ID == "" {
		t.Error("ID not generated")
	}
	if op.Status != "pending" {
		t.Errorf("default Status = %q, want pending", op.Status)
	}
	if !op.StartedAt.Equal(clock.Now().UTC()) {
		t.Errorf("StartedAt = %v, want %v", op.StartedAt, clock.Now().UTC())
	}

	got, err := repo.GetOperation(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if got.Type != "ddns_update" || got.Status != "pending" {
		t.Errorf("fields: %+v", got)
	}
	if got.TargetID == nil || *got.TargetID != "ddns-abc" {
		t.Errorf("TargetID = %v", got.TargetID)
	}
	if got.RequestID == nil || *got.RequestID != "req-xyz" {
		t.Errorf("RequestID = %v", got.RequestID)
	}
	if string(got.Data) != `{"ip":"1.2.3.4"}` {
		t.Errorf("Data = %s", string(got.Data))
	}
	if got.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want nil (pending)", got.CompletedAt)
	}
}

func TestOps_GetNotFound(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.GetOperation(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("err = %v, want ErrOperationNotFound", err)
	}
}

func TestOps_RejectsInvalidTriggeredBy(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	op := &NetworkOperation{
		Type:        "x",
		TriggeredBy: "cli:something", // CHECK rechaza
	}
	tx, _ := c.db.Begin()
	defer tx.Rollback()

	err := repo.CreateOperation(context.Background(), tx, op)
	if err == nil {
		t.Error("expected CHECK error for invalid triggered_by")
	}
}

func TestOps_StatusTransitions(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	op := &NetworkOperation{
		Type:        "ddns_update",
		TriggeredBy: "reconciler:ddns_updater",
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, op)
	})

	// pending → in_progress: NO setea completed_at
	clock.Advance(time.Second)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateOperationStatus(context.Background(), tx, op.ID, "in_progress", nil, nil)
	})
	got, _ := repo.GetOperation(context.Background(), op.ID)
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
	if got.CompletedAt != nil {
		t.Errorf("in_progress: CompletedAt = %v, want nil", got.CompletedAt)
	}

	// in_progress → completed: SÍ setea completed_at
	clock.Advance(time.Second)
	completedAt := clock.Now().UTC()
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateOperationStatus(context.Background(), tx, op.ID, "completed", nil, nil)
	})
	got, _ = repo.GetOperation(context.Background(), op.ID)
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt = %v, want %v", got.CompletedAt, completedAt)
	}
}

func TestOps_FailedWithErrorMessage(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	op := &NetworkOperation{Type: "cert_issue", TriggeredBy: "reconciler:cert_renewer"}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, op)
	})

	errCode := "DNS_TIMEOUT"
	errMsg := "dns-01 challenge timeout after 30s"
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateOperationStatus(context.Background(), tx, op.ID, "failed", &errCode, &errMsg)
	})

	got, _ := repo.GetOperation(context.Background(), op.ID)
	if got.Status != "failed" || got.Error == nil || *got.Error != errMsg {
		t.Errorf("got %+v", got)
	}
	if got.ErrorCode == nil || *got.ErrorCode != errCode {
		t.Errorf("ErrorCode = %v, want %s", got.ErrorCode, errCode)
	}
}

func TestOps_ParentChildTracing(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	// Operación padre (batch)
	parent := &NetworkOperation{Type: "batch_renew", TriggeredBy: "user:admin"}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, parent)
	})

	// 3 hijos
	for i := 0; i < 3; i++ {
		child := &NetworkOperation{
			Type:            fmt.Sprintf("cert_issue_%d", i),
			TriggeredBy:     "reconciler:cert_renewer",
			ParentOperation: &parent.ID,
		}
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateOperation(context.Background(), tx, child)
		})
	}

	children, err := repo.ListChildOperations(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("ListChildOperations: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("got %d children, want 3", len(children))
	}
	for _, c := range children {
		if c.ParentOperation == nil || *c.ParentOperation != parent.ID {
			t.Errorf("child parent = %v, want %s", c.ParentOperation, parent.ID)
		}
	}
}

func TestOps_FKSetNullOnParentDelete(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	parent := &NetworkOperation{Type: "batch", TriggeredBy: "user:admin"}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, parent)
	})

	child := &NetworkOperation{
		Type:            "step",
		TriggeredBy:     "reconciler:x",
		ParentOperation: &parent.ID,
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateOperation(context.Background(), tx, child)
	})

	// Borrar parent → child.parent_operation = NULL
	_, err := c.db.Exec(`DELETE FROM network_operations WHERE id = ?`, parent.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetOperation(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("child should still exist after parent delete: %v", err)
	}
	if got.ParentOperation != nil {
		t.Errorf("ParentOperation = %v, want nil after FK SET NULL", got.ParentOperation)
	}
}

func TestOps_ListByStatus(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	statuses := []string{"pending", "completed", "pending", "failed", "in_progress"}
	for i, st := range statuses {
		op := &NetworkOperation{
			Type:        fmt.Sprintf("op_%d", i),
			Status:      st,
			TriggeredBy: "reconciler:x",
		}
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateOperation(context.Background(), tx, op)
		})
	}

	pending, err := repo.ListOperationsByStatus(context.Background(), "pending", 0)
	if err != nil {
		t.Fatalf("ListOperationsByStatus: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending count = %d, want 2", len(pending))
	}
}

func TestOps_ListByTriggeredBy(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	triggers := []string{"user:admin", "reconciler:x", "user:admin", "system:scheduler"}
	for i, tr := range triggers {
		op := &NetworkOperation{Type: fmt.Sprintf("op_%d", i), TriggeredBy: tr}
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateOperation(context.Background(), tx, op)
		})
	}

	user, err := repo.ListOperationsByTriggeredBy(context.Background(), "user:admin", 0)
	if err != nil {
		t.Fatalf("ListOperationsByTriggeredBy: %v", err)
	}
	if len(user) != 2 {
		t.Errorf("user:admin count = %d, want 2", len(user))
	}
}

func TestOps_ListByRequest(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	reqA := "req-a"
	reqB := "req-b"
	requests := []*string{&reqA, &reqA, &reqB, nil}
	for i, r := range requests {
		op := &NetworkOperation{
			Type:        fmt.Sprintf("op_%d", i),
			TriggeredBy: "user:admin",
			RequestID:   r,
		}
		withNetTx(t, c.db, func(tx *sql.Tx) error {
			return repo.CreateOperation(context.Background(), tx, op)
		})
	}

	got, err := repo.ListOperationsByRequest(context.Background(), "req-a")
	if err != nil {
		t.Fatalf("ListOperationsByRequest: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("req-a count = %d, want 2", len(got))
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// network_operations — Retention
// ═════════════════════════════════════════════════════════════════════════════

// seedOpAt inserta una operación con timestamps específicos para retention tests.
func seedOpAt(t *testing.T, db *sql.DB, id, status string, startedAt time.Time, completedAt *time.Time) {
	t.Helper()
	var completedArg interface{}
	if completedAt != nil {
		completedArg = completedAt.UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(`
		INSERT INTO network_operations (id, type, status, triggered_by, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, "test", status, "user:admin", startedAt.UTC().Format(time.RFC3339), completedArg)
	if err != nil {
		t.Fatalf("seedOpAt: %v", err)
	}
}

func TestOps_Retention_DeletesOldTerminal(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour) // hace 100 días
	recent := now.Add(-1 * time.Hour)     // hace 1 hora

	// 3 operaciones terminales antiguas (deben borrarse)
	for i, st := range []string{"completed", "failed", "rolled_back"} {
		oldStart := old.Add(-time.Duration(i) * time.Minute)
		seedOpAt(t, c.db, fmt.Sprintf("old-%s", st), st, oldStart, &old)
	}
	// 2 operaciones terminales recientes (NO borrar)
	seedOpAt(t, c.db, "recent-completed", "completed", recent.Add(-time.Minute), &recent)
	seedOpAt(t, c.db, "recent-failed", "failed", recent.Add(-time.Minute), &recent)

	// 2 operaciones pending/in_progress antiguas (NO borrar, no son terminales)
	seedOpAt(t, c.db, "old-pending", "pending", old, nil)
	seedOpAt(t, c.db, "old-inprog", "in_progress", old, nil)

	threshold := now.Add(-30 * 24 * time.Hour) // 30 días

	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = repo.PruneCompletedOperations(context.Background(), tx, threshold)
		return err
	})

	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	// Verificar: los recientes terminales sobreviven; los pending viejos sobreviven
	for _, id := range []string{"recent-completed", "recent-failed", "old-pending", "old-inprog"} {
		_, err := repo.GetOperation(context.Background(), id)
		if err != nil {
			t.Errorf("%s should still exist, err = %v", id, err)
		}
	}
	// Los terminales viejos NO existen
	for _, id := range []string{"old-completed", "old-failed", "old-rolled_back"} {
		_, err := repo.GetOperation(context.Background(), id)
		if !errors.Is(err, ErrOperationNotFound) {
			t.Errorf("%s should be deleted, err = %v", id, err)
		}
	}
}

func TestOps_Retention_Noop(t *testing.T) {
	repo, clock, c, cleanup := newTestRepo(t)
	defer cleanup()

	now := clock.Now().UTC()
	// Solo ops recientes
	for i := 0; i < 5; i++ {
		t0 := now.Add(-time.Duration(i) * time.Hour)
		seedOpAt(t, c.db, fmt.Sprintf("op-%d", i), "completed", t0.Add(-time.Minute), &t0)
	}

	threshold := now.Add(-30 * 24 * time.Hour)
	var deleted int64
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		var err error
		deleted, err = repo.PruneCompletedOperations(context.Background(), tx, threshold)
		return err
	})
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}
