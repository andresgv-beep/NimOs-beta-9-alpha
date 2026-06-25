package main

// storage_btrfs_import.go — Importar pools BTRFS huérfanos al managed state.
//
// Fase 7 Bloque C3.1 (Beta 8.1):
//   Permite registrar en SQLite un filesystem BTRFS que ya existe
//   físicamente en los discos pero no está gestionado por NimOS.
//
// Caso de uso típico:
//   1. Usuario desmonta un pool (export) → BTRFS sigue en discos
//   2. El observer lo detecta como orphan_filesystem
//   3. La UI ofrece "Importar como pool gestionado"
//   4. Este código toma el UUID y registra el pool en SQLite + lo monta
//
// Es la operación inversa de exportPoolBtrfs. Diferencias clave:
//
//   exportPoolBtrfs   →  managed → observed (datos preservados)
//   destroyPoolBtrfs  →  managed → nada     (datos perdidos)
//   importPoolBtrfs   →  observed → managed (recuperación)
//
// Lo que NO hace este código:
//   · NO ejecuta mkfs (sería destructivo)
//   · NO modifica el filesystem
//   · NO toca los datos del usuario
//
// Lo que SÍ hace:
//   · Lee el observed state via globalObserver
//   · Localiza el filesystem por UUID
//   · Registra pool + relación N:M de devices en SQLite
//   · Monta el filesystem (si no está montado)
//   · Añade entrada a /etc/fstab
//   · Llama notifyStorageChanged() para refrescar el observer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// importPoolBtrfs registra un filesystem BTRFS detectado en el sistema
// como pool gestionado por NimOS.
//
// Inputs:
//   uuid     — UUID BTRFS del filesystem a importar (de /observed)
//   name     — nombre que tendrá el pool en NimOS (slug)
//
// Validaciones:
//   · UUID debe existir en el observer snapshot
//   · UUID NO debe estar ya gestionado (storage_pools.btrfs_uuid)
//   · Nombre debe ser válido y único
//   · Devices del FS deben estar online (todos)
//
// Devuelve un map con la misma forma que createPoolBtrfs:
//   { "ok": true, "pool": {...} }
//   { "error": "...", "code": "...", "details": {...} }
func importPoolBtrfs(body map[string]interface{}) map[string]interface{} {
	uuid := strings.TrimSpace(bodyStr(body, "uuid"))
	name := strings.TrimSpace(bodyStr(body, "name"))

	if uuid == "" {
		return map[string]interface{}{"error": "uuid is required"}
	}
	if name == "" {
		return map[string]interface{}{"error": "name is required"}
	}
	// Reusar validación de nombre del create
	if !isValidPoolName(name) {
		return map[string]interface{}{"error": "Invalid pool name. Use alphanumeric + hyphens, max 32 chars."}
	}

	// ── Verificar observer disponible ──
	if globalObserver == nil {
		return map[string]interface{}{
			"error": "storage observer not running — cannot detect BTRFS filesystems",
		}
	}
	snap := globalObserver.Snapshot()
	if snap == nil {
		return map[string]interface{}{"error": "observer snapshot not ready"}
	}

	// ── Localizar el filesystem en el observed state ──
	var target *ObservedBtrfs
	for i := range snap.Filesystems {
		if snap.Filesystems[i].UUID == uuid {
			target = &snap.Filesystems[i]
			break
		}
	}
	if target == nil {
		return map[string]interface{}{
			"error": fmt.Sprintf("BTRFS filesystem with UUID %s not found in observed state", uuid),
			"code":  "FS_NOT_OBSERVED",
		}
	}

	// ── Validar que no está ya managed ──
	if target.IsManaged {
		return map[string]interface{}{
			"error":     fmt.Sprintf("filesystem %s already managed as pool %q", uuid, target.ManagedPoolName),
			"code":      "ALREADY_MANAGED",
			"pool_name": target.ManagedPoolName,
		}
	}

	// ── Validar devices ──
	// ANTES: un filesystem incompleto (le faltan discos) se RECHAZABA aquí
	// con "Cannot import". Eso creaba un bucle sin salida: Observados ofrecía
	// "Importar" pero siempre fallaba, y el usuario no podía montar el pool
	// degradado para recuperarlo NI repararlo (meter disco nuevo) desde la UI.
	// La única salida era "Destruir" → perder los datos de un raid1 al que solo
	// le falta un disco. Eso anula el propósito del raid1.
	//
	// AHORA: un filesystem incompleto SÍ se importa, en modo degradado,ro (más
	// abajo). El usuario recupera sus datos y puede reparar el pool añadiendo
	// el disco nuevo. Solo bloqueamos si NO hay NINGÚN disco online (no hay nada
	// que montar).
	fsDegraded := target.DevicesOnline < target.DevicesExpected
	if target.DevicesOnline == 0 {
		return map[string]interface{}{
			"error": fmt.Sprintf("filesystem no importable: 0 de %d discos online. No hay nada que montar.",
				target.DevicesExpected),
			"code":             "FS_NO_DEVICES",
			"devices_online":   target.DevicesOnline,
			"devices_expected": target.DevicesExpected,
		}
	}
	if fsDegraded {
		logMsg("importPoolBtrfs: filesystem DEGRADADO (%d/%d discos) — se importará en modo solo-lectura para permitir recuperación y reparación",
			target.DevicesOnline, target.DevicesExpected)
	}

	// ── Tomar el lock global ──
	storageMu.Lock()
	defer storageMu.Unlock()

	// ── Verificar nombre único (Beta 8.1: service v2) ──
	if storageService != nil {
		existingPools, err := storageService.ListPools(context.Background())
		if err == nil {
			for _, p := range existingPools {
				if p.Name == name {
					return map[string]interface{}{
						"error": fmt.Sprintf(`Pool "%s" already exists`, name),
						"code":  "NAME_TAKEN",
					}
				}
			}
		}
	}

	// ── Hacer scan de devices ANTES de tocar SQLite ──
	// (Mismo patrón que service.CreatePool: scan fuera de locks profundos)
	if storageService != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := storageService.ScanDevices(ctx)
		cancel()
		if err != nil {
			return map[string]interface{}{
				"error": fmt.Sprintf("pre-import scan failed: %s", err.Error()),
			}
		}
	}

	mountPoint := nimosPoolsDir + "/" + name
	opts := CmdOptions{Timeout: 30 * time.Second}

	logMsg("importPoolBtrfs: importing UUID=%s as pool %q (devices=%d, profile=%s)",
		uuid, name, target.DevicesOnline, target.Profile)

	// ── Si no está montado, montarlo ──
	if !target.IsMounted {
		logMsg("importPoolBtrfs: filesystem not mounted, mounting at %s", mountPoint)

		// Asegurar mount point existe
		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			return map[string]interface{}{
				"error": fmt.Sprintf("cannot create mount point %s: %v", mountPoint, err),
			}
		}

		// Scan BTRFS devices (necesario para multi-device FS)
		runCmd("btrfs", []string{"device", "scan"}, opts)
		time.Sleep(500 * time.Millisecond)

		// ── Detectar si el filesystem está INCOMPLETO (faltan discos) ──────
		// Un raid1 (u otro perfil multi-disco) al que le falta un miembro NO
		// se monta sin la opción `degraded` — btrfs se niega por seguridad.
		// Ese era el bucle: "Importar" fallaba al montar un pool degradado y
		// el usuario no tenía forma de importarlo/repararlo desde la UI.
		// Si faltan discos: añadimos `degraded` Y montamos READ-ONLY (`ro`):
		// escribir en un raid degradado crea bloques sin redundancia, peligroso.
		// Read-only permite VER/recuperar datos y luego reparar (añadir disco).
		mountOpts := "noatime,compress=zstd"
		if fsDegraded {
			mountOpts = "degraded,ro,noatime"
			logMsg("importPoolBtrfs: filesystem INCOMPLETO (%d/%d discos) → montando degraded,ro para permitir recuperación/reparación",
				target.DevicesOnline, target.DevicesExpected)
		}

		// Montar vía UUID (estable, independiente de paths de /dev)
		_, err := runCmd("mount", []string{
			"-t", "btrfs",
			"-o", mountOpts,
			"UUID=" + uuid,
			mountPoint,
		}, opts)
		if err != nil {
			return map[string]interface{}{
				"error": fmt.Sprintf("mount failed: %v", err),
			}
		}

		// Verificar mount real
		verifyRes, _ := runCmd("findmnt", []string{"-n", "-o", "SOURCE", mountPoint}, opts)
		if strings.TrimSpace(verifyRes.Stdout) == "" {
			return map[string]interface{}{
				"error": fmt.Sprintf("mount verification failed at %s", mountPoint),
			}
		}
		logMsg("importPoolBtrfs: mounted at %s (source: %s)",
			mountPoint, strings.TrimSpace(verifyRes.Stdout))
	} else {
		// Ya estaba montado en otro mount point. Lo dejamos donde estaba.
		mountPoint = target.MountPoint
		logMsg("importPoolBtrfs: filesystem already mounted at %s, using that path", mountPoint)
	}

	// ── Profile: si el observer no detectó uno, asumimos "single" ──
	profile := target.Profile
	if profile == "" {
		profile = "single"
	}

	// ── Persistir pool en SQLite directamente (Beta 8.1 Bloque C: sin adapter) ──
	if storageService == nil {
		return map[string]interface{}{"error": "storage service not initialized"}
	}
	ctx := context.Background()

	// Resolver device IDs en SQLite a partir de los by-id paths del observer
	deviceIDs := make([]string, 0, len(target.Devices))
	for _, d := range target.Devices {
		if d.ByIDPath == "" {
			continue
		}
		dev, err := storageService.repo.GetDeviceByByIDPath(ctx, d.ByIDPath)
		if err != nil || dev == nil {
			continue
		}
		deviceIDs = append(deviceIDs, dev.ID)
	}
	if len(deviceIDs) == 0 {
		return map[string]interface{}{"error": "could not resolve any device by by-id path"}
	}

	// Determinar si es el primer pool managed → primary
	existingPools, _ := storageService.repo.ListPools(ctx)
	isFirst := len(existingPools) == 0

	poolID := newUUID()
	newPool := &Pool{
		ID:           poolID,
		Name:         name,
		BtrfsUUID:    uuid,
		Profile:      Profile(profile),
		MountPoint:   mountPoint,
		Role:         RoleData,
		ControlState: ControlStateManaged,
		Compression:  "zstd:3",
	}

	err := storageService.runInTx(ctx, func(tx *sql.Tx) error {
		if err := storageService.repo.CreatePool(ctx, tx, newPool); err != nil {
			return err
		}
		for _, devID := range deviceIDs {
			if err := storageService.repo.AssignDeviceToPool(ctx, tx, poolID, devID); err != nil {
				return err
			}
		}
		if err := storageService.repo.SetPoolCapabilities(ctx, tx, poolID,
			DefaultBtrfsManagedCapabilities()); err != nil {
			return err
		}
		// Si es el primer pool managed, marcarlo como primary
		if isFirst {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO storage_metadata (key, value) VALUES ('primary_pool', ?)`,
				poolID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO storage_metadata (key, value) VALUES ('configured_at', ?)`,
				time.Now().UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logMsg("importPoolBtrfs: SQLite write failed: %v", err)
		return map[string]interface{}{"error": fmt.Sprintf("DB write failed: %s", err)}
	}

	// ── Añadir entrada de fstab para persistencia ──
	appendFstab(uuid, mountPoint, "btrfs")

	logMsg("importPoolBtrfs: imported UUID=%s as %q (mount=%s, primary=%v)",
		uuid, name, mountPoint, isFirst)

	// ── Notificar al observer ──
	notifyStorageChanged()

	return map[string]interface{}{
		"ok": true,
		"pool": map[string]interface{}{
			"name":       name,
			"type":       "btrfs",
			"profile":    profile,
			"mountPoint": mountPoint,
			"uuid":       uuid,
		},
		"imported_devices": target.DevicesOnline,
	}
}

// isValidPoolName valida el slug del pool (reusable desde createPoolBtrfs).
func isValidPoolName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	// alfanumérico + guiones
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-') {
			return false
		}
	}
	// nombres reservados
	reserved := map[string]bool{
		"system": true, "config": true, "temp": true,
		"swap": true, "root": true, "boot": true,
	}
	return !reserved[strings.ToLower(name)]
}
