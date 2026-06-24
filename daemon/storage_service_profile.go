package main

import (
	"context"
	"database/sql"
	"fmt"
)

// ConvertProfile cambia el perfil de un pool (ej: single → raid1, raid1 → raid10).
// Operación pesada (mueve datos). Validaciones:
//   - Pool managed, capability convert_profile
//   - El profile destino es válido y compatible con número de discos actual
//   - El profile destino es DIFERENTE al actual
func (s *StorageService) ConvertProfile(ctx context.Context, req ConvertProfileRequest) (*Operation, error) {
	// HARD-3 fix: lock global. Ver comentario en CreatePool.
	storageMu.Lock()
	defer storageMu.Unlock()

	pool, err := s.GetPool(ctx, req.PoolID)
	if err != nil {
		return nil, err
	}

	if err := s.checkPolicy(pool, OpTypeConvertProfile); err != nil {
		return nil, err
	}

	if !req.NewProfile.IsValid() {
		return nil, errFromCode(ErrCodeProfileInvalid,
			fmt.Sprintf("invalid profile %q", req.NewProfile))
	}
	if req.NewProfile == pool.Profile {
		return nil, errFromCode(ErrCodeBadRequest,
			fmt.Sprintf("pool is already in profile %q", req.NewProfile))
	}
	if len(pool.Devices) < req.NewProfile.MinDisks() {
		return nil, errFromCode(ErrCodeInsufficientDisks,
			fmt.Sprintf("profile %s requires at least %d disks, pool has %d",
				req.NewProfile, req.NewProfile.MinDisks(), len(pool.Devices)))
	}

	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeConvertProfile,
		PoolID: &pool.ID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"from_profile": string(pool.Profile),
			"to_profile":   string(req.NewProfile),
		}),
	}
	err = s.runInTx(ctx, func(tx *sql.Tx) error {
		return s.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return nil, errFromCode(ErrCodeOperationInProgress,
			fmt.Sprintf("another layout operation is in progress on pool %s", pool.ID))
	}

	// ─── ASYNC: el balance corre en background ─────────────────────────────
	// Un balance de conversión puede tardar de minutos a HORAS según el
	// tamaño del array. Si lo ejecutáramos inline con r.Context():
	//   1. el HTTP del navegador se quedaría colgado hasta el final, y
	//   2. si el navegador corta (timeout/cerrar pestaña), r.Context() se
	//      cancela y MATARÍA el balance a medias → drift de layout.
	// Por eso: goroutine con context.Background() (vive lo que viva el
	// daemon) y devolvemos la Operation in_progress ya. El frontend hace
	// polling de la operation y de /balance-status para el progreso.
	//
	// Exclusión: la Operation in_progress en BD (índice único
	// idx_one_layout_op_per_pool) bloquea cualquier otra op de layout sobre
	// este pool hasta que termine. Si el daemon muere a media conversión,
	// el recovery (inconclusive) + STOR-01 (drift de layout) lo detectan.
	poolID, mountPoint, newProfile := pool.ID, pool.MountPoint, req.NewProfile
	go func() {
		bgCtx := context.Background()
		if err := s.btrfs.ConvertProfile(bgCtx, mountPoint, newProfile); err != nil {
			s.markOperationFailed(bgCtx, op.ID, err.Error(), ErrCodeBtrfsCommandFailed)
			logMsg("ConvertProfile async: balance falló en pool %s: %v", poolID, err)
			return
		}
		// Persistir el nuevo profile + cerrar la operation, atómico.
		err := s.runInTx(bgCtx, func(tx *sql.Tx) error {
			if e := s.repo.SetPoolProfile(bgCtx, tx, poolID, newProfile); e != nil {
				return e
			}
			return s.repo.UpdateOperationStatus(bgCtx, tx, op.ID, OpStatusCompleted, nil, nil)
		})
		if err != nil {
			s.markOperationFailed(bgCtx, op.ID, err.Error(), ErrCodeInternal)
			return
		}
		logMsg("ConvertProfile async: pool %s convertido a %s", poolID, newProfile)
	}()

	// Devolver la operation in_progress inmediatamente.
	return s.repo.GetOperation(ctx, op.ID)
}
