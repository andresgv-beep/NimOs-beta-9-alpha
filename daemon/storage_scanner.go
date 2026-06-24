// storage_scanner.go — DeviceScanner: detecta discos físicos del sistema.
//
// El scanner se encarga de:
//   1. Listar todos los block devices (vía lsblk -J)
//   2. Filtrar a discos reales (no particiones, no virtuales, > 1GB)
//   3. Resolver el by-id-path de cada disco (en /dev/disk/by-id/)
//   4. Identificar el boot disk para excluirlo
//
// NO escribe a la DB. Solo escanea y devuelve la lista. La persistencia
// la hace StorageService.ScanDevices llamando a repo.UpsertDevice.
//
// Abstracción via DeviceScanner para mockear en tests sin lsblk real.
//
// see docs/storage_invariants.md#3 (identidad por serial)
// see docs/storage_state_machines.md §5 (Device lifecycle)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// DeviceScanner interface
// ─────────────────────────────────────────────────────────────────────────────

// DeviceScanner devuelve los discos físicos elegibles para storage.
// Los tests usan MockDeviceScanner con datos predefinidos.
type DeviceScanner interface {
	// ScanDevices lista los discos físicos del sistema. Excluye:
	//   - particiones (devType != "disk")
	//   - prefijos no estándar (no sd*, nvme*, vd*)
	//   - discos < 1GB
	//   - boot disk
	ScanDevices(ctx context.Context) ([]ScannedDevice, error)
}

// ScannedDevice es la representación cruda de un disco tras escanear.
// Contiene SOLO datos observables (no clasificación, no inferencia).
type ScannedDevice struct {
	Name        string // sd, nvme0n1, etc.
	DevicePath  string // /dev/sdb
	ByIDPath    string // /dev/disk/by-id/ata-... (vacío si no existe)
	Serial      string // del firmware
	Model       string
	WWN         string // World Wide Name (puede ser vacío)
	SizeBytes   int64
	Transport   string // sata, nvme, usb, virtio, ...
	Rotational  bool
	Removable   bool
}

// ─────────────────────────────────────────────────────────────────────────────
// LsblkDeviceScanner — implementación real basada en lsblk
// ─────────────────────────────────────────────────────────────────────────────

// LsblkDeviceScanner usa lsblk + /dev/disk/by-id/ para descubrir discos.
type LsblkDeviceScanner struct{}

// NewLsblkDeviceScanner crea el scanner con defaults sensatos.
func NewLsblkDeviceScanner() *LsblkDeviceScanner {
	return &LsblkDeviceScanner{}
}

func (s *LsblkDeviceScanner) ScanDevices(ctx context.Context) ([]ScannedDevice, error) {
	// 1. Identificar el boot disk para excluirlo
	bootDisk := findRootDeviceName()

	// 2. Ejecutar lsblk con JSON output
	cmd := exec.CommandContext(ctx, "lsblk", "-J", "-b",
		"-o", "NAME,SIZE,TYPE,ROTA,MOUNTPOINT,MODEL,SERIAL,TRAN,RM,WWN")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("LsblkDeviceScanner: lsblk failed: %w", err)
	}

	var parsed struct {
		BlockDevices []struct {
			Name       string  `json:"name"`
			Size       int64   `json:"size"`
			Type       string  `json:"type"`
			Rota       bool    `json:"rota"`
			MountPoint *string `json:"mountpoint"`
			Model      string  `json:"model"`
			Serial     string  `json:"serial"`
			Tran       string  `json:"tran"`
			Rm         bool    `json:"rm"`
			WWN        string  `json:"wwn"`
		} `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("LsblkDeviceScanner: parse JSON: %w", err)
	}

	// 3. Mapear /dev/disk/by-id/ → device path real (símlinks)
	byIDMap, err := buildByIDMap()
	if err != nil {
		// No fatal: si no podemos resolver by-id, devolvemos lista
		// con ByIDPath vacío. El service lo rechazará al persistir.
		logMsg("LsblkDeviceScanner: warning, cannot build by-id map: %v", err)
		byIDMap = map[string]string{}
	}

	// 4. Filtrar y construir lista
	devices := []ScannedDevice{}
	for _, d := range parsed.BlockDevices {
		// Solo "disk" (no particiones, no virtuales, no raid)
		if d.Type != "disk" {
			continue
		}

		// Whitelist de prefijos conocidos
		if !isStorageDeviceName(d.Name) {
			continue
		}

		// Filtrar tamaños absurdos
		if d.Size < 1*1024*1024*1024 {
			continue
		}

		// Excluir boot disk
		if d.Name == bootDisk {
			continue
		}

		// Serial obligatorio (storage_invariants.md#3.3)
		if strings.TrimSpace(d.Serial) == "" {
			logMsg("LsblkDeviceScanner: skipping %s (no serial)", d.Name)
			continue
		}

		devicePath := "/dev/" + d.Name
		byIDPath := byIDMap[devicePath]

		devices = append(devices, ScannedDevice{
			Name:       d.Name,
			DevicePath: devicePath,
			ByIDPath:   byIDPath,
			Serial:     strings.TrimSpace(d.Serial),
			Model:      strings.TrimSpace(d.Model),
			WWN:        strings.TrimSpace(d.WWN),
			SizeBytes:  d.Size,
			Transport:  d.Tran,
			Rotational: d.Rota,
			Removable:  d.Rm,
		})
	}

	return devices, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MockDeviceScanner — para tests
// ─────────────────────────────────────────────────────────────────────────────

// MockDeviceScanner devuelve una lista predefinida de devices.
// Útil en tests para no depender de lsblk ni hardware real.
type MockDeviceScanner struct {
	Devices []ScannedDevice
	Err     error
	calls   atomic.Int64
}

func NewMockDeviceScanner(devs []ScannedDevice) *MockDeviceScanner {
	return &MockDeviceScanner{Devices: devs}
}

func (m *MockDeviceScanner) ScanDevices(ctx context.Context) ([]ScannedDevice, error) {
	m.calls.Add(1)
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Devices, nil
}

// CallCount devuelve cuántas veces se ha llamado ScanDevices. Útil para
// tests que verifican comportamiento sin scans inesperados.
func (m *MockDeviceScanner) CallCount() int64 {
	return m.calls.Load()
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers internos
// ─────────────────────────────────────────────────────────────────────────────

// isStorageDeviceName devuelve true si el name corresponde a un device
// de storage tradicional (sd*, nvme*, vd*).
func isStorageDeviceName(name string) bool {
	for _, prefix := range []string{"sd", "nvme", "vd", "hd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// findRootDeviceName devuelve el name (sin /dev/) del disco padre que
// contiene la partición montada en "/". Vacío si no se puede determinar.
func findRootDeviceName() string {
	// Estrategia: leer /proc/mounts, encontrar device del mount "/",
	// quitar dígitos finales para obtener el disco padre (sda1 → sda).
	mounted := findRootMountedDevice()
	if mounted == "" {
		return ""
	}
	// /dev/sda1 → sda
	base := filepath.Base(mounted)
	// Para NVMe: nvme0n1p1 → nvme0n1
	if strings.HasPrefix(base, "nvme") {
		// Encontrar "p" seguido de dígitos al final
		if idx := strings.LastIndex(base, "p"); idx > 0 {
			suffix := base[idx+1:]
			isDigits := suffix != ""
			for _, c := range suffix {
				if c < '0' || c > '9' {
					isDigits = false
					break
				}
			}
			if isDigits {
				return base[:idx]
			}
		}
		return base
	}
	// Para sd*, vd*: quitar dígitos del final
	return strings.TrimRight(base, "0123456789")
}

// findRootMountedDevice devuelve el device path montado en "/".
func findRootMountedDevice() string {
	data, err := readFile("/proc/mounts")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "/" {
			return fields[0]
		}
	}
	return ""
}

// buildByIDMap construye un mapa device_path → by_id_path.
// Recorre /dev/disk/by-id/ y resuelve los symlinks.
// Si dos by-id apuntan al mismo device, prefiere el que NO contiene "wwn"
// (los wwn-* son menos legibles que los ata-*, scsi-* o nvme-*).
func buildByIDMap() (map[string]string, error) {
	const byIDDir = "/dev/disk/by-id"
	entries, err := readDir(byIDDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", byIDDir, err)
	}

	result := map[string]string{}
	for _, name := range entries {
		// Excluir entradas de particiones (-part1, -part2, etc.)
		if strings.Contains(name, "-part") {
			continue
		}
		fullByID := byIDDir + "/" + name
		realPath, err := filepath.EvalSymlinks(fullByID)
		if err != nil {
			continue
		}

		existing, ok := result[realPath]
		if !ok {
			result[realPath] = fullByID
			continue
		}
		// Si ya hay uno, preferimos ata-/scsi-/nvme- sobre wwn-
		existingIsWWN := strings.HasPrefix(filepath.Base(existing), "wwn-")
		newIsWWN := strings.HasPrefix(name, "wwn-")
		if existingIsWWN && !newIsWWN {
			result[realPath] = fullByID
		}
	}
	return result, nil
}

// readFile y readDir son helpers de bajo nivel separados para testabilidad.
// La impl real usa os.ReadFile / os.ReadDir. En tests se reasignan.

var readFile = func(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var readDir = func(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}
