// torrent_tmpdir.go — Resolución del directorio temporal de torrents.
//
// Diseño (decisión de arquitectura): los .torrent temporales se escriben en el
// POOL (/nimos/pools/<pool>/.nimos-tmp/torrents), NO en /var/cache del sistema.
//
// Por qué:
//   · El daemon corre con systemd ProtectSystem=strict → /var es read-only desde
//     dentro del sandbox. Escribir temporales en /var obliga a abrir agujeros en
//     ReadWritePaths ruta por ruta (frágil, mantenimiento sin fin).
//   · El pool ya está en ReadWritePaths y es donde viven los datos. Es coherente
//     con el resto del sistema: getDockerPath() ya pone los datos de Docker en el
//     pool por esta misma razón ("data must live on a pool").
//   · El destino final de la descarga ya está en el pool, así que el temp y el
//     destino comparten filesystem (move sin copia entre dispositivos).
//
// Si no hay pool montado, NO hay descarga posible de todas formas, así que
// devolver error aquí es la semántica correcta (no caer a una ruta del sistema).

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// torrentTmpDir devuelve (y crea) el directorio temporal de torrents dentro de
// un pool montado. Prioridad: pool primario → primer pool con mountpoint.
// Error si no hay ningún pool montado.
func torrentTmpDir() (string, error) {
	mount, err := firstWritablePoolPath()
	if err != nil {
		return "", err
	}
	tmpDir := filepath.Join(mount, ".nimos-tmp", "torrents")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("creando tmp dir en pool: %w", err)
	}
	return tmpDir, nil
}

// handleTorrentStorageStatus informa de la puerta de escritura real. Es
// deliberadamente independiente de torrentd: aunque el servicio esté parado,
// el usuario debe poder saber que primero necesita un pool sano y montado.
func handleTorrentStorageStatus(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, err := firstWritablePoolPath()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ready":   false,
			"message": torrentStorageErrorMessage(err),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ready": true})
}

// firstWritablePoolPath mantiene la prioridad del pool primario, pero no deja
// que un primario en solo lectura o desmontado oculte otro pool sano.
func firstWritablePoolPath() (string, error) {
	if storageService == nil {
		return "", fmt.Errorf("storage service not initialized")
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return "", fmt.Errorf("listing pools: %w", err)
	}
	for _, primaryOnly := range []bool{true, false} {
		for _, pool := range pools {
			if pool.IsPrimary != primaryOnly || pool.MountPoint == "" {
				continue
			}
			if err := assertPoolWritable(pool.MountPoint); err == nil {
				return pool.MountPoint, nil
			}
		}
	}
	return "", fmt.Errorf("no mounted writable pool available")
}

func torrentStorageErrorMessage(err error) string {
	if writableErr, ok := err.(*PoolWritableError); ok && writableErr.Code == "read_only" {
		return "El pool está en solo lectura; revisa su estado antes de descargar"
	}
	return "Necesitas un pool local montado y disponible para descargar"
}

// firstMountedPoolPath resuelve el mountpoint de un pool montado, primario si
// existe. Reutiliza storageService (misma fuente que getDockerPath).
func firstMountedPoolPath() (string, error) {
	if storageService == nil {
		return "", fmt.Errorf("storage service not initialized")
	}
	pools, err := storageService.ListPools(context.Background())
	if err != nil {
		return "", fmt.Errorf("listing pools: %w", err)
	}
	// Primario primero.
	for _, p := range pools {
		if p.IsPrimary && p.MountPoint != "" && isPathOnMountedPool(p.MountPoint) {
			return p.MountPoint, nil
		}
	}
	// Cualquier pool montado.
	for _, p := range pools {
		if p.MountPoint != "" && isPathOnMountedPool(p.MountPoint) {
			return p.MountPoint, nil
		}
	}
	return "", fmt.Errorf("no mounted pool available")
}
