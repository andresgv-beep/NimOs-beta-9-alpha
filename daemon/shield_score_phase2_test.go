// shield_score_phase2_test.go — Verifica la Fase 2 del motor de comportamiento:
// auto-bloqueo al cruzar el umbral de score, con sus salvaguardas (toggle
// default OFF, exención de sesiones válidas y de whitelisteados).

package main

import (
	"strings"
	"testing"
)

// behavEnforceForTest arma la Fase 2 y devuelve su restauración.
func behavEnforceForTest(on bool) func() {
	prev := shieldBehavEnforce.Load()
	shieldBehavEnforce.Store(on)
	return func() { shieldBehavEnforce.Store(prev) }
}

// criticalEvent fabrica un evento anónimo de severidad crítica (-50 puntos).
func criticalEvent(ip string) ShieldEvent {
	return ShieldEvent{
		Category: "injection", Severity: "critical", SourceIP: ip,
		Details: map[string]interface{}{"rule": "INJ-002"},
	}
}

func TestBehavPhase2_ArmedBlocksOnThresholdCross(t *testing.T) {
	defer setupShieldTest(t)()
	defer behavEnforceForTest(true)()
	ip := "203.0.113.130"

	// 100 → 50 (sin cruce) → 0 (cruce del umbral 30) → bloqueo BEHAV-001.
	shieldScorePenalize(criticalEvent(ip))
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("a score 50 no debe haber bloqueo todavía")
	}
	shieldScorePenalize(criticalEvent(ip))
	blocked, reason := shieldIsBlocked(ip)
	if !blocked {
		t.Fatal("cruzar el umbral con la Fase 2 armada debe auto-bloquear")
	}
	if !strings.Contains(reason, "Comportamiento") {
		t.Errorf("la razón debe ser de comportamiento, fue: %q", reason)
	}
}

func TestBehavPhase2_DisarmedOnlyObserves(t *testing.T) {
	defer setupShieldTest(t)()
	defer behavEnforceForTest(false)()
	ip := "203.0.113.131"

	shieldScorePenalize(criticalEvent(ip))
	shieldScorePenalize(criticalEvent(ip))
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("desarmada, la Fase 2 solo observa (HABRÍA AUTO-BLOQUEADO)")
	}
	// El score sí se hundió (Fase 1 siempre activa).
	if s := shieldScoreRead(ip); s >= scoreBlockThreshold {
		t.Fatalf("el score debería estar bajo el umbral; es %d", s)
	}
}

func TestBehavPhase2_AuthenticatedEventsNeverTrigger(t *testing.T) {
	defer setupShieldTest(t)()
	defer behavEnforceForTest(true)()
	ip := "203.0.113.132"

	ev := criticalEvent(ip)
	ev.Details["authenticated"] = true
	// Dos backticks del dueño en renames: el score cae (observabilidad),
	// pero JAMÁS gatilla bloqueo por la puerta de atrás.
	shieldScorePenalize(ev)
	shieldScorePenalize(ev)
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("eventos de sesión válida no deben gatillar el auto-bloqueo")
	}
	if s := shieldScoreRead(ip); s >= scoreBlockThreshold {
		t.Fatalf("el score sí debe reflejar los eventos; es %d", s)
	}
}

func TestBehavPhase2_WhitelistedNeverBlocked(t *testing.T) {
	defer setupShieldTest(t)()
	defer behavEnforceForTest(true)()
	ip := "203.0.113.133"

	shieldBlockMu.Lock()
	shieldWhitelist[ip] = true
	shieldBlockMu.Unlock()
	defer func() {
		shieldBlockMu.Lock()
		delete(shieldWhitelist, ip)
		shieldBlockMu.Unlock()
	}()

	shieldScorePenalize(criticalEvent(ip))
	shieldScorePenalize(criticalEvent(ip))
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("una IP whitelisteada jamás se auto-bloquea por comportamiento")
	}
}
