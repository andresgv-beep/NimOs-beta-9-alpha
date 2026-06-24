// shield_reputation_test.go — La capa de reputación de NimShield.
//
// Probamos el contrato que pediste:
//   · un habitual no se bloquea por 1-2 despistes
//   · un habitual que falla 3 SEGUIDOS entra en desconfianza (trato duro)
//   · una IP desconocida mantiene el umbral estricto de siempre
//   · un login exitoso BORRA la racha de fallos
//   · una racha vieja (fallos espaciados) no dispara desconfianza

package main

import (
	"testing"
	"time"
)

func setupRepTest(t *testing.T) func() {
	t.Helper()
	shieldTestMu.Lock()
	prevDB := db
	c, dbCleanup := setupNetworkDB(t)
	db = c.db
	dbShieldInit() // crea también shield_reputation
	shieldBlockMu.Lock()
	shieldBlocklist = map[string]*BlockEntry{}
	shieldBlockMu.Unlock()
	authFailWindow = newSlidingWindow()
	return func() {
		db = prevDB
		dbCleanup()
		shieldTestMu.Unlock()
	}
}

// helper: fuerza N éxitos para subir la reputación de una IP.
func seedSuccesses(ip string, n int) {
	for i := 0; i < n; i++ {
		ShieldAuthSuccess(ip)
	}
}

// helper: dispara un login_fail por el motor real.
func feedLoginFail(ip string) {
	processAuthRules(ShieldEvent{
		Category: "auth", SourceIP: ip,
		Details: map[string]interface{}{"type": "login_fail", "username": "andres"},
	})
}

func TestRep_ThresholdsByLevel(t *testing.T) {
	cases := []struct {
		success, streak int
		wantThreshold   int
		wantDistrust    bool
	}{
		{0, 0, repThresholdUnknown, false},    // desconocida
		{0, 5, repThresholdUnknown, false},    // desconocida en racha sigue dura (sin distrust: no tiene éxitos)
		{1, 0, repThresholdKnown, false},      // conocida
		{10, 0, repThresholdHabitual, false},  // habitual
		{10, 2, repThresholdHabitual, false},  // habitual con 2 fallos: aún margen
		{10, 3, repThresholdDistrust, true},   // habitual en racha 3 → DESCONFIANZA
		{50, 4, repThresholdDistrust, true},   // muy habitual, da igual: racha manda
	}
	for _, c := range cases {
		gotT, gotD := shieldLoginFailThreshold(c.success, c.streak)
		if gotT != c.wantThreshold || gotD != c.wantDistrust {
			t.Errorf("success=%d streak=%d → (%d,%v), want (%d,%v)",
				c.success, c.streak, gotT, gotD, c.wantThreshold, c.wantDistrust)
		}
	}
}

func TestRep_HabitualToleratesTwoMistakes(t *testing.T) {
	defer setupRepTest(t)()
	ip := "198.51.100.20"
	seedSuccesses(ip, 12) // habitual

	// 2 despistes → NO bloqueo (umbral habitual = 10).
	feedLoginFail(ip)
	feedLoginFail(ip)
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("un habitual no debería bloquearse por 2 fallos")
	}
}

func TestRep_HabitualDistrustOnStreak(t *testing.T) {
	defer setupRepTest(t)()
	ip := "198.51.100.21"
	seedSuccesses(ip, 20) // muy habitual

	// 3 fallos SEGUIDOS → desconfianza → umbral cae a 3 → bloqueo.
	feedLoginFail(ip)
	feedLoginFail(ip)
	feedLoginFail(ip)
	blocked, reason := shieldIsBlocked(ip)
	if !blocked {
		t.Fatal("un habitual con 3 fallos seguidos DEBE entrar en desconfianza y bloquearse")
	}
	t.Logf("desconfianza activada: %s", reason)
}

func TestRep_SuccessResetsStreak(t *testing.T) {
	defer setupRepTest(t)()
	ip := "198.51.100.22"
	seedSuccesses(ip, 12)

	// 2 fallos, luego ENTRA bien → la racha se borra.
	feedLoginFail(ip)
	feedLoginFail(ip)
	ShieldAuthSuccess(ip) // acertó → eras tú

	streak, _ := shieldRepRecordFail(ip) // un fallo posterior empieza de 1
	if streak != 1 {
		t.Errorf("tras un éxito la racha debe reiniciarse; primer fallo después = streak %d, want 1", streak)
	}
}

func TestRep_UnknownStaysStrict(t *testing.T) {
	defer setupRepTest(t)()
	ip := "198.51.100.23" // 0 éxitos

	// Desconocida: umbral 5. 4 fallos no bloquean, el 5º sí.
	for i := 0; i < 4; i++ {
		feedLoginFail(ip)
	}
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("desconocida no debería bloquear con 4 fallos (umbral 5)")
	}
	feedLoginFail(ip)
	if blocked, _ := shieldIsBlocked(ip); !blocked {
		t.Fatal("desconocida debe bloquear al 5º fallo")
	}
}

func TestRep_StaleStreakDoesNotTriggerDistrust(t *testing.T) {
	defer setupRepTest(t)()
	ip := "198.51.100.24"
	seedSuccesses(ip, 12)

	// Simulamos 2 fallos viejos: metemos una fila con last_fail antiguo.
	old := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	db.Exec(`UPDATE shield_reputation SET fail_streak = 2, last_fail = ? WHERE ip = ?`, old, ip)

	// Un fallo nuevo: como el anterior es viejo (>15min), la racha NO
	// continúa en 3 — empieza fresca en 1. Sin desconfianza.
	streak, success := shieldRepRecordFail(ip)
	if streak != 1 {
		t.Errorf("fallo tras racha fría debe reiniciar a 1, got %d", streak)
	}
	_, distrust := shieldLoginFailThreshold(success, streak)
	if distrust {
		t.Error("una racha fría no debe disparar desconfianza")
	}
}
