// docker_subvolume.go — Item 8 (backlog AppStore): el directorio de datos de
// cada app (containers/<id>) se crea como SUBVOLUMEN BTRFS en vez de dir plano.
//
// Por qué (no es preparatorio · valor concreto):
//   - Quota por app · la maquinaria ya existe (createBtrfsSubvolIfMissing acepta
//     quotaBytes · qgroup limit). Capar un container que se desmadre.
//   - Borrado ATÓMICO con `btrfs subvolume delete` en vez de `rm -rf`.
//   - Consistencia con el modelo de shares (que ya son subvolúmenes).
//
// No-breaking: las apps viejas son dirs planos · se respetan y se borran con
// RemoveAll como siempre. Las nuevas son subvolúmenes. Cada una tratada bien.
// Si el path no está en BTRFS (o falla la creación), fallback a directorio
// normal · una instalación NUNCA se rompe por esto.

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// ensureContainerSubvolume crea el dir de datos del container como subvolumen
// BTRFS. Idempotente: si ya existe (subvolumen o dir legacy), lo respeta.
// Si la creación del subvolumen falla (path no-BTRFS, etc.), cae a MkdirAll
// con los permisos de fallback · la instalación continúa siempre.
func ensureContainerSubvolume(path string, fallbackPerm os.FileMode) {
	if _, err := os.Stat(path); err == nil {
		return // ya existe (subvolumen o dir legacy) · idempotente, no tocar
	}
	// quotaBytes=0 → sin límite por ahora · el subvolumen es el habilitador,
	// exponer la quota (default/configField) es un pasito aparte.
	if err := createBtrfsSubvolIfMissing(path, 0); err == nil {
		logMsg("docker: containers/%s creado como subvolumen BTRFS (quota-ready, borrado atómico)", filepath.Base(path))
		return
	} else {
		logMsg("docker: subvolumen BTRFS para %q falló (%v) · fallback a directorio normal", path, err)
	}
	os.MkdirAll(path, fallbackPerm)
}

// removeContainerPath borra el dir de datos del container. Si es un subvolumen
// BTRFS usa `btrfs subvolume delete` (atómico · un dir-subvolumen no se puede
// quitar con rmdir/RemoveAll). Si es un dir plano (legacy), RemoveAll de siempre.
func removeContainerPath(path string) {
	if isBtrfsSubvolume(path) {
		if out, ok := runSafe("btrfs", "subvolume", "delete", path); ok {
			logMsg("docker: subvolumen %q borrado (uninstall wipe)", path)
			return
		} else {
			logMsg("docker: subvolume delete %q falló (%s) · fallback RemoveAll", path, strings.TrimSpace(out))
		}
	}
	os.RemoveAll(path)
}

// isBtrfsSubvolume · ¿el path es un subvolumen BTRFS? (consulta a btrfs).
func isBtrfsSubvolume(path string) bool {
	out, ok := runSafe("btrfs", "subvolume", "show", path)
	return interpretSubvolumeShow(out, ok)
}

// interpretSubvolumeShow · PURA · decide si la salida de `btrfs subvolume show`
// indica que el path ES un subvolumen: el comando tuvo éxito (ok) y devolvió
// info no vacía. Un dir plano hace que `show` falle (ok=false).
func interpretSubvolumeShow(out string, ok bool) bool {
	return ok && strings.TrimSpace(out) != ""
}
