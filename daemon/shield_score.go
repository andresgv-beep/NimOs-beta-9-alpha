// shield_score.go — NimShield · Motor de comportamiento (Fase 1: score + correlación).
//
// Asigna a cada IP un SCORE de comportamiento 0-100 (100 = confianza total) que
// agrega TODAS las señales del shield sobre el tiempo, no solo el login. Cada
// evento de detección resta puntos según su severidad; si una IP combina varios
// vectores en poco tiempo (honeypot + scan + injection) se aplica una penalización
// de CORRELACIÓN. El score se RECUPERA solo con el tiempo si no hay eventos nuevos,
// así un desliz puntual se desvanece y solo el ataque sostenido hunde el score.
//
// FASE 1 = OBSERVACIÓN: calcula y registra "habría auto-bloqueado", pero NO bloquea.
// El auto-bloqueo llega en la Fase 2 (con toggle de admin, como el feed intel).
package main

import (
	"time"
)

const (
	scoreMax             = 100
	scoreStart           = 100 // una IP sin historial empieza con confianza total
	scoreBlockThreshold  = 30  // < umbral → candidata a auto-bloqueo (Fase 1 solo registra)
	scoreRecoveryPerHour = 5   // puntos/hora de recuperación hacia 100 sin eventos nuevos

	// Correlación: ≥ N categorías de ataque distintas en la ventana = multi-vector.
	scoreCorrelationWindow = "-10 minutes" // modificador SQLite (coherente con el texto del log)
	scoreCorrelationMin    = 3
	scoreCorrelationExtra  = 30 // penalización extra por ataque correlado
)

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
// aplicada). scoreStart si la IP no tiene historial.
func shieldScoreRead(ip string) int {
	if db == nil || ip == "" {
		return scoreStart
	}
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
// una IP en la ventana de correlación. ≥ scoreCorrelationMin = multi-vector.
func shieldScoreCorrelate(ip string) int {
	if db == nil || ip == "" {
		return 0
	}
	var n int
	db.QueryRow(
		`SELECT COUNT(DISTINCT category) FROM shield_events
		 WHERE source_ip = ? AND created_at >= datetime('now', ?)`,
		ip, scoreCorrelationWindow).Scan(&n)
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

	// Cruce del umbral hacia abajo: registrar (Fase 1 = observación, NO bloquea).
	if old >= scoreBlockThreshold && newScore < scoreBlockThreshold {
		extra := ""
		if correlated {
			extra = " [multi-vector correlado]"
		}
		logMsg("behav: IP %s cruzó el umbral de comportamiento (score %d, por %s/%s)%s — HABRÍA AUTO-BLOQUEADO [OBSERVE]",
			ip, newScore, event.Category, event.Severity, extra)
	}
}
