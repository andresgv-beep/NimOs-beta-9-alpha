// maintenance_scheduler.go — Scheduler del subsistema de mantenimiento (Fase 3).
//
// Un único goroutine "tick" evalúa cada minuto qué tareas toca ejecutar según su
// config (interval / daily / weekly / at_boot). Ejecución SECUENCIAL: una tarea a
// la vez, para no machacar el disco con varios sweeps simultáneos.
//
// Fuente de verdad de "última ejecución": la fila más reciente en
// maintenance_history (no se añaden columnas). El scheduler NO ejecuta tareas
// deshabilitadas (enabled=0); la ejecución manual por API sigue funcionando
// aparte (RunTask no mira enabled).

package main

import (
	"sync"
	"time"
)

// maintenanceSchedulerOnce evita arrancar el scheduler dos veces.
var maintenanceSchedulerOnce sync.Once

// startMaintenanceScheduler arranca el tick en background. Idempotente.
func startMaintenanceScheduler() {
	maintenanceSchedulerOnce.Do(func() {
		go maintenanceTickLoop()
		logMsg("maintenance: scheduler iniciado (tick 1 min, ejecución secuencial)")
	})
}

// maintenanceTickLoop evalúa las tareas cada minuto.
func maintenanceTickLoop() {
	// Primer barrido de tareas at_boot poco después de arrancar (da margen a que
	// storage/servicios estén listos).
	time.Sleep(90 * time.Second)
	runDueTasks(time.Now(), true)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		runDueTasks(now, false)
	}
}

// runDueTasks recorre las tareas registradas y ejecuta las que toquen. Secuencial.
// includeAtBoot solo es true en el primer barrido tras arrancar.
func runDueTasks(now time.Time, includeAtBoot bool) {
	for _, t := range maintenanceManager.List() {
		cfg := dbMaintenanceGetConfig(t.ID())
		if !cfg.Enabled {
			continue
		}
		last := dbMaintenanceLastRun(t.ID())
		// last viene en UTC (datetime('now')); el usuario configura horas en local.
		// interval es inmune (resta de duraciones); daily/weekly comparan en local.
		if !last.IsZero() {
			last = last.Local()
		}
		if taskIsDue(cfg.Schedule, last, now, includeAtBoot) {
			// RunTask ya hace recover + registra en history (que actualiza "last").
			res, _ := maintenanceManager.RunTask(t.ID())
			if res.Skipped {
				logMsg("maintenance: %s saltada por scheduler (%s)", t.ID(), res.SkipReason)
			} else {
				logMsg("maintenance: %s ejecutada por scheduler (items=%d)", t.ID(), res.ItemsRemoved)
			}
		}
	}
}

// taskIsDue decide si una tarea debe ejecutarse ahora.
//   - interval: si ha pasado >= IntervalMinutes desde la última ejecución.
//   - daily:    si la hora/minuto actual coincide y no se ejecutó ya hoy.
//   - weekly:   si el día de la semana + hora/minuto coinciden y no se ejecutó hoy.
//   - at_boot:  solo en el barrido inicial (includeAtBoot).
//
// `last` es la última ejecución (zero si nunca corrió).
func taskIsDue(s Schedule, last time.Time, now time.Time, includeAtBoot bool) bool {
	switch s.Kind {
	case ScheduleAtBoot:
		return includeAtBoot

	case ScheduleInterval:
		mins := s.IntervalMinutes
		if mins <= 0 {
			mins = 360 // default defensivo: 6h
		}
		if last.IsZero() {
			return true // nunca corrió → ejecútala ya
		}
		return now.Sub(last) >= time.Duration(mins)*time.Minute

	case ScheduleDaily:
		if now.Hour() != s.AtHour || now.Minute() != s.AtMinute {
			return false
		}
		return !sameDay(last, now) // no repetir si ya corrió hoy

	case ScheduleWeekly:
		if int(now.Weekday()) != s.AtWeekday {
			return false
		}
		if now.Hour() != s.AtHour || now.Minute() != s.AtMinute {
			return false
		}
		return !sameDay(last, now)

	default:
		return false
	}
}

// sameDay reporta si dos instantes caen el mismo día natural.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// nextRunEstimate calcula una estimación legible de la próxima ejecución de una
// tarea, para mostrarla en la UI. Devuelve "" si está deshabilitada o no aplica.
func nextRunEstimate(s Schedule, last time.Time, enabled bool, now time.Time) string {
	if !enabled {
		return ""
	}
	if !last.IsZero() {
		last = last.Local()
	}
	switch s.Kind {
	case ScheduleAtBoot:
		return "al próximo arranque"
	case ScheduleInterval:
		mins := s.IntervalMinutes
		if mins <= 0 {
			mins = 360
		}
		if last.IsZero() {
			return "pendiente (en el próximo tick)"
		}
		next := last.Add(time.Duration(mins) * time.Minute)
		if next.Before(now) {
			return "pendiente (en el próximo tick)"
		}
		return next.Format("2006-01-02 15:04")
	case ScheduleDaily:
		next := nextDailyOccurrence(s.AtHour, s.AtMinute, now)
		return next.Format("2006-01-02 15:04")
	case ScheduleWeekly:
		next := nextWeeklyOccurrence(s.AtWeekday, s.AtHour, s.AtMinute, now)
		return next.Format("2006-01-02 15:04")
	default:
		return ""
	}
}

// nextDailyOccurrence devuelve el próximo instante hoy/mañana a hh:mm.
func nextDailyOccurrence(hour, minute int, now time.Time) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// nextWeeklyOccurrence devuelve el próximo instante en el weekday dado a hh:mm.
func nextWeeklyOccurrence(weekday, hour, minute int, now time.Time) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	daysAhead := (weekday - int(now.Weekday()) + 7) % 7
	candidate = candidate.AddDate(0, 0, daysAhead)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}
