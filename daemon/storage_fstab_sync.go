// storage_fstab_sync.go — FIX-4 · fstab generado desde la BD.
//
// PROBLEMA (incidente data8, junio 2026):
// `appendFstab` ANEXA una línea por pool, y solo se llama en create/import. El
// camino de reparación (replace/convert manual) no pasa por ahí → el pool quedó
// FUERA de fstab → no automontó tras el reinicio → toda la saga.
//
// SOLUCIÓN:
// NimOS es DUEÑO de un bloque de fstab entre marcadores `# >>> [nimos]` … que
// REGENERA desde la BD. Preserva las líneas del usuario, absorbe entradas legacy
// sueltas de /nimos/pools/, y es idempotente. Al ejecutarse en el arranque,
// AUTO-CURA cualquier drift: un pool que esté en la BD pero no en fstab se añade
// solo. Con `nofail`, un disco ausente nunca bloquea el arranque del sistema.
//
// REGLA 16: el fstab de los pools se DERIVA de la BD (que a su vez se reconcilia
// con btrfs), en vez de acumularse a mano y poder divergir.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	fstabMarkerStart = "# >>> [nimos] pools gestionados · generado, no editar a mano"
	fstabMarkerEnd   = "# <<< [nimos]"
	fstabPoolOpts    = "defaults,nofail,noatime,compress=zstd"
)

// buildManagedFstab toma el contenido actual de fstab y la lista de pools y
// devuelve el nuevo contenido. PURA (no toca disco):
//   - preserva intactas las líneas del usuario,
//   - elimina el bloque [nimos] anterior,
//   - elimina líneas legacy sueltas de /nimos/pools/ (de appendFstab), que se
//     reescriben dentro del bloque,
//   - genera un bloque [nimos] limpio con una línea por pool válido.
func buildManagedFstab(current string, pools []*Pool) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(current, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == fstabMarkerStart {
			inBlock = true
			continue
		}
		if inBlock {
			if trimmed == fstabMarkerEnd {
				inBlock = false
			}
			continue // descartar todo lo que haya dentro del bloque viejo
		}
		if isNimosPoolFstabLine(trimmed) {
			continue // línea legacy de un pool, fuera del bloque → absorber
		}
		kept = append(kept, line)
	}

	content := strings.TrimRight(strings.Join(kept, "\n"), "\n")

	var b strings.Builder
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(fstabMarkerStart + "\n")
	for _, p := range pools {
		if p == nil || p.BtrfsUUID == "" || p.MountPoint == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("UUID=%s %s btrfs %s 0 2\n",
			p.BtrfsUUID, p.MountPoint, fstabPoolOpts))
	}
	b.WriteString(fstabMarkerEnd + "\n")
	return b.String()
}

// isNimosPoolFstabLine indica si una línea (no comentada) monta algo bajo
// /nimos/pools/ — es decir, una entrada de pool de NimOS.
func isNimosPoolFstabLine(trimmed string) bool {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return false
	}
	return strings.HasPrefix(fields[1], nimosPoolsDir+"/")
}

// syncFstabFromDB regenera el bloque [nimos] de /etc/fstab desde los pools de la
// BD. Idempotente y auto-curativa: si un pool está en la BD pero no en fstab, se
// añade. Pensada para llamarse al arranque (auto-cura) y tras crear/importar/
// renombrar/eliminar un pool (inmediatez).
func syncFstabFromDB(ctx context.Context) error {
	if storageService == nil {
		return fmt.Errorf("almacenamiento no inicializado")
	}
	pools, err := storageService.ListPools(ctx)
	if err != nil {
		return err
	}
	return writeManagedFstab(pools)
}

// writeManagedFstab escribe el fstab regenerado de forma atómica (tmp + rename),
// dejando un backup, y recarga systemd. No-op si no hay cambios.
func writeManagedFstab(pools []*Pool) error {
	current, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return fmt.Errorf("leyendo /etc/fstab: %w", err)
	}
	next := buildManagedFstab(string(current), pools)
	if next == string(current) {
		return nil // sin cambios → no tocar nada
	}

	// Backup del fstab previo (por si acaso) + escritura atómica.
	_ = os.WriteFile("/etc/fstab.nimos.bak", current, 0644)
	tmp := "/etc/fstab.nimos.tmp"
	if err := os.WriteFile(tmp, []byte(next), 0644); err != nil {
		return fmt.Errorf("escribiendo fstab temporal: %w", err)
	}
	if err := os.Rename(tmp, "/etc/fstab"); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renombrando fstab: %w", err)
	}

	// systemd cachea fstab: sin daemon-reload, las entradas nuevas se ignoran
	// hasta el próximo arranque.
	runSafe("systemctl", "daemon-reload")
	logMsg("syncFstabFromDB: bloque [nimos] regenerado (%d pools)", len(pools))
	return nil
}
