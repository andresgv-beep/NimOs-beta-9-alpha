// maintenance.go — Subsistema de Limpieza y Mantenimiento (núcleo, Fase 1).
//
// Da coherencia a la higiene del sistema (hoy dispersa) bajo un manager central.
// Ver NimOS-Mantenimiento-Limpieza-v1.md para el diseño completo.
//
// LÍNEA ROJA: este subsistema JAMÁS toca datos de usuario. Solo temporales,
// cachés, logs, huérfanos y registros internos del sistema.
//
// Fase 1 (este archivo): interface, registro, ejecutor con recover, persistencia
// (config + history). SIN scheduler todavía — solo ejecución manual vía API.

package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ── Contrato ────────────────────────────────────────────────────────────────

// MaintenanceTask es el contrato que cumple toda tarea de mantenimiento.
// Implementaciones en archivos propios (p.ej. maintenance_torrent.go).
type MaintenanceTask interface {
	ID() string          // único y estable, p.ej. "torrent_tmp_sweep"
	Name() string        // legible para la UI
	Description() string // qué hace, legible para la UI
	// Category agrupa la tarea en la UI (p.ej. "Docker", "Almacenamiento").
	// Usar las constantes MaintCategory*. Tareas sin grupo claro → MaintCategoryGeneral.
	Category() string
	// Run ejecuta la tarea. DEBE cumplir las 4 reglas de seguridad (refuse-if-
	// uncertain, skip-known, grace-period, log-everything) y NUNCA entrar en
	// pánico — el manager además lo protege con recover por si acaso.
	Run(ctx context.Context) MaintenanceResult
	DefaultSchedule() Schedule
}

// Categorías de mantenimiento · agrupan tareas afines en la UI (subcategorías).
// Son strings legibles (se muestran tal cual); usar las constantes evita typos.
const (
	MaintCategoryDocker  = "Docker"         // imágenes, redes, higiene de apps Docker
	MaintCategoryStorage = "Almacenamiento" // directorios huérfanos, temporales
	MaintCategoryGeneral = "General"        // sin grupo específico
)

// MaintenanceResult son las métricas/resultado de una ejecución.
type MaintenanceResult struct {
	ItemsRemoved int64  // ficheros/filas/dirs borrados
	BytesFreed   int64  // espacio liberado (0 si no aplica)
	Skipped      bool   // true si un guard decidió NO actuar
	SkipReason   string // por qué se saltó
	Err          error  // error si falló
}

// ScheduleKind enumera los modelos de programación (el scheduler llega en Fase 3;
// aquí solo se persiste la preferencia).
type ScheduleKind string

const (
	ScheduleInterval ScheduleKind = "interval"
	ScheduleDaily    ScheduleKind = "daily"
	ScheduleWeekly   ScheduleKind = "weekly"
	ScheduleAtBoot   ScheduleKind = "at_boot"
)

// Schedule describe cuándo corre una tarea.
type Schedule struct {
	Kind            ScheduleKind `json:"kind"`
	IntervalMinutes int          `json:"intervalMinutes,omitempty"` // interval
	AtHour          int          `json:"atHour,omitempty"`          // daily/weekly 0-23
	AtMinute        int          `json:"atMinute,omitempty"`        // 0-59
	AtWeekday       int          `json:"atWeekday,omitempty"`       // weekly 0=domingo
	GraceMinutes    int          `json:"graceMinutes,omitempty"`
	RetentionDays   int          `json:"retentionDays,omitempty"`
}

// TaskConfig es la config persistida y editable por el usuario.
type TaskConfig struct {
	TaskID   string   `json:"taskId"`
	Enabled  bool     `json:"enabled"`
	Schedule Schedule `json:"schedule"`
}

// ── Manager ─────────────────────────────────────────────────────────────────

// MaintenanceManager registra y ejecuta tareas. Thread-safe.
type MaintenanceManager struct {
	mu    sync.RWMutex
	tasks map[string]MaintenanceTask
}

var maintenanceManager = &MaintenanceManager{
	tasks: map[string]MaintenanceTask{},
}

// Register añade una tarea al manager y persiste su config por defecto si aún no
// existe. Idempotente. Llamado al arrancar por cada tarea.
func (m *MaintenanceManager) Register(t MaintenanceTask) {
	m.mu.Lock()
	m.tasks[t.ID()] = t
	m.mu.Unlock()
	// Sembrar config por defecto si no hay fila aún (no pisa la del usuario).
	dbMaintenanceSeedConfig(t.ID(), t.DefaultSchedule())
}

// Get devuelve una tarea por ID.
func (m *MaintenanceManager) Get(id string) (MaintenanceTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	return t, ok
}

// List devuelve todas las tareas registradas, ordenadas por ID de forma
// estable. IMPORTANTE: iterar el map directamente da orden ALEATORIO en cada
// llamada (Go), lo que hacía que la UI rebarajara las tarjetas en cada refresco.
// Ordenar aquí garantiza orden consistente para todos los consumidores.
func (m *MaintenanceManager) List() []MaintenanceTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MaintenanceTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// RunTask ejecuta una tarea por ID, con recover y timeout, y registra el
// resultado en el historial. Devuelve el resultado o un error si la tarea no
// existe. NO comprueba 'enabled' (la ejecución manual desde la API es válida
// aunque la tarea esté deshabilitada para el scheduler).
func (m *MaintenanceManager) RunTask(id string) (MaintenanceResult, error) {
	t, ok := m.Get(id)
	if !ok {
		return MaintenanceResult{}, fmt.Errorf("tarea desconocida: %s", id)
	}
	res := m.runWithRecover(t)
	dbMaintenanceRecord(id, res, res.duration)
	return res.MaintenanceResult, nil
}

// timedResult envuelve el resultado con la duración medida.
type timedResult struct {
	MaintenanceResult
	duration time.Duration
}

// runWithRecover ejecuta t.Run protegido: un pánico se convierte en error y NO
// tumba el manager ni otras tareas. Aplica un timeout defensivo.
func (m *MaintenanceManager) runWithRecover(t MaintenanceTask) (res timedResult) {
	start := time.Now()
	defer func() {
		res.duration = time.Since(start)
		if r := recover(); r != nil {
			logMsg("maintenance: PÁNICO en tarea %s: %v", t.ID(), r)
			res.MaintenanceResult = MaintenanceResult{
				Err: fmt.Errorf("panic: %v", r),
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	res.MaintenanceResult = t.Run(ctx)
	return res
}

// startMaintenance inicializa las tablas. Llamado una vez al arrancar, ANTES de
// registrar tareas. El scheduler se añade en Fase 3.
func startMaintenance() {
	dbMaintenanceInit()
	logMsg("maintenance: subsistema inicializado (Fase 1: ejecución manual)")
}
