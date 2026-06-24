package main

import (
	"fmt"
	"time"
)

func checkRateLimit(key string) (bool, string) {
	rateLimitsMu.Lock()
	defer rateLimitsMu.Unlock()

	r, ok := rateLimits[key]
	if !ok {
		return true, ""
	}
	now := time.Now().UnixMilli()
	if r.lockedUntil > 0 && now < r.lockedUntil {
		remaining := (r.lockedUntil - now) / 60000
		if remaining < 1 {
			remaining = 1
		}
		return false, fmt.Sprintf("Too many attempts. Try again in %d minutes.", remaining)
	}
	if r.lockedUntil > 0 && now >= r.lockedUntil {
		delete(rateLimits, key)
		return true, ""
	}
	return true, ""
}

func recordFailedAttempt(key string) {
	rateLimitsMu.Lock()
	defer rateLimitsMu.Unlock()

	now := time.Now().UnixMilli()
	r, ok := rateLimits[key]
	if !ok {
		r = &rateLimitEntry{}
		rateLimits[key] = r
	}
	if now-r.lastAttempt > int64(lockoutDuration) {
		r.count = 0
	}
	r.count++
	r.lastAttempt = now
	if r.count >= maxLoginAttempts {
		r.lockedUntil = now + int64(lockoutDuration)
	}
}

func clearFailedAttempts(key string) {
	rateLimitsMu.Lock()
	defer rateLimitsMu.Unlock()
	delete(rateLimits, key)
}

// Periodic cleanup of old rate limit entries
func startRateLimitCleanup() {
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			rateLimitsMu.Lock()
			now := time.Now().UnixMilli()
			for k, r := range rateLimits {
				if now-r.lastAttempt > int64(lockoutDuration)*2 {
					delete(rateLimits, k)
				}
			}
			rateLimitsMu.Unlock()
		}
	}()
}
