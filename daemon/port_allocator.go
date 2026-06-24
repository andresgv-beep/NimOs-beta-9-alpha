package main

import "fmt"

// port_allocator.go — Fase 2 del Port Allocator (PORT-ALLOCATOR-DESIGN v1.1).
//
// Función PURA que decide el puerto HOST de un binding. Sin DB ni red: todo el
// estado (ocupación, reservados, puerto pegajoso) entra por argumento. El wiring
// que la invoca en install vive en la Fase 4.

// allocatePort decide el puerto HOST para un único puerto lógico.
//
//	preferred : puerto host deseado (lado host del compose / default del configField).
//	fixed     : true si es protocolo fijo (DNS, juego) que NO puede reasignarse.
//	sticky    : puerto host previamente asignado a esta app (reinstalación); 0 si nuevo.
//	occupied  : puertos host en uso por OTRAS apps (el caller EXCLUYE la app actual,
//	            para que su propio puerto previo no se bloquee a sí mismo).
//	hard/soft : reservados duros / blandos (port_reserved.go).
//
// Política:
//   - Pegajoso: si el puerto previo sigue disponible, se conserva (no rompe URLs).
//   - Flotante: preferido si está libre y no es blando; si no, primer libre del pool.
//   - Fijo: preferido (puede reclamar un blando si está libre); si está ocupado,
//     error "elige una" (los fijos NO se reasignan).
//
// tcp+udp del mismo puerto lógico: el caller llama UNA vez y aplica el resultado a
// ambas líneas (ver §5 del diseño). Esta función es agnóstica al protocolo.
//
// Si el caller asigna varios puertos para la misma app, debe marcar cada resultado
// como `occupied` antes de la siguiente llamada (para no repetir dentro del pool).
func allocatePort(preferred int, fixed bool, sticky int, occupied, hard, soft map[int]bool) (int, error) {
	// 1) Pegajoso · conservar el puerto previo si sigue disponible.
	if sticky > 0 && isPortFree(sticky, occupied, hard) {
		// Los flotantes no deben caer sobre un blando; los fijos sí pueden.
		if fixed || !soft[sticky] {
			return sticky, nil
		}
	}

	// 2) Fijo · preferido o error (no se reasigna).
	if fixed {
		if preferred < 1 || preferred > 65535 {
			return 0, fmt.Errorf("puerto fijo inválido: %d", preferred)
		}
		// isPortFree ignora el blando a propósito → un fijo puede reclamar el 53.
		if isPortFree(preferred, occupied, hard) {
			return preferred, nil
		}
		return 0, fmt.Errorf("puerto fijo %d ya está en uso · elige otro o desinstala la app que lo ocupa", preferred)
	}

	// 3) Flotante · preferido si libre y no-blando.
	if preferred >= 1 && preferred <= 65535 && isPortFree(preferred, occupied, hard) && !soft[preferred] {
		return preferred, nil
	}

	// 4) Flotante · primer libre del pool (30000–59999).
	// El pool nunca solapa blandos (todos <1024), así que basta isPortFree.
	for p := floatPoolMin; p <= floatPoolMax; p++ {
		if isPortFree(p, occupied, hard) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("pool de puertos flotantes agotado (%d-%d)", floatPoolMin, floatPoolMax)
}
