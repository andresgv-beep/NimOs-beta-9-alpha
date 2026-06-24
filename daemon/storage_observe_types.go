package main

// storage_observe_types.go — Tipos del Storage Observer.
//
// Diseño completo en docs/storage_observer_design.md.
//
// Resumen rápido:
//   · Managed State (SQLite) vs Observed State (runtime cache)
//   · ObservedSnapshot inmutable, versionada por Generation
//   · Lecturas vía atomic.Pointer (lock-free)
//   · Divergence analysis pre-computada al hacer snapshot

import "time"

// ObservedSnapshot es la foto del observed state en un instante.
// Inmutable una vez creada — la UI/handlers la leen vía atomic.Load
// y no deben mutar campos.
type ObservedSnapshot struct {
	// Generation es un contador monotónico que aumenta cada vez que
	// el snapshot cambia su contenido (no cada scan: solo si hay diff).
	Generation uint64 `json:"generation"`

	// Timestamp del momento del scan.
	Timestamp time.Time `json:"timestamp"`

	// Filesystems BTRFS detectados en el sistema.
	// Cada uno puede ser Managed (cubierto por pool SQLite) o no.
	Filesystems []ObservedBtrfs `json:"filesystems"`

	// LooseDevices son discos sin filesystem útil (sin BTRFS, sin partición
	// de sistema, no en uso). Es decir, candidatos para ser usados en un pool.
	LooseDevices []ObservedDevice `json:"loose_devices"`

	// Divergences pre-computadas en el momento del snapshot.
	// La UI puede usar esto directamente sin reanalizar.
	Divergences []Divergence `json:"divergences"`

	// Métricas internas (útiles para debug + dashboard NIMA futuro).
	ScanDurationMs  int64    `json:"scan_duration_ms"`
	FingerprintHash [32]byte `json:"-"` // no exponer en JSON
}

// ObservedBtrfs es un filesystem BTRFS detectado en el sistema.
// Puede o no estar gestionado por NimOS (Managed=true).
type ObservedBtrfs struct {
	// Identidad
	UUID  string `json:"uuid"`
	Label string `json:"label,omitempty"`

	// Profile real del filesystem (data + metadata)
	Profile     string `json:"profile"`      // single/raid1/raid10/...
	MetaProfile string `json:"meta_profile"` // suele coincidir con Profile

	// Devices que componen este filesystem
	Devices         []ObservedDevice `json:"devices"`
	DevicesExpected int              `json:"devices_expected"`
	DevicesOnline   int              `json:"devices_online"`
	DevicesMissing  int              `json:"devices_missing"`

	// Mount status
	IsMounted     bool   `json:"is_mounted"`
	MountPoint    string `json:"mount_point,omitempty"`
	HasMountPoint bool   `json:"has_mount_point"` // heurística: ¿esperaríamos que monte?

	// Capacidad real (del kernel via statfs, no estimated)
	SizeBytes int64 `json:"size_bytes"`
	UsedBytes int64 `json:"used_bytes"`
	FreeBytes int64 `json:"free_bytes"`

	// Errores agregados de todos los devices del filesystem
	IOErrorCount int64 `json:"io_error_count"`

	// Cruce con managed state
	IsManaged       bool   `json:"is_managed"`
	ManagedPoolID   string `json:"managed_pool_id,omitempty"`
	ManagedPoolName string `json:"managed_pool_name,omitempty"`

	// Estado computado (uno de: healthy/incomplete/degraded/partial/unknown)
	ObservationHealth HealthStatus `json:"observation_health"`

	// Diagnóstico
	CanProbe bool      `json:"can_probe"` // true si todos los comandos respondieron
	LastSeen time.Time `json:"last_seen"`
}

// ObservedDevice es un disco físico (o miembro de un FS).
type ObservedDevice struct {
	Path      string `json:"path"`                 // /dev/sda
	ByIDPath  string `json:"by_id_path,omitempty"` // /dev/disk/by-id/...
	Serial    string `json:"serial,omitempty"`
	Model     string `json:"model,omitempty"`
	SizeBytes int64  `json:"size_bytes"`

	// InFS es el UUID del FS al que pertenece, si pertenece a alguno.
	// Empty = disco "loose".
	InFS string `json:"in_fs,omitempty"`

	IOErrors int64 `json:"io_errors"`
	Present  bool  `json:"present"` // visible físicamente ahora
}

// Divergence representa una diferencia entre managed y observed.
// Tipos posibles:
//   · pool_missing_device   — pool managed con N devices, observed ve N-k
//   · orphan_filesystem     — BTRFS observed sin pool managed que lo cubra
//   · unexpected_io_errors  — observed reporta errors en devices managed
//   · pool_unmounted        — pool managed con mount_point pero no mounted
//   · profile_mismatch      — profile declarado != profile real (raro)
type Divergence struct {
	Type     string `json:"type"`
	Severity string `json:"severity"` // info / warning / critical

	// Si la divergencia afecta a un pool managed, su id y name
	PoolID   string `json:"pool_id,omitempty"`
	PoolName string `json:"pool_name,omitempty"`

	// Si afecta a un filesystem observed específico
	FSUUID string `json:"fs_uuid,omitempty"`

	Detail string `json:"detail"`         // mensaje legible
	Hint   string `json:"hint,omitempty"` // sugerencia de acción
}

// ObservationHealth values: las constantes HealthHealthy, HealthDegraded,
// HealthFailed, HealthPartial, HealthIncomplete, HealthUnknown, HealthStale
// son globales y viven en nimos_health.go (tipo HealthStatus).

// Divergence severities
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Divergence types
const (
	DivPoolMissingDevice   = "pool_missing_device"
	DivOrphanFilesystem    = "orphan_filesystem"
	DivUnexpectedIOErrors  = "unexpected_io_errors"
	DivPoolUnmounted       = "pool_unmounted"
	DivProfileMismatch     = "profile_mismatch"
)
