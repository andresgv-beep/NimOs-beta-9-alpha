// intel_scheduler.go — NimShield Intelligence · arranque y refresco automático.
//
// FASE D: al arrancar, carga el feed (caché si la hay, si no descarga). Luego
// refresca cada ~2 días en segundo plano. El flag de enforcement (bloquear en
// duro) se persiste en intel_meta para que sobreviva a reinicios.
package main

import (
	"time"
)

// intelRefreshInterval — cada cuánto NimOS busca un feed nuevo.
const intelRefreshInterval = 48 * time.Hour

// startIntel arranca el subsistema de inteligencia. No bloquea: lanza una
// goroutine que carga el feed y luego refresca periódicamente. Se llama desde
// startShieldEngine.
func startIntel() {
	// restaurar el flag de enforcement persistido (por defecto: observación)
	intelEnforce.Store(dbIntelGetEnforce())

	go func() {
		// 1. Arranque inmediato: primero intenta la red; si falla, la caché.
		if _, err := intelRefresh(); err != nil {
			logMsg("intel: refresh inicial falló (%v) — intento caché", err)
			if _, cerr := intelLoadFromCache(); cerr != nil {
				logMsg("intel: sin feed disponible todavía (%v)", cerr)
			}
		}

		// 2. Refresco periódico.
		ticker := time.NewTicker(intelRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := intelRefresh(); err != nil {
				logMsg("intel: refresh periódico falló: %v (se mantiene el feed vigente)", err)
			}
		}
	}()
}

// ─── Persistencia del flag de enforcement ───

func dbIntelGetEnforce() bool {
	if db == nil {
		return false
	}
	var v string
	db.QueryRow(`SELECT value FROM intel_meta WHERE key = 'enforce'`).Scan(&v)
	return v == "1"
}

func dbIntelSetEnforce(on bool) {
	if db == nil {
		return
	}
	v := "0"
	if on {
		v = "1"
	}
	db.Exec(`INSERT OR REPLACE INTO intel_meta (key, value) VALUES ('enforce', ?)`, v)
}

// intelSetEnforce activa/desactiva el bloqueo en duro y lo persiste.
func intelSetEnforce(on bool) {
	intelEnforce.Store(on)
	dbIntelSetEnforce(on)
	logMsg("intel: enforcement %s by admin", map[bool]string{true: "ENABLED", false: "disabled"}[on])
}
