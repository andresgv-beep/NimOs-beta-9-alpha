package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// RenamePool cambia el nombre legible de un pool.
// Síncrona. El id interno no cambia. Las shares siguen funcionando.
func (s *StorageService) RenamePool(ctx context.Context, id, newName string) (*Operation, error) {
	pool, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, errFromCode(ErrCodePoolNotFound, fmt.Sprintf("pool %s not found", id))
	}

	if err := s.checkPolicy(pool, OpTypeRenamePool); err != nil {
		return nil, err
	}

	// Verificar que el nombre no esté tomado
	other, err := s.repo.GetPoolByName(ctx, newName)
	if err != nil {
		return nil, err
	}
	if other != nil && other.ID != pool.ID {
		return nil, errFromCode(ErrCodePoolNameTaken,
			fmt.Sprintf("pool name %q already in use", newName))
	}

	// Crear Operation + ejecutar dentro de la misma tx
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeRenamePool,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]string{"from": pool.Name, "to": newName}),
	}

	// ── Renombrar es una operación COMPLETA, no solo cambiar la BD ──────────
	// Regla 16: la etiqueta del filesystem y el mount point los posee el
	// sistema (BTRFS/kernel/fstab), no la BD. Un rename que solo cambia el
	// nombre en BD deja el pool desincronizado: etiqueta vieja en disco, fstab
	// apuntando a una ruta que no monta → el pool queda sin montar y cualquier
	// servicio escribe en el directorio vacío sobre el disco de sistema.
	// (Este es el bug que dejaba data6→data9 a medias.)
	//
	// El orden importa y debe ser reversible: primero los cambios físicos
	// (que pueden fallar), y solo si TODOS salen bien, confirmamos en BD.
	oldMountPoint := pool.MountPoint
	newMountPoint := filepath.Join("/nimos/pools", newName)

	// El rename físico (etiqueta BTRFS + fstab + remontaje) es inyectable para
	// tests: en producción aplica los cambios reales; en tests se sustituye por
	// un stub que no toca el sistema. La lógica pura (transformFstabMountPoint)
	// se testea aparte.
	if err := applyPoolRenamePhysicalFn(s, ctx, pool, newName, oldMountPoint, newMountPoint); err != nil {
		// Algo físico falló. NO tocamos la BD → el pool sigue coherente con su
		// nombre viejo. Registramos la op como fallida.
		_ = s.runInTx(ctx, func(tx *sql.Tx) error {
			if e := s.repo.CreateOperation(ctx, tx, op); e != nil {
				return e
			}
			return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusFailed, nil, strPtr(err.Error()))
		})
		return nil, errFromCode(ErrCodeBtrfsCommandFailed,
			fmt.Sprintf("no se pudo renombrar el pool físicamente: %v (la BD no se modificó, el pool sigue como %q)", err, pool.Name))
	}

	// Los cambios físicos salieron bien. Ahora confirmamos en BD (nombre +
	// mount point nuevos) de forma atómica.
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateOperation(ctx, tx, op); err != nil {
			return err
		}
		if err := s.repo.RenamePool(ctx, tx, pool.ID, newName); err != nil {
			return err
		}
		if err := s.repo.SetPoolMountPoint(ctx, tx, pool.ID, newMountPoint); err != nil {
			return err
		}
		return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusCompleted, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	// Recargar la operation con su completed_at actualizado
	return s.repo.GetOperation(ctx, op.ID)
}

// SetPoolCompression cambia la compresión de un pool.
// Síncrona. Solo afecta a archivos escritos a partir del cambio.
func (s *StorageService) SetPoolCompression(ctx context.Context, id, algorithm string) (*Operation, error) {
	// Usar GetPool del service (no del repo) porque hidrata capabilities,
	// que policy necesita para validar la op.
	pool, err := s.GetPool(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeSetCompression); err != nil {
		return nil, err
	}

	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeSetCompression,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data:   rawJSON(map[string]string{"from": pool.Compression, "to": algorithm}),
	}

	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreateOperation(ctx, tx, op); err != nil {
			return err
		}
		if err := s.repo.SetPoolCompression(ctx, tx, pool.ID, algorithm); err != nil {
			return err
		}
		return s.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusCompleted, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	return s.repo.GetOperation(ctx, op.ID)
}

// CreatePool crea un nuevo pool BTRFS con los devices indicados.
// Asíncrona conceptualmente (genera Operation) pero ejecuta inline en
// Beta 8. El frontend hace polling vía la Operation.
//
// Pasos:
//  1. Validar request (name único, devices existen y están libres, profile válido)
//  2. Crear Operation con status in_progress
//  3. Ejecutar btrfs (mkfs, mount, identity file)
//  4. Persistir pool + assignments + capabilities en DB
//  5. Marcar Operation completed (o failed con rollback)
//  6. Devolver la Operation
func (s *StorageService) CreatePool(ctx context.Context, req CreatePoolRequest) (*Operation, error) {
	// HARD-3 fix: lock global de mutación storage. Garantiza que
	// CreatePool no haga race con otro CreatePool/Destroy/Export
	// que toque los mismos discos. Sin esto, dos creates concurrentes
	// con `mkfs.btrfs -f` (cuando WipeFirst=true) podrían sobreescribirse.
	// Es el mismo mutex que ya usan destroy/export/wipe; CreatePool
	// debe estar bajo la misma disciplina.
	storageMu.Lock()
	defer storageMu.Unlock()

	// ─── Validación + normalización (Disks → DeviceIDs si aplica) ──────
	// req.Validate() es el contrato:
	//   · Name no vacío
	//   · Profile válido
	//   · Exactamente uno de Disks o DeviceIDs presente
	//   · Después de Validate, req.DeviceIDs siempre está poblado
	//   · DeviceIDs cumple req.Profile.MinDisks()
	if err := req.Validate(ctx, s.repo); err != nil {
		return nil, err
	}

	// ¿Nombre ya tomado?
	existing, err := s.repo.GetPoolByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, errFromCode(ErrCodePoolNameTaken,
			fmt.Sprintf("pool name %q already in use", req.Name))
	}

	// ¿Devices existen y están libres? (Validate ya garantizó que existen
	// y son válidos como entidades; aquí verificamos disponibilidad.)
	devices := make([]*Device, 0, len(req.DeviceIDs))
	for _, id := range req.DeviceIDs {
		d, err := s.repo.GetDevice(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			// Race posible: el device existía cuando Validate corrió pero
			// fue borrado entre medias. Mensaje claro.
			return nil, errFromCode(ErrCodeDeviceNotFound,
				fmt.Sprintf("device %q not found (concurrent delete?)", id))
		}
		// ¿Ya está en un pool?
		inPool, err := s.deviceIsAssigned(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		if inPool {
			return nil, errFromCode(ErrCodeDeviceInUse,
				fmt.Sprintf("device %q is already in a pool", d.ID))
		}
		devices = append(devices, d)
	}

	// ─── Pre-flight: detectar storage existente en los discos ──────────
	// Bloque C3.4 (Beta 8.1) — protección contra pérdida silenciosa de
	// datos. Si algún disco tiene un filesystem detectable, abortamos
	// con error tipado para que la UI muestre el wizard de doble
	// intención (importar vs destruir).
	//
	// WipeFirst=true salta el check: el usuario aceptó conscientemente
	// destruir lo que haya en los discos.
	//
	// El error tipado *ErrDiskHasFilesystem fluye TAL CUAL hasta
	// writeServiceError, que lo serializa con error.details rico para
	// la UI. No lo envolvemos en ServiceError para no perder los campos.
	//
	// DEUDA-ARQUI-OBSERVABLE-ENTITY (Beta 9): hoy checkDevicesAvailable
	// invoca preFlightCheck BTRFS-céntrico (vía DeviceChecker inyectable).
	// El día que NimOS soporte otros tipos de entidad (ext4, LUKS,
	// mdraid, NTFS USB...), solo cambia la impl del DeviceChecker — su
	// firma y su llamada desde CreatePool no cambian. Punto de extensión
	// deliberado.
	if !req.WipeFirst {
		if err := s.checkDevicesAvailable(ctx, devices); err != nil {
			return nil, err
		}
	}

	// ─── Crear Operation con status in_progress ────────────────────────
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeCreatePool,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       req.Name,
			"profile":    string(req.Profile),
			"device_ids": req.DeviceIDs,
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return nil, err
	}

	// ─── Ejecutar BTRFS ────────────────────────────────────────────────
	byIDPaths := make([]string, len(devices))
	for i, d := range devices {
		byIDPaths[i] = d.ByIDPath
	}

	fsInfo, err := s.btrfs.CreateFilesystem(ctx, CreateFilesystemRequest{
		Label:     req.Name,
		Profile:   req.Profile,
		ByIDPaths: byIDPaths,
		WipeFirst: req.WipeFirst,
	})
	if err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// Montar. Usamos resolveDevicePath (verifica que el by-id EXISTE y, si no,
	// cae a CurrentPath) en vez del ByIDPath crudo. Un by-id obsoleto/ausente
	// hacía que el mount fallara o montara raro, dejando el pool sin montar y
	// la carpeta sobre el disco de sistema. (Mismo patrón ya aplicado en las
	// device ops; aquí faltaba.)
	mountPoint := filepath.Join("/nimos/pools", req.Name)
	mountDevice := resolveDevicePath(devices[0])
	if err := s.btrfs.MountFilesystem(ctx, mountDevice, mountPoint); err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeMountFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// VERIFICAR que el montaje ocurrió de verdad. `mount` puede devolver éxito
	// pero dejar el filesystem mal montado, o montarlo en un sitio que cae
	// sobre el disco de sistema. Si isPoolMounted dice que NO está montado
	// sobre un dispositivo distinto de /, es un fallo: no dejamos el pool a
	// medias (creado pero escribiendo en el disco de sistema).
	if !verifyPoolMountedFn(mountPoint) {
		logMsg("CreatePool: ERROR — %s creado pero NO quedó montado (mount aparente OK pero isPoolMounted=false). Device=%s", mountPoint, mountDevice)
		s.markOperationFailed(ctx, op.ID,
			fmt.Sprintf("el pool se creó pero no se pudo montar en %s (¿by-id inválido?)", mountPoint),
			ErrCodeMountFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}
	logMsg("CreatePool: %s montado y verificado sobre %s", mountPoint, mountDevice)

	// ─── Persistir en /etc/fstab para que sobreviva al reinicio ──────────
	// Simétrico con importPoolBtrfs (storage_btrfs_import.go), que ya lo hace.
	// SIN esto el pool no se remonta al arrancar y udisks2 (auto-mount del
	// escritorio) lo monta en /media/<user>/, dejándolo fuera de /nimos/pools/
	// → NimOS no lo encuentra y los servicios dependientes entran en error.
	// appendFstab ya incluye `nofail` (no rompe el boot si falta el disco).
	appendFstab(fsInfo.BtrfsUUID, mountPoint, "btrfs")

	// Validar que la entrada no rompió fstab (un fstab malformado puede
	// impedir el siguiente arranque). Si falla, lo dejamos registrado.
	if vr, verr := runCmd("findmnt", []string{"--verify"}, CmdOptions{Timeout: 10 * time.Second}); verr != nil || strings.TrimSpace(vr.Stderr) != "" {
		logMsg("CreatePool: WARNING findmnt --verify tras appendFstab: err=%v stderr=%s", verr, strings.TrimSpace(vr.Stderr))
	}

	// ─── Persistir en DB (pool + devices + capabilities) ───────────────
	poolID := newUUID()
	pool := &Pool{
		ID:           poolID,
		Name:         req.Name,
		BtrfsUUID:    fsInfo.BtrfsUUID,
		Profile:      req.Profile,
		MountPoint:   mountPoint,
		Role:         RoleData,
		ControlState: ControlStateManaged,
		Compression:  defaultIfEmpty(req.Compression, "none"),
	}

	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.CreatePool(ctx, tx, pool); err != nil {
			return err
		}
		for _, d := range devices {
			if err := s.repo.AssignDeviceToPool(ctx, tx, poolID, d.ID); err != nil {
				return err
			}
		}
		if err := s.repo.SetPoolCapabilities(ctx, tx, poolID,
			DefaultBtrfsManagedCapabilities()); err != nil {
			return err
		}
		// Actualizar la operation con el pool_id ahora que lo conocemos
		op.PoolID = &poolID
		if _, err := tx.ExecContext(ctx,
			`UPDATE storage_operations SET pool_id = ? WHERE id = ?`,
			poolID, op.ID); err != nil {
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

// DestroyPool destruye un pool BTRFS y libera sus devices.
// Asíncrona conceptualmente; ejecuta inline en Beta 8.
func (s *StorageService) DestroyPool(ctx context.Context, poolID string) (*Operation, error) {
	// HARD-3 fix (completar): lock global. Ver comentario en CreatePool.
	// Antes faltaba en DestroyPool del Service (sí lo tenía destroyPoolBtrfs
	// del path legacy, pero no esta función). Cierre auditoría 20/05/2026.
	storageMu.Lock()
	defer storageMu.Unlock()

	pool, err := s.GetPool(ctx, poolID)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeDestroyPool); err != nil {
		return nil, err
	}

	// Crear operation
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeDestroyPool,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"name":       pool.Name,
			"btrfs_uuid": pool.BtrfsUUID,
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return nil, err
	}

	// Recolectar by-id paths antes de borrar nada
	byIDPaths := make([]string, len(pool.Devices))
	for i, d := range pool.Devices {
		byIDPaths[i] = d.ByIDPath
	}

	// Ejecutar destroy físico
	err = s.btrfs.DestroyFilesystem(ctx, DestroyFilesystemRequest{
		MountPoint: pool.MountPoint,
		ByIDPaths:  byIDPaths,
		Force:      false,
	})
	if err != nil {
		s.markOperationFailed(ctx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
		return s.repo.GetOperation(ctx, op.ID)
	}

	// Borrar pool de DB (CASCADE limpia pool_devices, capabilities;
	// SET NULL preserva las operations en histórico)
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		if err := s.repo.DeletePool(ctx, tx, pool.ID); err != nil {
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

// markOperationFailed actualiza la operation a failed con el código dado.
//
// STOR-08: es best-effort POR DISEÑO, y es seguro por una garantía concreta:
// si esta actualización falla (p.ej. la DB está momentáneamente bloqueada),
// la operation queda en `in_progress`, y RecoverPendingOperations la recoge
// en el siguiente arranque y la resuelve (storage_recovery.go). Es decir, el
// peor caso de un fallo aquí es "la op se resuelve en el próximo boot en vez
// de ahora", nunca una inconsistencia permanente.
//
// Hacemos un reintento corto para cerrar la ventana en el caso común (lock
// transitorio), sin convertir esto en una operación que pueda bloquear.
func (s *StorageService) markOperationFailed(ctx context.Context, opID, errMsg, errCode string) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = s.runInTx(ctx, func(tx *sql.Tx) error {
			return s.repo.UpdateOperationStatus(ctx, tx, opID, OpStatusFailed, &errMsg, &errCode)
		})
		if err == nil {
			return
		}
		if attempt == 0 {
			time.Sleep(50 * time.Millisecond) // breve, por si es un lock transitorio
		}
	}
	// Tras el reintento sigue fallando: la op queda in_progress y el recovery
	// la resolverá al próximo arranque. No propagamos (garantía documentada arriba).
	logMsg("markOperationFailed: no se pudo marcar op %s tras reintento: %v "+
		"(quedará para RecoverPendingOperations en el próximo boot)", opID, err)
}
