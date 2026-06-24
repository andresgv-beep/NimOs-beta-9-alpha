package main

import "strings"

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Models — Typed structs for database entities
//
// Each struct has a ToMap() method for backward compatibility with code
// that still uses map[string]interface{}. Migrate consumers one by one,
// then remove ToMap() when no longer needed.
// ═══════════════════════════════════════════════════════════════════════════════

// ─── User ────────────────────────────────────────────────────────────────────

type DBUser struct {
	Username    string
	Password    string
	Role        string
	Description string
	TotpSecret  string
	TotpEnabled bool
	BackupCodes []interface{}
	CreatedAt   string
	UpdatedAt   string
}

func (u DBUser) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"username":    u.Username,
		"password":    u.Password,
		"role":        u.Role,
		"description": u.Description,
		"totpSecret":  u.TotpSecret,
		"totpEnabled": u.TotpEnabled,
		"created":     u.CreatedAt,
	}
	if u.BackupCodes != nil {
		m["backupCodes"] = u.BackupCodes
	}
	return m
}

// DBUserSummary is the lightweight version returned by list operations
type DBUserSummary struct {
	Username    string
	Role        string
	Description string
	TotpEnabled bool
	CreatedAt   string
}

func (u DBUserSummary) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"username":    u.Username,
		"role":        u.Role,
		"description": u.Description,
		"totpEnabled": u.TotpEnabled,
		"created":     u.CreatedAt,
	}
}

// ─── Session ─────────────────────────────────────────────────────────────────

type DBSession struct {
	Username  string
	Role      string
	CreatedAt int64
	ExpiresAt int64
	IP        string
}

func (s DBSession) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"username": s.Username,
		"role":     s.Role,
		"created":  s.CreatedAt,
		"expires":  s.ExpiresAt,
		"ip":       s.IP,
	}
}

// ─── Share ────────────────────────────────────────────────────────────────────

type DBShare struct {
	Name           string
	DisplayName    string
	Description    string
	Path           string
	Volume         string
	Pool           string
	RecycleBin     bool
	CreatedBy      string
	CreatedAt      string
	Permissions    map[string]string
	AppPermissions []AppPermission
}

type AppPermission struct {
	AppId      string
	Uid        int
	Permission string
}

func (s DBShare) ToMap() map[string]interface{} {
	appPerms := make([]map[string]interface{}, 0, len(s.AppPermissions))
	for _, ap := range s.AppPermissions {
		appPerms = append(appPerms, map[string]interface{}{
			"appId":      ap.AppId,
			"uid":        ap.Uid,
			"permission": ap.Permission,
		})
	}

	return map[string]interface{}{
		"name":           s.Name,
		"displayName":    s.DisplayName,
		"description":    s.Description,
		"path":           s.Path,
		"volume":         s.Volume,
		"pool":           s.Pool,
		"recycleBin":     s.RecycleBin,
		"createdBy":      s.CreatedBy,
		"created":        s.CreatedAt,
		"permissions":    s.Permissions,
		"appPermissions": appPerms,
	}
}

// ShareView is the enriched version of DBShare with runtime data from the filesystem.
// Built by buildShareViews() — never mutated after construction.
type ShareView struct {
	DBShare
	PoolType   string
	MountPoint string
	Quota      int64
	Used       int64
	Available  int64
	FileStats  map[string]int64
}

func (v ShareView) ToMap() map[string]interface{} {
	m := v.DBShare.ToMap()
	m["poolType"] = v.PoolType
	m["mountPoint"] = v.MountPoint
	m["quota"] = v.Quota
	m["used"] = v.Used
	m["available"] = v.Available
	m["fileStats"] = v.FileStats
	return m
}

// ─── App Access Grant ────────────────────────────────────────────────────────

type DBAppGrant struct {
	Username   string
	AppId      string
	Permission string
	GrantedBy  string
	GrantedAt  string
}

func (g DBAppGrant) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"username":   g.Username,
		"appId":      g.AppId,
		"permission": g.Permission,
		"grantedBy":  g.GrantedBy,
		"grantedAt":  g.GrantedAt,
	}
}

// ─── App Registry ────────────────────────────────────────────────────────────

type DBAppRegistryEntry struct {
	Id        string
	Name      string
	Category  string
	AdminOnly bool
	Public    bool
}

func (a DBAppRegistryEntry) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":        a.Id,
		"name":      a.Name,
		"category":  a.Category,
		"adminOnly": a.AdminOnly,
		"public":    a.Public,
	}
}

// ─── Update Params (optional fields via pointers) ────────────────────────────

// UserUpdate holds optional fields for updating a user.
// nil fields are not updated.
type UserUpdate struct {
	Password    *string
	Role        *string
	Description *string
	TotpSecret  *string
	TotpEnabled *bool
	BackupCodes interface{} // []interface{} or nil — serialized as JSON
}

// ShareUpdate holds optional fields for updating a share.
type ShareUpdate struct {
	Description *string
	RecycleBin  *bool
}

// strPtr returns a pointer to a string (helper for building updates)
func strPtr(s string) *string { return &s }

// boolPtr returns a pointer to a bool
func boolPtr(b bool) *bool { return &b }

// ─── Resolved Share (local + remote unified) ─────────────────────────────────

// RemoteInfo holds metadata for a remotely mounted share.
type RemoteInfo struct {
	Host       string
	DeviceName string
}

// ResolvedShare is the unified type returned by resolveShare.
// Local shares have Local set (with Permissions). Remote shares have Remote set.
type ResolvedShare struct {
	Name        string
	DisplayName string
	Path        string
	Pool        string
	Remote      *RemoteInfo       // nil if local
	Permissions map[string]string // nil if remote
}

// IsRemote returns true if this is a remotely mounted share.
func (s *ResolvedShare) IsRemote() bool {
	return s.Remote != nil
}

// ─── Service Registry ────────────────────────────────────────────────────────

type ServiceInstance struct {
	ID        string
	AppID     string
	PoolName  string
	Path      string
	Status    string // running, stopped, starting, stopping, failed, error, unknown
	Health    string // healthy, degraded, unreachable, unknown
	Owner     string // system, user
	Config    string // JSON
	CreatedAt string
	UpdatedAt string
}

func (s ServiceInstance) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":        s.ID,
		"appId":     s.AppID,
		"poolName":  s.PoolName,
		"path":      s.Path,
		"status":    s.Status,
		"health":    s.Health,
		"owner":     s.Owner,
		"config":    s.Config,
		"createdAt": s.CreatedAt,
		"updatedAt": s.UpdatedAt,
	}
}

type ServiceDependency struct {
	InstanceID string
	DepType    string // pool, share, path
	Target     string
	Required   string // required, soft, optional
}

func (d ServiceDependency) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"instanceId": d.InstanceID,
		"depType":    d.DepType,
		"target":     d.Target,
		"required":   d.Required,
	}
}

// PoolDependencyInfo is the enriched view for the destroy pool check
type PoolDependencyInfo struct {
	InstanceID string
	AppID      string
	AppName    string
	Status     string
	Health     string
	Required   string
}

func (d PoolDependencyInfo) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":       d.InstanceID,
		"appId":    d.AppID,
		"app":      d.AppName,
		"status":   d.Status,
		"health":   d.Health,
		"required": d.Required,
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Storage Health — Diagnostic Layer + State Reducer types
//
// CollectDiagnostics() generates []Diagnostic (all signals, no priority)
// ComputePoolHealth() reduces them to a single PoolHealth (deterministic)
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Diagnostic ──────────────────────────────────────────────────────────────

// Diagnostic is a single health signal detected during pool inspection.
// CollectDiagnostics generates ALL signals without prioritizing — the reducer
// (ComputePoolHealth) decides what matters.
type Diagnostic struct {
	Code     string `json:"code"`     // "smart_warning", "smart_critical", "io_errors", "disk_missing",
	//                                    "disk_faulted", "disk_unavailable", "disk_removed",
	//                                    "temp_high", "pool_faulted"
	Severity int    `json:"severity"` // 1=info, 2=warning, 3=error, 4=critical
	Disk     string `json:"disk"`     // affected disk (empty if pool-level)
	Detail   string `json:"detail"`   // human-readable detail
}

func (d Diagnostic) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"code":     d.Code,
		"severity": d.Severity,
		"disk":     d.Disk,
		"detail":   d.Detail,
	}
}

// ─── DiskStatus (from zpool status / btrfs device stats parsing) ─────────────

// DiskStatus holds the per-disk state parsed from zpool status or btrfs device stats.
type DiskStatus struct {
	State          string // ONLINE, DEGRADED, FAULTED, OFFLINE, REMOVED, UNAVAIL
	ReadErrors     int
	WriteErrors    int
	ChecksumErrors int
}

// ─── SmartDetails (extracted from getDiskSmart for poolHealth) ───────────────

// SmartDetails holds the SMART metrics relevant to pool health diagnostics.
// Sourced from getDiskSmart() in hardware.go via the smartHistory cache.
type SmartDetails struct {
	ReallocatedSectors int
	PendingSectors     int
	Uncorrectable      int
	PowerOnHours       int
	Temperature        int
	Partial            bool // true if NVMe with incomplete data
}

func (s SmartDetails) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"reallocatedSectors": s.ReallocatedSectors,
		"pendingSectors":     s.PendingSectors,
		"uncorrectable":      s.Uncorrectable,
		"powerOnHours":       s.PowerOnHours,
		"temperature":        s.Temperature,
		"partial":            s.Partial,
	}
}

// ─── PoolHealthReason ────────────────────────────────────────────────────────

// PoolHealthReason holds the primary reason for the pool's status plus any
// secondary signals that were present but not the deciding factor.
type PoolHealthReason struct {
	Primary   string   `json:"primary"`   // diagnostic code that determined the status
	Message   string   `json:"message"`   // human-readable explanation
	Secondary []string `json:"secondary"` // other diagnostic codes present
}

func (r PoolHealthReason) ToMap() map[string]interface{} {
	secondary := r.Secondary
	if secondary == nil {
		secondary = []string{}
	}
	return map[string]interface{}{
		"primary":   r.Primary,
		"message":   r.Message,
		"secondary": secondary,
	}
}

// ─── PoolRedundancy ──────────────────────────────────────────────────────────

// PoolRedundancy describes the redundancy characteristics of the pool.
type PoolRedundancy struct {
	Type      string `json:"type"`      // none | mirror | raidz1 | raidz2 | raidz3 | raid1 | raid10 | single
	Expected  int    `json:"expected"`  // disks according to config
	Current   int    `json:"current"`   // disks online now
	CanLose   int    `json:"can_lose"`  // tolerance of the vdev type
	Effective int    `json:"effective"` // canLose - disksMissing (how many MORE it can lose)
}

func (r PoolRedundancy) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"type":      r.Type,
		"expected":  r.Expected,
		"current":   r.Current,
		"canLose":   r.CanLose,
		"effective": r.Effective,
	}
}

// ─── PoolHealth ──────────────────────────────────────────────────────────────

// PoolHealth is the final reduced state of a storage pool.
// status: healthy | at_risk | unstable | degraded | critical
// intent: normal | rebuilding | replacing
type PoolHealth struct {
	Version int `json:"version"` // schema version for future compatibility

	Status string           `json:"status"` // healthy | at_risk | unstable | degraded | critical
	Reason PoolHealthReason `json:"reason"`

	Redundancy PoolRedundancy `json:"redundancy"`

	DisksTotal           int `json:"disks_total"`
	DisksOnline          int `json:"disks_online"`
	DisksMissing         int `json:"disks_missing"`
	DisksWithSmartIssues int `json:"disks_with_smart_issues"`
	DisksWithIoErrors    int `json:"disks_with_io_errors"`

	ResilverActive   bool    `json:"resilver_active"`
	ResilverProgress float64 `json:"resilver_progress"`
	ResilverEta      string  `json:"resilver_eta"`

	Intent string `json:"intent"` // normal | rebuilding

	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (h PoolHealth) ToMap() map[string]interface{} {
	diags := make([]map[string]interface{}, 0, len(h.Diagnostics))
	for _, d := range h.Diagnostics {
		diags = append(diags, d.ToMap())
	}
	if diags == nil {
		diags = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"version":              h.Version,
		"status":               h.Status,
		"reason":               h.Reason.ToMap(),
		"redundancy":           h.Redundancy.ToMap(),
		"disksTotal":           h.DisksTotal,
		"disksOnline":          h.DisksOnline,
		"disksMissing":         h.DisksMissing,
		"disksWithSmartIssues": h.DisksWithSmartIssues,
		"disksWithIoErrors":    h.DisksWithIoErrors,
		"resilverActive":       h.ResilverActive,
		"resilverProgress":     h.ResilverProgress,
		"resilverEta":          h.ResilverEta,
		"intent":               h.Intent,
		"diagnostics":          diags,
	}
}

// ─── EnrichedDisk (full disk info for pool detail view) ──────────────────────

// EnrichedDisk is the complete per-disk view returned inside pool info.
// Combines physical info (model, size), SMART status+details, and pool status.
type EnrichedDisk struct {
	Name        string       // "sda"
	Model       string       // "ST4000DM004-2CV104"
	Size        string       // "3,6T"
	SmartStatus string       // ok | warning | critical | partial | missing | unknown
	Smart       SmartDetails // detailed SMART metrics
	PoolStatus  string       // online | degraded | faulted | offline | removed | unavailable | missing
	IoErrors    DiskStatus   // read/write/checksum error counts
}

func (d EnrichedDisk) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"name":        d.Name,
		"model":       d.Model,
		"size":        d.Size,
		"smartStatus": d.SmartStatus,
		"smart":       d.Smart.ToMap(),
		"poolStatus":  d.PoolStatus,
		"ioErrors": map[string]interface{}{
			"read":     d.IoErrors.ReadErrors,
			"write":    d.IoErrors.WriteErrors,
			"checksum": d.IoErrors.ChecksumErrors,
		},
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Service Model — Unified base for ALL services displayed in NimHealth
//
// ServiceBase is the common shape for system services (NimTorrent, NimBackup),
// Docker engine, and Docker apps. The UI receives these fields for every
// service type and never needs to branch on type to render basic info.
//
// Status is NORMALIZED: only "running" | "stopped" | "error"
// Health is NORMALIZED: only "healthy" | "degraded" | "unhealthy" | "idle"
// ═══════════════════════════════════════════════════════════════════════════════

// ─── ServiceBase ─────────────────────────────────────────────────────────────

type ServiceBase struct {
	ID     string // "docker@poolzfs1", "nimtorrent@poolzfs1", "jellyfin"
	Type   string // "system" | "docker" | "docker-app"
	Parent string // "" for root services, "docker@poolzfs1" for Docker apps
	Name   string // "Docker Engine", "NimTorrent", "Jellyfin"
	Status string // "running" | "stopped" | "error"
	Health string // "healthy" | "degraded" | "unhealthy" | "idle"
}

func (s ServiceBase) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":     s.ID,
		"type":   s.Type,
		"parent": s.Parent,
		"name":   s.Name,
		"status": s.Status,
		"health": s.Health,
	}
}

// ─── PortBinding ─────────────────────────────────────────────────────────────

// PortBinding represents a single port mapping for a Docker container.
// A container can have multiple bindings (bridge, host, NAT, multiple ports).
type PortBinding struct {
	Declared int    // port from compose (e.g. 8096)
	Host     int    // port on host (e.g. 8096, may differ)
	Protocol string // "tcp" | "udp"
}

func (p PortBinding) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"declared": p.Declared,
		"host":     p.Host,
		"protocol": p.Protocol,
	}
}

// ─── DockerAppStatus ─────────────────────────────────────────────────────────

// DockerAppStatus is the runtime state of an installed Docker app.
// Built by crossing docker ps (runtime) + installed-apps.json (config).
// docker inspect is on-demand only (detail view), NOT during polling.
type DockerAppStatus struct {
	ServiceBase
	Ports         []PortBinding // real port bindings (can be multiple)
	Image         string        // "jellyfin/jellyfin:latest"
	Icon          string        // URL of the app icon
	ContainerName string        // actual container name in Docker
	OpenMode      string        // "internal" | "external" | "auto"
	Uptime        string        // "2d 14h" (from docker ps STATUS column)
}

func (d DockerAppStatus) ToMap() map[string]interface{} {
	m := d.ServiceBase.ToMap()
	ports := make([]map[string]interface{}, len(d.Ports))
	for i, p := range d.Ports {
		ports[i] = p.ToMap()
	}
	if ports == nil {
		ports = []map[string]interface{}{}
	}
	m["ports"] = ports
	m["image"] = d.Image
	m["icon"] = d.Icon
	m["containerName"] = d.ContainerName
	m["openMode"] = d.OpenMode
	m["uptime"] = d.Uptime
	return m
}

// ─── State Normalization ─────────────────────────────────────────────────────

// NormalizeDockerStatus maps raw Docker status strings to the 3 valid states.
// NEVER expose raw Docker states to the UI.
//
//	"Up 3 hours"          → "running"
//	"Exited (0) 2h ago"   → "stopped"
//	"Created"             → "stopped"
//	"Dead", "Removing"    → "error"
func NormalizeDockerStatus(dockerStatus string) string {
	lower := strings.ToLower(dockerStatus)
	switch {
	case strings.Contains(lower, "up"):
		return "running"
	case strings.Contains(lower, "exited"),
		strings.Contains(lower, "created"):
		return "stopped"
	default:
		return "error"
	}
}

// NormalizeDockerHealth maps Docker health check output to the 4 valid states.
//
//	"healthy"   → "healthy"
//	"unhealthy" → "unhealthy"
//	"starting"  → "degraded"
//	"none", ""  → "healthy" (no health check = trust status)
func NormalizeDockerHealth(healthStatus string) string {
	switch strings.ToLower(strings.TrimSpace(healthStatus)) {
	case "healthy":
		return "healthy"
	case "unhealthy":
		return "unhealthy"
	case "starting":
		return "degraded"
	default:
		return "healthy"
	}
}

