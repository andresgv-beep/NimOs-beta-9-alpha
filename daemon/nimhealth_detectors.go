// nimhealth_detectors.go — Service detectors para NimHealth.
//
// Patrón ligero · slice de funciones, NO interface formal (disciplina §1
// anti-abstracción anticipada). Cada detector:
//
//   · Recibe un contexto (para timeouts si fuera necesario)
//   · Devuelve 0..N registrableService con sus dependencies
//   · Es LIBRE de decidir si su servicio existe en el sistema o no
//   · Si no aplica, devuelve nil (será ignorado)
//
// Para añadir un nuevo servicio detectable, basta con:
//   1. Crear función detectX(ctx) []registrableService
//   2. Añadirla al slice `detectors`
//
// El loop runAutoRegister hace INSERT OR IGNORE en app_registry primero
// para servicios que aún no estén ahí (caso típico: ssh, samba, nfs).

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// registrableService agrupa una ServiceInstance con sus dependencies +
// metadata del app_registry necesaria para auto-seedear si no existe.
type registrableService struct {
	instance    ServiceInstance
	deps        []ServiceDependency
	appName     string // nombre legible (usado solo si AppID no está en app_registry)
	appCategory string // category para auto-seed
	adminOnly   bool
	managedBy   string // "systemd"/"docker"/"internal"/"none"
}

// ServiceDetector · firma de cada detector individual.
type ServiceDetector func(ctx context.Context) []registrableService

// detectors · lista canónica. Para añadir un servicio detectable nuevo
// en el futuro: implementar la función y añadirla aquí.
var detectors = []ServiceDetector{
	detectDockerEngine,
	detectNimTorrent,
	detectNimBackup,
	detectVMs,
	detectSSH,
	detectSamba,
	detectNFS,
}

// runAutoRegister ejecuta todos los detectores y registra las instances
// que no existan ya en la DB. Llamado periódicamente desde el observer
// Reconcile (vía services.go::autoRegisterServices que delega aquí).
//
// Idempotente · si una instance ya existe en service_instances, se ignora.
// Si el app_id no existe en app_registry, se inserta con INSERT OR IGNORE
// para satisfacer la FOREIGN KEY antes de registrar la instance.
func runAutoRegister(ctx context.Context) {
	existing, _ := dbServiceListWithTimeout("")
	existingIDs := make(map[string]bool, len(existing))
	for _, inst := range existing {
		existingIDs[inst.ID] = true
	}

	for _, detect := range detectors {
		for _, rs := range detect(ctx) {
			if existingIDs[rs.instance.ID] {
				continue
			}
			adminOnly := 0
			if rs.adminOnly {
				adminOnly = 1
			}
			db.Exec(`INSERT OR IGNORE INTO app_registry (id, name, category, admin_only, public, type, managed_by)
				VALUES (?, ?, ?, ?, 0, 'system', ?)`,
				rs.instance.AppID, rs.appName, rs.appCategory, adminOnly, rs.managedBy)

			if err := dbServiceRegister(rs.instance, rs.deps); err != nil {
				logMsg("nimhealth: auto-register %s failed: %v", rs.instance.ID, err)
				continue
			}
			logMsg("nimhealth: auto-registered %s", rs.instance.ID)
		}
	}
}

// ── Helpers compartidos por detectores ──────────────────────────────────

// systemdUnitExists comprueba si una unit existe (instalada, no
// necesariamente activa). Usa `systemctl cat` que devuelve 0 si la
// unit existe.
func systemdUnitExists(unitName string) bool {
	out, ok := runSafe("systemctl", "cat", unitName)
	return ok && out != ""
}

// findPoolFromPath · dado un path absoluto que vive en
// /nimos/pools/{pool}/... devuelve el nombre del pool. "" si no aplica.
func findPoolFromPath(path string) string {
	prefix := nimosPoolsDir + "/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// ── Detectores ──────────────────────────────────────────────────────────

// detectDockerEngine · 1 instance si Docker está configurado en un pool.
//
// APP-030 · defensive logging: si docker.json dice installed=true pero el
// path no vive bajo nimosPoolsDir, el engine NO se registra. Sin log
// visible, el síntoma (NimHealth no muestra Docker) es difícil de
// diagnosticar. El warning explícito guía al usuario o al log de soporte.
//
// Convención asumida: docker data path siempre bajo /nimos/pools/{name}/docker.
// Si el path es custom, la integración con el resto de NimOS (services
// registry, pool dependencies, share creation) no funciona — por eso el
// engine no se registra como instance. El warning lo deja claro.
func detectDockerEngine(_ context.Context) []registrableService {
	conf := getDockerConfigGo()
	installed, _ := conf["installed"].(bool)
	dockerPath, _ := conf["path"].(string)
	if !installed || dockerPath == "" {
		return nil
	}
	poolName := findPoolFromPath(dockerPath)
	if poolName == "" {
		logMsg("nimhealth: detectDockerEngine: docker.json says installed=true but path %q "+
			"doesn't live under %s/ — engine NOT registered as a service instance. "+
			"Either move Docker data to a pool path or fix docker.json manually.",
			dockerPath, nimosPoolsDir)
		return nil
	}
	id := "docker@" + poolName
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "containers", PoolName: poolName, Path: dockerPath,
			Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
		},
		deps: []ServiceDependency{
			{InstanceID: id, DepType: "pool", Target: poolName, Required: "required"},
			{InstanceID: id, DepType: "path", Target: dockerPath, Required: "required"},
		},
		appName: "Containers", appCategory: "system", managedBy: "docker",
	}}
}

// detectNimTorrent · 1 instance si torrent.conf apunta a un pool.
func detectNimTorrent(_ context.Context) []registrableService {
	data, err := os.ReadFile(torrentConfPath)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "download_dir=") {
			continue
		}
		dir := strings.TrimSpace(strings.TrimPrefix(line, "download_dir="))
		poolName := findPoolFromPath(dir)
		if poolName == "" {
			continue
		}
		id := "nimtorrent@" + poolName
		path := filepath.Join(nimosPoolsDir, poolName, "shares")
		return []registrableService{{
			instance: ServiceInstance{
				ID: id, AppID: "nimtorrent", PoolName: poolName, Path: path,
				Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
			},
			deps: []ServiceDependency{
				{InstanceID: id, DepType: "pool", Target: poolName, Required: "required"},
			},
			appName: "NimTorrent", appCategory: "app", managedBy: "systemd",
		}}
	}
	return nil
}

// detectNimBackup · siempre presente (interno al daemon).
// Si el daemon vive, NimBackup vive. PoolName="" porque es system-wide.
func detectNimBackup(_ context.Context) []registrableService {
	id := "nimbackup@system"
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "nimbackup", PoolName: "", Path: "/var/lib/nimos",
			Status: "running", Health: string(HealthHealthy), Owner: "system", Config: "{}",
		},
		deps:    []ServiceDependency{},
		appName: "NimBackup", appCategory: "system", managedBy: "internal",
	}}
}

// detectVMs · 1 instance si virsh + qemu + libvirtd están presentes.
func detectVMs(_ context.Context) []registrableService {
	_, virshOk := runSafe("which", "virsh")
	_, qemuOk := runSafe("which", "qemu-system-x86_64")
	if !virshOk || !qemuOk {
		return nil
	}
	if !systemdUnitExists("libvirtd.service") {
		return nil
	}
	id := "vms@system"
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "vms", PoolName: "", Path: "/var/lib/nimos/vms",
			Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
		},
		deps:    []ServiceDependency{},
		appName: "Virtual Machines", appCategory: "system", managedBy: "systemd",
	}}
}

// detectSSH · 1 instance si ssh.service existe (universal en Linux NAS).
func detectSSH(_ context.Context) []registrableService {
	if !systemdUnitExists("ssh.service") {
		return nil
	}
	id := "ssh@system"
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "ssh", PoolName: "", Path: "/etc/ssh",
			Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
		},
		deps:        []ServiceDependency{},
		appName:     "SSH",
		appCategory: "system",
		adminOnly:   true,
		managedBy:   "systemd",
	}}
}

// detectSamba · 1 instance si smbd.service existe.
func detectSamba(_ context.Context) []registrableService {
	if !systemdUnitExists("smbd.service") {
		return nil
	}
	id := "samba@system"
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "samba", PoolName: "", Path: "/etc/samba",
			Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
		},
		deps:        []ServiceDependency{},
		appName:     "Samba (SMB/CIFS)",
		appCategory: "system",
		adminOnly:   true,
		managedBy:   "systemd",
	}}
}

// detectNFS · 1 instance si nfs-server.service existe.
func detectNFS(_ context.Context) []registrableService {
	if !systemdUnitExists("nfs-server.service") {
		return nil
	}
	id := "nfs@system"
	return []registrableService{{
		instance: ServiceInstance{
			ID: id, AppID: "nfs", PoolName: "", Path: "/etc/exports",
			Status: "unknown", Health: string(HealthUnknown), Owner: "system", Config: "{}",
		},
		deps:        []ServiceDependency{},
		appName:     "NFS Server",
		appCategory: "system",
		adminOnly:   true,
		managedBy:   "systemd",
	}}
}
