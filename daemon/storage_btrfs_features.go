package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS Storage — BTRFS Features (Snapshots, Scrub, Scheduler)
//
// Endpoints match the existing UI contract.
//
// Beta 8 note: ZFS support removed in Fase 5. Snapshot/dataset endpoints
// that were ZFS-only are now stubs returning empty/unsupported. They can
// be re-implemented for BTRFS (subvolumes) in Beta 9+ when needed.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─── Core snapshot primitives (used by storage UI and backup) ────────────────

// btrfsSnapshotCreate creates a BTRFS read-only subvolume snapshot.
// source is the path to the subvolume to snapshot, snapPath is the destination.
// Returns ("", nil) on success, ("error message", error) on failure.
func btrfsSnapshotCreate(source, snapPath string) (string, error) {
	opts := CmdOptions{Timeout: 30 * time.Second}
	res, err := runCmd("btrfs", []string{"subvolume", "snapshot", "-r", source, snapPath}, opts)
	if err != nil {
		return res.Stderr, err
	}
	return "", nil
}

// btrfsSnapshotDestroy destroys a BTRFS subvolume snapshot.
func btrfsSnapshotDestroy(snapPath string) (string, error) {
	opts := CmdOptions{Timeout: 30 * time.Second}
	res, err := runCmd("btrfs", []string{"subvolume", "delete", snapPath}, opts)
	if err != nil {
		return res.Stderr, err
	}
	return "", nil
}

// ─── Snapshot endpoints (BTRFS subvolumes) ──────────────────────────────────
//
// Los snapshots viven en <mountpoint>/.snapshots/<nombre>. Son snapshots
// read-only de subvolumen (btrfs subvolume snapshot -r), baratos en BTRFS
// (copy-on-write, no duplican datos hasta que divergen).

const snapshotsSubdir = ".snapshots"

// listSnapshots devuelve los snapshots existentes de un pool, leyendo el
// directorio .snapshots de su mountpoint.
func listSnapshots(poolName string) map[string]interface{} {
	mp := resolveMountPointByName(poolName)
	snapDir := filepath.Join(mp, snapshotsSubdir)

	entries, err := os.ReadDir(snapDir)
	if err != nil {
		// Si no existe el dir .snapshots, no hay snapshots (no es error).
		return map[string]interface{}{"snapshots": []interface{}{}}
	}

	snapshots := []interface{}{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		created := ""
		if ierr == nil {
			created = info.ModTime().UTC().Format(time.RFC3339)
		}
		snapshots = append(snapshots, map[string]interface{}{
			"name":    e.Name(),
			"path":    filepath.Join(snapDir, e.Name()),
			"created": created,
		})
	}
	return map[string]interface{}{"snapshots": snapshots}
}

// createSnapshot crea un snapshot read-only del pool. Body: {pool, name}.
func createSnapshot(body map[string]interface{}) map[string]interface{} {
	poolName, _ := body["pool"].(string)
	snapName, _ := body["name"].(string)
	if poolName == "" || snapName == "" {
		return map[string]interface{}{"ok": false, "error": "pool y name son requeridos"}
	}
	if !isValidSnapshotName(snapName) {
		return map[string]interface{}{"ok": false, "error": "nombre de snapshot inválido (solo a-z, 0-9, _, -)"}
	}

	mp := resolveMountPointByName(poolName)
	snapDir := filepath.Join(mp, snapshotsSubdir)
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("no se pudo crear .snapshots: %v", err)}
	}

	snapPath := filepath.Join(snapDir, snapName)
	if _, err := os.Stat(snapPath); err == nil {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("ya existe un snapshot llamado %q", snapName)}
	}

	// Snapshot read-only del subvolumen raíz del pool (el mountpoint).
	if stderr, err := btrfsSnapshotCreate(mp, snapPath); err != nil {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("btrfs snapshot falló: %s", stderr)}
	}

	logMsg("Snapshot creado: pool=%s name=%s path=%s", poolName, snapName, snapPath)
	return map[string]interface{}{"ok": true, "name": snapName, "path": snapPath}
}

// rollbackSnapshot revierte el pool al estado de un snapshot. Body: {pool, name}.
//
// OPERACIÓN DELICADA: toca datos vivos. BTRFS no tiene un "rollback" atómico
// nativo para el subvolumen raíz montado, así que la estrategia segura es:
//  1. Verificar que el snapshot existe.
//  2. Crear un snapshot de seguridad del estado ACTUAL antes de revertir
//     (red de seguridad: si el rollback sale mal, el usuario puede volver).
//  3. Sincronizar a disco (sync).
//
// El "swap" real del subvolumen requiere remontar y es disruptivo; en 8.1
// dejamos el rollback como "restaurable vía snapshot de seguridad + aviso",
// que es honesto y no arriesga datos. El swap completo es Beta 8.2.
func rollbackSnapshot(body map[string]interface{}) map[string]interface{} {
	poolName, _ := body["pool"].(string)
	snapName, _ := body["name"].(string)
	if poolName == "" || snapName == "" {
		return map[string]interface{}{"ok": false, "error": "pool y name son requeridos"}
	}
	if !isValidSnapshotName(snapName) {
		return map[string]interface{}{"ok": false, "error": "nombre de snapshot inválido"}
	}

	mp := resolveMountPointByName(poolName)
	snapDir := filepath.Join(mp, snapshotsSubdir)
	snapPath := filepath.Join(snapDir, snapName)

	// 1. El snapshot debe existir.
	if _, err := os.Stat(snapPath); err != nil {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("el snapshot %q no existe", snapName)}
	}

	// 2. Red de seguridad: snapshot del estado actual antes de revertir.
	safetyName := fmt.Sprintf("pre-rollback-%s-%d", snapName, time.Now().Unix())
	safetyPath := filepath.Join(snapDir, safetyName)
	if stderr, err := btrfsSnapshotCreate(mp, safetyPath); err != nil {
		return map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("no se pudo crear snapshot de seguridad antes del rollback: %s", stderr),
		}
	}

	// 3. Sincronizar a disco.
	runCmd("sync", []string{}, CmdOptions{Timeout: 30 * time.Second})

	logMsg("Rollback preparado: pool=%s snapshot=%s, seguridad=%s", poolName, snapName, safetyName)
	return map[string]interface{}{
		"ok":              true,
		"snapshot":        snapName,
		"safety_snapshot": safetyName,
		"note": "Estado actual guardado en snapshot de seguridad. El intercambio " +
			"completo del subvolumen activo (rollback total) requiere remontar el " +
			"pool y se aplicará en Beta 8.2; por ahora los datos del snapshot están " +
			"accesibles en " + snapPath + " para restauración manual.",
	}
}

// isValidSnapshotName valida el nombre de un snapshot (evita inyección de path).
func isValidSnapshotName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// ─── SCRUB ───────────────────────────────────────────────────────────────────

// resolveMountPointByName busca el mount_point de un pool por nombre vía service.
// Si no encuentra el pool, devuelve el path por defecto en /nimos/pools/<name>.
func resolveMountPointByName(poolName string) string {
	if storageService == nil {
		return nimosPoolsDir + "/" + poolName
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return nimosPoolsDir + "/" + poolName
	}
	for _, p := range pools {
		if p.Name == poolName && p.MountPoint != "" {
			return p.MountPoint
		}
	}
	return nimosPoolsDir + "/" + poolName
}

// startScrub starts a BTRFS integrity check.
// POST /api/storage/scrub { pool }
func startScrub(body map[string]interface{}) map[string]interface{} {
	pool := bodyStr(body, "pool")

	// Resolve mount point via service v2
	mountPoint := resolveMountPointByName(pool)

	if _, err := runCmd("btrfs", []string{"filesystem", "show", mountPoint}, CmdOptions{Timeout: 5 * time.Second}); err != nil {
		return map[string]interface{}{"ok": false, "error": "Pool not found or not a BTRFS filesystem"}
	}

	if err := startScrubOnPool(mountPoint, pool); err != nil {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("btrfs scrub failed: %s", err)}
	}
	return map[string]interface{}{"ok": true, "type": "btrfs"}
}

// startScrubOnPool lanza un `btrfs scrub` (NO bloqueante: el kernel lo corre en
// segundo plano) sobre un mountpoint ya resuelto. Extraído de startScrub para
// poder reutilizarlo desde flujos internos —p.ej. el auto-scrub tras un replace
// exitoso— sin pasar por el body HTTP. Var para inyectarlo en tests.
var startScrubOnPool = func(mountPoint, poolName string) error {
	if mountPoint == "" {
		return fmt.Errorf("startScrubOnPool: mountPoint vacío")
	}
	if _, err := runCmd("btrfs", []string{"scrub", "start", mountPoint}, CmdOptions{Timeout: 15 * time.Second}); err != nil {
		return err
	}
	logMsg("BTRFS scrub started on %s", mountPoint)
	addNotification("info", "system", "Verificación iniciada",
		fmt.Sprintf("Verificación de integridad iniciada en volumen %s", poolName))
	return nil
}

// getScrubStatus returns detailed scrub status for a BTRFS pool.
// GET /api/storage/scrub/status?pool=NAME
func getScrubStatus(poolName string) map[string]interface{} {
	mountPoint := resolveMountPointByName(poolName)

	if _, err := runCmd("btrfs", []string{"filesystem", "show", mountPoint}, CmdOptions{Timeout: 5 * time.Second}); err != nil {
		return map[string]interface{}{"status": "error", "error": "Pool not found", "filesystem": "unknown"}
	}
	return getBtrfsScrubStatus(mountPoint, poolName)
}

func getBtrfsScrubStatus(mountPoint, poolName string) map[string]interface{} {
	opts := CmdOptions{Timeout: 10 * time.Second}
	res, _ := runCmd("btrfs", []string{"scrub", "status", mountPoint}, opts)
	output := res.Stdout

	result := map[string]interface{}{
		"status":       "idle",
		"progress":     0,
		"errors":       0,
		"duration":     "—",
		"lastScrub":    nil,
		"lastDuration": nil,
		"lastErrors":   nil,
		"dataErrors":   "—",
		"filesystem":   "btrfs",
	}

	if strings.Contains(output, "no stats available") || strings.Contains(output, "not started") {
		result["status"] = "never"
		return result
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Status:") {
			status := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			switch status {
			case "running":
				result["status"] = "scrubbing"
			case "finished":
				result["status"] = "done"
			case "aborted":
				result["status"] = "canceled"
			}
		}

		if strings.HasPrefix(line, "Scrub started:") {
			timeStr := strings.TrimSpace(strings.TrimPrefix(line, "Scrub started:"))
			for _, layout := range []string{
				"Mon Jan  2 15:04:05 2006",
				"Mon Jan 2 15:04:05 2006",
				"2006-01-02 15:04:05",
			} {
				if t, err := time.Parse(layout, timeStr); err == nil {
					result["lastScrub"] = t.Format(time.RFC3339)
					break
				}
			}
		}

		if strings.HasPrefix(line, "Duration:") {
			dur := strings.TrimSpace(strings.TrimPrefix(line, "Duration:"))
			result["duration"] = dur
			result["lastDuration"] = dur
		}

		if strings.HasPrefix(line, "Rate:") {
			result["speed"] = strings.TrimSpace(strings.TrimPrefix(line, "Rate:"))
		}

		if strings.HasPrefix(line, "Error summary:") {
			errStr := strings.TrimSpace(strings.TrimPrefix(line, "Error summary:"))
			result["dataErrors"] = errStr
			if strings.Contains(errStr, "no errors") {
				result["errors"] = 0
				result["lastErrors"] = 0
			} else {
				totalErrs := 0
				for _, part := range strings.Split(errStr, " ") {
					if strings.Contains(part, "=") {
						kv := strings.SplitN(part, "=", 2)
						if len(kv) == 2 {
							n, _ := strconv.Atoi(kv[1])
							totalErrs += n
						}
					}
				}
				result["errors"] = totalErrs
				result["lastErrors"] = totalErrs
			}
		}

		if strings.HasPrefix(line, "Total to scrub:") {
			result["totalSize"] = strings.TrimSpace(strings.TrimPrefix(line, "Total to scrub:"))
		}
	}

	return result
}

// ─── SCRUB SCHEDULER ─────────────────────────────────────────────────────────
//
// Beta 8.1: tabla scrub_schedule definida en storage_schema.sql §8.

func calculateNextRun(freq string, hour, minute, dow, dom int) interface{} {
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())

	switch freq {
	case "daily":
		if !target.After(now) {
			target = target.Add(24 * time.Hour)
		}
	case "weekly":
		daysUntil := (dow - int(now.Weekday()) + 7) % 7
		if daysUntil == 0 && !target.After(now) {
			daysUntil = 7
		}
		target = target.AddDate(0, 0, daysUntil)
	case "monthly":
		target = time.Date(now.Year(), now.Month(), dom, hour, minute, 0, 0, now.Location())
		if !target.After(now) {
			target = target.AddDate(0, 1, 0)
		}
	default:
		return nil
	}
	return target.Format(time.RFC3339)
}

// startScrubScheduler starts a background goroutine that periodically
// checks the scrub_schedule table and runs scheduled scrubs.
func startScrubScheduler() {
	go func() {
		// Wait a bit on startup so we don't run immediately after boot
		time.Sleep(60 * time.Second)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkAndRunScheduledScrubs()
		}
	}()
	logMsg("Scrub scheduler started (check interval: 60s)")
}

func checkAndRunScheduledScrubs() {
	rows, err := db.Query(`SELECT pool_name, frequency, day_of_week, day_of_month, hour, minute, last_run
		FROM scrub_schedule WHERE enabled = 1`)
	if err != nil {
		return
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var (
			poolName       string
			freq           string
			dow, dom, h, m int
			lastRun        *string
		)
		if err := rows.Scan(&poolName, &freq, &dow, &dom, &h, &m, &lastRun); err != nil {
			continue
		}
		lastRunStr := ""
		if lastRun != nil {
			lastRunStr = *lastRun
		}
		if shouldRunNow(freq, h, m, dow, dom, lastRunStr, now) {
			logMsg("Scrub scheduler: starting scheduled scrub on %s", poolName)
			res := startScrub(map[string]interface{}{"pool": poolName})

			// FIX2: solo marcar como ejecutado si el scrub REALMENTE arrancó.
			// Si falló (pool degradado, otro scrub en curso, error), NO tocar
			// last_run → el scheduler reintenta en el próximo tick, y se avisa.
			if ok, _ := res["ok"].(bool); ok {
				_, _ = db.Exec(`UPDATE scrub_schedule SET last_run = ?, next_run = ? WHERE pool_name = ?`,
					now.Format(time.RFC3339),
					calculateNextRun(freq, h, m, dow, dom),
					poolName)
			} else {
				reason, _ := res["error"].(string)
				if reason == "" {
					reason = "motivo desconocido"
				}
				logMsg("Scrub scheduler: scrub on %s NO arrancó (%s) — se reintentará", poolName, reason)
				addNotification("warning", "system",
					fmt.Sprintf("Scrub programado no pudo arrancar en %s", poolName),
					fmt.Sprintf("La verificación de integridad programada de %s no pudo iniciarse: %s. Se reintentará automáticamente.", poolName, reason))
			}
		}
	}
}

func shouldRunNow(freq string, hour, minute, dow, dom int, lastRun string, now time.Time) bool {
	// Avoid double-runs within the same minute window
	if lastRun != "" {
		if last, err := time.Parse(time.RFC3339, lastRun); err == nil {
			if now.Sub(last) < 50*time.Second {
				return false
			}
		}
	}
	if now.Hour() != hour || now.Minute() != minute {
		return false
	}
	switch freq {
	case "daily":
		return true
	case "weekly":
		return int(now.Weekday()) == dow
	case "monthly":
		return now.Day() == dom
	}
	return false
}
