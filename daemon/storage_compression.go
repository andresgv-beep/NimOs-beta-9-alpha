// storage_compression.go — Fuente de verdad de la compresión de un pool.
//
// AUDIT F4 (auditoría 2026-07-03): la compresión vivía en TRES sitios
// descoordinados y la UI mentía:
//
//	· fstab hardcodeaba compress=zstd para TODOS los pools,
//	· SetPoolCompression solo escribía la BD (el selector era decorativo),
//	· SOT-05 leía `btrfs property get` — que NO ve la opción de montaje, así
//	  que un pool montado con compress=zstd:3 se servía como "none".
//
// Modelo tras el fix:
//
//	· La OPCIÓN DE MONTAJE es la realidad (compress=<algo> en findmnt).
//	· La BD guarda el valor deseado; fstab se DERIVA de la BD por pool.
//	· SetPoolCompression cambia la realidad primero (remount) y luego BD+fstab.
//	· SOT-05 lee findmnt y auto-cura la BD si diverge (Regla 16).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// validCompressionAlgo acepta los valores que NimOS gestiona: none, lzo,
// zlib[:1-9], zstd[:1-15]. Es también la barrera anti-inyección: el valor
// acaba en /etc/fstab y en un `mount -o`, así que nada fuera de esta lista
// puede colarse (antes el handler HTTP aceptaba cualquier string).
func validCompressionAlgo(algo string) bool {
	switch algo {
	case "none", "lzo", "zlib", "zstd":
		return true
	}
	base, level, found := strings.Cut(algo, ":")
	if !found {
		return false
	}
	n, err := strconv.Atoi(level)
	if err != nil {
		return false
	}
	switch base {
	case "zstd":
		return n >= 1 && n <= 15
	case "zlib":
		return n >= 1 && n <= 9
	}
	return false
}

// compressionFromMountOpts extrae la compresión real de las opciones de
// montaje (salida de `findmnt -no OPTIONS`). Devuelve:
//
//	"zstd:3", "lzo", ...  → algo activo (compress= o compress-force=)
//	"none"                → montado SIN compresión (o compress=no)
//
// El llamante decide qué hacer si las opciones no son legibles (string
// vacío de findmnt): esta función asume que `opts` es una lista válida.
func compressionFromMountOpts(opts string) string {
	for _, opt := range strings.Split(opts, ",") {
		opt = strings.TrimSpace(opt)
		val, found := strings.CutPrefix(opt, "compress=")
		if !found {
			val, found = strings.CutPrefix(opt, "compress-force=")
		}
		if !found {
			continue
		}
		val = strings.ToLower(strings.TrimSpace(val))
		if val == "" || val == "no" || val == "none" {
			return "none"
		}
		return val
	}
	return "none"
}

// mountOptForCompression traduce el valor de pool.Compression a la opción de
// montaje correspondiente ("" si no hay que añadir nada). El kernel usa
// compress=no para desactivar en un remount (quitar la opción no basta).
func mountOptForCompression(algo string) string {
	if algo == "" || algo == "none" {
		return "compress=no"
	}
	return "compress=" + algo
}

// remountPoolCompressionFn aplica la compresión en vivo con un remount.
// Inyectable para tests (patrón applyPoolRenamePhysicalFn). Solo se llama
// con el pool montado; el fstab cubre los montajes futuros.
var remountPoolCompressionFn = func(mountPoint, algo string) error {
	opt := mountOptForCompression(algo)
	if out, ok := runSafe("mount", "-o", "remount,"+opt, mountPoint); !ok {
		return fmt.Errorf("remount %s con %s falló: %s", mountPoint, opt, strings.TrimSpace(out))
	}
	return nil
}

// seedCompressionFromReality alinea pool.Compression en la BD con la opción
// de montaje real de cada pool montado. Corre al arranque, ANTES de
// syncFstabFromDB, para que el fstab regenerado refleje la realidad.
//
// Caso que cura: pools creados cuando el fstab hardcodeaba compress=zstd
// pero la BD decía "none" (AUDIT F4) — sin esta siembra, el primer fstab
// derivado de la BD apagaría la compresión en el siguiente reboot.
// Idempotente: cuando BD == realidad no toca nada.
func seedCompressionFromReality(ctx context.Context) {
	if storageService == nil {
		return
	}
	pools, err := storageService.repo.ListPools(ctx)
	if err != nil {
		logMsg("seedCompressionFromReality: ListPools falló: %v", err)
		return
	}
	for _, p := range pools {
		if p == nil || p.MountPoint == "" || !isPoolMounted(p.MountPoint) {
			continue
		}
		out, ok := runSafe("findmnt", "-no", "OPTIONS", p.MountPoint)
		if !ok || strings.TrimSpace(out) == "" {
			continue
		}
		real := compressionFromMountOpts(strings.TrimSpace(out))
		db := p.Compression
		if db == "" {
			db = "none"
		}
		if real == db {
			continue
		}
		poolID, name := p.ID, p.Name
		err := storageService.runInTx(ctx, func(tx *sql.Tx) error {
			return storageService.repo.SetPoolCompression(ctx, tx, poolID, real)
		})
		if err != nil {
			logMsg("seedCompressionFromReality: pool '%s' no se pudo sembrar: %v", name, err)
			continue
		}
		logMsg("seedCompressionFromReality: pool '%s' compression BD=%s → realidad=%s", name, db, real)
	}
}
