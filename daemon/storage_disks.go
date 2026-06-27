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

	// Discos asignados a pools, indexados por SERIAL (identidad absoluta), NO por
	// ruta. La ruta (/dev/sdb) es una BAHÍA, no un disco: al cambiar físicamente
	// un disco, el registro viejo conserva su current_path en la BD y colisiona
	// con el disco NUEVO que ahora ocupa esa misma ruta. Indexar por path hacía
	// que el disco nuevo se clasificara como "provisioned" (miembro del pool),
	// desapareciera de "discos libres" y no se pudiera usar para reparar el pool.
	// El serial es la identidad real del firmware: solo es miembro del pool el
	// disco cuyo serial figura en el pool. (Regla 16: el kernel/identidad manda.)
	poolSerials := map[string]bool{}
	if storageService != nil {
		if pools, err := storageService.ListPools(context.Background()); err == nil {
			for _, p := range pools {
				for _, dev := range p.Devices {
					if dev.Serial != "" {
						poolSerials[dev.Serial] = true
					}
				}
			}
		}
	}

	// Disco objetivo de un replace EN CURSO: aún no es miembro del pool en la BD
	// (la membresía se cambia al terminar), pero btrfs ya está reconstruyendo
	// sobre él → NO debe aparecer como "libre". Lo tratamos como en-uso para que
	// salga de la lista de elegibles mientras dura la reconstrucción.
	for s := range inProgressReplaceTargetSerials() {
		poolSerials[s] = true
	}

	return parseDetectedDisks(lsblkRaw, rootDisk, poolSerials)
}

// inProgressReplaceTargetSerials devuelve el conjunto de seriales que son el
// disco NUEVO de una operación de replace pendiente o en curso. Se leen del JSON
// `data` de storage_operations. Vacío si no hay ninguna o ante cualquier error.
func inProgressReplaceTargetSerials() map[string]bool {
	out := map[string]bool{}
	if storageService == nil {
		return out
	}
	rows, err := storageService.repo.db.QueryContext(context.Background(),
		`SELECT data FROM storage_operations WHERE type = ? AND status IN ('pending','in_progress')`,
		string(OpTypeReplaceDevice))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		var d struct {
			NewDeviceSerial string `json:"new_device_serial"`
		}
		if json.Unmarshal([]byte(data), &d) == nil && d.NewDeviceSerial != "" {
			out[d.NewDeviceSerial] = true
		}
	}
	return out
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
// root y el set de SERIALES ya asignados a pools, produce el DiskScanResult.
// Esto la hace testeable con inputs simulados (red de seguridad del refactor).
//
// poolSerials es el conjunto de seriales (identidad de firmware) que pertenecen
// a algún pool — NO rutas. Ver detectStorageDisks para el porqué.
func parseDetectedDisks(lsblkRaw, rootDisk string, poolSerials map[string]bool) DiskScanResult {
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

		// Provisioned por IDENTIDAD (serial), no por ruta: así un disco nuevo
		// que ocupa la bahía de uno reemplazado NO se confunde con el miembro
		// del pool. Sin serial no podemos afirmar pertenencia → no provisioned.
		if disk.Serial != "" && poolSerials[disk.Serial] {
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
