// shield_firewall.go — NimShield · Escalado de bloqueos al firewall (nftables).
//
// PROBLEMA: una clave bloqueada a nivel HTTP sigue consumiendo handshake TLS y
// stack HTTP en cada petición — el 403 es barato, pero no gratis, y un
// atacante persistente puede aporrear indefinidamente. ESCALADO: cuando una
// clave de red reincide (2º bloqueo o posterior), se añade a un set de
// nftables con regla DROP y desaparece del proceso: el kernel tira sus
// paquetes antes de que toquen Caddy o el daemon.
//
// DISEÑO:
//   · Tabla propia `inet nimshield` (chain input, prioridad -150 = antes del
//     filter de ufw). Se crea/destruye ENTERA: cero interferencia con las
//     reglas de ufw del admin. El daemon corre como root → nft directo.
//   · El ciclo de vida lo gobierna el daemon, no el kernel: unblock/expiración/
//     whitelist quitan el elemento; toggle off hace teardown de la tabla;
//     el arranque reconstruye desde los bloqueos persistidos (resync).
//
// SALVAGUARDAS (el DROP de kernel no sabe de whitelists — se filtra ANTES):
//   · Flag persistido, por defecto OFF (patrón intelEnforce: el admin lo arma).
//     Apagado, cada escalado que HABRÍA ocurrido se registra (FW-OBSERVE).
//   · Solo claves PÚBLICAS: loopback, rangos privados (RFC1918/ULA), link-local
//     y multicast jamás se escalan — un vecino LAN travieso se queda en el
//     bloqueo HTTP, que sí respeta la whitelist en caliente.
//   · Una clave que CONTIENE una IP whitelisteada no se escala nunca (p.ej. el
//     /64 del admin bloqueado por un vecino de su misma red).
//   · Whitelistear una IP ya escalada la libera: el flujo whitelist→unblock
//     quita también el elemento del kernel.

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"net/netip"
)

const shieldFWTable = "nimshield"

// shieldFWEnabled — flag en caliente del escalado. Persistido en
// shield_settings ('firewall_escalation'); por defecto OFF.
var shieldFWEnabled atomic.Bool

// shieldFWActive refleja los elementos vivos en el set del kernel (para el
// panel de estado y para no re-añadir duplicados).
var (
	shieldFWActive = map[string]bool{}
	shieldFWMu     sync.Mutex
)

// nftExec ejecuta nft. Variable para que los tests lo stubbeen: los tests NO
// deben tocar el firewall real de la máquina.
var nftExec = func(args ...string) error {
	out, err := exec.Command("nft", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// shieldFWInit crea la tabla desde cero (borra la anterior si existía: así la
// re-creación es idempotente y nunca acumula reglas duplicadas).
func shieldFWInit() error {
	_ = nftExec("delete", "table", "inet", shieldFWTable) // puede no existir
	steps := [][]string{
		{"add", "table", "inet", shieldFWTable},
		{"add", "chain", "inet", shieldFWTable, "input", "{ type filter hook input priority -150 ; policy accept ; }"},
		{"add", "set", "inet", shieldFWTable, "blocked4", "{ type ipv4_addr ; }"},
		{"add", "set", "inet", shieldFWTable, "blocked6", "{ type ipv6_addr ; flags interval ; }"},
		{"add", "rule", "inet", shieldFWTable, "input", "ip", "saddr", "@blocked4", "drop"},
		{"add", "rule", "inet", shieldFWTable, "input", "ip6", "saddr", "@blocked6", "drop"},
	}
	for _, s := range steps {
		if err := nftExec(s...); err != nil {
			return err
		}
	}
	shieldFWMu.Lock()
	shieldFWActive = map[string]bool{}
	shieldFWMu.Unlock()
	return nil
}

// shieldFWTeardown elimina la tabla entera (al desactivar el escalado o
// apagar el shield). Ignora errores: si no existía, ya está.
func shieldFWTeardown() {
	_ = nftExec("delete", "table", "inet", shieldFWTable)
	shieldFWMu.Lock()
	shieldFWActive = map[string]bool{}
	shieldFWMu.Unlock()
}

// shieldFWSetFor devuelve el set nftables que corresponde a una clave de red.
func shieldFWSetFor(key string) string {
	if strings.Contains(key, ":") {
		return "blocked6"
	}
	return "blocked4"
}

// shieldFWAdd añade una clave de red al set del kernel. La clave DEBE haber
// pasado shieldFWEligible (ahí se garantiza que parsea y que es segura).
func shieldFWAdd(key string) {
	shieldFWMu.Lock()
	if shieldFWActive[key] {
		shieldFWMu.Unlock()
		return // ya está en el kernel
	}
	shieldFWMu.Unlock()

	if err := nftExec("add", "element", "inet", shieldFWTable, shieldFWSetFor(key), "{ "+key+" }"); err != nil {
		logMsg("shield FW: error añadiendo %s al kernel: %v", key, err)
		return
	}
	shieldFWMu.Lock()
	shieldFWActive[key] = true
	shieldFWMu.Unlock()
	logMsg("shield FW: %s → DROP en kernel (reincidente)", key)
}

// shieldFWRemove quita una clave del set del kernel (unblock, expiración,
// whitelist). Ignora errores: si no estaba, ya está quitada.
func shieldFWRemove(key string) {
	shieldFWMu.Lock()
	present := shieldFWActive[key]
	delete(shieldFWActive, key)
	shieldFWMu.Unlock()
	if !present {
		return
	}
	if err := nftExec("delete", "element", "inet", shieldFWTable, shieldFWSetFor(key), "{ "+key+" }"); err != nil {
		logMsg("shield FW: error quitando %s del kernel: %v", key, err)
		return
	}
	logMsg("shield FW: %s liberado del kernel", key)
}

// shieldFWCount devuelve cuántas claves están escaladas (para el status).
func shieldFWCount() int {
	shieldFWMu.Lock()
	defer shieldFWMu.Unlock()
	return len(shieldFWActive)
}

// shieldFWEligible decide si una clave de red PUEDE escalarse al kernel.
// Estricto por diseño: en la duda, no se escala (el bloqueo HTTP ya cubre).
func shieldFWEligible(key string) bool {
	var addr netip.Addr
	if p, err := netip.ParsePrefix(key); err == nil {
		addr = p.Addr()
	} else if a, err := netip.ParseAddr(key); err == nil {
		addr = a
	} else {
		return false // no parsea → jamás al kernel
	}
	// Solo direcciones públicas: la LAN del admin, loopback y compañía se
	// quedan en el bloqueo HTTP (que respeta whitelist en caliente).
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	// Una clave que contiene una IP whitelisteada no se escala: el DROP de
	// kernel ignoraría la whitelist (p.ej. el /64 del admin, bloqueado por
	// culpa de un vecino de su red). Aplica a las dos formas de whitelist:
	// IPs exactas y rangos CIDR (cualquier solape veta el escalado).
	shieldBlockMu.RLock()
	defer shieldBlockMu.RUnlock()
	for wl := range shieldWhitelist {
		if shieldNetKey(wl) == key {
			return false
		}
	}
	for _, p := range shieldWhitelistCIDRs {
		if keyOverlapsPrefix(key, p) {
			return false
		}
	}
	return true
}

// shieldFWMaybeEscalate aplica la política de escalado tras un bloqueo:
// reincidente (2º bloqueo o posterior) + clave elegible. Con el flag apagado
// registra el "habría escalado" (FW-OBSERVE) para calibrar antes de armar.
// prevBlocks = bloqueos PREVIOS de la clave (0 = primera vez).
func shieldFWMaybeEscalate(key string, prevBlocks int) {
	if prevBlocks < 1 || !shieldFWEligible(key) {
		return
	}
	if !shieldFWEnabled.Load() {
		logMsg("shield FW-OBSERVE: %s habría pasado a DROP de kernel (bloqueo nº%d) — escalado desactivado", key, prevBlocks+1)
		return
	}
	shieldFWAdd(key)
}

// shieldFWResync reconstruye el set del kernel desde los bloqueos activos en
// memoria (tras un arranque): solo claves reincidentes y elegibles.
func shieldFWResync() {
	shieldBlockMu.RLock()
	keys := make([]string, 0, len(shieldBlocklist))
	for k := range shieldBlocklist {
		keys = append(keys, k)
	}
	shieldBlockMu.RUnlock()

	n := 0
	for _, k := range keys {
		if shieldRepBlockCount(k) >= 2 && shieldFWEligible(k) {
			shieldFWAdd(k)
			n++
		}
	}
	if n > 0 {
		logMsg("shield FW: resync — %d claves reincidentes re-escaladas al kernel", n)
	}
}

// ─── Persistencia del flag ───

func dbShieldSetFWEnabled(enabled bool) {
	v := "0"
	if enabled {
		v = "1"
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO shield_settings (key, value) VALUES ('firewall_escalation', ?)`, v); err != nil {
		logMsg("shield FW: no pude persistir el flag: %v", err)
	}
}

// loadShieldFWEnabled lee el flag persistido. Sin fila → OFF (el default
// seguro: el admin arma el escalado explícitamente, como el enforce de intel).
func loadShieldFWEnabled() {
	var v string
	if err := db.QueryRow(`SELECT value FROM shield_settings WHERE key = 'firewall_escalation'`).Scan(&v); err != nil {
		return
	}
	shieldFWEnabled.Store(v == "1")
}

// shieldFWSetEnabled activa/desactiva el escalado en caliente y lo persiste.
// Al activar: tabla desde cero + resync de reincidentes activos. Al
// desactivar: teardown completo (ningún DROP superviviente).
func shieldFWSetEnabled(on bool) error {
	if on {
		if err := shieldFWInit(); err != nil {
			return err
		}
		shieldFWEnabled.Store(true)
		dbShieldSetFWEnabled(true)
		shieldFWResync()
		logMsg("shield FW: escalado a kernel ACTIVADO")
		return nil
	}
	shieldFWEnabled.Store(false)
	dbShieldSetFWEnabled(false)
	shieldFWTeardown()
	logMsg("shield FW: escalado a kernel desactivado (tabla eliminada)")
	return nil
}
