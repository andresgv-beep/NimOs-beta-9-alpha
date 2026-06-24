package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// storage_disks.go — Detección de discos con TIPOS FUERTES (Ola 3 del cierre).
//
// Migra detectStorageDisksGo (que devolvía map[string]interface{} y construía
// cada disco como otro map) a structs tipados. El objetivo es ROBUSTEZ, no
// cambiar comportamiento: el JSON que recibe el frontend es byte-a-byte idéntico
// al de la versión map (verificado por tests de equivalencia).
//
// Principio rector (DISCIPLINE): tipar SIN cambiar semántica. La clasificación
// (eligible/usb/nvme/provisioned), los heurísticos (pendrive = usb+removable+
// <10GB, etc.) y el contrato JSON quedan EXACTAMENTE igual. Las decisiones de
// producto pendientes (NVMe elegible, regla de pendrive) son un frente separado.
//
// Detalle de fidelidad del contrato: los campos de partición que lsblk puede
// devolver como null (fstype, label, mountpoint, y name) se mantienen como
// interface{} con el valor crudo, para preservar null↔null en el JSON (un string
// emitiría "" y rompería la equivalencia). Los campos del disco que el código
// siempre normaliza a string (model/serial con TrimSpace, fstype con
// normalizeFstype) sí son string.
// ─────────────────────────────────────────────────────────────────────────────

// DiskClass enumera las categorías de clasificación. SEMÁNTICA CONGELADA:
// replica exactamente la del código map. NO se cambia en este refactor.
type DiskClass string

const (
	DiskClassEligible    DiskClass = "eligible"
	DiskClassUSB         DiskClass = "usb"
	DiskClassNVMe        DiskClass = "nvme"
	DiskClassProvisioned DiskClass = "provisioned"
)

// DetectedPartition es una partición hija de un disco. name/fstype/label/
// mountpoint son interface{} para preservar el null de lsblk en el JSON.
type DetectedPartition struct {
	Name       interface{} `json:"name"`
	Path       string      `json:"path"`
	Size       int64       `json:"size"`
	Fstype     interface{} `json:"fstype"`
	Label      interface{} `json:"label"`
	Mountpoint interface{} `json:"mountpoint"`
}

// DetectedDisk es un disco físico detectado por el escaneo de hardware.
// Los tags json preservan el camelCase EXACTO del contrato (sizeFormatted,
// hasExistingData, isBoot). smartStatus/smart solo aparecen en eligible
// (omitempty + puntero/asignación condicional replican que el map solo añadía
// esas keys en la rama eligible).
type DetectedDisk struct {
	Name            string              `json:"name"`
	Path            string              `json:"path"`
	Model           string              `json:"model"`
	Serial          string              `json:"serial"`
	Size            int64               `json:"size"`
	SizeFormatted   string              `json:"sizeFormatted"`
	Transport       string              `json:"transport"`
	Rotational      bool                `json:"rotational"`
	Removable       bool                `json:"removable"`
	IsBoot          bool                `json:"isBoot"`
	Partitions      []DetectedPartition `json:"partitions"`
	HasExistingData bool                `json:"hasExistingData"`
	Fstype          string              `json:"fstype"`
	Classification  DiskClass           `json:"classification"`

	// Solo presentes en eligible. omitempty + el puntero hacen que, en las
	// otras categorías, estas keys NO aparezcan (igual que el map, que solo
	// las añadía en la rama eligible).
	SmartStatus string                 `json:"smartStatus,omitempty"`
	Smart       map[string]interface{} `json:"smart,omitempty"`
}

// DiskScanResult es la respuesta completa del escaneo. Las cuatro listas se
// inicializan vacías (nunca null) para reproducir el JSON del map.
type DiskScanResult struct {
	Eligible    []DetectedDisk `json:"eligible"`
	NVMe        []DetectedDisk `json:"nvme"`
	USB         []DetectedDisk `json:"usb"`
	Provisioned []DetectedDisk `json:"provisioned"`
}

// detectStorageDisks es la versión tipada de detectStorageDisksGo. Misma lógica,
// mismos heurísticos, mismo JSON. Hace el I/O (lsblk + service) y delega el
// parsing en parseDetectedDisks (pura, testeable).
func detectStorageDisks() DiskScanResult {
	lsblkRaw, ok := runSafe("lsblk", "-J", "-b", "-o", "NAME,SIZE,TYPE,ROTA,MOUNTPOINT,MODEL,SERIAL,TRAN,RM,FSTYPE,LABEL,PKNAME")
	if !ok || lsblkRaw == "" {
		return emptyDiskScanResult()
	}

	rootDisk := findRootDiskGo(lsblkRaw)

	// Beta 8.1: leer discos asignados a pools vía service v2
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

	return parseDetectedDisks(lsblkRaw, rootDisk, poolDisks)
}

func emptyDiskScanResult() DiskScanResult {
	return DiskScanResult{
		Eligible:    []DetectedDisk{},
		NVMe:        []DetectedDisk{},
		USB:         []DetectedDisk{},
		Provisioned: []DetectedDisk{},
	}
}

// parseDetectedDisks contiene TODA la lógica de clasificación y construcción.
// Pura: no toca hardware ni el service. Dado el JSON crudo de lsblk, el disco
// root y el set de discos ya asignados a pools, produce el DiskScanResult.
// Esto la hace testeable con inputs simulados (red de seguridad del refactor).
func parseDetectedDisks(lsblkRaw, rootDisk string, poolDisks map[string]bool) DiskScanResult {
	result := emptyDiskScanResult()

	var data struct {
		BlockDevices []json.RawMessage `json:"blockdevices"`
	}
	if json.Unmarshal([]byte(lsblkRaw), &data) != nil {
		return result
	}

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

		disk := DetectedDisk{
			Name:          devName,
			Path:          "/dev/" + devName,
			Model:         strings.TrimSpace(model),
			Serial:        strings.TrimSpace(serial),
			Size:          size,
			SizeFormatted: formatBytes(size),
			Transport:     transport,
			Rotational:    rotaBool,
			Removable:     removableBool,
			IsBoot:        devName == rootDisk,
			Partitions:    []DetectedPartition{},
		}

		// Parse partitions — valores crudos para preservar null en el JSON.
		var partitions []DetectedPartition
		if children, ok := dev["children"].([]interface{}); ok {
			for _, child := range children {
				cm, ok := child.(map[string]interface{})
				if !ok {
					continue
				}
				partSize := jsonToInt64(cm["size"])
				partitions = append(partitions, DetectedPartition{
					Name:       cm["name"],
					Path:       "/dev/" + fmt.Sprintf("%v", cm["name"]),
					Size:       partSize,
					Fstype:     cm["fstype"],
					Label:      cm["label"],
					Mountpoint: cm["mountpoint"],
				})
			}
		}
		if partitions == nil {
			partitions = []DetectedPartition{}
		}
		disk.Partitions = partitions

		// hasExistingData: ver comentario extenso en la versión original. Un
		// disco con FS a disco completo (sin tabla de particiones) cuenta como
		// "con datos" — es cómo BTRFS crea miembros de pool.
		diskFstype := strings.TrimSpace(fmt.Sprintf("%v", dev["fstype"]))
		disk.HasExistingData = diskHasExistingData(len(partitions), diskFstype)
		disk.Fstype = normalizeFstype(diskFstype)

		// SMART desde cache (lightweight — sin llamada a smartctl). Se lee para
		// TODOS los discos, no solo los eligible: un disco PROVISIONED (en un
		// pool) es justamente el que más importa monitorizar. Antes esto vivía
		// tras los `continue` de clasificación, así que provisioned/usb/nvme
		// salían SIN estado SMART y se mostraban sanos por defecto — el disco
		// dañado dentro de un pool era invisible. (bug de la auditoría)
		smartStatus, smartDetails := getSmartDetailsForDisk(devName)
		disk.SmartStatus = smartStatus
		disk.Smart = map[string]interface{}{
			"temperature":        smartDetails.Temperature,
			"powerOnHours":       smartDetails.PowerOnHours,
			"pendingSectors":     smartDetails.PendingSectors,
			"uncorrectable":      smartDetails.Uncorrectable,
			"reallocatedSectors": smartDetails.ReallocatedSectors,
		}

		// Classify (orden EXACTO del original)
		if devName == rootDisk {
			continue // boot disk — never show
		}

		if poolDisks["/dev/"+devName] {
			disk.Classification = DiskClassProvisioned
			result.Provisioned = append(result.Provisioned, disk)
			continue
		}

		// USB pendrive: USB + removable + < 10GB
		if transport == "usb" && removableBool && size < 10*1024*1024*1024 {
			disk.Classification = DiskClassUSB
			result.USB = append(result.USB, disk)
			continue
		}

		// NVMe that isn't boot
		if strings.HasPrefix(devName, "nvme") {
			disk.Classification = DiskClassNVMe
			result.NVMe = append(result.NVMe, disk)
			continue
		}

		// Everything else is eligible
		disk.Classification = DiskClassEligible
		result.Eligible = append(result.Eligible, disk)
	}

	return result
}
