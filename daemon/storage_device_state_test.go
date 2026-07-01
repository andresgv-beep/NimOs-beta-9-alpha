package main

import "testing"

// resolveDeviceState es la fuente ÚNICA de verdad del estado de un device.
// Lo crítico: presencia e identidad GANAN al SMART. Un disco ausente nunca es
// "ok" aunque su SMART cacheado lo diga. Esto mata las contradicciones de la UI.

func withDeviceProbes(present func(*Device) bool, pathExists func(string) bool, serial func(string) string, fn func()) {
	op, ope, os := deviceIsPresent, devicePathExists, readDeviceSerial
	deviceIsPresent, devicePathExists, readDeviceSerial = present, pathExists, serial
	defer func() { deviceIsPresent, devicePathExists, readDeviceSerial = op, ope, os }()
	fn()
}

func TestResolveDeviceState_MissingBeatsOkSmart(t *testing.T) {
	// EL CASO DEL INCIDENTE: disco ausente, pero SMART cacheado dice "ok".
	// La presencia manda → missing, NO ok.
	withDeviceProbes(
		func(*Device) bool { return false }, // no presente
		func(string) bool { return false },  // path no existe
		func(string) string { return "" },
		func() {
			d := &Device{CurrentPath: "/dev/sdb", Serial: "9YG142"}
			st := resolveDeviceState(d, "ok") // SMART dice ok...
			if st != DeviceHealthMissing {
				t.Errorf("disco ausente con SMART 'ok' debe ser missing, got %s", st)
			}
		})
}

func TestResolveDeviceState_SwappedDisk(t *testing.T) {
	// El path existe pero es OTRO disco (serial distinto) → swapped.
	withDeviceProbes(
		func(*Device) bool { return true },
		func(string) bool { return true },        // path existe
		func(string) string { return "SSD-NEW" }, // pero otro serial
		func() {
			d := &Device{CurrentPath: "/dev/sdb", Serial: "9YG142"}
			st := resolveDeviceState(d, "ok")
			if st != DeviceHealthSwapped {
				t.Errorf("disco cambiado debe ser swapped, got %s", st)
			}
		})
}

func TestResolveDeviceState_PresentCritical(t *testing.T) {
	// Presente y SMART crítico → critical (el del x86 con el 4TB malo).
	withDeviceProbes(
		func(*Device) bool { return true },
		func(string) bool { return true },
		func(string) string { return "SAME" },
		func() {
			d := &Device{CurrentPath: "/dev/sda", Serial: "SAME"}
			st := resolveDeviceState(d, "critical")
			if st != DeviceHealthCritical {
				t.Errorf("disco presente con SMART critical debe ser critical, got %s", st)
			}
		})
}

func TestResolveDeviceState_PresentHealthy(t *testing.T) {
	withDeviceProbes(
		func(*Device) bool { return true },
		func(string) bool { return true },
		func(string) string { return "SAME" },
		func() {
			d := &Device{CurrentPath: "/dev/sda", Serial: "SAME"}
			st := resolveDeviceState(d, "ok")
			if st != DeviceHealthPresent {
				t.Errorf("disco sano debe ser present, got %s", st)
			}
		})
}

// La proyección a SmartStatus (lo que consume la UI hoy) es coherente con el
// estado canónico: un disco missing NUNCA proyecta "ok".
func TestDeviceStateToSmartStatus_NoFalseOk(t *testing.T) {
	if s := deviceStateToSmartStatus(DeviceHealthMissing, "ok"); s != "missing" {
		t.Errorf("missing no puede proyectar a 'ok', got %s", s)
	}
	if s := deviceStateToSmartStatus(DeviceHealthSwapped, "ok"); s != "swapped" {
		t.Errorf("swapped debe proyectar swapped, got %s", s)
	}
	if s := deviceStateToSmartStatus(DeviceHealthPresent, "ok"); s != "ok" {
		t.Errorf("present con smart ok proyecta ok, got %s", s)
	}
	if s := deviceStateToSmartStatus(DeviceHealthCritical, "critical"); s != "critical" {
		t.Errorf("critical proyecta critical, got %s", s)
	}
}
