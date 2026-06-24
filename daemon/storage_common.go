package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Storage — Common helpers shared by ZFS and BTRFS pool operations
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── Config Helpers ──────────────────────────────────────────────────────────

// deleteSharesForPool removes all shares associated with a pool from the DB.
func deleteSharesForPool(poolName, mountPoint string) {
	shares, _ := dbSharesListRaw()
	for _, s := range shares {
		if s.Pool == poolName || s.Volume == poolName || (mountPoint != "" && strings.HasPrefix(s.Path, mountPoint)) {
			handleOp(Request{Op: "share.delete", ShareName: s.Name})
			dbSharesDelete(s.Name)
		}
	}
}

// ─── Fstab ───────────────────────────────────────────────────────────────────

// removeFstabEntry removes a mount point entry from /etc/fstab.
//
// Beta 8 bug fix: previously used strings.Contains(line, mountPoint)
// which matched any line containing the path as a substring. That
// would remove /nimos/pools/data-backup when asked to remove
// /nimos/pools/data, or /etc/cron.d/data-stuff if a path collided.
//
// fstab format: <device> <mountpoint> <fstype> <opts> <dump> <pass>
// Now we parse fields by whitespace and compare field[1] exactly.
func removeFstabEntry(mountPoint string) {
	if mountPoint == "" {
		return
	}
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return
	}
	var kept []string
	removed := 0
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		// Preserve comments and blank lines verbatim
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && fields[1] == mountPoint {
			logMsg("Removing fstab entry: %s", trimmed)
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		// Nothing matched: don't bother rewriting (avoids unnecessary IO
		// and possible permission issues).
		return
	}
	// In-place write with a backup copy. We deliberately avoid the tmp+rename
	// pattern here: rename into /etc would require /etc itself to be writable
	// under systemd ProtectSystem=strict, which we don't grant (only the
	// /etc/fstab file is in ReadWritePaths). Instead we keep a .bak alongside so
	// a mid-write crash is recoverable, then truncate-write /etc/fstab directly.
	newContent := []byte(strings.Join(kept, "\n") + "\n")
	if orig, rerr := os.ReadFile("/etc/fstab"); rerr == nil {
		// Best-effort backup; the backup path is the fstab file's own sibling
		// name, also covered by being the same file target is not — so we write
		// the backup into a path we ARE allowed to write: /var/lib/nimos.
		_ = os.WriteFile("/var/lib/nimos/fstab.bak", orig, 0644)
	}
	if err := os.WriteFile("/etc/fstab", newContent, 0644); err != nil {
		logMsg("removeFstabEntry: in-place write failed: %v", err)
		return
	}
}

// ─── P5 · Auto-restore de /etc/fstab corrupto al boot ────────────────────────
//
// removeFstabEntry hace truncate-write directo sobre /etc/fstab (no puede usar
// tmp+rename bajo systemd ProtectSystem=strict). Un crash a media escritura deja
// un fstab corrupto. nofail evita que bloquee el boot, pero el pool no monta y
// requeriría restauración manual desde el .bak.
//
// restoreFstabIfCorrupt corre al arranque: si /etc/fstab está corrupto (alguna
// línea no-comentario malformada) y existe un .bak válido, lo restaura. Elimina
// el "requiere intervención manual" del modo de fallo.

const fstabBackupPath = "/var/lib/nimos/fstab.bak"

// restoreFstabIfCorrupt comprueba /etc/fstab y, si está corrupto, lo restaura
// desde el backup (si el backup es válido). Best-effort: cualquier error se
// loggea y se continúa (nunca aborta el boot).
func restoreFstabIfCorrupt() {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		logMsg("restoreFstabIfCorrupt: no se pudo leer /etc/fstab: %v", err)
		return
	}
	if fstabContentIsValid(string(data)) {
		return // fstab sano, nada que hacer
	}

	logMsg("restoreFstabIfCorrupt: /etc/fstab parece CORRUPTO")
	bak, berr := os.ReadFile(fstabBackupPath)
	if berr != nil {
		logMsg("restoreFstabIfCorrupt: no hay backup utilizable en %s: %v (intervención manual)",
			fstabBackupPath, berr)
		return
	}
	if !fstabContentIsValid(string(bak)) {
		logMsg("restoreFstabIfCorrupt: el backup %s TAMBIÉN está corrupto; no se restaura", fstabBackupPath)
		return
	}

	if werr := os.WriteFile("/etc/fstab", bak, 0644); werr != nil {
		logMsg("restoreFstabIfCorrupt: fallo al restaurar /etc/fstab desde backup: %v", werr)
		return
	}
	logMsg("restoreFstabIfCorrupt: /etc/fstab RESTAURADO desde %s", fstabBackupPath)
	addNotification("warning", "system",
		"fstab restaurado automáticamente",
		"Se detectó un /etc/fstab corrupto (posible crash a media escritura) y se restauró desde la copia de seguridad. Revisa que tus montajes sean correctos.")
}

// fstabContentIsValid hace una validación estructural de un contenido de fstab.
// Devuelve false si alguna línea no-vacía y no-comentario está malformada (un
// fstab válido necesita al menos device, mountpoint, fstype, opts = 4 campos).
// Función pura para test.
func fstabContentIsValid(content string) bool {
	if strings.TrimSpace(content) == "" {
		// Un fstab vacío no es "corrupto" per se, pero tampoco hay nada que
		// validar. Lo tratamos como válido para no restaurar sin causa.
		return true
	}
	sawEntry := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			// Línea de montaje malformada → corrupto.
			return false
		}
		sawEntry = true
	}
	// Si no había NINGUNA entrada real (solo comentarios/blancos) lo damos por
	// válido — no es trabajo de esta función decidir que faltan montajes.
	_ = sawEntry
	return true
}

// cleanOrphanPoolDirs removes directories in /nimos/pools/ that are not
// associated with any configured pool and have nothing mounted on them.
// Safe to call AFTER pool operations (destroy, create), never at startup
// before pools have mounted.
//
// Beta 8 safety guard: if the pool config is empty or unreadable, we
// REFUSE to clean. Otherwise a corrupt/missing storage.json would cause
// us to delete every directory under /nimos/pools/ — including the
// mount points of pools whose mount currently isn't visible to us due
// to a transient error.
//
// Rule: deletion is only allowed when we have a positively-known list
// of pools to compare against. "Empty list" is treated as "I don't know
// what's there", which is the safe default.
func cleanOrphanPoolDirs() {
	// Wrapper sin retorno para las llamadas existentes (boot, post-destroy).
	// La lógica vive en cleanOrphanPoolDirsResult, que devuelve métricas para
	// que el subsistema de mantenimiento pueda reportarlas en la UI.
	_, _, _, _ = cleanOrphanPoolDirsResult()
}

// cleanOrphanPoolDirsResult es el núcleo de la limpieza de directorios huérfanos.
// Devuelve: nº de dirs borrados, bytes liberados (best-effort), si se saltó por
// la guarda de seguridad, y el motivo del skip. Misma lógica y mismas guardas
// que antes — solo añade contabilidad para el módulo de mantenimiento.
func cleanOrphanPoolDirsResult() (removed int64, bytesFreed int64, skipped bool, skipReason string) {
	return cleanOrphanPoolDirsCore(false)
}

// cleanOrphanPoolDirsGuarded es la variante para el subsistema de mantenimiento
// (lanzada por el usuario o por schedule). Añade una salvaguarda extra: se
// abstiene si hay CUALQUIER operación de storage en curso, porque un pool puede
// estar en transición (p.ej. cambiando un disco: desmontado, a medio remontar).
// Las llamadas internas (boot, post-destroy) usan cleanOrphanPoolDirsResult,
// que NO aplica este guard porque corren en un contexto controlado donde la
// propia operación que las dispara sigue in_progress.
func cleanOrphanPoolDirsGuarded() (removed int64, bytesFreed int64, skipped bool, skipReason string) {
	return cleanOrphanPoolDirsCore(true)
}

// cleanOrphanPoolDirsCore es el núcleo. checkActiveOps=true añade la salvaguarda
// de "no limpiar si hay operaciones de storage en curso" (para la limpieza
// manual/programada del mantenimiento).
func cleanOrphanPoolDirsCore(checkActiveOps bool) (removed int64, bytesFreed int64, skipped bool, skipReason string) {
	// ── SALVAGUARDA A · No limpiar durante operaciones de storage ──────────
	// Solo para la limpieza del mantenimiento. Si hay una operación RECIENTE en
	// curso (crear/destruir/añadir disco/reemplazar/convertir/renombrar/
	// balance), un pool puede estar EN TRANSICIÓN. Ante una operación activa
	// reciente, NO tocamos nada.
	//
	// IMPORTANTE: solo cuentan operaciones recientes. Una operación "colgada"
	// en in_progress de hace horas (p.ej. un rename que no se cerró por un bug)
	// NO debe bloquear la limpieza para siempre — eso convertiría una
	// protección en un bloqueo permanente. Si lleva > stuckOpThreshold, se
	// considera abandonada y se ignora a efectos de este guard.
	if checkActiveOps && storageService != nil {
		ops, err := storageService.repo.ListPendingOperations(context.Background())
		if err != nil {
			logMsg("orphan_dir_sweep: SKIP — no se pudo verificar operaciones en curso: %v", err)
			return 0, 0, true, "no se pudo verificar si hay operaciones de storage en curso — no se limpia por seguridad"
		}
		recent := 0
		const stuckOpThreshold = 30 * time.Minute
		for _, op := range ops {
			// StartedAt reciente = operación de verdad activa. Antigua =
			// probablemente colgada/abandonada, no bloquea.
			if !op.StartedAt.IsZero() && time.Since(op.StartedAt) < stuckOpThreshold {
				recent++
			} else {
				logMsg("orphan_dir_sweep: ignorando operación %s (in_progress pero antigua, probablemente colgada)", op.ID)
			}
		}
		if recent > 0 {
			logMsg("orphan_dir_sweep: SKIP — %d operación(es) de storage recientes en curso", recent)
			return 0, 0, true, fmt.Sprintf("hay %d operación(es) de storage en curso — la limpieza espera a que terminen", recent)
		}
	}

	// ── SALVAGUARDA B · La lista de pools debe leerse SIN error ────────────
	// Si ListPools falla (BD bloqueada, error transitorio), NO podemos saber
	// qué dirs son legítimos → NO limpiamos.
	knownMounts := map[string]bool{}
	if storageService != nil {
		pools, err := storageService.ListPools(context.Background())
		if err != nil {
			logMsg("orphan_dir_sweep: SKIP — no se pudo leer la lista de pools: %v", err)
			return 0, 0, true, "no se pudo leer la lista de pools (BD ocupada o error) — no se limpia por seguridad"
		}
		for _, p := range pools {
			if p.MountPoint != "" {
				knownMounts[p.MountPoint] = true
			}
		}
	} else {
		logMsg("orphan_dir_sweep: SKIP — servicio de storage no disponible")
		return 0, 0, true, "servicio de storage no disponible — no se limpia por seguridad"
	}
	logMsg("orphan_dir_sweep: %d pools conocidos en BD: %v", len(knownMounts), knownMounts)

	// SAFETY GUARD: if we have no known pools, do nothing. A corrupt or
	// missing config would otherwise lead to mass deletion under
	// /nimos/pools/.
	if len(knownMounts) == 0 {
		if entries, err := os.ReadDir(nimosPoolsDir); err == nil && len(entries) > 0 {
			logMsg("cleanOrphanPoolDirs: REFUSING to clean — config has no pools but %d directories exist in %s. Possible corrupt config.",
				len(entries), nimosPoolsDir)
			return 0, 0, true, fmt.Sprintf("config sin pools pero %d directorios presentes (posible config corrupta) — no se limpia por seguridad", len(entries))
		}
		return 0, 0, true, "no hay pools conocidos en la configuración"
	}

	entries, err := os.ReadDir(nimosPoolsDir)
	if err != nil {
		return 0, 0, true, "no se pudo leer el directorio de pools"
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirPath := filepath.Join(nimosPoolsDir, e.Name())

		// Regla 1: saltar pools conocidos (en la BD)
		if knownMounts[dirPath] {
			continue
		}

		// Regla 2: saltar si hay algo montado aquí (pool vivo)
		if isPathOnMountedPool(dirPath) {
			continue
		}

		// Regla 3 (grace period): no borrar carpetas recién creadas — podría
		// ser un pool en proceso de creación (carpeta existe pero aún no está
		// montada ni en la BD por unos segundos). Evita una race con CreatePool.
		if info, err := e.Info(); err == nil {
			if time.Since(info.ModTime()) < 5*time.Minute {
				logMsg("cleanOrphanPoolDirs: skip '%s' — creada hace <5min (grace period)", dirPath)
				continue
			}
		}

		// Pasó las reglas: es una carpeta huérfana confirmada (no es pool
		// conocido, no está montada, no es reciente). Decisión de diseño
		// (Andrés, 01/06): destruir un pool = dejarlo limpio. Una huérfana con
		// contenido es basura de un pool ya destruido (Docker escribe al pool,
		// no a la SD, desde el fix de data-root). Se borra con contenido y todo.
		subEntries, _ := os.ReadDir(dirPath)
		if len(subEntries) > 0 {
			logMsg("cleanOrphanPoolDirs: borrando huérfana '%s' con %d items (basura de pool destruido)",
				dirPath, len(subEntries))
		}
		if err := os.RemoveAll(dirPath); err != nil {
			logMsg("cleanOrphanPoolDirs: failed to remove %s: %v", dirPath, err)
			continue
		}
		logMsg("Cleaned orphan directory: %s", dirPath)
		removed++
	}
	logMsg("orphan_dir_sweep: completado — %d directorios huérfanos borrados de %s", removed, nimosPoolsDir)
	return removed, bytesFreed, false, ""
}

// ─── Torrent Config ──────────────────────────────────────────────────────────

// updateTorrentConfig updates NimTorrent's download_dir to point to the primary
// pool's shares directory. Called after create/destroy pool.
// Without this, NimTorrent writes to the system disk.
const torrentConfPath = "/etc/nimos/torrent.conf"

func updateTorrentConfig() {
	// Beta 8.1: usa service v2 para obtener primary pool
	newDir := ""
	if storageService != nil {
		if pools, err := storageService.ListPools(context.Background()); err == nil {
			for _, p := range pools {
				if p.IsPrimary && p.MountPoint != "" {
					newDir = filepath.Join(p.MountPoint, "shares")
					break
				}
			}
		}
	}

	// Read current config
	data, err := os.ReadFile(torrentConfPath)
	if err != nil {
		// No torrent config — nothing to update
		return
	}

	// Replace download_dir line
	var lines []string
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "download_dir=") {
			if newDir != "" {
				lines = append(lines, "download_dir="+newDir)
			} else {
				lines = append(lines, "download_dir=")
			}
			found = true
		} else {
			lines = append(lines, line)
		}
	}
	if !found && newDir != "" {
		lines = append(lines, "download_dir="+newDir)
	}

	os.WriteFile(torrentConfPath, []byte(strings.Join(lines, "\n")), 0644)

	// Restart torrentd to pick up new config
	runCmd("systemctl", []string{"restart", "nimos-torrentd"}, CmdOptions{Timeout: 10 * time.Second})

	if newDir != "" {
		logMsg("Updated NimTorrent download_dir to %s", newDir)
	} else {
		logMsg("Cleared NimTorrent download_dir (no pools)")
	}
}
