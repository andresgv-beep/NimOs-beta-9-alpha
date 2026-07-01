package main

// ═══════════════════════════════════════════════════════════════════════
// db_app_images.go · CRUD sobre tabla docker_app_images
// ───────────────────────────────────────────────────────────────────────
// Sprint Updates · 25/05/2026
//
// Tracking de digests de imágenes Docker para detección de actualizaciones.
//
// Diseño (ver apps_schema.sql para detalle):
//   - PRIMARY KEY (app_id, service_name) · una row por servicio
//   - local_digest:  imagen instalada actualmente (de `docker image inspect`)
//   - remote_digest: último digest visto en el registry (de `docker manifest inspect`)
//   - Si difieren → hay update disponible
//
// Patrón de uso:
//   1. Tras `docker compose up -d` exitoso → UpsertLocalDigest()
//   2. Periódicamente (TTL 6h) o por demanda → UpdateRemoteDigest()
//   3. UI consulta → ListWithUpdates() / GetByApp() / SummaryWithUpdates()
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AppImage representa una imagen Docker tracked para una app/servicio.
type AppImage struct {
	AppID           string
	ServiceName     string
	Image           string
	LocalDigest     string
	RemoteDigest    string
	RemoteCheckedAt string // ISO timestamp · vacío si nunca comprobado
	CheckStatus     string // 'ok' | 'unsupported' | 'rate_limited' | 'unauthorized' | 'error'
}

// HasUpdate devuelve true si la imagen tiene actualización disponible.
// Solo es true si hay un digest remoto válido Y difiere del local.
// Si nunca se ha comprobado el remoto (RemoteDigest vacío), devuelve false
// porque no podemos saberlo con certeza.
func (a *AppImage) HasUpdate() bool {
	return a.RemoteDigest != "" && a.RemoteDigest != a.LocalDigest
}

// IsCheckable devuelve true si la imagen puede comprobarse contra un registry.
// Apps con check_status 'unsupported' / 'unauthorized' no se comprueban porque
// fallarían siempre (registry privado, sin auth, etc.) · el frontend oculta
// el botón Actualizar para ellas.
func (a *AppImage) IsCheckable() bool {
	return a.CheckStatus == "ok" || a.CheckStatus == ""
}

// AppImagesRepo encapsula las operaciones SQL sobre docker_app_images.
type AppImagesRepo struct {
	db *sql.DB
}

// NewAppImagesRepo construye un repo nuevo · el daemon mantiene una instancia
// global (similar a appsRepo y operationsRepo).
func NewAppImagesRepo(db *sql.DB) *AppImagesRepo {
	return &AppImagesRepo{db: db}
}

// UpsertLocalDigest inserta o actualiza una row con el digest local. Se llama
// tras un `docker compose up -d` exitoso · tenemos el digest local nuevo y
// asumimos que es también el remoto (acabamos de descargar lo último).
//
// Si la row no existe, se crea. Si existe, se actualizan los tres campos.
// CheckStatus se preserva en 'ok' (la app sigue siendo comprobable).
func (r *AppImagesRepo) UpsertLocalDigest(ctx context.Context, appID, serviceName, image, digest string) error {
	if appID == "" || serviceName == "" || image == "" {
		return fmt.Errorf("UpsertLocalDigest: appID, serviceName e image son requeridos")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO docker_app_images
			(app_id, service_name, image, local_digest, remote_digest, remote_checked_at, check_status)
		VALUES (?, ?, ?, ?, ?, ?, 'ok')`,
		appID, serviceName, image, digest, digest, now)
	if err != nil {
		return fmt.Errorf("UpsertLocalDigest %s/%s: %w", appID, serviceName, err)
	}
	return nil
}

// UpdateLocalDigest actualiza solo el local_digest · se llama tras una
// actualización exitosa (`compose pull && up -d`) cuando la nueva versión
// ya está corriendo. NO modifica remote_digest ni remote_checked_at: la
// comprobación remota es independiente del install.
func (r *AppImagesRepo) UpdateLocalDigest(ctx context.Context, appID, serviceName, digest string) error {
	if appID == "" || serviceName == "" {
		return fmt.Errorf("UpdateLocalDigest: appID y serviceName son requeridos")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE docker_app_images
		SET local_digest = ?
		WHERE app_id = ? AND service_name = ?`,
		digest, appID, serviceName)
	if err != nil {
		return fmt.Errorf("UpdateLocalDigest %s/%s: %w", appID, serviceName, err)
	}
	return nil
}

// UpdateRemoteDigest actualiza remote_digest tras un `docker manifest inspect`
// exitoso. También refresca remote_checked_at (TTL del cache) y check_status.
func (r *AppImagesRepo) UpdateRemoteDigest(ctx context.Context, appID, serviceName, digest, status string) error {
	if appID == "" || serviceName == "" {
		return fmt.Errorf("UpdateRemoteDigest: appID y serviceName son requeridos")
	}
	if status == "" {
		status = "ok"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		UPDATE docker_app_images
		SET remote_digest = ?, remote_checked_at = ?, check_status = ?
		WHERE app_id = ? AND service_name = ?`,
		digest, now, status, appID, serviceName)
	if err != nil {
		return fmt.Errorf("UpdateRemoteDigest %s/%s: %w", appID, serviceName, err)
	}
	return nil
}

// GetByApp devuelve todas las imágenes (servicios) de una app concreta.
// Útil para el detail de la app y para el endpoint update-check.
//
// Devuelve slice vacío si la app no tiene imágenes tracked todavía (ej. apps
// instaladas antes del sprint Updates · backfill futuro).
func (r *AppImagesRepo) GetByApp(ctx context.Context, appID string) ([]AppImage, error) {
	if appID == "" {
		return nil, fmt.Errorf("GetByApp: appID requerido")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT app_id, service_name, image, local_digest, remote_digest,
		       remote_checked_at, check_status
		FROM docker_app_images
		WHERE app_id = ?
		ORDER BY service_name`, appID)
	if err != nil {
		return nil, fmt.Errorf("GetByApp %s: %w", appID, err)
	}
	defer rows.Close()

	var out []AppImage
	for rows.Next() {
		var img AppImage
		if err := rows.Scan(&img.AppID, &img.ServiceName, &img.Image,
			&img.LocalDigest, &img.RemoteDigest, &img.RemoteCheckedAt, &img.CheckStatus); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// DeleteByApp borra todas las imágenes de una app. Se llama tras desinstalar
// (tanto modo soft como wipe · la BD docker_apps se limpia, esta tabla debe
// seguir el mismo destino).
func (r *AppImagesRepo) DeleteByApp(ctx context.Context, appID string) error {
	if appID == "" {
		return fmt.Errorf("DeleteByApp: appID requerido")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM docker_app_images WHERE app_id = ?`, appID)
	if err != nil {
		return fmt.Errorf("DeleteByApp %s: %w", appID, err)
	}
	return nil
}

// AppUpdateSummary resume el estado de actualizaciones de una app.
// Usado por el endpoint updates-summary que alimenta el sidebar.
type AppUpdateSummary struct {
	AppID              string `json:"appId"`
	ServicesTotal      int    `json:"servicesTotal"`
	ServicesWithUpdate int    `json:"servicesWithUpdate"`
	OldestCheckAt      string `json:"oldestCheckAt"` // el remote_checked_at más antiguo
}

// ListAppsWithUpdates devuelve solo las apps que tienen al menos un servicio
// con update disponible. Cada row resume el estado · servicesWithUpdate > 0
// significa que al menos uno tiene local_digest != remote_digest.
//
// El sidebar lo usa para mostrar el icono azul + count. El catálogo lo usa
// para marcar las cards con badge "NUEVA".
func (r *AppImagesRepo) ListAppsWithUpdates(ctx context.Context) ([]AppUpdateSummary, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			app_id,
			COUNT(*) AS services_total,
			SUM(CASE WHEN local_digest != remote_digest AND remote_digest != '' THEN 1 ELSE 0 END) AS services_with_update,
			MIN(remote_checked_at) AS oldest_check
		FROM docker_app_images
		WHERE check_status = 'ok' OR check_status = ''
		GROUP BY app_id
		HAVING services_with_update > 0
		ORDER BY app_id`)
	if err != nil {
		return nil, fmt.Errorf("ListAppsWithUpdates: %w", err)
	}
	defer rows.Close()

	var out []AppUpdateSummary
	for rows.Next() {
		var s AppUpdateSummary
		var oldest sql.NullString
		if err := rows.Scan(&s.AppID, &s.ServicesTotal, &s.ServicesWithUpdate, &oldest); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if oldest.Valid {
			s.OldestCheckAt = oldest.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountAppsWithUpdates · helper para el sidebar (necesita solo el número).
// Más eficiente que ListAppsWithUpdates si solo queremos saber el count.
func (r *AppImagesRepo) CountAppsWithUpdates(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT app_id)
		FROM docker_app_images
		WHERE local_digest != remote_digest
		  AND remote_digest != ''
		  AND (check_status = 'ok' OR check_status = '')`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("CountAppsWithUpdates: %w", err)
	}
	return count, nil
}

// NeedsRemoteCheck devuelve true si el remote_digest está caducado (más viejo
// que el TTL) o nunca se comprobó. Útil para decidir cuándo llamar al registry.
//
// TTL recomendado: 6 horas (ver doc del sprint).
//
// Casos:
//   - check_status='unsupported' o 'unauthorized' → SIEMPRE false (no recomprobar)
//   - remote_checked_at vacío → true (nunca comprobado)
//   - fecha mal parseada → true (recomprobar defensivamente)
//   - check reciente (< TTL) → false (usar cache)
//   - check viejo (> TTL) → true (refrescar)
func (img *AppImage) NeedsRemoteCheck(ttl time.Duration) bool {
	// Primer check · estados terminales que NO se recomprueban.
	// Una imagen privada o no encontrada en el registry no va a cambiar
	// de status por reintentar · solo gastaría rate limit.
	if img.CheckStatus == "unsupported" || img.CheckStatus == "unauthorized" {
		return false
	}
	if img.RemoteCheckedAt == "" {
		return true // nunca comprobado
	}
	t, err := time.Parse(time.RFC3339, img.RemoteCheckedAt)
	if err != nil {
		return true // fecha mal parseada · mejor recomprobar
	}
	return time.Since(t) > ttl
}

// Global instance · inicializado en db.go
var appImagesRepo *AppImagesRepo
