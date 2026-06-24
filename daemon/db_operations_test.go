// db_operations_test.go — Tests del OperationsRepo (Beta 8.1.x · APP-012).
//
// Estos tests usan un archivo SQLite en /tmp con el schema completo aplicado.
// No requieren Docker ni servicios externos · solo archivo en disco.
//
// Ejecutar desde /opt/nimos/daemon:
//
//	go test -run TestOperations -v .
//
// Cobertura:
//   · Create / Get / List
//   · State machine: Pending → Running → Succeeded
//   · State machine: Pending → Running → Failed
//   · State machine: Pending → Failed (sin pasar por running)
//   · Transiciones inválidas (double-mark, resucitar terminal)
//   · UpdateProgress · solo válido en running
//   · DeleteExpired · GC

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

// setupTestOperationsDB construye una BD temporal con el schema aplicado.
// Patrón consistente con setupTestAppsDB en db_apps_test.go.
func setupTestOperationsDB(t *testing.T) (*sql.DB, *OperationsRepo, func()) {
	t.Helper()

	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_operations_test_" + safeName + ".db"
	os.Remove(tmpDB)

	conn, err := sql.Open("sqlite", tmpDB+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := initOperationsSchema(conn); err != nil {
		t.Fatalf("initOperationsSchema: %v", err)
	}

	repo := NewOperationsRepo(conn)
	cleanup := func() {
		conn.Close()
		os.Remove(tmpDB)
	}
	return conn, repo, cleanup
}

// ═══════════════════════════════════════════════════════════════════════
// CRUD básico
// ═══════════════════════════════════════════════════════════════════════

// TestOperationsCreateGet · happy path · create + get devuelve la op.
func TestOperationsCreateGet(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, err := repo.Create(ctx, "docker.install", "andres")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if op.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if !strings.HasPrefix(op.ID, "op_") {
		t.Errorf("expected ID to start with 'op_', got %q", op.ID)
	}
	if op.Status != OpsStatusPending {
		t.Errorf("expected Status=pending, got %q", op.Status)
	}
	if op.Progress != 0 {
		t.Errorf("expected Progress=0, got %d", op.Progress)
	}

	got, err := repo.Get(ctx, op.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %v", err, got)
	}
	if got.Type != "docker.install" {
		t.Errorf("Type roundtrip mismatch: got %q", got.Type)
	}
	if got.CreatedBy != "andres" {
		t.Errorf("CreatedBy mismatch: got %q", got.CreatedBy)
	}
}

// TestOperationsCreate_RequiresType · type vacío es error.
func TestOperationsCreate_RequiresType(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	_, err := repo.Create(ctx, "", "andres")
	if err == nil {
		t.Error("expected error on empty type, got nil")
	}
}

// TestOperationsCreate_RequiresCreatedBy · createdBy vacío es error.
func TestOperationsCreate_RequiresCreatedBy(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	_, err := repo.Create(ctx, "docker.install", "")
	if err == nil {
		t.Error("expected error on empty createdBy, got nil")
	}
}

// TestOperationsGet_NotFound · op inexistente devuelve (nil, nil), no error.
func TestOperationsGet_NotFound(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, err := repo.Get(ctx, "op_999_abcdef01")
	if err != nil {
		t.Errorf("expected nil error on miss, got %v", err)
	}
	if op != nil {
		t.Errorf("expected nil op on miss, got %v", op)
	}
}

// TestOperationsList · filtros funcionan combinables.
func TestOperationsList(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	// 3 ops: 2 de docker.install (alice + bob), 1 de docker.pull (alice).
	op1, _ := repo.Create(ctx, "docker.install", "alice")
	op2, _ := repo.Create(ctx, "docker.install", "bob")
	op3, _ := repo.Create(ctx, "docker.pull", "alice")
	_ = op2
	_ = op3

	// Filtro por type
	got, err := repo.List(ctx, "docker.install", "", "", 0)
	if err != nil {
		t.Fatalf("List by type: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 docker.install ops, got %d", len(got))
	}

	// Filtro por createdBy
	got, _ = repo.List(ctx, "", "", "alice", 0)
	if len(got) != 2 {
		t.Errorf("expected 2 ops by alice, got %d", len(got))
	}

	// Filtro combinado
	got, _ = repo.List(ctx, "docker.install", "", "alice", 0)
	if len(got) != 1 {
		t.Errorf("expected 1 op (install+alice), got %d", len(got))
	}
	if got[0].ID != op1.ID {
		t.Errorf("expected op1, got %q", got[0].ID)
	}

	// Sin filtros · devuelve todas
	got, _ = repo.List(ctx, "", "", "", 0)
	if len(got) != 3 {
		t.Errorf("expected 3 total, got %d", len(got))
	}

	// Limit
	got, _ = repo.List(ctx, "", "", "", 2)
	if len(got) != 2 {
		t.Errorf("limit=2 expected 2, got %d", len(got))
	}
}

// ═══════════════════════════════════════════════════════════════════════
// State machine
// ═══════════════════════════════════════════════════════════════════════

// TestOperationsStateMachine_HappyPath · pending → running → succeeded.
func TestOperationsStateMachine_HappyPath(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")

	// MarkRunning
	if err := repo.MarkRunning(ctx, op.ID); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	got, _ := repo.Get(ctx, op.ID)
	if got.Status != OpsStatusRunning {
		t.Errorf("expected running, got %q", got.Status)
	}
	if got.StartedAt == "" {
		t.Error("expected StartedAt to be set after MarkRunning")
	}

	// UpdateProgress
	if err := repo.UpdateProgress(ctx, op.ID, 50, "Pulling image..."); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ = repo.Get(ctx, op.ID)
	if got.Progress != 50 {
		t.Errorf("expected progress=50, got %d", got.Progress)
	}
	if got.Message != "Pulling image..." {
		t.Errorf("expected message set, got %q", got.Message)
	}

	// MarkSucceeded
	if err := repo.MarkSucceeded(ctx, op.ID, `{"containerId":"abc123"}`); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	got, _ = repo.Get(ctx, op.ID)
	if got.Status != OpsStatusSucceeded {
		t.Errorf("expected succeeded, got %q", got.Status)
	}
	if got.FinishedAt == "" {
		t.Error("expected FinishedAt to be set")
	}
	if got.ExpiresAt == "" {
		t.Error("expected ExpiresAt to be set")
	}
	if got.ResultJSON != `{"containerId":"abc123"}` {
		t.Errorf("ResultJSON roundtrip mismatch: got %q", got.ResultJSON)
	}
}

// TestOperationsStateMachine_PendingToFailed · pending → failed (sin running).
// Caso de uso: la op nunca llegó a empezar (e.g. validation falló).
func TestOperationsStateMachine_PendingToFailed(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	if err := repo.MarkFailed(ctx, op.ID, "Image not found in registry", ""); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, _ := repo.Get(ctx, op.ID)
	if got.Status != OpsStatusFailed {
		t.Errorf("expected failed, got %q", got.Status)
	}
	if got.Error != "Image not found in registry" {
		t.Errorf("Error mismatch: got %q", got.Error)
	}
}

// TestOperationsStateMachine_DoubleSucceed · marcar succeed dos veces es error.
func TestOperationsStateMachine_DoubleSucceed(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	repo.MarkRunning(ctx, op.ID)
	repo.MarkSucceeded(ctx, op.ID, "")

	// Segundo intento debe fallar (op ya está en terminal)
	err := repo.MarkSucceeded(ctx, op.ID, "")
	if err == nil {
		t.Error("expected error on double MarkSucceeded")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("expected error to mention terminal, got %v", err)
	}
}

// TestOperationsStateMachine_RunningTwice · MarkRunning sobre running es error.
func TestOperationsStateMachine_RunningTwice(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	if err := repo.MarkRunning(ctx, op.ID); err != nil {
		t.Fatalf("first MarkRunning: %v", err)
	}
	err := repo.MarkRunning(ctx, op.ID)
	if err == nil {
		t.Error("expected error on double MarkRunning")
	}
}

// TestOperationsStateMachine_RunningNonexistent · MarkRunning sobre op inexistente es error.
func TestOperationsStateMachine_RunningNonexistent(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	err := repo.MarkRunning(ctx, "op_999_deadbeef")
	if err == nil {
		t.Error("expected error on MarkRunning of nonexistent op")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %v", err)
	}
}

// TestOperationsStateMachine_ResurrectTerminal · una vez en succeeded,
// MarkFailed sobre ella es error.
func TestOperationsStateMachine_ResurrectTerminal(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	repo.MarkRunning(ctx, op.ID)
	repo.MarkSucceeded(ctx, op.ID, "")

	err := repo.MarkFailed(ctx, op.ID, "race condition?", "")
	if err == nil {
		t.Error("expected error on MarkFailed of succeeded op")
	}
}

// TestOperationsUpdateProgress_NotRunning · UpdateProgress sobre pending es error.
func TestOperationsUpdateProgress_NotRunning(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	err := repo.UpdateProgress(ctx, op.ID, 50, "")
	if err == nil {
		t.Error("expected error on UpdateProgress for pending op")
	}
}

// TestOperationsUpdateProgress_Clamps · progress se trunca a [0, 100].
func TestOperationsUpdateProgress_Clamps(t *testing.T) {
	_, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	op, _ := repo.Create(ctx, "docker.install", "andres")
	repo.MarkRunning(ctx, op.ID)

	repo.UpdateProgress(ctx, op.ID, -50, "")
	got, _ := repo.Get(ctx, op.ID)
	if got.Progress != 0 {
		t.Errorf("expected clamped to 0, got %d", got.Progress)
	}

	repo.UpdateProgress(ctx, op.ID, 200, "")
	got, _ = repo.Get(ctx, op.ID)
	if got.Progress != 100 {
		t.Errorf("expected clamped to 100, got %d", got.Progress)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Garbage collection
// ═══════════════════════════════════════════════════════════════════════

// TestOperationsDeleteExpired · solo borra ops con expires_at en el pasado.
func TestOperationsDeleteExpired(t *testing.T) {
	conn, repo, cleanup := setupTestOperationsDB(t)
	defer cleanup()
	ctx := context.Background()

	// Op 1: succeeded ahora (expira en +24h)
	op1, _ := repo.Create(ctx, "docker.install", "andres")
	repo.MarkRunning(ctx, op1.ID)
	repo.MarkSucceeded(ctx, op1.ID, "")

	// Op 2: succeeded pero con expires_at manipulado al pasado (simula GC pendiente)
	op2, _ := repo.Create(ctx, "docker.install", "andres")
	repo.MarkRunning(ctx, op2.ID)
	repo.MarkSucceeded(ctx, op2.ID, "")
	pastTime := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	_, _ = conn.Exec(`UPDATE nimos_operations SET expires_at = ? WHERE id = ?`, pastTime, op2.ID)

	// Op 3: pending (sin expires_at)
	op3, _ := repo.Create(ctx, "docker.install", "andres")
	_ = op3

	count, err := repo.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired deleted, got %d", count)
	}

	// op1 sigue presente
	if got, _ := repo.Get(ctx, op1.ID); got == nil {
		t.Error("op1 (succeeded recent) should still exist")
	}
	// op2 borrada
	if got, _ := repo.Get(ctx, op2.ID); got != nil {
		t.Error("op2 (expired) should be deleted")
	}
	// op3 (pending, sin expires_at) sigue presente · DeleteExpired no toca rows con expires_at vacío
	if got, _ := repo.Get(ctx, op3.ID); got == nil {
		t.Error("op3 (pending, no expires_at) should still exist")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// ID generation
// ═══════════════════════════════════════════════════════════════════════

// TestGenerateOperationID_Format · IDs siguen el formato esperado.
func TestGenerateOperationID_Format(t *testing.T) {
	id := generateOperationID()
	if !operationIDRegex.MatchString(id) {
		t.Errorf("generated ID %q does not match expected format", id)
	}
}

// TestGenerateOperationID_Unique · 100 IDs consecutivos son únicos.
// Bajo concurrencia esto podría romperse · si pasa, añadir mutex en generador.
func TestGenerateOperationID_Unique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := generateOperationID()
		if seen[id] {
			t.Errorf("duplicate ID generated: %q", id)
		}
		seen[id] = true
	}
}

// ═══════════════════════════════════════════════════════════════════════
// ToMap output
// ═══════════════════════════════════════════════════════════════════════

// TestDBOperationToMap_PendingOmitsResult · una op pending no incluye
// resultRaw ni error en la salida.
func TestDBOperationToMap_PendingOmitsResult(t *testing.T) {
	op := &DBOperation{
		ID:     "op_1_abc12345",
		Type:   "docker.install",
		Status: OpsStatusPending,
	}
	m := op.ToMap()
	if _, ok := m["resultRaw"]; ok {
		t.Error("pending should NOT include resultRaw")
	}
	if _, ok := m["error"]; ok {
		t.Error("pending should NOT include error")
	}
}

// TestDBOperationToMap_FailedIncludesError · op failed incluye error.
func TestDBOperationToMap_FailedIncludesError(t *testing.T) {
	op := &DBOperation{
		ID:     "op_1_abc12345",
		Type:   "docker.install",
		Status: OpsStatusFailed,
		Error:  "Image pull timeout",
	}
	m := op.ToMap()
	if errMsg, ok := m["error"].(string); !ok || errMsg != "Image pull timeout" {
		t.Errorf("failed op should include error: got %v", m["error"])
	}
}

// TestDBOperationToMap_SucceededIncludesResult · op succeeded con result
// devuelve resultRaw.
func TestDBOperationToMap_SucceededIncludesResult(t *testing.T) {
	op := &DBOperation{
		ID:         "op_1_abc12345",
		Type:       "docker.install",
		Status:     OpsStatusSucceeded,
		ResultJSON: `{"containerId":"abc"}`,
	}
	m := op.ToMap()
	if raw, ok := m["resultRaw"].(string); !ok || raw != `{"containerId":"abc"}` {
		t.Errorf("succeeded should include resultRaw, got %v", m["resultRaw"])
	}
}
