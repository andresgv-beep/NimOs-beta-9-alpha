package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// deviceIsAssigned devuelve true si el device está asignado a algún pool.
func (s *StorageService) deviceIsAssigned(ctx context.Context, deviceID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_pool_devices WHERE device_id = ?`,
		deviceID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("deviceIsAssigned: %w", err)
	}
	return count > 0, nil
}

// ScanDevices ejecuta el scanner y persiste los resultados en la DB.
//
// Comportamiento:
//   - Cada disco visto por el scanner se hace UPSERT por serial (identidad absoluta)
//   - Devices ya en DB cuyo current_path cambió se actualizan
//   - Devices en DB que NO aparecen en el scan NO se borran (auditoría;
//     se marcarán como "missing" por el reconciler de Fase 4)
//   - Devices sin serial son rechazados (storage_invariants.md#3.3)
//   - Devices sin by_id_path se loggean como warning pero se intentan persistir
//     (por si en el siguiente scan udev los expone)
//
// Idempotente: se puede ejecutar muchas veces sin efectos secundarios.
//
// see docs/storage_state_machines.md §5 (Device lifecycle)
func (s *StorageService) ScanDevices(ctx context.Context) (*ScanResult, error) {
	scanned, err := s.scanner.ScanDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("ScanDevices: scanner failed: %w", err)
	}

	result := &ScanResult{Total: len(scanned)}

	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		for _, sd := range scanned {
			if sd.Serial == "" {
				// Defensive: el scanner ya filtra esto, pero confirmamos
				result.Skipped++
				continue
			}

			// Construir el Device (si by_id_path está vacío, usar device_path
			// como fallback temporal; siguiente scan lo corregirá)
			byIDPath := sd.ByIDPath
			if byIDPath == "" {
				byIDPath = sd.DevicePath
				logMsg("ScanDevices: warning, %s has no by-id symlink yet", sd.Serial)
			}

			dev := &Device{
				ID:          newUUID(), // se ignora si ya existe (UpsertDevice usa serial)
				Serial:      sd.Serial,
				ByIDPath:    byIDPath,
				CurrentPath: sd.DevicePath,
				WWN:         sd.WWN,
				Model:       sd.Model,
				SizeBytes:   sd.SizeBytes,
				LastSeenAt:  s.clock.Now().UTC(),
			}

			// UpsertDevice devuelve true si fue insert (nuevo), false si update.
			wasInsert, err := s.repo.UpsertDevice(ctx, tx, dev)
			if err != nil {
				return fmt.Errorf("ScanDevices: upsert %s: %w", sd.Serial, err)
			}
			if wasInsert {
				result.Inserted++
			} else {
				result.Updated++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	logMsg("ScanDevices: total=%d inserted=%d updated=%d skipped=%d",
		result.Total, result.Inserted, result.Updated, result.Skipped)
	return result, nil
}

// resolveDevicePath devuelve el path utilizable de un device, prefiriendo la
// identidad estable (by-id) pero verificando que exista de verdad en /dev.
// Si el by-id está obsoleto (symlink muerto), cae a current_path. Si ninguno
// existe, devuelve "" (el caller debe fallar la operación con mensaje claro).
//
// Regla 16 · SOT-04: nunca confiar en un path cacheado sin verificar que vive.
// Centraliza la lógica que antes estaba inline solo en AddDevice, para que
// Remove/Replace/Wipe la compartan.
func resolveDevicePath(d *Device) string {
	if d == nil {
		return ""
	}
	if d.ByIDPath != "" && devicePathExists(d.ByIDPath) {
		return d.ByIDPath
	}
	if d.CurrentPath != "" && devicePathExists(d.CurrentPath) {
		return d.CurrentPath
	}
	return ""
}

func (s *StorageService) AddDevice(ctx context.Context, req AddDeviceRequest) (*Operation, error) {
	// HARD-3 fix: lock global. Ver comentario en CreatePool.
	storageMu.Lock()
	defer storageMu.Unlock()

	// ─── Validaciones ──────────────────────────────────────────────────
	pool, err := s.GetPool(ctx, req.PoolID)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeAddDevice); err != nil {
		return nil, err
	}

	// FIX-1: gate de montaje. `btrfs device add` exige el pool montado; sin esto
	// un pool no montado da el críptico "not a btrfs filesystem".
	if err := assertLayoutOpAllowed(pool); err != nil {
		return nil, err
	}

	// El frontend identifica el disco libre por su /dev/path (la lista de
	// discos eligible viene de lsblk, no expone el ID interno). Resolvemos
	// path → ID buscando en los devices registrados.
	if req.DeviceID == "" && req.DevicePath != "" {
		allDevices, err := s.repo.ListDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("AddDevice: list devices: %w", err)
		}
		for _, d := range allDevices {
			if d.CurrentPath == req.DevicePath {
				req.DeviceID = d.ID
				break
			}
		}
		if req.DeviceID == "" {
			return nil, errFromCode(ErrCodeDeviceNotFound,
				fmt.Sprintf("device path %q not registered (run scan first?)", req.DevicePath))
		}
	}

	device, err := s.repo.GetDevice(ctx, req.DeviceID)
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, errFromCode(ErrCodeDeviceNotFound,
			fmt.Sprintf("device %q not found", req.DeviceID))
	}

	inUse, err := s.deviceIsAssigned(ctx, device.ID)
	if err != nil {
		return nil, err
	}
	if inUse {
		return nil, errFromCode(ErrCodeDeviceInUse,
			fmt.Sprintf("device %q is already in a pool", device.ID))
	}

	// ─── Crear Operation ───────────────────────────────────────────────
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeAddDevice,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"device_id":     device.ID,
			"device_serial": device.Serial,
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		// Si esto falla por INV-1 (UNIQUE parcial), error útil al caller
		return nil, errFromCode(ErrCodeOperationInProgress,
			fmt.Sprintf("another layout operation is in progress on pool %s", pool.ID))
	}

	// ─── Wipe defensivo opcional ───────────────────────────────────────
	if req.WipeFirst {
		wipePath := resolveDevicePath(device)
		if wipePath == "" {
			s.markOperationFailed(ctx, op.ID,
				fmt.Sprintf("ningún path del device existe en /dev para wipe (by-id=%q current=%q)",
					device.ByIDPath, device.CurrentPath),
				ErrCodeBtrfsCommandFailed)
			return s.repo.GetOperation(ctx, op.ID)
		}
		if err := s.btrfs.WipeDevice(ctx, wipePath); err != nil {
			s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
			return s.repo.GetOperation(ctx, op.ID)
		}
	}

	// ─── Ejecutar btrfs device add ─────────────────────────────────────
	// Regla 16 · SOT-04: resolver el path verificando que existe (by-id
	// preferido, fallback a current_path). Evita el bug del by-id obsoleto.
	addPath := resolveDevicePath(device)
	if addPath == "" {
		s.markOperationFailed(ctx, op.ID,
			fmt.Sprintf("ningún path del device existe en /dev (by-id=%q current=%q); reconecta el disco o re-escanea",
				device.ByIDPath, device.CurrentPath),
			ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}
	if err := s.btrfs.AddDevice(ctx, pool.MountPoint, addPath); err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// ─── Persistir asignación ──────────────────────────────────────────
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.AssignDeviceToPool(ctx, tx, pool.ID, device.ID); err != nil {
			return err
		}
		return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusCompleted, nil, nil)
	})
	if err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeInternal)
		return s.repo.GetOperation(ctx, op.ID)
	}

	return s.repo.GetOperation(ctx, op.ID)
}

// RemoveDevice quita un device del pool. BTRFS hace balance implícito
// (mueve datos del device a los demás). Operación pesada — puede tardar.
//
// Validaciones:
//   - Pool managed, capability remove_device
//   - Device pertenece al pool indicado
//   - Tras quitar este device, el pool sigue teniendo >= MinDisks()
//     para su profile (no degradar el profile)
func (s *StorageService) RemoveDevice(ctx context.Context, req RemoveDeviceRequest) (*Operation, error) {
	// HARD-3 fix: lock global. Ver comentario en CreatePool.
	storageMu.Lock()
	defer storageMu.Unlock()

	pool, err := s.GetPool(ctx, req.PoolID)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeRemoveDevice); err != nil {
		return nil, err
	}

	// FIX-1: gate de montaje. `btrfs device remove` exige el pool montado.
	if err := assertLayoutOpAllowed(pool); err != nil {
		return nil, err
	}

	// Buscar el device en este pool
	var device *Device
	for i := range pool.Devices {
		if pool.Devices[i].ID == req.DeviceID {
			device = &pool.Devices[i]
			break
		}
	}
	if device == nil {
		return nil, errFromCode(ErrCodeDeviceNotFound,
			fmt.Sprintf("device %q is not part of pool %s", req.DeviceID, pool.ID))
	}

	// No bajar del mínimo del profile
	if len(pool.Devices)-1 < pool.Profile.MinDisks() {
		return nil, errFromCode(ErrCodeMinDisksReached,
			fmt.Sprintf("cannot remove device: profile %s requires at least %d disks",
				pool.Profile, pool.Profile.MinDisks()))
	}

	// Operation
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeRemoveDevice,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"device_id":     device.ID,
			"device_serial": device.Serial,
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return nil, errFromCode(ErrCodeOperationInProgress,
			fmt.Sprintf("another layout operation is in progress on pool %s", pool.ID))
	}

	// Ejecutar btrfs device remove
	// Regla 16 · SOT-04: path verificado (by-id obsoleto → fallback).
	rmPath := resolveDevicePath(device)
	if rmPath == "" {
		s.markOperationFailed(ctx, op.ID,
			fmt.Sprintf("ningún path del device existe en /dev (by-id=%q current=%q); reconecta el disco o re-escanea",
				device.ByIDPath, device.CurrentPath),
			ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}
	if err := s.btrfs.RemoveDevice(ctx, pool.MountPoint, rmPath); err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// Persistir desasignación
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.UnassignDeviceFromPool(ctx, tx, pool.ID, device.ID); err != nil {
			return err
		}
		return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusCompleted, nil, nil)
	})
	if err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeInternal)
		return s.repo.GetOperation(ctx, op.ID)
	}

	return s.repo.GetOperation(ctx, op.ID)
}

// ReplaceDevice sustituye un device dentro del pool por otro. Más eficiente
// que remove+add porque btrfs replace start sincroniza desde los demás
// miembros sin un balance completo.
//
// IMPORTANTE: el old device se hace wipefs SEGURO (no a ciegas).
// see docs/storage_invariants.md#4.2
func (s *StorageService) ReplaceDevice(ctx context.Context, req ReplaceDeviceRequest) (*Operation, error) {
	// HARD-3 fix: lock global. Ver comentario en CreatePool.
	storageMu.Lock()
	defer storageMu.Unlock()

	pool, err := s.GetPool(ctx, req.PoolID)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeReplaceDevice); err != nil {
		return nil, err
	}

	// FIX-1: gate de montaje. `btrfs replace start` exige el pool montado
	// (también en degradado rw, el caso típico de reparación).
	if err := assertLayoutOpAllowed(pool); err != nil {
		return nil, err
	}

	// Old debe estar en el pool
	var oldDev *Device
	for i := range pool.Devices {
		if pool.Devices[i].ID == req.OldDeviceID {
			oldDev = &pool.Devices[i]
			break
		}
	}
	if oldDev == nil {
		return nil, errFromCode(ErrCodeDeviceNotFound,
			fmt.Sprintf("old device %q is not part of pool %s", req.OldDeviceID, pool.ID))
	}

	// New debe existir y NO estar en otro pool. Puede venir identificado por:
	//   - id de BD (storage_devices) — si ya estaba registrado, o
	//   - path (/dev/sdX) o serial — si es un disco LIBRE recién insertado que
	//     aún no tiene fila en storage_devices (caso típico de reparación: metes
	//     un disco nuevo y lo reemplazas sin haberlo "asignado" antes).
	newDev, err := s.repo.GetDevice(ctx, req.NewDeviceID)
	if err != nil {
		return nil, err
	}
	if newDev == nil {
		// No está por id → intentar resolver por serial y por path desde el
		// escaneo en vivo del sistema (la realidad del kernel).
		newDev = s.resolveFreeDeviceByPathOrSerial(ctx, req.NewDeviceID)
	}
	if newDev == nil {
		return nil, errFromCode(ErrCodeDeviceNotFound,
			fmt.Sprintf("new device %q not found", req.NewDeviceID))
	}
	inUse, err := s.deviceIsAssigned(ctx, newDev.ID)
	if err != nil {
		return nil, err
	}
	if inUse {
		return nil, errFromCode(ErrCodeDeviceInUse,
			fmt.Sprintf("new device %q is already in a pool", newDev.ID))
	}

	// Validar tamaño del nuevo >= old (BTRFS no permite shrink implícito)
	if newDev.SizeBytes < oldDev.SizeBytes {
		return nil, errFromCode(ErrCodeDeviceNotEligible,
			fmt.Sprintf("new device size (%d) < old device size (%d)",
				newDev.SizeBytes, oldDev.SizeBytes))
	}

	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeReplaceDevice,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id":     oldDev.ID,
			"old_device_serial": oldDev.Serial,
			"new_device_id":     newDev.ID,
			"new_device_serial": newDev.Serial,
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return nil, errFromCode(ErrCodeOperationInProgress,
			fmt.Sprintf("another layout operation is in progress on pool %s", pool.ID))
	}

	// ── Asegurar que el pool es ESCRIBIBLE antes del replace ───────────────
	// Un raid degradado lo montamos en `degraded,ro` por seguridad (no escribir
	// sin redundancia). Pero `btrfs replace` NECESITA escribir (copia los datos
	// al disco nuevo), y falla con "Never started" sobre un FS read-only.
	// Por eso, justo para la reparación, remontamos `degraded,rw`. Es un riesgo
	// calculado y consciente: durante el replace se escribe sin redundancia,
	// pero es la ÚNICA forma de reconstruirla. Al terminar, el pool queda
	// completo y se puede remontar rw normal.
	remountedForRepair := false
	if poolMountIsReadOnly(pool.MountPoint) {
		logMsg("ReplaceDevice: pool %s está en read-only; remontando degraded,rw para permitir la reparación", pool.MountPoint)
		if err := remountPoolReadWriteDegraded(pool.MountPoint); err != nil {
			s.markOperationFailed(ctx, op.ID,
				fmt.Sprintf("no se pudo poner el pool en modo escritura para repararlo: %v", err),
				ErrCodeBtrfsCommandFailed)
			return s.repo.GetOperation(ctx, op.ID)
		}
		remountedForRepair = true
	}

	// Ejecutar btrfs replace (incluye wipefs seguro del old)
	// Regla 16 · SOT-04: ambos paths verificados. El NEW debe existir sí o sí
	// (es el disco que entra); el OLD puede estar muerto (justamente por eso
	// se reemplaza), así que si su by-id no resuelve, btrfs replace admite el
	// devid — pero priorizamos un path vivo cuando lo haya.
	newPath := resolveDevicePath(newDev)
	if newPath == "" {
		s.markOperationFailed(ctx, op.ID,
			fmt.Sprintf("el device nuevo no existe en /dev (by-id=%q current=%q); reconecta o re-escanea",
				newDev.ByIDPath, newDev.CurrentPath),
			ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}
	oldPath := resolveDevicePath(oldDev)
	if oldPath == "" {
		// El viejo puede estar físicamente muerto (caso típico de replace).
		// Usamos su by-id cacheado como mejor esfuerzo: btrfs replace puede
		// resolverlo por devid aunque el symlink ya no exista.
		oldPath = oldDev.ByIDPath
	}
	if err := s.btrfs.ReplaceDevice(ctx, pool.MountPoint, oldPath, newPath); err != nil {
		// Si habíamos puesto el pool rw SOLO para reparar, revertirlo a ro tras el
		// fallo: un pool degradado en escritura sin redundancia es el peor estado.
		// Best-effort: si el revert falla, lo logueamos pero el fallo del replace
		// es lo que manda en la operación.
		if remountedForRepair {
			if rerr := remountPoolReadOnlyDegraded(pool.MountPoint); rerr != nil {
				logMsg("ReplaceDevice: no se pudo revertir %s a ro tras fallo del replace: %v", pool.MountPoint, rerr)
			}
		}
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// Swap atómico: desasignar old, asignar new
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.UnassignDeviceFromPool(ctx, tx, pool.ID, oldDev.ID); err != nil {
			return err
		}
		if err := s.repo.AssignDeviceToPool(ctx, tx, pool.ID, newDev.ID); err != nil {
			return err
		}
		return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusCompleted, nil, nil)
	})
	if err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeInternal)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// Redundancia restaurada (el replace con -B ya terminó). Lanzamos un scrub
	// para VERIFICAR que la copia reconstruida es íntegra (detecta/corrige bit-rot
	// silencioso contra la otra copia). No bloqueante: `btrfs scrub start` corre
	// en el kernel. Best-effort: si no arranca, el replace ya está completo y no
	// se invalida — solo se loguea.
	if serr := startScrubOnPool(pool.MountPoint, pool.Name); serr != nil {
		logMsg("ReplaceDevice: no se pudo lanzar el scrub de verificación post-replace en %s: %v", pool.MountPoint, serr)
	}

	return s.repo.GetOperation(ctx, op.ID)
}

// checkDevicesAvailable verifica que los devices están realmente disponibles
// para crear un pool nuevo. Detecta filesystems o estructuras de storage
// preexistentes que NimOS borraría silenciosamente si procediera.
//
// Devuelve:
//
//	nil                           si todos los devices están limpios
//	*ErrDiskHasFilesystem         si algún device tiene BTRFS detectable
//	                              (con detalles managed/observed para UX)
//	error genérico                otros fallos (disco missing, boot disk,
//	                              holders activos: LVM/dm/RAID)
//
// Delega en s.deviceChecker (inyectable). Si no está seteado, usa el
// checker de producción (realDevicePreFlightCheck → preFlightCheck BTRFS).
// Los tests inyectan noop para no requerir devices físicos en /dev.
//
// PUNTO DE EXTENSIÓN (DEUDA-ARQUI-OBSERVABLE-ENTITY, Beta 9):
// Cuando NimOS soporte detección de otras entidades observables
// (ext4, mdraid, LUKS, ZFS reimport, NTFS/exFAT en USB...), se cambia
// la implementación de DeviceChecker — su firma y su llamada desde
// CreatePool no cambian.
//
// see storage_wipe.go: preFlightCheck, ErrDiskHasFilesystem
func (s *StorageService) checkDevicesAvailable(ctx context.Context, devices []*Device) error {
	checker := s.deviceChecker
	if checker == nil {
		checker = realDevicePreFlightCheck
	}
	return checker(devices)
}

// noopDeviceChecker es un DeviceChecker que siempre pasa. Útil en tests
// donde los devices son fake (no existen en /dev) y queremos verificar
// la lógica de CreatePool sin que el preflight real falle por stat().
//
// Producción NUNCA debe usar esto — es solo para inyectar en tests
// vía service.deviceChecker = noopDeviceChecker.
func noopDeviceChecker(devices []*Device) error {
	return nil
}

// resolveFreeDeviceByPathOrSerial intenta encontrar un device por su path
// (/dev/sdX) o serial cuando NO se encontró por id de BD. Caso de uso: reparar
// un pool metiendo un disco LIBRE recién insertado que aún no tiene fila propia
// en storage_devices (o que el frontend referenció por path).
//
// Estrategia (Regla 16, el kernel manda):
//  1. Forzar un ScanDevices → registra/actualiza los discos presentes.
//  2. Buscar en la lista actualizada por CurrentPath exacto o por Serial.
//
// Devuelve nil si no se encuentra ningún disco presente con ese path/serial.
func (s *StorageService) resolveFreeDeviceByPathOrSerial(ctx context.Context, ref string) *Device {
	if ref == "" {
		return nil
	}
	// 1. Refrescar el inventario con la realidad del sistema.
	if _, err := s.ScanDevices(ctx); err != nil {
		logMsg("resolveFreeDeviceByPathOrSerial: ScanDevices falló: %v", err)
		// seguimos: puede que ya esté en BD de un scan previo
	}

	devices, err := s.repo.ListDevices(ctx)
	if err != nil {
		logMsg("resolveFreeDeviceByPathOrSerial: ListDevices falló: %v", err)
		return nil
	}

	// Normalizar: aceptar "/dev/sdb" o "sdb".
	refPath := ref
	if !strings.HasPrefix(refPath, "/dev/") {
		refPath = "/dev/" + refPath
	}

	for _, d := range devices {
		if d.CurrentPath == refPath || d.CurrentPath == ref {
			return d
		}
		if d.Serial != "" && d.Serial == ref {
			return d
		}
		if d.ByIDPath != "" && d.ByIDPath == ref {
			return d
		}
	}
	return nil
}

// remountPoolReadWriteDegraded remonta un pool que está en read-only a
// `degraded,rw`, SIN desmontarlo (mount -o remount). Necesario para reparar:
// btrfs replace requiere escritura, pero un raid degradado lo montamos ro por
// seguridad. Este remonta in-situ para permitir la reparación.
//
// Usa `remount` (no umount+mount) porque:
//   - No interrumpe procesos con el pool abierto.
//   - Es atómico desde el punto de vista del usuario.
//   - Conserva el resto de opciones de montaje.
//
// Inyectable para tests.
var remountPoolReadWriteDegraded = func(mountPoint string) error {
	// remount,rw,degraded: quita el ro, mantiene degraded (falta un disco).
	out, ok := runSafe("mount", "-o", "remount,rw,degraded", mountPoint)
	if !ok {
		return fmt.Errorf("mount remount rw degraded falló en %s: %s", mountPoint, strings.TrimSpace(out))
	}
	// Verificar que de verdad quedó en rw (la realidad manda).
	if poolMountIsReadOnly(mountPoint) {
		return fmt.Errorf("el pool %s sigue en read-only tras el remount", mountPoint)
	}
	logMsg("remountPoolReadWriteDegraded: %s remontado en degraded,rw para reparación", mountPoint)
	return nil
}

// remountPoolReadOnlyDegraded revierte un pool a `degraded,ro` (el estado seguro
// de un raid sin redundancia) tras un intento de reparación fallido. Contraparte
// de remountPoolReadWriteDegraded: si pusimos el pool en escritura solo para el
// replace y este falló, no debe quedarse rw degradado. Inyectable para tests.
var remountPoolReadOnlyDegraded = func(mountPoint string) error {
	out, ok := runSafe("mount", "-o", "remount,ro,degraded", mountPoint)
	if !ok {
		return fmt.Errorf("mount remount ro degraded falló en %s: %s", mountPoint, strings.TrimSpace(out))
	}
	logMsg("remountPoolReadOnlyDegraded: %s revertido a degraded,ro tras fallo de reparación", mountPoint)
	return nil
}
