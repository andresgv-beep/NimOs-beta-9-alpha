// storage_pool_rename.go — Rename COMPLETO de un pool.
//
// Renombrar un pool no es solo cambiar el nombre en la BD. El nombre se refleja
// en hechos que poseen sistemas externos (Regla 16 · DISCIPLINE):
//   - la ETIQUETA del filesystem BTRFS (grabada en el disco)
//   - el MOUNT POINT (/nimos/pools/<name>, en el kernel y en fstab)
//
// Un rename a medias (solo BD) deja el pool desincronizado: etiqueta vieja,
// fstab apuntando a una ruta inexistente → el pool no monta → cualquier
// servicio escribe en el directorio vacío sobre el disco de sistema. Este es
// EXACTAMENTE el fallo que dejó data6→data9 a medias (Docker escribiendo en
// /dev/sda2 en vez de en el RAID1).
//
// applyPoolRenamePhysical aplica los cambios físicos de forma ordenada y, si
// algo falla, intenta dejar el sistema en un estado coherente con el nombre
// VIEJO (el caller no toca la BD si esto devuelve error).

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// applyPoolRenamePhysicalFn es inyectable para tests. En producción apunta al
// método real; en tests se sustituye por un stub que no toca btrfs/fstab/mount.
var applyPoolRenamePhysicalFn = func(s *StorageService, ctx context.Context, pool *Pool, newName, oldMountPoint, newMountPoint string) error {
	return s.applyPoolRenamePhysical(ctx, pool, newName, oldMountPoint, newMountPoint)
}

// applyPoolRenamePhysical reetiqueta el filesystem, actualiza fstab y remonta el
// pool en su nueva ruta. Devuelve error sin haber tocado la BD si algo falla.
//
// Orden:
//  1. Validar que el pool está montado (necesario para reetiquetar y para no
//     operar a ciegas sobre un pool en estado raro).
//  2. Reetiquetar el filesystem BTRFS (la autoridad del nombre real).
//  3. Crear el nuevo directorio de montaje.
//  4. Reescribir la entrada de fstab (vieja ruta → nueva ruta).
//  5. Remontar: desmontar la vieja, montar la nueva.
//     Si 4/5 fallan, se intenta revertir fstab para no dejar el boot roto.
func (s *StorageService) applyPoolRenamePhysical(ctx context.Context, pool *Pool, newName, oldMountPoint, newMountPoint string) error {
	// 1. El pool debe estar montado. Renombrar un pool desmontado es
	//    justamente cómo se llega al estado roto; exigimos que esté sano.
	if !isPoolMounted(oldMountPoint) {
		return fmt.Errorf("el pool no está montado en %s; móntalo antes de renombrar", oldMountPoint)
	}

	// 2. Reetiquetar el filesystem BTRFS. La etiqueta vive en el disco y es la
	//    autoridad real del nombre. `btrfs filesystem label <mp> <name>` se
	//    puede hacer en caliente (montado).
	if _, ok := runSafe("btrfs", "filesystem", "label", oldMountPoint, newName); !ok {
		return fmt.Errorf("no se pudo aplicar la etiqueta BTRFS %q", newName)
	}

	// 3. Crear el nuevo punto de montaje.
	if err := os.MkdirAll(newMountPoint, 0755); err != nil {
		return fmt.Errorf("crear mount point %s: %w", newMountPoint, err)
	}

	// 4. Reescribir fstab: la entrada vieja (por mount point) pasa a la nueva
	//    ruta, conservando el resto (UUID, opciones). Guardamos el contenido
	//    original por si hay que revertir.
	origFstab, _ := os.ReadFile("/etc/fstab")
	if err := rewriteFstabMountPoint(oldMountPoint, newMountPoint); err != nil {
		return fmt.Errorf("actualizar fstab: %w", err)
	}

	// 5. Remontar en la ruta nueva. Desmontar la vieja primero.
	if _, ok := runSafe("umount", oldMountPoint); !ok {
		// No se pudo desmontar (¿en uso?). Revertir fstab y abortar.
		if origFstab != nil {
			_ = os.WriteFile("/etc/fstab", origFstab, 0644)
		}
		return fmt.Errorf("no se pudo desmontar %s (¿hay servicios usándolo? para Docker antes de renombrar)", oldMountPoint)
	}
	if _, ok := runSafe("mount", newMountPoint); !ok {
		// El montaje nuevo falló. Intentar volver atrás: restaurar fstab y
		// remontar la ruta vieja para no dejar el pool inaccesible.
		if origFstab != nil {
			_ = os.WriteFile("/etc/fstab", origFstab, 0644)
		}
		runSafe("mount", oldMountPoint)
		return fmt.Errorf("no se pudo montar en la ruta nueva %s (revertido a %s)", newMountPoint, oldMountPoint)
	}

	// Limpieza best-effort del directorio viejo si quedó vacío.
	if entries, derr := os.ReadDir(oldMountPoint); derr == nil && len(entries) == 0 {
		_ = os.Remove(oldMountPoint)
	}

	logMsg("RenamePool: %q → %q (etiqueta+fstab+remontaje aplicados)", pool.Name, newName)
	return nil
}

// rewriteFstabMountPoint reemplaza el mount point (campo 2) de la entrada de
// fstab que apunta a oldMountPoint, dejándolo en newMountPoint. Conserva el
// resto de campos (device/UUID, fstype, opciones). Mismo patrón de escritura
// que removeFstabEntry (truncate-write con .bak, por ProtectSystem=strict).
func rewriteFstabMountPoint(oldMountPoint, newMountPoint string) error {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err
	}
	newContent, replaced := transformFstabMountPoint(string(data), oldMountPoint, newMountPoint)
	if replaced == 0 {
		return fmt.Errorf("no se encontró entrada de fstab para %s", oldMountPoint)
	}
	// Backup recuperable antes de truncar (mismo motivo que removeFstabEntry).
	_ = os.WriteFile(filepath.Join("/var/lib/nimos", "fstab.bak"), data, 0644)
	return os.WriteFile("/etc/fstab", []byte(newContent), 0644)
}

// transformFstabMountPoint es la lógica PURA del rewrite (string→string),
// testeable sin tocar /etc/fstab. Reemplaza el campo 2 (mount point) de la
// entrada que apunta a oldMountPoint. Preserva comentarios, líneas en blanco y
// el resto de campos (UUID, fstype, opciones). Devuelve el contenido nuevo y
// cuántas entradas reemplazó.
func transformFstabMountPoint(content, oldMountPoint, newMountPoint string) (string, int) {
	var out []string
	replaced := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && fields[1] == oldMountPoint {
			fields[1] = newMountPoint
			out = append(out, strings.Join(fields, " "))
			replaced++
			continue
		}
		out = append(out, line)
	}
	newContent := strings.Join(out, "\n")
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	return newContent, replaced
}
