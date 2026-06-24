package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Storage Health Monitor — bucle proactivo único (Ola 1 del cierre de storage)
//
// Reemplaza el viejo checkStorageHealthGo (que SOLO miraba UsagePercent) por un
// único bucle que cada ciclo evalúa, por pool:
//
//   · FIX1 + P6 — Salud del pool vía buildPoolHealth (degraded/critical con el
//     margen por profile YA calculado por ComputePoolHealth) → notifica SOLO en
//     transiciones de estado, replicando el patrón probado del SMART monitor.
//   · P1        — Espacio sin asignar (unallocated) por device → alerta ENOSPC
//     de metadata, precursor del read-only silencioso de BTRFS.
//
// Principio rector (igual que el SMART monitor): notificar en TRANSICIONES de
// estado, no en cada lectura. Esto da dedupe natural y captura recoveries.
//
// NO cambia la detección (CollectDiagnostics/ComputePoolHealth ya detectan bien)
// — solo conecta la detección al canal de notificaciones existente.
// ─────────────────────────────────────────────────────────────────────────────

// Umbral de unallocated por device por debajo del cual se considera riesgo de
// ENOSPC de metadata. BTRFS necesita poder asignar nuevos chunks de metadata; si
// el unallocated cae cerca de cero, el pool puede pasar a read-only de golpe.
// 1 GiB da margen para avisar antes del bloqueo.
const unallocatedCriticalBytes int64 = 1 << 30 // 1 GiB

// Estado previo notificado por pool (healthy/at_risk/unstable/degraded/critical).
// Permite notificar solo en transiciones, como smartHistory en el SMART monitor.
var (
	poolHealthMu      sync.Mutex
	poolHealthHistory = map[string]string{}
	// unallocatedHistory: estado previo de espacio sin asignar por pool
	// ("ok" | "critical"). Evita spamear la alerta ENOSPC cada ciclo.
	unallocatedHistory = map[string]string{}
)

// runStorageHealthCheck ejecuta UNA pasada del bucle proactivo. Llamada cada
// ciclo desde startStorageMonitoring. También recalcula storageAlertsGo (las
// alertas de capacidad en memoria que consume el HTTP) para no perder esa vía.
func runStorageHealthCheck() {
	if storageService == nil {
		return
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return
	}

	// Mantener storageAlertsGo (capacidad %) — vía legacy que el HTTP consume.
	checkStorageHealthGo()

	for _, p := range pools {
		if p.MountPoint == "" {
			continue
		}
		// Solo evaluamos pools realmente montados; un pool desmontado lo
		// gestiona el reconciler de mount, no este bucle de salud.
		if !isPoolMounted(p.MountPoint) {
			continue
		}

		checkPoolHealthTransition(p)
		checkPoolUnallocated(p)
	}
}

// checkPoolHealthTransition computa la salud del pool con el motor existente y
// notifica solo si el status cambió respecto al último ciclo. (FIX1 + P6)
func checkPoolHealthTransition(p *Pool) {
	configDisks := make([]string, 0, len(p.Devices))
	for _, d := range p.Devices {
		name := strings.TrimPrefix(d.CurrentPath, "/dev/")
		if name != "" {
			configDisks = append(configDisks, name)
		}
	}
	if len(configDisks) == 0 {
		return
	}

	health := buildPoolHealth(DiagnosticInput{
		PoolType:    "btrfs",
		VdevType:    btrfsVdevTypeForProfile(string(p.Profile)),
		ConfigDisks: configDisks,
		MountPoint:  p.MountPoint,
	})
	current := health.Status

	poolHealthMu.Lock()
	prevStatus, existed := poolHealthHistory[p.Name]
	poolHealthHistory[p.Name] = current
	poolHealthMu.Unlock()

	if shouldNotifyHealth(prevStatus, existed, current) {
		notifyPoolHealth(p, prevStatus, current, health)
	}
}

// shouldNotifyHealth decide si una transición de estado debe notificar.
// Política (igual que el SMART monitor): notificar solo en transiciones.
//   - primera observación: notificar solo si NO nace healthy (evita ruido al boot)
//   - observaciones siguientes: notificar solo si el estado cambió
func shouldNotifyHealth(prev string, existed bool, current string) bool {
	if !existed {
		return current != "healthy"
	}
	return current != prev
}

// notifyPoolHealth emite la notificación adecuada según la transición. El mensaje
// reusa health.Reason.Message, que YA incluye el margen por profile (p.ej.
// "Sin redundancia — 1 de 2 discos" para raid1, "puede perder N discos más"
// para raid1c3/raid10).
func notifyPoolHealth(p *Pool, prev, current string, health PoolHealth) {
	reason := strings.TrimSpace(health.Reason.Message)

	switch current {
	case "critical":
		msg := reason
		if msg == "" {
			msg = "Estado crítico — datos en riesgo. Revisa la sección Salud."
		}
		addNotification("error", "system",
			fmt.Sprintf("Pool %s en estado crítico", p.Name), msg)
		logMsg("HEALTH CRITICAL: pool %s %s→critical (%s)", p.Name, prev, reason)

	case "degraded":
		msg := reason
		if msg == "" {
			msg = "Pool degradado. Revisa la sección Salud."
		}
		addNotification("error", "system",
			fmt.Sprintf("Pool %s degradado", p.Name), msg)
		logMsg("HEALTH DEGRADED: pool %s %s→degraded (%s)", p.Name, prev, reason)

	case "unstable", "at_risk":
		msg := reason
		if msg == "" {
			msg = "El pool muestra señales de inestabilidad. Revisa la sección Salud."
		}
		addNotification("warning", "system",
			fmt.Sprintf("Pool %s requiere atención", p.Name), msg)
		logMsg("HEALTH %s: pool %s %s→%s (%s)", strings.ToUpper(current), p.Name, prev, current, reason)

	case "healthy":
		// Recovery: solo notificar si veníamos de un estado malo.
		if prev != "" && prev != "healthy" {
			addNotification("success", "system",
				fmt.Sprintf("Pool %s recuperado", p.Name),
				fmt.Sprintf("El pool %s ha vuelto a estado saludable.", p.Name))
			logMsg("HEALTH OK: pool %s recovered from %s", p.Name, prev)
		}
	}
}

// checkPoolUnallocated lee el espacio sin asignar por device y alerta cuando
// el unallocated EFECTIVO (según el perfil del pool) cae por debajo del umbral
// crítico — precursor del ENOSPC de metadata. (P1)
//
// El unallocated efectivo NO es el mínimo por device: un chunk nuevo de metadata
// en RAID1/RAID1C3/RAID10 necesita espacio en N devices SIMULTÁNEAMENTE, así que
// la métrica correcta depende del perfil (ver effectiveUnallocated). Usar el
// mínimo daba falsos positivos en arrays asimétricos (p. ej. RAID1 de 8TB+1TB:
// el device pequeño marca el mínimo pero aún hay margen real para metadata).
func checkPoolUnallocated(p *Pool) {
	byDev, ok := readUnallocatedByDevice(p.MountPoint)
	if !ok {
		return // no se pudo leer; no inventamos estado
	}
	effective := effectiveUnallocated(p.Profile, byDev)

	state := "ok"
	if effective < unallocatedCriticalBytes {
		state = "critical"
	}

	poolHealthMu.Lock()
	prev, existed := unallocatedHistory[p.Name]
	unallocatedHistory[p.Name] = state
	poolHealthMu.Unlock()

	// Notificar solo al entrar en estado crítico (transición ok→critical o
	// primera observación ya crítica) y al recuperar (critical→ok).
	if state == "critical" && (!existed || prev != "critical") {
		addNotification("error", "system",
			fmt.Sprintf("Pool %s: espacio sin asignar crítico", p.Name),
			fmt.Sprintf("%s: solo quedan %s de margen efectivo (perfil %s). Riesgo de bloqueo en solo-lectura (ENOSPC de metadata). Ejecuta un balance (btrfs balance -dusage=N) para recuperar chunks.",
				p.Name, humanBytes(effective), p.Profile))
		logMsg("HEALTH ENOSPC: pool %s unallocated efectivo crítico (%d bytes, perfil %s)", p.Name, effective, p.Profile)
	} else if state == "ok" && existed && prev == "critical" {
		addNotification("success", "system",
			fmt.Sprintf("Pool %s: espacio sin asignar recuperado", p.Name),
			fmt.Sprintf("%s vuelve a tener margen de espacio sin asignar (%s efectivo).", p.Name, humanBytes(effective)))
		logMsg("HEALTH ENOSPC OK: pool %s unallocated efectivo recuperado (%d bytes)", p.Name, effective)
	}
}

// effectiveUnallocated calcula el espacio sin asignar realmente disponible para
// un chunk NUEVO de metadata, según el perfil del pool. Un chunk de metadata con
// N copias necesita unallocated en N devices a la vez, así que el margen real es
// el N-ésimo mayor unallocated (no el total ni el mínimo).
//
//	single  → min (conservador: garantiza el peor caso, evita falsos negativos)
//	raid1   → 2º mayor   (2 copias)
//	raid1c3 → 3er mayor  (3 copias)
//	raid10  → menor de los 4 mayores (stripe sobre mirrors, mínimo 4 devices)
//
// Si hay menos devices de los que el perfil exige (estado degradado), devuelve
// 0: no se puede asignar un chunk redundante nuevo, que es la verdad.
func effectiveUnallocated(profile Profile, byDev map[string]int64) int64 {
	vals := make([]int64, 0, len(byDev))
	for _, v := range byDev {
		vals = append(vals, v)
	}
	if len(vals) == 0 {
		return 0
	}
	// Orden descendente: vals[0] es el mayor unallocated.
	sort.Slice(vals, func(i, j int) bool { return vals[i] > vals[j] })

	// nth devuelve el k-ésimo mayor (1-indexed), o 0 si no hay tantos devices.
	nth := func(k int) int64 {
		if len(vals) < k {
			return 0
		}
		return vals[k-1]
	}

	switch profile {
	case ProfileRaid1:
		return nth(2)
	case ProfileRaid1c3:
		return nth(3)
	case ProfileRaid10:
		// raid10 necesita ≥4 devices; el limitante es el menor de los 4 mayores.
		return nth(4)
	case ProfileSingle:
		fallthrough
	default:
		// single (y fallback seguro): el mínimo entre devices.
		min := vals[0]
		for _, v := range vals {
			if v < min {
				min = v
			}
		}
		return min
	}
}

// readUnallocatedByDevice ejecuta `btrfs filesystem usage -b <mp>` y devuelve el
// unallocated por device. Devuelve (map, true) si pudo leer al menos un device;
// (nil, false) si el comando falló o no había líneas Unallocated.
func readUnallocatedByDevice(mountPoint string) (map[string]int64, bool) {
	out, ok := runSafe("btrfs", "filesystem", "usage", "-b", mountPoint)
	if !ok {
		return nil, false
	}
	return parseUnallocatedByDevice(out)
}

// parseUnallocatedByDevice extrae el unallocated por device del output de
// `btrfs filesystem usage -b`. La sección final lista, por device, una cabecera
// "/dev/sdX, ID: N" seguida de líneas indentadas, entre ellas "Unallocated:".
// Separado para testearlo sin ejecutar btrfs.
//
//	/dev/sda, ID: 1
//	   Device size:            120034123776
//	   Unallocated:            113558118400
//	/dev/sdb, ID: 2
//	   Device size:            120034123776
//	   Unallocated:               536870912
func parseUnallocatedByDevice(usageOutput string) (map[string]int64, bool) {
	byDev := map[string]int64{}
	currentDev := ""
	for _, line := range strings.Split(usageOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		// Cabecera de device: empieza por "/dev/" y contiene ", ID:".
		if strings.HasPrefix(trimmed, "/dev/") && strings.Contains(trimmed, "ID:") {
			// "/dev/sda, ID: 1" → "/dev/sda"
			currentDev = strings.TrimSpace(strings.SplitN(trimmed, ",", 2)[0])
			continue
		}
		if strings.HasPrefix(trimmed, "Unallocated:") && currentDev != "" {
			byDev[currentDev] = parseTrailingInt(trimmed)
			currentDev = ""
		}
	}
	if len(byDev) == 0 {
		return nil, false
	}
	return byDev, true
}

// humanBytes formatea bytes de forma legible para los mensajes de notificación.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
