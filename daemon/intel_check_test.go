package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// helper: carga un trie con una IP conocida y la deja activa con una acción.
func loadIntelWith(ip, action string, observeOnly bool) {
	trie := newIntelTrie()
	p, _ := parsePrefix(ip)
	trie.insert(p, action)
	intelActive.Store(&IntelState{
		trie:        trie,
		feedVersion: 1,
		source:      "test",
		observeOnly: observeOnly,
	})
}

func intelReq(ip string) *http.Request {
	r := httptest.NewRequest("GET", "/api/whatever", nil)
	r.RemoteAddr = ip + ":12345"
	return r
}

// TestIntelC_ConcurrentRefreshAndCheck_NoRace — regresión de la data race de
// intelActive: el refresco publicaba los campos (feedVersion, observeOnly…)
// uno a uno mientras el hot path los leía. Ahora es un atomic.Pointer con
// snapshot por petición. Este test, corrido con -race, caza cualquier vuelta
// atrás a escrituras sueltas.
func TestIntelC_ConcurrentRefreshAndCheck_NoRace(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.50", "observe", true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			// simula el refresco: publica un estado completo nuevo
			loadIntelWith("203.0.113.50", "observe", true)
		}
	}()

	r := intelReq("203.0.113.50")
	for {
		select {
		case <-done:
			return
		default:
			shieldIntelCheck(r) // hot path leyendo mientras se publica
		}
	}
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

// #4: el rate-limit no debe emitir dos eventos seguidos para la misma IP,
// pero el contador SÍ debe subir siempre.
func TestIntelC_ObserveRateLimit(t *testing.T) {
	defer setupIntelTest(t)()
	loadIntelWith("203.0.113.77", "observe", true)

	// limpiar el mapa de rate-limit
	intelObserveSeenMu.Lock()
	intelObserveSeen = map[string]time.Time{}
	intelObserveSeenMu.Unlock()

	before := intelObservedTotal.Load()
	// primera vez: debe emitir
	if !intelShouldEmitObserve("203.0.113.77") {
		t.Fatal("primera observación debería emitir evento")
	}
	// inmediatamente después: NO debe emitir (cooldown)
	if intelShouldEmitObserve("203.0.113.77") {
		t.Error("dentro del cooldown no debería emitir otro evento")
	}
	// otra IP distinta: sí emite
	if !intelShouldEmitObserve("203.0.113.78") {
		t.Error("una IP distinta debería emitir")
	}

	// el contador sube en cada shieldIntelCheck aunque no emita evento
	shieldIntelCheck(intelReq("203.0.113.77"))
	shieldIntelCheck(intelReq("203.0.113.77"))
	if intelObservedTotal.Load() < before+2 {
		t.Error("el contador debe subir en cada match, con o sin evento")
	}
}
