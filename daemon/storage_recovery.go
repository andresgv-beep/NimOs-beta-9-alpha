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
	"path/filepath"
	"strings"
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
					switch op.Type {
					case OpTypeStartScrub:
						// AUDIT B6: scrub vivo tras restart → mismo watcher
						// que en el arranque normal del scrub.
						go watchScrubOperation(op.ID, pool.MountPoint, pool.Name)
					case OpTypeReplaceDevice:
						// AUDIT-R3: un replace re-adoptado necesita SU watcher
						// (consulta replace status y persiste el swap old→new).
						// El watcher de balance lo cerraría al instante sin
						// swap: completed mentiroso + membresía divorciada.
						go s.watchReadoptedReplace(op, pool.ID, pool.MountPoint)
					default:
						go s.watchReadoptedBalance(op.ID, pool.ID, pool.MountPoint)
					}
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
	case OpTypeStartScrub:
		// AUDIT B6: un scrub corre en el KERNEL y sobrevive al restart del
		// daemon. Consultar su estado real: vivo → re-adoptar (el caller
		// relanza el watcher); terminado → completed; ilegible → inconclusive.
		return s.resolveOrphanScrub(ctx, op)
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

// resolveOrphanScrub — caso start_scrub interrumpido por restart del daemon.
// El scrub del kernel sigue su curso sin nosotros; se decide por su estado real.
func (s *StorageService) resolveOrphanScrub(ctx context.Context, op *Operation) recoveryOutcome {
	if op.PoolID == nil {
		return inconclusiveOutcome("scrub op sin pool asociado")
	}
	pool, err := s.repo.GetPool(ctx, *op.PoolID)
	if err != nil || pool == nil {
		return inconclusiveOutcome("el pool del scrub ya no existe en la BD")
	}
	out, ok := runSafe("btrfs", "scrub", "status", pool.MountPoint)
	if !ok {
		return inconclusiveOutcome("btrfs scrub status ilegible en " + pool.MountPoint)
	}
	switch st := parseScrubStatusOutput(out); st["status"] {
	case "scrubbing":
		return recoveryOutcome{NewStatus: OpStatusInProgress, Readopted: true}
	case "done":
		return recoveryOutcome{NewStatus: OpStatusCompleted}
	case "canceled":
		return recoveryOutcome{NewStatus: OpStatusCancelled}
	default:
		return inconclusiveOutcome(fmt.Sprintf("estado de scrub %v tras restart", st["status"]))
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

// ─────────────────────────────────────────────────────────────────────────────
// AUDIT-R2 (2026-07-06) · Estado del replace del kernel
//
// Un `btrfs replace` NO aparece en `btrfs balance status`: tiene su propio
// `btrfs replace status`. Con solo el check de balance, TODA op de replace
// huérfana caía a inconclusive→failed: el lock se liberaba con el kernel aún
// copiando, un reintento chocaba con el críptico "already running" de btrfs,
// y el swap old→new en BD jamás se persistía (membresía divorciada de la
// realidad). Estas funciones dan al recovery la vista real del replace.
// ─────────────────────────────────────────────────────────────────────────────

// replaceKernelState resume la salida de `btrfs replace status -1`.
type replaceKernelState struct {
	Active   bool    // copia en curso
	Finished bool    // terminó con éxito ("finished on ...")
	// AUDIT-R5: tras un REBOOT completo de la máquina (no solo del daemon),
	// btrfs SUSPENDE el replace y solo lo reanuda al montar el pool en rw.
	// Como NimOS monta los pools degradados en `degraded,ro`, un replace
	// suspendido se quedaría suspendido para siempre y su status no dice
	// ni "% done" ni "finished" — sin este estado, la recovery lo daría
	// por muerto (inconclusive) con la copia a medias en el disco.
	Suspended bool
	Pct       float64 // % de progreso si Active
}

// readReplaceStateFn es inyectable para tests. Producción: readReplaceState.
var readReplaceStateFn = readReplaceState

func readReplaceState(mountPoint string) replaceKernelState {
	// `-1` obligatorio: sin él, replace status monitoriza en bucle y no
	// retorna (mismo bug que ya mordió en storage_health).
	out, ok := runSafe("btrfs", "replace", "status", "-1", mountPoint)
	if !ok {
		return replaceKernelState{}
	}
	pct, running := parseReplaceProgress(out)
	st := replaceKernelState{
		Active: running,
		// parseReplaceProgress devuelve (100,false) SOLO con "finished"/"completed"
		Finished: !running && pct == 100,
		Pct:      pct,
	}
	// Detección defensiva por substring: el formato exacto de la línea de
	// suspensión varía entre versiones de btrfs-progs, pero la palabra
	// "suspended" es constante.
	if !st.Active && !st.Finished && strings.Contains(strings.ToLower(out), "suspended") {
		st.Suspended = true
	}
	return st
}

// missingDevidCheckFn es inyectable para tests. Producción: missingDevidForPool
// (storage_executor_real.go) — devuelve "" si el pool no tiene disco missing.
var missingDevidCheckFn = missingDevidForPool

// kernelPoolHasDeviceFn verifica que un device es MIEMBRO REAL del filesystem
// según el kernel (btrfs filesystem show). Inyectable para tests.
//
// AUDIT-R6 (endurecimiento del swap): el check de "sin disco missing" no
// basta en un caso estrecho — daemon crasheado DESPUÉS de crear la op pero
// ANTES de que `replace start` ejecutara, sobre un pool que ya tuvo un
// replace terminado antes (status residual "finished") y cuyo disco a
// reemplazar sigue vivo (reemplazo proactivo: no hay missing). Sin esta
// verificación, el watcher persistiría un swap que el kernel jamás hizo.
// Regla 16: la membresía en BD solo cambia si el kernel confirma que el
// disco NUEVO está dentro.
var kernelPoolHasDeviceFn = kernelPoolHasDevice

func kernelPoolHasDevice(mountPoint string, dev *Device) bool {
	if dev == nil {
		return false
	}
	out, ok := runSafe("btrfs", "filesystem", "show", mountPoint)
	if !ok {
		return false // sin lectura del kernel no hay certeza → no confirmar
	}
	// `filesystem show` imprime paths /dev/sdX; el by-id es un symlink que
	// puede no aparecer literal. Comprobar ambos y, como refuerzo, resolver
	// el symlink del by-id a su target real.
	if dev.CurrentPath != "" && strings.Contains(out, dev.CurrentPath) {
		return true
	}
	if dev.ByIDPath != "" {
		if strings.Contains(out, dev.ByIDPath) {
			return true
		}
		if target, err := filepath.EvalSymlinks(dev.ByIDPath); err == nil && target != "" &&
			strings.Contains(out, target) {
			return true
		}
	}
	return false
}

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

	// AUDIT-R2: un replace del kernel vive en `replace status`, no en
	// `balance status`. Consultar SU estado antes de dar la op por muerta.
	// Si sigue copiando (Active), quedó suspendido por un reboot completo
	// (Suspended, AUDIT-R5) o ya terminó (Finished — el swap en BD quedó
	// pendiente porque el goroutine murió con el daemon), re-adoptar:
	// watchReadoptedReplace reanuda/espera y cierra la op con certeza.
	if op.Type == OpTypeReplaceDevice {
		rst := readReplaceStateFn(pool.MountPoint)
		if rst.Active || rst.Finished || rst.Suspended {
			state := "TERMINADO"
			if rst.Active {
				state = "ACTIVO"
			} else if rst.Suspended {
				state = "SUSPENDIDO (reboot a media copia)"
			}
			logMsg("Recovery: op %s (replace) en pool %s tiene replace del kernel %s (%.1f%%) → re-adoptando",
				op.ID, pool.Name, state, rst.Pct)
			return recoveryOutcome{NewStatus: OpStatusInProgress, Readopted: true}
		}
		// Sin replace vivo, suspendido ni terminado ("Never started" o
		// cancelado): cae al camino inconclusive de abajo (seguro).
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

// watchReadoptedReplace espera a que el `btrfs replace` del kernel de una op
// re-adoptada termine y entonces PERSISTE EL SWAP old→new en BD y cierra la op.
//
// AUDIT-R3: watchReadoptedBalance solo cierra la op y reconcilia el profile —
// nunca la MEMBRESÍA. Para un replace re-adoptado eso dejaba el disco viejo
// asignado y el nuevo huérfano en BD para siempre (el reconciler de membresía
// solo AÑADE miembros observados, no desasigna). Este watcher replica el final
// del goroutine original de ReplaceDevice: swap atómico + completed.
//
// Regla 16 antes de tocar la BD: el swap solo se persiste si el kernel dice
// "finished" Y el pool ya no reporta ningún disco missing. Ante cualquier otra
// cosa → failed/inconclusive sin tocar membresía.
// replaceWatchPollInterval es el intervalo de sondeo del watcher de replace.
// Var (no const) para que los tests puedan acelerarlo sin dormir 10s reales.
var replaceWatchPollInterval = 10 * time.Second

func (s *StorageService) watchReadoptedReplace(op *Operation, poolID, mountPoint string) {
	bgCtx := context.Background()
	pollInterval := replaceWatchPollInterval
	const maxWait = 48 * time.Hour // un replace de TB puede superar las 24h del balance

	var data struct {
		OldDeviceID string `json:"old_device_id"`
		NewDeviceID string `json:"new_device_id"`
	}
	if err := json.Unmarshal(op.Data, &data); err != nil ||
		data.OldDeviceID == "" || data.NewDeviceID == "" {
		s.markOperationFailed(bgCtx, op.ID,
			"replace re-adoptado sin old/new device en Data; la membresía queda sin tocar (revisión manual)",
			ErrCodeRecoveryInconclusive)
		notifError("Reemplazo de disco: recuperación incompleta",
			"El reemplazo interrumpido no guarda qué discos intervenían; revisa el pool en Almacenamiento.")
		return
	}

	notifWarning("Reinicio durante un reemplazo de disco",
		"NimOS se reinició con una reconstrucción en marcha; se ha re-adoptado y continúa. Verás el progreso en Almacenamiento.")

	deadline := time.Now().Add(maxWait)
	for {
		st := readReplaceStateFn(mountPoint)

		// AUDIT-R5: reboot completo → el kernel SUSPENDE el replace y solo lo
		// reanuda al montar rw. NimOS monta los pools degradados en ro, así
		// que sin este empujón la copia quedaría congelada para siempre.
		if st.Suspended {
			if poolMountIsReadOnly(mountPoint) {
				logMsg("watchReadoptedReplace: replace SUSPENDIDO y pool %s en ro; remontando degraded,rw para reanudar", mountPoint)
				if err := remountPoolReadWriteDegraded(mountPoint); err != nil {
					s.markOperationFailed(bgCtx, op.ID,
						fmt.Sprintf("replace suspendido tras reboot y no se pudo remontar rw para reanudarlo: %v", err),
						ErrCodeBtrfsCommandFailed)
					notifError("Reemplazo de disco suspendido",
						"Tras el reinicio, la reconstrucción quedó suspendida y no se pudo reanudar automáticamente. Revisa el pool en Almacenamiento.")
					return
				}
				notifInfo("Reconstrucción reanudada",
					"El reemplazo de disco suspendido por el reinicio se ha reanudado automáticamente.")
			}
			// darle tiempo al kernel a reanudar y volver a mirar
			time.Sleep(pollInterval)
			if time.Now().After(deadline) {
				s.markOperationFailed(bgCtx, op.ID,
					"replace re-adoptado excedió el tiempo máximo de espera (48h)",
					ErrCodeRecoveryInconclusive)
				return
			}
			continue
		}

		if !st.Active {
			if !st.Finished {
				// Cancelado o estado ilegible: sin certeza no hay completed
				// ni swap (Regla 16). El pool sigue como esté; el usuario
				// puede relanzar el replace.
				s.markOperationFailed(bgCtx, op.ID,
					"el replace del kernel terminó sin estado 'finished' (¿cancelado?); membresía sin tocar",
					ErrCodeRecoveryInconclusive)
				notifError("Reemplazo de disco interrumpido",
					"La reconstrucción no llegó a terminar (posiblemente cancelada). El pool no ha cambiado; puedes relanzar el reemplazo desde Almacenamiento.")
				return
			}
			// Reality check 1: tras un replace finished no debe quedar disco
			// missing en el pool. Si lo hay, el 'finished' es de otro replace
			// anterior y NO corresponde a esta op → no tocar membresía.
			if devid := missingDevidCheckFn(mountPoint); devid != "" {
				s.markOperationFailed(bgCtx, op.ID,
					fmt.Sprintf("replace 'finished' pero el pool aún reporta un disco missing (devid %s); membresía sin tocar", devid),
					ErrCodeRecoveryInconclusive)
				notifError("Reemplazo de disco: estado incoherente",
					"El kernel reporta un reemplazo terminado pero al pool aún le falta un disco. NimOS no ha tocado nada; revisa Almacenamiento.")
				return
			}
			// Reality check 2 (AUDIT-R6): el disco NUEVO debe ser miembro real
			// del filesystem según el kernel. Cubre el caso estrecho de un
			// 'finished' residual de un replace ANTERIOR con esta op creada
			// pero jamás ejecutada (reemplazo proactivo sin disco missing).
			newDev, derr := s.repo.GetDevice(bgCtx, data.NewDeviceID)
			if derr != nil || newDev == nil || !kernelPoolHasDeviceFn(mountPoint, newDev) {
				s.markOperationFailed(bgCtx, op.ID,
					"replace 'finished' pero el kernel no confirma el disco nuevo como miembro del pool; membresía sin tocar",
					ErrCodeRecoveryInconclusive)
				notifError("Reemplazo de disco: sin confirmación del kernel",
					"No se pudo confirmar que el disco nuevo forma parte del pool. NimOS no ha cambiado nada; revisa Almacenamiento.")
				return
			}
			// Swap atómico old→new + completed (mismo cierre que el goroutine
			// original de ReplaceDevice).
			err := s.runInTx(bgCtx, func(tx *sql.Tx) error {
				if err := s.repo.UnassignDeviceFromPool(bgCtx, tx, poolID, data.OldDeviceID); err != nil {
					return err
				}
				if err := s.repo.AssignDeviceToPool(bgCtx, tx, poolID, data.NewDeviceID); err != nil {
					return err
				}
				return s.repo.UpdateOperationStatus(bgCtx, tx, op.ID, OpStatusCompleted, nil, nil)
			})
			if err != nil {
				s.markOperationFailed(bgCtx, op.ID,
					fmt.Sprintf("watcher de replace no pudo persistir el swap: %v", err),
					ErrCodeInternal)
				return
			}
			logMsg("Recovery: replace re-adoptado del pool %s terminó → swap %s→%s persistido, op %s completed",
				poolID, data.OldDeviceID, data.NewDeviceID, op.ID)
			notifSuccess("Reemplazo de disco completado",
				"La reconstrucción que sobrevivió al reinicio ha terminado y el pool vuelve a tener redundancia completa.")
			// Scrub de verificación, igual que en el flujo normal (best-effort).
			if serr := startScrubOnPool(mountPoint, poolID); serr != nil {
				logMsg("watchReadoptedReplace: no se pudo lanzar el scrub post-replace en %s: %v", mountPoint, serr)
			}
			return
		}

		if time.Now().After(deadline) {
			s.markOperationFailed(bgCtx, op.ID,
				"replace re-adoptado excedió el tiempo máximo de espera (48h)",
				ErrCodeRecoveryInconclusive)
			logMsg("Recovery: watcher de replace del pool %s excedió 48h → op %s failed", poolID, op.ID)
			notifError("Reemplazo de disco atascado",
				"La reconstrucción lleva más de 48h sin terminar. Revisa la salud de los discos en Almacenamiento.")
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
