// storage_recovery.go — Recuperación de operations huérfanas tras reinicio.
//
// Si el daemon muere (crash, kill -9, reboot inesperado) durante una
// operación de storage, esa operation queda en estado in_progress o
// pending en la DB, pero el efecto físico puede haberse aplicado,
// haberse aplicado a medias, o no haberse aplicado en absoluto.
//
// RecoverPendingOperations() se ejecuta al arranque, examina cada
// operation huérfana, consulta el estado real de BTRFS, y decide:
//
//   - "Sin certeza" → marca failed con code recovery_inconclusive.
//     El usuario debe verificar manualmente.
//   - "BTRFS confirma que NO se aplicó" → marca failed con code
//     recovery_rolled_back. Seguro reintentarlo.
//   - "BTRFS confirma que SÍ se aplicó completamente" → marca completed.
//     Solo para casos sin ambigüedad.
//
// Filosofía: ante duda, recovery_inconclusive. NUNCA marcar completed
// sin evidencia clara. Un falso positivo aquí lleva al usuario a creer
// que su pool existe cuando no.
//
// see docs/storage_state_machines.md §4.4 (recovery)
// see docs/nimos_beta8_storage_plan.md fase 4

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// API pública
// ─────────────────────────────────────────────────────────────────────────────

// RecoveryResult resume lo que hizo RecoverPendingOperations.
type RecoveryResult struct {
	Inspected    int // operations huérfanas encontradas
	Completed    int // marcadas como completed (evidencia clara)
	RolledBack   int // marcadas como failed con code rolled_back
	Inconclusive int // marcadas como failed con code inconclusive
	Readopted    int // P3: re-adoptadas (balance vivo), siguen in_progress con watcher
}

// RecoverPendingOperations examina operations huérfanas (in_progress o
// pending) y decide su destino consultando BTRFS.
//
// Idempotente: si se ejecuta dos veces, la segunda no encontrará operations
// huérfanas (la primera ya las habrá resuelto).
//
// Llamar UNA vez al arranque del daemon, después de initStorageModule.
//
// see docs/storage_state_machines.md §4.4
func (s *StorageService) RecoverPendingOperations(ctx context.Context) (*RecoveryResult, error) {
	orphans, err := s.repo.ListPendingOperations(ctx)
	if err != nil {
		return nil, fmt.Errorf("RecoverPendingOperations: list orphans: %w", err)
	}

	result := &RecoveryResult{Inspected: len(orphans)}
	if len(orphans) == 0 {
		return result, nil
	}

	logMsg("Recovery: found %d orphan operations from previous run", len(orphans))

	for _, op := range orphans {
		outcome := s.resolveOrphanOperation(ctx, op)

		// Persistir el desenlace en una tx propia (cada op es independiente:
		// que falle el resolve de una NO debe abortar las demás).
		err := s.runInTx(ctx, func(tx *sql.Tx) error {
			return s.repo.UpdateOperationStatus(ctx, tx, op.ID,
				outcome.NewStatus, outcome.ErrorMsg, outcome.ErrorCode)
		})
		if err != nil {
			logMsg("Recovery: failed to update op %s: %v (will retry next boot)", op.ID, err)
			continue
		}

		logMsg("Recovery: op %s (%s) → %s (%s)",
			op.ID, op.Type, outcome.NewStatus, stringOrEmpty(outcome.ErrorCode))

		// P3: op re-adoptada (balance vivo). Se mantiene in_progress (el lock
		// se conserva); lanzamos el watcher que la cerrará al terminar el
		// balance. El UpdateOperationStatus de arriba ya la dejó in_progress.
		if outcome.Readopted {
			result.Readopted++
			if op.PoolID != nil {
				if pool, gErr := s.repo.GetPool(ctx, *op.PoolID); gErr == nil && pool != nil {
					go s.watchReadoptedBalance(op.ID, pool.ID, pool.MountPoint)
				}
			}
			continue
		}

		switch outcome.NewStatus {
		case OpStatusCompleted:
			result.Completed++
		case OpStatusFailed:
			if outcome.ErrorCode != nil && *outcome.ErrorCode == ErrCodeRecoveryRolledBack {
				result.RolledBack++
			} else {
				result.Inconclusive++
			}
		}
	}

	logMsg("Recovery complete: inspected=%d completed=%d rolled_back=%d inconclusive=%d readopted=%d",
		result.Inspected, result.Completed, result.RolledBack, result.Inconclusive, result.Readopted)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internals
// ─────────────────────────────────────────────────────────────────────────────

// recoveryOutcome es el desenlace que resolveOrphanOperation decide para
// cada operation huérfana.
type recoveryOutcome struct {
	NewStatus OperationStatus
	ErrorMsg  *string
	ErrorCode *string
	// Readopted (P3): la op se re-adopta porque su balance BTRFS sigue VIVO en
	// el kernel tras el restart del daemon. La op se mantiene in_progress (el
	// lock se conserva) y el caller lanza un watcher que la cierra cuando el
	// balance termine. NewStatus en este caso es OpStatusInProgress.
	Readopted bool
}

// resolveOrphanOperation decide el desenlace de una operation huérfana
// consultando el estado real de BTRFS. Por seguridad, NUNCA devuelve
// completed sin certeza alta. Ante duda → failed/inconclusive.
//
// Esta función es PURA respecto a la DB: no escribe. La persistencia
// la hace el caller en su propia tx.
func (s *StorageService) resolveOrphanOperation(ctx context.Context, op *Operation) recoveryOutcome {
	switch op.Type {
	case OpTypeCreatePool:
		return s.resolveOrphanCreatePool(ctx, op)
	case OpTypeDestroyPool:
		return s.resolveOrphanDestroyPool(ctx, op)
	case OpTypeImportPool:
		return s.resolveOrphanImportPool(ctx, op)
	case OpTypeAddDevice, OpTypeRemoveDevice, OpTypeReplaceDevice, OpTypeConvertProfile:
		// Estas ops mutan un pool existente vía un balance BTRFS que corre
		// en el kernel. Un balance SOBREVIVE al restart del daemon (la
		// goroutine de NimOS muere, pero el kernel sigue balanceando).
		//
		// P3: antes de matar la op (inconclusive→failed, que liberaría el
		// lock y permitiría otra op de layout sobre un pool aún balanceando),
		// consultamos si el balance sigue VIVO. Si lo está, re-adoptamos la
		// op (se mantiene in_progress, el lock se conserva) y el caller lanza
		// un watcher que la cierra al terminar el balance. Si NO hay balance
		// activo, el camino actual (inconclusive) es correcto.
		return s.resolveOrphanLayoutOp(ctx, op)
	default:
		// Ops que pillamos por accidente (rename, set_compression, etc).
		// Son síncronas y nunca deberían quedar huérfanas en realidad,
		// pero si ocurre, inconclusive.
		return inconclusiveOutcome(fmt.Sprintf(
			"sync operation %s found orphan (should not happen)", op.Type))
	}
}

// resolveOrphanCreatePool — caso create_pool interrumpido.
//
// La operation guarda el "name" en Data. Buscamos si BTRFS tiene
// un filesystem con ese label, vía btrfs filesystem show.
//
// Importante: no sabemos el btrfs_uuid en el momento de la op (se
// genera en mkfs). Por eso usamos el LABEL para detectar parcial.
//
// Resoluciones:
//   - BTRFS no ve ningún FS con ese label → rolled_back (mkfs no ejecutó)
//   - BTRFS sí ve un FS con ese label pero el pool no existe en DB →
//     inconclusive (mkfs ejecutó pero post-mkfs no completó; manual cleanup)
func (s *StorageService) resolveOrphanCreatePool(ctx context.Context, op *Operation) recoveryOutcome {
	var data struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(op.Data, &data); err != nil {
		return inconclusiveOutcome(fmt.Sprintf("create_pool data unmarshal: %v", err))
	}
	if data.Name == "" {
		return inconclusiveOutcome("create_pool: empty name in operation data")
	}

	// Buscamos por label. UUID no lo tenemos porque mkfs no había acabado
	// (o acabó pero no persistimos). FilesystemExistsByUUID también acepta
	// label en btrfs filesystem show.
	exists, err := s.btrfs.FilesystemExistsByUUID(ctx, data.Name)
	if err != nil {
		return inconclusiveOutcome(fmt.Sprintf(
			"cannot determine if filesystem '%s' exists: %v", data.Name, err))
	}
	if !exists {
		// Limpio. mkfs no ejecutó o falló. Seguro reintentar.
		return rolledBackOutcome(fmt.Sprintf(
			"create_pool '%s' rolled back: no BTRFS filesystem found with that label",
			data.Name))
	}

	// BTRFS sí ve un filesystem pero la DB no tiene el pool. Esto significa
	// que mkfs ejecutó pero algo posterior (mount, persist) falló. Estado
	// sucio que requiere intervención humana — quizás haya que wipefs ese
	// FS, quizás reimportarlo. No tocamos automáticamente.
	return inconclusiveOutcome(fmt.Sprintf(
		"create_pool '%s' left a BTRFS filesystem on disk but pool not persisted. "+
			"Manual cleanup may be needed (wipefs of devices, or import as observed pool).",
		data.Name))
}

// resolveOrphanDestroyPool — caso destroy_pool interrumpido.
//
// La operation guarda el "btrfs_uuid" en Data (lo recogemos antes de
// destruir). Comprobamos si el FS sigue existiendo en disco.
//
// Resoluciones:
//   - BTRFS no ve ese UUID → completed (filesystem destruido limpio)
//   - BTRFS sí ve ese UUID → inconclusive (destroy a medias, manual)
func (s *StorageService) resolveOrphanDestroyPool(ctx context.Context, op *Operation) recoveryOutcome {
	var data struct {
		Name      string `json:"name"`
		BtrfsUUID string `json:"btrfs_uuid"`
	}
	if err := json.Unmarshal(op.Data, &data); err != nil {
		return inconclusiveOutcome(fmt.Sprintf("destroy_pool data unmarshal: %v", err))
	}
	if data.BtrfsUUID == "" {
		return inconclusiveOutcome("destroy_pool: empty btrfs_uuid in operation data")
	}

	exists, err := s.btrfs.FilesystemExistsByUUID(ctx, data.BtrfsUUID)
	if err != nil {
		return inconclusiveOutcome(fmt.Sprintf(
			"cannot determine if filesystem '%s' exists: %v", data.BtrfsUUID, err))
	}
	if !exists {
		// El destroy físico se completó. Si el pool sigue en DB es
		// porque el delete no se ejecutó; eso lo manejamos abajo.
		if op.PoolID != nil {
			err := s.runInTx(ctx, func(tx *sql.Tx) error {
				return s.repo.DeletePool(ctx, tx, *op.PoolID)
			})
			if err != nil {
				return inconclusiveOutcome(fmt.Sprintf(
					"destroy completed in BTRFS but cannot remove pool %s from DB: %v",
					*op.PoolID, err))
			}
		}
		return completedOutcome()
	}

	return inconclusiveOutcome(fmt.Sprintf(
		"destroy_pool: BTRFS filesystem '%s' still exists on disk. "+
			"Manual cleanup required.", data.BtrfsUUID))
}

// resolveOrphanImportPool — caso import_pool interrumpido.
//
// Import adopta un filesystem BTRFS ya existente en disco como pool managed:
// NO toca el filesystem (no mkfs, no wipe), solo lo registra en la DB y lo
// monta. Por eso la recuperación es segura y determinista:
//
//   - El FS de origen NUNCA se daña (import no es destructivo).
//   - Si el pool quedó persistido en la DB → import completó → completed.
//   - Si el pool NO está en la DB → el registro no se completó. Como el FS
//     sigue intacto en disco, es seguro marcar rolled_back: el usuario puede
//     reintentar el import sin riesgo (aparecerá de nuevo como observado).
func (s *StorageService) resolveOrphanImportPool(ctx context.Context, op *Operation) recoveryOutcome {
	var data struct {
		Name      string `json:"name"`
		BtrfsUUID string `json:"btrfs_uuid"`
		UUID      string `json:"uuid"`
	}
	if err := json.Unmarshal(op.Data, &data); err != nil {
		return inconclusiveOutcome(fmt.Sprintf("import_pool data unmarshal: %v", err))
	}
	uuid := data.BtrfsUUID
	if uuid == "" {
		uuid = data.UUID
	}
	if uuid == "" {
		return inconclusiveOutcome("import_pool: empty uuid in operation data")
	}

	// ¿El pool quedó registrado en la DB?
	pool, err := s.repo.GetPoolByBtrfsUUID(ctx, uuid)
	if err == nil && pool != nil {
		// Import completó: el pool existe en la DB. Completed.
		return completedOutcome()
	}

	// El pool no está en la DB. Como import NO daña el FS de origen, es seguro
	// marcar rolled_back: el FS sigue intacto en disco y reaparecerá como
	// observado para reintentarlo. Sin riesgo de pérdida de datos.
	return rolledBackOutcome(fmt.Sprintf(
		"import_pool '%s' (uuid %s) rolled back: no quedó registrado en la BD. "+
			"El filesystem sigue intacto en disco; reaparecerá como observado para reimportar.",
		data.Name, uuid))
}

// ─────────────────────────────────────────────────────────────────────────────
// P3 · Recovery de ops de layout con balance vivo
// ─────────────────────────────────────────────────────────────────────────────

// readBalanceStatusFn es inyectable para tests (sin btrfs real). En producción
// apunta a readBalanceStatus.
var readBalanceStatusFn = readBalanceStatus

// resolveOrphanLayoutOp decide el desenlace de una op de layout (add/remove/
// replace device, convert profile) interrumpida por un restart del daemon.
//
// El balance BTRFS que ejecuta estas ops vive en el KERNEL, no en el daemon, así
// que sobrevive al restart. Si sigue activo, re-adoptamos la op (in_progress, el
// lock se mantiene) y lanzamos un watcher que la cierra al terminar. Si no hay
// balance activo, no podemos saber si terminó limpio → inconclusive (camino
// actual, seguro).
func (s *StorageService) resolveOrphanLayoutOp(ctx context.Context, op *Operation) recoveryOutcome {
	if op.PoolID == nil {
		return inconclusiveOutcome(fmt.Sprintf(
			"layout op %s sin pool_id, no se puede consultar balance", op.Type))
	}

	pool, err := s.repo.GetPool(ctx, *op.PoolID)
	if err != nil || pool == nil || pool.MountPoint == "" {
		// Sin pool/mountpoint no podemos consultar el balance → inconclusive.
		return inconclusiveOutcome(fmt.Sprintf(
			"layout op %s on pool %v interrupted by daemon restart (pool no resoluble)",
			op.Type, derefStr(op.PoolID)))
	}

	st := readBalanceStatusFn(pool.MountPoint)
	if !st.Active {
		// No hay balance vivo. O terminó (y no lo cerramos), o nunca arrancó.
		// No podemos distinguir con certeza → inconclusive (seguro). El
		// self-heal del profile (Regla 16) corrige el estado en BD aparte.
		return inconclusiveOutcome(fmt.Sprintf(
			"layout op %s on pool %s interrupted by daemon restart (sin balance activo)",
			op.Type, pool.Name))
	}

	// Balance VIVO: re-adoptar. Mantener in_progress conserva el lock (índice
	// único parcial) y bloquea otra op de layout sobre este pool hasta que el
	// balance termine y el watcher cierre esta op.
	logMsg("Recovery: op %s (%s) en pool %s tiene balance ACTIVO (%.0f%%) → re-adoptando",
		op.ID, op.Type, pool.Name, st.PercentDone)
	return recoveryOutcome{NewStatus: OpStatusInProgress, Readopted: true}
}

// watchReadoptedBalance espera a que el balance BTRFS de un pool re-adoptado
// termine y entonces cierra la op (completed) y reconcilia el profile real.
// Corre en su propia goroutine con context.Background() (vive lo que viva el
// daemon), replicando el patrón del convert_profile async.
func (s *StorageService) watchReadoptedBalance(opID, poolID, mountPoint string) {
	bgCtx := context.Background()
	const pollInterval = 10 * time.Second
	const maxWait = 24 * time.Hour // tope defensivo: un balance no dura días

	deadline := time.Now().Add(maxWait)
	for {
		st := readBalanceStatusFn(mountPoint)
		if !st.Active {
			// Balance terminado. Cerrar la op y reconciliar el profile real.
			// Reusa el self-heal de Regla 16: leer el profile real de BTRFS y
			// persistirlo, luego marcar la op completed.
			if pool, err := s.repo.GetPool(bgCtx, poolID); err == nil && pool != nil {
				reconcilePoolProfileWithReality(pool)
			}
			err := s.runInTx(bgCtx, func(tx *sql.Tx) error {
				return s.repo.UpdateOperationStatus(bgCtx, tx, opID, OpStatusCompleted, nil, nil)
			})
			if err != nil {
				s.markOperationFailed(bgCtx, opID,
					fmt.Sprintf("watcher no pudo cerrar op re-adoptada: %v", err),
					ErrCodeInternal)
				return
			}
			logMsg("Recovery: balance re-adoptado del pool %s terminó → op %s completed", poolID, opID)
			return
		}

		if time.Now().After(deadline) {
			// El balance lleva demasiado: algo va mal. Marcar failed para
			// liberar el lock; el siguiente boot/reconcile lo reevaluará.
			s.markOperationFailed(bgCtx, opID,
				"balance re-adoptado excedió el tiempo máximo de espera (24h)",
				ErrCodeRecoveryInconclusive)
			logMsg("Recovery: watcher de balance del pool %s excedió 24h → op %s failed", poolID, opID)
			return
		}

		time.Sleep(pollInterval)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Constructores de outcomes
// ─────────────────────────────────────────────────────────────────────────────

func completedOutcome() recoveryOutcome {
	return recoveryOutcome{NewStatus: OpStatusCompleted}
}

func rolledBackOutcome(msg string) recoveryOutcome {
	code := ErrCodeRecoveryRolledBack
	return recoveryOutcome{
		NewStatus: OpStatusFailed,
		ErrorMsg:  &msg,
		ErrorCode: &code,
	}
}

func inconclusiveOutcome(msg string) recoveryOutcome {
	code := ErrCodeRecoveryInconclusive
	return recoveryOutcome{
		NewStatus: OpStatusFailed,
		ErrorMsg:  &msg,
		ErrorCode: &code,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
