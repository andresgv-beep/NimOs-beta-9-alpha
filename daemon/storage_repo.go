// storage_repo.go — Capa de acceso a datos del módulo storage (Beta 8).
//
// StorageRepo es la ÚNICA capa que toca SQLite directamente para las tablas
// storage_*. Encapsula queries y mutaciones, manteniendo la integridad
// transaccional. El resto del código (StorageService, handlers HTTP, etc.)
// usa StorageRepo, nunca SQL directo.
//
// Queries → devuelven entidades (Pool, Device, Operation, Event)
// Mutaciones → reciben *sql.Tx para que el caller controle la transacción
//
// Invariantes:
//   - Toda mutación incrementa la generation de la entidad afectada Y
//     el global_generation de storage_metadata (helper incrementGeneration).
//   - Las mutaciones multi-tabla SIEMPRE en transacción.
//   - PRAGMA foreign_keys debe estar ON (verificado en initStorageSchema).
//
// see docs/storage_invariants.md#5
// see docs/storage_api.md §3 para firmas completas

package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// StorageRepo
// ─────────────────────────────────────────────────────────────────────────────

// StorageRepo encapsula el acceso a las tablas storage_* en SQLite.
type StorageRepo struct {
	db *sql.DB
}

// NewStorageRepo crea un repositorio sobre la conexión SQLite dada.
// La conexión debe tener foreign_keys ON (initStorageSchema lo verifica).
func NewStorageRepo(db *sql.DB) *StorageRepo {
	return &StorageRepo{db: db}
}

// storageRepo es la instancia global del repo. Se inicializa al arranque
// junto con la DB. Acceso conveniente para código que no recibe el repo
// por parámetro; el código nuevo debe usar inyección explícita.
var storageRepo *StorageRepo

// initStorageRepo crea la instancia global del repo. Llamar tras
// initStorageSchema en el arranque del daemon.
func initStorageRepo() {
	storageRepo = NewStorageRepo(db)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers internos
// ─────────────────────────────────────────────────────────────────────────────

// incrementGlobalGeneration incrementa el contador global de mutaciones.
// Debe llamarse al final de cada mutación, dentro de la transacción.
// Devuelve el nuevo valor.
func (r *StorageRepo) incrementGlobalGeneration(ctx context.Context, tx *sql.Tx) (int64, error) {
	_, err := tx.ExecContext(ctx,
		`UPDATE storage_metadata
		 SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
		 WHERE key = 'global_generation'`)
	if err != nil {
		return 0, fmt.Errorf("incrementGlobalGeneration: %w", err)
	}
	var gen int64
	err = tx.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM storage_metadata
		 WHERE key = 'global_generation'`).Scan(&gen)
	if err != nil {
		return 0, fmt.Errorf("incrementGlobalGeneration read: %w", err)
	}
	return gen, nil
}

// nullableString convierte un *string a sql.NullString para escritura.
func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// stringFromNull convierte un sql.NullString a *string (nil si no es valid).
func stringFromNull(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

// nullableTime convierte un *time.Time a sql.NullString (ISO 8601) para escritura.
func nullableTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339Nano), Valid: true}
}

// timeFromNull parsea un sql.NullString (ISO 8601) a *time.Time.
func timeFromNull(n sql.NullString) *time.Time {
	if !n.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, n.String)
	if err != nil {
		return nil
	}
	return &t
}

// ═════════════════════════════════════════════════════════════════════════════
// POOLS — Queries
// ═════════════════════════════════════════════════════════════════════════════

// poolColumns es la lista de columnas que devuelven todos los SELECT de pools.
// Mantenerla constante simplifica el scanning.
const poolColumns = `id, name, btrfs_uuid, profile, mount_point, role, compression,
	control_state, discovered_at, created_at, generation`

// scanPool lee una fila en un *Pool. Compatible con poolColumns.
func scanPool(rows interface {
	Scan(dest ...any) error
}) (*Pool, error) {
	var p Pool
	var discoveredAt sql.NullString
	var createdAt string
	var role, controlState, compression string

	err := rows.Scan(
		&p.ID, &p.Name, &p.BtrfsUUID, &p.Profile, &p.MountPoint,
		&role, &compression, &controlState,
		&discoveredAt, &createdAt, &p.Generation,
	)
	if err != nil {
		return nil, err
	}
	p.Role = Role(role)
	p.ControlState = ControlState(controlState)
	p.Compression = compression
	p.DiscoveredAt = timeFromNull(discoveredAt)
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	return &p, nil
}

// GetPool devuelve un pool por su ID interno (UUID).
func (r *StorageRepo) GetPool(ctx context.Context, id string) (*Pool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+poolColumns+` FROM storage_pools WHERE id = ?`, id)
	p, err := scanPool(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// GetPoolByName devuelve un pool por su nombre legible.
func (r *StorageRepo) GetPoolByName(ctx context.Context, name string) (*Pool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+poolColumns+` FROM storage_pools WHERE name = ?`, name)
	p, err := scanPool(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// GetPoolByBtrfsUUID devuelve un pool por su UUID de filesystem.
func (r *StorageRepo) GetPoolByBtrfsUUID(ctx context.Context, uuid string) (*Pool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+poolColumns+` FROM storage_pools WHERE btrfs_uuid = ?`, uuid)
	p, err := scanPool(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// ListPools devuelve todos los pools ordenados por created_at.
func (r *StorageRepo) ListPools(ctx context.Context) ([]*Pool, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+poolColumns+` FROM storage_pools ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("ListPools: %w", err)
	}
	defer rows.Close()

	pools := []*Pool{}
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPools scan: %w", err)
		}
		pools = append(pools, p)
	}
	return pools, rows.Err()
}

// ListPoolsByControlState devuelve pools filtrados por control_state.
func (r *StorageRepo) ListPoolsByControlState(ctx context.Context, state ControlState) ([]*Pool, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+poolColumns+` FROM storage_pools WHERE control_state = ? ORDER BY created_at`,
		string(state))
	if err != nil {
		return nil, fmt.Errorf("ListPoolsByControlState: %w", err)
	}
	defer rows.Close()

	pools := []*Pool{}
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, fmt.Errorf("ListPoolsByControlState scan: %w", err)
		}
		pools = append(pools, p)
	}
	return pools, rows.Err()
}

// HasAnyPool devuelve true si hay al menos un pool en la DB.
// Pública porque puede ser útil para verificar si el sistema tiene
// storage configurado (ej. wizards de bootstrap, checks de health).
func (r *StorageRepo) HasAnyPool(ctx context.Context) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_pools`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("HasAnyPool: %w", err)
	}
	return count > 0, nil
}

// ═════════════════════════════════════════════════════════════════════════════
// POOLS — Mutaciones (transaccionales)
// ═════════════════════════════════════════════════════════════════════════════

// CreatePool inserta un nuevo pool. Falla si el name o btrfs_uuid ya existen.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) CreatePool(ctx context.Context, tx *sql.Tx, p *Pool) error {
	if !p.Profile.IsValid() {
		return fmt.Errorf("CreatePool: invalid profile %q", p.Profile)
	}
	if p.Role == "" {
		p.Role = RoleData
	}
	if p.ControlState == "" {
		p.ControlState = ControlStateManaged
	}
	if p.Compression == "" {
		p.Compression = "none"
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO storage_pools
		 (id, name, btrfs_uuid, profile, mount_point, role, compression,
		  control_state, discovered_at, created_at, generation)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		p.ID, p.Name, p.BtrfsUUID, string(p.Profile), p.MountPoint,
		string(p.Role), p.Compression, string(p.ControlState),
		nullableTime(p.DiscoveredAt),
		p.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("CreatePool: %w", err)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// SetPoolMountPoint actualiza el mount point de un pool. Usado por el rename,
// donde la ruta deriva del nombre (/nimos/pools/<name>). En transacción.
func (r *StorageRepo) SetPoolMountPoint(ctx context.Context, tx *sql.Tx, id, mountPoint string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE storage_pools SET mount_point = ?, generation = generation + 1 WHERE id = ?`,
		mountPoint, id)
	if err != nil {
		return fmt.Errorf("SetPoolMountPoint: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("SetPoolMountPoint: pool %q not found", id)
	}
	return nil
}

// RenamePool cambia el name de un pool (el id interno NO cambia).
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) RenamePool(ctx context.Context, tx *sql.Tx, id, newName string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE storage_pools
		 SET name = ?, generation = generation + 1
		 WHERE id = ?`,
		newName, id)
	if err != nil {
		return fmt.Errorf("RenamePool: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("RenamePool: pool %q not found", id)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// SetPoolProfile cambia el profile de un pool. Debe llamarse dentro de tx.
// Añadido para STOR-07 (evitar UPDATE inline en el service) y STOR-01-C
// (aceptar el profile real tras un drift).
func (r *StorageRepo) SetPoolProfile(ctx context.Context, tx *sql.Tx, id string, profile Profile) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE storage_pools
		 SET profile = ?, generation = generation + 1
		 WHERE id = ?`,
		string(profile), id)
	if err != nil {
		return fmt.Errorf("SetPoolProfile: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("SetPoolProfile: pool %q not found", id)
	}
	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// SetPoolControlState cambia el control_state de un pool.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) SetPoolControlState(ctx context.Context, tx *sql.Tx, id string, state ControlState) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE storage_pools
		 SET control_state = ?, generation = generation + 1
		 WHERE id = ?`,
		string(state), id)
	if err != nil {
		return fmt.Errorf("SetPoolControlState: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("SetPoolControlState: pool %q not found", id)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// SetPoolCompression cambia la compresión de un pool.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) SetPoolCompression(ctx context.Context, tx *sql.Tx, id, compression string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE storage_pools
		 SET compression = ?, generation = generation + 1
		 WHERE id = ?`,
		compression, id)
	if err != nil {
		return fmt.Errorf("SetPoolCompression: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("SetPoolCompression: pool %q not found", id)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// DeletePool elimina un pool. Los pool_devices y pool_capabilities se borran
// por CASCADE; las operations conservan el histórico con pool_id NULL.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) DeletePool(ctx context.Context, tx *sql.Tx, id string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM storage_pools WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("DeletePool: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("DeletePool: pool %q not found", id)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// ═════════════════════════════════════════════════════════════════════════════
// DEVICES — Queries
// ═════════════════════════════════════════════════════════════════════════════

const deviceColumns = `id, serial, by_id_path, current_path, wwn, model,
	size_bytes, last_seen_at, generation`

func scanDevice(rows interface {
	Scan(dest ...any) error
}) (*Device, error) {
	var d Device
	var wwn, model sql.NullString
	var sizeBytes sql.NullInt64
	var lastSeenAt sql.NullString

	err := rows.Scan(
		&d.ID, &d.Serial, &d.ByIDPath, &d.CurrentPath,
		&wwn, &model, &sizeBytes, &lastSeenAt, &d.Generation,
	)
	if err != nil {
		return nil, err
	}
	if wwn.Valid {
		d.WWN = wwn.String
	}
	if model.Valid {
		d.Model = model.String
	}
	if sizeBytes.Valid {
		d.SizeBytes = sizeBytes.Int64
	}
	if lastSeenAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, lastSeenAt.String); err == nil {
			d.LastSeenAt = t
		}
	}
	return &d, nil
}

// GetDevice devuelve un device por su ID interno.
func (r *StorageRepo) GetDevice(ctx context.Context, id string) (*Device, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM storage_devices WHERE id = ?`, id)
	d, err := scanDevice(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

// GetDeviceBySerial devuelve un device por su serial (identidad absoluta).
// see docs/storage_invariants.md#3.1
func (r *StorageRepo) GetDeviceBySerial(ctx context.Context, serial string) (*Device, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM storage_devices WHERE serial = ?`, serial)
	d, err := scanDevice(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

// GetDeviceByByIDPath devuelve un device por su by_id_path.
// Útil para reconciliación al boot. La identidad real es por serial.
func (r *StorageRepo) GetDeviceByByIDPath(ctx context.Context, byIDPath string) (*Device, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM storage_devices WHERE by_id_path = ?`, byIDPath)
	d, err := scanDevice(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

// ListDevices devuelve todos los devices conocidos por el sistema.
func (r *StorageRepo) ListDevices(ctx context.Context) ([]*Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM storage_devices ORDER BY current_path`)
	if err != nil {
		return nil, fmt.Errorf("ListDevices: %w", err)
	}
	defer rows.Close()

	devices := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("ListDevices scan: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ListAvailableDevices devuelve devices que no están asignados a ningún pool.
func (r *StorageRepo) ListAvailableDevices(ctx context.Context) ([]*Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM storage_devices d
		 WHERE NOT EXISTS (
			SELECT 1 FROM storage_pool_devices pd WHERE pd.device_id = d.id
		 )
		 ORDER BY d.current_path`)
	if err != nil {
		return nil, fmt.Errorf("ListAvailableDevices: %w", err)
	}
	defer rows.Close()

	devices := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("ListAvailableDevices scan: %w", err)
		}
		d.Available = true
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ListDevicesInPool devuelve los devices asignados a un pool.
func (r *StorageRepo) ListDevicesInPool(ctx context.Context, poolID string) ([]*Device, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT d.id, d.serial, d.by_id_path, d.current_path, d.wwn, d.model,
		        d.size_bytes, d.last_seen_at, d.generation
		 FROM storage_devices d
		 JOIN storage_pool_devices pd ON pd.device_id = d.id
		 WHERE pd.pool_id = ?
		 ORDER BY pd.added_at`,
		poolID)
	if err != nil {
		return nil, fmt.Errorf("ListDevicesInPool: %w", err)
	}
	defer rows.Close()

	devices := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("ListDevicesInPool scan: %w", err)
		}
		poolIDCopy := poolID
		d.InPool = &poolIDCopy
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ═════════════════════════════════════════════════════════════════════════════
// DEVICES — Mutaciones (transaccionales)
// ═════════════════════════════════════════════════════════════════════════════

// UpsertDevice inserta o actualiza un device. Matching por serial (identidad
// absoluta). Si el serial existe, actualiza by_id_path, current_path y model
// (que pueden cambiar entre reboots o tras kernel updates).
// Devuelve true si fue INSERT (nuevo), false si fue UPDATE (existente).
// Debe llamarse dentro de una transacción.
// see docs/storage_invariants.md#3
func (r *StorageRepo) UpsertDevice(ctx context.Context, tx *sql.Tx, d *Device) (wasInsert bool, err error) {
	if d.Serial == "" {
		return false, fmt.Errorf("UpsertDevice: serial is required (no identity)")
	}
	if d.LastSeenAt.IsZero() {
		d.LastSeenAt = time.Now().UTC()
	}

	// Buscar por serial primero (identidad absoluta).
	var existingID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM storage_devices WHERE serial = ?`, d.Serial).Scan(&existingID)

	if err == sql.ErrNoRows {
		// Disco nuevo: INSERT.
		_, err = tx.ExecContext(ctx,
			`INSERT INTO storage_devices
			 (id, serial, by_id_path, current_path, wwn, model, size_bytes,
			  last_seen_at, generation)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			d.ID, d.Serial, d.ByIDPath, d.CurrentPath,
			nullableString(stringOrNil(d.WWN)),
			nullableString(stringOrNil(d.Model)),
			d.SizeBytes,
			d.LastSeenAt.Format(time.RFC3339Nano),
		)
		if err != nil {
			return false, fmt.Errorf("UpsertDevice INSERT: %w", err)
		}
		wasInsert = true
	} else if err == nil {
		// Disco conocido: actualizar campos volátiles. by_id_path y current_path
		// pueden cambiar entre reboots; serial nunca.
		_, err = tx.ExecContext(ctx,
			`UPDATE storage_devices
			 SET by_id_path = ?, current_path = ?, wwn = ?, model = ?,
			     size_bytes = ?, last_seen_at = ?, generation = generation + 1
			 WHERE id = ?`,
			d.ByIDPath, d.CurrentPath,
			nullableString(stringOrNil(d.WWN)),
			nullableString(stringOrNil(d.Model)),
			d.SizeBytes,
			d.LastSeenAt.Format(time.RFC3339Nano),
			existingID,
		)
		if err != nil {
			return false, fmt.Errorf("UpsertDevice UPDATE: %w", err)
		}
		d.ID = existingID
		wasInsert = false
	} else {
		return false, fmt.Errorf("UpsertDevice lookup: %w", err)
	}

	if _, err := r.incrementGlobalGeneration(ctx, tx); err != nil {
		return wasInsert, err
	}
	return wasInsert, nil
}

// UpdateDeviceCurrentPath actualiza solo el current_path (cache runtime).
// No incrementa generation porque es cache, no identidad.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) UpdateDeviceCurrentPath(ctx context.Context, tx *sql.Tx, id, newPath string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE storage_devices SET current_path = ? WHERE id = ?`,
		newPath, id)
	return err
}

// UpdateDeviceLastSeen actualiza last_seen_at. Llamado por el background
// reconciler en cada scan. No incrementa generation.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) UpdateDeviceLastSeen(ctx context.Context, tx *sql.Tx, id string, t time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE storage_devices SET last_seen_at = ? WHERE id = ?`,
		t.UTC().Format(time.RFC3339Nano), id)
	return err
}

// ═════════════════════════════════════════════════════════════════════════════
// POOL-DEVICE ASSIGNMENTS — Mutaciones
// ═════════════════════════════════════════════════════════════════════════════

// AssignDeviceToPool añade una entrada en storage_pool_devices.
// Falla si el device ya está asignado (PRIMARY KEY) o si pool/device no existen
// (FOREIGN KEY).
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) AssignDeviceToPool(ctx context.Context, tx *sql.Tx, poolID, deviceID string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO storage_pool_devices (pool_id, device_id, added_at)
		 VALUES (?, ?, ?)`,
		poolID, deviceID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("AssignDeviceToPool: %w", err)
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// UnassignDeviceFromPool elimina la relación pool↔device.
// Debe llamarse dentro de una transacción.
func (r *StorageRepo) UnassignDeviceFromPool(ctx context.Context, tx *sql.Tx, poolID, deviceID string) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM storage_pool_devices WHERE pool_id = ? AND device_id = ?`,
		poolID, deviceID)
	if err != nil {
		return fmt.Errorf("UnassignDeviceFromPool: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("UnassignDeviceFromPool: assignment not found")
	}

	_, err = r.incrementGlobalGeneration(ctx, tx)
	return err
}

// ═════════════════════════════════════════════════════════════════════════════
// METADATA — Acceso simple
// ═════════════════════════════════════════════════════════════════════════════

// GetMetadata devuelve un valor de storage_metadata. Si no existe, "" y nil.
func (r *StorageRepo) GetMetadata(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM storage_metadata WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("GetMetadata %q: %w", key, err)
	}
	return value, nil
}

// SetMetadata escribe (insert or replace) un valor en storage_metadata.
// Útil para 'primary_pool', 'configured_at', etc.
// see docs/schema.sql §1 para la lista de keys válidas.
func (r *StorageRepo) SetMetadata(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO storage_metadata (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("SetMetadata %q: %w", key, err)
	}
	return nil
}

// GetGlobalGeneration devuelve el contador global de mutaciones.
// Útil para que el frontend detecte si algo cambió sin descargar listas enteras.
func (r *StorageRepo) GetGlobalGeneration(ctx context.Context) (int64, error) {
	v, err := r.GetMetadata(ctx, "global_generation")
	if err != nil {
		return 0, err
	}
	var gen int64
	fmt.Sscanf(v, "%d", &gen)
	return gen, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// stringOrNil devuelve nil si la string está vacía, *s en caso contrario.
// Útil para escribir NULL en columnas opcionales.
func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
