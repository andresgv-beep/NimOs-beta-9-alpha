// shield_whitelist_cidr_test.go — Verifica la whitelist con rangos CIDR:
// matching en caliente, liberación de bloqueos cubiertos y veto del escalado
// a firewall cuando el rango whitelisteado solapa la clave.

package main

import (
	"testing"
	"time"
)

// cidrForTest aplica una entrada CIDR en caliente y devuelve su limpieza.
func cidrForTest(entry string) func() {
	shieldWhitelistApplyLive(entry)
	return func() { shieldWhitelistRemoveLive(entry) }
}

func TestWhitelistCIDR_MatchesInsideRange(t *testing.T) {
	defer setupShieldTest(t)()
	defer cidrForTest("203.0.113.0/24")()

	if !shieldIsWhitelisted("203.0.113.55") {
		t.Fatal("una IP dentro del CIDR whitelisteado debe estar whitelisteada")
	}
	if shieldIsWhitelisted("203.0.114.1") {
		t.Fatal("una IP fuera del CIDR no debe estar whitelisteada")
	}
	// Forma v4-mapped de una IP dentro del rango → también cubierta.
	if !shieldIsWhitelisted("::ffff:203.0.113.55") {
		t.Fatal("la forma ::ffff: de una IP cubierta debe estar whitelisteada")
	}
}

func TestWhitelistCIDR_IPv6Range(t *testing.T) {
	defer setupShieldTest(t)()
	defer cidrForTest("2001:db8::/32")()

	if !shieldIsWhitelisted("2001:db8:ff::1") {
		t.Fatal("una IPv6 dentro del /32 whitelisteado debe estar whitelisteada")
	}
	if shieldIsWhitelisted("2001:db9::1") {
		t.Fatal("una IPv6 fuera del rango no debe estar whitelisteada")
	}
}

func TestWhitelistCIDR_RemoveRestoresStrictness(t *testing.T) {
	defer setupShieldTest(t)()
	cleanup := cidrForTest("203.0.113.0/24")
	if !shieldIsWhitelisted("203.0.113.55") {
		t.Fatal("setup: debería estar whitelisteada")
	}
	cleanup()
	if shieldIsWhitelisted("203.0.113.55") {
		t.Fatal("tras quitar el CIDR, la IP vuelve al trato estricto")
	}
}

func TestWhitelistCIDR_UnblocksCoveredKeys(t *testing.T) {
	defer setupShieldTest(t)()

	// Un bloqueo v4 exacto y uno v6 (clave /64), ambos cubiertos por rangos.
	shieldBlockIP("203.0.113.7", time.Hour, "test", "TEST-001")
	shieldBlockIP("2001:db8:1:2::5", time.Hour, "test", "TEST-001")

	defer cidrForTest("203.0.113.0/24")()
	shieldUnblockCovered("203.0.113.0/24")
	if blocked, _ := shieldIsBlocked("203.0.113.7"); blocked {
		t.Fatal("el bloqueo v4 cubierto por el CIDR debe liberarse")
	}

	// El /64 bloqueado se solapa con el /32 whitelisteado → también fuera.
	defer cidrForTest("2001:db8::/32")()
	shieldUnblockCovered("2001:db8::/32")
	if blocked, _ := shieldIsBlocked("2001:db8:1:2::9"); blocked {
		t.Fatal("la clave /64 cubierta por el CIDR v6 debe liberarse")
	}
}

func TestShieldFWEligible_CIDROverlapVetoesEscalation(t *testing.T) {
	defer setupShieldTest(t)()
	defer cidrForTest("198.51.100.0/24")()
	defer cidrForTest("2001:db8::/32")()

	if shieldFWEligible("198.51.100.9") {
		t.Fatal("una clave dentro de un CIDR whitelisteado no debe escalar al kernel")
	}
	if shieldFWEligible("2001:db8:1:2::/64") {
		t.Fatal("un /64 que solapa el CIDR v6 whitelisteado no debe escalar")
	}
	if !shieldFWEligible("198.51.101.9") {
		t.Fatal("una clave fuera de los rangos sí es elegible")
	}
}
