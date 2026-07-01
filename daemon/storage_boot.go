// storage_boot.go — Inicialización del módulo storage Beta 8.
//
// Centraliza el arranque del nuevo stack (Repo + Policy) en una sola
// función llamada desde main.go tras initStorageSchema().
//
// Orden de arranque del módulo:
//   1. openDB              ← db.go (PRAGMA foreign_keys + WAL)
//   2. createTables        ← db.go (tablas base del daemon)
//   3. migrateFromJSON     ← db.go (compatibilidad con JSON viejo)
//   4. initStorageSchema   ← storage_schema.go (tablas storage_* Beta 8)
//   5. initStorageModule   ← este archivo (Repo + Policy listos)
//
// Tras este punto:
//   - storageRepo es la instancia global del StorageRepo
//   - storagePolicy es la instancia global del PolicyChecker
//   - Ambos pueden usarse desde cualquier parte del daemon
//
// see docs/storage_invariants.md
// see docs/storage_api.md §2 (capa de servicio)

package main

import (
	"context"
	"fmt"
	"time"
)

// initStorageModule inicializa el módulo de storage Beta 8.
// Debe llamarse DESPUÉS de initStorageSchema() (tablas creadas) y ANTES
// de cualquier código que use storageRepo o storagePolicy.
//
// Crea los singletons globales del módulo y verifica que están operativos
// con una query de comprobación (defensive: si la conexión está rota
// queremos saberlo aquí, no en el primer request HTTP).
func initStorageModule() error {
	if db == nil {
		return fmt.Errorf("initStorageModule: db is nil (call openDB first)")
	}

	// Crear singletons globales.
	initStorageRepo()
	initStoragePolicy()
	initStorageService()

	// Handler HTTP del nuevo stack — se registra en http.go al arrancar
	// el servidor (debe estar listo antes de startHTTPServer).
	storageHTTPHandler = NewStorageHTTPHandler(storageService)

	// Verificación defensiva: leer global_generation. Si esto falla,
	// algo está mal con la conexión o con el schema.
	gen, err := storageRepo.GetGlobalGeneration(context.Background())
	if err != nil {
		return fmt.Errorf("initStorageModule: cannot read global_generation: %w", err)
	}

	logMsg("Storage module ready (global_generation=%d)", gen)
	return nil
}

// runStorageStartupTasks ejecuta las tareas de arranque del módulo storage
// que requieren al servicio ya inicializado:
//
//  1. RecoverPendingOperations — resuelve operations huérfanas tras crash
//  2. ReconcileDevicesAtBoot   — scan inicial, actualiza last_seen_at
//
// Llamar DESPUÉS de initStorageModule() y ANTES de servir tráfico HTTP.
//
// Ningún fallo aquí debe abortar el daemon — son tareas best-effort. Si
// algo falla, loggeamos y seguimos; el frontend o el reconciler en
// background recogerán el guante.
func runStorageStartupTasks(ctx context.Context) {
	if storageService == nil {
		logMsg("runStorageStartupTasks: service not initialized, skipping")
		return
	}

	// 0. P5 — restaurar /etc/fstab si quedó corrupto tras un crash a media
	//    escritura. Va PRIMERO: el remontaje de pools (reconcileMountState,
	//    que corre poco después) depende de un fstab sano.
	restoreFstabIfCorrupt()

	// 1. Recovery de operations huérfanas
	rec, err := storageService.RecoverPendingOperations(ctx)
	if err != nil {
		logMsg("Storage recovery: ERROR (continuing anyway): %v", err)
	} else if rec.Inspected > 0 {
		logMsg("Storage recovery: %d operations inspected (%d completed, %d rolled_back, %d inconclusive, %d readopted)",
			rec.Inspected, rec.Completed, rec.RolledBack, rec.Inconclusive, rec.Readopted)
	}

	// 2. Boot reconciliation
	if err := storageService.ReconcileDevicesAtBoot(ctx); err != nil {
		logMsg("Storage boot reconciliation: ERROR (continuing anyway): %v", err)
	}

	// 3. Habilitar quota BTRFS en todos los pools (idempotente).
	//    Repara pools existentes que nunca tuvieron `btrfs quota enable`, sin
	//    el cual las quotas de share/carpeta no se aplican.
	//
	//    Va en goroutine con su PROPIO contexto (no el `ctx` del arranque, que
	//    se cancela al retornar esta función y mataría el barrido antes de
	//    ejecutarlo). Un pequeño retardo da margen a que el storage service
	//    quede plenamente operativo. Habilitar quota en un pool con datos
	//    dispara un rescan asíncrono de BTRFS, así que esto no debe bloquear.
	go func() {
		time.Sleep(3 * time.Second)
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		enableQuotaOnAllPools(bgCtx)
	}()
}
