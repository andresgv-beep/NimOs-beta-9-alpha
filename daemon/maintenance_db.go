// maintenance_db.go — Persistencia del subsistema de mantenimiento.
//
// Tablas: maintenance_config (config editable por usuario) y maintenance_history
// (auditoría de ejecuciones). Ver diseño en NimOS-Mantenimiento-Limpieza-v1.md.

package main

import (
	"time"
)

// dbMaintenanceInit crea las tablas si no existen.
func dbMaintenanceInit() {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS maintenance_config (
			task_id          TEXT PRIMARY KEY,
			enabled          INTEGER NOT NULL DEFAULT 1,
			schedule_kind    TEXT NOT NULL DEFAULT 'interval',
			interval_minutes INTEGER,
			at_hour          INTEGER,
			at_minute        INTEGER,
			at_weekday       INTEGER,
			grace_minutes    INTEGER,
			retention_days   INTEGER,
			updated_at       TEXT DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS maintenance_history (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id       TEXT NOT NULL,
			ran_at        TEXT NOT NULL DEFAULT (datetime('now')),
			items_removed INTEGER DEFAULT 0,
			bytes_freed   INTEGER DEFAULT 0,
			skipped       INTEGER DEFAULT 0,
			skip_reason   TEXT,
			error         TEXT,
			duration_ms   INTEGER
		);

		CREATE INDEX IF NOT EXISTS idx_maint_history_task ON maintenance_history(task_id, ran_at);
	`)
	if err != nil {
		logMsg("maintenance: DB init error: %v", err)
	}
}

// dbMaintenanceSeedConfig inserta la config por defecto de una tarea SOLO si no
// existe ya una fila (no pisa la del usuario). Idempotente.
func dbMaintenanceSeedConfig(taskID string, s Schedule) {
	var exists int
	_ = db.QueryRow(`SELECT 1 FROM maintenance_config WHERE task_id = ?`, taskID).Scan(&exists)
	if exists == 1 {
		return
	}
	_, err := db.Exec(`
		INSERT INTO maintenance_config
			(task_id, enabled, schedule_kind, interval_minutes, at_hour, at_minute, at_weekday, grace_minutes, retention_days)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, string(s.Kind), nullZero(s.IntervalMinutes), nullZero(s.AtHour),
		nullZero(s.AtMinute), nullZero(s.AtWeekday), nullZero(s.GraceMinutes), nullZero(s.RetentionDays),
	)
	if err != nil {
		logMsg("maintenance: seed config %s error: %v", taskID, err)
	}
}

// dbMaintenanceGetConfig lee la config de una tarea. Si no hay fila, devuelve
// enabled=true y schedule vacío (defensivo).
func dbMaintenanceGetConfig(taskID string) TaskConfig {
	cfg := TaskConfig{TaskID: taskID, Enabled: true}
	var enabled int
	var kind string
	var iv, ah, am, aw, gm, rd *int
	err := db.QueryRow(`
		SELECT enabled, schedule_kind, interval_minutes, at_hour, at_minute, at_weekday, grace_minutes, retention_days
		FROM maintenance_config WHERE task_id = ?`, taskID).
		Scan(&enabled, &kind, &iv, &ah, &am, &aw, &gm, &rd)
	if err != nil {
		return cfg
	}
	cfg.Enabled = enabled == 1
	cfg.Schedule = Schedule{
		Kind:            ScheduleKind(kind),
		IntervalMinutes: deref(iv),
		AtHour:          deref(ah),
		AtMinute:        deref(am),
		AtWeekday:       deref(aw),
		GraceMinutes:    deref(gm),
		RetentionDays:   deref(rd),
	}
	return cfg
}

// dbMaintenanceUpdateConfig actualiza la config de una tarea (desde la API/UI).
func dbMaintenanceUpdateConfig(cfg TaskConfig) error {
	s := cfg.Schedule
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	_, err := db.Exec(`
		UPDATE maintenance_config SET
			enabled = ?, schedule_kind = ?, interval_minutes = ?, at_hour = ?,
			at_minute = ?, at_weekday = ?, grace_minutes = ?, retention_days = ?,
			updated_at = datetime('now')
		WHERE task_id = ?`,
		enabled, string(s.Kind), nullZero(s.IntervalMinutes), nullZero(s.AtHour),
		nullZero(s.AtMinute), nullZero(s.AtWeekday), nullZero(s.GraceMinutes),
		nullZero(s.RetentionDays), cfg.TaskID,
	)
	return err
}

// dbMaintenanceRecord guarda una ejecución en el historial.
func dbMaintenanceRecord(taskID string, res timedResult, dur time.Duration) {
	skipped := 0
	if res.Skipped {
		skipped = 1
	}
	var errStr string
	if res.Err != nil {
		errStr = res.Err.Error()
	}
	_, err := db.Exec(`
		INSERT INTO maintenance_history
			(task_id, items_removed, bytes_freed, skipped, skip_reason, error, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, res.ItemsRemoved, res.BytesFreed, skipped, res.SkipReason, errStr, dur.Milliseconds(),
	)
	if err != nil {
		logMsg("maintenance: record history %s error: %v", taskID, err)
	}
}

// MaintenanceHistoryEntry es una fila del historial para la API.
type MaintenanceHistoryEntry struct {
	TaskID       string `json:"taskId"`
	RanAt        string `json:"ranAt"`
	ItemsRemoved int64  `json:"itemsRemoved"`
	BytesFreed   int64  `json:"bytesFreed"`
	Skipped      bool   `json:"skipped"`
	SkipReason   string `json:"skipReason,omitempty"`
	Error        string `json:"error,omitempty"`
	DurationMs   int64  `json:"durationMs"`
}

// dbMaintenanceHistory devuelve las últimas n entradas (todas las tareas o una).
func dbMaintenanceHistory(taskID string, limit int) []MaintenanceHistoryEntry {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows interface {
		Next() bool
		Scan(...interface{}) error
		Close() error
	}
	var err error
	if taskID == "" {
		rows, err = db.Query(`
			SELECT task_id, ran_at, items_removed, bytes_freed, skipped, skip_reason, error, duration_ms
			FROM maintenance_history ORDER BY ran_at DESC, id DESC LIMIT ?`, limit)
	} else {
		rows, err = db.Query(`
			SELECT task_id, ran_at, items_removed, bytes_freed, skipped, skip_reason, error, duration_ms
			FROM maintenance_history WHERE task_id = ? ORDER BY ran_at DESC, id DESC LIMIT ?`, taskID, limit)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []MaintenanceHistoryEntry{}
	for rows.Next() {
		var e MaintenanceHistoryEntry
		var skipped int
		var skipReason, errStr *string
		if err := rows.Scan(&e.TaskID, &e.RanAt, &e.ItemsRemoved, &e.BytesFreed,
			&skipped, &skipReason, &errStr, &e.DurationMs); err != nil {
			continue
		}
		e.Skipped = skipped == 1
		if skipReason != nil {
			e.SkipReason = *skipReason
		}
		if errStr != nil {
			e.Error = *errStr
		}
		out = append(out, e)
	}
	return out
}

// ── helpers ──

// nullZero devuelve nil si v==0 (para guardar NULL en vez de 0 en columnas
// opcionales), o el valor en otro caso.
func nullZero(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

// deref devuelve el valor de un *int o 0 si es nil.
func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// dbMaintenanceLastRun devuelve el instante de la última ejecución NO saltada de
// una tarea (la última vez que realmente hizo trabajo o falló), o el zero time si
// nunca corrió. Lo usa el scheduler para calcular si toca ejecutar de nuevo.
//
// Se cuentan también las ejecuciones con error y las saltadas: si una tarea se
// saltó (p.ej. no había pool), igualmente "se intentó" en ese momento, y no
// queremos reintentar en bucle cada minuto. Por eso miramos el último ran_at sin
// filtrar por skipped.
func dbMaintenanceLastRun(taskID string) time.Time {
	var ranAt string
	err := db.QueryRow(
		`SELECT ran_at FROM maintenance_history WHERE task_id = ? ORDER BY ran_at DESC, id DESC LIMIT 1`,
		taskID,
	).Scan(&ranAt)
	if err != nil {
		return time.Time{}
	}
	// ran_at se guarda como datetime('now') → UTC "YYYY-MM-DD HH:MM:SS".
	t, perr := time.Parse("2006-01-02 15:04:05", ranAt)
	if perr != nil {
		return time.Time{}
	}
	return t.UTC()
}
