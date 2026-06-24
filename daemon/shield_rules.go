package main

import (
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════════
// NimShield — Rule Engine
// Deterministic rules with sliding window counters
// ═══════════════════════════════════════════════════════════════════════════════

// ── Sliding Window Counter ───────────────────────────────────────────────────

type windowEntry struct {
	timestamps []time.Time
}

type SlidingWindow struct {
	mu      sync.Mutex
	entries map[string]*windowEntry // key → timestamps
}

func newSlidingWindow() *SlidingWindow {
	return &SlidingWindow{entries: map[string]*windowEntry{}}
}

// count returns events in the window, and adds the current timestamp
func (sw *SlidingWindow) countAndAdd(key string, window time.Duration) int {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	entry, exists := sw.entries[key]
	if !exists {
		entry = &windowEntry{}
		sw.entries[key] = entry
	}

	// Expire old entries
	fresh := make([]time.Time, 0, len(entry.timestamps))
	for _, t := range entry.timestamps {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	fresh = append(fresh, now)
	entry.timestamps = fresh

	return len(fresh)
}

// count returns events in window without adding
func (sw *SlidingWindow) count(key string, window time.Duration) int {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	entry, exists := sw.entries[key]
	if !exists {
		return 0
	}

	cutoff := time.Now().Add(-window)
	count := 0
	for _, t := range entry.timestamps {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// cleanup removes expired entries (call periodically)
func (sw *SlidingWindow) cleanup(maxAge time.Duration) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, entry := range sw.entries {
		fresh := make([]time.Time, 0)
		for _, t := range entry.timestamps {
			if t.After(cutoff) {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			delete(sw.entries, key)
		} else {
			entry.timestamps = fresh
		}
	}
}

// ── Rule Windows (global counters) ───────────────────────────────────────────

var (
	authFailWindow  = newSlidingWindow() // AUTH-001: login failures per IP
	authUserWindow  = newSlidingWindow() // AUTH-002: distinct users per IP
	tokenFailWindow = newSlidingWindow() // AUTH-003: invalid tokens per IP
	scanWindow      = newSlidingWindow() // SCAN-001: 404s per IP
	apiEnumWindow   = newSlidingWindow() // SCAN-002: distinct endpoints per IP
	travWindow      = newSlidingWindow() // TRAV-001: traversal attempts per IP
	sqliWindow      = newSlidingWindow() // INJ-001: SQLi attempts per IP
	xssWindow       = newSlidingWindow() // INJ-003/CSP-001: XSS attempts per IP
)

// ── Rule Processor ───────────────────────────────────────────────────────────

func processRules(event ShieldEvent) {
	ip := event.SourceIP
	if ip == "" || shieldIsWhitelisted(ip) {
		return
	}

	switch event.Category {

	case "auth":
		processAuthRules(event)

	case "scan":
		// SCAN-001: Port scan / 404 enumeration
		if event.Status == 404 {
			count := scanWindow.countAndAdd("ip:"+ip, 1*time.Minute)
			if count >= 10 {
				shieldBlockIP(ip, 30*time.Minute, "Port scan: 10+ 404s in 1min", "SCAN-001")
			}
		}
		// SCAN-002: API enumeration
		apiEnumWindow.countAndAdd("ip:"+ip+":"+event.Endpoint, 2*time.Minute)
		distinctEndpoints := apiEnumWindow.count("ip:"+ip, 2*time.Minute)
		if distinctEndpoints >= 20 {
			shieldBlockIP(ip, 30*time.Minute, "API enumeration: 20+ endpoints in 2min", "SCAN-002")
		}

	case "traversal":
		// TRAV-001: Path traversal
		count := travWindow.countAndAdd("ip:"+ip, 1*time.Minute)
		if count >= 3 {
			shieldBlockIP(ip, 2*time.Hour, "Path traversal: 3+ attempts in 1min", "TRAV-001")
		}

	case "injection":
		rule, _ := event.Details["rule"].(string)
		switch rule {
		case "INJ-001":
			count := sqliWindow.countAndAdd("ip:"+ip, 5*time.Minute)
			if count >= 3 {
				shieldBlockIP(ip, 2*time.Hour, "SQL injection: 3+ attempts in 5min", "INJ-001")
			}
		case "CSP-001", "INJ-003":
			count := xssWindow.countAndAdd("ip:"+ip, 5*time.Minute)
			if count >= 5 {
				shieldBlockIP(ip, 1*time.Hour, "XSS attack: 5+ attempts in 5min", "INJ-003")
			}
		// INJ-002 (command injection) is handled in middleware — instant block
		}

	case "honeypot":
		// Already handled in middleware — instant block
	}
}

// ── Auth Rules ───────────────────────────────────────────────────────────────

func processAuthRules(event ShieldEvent) {
	ip := event.SourceIP

	detailType, _ := event.Details["type"].(string)

	switch detailType {
	case "login_fail":
		// AUTH-001: Brute force con UMBRAL DINÁMICO por reputación.
		// Registramos el fallo (racha++) y dejamos que la reputación decida
		// cuántos fallos toleramos: una IP habitual tiene margen para
		// despistes; una IP conocida en racha de fallos (dispositivo
		// robado) pierde el margen al instante (modo desconfianza).
		failStreak, successCount := shieldRepRecordFail(ip)
		cfg := getShieldConfig()
		windowCount := authFailWindow.countAndAdd("ip:"+ip, 5*time.Minute)
		block, distrust := shieldAuthDecision(cfg, successCount, failStreak, windowCount)
		if block {
			// Escalado por reincidencia: cada bloqueo previo de esta IP sube
			// el siguiente (5min → 15min → 1h → 24h, configurable).
			offenses := shieldRepRecordBlock(ip)
			dur := escalatedBlockDuration(cfg, offenses)
			reason := "Brute force: login failures over threshold"
			if distrust {
				reason = "Distrust: known IP failing in burst (possible stolen device)"
			}
			shieldBlockIP(ip, dur, reason, "AUTH-001")
			return
		}

		// AUTH-002: Credential stuffing — 3+ different users / 2min / IP
		username, _ := event.Details["username"].(string)
		if username != "" {
			authUserWindow.countAndAdd("ip:"+ip+":user:"+username, 2*time.Minute)
			// Count distinct users from this IP
			distinctUsers := countDistinctUsers(ip, 2*time.Minute)
			if distinctUsers >= 3 {
				shieldBlockIP(ip, 1*time.Hour, "Credential stuffing: 3+ users in 2min", "AUTH-002")
			}
		}

	case "token_invalid":
		// AUTH-003: Token spray — 10+ invalid tokens / 1min / IP
		count := tokenFailWindow.countAndAdd("ip:"+ip, 1*time.Minute)
		if count >= 10 {
			shieldBlockIP(ip, 1*time.Hour, "Token spray: 10+ invalid tokens in 1min", "AUTH-003")
		}
	}
}

// countDistinctUsers counts unique usernames tried from an IP in a window
func countDistinctUsers(ip string, window time.Duration) int {
	authUserWindow.mu.Lock()
	defer authUserWindow.mu.Unlock()

	cutoff := time.Now().Add(-window)
	prefix := "ip:" + ip + ":user:"
	seen := map[string]bool{}

	for key, entry := range authUserWindow.entries {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			for _, t := range entry.timestamps {
				if t.After(cutoff) {
					user := key[len(prefix):]
					seen[user] = true
					break
				}
			}
		}
	}

	return len(seen)
}

// ── Emit helpers (called from other modules) ─────────────────────────────────

// ShieldAuthFail emits a login failure event. Call from auth.go on invalid credentials.
func ShieldAuthFail(ip, username, ua string) {
	shieldEmit(ShieldEvent{
		Category:  "auth",
		Severity:  "medium",
		SourceIP:  ip,
		Username:  username,
		UserAgent: ua,
		Endpoint:  "/api/auth/login",
		Details:   map[string]interface{}{"type": "login_fail", "username": username},
	})
}

// ShieldAuthTokenFail emits an invalid token event. Call from auth.go on bad bearer token.
func ShieldAuthTokenFail(ip, ua, endpoint string) {
	shieldEmit(ShieldEvent{
		Category:  "auth",
		Severity:  "low",
		SourceIP:  ip,
		UserAgent: ua,
		Endpoint:  endpoint,
		Details:   map[string]interface{}{"type": "token_invalid"},
	})
}

// Shield404 emits a 404 event for scan detection.
func Shield404(ip, ua, path string) {
	shieldEmit(ShieldEvent{
		Category: "scan",
		Severity: "low",
		SourceIP: ip,
		UserAgent: ua,
		Endpoint: path,
		Status:   404,
		Details:  map[string]interface{}{"type": "not_found"},
	})
}
