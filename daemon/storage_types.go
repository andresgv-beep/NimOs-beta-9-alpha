// storage_types.go — Tipos del dominio del módulo de storage refactorizado (Beta 8).
//
// Este archivo define las entidades (Pool, Device, Operation, Event) y los
// tipos auxiliares (Profile, Role, ControlState, OperationType, etc.) que
// usan storage_repo.go, storage_service.go y storage_policy.go.
//
// Invariantes referenciables: see docs/storage_invariants.md
// Lifecycles:                 see docs/storage_state_machines.md
// API HTTP:                   see docs/storage_http_api.md

package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Pool
// ─────────────────────────────────────────────────────────────────────────────

// Pool representa un filesystem BTRFS gestionado u observado por NimOS.
// El ID interno es estable; el Name puede cambiar sin romper foreign keys.
type Pool struct {
	ID            string       `json:"id"`             // UUID interno, estable, nunca cambia
	Name          string       `json:"name"`           // Nombre legible, único, puede cambiar (rename)
	BtrfsUUID     string       `json:"btrfs_uuid"`     // UUID del filesystem (de blkid)
	Profile       Profile      `json:"profile"`        // single | raid1 | raid1c3 | raid10
	MountPoint    string       `json:"mount_point"`    // /nimos/pools/<name>
	Role          Role         `json:"role"`           // Beta 8 siempre "data". Consumers futuros.
	Compression   string       `json:"compression"`    // none | lzo | zstd:1..15
	ControlState  ControlState `json:"control_state"`  // managed | observed (Beta 8)
	DiscoveredAt  *time.Time   `json:"discovered_at"`  // primera vez que se vio, nullable
	CreatedAt     time.Time    `json:"created_at"`
	Generation    int64        `json:"generation"`     // incrementa en cada mutación

	// Campos cargados bajo demanda (no siempre presentes)
	Capabilities []string `json:"capabilities,omitempty"` // ["snapshots", "balance", "replace_device", ...]
	Devices      []Device `json:"devices,omitempty"`      // miembros del pool

	// Campos enriquecidos por ListPools (no almacenados en DB)
	Usage     *PoolUsage  `json:"usage,omitempty"`      // Capacidad runtime (de btrfs filesystem usage)
	Health    *PoolHealth `json:"health,omitempty"`     // Estado de salud computado
	IsPrimary bool        `json:"is_primary"`           // true si es el primary_pool
	Mounted   bool        `json:"mounted"`              // true si está montado en MountPoint

	// Conteo REAL de devices según el kernel (observer btrfs), NO la BD. Permite
	// a la UI no mentir cuando el filesystem tiene discos que la BD no conoce
	// (p.ej. un device añadido por CLI fuera de NimOS) o más ausentes de los
	// registrados. 0 = sin datos del observer (omitempty). Solo-lectura: NO muta
	// la lista Devices ni la BD (alineación de membresía, reality-wins).
	KernelDevicesExpected int `json:"kernel_devices_expected,omitempty"`
	KernelDevicesOnline   int `json:"kernel_devices_online,omitempty"`
	KernelDevicesMissing  int `json:"kernel_devices_missing,omitempty"`
}

// PoolUsage representa el uso de capacidad de un pool en runtime.
// Se calcula a partir de `btrfs filesystem usage` (correcto para RAID asimétrico).
//
// IMPORTANTE: TotalBytes = UsedBytes + AvailableBytes (capacidad usable real),
// NO el tamaño bruto de los discos. En RAID1 con discos asimétricos esto
// refleja la capacidad real, no la cálculo ingenuo (suma/2).
type PoolUsage struct {
	TotalBytes     int64 `json:"total_bytes"`
	UsedBytes      int64 `json:"used_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
	UsagePercent   int   `json:"usage_percent"` // 0..100
}

// Profile representa la disposición de datos en BTRFS.
type Profile string

const (
	ProfileSingle  Profile = "single"
	ProfileRaid1   Profile = "raid1"
	ProfileRaid1c3 Profile = "raid1c3"
	ProfileRaid10  Profile = "raid10"
)

// MinDisks devuelve el número mínimo de discos requeridos para el profile.
func (p Profile) MinDisks() int {
	switch p {
	case ProfileSingle:
		return 1
	case ProfileRaid1:
		return 2
	case ProfileRaid1c3:
		return 3
	case ProfileRaid10:
		return 4
	}
	return 0
}

// IsValid devuelve true si el profile es uno de los soportados.
func (p Profile) IsValid() bool {
	switch p {
	case ProfileSingle, ProfileRaid1, ProfileRaid1c3, ProfileRaid10:
		return true
	}
	return false
}

// Role indica la función del pool dentro del sistema.
// En Beta 8 solo se usa RoleData en runtime. Los demás están reservados
// para consumidores planeados (NimBackup, cache tiering).
type Role string

const (
	RoleData   Role = "data"   // Beta 8 default
	RoleBackup Role = "backup" // TODO(beta9): consumido por NimBackup
	RoleCache  Role = "cache"  // TODO(beta10): cache tier SSD
	RoleSystem Role = "system" // TODO(future): pool del SO
)

// ControlState indica el grado de autoridad de NimOS sobre el pool.
// Ver docs/storage_state_machines.md §3 para diagrama de transiciones.
type ControlState string

const (
	ControlStateManaged  ControlState = "managed"  // Beta 8: dueño completo
	ControlStateObserved ControlState = "observed" // Beta 8: solo lectura
	ControlStateImported ControlState = "imported" // TODO(beta9): pendiente adopción
	ControlStateForeign  ControlState = "foreign"  // TODO(beta9): filesystem no entendido
	ControlStateRecovery ControlState = "recovery" // TODO(beta9): reconciliación
)

// Helpers de Pool (centralizan lógica para no dispersarla por handlers)

// IsManaged devuelve true si NimOS tiene autoridad completa sobre este pool.
func (p *Pool) IsManaged() bool { return p.ControlState == ControlStateManaged }

// IsObserved devuelve true si NimOS solo observa el pool sin gestionarlo.
func (p *Pool) IsObserved() bool { return p.ControlState == ControlStateObserved }

// HasCapability devuelve true si el pool soporta una capability concreta.
func (p *Pool) HasCapability(cap string) bool {
	for _, c := range p.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Device
// ─────────────────────────────────────────────────────────────────────────────

// Device representa un disco físico conocido por el sistema.
//
// JERARQUÍA DE IDENTIDAD (de más fuerte a más débil):
//
//  1. Serial — IDENTIDAD ABSOLUTA. Grabado en firmware, no cambia.
//     UNIQUE NOT NULL en el schema. Si un disco no expone serial, no se gestiona.
//  2. ByIDPath — IDENTIDAD ESTABLE. Construido de model+serial. Puede variar
//     entre controladoras SATA o tras kernel updates.
//  3. CurrentPath — CACHE RUNTIME. /dev/sdb cambia entre reboots según orden
//     de detección. NO es identidad nunca.
//
// see docs/storage_invariants.md#3
type Device struct {
	ID          string    `json:"id"`              // UUID interno
	Serial      string    `json:"serial"`          // IDENTIDAD ABSOLUTA
	ByIDPath    string    `json:"by_id_path"`      // /dev/disk/by-id/ata-...
	CurrentPath string    `json:"current_path"`    // /dev/sdb (cache, cambia entre reboots)
	WWN         string    `json:"wwn,omitempty"`   // identificador adicional, puede ser vacío
	Model       string    `json:"model"`
	SizeBytes   int64     `json:"size_bytes"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Generation  int64     `json:"generation"`

	// Computed (no en DB)
	InPool      *string `json:"in_pool,omitempty"`      // pool_id si está asignado, nil si libre
	Available   bool    `json:"available"`              // true si está libre y elegible para nuevo pool
	SmartStatus string  `json:"smart_status,omitempty"` // ok | warning | critical | unknown (runtime, de smartctl cache)
}

// ─────────────────────────────────────────────────────────────────────────────
// Operation
// ─────────────────────────────────────────────────────────────────────────────

// Operation representa una operación de storage en curso o histórica.
// TODA mutación genera una Operation, sea sync (rename, set_compression) o
// async (create_pool, replace_device). Esto mantiene un timeline consistente
// y auditoría completa.
//
// see docs/storage_state_machines.md §4
type Operation struct {
	ID          string          `json:"id"`                     // UUID
	Type        OperationType   `json:"type"`                   // create_pool | rename_pool | ...
	PoolID      *string         `json:"pool_id,omitempty"`      // nullable
	Status      OperationStatus `json:"status"`                 // pending | in_progress | completed | failed | rolled_back | cancelled
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       *string         `json:"error,omitempty"`        // mensaje libre del error si falló
	ErrorCode   *string         `json:"error_code,omitempty"`   // código semántico (ErrCode*) si falló
	Data        json.RawMessage `json:"data,omitempty"`         // payload temporal (parámetros + progreso)

	// Eventos cargados bajo demanda
	Events []Event `json:"events,omitempty"`
}

// OperationType identifica la operación concreta.
type OperationType string

const (
	// Sync ops (metadata mutations)
	OpTypeRenamePool      OperationType = "rename_pool"
	OpTypeChangeRole      OperationType = "change_role"
	OpTypeSetCompression  OperationType = "set_compression"
	OpTypeSetScrubPolicy  OperationType = "set_scrub_policy"
	OpTypeControlChange   OperationType = "control_state_change"
	OpTypeBalancePause    OperationType = "balance_pause"
	OpTypeBalanceResume   OperationType = "balance_resume"

	// Async ops (long-running)
	OpTypeCreatePool     OperationType = "create_pool"
	OpTypeDestroyPool    OperationType = "destroy_pool"
	OpTypeAddDevice      OperationType = "add_device"
	OpTypeRemoveDevice   OperationType = "remove_device"
	OpTypeReplaceDevice  OperationType = "replace_device"
	OpTypeConvertProfile OperationType = "convert_profile"
	OpTypeStartScrub     OperationType = "start_scrub"
	OpTypeCreateSnapshot OperationType = "create_snapshot"
	OpTypeDeleteSnapshot OperationType = "delete_snapshot"
	OpTypeImportPool     OperationType = "import_pool"
)

// OperationStatus es el estado de la operación.
type OperationStatus string

const (
	OpStatusPending    OperationStatus = "pending"
	OpStatusInProgress OperationStatus = "in_progress"
	OpStatusCompleted  OperationStatus = "completed"
	OpStatusFailed     OperationStatus = "failed"
	OpStatusRolledBack OperationStatus = "rolled_back"
	OpStatusCancelled  OperationStatus = "cancelled"
)

// OperationMode indica si la operación se completa dentro de la petición
// HTTP (sync) o si requiere polling posterior (async).
type OperationMode string

const (
	OperationModeSync  OperationMode = "sync"
	OperationModeAsync OperationMode = "async"
)

// operationModeMap es la verdad sobre qué operaciones son sync o async.
// NO se puede cambiar por handler. Si necesitas cambiar el modo, edita
// esta tabla y todo el código se ajusta automáticamente.
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

// Mode devuelve el modo (sync/async) de este tipo de operación.
// Safe default: async para tipos no mapeados.
func (op OperationType) Mode() OperationMode {
	if mode, ok := operationModeMap[op]; ok {
		return mode
	}
	return OperationModeAsync
}

// IsTerminal devuelve true si el estado es final (no se puede transicionar).
func (s OperationStatus) IsTerminal() bool {
	switch s {
	case OpStatusCompleted, OpStatusFailed, OpStatusRolledBack, OpStatusCancelled:
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Event
// ─────────────────────────────────────────────────────────────────────────────

// Event representa un suceso dentro de una Operation.
// Permite reconstruir el timeline detallado de qué pasó durante la operación.
type Event struct {
	ID          string     `json:"id"`
	OperationID string     `json:"operation_id"`
	Timestamp   time.Time  `json:"timestamp"`
	Level       EventLevel `json:"level"`
	Message     string     `json:"message"`
}

// EventLevel es el nivel de severidad del evento.
type EventLevel string

const (
	EventLevelDebug EventLevel = "debug"
	EventLevelInfo  EventLevel = "info"
	EventLevelWarn  EventLevel = "warn"
	EventLevelError EventLevel = "error"
)

// ─────────────────────────────────────────────────────────────────────────────
// Códigos de error semánticos
// ─────────────────────────────────────────────────────────────────────────────

// Constantes string para el campo ErrorCode de Operation y respuestas HTTP.
// El frontend puede reaccionar al código sin parsear el mensaje libre.
// see docs/storage_http_api.md §5 para la lista completa con códigos HTTP.
const (
	ErrCodePoolNotFound          = "pool_not_found"
	ErrCodePoolNameTaken         = "pool_name_taken"
	ErrCodePoolObserved          = "pool_observed"
	ErrCodePoolRecovery          = "pool_recovery" // STOR-01-B: pool en recovery, solo ops de salida
	ErrCodeCapabilityMissing     = "capability_missing"
	ErrCodeOperationInProgress   = "operation_in_progress"
	ErrCodeOperationNotFound     = "operation_not_found"
	ErrCodeOperationNotCancellable = "operation_not_cancellable"
	ErrCodeDeviceNotFound        = "device_not_found"
	ErrCodeDeviceInUse           = "device_in_use"
	ErrCodeDeviceMissing         = "device_missing"
	ErrCodeDeviceNotEligible     = "device_not_eligible"
	ErrCodeProfileInvalid        = "profile_invalid"
	ErrCodeInsufficientDisks     = "insufficient_disks"
	ErrCodeMinDisksReached       = "min_disks_reached"
	ErrCodeServicesActive        = "services_active"
	ErrCodeMountFailed           = "mount_failed"
	ErrCodeUnmountFailed         = "unmount_failed"
	ErrCodeBtrfsCommandFailed    = "btrfs_command_failed"
	ErrCodeTransitionNotPermitted = "transition_not_permitted"
	ErrCodeCrashedDuringOperation = "crashed_during_operation"
	ErrCodeBadRequest            = "bad_request"
	ErrCodeInternal              = "internal"

	// ─── Recovery (Fase 4) ───────────────────────────────────────────────
	// Se aplica a operations marcadas failed por el recovery al arranque,
	// cuando no se puede determinar con certeza si la op se completó o no.
	// El usuario debe revisar manualmente.
	ErrCodeRecoveryInconclusive = "recovery_inconclusive"
	// La op estaba in_progress cuando el daemon murió, BTRFS confirma que
	// el efecto deseado NO se aplicó (filesystem no existe, device no
	// añadido, etc.). Marcada failed sin ambigüedad.
	ErrCodeRecoveryRolledBack = "recovery_rolled_back"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers compartidos del módulo
// ─────────────────────────────────────────────────────────────────────────────

// newUUID devuelve un UUID v4 para usar como ID de entidades.
// Wrapper sobre google/uuid para no esparcir el import por todo el código.
func newUUID() string {
	return uuid.NewString()
}

// rawJSON serializa cualquier valor a json.RawMessage. Si la serialización
// falla (raro con tipos simples), devuelve un JSON con error legible.
// Usado para el campo Operation.Data.
func rawJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Fallback: nunca devolver Data inválido
		return json.RawMessage(fmt.Sprintf(`{"_error":"marshal failed: %s"}`, err))
	}
	return json.RawMessage(b)
}
