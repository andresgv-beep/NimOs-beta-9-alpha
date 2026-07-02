// shield_score.go — NimShield · Motor de comportamiento (score + correlación).
//
// Asigna a cada clave de red un SCORE de comportamiento 0-100 (100 = confianza
// total) que agrega TODAS las señales del shield sobre el tiempo, no solo el
// login. Cada evento de detección resta puntos según su severidad; si una clave
// combina varios vectores en poco tiempo (honeypot + scan + injection) se aplica
// una penalización de CORRELACIÓN. El score se RECUPERA solo con el tiempo si no
// hay eventos nuevos, así un desliz puntual se desvanece y solo el ataque
// sostenido hunde el score.
//
// FASE 1 (siempre activa): calcula, registra y correla.
// FASE 2 (toggle de admin, default OFF — patrón intelEnforce): al cruzar el
// umbral hacia abajo, AUTO-BLOQUEA (BEHAV-001) con la duración escalada por
// reincidencia. Salvaguardas: jamás por eventos de una sesión válida (el score
// los cuenta, pero no gatillan bloqueo — mismo criterio que los payload rules)
// y jamás contra una IP whitelisteada. Validado en observación: 3 días sin un
// solo falso positivo y un atacante real cazado (2026-06-29, score 19).
package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

const (
	scoreMax             = 100
	scoreStart           = 100 // una IP sin historial empieza con confianza total
	scoreBlockThreshold  = 30  // < umbral → auto-bloqueo si la Fase 2 está armada
	scoreRecoveryPerHour = 5   // puntos/hora de recuperación hacia 100 sin eventos nuevos

	// Correlación: ≥ N categorías de ataque distintas en la ventana = multi-vector.
	scoreCorrelationWindow = "-10 minutes" // modificador SQLite (coherente con el texto del log)
	scoreCorrelationMin    = 3
	scoreCorrelationExtra  = 30 // penalización extra por ataque correlado
)

// shieldBehavEnforce — Fase 2 en caliente: si está armada, cruzar el umbral
// hacia abajo bloquea de verdad. Persistido en shield_settings
// ('behavior_enforce'); por defecto OFF (el admin la arma, como el intel).
var shieldBehavEnforce atomic.Bool

func dbShieldSetBehavEnforce(enabled bool) {
	v := "0"
	if enabled {
		v = "1"
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO shield_settings (key, value) VALUES ('behavior_enforce', ?)`, v); err != nil {
		logMsg("behav: no pude persistir el flag: %v", err)
	}
}

// loadShieldBehavEnforce lee el flag persistido. Sin fila → OFF.
func loadShieldBehavEnforce() {
	var v string
	if err := db.QueryRow(`SELECT value FROM shield_settings WHERE key = 'behavior_enforce'`).Scan(&v); err != nil {
		return
	}
	shieldBehavEnforce.Store(v == "1")
}

// scorePenalty traduce la severidad de un evento a puntos a restar del score.
func scorePenalty(severity string) int {
	switch severity {
	case "critical": // honeypot, command injection…
		return 50
	case "high": // scanner-UA, XSS/SQLi, traversal…
		return 25
	case "medium":
		return 12
	default: // low / desconocida (p.ej. match del feed en observación)
		return 6
	}
}

// scoreApplyDecay recupera el score hacia 100 según el tiempo transcurrido desde
// la última actualización (recuperación lineal). Nunca baja, solo sube.
func scoreApplyDecay(score int, lastUpdate string) int {
	if lastUpdate == "" {
		return score
	}
	t, err := time.Parse(time.RFC3339, lastUpdate)
	if err != nil {
		return score
	}
	hours := time.Since(t).Hours()
	if hours <= 0 {
		return score
	}
	recovered := score + int(hours*float64(scoreRecoveryPerHour))
	if recovered > scoreMax {
		return scoreMax
	}
	return recovered
}

// shieldScoreRead devuelve el score ACTUAL de una IP (recuperación por tiempo ya
// aplicada). scoreStart si la IP no tiene historial. El score vive en la tabla
// de reputación, así que agrega por CLAVE DE RED (IPv6 → /64, shieldNetKey).
func shieldScoreRead(ip string) int {
	if db == nil || ip == "" {
		return scoreStart
	}
	ip = shieldNetKey(ip)
	score := scoreStart
	var last string
	if err := db.QueryRow(
		`SELECT score, COALESCE(last_score_update,'') FROM shield_reputation WHERE ip = ?`,
		ip).Scan(&score, &last); err != nil {
		return scoreStart // sin fila → confianza por defecto
	}
	return scoreApplyDecay(score, last)
}

// shieldScoreCorrelate cuenta cuántas CATEGORÍAS de ataque distintas ha generado
// una CLAVE DE RED en la ventana de correlación. ≥ scoreCorrelationMin =
// multi-vector. Correlar por net_key (no por source_ip) evita que un atacante
// IPv6 disperse sus vectores entre IPs de su /64 para no correlar nunca.
// (Eventos anteriores a la columna net_key tienen NULL y no correlan; expiran
// con la retención de 7 días.)
func shieldScoreCorrelate(ip string) int {
	if db == nil || ip == "" {
		return 0
	}
	var n int
	db.QueryRow(
		`SELECT COUNT(DISTINCT category) FROM shield_events
		 WHERE net_key = ? AND created_at >= datetime('now', ?)`,
		shieldNetKey(ip), scoreCorrelationWindow).Scan(&n)
	return n
}

// shieldScorePenalize aplica el efecto de un evento de detección sobre el score
// de su IP. Se llama desde el event loop (async, FUERA del hot path). En Fase 1
// solo REGISTRA si la IP cruza el umbral de bloqueo; NO bloquea.
func shieldScorePenalize(event ShieldEvent) {
	ip := event.SourceIP
	if db == nil || ip == "" {
		return
	}
	ip = shieldNetKey(ip) // el score agrega por clave de red

	old := shieldScoreRead(ip) // score actual, con decay aplicado

	penalty := scorePenalty(event.Severity)
	correlated := false
	if shieldScoreCorrelate(ip) >= scoreCorrelationMin {
		penalty += scoreCorrelationExtra
		correlated = true
	}

	newScore := old - penalty
	if newScore < 0 {
		newScore = 0
	}

	now := time.Now().UTC().Format(time.RFC3339)
	db.Exec(`
		INSERT INTO shield_reputation (ip, score, last_score_update) VALUES (?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET score = excluded.score, last_score_update = excluded.last_score_update
	`, ip, newScore, now)

	// Cruce del umbral hacia abajo: auto-bloqueo (Fase 2) o registro (observación).
	if old >= scoreBlockThreshold && newScore < scoreBlockThreshold {
		extra := ""
		if correlated {
			extra = " [multi-vector correlado]"
		}

		// Salvaguardas del auto-bloqueo (mismo criterio que los payload rules):
		//   · un evento de SESIÓN VÁLIDA nunca gatilla bloqueo — el score lo
		//     cuenta (observabilidad), pero el caso backtick/compose del dueño
		//     no puede convertirse en bloqueo por la puerta de atrás;
		//   · una IP whitelisteada jamás se bloquea (el check va sobre la IP
		//     real del evento: cubre exactas y rangos CIDR).
		if eventIsAuthenticated(event) || shieldIsWhitelisted(event.SourceIP) {
			logMsg("behav: %s cruzó el umbral (score %d, por %s/%s)%s — exenta de auto-bloqueo (sesión válida o whitelist)",
				ip, newScore, event.Category, event.Severity, extra)
			return
		}

		if !shieldBehavEnforce.Load() {
			logMsg("behav: %s cruzó el umbral de comportamiento (score %d, por %s/%s)%s — HABRÍA AUTO-BLOQUEADO [OBSERVE]",
				ip, newScore, event.Category, event.Severity, extra)
			return
		}

		// FASE 2 armada: bloqueo real, con la duración escalada por
		// reincidencia (misma política que AUTH-001; shieldBlockIP incrementa
		// el contador y, si ya es reincidente, escala también al firewall).
		dur := escalatedBlockDuration(getShieldConfig(), shieldRepBlockCount(ip))
		reason := fmt.Sprintf("Comportamiento malicioso sostenido (score %d)%s", newScore, extra)
		logMsg("behav: %s AUTO-BLOQUEADA por comportamiento (score %d, por %s/%s)%s [BEHAV-001]",
			ip, newScore, event.Category, event.Severity, extra)
		shieldBlockIP(ip, dur, reason, "BEHAV-001")
	}
}
