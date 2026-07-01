// intel_check.go — NimShield Intelligence · enganche en el hot path.
//
// FASE C: por cada petición entrante, consultamos el trie de la blocklist.
//
//	· MODO OBSERVACIÓN (action="observe"): registramos un evento "habría
//	  bloqueado esta IP" SIN bloquear. Permite medir el impacto real del feed
//	  en TU tráfico antes de activar el bloqueo en duro. Cero riesgo.
//	· MODO BLOQUEO (action="block"): bloqueo efectivo.
//
// SIEMPRE respeta la whitelist: una IP de confianza nunca se toca, da igual lo
// que diga el feed (la whitelist manda sobre la inteligencia).
package main

import (
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Contadores de observación (para el panel de la Fase D). Atómicos: el hot
// path los toca en concurrencia.
var (
	intelObservedTotal atomic.Int64 // matches en modo observación (no bloqueados)
	intelBlockedTotal  atomic.Int64 // bloqueos efectivos por el feed
)

// intelEnforce controla si el feed bloquea en duro. Arranca en FALSE: aunque
// el feed trajera action="block", NimOS no bloquea hasta que el admin lo
// active explícitamente (doble salvaguarda sobre el modo observación del feed).
var intelEnforce atomic.Bool

// Rate-limit de eventos de observación por IP (#4): una IP que aporrea el NAS
// no debe inflar la tabla de eventos. Los CONTADORES siguen subiendo siempre;
// solo limitamos cuántos ShieldEvent INTEL-OBSERVE se emiten por IP.
const intelObserveEventCooldown = 10 * time.Minute

var (
	intelObserveSeen   = map[string]time.Time{}
	intelObserveSeenMu sync.Mutex
)

// intelShouldEmitObserve devuelve true si toca emitir evento para esta IP
// (no se ha emitido uno en la última ventana de cooldown).
func intelShouldEmitObserve(ip string) bool {
	now := time.Now()
	intelObserveSeenMu.Lock()
	defer intelObserveSeenMu.Unlock()
	last, ok := intelObserveSeen[ip]
	if ok && now.Sub(last) < intelObserveEventCooldown {
		return false
	}
	intelObserveSeen[ip] = now
	// poda perezosa: si el mapa crece mucho, limpiamos entradas viejas
	if len(intelObserveSeen) > 4096 {
		for k, t := range intelObserveSeen {
			if now.Sub(t) > intelObserveEventCooldown {
				delete(intelObserveSeen, k)
			}
		}
	}
	return true
}

// shieldIntelCheck consulta la blocklist para una petición. Devuelve true si
// la petición debe CORTARSE (solo en modo bloqueo activo). En observación
// devuelve false (no corta) pero deja registro.
//
// Se llama desde shieldMiddleware, después del check de IP ya bloqueada y antes
// de los honeypots.
func shieldIntelCheck(r *http.Request) (block bool) {
	// Snapshot atómico del estado del feed: toda la petición se decide con
	// una vista CONSISTENTE (trie + observeOnly + feedVersion del mismo
	// feed), aunque un refresco publique uno nuevo a mitad.
	st := intelActive.Load()
	// feed sin cargar → nada que hacer
	if st.trie.size() == 0 {
		return false
	}
	ip := clientIP(r)
	if ip == "" || shieldIsWhitelisted(ip) {
		return false // la whitelist SIEMPRE manda sobre el feed
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}

	m := st.trie.lookup(addr)
	if !m.Hit {
		return false
	}

	// Decisión: ¿bloqueamos de verdad o solo observamos?
	//   · el feed debe traer action="block" (no "observe")
	//   · Y el admin debe haber activado el enforcement (intelEnforce)
	enforce := m.Action == "block" && intelEnforce.Load() && !st.observeOnly

	if enforce {
		intelBlockedTotal.Add(1)
		shieldEmit(ShieldEvent{
			Category:  "intel",
			Severity:  "high",
			SourceIP:  ip,
			UserAgent: r.UserAgent(),
			Endpoint:  r.URL.Path,
			Method:    r.Method,
			Details: map[string]interface{}{
				"rule":         "INTEL-001",
				"feed_version": st.feedVersion,
				"mode":         "block",
			},
		})
		shieldBlockIP(ip, 6*time.Hour, "Listed in NimShield Intelligence threat feed", "INTEL-001")
		return true
	}

	// MODO OBSERVACIÓN: registrar sin bloquear.
	intelObservedTotal.Add(1) // el contador SIEMPRE sube
	// pero el evento solo se emite con rate-limit por IP (#4), para no inflar
	// la tabla de eventos si una IP del feed aporrea el NAS.
	if intelShouldEmitObserve(ip) {
		shieldEmit(ShieldEvent{
			Category:  "intel",
			Severity:  "low", // observación: informativo, no es un bloqueo
			SourceIP:  ip,
			UserAgent: r.UserAgent(),
			Endpoint:  r.URL.Path,
			Method:    r.Method,
			Details: map[string]interface{}{
				"rule":         "INTEL-OBSERVE",
				"feed_version": st.feedVersion,
				"mode":         "observe",
				"note":         "habría bloqueado (modo observación)",
			},
		})
	}
	return false
}

// IntelStatus resume el estado del feed para el panel/API (Fase D).
type IntelStatus struct {
	Loaded          bool   `json:"loaded"`
	FeedVersion     int    `json:"feed_version"`
	SchemaVersion   int    `json:"schema_version"`
	SchemaSupported int    `json:"schema_supported"`
	SchemaAhead     bool   `json:"schema_ahead"`
	Prefixes        int    `json:"prefixes"`
	Source          string `json:"source"`         // embedded | cache | network | none
	ObserveOnly     bool   `json:"observe_only"`   // el feed viene en observación
	EnforceActive   bool   `json:"enforce_active"` // el admin activó el bloqueo
	GeneratedAt     string `json:"generated_at"`
	LoadedAt        string `json:"loaded_at"`
	ObservedTotal   int64  `json:"observed_total"` // matches observados (no bloqueados)
	BlockedTotal    int64  `json:"blocked_total"`  // bloqueos efectivos
}

func intelStatus() IntelStatus {
	st := IntelStatus{
		EnforceActive: intelEnforce.Load(),
		ObservedTotal: intelObservedTotal.Load(),
		BlockedTotal:  intelBlockedTotal.Load(),
	}
	cur := intelActive.Load() // snapshot consistente del feed vigente
	st.Loaded = cur.trie.size() > 0
	st.FeedVersion = cur.feedVersion
	st.SchemaVersion = cur.schemaVersion
	st.SchemaSupported = intelSupportedSchema
	st.SchemaAhead = cur.schemaVersion > intelSupportedSchema
	st.Prefixes = cur.trie.size()
	st.Source = cur.source
	st.ObserveOnly = cur.observeOnly
	st.GeneratedAt = cur.generatedAt
	if !cur.loadedAt.IsZero() {
		st.LoadedAt = cur.loadedAt.UTC().Format(time.RFC3339)
	}
	return st
}
