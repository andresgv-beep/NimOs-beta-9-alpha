package main

import (
	"testing"
	"time"
)

func TestTaskIsDue_Interval(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.Local)
	s := Schedule{Kind: ScheduleInterval, IntervalMinutes: 60}

	// Nunca ejecutada → toca.
	if !taskIsDue(s, time.Time{}, now, false) {
		t.Error("interval nunca ejecutada debería tocar")
	}
	// Ejecutada hace 30 min, intervalo 60 → NO toca.
	if taskIsDue(s, now.Add(-30*time.Minute), now, false) {
		t.Error("interval a la mitad no debería tocar")
	}
	// Ejecutada hace 61 min → toca.
	if !taskIsDue(s, now.Add(-61*time.Minute), now, false) {
		t.Error("interval pasado debería tocar")
	}
}

func TestTaskIsDue_Daily(t *testing.T) {
	s := Schedule{Kind: ScheduleDaily, AtHour: 4, AtMinute: 0}
	atTime := time.Date(2026, 6, 11, 4, 0, 0, 0, time.Local)
	offTime := time.Date(2026, 6, 11, 4, 1, 0, 0, time.Local)

	// A las 4:00 exactas, sin ejecución previa → toca.
	if !taskIsDue(s, time.Time{}, atTime, false) {
		t.Error("daily a la hora exacta debería tocar")
	}
	// A las 4:01 → no toca (solo en el minuto exacto).
	if taskIsDue(s, time.Time{}, offTime, false) {
		t.Error("daily fuera del minuto no debería tocar")
	}
	// A las 4:00 pero ya ejecutada hoy a las 4:00 → no repite.
	if taskIsDue(s, atTime, atTime, false) {
		t.Error("daily ya ejecutada hoy no debería repetir")
	}
	// A las 4:00 ejecutada AYER → toca.
	if !taskIsDue(s, atTime.AddDate(0, 0, -1), atTime, false) {
		t.Error("daily ejecutada ayer debería tocar hoy")
	}
}

func TestTaskIsDue_Weekly(t *testing.T) {
	// Domingo = 0. 11 jun 2026 es jueves (weekday 4).
	jueves4am := time.Date(2026, 6, 11, 4, 0, 0, 0, time.Local)
	if jueves4am.Weekday() != time.Thursday {
		t.Fatalf("fecha base mal: %v", jueves4am.Weekday())
	}
	sJueves := Schedule{Kind: ScheduleWeekly, AtWeekday: int(time.Thursday), AtHour: 4, AtMinute: 0}
	sDomingo := Schedule{Kind: ScheduleWeekly, AtWeekday: int(time.Sunday), AtHour: 4, AtMinute: 0}

	if !taskIsDue(sJueves, time.Time{}, jueves4am, false) {
		t.Error("weekly en su día/hora debería tocar")
	}
	if taskIsDue(sDomingo, time.Time{}, jueves4am, false) {
		t.Error("weekly en otro día no debería tocar")
	}
}

func TestTaskIsDue_AtBoot(t *testing.T) {
	s := Schedule{Kind: ScheduleAtBoot}
	now := time.Now()
	if !taskIsDue(s, time.Time{}, now, true) {
		t.Error("at_boot debería tocar en el barrido inicial")
	}
	if taskIsDue(s, time.Time{}, now, false) {
		t.Error("at_boot NO debería tocar en ticks normales")
	}
}

func TestSameDay(t *testing.T) {
	a := time.Date(2026, 6, 11, 1, 0, 0, 0, time.Local)
	b := time.Date(2026, 6, 11, 23, 0, 0, 0, time.Local)
	c := time.Date(2026, 6, 12, 0, 30, 0, 0, time.Local)
	if !sameDay(a, b) {
		t.Error("mismo día debería ser true")
	}
	if sameDay(b, c) {
		t.Error("días distintos debería ser false")
	}
}

func TestNextDailyOccurrence(t *testing.T) {
	now := time.Date(2026, 6, 11, 5, 0, 0, 0, time.Local) // 5am
	// Próxima 4:00 → mañana (ya pasó hoy).
	next := nextDailyOccurrence(4, 0, now)
	if next.Day() != 12 || next.Hour() != 4 {
		t.Errorf("esperado mañana 4:00, got %v", next)
	}
	// Próxima 8:00 → hoy.
	next2 := nextDailyOccurrence(8, 0, now)
	if next2.Day() != 11 || next2.Hour() != 8 {
		t.Errorf("esperado hoy 8:00, got %v", next2)
	}
}

func TestNextRunEstimate_Disabled(t *testing.T) {
	s := Schedule{Kind: ScheduleInterval, IntervalMinutes: 60}
	if got := nextRunEstimate(s, time.Time{}, false, time.Now()); got != "" {
		t.Errorf("tarea deshabilitada no debería tener próxima ejecución, got %q", got)
	}
}
