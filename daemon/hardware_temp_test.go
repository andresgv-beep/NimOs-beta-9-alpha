package main

import "testing"

// ─── nextTempState — FIX3: debounce de temperatura con histéresis ─────────────
//
// Reglas:
//   - sube a "high" al alcanzar tempHighC (55°C)
//   - baja a "normal" solo por debajo de tempRecoverC (50°C)
//   - en la banda intermedia (50..54) mantiene el estado previo (anti-parpadeo)
//   - primera observación: "high" si ya está caliente, si no "normal"

func TestNextTempState_FirstObservationCool(t *testing.T) {
	if got := nextTempState("", 40); got != "normal" {
		t.Errorf("primer scan frío: got %q, want normal", got)
	}
}

func TestNextTempState_FirstObservationHot(t *testing.T) {
	if got := nextTempState("", 60); got != "high" {
		t.Errorf("primer scan caliente: got %q, want high", got)
	}
}

func TestNextTempState_CrossUpToHigh(t *testing.T) {
	if got := nextTempState("normal", 55); got != "high" {
		t.Errorf("cruzar 55 hacia arriba: got %q, want high", got)
	}
}

func TestNextTempState_StaysHighInBand(t *testing.T) {
	// Banda intermedia (50..54): si veníamos de high, seguimos high
	// (no recupera hasta bajar de 50). Esto evita el parpadeo.
	if got := nextTempState("high", 52); got != "high" {
		t.Errorf("banda intermedia desde high: got %q, want high (histéresis)", got)
	}
}

func TestNextTempState_RecoversBelowHysteresis(t *testing.T) {
	if got := nextTempState("high", 49); got != "normal" {
		t.Errorf("bajar de 50 recupera: got %q, want normal", got)
	}
}

func TestNextTempState_StaysNormalInBand(t *testing.T) {
	// Banda intermedia viniendo de normal: sigue normal (no salta a high
	// hasta alcanzar 55). Simetría de la histéresis.
	if got := nextTempState("normal", 53); got != "normal" {
		t.Errorf("banda intermedia desde normal: got %q, want normal", got)
	}
}

func TestNextTempState_SustainedHotNoFlap(t *testing.T) {
	// Mantenerse por encima del umbral NO debe cambiar de estado (el caller
	// solo notifica en transición → no re-spamea).
	s := nextTempState("normal", 56) // → high (notificaría aquí)
	if s != "high" {
		t.Fatalf("setup: got %q, want high", s)
	}
	s = nextTempState(s, 57) // sigue high
	if s != "high" {
		t.Errorf("caliente sostenido: got %q, want high (sin re-notif)", s)
	}
	s = nextTempState(s, 56) // sigue high
	if s != "high" {
		t.Errorf("caliente sostenido: got %q, want high", s)
	}
}
