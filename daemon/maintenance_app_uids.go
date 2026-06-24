// maintenance_app_uids.go — Tarea: higiene de UIDs de apps Docker.
//
// Ejecuta el reconciler de higiene (app_uids.go · Fase 4 del refactor de
// permisos): limpia los usuarios de sistema fantasma de apps desinstaladas-total
// (sin datos en disco), SIN reusar jamás un UID.
//
// Cumple las 4 reglas del contrato de mantenimiento:
//   1. refuse-if-uncertain → sin pool montado, Skipped (no inventa rutas, y el
//      find -uid no tendría dónde buscar de forma fiable).
//   2. skip-known          → apps activas y apps con datos conservados
//      (desinstaladas-normal) se PRESERVAN, nunca se tocan.
//   3. grace-period         → no aplica directamente; la seguridad viene de
//      "released_at IS NOT NULL" + "sin archivos en disco". Una app recién
//      desinstalada-total sin datos es basura segura inmediatamente.
//   4. log-everything       → el reconciler registra cada limpieza/preservación.
//
// CRÍTICO: nunca libera ni reusa el número de UID (anti-reciclaje). Solo elimina
// el usuario/grupo de sistema cuando NO quedan datos del UID.

package main

import (
	"context"
	"fmt"
)

type appUIDsHygieneTask struct{}

func (t *appUIDsHygieneTask) ID() string   { return "app_uids_hygiene" }
func (t *appUIDsHygieneTask) Name() string { return "Higiene de usuarios de apps Docker" }
func (t *appUIDsHygieneTask) Category() string { return MaintCategoryDocker }
func (t *appUIDsHygieneTask) Description() string {
	return "Elimina los usuarios de sistema sobrantes de apps Docker desinstaladas por completo. " +
		"Nunca toca apps activas ni apps cuyos datos se conservaron, y nunca reutiliza identificadores."
}

func (t *appUIDsHygieneTask) DefaultSchedule() Schedule {
	// Diario · es barato (un find acotado) y no urge. Sin grace especial.
	return Schedule{Kind: ScheduleDaily, AtHour: 4, AtMinute: 30}
}

func (t *appUIDsHygieneTask) Run(ctx context.Context) MaintenanceResult {
	// Regla 1 (refuse-if-uncertain): sin pool montado, no buscamos.
	mount, err := firstMountedPoolPath()
	if err != nil {
		return MaintenanceResult{Skipped: true, SkipReason: "no hay pool montado"}
	}
	if db == nil {
		return MaintenanceResult{Skipped: true, SkipReason: "base de datos no disponible"}
	}

	// activeAppIDs · apps actualmente instaladas (para no tocar sus usuarios).
	active := map[string]bool{}
	if appsRepo != nil {
		if apps, err := appsRepo.ListDockerApps(ctx); err == nil {
			for _, a := range apps {
				if a.ID != "" {
					active[a.ID] = true
				}
			}
		} else {
			// Si no podemos saber qué apps están activas, mejor NO limpiar nada
			// (regla 1): un falso "inactiva" borraría un usuario en uso.
			return MaintenanceResult{Skipped: true, SkipReason: fmt.Sprintf("no se pudo listar apps activas: %v", err)}
		}
	}

	// El reconciler acota el find al área de containers del pool montado.
	rep := reconcileAppUIDs(db, mount, active, nil)

	return MaintenanceResult{
		ItemsRemoved: int64(len(rep.Cleaned)),
		// No liberamos espacio (solo usuarios de sistema) · BytesFreed = 0.
		Skipped:    false,
		SkipReason: "",
	}
}
