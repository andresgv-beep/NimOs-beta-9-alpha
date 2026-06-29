// shield_reputation.go — NimShield · Capa de reputación por IP.
//
// IDEA: NimShield no debe tratar igual a tu equipo de cada día que a una IP
// que aparece por primera vez. Una IP que entra con ÉXITO de forma habitual
// se gana margen (no la bloqueamos por un par de despistes). Pero la
// confianza es VIVA, no un escudo: si una IP conocida empieza a fallar en
// ráfaga (3+ seguidos), NimShield DESCONFÍA y le quita el beneficio de la
// duda al instante — porque un habitual no falla 3 veces seguidas, pero un
// ladrón con su dispositivo sí.
//
// PRINCIPIOS DE SEGURIDAD (intocables):
//   · La reputación SOLO modula el umbral de fuerza-bruta de login
//     (AUTH-001). NO toca honeypots, inyección, scanner-UA ni traversal:
//     esas siguen instantáneas para CUALQUIER IP, tenga la reputación que
//     tenga. La confianza relaja UN vector, no abre la puerta.
//   · La reputación solo se gana con logins REALMENTE exitosos, verificados
//     server-side. No hay forma de inflarla desde fuera.
//   · Un login exitoso BORRA la racha de fallos: si aciertas la contraseña,
//     eras tú; si el atacante la acierta, ya no es ataque, es acceso válido.

package main

import (
	"time"
)

// Niveles de reputación según logins exitosos acumulados.
const (
	repHabitualThreshold = 10 // success_count ≥ → "habitual" (margen amplio)
	repKnownThreshold    = 1  // success_count ≥ → "conocida" (margen medio)

	// Umbrales de fallos (en la ventana de 5min de AUTH-001) según nivel.
	repThresholdUnknown  = 5  // como hoy
	repThresholdKnown    = 7  // algo más de margen
	repThresholdHabitual = 10 // margen amplio para despistes honestos
	repThresholdDistrust = 3  // habitual en racha de fallos → trato DURO

	// Una racha de este tamaño en una IP conocida dispara la desconfianza.
	repDistrustStreak = 3

	// Si el último fallo es más viejo que esto, la racha se considera
	// terminada: fallos espaciados en el tiempo son vida normal, no un
	// ataque. Solo las RÁFAGAS cuentan como racha.
	repStreakDecay = 15 * time.Minute
)

// dbShieldReputationInit crea la tabla. Se llama desde dbShieldInit.
func dbShieldReputationInit() {
	if db == nil {
		return
	}
	db.Exec(`
		CREATE TABLE IF NOT EXISTS shield_reputation (
			ip            TEXT PRIMARY KEY,
			success_count INTEGER NOT NULL DEFAULT 0,
			fail_streak   INTEGER NOT NULL DEFAULT 0,
			block_count   INTEGER NOT NULL DEFAULT 0,
			last_success  TEXT,
			last_fail     TEXT
		);
	`)
	db.Exec(`ALTER TABLE shield_reputation ADD COLUMN block_count INTEGER NOT NULL DEFAULT 0`)
	// Motor de comportamiento (Fase 1): score 0-100 y momento de su última
	// actualización (para el decay). ALTER idempotente: si ya existen, no pasa nada.
	db.Exec(`ALTER TABLE shield_reputation ADD COLUMN score INTEGER NOT NULL DEFAULT 100`)
	db.Exec(`ALTER TABLE shield_reputation ADD COLUMN last_score_update TEXT`)
}

// ShieldAuthSuccess registra un login EXITOSO: +1 a la cuenta y borra la
// racha de fallos. Gemelo de ShieldAuthFail. Se llama desde auth.go al
// crear sesión válida.
func ShieldAuthSuccess(ip string) {
	if db == nil || ip == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	db.Exec(`
		INSERT INTO shield_reputation (ip, success_count, fail_streak, last_success)
		VALUES (?, 1, 0, ?)
		ON CONFLICT(ip) DO UPDATE SET
			success_count = success_count + 1,
			fail_streak   = 0,
			last_success  = excluded.last_success
	`, ip, now)
}

// shieldRepRecordFail incrementa la racha de fallos (con decaimiento si el
// fallo anterior es viejo) y devuelve la racha resultante y los éxitos
// acumulados. Crea la fila si la IP es nueva.
func shieldRepRecordFail(ip string) (failStreak, successCount int) {
	if db == nil || ip == "" {
		return 0, 0
	}
	now := time.Now().UTC()

	var prevStreak, prevSuccess int
	var lastFailStr string
	err := db.QueryRow(
		`SELECT success_count, fail_streak, COALESCE(last_fail, '') FROM shield_reputation WHERE ip = ?`,
		ip).Scan(&prevSuccess, &prevStreak, &lastFailStr)

	streak := 1
	if err == nil {
		// Fila existente: ¿la racha sigue viva o estaba fría?
		fresh := true
		if lastFailStr != "" {
			if t, perr := time.Parse(time.RFC3339, lastFailStr); perr == nil {
				if now.Sub(t) <= repStreakDecay {
					fresh = false
				}
			}
		}
		if fresh {
			streak = 1 // racha vieja terminada → empieza una nueva
		} else {
			streak = prevStreak + 1
		}
	}

	nowStr := now.Format(time.RFC3339)
	db.Exec(`
		INSERT INTO shield_reputation (ip, success_count, fail_streak, last_fail)
		VALUES (?, 0, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET
			fail_streak = ?,
			last_fail   = excluded.last_fail
	`, ip, streak, nowStr, streak)

	return streak, prevSuccess
}

// shieldLoginFailThreshold decide cuántos fallos (en la ventana AUTH-001)
// tolera esta IP antes del bloqueo, según su reputación y su racha actual.
// Devuelve también si está en modo desconfianza (para endurecer la duración).
func shieldLoginFailThreshold(successCount, failStreak int) (threshold int, distrust bool) {
	// DESCONFIANZA: una IP CONOCIDA fallando en ráfaga es anómalo
	// (dispositivo robado / credenciales filtradas). Le quitamos el margen
	// de golpe — trato duro pase lo que pase con su historial.
	if successCount > 0 && failStreak >= repDistrustStreak {
		return repThresholdDistrust, true
	}
	switch {
	case successCount >= repHabitualThreshold:
		return repThresholdHabitual, false
	case successCount >= repKnownThreshold:
		return repThresholdKnown, false
	default:
		return repThresholdUnknown, false
	}
}

// shieldRepLevel traduce (éxitos, racha) a una etiqueta legible para la UI.
func shieldRepLevel(successCount, failStreak int) string {
	if successCount > 0 && failStreak >= repDistrustStreak {
		return "distrust" // conocida en racha de fallos → desconfianza
	}
	switch {
	case successCount >= repHabitualThreshold:
		return "habitual"
	case successCount >= repKnownThreshold:
		return "known"
	default:
		return "unknown"
	}
}

// shieldRepList devuelve las IPs con reputación registrada, de más a menos
// activas, con su nivel ya calculado para que la UI solo renderice.
func shieldRepList() []map[string]interface{} {
	out := []map[string]interface{}{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`
		SELECT ip, success_count, fail_streak, COALESCE(last_success,''), COALESCE(last_fail,''),
		       score, COALESCE(last_score_update,'')
		FROM shield_reputation
		ORDER BY score ASC, success_count DESC, last_success DESC
		LIMIT 500
	`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ip, lastSuccess, lastFail, lastScoreUpd string
		var success, streak, score int
		if err := rows.Scan(&ip, &success, &streak, &lastSuccess, &lastFail, &score, &lastScoreUpd); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"ip":           ip,
			"successCount": success,
			"failStreak":   streak,
			"lastSuccess":  lastSuccess,
			"lastFail":     lastFail,
			"level":        shieldRepLevel(success, streak),
			"score":        scoreApplyDecay(score, lastScoreUpd), // 0-100, con recuperación aplicada
		})
	}
	return out
}

// shieldRepForget borra la reputación de una IP (vuelve a "desconocida").
// Útil si sospechas que una IP de confianza se ha comprometido: la degradas
// y vuelve al trato estricto.
func shieldRepForget(ip string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM shield_reputation WHERE ip = ?`, ip)
	return err
}

// shieldRepRecordBlock incrementa el contador de bloqueos de una IP y
// devuelve cuántos bloqueos PREVIOS tenía (0 = primera vez). Alimenta el
// escalado de duración por reincidencia.
func shieldRepRecordBlock(ip string) int {
	if db == nil || ip == "" {
		return 0
	}
	var prev int
	db.QueryRow(`SELECT block_count FROM shield_reputation WHERE ip = ?`, ip).Scan(&prev)
	db.Exec(`
		INSERT INTO shield_reputation (ip, block_count) VALUES (?, 1)
		ON CONFLICT(ip) DO UPDATE SET block_count = block_count + 1
	`, ip)
	return prev
}
