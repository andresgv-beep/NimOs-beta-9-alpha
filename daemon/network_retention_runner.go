// network_retention_runner.go — Runner periódico que ejecuta las purgas
// de retention del módulo network.
//
// F-008 entrega:
//   - Goroutine que corre 1 vez al día (configurable) y ejecuta:
//     1. PruneObservedSnapshots — poda network_observed.
//     2. PruneCompletedOperations — poda operations terminales >30d.
//     3. PruneEventsByRetention — poda network_events según TTL por nivel.
//
//   - Por qué un cron y no Reconciler: las purgas no son "reconciliar"
//     estado deseado/real. Son housekeeping puro. No encajan en el
//     patrón Reconciler. Un runner simple es más honesto.
//
//   - Por qué NO usar time.Tick(24h): los relojes pueden saltar (NTP,
//     suspend/resume, daylight saving). Mejor calcular cuándo es la
//     próxima ventana 03:00 UTC y dormir hasta entonces.
//
// Filosofía:
//   - El runner NO bloquea boot. Se inicia y dispara su primera pasada
//     en el siguiente 03:00. No corre en boot porque puede ser caro y
//     queremos que el daemon esté responsive antes.
//
//   - Si una pasada falla, log warn y continuar — la siguiente pasada
//     intenta de nuevo.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// RetentionRunnerConfig agrupa parámetros del runner.
type RetentionRunnerConfig struct {
	// RunHourUTC: hora del día (0-23) en que dispara la purga. Default 3
	// (03:00 UTC, ventana low-traffic global).
	RunHourUTC int

	// CompletedOperationsTTL: edad mínima a partir de la cual una
	// operation terminada se borra. Default 30 días.
	CompletedOperationsTTL time.Duration
}

// DefaultRetentionRunnerConfig.
func DefaultRetentionRunnerConfig() RetentionRunnerConfig {
	return RetentionRunnerConfig{
		RunHourUTC:             3,
		CompletedOperationsTTL: 30 * 24 * time.Hour,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Runner
// ─────────────────────────────────────────────────────────────────────────────

// RetentionRunner ejecuta las purgas en una goroutine de fondo.
type RetentionRunner struct {
	repo    *NetworkRepo
	emitter *EventEmitter
	clock   Clock
	config  RetentionRunnerConfig

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}
}

// NewRetentionRunner construye el runner.
func NewRetentionRunner(repo *NetworkRepo, emitter *EventEmitter, clock Clock, config RetentionRunnerConfig) (*RetentionRunner, error) {
	if repo == nil {
		return nil, fmt.Errorf("NewRetentionRunner: repo is nil")
	}
	if emitter == nil {
		return nil, fmt.Errorf("NewRetentionRunner: emitter is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	defaults := DefaultRetentionRunnerConfig()
	// Tratar 0 como "no especificado" (convención Go zero-value).
	// Esto impide configurar medianoche explícitamente, pero es coherente
	// con el resto del módulo. Si en
	// futuro aparece necesidad de medianoche real, cambiar a *int.
	if config.RunHourUTC == 0 || config.RunHourUTC < 0 || config.RunHourUTC > 23 {
		config.RunHourUTC = defaults.RunHourUTC
	}
	if config.CompletedOperationsTTL == 0 {
		config.CompletedOperationsTTL = defaults.CompletedOperationsTTL
	}
	return &RetentionRunner{
		repo:    repo,
		emitter: emitter,
		clock:   clock,
		config:  config,
	}, nil
}

// Start arranca la goroutine. Idempotente: si ya está arrancada, no-op.
func (r *RetentionRunner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	r.mu.Unlock()

	go r.run(ctx)
}

// Stop señaliza al goroutine que termine y espera a que acabe.
// Idempotente: si nunca arrancó, no-op.
func (r *RetentionRunner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	close(r.stop)
	done := r.done
	r.started = false
	r.mu.Unlock()
	<-done
}

// run es el loop principal. Calcula el próximo 03:00 UTC, duerme hasta
// allí, ejecuta RunOnce, repite.
func (r *RetentionRunner) run(ctx context.Context) {
	defer close(r.done)

	for {
		now := r.clock.Now().UTC()
		next := r.nextRunTime(now)
		wait := next.Sub(now)

		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-time.After(wait):
		}

		if err := r.RunOnce(ctx); err != nil {
			logMsg("retention runner: pass failed: %v", err)
		}
	}
}

// nextRunTime devuelve la próxima instancia de RunHourUTC. Si ya pasó
// hoy, devuelve mañana a esa hora.
func (r *RetentionRunner) nextRunTime(now time.Time) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(),
		r.config.RunHourUTC, 0, 0, 0, time.UTC)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// ─────────────────────────────────────────────────────────────────────────────
// RunOnce — ejecuta todas las purgas en una pasada
// ─────────────────────────────────────────────────────────────────────────────

// RunOnce ejecuta las tres purgas. Cada una en su propia transacción
// para que un fallo en una no rollbackee las demás. Devuelve el primer
// error encontrado pero ejecuta todas igualmente — queremos máxima
// limpieza posible.
//
// Puede llamarse directamente desde código (e.g. tests) o por endpoint
// de admin futuro.
func (r *RetentionRunner) RunOnce(ctx context.Context) error {
	now := r.clock.Now().UTC()
	var firstErr error
	var totalDeleted int64

	// 1. Observed snapshots.
	if n, err := r.runInTx(ctx, func(tx *sql.Tx) (int64, error) {
		return r.repo.PruneObservedSnapshots(ctx, tx, now)
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune observed: %w", err)
		}
		logMsg("retention runner: PruneObservedSnapshots: %v", err)
	} else {
		totalDeleted += n
	}

	// 2. Completed operations.
	if n, err := r.runInTx(ctx, func(tx *sql.Tx) (int64, error) {
		return r.repo.PruneCompletedOperations(ctx, tx, now.Add(-r.config.CompletedOperationsTTL))
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune operations: %w", err)
		}
		logMsg("retention runner: PruneCompletedOperations: %v", err)
	} else {
		totalDeleted += n
	}

	// 3. Events por nivel.
	if n, err := r.runInTx(ctx, func(tx *sql.Tx) (int64, error) {
		return r.emitter.PruneEventsByRetention(ctx, tx, now)
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune events: %w", err)
		}
		logMsg("retention runner: PruneEventsByRetention: %v", err)
	} else {
		totalDeleted += n
	}

	// Emitir evento de auditoría si hubo trabajo (level debug — no
	// queremos spamear info cada noche).
	if totalDeleted > 0 {
		_, _ = r.emitter.Emit(ctx, EventInput{
			Category: CategoryObserver,
			Event:    "retention_pass_completed",
			Level:    EventLevelDebug,
			Message:  fmt.Sprintf("Retention pass deleted %d rows", totalDeleted),
		})
	}

	return firstErr
}

// runInTx wrapper sencillo para llamar prune-funcs que devuelven (count, err).
func (r *RetentionRunner) runInTx(ctx context.Context, fn func(tx *sql.Tx) (int64, error)) (int64, error) {
	tx, err := r.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	n, err := fn(tx)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
