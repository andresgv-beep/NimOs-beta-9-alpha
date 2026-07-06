// storage_audit_replace_test.go — Auditoría del flujo de REPLACE (2026-07-06).
//
// Origen: incidente real reemplazando un disco dañado ("la odisea"). Tres
// mentiras confirmadas en la auditoría, cada una con su test de regresión:
//
//	AUDIT-R1 · El guard de layout rechazaba read_only también para REPLACE.
//	           NimOS monta los pools degradados en `degraded,ro`, así que el
//	           flujo de reparación se auto-bloqueaba: el remount degraded,rw
//	           del goroutine era código muerto. El test previo no lo veía
//	           porque setupTestService neutraliza defaultPoolWritableChecks
//	           mientras inyecta poolMountIsReadOnly por separado.
//
//	AUDIT-R2 · resolveOrphanLayoutOp consultaba SOLO `balance status` para
//	           decidir si re-adoptar. Un `btrfs replace` del kernel no
//	           aparece ahí (tiene su propio `replace status`): todo replace
//	           huérfano tras un restart del daemon caía a failed con el
//	           kernel aún copiando → lock liberado, reintentos con "already
//	           running", swap old→new jamás persistido.
//
//	AUDIT-R3 · Incluso re-adoptado, watchReadoptedBalance cerraba la op sin
//	           tocar la MEMBRESÍA. watchReadoptedReplace debe persistir el
//	           swap old→new al terminar el replace, y solo con certeza
//	           (finished + sin disco missing).
package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R1 · ReplaceDevice debe PASAR el guard con el pool en read-only
// (degraded,ro es el estado normal de un pool a reparar). AddDevice, en
// cambio, debe seguir cortado: la excepción es SOLO para replace.
// ─────────────────────────────────────────────────────────────────────────

func TestReplaceDeviceAllowedOnReadOnlyDegradedPool(t *testing.T) {
	service, mock, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()
	mock.Reset()

	// Simular PRODUCCIÓN: el pool degradado está montado ro y el guard lo ve.
	// (setupTestService deja readOnly=false; aquí lo forzamos como en la
	// realidad — este es exactamente el agujero que el test antiguo tapaba.)
	origChecks := defaultPoolWritableChecks
	defaultPoolWritableChecks = poolWritableChecks{
		mountedPool: func(string) bool { return true },
		readOnly:    func(string) bool { return true },
	}
	origRO, origRW, origToRO := poolMountIsReadOnly, remountPoolReadWriteDegraded, remountPoolReadOnlyDegraded
	defer func() {
		defaultPoolWritableChecks = origChecks
		poolMountIsReadOnly = origRO
		remountPoolReadWriteDegraded = origRW
		remountPoolReadOnlyDegraded = origToRO
	}()
	poolMountIsReadOnly = func(string) bool { return true }
	rwCalled := false
	remountPoolReadWriteDegraded = func(string) error { rwCalled = true; return nil }
	remountPoolReadOnlyDegraded = func(string) error { return nil }

	op, err := service.ReplaceDevice(ctx, ReplaceDeviceRequest{
		PoolID:      poolID,
		OldDeviceID: deviceIDs[0],
		NewDeviceID: "new-1",
	})
	if err != nil {
		t.Fatalf("AUDIT-R1: el replace sobre un pool degraded,ro debe estar PERMITIDO (es el caso de reparación); got %v", err)
	}
	final := waitForOperation(t, service, ctx, op.ID, 3*time.Second)
	if final.Status != OpStatusCompleted {
		t.Fatalf("el replace debería completar; got %+v", final)
	}
	if !rwCalled {
		t.Error("el goroutine debió remontar degraded,rw para reparar")
	}

	// La excepción NO se extiende a las demás ops de layout: AddDevice sobre
	// el mismo pool ro debe seguir rechazado con read_only.
	tx2, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx2, &Device{
		ID: "extra-ro", Serial: "EXTRA-RO",
		ByIDPath: "/dev/disk/by-id/extra-ro", CurrentPath: "/dev/sdx",
		SizeBytes: 1e12,
	})
	tx2.Commit()
	if _, err := service.AddDevice(ctx, AddDeviceRequest{PoolID: poolID, DeviceID: "extra-ro"}); err == nil {
		t.Error("AddDevice sobre pool read-only debe seguir cortado por el guard")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R2 · Una op de replace huérfana con el replace del kernel VIVO debe
// re-adoptarse aunque `balance status` no muestre nada (que es lo normal:
// replace ≠ balance).
// ─────────────────────────────────────────────────────────────────────────

func TestRecoveryReplaceOrphanReadoptedViaReplaceStatus(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// Op de replace huérfana (in_progress, como la deja un crash del daemon).
	op := &Operation{
		ID:     newUUID(),
		Type:   OpTypeReplaceDevice,
		PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0],
			"new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	// Realidad del kernel: SIN balance, CON replace al 42% — el escenario
	// exacto que la lógica antigua (solo balance) daba por muerto.
	origBal, origRep := readBalanceStatusFn, readReplaceStateFn
	defer func() { readBalanceStatusFn, readReplaceStateFn = origBal, origRep }()
	readBalanceStatusFn = func(string) BalanceStatus { return BalanceStatus{Active: false} }
	readReplaceStateFn = func(string) replaceKernelState {
		return replaceKernelState{Active: true, Pct: 42.0}
	}

	outcome := service.resolveOrphanLayoutOp(ctx, op)
	if !outcome.Readopted {
		t.Fatalf("AUDIT-R2: replace del kernel VIVO → la op debe re-adoptarse, no morir inconclusive (got %+v)", outcome)
	}
	if outcome.NewStatus != OpStatusInProgress {
		t.Errorf("una op re-adoptada mantiene in_progress (conserva el lock); got %q", outcome.NewStatus)
	}
}

// Variante: sin replace vivo ni terminado, el camino seguro (inconclusive)
// se conserva — la excepción no convierte cualquier huérfana en re-adoptada.
func TestRecoveryReplaceOrphanStillInconclusiveWithoutKernelReplace(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	origBal, origRep := readBalanceStatusFn, readReplaceStateFn
	defer func() { readBalanceStatusFn, readReplaceStateFn = origBal, origRep }()
	readBalanceStatusFn = func(string) BalanceStatus { return BalanceStatus{Active: false} }
	readReplaceStateFn = func(string) replaceKernelState { return replaceKernelState{} } // Never started

	outcome := service.resolveOrphanLayoutOp(ctx, op)
	if outcome.Readopted {
		t.Fatal("sin replace del kernel no hay re-adopción: debe caer al camino inconclusive")
	}
	if outcome.NewStatus != OpStatusFailed {
		t.Errorf("camino seguro: failed/inconclusive; got %q", outcome.NewStatus)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R3 · watchReadoptedReplace debe persistir el SWAP old→new al
// terminar el replace, y solo con certeza (finished + sin disco missing).
// ─────────────────────────────────────────────────────────────────────────

func TestWatchReadoptedReplacePersistsSwapOnFinish(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	// El disco nuevo existe en BD pero aún no está asignado (el goroutine
	// original murió antes del swap).
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()

	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	origRep, origMissing, origKernel := readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn
	defer func() {
		readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn = origRep, origMissing, origKernel
	}()
	readReplaceStateFn = func(string) replaceKernelState {
		return replaceKernelState{Active: false, Finished: true, Pct: 100}
	}
	missingDevidCheckFn = func(string) string { return "" } // pool sano
	// AUDIT-R6: el kernel confirma el disco nuevo como miembro.
	kernelPoolHasDeviceFn = func(string, *Device) bool { return true }

	// El watcher sale en la primera iteración (finished): síncrono en test.
	service.watchReadoptedReplace(op, poolID, "/nimos/pools/data")

	final, _ := service.repo.GetOperation(ctx, op.ID)
	if final.Status != OpStatusCompleted {
		t.Fatalf("AUDIT-R3: con finished+sin missing la op debe completar; got %+v", final)
	}

	pool, _ := service.GetPool(ctx, poolID)
	oldStill, newIn := false, false
	for _, d := range pool.Devices {
		if d.ID == deviceIDs[0] {
			oldStill = true
		}
		if d.ID == "new-1" {
			newIn = true
		}
	}
	if oldStill {
		t.Error("AUDIT-R3: el disco viejo debe quedar DESASIGNADO tras el swap")
	}
	if !newIn {
		t.Error("AUDIT-R3: el disco nuevo debe quedar ASIGNADO tras el swap")
	}
}

// Regla 16: si el kernel aún reporta un disco missing, el 'finished' no es de
// esta op → NO tocar membresía, op failed/inconclusive.
func TestWatchReadoptedReplaceRefusesSwapIfDiskStillMissing(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	origRep, origMissing, origKernel := readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn
	defer func() {
		readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn = origRep, origMissing, origKernel
	}()
	readReplaceStateFn = func(string) replaceKernelState {
		return replaceKernelState{Finished: true, Pct: 100}
	}
	missingDevidCheckFn = func(string) string { return "2" } // aún falta un disco
	kernelPoolHasDeviceFn = func(string, *Device) bool { return true }

	service.watchReadoptedReplace(op, poolID, "/nimos/pools/data")

	final, _ := service.repo.GetOperation(ctx, op.ID)
	if final.Status != OpStatusFailed {
		t.Fatalf("con disco missing el swap NO se persiste y la op falla; got %+v", final)
	}
	pool, _ := service.GetPool(ctx, poolID)
	for _, d := range pool.Devices {
		if d.ID == "new-1" {
			t.Error("la membresía no debe tocarse sin certeza")
		}
	}
	oldStill := false
	for _, d := range pool.Devices {
		if d.ID == deviceIDs[0] {
			oldStill = true
		}
	}
	if !oldStill {
		t.Error("el disco viejo debe seguir asignado si no hubo swap")
	}
}


// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R6 · El swap exige que el KERNEL confirme el disco nuevo como
// miembro. Cubre el 'finished' residual de un replace anterior sobre una op
// creada pero jamás ejecutada (reemplazo proactivo, sin disco missing).
// ─────────────────────────────────────────────────────────────────────────

func TestWatchReadoptedReplaceRefusesSwapIfKernelLacksNewDevice(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()

	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	origRep, origMissing, origKernel := readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn
	defer func() {
		readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn = origRep, origMissing, origKernel
	}()
	readReplaceStateFn = func(string) replaceKernelState {
		return replaceKernelState{Finished: true, Pct: 100} // residual de OTRO replace
	}
	missingDevidCheckFn = func(string) string { return "" }              // sin missing (proactivo)
	kernelPoolHasDeviceFn = func(string, *Device) bool { return false } // kernel NO ve el nuevo

	service.watchReadoptedReplace(op, poolID, "/nimos/pools/data")

	final, _ := service.repo.GetOperation(ctx, op.ID)
	if final.Status != OpStatusFailed {
		t.Fatalf("sin confirmación del kernel el swap NO se persiste; got %+v", final)
	}
	pool, _ := service.GetPool(ctx, poolID)
	for _, d := range pool.Devices {
		if d.ID == "new-1" {
			t.Error("AUDIT-R6: la membresía no debe tocarse sin confirmación del kernel")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R5 · Replace SUSPENDIDO tras reboot completo: el watcher debe
// remontar degraded,rw para que el kernel lo reanude, y al terminar hacer
// el swap normal.
// ─────────────────────────────────────────────────────────────────────────

func TestWatchReadoptedReplaceResumesSuspendedAfterReboot(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	tx, _ := service.db.BeginTx(ctx, nil)
	service.repo.UpsertDevice(ctx, tx, &Device{
		ID: "new-1", Serial: "NEW-1",
		ByIDPath: "/dev/disk/by-id/new-1", CurrentPath: "/dev/sdn",
		SizeBytes: 2e12,
	})
	tx.Commit()

	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	// Secuencia real de un reboot a media copia: el status dice "suspended"
	// hasta que el pool se monta rw; entonces el kernel reanuda y termina.
	origRep, origMissing, origKernel := readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn
	origRO, origRW := poolMountIsReadOnly, remountPoolReadWriteDegraded
	origPoll := replaceWatchPollInterval
	defer func() {
		readReplaceStateFn, missingDevidCheckFn, kernelPoolHasDeviceFn = origRep, origMissing, origKernel
		poolMountIsReadOnly, remountPoolReadWriteDegraded = origRO, origRW
		replaceWatchPollInterval = origPoll
	}()
	replaceWatchPollInterval = 5 * time.Millisecond // acelerar el test

	resumed := false
	rwCalled := false
	poolMountIsReadOnly = func(string) bool { return !rwCalled } // ro hasta el remount
	remountPoolReadWriteDegraded = func(string) error {
		rwCalled = true
		resumed = true // el kernel reanuda al montar rw
		return nil
	}
	readReplaceStateFn = func(string) replaceKernelState {
		if !resumed {
			return replaceKernelState{Suspended: true, Pct: 37.5}
		}
		return replaceKernelState{Finished: true, Pct: 100}
	}
	missingDevidCheckFn = func(string) string { return "" }
	kernelPoolHasDeviceFn = func(string, *Device) bool { return true }

	service.watchReadoptedReplace(op, poolID, "/nimos/pools/data")

	if !rwCalled {
		t.Error("AUDIT-R5: con el replace suspendido y el pool en ro, el watcher debe remontar degraded,rw para reanudar")
	}
	final, _ := service.repo.GetOperation(ctx, op.ID)
	if final.Status != OpStatusCompleted {
		t.Fatalf("tras reanudar y terminar, la op debe completar con swap; got %+v", final)
	}
	pool, _ := service.GetPool(ctx, poolID)
	newIn := false
	for _, d := range pool.Devices {
		if d.ID == "new-1" {
			newIn = true
		}
		if d.ID == deviceIDs[0] {
			t.Error("el disco viejo debe quedar desasignado tras el swap")
		}
	}
	if !newIn {
		t.Error("el disco nuevo debe quedar asignado tras el swap")
	}
}

// El resolver también re-adopta un replace SUSPENDIDO (no solo activo/finished).
func TestRecoveryReplaceOrphanReadoptedWhenSuspended(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)
	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}

	origBal, origRep := readBalanceStatusFn, readReplaceStateFn
	defer func() { readBalanceStatusFn, readReplaceStateFn = origBal, origRep }()
	readBalanceStatusFn = func(string) BalanceStatus { return BalanceStatus{Active: false} }
	readReplaceStateFn = func(string) replaceKernelState {
		return replaceKernelState{Suspended: true, Pct: 37.5}
	}

	outcome := service.resolveOrphanLayoutOp(ctx, op)
	if !outcome.Readopted {
		t.Fatalf("AUDIT-R5: replace SUSPENDIDO tras reboot → re-adoptar, no inconclusive (got %+v)", outcome)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// AUDIT-R7 · UI honesta: una op de layout FALLIDA reciente debe salir como
// alerta persistente (banner), no quedar invisible en la tabla de ops.
// ─────────────────────────────────────────────────────────────────────────

func TestFailedLayoutOpBecomesAlert(t *testing.T) {
	service, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// failedLayoutOpAlerts lee el service por la global (la usa el health
	// check legacy). Inyectar y restaurar.
	origSvc := storageService
	storageService = service
	defer func() { storageService = origSvc }()

	poolID, deviceIDs := createTestPool(t, service, ctx, "data", ProfileRaid1, 2)

	op := &Operation{
		ID: newUUID(), Type: OpTypeReplaceDevice, PoolID: &poolID,
		Status: OpStatusInProgress,
		Data: rawJSON(map[string]interface{}{
			"old_device_id": deviceIDs[0], "new_device_id": "new-1",
		}),
	}
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.CreateOperation(ctx, tx, op)
	}); err != nil {
		t.Fatalf("setup op: %v", err)
	}
	// Marcarla FALLIDA (el caso que antes desaparecía en silencio).
	errMsg := "boom: target write error"
	errCode := ErrCodeBtrfsCommandFailed
	if err := service.runInTx(ctx, func(tx *sql.Tx) error {
		return service.repo.UpdateOperationStatus(ctx, tx, op.ID, OpStatusFailed, &errMsg, &errCode)
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	alerts := failedLayoutOpAlerts(ctx)
	found := false
	for _, a := range alerts {
		if a["kind"] == "operation_failed" && a["operation_id"] == op.ID {
			found = true
			if sev, _ := a["severity"].(string); sev != "critical" {
				t.Errorf("severity: got %q, want critical", sev)
			}
			if msg, _ := a["message"].(string); !strings.Contains(msg, "boom") {
				t.Errorf("la alerta debe llevar el error real; got %q", msg)
			}
		}
	}
	if !found {
		t.Fatal("AUDIT-R7: un replace fallido reciente debe aparecer como alerta")
	}
}
