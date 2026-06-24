// storage_repo_ops.go — StorageRepo: Operations, Events, Capabilities.
//
// Operations son el journal de TODA mutación (sync y async). Cada mutación
// genera una Operation con status pending/in_progress/completed/failed.
//
// Events son el timeline detallado de pasos dentro de una Operation
// (wipe OK, mkfs OK, mount FAILED, etc.). Permiten reconstruir post-mortem
// qué pasó durante una operación.
//
// Capabilities son las operaciones que un pool soporta (snapshots, balance,
// replace_device, etc.). Beta 8 todos los pools BTRFS managed tienen el
// set completo; futuros tipos (ext4 observed, mdraid) tendrán subsets.
//
// see docs/storage_api.md §3 para firmas completas
// see docs/storage_invariants.md#5 para reglas de transacciones

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ═════════════════════════════════════════════════════════════════════════════
// OPERATIONS — Queries
// ═════════════════════════════════════════════════════════════════════════════

const operationColumns = `id, type, pool_id, status, started_at, completed_at,
	error, error_code, data`

func scanOperation(rows interface {
	Scan(dest ...any) error
}) (*Operation, error) {
	var op Operation
	var poolID, completedAt, errMsg, errCode, dataStr sql.NullString
	var startedAt string

	err := rows.Scan(
		&op.ID, &op.Type, &poolID, &op.Status, &startedAt,
		&completedAt, &errMsg, &errCode, &dataStr,
	)
	if err != nil {
		return nil, err
	}

	if poolID.Valid {
		op.PoolID = &poolID.String
	}
	if t, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
		op.StartedAt = t
	}
	op.CompletedAt = timeFromNull(completedAt)
	op.Error = stringFromNull(errMsg)
	op.ErrorCode = stringFromNull(errCode)
	if dataStr.Valid && dataStr.String != "" {
		op.Data = json.RawMessage(dataStr.String)
	}
	return &op, nil
}

// GetOperation devuelve una operación por su ID. Sin events (cargar aparte).
func (r *StorageRepo) GetOperation(ctx context.Context, id string) (*Operation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+operationColumns+` FROM storage_operations WHERE id = ?`, id)
	op, err := scanOperation(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return op, err
}

// OperationFilter permite filtrar listados de operaciones.
type OperationFilter struct {
	PoolID *string
	Status *OperationStatus
	Type   *OperationType
	Since  *time.Time
	Limit  int // 0 = sin límite (cuidado), default si llega 0 a ListOperations: 50
}

// ListOperations devuelve operaciones que cumplen el filtro, más recientes
// primero. Límite default: 50 si Filter.Limit == 0.
func (r *StorageRepo) ListOperations(ctx context.Context, f OperationFilter) ([]*Operation, error) {
	q := strings.Builder{}
	q.WriteString(`SELECT ` + operationColumns + ` FROM storage_operations WHERE 1=1`)
	args := []any{}

	if f.PoolID != nil {
		q.WriteString(` AND pool_id = ?`)
		args = append(args, *f.PoolID)
	}
	if f.Status != nil {
		q.WriteString(` AND status = ?`)
		args = append(args, string(*f.Status))
	}
	if f.Type != nil {
		q.WriteString(` AND type = ?`)
		args = append(args, string(*f.Type))
	}
	if f.Since != nil {
		q.WriteString(` AND started_at >= ?`)
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}

	q.WriteString(` ORDER BY started_at DESC`)
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	q.WriteString(` LIMIT ?`)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("ListOperations: %w", err)
	}
	defer rows.Close()

	out := []*Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("ListOperations scan: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ListPendingOperations devuelve operaciones en pending o in_progress.
// Usado por RecoverPendingOperations al arranque.
// see docs/storage_state_machines.md §4.5
func (r *StorageRepo) ListPendingOperations(ctx context.Context) ([]*Operation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+operationColumns+` FROM storage_operations
		 WHERE status IN ('pending', 'in_progress')
		 ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("ListPendingOperations: %w", err)
	}
	defer rows.Close()

	out := []*Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPendingOperations scan: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ═════════════════════════════════════════════════════════════════════════════
// OPERATIONS — Mutaciones (transaccionales)
// ═════════════════════════════════════════════════════════════════════════════

// CreateOperation inserta una nueva operación. Status inicial pending o
// in_progress según el caller. Las invariantes del schema (UNIQUE parciales)
// rechazan layout-ops o scrubs concurrentes en el mismo pool.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) CreateOperation(ctx context.Context, tx *sql.Tx, op *Operation) error {
	if op.ID == "" {
		return fmt.Errorf("CreateOperation: ID is required")
	}
	if op.Type == "" {
		return fmt.Errorf("CreateOperation: Type is required")
	}
	if op.Status == "" {
		op.Status = OpStatusPending
	}
	if op.StartedAt.IsZero() {
		op.StartedAt = time.Now().UTC()
	}

	var dataStr sql.NullString
	if len(op.Data) > 0 {
		dataStr = sql.NullString{String: string(op.Data), Valid: true}
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO storage_operations
		 (id, type, pool_id, status, started_at, completed_at, error, error_code, data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, string(op.Type), nullableString(op.PoolID), string(op.Status),
		op.StartedAt.Format(time.RFC3339Nano),
		nullableTime(op.CompletedAt),
		nullableString(op.Error),
		nullableString(op.ErrorCode),
		dataStr,
	)
	if err != nil {
		return fmt.Errorf("CreateOperation: %w", err)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// UpdateOperationStatus actualiza el status de una operación. Si el nuevo
// status es terminal (completed/failed/rolled_back/cancelled), también
// rellena completed_at automáticamente.
// errMsg y errCode solo se usan si status indica fallo.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) UpdateOperationStatus(ctx context.Context, tx *sql.Tx,
	id string, status OperationStatus, errMsg, errCode *string) error {

	var completedAt sql.NullString
	if status.IsTerminal() {
		completedAt = sql.NullString{
			String: time.Now().UTC().Format(time.RFC3339Nano),
			Valid:  true,
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE storage_operations
		 SET status = ?, completed_at = ?, error = ?, error_code = ?
		 WHERE id = ?`,
		string(status),
		completedAt,
		nullableString(errMsg),
		nullableString(errCode),
		id,
	)
	if err != nil {
		return fmt.Errorf("UpdateOperationStatus: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("UpdateOperationStatus: operation %q not found", id)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// UpdateOperationData reemplaza el campo data (JSON payload) de la operación.
// Útil para actualizar progreso durante una op larga (ej: progress de balance).
// NO incrementa generation (es muy frecuente y no afecta a "estructura").
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) UpdateOperationData(ctx context.Context, tx *sql.Tx,
	id string, data json.RawMessage) error {

	var dataStr sql.NullString
	if len(data) > 0 {
		dataStr = sql.NullString{String: string(data), Valid: true}
	}

	_, err := tx.ExecContext(ctx,
		`UPDATE storage_operations SET data = ? WHERE id = ?`,
		dataStr, id)
	if err != nil {
		return fmt.Errorf("UpdateOperationData: %w", err)
	}
	return nil
}

// ═════════════════════════════════════════════════════════════════════════════
// EVENTS — Queries y mutaciones
// ═════════════════════════════════════════════════════════════════════════════

// ListEvents devuelve los eventos de una operación, en orden cronológico.
func (r *StorageRepo) ListEvents(ctx context.Context, operationID string) ([]*Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, operation_id, timestamp, level, message
		 FROM storage_events
		 WHERE operation_id = ?
		 ORDER BY timestamp ASC`,
		operationID)
	if err != nil {
		return nil, fmt.Errorf("ListEvents: %w", err)
	}
	defer rows.Close()

	out := []*Event{}
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &e.OperationID, &ts, &e.Level, &e.Message); err != nil {
			return nil, fmt.Errorf("ListEvents scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.Timestamp = t
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// AppendEvent añade un evento a una operación. Llamar desde runSteps cada vez
// que un paso completa o falla.
// NO incrementa generation (eventos son frecuentes).
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) AppendEvent(ctx context.Context, tx *sql.Tx, e *Event) error {
	if e.ID == "" {
		return fmt.Errorf("AppendEvent: ID is required")
	}
	if e.OperationID == "" {
		return fmt.Errorf("AppendEvent: OperationID is required")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.Level == "" {
		e.Level = EventLevelInfo
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO storage_events (id, operation_id, timestamp, level, message)
		 VALUES (?, ?, ?, ?, ?)`,
		e.ID, e.OperationID,
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		string(e.Level), e.Message,
	)
	if err != nil {
		return fmt.Errorf("AppendEvent: %w", err)
	}
	return nil
}

// ═════════════════════════════════════════════════════════════════════════════
// CAPABILITIES — Acceso por pool
// ═════════════════════════════════════════════════════════════════════════════

// GetPoolCapabilities devuelve la lista de capabilities soportadas por un pool.
func (r *StorageRepo) GetPoolCapabilities(ctx context.Context, poolID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT capability FROM storage_pool_capabilities
		 WHERE pool_id = ?
		 ORDER BY capability`,
		poolID)
	if err != nil {
		return nil, fmt.Errorf("GetPoolCapabilities: %w", err)
	}
	defer rows.Close()

	caps := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("GetPoolCapabilities scan: %w", err)
		}
		caps = append(caps, c)
	}
	return caps, rows.Err()
}

// HasCapability devuelve true si el pool soporta la capability indicada.
func (r *StorageRepo) HasCapability(ctx context.Context, poolID, capability string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_pool_capabilities
		 WHERE pool_id = ? AND capability = ?`,
		poolID, capability).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("HasCapability: %w", err)
	}
	return count > 0, nil
}

// SetPoolCapabilities reemplaza el conjunto de capabilities de un pool.
// Borra las existentes y reinserta las nuevas dentro de la transacción.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) SetPoolCapabilities(ctx context.Context, tx *sql.Tx,
	poolID string, caps []string) error {

	// Borrar todas las capabilities existentes
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM storage_pool_capabilities WHERE pool_id = ?`,
		poolID); err != nil {
		return fmt.Errorf("SetPoolCapabilities delete: %w", err)
	}

	// Insertar las nuevas (deduplicadas por PRIMARY KEY compuesta)
	for _, c := range caps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO storage_pool_capabilities (pool_id, capability)
			 VALUES (?, ?)`,
			poolID, c); err != nil {
			return fmt.Errorf("SetPoolCapabilities insert %q: %w", c, err)
		}
	}

	_, err := r.incrementGlobalGeneration(ctx, tx)
	return err
}

// DefaultBtrfsManagedCapabilities devuelve el set completo de capabilities
// para un pool BTRFS managed en Beta 8. Llamar tras CreatePool.
func DefaultBtrfsManagedCapabilities() []string {
	return []string{
		"snapshots",
		"balance",
		"replace_device",
		"add_device",
		"remove_device",
		"convert_profile",
		"scrub",
		"compression",
	}
}
