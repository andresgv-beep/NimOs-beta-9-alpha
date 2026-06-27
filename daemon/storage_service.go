// storage_service.go — Capa de orquestación del módulo storage (Beta 8).
//
// Núcleo: tipo StorageService, lectores y validación. Las operaciones se
// reparten (Beta 8.2): pools en storage_service_pool.go, dispositivos en
// storage_service_device.go, perfiles en storage_service_profile.go.
//
// StorageService es la ÚNICA capa que ejecuta operaciones. Coordina:
//   - StorageRepo (persistencia en SQLite)
//   - PolicyChecker (validación de permisos)
//   - BtrfsExecutor (operaciones reales sobre BTRFS) ← futuro Bloque 2+
//
// Patrón de cada método:
//   1. Verificar policy (¿el caller puede hacer esto?)
//   2. Crear Operation en DB con status pending/in_progress
//   3. Ejecutar la acción física (BTRFS o metadata-only)
//   4. Persistir resultado y marcar Operation completed/failed
//   5. Devolver la Operation al caller
//
// Todo dentro de transacción SQLite cuando toca múltiples tablas.
//
// see docs/storage_invariants.md
// see docs/storage_api.md §4 para firmas completas

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
)

// validPoolName valida nombres de pool en Beta 8.1.
// Reglas:
//   - 2-32 caracteres
//   - Solo letras minúsculas, dígitos, guion (-) y guion-bajo (_)
//   - Debe empezar con letra (no número ni símbolo)
//
// Razón (HARD-1 fix): el nombre se usa como BTRFS label, mountpoint,
// SMB share name, fstab entry. Limitarlo evita problemas con caracteres
// especiales en cualquiera de esos contextos.
var validPoolName = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// ═════════════════════════════════════════════════════════════════════════════
// StorageService
// ═════════════════════════════════════════════════════════════════════════════

// StorageService es la capa de orquestación. Recibe dependencias por
// constructor para facilitar tests con mocks.
type StorageService struct {
	repo    *StorageRepo
	policy  *PolicyChecker
	btrfs   BtrfsExecutor
	scanner DeviceScanner
	clock   Clock
	db      *sql.DB // necesario para iniciar transacciones

	// deviceChecker es el preflight check ejecutado en CreatePool para
	// detectar storage preexistente en los discos antes de crear pool nuevo.
	// Inyectable para que los tests puedan saltar la validación de
	// existencia física en /dev (que falla con devices mockeados).
	//
	// Si es nil, se usa realDevicePreFlightCheck por defecto.
	//
	// DEUDA-ARQUI-OBSERVABLE-ENTITY (Beta 9): este es el punto de
	// extensión natural para soportar más kinds de storage observable
	// (ext4, mdraid, LUKS, ZFS, NTFS USB...). Hoy solo BTRFS.
	deviceChecker DeviceChecker
}

// DeviceChecker es el contrato del preflight check sobre devices.
// Toma una lista de devices resueltos y devuelve:
//
//	nil                     si todos están limpios y disponibles
//	*ErrDiskHasFilesystem   si alguno tiene storage detectable (con detalles)
//	error genérico          otros fallos (boot disk, holders, missing)
type DeviceChecker func(devices []*Device) error

// realDevicePreFlightCheck es el DeviceChecker de producción. Invoca
// preFlightCheck (storage_wipe.go) sobre cada device, que a su vez
// consulta el observer BTRFS y hace fallback a blkid si no está.
//
// Tests inyectan un noop o un mock vía service.deviceChecker = ...
func realDevicePreFlightCheck(devices []*Device) error {
	for _, d := range devices {
		diskPath := d.CurrentPath
		if diskPath == "" {
			diskPath = d.ByIDPath
		}
		if err := preFlightCheck(diskPath); err != nil {
			return err
		}
	}
	return nil
}

// NewStorageService crea el servicio con sus dependencias inyectadas.
func NewStorageService(db *sql.DB, repo *StorageRepo, policy *PolicyChecker,
	btrfs BtrfsExecutor, scanner DeviceScanner) *StorageService {
	return &StorageService{
		repo:    repo,
		policy:  policy,
		btrfs:   btrfs,
		scanner: scanner,
		clock:   NewRealClock(),
		db:      db,
	}
}

// SetClock inyecta un Clock personalizado. Solo para tests (FakeClock).
// En producción, se usa el RealClock por defecto.
func (s *StorageService) SetClock(c Clock) {
	s.clock = c
}

// Instancia global, conveniente para código que aún no usa inyección.
var storageService *StorageService

// initStorageService crea la instancia global. Llamar tras
// initStorageRepo() y initStoragePolicy().
func initStorageService() {
	executor := NewRealBtrfsExecutor()
	scanner := NewLsblkDeviceScanner()
	storageService = NewStorageService(db, storageRepo, storagePolicy, executor, scanner)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers compartidos para los métodos del service
// ─────────────────────────────────────────────────────────────────────────────

// runInTx ejecuta fn dentro de una transacción. Si fn devuelve error,
// hace rollback automático. Si fn devuelve nil, hace commit.
//
// Centralizar este patrón evita repetir BeginTx/defer Rollback/Commit
// en cada método del service.
func (s *StorageService) runInTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("BeginTx: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// checkPolicy es helper que valida y devuelve error semántico si no permite.
// Centraliza el patrón "policy.Allows + error con código".
func (s *StorageService) checkPolicy(pool *Pool, op OperationType) error {
	allowed, code := s.policy.AllowsWithReason(pool, op)
	if !allowed {
		return &ServiceError{
			Code: code,
			Msg:  fmt.Sprintf("operation %s not permitted on pool %s", op, pool.ID),
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ServiceError — error con código semántico
// ─────────────────────────────────────────────────────────────────────────────

// ServiceError es el error que devuelven los métodos del service cuando
// fallan por una razón identificable. El handler HTTP puede leer el Code
// y devolver el código HTTP correcto al frontend.
type ServiceError struct {
	Code string // ErrCode* (ver storage_types.go)
	Msg  string

	// Details es información estructurada opcional sobre el error, serializada
	// como `error.details` en la respuesta HTTP. Útil cuando el frontend necesita
	// más que un mensaje (ej: DISK_HAS_FILESYSTEM necesita saber qué pool, qué
	// UUID, qué profile detectó para construir el wizard de doble intención).
	//
	// Si Details es nil, el JSON no incluye el campo. Si contiene un struct
	// con json tags, se serializa según esos tags.
	Details interface{} `json:"-"`
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// errFromCode devuelve un ServiceError con el código y mensaje dados.
func errFromCode(code, msg string) error {
	return &ServiceError{Code: code, Msg: msg}
}

// errFromCodeWithDetails devuelve un ServiceError con código, mensaje y
// payload estructurado adicional para que la HTTP layer pueda exponerlo
// en error.details.
func errFromCodeWithDetails(code, msg string, details interface{}) error {
	return &ServiceError{Code: code, Msg: msg, Details: details}
}

// ═════════════════════════════════════════════════════════════════════════════
// Queries síncronas — proyecciones que el frontend pedirá
// ═════════════════════════════════════════════════════════════════════════════

// ListPools devuelve todos los pools con sus devices cargados y enriquecidos
// con datos runtime (Usage, Health, IsPrimary, Mounted).
// Esta es la query principal que el frontend usa para mostrar el estado.
func (s *StorageService) ListPools(ctx context.Context) ([]*Pool, error) {
	pools, err := s.repo.ListPools(ctx)
	if err != nil {
		return nil, err
	}

	primaryPool := getPrimaryPoolName()
	divergences := observerDivergencesFn() // FIX-2: verdad del observer (btrfs)
	filesystems := observerFilesystemsFn() // FIX-3: conteo real de devices del kernel

	// Hidratar cada pool con sus devices y capabilities + enriquecer
	for _, p := range pools {
		devices, err := s.repo.ListDevicesInPool(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("ListPools: hydrate devices for %s: %w", p.ID, err)
		}
		p.Devices = make([]Device, len(devices))
		for i, d := range devices {
			p.Devices[i] = *d
		}

		caps, err := s.repo.GetPoolCapabilities(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("ListPools: hydrate caps for %s: %w", p.ID, err)
		}
		p.Capabilities = caps

		// Campos derivados runtime (Usage, Health, IsPrimary, Mounted)
		enrichPool(p, primaryPool)

		// FIX-2 (split-brain): la realidad del observer sobrescribe la caché.
		// Si btrfs ve una divergencia para este pool, no puede quedar "healthy".
		reconcileHealthWithDivergences(p, divergences)
		reconcilePoolDeviceCounts(p, filesystems)
	}

	return pools, nil
}

// GetPool devuelve un pool por su ID con devices y capabilities hidratados
// y enriquecido con datos runtime (Usage, Health, IsPrimary, Mounted).
func (s *StorageService) GetPool(ctx context.Context, id string) (*Pool, error) {
	pool, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, errFromCode(ErrCodePoolNotFound,
			fmt.Sprintf("pool %s not found", id))
	}

	devices, err := s.repo.ListDevicesInPool(ctx, pool.ID)
	if err != nil {
		return nil, err
	}
	pool.Devices = make([]Device, len(devices))
	for i, d := range devices {
		pool.Devices[i] = *d
	}

	caps, err := s.repo.GetPoolCapabilities(ctx, pool.ID)
	if err != nil {
		return nil, err
	}
	pool.Capabilities = caps

	// Campos derivados runtime (Usage, Health, IsPrimary, Mounted)
	enrichPool(pool, getPrimaryPoolName())

	// FIX-2 (split-brain): la realidad del observer sobrescribe la caché.
	reconcileHealthWithDivergences(pool, observerDivergencesFn())
	reconcilePoolDeviceCounts(pool, observerFilesystemsFn())

	return pool, nil
}

// ListDevices devuelve todos los devices del sistema.
func (s *StorageService) ListDevices(ctx context.Context) ([]*Device, error) {
	return s.repo.ListDevices(ctx)
}

// ListAvailableDevices devuelve devices libres (no asignados a pool).
func (s *StorageService) ListAvailableDevices(ctx context.Context) ([]*Device, error) {
	return s.repo.ListAvailableDevices(ctx)
}

// ListOperations devuelve operaciones del journal según filtro.
// Útil para el activity timeline del frontend.
func (s *StorageService) ListOperations(ctx context.Context, f OperationFilter) ([]*Operation, error) {
	return s.repo.ListOperations(ctx, f)
}

// GetGeneration devuelve el contador global de mutaciones.
// El frontend puede usarlo para detectar si algo cambió antes de re-fetch.
func (s *StorageService) GetGeneration(ctx context.Context) (int64, error) {
	return s.repo.GetGlobalGeneration(ctx)
}

// ═════════════════════════════════════════════════════════════════════════════
// Mutaciones síncronas — metadata-only (sin BTRFS)
// ═════════════════════════════════════════════════════════════════════════════
//
// Estas operaciones solo modifican metadata en SQLite, sin tocar el
// filesystem real. Generan Operation con status=completed inmediato.
// see docs/storage_api.md §4.2

// ═════════════════════════════════════════════════════════════════════════════
// Stubs de mutaciones async (Bloque 2+)
// ═════════════════════════════════════════════════════════════════════════════
//
// Estos métodos están declarados como esqueleto. Su implementación real
// llegará cuando integremos BtrfsExecutor (Bloque 2 en adelante).
// De momento devuelven "not implemented" para que el código que los llame
// falle explícitamente en vez de silenciosamente.

// CreatePoolRequest es el payload de CreatePool.
//
// FORMATO DUAL DE ENTRADA (Postel's Law — "be liberal in what you accept"):
//
//	El campo de devices puede llegar de dos formas, y exactamente UNA de
//	ellas debe estar presente:
//
//	  1. DeviceIDs — UUIDs internos de Beta 8 (forma canónica, estable)
//	     Usado por:
//	       · Tests
//	       · Clientes que ya hicieron GET /v2/devices y conocen los IDs
//	       · Llamadas internas desde otro código Go
//
//	  2. Disks — paths de Linux (/dev/sdb, /dev/sdc)
//	     Usado por:
//	       · UI humana (el usuario piensa en paths, no UUIDs)
//	       · Migración legacy desde storage.json
//	       · Scripts/CLI manuales
//
//	Validate() normaliza ambos formatos a DeviceIDs internamente. Después
//	de Validate(), el resto del código del service trabaja siempre con
//	DeviceIDs (forma canónica). Una sola fuente de verdad.
//
// ERRORES de Validate():
//
//	· Ningún campo presente   → ErrCodeBadRequest "no devices specified"
//	· Ambos campos presentes  → ErrCodeBadRequest "specify EITHER disks OR device_ids"
//	· Path inexistente        → ErrCodeDeviceNotFound "device path %q not registered"
//	· Profile inválido        → ErrCodeProfileInvalid
//	· Insufficient disks      → ErrCodeInsufficientDisks
type CreatePoolRequest struct {
	Name    string  `json:"name"`
	Profile Profile `json:"profile"`

	// Exactamente UNO de estos dos debe estar presente.
	// Validate() los normaliza: rellena DeviceIDs a partir de Disks si hace falta.
	DeviceIDs []string `json:"device_ids,omitempty"`
	Disks     []string `json:"disks,omitempty"`

	Compression string `json:"compression,omitempty"`
	WipeFirst   bool   `json:"wipe_first,omitempty"`
}

// Validate verifica el request y normaliza Disks → DeviceIDs si aplica.
// Tras una llamada exitosa, req.DeviceIDs está poblado y Disks vacío.
//
// Por qué normaliza in-place y no devuelve un nuevo struct:
//
//	· El service ya recibe el request por valor (no se comparte estado externo)
//	· Evita allocar otra estructura
//	· El caller que quiera el request original lo conserva por valor antes de llamar
//
// Llamada típica:
//
//	if err := req.Validate(ctx, repo); err != nil { return nil, err }
//	// a partir de aquí req.DeviceIDs está garantizado poblado
func (req *CreatePoolRequest) Validate(ctx context.Context, repo *StorageRepo) error {
	// Validaciones que no dependen del repo
	if req.Name == "" {
		return errFromCode(ErrCodeBadRequest, "pool name is required")
	}
	// HARD-1 fix: validar charset + length del nombre del pool.
	// Si esto falla, el nombre podría romper BTRFS label, fstab,
	// SMB share name, o cualquier integración shell aguas abajo.
	if !validPoolName.MatchString(req.Name) {
		return errFromCode(ErrCodeBadRequest,
			"pool name must be 2-32 chars, lowercase letters/digits/-/_, starting with a letter")
	}
	if !req.Profile.IsValid() {
		return errFromCode(ErrCodeProfileInvalid,
			fmt.Sprintf("invalid profile %q", req.Profile))
	}

	// Resolver el formato dual de devices
	hasIDs := len(req.DeviceIDs) > 0
	hasDisks := len(req.Disks) > 0

	switch {
	case hasIDs && hasDisks:
		return errFromCode(ErrCodeBadRequest,
			"specify EITHER disks OR device_ids, not both")
	case !hasIDs && !hasDisks:
		return errFromCode(ErrCodeBadRequest, "no devices specified")
	case hasDisks:
		// Resolver paths → IDs vía repo.ListDevices.
		// Es O(N*M) pero N (discos del pool) es típicamente 2-8 y M
		// (devices totales en el sistema) es típicamente 2-20.
		// Para sistemas con 100+ discos, optimizar a un índice path→ID
		// pero no es necesario para el caso de uso real de NimOS.
		allDevices, err := repo.ListDevices(ctx)
		if err != nil {
			return fmt.Errorf("Validate: list devices: %w", err)
		}
		byPath := make(map[string]*Device, len(allDevices))
		for _, d := range allDevices {
			byPath[d.CurrentPath] = d
		}
		ids := make([]string, 0, len(req.Disks))
		for _, path := range req.Disks {
			d, ok := byPath[path]
			if !ok {
				return errFromCode(ErrCodeDeviceNotFound,
					fmt.Sprintf("device path %q not registered (run scan first?)", path))
			}
			ids = append(ids, d.ID)
		}
		req.DeviceIDs = ids
		req.Disks = nil // forma canónica para el resto del flujo
	}

	// Validación que requiere DeviceIDs ya resueltos
	if len(req.DeviceIDs) < req.Profile.MinDisks() {
		return errFromCode(ErrCodeInsufficientDisks,
			fmt.Sprintf("profile %s requires at least %d disks, got %d",
				req.Profile, req.Profile.MinDisks(), len(req.DeviceIDs)))
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers privados de mutaciones async
// ─────────────────────────────────────────────────────────────────────────────

// defaultIfEmpty devuelve fallback si s es "".
func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ═════════════════════════════════════════════════════════════════════════════
// ScanDevices — descubrir hardware y reconciliar con DB
// ═════════════════════════════════════════════════════════════════════════════

// ScanResult resume el resultado de una ejecución de ScanDevices.
type ScanResult struct {
	Total    int // discos físicos vistos
	Inserted int // discos nuevos registrados en DB
	Updated  int // discos ya conocidos cuya info se actualizó
	Skipped  int // discos descartados por no tener serial
}

// ═════════════════════════════════════════════════════════════════════════════
// AddDevice — expandir un pool añadiendo un disco
// ═════════════════════════════════════════════════════════════════════════════

// AddDeviceRequest es el payload de AddDevice.
type AddDeviceRequest struct {
	PoolID     string `json:"pool_id"`
	DeviceID   string `json:"device_id"`
	DevicePath string `json:"device_path,omitempty"`
	WipeFirst  bool   `json:"wipe_first,omitempty"`
}

// AddDevice añade un device a un pool BTRFS existente.
// Genera Operation con type=add_device. Ejecuta inline en Beta 8
// (el frontend hace polling vía la Operation).
//
// Pasos:
//  1. Verificar policy (pool managed, capability add_device)
//  2. Verificar que el device existe y NO está en otro pool
//  3. Crear Operation con status in_progress
//  4. Ejecutar btrfs device add
//  5. Persistir la asignación en DB
//  6. Marcar Operation completed (o failed con rollback)
//
// verifyPoolMountedFn verifica que un pool quedó realmente montado tras crearlo.
// Inyectable: en producción usa isPoolMounted (consulta el kernel); en tests se
// sustituye porque el executor mock no monta de verdad.
var verifyPoolMountedFn = func(mountPoint string) bool {
	return isPoolMounted(mountPoint)
}

// devicePathExists comprueba que un path de device existe realmente en /dev.
// Es var (no func) para que los tests puedan inyectar un stub: en producción
// usa os.Stat; en tests se sobreescribe para aceptar paths ficticios.
var devicePathExists = func(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ═════════════════════════════════════════════════════════════════════════════
// RemoveDevice — quitar un disco del pool
// ═════════════════════════════════════════════════════════════════════════════

// RemoveDeviceRequest es el payload de RemoveDevice.
type RemoveDeviceRequest struct {
	PoolID   string `json:"pool_id"`
	DeviceID string `json:"device_id"`
}

// ═════════════════════════════════════════════════════════════════════════════
// ReplaceDevice — sustituir un disco por otro
// ═════════════════════════════════════════════════════════════════════════════

// ReplaceDeviceRequest es el payload de ReplaceDevice.
type ReplaceDeviceRequest struct {
	PoolID      string `json:"pool_id"`
	OldDeviceID string `json:"old_device_id"`
	NewDeviceID string `json:"new_device_id"`
}

// ═════════════════════════════════════════════════════════════════════════════
// ConvertProfile — cambiar el perfil de un pool
// ═════════════════════════════════════════════════════════════════════════════

// ConvertProfileRequest es el payload de ConvertProfile.
type ConvertProfileRequest struct {
	PoolID     string  `json:"pool_id"`
	NewProfile Profile `json:"new_profile"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Pre-flight checks
// ─────────────────────────────────────────────────────────────────────────────
