package main

import (
	"context"
	"testing"
)

// --- tareas de prueba ---

type panicTask struct{}

func (panicTask) ID() string                            { return "test_panic" }
func (panicTask) Name() string                          { return "panic" }
func (panicTask) Description() string                   { return "" }
func (panicTask) Category() string                      { return MaintCategoryGeneral }
func (panicTask) DefaultSchedule() Schedule             { return Schedule{Kind: ScheduleInterval, IntervalMinutes: 60} }
func (panicTask) Run(ctx context.Context) MaintenanceResult {
	panic("boom")
}

type okTask struct{ ran *bool }

func (okTask) ID() string                { return "test_ok" }
func (okTask) Name() string              { return "ok" }
func (okTask) Description() string       { return "" }
func (okTask) Category() string          { return MaintCategoryGeneral }
func (okTask) DefaultSchedule() Schedule { return Schedule{Kind: ScheduleAtBoot} }
func (t okTask) Run(ctx context.Context) MaintenanceResult {
	if t.ran != nil {
		*t.ran = true
	}
	return MaintenanceResult{ItemsRemoved: 3, BytesFreed: 1024}
}

// TestManager_RecoverFromPanic — REGLA CLAVE: una tarea que entra en pánico NO
// debe tumbar el manager; runWithRecover lo convierte en error.
func TestManager_RecoverFromPanic(t *testing.T) {
	m := &MaintenanceManager{tasks: map[string]MaintenanceTask{}}
	// Register persiste config en DB; aquí solo metemos la tarea en el mapa para
	// no depender de la DB en este test unitario puro.
	m.tasks["test_panic"] = panicTask{}

	res := m.runWithRecover(panicTask{})
	if res.Err == nil {
		t.Fatal("se esperaba error tras el pánico, got nil")
	}
	// El manager sigue vivo: podemos ejecutar otra cosa.
	res2 := m.runWithRecover(okTask{})
	if res2.Err != nil {
		t.Errorf("manager debería seguir funcional tras un pánico, got: %v", res2.Err)
	}
}

// TestManager_RunResult — una tarea normal devuelve sus métricas.
func TestManager_RunResult(t *testing.T) {
	m := &MaintenanceManager{tasks: map[string]MaintenanceTask{}}
	ran := false
	res := m.runWithRecover(okTask{ran: &ran})
	if !ran {
		t.Error("la tarea no se ejecutó")
	}
	if res.ItemsRemoved != 3 || res.BytesFreed != 1024 {
		t.Errorf("métricas inesperadas: items=%d bytes=%d", res.ItemsRemoved, res.BytesFreed)
	}
	if res.duration <= 0 {
		t.Error("la duración debería medirse")
	}
}

// TestManager_GetList — registro y consulta en memoria.
func TestManager_GetList(t *testing.T) {
	m := &MaintenanceManager{tasks: map[string]MaintenanceTask{}}
	m.tasks["test_ok"] = okTask{}
	if _, ok := m.Get("test_ok"); !ok {
		t.Error("Get debería encontrar la tarea registrada")
	}
	if _, ok := m.Get("inexistente"); ok {
		t.Error("Get no debería encontrar una tarea inexistente")
	}
	if len(m.List()) != 1 {
		t.Errorf("List debería devolver 1 tarea, got %d", len(m.List()))
	}
}

// TestTorrentSweep_Schedule — la primera tarea declara su default coherente.
func TestTorrentSweep_Schedule(t *testing.T) {
	task := &torrentTmpSweepTask{}
	s := task.DefaultSchedule()
	if s.Kind != ScheduleInterval || s.IntervalMinutes != 360 || s.GraceMinutes != 15 {
		t.Errorf("schedule por defecto inesperado: %+v", s)
	}
	if task.ID() != "torrent_tmp_sweep" {
		t.Errorf("ID inesperado: %s", task.ID())
	}
}
