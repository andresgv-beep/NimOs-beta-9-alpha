// network_repo.go — Repositorio del módulo network (Beta 8 v4).
//
// Encapsula el acceso a las tablas network_ports y network_ddns.
// Estas son las tablas con TRIPLE GENERATION
// (desired/observed/applied) — las reconciables. Las tablas auditables
// (observed, operations, events) viven en network_repo_audit.go.
//
// Convenciones (alineadas con storage_repo.go):
//
//   - Mutaciones reciben (ctx, tx) explícitos. El caller compone
//     atomicidad: si crear DDNS + insertar token deben ser atómicos,
//     el caller abre la tx, llama a SecretsStore.CreateSecret +
//     NetworkRepo.CreateDdns, luego commit.
//   - Lecturas van directas contra *sql.DB (no necesitan tx).
//   - Scanner functions per table aceptan interfaz rowScanner para
//     poder usarse con *sql.Row y *sql.Rows.
//   - Errores envueltos con %w para errors.Is.
//   - Clock inyectable para timestamps deterministas en tests.
//
// Triple generation API:
//
//   Create*           → inserta con desired=1, observed=0, applied=0
//   Update*Config     → incrementa desired_generation
//   Record*Observed   → incrementa observed_generation (drift detectado)
//   Mark*Applied      → applied_generation = desired_generation
//                       (reconciler convergió)
//
//   List*Pending      → applied < desired (necesitan reconciliación)
//   List*Drifted      → observed != applied (drift detectado por observer)

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	ErrPortNotFound = errors.New("network port not found")
	ErrDdnsNotFound = errors.New("network ddns not found")

	// ErrPortAlreadyExists se devuelve al intentar Create un port con un
	// ID que ya existe. Usar Update*Config para cambiar uno existente.
	ErrPortAlreadyExists = errors.New("network port already exists")

	// ErrInvalidPortID se devuelve si el ID no es 'http' o 'https'.
	// La DB también lo rechaza vía CHECK, pero validamos antes para dar
	// un mensaje más útil.
	ErrInvalidPortID = errors.New("port id must be 'http' or 'https'")
)

// ─────────────────────────────────────────────────────────────────────────────
// Convergence — triple generation tipo público
// ─────────────────────────────────────────────────────────────────────────────

// Convergence agrupa las tres generations de una entidad reconciable.
// Cada entidad triple-gen incluye una Convergence en su struct público.
//
// Semántica (NIMOS_DISCIPLINE.md §1):
//   - Desired:  lo que el usuario/sistema declara querer.
//   - Observed: lo que el observer ve realmente en runtime.
//   - Applied:  lo que el reconciler aplicó con éxito.
type Convergence struct {
	Desired  int64 `json:"desired_generation"`
	Observed int64 `json:"observed_generation"`
	Applied  int64 `json:"applied_generation"`
}

// IsConverged es true cuando applied == desired (no hay cambios
// pendientes de aplicar).
func (c Convergence) IsConverged() bool { return c.Applied == c.Desired }

// HasDrifted es true cuando observed != applied (el observer ha
// detectado que el estado real ya no coincide con lo aplicado).
func (c Convergence) HasDrifted() bool { return c.Observed != c.Applied }

// IsPending es true cuando applied < desired (hay un cambio de config
// que el reconciler aún no ha aplicado).
func (c Convergence) IsPending() bool { return c.Applied < c.Desired }

// ─────────────────────────────────────────────────────────────────────────────
// Repo
// ─────────────────────────────────────────────────────────────────────────────

// NetworkRepo encapsula el acceso a las tablas network_*. Thread-safe
// vía *sql.DB (la conexión se serializa en SQLite).
type NetworkRepo struct {
	db    *sql.DB
	clock Clock
}

// NewNetworkRepo crea un repositorio sobre la conexión SQLite dada.
// La conexión debe tener foreign_keys ON. Clock nil → RealClock.
func NewNetworkRepo(db *sql.DB, clock Clock) *NetworkRepo {
	if clock == nil {
		clock = NewRealClock()
	}
	return &NetworkRepo{db: db, clock: clock}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// rowScanner es la interfaz mínima compartida por *sql.Row y *sql.Rows.
// Permite que las funciones scan* funcionen con ambos.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func boolFromInt(i int) bool { return i != 0 }
func intFromBool(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStringPtr convierte un *string a sql.NullString (para INSERT/UPDATE).
func nullStringPtr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// ptrFromNullString convierte un sql.NullString leído de la DB a *string.
func ptrFromNullString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

// ptrFromNullTime parsea un sql.NullString RFC3339 a *time.Time.
// Si el parse falla, devuelve nil (logueado en caller si importa).
func ptrFromNullTime(n sql.NullString) *time.Time {
	if !n.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339, n.String)
	if err != nil {
		return nil
	}
	return &t
}

// parseTime parsea un string RFC3339 a time.Time. Devuelve la zero
// value en caso de error (los CHECKs de la tabla aseguran que los
// campos NOT NULL tienen formato válido al haberse insertado, así que
// esto solo falla si alguien ha corrompido la DB manualmente).
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ═════════════════════════════════════════════════════════════════════════════
// network_ports
// ═════════════════════════════════════════════════════════════════════════════

// NetworkPort representa una fila de network_ports.
//
// IDs válidos: 'http' o 'https' (singletons del daemon, hardcoded).
// El UpdatedAt se setea en cada Update*Config.
type NetworkPort struct {
	ID          string      `json:"id"`
	Port        int         `json:"port"`
	BindAddress string      `json:"bind_address"`
	Enabled     bool        `json:"enabled"`
	Convergence Convergence `json:"convergence"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// isValidPortID valida sin tocar la DB. El CHECK del schema repite la
// validación; aquí preferimos un error útil antes que un constraint failure.
func isValidPortID(id string) bool {
	return id == "http" || id == "https"
}

func scanPort(rs rowScanner) (*NetworkPort, error) {
	var (
		p            NetworkPort
		enabledInt   int
		updatedAtStr string
	)
	err := rs.Scan(
		&p.ID, &p.Port, &p.BindAddress, &enabledInt,
		&p.Convergence.Desired, &p.Convergence.Observed, &p.Convergence.Applied,
		&updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	p.Enabled = boolFromInt(enabledInt)
	p.UpdatedAt = parseTime(updatedAtStr)
	return &p, nil
}

const portColumns = `
	id, port, bind_address, enabled,
	desired_generation, observed_generation, applied_generation,
	updated_at
`

// CreatePort inserta un port nuevo con desired_generation=1,
// observed_generation=0, applied_generation=0 (la config está declarada
// pero todavía no se ha aplicado al kernel).
//
// Devuelve ErrInvalidPortID si id no es 'http' o 'https'.
// Devuelve ErrPortAlreadyExists si ya existe un port con ese id.
func (r *NetworkRepo) CreatePort(ctx context.Context, tx *sql.Tx, p *NetworkPort) error {
	if !isValidPortID(p.ID) {
		return fmt.Errorf("%w: %q", ErrInvalidPortID, p.ID)
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_ports (
			id, port, bind_address, enabled,
			desired_generation, observed_generation, applied_generation,
			updated_at
		) VALUES (?, ?, ?, ?, 1, 0, 0, ?)
	`, p.ID, p.Port, p.BindAddress, intFromBool(p.Enabled), now)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("%w: id=%q", ErrPortAlreadyExists, p.ID)
		}
		return fmt.Errorf("CreatePort: %w", err)
	}
	// Mantener el struct in-sync con lo persistido.
	p.Convergence = Convergence{Desired: 1, Observed: 0, Applied: 0}
	p.UpdatedAt = parseTime(now)
	return nil
}

// GetPort lee un port por id. Devuelve ErrPortNotFound si no existe.
func (r *NetworkRepo) GetPort(ctx context.Context, id string) (*NetworkPort, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+portColumns+` FROM network_ports WHERE id = ?`, id)
	p, err := scanPort(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPortNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetPort: %w", err)
	}
	return p, nil
}

// ListPorts devuelve todos los ports configurados (típicamente 2).
func (r *NetworkRepo) ListPorts(ctx context.Context) ([]*NetworkPort, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+portColumns+` FROM network_ports ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("ListPorts: %w", err)
	}
	defer rows.Close()
	var out []*NetworkPort
	for rows.Next() {
		p, err := scanPort(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPorts scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdatePortConfig cambia port, bind_address y/o enabled. Incrementa
// desired_generation: el reconciler verá pending y aplicará el cambio.
//
// Devuelve ErrPortNotFound si id no existe.
func (r *NetworkRepo) UpdatePortConfig(ctx context.Context, tx *sql.Tx, id string, port int, bindAddress string, enabled bool) error {
	if !isValidPortID(id) {
		return fmt.Errorf("%w: %q", ErrInvalidPortID, id)
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ports
		SET port = ?, bind_address = ?, enabled = ?,
		    desired_generation = desired_generation + 1,
		    updated_at = ?
		WHERE id = ?
	`, port, bindAddress, intFromBool(enabled), now, id)
	if err != nil {
		return fmt.Errorf("UpdatePortConfig: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPortNotFound
	}
	return nil
}

// RecordPortObserved incrementa observed_generation. Lo llama el
// observer cuando detecta que el estado real difiere del último
// applied — equivalente a "drift detectado".
//
// El observer es responsable de NO llamar a esto si el estado real
// coincide con applied (eso sería ruido en la tabla).
func (r *NetworkRepo) RecordPortObserved(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ports
		SET observed_generation = observed_generation + 1
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("RecordPortObserved: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPortNotFound
	}
	return nil
}

// MarkPortApplied sincroniza applied_generation Y observed_generation con
// el desired actual. Lo llama el reconciler tras rebindar el listener
// con éxito.
//
// Por qué sincroniza observed también: tras aplicar, el estado real
// DEBERÍA coincidir con lo aplicado. Si observed se quedara atrás,
// HasDrifted devolvería true espuriamente justo después de aplicar.
// El observer es responsable de incrementar observed_generation SOLO
// cuando detecte una divergencia real respecto a este punto.
//
// IMPORTANTE: usa el desired actual de la fila, NO un valor pasado por
// el caller. Eso evita race conditions donde el desired ha cambiado
// entre que el reconciler lo leyó y aplicó.
func (r *NetworkRepo) MarkPortApplied(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ports
		SET applied_generation  = desired_generation,
		    observed_generation = desired_generation
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("MarkPortApplied: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPortNotFound
	}
	return nil
}

// DeletePort elimina un port. Idempotente.
func (r *NetworkRepo) DeletePort(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM network_ports WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("DeletePort: %w", err)
	}
	return nil
}

// ListPendingPorts devuelve los ports con applied_generation <
// desired_generation (necesitan que el reconciler aplique el cambio).
func (r *NetworkRepo) ListPendingPorts(ctx context.Context) ([]*NetworkPort, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+portColumns+`
		FROM network_ports
		WHERE applied_generation < desired_generation
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("ListPendingPorts: %w", err)
	}
	defer rows.Close()
	return collectPorts(rows)
}

// ListDriftedPorts devuelve los ports con observed_generation !=
// applied_generation (drift detectado por el observer; el reconciler
// debería re-aplicar).
func (r *NetworkRepo) ListDriftedPorts(ctx context.Context) ([]*NetworkPort, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+portColumns+`
		FROM network_ports
		WHERE observed_generation != applied_generation
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("ListDriftedPorts: %w", err)
	}
	defer rows.Close()
	return collectPorts(rows)
}

func collectPorts(rows *sql.Rows) ([]*NetworkPort, error) {
	var out []*NetworkPort
	for rows.Next() {
		p, err := scanPort(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ═════════════════════════════════════════════════════════════════════════════
// network_ddns
// ═════════════════════════════════════════════════════════════════════════════

// NetworkDdns representa una fila de network_ddns. El token NO está
// aquí — vive cifrado en nimos_secrets, referenciado por TokenSecretID.
type NetworkDdns struct {
	ID             string      `json:"id"`
	Provider       string      `json:"provider"`
	Domain         string      `json:"domain"`
	TokenSecretID  string      `json:"token_secret_id"`
	Enabled        bool        `json:"enabled"`
	AutoUpdate     bool        `json:"auto_update"`
	UpdateInterval int         `json:"update_interval"`
	LastRunAt      *time.Time  `json:"last_run_at,omitempty"`
	LastRunResult  *string     `json:"last_run_result,omitempty"`
	LastIP         *string     `json:"last_ip,omitempty"`
	Convergence    Convergence `json:"convergence"`
}

const ddnsColumns = `
	id, provider, domain, token_secret_id,
	enabled, auto_update, update_interval,
	last_run_at, last_run_result, last_ip,
	desired_generation, observed_generation, applied_generation
`

func scanDdns(rs rowScanner) (*NetworkDdns, error) {
	var (
		d             NetworkDdns
		enabledInt    int
		autoUpdateInt int
		lastRunAt     sql.NullString
		lastRunResult sql.NullString
		lastIP        sql.NullString
	)
	err := rs.Scan(
		&d.ID, &d.Provider, &d.Domain, &d.TokenSecretID,
		&enabledInt, &autoUpdateInt, &d.UpdateInterval,
		&lastRunAt, &lastRunResult, &lastIP,
		&d.Convergence.Desired, &d.Convergence.Observed, &d.Convergence.Applied,
	)
	if err != nil {
		return nil, err
	}
	d.Enabled = boolFromInt(enabledInt)
	d.AutoUpdate = boolFromInt(autoUpdateInt)
	d.LastRunAt = ptrFromNullTime(lastRunAt)
	d.LastRunResult = ptrFromNullString(lastRunResult)
	d.LastIP = ptrFromNullString(lastIP)
	return &d, nil
}

// CreateDdns inserta una entrada DDNS. El TokenSecretID DEBE existir
// previamente en nimos_secrets (la FK lo verifica).
//
// Si el ID está vacío, se genera un UUID.
// Convergencia inicial: desired=1, observed=0, applied=0.
func (r *NetworkRepo) CreateDdns(ctx context.Context, tx *sql.Tx, d *NetworkDdns) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	if d.UpdateInterval == 0 {
		d.UpdateInterval = 900
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_ddns (
			id, provider, domain, token_secret_id,
			enabled, auto_update, update_interval,
			desired_generation, observed_generation, applied_generation
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 0, 0)
	`, d.ID, d.Provider, d.Domain, d.TokenSecretID,
		intFromBool(d.Enabled), intFromBool(d.AutoUpdate), d.UpdateInterval)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("ddns domain %q already exists: %w", d.Domain, err)
		}
		// FK fail mensaje genérico de SQLite: lo enriquecemos.
		if isFKError(err) {
			return fmt.Errorf("token_secret_id %q does not exist in nimos_secrets: %w",
				d.TokenSecretID, err)
		}
		return fmt.Errorf("CreateDdns: %w", err)
	}
	d.Convergence = Convergence{Desired: 1, Observed: 0, Applied: 0}
	return nil
}

// GetDdns lee una entrada por id.
func (r *NetworkRepo) GetDdns(ctx context.Context, id string) (*NetworkDdns, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+ddnsColumns+` FROM network_ddns WHERE id = ?`, id)
	d, err := scanDdns(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDdnsNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetDdns: %w", err)
	}
	return d, nil
}

// GetDdnsByDomain lee una entrada por domain. Útil porque domain es UNIQUE
// y es la "clave natural" del DDNS.
func (r *NetworkRepo) GetDdnsByDomain(ctx context.Context, domain string) (*NetworkDdns, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+ddnsColumns+` FROM network_ddns WHERE domain = ?`, domain)
	d, err := scanDdns(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDdnsNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetDdnsByDomain: %w", err)
	}
	return d, nil
}

// ListDdns devuelve todas las entradas DDNS, ordenadas por domain.
func (r *NetworkRepo) ListDdns(ctx context.Context) ([]*NetworkDdns, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ddnsColumns+` FROM network_ddns ORDER BY domain`)
	if err != nil {
		return nil, fmt.Errorf("ListDdns: %w", err)
	}
	defer rows.Close()
	return collectDdns(rows)
}

// UpdateDdnsConfig cambia campos de configuración (NO el token). Incrementa
// desired_generation. Para cambiar el token, hacer UpdateSecret() en el
// SecretsStore — token_secret_id mantiene la misma FK.
func (r *NetworkRepo) UpdateDdnsConfig(ctx context.Context, tx *sql.Tx, id string, enabled, autoUpdate bool, updateInterval int) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ddns
		SET enabled = ?, auto_update = ?, update_interval = ?,
		    desired_generation = desired_generation + 1
		WHERE id = ?
	`, intFromBool(enabled), intFromBool(autoUpdate), updateInterval, id)
	if err != nil {
		return fmt.Errorf("UpdateDdnsConfig: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDdnsNotFound
	}
	return nil
}

// RecordDdnsRun registra el resultado de una ejecución del reconciler
// DDNS. NO toca generations (eso lo hace MarkDdnsApplied por separado).
//
// result debe ser 'success', 'failed' o 'no_change'.
// lastIP puede ser nil (cuando el run falló antes de obtener la IP).
func (r *NetworkRepo) RecordDdnsRun(ctx context.Context, tx *sql.Tx, id, result string, lastIP *string) error {
	switch result {
	case "success", "failed", "no_change":
		// OK
	default:
		return fmt.Errorf("invalid ddns run result %q", result)
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ddns
		SET last_run_at = ?, last_run_result = ?, last_ip = ?
		WHERE id = ?
	`, now, result, nullStringPtr(lastIP), id)
	if err != nil {
		return fmt.Errorf("RecordDdnsRun: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDdnsNotFound
	}
	return nil
}

// RecordDdnsObserved incrementa observed_generation (drift detectado).
func (r *NetworkRepo) RecordDdnsObserved(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ddns
		SET observed_generation = observed_generation + 1
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("RecordDdnsObserved: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDdnsNotFound
	}
	return nil
}

// MarkDdnsApplied: applied_generation = desired_generation. También
// sincroniza observed_generation — ver razón en MarkPortApplied.
func (r *NetworkRepo) MarkDdnsApplied(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_ddns
		SET applied_generation  = desired_generation,
		    observed_generation = desired_generation
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("MarkDdnsApplied: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDdnsNotFound
	}
	return nil
}

// DeleteDdns borra la entrada. NO borra el secret asociado (eso es
// decisión separada del caller — quizás quiere reusarlo).
func (r *NetworkRepo) DeleteDdns(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM network_ddns WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("DeleteDdns: %w", err)
	}
	return nil
}

// ListPendingDdns: entries que el reconciler debe procesar (config
// cambió o auto_update ha pasado el interval).
//
// Esta query es minimal: solo applied < desired. El cron del reconciler
// además seleccionará por interval transcurrido — esa lógica vive en el
// reconciler, no aquí.
func (r *NetworkRepo) ListPendingDdns(ctx context.Context) ([]*NetworkDdns, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+ddnsColumns+`
		FROM network_ddns
		WHERE applied_generation < desired_generation AND enabled = 1
		ORDER BY domain
	`)
	if err != nil {
		return nil, fmt.Errorf("ListPendingDdns: %w", err)
	}
	defer rows.Close()
	return collectDdns(rows)
}

// ListDriftedDdns: observed != applied (la IP pública cambió o el
// provider tiene un valor distinto del esperado).
func (r *NetworkRepo) ListDriftedDdns(ctx context.Context) ([]*NetworkDdns, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+ddnsColumns+`
		FROM network_ddns
		WHERE observed_generation != applied_generation AND enabled = 1
		ORDER BY domain
	`)
	if err != nil {
		return nil, fmt.Errorf("ListDriftedDdns: %w", err)
	}
	defer rows.Close()
	return collectDdns(rows)
}

func collectDdns(rows *sql.Rows) ([]*NetworkDdns, error) {
	var out []*NetworkDdns
	for rows.Next() {
		d, err := scanDdns(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// FK error detection
// ─────────────────────────────────────────────────────────────────────────────

// isFKError detecta un fallo de foreign key constraint. modernc.org/sqlite
// usa "FOREIGN KEY constraint failed" en el message.
func isFKError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
