package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Storage — BTRFS Pool Create & Destroy
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// ─── Destroy Pool BTRFS ──────────────────────────────────────────────────────

func destroyPoolBtrfs(poolName string) map[string]interface{} {
	storageMu.Lock()
	defer storageMu.Unlock()

	poolLocked[poolName] = true
	defer delete(poolLocked, poolName)

	// NOTA Beta 8.1: la antigua guarda canDestroyPool (que consultaba la
	// tabla service_dependencies) se eliminó. Falló en silencio en producción
	// (28/05/2026): la tabla estaba desincronizada → devolvía "sin deps" aunque
	// había containers vivos sobre el pool. Storage no consulta metadatos de
	// otros módulos; la protección real es poolHasSubmounts (mira el kernel).

	// ── Buscar pool vía service v2 (Beta 8.1) ──
	if storageService == nil {
		return map[string]interface{}{"error": "storage service not initialized"}
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("listing pools: %s", err)}
	}
	var targetPool *Pool
	for _, p := range pools {
		if p.Name == poolName {
			targetPool = p
			break
		}
	}
	if targetPool == nil {
		return map[string]interface{}{"error": fmt.Sprintf(`Pool "%s" not found`, poolName)}
	}

	// STOR-05: validar política antes de destruir. La ruta legacy (la que usa
	// el frontend) no pasaba por la capa Service, así que se saltaba el
	// checkPolicy — en concreto, la verificación de que el pool es `managed`
	// y no `observed`. Sin esto, se podría destruir un pool que NimOS solo
	// observa. Reutilizamos el policy del service (no duplicamos lógica).
	if storageService != nil {
		if err := storageService.checkPolicy(targetPool, OpTypeDestroyPool); err != nil {
			return map[string]interface{}{
				"error":   ErrCodePoolObserved,
				"message": "No se puede destruir este pool: no está gestionado por NimOS (estado observado) o la operación no está permitida.",
			}
		}
	}

	mountPoint := targetPool.MountPoint
	opts := CmdOptions{Timeout: 30 * time.Second}

	logMsg("Destroying BTRFS pool '%s' (mount: %s)", poolName, mountPoint)

	// 1. Pre-check: ¿hay filesystems montados ENCIMA del pool?
	// Storage solo mira el estado real del filesystem, no consulta otros
	// módulos. Si hay submounts (overlays Docker, binds...) abortamos antes
	// de wipear nada — destruir con submounts vivos corrompe y deja fantasma.
	if mountPoint != "" && poolHasSubmounts(mountPoint, opts) {
		logMsg("Destroy pool '%s' ABORTED: submounts activos encima de %s", poolName, mountPoint)
		return map[string]interface{}{
			"error":   "pool_busy_submounts",
			"message": "El pool tiene filesystems montados encima (servicios activos usándolo). Deténlos antes de destruir.",
		}
	}

	// 2. Unmount limpio — SIN lazy. Si falla, abortamos ANTES de wipear.
	// Wipear un disco montado corrompe el FS vivo y deja la BD inconsistente.
	if mountPoint != "" {
		if errMap := unmountStrict(mountPoint, opts); errMap != nil {
			return errMap
		}
	}

	// 3. A partir de aquí el filesystem está 100% desmontado y confirmado.
	// Ahora es seguro destruir: limpiar mountpoint, fstab, y wipear discos.
	deleteSharesForPool(poolName, mountPoint)

	if mountPoint != "" && strings.HasPrefix(mountPoint, nimosPoolsDir) {
		os.RemoveAll(mountPoint)
	}
	removeFstabEntry(mountPoint)

	// Release BTRFS multi-device lock and wipe disks
	runCmd("btrfs", []string{"device", "scan", "--forget"}, opts)
	for _, dev := range targetPool.Devices {
		if dev.CurrentPath != "" {
			runCmd("wipefs", []string{"-af", dev.CurrentPath}, opts)
		}
	}

	// 4. Remove pool from SQLite (Beta 8.1 Bloque C: sin adapter)
	// Si era primary, transferir flag al primer pool restante o limpiar metadata.
	// La lógica transaccional vive en removePoolFromDB (testeada en isolation).
	ctx := context.Background()
	err = removePoolFromDB(ctx, storageService, targetPool.ID)
	if err != nil {
		logMsg("destroyPoolBtrfs: SQLite update failed: %v", err)
		return map[string]interface{}{"error": fmt.Sprintf("DB update failed: %s", err)}
	}

	// 7. Rescan
	runCmd("partprobe", nil, opts)
	rescanSCSIBuses()

	// 8. Clean orphans
	cleanOrphanPoolDirs()

	logMsg("BTRFS pool '%s' destroyed", poolName)
	updateTorrentConfig()

	// Bloque C2: notificar al observer — el pool ya no está, los discos
	// vuelven a ser loose devices.
	notifyStorageChanged()

	// Clean up service registry for this pool
	dbServiceDeleteByPool(poolName)

	return map[string]interface{}{"ok": true}
}

// exportPoolBtrfs unmounts a BTRFS pool without wiping disks.
func exportPoolBtrfs(poolName string) map[string]interface{} {
	storageMu.Lock()
	defer storageMu.Unlock()

	// NOTA Beta 8.1: canDestroyPool eliminada (ver destroyPoolBtrfs). La
	// protección real es poolHasSubmounts, que mira el kernel en vez de una
	// tabla de la DB que puede estar desincronizada.

	// ── Buscar pool vía service v2 (Beta 8.1) ──
	if storageService == nil {
		return map[string]interface{}{"error": "storage service not initialized"}
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("listing pools: %s", err)}
	}
	var targetPool *Pool
	for _, p := range pools {
		if p.Name == poolName {
			targetPool = p
			break
		}
	}
	if targetPool == nil {
		return map[string]interface{}{"error": fmt.Sprintf(`Pool "%s" not found`, poolName)}
	}

	mountPoint := targetPool.MountPoint
	opts := CmdOptions{Timeout: 30 * time.Second}

	logMsg("Exporting BTRFS pool '%s' — data preserved", poolName)

	// 1. Pre-check: ¿hay filesystems montados ENCIMA del pool?
	// Storage no consulta a Docker ni a servicios (cada módulo es responsable
	// de lo suyo). Solo comprueba el estado real del filesystem: si hay
	// submounts (overlays Docker, binds, etc.) el umount fallaría y dejaría
	// un pool fantasma. Abortamos limpio antes de tocar nada.
	if mountPoint != "" && poolHasSubmounts(mountPoint, opts) {
		logMsg("Export pool '%s' ABORTED: submounts activos encima de %s", poolName, mountPoint)
		return map[string]interface{}{
			"error":   "pool_busy_submounts",
			"message": "El pool tiene filesystems montados encima (servicios activos usándolo). Deténlos antes de desmontar.",
		}
	}

	// 2. Unmount limpio — SIN lazy. Si falla, abortamos sin tocar la BD.
	// El lazy (umount -l) miente: desmonta del namespace pero deja los inodos
	// vivos, generando el estado fantasma. Preferimos fallar claro.
	if mountPoint != "" {
		if errMap := unmountStrict(mountPoint, opts); errMap != nil {
			return errMap
		}
	}

	// 3. A partir de aquí el filesystem está 100% desmontado y confirmado.
	// Ahora SÍ es seguro tocar la BD y el fstab.
	deleteSharesForPool(poolName, mountPoint)
	removeFstabEntry(mountPoint)

	// 4. Remove pool from SQLite (Beta 8.1 Bloque C: sin adapter)
	ctx := context.Background()
	err = removePoolFromDB(ctx, storageService, targetPool.ID)
	if err != nil {
		logMsg("exportPoolBtrfs: SQLite update failed: %v", err)
		return map[string]interface{}{"error": fmt.Sprintf("DB update failed: %s", err)}
	}

	dbServiceDeleteByPool(poolName)
	logMsg("BTRFS pool '%s' exported — data preserved, re-import via Restaurar volumen", poolName)
	updateTorrentConfig()

	// Bloque C2: notificar al observer — el pool desaparece del managed
	// pero el filesystem sigue en disco. Pasará a ser orphan_filesystem
	// en el próximo snapshot.
	notifyStorageChanged()

	return map[string]interface{}{"ok": true}
}

// ─── Helpers internos ────────────────────────────────────────────────────────

// poolHasSubmounts comprueba si hay filesystems montados POR ENCIMA del
// mountpoint del pool (overlays de Docker, bind mounts, etc.).
//
// Filosofía Storage: este módulo NO consulta a Docker ni al registro de
// servicios — cada módulo es responsable de lo suyo. Solo mira el estado
// real del kernel vía findmnt. Si algo tiene montado un FS encima del pool,
// el umount fallaría y dejaría un pool fantasma; mejor abortar antes.
//
// findmnt -R lista el mountpoint y todos sus submounts recursivamente.
// Un submount REAL tiene un target que cuelga del pool: empieza por
// "<mountPoint>/". El propio pool es exactamente "<mountPoint>" (sin nada
// detrás). Contamos solo los hijos reales — NO líneas crudas.
//
// Por qué no contar líneas: findmnt puede partir una sola entrada en varias
// líneas de salida (opciones largas, formato del terminal), produciendo un
// falso positivo que bloqueaba el desmontaje de un pool sin submounts reales.
// (bug encontrado en hardware: pool de 1 solo mount marcado como busy)
func poolHasSubmounts(mountPoint string, opts CmdOptions) bool {
	res, err := runCmd("findmnt", []string{"-R", "-n", "-o", "TARGET", mountPoint}, opts)
	if err != nil {
		// findmnt falla si el mountpoint no está montado → sin submounts.
		return false
	}
	return countRealSubmounts(res.Stdout, mountPoint) > 0
}

// countRealSubmounts cuenta cuántos targets del output de findmnt son hijos
// reales de mountPoint (empiezan por "mountPoint/"). El propio pool y cualquier
// línea de continuación/ruido no cuentan. Función pura para test.
func countRealSubmounts(findmntOutput, mountPoint string) int {
	prefix := strings.TrimRight(mountPoint, "/") + "/"
	n := 0
	for _, l := range strings.Split(findmntOutput, "\n") {
		target := strings.TrimSpace(l)
		if target == "" {
			continue
		}
		// Solo cuenta si el target cuelga DEL pool (es un submount real).
		// Excluye el propio pool (target == mountPoint) y cualquier línea
		// que no sea una ruta absoluta hija.
		if strings.HasPrefix(target, prefix) {
			n++
		}
	}
	return n
}

// unmountStrict desmonta el pool SIN lazy unmount y verifica el resultado.
// Devuelve nil si el umount fue 100% confirmado, o un map de error listo
// para devolver al caller si quedó montado.
//
// Por qué NO lazy: `umount -l` desmonta del namespace pero deja los inodos
// vivos si hay procesos con FDs abiertos. El filesystem sigue recibiendo
// escrituras mientras NimOS cree que ya no existe — el bug fantasma. Si el
// umount limpio falla, preferimos error claro a inconsistencia silenciosa.
func unmountStrict(mountPoint string, opts CmdOptions) map[string]interface{} {
	umountRes, umountErr := runCmd("umount", []string{"-f", mountPoint}, opts)
	time.Sleep(1 * time.Second)

	// Verificación post-unmount obligatoria: findmnt debe devolver vacío.
	verifyRes, _ := runCmd("findmnt", []string{"-n", "-o", "TARGET", mountPoint}, opts)
	if strings.TrimSpace(verifyRes.Stdout) != "" {
		// El stderr del kernel es ORO para diagnosticar — no tirarlo.
		// (bug 13/06: el umount del daemon fallaba, el manual funcionaba,
		// y sin el stderr era imposible saber por qué)
		kernelErr := strings.TrimSpace(umountRes.Stderr)
		if kernelErr == "" && umountErr != nil {
			kernelErr = umountErr.Error()
		}
		logMsg("unmountStrict: %s sigue montado tras umount -f — abortando (sin lazy). umount stderr: %q", mountPoint, kernelErr)

		// Diagnóstico best-effort: ¿qué procesos lo retienen?
		// fuser sale por stderr; -m lista PIDs con archivos abiertos en el FS.
		holders := ""
		if fuserRes, ferr := runCmd("fuser", []string{"-vm", mountPoint}, CmdOptions{Timeout: 5 * time.Second}); ferr == nil || fuserRes.Stderr != "" {
			holders = strings.TrimSpace(fuserRes.Stderr)
			if holders != "" {
				logMsg("unmountStrict: procesos reteniendo %s:\n%s", mountPoint, holders)
			}
		}

		msg := "El pool sigue montado tras intentar desmontarlo."
		if kernelErr != "" {
			msg += " Error del sistema: " + kernelErr + "."
		}
		if holders != "" {
			msg += " Procesos con archivos abiertos: ver log del daemon."
		}
		msg += " Detén los servicios que usan este pool e inténtalo de nuevo."

		return map[string]interface{}{
			"error":   "unmount_failed",
			"message": msg,
			"detail":  kernelErr,
		}
	}
	return nil
}

// removePoolFromDB elimina el pool de SQLite y reasigna o limpia la metadata
// `primary_pool` de forma atómica. Es el "core transaccional" compartido por
// destroyPoolBtrfs y exportPoolBtrfs.
//
// Semántica:
//   - DELETE del pool (CASCADE limpia pool_devices y capabilities).
//   - Si el pool destruido era el primary_pool actual:
//   - Busca otro pool en estado 'managed'. Si existe → transfiere primary.
//   - Si no hay otro managed → borra primary_pool y configured_at.
//   - Toda la operación es atómica vía runInTx. Cualquier fallo intermedio
//     hace rollback completo (CRIT-2 fix: errores ya no se ignoran).
//
// Pre-condiciones:
//   - svc != nil
//   - poolID es el ID en DB del pool a borrar (no el nombre).
//
// Post-condiciones (en caso de éxito):
//   - El pool ya no existe en storage_pools.
//   - storage_metadata.primary_pool apunta a un pool real o no existe.
//
// Esta función NO toca disco, ni shell, ni observer. Sólo DB.
// Eso la hace 100% testeable con DB temporal (ver storage_btrfs_pool_test.go).
func removePoolFromDB(ctx context.Context, svc *StorageService, poolID string) error {
	return svc.runInTx(ctx, func(tx *sql.Tx) error {
		// Borrar el pool (CASCADE limpia pool_devices, capabilities)
		if err := svc.repo.DeletePool(ctx, tx, poolID); err != nil {
			return err
		}

		// Si era el primario, transferir a otro pool managed o limpiar metadata.
		// CRIT-2 fix: propagamos errores DB para que la tx haga rollback
		// si algo falla. Antes se ignoraban con `_`, lo que podía dejar
		// primary_pool apuntando a un pool inexistente tras un commit
		// "exitoso" pero parcial.
		var currentPrimaryID string
		if err := tx.QueryRowContext(ctx,
			`SELECT value FROM storage_metadata WHERE key = 'primary_pool'`).Scan(&currentPrimaryID); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read primary_pool: %w", err)
		}

		// Si este pool no era el primary, no hay nada más que hacer.
		if currentPrimaryID != poolID {
			return nil
		}

		// Buscar otro pool managed para transferir el primary.
		var newPrimaryID string
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM storage_pools WHERE control_state = 'managed' LIMIT 1`).Scan(&newPrimaryID); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("find new primary: %w", err)
		}

		if newPrimaryID != "" {
			// Hay otro pool managed: transferir primary.
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO storage_metadata (key, value) VALUES ('primary_pool', ?)`, newPrimaryID); err != nil {
				return fmt.Errorf("transfer primary_pool: %w", err)
			}
			return nil
		}

		// No quedan pools managed: limpiar metadata.
		if _, err := tx.ExecContext(ctx, `DELETE FROM storage_metadata WHERE key = 'primary_pool'`); err != nil {
			return fmt.Errorf("delete primary_pool: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM storage_metadata WHERE key = 'configured_at'`); err != nil {
			return fmt.Errorf("delete configured_at: %w", err)
		}
		return nil
	})
}
