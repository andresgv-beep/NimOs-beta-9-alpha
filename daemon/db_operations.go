// db_operations.go — Repository de nimos_operations (Beta 8.1.x · APP-012).
//
// El AppsRepo de db_apps.go separa fuente-de-verdad (BD) del acceso global
// vía variable. Este archivo replica el patrón para operations.
//
// El OperationsRepo encapsula CRUD y transiciones de estado. La state machine
// está validada en el repo, no en el caller:
//
//   pending → running                  (MarkRunning)
//   running → succeeded                (MarkSucceeded · idempotente)
//   running → failed                   (MarkFailed)
//   pending → failed                   (MarkFailed antes de empezar)
//   running → running                  (UpdateProgress · sin transición real)
//
// Transiciones inválidas (e.g. succeeded → running) devuelven error. Esto
// previene resucitar ops terminales por mistake.
//
// Tests: db_operations_test.go valida el contrato del repo.

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────────────────────────────
// Constantes del state machine
// ─────────────────────────────────────────────────────────────────────

const (
	OpsStatusPending   = "pending"
	OpsStatusRunning   = "running"
	OpsStatusSucceeded = "succeeded"
	OpsStatusFailed    = "failed"
	OpsStatusCancelled = "cancelled"

	// Defaultexpiry tras finalización · GC manual usa este valor.
	OpsExpiryAfterFinish = 24 * time.Hour
)

// IsTerminalOpsStatus · status del que no se puede salir.
func IsTerminalOpsStatus(status string) bool {
	return status == OpsStatusSucceeded || status == OpsStatusFailed || status == OpsStatusCancelled
}

// ─────────────────────────────────────────────────────────────────────
// Modelo Go
// ─────────────────────────────────────────────────────────────────────

// DBOperation representa una fila de la tabla nimos_operations.
type DBOperation struct {
	ID         string
	Type       string // 'docker.install' | 'docker.pull' | ...
	Status     string // pending|running|succeeded|failed|cancelled
	Progress   int    // 0..100
	Message    string // descripción del paso actual
	ResultJSON string // JSON serializado al completar
	Error      string // si Status='failed'
	CreatedAt  string // ISO
	StartedAt  string // ISO · vacío si nunca llegó a running
	FinishedAt string // ISO · vacío si no terminó
	ExpiresAt  string // ISO · típicamente FinishedAt + 24h
	CreatedBy  string // username
}

// ToMap convierte a map para serialización JSON HTTP.
// No incluye CreatedBy en el output por defecto (info interna).
func (op *DBOperation) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"id":         op.ID,
		"type":       op.Type,
		"status":     op.Status,
		"progress":   op.Progress,
		"message":    op.Message,
		"createdAt":  op.CreatedAt,
		"startedAt":  op.StartedAt,
		"finishedAt": op.FinishedAt,
	}
	if op.Status == OpsStatusFailed && op.Error != "" {
		m["error"] = op.Error
	}
	// result_json se devuelve como objeto JSON deserializado para que el
	// cliente no tenga que parsear un string. Si está vacío o malformado,
	// se omite.
	if op.ResultJSON != "" && op.Status == OpsStatusSucceeded {
		// El caller que tenga el JSON sabe deserializarlo. Aquí mantenemos
		// el string para que el cliente decida. Decisión: devolver como
		// `resultRaw` string · es predecible y no requiere doble parse.
		m["resultRaw"] = op.ResultJSON
	}
	return m
}

// ─────────────────────────────────────────────────────────────────────
// Repository
// ─────────────────────────────────────────────────────────────────────

// OperationsRepo encapsula el acceso a la tabla nimos_operations.
type OperationsRepo struct {
	db *sql.DB
}

// NewOperationsRepo construye un repo a partir de una conexión SQL.
// Patrón consistente con NewAppsRepo.
func NewOperationsRepo(db *sql.DB) *OperationsRepo {
	return &OperationsRepo{db: db}
}

// operationsRepo es la instancia global · inicializada al arranque junto con
// la DB en openDB(). Patrón consistente con appsRepo.
var operationsRepo *OperationsRepo

// ─────────────────────────────────────────────────────────────────────
// ID generation
// ─────────────────────────────────────────────────────────────────────

// generateOperationID produce un ID único legible: op_<unix>_<8hex>.
// Combina timestamp (orden cronológico aproximado) + entropía aleatoria
// para evitar colisiones bajo concurrencia alta (improbable pero barato).
func generateOperationID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("op_%d_%s", time.Now().Unix(), hex.EncodeToString(buf))
}

// ─────────────────────────────────────────────────────────────────────
// CRUD básico
// ─────────────────────────────────────────────────────────────────────

// Create inserta una nueva operation en estado 'pending'.
//
// El ID se genera internamente y se devuelve. type debe ser no-vacío.
// createdBy es el username del session que creó la op (necesario para
// autorización del polling).
//
// El caller debe inmediatamente lanzar la goroutine de trabajo que llamará
// a MarkRunning / UpdateProgress / MarkSucceeded|MarkFailed.
func (r *OperationsRepo) Create(ctx context.Context, opType, createdBy string) (*DBOperation, error) {
	if opType == "" {
		return nil, fmt.Errorf("operation type required")
	}
	if createdBy == "" {
		return nil, fmt.Errorf("createdBy required")
	}

	op := &DBOperation{
		ID:        generateOperationID(),
		Type:      opType,
		Status:    OpsStatusPending,
		Progress:  0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		CreatedBy: createdBy,
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO nimos_operations
			(id, type, status, progress, message, result_json, error,
			 created_at, started_at, finished_at, expires_at, created_by)
		VALUES (?, ?, ?, ?, '', '', '', ?, '', '', '', ?)
	`, op.ID, op.Type, op.Status, op.Progress, op.CreatedAt, op.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("create operation: %w", err)
	}
	return op, nil
}

// Get obtiene una operation por id. Devuelve (nil, nil) si no existe (no error).
func (r *OperationsRepo) Get(ctx context.Context, id string) (*DBOperation, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, type, status, progress, message, result_json, error,
		       created_at, started_at, finished_at, expires_at, created_by
		FROM nimos_operations WHERE id = ?
	`, id)

	var op DBOperation
	err := row.Scan(&op.ID, &op.Type, &op.Status, &op.Progress, &op.Message,
		&op.ResultJSON, &op.Error, &op.CreatedAt, &op.StartedAt,
		&op.FinishedAt, &op.ExpiresAt, &op.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get operation %q: %w", id, err)
	}
	return &op, nil
}

// List devuelve operations ordenadas por created_at descendente (más recientes
// primero). Filtros opcionales (vacío = no filtra):
//
//   - opType: solo este tipo
//   - status: solo este status
//   - createdBy: solo este creador
//   - limit > 0 acota el resultado
//
// Si todos los filtros son vacíos y limit=0, devuelve todas las rows.
// CUIDADO: en tablas grandes esto puede ser caro. El caller decide.
func (r *OperationsRepo) List(ctx context.Context, opType, status, createdBy string, limit int) ([]*DBOperation, error) {
	query := `
		SELECT id, type, status, progress, message, result_json, error,
		       created_at, started_at, finished_at, expires_at, created_by
		FROM nimos_operations WHERE 1=1`
	var args []interface{}
	if opType != "" {
		query += ` AND type = ?`
		args = append(args, opType)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if createdBy != "" {
		query += ` AND created_by = ?`
		args = append(args, createdBy)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	defer rows.Close()

	var ops []*DBOperation
	for rows.Next() {
		var op DBOperation
		if err := rows.Scan(&op.ID, &op.Type, &op.Status, &op.Progress, &op.Message,
			&op.ResultJSON, &op.Error, &op.CreatedAt, &op.StartedAt,
			&op.FinishedAt, &op.ExpiresAt, &op.CreatedBy); err != nil {
			return nil, fmt.Errorf("scan operation: %w", err)
		}
		ops = append(ops, &op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter operations: %w", err)
	}
	return ops, nil
}

// ─────────────────────────────────────────────────────────────────────
// Transiciones de estado
// ─────────────────────────────────────────────────────────────────────

// MarkRunning transiciona pending → running.
// Devuelve error si la op no está en pending (e.g. ya está running, succeeded...).
func (r *OperationsRepo) MarkRunning(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		UPDATE nimos_operations
		SET status = ?, started_at = ?
		WHERE id = ? AND status = ?
	`, OpsStatusRunning, now, id, OpsStatusPending)
	if err != nil {
		return fmt.Errorf("mark running %q: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// O bien no existe o no está en pending. Distinguimos:
		op, getErr := r.Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		if op == nil {
			return fmt.Errorf("operation %q not found", id)
		}
		return fmt.Errorf("operation %q cannot transition to running from status %q", id, op.Status)
	}
	return nil
}

// UpdateProgress actualiza progress (0..100) y message sin cambiar status.
// Solo válido en status='running'. Idempotente.
//
// progress se trunca a [0, 100]. message no se trunca (responsabilidad del caller).
func (r *OperationsRepo) UpdateProgress(ctx context.Context, id string, progress int, message string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE nimos_operations
		SET progress = ?, message = ?
		WHERE id = ? AND status = ?
	`, progress, message, id, OpsStatusRunning)
	if err != nil {
		return fmt.Errorf("update progress %q: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		op, getErr := r.Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		if op == nil {
			return fmt.Errorf("operation %q not found", id)
		}
		return fmt.Errorf("operation %q is not running (status=%q)", id, op.Status)
	}
	return nil
}

// MarkSucceeded transiciona pending|running → succeeded.
// resultJSON es el payload serializado (formato libre por tipo de op).
// expires_at se setea a now + OpsExpiryAfterFinish.
func (r *OperationsRepo) MarkSucceeded(ctx context.Context, id, resultJSON string) error {
	return r.markFinished(ctx, id, OpsStatusSucceeded, resultJSON, "")
}

// MarkFailed transiciona pending|running → failed.
// errorMsg debe ser el mensaje de error legible.
// resultJSON puede ser vacío o contener info parcial recuperable.
func (r *OperationsRepo) MarkFailed(ctx context.Context, id, errorMsg, resultJSON string) error {
	if errorMsg == "" {
		errorMsg = "operation failed"
	}
	return r.markFinished(ctx, id, OpsStatusFailed, resultJSON, errorMsg)
}

// markFinished es el helper común. No se exporta porque la state machine
// se controla con los métodos públicos.
func (r *OperationsRepo) markFinished(ctx context.Context, id, finalStatus, resultJSON, errorMsg string) error {
	if !IsTerminalOpsStatus(finalStatus) {
		return fmt.Errorf("markFinished called with non-terminal status %q", finalStatus)
	}
	now := time.Now().UTC()
	finishedAt := now.Format(time.RFC3339)
	expiresAt := now.Add(OpsExpiryAfterFinish).Format(time.RFC3339)

	// Validamos que NO esté ya en terminal · si lo está, devolvemos error
	// para que el caller sepa que algo raro pasó (e.g. double-mark, race).
	res, err := r.db.ExecContext(ctx, `
		UPDATE nimos_operations
		SET status = ?, finished_at = ?, expires_at = ?,
		    result_json = ?, error = ?
		WHERE id = ? AND status NOT IN (?, ?, ?)
	`, finalStatus, finishedAt, expiresAt, resultJSON, errorMsg, id,
		OpsStatusSucceeded, OpsStatusFailed, OpsStatusCancelled)
	if err != nil {
		return fmt.Errorf("mark %s %q: %w", finalStatus, id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		op, getErr := r.Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		if op == nil {
			return fmt.Errorf("operation %q not found", id)
		}
		// Ya estaba en terminal · double-mark.
		return fmt.Errorf("operation %q already in terminal status %q", id, op.Status)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Garbage collection
// ─────────────────────────────────────────────────────────────────────

// DeleteExpired elimina operations con expires_at < now. Devuelve el número
// de rows borradas. Pensado para job periódico (cron interno o Reconciler).
//
// No se invoca automáticamente en este batch · pendiente de decisión sobre
// cadencia (fase 3 podría meterlo como Reconciler).
func (r *OperationsRepo) DeleteExpired(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM nimos_operations
		WHERE expires_at != '' AND expires_at < ?
	`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired operations: %w", err)
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}
