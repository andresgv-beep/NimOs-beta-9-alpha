package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Service Registry — Centralized service lifecycle management
//
// Tracks which services depend on which pools, enabling safe pool destruction
// and centralized start/stop control.
//
// See: documents/NIMOS-SERVICE-REGISTRY-v1.2.md
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Pool lock for destroy operations ────────────────────────────────────────

var poolLocked = map[string]bool{} // protected by storageMu

// ─── Table creation ──────────────────────────────────────────────────────────

func createServiceRegistryTables() error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS service_instances (
		id               TEXT PRIMARY KEY,
		app_id           TEXT NOT NULL,
		pool_name        TEXT NOT NULL,
		path             TEXT NOT NULL,
		status           TEXT CHECK (status IN
		                   ('running','stopped','starting','stopping','error','unknown'))
		                   DEFAULT 'unknown',
		health           TEXT CHECK (health IN
		                   ('healthy','degraded','failed','partial','unknown','stale'))
		                   DEFAULT 'unknown',
		owner            TEXT DEFAULT 'system',
		config           TEXT DEFAULT '{}',
		created_at       TEXT NOT NULL,
		updated_at       TEXT NOT NULL,
		last_observed_at TEXT,
		FOREIGN KEY (app_id) REFERENCES app_registry(id)
	);

	CREATE TABLE IF NOT EXISTS service_dependencies (
		instance_id TEXT NOT NULL,
		dep_type    TEXT NOT NULL,
		target      TEXT NOT NULL,
		required    TEXT DEFAULT 'required',
		PRIMARY KEY (instance_id, dep_type, target),
		FOREIGN KEY (instance_id) REFERENCES service_instances(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_si_pool ON service_instances(pool_name);
	CREATE INDEX IF NOT EXISTS idx_si_status ON service_instances(status);
	CREATE INDEX IF NOT EXISTS idx_sd_target ON service_dependencies(target);
	`)
	return err
}

// ─── Validation helpers ──────────────────────────────────────────────────────

// instanceIDRegex valida el formato de IDs de service_instances.
// Forma canónica: app_id@pool_name (o app_id@system para servicios system-wide).
// Reglas:
//
//	· Ambas partes (antes/después de @) son obligatorias, mínimo 1 char.
//	· Primer carácter de cada parte ∈ [a-z0-9_] (sin '-' al inicio para no
//	  parecer flag CLI).
//	· Resto de caracteres ∈ [a-z0-9_-].
//	· Exactamente UN '@' (no admite app@pool@otro).
//
// Acepta: docker@plex_pool, ssh@system, nimbackup@system, nimtorrent@media-1
// Rechaza: @bar, foo@, foo@bar@baz, -bad@pool, foo bar@pool, foo@bar/baz
var instanceIDRegex = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]*@[a-z0-9_][a-z0-9_-]*$`)

func validateInstanceID(id string) error {
	if !instanceIDRegex.MatchString(id) {
		return fmt.Errorf("invalid instance ID %q: must match app_id@pool_name "+
			"(lowercase alphanumeric + _ -)", id)
	}
	return nil
}

// validateServicePath asegura que el path del servicio es coherente con su
// scope (pool-managed vs system-wide).
//
// Dos casos:
//
//  1. PoolName != "" — service vive dentro de un pool gestionado (apps
//     Docker, NimTorrent, etc.). El path DEBE estar bajo el mount point
//     /nimos/pools/<poolName>/.
//
//  2. PoolName == "" — service system-wide (ssh, samba, nfs, nimbackup,
//     vms). Estos servicios viven en paths del sistema (/etc/ssh,
//     /var/lib/nimos, etc.) y no pertenecen a ningún pool. Solo validamos
//     que el path sea absoluto (mínima sanity check para evitar inputs
//     vacíos o relativos por bug).
//
// Ver nimhealth_detectors.go para los 4 detectors que generan instancias
// con PoolName="" (system services).
func validateServicePath(path, poolName string) error {
	if poolName == "" {
		// System service: solo exigimos path absoluto.
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("system service path must be absolute: %q", path)
		}
		return nil
	}
	// Pool-managed service: path DEBE estar dentro del mount point.
	prefix := nimosPoolsDir + "/" + poolName + "/"
	if !strings.HasPrefix(path, prefix) {
		return fmt.Errorf("path must be within pool mount point (%s)", prefix)
	}
	return nil
}

func validateDepType(depType string) error {
	switch depType {
	case "pool", "share", "path":
		return nil
	}
	return fmt.Errorf("invalid dep_type: %s (must be pool, share, or path)", depType)
}

func validateRequired(req string) error {
	switch req {
	case "required", "soft", "optional":
		return nil
	}
	return fmt.Errorf("invalid required level: %s (must be required, soft, or optional)", req)
}

// ─── DB operations ───────────────────────────────────────────────────────────

func dbServiceRegister(instance ServiceInstance, deps []ServiceDependency) error {
	if err := validateInstanceID(instance.ID); err != nil {
		return err
	}
	if err := validateServicePath(instance.Path, instance.PoolName); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	_, err = tx.Exec(`INSERT OR REPLACE INTO service_instances
		(id, app_id, pool_name, path, status, health, owner, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		instance.ID, instance.AppID, instance.PoolName, instance.Path,
		instance.Status, instance.Health, instance.Owner, instance.Config,
		now, now)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, dep := range deps {
		if err := validateDepType(dep.DepType); err != nil {
			tx.Rollback()
			return err
		}
		if err := validateRequired(dep.Required); err != nil {
			tx.Rollback()
			return err
		}
		_, err = tx.Exec(`INSERT OR REPLACE INTO service_dependencies
			(instance_id, dep_type, target, required) VALUES (?, ?, ?, ?)`,
			instance.ID, dep.DepType, dep.Target, dep.Required)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	tx.Commit()
	logMsg("service: registered %s (app=%s, pool=%s, status=%s)",
		instance.ID, instance.AppID, instance.PoolName, instance.Status)

	// Audit notification
	addNotification("info", "system", "Service registered",
		fmt.Sprintf("%s registered on pool %s", instance.ID, instance.PoolName))

	return nil
}

func dbServiceUpdateStatus(instanceID, status, health string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`UPDATE service_instances SET status = ?, health = ?, updated_at = ? WHERE id = ?`,
		status, health, now, instanceID)
	return err
}

func dbServiceDelete(instanceID string) error {
	_, err := db.Exec(`DELETE FROM service_instances WHERE id = ?`, instanceID)
	// dependencies cascade automatically
	return err
}

func dbServiceDeleteByPool(poolName string) error {
	_, err := db.Exec(`DELETE FROM service_instances WHERE pool_name = ?`, poolName)
	return err
}

func dbServiceGet(instanceID string) (*ServiceInstance, error) {
	var s ServiceInstance
	err := db.QueryRow(`SELECT id, app_id, pool_name, path, status, health, owner, config, created_at, updated_at
		FROM service_instances WHERE id = ?`, instanceID).
		Scan(&s.ID, &s.AppID, &s.PoolName, &s.Path, &s.Status, &s.Health, &s.Owner, &s.Config, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("service instance not found: %s", instanceID)
	}
	return &s, nil
}

func dbServiceList(poolFilter string) ([]ServiceInstance, error) {
	query := `SELECT si.id, si.app_id, si.pool_name, si.path, si.status, si.health, si.owner, si.config, si.created_at, si.updated_at
		FROM service_instances si`
	var args []interface{}
	if poolFilter != "" {
		query += ` WHERE si.pool_name = ?`
		args = append(args, poolFilter)
	}
	query += ` ORDER BY si.pool_name, si.app_id`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ServiceInstance
	for rows.Next() {
		var s ServiceInstance
		rows.Scan(&s.ID, &s.AppID, &s.PoolName, &s.Path, &s.Status, &s.Health, &s.Owner, &s.Config, &s.CreatedAt, &s.UpdatedAt)
		result = append(result, s)
	}
	if result == nil {
		result = []ServiceInstance{}
	}
	return result, nil
}

func dbServiceDependencies(instanceID string) ([]ServiceDependency, error) {
	rows, err := db.Query(`SELECT instance_id, dep_type, target, required
		FROM service_dependencies WHERE instance_id = ?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ServiceDependency
	for rows.Next() {
		var d ServiceDependency
		rows.Scan(&d.InstanceID, &d.DepType, &d.Target, &d.Required)
		result = append(result, d)
	}
	if result == nil {
		result = []ServiceDependency{}
	}
	return result, nil
}

// ─── Pool dependency check (for destroy) ─────────────────────────────────────

// checkPoolDependencies returns active services that depend on a pool.
// Used by destroy pool to determine if destruction is safe.
func checkPoolDependencies(poolName string) ([]PoolDependencyInfo, error) {
	rows, err := db.Query(`
		SELECT si.id, si.app_id, ar.name, si.status, si.health, sd.required
		FROM service_instances si
		JOIN service_dependencies sd ON sd.instance_id = si.id
		JOIN app_registry ar ON ar.id = si.app_id
		WHERE sd.dep_type = 'pool' AND sd.target = ?
		AND si.status IN ('running', 'starting', 'stopping')
		ORDER BY sd.required DESC, ar.name`, poolName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PoolDependencyInfo
	for rows.Next() {
		var d PoolDependencyInfo
		rows.Scan(&d.InstanceID, &d.AppID, &d.AppName, &d.Status, &d.Health, &d.Required)
		result = append(result, d)
	}
	if result == nil {
		result = []PoolDependencyInfo{}
	}
	return result, nil
}

// canDestroyPool checks if a pool can be safely destroyed.
// Returns the dependency list, whether destroy is possible, and whether force is available.
func canDestroyPool(poolName string) (deps []PoolDependencyInfo, canDestroy bool, canForce bool, err error) {
	deps, err = checkPoolDependencies(poolName)
	if err != nil {
		return nil, false, false, err
	}

	if len(deps) == 0 {
		return deps, true, false, nil
	}

	hasRequired := false
	hasSoft := false
	for _, d := range deps {
		if d.Required == "required" {
			hasRequired = true
		}
		if d.Required == "soft" {
			hasSoft = true
		}
	}

	if hasRequired {
		return deps, false, false, nil
	}
	if hasSoft {
		return deps, false, true, nil
	}
	// Only optional deps — can destroy
	return deps, true, false, nil
}

// ─── Service lifecycle (start/stop) ──────────────────────────────────────────

func serviceStop(instanceID string) error {
	instance, err := dbServiceGet(instanceID)
	if err != nil {
		return err
	}

	// Check managed_by from app_registry
	var managedBy string
	db.QueryRow(`SELECT managed_by FROM app_registry WHERE id = ?`, instance.AppID).Scan(&managedBy)

	// Estado intermedio honesto · el observer confirmará la transición real
	// en su próximo tick (≤30s). NO escribimos 'stopped' al final asumiendo
	// éxito · el systemctl stop puede fallar silenciosamente o el proceso
	// puede tardar en morir.
	dbServiceUpdateStatus(instanceID, "stopping", "unknown")

	opts := CmdOptions{Timeout: 30 * time.Second}
	var stopErr error

	switch managedBy {
	case "systemd":
		// Get systemd unit name from config or convention
		unitName := getSystemdUnit(instance)
		if unitName == "" {
			// No unit definido para este AppID · operación no aplicable.
			// (caso típico: managed_by inconsistente con AppID)
			return fmt.Errorf("no systemd unit defined for app %s", instance.AppID)
		}
		_, stopErr = runCmd("systemctl", []string{"stop", unitName}, opts)
	case "docker":
		// Parar Docker Y containerd juntos. containerd es independiente de
		// dockerd y mantiene los overlays (snapshots) montados sobre el pool;
		// parar solo docker deja containerd agarrando el filesystem → el pool
		// no se puede desmontar (POOL BUSY). Son un combo para liberar el pool.
		_, stopErr = runCmd("systemctl", []string{"stop", "docker.socket", "docker", "containerd"}, opts)
	case "internal":
		// Handled by daemon internally — no external process to stop
		stopErr = nil
	default:
		stopErr = nil
	}

	if stopErr != nil {
		// Acción falló · marcar como error/failed (estado terminal honesto)
		dbServiceUpdateStatus(instanceID, "error", "failed")
		return fmt.Errorf("failed to stop %s: %v", instanceID, stopErr)
	}

	// Comando emitido sin error · NO escribimos 'stopped/healthy' final.
	// El observer detectará el estado real en su próximo tick.
	// Para 'internal' (nimbackup) sí podemos afirmar el estado, ya que
	// somos el daemon que ejecuta esa lógica.
	if managedBy == "internal" {
		dbServiceUpdateStatus(instanceID, "stopped", "unknown")
	}
	addNotification("info", "system", "Service stop requested", fmt.Sprintf("%s stop command issued", instanceID))
	return nil
}

func serviceStart(instanceID string) error {
	instance, err := dbServiceGet(instanceID)
	if err != nil {
		return err
	}

	// Check pool lock (destroy in progress)
	storageMu.Lock()
	if poolLocked[instance.PoolName] {
		storageMu.Unlock()
		return fmt.Errorf("pool %s is being destroyed — cannot start services", instance.PoolName)
	}
	storageMu.Unlock()

	// Verify pool exists and is mounted
	if !isPathOnMountedPool(nimosPoolsDir + "/" + instance.PoolName) {
		dbServiceUpdateStatus(instanceID, "error", "failed")
		return fmt.Errorf("pool %s is not mounted", instance.PoolName)
	}

	var managedBy string
	db.QueryRow(`SELECT managed_by FROM app_registry WHERE id = ?`, instance.AppID).Scan(&managedBy)

	dbServiceUpdateStatus(instanceID, "starting", "unknown")

	opts := CmdOptions{Timeout: 30 * time.Second}
	var startErr error

	switch managedBy {
	case "systemd":
		unitName := getSystemdUnit(instance)
		if unitName == "" {
			return fmt.Errorf("no systemd unit defined for app %s", instance.AppID)
		}
		_, startErr = runCmd("systemctl", []string{"start", unitName}, opts)
	case "docker":
		_, startErr = runCmd("systemctl", []string{"start", "docker"}, opts)
	case "internal":
		startErr = nil
	default:
		startErr = nil
	}

	if startErr != nil {
		dbServiceUpdateStatus(instanceID, "error", "failed")
		return fmt.Errorf("failed to start %s: %v", instanceID, startErr)
	}

	// Comando emitido sin error · NO escribimos 'running/healthy' final.
	// El observer detectará el estado real en su próximo tick. Si el servicio
	// crashea 200ms después de arrancar, el observer lo verá y lo marcará
	// como error/failed · hoy nunca se enteraría.
	// Para 'internal' (nimbackup) sí afirmamos estado, somos el daemon.
	if managedBy == "internal" {
		dbServiceUpdateStatus(instanceID, "running", "healthy")
	}
	addNotification("info", "system", "Service start requested", fmt.Sprintf("%s start command issued", instanceID))
	return nil
}

// getSystemdUnit returns the systemd unit name for a service instance.
// Returns "" if no unit is appropriate (e.g. internal services that don't
// have a separate systemd unit). Callers MUST check for "" and handle it
// rather than passing it to systemctl, which would either fail or — for the
// dangerous default "nimos-daemon.service" of older versions — affect the
// daemon itself.
func getSystemdUnit(instance *ServiceInstance) string {
	switch instance.AppID {
	case "nimtorrent":
		return "nimos-torrentd.service"
	case "nimbackup":
		// nimbackup runs INSIDE the daemon (managed_by='internal').
		// Returning "nimos-daemon.service" here would mean Stop nimbackup
		// = kill daemon. Return "" so callers don't accidentally do that.
		return ""
	// System-level services that ship with the OS · NOT prefixed with "nimos-"
	case "ssh":
		return "ssh.service"
	case "samba":
		return "smbd.service"
	case "nfs":
		return "nfs-server.service"
	case "vms":
		return "libvirtd.service"
	default:
		return "nimos-" + instance.AppID + ".service"
	}
}

// ─── Service logs ────────────────────────────────────────────────────────────

// getServiceLogs returns the last N lines of logs for a service instance.
func getServiceLogs(instanceID string, n int) ([]map[string]interface{}, error) {
	instance, err := dbServiceGet(instanceID)
	if err != nil {
		return nil, err
	}

	var managedBy string
	db.QueryRow(`SELECT managed_by FROM app_registry WHERE id = ?`, instance.AppID).Scan(&managedBy)

	var rawOutput string

	switch managedBy {
	case "systemd":
		unitName := getSystemdUnit(instance)
		if unitName == "" {
			// No unit aplicable · sin logs systemd que mostrar.
			return []map[string]interface{}{}, nil
		}
		out, _ := runCmd("journalctl", []string{
			"-u", unitName,
			"-n", fmt.Sprintf("%d", n),
			"--no-pager",
			"-o", "short-iso",
		}, CmdOptions{Timeout: 5 * time.Second})
		rawOutput = out.Stdout

	case "docker":
		// Get logs from docker daemon
		out, _ := runCmd("journalctl", []string{
			"-u", "docker",
			"-n", fmt.Sprintf("%d", n),
			"--no-pager",
			"-o", "short-iso",
		}, CmdOptions{Timeout: 5 * time.Second})
		rawOutput = out.Stdout

	case "internal":
		// NimBackup and internal services log to the daemon log
		out, _ := runCmd("journalctl", []string{
			"-u", "nimos-daemon",
			"-n", fmt.Sprintf("%d", n),
			"--no-pager",
			"-o", "short-iso",
			"--grep", instance.AppID,
		}, CmdOptions{Timeout: 5 * time.Second})
		rawOutput = out.Stdout
		// If grep found nothing, fall back to daemon logs without filter
		if strings.TrimSpace(rawOutput) == "" || strings.Contains(rawOutput, "No entries") {
			out, _ = runCmd("journalctl", []string{
				"-u", "nimos-daemon",
				"-n", fmt.Sprintf("%d", n),
				"--no-pager",
				"-o", "short-iso",
			}, CmdOptions{Timeout: 5 * time.Second})
			rawOutput = out.Stdout
		}

	default:
		return []map[string]interface{}{}, nil
	}

	// Parse output into structured lines
	var lines []map[string]interface{}
	for _, line := range strings.Split(rawOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-- ") {
			continue
		}
		// short-iso format: "2026-04-02T10:23:45+0200 hostname unit[pid]: message"
		// Try to split timestamp from message
		ts := ""
		msg := line
		if len(line) > 25 && (line[4] == '-' || line[10] == 'T') {
			// Find first space after timestamp+hostname+unit
			// Format: "2026-04-02T10:23:45+0200 hostname unit[123]: actual message"
			idx := strings.Index(line, "]: ")
			if idx > 0 {
				ts = line[:25] // ISO timestamp portion
				msg = strings.TrimSpace(line[idx+3:])
			} else {
				// Simpler format, just split at first colon after timestamp
				spaceIdx := strings.Index(line[25:], " ")
				if spaceIdx > 0 {
					ts = line[:25]
					msg = strings.TrimSpace(line[25+spaceIdx:])
				}
			}
		}
		lines = append(lines, map[string]interface{}{
			"timestamp": ts,
			"message":   msg,
		})
	}

	if lines == nil {
		lines = []map[string]interface{}{}
	}
	return lines, nil
}

// ─── Boot reconciliation ─────────────────────────────────────────────────────

// autoRegisterServices detects running services and registers them if not
// already present. Called periódicamente desde reconcileServices.
//
// Fase 5: delegación al slice de detectores en nimhealth.go. Para añadir
// un nuevo servicio detectable, añadir su función al slice `detectors`
// en nimhealth.go · NO modificar esta función.
func autoRegisterServices() {
	runAutoRegister(context.Background())
}

// reconcileMaxConcurrency · número máximo de instances reconciliadas en
// paralelo. 4 es razonable para Pi 4 (4 cores). Cada worker hace 1-2
// runCmd con timeout 5s, así que el peor caso es CPU-bound, no IO-bound.
const reconcileMaxConcurrency = 4

func reconcileServices() {
	// Auto-register first — detect services that exist but aren't registered
	autoRegisterServices()

	// ── Clean orphan services whose pool no longer exists (Beta 8.1: service v2) ──
	poolNames := map[string]bool{}
	if storageService != nil {
		if pools, err := storageService.ListPools(context.Background()); err == nil {
			for _, p := range pools {
				if p.Name != "" {
					poolNames[p.Name] = true
				}
			}
		}
	}

	allInstances, _ := dbServiceList("")
	for _, inst := range allInstances {
		if inst.PoolName != "" && !poolNames[inst.PoolName] {
			db.Exec(`DELETE FROM service_dependencies WHERE instance_id = ?`, inst.ID)
			dbServiceDelete(inst.ID)
			logMsg("service reconcile: cleaned orphan %s (pool %s no longer exists)", inst.ID, inst.PoolName)
		}
	}

	// ── Reconcile remaining services in parallel (Fase 6) ──
	// Cada instance es independiente (lecturas/escrituras de SU fila),
	// así que paralelizamos con semáforo bounded. Disciplina §1: NO usamos
	// errgroup (dependencia externa) cuando WaitGroup+canal cubre el caso.
	instances, err := dbServiceList("")
	if err != nil {
		logMsg("service reconcile: error loading instances: %v", err)
		return
	}

	sem := make(chan struct{}, reconcileMaxConcurrency)
	var wg sync.WaitGroup
	for _, inst := range instances {
		inst := inst // capture por valor para la goroutine
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			reconcileOneInstance(inst)
		}()
	}
	wg.Wait()
}

// reconcileOneInstance reconcilia el estado de UNA instance. Llamada
// desde un worker pool en reconcileServices. Cada invocación es
// independiente · no comparte estado con otras instances.
func reconcileOneInstance(inst ServiceInstance) {
	// System-wide services (PoolName=="") no están atados a un pool ·
	// son cosas como SSH, Samba, NFS, NimBackup interno, libvirtd, etc.
	// No se valida pool/path; comprobamos directamente su status via
	// systemctl/internal.
	if inst.PoolName == "" {
		var managedBy string
		db.QueryRow(`SELECT managed_by FROM app_registry WHERE id = ?`, inst.AppID).Scan(&managedBy)

		newStatus := "unknown"
		newHealth := "unknown"

		switch managedBy {
		case "systemd":
			unitName := getSystemdUnit(&inst)
			if unitName != "" {
				out, _ := runCmd("systemctl", []string{"is-active", unitName}, CmdOptions{Timeout: 5 * time.Second})
				if strings.TrimSpace(out.Stdout) == "active" {
					newStatus = "running"
					newHealth = "healthy"
				} else {
					newStatus = "stopped"
					newHealth = "unknown"
				}
			}
		case "internal":
			// Daemon is alive (we are the daemon), so internal services are running
			newStatus = "running"
			newHealth = "healthy"
		}

		if inst.Status != newStatus || inst.Health != newHealth {
			dbServiceUpdateStatus(inst.ID, newStatus, newHealth)
			logMsg("service reconcile: %s → %s/%s", inst.ID, newStatus, newHealth)
		}
		return
	}

	poolPath := nimosPoolsDir + "/" + inst.PoolName

	// Check pool exists and is mounted
	if !isPathOnMountedPool(poolPath) {
		if inst.Status != "error" {
			dbServiceUpdateStatus(inst.ID, "error", "failed")
			logMsg("service reconcile: %s → error (pool %s not mounted)", inst.ID, inst.PoolName)
		}
		return
	}

	// Check path exists
	if _, err := runCmd("test", []string{"-d", inst.Path}, CmdOptions{Timeout: 2 * time.Second}); err != nil {
		if inst.Status != "error" {
			dbServiceUpdateStatus(inst.ID, "error", "failed")
			logMsg("service reconcile: %s → error (path %s missing)", inst.ID, inst.Path)
		}
		return
	}

	// Sync real status
	var managedBy string
	db.QueryRow(`SELECT managed_by FROM app_registry WHERE id = ?`, inst.AppID).Scan(&managedBy)

	reallyRunning := false
	switch managedBy {
	case "systemd":
		unitName := getSystemdUnit(&inst)
		if unitName != "" {
			out, _ := runCmd("systemctl", []string{"is-active", unitName}, CmdOptions{Timeout: 5 * time.Second})
			reallyRunning = strings.TrimSpace(out.Stdout) == "active"
		}
	case "docker":
		out, _ := runCmd("systemctl", []string{"is-active", "docker"}, CmdOptions{Timeout: 5 * time.Second})
		reallyRunning = strings.TrimSpace(out.Stdout) == "active"
	case "internal":
		reallyRunning = true // daemon is running, so internal services are too
	}

	newStatus := "stopped"
	newHealth := "unknown"
	if reallyRunning {
		newStatus = "running"
		newHealth = "healthy"
	}

	if inst.Status != newStatus || inst.Health != newHealth {
		dbServiceUpdateStatus(inst.ID, newStatus, newHealth)
		logMsg("service reconcile: %s → %s/%s", inst.ID, newStatus, newHealth)
	}
}
