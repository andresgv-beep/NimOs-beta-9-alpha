package main

import "testing"

// El flag de enforce se persiste y se restaura.
func TestIntelD_EnforcePersists(t *testing.T) {
	defer setupIntelTest(t)()

	if dbIntelGetEnforce() {
		t.Error("enforce debería arrancar en false (observación)")
	}
	intelSetEnforce(true)
	if !dbIntelGetEnforce() {
		t.Error("enforce no persistió en DB")
	}
	if !intelEnforce.Load() {
		t.Error("flag en memoria no se actualizó")
	}
	// simular reinicio: resetear memoria y restaurar de DB
	intelEnforce.Store(false)
	intelEnforce.Store(dbIntelGetEnforce())
	if !intelEnforce.Load() {
		t.Error("enforce no se restauró tras 'reinicio'")
	}
	// volver a observación
	intelSetEnforce(false)
	if dbIntelGetEnforce() {
		t.Error("enforce debería estar desactivado")
	}
}

// intelStatus refleja el estado correctamente.
func TestIntelD_Status(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.50", "observe", true)

	st := intelStatus()
	if !st.Loaded || st.Prefixes != 1 {
		t.Errorf("status incorrecto: %+v", st)
	}
	if st.EnforceActive {
		t.Error("enforce debería estar off por defecto")
	}
	if !st.ObserveOnly {
		t.Error("el feed de test es observe")
	}
}
