// network_events.go — Log auditable del módulo network con anti-explosión.
//
// Este NO es un CRUD puro: la operación principal es Emit(), que aplica
// dedupe + rate limit antes de tocar la DB. La razón es NIMOS_DISCIPLINE
// §4 v2: el módulo network puede producir miles de eventos al día si
// está mal calibrado, y eso desestabiliza el daemon (DB swelling, query
// slowdowns, disk pressure). Los antídotos son:
//
//   (A) Dedupe runtime (5 min):
//       Si llega un evento con la misma (category, event, target_id)
//       dentro de la ventana de 5 min, NO insertamos fila nueva —
//       incrementamos `occurrences` y actualizamos `last_seen_at`.
//
//   (B) Rate limit por category (10/min):
//       Bucket en memoria. Si una category supera 10 emits en 1 min,
//       los siguientes se dropean (con métrica de drops).
//
//   (C) Aggregation nocturna (03:00):
//       Cron comprime el día anterior. Mantiene errors+warns intactos,
//       resume eventos rutinarios en una fila por (category, event, day).
//
//   (D) Retention por nivel:
//       error: 90d / warn: 30d / info: 7d / debug: 24h.
//
//   (E) Niveles correctos (responsabilidad del caller, no de Emit):
//       reconciler_started=debug, NO info. Solo cambios reales son info+.
//
// El EventEmitter es thread-safe. Está pensado para ser singleton dentro
// del daemon.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipos públicos
// ─────────────────────────────────────────────────────────────────────────────

// NOTA: EventLevel y sus constantes (EventLevelDebug, EventLevelInfo,
// EventLevelWarn, EventLevelError) están definidos en storage_types.go
// porque son universales — los reusamos aquí.

// EventCategory es una de las categorías aceptadas por la DB.
type EventCategory string

const (
	CategoryDdns       EventCategory = "ddns"
	CategoryCert       EventCategory = "cert"
	CategoryPort       EventCategory = "port"
	CategoryUPnP       EventCategory = "upnp"
	CategoryBreaker    EventCategory = "breaker"
	CategoryObserver   EventCategory = "observer"
	CategoryCapability EventCategory = "capability"
	CategoryExposure   EventCategory = "exposure"
)

// EventInput es lo que el caller pasa a Emit. Los campos opcionales
// pueden quedar zero/nil.
type EventInput struct {
	OperationID *string // FK a network_operations, opcional
	Category    EventCategory
	Event       string  // 'update_started', 'cert_issued', etc.
	TargetID    *string // entidad afectada (ddns_id, cert_id, ...)
	Level       EventLevel
	Message     string
	Details     json.RawMessage // JSON opcional
}

// NetworkEvent representa una fila de network_events (lectura).
type NetworkEvent struct {
	ID          string          `json:"id"`
	OperationID *string         `json:"operation_id,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
	Category    string          `json:"category"`
	Event       string          `json:"event"`
	TargetID    *string         `json:"target_id,omitempty"`
	Level       string          `json:"level"`
	Message     string          `json:"message"`
	Details     json.RawMessage `json:"details,omitempty"`
	Occurrences int64           `json:"occurrences"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Configuración (anti-explosión)
// ─────────────────────────────────────────────────────────────────────────────

// EventEmitterConfig agrupa los parámetros tunables. Los defaults vienen
// de DefaultEventEmitterConfig() y reflejan lo decidido en DISCIPLINE §4 v2.
type EventEmitterConfig struct {
	// DedupeWindow: ventana en la que un (category,event,target) repetido
	// incrementa occurrences en lugar de insertar nueva fila.
	DedupeWindow time.Duration

	// RateLimitPerCategory: máximo de emits por minuto por category.
	// Si se excede, los siguientes se dropean.
	RateLimitPerCategory int

	// RateLimitWindow: ventana del bucket de rate limit (default 1 min).
	RateLimitWindow time.Duration

	// Retention por nivel.
	RetentionError time.Duration
	RetentionWarn  time.Duration
	RetentionInfo  time.Duration
	RetentionDebug time.Duration
}

// DefaultEventEmitterConfig devuelve la configuración por defecto.
func DefaultEventEmitterConfig() EventEmitterConfig {
	return EventEmitterConfig{
		DedupeWindow:         5 * time.Minute,
		RateLimitPerCategory: 10,
		RateLimitWindow:      time.Minute,
		RetentionError:       90 * 24 * time.Hour,
		RetentionWarn:        30 * 24 * time.Hour,
		RetentionInfo:        7 * 24 * time.Hour,
		RetentionDebug:       24 * time.Hour,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EventEmitter
// ─────────────────────────────────────────────────────────────────────────────

// rateLimitBucket es el contador de emits para una category. Se mantiene
// en memoria — al reiniciar el daemon, todas las categories arrancan a 0.
type rateLimitBucket struct {
	count      int
	windowEnd  time.Time
	totalDrops int64 // contador histórico de drops (para métricas/debug)
}

// EventEmitter es el emisor central de eventos del módulo network.
// Thread-safe. Se construye una vez en el arranque del daemon.
type EventEmitter struct {
	db     *sql.DB
	clock  Clock
	config EventEmitterConfig

	mu      sync.Mutex
	buckets map[EventCategory]*rateLimitBucket
}

// NewEventEmitter crea un emisor. db debe tener network_events ya creado.
// Si clock es nil, usa RealClock.
func NewEventEmitter(db *sql.DB, clock Clock, config EventEmitterConfig) *EventEmitter {
	if clock == nil {
		clock = NewRealClock()
	}
	return &EventEmitter{
		db:      db,
		clock:   clock,
		config:  config,
		buckets: make(map[EventCategory]*rateLimitBucket),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Emit
// ─────────────────────────────────────────────────────────────────────────────

// ErrEventRateLimited se devuelve cuando se ha alcanzado el rate limit
// para la category. El caller no necesita actuar — el evento se descarta
// silenciosamente. Devolverlo permite que tests verifiquen el
// comportamiento.
var ErrEventRateLimited = errors.New("event rate limit exceeded for category")

// Emit registra un evento. Aplica primero el rate limit (en memoria),
// luego el dedupe (en DB), y finalmente inserta si procede.
//
// Devuelve:
//   - emitted=true,  err=nil       → fila nueva insertada.
//   - emitted=false, err=nil       → dedupe disparó: incrementó occurrences.
//   - emitted=false, err=ErrEventRateLimited → drop por rate limit.
//   - err != nil                    → error en DB; el caller debe loguear.
//
// Las validaciones triviales (category/level válidos) las hace la DB.
// Aquí solo defensive checks.
func (e *EventEmitter) Emit(ctx context.Context, in EventInput) (emitted bool, err error) {
	if in.Category == "" || in.Event == "" || in.Level == "" || in.Message == "" {
		return false, fmt.Errorf("Emit: category, event, level, message required")
	}

	// (B) Rate limit en memoria.
	if !e.allowByRateLimit(in.Category) {
		return false, ErrEventRateLimited
	}

	now := e.clock.Now().UTC()

	// Abrimos transacción solo para el dedupe + insert. La DB serializa
	// SQLite a nivel de archivo, así que no hay race entre dos goroutines
	// que emiten misma key — la segunda verá la primera y dedupará.
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("Emit begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// (A) Dedupe: buscar el último evento con misma (category, event, target_id)
	// dentro de la ventana. Si existe, incrementar occurrences.
	dedupeCutoff := now.Add(-e.config.DedupeWindow).Format(time.RFC3339)

	var existingID string
	var existingOccurrences int64
	queryDedupe := `
		SELECT id, occurrences FROM network_events
		WHERE category = ? AND event = ?
		  AND (target_id IS ? OR target_id = ?)
		  AND last_seen_at >= ?
		ORDER BY last_seen_at DESC
		LIMIT 1
	`
	// Manejo NULL: si in.TargetID es nil, queremos rows con target_id IS NULL.
	// Si in.TargetID no es nil, queremos rows con target_id = <valor>.
	var targetArg interface{}
	if in.TargetID != nil {
		targetArg = *in.TargetID
	}
	err = tx.QueryRowContext(ctx, queryDedupe,
		string(in.Category), in.Event, targetArg, targetArg, dedupeCutoff,
	).Scan(&existingID, &existingOccurrences)

	if err == nil {
		// Hit de dedupe: incrementar.
		_, err = tx.ExecContext(ctx, `
			UPDATE network_events
			SET occurrences = occurrences + 1, last_seen_at = ?
			WHERE id = ?
		`, now.Format(time.RFC3339), existingID)
		if err != nil {
			return false, fmt.Errorf("Emit dedupe update: %w", err)
		}
		if err = tx.Commit(); err != nil {
			return false, fmt.Errorf("Emit commit dedupe: %w", err)
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("Emit dedupe lookup: %w", err)
	}
	// No hit — insertar.

	id := uuid.New().String()
	var detailsArg interface{}
	if len(in.Details) > 0 {
		detailsArg = string(in.Details)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO network_events (
			id, operation_id, timestamp,
			category, event, target_id,
			level, message, details,
			occurrences, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
	`,
		id, nullStringPtr(in.OperationID), now.Format(time.RFC3339),
		string(in.Category), in.Event, nullStringPtr(in.TargetID),
		string(in.Level), in.Message, detailsArg,
		now.Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("Emit insert: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("Emit commit insert: %w", err)
	}
	return true, nil
}

// allowByRateLimit aplica el bucket en memoria. Devuelve true si el
// evento puede continuar. Incrementa el contador si pasa.
func (e *EventEmitter) allowByRateLimit(cat EventCategory) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.clock.Now()
	b, ok := e.buckets[cat]
	if !ok || now.After(b.windowEnd) {
		// Ventana nueva o expirada.
		newBucket := &rateLimitBucket{
			count:     1,
			windowEnd: now.Add(e.config.RateLimitWindow),
		}
		// Preservar el contador histórico de drops entre ventanas.
		if b != nil {
			newBucket.totalDrops = b.totalDrops
		}
		e.buckets[cat] = newBucket
		return true
	}
	if b.count >= e.config.RateLimitPerCategory {
		b.totalDrops++
		return false
	}
	b.count++
	return true
}

// DropsForCategory devuelve cuántos eventos han sido dropeados
// históricamente por esta category (suma a lo largo de toda la vida
// del proceso). Útil para métricas y debug.
func (e *EventEmitter) DropsForCategory(cat EventCategory) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if b, ok := e.buckets[cat]; ok {
		return b.totalDrops
	}
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// Queries de lectura
// ─────────────────────────────────────────────────────────────────────────────

const eventColumns = `
	id, operation_id, timestamp,
	category, event, target_id,
	level, message, details,
	occurrences, last_seen_at
`

func scanEvent(rs rowScanner) (*NetworkEvent, error) {
	var (
		e            NetworkEvent
		operationID  sql.NullString
		timestampStr string
		targetID     sql.NullString
		details      sql.NullString
		lastSeenStr  string
	)
	err := rs.Scan(
		&e.ID, &operationID, &timestampStr,
		&e.Category, &e.Event, &targetID,
		&e.Level, &e.Message, &details,
		&e.Occurrences, &lastSeenStr,
	)
	if err != nil {
		return nil, err
	}
	e.OperationID = ptrFromNullString(operationID)
	e.Timestamp = parseTime(timestampStr)
	e.TargetID = ptrFromNullString(targetID)
	if details.Valid {
		e.Details = json.RawMessage(details.String)
	}
	e.LastSeenAt = parseTime(lastSeenStr)
	return &e, nil
}

// ListEventsSince devuelve los eventos con timestamp >= since,
// ordenados de más reciente a más antiguo.
func (e *EventEmitter) ListEventsSince(ctx context.Context, since time.Time, limit int) ([]*NetworkEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT `+eventColumns+`
		FROM network_events
		WHERE timestamp >= ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("ListEventsSince: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// ListEventsByCategory filtra por category, ordenado por timestamp DESC.
func (e *EventEmitter) ListEventsByCategory(ctx context.Context, cat EventCategory, limit int) ([]*NetworkEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT `+eventColumns+`
		FROM network_events
		WHERE category = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, string(cat), limit)
	if err != nil {
		return nil, fmt.Errorf("ListEventsByCategory: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// ListEventsByOperation devuelve los eventos linkados a una operation_id.
func (e *EventEmitter) ListEventsByOperation(ctx context.Context, operationID string) ([]*NetworkEvent, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT `+eventColumns+`
		FROM network_events
		WHERE operation_id = ?
		ORDER BY timestamp ASC
	`, operationID)
	if err != nil {
		return nil, fmt.Errorf("ListEventsByOperation: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// CountEvents devuelve total de filas (útil para métricas).
func (e *EventEmitter) CountEvents(ctx context.Context) (int64, error) {
	var n int64
	err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_events`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountEvents: %w", err)
	}
	return n, nil
}

func collectEvents(rows *sql.Rows) ([]*NetworkEvent, error) {
	var out []*NetworkEvent
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// Retention (cron 03:00)
// ─────────────────────────────────────────────────────────────────────────────

// PruneEventsByRetention borra eventos según la política por nivel
// configurada en EventEmitterConfig.
//
// Política (defaults):
//   - error: > 90d
//   - warn:  > 30d
//   - info:  > 7d
//   - debug: > 24h
//
// Devuelve el total de filas borradas.
//
// IMPORTANT: cada nivel se procesa con su propio threshold. Un evento
// 'error' de hace 80 días NO se borra; uno de hace 100 días sí.
func (e *EventEmitter) PruneEventsByRetention(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	type levelRetention struct {
		level     EventLevel
		retention time.Duration
	}
	plan := []levelRetention{
		{EventLevelError, e.config.RetentionError},
		{EventLevelWarn, e.config.RetentionWarn},
		{EventLevelInfo, e.config.RetentionInfo},
		{EventLevelDebug, e.config.RetentionDebug},
	}

	var total int64
	for _, p := range plan {
		threshold := now.Add(-p.retention).UTC().Format(time.RFC3339)
		res, err := tx.ExecContext(ctx, `
			DELETE FROM network_events
			WHERE level = ? AND timestamp < ?
		`, string(p.level), threshold)
		if err != nil {
			return total, fmt.Errorf("PruneEventsByRetention level=%s: %w", p.level, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Aggregation (cron 03:00) — DISCIPLINE §4 antídoto C
// ─────────────────────────────────────────────────────────────────────────────

// AggregateRoutineEventsForDay comprime los eventos rutinarios (level
// debug e info) del día especificado en una fila por (category, event,
// day). Mantiene errors+warns intactos.
//
// "Day" = la fecha en UTC de `day` (se ignora la hora).
//
// El insert resultante es UN SOLO evento sintético por grupo, con:
//   - id      = UUID nuevo
//   - timestamp  = inicio del día (00:00 UTC)
//   - last_seen_at = momento del último evento del grupo (preservado)
//   - occurrences  = suma de occurrences del grupo
//   - message  = "Aggregated N events"
//   - details  = JSON con el primer/último timestamp y N originales
//
// Devuelve cuántas filas originales se compactaron (no cuántas filas
// nuevas se crearon).
//
// IMPORTANTE: solo se aplica a 'debug' e 'info'. Errors y warns nunca
// se comprimen — son ruido bajo y necesitamos detalle individual.
//
// Idempotencia: si ya hay un evento sintético del día anterior con
// el mismo (category, event), incrementamos su occurrences en vez de
// crear otro. Detectamos lo sintético por message LIKE 'Aggregated %'.
func (e *EventEmitter) AggregateRoutineEventsForDay(ctx context.Context, tx *sql.Tx, day time.Time) (compacted int64, err error) {
	startOfDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)

	rows, err := tx.QueryContext(ctx, `
		SELECT category, event, target_id, level,
		       MIN(timestamp) AS first_ts, MAX(last_seen_at) AS last_ts,
		       SUM(occurrences) AS total_occ,
		       COUNT(*) AS row_count
		FROM network_events
		WHERE level IN ('debug', 'info')
		  AND timestamp >= ?
		  AND timestamp < ?
		  AND message NOT LIKE 'Aggregated %'
		GROUP BY category, event, target_id, level
		HAVING row_count > 1
	`, startOfDay.Format(time.RFC3339), endOfDay.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("AggregateRoutineEvents query: %w", err)
	}

	type group struct {
		category, event, level string
		targetID               sql.NullString
		firstTs, lastTs        string
		totalOcc, rowCount     int64
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.category, &g.event, &g.targetID, &g.level,
			&g.firstTs, &g.lastTs, &g.totalOcc, &g.rowCount); err != nil {
			rows.Close()
			return 0, fmt.Errorf("AggregateRoutineEvents scan: %w", err)
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("AggregateRoutineEvents rows: %w", err)
	}

	for _, g := range groups {
		// Borrar las originales (CASCADE no aplica aquí, son borrados directos).
		var targetCondition string
		args := []interface{}{g.category, g.event, g.level,
			startOfDay.Format(time.RFC3339), endOfDay.Format(time.RFC3339)}
		if g.targetID.Valid {
			targetCondition = "AND target_id = ?"
			args = append(args, g.targetID.String)
		} else {
			targetCondition = "AND target_id IS NULL"
		}

		_, err := tx.ExecContext(ctx, `
			DELETE FROM network_events
			WHERE category = ? AND event = ? AND level = ?
			  AND timestamp >= ? AND timestamp < ?
			`+targetCondition+`
			  AND message NOT LIKE 'Aggregated %'
		`, args...)
		if err != nil {
			return compacted, fmt.Errorf("AggregateRoutineEvents delete: %w", err)
		}

		// Insertar evento sintético compactado.
		syntheticID := uuid.New().String()
		details, _ := json.Marshal(map[string]interface{}{
			"first_seen":        g.firstTs,
			"last_seen":         g.lastTs,
			"original_rows":     g.rowCount,
			"total_occurrences": g.totalOcc,
			"aggregated_at":     e.clock.Now().UTC().Format(time.RFC3339),
		})
		var targetArg interface{}
		if g.targetID.Valid {
			targetArg = g.targetID.String
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO network_events (
				id, timestamp, category, event, target_id,
				level, message, details, occurrences, last_seen_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			syntheticID, startOfDay.Format(time.RFC3339),
			g.category, g.event, targetArg,
			g.level, fmt.Sprintf("Aggregated %d events", g.rowCount), string(details),
			g.totalOcc, g.lastTs,
		)
		if err != nil {
			return compacted, fmt.Errorf("AggregateRoutineEvents insert synthetic: %w", err)
		}
		compacted += g.rowCount
	}
	return compacted, nil
}
