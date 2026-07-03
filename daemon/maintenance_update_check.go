// maintenance_update_check.go — Tarea: búsqueda diaria de actualizaciones de apps.
//
// UPD-CRON. Hasta ahora las actualizaciones solo se detectaban al ABRIR la
// ficha de una app instalada (update-check bajo demanda). Esta tarea hace la
// detección PROACTIVA: cada día (por defecto 03:00) recorre todas las apps
// instaladas, compara el digest local vs el del registry (manifest, SIN
// descargar imágenes) y persiste el resultado en BD. Los consumidores ya
// existentes (updates-summary → badge del AppStore, ficha de la app) leen esa
// BD, así que el usuario ve los updates pendientes sin entrar app por app.
//
// SOLO DETECTA Y MARCA · NUNCA AUTO-ACTUALIZA. Una actualización desatendida
// puede romper una app mientras nadie mira (breaking changes, migraciones de
// schema); actualizar es SIEMPRE decisión explícita del usuario desde la UI.
//
// Rate limiting (Docker Hub anónimo ~100 pulls/6h/IP, y cada manifest de una
// imagen multi-arch cuesta varias consultas): las apps se procesan en TANDAS
// de updateCheckBatchSize consultas concurrentes, con updateCheckBatchDelay
// de espera entre tandas, hasta completar la lista. Además se respeta el TTL
// de cache (updateCheckTTL): las apps comprobadas hace poco se saltan sin
// tocar el registry.
//
// Cumple el contrato de mantenimiento:
//  1. refuse-if-uncertain → sin Docker o sin repo de imágenes, SE SALTA.
//  2. skip-known          → solo lee manifests remotos y escribe en BD propia;
//                           no toca containers, imágenes locales ni datos.
//  3. grace-period        → no aplica (no borra nada).
//  4. log-everything      → registra apps consultadas, saltadas por TTL y
//                           cuántas tienen update disponible.

package main

import (
	"context"
	"sync"
	"time"
)

// Parámetros del chequeo por tandas · constantes con nombre para poder
// ajustarlas en una línea (no números mágicos). Diseño acordado: robustez
// sobre velocidad — es un job nocturno desatendido, nadie espera mirando.
const (
	// updateCheckBatchSize · apps consultadas EN PARALELO por tanda.
	// 3 es el punto dulce: suficiente paralelismo, suave con el rate limit
	// (cada app puede costar varias consultas al registry por multi-arch).
	updateCheckBatchSize = 3

	// updateCheckBatchDelay · espera entre tandas.
	updateCheckBatchDelay = 60 * time.Second

	// updateCheckPerAppTimeout · timeout por app (mismo criterio que el
	// update-check HTTP: ~5 imágenes × 15s peor caso, acotado).
	updateCheckPerAppTimeout = 30 * time.Second
)

type appUpdateCheckTask struct{}

func (t *appUpdateCheckTask) ID() string       { return "app_update_check" }
func (t *appUpdateCheckTask) Name() string     { return "Búsqueda de actualizaciones de apps" }
func (t *appUpdateCheckTask) Category() string { return MaintCategoryDocker }
func (t *appUpdateCheckTask) Description() string {
	return "Comprueba a diario si hay versiones nuevas de las apps instaladas comparando digests con el registry (sin descargar nada). Solo detecta y lo marca en el AppStore · nunca actualiza por sí sola."
}

func (t *appUpdateCheckTask) DefaultSchedule() Schedule {
	// Diario a las 03:00 · hora valle acordada. El usuario puede cambiarlo.
	return Schedule{Kind: ScheduleDaily, AtHour: 3, AtMinute: 0}
}

func (t *appUpdateCheckTask) Run(ctx context.Context) MaintenanceResult {
	// 1. refuse-if-uncertain.
	if !isDockerInstalledGo() {
		return MaintenanceResult{Skipped: true, SkipReason: "Docker no instalado"}
	}
	if appImagesRepo == nil {
		return MaintenanceResult{Skipped: true, SkipReason: "Repositorio de imágenes no disponible"}
	}

	appIDs, err := appImagesRepo.ListAppIDs(ctx)
	if err != nil {
		return MaintenanceResult{Err: err}
	}
	if len(appIDs) == 0 {
		return MaintenanceResult{Skipped: true, SkipReason: "No hay apps instaladas"}
	}

	checked, skippedFresh := 0, 0

	// 2. Recorrer en tandas de updateCheckBatchSize concurrentes + delay.
	for start := 0; start < len(appIDs); start += updateCheckBatchSize {
		end := start + updateCheckBatchSize
		if end > len(appIDs) {
			end = len(appIDs)
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, appID := range appIDs[start:end] {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()

				// TTL · si TODAS las imágenes de la app se comprobaron hace
				// poco, no tocamos el registry (cache fresca). Mismo criterio
				// que el update-check HTTP sin ?force.
				images, gerr := appImagesRepo.GetByApp(ctx, id)
				if gerr != nil {
					logMsg("maintenance: app_update_check GetByApp(%s): %v", id, gerr)
					return
				}
				stale := false
				for _, img := range images {
					if img.NeedsRemoteCheck(updateCheckTTL) {
						stale = true
						break
					}
				}
				if !stale {
					mu.Lock()
					skippedFresh++
					mu.Unlock()
					return
				}

				cctx, cancel := context.WithTimeout(ctx, updateCheckPerAppTimeout)
				defer cancel()
				if _, rerr := refreshRemoteDigestsForApp(cctx, appImagesRepo, id); rerr != nil {
					// No abortamos la tanda: el fallo por-app queda logueado
					// dentro de refreshRemoteDigestsForApp y persistido como
					// check_status para que la UI lo refleje.
					logMsg("maintenance: app_update_check refresh(%s): %v", id, rerr)
					return
				}
				mu.Lock()
				checked++
				mu.Unlock()
			}(appID)
		}
		wg.Wait()

		// Delay entre tandas (no tras la última) · respetando cancelación.
		if end < len(appIDs) {
			select {
			case <-ctx.Done():
				return MaintenanceResult{Err: ctx.Err()}
			case <-time.After(updateCheckBatchDelay):
			}
		}
	}

	// 3. Métrica final: cuántas apps quedaron con update disponible.
	// ItemsRemoved se reutiliza como "apps con update" (la UI lo muestra como
	// "N elem"): es la cifra útil de esta tarea, que no borra nada.
	withUpdates, cerr := appImagesRepo.CountAppsWithUpdates(ctx)
	if cerr != nil {
		logMsg("maintenance: app_update_check count: %v", cerr)
		withUpdates = 0
	}

	// 4. log-everything.
	logMsg("maintenance: app_update_check · %d app(s) consultadas al registry, %d con cache fresca (TTL), %d con update disponible",
		checked, skippedFresh, withUpdates)

	return MaintenanceResult{ItemsRemoved: int64(withUpdates)}
}
