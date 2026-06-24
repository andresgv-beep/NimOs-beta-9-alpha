package main

// storage_btrfs_probe.go — Wrappers de comandos del sistema para el observer.
//
// Objetivo: aislar la ejecución de `btrfs filesystem show`, `btrfs filesystem
// usage`, `blkid`, `lsblk` y `/proc/self/mounts` detrás de funciones puras.
//
// Esto permite:
//   · Mockear en tests sin hacer comandos reales
//   · Cambiar la implementación sin tocar el observer
//   · Razonar sobre I/O en un solo sitio
//
// Diseño en docs/storage_observer_design.md sección 4.

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fingerprint barato — detecta si el estado físico ha cambiado
// ─────────────────────────────────────────────────────────────────────────────
//
// Compute objetivo: <10ms incluso en sistemas con 50+ discos.
//
// Fuentes baratas:
//   1. /sys/block — directorio, sin spawn de procesos
//   2. /proc/self/mounts — lectura de archivo
//   3. /run/blkid/blkid.tab — stat del archivo (no su contenido)
//
// Si todas estas no cambian, podemos asumir que el observed state no cambió
// significativamente y saltamos el scan caro.

func computeFingerprint() [32]byte {
	h := sha256.New()

	// 1. Block devices: nombres ordenados de /sys/block
	if entries, err := os.ReadDir("/sys/block"); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			// Excluir devices virtuales que no nos interesan
			if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "zram") {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		h.Write([]byte("block:"))
		h.Write([]byte(strings.Join(names, ",")))
	}

	// 2. Mount table
	if data, err := os.ReadFile("/proc/self/mounts"); err == nil {
		// Filtrar líneas BTRFS y mount points en /nimos/ para reducir ruido
		// de cgroups, /tmp, etc. que cambian sin afectar storage.
		var relevant []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "btrfs") || strings.Contains(line, "/nimos/") {
				relevant = append(relevant, line)
			}
		}
		sort.Strings(relevant)
		h.Write([]byte("mounts:"))
		h.Write([]byte(strings.Join(relevant, "\n")))
	}

	// 3. blkid cache mtime
	if st, err := os.Stat("/run/blkid/blkid.tab"); err == nil {
		h.Write([]byte("blkid:"))
		h.Write([]byte(st.ModTime().Format(time.RFC3339Nano)))
	}

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// BTRFS probe
// ─────────────────────────────────────────────────────────────────────────────

// probeBtrfsFilesystems descubre todos los filesystems BTRFS del sistema.
//
// Usa `btrfs filesystem show` que enumera filesystems registrados en el
// kernel (montados o no). Para cada uno, parseamos:
//   · Label
//   · UUID
//   · Lista de devices (con paths reales)
//   · "Total devices" expected
//   · "missing" devices si los hay
//
// Después enriquecemos cada FS con:
//   · Mount status via findmnt
//   · Profile via btrfs filesystem df (si está montado)
//   · Capacity via btrfs filesystem usage (si está montado)
//   · IO errors via btrfs device stats
//
// Si btrfs no responde, devuelve ([], false). El observer lo marca como
// can_probe=false en lugar de panic.
func probeBtrfsFilesystems() ([]ObservedBtrfs, bool) {
	out, ok := runSafe("btrfs", "filesystem", "show", "--raw")
	if !ok {
		return nil, false
	}

	var results []ObservedBtrfs
	var current *ObservedBtrfs

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// "Label: 'datos1'  uuid: 884ec939-..."
		if strings.HasPrefix(line, "Label:") {
			if current != nil {
				results = append(results, *current)
			}
			current = &ObservedBtrfs{
				CanProbe: true,
				LastSeen: time.Now().UTC(),
			}
			// Parse label
			if idx := strings.Index(line, "'"); idx >= 0 {
				rest := line[idx+1:]
				if end := strings.Index(rest, "'"); end >= 0 {
					current.Label = rest[:end]
				}
			}
			// Parse UUID
			if idx := strings.Index(line, "uuid:"); idx >= 0 {
				rest := strings.TrimSpace(line[idx+len("uuid:"):])
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					current.UUID = fields[0]
				}
			}
			continue
		}

		if current == nil {
			continue
		}

		// "Total devices 2 FS bytes used 552.00KiB"
		if strings.HasPrefix(line, "Total devices") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				if n, err := strconv.Atoi(fields[2]); err == nil {
					current.DevicesExpected = n
				}
			}
			continue
		}

		// "devid 1 size 111.79GiB used 2.01GiB path /dev/sda"
		// "*** Some devices missing"
		if strings.HasPrefix(line, "devid") {
			dev := parseDevidLine(line)
			if dev != nil {
				dev.InFS = current.UUID
				dev.Present = true
				current.Devices = append(current.Devices, *dev)
				current.DevicesOnline++
			}
			continue
		}

		if strings.Contains(line, "Some devices missing") {
			// El recuento exacto se calcula al final con Expected - Online
		}
	}
	if current != nil {
		results = append(results, *current)
	}

	// Enriquecer cada FS con mount + profile + capacity + io errors
	for i := range results {
		enrichBtrfsFilesystem(&results[i])
	}

	return results, true
}

func parseDevidLine(line string) *ObservedDevice {
	// "devid 1 size 111.79GiB used 2.01GiB path /dev/sda"
	// Con --raw: "devid 1 size 120033041920 used 2155872256 path /dev/sda"
	idx := strings.Index(line, "path ")
	if idx < 0 {
		return nil
	}
	path := strings.TrimSpace(line[idx+5:])
	if path == "" {
		return nil
	}

	dev := &ObservedDevice{Path: path}

	// Parse size (raw bytes con --raw)
	if i := strings.Index(line, "size "); i >= 0 {
		rest := line[i+5:]
		if end := strings.Index(rest, " "); end > 0 {
			sizeStr := strings.TrimSpace(rest[:end])
			if n, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
				dev.SizeBytes = n
			}
		}
	}

	return dev
}

func enrichBtrfsFilesystem(fs *ObservedBtrfs) {
	if fs.UUID == "" {
		return
	}

	// Missing devices = expected - online
	if fs.DevicesExpected > fs.DevicesOnline {
		fs.DevicesMissing = fs.DevicesExpected - fs.DevicesOnline
	}

	// ¿Está montado? Buscar via findmnt UUID-based
	mountOut, ok := runSafe("findmnt", "-n", "-S", "UUID="+fs.UUID, "-o", "TARGET")
	if ok && strings.TrimSpace(mountOut) != "" {
		fs.IsMounted = true
		fs.MountPoint = strings.TrimSpace(strings.Split(mountOut, "\n")[0])
		fs.HasMountPoint = true
	}

	// Si está montado, enriquecer con profile + capacity
	if fs.IsMounted {
		enrichBtrfsCapacity(fs)
		enrichBtrfsProfile(fs)
		enrichBtrfsIOErrors(fs)
	}

	// Determinar by-id paths para los devices (si están disponibles)
	for i := range fs.Devices {
		fs.Devices[i].ByIDPath = resolveByIDPath(fs.Devices[i].Path)
	}

	// ObservationHealth
	fs.ObservationHealth = computeObservationHealth(fs)
}

func enrichBtrfsCapacity(fs *ObservedBtrfs) {
	out, ok := runSafe("btrfs", "filesystem", "usage", "-b", fs.MountPoint)
	if !ok {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Used:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Used:"))
			fs.UsedBytes = parseInt64(val)
		} else if strings.HasPrefix(line, "Free (statfs, df):") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Free (statfs, df):"))
			if idx := strings.Index(val, "("); idx > 0 {
				val = strings.TrimSpace(val[:idx])
			}
			fs.FreeBytes = parseInt64(val)
		}
	}
	if fs.UsedBytes > 0 || fs.FreeBytes > 0 {
		fs.SizeBytes = fs.UsedBytes + fs.FreeBytes
	}
}

func enrichBtrfsProfile(fs *ObservedBtrfs) {
	out, ok := runSafe("btrfs", "filesystem", "df", fs.MountPoint)
	if !ok {
		return
	}
	// Output típico:
	//   Data, RAID1: total=1.00GiB, used=408.00KiB
	//   Metadata, RAID1: total=1.00GiB, used=128.00KiB
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Data,") {
			fs.Profile = parseProfileFromDfLine(line)
		} else if strings.HasPrefix(line, "Metadata,") {
			fs.MetaProfile = parseProfileFromDfLine(line)
		}
	}
}

// parseProfileFromDfLine extrae el profile de una línea "Data, RAID1: ..."
// devolviendo "raid1" lowercase.
func parseProfileFromDfLine(line string) string {
	// "Data, RAID1: ..."  → tomar lo entre "," y ":"
	comma := strings.Index(line, ",")
	colon := strings.Index(line, ":")
	if comma < 0 || colon < 0 || comma >= colon {
		return ""
	}
	profile := strings.TrimSpace(line[comma+1 : colon])
	return strings.ToLower(profile)
}

func enrichBtrfsIOErrors(fs *ObservedBtrfs) {
	for i := range fs.Devices {
		// btrfs device stats /dev/sda
		out, ok := runSafe("btrfs", "device", "stats", fs.Devices[i].Path)
		if !ok {
			continue
		}
		var deviceErrs int64
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					deviceErrs += n
				}
			}
		}
		fs.Devices[i].IOErrors = deviceErrs
		fs.IOErrorCount += deviceErrs
	}
}

// resolveByIDPath busca el symlink en /dev/disk/by-id/ que apunta al path dado.
// Devuelve "" si no encuentra (sin /dev/disk/by-id/, sandbox, etc.).
func resolveByIDPath(devPath string) string {
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return ""
	}
	target := strings.TrimPrefix(devPath, "/dev/")
	for _, e := range entries {
		linkPath := "/dev/disk/by-id/" + e.Name()
		resolved, err := filepath.EvalSymlinks(linkPath)
		if err != nil {
			continue
		}
		if strings.TrimPrefix(resolved, "/dev/") == target {
			return linkPath
		}
	}
	return ""
}

// computeObservationHealth determina el ObservationHealth basado en el
// estado del FS. Función pura (testeable sin mocks).
func computeObservationHealth(fs *ObservedBtrfs) HealthStatus {
	if !fs.CanProbe {
		return HealthUnknown
	}
	if fs.DevicesExpected > 0 && fs.DevicesOnline < fs.DevicesExpected {
		return HealthIncomplete
	}
	if fs.IOErrorCount > 0 {
		return HealthDegraded
	}
	if !fs.IsMounted && fs.DevicesOnline > 0 {
		// Tenemos los devices, pero no monta. Estado raro.
		return HealthPartial
	}
	return HealthHealthy
}

// ─────────────────────────────────────────────────────────────────────────────
// Loose devices probe
// ─────────────────────────────────────────────────────────────────────────────

// probeLooseDevices devuelve discos físicos que NO pertenecen a ningún
// filesystem BTRFS observado. Útil para crear pools nuevos.
//
// fsesByDevice mapa: path device → UUID del FS (para excluir los usados).
func probeLooseDevices(fsByDevice map[string]string) []ObservedDevice {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var loose []ObservedDevice
	for _, e := range entries {
		name := e.Name()
		// Filtrar virtuales y el disco del sistema (root)
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "mmcblk") {
			continue
		}
		path := "/dev/" + name

		if _, inFS := fsByDevice[path]; inFS {
			continue
		}

		dev := ObservedDevice{
			Path:    path,
			Present: true,
		}

		// Size
		if data, err := os.ReadFile("/sys/block/" + name + "/size"); err == nil {
			if sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				dev.SizeBytes = sectors * 512
			}
		}

		// by-id path
		dev.ByIDPath = resolveByIDPath(path)

		loose = append(loose, dev)
	}

	return loose
}
