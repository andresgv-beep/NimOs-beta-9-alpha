package main

import (
	"encoding/json"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tests de comportamiento del escaneo de discos.
//
// NOTA HISTÓRICA: aquí vivía un test de EQUIVALENCIA que garantizaba que la
// versión tipada (parseDetectedDisks) producía el mismo JSON que el map original
// (parseDetectedDisksLegacy). Cumplió su función en la Ola 3. Pero el fix de
// SMART (auditoría: discos en pool sin estado SMART) cambia el contrato A
// PROPÓSITO: ahora TODOS los discos llevan smart/smartStatus, no solo eligible.
// El legacy reproducía el bug, así que ya no es un oráculo válido. Estos tests
// verifican el COMPORTAMIENTO CORRECTO, no la equivalencia con el código viejo.
// ─────────────────────────────────────────────────────────────────────────────

// hasSmartFields indica si un disco serializado incluye smart + smartStatus.
func hasSmartFields(d DetectedDisk) bool {
	b, _ := json.Marshal(d)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	_, hasSmart := m["smart"]
	_, hasStatus := m["smartStatus"]
	return hasSmart && hasStatus
}

// TODOS los discos visibles deben llevar estado SMART, sea cual sea su categoría.
// Este es el fix de la auditoría: antes solo eligible lo tenía, y un disco
// dañado dentro de un pool (provisioned) era invisible.
func TestDisksSmartPresentInAllCategories(t *testing.T) {
	lsblk := `{"blockdevices":[
		{"name":"sdb","size":2000398934016,"type":"disk","rota":true,"tran":"sata","rm":false,"fstype":"btrfs","model":"Pool","serial":"P1"},
		{"name":"sdc","size":1000204886016,"type":"disk","rota":true,"tran":"sata","rm":false,"model":"Free","serial":"F1"},
		{"name":"sdd","size":8000000000,"type":"disk","rota":false,"tran":"usb","rm":true,"fstype":"vfat","model":"USB","serial":"U1"},
		{"name":"nvme0n1","size":512110190592,"type":"disk","rota":false,"tran":"nvme","rm":false,"model":"NVMe","serial":"N1"}
	]}`
	r := parseDetectedDisks(lsblk, "", map[string]bool{"P1": true})

	for _, d := range r.Provisioned {
		if !hasSmartFields(d) {
			t.Errorf("disco provisioned %s sin campos SMART (el bug que arreglamos)", d.Name)
		}
	}
	for _, d := range r.USB {
		if !hasSmartFields(d) {
			t.Errorf("disco usb %s sin campos SMART", d.Name)
		}
	}
	for _, d := range r.NVMe {
		if !hasSmartFields(d) {
			t.Errorf("disco nvme %s sin campos SMART", d.Name)
		}
	}
	for _, d := range r.Eligible {
		if !hasSmartFields(d) {
			t.Errorf("disco eligible %s sin campos SMART", d.Name)
		}
	}
	// Sanity: la clasificación sigue siendo correcta.
	if len(r.Provisioned) != 1 || r.Provisioned[0].Name != "sdb" {
		t.Errorf("sdb debería ser provisioned, got %+v", r.Provisioned)
	}
}

// La clasificación en sí no cambió con el fix de SMART.
func TestDisksClassificationUnchanged(t *testing.T) {
	lsblk := `{"blockdevices":[
		{"name":"sda","size":256060514304,"type":"disk","tran":"sata","rm":false,
		 "children":[{"name":"sda1","size":256060514304,"fstype":"ext4","mountpoint":"/"}]},
		{"name":"sdb","size":2000398934016,"type":"disk","tran":"sata","rm":false}
	]}`
	r := parseDetectedDisks(lsblk, "sda", map[string]bool{})
	if len(r.Eligible) != 1 || r.Eligible[0].Name != "sdb" {
		t.Errorf("sdb eligible esperado, got %+v", r.Eligible)
	}
	// sda es boot → no aparece en ninguna categoría.
	total := len(r.Eligible) + len(r.USB) + len(r.NVMe) + len(r.Provisioned)
	if total != 1 {
		t.Errorf("solo sdb debería aparecer (sda es boot), got %d discos", total)
	}
}
