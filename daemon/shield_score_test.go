package main

import (
	"testing"
	"time"
)

func TestScorePenalty(t *testing.T) {
	cases := map[string]int{
		"critical": 50,
		"high":     25,
		"medium":   12,
		"low":      6,
		"":         6, // desconocida → trato de low
	}
	for sev, want := range cases {
		if got := scorePenalty(sev); got != want {
			t.Errorf("scorePenalty(%q) = %d, want %d", sev, got, want)
		}
	}
}

func TestScoreApplyDecay(t *testing.T) {
	now := time.Now().UTC()

	// hace 4h con score 20 → +20 (5/h) = 40
	got := scoreApplyDecay(20, now.Add(-4*time.Hour).Format(time.RFC3339))
	if got != 40 {
		t.Errorf("decay 4h desde 20 = %d, want 40", got)
	}

	// recuperación tope a 100 (no se pasa)
	if got := scoreApplyDecay(90, now.Add(-100*time.Hour).Format(time.RFC3339)); got != scoreMax {
		t.Errorf("decay largo = %d, want %d (cap)", got, scoreMax)
	}

	// sin last_update → score intacto
	if got := scoreApplyDecay(50, ""); got != 50 {
		t.Errorf("decay sin fecha = %d, want 50", got)
	}

	// fecha futura (reloj raro) → no baja
	if got := scoreApplyDecay(50, now.Add(2*time.Hour).Format(time.RFC3339)); got != 50 {
		t.Errorf("decay futuro = %d, want 50 (no baja)", got)
	}
}

// Comprueba que un evento crítico hunde más que uno bajo (orden de severidad).
func TestScorePenaltyOrdering(t *testing.T) {
	if !(scorePenalty("critical") > scorePenalty("high") &&
		scorePenalty("high") > scorePenalty("medium") &&
		scorePenalty("medium") > scorePenalty("low")) {
		t.Error("las penalizaciones no respetan el orden de severidad")
	}
}
