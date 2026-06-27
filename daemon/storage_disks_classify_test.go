package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// Tests de clasificación — CONGELAN la semántica actual.
//
// Verifican que un disco con tales atributos cae en la categoría correcta. Esto
// fija el comportamiento actual para que el refactor (y futuros cambios) no lo
// alteren sin querer. Las decisiones de producto pendientes (NVMe elegible,
// regla de pendrive) se decidirán aparte; mientras tanto, estos tests
// documentan lo que NimOS hace HOY.
// ─────────────────────────────────────────────────────────────────────────────

// classifyOne corre parseDetectedDisks con un solo disco y devuelve en qué
// categoría cayó (o "" si fue filtrado/excluido). poolSerials = seriales que
// pertenecen a algún pool (identidad), no rutas.
func classifyOne(lsblk, rootDisk string, poolSerials map[string]bool) string {
	r := parseDetectedDisks(lsblk, rootDisk, poolSerials)
	switch {
	case len(r.Eligible) == 1:
		return "eligible"
	case len(r.USB) == 1:
		return "usb"
	case len(r.NVMe) == 1:
		return "nvme"
	case len(r.Provisioned) == 1:
		return "provisioned"
	default:
		return ""
	}
}

func TestClassify_SataHddEligible(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"sda","size":2000398934016,"type":"disk","tran":"sata","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "eligible" {
		t.Errorf("SATA HDD: got %q, want eligible", got)
	}
}

func TestClassify_UsbSmallRemovableIsUsb(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"sdb","size":8000000000,"type":"disk","tran":"usb","rm":true}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "usb" {
		t.Errorf("USB pendrive: got %q, want usb", got)
	}
}

func TestClassify_UsbLargeIsEligible(t *testing.T) {
	// USB pero >= 10GB → NO es pendrive, cae en eligible (semántica actual).
	lsblk := `{"blockdevices":[{"name":"sdb","size":500000000000,"type":"disk","tran":"usb","rm":true}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "eligible" {
		t.Errorf("USB grande: got %q, want eligible (regla actual: pendrive solo si <10GB)", got)
	}
}

func TestClassify_UsbNotRemovableIsEligible(t *testing.T) {
	// USB pero no removable → no es pendrive.
	lsblk := `{"blockdevices":[{"name":"sdb","size":8000000000,"type":"disk","tran":"usb","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "eligible" {
		t.Errorf("USB no-removable: got %q, want eligible", got)
	}
}

func TestClassify_NvmeNonBootIsNvme(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"nvme0n1","size":512110190592,"type":"disk","tran":"nvme","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "nvme" {
		t.Errorf("NVMe no-boot: got %q, want nvme", got)
	}
}

func TestClassify_ProvisionedTakesPriority(t *testing.T) {
	// Un disco cuyo SERIAL está en un pool es provisioned aunque sería eligible.
	lsblk := `{"blockdevices":[{"name":"sdc","size":2000398934016,"type":"disk","tran":"sata","rm":false,"serial":"POOLSER1"}]}`
	if got := classifyOne(lsblk, "", map[string]bool{"POOLSER1": true}); got != "provisioned" {
		t.Errorf("disco en pool: got %q, want provisioned", got)
	}
}

// TestClassify_SwappedDiskBecomesEligible — REGRESIÓN del bug del cambio de
// disco. Al sustituir físicamente un disco del pool, el registro viejo conservaba
// su ruta (/dev/sdb) en la BD. La clasificación por RUTA marcaba el disco NUEVO
// (otro serial, misma /dev/sdb) como "provisioned", lo escondía de "discos
// libres" y hacía imposible reemplazarlo por la UI. Clasificando por SERIAL, el
// disco nuevo aparece como eligible y se puede usar para reparar el pool.
func TestClassify_SwappedDiskBecomesEligible(t *testing.T) {
	// El pool conoce al disco VIEJO por su serial.
	poolSerials := map[string]bool{"OLD_SERIAL": true}
	// Pero en /dev/sdb hay ahora un disco NUEVO (otro serial, misma bahía).
	lsblk := `{"blockdevices":[{"name":"sdb","size":4000787030016,"type":"disk","tran":"sata","rm":false,"serial":"NEW_SERIAL"}]}`
	if got := classifyOne(lsblk, "", poolSerials); got != "eligible" {
		t.Errorf("disco nuevo tras swap: got %q, want eligible (no debe esconderse como provisioned)", got)
	}
}

// TestClassify_MemberByPathButDifferentSerialNotProvisioned — variante explícita:
// que coincida la RUTA con un miembro del pool ya no basta; manda el serial.
func TestClassify_DiskWithoutSerialNotProvisioned(t *testing.T) {
	// Un disco sin serial no puede afirmarse como miembro → no provisioned.
	lsblk := `{"blockdevices":[{"name":"sdb","size":4000787030016,"type":"disk","tran":"sata","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{"SOMESER": true}); got != "eligible" {
		t.Errorf("disco sin serial: got %q, want eligible", got)
	}
}

func TestClassify_BootExcluded(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"sda","size":256060514304,"type":"disk","tran":"sata","rm":false}]}`
	if got := classifyOne(lsblk, "sda", map[string]bool{}); got != "" {
		t.Errorf("boot disk: got %q, want excluido", got)
	}
}

func TestClassify_TooSmallFiltered(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"sda","size":500000000,"type":"disk","tran":"sata","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "" {
		t.Errorf("<1GB: got %q, want filtrado", got)
	}
}

func TestClassify_NonWhitelistedPrefixFiltered(t *testing.T) {
	// mmcblk, dm-*, etc. no están en la whitelist sd/nvme/vd.
	lsblk := `{"blockdevices":[{"name":"mmcblk0","size":32000000000,"type":"disk","tran":"","rm":false}]}`
	if got := classifyOne(lsblk, "", map[string]bool{}); got != "" {
		t.Errorf("mmcblk: got %q, want filtrado (no whitelisted)", got)
	}
}
