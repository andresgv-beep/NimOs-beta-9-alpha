package main

// ═══════════════════════════════════════════════════════════════════
// TEMP SHARES · enlaces temporales de archivos (Files → Compartir)
// ───────────────────────────────────────────────────────────────────
// Módulo autocontenido: tabla + CRUD + limpieza + semáforo de
// descargas concurrentes. La capa HTTP vive en tempshares_http.go.
//
// Concepto: un enlace público /s/{token} que sirve UN archivo de un
// share durante un tiempo limitado. Distinto de download_tokens
// (one-shot interno de Files) y de las carpetas SMB persistentes.
//
// Decisión: el token se guarda EN CLARO porque la UI de gestión
// (Panel de Control → Enlaces compartidos) necesita re-mostrar el
// enlace completo. Es una capability URL de vida corta y revocable;
// el riesgo de fuga de DB lo cubre la caducidad + revocación.
// ═══════════════════════════════════════════════════════════════════

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

const (
	tempShareTokenLen   = 10  // base62 → ~59 bits de entropía
	tempShareMaxTTLHrs  = 720 // 30 días de tope
	tempShareGraceMs    = 24 * 60 * 60 * 1000 // expirados visibles 24h, luego se limpian
)

type TempShare struct {
	Token         string `json:"token"`
	Share         string `json:"share"`
	Path          string `json:"path"`      // relativa al share (ya validada)
	FileName      string `json:"fileName"`
	SizeBytes     int64  `json:"sizeBytes"`
	Scope         string `json:"scope"` // "lan" | "public"
	CreatedBy     string `json:"createdBy"`
	CreatedAt     int64  `json:"createdAt"`
	ExpiresAt     int64  `json:"expiresAt"`
	HasPassword   bool   `json:"hasPassword"`
	passwordHash  string // interno, nunca sale por JSON
	MaxConcurrent int    `json:"maxConcurrent"` // 0 = ilimitadas
	Downloads     int64  `json:"downloads"`     // contador total (informativo)
}

func createTempSharesTable() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS temp_shares (
		token          TEXT PRIMARY KEY,
		share          TEXT NOT NULL,
		path           TEXT NOT NULL,
		file_name      TEXT NOT NULL,
		size_bytes     INTEGER NOT NULL DEFAULT 0,
		scope          TEXT NOT NULL DEFAULT 'public',
		created_by     TEXT NOT NULL,
		created_at     INTEGER NOT NULL,
		expires_at     INTEGER NOT NULL,
		password_hash  TEXT NOT NULL DEFAULT '',
		max_concurrent INTEGER NOT NULL DEFAULT 0,
		downloads      INTEGER NOT NULL DEFAULT 0
	)`)
	return err
}

// tempShareNewToken genera un id corto apto para URL (base62).
func tempShareNewToken() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, tempShareTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw), nil
}

func dbTempShareCreate(ts *TempShare) error {
	token, err := tempShareNewToken()
	if err != nil {
		return err
	}
	ts.Token = token
	ts.CreatedAt = time.Now().UnixMilli()
	_, err = db.Exec(`INSERT INTO temp_shares
		(token, share, path, file_name, size_bytes, scope, created_by,
		 created_at, expires_at, password_hash, max_concurrent, downloads)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		ts.Token, ts.Share, ts.Path, ts.FileName, ts.SizeBytes, ts.Scope,
		ts.CreatedBy, ts.CreatedAt, ts.ExpiresAt, ts.passwordHash, ts.MaxConcurrent)
	return err
}

func scanTempShare(scan func(dest ...interface{}) error) (*TempShare, error) {
	var ts TempShare
	err := scan(&ts.Token, &ts.Share, &ts.Path, &ts.FileName, &ts.SizeBytes,
		&ts.Scope, &ts.CreatedBy, &ts.CreatedAt, &ts.ExpiresAt,
		&ts.passwordHash, &ts.MaxConcurrent, &ts.Downloads)
	if err != nil {
		return nil, err
	}
	ts.HasPassword = ts.passwordHash != ""
	return &ts, nil
}

const tempShareCols = `token, share, path, file_name, size_bytes, scope,
	created_by, created_at, expires_at, password_hash, max_concurrent, downloads`

func dbTempShareGet(token string) (*TempShare, error) {
	row := db.QueryRow(`SELECT `+tempShareCols+` FROM temp_shares WHERE token = ?`, token)
	return scanTempShare(row.Scan)
}

// dbTempShareList devuelve todos los enlaces (admin) o solo los del usuario.
func dbTempShareList(username string, all bool) ([]*TempShare, error) {
	q := `SELECT ` + tempShareCols + ` FROM temp_shares ORDER BY created_at DESC`
	args := []interface{}{}
	if !all {
		q = `SELECT ` + tempShareCols + ` FROM temp_shares WHERE created_by = ? ORDER BY created_at DESC`
		args = append(args, username)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TempShare{}
	for rows.Next() {
		ts, serr := scanTempShare(rows.Scan)
		if serr != nil {
			return nil, serr
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// dbTempShareUpdate aplica la reconfiguración (solo campos gestionables).
func dbTempShareUpdate(ts *TempShare) error {
	_, err := db.Exec(`UPDATE temp_shares SET
		scope = ?, expires_at = ?, password_hash = ?, max_concurrent = ?
		WHERE token = ?`,
		ts.Scope, ts.ExpiresAt, ts.passwordHash, ts.MaxConcurrent, ts.Token)
	return err
}

func dbTempShareDelete(token string) error {
	_, err := db.Exec(`DELETE FROM temp_shares WHERE token = ?`, token)
	return err
}

func dbTempShareCountDownload(token string) {
	db.Exec(`UPDATE temp_shares SET downloads = downloads + 1 WHERE token = ?`, token)
}

// dbTempShareCleanup borra los expirados hace más de 24h (lazy: se invoca
// desde list/create, sin goroutine propia). Los expirados recientes se
// conservan para que la UI de gestión los muestre como "expirado".
func dbTempShareCleanup() {
	cutoff := time.Now().UnixMilli() - tempShareGraceMs
	db.Exec(`DELETE FROM temp_shares WHERE expires_at < ?`, cutoff)
}

// dbTempShareDeleteExpired borra TODOS los expirados ya (botón "Limpiar
// expirados" de la UI). Respeta ownership: admin limpia todo, usuario lo suyo.
func dbTempShareDeleteExpired(username string, all bool) (int64, error) {
	now := time.Now().UnixMilli()
	var res interface{ RowsAffected() (int64, error) }
	var err error
	if all {
		res, err = db.Exec(`DELETE FROM temp_shares WHERE expires_at < ?`, now)
	} else {
		res, err = db.Exec(`DELETE FROM temp_shares WHERE expires_at < ? AND created_by = ?`, now, username)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ─── Semáforo de descargas concurrentes por token ──────────────────
// En memoria: si el daemon reinicia, los slots se liberan solos.

var (
	tempShareSlotsMu sync.Mutex
	tempShareSlots   = map[string]int{}
)

// tempShareAcquireSlot intenta ocupar un slot de descarga. max<=0 → sin límite.
func tempShareAcquireSlot(token string, max int) bool {
	if max <= 0 {
		return true
	}
	tempShareSlotsMu.Lock()
	defer tempShareSlotsMu.Unlock()
	if tempShareSlots[token] >= max {
		return false
	}
	tempShareSlots[token]++
	return true
}

func tempShareReleaseSlot(token string, max int) {
	if max <= 0 {
		return
	}
	tempShareSlotsMu.Lock()
	defer tempShareSlotsMu.Unlock()
	if tempShareSlots[token] > 0 {
		tempShareSlots[token]--
	}
	if tempShareSlots[token] == 0 {
		delete(tempShareSlots, token)
	}
}

// tempShareValidateTTL normaliza horas de vida (1h .. 30d).
func tempShareValidateTTL(hours float64) (int64, error) {
	if hours < 1 || hours > tempShareMaxTTLHrs {
		return 0, fmt.Errorf("ttl fuera de rango (1-%d horas)", tempShareMaxTTLHrs)
	}
	return time.Now().UnixMilli() + int64(hours*3600*1000), nil
}
