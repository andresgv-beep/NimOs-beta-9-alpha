// storage_device_state.go — Fuente ÚNICA de verdad del estado de un device.
//
// PROBLEMA QUE RESUELVE:
// El estado de un disco se calculaba en varios sitios (enrichPool, la tabla de
// discos, el health) con lógicas distintas. Podían DIVERGIR: el incidente real
// fue el health diciendo "degraded" mientras la tabla decía "ok" sobre el mismo
// disco ausente. Eso destruye la confianza en el NAS.
//
// resolveDeviceState es la ÚNICA función que decide el estado de un device.
// Todas las vistas (tabla del pool, resumen, widget, health) deben usarla. Si
// dice "missing", NINGUNA vista puede decir "ok" — imposible contradecirse.
//
// PRINCIPIO (Regla 16): la realidad del kernel manda. Un device cuyo disco no
// está presente es "missing", pase lo que pase su SMART cacheado.

package main

// DeviceState es el estado canónico de un device de pool.
type DeviceHealthState string

const (
	DeviceHealthPresent  DeviceHealthState = "present"  // existe y responde
	DeviceHealthMissing  DeviceHealthState = "missing"  // no está físicamente
	DeviceHealthSwapped  DeviceHealthState = "swapped"  // el path existe pero es OTRO disco (serial distinto)
	DeviceHealthCritical DeviceHealthState = "critical" // presente pero SMART crítico
	DeviceHealthWarning  DeviceHealthState = "warning"  // presente pero SMART advierte
)

// resolveDeviceState decide el estado canónico de un device. Orden de prioridad:
// la AUSENCIA y la IDENTIDAD ganan al SMART (un disco ausente nunca es "ok",
// aunque su SMART cacheado lo diga).
//
//  1. ¿El path existe pero es OTRO disco (serial distinto)? → swapped
//  2. ¿El disco no está presente?                          → missing
//  3. ¿Presente pero SMART crítico/warning?                → critical/warning
//  4. Presente y sano                                      → present
//
// smartStatus es el estado SMART cacheado del disco (de getSmartDetailsForDisk).
// Inyectable vía deviceIsPresent/readDeviceSerial (ya existentes).
func resolveDeviceState(d *Device, smartStatus string) DeviceHealthState {
	// 1+2. Identidad y presencia primero — mandan sobre el SMART.
	if d.CurrentPath != "" && devicePathExists(d.CurrentPath) {
		// El path existe: ¿es el MISMO disco? (serial es la identidad real)
		if d.Serial != "" {
			actual := readDeviceSerial(d.CurrentPath)
			if actual != "" && actual != d.Serial {
				// El path existe pero es otro disco → el registrado fue
				// reemplazado sin pasar por NimOS.
				return DeviceHealthSwapped
			}
		}
	} else if !deviceIsPresent(d) {
		// Ni path ni by-id → el disco no está.
		return DeviceHealthMissing
	}

	// 3. Presente: el SMART decide si está sano o no.
	switch smartStatus {
	case "critical", "failed":
		return DeviceHealthCritical
	case "warning", "warn":
		return DeviceHealthWarning
	case "missing":
		// Defensa: si el SMART venía marcado missing de un cálculo previo pero
		// el disco SÍ está presente, prevalece la realidad (present).
		return DeviceHealthPresent
	}

	// 4. Presente y sin alertas SMART.
	return DeviceHealthPresent
}

// deviceStateToSmartStatus traduce el estado canónico al campo SmartStatus que
// la UI ya consume (compatibilidad con las vistas actuales mientras migran).
// Así resolveDeviceState es la fuente, y SmartStatus es su proyección.
func deviceStateToSmartStatus(st DeviceHealthState, originalSmart string) string {
	switch st {
	case DeviceHealthMissing:
		return "missing"
	case DeviceHealthSwapped:
		return "swapped"
	case DeviceHealthCritical:
		return "critical"
	case DeviceHealthWarning:
		return "warning"
	case DeviceHealthPresent:
		// Presente: conservar el SMART real si era informativo (ok), o "ok".
		if originalSmart != "" && originalSmart != "missing" && originalSmart != "swapped" {
			return originalSmart
		}
		return "ok"
	}
	return originalSmart
}
