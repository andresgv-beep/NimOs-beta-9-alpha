package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// readSmartWithFallback intenta leer SMART probando device-types en orden:
// primero el default (auto-detección de smartctl), luego "-d sat" (capa SAT,
// el caso de muchas controladoras USB/SATA), luego "-d ata". Devuelve el primer
// output que contenga una tabla SMART real, el device-type que funcionó (""
// para el default), y ok=false si ninguno sirvió.
//
// El orden importa: el default cubre la mayoría de discos directos; sat cubre
// los que están detrás de traducción SCSI-ATA; ata es el último recurso para
// discos PATA/legacy.
func readSmartWithFallback(safe string) (out string, usedDevType string, ok bool) {
	dev := "/dev/" + safe

	attempts := []struct {
		devType string
		args    []string
	}{
		{"", []string{"-i", "-A", "-H", dev}},
		{"sat", []string{"-d", "sat", "-i", "-A", "-H", dev}},
		{"ata", []string{"-d", "ata", "-i", "-A", "-H", dev}},
	}

	for _, a := range attempts {
		o, runOk := runSafe("smartctl", a.args...)
		if runOk && smartOutputIsUsable(o) {
			return o, a.devType, true
		}
	}
	return "", "", false
}

// smartOutputIsUsable indica si la salida de smartctl contiene datos SMART
// reales (la tabla de atributos o el veredicto de salud), y NO solo el aviso de
// "prueba con -d sat". Función pura para test.
func smartOutputIsUsable(out string) bool {
	if out == "" {
		return false
	}
	// La pista de SAT no es una lectura útil: hay que seguir probando.
	if strings.Contains(out, "behind a SAT layer") ||
		strings.Contains(out, "Try an additional") {
		// Solo descartamos si ADEMÁS no hay tabla real (a veces el aviso sale
		// junto a datos parciales; exigimos señal positiva abajo).
		if !strings.Contains(out, "ATTRIBUTE_NAME") &&
			!strings.Contains(out, "self-assessment test result") {
			return false
		}
	}
	// Señal positiva: o la tabla de atributos, o el veredicto de salud.
	return strings.Contains(out, "ATTRIBUTE_NAME") ||
		strings.Contains(out, "self-assessment test result")
}

func getDiskSmart(diskName string) map[string]interface{} {
	// Sanitize — only allow alphanumeric
	safe := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(diskName, "")
	if safe == "" {
		return map[string]interface{}{"error": "Invalid disk name"}
	}

	result := map[string]interface{}{
		"disk":           safe,
		"healthy":        true,
		"status":         "ok", // ok | warning | critical
		"temperature":    nil,
		"powerOnHours":   nil,
		"powerCycles":    nil,
		"reallocated":    0,
		"pending":        0,
		"uncorrectable":  0,
		"attributes":     []map[string]interface{}{},
		"smartSupported": false,
		"model":          "",
		"serial":         "",
		"firmware":       "",
	}

	if !hasSmartctl {
		result["error"] = "smartctl not installed"
		return result
	}

	// Get SMART info. Algunos discos cuelgan de una capa SAT (SCSI-ATA
	// Translation): controladoras USB/SATA que no exponen el dispositivo ATA
	// directamente. smartctl interactivo auto-detecta y reintenta solo, pero
	// bajo systemd (sin TTY, entorno restringido) esa auto-detección falla y
	// devuelve "Probable ATA device behind a SAT layer / Try -d sat". El
	// resultado era que el monitor no podía leer NINGÚN dato y el disco se
	// marcaba sano por defecto. Probamos device-types en orden hasta que uno
	// devuelva una tabla SMART real.
	out, usedDevType, ok := readSmartWithFallback(safe)
	if !ok || out == "" {
		result["error"] = "Could not read SMART data"
		logMsg("SMART: no se pudo leer /dev/%s con ningún device-type (auto/sat/ata)", safe)
		return result
	}
	if usedDevType != "" {
		result["devType"] = usedDevType
	}

	result["smartSupported"] = true

	// Parse health status
	if strings.Contains(out, "PASSED") {
		result["status"] = "ok"
		result["healthy"] = true
	} else if strings.Contains(out, "FAILED") {
		result["status"] = "critical"
		result["healthy"] = false
	}

	// Parse info section
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Model Family:") || strings.HasPrefix(line, "Device Model:") {
			result["model"] = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
		if strings.HasPrefix(line, "Serial Number:") {
			result["serial"] = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
		if strings.HasPrefix(line, "Firmware Version:") {
			result["firmware"] = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}

	// Parse SMART attributes table
	// Format: "ID# ATTRIBUTE_NAME          FLAG     VALUE WORST THRESH TYPE      UPDATED  WHEN_FAILED RAW_VALUE"
	var attrs []map[string]interface{}
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "ID#") && strings.Contains(line, "ATTRIBUTE_NAME") {
			inTable = true
			continue
		}
		if inTable && strings.TrimSpace(line) == "" {
			break
		}
		if !inTable {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		id := fields[0]
		name := fields[1]
		value := parseIntDefault(fields[3], 0)
		worst := parseIntDefault(fields[4], 0)
		thresh := parseIntDefault(fields[5], 0)
		rawVal := fields[9]
		// Raw values can be: "0", "32 (Min/Max 20/45)", "17551h+03m+04.440s"
		// Extract the leading number
		rawNum := parseRawSmartValue(rawVal)

		// Per-attribute status for the UI table
		// Only mark warning/critical based on REAL problems, not cosmetic thresholds
		attrStatus := "ok"
		if thresh > 0 && value <= thresh && rawNum > 0 {
			attrStatus = "critical"
		} else if thresh > 0 && value <= thresh && rawNum == 0 {
			// Value crossed threshold but raw is 0 — cosmetic, not real failure
			attrStatus = "warning"
		}

		attr := map[string]interface{}{
			"id":     id,
			"name":   name,
			"value":  value,
			"worst":  worst,
			"thresh": thresh,
			"raw":    rawNum,
			"rawStr": rawVal,
			"status": attrStatus,
		}
		attrs = append(attrs, attr)

		// ── Disk-level status escalation ──
		// Philosophy: only alert when the user needs to ACT.
		// Synology/TrueNAS approach: real sector problems and temperature, not
		// historical counters or attributes "near threshold" with raw=0.
		//
		// RED (critical) — act now:
		//   Offline_Uncorrectable > 0, Current_Pending rising, value <= thresh with raw > 0
		// YELLOW (warning) — plan replacement:
		//   Reallocated > 0, Pending > 0, temperature > 50°C sustained
		// NO ALERT:
		//   Reported_Uncorrect (historical counter, only goes up)
		//   Spin_Retry_Count with raw=0 (cosmetic threshold)
		//   End-to-End_Error with raw=0 (cosmetic)
		//   Any attr "near threshold" with raw=0

		switch name {
		case "Temperature_Celsius", "Temperature_Internal", "Airflow_Temperature_Cel":
			result["temperature"] = rawNum
			if rawNum > 55 {
				if result["status"] == "ok" {
					result["status"] = "warning"
				}
			}
		case "Power_On_Hours", "Power_On_Hours_and_Msec":
			result["powerOnHours"] = rawNum
		case "Power_Cycle_Count":
			result["powerCycles"] = rawNum

		// ── These indicate REAL problems — escalate disk status ──
		case "Reallocated_Sector_Ct":
			result["reallocated"] = rawNum
			if rawNum > 0 {
				if result["status"] == "ok" {
					result["status"] = "warning"
				}
			}
		case "Current_Pending_Sector":
			result["pending"] = rawNum
			if rawNum > 0 {
				if result["status"] == "ok" {
					result["status"] = "warning"
				}
			}
		case "Offline_Uncorrectable":
			result["uncorrectable"] = rawNum
			if rawNum > 0 {
				result["status"] = "critical"
				result["healthy"] = false
			}
		case "Reallocated_Event_Count":
			if rawNum > 0 {
				if result["status"] == "ok" {
					result["status"] = "warning"
				}
			}

		// ── These are informational — do NOT escalate disk status ──
		// Reported_Uncorrect: historical ECC counter, only goes up, common on
		// desktop drives used in NAS. Not actionable.
		// Runtime_Bad_Block: low counts are normal wear, not actionable.
		// Spin_Retry_Count: raw=0 means no actual retries.
		// End-to-End_Error: raw=0 means no actual errors.
		// UDMA_CRC_Error_Count: cable issue, not disk failure.
		case "Reported_Uncorrect", "Runtime_Bad_Block", "Spin_Retry_Count",
			"End-to-End_Error", "UDMA_CRC_Error_Count", "Command_Timeout":
			// Tracked but not escalated — informational only
		}
	}

	if attrs == nil {
		attrs = []map[string]interface{}{}
	}
	result["attributes"] = attrs

	return result
}

// parseRawSmartValue extracts the leading integer from SMART raw values
// Handles formats like: "0", "17551h+03m+04.440s", "32 (Min/Max 20/45)", "36"
func parseRawSmartValue(raw string) int {
	if raw == "" {
		return 0
	}
	// Extract leading digits
	numStr := ""
	for _, c := range raw {
		if c >= '0' && c <= '9' {
			numStr += string(c)
		} else {
			break
		}
	}
	if numStr == "" {
		return 0
	}
	n, _ := strconv.Atoi(numStr)
	return n
}

func startSmartMonitor() {
	// Wait for system to be ready
	time.Sleep(30 * time.Second)

	if !hasSmartctl {
		logMsg("SMART monitor: smartctl not available, monitor disabled")
		return
	}

	logMsg("SMART monitor started (interval: 30min)")

	// Initial scan
	checkAllDisksSmart()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		checkAllDisksSmart()
	}
}

func checkAllDisksSmart() {
	// Get all disks from lsblk
	out, ok := runSafe("lsblk", "-d", "-n", "-o", "NAME,TYPE")
	if !ok || out == "" {
		return
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "disk" {
			continue
		}
		diskName := fields[0]

		// Skip loop/ram devices
		if strings.HasPrefix(diskName, "loop") || strings.HasPrefix(diskName, "ram") || strings.HasPrefix(diskName, "zram") {
			continue
		}

		smartResult := getDiskSmart(diskName)
		currentStatus, _ := smartResult["status"].(string)
		if currentStatus == "" {
			// El disco no devolvió un status legible. ANTES esto se tragaba en
			// silencio y la cache quedaba vacía → el disco se mostraba sano por
			// defecto. Ahora lo logueamos: un disco que no se puede leer es
			// información, no un no-evento.
			reason, _ := smartResult["error"].(string)
			if reason == "" {
				reason = "status vacío"
			}
			logMsg("SMART: /dev/%s sin lectura utilizable (%s) — no se actualiza su estado", diskName, reason)
			continue
		}

		// Cache detail metrics for getSmartDetailsForDisk (used by pool health)
		details := SmartDetails{}
		if v, ok := smartResult["reallocated"].(int); ok {
			details.ReallocatedSectors = v
		}
		if v, ok := smartResult["pending"].(int); ok {
			details.PendingSectors = v
		}
		if v, ok := smartResult["uncorrectable"].(int); ok {
			details.Uncorrectable = v
		}
		if v, ok := smartResult["powerOnHours"].(int); ok {
			details.PowerOnHours = v
		}
		if v, ok := smartResult["temperature"].(int); ok {
			details.Temperature = v
		}

		smartMu.Lock()
		prevStatus, existed := smartHistory[diskName]
		smartHistory[diskName] = currentStatus
		smartDetailsCache[diskName] = details
		smartMu.Unlock()

		// Only notify on status changes (not on first scan unless bad)
		if !existed {
			// First scan — only notify if already bad
			if currentStatus == "warning" {
				model, _ := smartResult["model"].(string)
				addNotification("warning", "system",
					fmt.Sprintf("Disco %s requiere atención", diskName),
					fmt.Sprintf("SMART detecta problemas en %s (%s). Revisa la sección Salud.", diskName, model))
			} else if currentStatus == "critical" {
				model, _ := smartResult["model"].(string)
				addNotification("error", "system",
					fmt.Sprintf("Disco %s en riesgo de fallo", diskName),
					fmt.Sprintf("SMART indica errores críticos en %s (%s). Reemplaza el disco lo antes posible.", diskName, model))
			}
			continue
		}

		// Status changed
		if currentStatus != prevStatus {
			model, _ := smartResult["model"].(string)

			switch {
			case currentStatus == "critical" && prevStatus != "critical":
				addNotification("error", "system",
					fmt.Sprintf("Disco %s en riesgo de fallo", diskName),
					fmt.Sprintf("SMART indica errores críticos en %s (%s). Reemplaza el disco lo antes posible.", diskName, model))
				logMsg("SMART CRITICAL: disk %s status changed from %s to critical", diskName, prevStatus)

			case currentStatus == "warning" && prevStatus == "ok":
				addNotification("warning", "system",
					fmt.Sprintf("Disco %s requiere atención", diskName),
					fmt.Sprintf("SMART detecta nuevos problemas en %s (%s). Revisa la sección Salud.", diskName, model))
				logMsg("SMART WARNING: disk %s status changed from ok to warning", diskName)

			case currentStatus == "ok" && prevStatus != "ok":
				addNotification("success", "system",
					fmt.Sprintf("Disco %s recuperado", diskName),
					fmt.Sprintf("SMART de %s (%s) ha vuelto a estado normal.", diskName, model))
				logMsg("SMART OK: disk %s status recovered from %s", diskName, prevStatus)
			}
		}

		// Temperature alert — FIX3: debounce con histéresis.
		// Notifica solo en la transición normal→high, y la recuperación
		// (high→normal) solo cuando baja de tempRecoverC. No re-notifica
		// cada ciclo mientras se mantiene caliente.
		if temp, ok := smartResult["temperature"].(int); ok {
			smartMu.Lock()
			prevTemp := tempHistory[diskName] // "" en primera observación
			newTemp := nextTempState(prevTemp, temp)
			tempHistory[diskName] = newTemp
			smartMu.Unlock()

			if newTemp == "high" && prevTemp != "high" {
				addNotification("warning", "system",
					fmt.Sprintf("Temperatura alta en disco %s", diskName),
					fmt.Sprintf("El disco %s está a %d°C. Verifica la ventilación.", diskName, temp))
				logMsg("SMART TEMP WARNING: disk %s at %d°C", diskName, temp)
			} else if newTemp == "normal" && prevTemp == "high" {
				addNotification("success", "system",
					fmt.Sprintf("Temperatura normalizada en disco %s", diskName),
					fmt.Sprintf("El disco %s ha bajado a %d°C.", diskName, temp))
				logMsg("SMART TEMP OK: disk %s recovered to %d°C", diskName, temp)
			}
		}
	}
}

// nextTempState aplica la histéresis de temperatura. Dado el estado previo
// ("normal"/"high"/"") y la temperatura actual, devuelve el nuevo estado:
//   - sube a "high" al alcanzar tempHighC
//   - baja a "normal" solo por debajo de tempRecoverC
//   - en la banda intermedia (tempRecoverC..tempHighC) mantiene el estado previo
//   - primera observación (prev==""): "high" si ya está caliente, si no "normal"
func nextTempState(prev string, temp int) string {
	switch {
	case temp >= tempHighC:
		return "high"
	case temp < tempRecoverC:
		return "normal"
	default:
		if prev == "" {
			return "normal"
		}
		return prev
	}
}

// getSmartSummary returns a summary of all disks' SMART status
// GET /api/disks/smart/summary
func getSmartSummary() map[string]interface{} {
	smartMu.Lock()
	defer smartMu.Unlock()

	disks := make([]map[string]interface{}, 0)
	worstStatus := "ok"

	for name, status := range smartHistory {
		disks = append(disks, map[string]interface{}{
			"name":   name,
			"status": status,
		})
		if status == "critical" {
			worstStatus = "critical"
		} else if status == "warning" && worstStatus != "critical" {
			worstStatus = "warning"
		}
	}

	return map[string]interface{}{
		"disks":       disks,
		"worstStatus": worstStatus,
		"lastCheck":   time.Now().Format(time.RFC3339),
	}
}
