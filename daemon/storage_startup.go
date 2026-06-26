package main

// NimOS Storage — Startup, detection, disk scanning, pool dirs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── Startup functions (called from main.go) ────────────────────────────────

// Beta 8: zfsAutoImportOnStartup() removed (ZFS no longer supported).

func btrfsAutoMountOnStartup() {
	if !hasBtrfs {
		return
	}
	if storageService == nil {
		return
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return
	}
	for _, p := range pools {
		if p.MountPoint == "" {
			continue
		}
		// Si ya está montado (p.ej. por la entrada de fstab al arrancar), NO
		// volver a montar — un segundo `mount` apila otra capa sobre el mismo
		// punto, y cada capa extra confunde a containerd (lee de una, escribe
		// en otra) → snapshots corruptos. Verificamos antes de montar.
		if isPathOnMountedPool(p.MountPoint) {
			logMsg("auto-mount: '%s' ya montado en %s — omitido", p.Name, p.MountPoint)
			continue
		}
		// Try mount from fstab
		runSafe("mount", p.MountPoint)
	}
	logMsg("Btrfs auto-mount completed")
}

func startupStorage() {
	logMsg("startup: Storage initialization...")
	if storageService == nil {
		logMsg("startup: storage service not initialized")
		return
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		logMsg("startup: error listing pools: %v", err)
		return
	}
	if len(pools) == 0 {
		logMsg("startup: No pools configured")
		return
	}
	// Verify pools are mounted and create dirs if needed
	for _, p := range pools {
		if p.MountPoint == "" {
			continue
		}
		if isPathOnMountedPool(p.MountPoint) {
			logMsg("startup: Pool '%s' mounted at %s", p.Name, p.MountPoint)
			createPoolDirs(p.MountPoint)
		} else {
			logMsg("startup: WARNING — Pool '%s' NOT mounted at %s", p.Name, p.MountPoint)
		}
	}
	logMsg("startup: Storage initialization complete")
}

func startStorageMonitoring() {
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			runStorageHealthCheck()
		}
	}()
}

// Beta 8: startZfsScheduler() removed (ZFS no longer supported).
// Para Beta 9 / BTRFS scrub scheduling: ver startScrubScheduler()
// en storage_btrfs_features.go

// ─── Detection (called from hardware.go) ─────────────────────────────────────

func detectBtrfs() {
	if _, ok := runSafe("which", "mkfs.btrfs"); ok {
		hasBtrfs = true
		logMsg("Btrfs: available")
	} else {
		logMsg("Btrfs: not available")
	}
}

// ─── Disk detection ──────────────────────────────────────────────────────────

// normalizeFstype limpia el valor de fstype que viene de lsblk (que puede ser
// el literal "<nil>" cuando el campo es null en el JSON).
func normalizeFstype(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "<nil>" || raw == "null" {
		return ""
	}
	return raw
}

// diskHasExistingData decide si un disco contiene datos preexistentes.
// SEGURIDAD: true si tiene particiones O un filesystem a disco completo.
// Un disco con FS whole-disk (como los miembros BTRFS de un pool, o un disco
// ext4/xfs de otro sistema) NO debe reportarse como vacío: la UI lo marcaría
// "elegible/sin datos" y sería una invitación al wipe.
func diskHasExistingData(numPartitions int, diskFstype string) bool {
	if numPartitions > 0 {
		return true
	}
	return normalizeFstype(diskFstype) != ""
}

func detectStorageDisksGo() map[string]interface{} {
	lsblkRaw, ok := runSafe("lsblk", "-J", "-b", "-o", "NAME,SIZE,TYPE,ROTA,MOUNTPOINT,MODEL,SERIAL,TRAN,RM,FSTYPE,LABEL,PKNAME")
	if !ok || lsblkRaw == "" {
		return map[string]interface{}{
			"eligible":    []interface{}{},
			"nvme":        []interface{}{},
			"usb":         []interface{}{},
			"provisioned": []interface{}{},
		}
	}

	rootDisk := findRootDiskGo(lsblkRaw)
	poolDisks := map[string]bool{}
	if storageService != nil {
		if pools, err := storageService.ListPools(context.Background()); err == nil {
			for _, p := range pools {
				for _, dev := range p.Devices {
					if dev.CurrentPath != "" {
						poolDisks[dev.CurrentPath] = true
					}
				}
			}
		}
	}
	return parseDetectedDisksLegacy(lsblkRaw, rootDisk, poolDisks)
}

// parseDetectedDisksLegacy es el core map[string]interface{} original, extraído
// para poder compararlo contra parseDetectedDisks (tipado) en un test de
// equivalencia con el MISMO input. Es la red de seguridad del refactor: si
// ambos producen JSON idéntico, la migración no cambió el contrato.
//
// Se ELIMINARÁ cuando el test de equivalencia se considere estable y el endpoint
// lleve tiempo sirviendo la versión tipada sin incidencias.
func parseDetectedDisksLegacy(lsblkRaw, rootDisk string, poolDisks map[string]bool) map[string]interface{} {
	result := map[string]interface{}{
		"eligible":    []interface{}{},
		"nvme":        []interface{}{},
		"usb":         []interface{}{},
		"provisioned": []interface{}{},
	}

	var data struct {
		BlockDevices []json.RawMessage `json:"blockdevices"`
	}
	if json.Unmarshal([]byte(lsblkRaw), &data) != nil {
		return result
	}

	var eligible, nvme, usb, provisioned []interface{}

	for _, raw := range data.BlockDevices {
		var dev map[string]interface{}
		json.Unmarshal(raw, &dev)

		devType, _ := dev["type"].(string)
		if devType != "disk" {
			continue
		}
		devName, _ := dev["name"].(string)

		// Whitelist: only sd*, nvme*, vd*
		validPrefix := false
		for _, prefix := range []string{"sd", "nvme", "vd"} {
			if strings.HasPrefix(devName, prefix) {
				validPrefix = true
				break
			}
		}
		if !validPrefix {
			continue
		}

		size := jsonToInt64(dev["size"])
		if size < 1024*1024*1024 { // < 1GB
			continue
		}

		transport, _ := dev["tran"].(string)
		model, _ := dev["model"].(string)
		serial, _ := dev["serial"].(string)
		rotaBool := jsonToBool(dev["rota"])
		removableBool := jsonToBool(dev["rm"])

		diskInfo := map[string]interface{}{
			"name":          devName,
			"path":          "/dev/" + devName,
			"model":         strings.TrimSpace(model),
			"serial":        strings.TrimSpace(serial),
			"size":          size,
			"sizeFormatted": formatBytes(size),
			"transport":     transport,
			"rotational":    rotaBool,
			"removable":     removableBool,
			"isBoot":        devName == rootDisk,
			"partitions":    []interface{}{},
		}

		// Parse partitions
		var partitions []interface{}
		if children, ok := dev["children"].([]interface{}); ok {
			for _, child := range children {
				cm, ok := child.(map[string]interface{})
				if !ok {
					continue
				}
				partSize := jsonToInt64(cm["size"])
				partitions = append(partitions, map[string]interface{}{
					"name":       cm["name"],
					"path":       "/dev/" + fmt.Sprintf("%v", cm["name"]),
					"size":       partSize,
					"fstype":     cm["fstype"],
					"label":      cm["label"],
					"mountpoint": cm["mountpoint"],
				})
			}
		}
		if partitions == nil {
			partitions = []interface{}{}
		}
		diskInfo["partitions"] = partitions

		diskFstype := strings.TrimSpace(fmt.Sprintf("%v", dev["fstype"]))
		diskInfo["hasExistingData"] = diskHasExistingData(len(partitions), diskFstype)
		diskInfo["fstype"] = normalizeFstype(diskFstype)

		// Classify
		if devName == rootDisk {
			continue // boot disk — never show
		}

		if poolDisks["/dev/"+devName] {
			diskInfo["classification"] = "provisioned"
			provisioned = append(provisioned, diskInfo)
			continue
		}

		// USB pendrive: USB + removable + < 10GB
		if transport == "usb" && removableBool && size < 10*1024*1024*1024 {
			diskInfo["classification"] = "usb"
			usb = append(usb, diskInfo)
			continue
		}

		// NVMe that isn't boot
		if strings.HasPrefix(devName, "nvme") {
			diskInfo["classification"] = "nvme"
			nvme = append(nvme, diskInfo)
			continue
		}

		// Everything else is eligible
		diskInfo["classification"] = "eligible"

		// Add SMART status from cache (lightweight — no smartctl call)
		smartStatus, smartDetails := getSmartDetailsForDisk(devName)
		diskInfo["smartStatus"] = smartStatus
		smart := map[string]interface{}{
			"temperature":        smartDetails.Temperature,
			"powerOnHours":       smartDetails.PowerOnHours,
			"pendingSectors":     smartDetails.PendingSectors,
			"uncorrectable":      smartDetails.Uncorrectable,
			"reallocatedSectors": smartDetails.ReallocatedSectors,
		}
		diskInfo["smart"] = smart

		eligible = append(eligible, diskInfo)
	}

	if eligible == nil {
		eligible = []interface{}{}
	}
	if nvme == nil {
		nvme = []interface{}{}
	}
	if usb == nil {
		usb = []interface{}{}
	}
	if provisioned == nil {
		provisioned = []interface{}{}
	}

	result["eligible"] = eligible
	result["nvme"] = nvme
	result["usb"] = usb
	result["provisioned"] = provisioned
	return result
}

func findRootDiskGo(lsblkJSON string) string {
	var data struct {
		BlockDevices []struct {
			Name     string `json:"name"`
			Children []struct {
				Mountpoint interface{} `json:"mountpoint"`
			} `json:"children"`
			Mountpoint interface{} `json:"mountpoint"`
		} `json:"blockdevices"`
	}
	json.Unmarshal([]byte(lsblkJSON), &data)
	for _, dev := range data.BlockDevices {
		for _, child := range dev.Children {
			if mp, _ := child.Mountpoint.(string); mp == "/" {
				return dev.Name
			}
		}
		if mp, _ := dev.Mountpoint.(string); mp == "/" {
			return dev.Name
		}
	}
	return ""
}

// ─── Pool dirs ───────────────────────────────────────────────────────────────

func createPoolDirs(mountPoint string) {
	dirs := []string{"shares", "system-backup/config", "system-backup/snapshots"}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(mountPoint, d), 0755)
	}
}

// ─── Health ──────────────────────────────────────────────────────────────────

func checkStorageHealthGo() []map[string]interface{} {
	var alerts []map[string]interface{}
	if storageService != nil {
		if pools, err := storageService.ListPools(context.Background()); err == nil {
			for _, p := range pools {
				if p.Usage == nil {
					continue
				}
				pct := p.Usage.UsagePercent
				if pct >= 95 {
					alerts = append(alerts, map[string]interface{}{"severity": "critical", "pool": p.Name, "message": fmt.Sprintf("Pool %s is %d%% full", p.Name, pct)})
				} else if pct >= 85 {
					alerts = append(alerts, map[string]interface{}{"severity": "warning", "pool": p.Name, "message": fmt.Sprintf("Pool %s is %d%% full", p.Name, pct)})
				}
			}
		}
	}
	if alerts == nil {
		alerts = []map[string]interface{}{}
	}
	storageAlertsMu.Lock()
	storageAlertsGo = alerts
	storageAlertsMu.Unlock()
	return alerts
}

// ─── Wipe (implemented in storage_wipe.go) ──────────────────────────────────

// ─── Scan / Restore (stubs) ──────────────────────────────────────────────────

func rescanSCSIBuses() {
	entries, err := os.ReadDir("/sys/class/scsi_host")
	if err != nil {
		return
	}
	for _, e := range entries {
		scanPath := filepath.Join("/sys/class/scsi_host", e.Name(), "scan")
		os.WriteFile(scanPath, []byte("- - -"), 0200)
	}
	runSafe("udevadm", "settle", "--timeout=5")
}

func backupConfigToPoolGo() {
	if storageService == nil {
		return
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil || len(pools) == 0 {
		return
	}

	// Ficheros de config PLANOS (JSON) → copia directa. La BD NO va aquí: se
	// respalda con un snapshot consistente (VACUUM INTO) más abajo, porque
	// copiarla a pelo perdería las escrituras que viven en el WAL.
	plainConfigFiles := []string{
		"/var/lib/nimos/config/docker.json",
		"/var/lib/nimos/config/remote-access.json",
		"/var/lib/nimos/config/security.json",
		"/etc/docker/daemon.json",
	}

	backed := 0
	for _, p := range pools {
		if p.MountPoint == "" {
			continue
		}

		backupDir := filepath.Join(p.MountPoint, "system-backup", "config")
		os.MkdirAll(backupDir, 0755)

		// La BD se respalda con un snapshot CONSISTENTE (VACUUM INTO), que
		// incluye lo que aún vive en el WAL. Copiarla a pelo perdería los
		// cambios recientes — fue la causa probable de la pérdida de shares.
		dbDst := filepath.Join(backupDir, filepath.Base(dbPath))
		if err := backupDBConsistent(dbDst); err != nil {
			logMsg("config backup: WAL-safe DB snapshot failed → %s: %v", dbDst, err)
		}

		for _, src := range plainConfigFiles {
			data, err := os.ReadFile(src)
			if err != nil {
				continue // file doesn't exist, skip
			}
			dst := filepath.Join(backupDir, filepath.Base(src))
			if err := os.WriteFile(dst, data, 0600); err != nil {
				logMsg("config backup: failed to write %s → %s: %v", src, dst, err)
			}
		}
		backed++
	}

	if backed > 0 {
		logMsg("config backup: saved to %d pool(s)", backed)
	}
}

// startConfigBackupLoop · backup de config event-driven (OP-1). Respalda al
// arrancar, luego ante cada cambio de config durable (con debounce de 5s para
// coalescer ráfagas) y, como red de seguridad, cada 30 min aunque nada marque
// dirty. markConfigDirty() vive en config_dirty.go.
func startConfigBackupLoop() {
	// Wait for system to settle
	time.Sleep(60 * time.Second)

	// Initial backup
	backupConfigToPoolGo()

	// Event-driven con backstop. stop=nil → corre indefinidamente.
	runConfigBackupLoop(configDirty, 5*time.Second, 30*time.Minute, backupConfigToPoolGo, nil)
}

func appendFstab(uuid, mountPoint, filesystem string) {
	logMsg("appendFstab: START uuid=%s mp=%s fs=%s", uuid, mountPoint, filesystem)

	existing, err := os.ReadFile("/etc/fstab")
	if err != nil {
		logMsg("appendFstab: ERROR reading /etc/fstab: %v", err)
		return
	}
	logMsg("appendFstab: read /etc/fstab (%d bytes)", len(existing))

	// BUG (saga 12-13/06): el check anterior usaba strings.Contains sobre el
	// fstab entero. Eso da FALSO POSITIVO cuando el mountpoint es substring de
	// otra entrada (p.ej. /nimos/pools/datatest1 contiene /nimos/pools/datatest,
	// o un residuo viejo), provocando un skip silencioso → la entrada NUNCA se
	// escribe → el pool se monta en caliente pero NO sobrevive al reinicio →
	// cae a directorio hueco en el disco de sistema. Comparar campo a campo.
	if fstabHasMountpoint(string(existing), mountPoint) {
		logMsg("appendFstab: skip — mount point %s already in fstab (exact match)", mountPoint)
		return
	}

	opts := "defaults,nofail,noatime"
	if filesystem == "btrfs" {
		opts = "defaults,nofail,noatime,compress=zstd"
	}
	entry := fmt.Sprintf("UUID=%s %s %s %s 0 2\n", uuid, mountPoint, filesystem, opts)
	logMsg("appendFstab: opening /etc/fstab for append")

	f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logMsg("appendFstab: ERROR opening /etc/fstab: %v", err)
		return
	}
	defer f.Close()

	logMsg("appendFstab: writing entry")
	n, werr := f.WriteString(entry)
	if werr != nil {
		logMsg("appendFstab: ERROR writing: %v (wrote %d bytes)", werr, n)
		return
	}
	logMsg("appendFstab: added %s (wrote %d bytes)", mountPoint, n)

	// Verificación post-escritura: releer y confirmar que la entrada quedó.
	// Si no aparece, el pool quedaría sin ancla y caería a directorio hueco
	// en el próximo reinicio — lo que provocó toda la saga. Mejor saberlo ya.
	if after, rerr := os.ReadFile("/etc/fstab"); rerr == nil {
		if !fstabHasMountpoint(string(after), mountPoint) {
			logMsg("appendFstab: WARNING — entry for %s NOT found after write; pool won't auto-mount on reboot", mountPoint)
			return
		}
		logMsg("appendFstab: verified entry for %s persisted", mountPoint)

		// systemd cachea fstab. Sin daemon-reload, la entrada recién escrita
		// se ignora hasta el próximo arranque → el pool no se monta aunque la
		// línea ya esté en fstab (segundo bug raíz de la saga 12-13/06: el
		// propio `mount` avisaba "fstab modificado pero systemd usa la versión
		// antigua"). Recargar deja la entrada activa de inmediato.
		if _, ok := runSafe("systemctl", "daemon-reload"); ok {
			logMsg("appendFstab: systemctl daemon-reload OK — fstab entry now active")
		} else {
			logMsg("appendFstab: WARNING — systemctl daemon-reload failed; entry persisted but may need manual reload")
		}
	}
}

// fstabHasMountpoint comprueba si el mountpoint ya existe como CAMPO EXACTO
// (segunda columna) en alguna línea no comentada de fstab. Evita el falso
// positivo de strings.Contains, que matchea substrings de otras rutas.
func fstabHasMountpoint(fstabContent, mountPoint string) bool {
	target := strings.TrimRight(mountPoint, "/")
	for _, line := range strings.Split(fstabContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		// fields[1] es el mountpoint en fstab. Comparación exacta (normalizada).
		if strings.TrimRight(fields[1], "/") == target {
			return true
		}
	}
	return false
}
