// storage_share_adopt.go — FIX-3 (shares) · Re-adopción de shares huérfanas.
//
// PROBLEMA (incidente data8, junio 2026):
// La tabla `shares` se vació (BD restaurada de un backup viejo) pero los
// subvolúmenes con los datos seguían en `<pool>/shares/`. NimOS no detectaba
// esos subvolúmenes huérfanos: para él, una share existe si tiene fila en la BD.
// El usuario tuvo que re-registrar las 3 carpetas a mano.
//
// SOLUCIÓN:
// Escanear los subvolúmenes físicos bajo `<pool>/shares/` y compararlos con la
// tabla `shares`. Los que están en disco pero NO en la BD son huérfanos
// re-adoptables. La re-adopción reutiliza CreateShare, que RESPETA el subvolumen
// existente (createBtrfsSubvolIfMissing) y registra la fila + permisos del owner
// SIN tocar los datos.
//
// REGLA 16: la verdad de qué shares existen está en el disco (los subvolúmenes),
// no solo en la caché (SQLite). Esto reconcilia ambas.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OrphanShare es un subvolumen de share presente en disco pero sin fila en la BD.
type OrphanShare struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// findOrphanSharesIn es la función PURA: compara los subvolúmenes bajo shares/ de
// cada pool con los nombres de share conocidos (BD), y devuelve los huérfanos.
// Recibe la realidad inyectada (lister de dirs, comprobador de subvolumen) para
// ser testeable sin tocar el disco.
func findOrphanSharesIn(
	pools []*Pool,
	knownByPool map[string]map[string]bool,
	listShareDirs func(sharesDir string) ([]string, error),
	isSubvol func(path string) bool,
) []OrphanShare {
	var out []OrphanShare
	for _, p := range pools {
		if p == nil || p.MountPoint == "" {
			continue
		}
		sharesDir := filepath.Join(p.MountPoint, "shares")
		dirs, err := listShareDirs(sharesDir)
		if err != nil {
			continue // pool sin carpeta shares/ o no accesible → nada que adoptar
		}
		known := knownByPool[p.Name]
		for _, name := range dirs {
			if strings.HasPrefix(name, ".") {
				continue // ocultos (.papelera, etc.)
			}
			if known[name] {
				continue // ya registrada en BD
			}
			full := filepath.Join(sharesDir, name)
			if !isSubvol(full) {
				continue // no es una share real (no es subvolumen btrfs)
			}
			out = append(out, OrphanShare{Pool: p.Name, Name: name, Path: full})
		}
	}
	return out
}

// findOrphanShares es el wrapper de producción: lee pools (montados) y shares de
// la BD, y delega en la función pura con el lister y el comprobador reales.
func findOrphanShares(ctx context.Context) ([]OrphanShare, error) {
	if storageService == nil {
		return nil, fmt.Errorf("almacenamiento no inicializado")
	}
	pools, err := storageService.ListPools(ctx)
	if err != nil {
		return nil, err
	}
	shares, err := dbSharesListRaw()
	if err != nil {
		return nil, err
	}
	knownByPool := map[string]map[string]bool{}
	for _, s := range shares {
		if knownByPool[s.Pool] == nil {
			knownByPool[s.Pool] = map[string]bool{}
		}
		knownByPool[s.Pool][s.Name] = true
	}
	return findOrphanSharesIn(pools, knownByPool, listShareSubdirs, isBtrfsSubvolume), nil
}

// listShareSubdirs lista los nombres de directorio bajo dir (sin recursión).
func listShareSubdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// readoptOrphanShare re-registra una share huérfana reutilizando la variante que
// PRESERVA los permisos de la carpeta existente (readoptShareService), registrando
// la fila + permisos del owner SIN re-chownear los datos. createdBy queda como owner.
func readoptOrphanShare(ctx context.Context, pool, name, createdBy string) error {
	_, err := readoptShareService(ctx, CreateShareInput{
		Name:      name,
		PoolName:  pool,
		CreatedBy: createdBy,
	})
	return err
}
