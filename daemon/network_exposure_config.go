// network_exposure_config.go — Config global de exposición (singleton).
//
// network_exposure_config tiene una sola fila (id='singleton'). Guarda los
// parámetros compartidos por todas las apps expuestas: el dominio base, la
// URL admin de Caddy y el interruptor global de exposición.
//
// No usa triple generation: es configuración, no una entidad reconciable.
// El reconciler la lee como parámetro de entrada en cada pasada.
//
// API:
//   GetExposureConfig  → devuelve la config; si no existe, devuelve defaults
//                        (no error) para que el sistema arranque "vacío".
//   SaveExposureConfig → upsert de la fila singleton.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// NetworkExposureConfig es la configuración global del subsistema de
// exposición.
type NetworkExposureConfig struct {
	BaseDomain    string    `json:"base_domain"`
	CaddyAdminURL string    `json:"caddy_admin_url"`
	Enabled       bool      `json:"enabled"`
	HTTPPort      int       `json:"http_port"`  // puerto HTTP de Caddy (default 80)
	HTTPSPort     int       `json:"https_port"` // puerto HTTPS de Caddy (default 443)
	UpdatedAt     time.Time `json:"updated_at"`

	// FWManagedPorts son los puertos que NimOS ha abierto en el firewall
	// del host (ufw). Estado del reconciler, NO configuración del usuario:
	// se persiste para saber qué reglas son nuestras y poder retirarlas
	// cuando los puertos cambien, sin tocar jamás reglas ajenas.
	FWManagedPorts []int `json:"-"`
}

// defaultExposureConfig devuelve la config inicial cuando no hay fila aún.
// Exposición desactivada y sin dominio: el admin debe configurarlo desde UI.
func defaultExposureConfig() NetworkExposureConfig {
	return NetworkExposureConfig{
		BaseDomain:    "",
		CaddyAdminURL: "http://127.0.0.1:2019",
		Enabled:       false,
		HTTPPort:      80,
		HTTPSPort:     443,
	}
}

// GetExposureConfig lee la fila singleton. Si no existe, devuelve los
// defaults (sin error) — el sistema arranca con exposición desactivada.
func (r *NetworkRepo) GetExposureConfig(ctx context.Context) (NetworkExposureConfig, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT base_domain, caddy_admin_url, enabled, http_port, https_port, fw_managed_ports, updated_at
		FROM network_exposure_config WHERE id = 'singleton'
	`)
	var (
		cfg        NetworkExposureConfig
		enabledInt int
		fwJSON     string
		updatedStr string
	)
	err := row.Scan(&cfg.BaseDomain, &cfg.CaddyAdminURL, &enabledInt,
		&cfg.HTTPPort, &cfg.HTTPSPort, &fwJSON, &updatedStr)
	if err == sql.ErrNoRows {
		return defaultExposureConfig(), nil
	}
	if err != nil {
		return defaultExposureConfig(), fmt.Errorf("GetExposureConfig: %w", err)
	}
	cfg.Enabled = enabledInt != 0
	if fwJSON != "" {
		_ = json.Unmarshal([]byte(fwJSON), &cfg.FWManagedPorts)
	}
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}
	if t, perr := time.Parse(time.RFC3339, updatedStr); perr == nil {
		cfg.UpdatedAt = t
	}
	return cfg, nil
}

// SaveExposureConfig hace upsert de la fila singleton. Si Enabled=true,
// exige base_domain no vacío (no tiene sentido exponer sin dominio).
func (r *NetworkRepo) SaveExposureConfig(ctx context.Context, tx *sql.Tx, cfg NetworkExposureConfig) error {
	if cfg.Enabled && cfg.BaseDomain == "" {
		return fmt.Errorf("SaveExposureConfig: cannot enable exposure without base_domain")
	}
	if cfg.CaddyAdminURL == "" {
		cfg.CaddyAdminURL = defaultExposureConfig().CaddyAdminURL
	}
	// Puertos: 0 = "no especificado" → defaults. Validar rango y colisión.
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}
	if cfg.HTTPPort < 1 || cfg.HTTPPort > 65535 || cfg.HTTPSPort < 1 || cfg.HTTPSPort > 65535 {
		return fmt.Errorf("SaveExposureConfig: ports must be 1-65535")
	}
	if cfg.HTTPPort == cfg.HTTPSPort {
		return fmt.Errorf("SaveExposureConfig: http_port and https_port must differ")
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO network_exposure_config (id, base_domain, caddy_admin_url, enabled, http_port, https_port, updated_at)
		VALUES ('singleton', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			base_domain     = excluded.base_domain,
			caddy_admin_url = excluded.caddy_admin_url,
			enabled         = excluded.enabled,
			http_port       = excluded.http_port,
			https_port      = excluded.https_port,
			updated_at      = excluded.updated_at
	`, cfg.BaseDomain, cfg.CaddyAdminURL, intFromBool(cfg.Enabled), cfg.HTTPPort, cfg.HTTPSPort, now)
	if err != nil {
		return fmt.Errorf("SaveExposureConfig: %w", err)
	}
	return nil
}

// UpdateFWManagedPorts persiste la lista de puertos que NimOS gestiona en
// el firewall del host. Solo la toca el reconciler (estado de máquina, no
// configuración de usuario): SaveExposureConfig no escribe esta columna.
func (r *NetworkRepo) UpdateFWManagedPorts(ctx context.Context, tx *sql.Tx, ports []int) error {
	if ports == nil {
		ports = []int{}
	}
	raw, err := json.Marshal(ports)
	if err != nil {
		return fmt.Errorf("UpdateFWManagedPorts: marshal: %w", err)
	}
	now := r.clock.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO network_exposure_config (id, fw_managed_ports, updated_at)
		VALUES ('singleton', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			fw_managed_ports = excluded.fw_managed_ports,
			updated_at       = excluded.updated_at
	`, string(raw), now)
	if err != nil {
		return fmt.Errorf("UpdateFWManagedPorts: %w", err)
	}
	return nil
}
