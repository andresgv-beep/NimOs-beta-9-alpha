package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// helper: carga un trie con una IP conocida y la deja activa con una acción.
func loadIntelWith(ip, action string, observeOnly bool) {
	trie := newIntelTrie()
	p, _ := parsePrefix(ip)
	trie.insert(p, action)
	intelActive = &IntelState{
		trie:        trie,
		feedVersion: 1,
		source:      "test",
		observeOnly: observeOnly,
	}
}

func intelReq(ip string) *http.Request {
	r := httptest.NewRequest("GET", "/api/whatever", nil)
	r.RemoteAddr = ip + ":12345"
	return r
}

func TestIntelC_ObserveDoesNotBlock(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.7", "observe", true)
	intelEnforce.Store(true) // aunque enforce esté on, observe NO bloquea

	before := intelObservedTotal.Load()
	if shieldIntelCheck(intelReq("203.0.113.7")) {
		t.Fatal("modo observación NO debe bloquear")
	}
	if intelObservedTotal.Load() != before+1 {
		t.Error("la observación debería haberse contado")
	}
}

func TestIntelC_WhitelistAlwaysWins(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.8", "block", false)
	intelEnforce.Store(true)

	// metemos la IP en whitelist → el feed NO debe tocarla, ni siquiera observar
	shieldBlockMu.Lock()
	shieldWhitelist["203.0.113.8"] = true
	shieldBlockMu.Unlock()
	defer func() {
		shieldBlockMu.Lock()
		delete(shieldWhitelist, "203.0.113.8")
		shieldBlockMu.Unlock()
	}()

	before := intelObservedTotal.Load()
	if shieldIntelCheck(intelReq("203.0.113.8")) {
		t.Fatal("una IP en whitelist JAMÁS debe bloquearse por el feed")
	}
	if intelObservedTotal.Load() != before {
		t.Error("una IP en whitelist no debería ni observarse")
	}
}

func TestIntelC_EnforceBlocks(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.9", "block", false) // feed en modo block
	intelEnforce.Store(true)                     // admin activó enforcement

	if !shieldIntelCheck(intelReq("203.0.113.9")) {
		t.Fatal("con action=block, observeOnly=false y enforce=true → debe BLOQUEAR")
	}
	// y debe quedar registrada en la blocklist activa
	if blocked, _ := shieldIsBlocked("203.0.113.9"); !blocked {
		t.Error("la IP debería estar bloqueada tras el enforcement")
	}
}

func TestIntelC_EnforceOffStaysObserve(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.10", "block", false)
	intelEnforce.Store(false) // admin NO ha activado → sigue observando

	if shieldIntelCheck(intelReq("203.0.113.10")) {
		t.Fatal("sin enforce activado, no debe bloquear aunque el feed diga block")
	}
}

func TestIntelC_UnlistedIgnored(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.11", "block", false)
	intelEnforce.Store(true)

	// una IP que NO está en el feed → ni bloqueo ni observación
	before := intelObservedTotal.Load()
	if shieldIntelCheck(intelReq("8.8.4.4")) {
		t.Fatal("una IP no listada no debe bloquearse")
	}
	if intelObservedTotal.Load() != before {
		t.Error("una IP no listada no debe contarse")
	}
}
