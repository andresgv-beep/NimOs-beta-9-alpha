package main

import "testing"

// deviceIsPresent: la pieza que arregla "disco ausente aparece como ok".
// Casos clave del incidente real de Andrés:
//   - quitó un disco del raid1 → debe detectarse ausente (no "ok")
//   - el path /dev/sdb existe pero es OTRO disco (SSD 256 vs Seagate 320) →
//     el registrado (Seagate) está ausente aunque el path exista

func withStubbedDeviceProbes(pathExists func(string) bool, serial func(string) string, fn func()) {
	origPath := devicePathExists
	origSerial := readDeviceSerial
	devicePathExists = pathExists
	readDeviceSerial = serial
	defer func() {
		devicePathExists = origPath
		readDeviceSerial = origSerial
	}()
	fn()
}

func TestDeviceIsPresent_ByIDExists(t *testing.T) {
	withStubbedDeviceProbes(
		func(p string) bool { return p == "/dev/disk/by-id/ata-XYZ" },
		func(string) string { return "" },
		func() {
			d := &Device{ByIDPath: "/dev/disk/by-id/ata-XYZ", Serial: "ABC"}
			if !deviceIsPresent(d) {
				t.Error("by-id existe → debe estar presente")
			}
		})
}

func TestDeviceIsPresent_RemovedDisk(t *testing.T) {
	// El disco se quitó: ni by-id ni current path existen → ausente.
	withStubbedDeviceProbes(
		func(string) bool { return false },
		func(string) string { return "" },
		func() {
			d := &Device{ByIDPath: "/dev/disk/by-id/ata-OLD", CurrentPath: "/dev/sdb", Serial: "9YG142"}
			if deviceIsPresent(d) {
				t.Error("disco quitado (nada existe) → debe estar AUSENTE")
			}
		})
}

func TestDeviceIsPresent_PathExistsButDifferentDisk(t *testing.T) {
	// EL CASO DE ANDRÉS: /dev/sdb existe, pero ahora es un SSD de 256GB con
	// serial distinto. El disco registrado (Seagate 9YG142) NO está presente.
	withStubbedDeviceProbes(
		func(p string) bool { return p == "/dev/sdb" }, // el path existe
		func(string) string { return "SSD-NEW-256" },   // pero es otro disco
		func() {
			d := &Device{CurrentPath: "/dev/sdb", Serial: "9YG142"} // esperábamos el Seagate
			if deviceIsPresent(d) {
				t.Error("el path existe pero es OTRO disco (serial distinto) → el registrado está AUSENTE")
			}
		})
}

func TestDeviceIsPresent_PathExistsSameSerial(t *testing.T) {
	// El disco está donde debe y es el mismo (serial coincide) → presente.
	withStubbedDeviceProbes(
		func(p string) bool { return p == "/dev/sda" },
		func(string) string { return "PBENIBB24072214942" },
		func() {
			d := &Device{CurrentPath: "/dev/sda", Serial: "PBENIBB24072214942"}
			if !deviceIsPresent(d) {
				t.Error("mismo disco (serial coincide) → presente")
			}
		})
}

func TestDeviceIsPresent_PathExistsNoSerialToCompare(t *testing.T) {
	// Path existe, no hay serial para contrastar → confiamos en el path.
	withStubbedDeviceProbes(
		func(p string) bool { return p == "/dev/sdc" },
		func(string) string { return "" },
		func() {
			d := &Device{CurrentPath: "/dev/sdc", Serial: ""}
			if !deviceIsPresent(d) {
				t.Error("sin serial para contrastar y path existe → presente")
			}
		})
}
