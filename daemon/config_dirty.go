// config_dirty.go — OP-1 · Backup de config event-driven (debounce + coalescencia).
//
// PROBLEMA:
// El backup de config corría por timer cada 30 min → ventana de pérdida de hasta
// 30 min. Un cambio de config (crear share, permiso, usuario) podía perderse si
// la SD moría antes del siguiente tick.
//
// SOLUCIÓN:
// Señalar `markConfigDirty()` en cada escritura de config DURABLE (shares, users,
// permisos, pools — NO sesiones/notifs/health, de alta rotación). Un flusher
// recoge la señal, espera un debounce corto acumulando más cambios (coalescencia:
// 20 cambios seguidos = 1 backup), y respalda una vez. Un backstop periódico sigue
// corriendo como red de seguridad. Baja la ventana de minutos a segundos.

package main

import "time"

// configDirty · canal de capacidad 1: una señal pendiente coalesce todas las que
// lleguen hasta que el flusher la consuma.
var configDirty = make(chan struct{}, 1)

// markConfigDirty señala que la config durable cambió. No bloquea nunca: si ya hay
// una señal pendiente, este cambio queda coalescido en ella (un backup los cubre).
func markConfigDirty() {
	select {
	case configDirty <- struct{}{}:
	default: // ya hay señal pendiente → coalescida
	}
}

// dirtyIfOK marca la config como sucia SOLO si la escritura tuvo éxito, y propaga
// el error tal cual. Helper para envolver los `return err` de las funciones db de
// config durable: `return dirtyIfOK(err)`.
func dirtyIfOK(err error) error {
	if err == nil {
		markConfigDirty()
	}
	return err
}

// runConfigBackupLoop es el flusher event-driven, extraído para ser testeable.
//   - dirty:    canal de señales de cambio.
//   - debounce: ventana de coalescencia tras la primera señal.
//   - backstop: respaldo periódico de seguridad (red por si algo no marca dirty).
//   - backup:   la acción de respaldo (en producción, backupConfigToPoolGo).
//   - stop:     cierra el loop (en producción, nil = nunca para).
func runConfigBackupLoop(dirty <-chan struct{}, debounce, backstop time.Duration, backup func(), stop <-chan struct{}) {
	ticker := time.NewTicker(backstop)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-dirty:
			// Coalescencia: tras la primera señal, esperar `debounce` acumulando
			// más señales. Cada nueva señal reinicia la ventana, de modo que una
			// ráfaga de cambios se resuelve en UN solo backup al final.
			timer := time.NewTimer(debounce)
			for waiting := true; waiting; {
				select {
				case <-stop:
					timer.Stop()
					return
				case <-dirty:
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(debounce)
				case <-timer.C:
					waiting = false
				}
			}
			backup()
		case <-ticker.C:
			backup()
		}
	}
}
