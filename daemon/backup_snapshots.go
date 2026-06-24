package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// parseRetention converts retention strings like "30d", "12", "7d", "4w" into a duration.
// If it's just a number, it's treated as count of snapshots to keep (handled elsewhere).
// Returns the max age as duration, or 0 if count-based.
func parseRetention(retention string) (time.Duration, int) {
	s := strings.TrimSpace(strings.ToLower(retention))
	if s == "" {
		return 30 * 24 * time.Hour, 0 // default 30 days
	}

	if strings.HasSuffix(s, "d") {
		n := parseInt(strings.TrimSuffix(s, "d"), 30)
		return time.Duration(n) * 24 * time.Hour, 0
	}
	if strings.HasSuffix(s, "w") {
		n := parseInt(strings.TrimSuffix(s, "w"), 4)
		return time.Duration(n) * 7 * 24 * time.Hour, 0
	}
	if strings.HasSuffix(s, "m") {
		n := parseInt(strings.TrimSuffix(s, "m"), 1)
		return time.Duration(n) * 30 * 24 * time.Hour, 0
	}

	// Pure number → count-based retention
	n := parseInt(s, 30)
	return 0, n
}

func applyRetention(job map[string]interface{}) {
	fsType, _ := job["fsType"].(string)
	source, _ := job["source"].(string)
	retention, _ := job["retention"].(string)

	maxAge, maxCount := parseRetention(retention)

	switch fsType {
	case "btrfs":
		applyRetentionBtrfs(source, maxAge, maxCount)
	}
}

func applyRetentionBtrfs(source string, maxAge time.Duration, maxCount int) {
	snapDir := fmt.Sprintf("%s/.snapshots", source)
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		return
	}
	var snaps []string
	for _, e := range entries {
		if strings.Contains(e.Name(), "nimbackup") {
			snaps = append(snaps, e.Name())
		}
	}
	if len(snaps) <= 1 {
		return
	}
	// sort alphabetically (timestamps sort correctly)
	sort.Strings(snaps)

	toDelete := []string{}

	if maxAge > 0 {
		cutoff := time.Now().UTC().Add(-maxAge)
		for _, snap := range snaps[:len(snaps)-1] {
			ts := extractTimestamp(snap)
			if !ts.IsZero() && ts.Before(cutoff) {
				toDelete = append(toDelete, snap)
			}
		}
	} else if maxCount > 0 {
		if len(snaps) > maxCount {
			toDelete = snaps[:len(snaps)-maxCount]
		}
	}

	for _, snap := range toDelete {
		snapPath := fmt.Sprintf("%s/%s", snapDir, snap)
		logMsg("backup: retention cleanup — deleting subvolume %s", snapPath)
		btrfsSnapshotDestroy(snapPath)
	}
}

// extractTimestamp parses "nimbackup-YYYYMMDD-HHMMSS" from a snapshot name.
func extractTimestamp(name string) time.Time {
	re := regexp.MustCompile(`nimbackup-(\d{8}-\d{6})`)
	m := re.FindStringSubmatch(name)
	if len(m) < 2 {
		return time.Time{}
	}
	t, err := time.Parse("20060102-150405", m[1])
	if err != nil {
		return time.Time{}
	}
	return t
}

func listBackupSnapshots(pool string) map[string]interface{} {
	var allSnaps []map[string]interface{}

	// Beta 8.1: solo BTRFS. La rama ZFS (zfs list -t snapshot) fue
	// eliminada porque ZFS ya no se soporta. El argumento `pool` se
	// mantiene en la firma para compat con callers pero solo se usa
	// para filtrado opcional vía path matching.
	//
	// BTRFS: buscar snapshots tipo "nimbackup-*" dentro de .snapshots/
	// de cualquier pool montado bajo /nimos/pools/<name>.
	btrfsCmd := "find /nimos/pools -path '*/.snapshots/nimbackup-*' -maxdepth 4 -type d 2>/dev/null"
	if out, ok := runShellStatic(btrfsCmd); ok && out != "" {
		for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
			if path == "" {
				continue
			}
			// Filtro opcional por pool: el path tiene forma
			// /nimos/pools/<poolName>/.snapshots/<snap>
			if pool != "" {
				expectedPrefix := "/nimos/pools/" + pool + "/"
				if !strings.HasPrefix(path, expectedPrefix) {
					continue
				}
			}
			parts := strings.Split(path, "/")
			name := parts[len(parts)-1]
			allSnaps = append(allSnaps, map[string]interface{}{
				"name": name,
				"path": path,
				"type": "btrfs",
				"time": extractTimestamp(name).Format(time.RFC3339),
			})
		}
	}

	if allSnaps == nil {
		allSnaps = []map[string]interface{}{}
	}

	return map[string]interface{}{"snapshots": allSnaps}
}

func createBackupSnapshot(source, fsType string) map[string]interface{} {
	timestamp := time.Now().UTC().Format("20060102-150405")
	snapName := fmt.Sprintf("nimbackup-%s", timestamp)

	switch fsType {
	case "btrfs":
		snapPath := fmt.Sprintf("%s/.snapshots/%s", source, snapName)
		os.MkdirAll(source+"/.snapshots", 0755)
		if errMsg, err := btrfsSnapshotCreate(source, snapPath); err != nil {
			return map[string]interface{}{"error": "Failed: " + errMsg}
		}
		// P4 — retention: tras crear, podar los snapshots más viejos por
		// encima del máximo. En BTRFS el espacio retenido por snapshots viejos
		// es invisible al % de uso normal y acelera el ENOSPC (ver P1), así
		// que limitar su número es robustez, no cosmética.
		pruneSnapshotsByRetention(source, "btrfs")
		return map[string]interface{}{"ok": true, "name": snapName, "path": snapPath, "type": "btrfs"}
	}

	return map[string]interface{}{"error": "Unsupported fsType: " + fsType}
}

// pruneSnapshotsByRetention borra los snapshots más viejos de un pool que
// excedan snapshotRetentionMax. Best-effort: los errores de borrado se loggean
// pero no abortan (el snapshot recién creado ya es válido).
func pruneSnapshotsByRetention(source, fsType string) {
	// El nombre del pool es el último segmento de /nimos/pools/<name>.
	pool := source
	if idx := strings.LastIndex(strings.TrimRight(source, "/"), "/"); idx >= 0 {
		pool = source[idx+1:]
	}

	res := listBackupSnapshots(pool)
	snaps, _ := res["snapshots"].([]map[string]interface{})
	names := make([]string, 0, len(snaps))
	for _, s := range snaps {
		if n, ok := s["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}

	toPrune := snapshotsToPrune(names, snapshotRetentionMax)
	for _, name := range toPrune {
		r := deleteBackupSnapshot(name, fsType, source)
		if _, ok := r["ok"]; ok {
			logMsg("Snapshot retention: borrado snapshot viejo %s (máx %d)", name, snapshotRetentionMax)
		} else {
			logMsg("Snapshot retention: no se pudo borrar %s: %v", name, r["error"])
		}
	}
}

// snapshotsToPrune decide qué snapshots borrar para respetar el máximo. Ordena
// por timestamp (extraído del nombre nimbackup-YYYYMMDD-HHMMSS) y devuelve los
// MÁS VIEJOS que exceden maxKeep. Función pura para test.
func snapshotsToPrune(names []string, maxKeep int) []string {
	if maxKeep <= 0 || len(names) <= maxKeep {
		return nil
	}
	// Ordenar por timestamp ascendente (más viejo primero). El formato del
	// timestamp es lexicográficamente ordenable, pero usamos extractTimestamp
	// para robustez ante nombres inesperados.
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Slice(sorted, func(i, j int) bool {
		return extractTimestamp(sorted[i]).Before(extractTimestamp(sorted[j]))
	})
	// Los primeros (len - maxKeep) son los más viejos a borrar.
	return sorted[:len(sorted)-maxKeep]
}

func deleteBackupSnapshot(name, fsType, source string) map[string]interface{} {
	switch fsType {
	case "btrfs":
		snapPath := fmt.Sprintf("%s/.snapshots/%s", source, name)
		if errMsg, err := btrfsSnapshotDestroy(snapPath); err != nil {
			return map[string]interface{}{"error": "Failed: " + errMsg}
		}
		return map[string]interface{}{"ok": true}
	}

	return map[string]interface{}{"error": "Unsupported fsType"}
}
