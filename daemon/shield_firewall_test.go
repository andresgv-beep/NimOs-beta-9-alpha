// shield_firewall_test.go — Verifica el escalado de bloqueos reincidentes al
// firewall (nftables) SIN tocar el firewall real: nftExec se stubbea siempre.

package main

import (
	"strings"
	"testing"
	"time"
)

// stubNFT sustituye nftExec por un grabador de llamadas y limpia el espejo en
// memoria. Devuelve el registro y la función de restauración.
func stubNFT() (*[]string, func()) {
	calls := &[]string{}
	prev := nftExec
	nftExec = func(args ...string) error {
		*calls = append(*calls, strings.Join(args, " "))
		return nil
	}
	shieldFWMu.Lock()
	shieldFWActive = map[string]bool{}
	shieldFWMu.Unlock()
	return calls, func() {
		nftExec = prev
		shieldFWMu.Lock()
		shieldFWActive = map[string]bool{}
		shieldFWMu.Unlock()
	}
}

// fwEnabledForTest arma el flag y devuelve su restauración.
func fwEnabledForTest(on bool) func() {
	prev := shieldFWEnabled.Load()
	shieldFWEnabled.Store(on)
	return func() { shieldFWEnabled.Store(prev) }
}

func hasCall(calls []string, substr string) bool {
	for _, c := range calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestShieldFWEligible(t *testing.T) {
	defer setupShieldTest(t)()
	cases := []struct {
		key  string
		want bool
	}{
		{"203.0.113.7", true},       // IPv4 pública
		{"2001:db8:1:2::/64", true}, // clave /64 pública
		{"192.168.1.50", false},     // RFC1918
		{"10.0.0.1", false},         // RFC1918
		{"127.0.0.1", false},        // loopback
		{"::1", false},              // loopback v6
		{"169.254.7.7", false},      // link-local v4
		{"fe80::/64", false},        // link-local v6
		{"fc00::/64", false},        // ULA (privada v6)
		{"no-parsea", false},        // basura jamás al kernel
		{"", false},                 // vacío
	}
	for _, c := range cases {
		if got := shieldFWEligible(c.key); got != c.want {
			t.Errorf("shieldFWEligible(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestShieldFWEligible_WhitelistInsideKeyBlocksEscalation(t *testing.T) {
	defer setupShieldTest(t)()
	wl := "2001:db8:5:5::a"
	shieldBlockMu.Lock()
	shieldWhitelist[wl] = true
	shieldBlockMu.Unlock()
	defer func() {
		shieldBlockMu.Lock()
		delete(shieldWhitelist, wl)
		shieldBlockMu.Unlock()
	}()

	if shieldFWEligible("2001:db8:5:5::/64") {
		t.Fatal("una clave que contiene una IP whitelisteada JAMÁS debe escalarse al kernel")
	}
	if !shieldFWEligible("2001:db8:6:6::/64") {
		t.Fatal("otro /64 sin whitelist dentro sí es elegible")
	}
}

func TestShieldBlockIP_EscalatesOnSecondBlock(t *testing.T) {
	defer setupShieldTest(t)()
	calls, restore := stubNFT()
	defer restore()
	defer fwEnabledForTest(true)()

	ip := "198.51.100.9"

	// 1er bloqueo: HTTP solo, sin kernel.
	shieldBlockIP(ip, time.Minute, "test", "TEST-001")
	if hasCall(*calls, "add element") {
		t.Fatal("el PRIMER bloqueo no debe escalar al kernel")
	}

	// 2º bloqueo: reincidente → DROP en kernel.
	shieldBlockIP(ip, time.Minute, "test", "TEST-001")
	if !hasCall(*calls, "add element inet nimshield blocked4 { 198.51.100.9 }") {
		t.Fatalf("el 2º bloqueo debe escalar al set v4; llamadas: %v", *calls)
	}

	// El unblock lo libera también del kernel.
	shieldUnblockIP(ip)
	if !hasCall(*calls, "delete element inet nimshield blocked4 { 198.51.100.9 }") {
		t.Fatalf("el unblock debe quitar el elemento del kernel; llamadas: %v", *calls)
	}
}

func TestShieldBlockIP_IPv6EscalatesAsSlash64(t *testing.T) {
	defer setupShieldTest(t)()
	calls, restore := stubNFT()
	defer restore()
	defer fwEnabledForTest(true)()

	// Dos bloqueos desde IPs distintas del mismo /64: misma clave → reincide.
	shieldBlockIP("2001:db8:7:7::1", time.Minute, "test", "TEST-001")
	shieldBlockIP("2001:db8:7:7::2", time.Minute, "test", "TEST-001")
	if !hasCall(*calls, "add element inet nimshield blocked6 { 2001:db8:7:7::/64 }") {
		t.Fatalf("el /64 reincidente debe escalar al set v6; llamadas: %v", *calls)
	}
}

func TestShieldFW_DisabledNeverTouchesKernel(t *testing.T) {
	defer setupShieldTest(t)()
	calls, restore := stubNFT()
	defer restore()
	defer fwEnabledForTest(false)()

	ip := "198.51.100.77"
	shieldBlockIP(ip, time.Minute, "test", "TEST-001")
	shieldBlockIP(ip, time.Minute, "test", "TEST-001")
	shieldBlockIP(ip, time.Minute, "test", "TEST-001")
	if hasCall(*calls, "add element") {
		t.Fatal("con el escalado desarmado el kernel no se toca (solo FW-OBSERVE en el log)")
	}
}

func TestShieldFW_PrivateNeverEscalates(t *testing.T) {
	defer setupShieldTest(t)()
	calls, restore := stubNFT()
	defer restore()
	defer fwEnabledForTest(true)()

	// Un vecino LAN travieso se queda en el bloqueo HTTP: reincida lo que
	// reincida, jamás pasa al kernel.
	for i := 0; i < 3; i++ {
		shieldBlockIP("192.168.1.66", time.Minute, "test", "TEST-001")
	}
	if hasCall(*calls, "add element") {
		t.Fatal("una IP privada/LAN no debe escalarse nunca al kernel")
	}
}
