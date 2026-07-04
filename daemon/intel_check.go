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

// ─── Persistencia de contadores ───
//
// Los atómicos son la verdad en caliente; intel_meta guarda un snapshot para
// que los totales sobrevivan reinicios del daemon (antes se reseteaban a 0 en
// cada deploy y el panel mentía). El flush es periódico (startIntel) y al
// apagar (installShutdownHandler) — NUNCA en el hot path: una IP listada
// aporreando el NAS no debe traducirse en un write SQLite por petición.
// Pérdida máxima en un crash duro: una ventana de flush.

const intelCounterFlushInterval = 60 * time.Second

// Último valor persistido de cada contador · el flush compara contra esto
// para ser no-op (dos loads atómicos) cuando no hay tráfico del feed.
var (
	intelSavedObserved atomic.Int64
	intelSavedBlocked  atomic.Int64
)

// dbIntelLoadCounters restaura los contadores desde intel_meta al arrancar.
// Si no hay filas (primera ejecución), los atómicos se quedan en 0.
func dbIntelLoadCounters() {
	if db == nil {
		return
	}
	var v int64
	if err := db.QueryRow(`SELECT value FROM intel_meta WHERE key = 'observed_total'`).Scan(&v); err == nil {
		intelObservedTotal.Store(v)
		intelSavedObserved.Store(v)
	}
	if err := db.QueryRow(`SELECT value FROM intel_meta WHERE key = 'blocked_total'`).Scan(&v); err == nil {
		intelBlockedTotal.Store(v)
		intelSavedBlocked.Store(v)
	}
}

// intelFlushCounters persiste los contadores que hayan cambiado desde el
// último flush. Barato de llamar aunque no haya cambios.
func intelFlushCounters() {
	if db == nil {
		return
	}
	if o := intelObservedTotal.Load(); o != intelSavedObserved.Load() {
		if _, err := db.Exec(`INSERT OR REPLACE INTO intel_meta (key, value) VALUES ('observed_total', ?)`, o); err == nil {
			intelSavedObserved.Store(o)
		}
	}
	if b := intelBlockedTotal.Load(); b != intelSavedBlocked.Load() {
		if _, err := db.Exec(`INSERT OR REPLACE INTO intel_meta (key, value) VALUES ('blocked_total', ?)`, b); err == nil {
			intelSavedBlocked.Store(b)
		}
	}
}

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
// (no se ha emitido uno en la última ventana de cooldown). El cooldown va por
// CLAVE DE RED (IPv6 → /64): el feed lista rangos, así que una red listada
// rotando de IP no debe poder inflar la tabla de eventos saltándose la ventana.
func intelShouldEmitObserve(ip string) bool {
	ip = shieldNetKey(ip)
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
