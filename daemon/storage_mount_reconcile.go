// storage_mount_reconcile.go — Fase R1 · Reconciliación de estado de montaje.
//
// Se ejecuta al arranque del daemon, DESPUÉS de RecoverPendingOperations.
// Para cada pool registrado en la BD, compara el estado REAL del kernel con
// lo esperado, y corrige las divergencias que el caos del 28-31/05 reveló:
//
//   a) Pool no montado          → montar desde fstab
//   b) Pool montado 1x correcto → OK, nada que hacer
//   c) Pool montado N>1 veces   → desapilar hasta dejar 1 (mounts apilados)
//   d) Pool montado en /media/  → desmontar de ahí, montar en /nimos/pools/
//   e) Pool montado read-only   → reportar (no se corrige aquí; ver R2 health)
//
// Filosofía Storage (disciplina §1): este módulo NO consulta Docker ni
// servicios. Solo mira el estado del kernel vía findmnt/mount. Cada módulo
// es responsable de lo suyo.
//
// Idempotente: ejecutar dos veces deja el mismo resultado (la segunda no
// encuentra nada que corregir).

package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MountReconcileResult resume lo que hizo reconcileMountState.
type MountReconcileResult struct {
	Inspected   int // pools examinados
	Mounted     int // pools que estaban sin montar y se montaron
	Unstacked   int // pools con capas apiladas que se desapilaron
	Relocated   int // pools montados en sitio equivocado, remontados
	ReadOnly    int // pools detectados en read-only (reportados, no corregidos)
	AlreadyOK   int // pools ya correctos (1 capa, sitio correcto, rw)
	Failed      int // pools que no se pudieron corregir
}

// reconcileMountState examina cada pool de la BD y corrige divergencias entre
// el estado real del kernel y lo esperado. Llamar UNA vez al arranque, tras
// RecoverPendingOperations.
func reconcileMountState(ctx context.Context) (*MountReconcileResult, error) {
	if storageService == nil {
		return nil, fmt.Errorf("reconcileMountState: storage service not initialized")
	}
	pools, err := storageService.ListPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconcileMountState: list pools: %w", err)
	}

	result := &MountReconcileResult{Inspected: len(pools)}
	if len(pools) == 0 {
		return result, nil
	}

	logMsg("MountReconcile: examinando %d pools", len(pools))
	opts := CmdOptions{Timeout: 30 * time.Second}

	for _, p := range pools {
		if p.MountPoint == "" {
			continue
		}

		layers := countMountLayers(p.MountPoint)
		wrongPlace := mountedInWrongPlace(p)

		switch {
		// Caso d) montado en sitio equivocado (p.ej. /media/ por udisks2)
		case wrongPlace != "":
			logMsg("MountReconcile: pool '%s' montado en sitio incorrecto (%s), reubicando", p.Name, wrongPlace)
			if reconcileRelocate(p, wrongPlace, opts) {
				result.Relocated++
			} else {
				result.Failed++
			}

		// Caso c) capas apiladas
		case layers > 1:
			logMsg("MountReconcile: pool '%s' tiene %d capas apiladas, desapilando", p.Name, layers)
			if reconcileUnstack(p, layers, opts) {
				result.Unstacked++
			} else {
				result.Failed++
			}

		// Caso a) no montado
		case layers == 0:
			logMsg("MountReconcile: pool '%s' no montado, montando desde fstab", p.Name)
			if _, ok := runSafe("mount", p.MountPoint); ok && isPathOnMountedPool(p.MountPoint) {
				result.Mounted++
			} else {
				logMsg("MountReconcile: WARNING — no se pudo montar '%s' en %s", p.Name, p.MountPoint)
				result.Failed++
			}

		// Caso b) 1 capa en sitio correcto
		default:
			// Caso e) detectar read-only (se reporta, no se corrige aquí)
			if isMountReadOnly(p.MountPoint) {
				logMsg("MountReconcile: WARNING — pool '%s' montado READ-ONLY (posibles errores de I/O)", p.Name)
				result.ReadOnly++
			} else {
				result.AlreadyOK++
			}
			createPoolDirs(p.MountPoint)
		}
	}

	logMsg("MountReconcile completado: inspected=%d ok=%d mounted=%d unstacked=%d relocated=%d readonly=%d failed=%d",
		result.Inspected, result.AlreadyOK, result.Mounted, result.Unstacked,
		result.Relocated, result.ReadOnly, result.Failed)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers — solo miran/operan filesystem, sin consultar otros módulos
// ─────────────────────────────────────────────────────────────────────────────

// countMountLayers cuenta cuántas veces está montado algo EXACTAMENTE en
// mountPoint (capas apiladas). 0 = no montado, 1 = normal, >1 = apilado.
func countMountLayers(mountPoint string) int {
	// /proc/mounts es la fuente de verdad del kernel. Contamos líneas cuyo
	// segundo campo (mountpoint) coincide exactamente con mountPoint.
	out, ok := runSafe("cat", "/proc/mounts")
	if !ok {
		return 0
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == mountPoint {
			count++
		}
	}
	return count
}

// mountedInWrongPlace devuelve el path donde el device del pool está montado
// si NO es su MountPoint esperado (p.ej. /media/andres/data7 por udisks2).
// Devuelve "" si no está mal ubicado.
func mountedInWrongPlace(p *Pool) string {
	if p.BtrfsUUID == "" {
		return ""
	}
	// Buscar dónde está montado el FS con ese UUID
	out, ok := runSafe("findmnt", "-n", "-o", "TARGET", "--source", "UUID="+p.BtrfsUUID)
	if !ok || strings.TrimSpace(out) == "" {
		return ""
	}
	for _, target := range strings.Split(strings.TrimSpace(out), "\n") {
		target = strings.TrimSpace(target)
		if target == "" || target == p.MountPoint {
			continue
		}
		// Está montado en un sitio que NO es el esperado
		if !strings.HasPrefix(target, nimosPoolsDir) {
			return target
		}
	}
	return ""
}

// reconcileUnstack desapila las capas extra dejando exactamente 1.
func reconcileUnstack(p *Pool, layers int, opts CmdOptions) bool {
	// Desmontar (layers-1) veces para dejar 1 capa.
	for i := 0; i < layers-1; i++ {
		runCmd("umount", []string{p.MountPoint}, opts)
	}
	remaining := countMountLayers(p.MountPoint)
	if remaining == 1 && isPathOnMountedPool(p.MountPoint) {
		createPoolDirs(p.MountPoint)
		return true
	}
	if remaining == 0 {
		// Nos pasamos — remontar 1 vez
		runSafe("mount", p.MountPoint)
		return isPathOnMountedPool(p.MountPoint)
	}
	logMsg("MountReconcile: '%s' sigue con %d capas tras desapilar", p.Name, remaining)
	return false
}

// reconcileRelocate desmonta el pool del sitio equivocado y lo monta en el
// MountPoint correcto desde fstab.
func reconcileRelocate(p *Pool, wrongPlace string, opts CmdOptions) bool {
	// Desmontar del sitio equivocado (todas las capas que haya allí)
	for i := 0; i < 5; i++ {
		if countMountLayers(wrongPlace) == 0 {
			break
		}
		runCmd("umount", []string{wrongPlace}, opts)
	}
	// Montar en el sitio correcto desde fstab
	runSafe("mount", p.MountPoint)
	return isPathOnMountedPool(p.MountPoint)
}

// isMountReadOnly comprueba si el mountPoint está montado en modo read-only.
func isMountReadOnly(mountPoint string) bool {
	out, ok := runSafe("findmnt", "-n", "-o", "OPTIONS", "--target", mountPoint)
	if !ok {
		return false
	}
	// La primera opción de mount es "ro" o "rw"
	opts := strings.Split(strings.TrimSpace(out), ",")
	for _, o := range opts {
		if o == "ro" {
			return true
		}
	}
	return false
}
