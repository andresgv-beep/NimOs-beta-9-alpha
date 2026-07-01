// network_exposure_repo.go — Repositorio de network_exposed_apps (Beta 8.1).
//
// Encapsula el acceso a la tabla network_exposed_apps, que declara qué
// apps se exponen a internet vía Caddy. Sigue las mismas convenciones que
// network_repo.go:
//
//   - Mutaciones reciben (ctx, tx) explícitos.
//   - Lecturas van directas contra *sql.DB.
//   - scanExposedApp acepta rowScanner (sirve para *sql.Row y *sql.Rows).
//   - Errores envueltos con %w para errors.Is.
//   - Clock inyectable para timestamps deterministas.
//
// Triple generation:
//   CreateExposedApp        → desired=1, observed=0, applied=0
//   UpdateExposedAppConfig  → desired_generation + 1
//   RecordExposedAppObserved→ observed_generation + 1 (drift)
//   MarkExposedAppApplied   → applied = desired (reconciler convergió)
//   ListPendingExposedApps  → applied < desired
//   ListDriftedExposedApps  → observed != applied

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrExposedAppNotFound se devuelve cuando no existe la fila pedida.
var ErrExposedAppNotFound = errors.New("network exposed app not found")

// NetworkExposedApp representa una fila de network_exposed_apps.
//
// Enrutado agnóstico (Opción C): al menos uno de Subdomain/Path no vacío.
//   - Subdomain != "" → Caddy enruta por host (<subdomain>.<base_domain>).
//   - Path      != "" → Caddy enruta por path (<base_domain><path>).
type NetworkExposedApp struct {
	ID           string      `json:"id"`
	AppID        string      `json:"app_id"`
	DisplayName  string      `json:"display_name"`
	Subdomain    string      `json:"subdomain"`
	Path         string      `json:"path"`
	UpstreamHost string      `json:"upstream_host"`
	UpstreamPort int         `json:"upstream_port"`
	Enabled      bool        `json:"enabled"`
	Convergence  Convergence `json:"convergence"`
	// LandingPath · ruta del panel de la app (ej. Pi-hole "/admin"). NO vive en
	// las tablas de Network · es un enriquecimiento del DTO que el handler añade
	// leyéndolo de docker_apps.config (read model · ver enrichExposureDTO).
	// omitempty: solo aparece si la app tiene landing_path.
	LandingPath string    `json:"landing_path,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const exposedAppColumns = `
	id, app_id, display_name, subdomain, path,
	upstream_host, upstream_port, enabled,
	desired_generation, observed_generation, applied_generation,
	created_at, updated_at
`

func scanExposedApp(rs rowScanner) (*NetworkExposedApp, error) {
	var (
		a            NetworkExposedApp
		enabledInt   int
		createdAtStr string
		updatedAtStr string
	)
	err := rs.Scan(
		&a.ID, &a.AppID, &a.DisplayName, &a.Subdomain, &a.Path,
		&a.UpstreamHost, &a.UpstreamPort, &enabledInt,
		&a.Convergence.Desired, &a.Convergence.Observed, &a.Convergence.Applied,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	a.Enabled = enabledInt != 0
	if t, perr := time.Parse(time.RFC3339, createdAtStr); perr == nil {
		a.CreatedAt = t
	}
	if t, perr := time.Parse(time.RFC3339, updatedAtStr); perr == nil {
		a.UpdatedAt = t
	}
	return &a, nil
}

// CreateExposedApp inserta una nueva app expuesta con desired=1.
// Si ID está vacío, genera un UUID. Valida que al menos subdomain o path
// estén presentes (el schema también lo fuerza vía CHECK).
func (r *NetworkRepo) CreateExposedApp(ctx context.Context, tx *sql.Tx, a *NetworkExposedApp) error {
	if a.AppID == "" {
		return fmt.Errorf("CreateExposedApp: app_id required")
	}
	if a.Subdomain == "" && a.Path == "" {
		return fmt.Errorf("CreateExposedApp: subdomain or path required")
	}
	if a.UpstreamPort <= 0 || a.UpstreamPort > 65535 {
		return fmt.Errorf("CreateExposedApp: invalid upstream_port %d", a.UpstreamPort)
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_exposed_apps (
			id, app_id, display_name, subdomain, path,
			upstream_host, upstream_port, enabled,
			desired_generation, observed_generation, applied_generation,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 0, 0, ?, ?)
	`,
		a.ID, a.AppID, a.DisplayName, a.Subdomain, a.Path,
		a.UpstreamHost, a.UpstreamPort, intFromBool(a.Enabled),
		now, now,
	)
	if err != nil {
		if isFKError(err) {
			return fmt.Errorf("CreateExposedApp: FK error: %w", err)
		}
		return fmt.Errorf("CreateExposedApp: %w", err)
	}
	a.Convergence = Convergence{Desired: 1, Observed: 0, Applied: 0}
	return nil
}

// GetExposedApp lee una app expuesta por id.
func (r *NetworkRepo) GetExposedApp(ctx context.Context, id string) (*NetworkExposedApp, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps WHERE id = ?`, id)
	a, err := scanExposedApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExposedAppNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetExposedApp: %w", err)
	}
	return a, nil
}

// GetExposedAppByAppID lee una app expuesta por app_id (UNIQUE).
func (r *NetworkRepo) GetExposedAppByAppID(ctx context.Context, appID string) (*NetworkExposedApp, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps WHERE app_id = ?`, appID)
	a, err := scanExposedApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExposedAppNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetExposedAppByAppID: %w", err)
	}
	return a, nil
}

// ListExposedApps devuelve todas las apps expuestas, ordenadas por app_id.
func (r *NetworkRepo) ListExposedApps(ctx context.Context) ([]*NetworkExposedApp, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("ListExposedApps: %w", err)
	}
	defer rows.Close()
	return collectExposedApps(rows)
}

// ListEnabledExposedApps devuelve solo las apps con enabled=1. Es lo que
// el generador de Caddy necesita para construir la config.
func (r *NetworkRepo) ListEnabledExposedApps(ctx context.Context) ([]*NetworkExposedApp, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps WHERE enabled = 1 ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("ListEnabledExposedApps: %w", err)
	}
	defer rows.Close()
	return collectExposedApps(rows)
}

// ListPendingExposedApps devuelve apps con applied < desired (cambios sin
// aplicar a Caddy todavía).
func (r *NetworkRepo) ListPendingExposedApps(ctx context.Context) ([]*NetworkExposedApp, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps
		 WHERE applied_generation < desired_generation ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("ListPendingExposedApps: %w", err)
	}
	defer rows.Close()
	return collectExposedApps(rows)
}

// ListDriftedExposedApps devuelve apps con observed != applied (el observer
// detectó que Caddy no refleja lo aplicado).
func (r *NetworkRepo) ListDriftedExposedApps(ctx context.Context) ([]*NetworkExposedApp, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+exposedAppColumns+` FROM network_exposed_apps
		 WHERE observed_generation != applied_generation ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("ListDriftedExposedApps: %w", err)
	}
	defer rows.Close()
	return collectExposedApps(rows)
}

// ErrExposedAppConflict — la app fue modificada por otro cliente desde que
// el caller la leyó (candado optimista: la generación esperada no coincide).
// El caller debe recargar el estado actual y reintentar conscientemente.
var ErrExposedAppConflict = errors.New("exposed app modified concurrently (stale generation)")

// UpdateExposedAppConfig cambia config de enrutado/upstream/enabled e
// incrementa desired_generation — SOLO si la generación actual coincide con
// expectedGen (candado optimista, CRIT-1). Así dos clientes editando a la
// vez no se pisan en silencio: el segundo recibe ErrExposedAppConflict.
func (r *NetworkRepo) UpdateExposedAppConfig(ctx context.Context, tx *sql.Tx, a *NetworkExposedApp, expectedGen int64) error {
	if a.Subdomain == "" && a.Path == "" {
		return fmt.Errorf("UpdateExposedAppConfig: subdomain or path required")
	}
	if a.UpstreamPort <= 0 || a.UpstreamPort > 65535 {
		return fmt.Errorf("UpdateExposedAppConfig: invalid upstream_port %d", a.UpstreamPort)
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		UPDATE network_exposed_apps
		SET display_name = ?, subdomain = ?, path = ?,
		    upstream_host = ?, upstream_port = ?, enabled = ?,
		    desired_generation = desired_generation + 1,
		    updated_at = ?
		WHERE id = ? AND desired_generation = ?
	`,
		a.DisplayName, a.Subdomain, a.Path,
		a.UpstreamHost, a.UpstreamPort, intFromBool(a.Enabled),
		now, a.ID, expectedGen,
	)
	if err != nil {
		return fmt.Errorf("UpdateExposedAppConfig: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return r.classifyMissOrConflict(ctx, tx, a.ID)
	}
	return nil
}

// classifyMissOrConflict distingue, tras un UPDATE/DELETE con candado que
// no afectó filas, entre "la app no existe" y "existe pero con otra
// generación" (conflicto concurrente).
func (r *NetworkRepo) classifyMissOrConflict(ctx context.Context, tx *sql.Tx, id string) error {
	var one int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM network_exposed_apps WHERE id = ?`, id).Scan(&one)
	if err == sql.ErrNoRows {
		return ErrExposedAppNotFound
	}
	if err != nil {
		return fmt.Errorf("classifyMissOrConflict: %w", err)
	}
	return ErrExposedAppConflict
}

// RecordExposedAppObserved incrementa observed_generation (drift detectado
// por el observer). No toca desired ni applied.
func (r *NetworkRepo) RecordExposedAppObserved(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_exposed_apps
		SET observed_generation = observed_generation + 1,
		    updated_at = ?
		WHERE id = ?
	`, r.clock.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("RecordExposedAppObserved: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrExposedAppNotFound
	}
	return nil
}

// MarkExposedAppApplied iguala applied_generation a desired_generation
// (el reconciler aplicó la config a Caddy con éxito).
func (r *NetworkRepo) MarkExposedAppApplied(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE network_exposed_apps
		SET applied_generation = desired_generation,
		    updated_at = ?
		WHERE id = ?
	`, r.clock.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("MarkExposedAppApplied: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrExposedAppNotFound
	}
	return nil
}

// DeleteExposedApp elimina una app expuesta — SOLO si la generación
// coincide (candado optimista: borrar basándose en una vista vieja también
// es un conflicto). El reconciler regenerará la config de Caddy sin ella.
func (r *NetworkRepo) DeleteExposedApp(ctx context.Context, tx *sql.Tx, id string, expectedGen int64) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM network_exposed_apps WHERE id = ? AND desired_generation = ?`,
		id, expectedGen)
	if err != nil {
		return fmt.Errorf("DeleteExposedApp: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return r.classifyMissOrConflict(ctx, tx, id)
	}
	return nil
}

func collectExposedApps(rows *sql.Rows) ([]*NetworkExposedApp, error) {
	var out []*NetworkExposedApp
	for rows.Next() {
		a, err := scanExposedApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
