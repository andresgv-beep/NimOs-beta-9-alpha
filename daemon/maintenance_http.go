// maintenance_http.go — API REST del subsistema de mantenimiento.
//
// Rutas (mutaciones admin-only, consistente con NimShield/Panel):
//   GET  /api/maintenance/tasks          lista + estado + config
//   GET  /api/maintenance/tasks/:id      detalle + últimas ejecuciones
//   PUT  /api/maintenance/tasks/:id      actualizar config
//   POST /api/maintenance/tasks/:id/run  ejecutar ahora
//   GET  /api/maintenance/history        historial paginado

package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// taskView es la representación de una tarea para la API (tarea + su config).
type taskView struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Config      TaskConfig `json:"config"`
	LastRun     string     `json:"lastRun,omitempty"`
	NextRun     string     `json:"nextRun,omitempty"`
}

// buildTaskView arma la vista de una tarea incluyendo última/próxima ejecución.
func buildTaskView(t MaintenanceTask) taskView {
	cfg := dbMaintenanceGetConfig(t.ID())
	last := dbMaintenanceLastRun(t.ID())
	lastStr := ""
	if !last.IsZero() {
		lastStr = last.Local().Format("2006-01-02 15:04")
	}
	return taskView{
		ID:          t.ID(),
		Name:        t.Name(),
		Description: t.Description(),
		Category:    t.Category(),
		Config:      cfg,
		LastRun:     lastStr,
		NextRun:     nextRunEstimate(cfg.Schedule, last, cfg.Enabled, time.Now()),
	}
}

func handleMaintenanceRoutes(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	method := r.Method

	// GET /api/maintenance/history
	if urlPath == "/api/maintenance/history" && method == "GET" {
		if requireAuth(w, r) == nil {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		taskID := r.URL.Query().Get("taskId")
		jsonOk(w, map[string]interface{}{"history": dbMaintenanceHistory(taskID, limit)})
		return
	}

	// GET /api/maintenance/tasks
	if urlPath == "/api/maintenance/tasks" && method == "GET" {
		if requireAuth(w, r) == nil {
			return
		}
		views := []taskView{}
		for _, t := range maintenanceManager.List() {
			views = append(views, buildTaskView(t))
		}
		jsonOk(w, map[string]interface{}{"tasks": views})
		return
	}

	// /api/maintenance/tasks/:id  y  /api/maintenance/tasks/:id/run
	if strings.HasPrefix(urlPath, "/api/maintenance/tasks/") {
		rest := strings.TrimPrefix(urlPath, "/api/maintenance/tasks/")
		rest = strings.Trim(rest, "/")

		// POST .../:id/run
		if strings.HasSuffix(rest, "/run") && method == "POST" {
			if requireAdmin(w, r) == nil {
				return
			}
			id := strings.TrimSuffix(rest, "/run")
			res, err := maintenanceManager.RunTask(id)
			if err != nil {
				jsonError(w, 404, err.Error())
				return
			}
			out := map[string]interface{}{
				"ok":           true,
				"itemsRemoved": res.ItemsRemoved,
				"bytesFreed":   res.BytesFreed,
				"skipped":      res.Skipped,
			}
			if res.SkipReason != "" {
				out["skipReason"] = res.SkipReason
			}
			if res.Err != nil {
				out["error"] = res.Err.Error()
			}
			jsonOk(w, out)
			return
		}

		id := rest

		// GET .../:id
		if method == "GET" {
			if requireAuth(w, r) == nil {
				return
			}
			t, ok := maintenanceManager.Get(id)
			if !ok {
				jsonError(w, 404, "tarea desconocida")
				return
			}
			jsonOk(w, map[string]interface{}{
				"task":    buildTaskView(t),
				"history": dbMaintenanceHistory(id, 20),
			})
			return
		}

		// PUT .../:id
		if method == "PUT" {
			if requireAdmin(w, r) == nil {
				return
			}
			if _, ok := maintenanceManager.Get(id); !ok {
				jsonError(w, 404, "tarea desconocida")
				return
			}
			body, _ := readBody(r)
			cfg := dbMaintenanceGetConfig(id) // base actual
			cfg.TaskID = id
			if v, ok := body["enabled"].(bool); ok {
				cfg.Enabled = v
			}
			if sched, ok := body["schedule"].(map[string]interface{}); ok {
				cfg.Schedule = parseSchedule(sched, cfg.Schedule)
			}
			if err := dbMaintenanceUpdateConfig(cfg); err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true, "config": cfg})
			return
		}
	}

	jsonError(w, 404, "not found")
}

// parseSchedule fusiona los campos presentes en el body sobre la base actual.
func parseSchedule(m map[string]interface{}, base Schedule) Schedule {
	if v, ok := m["kind"].(string); ok {
		base.Kind = ScheduleKind(v)
	}
	base.IntervalMinutes = jsonInt(m, "intervalMinutes", base.IntervalMinutes)
	base.AtHour = jsonInt(m, "atHour", base.AtHour)
	base.AtMinute = jsonInt(m, "atMinute", base.AtMinute)
	base.AtWeekday = jsonInt(m, "atWeekday", base.AtWeekday)
	base.GraceMinutes = jsonInt(m, "graceMinutes", base.GraceMinutes)
	base.RetentionDays = jsonInt(m, "retentionDays", base.RetentionDays)
	return base
}

// jsonInt extrae un int de un map JSON (los números llegan como float64).
func jsonInt(m map[string]interface{}, key string, def int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}
