# NimOS Beta 8 — Storage Internal API

**Autor**: Andrés + Claude Opus 4.7 — Mayo 2026
**Versión**: 1.0 (Fase 1 — diseño)
**Ámbito**: firmas Go del módulo `daemon/storage_*`

---

## 1. Filosofía de la API

Este documento define las firmas Go del módulo de storage refactorizado en Beta 8. Sigue las invariantes del plan principal:

- **Queries vs Commands separados** (sección 15.bis.1 del plan)
- **Identidad por UUID interno**, no por `current_path`
- **Mutaciones largas devuelven `*Operation`**, no `error` solo
- **Errores con código semántico** (constantes string), no solo texto
- **Policy layer** aparte del storage layer

---

## 2. Tipos del dominio

### 2.1 Pool

```go
// Pool representa un filesystem BTRFS gestionado u observado por NimOS.
// El campo Role es decorativo en Beta 8 (siempre "data"). Consumidores
// futuros (NimBackup, nodos remotos) lo usarán en Beta 9+.
type Pool struct {
    ID            string       // UUID interno, estable, nunca cambia
    Name          string       // Nombre legible, único, puede cambiar
    BtrfsUUID     string       // UUID del filesystem (de blkid)
    Profile       Profile      // single | raid1 | raid1c3 | raid10
    MountPoint    string       // /nimos/pools/<name>
    Role          Role         // data (Beta 8 siempre)
    ControlState  ControlState // managed | observed (Beta 8)
    DiscoveredAt  *time.Time   // primera vez que se vio (puede ser nil para pools creados, no observados)
    CreatedAt     time.Time
    Generation    int64        // incrementa en cada mutación
    Capabilities  []string     // ["snapshots", "balance", "replace_device", ...]
    Devices       []Device     // lista de dispositivos miembros (cargado bajo demanda)
}

// Profile representa la disposición de datos en BTRFS.
type Profile string

const (
    ProfileSingle  Profile = "single"
    ProfileRaid1   Profile = "raid1"
    ProfileRaid1c3 Profile = "raid1c3"
    ProfileRaid10  Profile = "raid10"
)

// Role indica la función del pool dentro del sistema.
// Ver beta8_storage_plan.md §16 para consumidores planeados.
type Role string

const (
    RoleData   Role = "data"   // Beta 8 default, único valor activo
    RoleBackup Role = "backup" // TODO(beta9): consumido por NimBackup
    RoleCache  Role = "cache"  // TODO(beta10): cache tier SSD
    RoleSystem Role = "system" // TODO(future): pool del SO
)

// ControlState indica el grado de autoridad de NimOS sobre el pool.
type ControlState string

const (
    ControlStateManaged  ControlState = "managed"  // Beta 8: dueño completo
    ControlStateObserved ControlState = "observed" // Beta 8: solo lectura
    ControlStateImported ControlState = "imported" // TODO(beta9): pendiente de adopción
    ControlStateForeign  ControlState = "foreign"  // TODO(beta9): filesystem no entendido
    ControlStateRecovery ControlState = "recovery" // TODO(beta9): reconciliación
)
```

### 2.2 Device

```go
// Device representa un disco físico conocido por el sistema.
//
// JERARQUÍA DE IDENTIDAD (de más fuerte a más débil):
//   1. Serial — IDENTIDAD ABSOLUTA. Grabado en firmware, no cambia.
//      Es UNIQUE NOT NULL. Si un disco no expone serial, no se gestiona.
//   2. ByIDPath — IDENTIDAD ESTABLE. Construido de model+serial, muy estable
//      pero puede variar ligeramente entre controladoras SATA o tras kernel
//      updates. Se usa para invocar comandos, no como identidad última.
//   3. CurrentPath — CACHE RUNTIME. /dev/sdb cambia entre reboots según
//      orden de detección. NO es identidad nunca.
//
// Cuando se vuelve a ver un disco tras reboot, el matching se hace por
// Serial primero. Si el ByIDPath cambió pero el Serial coincide, se
// actualiza el ByIDPath de la DB en lugar de crear un device nuevo.
type Device struct {
    ID          string    // UUID interno
    Serial      string    // IDENTIDAD ABSOLUTA (UNIQUE NOT NULL en schema)
    ByIDPath    string    // /dev/disk/by-id/ata-... (estable, cache de identidad)
    CurrentPath string    // /dev/sdb (CACHE, cambia entre reboots)
    WWN         string    // identificador adicional, puede ser vacío
    Model       string
    SizeBytes   int64
    LastSeenAt  time.Time
    Generation  int64

    // Computed fields
    InPool      *string   // pool_id si está asignado, nil si libre
    Available   bool      // true si está libre y elegible para nuevo pool
}
```

**Invariantes**:
- `CurrentPath` solo se usa para invocar comandos del sistema y mostrar al usuario. Cualquier lógica que decida algo a partir de `CurrentPath` es un bug.
- Resolver identidad por `Serial` (primero) o `ID` interno.
- `ByIDPath` se usa para construir el comando final (`mkfs.btrfs /dev/disk/by-id/...`) porque es más legible en logs y resiste cambios de letra. Pero la igualdad de devices se compara por `Serial`.
- Si un disco no expone serial (extremadamente raro, USB baratos), **NimOS no lo gestiona** y muestra una advertencia al usuario.

### 2.3 Operation

```go
// Operation representa una operación de storage en curso o histórica.
// Las mutaciones se modelan como Operations persistidas, INCLUSO LAS SÍNCRONAS.
// Esto mantiene un timeline consistente y auditoría completa.
type Operation struct {
    ID          string          // UUID
    Type        OperationType   // create_pool | add_device | rename_pool | ...
    PoolID      *string         // nil para operaciones que crean el pool
    Status      OperationStatus // pending | in_progress | completed | failed | rolled_back
    StartedAt   time.Time
    CompletedAt *time.Time
    Error       *string         // mensaje libre del error si falló
    ErrorCode   *string         // código semántico (ErrCode*) si falló
    Data        json.RawMessage // payload temporal (parámetros + progreso)
    Events      []Event         // timeline (cargado bajo demanda)
}

// OperationMode indica si la operación se completa dentro de la petición
// HTTP (sync) o si requiere polling de /api/storage/operations/:id (async).
type OperationMode string

const (
    OperationModeSync  OperationMode = "sync"   // completa en la respuesta HTTP
    OperationModeAsync OperationMode = "async"  // requiere polling posterior
)

// operationModeMap es la verdad sobre qué operaciones son sync o async.
// NO se puede cambiar por handler. Si necesitas cambiar el modo de una
// operación, edita esta tabla y todo el código se ajusta automáticamente.
var operationModeMap = map[OperationType]OperationMode{
    // Sync — completan en milisegundos
    OpTypeRenamePool:     OperationModeSync,
    OpTypeChangeRole:     OperationModeSync,
    OpTypeSetCompression: OperationModeSync,
    OpTypeSetScrubPolicy: OperationModeSync,
    OpTypeControlChange:  OperationModeSync,
    OpTypeBalancePause:   OperationModeSync,
    OpTypeBalanceResume:  OperationModeSync,

    // Async — segundos a horas
    OpTypeCreatePool:     OperationModeAsync,
    OpTypeDestroyPool:    OperationModeAsync,
    OpTypeAddDevice:      OperationModeAsync,
    OpTypeRemoveDevice:   OperationModeAsync,
    OpTypeReplaceDevice:  OperationModeAsync,
    OpTypeConvertProfile: OperationModeAsync,
    OpTypeStartScrub:     OperationModeAsync,
    OpTypeCreateSnapshot: OperationModeAsync,
    OpTypeDeleteSnapshot: OperationModeAsync,
    OpTypeImportPool:     OperationModeAsync,
}

func (op OperationType) Mode() OperationMode {
    if mode, ok := operationModeMap[op]; ok {
        return mode
    }
    return OperationModeAsync  // safe default si se añade una operación sin mapear
}

type OperationType string

const (
    // Sync ops (metadata mutations)
    OpTypeRenamePool       OperationType = "rename_pool"
    OpTypeChangeRole       OperationType = "change_role"
    OpTypeSetCompression   OperationType = "set_compression"
    OpTypeSetScrubPolicy   OperationType = "set_scrub_policy"
    OpTypeControlChange    OperationType = "control_state_change"
    OpTypeBalancePause     OperationType = "balance_pause"
    OpTypeBalanceResume    OperationType = "balance_resume"

    // Async ops (long-running)
    OpTypeCreatePool       OperationType = "create_pool"
    OpTypeDestroyPool      OperationType = "destroy_pool"
    OpTypeAddDevice        OperationType = "add_device"
    OpTypeRemoveDevice     OperationType = "remove_device"
    OpTypeReplaceDevice    OperationType = "replace_device"
    OpTypeConvertProfile   OperationType = "convert_profile"
    OpTypeStartScrub       OperationType = "start_scrub"
    OpTypeCreateSnapshot   OperationType = "create_snapshot"
    OpTypeDeleteSnapshot   OperationType = "delete_snapshot"
    OpTypeImportPool       OperationType = "import_pool"
)

type OperationStatus string

const (
    OpStatusPending     OperationStatus = "pending"
    OpStatusInProgress  OperationStatus = "in_progress"
    OpStatusCompleted   OperationStatus = "completed"
    OpStatusFailed      OperationStatus = "failed"
    OpStatusRolledBack  OperationStatus = "rolled_back"
    OpStatusCancelled   OperationStatus = "cancelled"
)
```

**Invariante crítica**: TODA mutación genera una `Operation`, sea sync o async. La diferencia entre sync y async no es "si se registra", sino "si la respuesta HTTP espera al resultado o no":

- **Sync**: handler crea operation → ejecuta → marca completed/failed → responde con `{pool, operation}` en una sola llamada HTTP (200 OK)
- **Async**: handler crea operation → lanza goroutine → responde inmediatamente con `{operation}` (202 Accepted) → cliente hace polling

**Beneficio del modelo unificado**:
- Timeline completo: aparecen TODAS las acciones, no solo las largas
- Auditoría real: "¿quién renombró el pool data?" → consulta el histórico
- Debugging: si hay un patrón raro de cambios de role, queda registrado
- Modelo mental único: una sola forma de pensar las mutaciones, no dos

**Coste**: una row más en SQLite por mutación. Despreciable.



### 2.4 Event

```go
// Event representa un suceso dentro de una Operation.
// Permite reconstruir el timeline detallado de qué pasó.
type Event struct {
    ID          string
    OperationID string
    Timestamp   time.Time
    Level       EventLevel
    Message     string
}

type EventLevel string

const (
    EventLevelDebug EventLevel = "debug"
    EventLevelInfo  EventLevel = "info"
    EventLevelWarn  EventLevel = "warn"
    EventLevelError EventLevel = "error"
)
```

### 2.5 Códigos de error semánticos

```go
// Constantes string para el campo ErrorCode de Operation y respuestas HTTP.
// El frontend puede reaccionar al código sin parsear el mensaje.
const (
    ErrCodePoolObserved        = "pool_observed"         // pool en estado observed, no permite mutación
    ErrCodePoolNotFound        = "pool_not_found"        // no existe pool con ese ID/nombre
    ErrCodePoolNameTaken       = "pool_name_taken"       // nombre duplicado
    ErrCodeCapabilityMissing   = "capability_missing"    // operación no soportada por este pool
    ErrCodeOperationInProgress = "operation_in_progress" // ya hay una op corriendo
    ErrCodeDeviceInUse         = "device_in_use"         // disco asignado a otro pool
    ErrCodeDeviceMissing       = "device_missing"        // disco no presente físicamente
    ErrCodeDeviceNotEligible   = "device_not_eligible"   // disco demasiado pequeño / boot / etc.
    ErrCodeProfileInvalid      = "profile_invalid"       // profile desconocido
    ErrCodeInsufficientDisks   = "insufficient_disks"    // faltan discos para el profile pedido
    ErrCodeMinDisksReached     = "min_disks_reached"     // remove_device dejaría el pool sin redundancia
    ErrCodeServicesActive      = "services_active"       // hay shares/services usando el pool
    ErrCodeMountFailed         = "mount_failed"
    ErrCodeUnmountFailed       = "unmount_failed"
    ErrCodeBtrfsCommandFailed  = "btrfs_command_failed"
    ErrCodeInternal            = "internal"              // bug genérico (logear)
)
```

---

## 3. StorageRepo — Capa de acceso a datos

`StorageRepo` es la única capa que toca SQLite directamente. Encapsula todas las queries y mantiene la integridad transaccional.

```go
type StorageRepo struct {
    db *sql.DB
}

func NewStorageRepo(db *sql.DB) *StorageRepo {
    return &StorageRepo{db: db}
}
```

### 3.1 Queries (devuelven entidades)

```go
// --- Pools ---

func (r *StorageRepo) GetPool(ctx context.Context, id string) (*Pool, error)
func (r *StorageRepo) GetPoolByName(ctx context.Context, name string) (*Pool, error)
func (r *StorageRepo) GetPoolByBtrfsUUID(ctx context.Context, uuid string) (*Pool, error)
func (r *StorageRepo) ListPools(ctx context.Context) ([]*Pool, error)
func (r *StorageRepo) ListPoolsByControlState(ctx context.Context, state ControlState) ([]*Pool, error)
func (r *StorageRepo) HasAnyPool(ctx context.Context) (bool, error)

// --- Devices ---

func (r *StorageRepo) GetDevice(ctx context.Context, id string) (*Device, error)
func (r *StorageRepo) GetDeviceByByIDPath(ctx context.Context, byIDPath string) (*Device, error)
func (r *StorageRepo) GetDeviceBySerial(ctx context.Context, serial string) (*Device, error)
func (r *StorageRepo) ListDevices(ctx context.Context) ([]*Device, error)
func (r *StorageRepo) ListAvailableDevices(ctx context.Context) ([]*Device, error) // no asignados, elegibles
func (r *StorageRepo) ListDevicesInPool(ctx context.Context, poolID string) ([]*Device, error)

// --- Operations ---

func (r *StorageRepo) GetOperation(ctx context.Context, id string) (*Operation, error)
func (r *StorageRepo) ListOperations(ctx context.Context, filter OperationFilter) ([]*Operation, error)
func (r *StorageRepo) ListPendingOperations(ctx context.Context) ([]*Operation, error)
func (r *StorageRepo) ListEvents(ctx context.Context, operationID string) ([]*Event, error)

// --- Metadata ---

func (r *StorageRepo) GetMetadata(ctx context.Context, key string) (string, error)
func (r *StorageRepo) SetMetadata(ctx context.Context, key, value string) error

// --- Capabilities ---

func (r *StorageRepo) GetPoolCapabilities(ctx context.Context, poolID string) ([]string, error)
func (r *StorageRepo) HasCapability(ctx context.Context, poolID, capability string) (bool, error)
```

### 3.2 Filtros

```go
type OperationFilter struct {
    PoolID    *string
    Status    *OperationStatus
    Type      *OperationType
    Since     *time.Time
    Limit     int  // 0 = sin límite, default 50
}
```

### 3.3 Mutaciones (transaccionales, internas)

Estas mutaciones NO son llamadas directamente por handlers. Las invoca `StorageService` dentro de transacciones controladas.

```go
// --- Pools ---

func (r *StorageRepo) CreatePool(ctx context.Context, tx *sql.Tx, p *Pool) error
func (r *StorageRepo) UpdatePool(ctx context.Context, tx *sql.Tx, p *Pool) error
func (r *StorageRepo) DeletePool(ctx context.Context, tx *sql.Tx, id string) error
func (r *StorageRepo) RenamePool(ctx context.Context, tx *sql.Tx, id, newName string) error
func (r *StorageRepo) SetPoolControlState(ctx context.Context, tx *sql.Tx, id string, state ControlState) error

// --- Devices ---

func (r *StorageRepo) UpsertDevice(ctx context.Context, tx *sql.Tx, d *Device) error
func (r *StorageRepo) UpdateDeviceCurrentPath(ctx context.Context, tx *sql.Tx, id, newPath string) error
func (r *StorageRepo) UpdateDeviceLastSeen(ctx context.Context, tx *sql.Tx, id string, t time.Time) error
func (r *StorageRepo) DeleteDevice(ctx context.Context, tx *sql.Tx, id string) error

// --- Pool-Device assignments ---

func (r *StorageRepo) AssignDeviceToPool(ctx context.Context, tx *sql.Tx, poolID, deviceID string) error
func (r *StorageRepo) UnassignDeviceFromPool(ctx context.Context, tx *sql.Tx, poolID, deviceID string) error

// --- Operations ---

func (r *StorageRepo) CreateOperation(ctx context.Context, tx *sql.Tx, op *Operation) error
func (r *StorageRepo) UpdateOperationStatus(ctx context.Context, tx *sql.Tx, id string, status OperationStatus, errMsg, errCode *string) error
func (r *StorageRepo) UpdateOperationData(ctx context.Context, tx *sql.Tx, id string, data json.RawMessage) error
func (r *StorageRepo) AppendEvent(ctx context.Context, tx *sql.Tx, e *Event) error

// --- Capabilities ---

func (r *StorageRepo) SetPoolCapabilities(ctx context.Context, tx *sql.Tx, poolID string, caps []string) error

// --- Generation ---

func (r *StorageRepo) IncrementGlobalGeneration(ctx context.Context, tx *sql.Tx) (int64, error)
```

**Regla**: cualquier mutación incrementa el contador global y la `generation` de la entidad afectada. Este helper lo centraliza:

```go
// incrementGeneration es un helper interno que se llama al final de cada
// mutación. Actualiza la generation de la entidad Y el global_generation
// de storage_metadata.
func (r *StorageRepo) incrementGeneration(tx *sql.Tx, entityTable, entityID string) error
```

---

## 4. StorageService — Lógica de negocio + Commands

`StorageService` es la capa de comandos. Recibe peticiones HTTP/internas, valida con policy layer, ejecuta operaciones BTRFS, y persiste vía `StorageRepo`.

```go
type StorageService struct {
    repo      *StorageRepo
    policy    *PolicyChecker
    btrfs     BtrfsExecutor   // interfaz para ejecutar comandos btrfs (mockeable en tests)
    notifier  Notifier        // notifications.go
    logger    *slog.Logger
}

func NewStorageService(repo *StorageRepo, policy *PolicyChecker, btrfs BtrfsExecutor, notifier Notifier) *StorageService
```

### 4.1 Commands (mutaciones, devuelven *Operation)

Las mutaciones se modelan como operaciones. Devuelven la `Operation` creada (en estado `pending` o `in_progress`), no esperan a que termine.

```go
// CreatePool inicia la creación de un nuevo pool BTRFS.
// La operación corre en background. Polling vía GetOperation.
func (s *StorageService) CreatePool(ctx context.Context, spec PoolSpec) (*Operation, error)

// DestroyPool elimina un pool existente. Falla si hay shares/services activos.
func (s *StorageService) DestroyPool(ctx context.Context, poolID string) (*Operation, error)

// AddDeviceToPool añade un disco a un pool existente, online.
func (s *StorageService) AddDeviceToPool(ctx context.Context, poolID, deviceID string) (*Operation, error)

// RemoveDeviceFromPool quita un disco de un pool, online (rebalance automático).
// Falla si la operación dejaría el pool sin redundancia mínima.
func (s *StorageService) RemoveDeviceFromPool(ctx context.Context, poolID, deviceID string) (*Operation, error)

// ReplaceDevice reemplaza un disco por otro (mejor que add+remove).
func (s *StorageService) ReplaceDevice(ctx context.Context, poolID, oldDeviceID, newDeviceID string) (*Operation, error)

// ConvertProfile cambia el profile del pool (single→raid1, raid1→raid10, etc.).
func (s *StorageService) ConvertProfile(ctx context.Context, poolID string, newProfile Profile) (*Operation, error)

// StartScrub inicia un scrub del pool.
func (s *StorageService) StartScrub(ctx context.Context, poolID string) (*Operation, error)

// CreateSnapshot crea un snapshot de un subvolume del pool.
func (s *StorageService) CreateSnapshot(ctx context.Context, poolID, subvolume, snapshotName string) (*Operation, error)

// DeleteSnapshot elimina un snapshot.
func (s *StorageService) DeleteSnapshot(ctx context.Context, poolID, snapshotName string) (*Operation, error)

// CancelOperation intenta cancelar una operación en curso (si soporta cancelación).
func (s *StorageService) CancelOperation(ctx context.Context, opID string) (*Operation, error)
```

### 4.2 Queries (lecturas síncronas, devuelven entidades)

Las lecturas son delegaciones simples al repo, con posible enriquecimiento.

```go
func (s *StorageService) GetPool(ctx context.Context, idOrName string) (*Pool, error)
func (s *StorageService) ListPools(ctx context.Context) ([]*Pool, error)
func (s *StorageService) GetDevice(ctx context.Context, id string) (*Device, error)
func (s *StorageService) ListDevices(ctx context.Context) ([]*Device, error)
func (s *StorageService) ListAvailableDevices(ctx context.Context) ([]*Device, error)
func (s *StorageService) GetOperation(ctx context.Context, id string) (*Operation, error)
func (s *StorageService) ListOperations(ctx context.Context, filter OperationFilter) ([]*Operation, error)
func (s *StorageService) GetPoolHealth(ctx context.Context, poolID string) (*PoolHealth, error)
func (s *StorageService) GetScrubStatus(ctx context.Context, poolID string) (*ScrubStatus, error)
func (s *StorageService) GetBalanceStatus(ctx context.Context, poolID string) (*BalanceStatus, error)
```

### 4.3 Specs (parámetros de creación)

```go
// PoolSpec es el input de CreatePool.
type PoolSpec struct {
    Name      string    // requerido, regex [a-zA-Z0-9_-]{3,32}
    Profile   Profile   // requerido
    DeviceIDs []string  // requerido, len >= profile.MinDisks()
    Role      Role      // opcional, default RoleData
}

// Validate verifica la coherencia básica del spec. Validación de policy
// (capability, disponibilidad) se hace en CreatePool.
func (s PoolSpec) Validate() error
```

### 4.4 Boot-time entry points

Estas funciones se llaman desde `main.go` al arranque del daemon.

```go
// InitStorage inicializa el módulo de storage al arrancar el daemon.
// Realiza: schema migration, device scan, recovery de operaciones pendientes,
// detección de pools observed.
func InitStorage(ctx context.Context, db *sql.DB) (*StorageService, error)

// ScanAndRegisterDevices escanea /dev/disk/by-id/ y actualiza storage_devices.
// Mantiene current_path al día. Llamado al arranque y cada N minutos.
func (s *StorageService) ScanAndRegisterDevices(ctx context.Context) error

// RecoverPendingOperations busca operaciones en estado pending/in_progress
// y decide qué hacer (reintentar, marcar como failed, rollback).
//
// IMPORTANTE: NO basta con marcar como failed sin más. Antes de decidir,
// la función consulta a BTRFS si la operación física sigue corriendo:
//
//   - balance:  `btrfs balance status /mnt/pool` → si dice "Running" la op sigue viva
//   - replace:  `btrfs replace status /mnt/pool` → si dice "Started" la op sigue viva
//   - scrub:    `btrfs scrub status /mnt/pool` → idem
//
// Decisión por estado físico:
//
//   BTRFS dice "running" → la op sigue viva. Mantener en in_progress y
//                          monitorizar progreso. La UI muestra spinner.
//
//   BTRFS dice "stopped/finished" → la op terminó mientras el daemon estaba
//                                    caído. Verificar resultado (revisar logs,
//                                    consultar estado del pool) y marcar como
//                                    completed o failed según corresponda.
//
//   BTRFS dice "no operation" → la op murió. Marcar como failed con
//                                ErrorCode = "crashed_during_operation".
//                                Si la op tenía rollback Steps definidos
//                                (vía runSteps), intentar rollback.
//
// Esta lógica evita el problema de "operación fantasma": marcar como failed
// una op que BTRFS sigue ejecutando, lo que llevaría a iniciar una segunda
// op y obtener "operation already in progress" del kernel.
func (s *StorageService) RecoverPendingOperations(ctx context.Context) error

// ReconcileDevicesAtBoot empareja discos físicos vistos con la DB.
// Actualiza current_path. Marca devices missing si no aparecen.
//
// Matching por Serial primero (identidad absoluta). Si un device de la DB
// no aparece en el scan físico, se marca como missing pero NO se borra.
// Si un disco físico no está en la DB pero tiene serial conocido, se
// inserta con un placeholder ByIDPath y se loguea como "imported device".
func (s *StorageService) ReconcileDevicesAtBoot(ctx context.Context) error
```

---

## 5. PolicyChecker — Decisiones de autorización

Capa separada que decide si una operación está permitida sobre un pool dado. **Centraliza** toda la lógica de "puedo / no puedo".

```go
type PolicyChecker struct{}

func NewPolicyChecker() *PolicyChecker {
    return &PolicyChecker{}
}

// Allows devuelve true si el pool permite la operación.
// Es la versión simple de Beta 8. Beta 9 evolucionará a CheckPermission.
func (p *PolicyChecker) Allows(pool *Pool, op Operation) bool

// AllowsWithReason es una variante que devuelve también el código de error
// específico si no permite la operación. Para que el handler HTTP devuelva
// el código correcto al frontend.
func (p *PolicyChecker) AllowsWithReason(pool *Pool, op Operation) (bool, string)

// Helpers de mappeo
func capabilityFor(op OperationType) string
```

**Uso típico en un Command**:

```go
func (s *StorageService) AddDeviceToPool(ctx context.Context, poolID, deviceID string) (*Operation, error) {
    pool, err := s.repo.GetPool(ctx, poolID)
    if err != nil { return nil, err }

    if allowed, reason := s.policy.AllowsWithReason(pool, OpTypeAddDevice); !allowed {
        return nil, &PolicyError{Code: reason, Message: "Operation not permitted on this pool"}
    }
    // ... continuar con la operación
}
```

---

## 6. BtrfsExecutor — Interfaz para ejecutar comandos

Capa que ejecuta los comandos BTRFS reales. Definida como interfaz para que se pueda mockear en tests con discos loopback.

```go
type BtrfsExecutor interface {
    // Filesystem operations
    Create(ctx context.Context, devices []string, profile Profile, label string) (btrfsUUID string, err error)
    Show(ctx context.Context, mountPoint string) (*BtrfsInfo, error)
    Mount(ctx context.Context, device, mountPoint string, opts []string) error
    Unmount(ctx context.Context, mountPoint string) error

    // Device operations
    DeviceAdd(ctx context.Context, mountPoint, device string) error
    DeviceRemove(ctx context.Context, mountPoint, device string) error
    DeviceReplace(ctx context.Context, mountPoint, oldDevice, newDevice string) error

    // Balance / Convert
    BalanceStart(ctx context.Context, mountPoint string, opts BalanceOpts) error
    BalanceStatus(ctx context.Context, mountPoint string) (*BalanceStatus, error)
    BalancePause(ctx context.Context, mountPoint string) error
    BalanceResume(ctx context.Context, mountPoint string) error

    // Scrub
    ScrubStart(ctx context.Context, mountPoint string) error
    ScrubStatus(ctx context.Context, mountPoint string) (*ScrubStatus, error)

    // Subvolumes
    SubvolumeCreate(ctx context.Context, path string) error
    SubvolumeDelete(ctx context.Context, path string) error
    SubvolumeList(ctx context.Context, mountPoint string) ([]Subvolume, error)
    SnapshotCreate(ctx context.Context, source, dest string, readOnly bool) error

    // Usage — capacidad real con `btrfs filesystem usage --raw`
    //
    // IMPORTANTE: NO calcular capacidad manualmente (size_disco / num_discos
    // dividido por profile). Es engañoso cuando se mezclan discos de distinto
    // tamaño en mirror, o cuando hay datos ya escritos que afectan al espacio
    // realmente usable. Siempre delegar en btrfs.
    Usage(ctx context.Context, mountPoint string) (*BtrfsUsage, error)
}

// BtrfsUsage representa el output parseado de `btrfs filesystem usage --raw`.
// Contiene la cifra real de espacio usable, no una estimación.
type BtrfsUsage struct {
    DeviceSize       int64  // suma total de discos
    DeviceAllocated  int64  // ya reservado por BTRFS
    Used             int64  // ocupado por datos+metadata reales
    Free             int64  // disponible para escribir (cifra real, no /N)
    DataRatio        float64 // ratio según profile (1.0 single, 2.0 raid1, etc.)
    MetadataRatio    float64
    GlobalReserve    int64
}

// Implementación real basada en exec.Command
type btrfsCLI struct {
    logger *slog.Logger
}

func NewBtrfsCLI(logger *slog.Logger) BtrfsExecutor
```

---

## 7. Invariantes operacionales críticas

Reglas de implementación que **no son negociables** porque su violación causa pérdida de datos o estados corruptos.

### 7.1 Guardián estricto contra borrado accidental

**Regla**: ninguna función puede invocar `os.RemoveAll` (o equivalente) sobre un directorio que NimOS no ha creado.

**Implementación**: antes de borrar cualquier directorio bajo `/nimos/pools/` o relacionado, verificar la presencia del archivo `.nimos-pool.json` en el directorio raíz.

```go
// safeRemovePoolDir borra un directorio de pool solo si está claramente
// gestionado por NimOS (tiene el identity file).
// Devuelve error si el directorio no parece nuestro — NUNCA borrar a ciegas.
func safeRemovePoolDir(path string) error {
    identityPath := filepath.Join(path, ".nimos-pool.json")
    if _, err := os.Stat(identityPath); os.IsNotExist(err) {
        return fmt.Errorf("refusing to remove %s: no .nimos-pool.json found", path)
    }
    // Verificar también que no haya datos importantes inesperados
    entries, err := os.ReadDir(path)
    if err != nil {
        return err
    }
    if len(entries) > expectedMaxEntries {
        return fmt.Errorf("refusing to remove %s: unexpected content (%d entries)", path, len(entries))
    }
    return os.RemoveAll(path)
}
```

**Casos donde aplica**: `cleanOrphanPoolDirs`, destroy de pool, rollback de creación fallida.

### 7.2 Cálculo de capacidad delegado a BTRFS

**Regla**: el espacio libre/usable de un pool **siempre** se obtiene de `btrfs filesystem usage --raw`. NUNCA se calcula manualmente.

**Razón**: BTRFS gestiona el espacio en chunks heterogéneos. Mezclar discos de distinto tamaño en mirror produce capacidades que no son `size / 2`. El espacio reservado por metadata, global reserve, y allocaciones temporales afectan al espacio realmente escribible.

**Anti-pattern (PROHIBIDO)**:
```go
// MAL: cálculo manual engañoso
totalSize := sumDiskSizes(disks)
usable := totalSize / 2  // raid1
```

**Pattern correcto**:
```go
// BIEN: delegado en btrfs
usage, err := btrfs.Usage(ctx, pool.MountPoint)
if err != nil { return 0, err }
return usage.Free, nil
```

### 7.3 Identidad por Serial, ByIDPath como cache

**Regla**: el matching de devices entre reboots se hace por `Serial`. El `ByIDPath` se trata como cache que se puede actualizar.

**Algoritmo de reconciliación**:
```go
// Al escanear discos físicos:
for each diskFisico:
    if diskFisico.Serial == "" {
        log.Warn("disk without serial, skipping")
        continue
    }

    dev := repo.GetDeviceBySerial(diskFisico.Serial)
    if dev != nil {
        // Disco conocido. Actualizar ByIDPath y CurrentPath si cambiaron.
        if dev.ByIDPath != diskFisico.ByIDPath {
            log.Info("ByIDPath changed for serial %s: %s → %s",
                diskFisico.Serial, dev.ByIDPath, diskFisico.ByIDPath)
            repo.UpdateDeviceByIDPath(dev.ID, diskFisico.ByIDPath)
        }
        repo.UpdateDeviceCurrentPath(dev.ID, diskFisico.CurrentPath)
        repo.UpdateDeviceLastSeen(dev.ID, now)
    } else {
        // Disco nuevo. Crear con el serial como UNIQUE.
        repo.UpsertDevice(diskFisico)
    }
```

### 7.4 Recovery de operaciones consulta BTRFS antes de decidir

**Regla**: nunca marcar una operación como `failed` por timeout o por reinicio del daemon sin verificar primero si BTRFS sigue ejecutándola.

Documentado en `RecoverPendingOperations()` (§4.3).

### 7.5 PRAGMA foreign_keys = ON obligatorio

**Regla**: la conexión SQLite **siempre** se abre con foreign keys activadas. Sin esto, los CASCADE y RESTRICT del schema son decorativos.

```go
db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=10000&_foreign_keys=ON")
```

Verificar en tests:
```go
// Debe fallar
_, err := db.Exec(`INSERT INTO storage_pool_devices (pool_id, device_id, added_at) VALUES ('fake', 'fake', ?)`, now)
assert.Error(t, err, "FK should have rejected this")
```

### 7.6 Containment estricto del código legacy

**Regla**: todo el código de compatibilidad hacia atrás vive **exclusivamente** en `storage_legacy.go`. No filtra a ningún otro archivo.

**Razón**: el patrón anti-pattern más común en refactors con backward compatibility es que la "capa temporal" se acaba metiendo en sitios nuevos. Cuando eso pasa, el modelo viejo persiste indefinidamente y el refactor pierde valor.

**Reglas duras**:
1. `storage_legacy.go` es el único archivo donde aparecen las funciones de compatibilidad
2. Código nuevo (`storage_service.go`, `storage_repo.go`, `storage_btrfs.go`, etc.) **nunca** llama a funciones legacy
3. Funciones legacy solo llaman a `service` o `repo`, nunca entre sí
4. Cada función legacy lleva comentario explícito de consumers y replacement plan

Detalle completo en §8.1.

---

## 8. Backward compatibility — Funciones legacy

El resto del daemon (`docker.go`, `shares.go`, `services.go`) llama a estas funciones por nombre. **Mantienen la firma** para no requerir cambios en otros módulos. Internamente delegan al nuevo `StorageService`.

### 8.1 Regla de containment estricto

**Toda la lógica legacy vive en un único archivo: `storage_legacy.go`**.

Esta regla NO es una sugerencia. Es **una invariante de proyecto** porque la deuda técnica más persistente nace de "adaptadores temporales que se vuelven permanentes". Para evitarlo:

1. **Una sola puerta**: `storage_legacy.go` es el único archivo donde puede aparecer código que conecta el modelo viejo con el nuevo.

2. **Las funciones legacy NUNCA llaman a otras funciones legacy**. Solo llaman al `StorageService` o `StorageRepo`. Si una función legacy necesita ayuda de otra, la ayuda se implementa en el service/repo, no como helper legacy.

3. **El nuevo código NUNCA llama a funciones legacy**. Solo el código antiguo (`docker.go`, `shares.go`, `services.go`) las consume. Si en `storage_service.go` o `storage_repo.go` aparece una llamada a `getStorageConfigFull()`, **es un bug**.

4. **Cada función legacy lleva un comentario explícito** indicando quién la llama y cuándo se eliminará:

```go
// LEGACY (TO REMOVE IN BETA 9)
// Consumers: docker.go (listContainers), shares.go (resolveSharePath)
// Replacement: call service.GetStorageConfig() directly from caller
//
// Devuelve la config como map (mismo shape que el viejo storage.json)
// para que los consumers no necesiten cambios en Beta 8.
//
// NO LLAMAR DESDE NUEVO CÓDIGO. Solo existe por compatibilidad.
func getStorageConfigFull() map[string]interface{}
```

5. **Build tag o linter para prevenir filtración**: opcionalmente, se puede añadir una build tag o un lint rule que prohíba importar `storage_legacy.go` desde archivos no permitidos. Beta 8 puede dejar esto como TODO; Beta 9 lo formaliza.

### 8.2 Lista de funciones legacy

```go
// LEGACY (TO REMOVE IN BETA 9)
// Consumers: docker.go, shares.go, services.go
// Replacement: service.GetStorageConfig() / service.GetPool(name)
func getStorageConfigFull() map[string]interface{}

// LEGACY (TO REMOVE IN BETA 9)
// Consumers: shares.go
// Replacement: service.UpdatePool() / service.UpdateMetadata()
func saveStorageConfigFull(config map[string]interface{}) error

// LEGACY (TO REMOVE IN BETA 9)
// Consumers: docker.go
// Replacement: repo.HasAnyPool(ctx)
func hasPoolGo() bool

// LEGACY (TO REMOVE IN BETA 9)
// Consumers: shares.go, services.go
// Replacement: service.ListPools(ctx) y construir el map en el caller si hace falta
func getStoragePoolsGo() []map[string]interface{}

// LEGACY (TO REMOVE IN BETA 9)
// Consumers: handlers HTTP antiguos del frontend (mientras el frontend no migre a REST nuevo)
// Replacement: GET /api/storage/devices (REST nuevo)
func detectStorageDisksGo() map[string]interface{}
```

### 8.3 Plan de eliminación (Beta 9)

En Beta 9, el flujo de eliminación de cada función legacy es:

1. Marcar la función con `// Deprecated:` en el comentario
2. Identificar cada caller (con `grep -r`)
3. Migrar el caller a usar `StorageService` directamente
4. Verificar que ningún caller queda
5. Eliminar la función legacy
6. Repetir hasta vaciar `storage_legacy.go`
7. Cuando el archivo quede vacío, **eliminarlo**

**Criterio de Beta 9 completada**: `storage_legacy.go` no existe.

---

## 9. Contexto y cancelación

**Todas las funciones aceptan `context.Context`** como primer parámetro. Esto permite:
- Cancelar operaciones largas si el daemon se reinicia
- Propagar deadlines de timeouts HTTP
- Trazabilidad con request IDs

```go
// Ejemplo de uso desde un handler HTTP
ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
defer cancel()

pool, err := service.GetPool(ctx, poolID)
```

Para operaciones de background (BalanceStatus polling, ScrubStatus monitoring) se usa un context derivado del context del daemon, cancelable al shutdown.

---

## 10. Estructura de archivos resultante

Después del refactor, los archivos del módulo storage quedan así:

```
daemon/
├── storage_types.go          (~200 líneas) — tipos del dominio (Pool, Device, Operation, etc.)
├── storage_repo.go           (~400 líneas) — StorageRepo, queries y mutaciones SQLite
├── storage_service.go        (~400 líneas) — StorageService, commands con Operations
├── storage_policy.go         (~150 líneas) — PolicyChecker, lógica de autorización
├── storage_btrfs.go          (~300 líneas) — BtrfsExecutor + btrfsCLI
├── storage_boot.go           (~150 líneas) — InitStorage, recovery, device scan
├── storage_health.go         (~700 líneas) — CONSERVADO de Beta 7 con limpieza ZFS
├── storage_wipe.go           (~500 líneas) — CONSERVADO de Beta 7 con conexión a DB
├── storage_common.go         (~200 líneas) — CONSERVADO con fixes
├── storage_legacy.go         (~100 líneas) — funciones LEGACY para compatibilidad
└── storage_http.go           (~300 líneas) — handlers HTTP (nuevos endpoints REST)

Total estimado: ~3400 líneas
```

**Nota**: la estimación sube a ~3400 líneas (no 1500) cuando contamos `storage_health.go` (700) y `storage_wipe.go` (500) que se conservan completos. La reducción real es del **40%** (5627 → 3400), no del 73% como decía el plan. Esto es honesto: el módulo health y wipe son grandes pero buenos, y no tiene sentido tocarlos.

El número que sí se reduce drásticamente es **funciones nuevas a escribir**: pasamos de "reescribir 102 funciones" a "escribir ~30 nuevas + mantener las buenas".

---

## 11. Próximos documentos a generar

Después de validar este documento:

1. `storage_http_api.md` — endpoints REST, request/response examples
2. `schema.sql` — script SQL ejecutable y testeado
3. `storage_state_machines.md` — diagramas de lifecycle
4. `storage_invariants.md` — las 5 invariantes referenciables desde código

---

*Documento generado en Fase 1 del refactor Beta 8.*
