// network_repo_audit.go — Repo de las tablas auditables del módulo network.
//
// Cubre:
//   - network_observed:    snapshots históricos del observer.
//   - network_operations:  operaciones auditables con tracing.
//
// La 6ª tabla, network_events, vive en network_events.go porque tiene
// lógica de emit con dedupe + rate limit + aggregation que no es CRUD
// puro.
//
// Estas tablas NO tienen triple generation:
//
//   - network_observed tiene una sola generation (counter monotónico que
//     incrementa con cada nuevo snapshot).
//   - network_operations no tiene generation (cada op es inmutable salvo
//     transiciones de status).
//
// Retention: ambas tablas crecen indefinidamente si no se podan. Las
// funciones Prune* aplican las políticas documentadas en NIMOS_DISCIPLINE
// (network_observed: 100 últimos + 1/h último día + 1/d último mes;
// network_operations: borrar completadas con completed_at < N días).
//
// Llamadas por el cron del scheduler (typically 03:00).

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrObservedNotFound  = errors.New("network observed snapshot not found")
	ErrOperationNotFound = errors.New("network operation not found")
)

// ═════════════════════════════════════════════════════════════════════════════
// network_observed
// ═════════════════════════════════════════════════════════════════════════════

// NetworkObservedSnapshot representa una fila de network_observed.
//
// SnapshotData es el JSON completo de la observación (todas las
// entidades, sus estados, etc). Las columnas adicionales (OverallHealth,
// PublicIP, etc.) son métricas pre-extraídas que permiten queries sin
// parsear el JSON.
type NetworkObservedSnapshot struct {
	ID              string          `json:"id"`
	Generation      int64           `json:"generation"`
	SnapshotAt      time.Time       `json:"snapshot_at"`
	SnapshotType    string          `json:"snapshot_type"`
	SnapshotData    json.RawMessage `json:"snapshot_data"`
	OverallHealth   string          `json:"overall_health"`
	PublicIP        *string         `json:"public_ip,omitempty"`
	DdnsSynced      *bool           `json:"ddns_synced,omitempty"`
	DivergenceCount int             `json:"divergence_count"`
	ScanDurationMs  int64           `json:"scan_duration_ms"`
}

const observedColumns = `
	id, generation, snapshot_at, snapshot_type, snapshot_data,
	overall_health, public_ip, ddns_synced,
	divergence_count,
	scan_duration_ms
`

func scanObserved(rs rowScanner) (*NetworkObservedSnapshot, error) {
	var (
		o             NetworkObservedSnapshot
		snapshotAtStr string
		snapshotData  []byte
		publicIP      sql.NullString
		ddnsSynced    sql.NullInt64
	)
	err := rs.Scan(
		&o.ID, &o.Generation, &snapshotAtStr, &o.SnapshotType, &snapshotData,
		&o.OverallHealth, &publicIP, &ddnsSynced,
		&o.DivergenceCount,
		&o.ScanDurationMs,
	)
	if err != nil {
		return nil, err
	}
	o.SnapshotAt = parseTime(snapshotAtStr)
	o.SnapshotData = json.RawMessage(snapshotData)
	o.PublicIP = ptrFromNullString(publicIP)
	if ddnsSynced.Valid {
		b := ddnsSynced.Int64 != 0
		o.DdnsSynced = &b
	}
	return &o, nil
}

// GetNextObservedGeneration calcula max(generation)+1. El observer debe
// llamarlo dentro de la MISMA tx que CreateObservedSnapshot para evitar
// races (el observer es singleton en NimOS, así que en práctica no hay
// race; el patrón es por disciplina).
//
// Devuelve 1 si la tabla está vacía (el schema exige generation > 0).
func (r *NetworkRepo) GetNextObservedGeneration(ctx context.Context, tx *sql.Tx) (int64, error) {
	var maxGen sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT MAX(generation) FROM network_observed`).Scan(&maxGen)
	if err != nil {
		return 0, fmt.Errorf("GetNextObservedGeneration: %w", err)
	}
	if !maxGen.Valid {
		return 1, nil
	}
	return maxGen.Int64 + 1, nil
}

// CreateObservedSnapshot inserta un snapshot. Si ID está vacío, se
// genera un UUID. Si SnapshotAt es zero, se usa clock.Now(). Si
// Generation es 0, se calcula con GetNextObservedGeneration.
func (r *NetworkRepo) CreateObservedSnapshot(ctx context.Context, tx *sql.Tx, o *NetworkObservedSnapshot) error {
	if o.ID == "" {
		o.ID = uuid.New().String()
	}
	if o.SnapshotAt.IsZero() {
		o.SnapshotAt = r.clock.Now().UTC()
	}
	if o.SnapshotType == "" {
		o.SnapshotType = "periodic"
	}
	if o.Generation == 0 {
		gen, err := r.GetNextObservedGeneration(ctx, tx)
		if err != nil {
			return err
		}
		o.Generation = gen
	}
	if len(o.SnapshotData) == 0 {
		o.SnapshotData = json.RawMessage("{}")
	}
	var ddnsSyncedArg interface{}
	if o.DdnsSynced != nil {
		ddnsSyncedArg = intFromBool(*o.DdnsSynced)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_observed (
			id, generation, snapshot_at, snapshot_type, snapshot_data,
			overall_health, public_ip, ddns_synced,
			divergence_count,
			scan_duration_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		o.ID, o.Generation, o.SnapshotAt.UTC().Format(time.RFC3339), o.SnapshotType,
		[]byte(o.SnapshotData),
		o.OverallHealth, nullStringPtr(o.PublicIP), ddnsSyncedArg,
		o.DivergenceCount,
		o.ScanDurationMs,
	)
	if err != nil {
		return fmt.Errorf("CreateObservedSnapshot: %w", err)
	}
	return nil
}

// GetObservedSnapshot lee un snapshot por id.
func (r *NetworkRepo) GetObservedSnapshot(ctx context.Context, id string) (*NetworkObservedSnapshot, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+observedColumns+` FROM network_observed WHERE id = ?`, id)
	o, err := scanObserved(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObservedNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetObservedSnapshot: %w", err)
	}
	return o, nil
}

// GetLatestObservedSnapshot devuelve el snapshot más reciente, o
// ErrObservedNotFound si la tabla está vacía.
func (r *NetworkRepo) GetLatestObservedSnapshot(ctx context.Context) (*NetworkObservedSnapshot, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+observedColumns+`
		FROM network_observed
		ORDER BY snapshot_at DESC
		LIMIT 1
	`)
	o, err := scanObserved(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObservedNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetLatestObservedSnapshot: %w", err)
	}
	return o, nil
}

// ListObservedSince devuelve los snapshots con snapshot_at >= since,
// ordenados de más reciente a más antiguo.
func (r *NetworkRepo) ListObservedSince(ctx context.Context, since time.Time) ([]*NetworkObservedSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+observedColumns+`
		FROM network_observed
		WHERE snapshot_at >= ?
		ORDER BY snapshot_at DESC
	`, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("ListObservedSince: %w", err)
	}
	defer rows.Close()
	return collectObserved(rows)
}

// ListObservedByType filtra por snapshot_type. Útil para el observer
// queriendo solo eventos (drift detection) o solo periodic.
func (r *NetworkRepo) ListObservedByType(ctx context.Context, snapshotType string, limit int) ([]*NetworkObservedSnapshot, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+observedColumns+`
		FROM network_observed
		WHERE snapshot_type = ?
		ORDER BY snapshot_at DESC
		LIMIT ?
	`, snapshotType, limit)
	if err != nil {
		return nil, fmt.Errorf("ListObservedByType: %w", err)
	}
	defer rows.Close()
	return collectObserved(rows)
}

// CountObservedSnapshots devuelve el total. Útil para el cron decidir
// si la prune merece la pena.
func (r *NetworkRepo) CountObservedSnapshots(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_observed`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountObservedSnapshots: %w", err)
	}
	return n, nil
}

func collectObserved(rows *sql.Rows) ([]*NetworkObservedSnapshot, error) {
	var out []*NetworkObservedSnapshot
	for rows.Next() {
		o, err := scanObserved(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// network_observed — Retention
// ─────────────────────────────────────────────────────────────────────────────

// PruneObservedSnapshots aplica la política de retención documentada
// en NIMOS_DISCIPLINE §2 v2:
//
//   - 100 snapshots más recientes (cualquier tipo).
//   - 1 por hora durante el último día (prefiere 'event' al deduplicar).
//   - 1 por día durante el último mes.
//   - Borrar todo lo demás.
//
// `now` se inyecta para tests deterministas (típicamente clock.Now()).
//
// Devuelve cuántas filas se borraron.
//
// La query usa una CTE con 3 ramas UNION para construir el set de IDs
// a preservar, y un DELETE WHERE id NOT IN (keep). Es un único
// statement atómico — si una rama falla, no se borra nada.
func (r *NetworkRepo) PruneObservedSnapshots(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	nowStr := now.UTC().Format(time.RFC3339)

	// La CTE devuelve los IDs que sobreviven. El DELETE borra todo lo
	// demás. Ver comentarios SQL inline.
	//
	// NOTA: SQLite no permite ORDER BY/LIMIT directos en una rama de
	// UNION — cada SELECT con ORDER BY debe envolverse en una subquery.
	res, err := tx.ExecContext(ctx, `
		WITH keep AS (
			-- Regla 1: los 100 más recientes (cualquier type)
			SELECT id FROM (
				SELECT id FROM network_observed
				ORDER BY snapshot_at DESC
				LIMIT 100
			)

			UNION

			-- Regla 2: 1 por hora durante el último día.
			-- Prefiere 'event' (raros, indican cambios reales) sobre
			-- 'periodic' (regulares, redundantes en una hora).
			SELECT id FROM (
				SELECT id,
				       ROW_NUMBER() OVER (
				           PARTITION BY strftime('%Y-%m-%d %H', snapshot_at)
				           ORDER BY
				               CASE snapshot_type WHEN 'event' THEN 0 ELSE 1 END,
				               snapshot_at DESC
				       ) AS rn
				FROM network_observed
				WHERE snapshot_at >= datetime(?, '-1 day')
			) WHERE rn = 1

			UNION

			-- Regla 3: 1 por día durante el último mes.
			SELECT id FROM (
				SELECT id,
				       ROW_NUMBER() OVER (
				           PARTITION BY date(snapshot_at)
				           ORDER BY snapshot_at DESC
				       ) AS rn
				FROM network_observed
				WHERE snapshot_at >= datetime(?, '-30 days')
			) WHERE rn = 1
		)
		DELETE FROM network_observed WHERE id NOT IN (SELECT id FROM keep)
	`, nowStr, nowStr)
	if err != nil {
		return 0, fmt.Errorf("PruneObservedSnapshots: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ═════════════════════════════════════════════════════════════════════════════
// network_operations
// ═════════════════════════════════════════════════════════════════════════════

// NetworkOperation representa una operación auditable del módulo network.
//
// Status válidos (CHECK del schema): 'pending', 'in_progress',
// 'completed', 'failed', 'rolled_back'.
//
// TriggeredBy debe matchear uno de los patrones del CHECK: 'user:<n>',
// 'reconciler:<n>', 'system:boot' o 'system:scheduler'. Validación a
// nivel DB.
type NetworkOperation struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	TargetID        *string         `json:"target_id,omitempty"`
	Status          string          `json:"status"`
	TriggeredBy     string          `json:"triggered_by"`
	RequestID       *string         `json:"request_id,omitempty"`
	ParentOperation *string         `json:"parent_operation,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	Error           *string         `json:"error,omitempty"`
	ErrorCode       *string         `json:"error_code,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

const opColumns = `
	id, type, target_id, status,
	triggered_by, request_id, parent_operation,
	started_at, completed_at,
	error, error_code, data
`

func scanOp(rs rowScanner) (*NetworkOperation, error) {
	var (
		op           NetworkOperation
		targetID     sql.NullString
		requestID    sql.NullString
		parentOp     sql.NullString
		startedAtStr string
		completedAt  sql.NullString
		errStr       sql.NullString
		errCode      sql.NullString
		data         sql.NullString
	)
	err := rs.Scan(
		&op.ID, &op.Type, &targetID, &op.Status,
		&op.TriggeredBy, &requestID, &parentOp,
		&startedAtStr, &completedAt,
		&errStr, &errCode, &data,
	)
	if err != nil {
		return nil, err
	}
	op.TargetID = ptrFromNullString(targetID)
	op.RequestID = ptrFromNullString(requestID)
	op.ParentOperation = ptrFromNullString(parentOp)
	op.StartedAt = parseTime(startedAtStr)
	op.CompletedAt = ptrFromNullTime(completedAt)
	op.Error = ptrFromNullString(errStr)
	op.ErrorCode = ptrFromNullString(errCode)
	if data.Valid {
		op.Data = json.RawMessage(data.String)
	}
	return &op, nil
}

// CreateOperation inserta una nueva operación. Si ID está vacío, se
// genera un UUID. Si StartedAt es zero, se usa clock.Now(). Si Status
// está vacío, default 'pending'.
//
// TriggeredBy DEBE estar en uno de los formatos válidos — la DB lo
// rechaza si no.
func (r *NetworkRepo) CreateOperation(ctx context.Context, tx *sql.Tx, op *NetworkOperation) error {
	if op.ID == "" {
		op.ID = uuid.New().String()
	}
	if op.StartedAt.IsZero() {
		op.StartedAt = r.clock.Now().UTC()
	}
	if op.Status == "" {
		op.Status = "pending"
	}
	var dataArg interface{}
	if len(op.Data) > 0 {
		dataArg = string(op.Data)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_operations (
			id, type, target_id, status,
			triggered_by, request_id, parent_operation,
			started_at, completed_at,
			error, error_code, data
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		op.ID, op.Type, nullStringPtr(op.TargetID), op.Status,
		op.TriggeredBy, nullStringPtr(op.RequestID), nullStringPtr(op.ParentOperation),
		op.StartedAt.UTC().Format(time.RFC3339), nullTimeArg(op.CompletedAt),
		nullStringPtr(op.Error), nullStringPtr(op.ErrorCode), dataArg,
	)
	if err != nil {
		return fmt.Errorf("CreateOperation: %w", err)
	}
	return nil
}

// nullTimeArg convierte *time.Time a interface para INSERT/UPDATE.
// Devuelve nil si el puntero es nil (NULL en DB), o el string RFC3339.
func nullTimeArg(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// GetOperation lee una operación por id.
func (r *NetworkRepo) GetOperation(ctx context.Context, id string) (*NetworkOperation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+opColumns+` FROM network_operations WHERE id = ?`, id)
	op, err := scanOp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOperationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetOperation: %w", err)
	}
	return op, nil
}

// UpdateOperationStatus cambia el status y, si el nuevo status es
// terminal ('completed', 'failed', 'rolled_back'), también setea
// completed_at = now y persiste error/errorCode si los hay.
//
// Para status intermedio ('in_progress') no setea completed_at.
//
// La DB valida los status válidos via CHECK.
func (r *NetworkRepo) UpdateOperationStatus(ctx context.Context, tx *sql.Tx, id, status string, errCode, errMsg *string) error {
	terminal := status == "completed" || status == "failed" || status == "rolled_back"
	var completedAtArg interface{}
	if terminal {
		completedAtArg = r.clock.Now().UTC().Format(time.RFC3339)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE network_operations
		SET status = ?, completed_at = ?, error = ?, error_code = ?
		WHERE id = ?
	`, status, completedAtArg, nullStringPtr(errMsg), nullStringPtr(errCode), id)
	if err != nil {
		return fmt.Errorf("UpdateOperationStatus: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrOperationNotFound
	}
	return nil
}

// ListOperationsByStatus devuelve operaciones en un status dado,
// ordenadas por started_at descendente.
func (r *NetworkRepo) ListOperationsByStatus(ctx context.Context, status string, limit int) ([]*NetworkOperation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+opColumns+`
		FROM network_operations
		WHERE status = ?
		ORDER BY started_at DESC
		LIMIT ?
	`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("ListOperationsByStatus: %w", err)
	}
	defer rows.Close()
	return collectOps(rows)
}

// ListOperationsByTriggeredBy filtra por prefix exacto (e.g.
// "reconciler:ddns_updater" o "user:admin"). Útil para tracing.
func (r *NetworkRepo) ListOperationsByTriggeredBy(ctx context.Context, triggeredBy string, limit int) ([]*NetworkOperation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+opColumns+`
		FROM network_operations
		WHERE triggered_by = ?
		ORDER BY started_at DESC
		LIMIT ?
	`, triggeredBy, limit)
	if err != nil {
		return nil, fmt.Errorf("ListOperationsByTriggeredBy: %w", err)
	}
	defer rows.Close()
	return collectOps(rows)
}

// ListOperationsByRequest devuelve todas las operaciones de un
// request_id (útil para reconstruir una llamada HTTP completa).
func (r *NetworkRepo) ListOperationsByRequest(ctx context.Context, requestID string) ([]*NetworkOperation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+opColumns+`
		FROM network_operations
		WHERE request_id = ?
		ORDER BY started_at ASC
	`, requestID)
	if err != nil {
		return nil, fmt.Errorf("ListOperationsByRequest: %w", err)
	}
	defer rows.Close()
	return collectOps(rows)
}

// ListChildOperations devuelve los hijos directos de una operación
// (filas con parent_operation = parentID), ordenadas por started_at.
func (r *NetworkRepo) ListChildOperations(ctx context.Context, parentID string) ([]*NetworkOperation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+opColumns+`
		FROM network_operations
		WHERE parent_operation = ?
		ORDER BY started_at ASC
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("ListChildOperations: %w", err)
	}
	defer rows.Close()
	return collectOps(rows)
}

func collectOps(rows *sql.Rows) ([]*NetworkOperation, error) {
	var out []*NetworkOperation
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// network_operations — Retention
// ─────────────────────────────────────────────────────────────────────────────

// PruneCompletedOperations borra operaciones cuyo completed_at <
// olderThan. Solo afecta a operaciones con status terminal
// ('completed', 'failed', 'rolled_back') — operations pending o
// in_progress se respetan aunque tengan started_at antiguo.
//
// Devuelve cuántas filas se borraron.
//
// NOTA: el FK CASCADE de network_events sobre network_operations
// borra automáticamente los eventos asociados.
func (r *NetworkRepo) PruneCompletedOperations(ctx context.Context, tx *sql.Tx, olderThan time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM network_operations
		WHERE status IN ('completed', 'failed', 'rolled_back')
		  AND completed_at IS NOT NULL
		  AND completed_at < ?
	`, olderThan.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("PruneCompletedOperations: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
