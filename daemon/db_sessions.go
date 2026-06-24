package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func dbSessionsDeleteByUsername(username string) {
	db.Exec(`DELETE FROM sessions WHERE username = ?`, username)
}

func dbSessionCreate(token, username, role, ip string) error {
	now := time.Now().UnixMilli()
	expires := now + sessionExpiryMs
	_, err := db.Exec(`INSERT OR REPLACE INTO sessions (token, username, role, created_at, expires_at, ip) VALUES (?, ?, ?, ?, ?, ?)`,
		token, username, role, now, expires, ip)
	return err
}

func dbSessionGet(token string) (*DBSession, error) {
	var s DBSession
	err := db.QueryRow(`SELECT username, role, created_at, expires_at, ip FROM sessions WHERE token = ?`, token).
		Scan(&s.Username, &s.Role, &s.CreatedAt, &s.ExpiresAt, &s.IP)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}
	if time.Now().UnixMilli() > s.ExpiresAt {
		db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, fmt.Errorf("session expired")
	}
	// Sliding expiry: renew on each use so active users stay logged in
	newExpiry := time.Now().UnixMilli() + sessionExpiryMs
	db.Exec(`UPDATE sessions SET expires_at = ? WHERE token = ?`, newExpiry, token)
	return &s, nil
}

func dbSessionDelete(token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func dbSessionCleanup() int64 {
	now := time.Now().UnixMilli()
	result, _ := db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	n, _ := result.RowsAffected()
	// Also clean expired download tokens
	db.Exec(`DELETE FROM download_tokens WHERE expires_at < ?`, now)
	return n
}

func createDownloadTokensTable() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS download_tokens (
		token      TEXT PRIMARY KEY,
		username   TEXT NOT NULL,
		role       TEXT NOT NULL,
		share      TEXT NOT NULL,
		path       TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`)
	return err
}

func dbDownloadTokenCreate(username, role, share, path string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	hashed := sha256Hex(token)
	now := time.Now().UnixMilli()
	expires := now + downloadTokenExpiryMs
	_, err := db.Exec(`INSERT INTO download_tokens (token, username, role, share, path, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashed, username, role, share, path, now, expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

// dbDownloadTokenConsume validates and deletes (one-time-use) a download token.
// Returns username, role, share, path if valid.
func dbDownloadTokenConsume(rawToken string) (string, string, string, string, error) {
	hashed := sha256Hex(rawToken)
	var username, role, share, path string
	var expiresAt int64
	err := db.QueryRow(`SELECT username, role, share, path, expires_at FROM download_tokens WHERE token = ?`, hashed).
		Scan(&username, &role, &share, &path, &expiresAt)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid download token")
	}
	// Always delete (one-time-use)
	db.Exec(`DELETE FROM download_tokens WHERE token = ?`, hashed)
	if time.Now().UnixMilli() > expiresAt {
		return "", "", "", "", fmt.Errorf("download token expired")
	}
	return username, role, share, path, nil
}
