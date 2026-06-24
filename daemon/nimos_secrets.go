// nimos_secrets.go — AES-256-GCM secrets store (NimOS core, global).
//
// Vive en /daemon/nimos_secrets.go porque la tabla nimos_secrets es
// global, no específica de network. Casos de uso esperados:
//
//   · network/ddns:        DuckDNS tokens, NoIP credentials
//   · network/dns_provider:Cloudflare API keys, Route53 keys (DNS-01)
//   · backup (futuro):     S3/B2 access keys
//   · apps:                Docker registry passwords
//   · notify (futuro):     Pushover/Telegram tokens
//   · shares:              SMB passwords, SSH private keys
//
// Diseño:
//
//   - AES-256-GCM. Master key 32 bytes en /var/lib/nimos/keys/master.key.
//   - Nonce 96 bits único por secret (NIST recommended for GCM).
//   - key_version=1 hoy. Schema soporta rotación gradual en el futuro:
//     cuando se rote, master.key vieja se mueve a key_history/v1.key,
//     se genera nueva master.key (v2), y los secrets viejos se re-cifran
//     lazy al accederlos.
//   - El plaintext NUNCA se loguea, NUNCA se serializa a JSON. Solo
//     vive en memoria mientras el caller lo usa.
//   - UNIQUE(category, label) en la DB: no se duplican secrets.
//
// Master key lifecycle:
//
//   - Generada al primer arranque si no existe (32 bytes crypto/rand).
//   - chmod 600, propietario el usuario del daemon.
//   - Si el archivo existe pero tiene tamaño incorrecto → ERROR.
//     NO se sobrescribe (eso destruiría todos los secrets cifrados con
//     la key vieja). El admin debe restaurar el archivo correcto.
//   - Backup del archivo es responsabilidad del admin. Sin él, los
//     secrets son irrecuperables.

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	// DefaultMasterKeyPath es la ubicación canónica de la master key en
	// producción. Tests pasan rutas alternativas en tmpdir.
	DefaultMasterKeyPath = "/var/lib/nimos/keys/master.key"

	masterKeySize = 32 // AES-256
	nonceSize     = 12 // GCM standard
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrSecretNotFound   = errors.New("secret not found")
	ErrSecretExists     = errors.New("secret already exists for (category, label)")
	ErrMasterKeyInvalid = errors.New("master key file has invalid size")
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// SecretID es un identificador opaco (UUID v4 stringificado) de un
// secret en la DB. Es seguro loguearlo y exponerlo en APIs — NO contiene
// material criptográfico.
type SecretID string

// Secret es una entrada descifrada. Se construye solo al leer (Get*) y
// se desecha tras uso. NUNCA se serializa a JSON con Plaintext incluido.
type Secret struct {
	ID           SecretID
	Category     string
	Label        string
	Plaintext    []byte
	CreatedAt    time.Time
	LastAccessed *time.Time
}

// SecretInfo es metadata SIN el plaintext. Seguro de exponer en API
// endpoints y logs.
type SecretInfo struct {
	ID           SecretID   `json:"id"`
	Category     string     `json:"category"`
	Label        string     `json:"label"`
	CreatedAt    time.Time  `json:"created_at"`
	LastAccessed *time.Time `json:"last_accessed,omitempty"`
}

// SecretsStore es la API pública. Thread-safe: el AEAD subyacente es
// seguro para uso concurrente, y la DB se accede vía *sql.DB que ya
// serializa writes.
type SecretsStore struct {
	db         *sql.DB
	aead       cipher.AEAD
	keyVersion int
	clock      Clock
}

// ─────────────────────────────────────────────────────────────────────────────
// Construction
// ─────────────────────────────────────────────────────────────────────────────

// NewSecretsStore crea el store usando una master key desde disco. Si
// el archivo no existe en masterKeyPath, lo crea con 32 bytes random y
// chmod 600.
//
// En producción: NewSecretsStore(db, DefaultMasterKeyPath, nil).
// En tests:      NewSecretsStore(db, filepath.Join(t.TempDir(), "k"), clk).
func NewSecretsStore(db *sql.DB, masterKeyPath string, clock Clock) (*SecretsStore, error) {
	key, err := loadOrCreateMasterKey(masterKeyPath)
	if err != nil {
		return nil, err
	}
	return newSecretsStoreFromKey(db, key, clock)
}

// NewSecretsStoreWithKey construye un store con una key inyectada en
// memoria (sin tocar el filesystem). Pensado para tests que necesitan
// controlar la key explícitamente (tamper tests, wrong-key tests).
//
// La key debe medir exactamente 32 bytes.
func NewSecretsStoreWithKey(db *sql.DB, key []byte, clock Clock) (*SecretsStore, error) {
	if len(key) != masterKeySize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d",
			ErrMasterKeyInvalid, len(key), masterKeySize)
	}
	return newSecretsStoreFromKey(db, key, clock)
}

func newSecretsStoreFromKey(db *sql.DB, key []byte, clock Clock) (*SecretsStore, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cannot create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cannot create GCM: %w", err)
	}
	return &SecretsStore{
		db:         db,
		aead:       aead,
		keyVersion: 1,
		clock:      clock,
	}, nil
}

// loadOrCreateMasterKey lee la master key del path. Si no existe la
// genera con crypto/rand y la persiste con chmod 600. Si existe pero
// tiene tamaño incorrecto, devuelve ErrMasterKeyInvalid SIN sobrescribir
// (sobrescribirla destruiría todos los secrets cifrados con la versión
// previa).
func loadOrCreateMasterKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != masterKeySize {
			return nil, fmt.Errorf("%w: %s has %d bytes, want %d",
				ErrMasterKeyInvalid, path, len(data), masterKeySize)
		}
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read master key %s: %w", path, err)
	}

	// No existe → generar.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create master key directory %s: %w", dir, err)
	}
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("cannot generate master key: %w", err)
	}
	// O_EXCL evita race con otro proceso que cree el archivo entre el
	// ReadFile de arriba y este OpenFile.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			// Otro proceso lo creó. Reintentamos la lectura.
			return loadOrCreateMasterKey(path)
		}
		return nil, fmt.Errorf("cannot create master key file %s: %w", path, err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("cannot write master key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("cannot close master key file: %w", err)
	}
	return key, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CRUD API
// ─────────────────────────────────────────────────────────────────────────────

// CreateSecret cifra plaintext y lo persiste. Devuelve el ID generado.
// Falla con ErrSecretExists si ya existe un secret con (category, label).
//
// Validación: category y label no pueden estar vacíos. plaintext no
// puede estar vacío (un secret vacío no tiene sentido y suele indicar
// un bug en el caller — preferimos fail-fast).
func (s *SecretsStore) CreateSecret(category, label string, plaintext []byte) (SecretID, error) {
	if category == "" {
		return "", fmt.Errorf("category cannot be empty")
	}
	if label == "" {
		return "", fmt.Errorf("label cannot be empty")
	}
	if len(plaintext) == 0 {
		return "", fmt.Errorf("plaintext cannot be empty")
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("cannot generate nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, plaintext, nil)

	id := SecretID(uuid.New().String())
	nowStr := s.clock.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(`
		INSERT INTO nimos_secrets (id, category, label, ciphertext, nonce, key_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, string(id), category, label, ciphertext, nonce, s.keyVersion, nowStr)
	if err != nil {
		if isUniqueConstraintError(err) {
			return "", fmt.Errorf("%w: category=%q label=%q",
				ErrSecretExists, category, label)
		}
		return "", fmt.Errorf("cannot insert secret: %w", err)
	}
	return id, nil
}

// GetSecret recupera y descifra el secret por su ID. Actualiza
// last_accessed (best-effort: si el UPDATE falla, NO se propaga, ya
// que el caller obtuvo el plaintext correctamente).
func (s *SecretsStore) GetSecret(id SecretID) (*Secret, error) {
	var (
		category     string
		label        string
		ciphertext   []byte
		nonce        []byte
		keyVer       int
		createdAtStr string
		lastAccessed sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT category, label, ciphertext, nonce, key_version, created_at, last_accessed
		FROM nimos_secrets WHERE id = ?
	`, string(id)).Scan(&category, &label, &ciphertext, &nonce, &keyVer, &createdAtStr, &lastAccessed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read secret %s: %w", id, err)
	}

	if keyVer != s.keyVersion {
		// En el futuro habrá fallback a key_history. Hoy: error.
		return nil, fmt.Errorf("secret %s uses key version %d, current is %d (rotation not yet supported)",
			id, keyVer, s.keyVersion)
	}

	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt secret %s (corrupted ciphertext or wrong key): %w",
			id, err)
	}

	// Update last_accessed (best-effort)
	nowStr := s.clock.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.Exec(`UPDATE nimos_secrets SET last_accessed = ? WHERE id = ?`,
		nowStr, string(id))

	secret := &Secret{
		ID:        id,
		Category:  category,
		Label:     label,
		Plaintext: plaintext,
	}
	if t, perr := time.Parse(time.RFC3339, createdAtStr); perr == nil {
		secret.CreatedAt = t
	}
	if lastAccessed.Valid {
		if t, perr := time.Parse(time.RFC3339, lastAccessed.String); perr == nil {
			secret.LastAccessed = &t
		}
	}
	return secret, nil
}

// GetSecretByLabel busca por (category, label). Útil para callers que
// conocen el "qué" pero no el ID interno (ej: un reconciler DDNS que
// quiere "el token de duckdns para nimosbarraca").
func (s *SecretsStore) GetSecretByLabel(category, label string) (*Secret, error) {
	var id string
	err := s.db.QueryRow(`
		SELECT id FROM nimos_secrets WHERE category = ? AND label = ?
	`, category, label).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot find secret (%s, %s): %w", category, label, err)
	}
	return s.GetSecret(SecretID(id))
}

// UpdateSecret reemplaza el plaintext de un secret existente. Genera
// nuevo nonce (NUNCA se reusa el viejo). La category y label NO cambian
// — para eso borra y crea otro.
//
// Devuelve ErrSecretNotFound si el ID no existe.
func (s *SecretsStore) UpdateSecret(id SecretID, newPlaintext []byte) error {
	if len(newPlaintext) == 0 {
		return fmt.Errorf("plaintext cannot be empty")
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("cannot generate nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, newPlaintext, nil)

	res, err := s.db.Exec(`
		UPDATE nimos_secrets SET ciphertext = ?, nonce = ?, key_version = ?
		WHERE id = ?
	`, ciphertext, nonce, s.keyVersion, string(id))
	if err != nil {
		return fmt.Errorf("cannot update secret %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSecretNotFound
	}
	return nil
}

// DeleteSecret borra el secret por su ID. No-op si no existe (no
// devuelve ErrSecretNotFound porque DELETE idempotente es más útil).
func (s *SecretsStore) DeleteSecret(id SecretID) error {
	_, err := s.db.Exec(`DELETE FROM nimos_secrets WHERE id = ?`, string(id))
	if err != nil {
		return fmt.Errorf("cannot delete secret %s: %w", id, err)
	}
	return nil
}

// ListSecrets devuelve la metadata de todos los secrets de una category,
// ordenados por label. NO incluye el plaintext.
//
// Si category está vacío, lista TODOS los secrets de la DB. Útil para
// admin UIs que muestran inventario completo.
func (s *SecretsStore) ListSecrets(category string) ([]SecretInfo, error) {
	var rows *sql.Rows
	var err error
	if category == "" {
		rows, err = s.db.Query(`
			SELECT id, category, label, created_at, last_accessed
			FROM nimos_secrets ORDER BY category ASC, label ASC
		`)
	} else {
		rows, err = s.db.Query(`
			SELECT id, category, label, created_at, last_accessed
			FROM nimos_secrets WHERE category = ?
			ORDER BY label ASC
		`, category)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list secrets: %w", err)
	}
	defer rows.Close()

	var out []SecretInfo
	for rows.Next() {
		var (
			id           string
			cat          string
			label        string
			createdAtStr string
			lastAccessed sql.NullString
		)
		if err := rows.Scan(&id, &cat, &label, &createdAtStr, &lastAccessed); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}
		info := SecretInfo{
			ID:       SecretID(id),
			Category: cat,
			Label:    label,
		}
		if t, perr := time.Parse(time.RFC3339, createdAtStr); perr == nil {
			info.CreatedAt = t
		}
		if lastAccessed.Valid {
			if t, perr := time.Parse(time.RFC3339, lastAccessed.String); perr == nil {
				info.LastAccessed = &t
			}
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// isUniqueConstraintError detecta una violación de UNIQUE constraint
// (sintaxis del driver modernc.org/sqlite).
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
