// shield_netkey_test.go — Verifica la CLAVE DE RED (shieldNetKey) y su efecto
// anti-rotación en blocklist y ventanas de reglas.
//
// Contexto: bloquear y contar por IP exacta regala a un atacante IPv6 la
// rotación gratuita dentro de su /64 (2^64 direcciones). La clave de red
// agrega por /64 en IPv6 y por IP canónica en IPv4. Con ello aparece un caso
// nuevo: una IP whitelisteada DENTRO de un /64 bloqueado debe seguir entrando
// (guard de whitelist en el check de bloqueados del middleware).

package main

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShieldNetKey_Normalization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.7"},                // IPv4: tal cual
		{"::ffff:203.0.113.7", "203.0.113.7"},         // v4-mapped → v4 canónica
		{"2001:db8:1:2::5", "2001:db8:1:2::/64"},      // IPv6 → /64
		{"2001:db8:1:2:ffff::9", "2001:db8:1:2::/64"}, // mismo /64 → misma clave
		{"2001:db8:9:9::1", "2001:db8:9:9::/64"},      // otro /64 → otra clave
		{"", ""},                                      // vacío → vacío
		{"no-es-ip", "no-es-ip"},                      // basura → tal cual
		{"2001:db8:1:2::/64", "2001:db8:1:2::/64"},    // clave ya normalizada → tal cual
	}
	for _, c := range cases {
		if got := shieldNetKey(c.in); got != c.want {
			t.Errorf("shieldNetKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShieldBlock_IPv6BlocksWholeSlash64(t *testing.T) {
	defer setupShieldTest(t)()

	// Bloquear una IP concreta del /64…
	shieldBlockIP("2001:db8:1:2::5", time.Hour, "test", "TEST-001")

	// …bloquea a cualquier hermana del MISMO /64 (rotación inútil).
	if blocked, _ := shieldIsBlocked("2001:db8:1:2:dead:beef::1"); !blocked {
		t.Fatal("una IP hermana del mismo /64 debe estar bloqueada")
	}
	// …pero NO a otro /64 (no hay sobre-bloqueo).
	if blocked, _ := shieldIsBlocked("2001:db8:9:9::1"); blocked {
		t.Fatal("un /64 distinto NO debe estar bloqueado")
	}

	// El desbloqueo acepta la clave /64 que muestra la UI.
	shieldUnblockIP("2001:db8:1:2::/64")
	if blocked, _ := shieldIsBlocked("2001:db8:1:2::5"); blocked {
		t.Fatal("tras desbloquear la clave /64, la IP original debe pasar")
	}
}

func TestShieldBlock_IPv4StaysExact(t *testing.T) {
	defer setupShieldTest(t)()

	shieldBlockIP("203.0.113.7", time.Hour, "test", "TEST-001")
	if blocked, _ := shieldIsBlocked("203.0.113.7"); !blocked {
		t.Fatal("la IPv4 bloqueada debe estar bloqueada")
	}
	// La forma v4-mapped es la MISMA dirección → misma clave.
	if blocked, _ := shieldIsBlocked("::ffff:203.0.113.7"); !blocked {
		t.Fatal("la forma ::ffff: de la misma IPv4 debe compartir bloqueo")
	}
	if blocked, _ := shieldIsBlocked("203.0.113.8"); blocked {
		t.Fatal("una IPv4 vecina NO debe estar bloqueada")
	}
}

func TestProcessRules_IPv6RotationAccumulates(t *testing.T) {
	defer setupShieldTest(t)()

	// 10 404s desde 10 IPs DISTINTAS del mismo /64: por IP exacta jamás
	// llegaría al umbral de SCAN-001 (10 por IP); por clave de red, sí.
	for i := 0; i < 10; i++ {
		processRules(ShieldEvent{
			Category: "scan", Status: 404,
			SourceIP: fmt.Sprintf("2001:db8:aa:bb::%x", i+1),
			Endpoint: "/probe",
		})
	}
	if blocked, _ := shieldIsBlocked("2001:db8:aa:bb::ffff"); !blocked {
		t.Fatal("la rotación dentro del /64 debe acumular y bloquear el rango (SCAN-001)")
	}
}

func TestShieldMiddleware_WhitelistedIPInsideBlockedSlash64Passes(t *testing.T) {
	defer setupShieldTest(t)()

	// Un vecino del /64 provoca el bloqueo del rango…
	shieldBlockIP("2001:db8:c:d::bad", time.Hour, "vecino malicioso", "TEST-001")

	// …pero la IP whitelisteada del admin, dentro del MISMO /64, debe entrar.
	adminIP := "2001:db8:c:d::a11"
	shieldBlockMu.Lock()
	shieldWhitelist[adminIP] = true
	shieldBlockMu.Unlock()
	defer func() {
		shieldBlockMu.Lock()
		delete(shieldWhitelist, adminIP)
		shieldBlockMu.Unlock()
	}()

	r := httptest.NewRequest("GET", "/api/whatever", nil)
	r.RemoteAddr = "[" + adminIP + "]:44321"
	w := httptest.NewRecorder()
	if handled := shieldMiddleware(w, r); handled {
		t.Fatal("la IP whitelisteada dentro de un /64 bloqueado debe seguir entrando")
	}

	// Y un tercero no whitelisteado del mismo /64 sigue bloqueado.
	r2 := httptest.NewRequest("GET", "/api/whatever", nil)
	r2.RemoteAddr = "[2001:db8:c:d::c0de]:44321"
	w2 := httptest.NewRecorder()
	if handled := shieldMiddleware(w2, r2); !handled {
		t.Fatal("el resto del /64 bloqueado debe seguir cortado")
	}
}
