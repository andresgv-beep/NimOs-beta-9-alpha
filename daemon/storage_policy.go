// storage_policy.go — Policy layer del módulo storage (Beta 8).
//
// PolicyChecker centraliza TODA la lógica de "¿puedo hacer esta operación?".
// Los handlers HTTP y StorageService consultan policy ANTES de ejecutar.
//
// Si la lógica de permisos se dispersa por handlers, en 6 meses tendrás 12
// sitios distintos verificando "¿es managed?" y "¿hay balance corriendo?"
// con código duplicado. Aquí se centraliza.
//
// see docs/storage_invariants.md#2 (Policy separado de Storage)
// see docs/storage_state_machines.md §2 (invariantes ejecutables)
// see docs/storage_api.md §5 (PolicyChecker)

package main

import "fmt"

// ═════════════════════════════════════════════════════════════════════════════
// PolicyChecker
// ═════════════════════════════════════════════════════════════════════════════

// PolicyChecker decide qué operaciones permite un pool dado.
// En Beta 8 es stateless (sin dependencias). En Beta 9+ puede recibir
// referencias a repo si necesita consultar más contexto.
type PolicyChecker struct{}

// NewPolicyChecker crea una instancia del policy checker.
func NewPolicyChecker() *PolicyChecker {
	return &PolicyChecker{}
}

// Instancia global, conveniente para código que no recibe el PolicyChecker
// por parámetro. Código nuevo debería inyectarla explícitamente.
var storagePolicy *PolicyChecker

// initStoragePolicy crea la instancia global. Llamar tras initStorageRepo.
func initStoragePolicy() {
	storagePolicy = NewPolicyChecker()
}

// ═════════════════════════════════════════════════════════════════════════════
// Allows / AllowsWithReason
// ═════════════════════════════════════════════════════════════════════════════

// Allows devuelve true si el pool permite la operación. Versión binaria
// simple. Para conocer la razón del rechazo, usar AllowsWithReason.
//
// Reglas Beta 8:
//  1. Si el pool está en estado != managed → todo rechazado (observed
//     no muta, los otros estados son Beta 9+).
//  2. Si la op requiere una capability, el pool debe tenerla.
//
// Beta 9+ añadirá: comprobar operaciones en curso (no permitir dos balances
// concurrentes... aunque el schema ya lo enforce), comprobar shares activos
// antes de destroy, etc.
func (p *PolicyChecker) Allows(pool *Pool, op OperationType) bool {
	allowed, _ := p.AllowsWithReason(pool, op)
	return allowed
}

// AllowsWithReason es como Allows pero devuelve también el código de error
// semántico cuando la op no está permitida. Para que el handler HTTP devuelva
// el código correcto al frontend (no parsear mensaje).
//
// Códigos posibles cuando devuelve (false, ...):
//   - ErrCodePoolObserved      → pool no es managed
//   - ErrCodeCapabilityMissing → pool no soporta esta op
func (p *PolicyChecker) AllowsWithReason(pool *Pool, op OperationType) (bool, string) {
	if pool == nil {
		return false, ErrCodePoolNotFound
	}

	// STOR-01-B: un pool en estado `recovery` (drift de layout detectado tras
	// crash) permite operaciones de "salida/gestión" — destruir, renombrar,
	// cambiar control — para que el usuario pueda actuar sobre él. Pero NO
	// permite nuevas operaciones de LAYOUT (add/remove/replace/convert): sería
	// apilar otra mutación sobre un estado ya inconsistente.
	if pool.ControlState == ControlStateRecovery {
		switch op {
		case OpTypeDestroyPool, OpTypeRenamePool, OpTypeControlChange:
			return true, ""
		default:
			return false, ErrCodePoolRecovery
		}
	}

	// Solo pools managed permiten mutaciones.
	if pool.ControlState != ControlStateManaged {
		return false, ErrCodePoolObserved
	}

	// Operaciones que siempre permite un pool managed (no requieren capability).
	switch op {
	case OpTypeDestroyPool,
		OpTypeRenamePool,
		OpTypeChangeRole,
		OpTypeControlChange:
		return true, ""
	}

	// Operaciones que requieren capability específica.
	requiredCap := CapabilityFor(op)
	if requiredCap == "" {
		// Op no mapeada — safe default: permitir si es managed.
		// Esto solo pasa con ops nuevas no documentadas todavía.
		return true, ""
	}

	if !pool.HasCapability(requiredCap) {
		return false, ErrCodeCapabilityMissing
	}
	return true, ""
}

// CapabilityFor devuelve la capability que requiere una OperationType.
// String vacía si la op no requiere capability específica.
//
// Esto es el "mapping operation → capability" que el documento storage_api.md
// menciona en §5.
func CapabilityFor(op OperationType) string {
	switch op {
	case OpTypeAddDevice:
		return "add_device"
	case OpTypeRemoveDevice:
		return "remove_device"
	case OpTypeReplaceDevice:
		return "replace_device"
	case OpTypeConvertProfile:
		return "convert_profile"
	case OpTypeStartScrub:
		return "scrub"
	case OpTypeCreateSnapshot, OpTypeDeleteSnapshot:
		return "snapshots"
	case OpTypeSetCompression:
		return "compression"
	}
	return ""
}

// ═════════════════════════════════════════════════════════════════════════════
// Transition Validators — Invariantes ejecutables
// ═════════════════════════════════════════════════════════════════════════════
//
// Los mapas siguientes son la VERDAD sobre transiciones válidas.
// El código consulta estos mapas antes de aplicar transiciones, y el
// documento storage_state_machines.md describe exactamente las mismas reglas.
// Si añades una transición aquí, añádela también al documento.
//
// see docs/storage_state_machines.md §2 (principio de invariantes ejecutables)

// TransitionError es el error semántico que devuelven los Validate*.
type TransitionError struct {
	From string
	To   string
	Code string
	Msg  string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s → %s (%s)", e.Code, e.From, e.To, e.Msg)
}

// ─────────────────────────────────────────────────────────────────────────────
// Pool transitions
// ─────────────────────────────────────────────────────────────────────────────

// poolStateNew es el "estado origen" virtual para CreatePool (no hay row
// en la DB todavía). Usamos string en lugar de ControlState para
// representar este pseudo-estado.
const poolStateNew = "*new*"

// permittedPoolTransitions define las transiciones VÁLIDAS de control_state.
// Beta 8 implementa solo managed ↔ observed; los demás están reservados
// para Beta 9+ (imported, foreign, recovery).
var permittedPoolTransitions = map[string]map[ControlState]bool{
	poolStateNew: {
		ControlStateManaged:  true,
		ControlStateObserved: true,
	},
	string(ControlStateManaged): {
		ControlStateObserved: true,
		// → "*removed*" se gestiona aparte (DeletePool, no es transición de estado)
	},
	string(ControlStateObserved): {
		ControlStateManaged: true,
	},

	// Beta 9+ (definidas en el schema pero no usadas en runtime):
	// string(ControlStateImported): { ControlStateManaged: true },
	// string(ControlStateRecovery): { ControlStateManaged: true },
}

// ValidatePoolTransition devuelve nil si la transición es válida, o un
// TransitionError con código semántico si no.
//
// from puede ser:
//   - poolStateNew (pool nuevo, sin row aún)
//   - cualquier valor de ControlState (estado actual del pool)
func ValidatePoolTransition(from string, to ControlState) error {
	allowed, ok := permittedPoolTransitions[from]
	if !ok {
		return &TransitionError{
			From: from, To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  fmt.Sprintf("unknown source state %q", from),
		}
	}
	if !allowed[to] {
		return &TransitionError{
			From: from, To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  fmt.Sprintf("transition not permitted in Beta 8"),
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Operation transitions
// ─────────────────────────────────────────────────────────────────────────────

// opStateNew es el pseudo-estado "antes de insertar la row".
const opStateNew = "*new*"

// permittedOperationTransitions define las transiciones VÁLIDAS de status.
// Estados terminales (completed, failed, rolled_back, cancelled) no tienen
// transiciones salientes — son finales.
var permittedOperationTransitions = map[string]map[OperationStatus]bool{
	opStateNew: {
		OpStatusPending:    true,
		OpStatusInProgress: true,
	},
	string(OpStatusPending): {
		OpStatusInProgress: true,
		OpStatusCancelled:  true,
	},
	string(OpStatusInProgress): {
		OpStatusCompleted: true,
		OpStatusFailed:    true,
	},
	string(OpStatusFailed): {
		OpStatusRolledBack: true,
	},

	// Estados terminales NO tienen salidas: completed, rolled_back, cancelled
}

// ValidateOperationTransition devuelve nil si la transición de status es
// válida, error semántico si no.
func ValidateOperationTransition(from string, to OperationStatus) error {
	allowed, ok := permittedOperationTransitions[from]
	if !ok {
		// from puede ser un estado terminal (no tiene salidas) → siempre rechaza
		return &TransitionError{
			From: from, To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  fmt.Sprintf("source state %q has no permitted transitions (terminal?)", from),
		}
	}
	if !allowed[to] {
		return &TransitionError{
			From: from, To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  "transition not permitted",
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Device transitions
// ─────────────────────────────────────────────────────────────────────────────
//
// El "estado" de un Device es PROYECCIÓN, no columna en DB:
//   - detected:  row existe, sin entrada en pool_devices
//   - assigned:  row existe + entrada en pool_devices
//   - missing:   row existe, last_seen_at > threshold
//   - <unknown>: no hay row con ese serial
//
// see docs/storage_state_machines.md §5

// DeviceState representa el estado proyectado de un device.
type DeviceState string

const (
	DeviceStateUnknown  DeviceState = "unknown"
	DeviceStateDetected DeviceState = "detected"
	DeviceStateAssigned DeviceState = "assigned"
	DeviceStateMissing  DeviceState = "missing"
)

// permittedDeviceTransitions define las transiciones válidas de DeviceState.
// (estos no se aplican a una columna, sino a las acciones que reconcilian
// estos estados implícitos: scan, assign, unassign, mark_missing)
var permittedDeviceTransitions = map[DeviceState]map[DeviceState]bool{
	DeviceStateUnknown: {
		DeviceStateDetected: true, // first scan
	},
	DeviceStateDetected: {
		DeviceStateAssigned: true, // AddDeviceToPool
		DeviceStateMissing:  true, // disappears from bus
	},
	DeviceStateAssigned: {
		DeviceStateDetected: true, // RemoveDeviceFromPool
		DeviceStateMissing:  true, // disappears from bus while in pool
	},
	DeviceStateMissing: {
		DeviceStateDetected: true, // reappears, no longer in pool
		DeviceStateAssigned: true, // reappears, still in pool (matched by serial)
	},
}

// ValidateDeviceTransition devuelve nil si la transición es válida, error
// semántico si no.
func ValidateDeviceTransition(from, to DeviceState) error {
	allowed, ok := permittedDeviceTransitions[from]
	if !ok {
		return &TransitionError{
			From: string(from), To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  fmt.Sprintf("unknown device state %q", from),
		}
	}
	if !allowed[to] {
		return &TransitionError{
			From: string(from), To: string(to),
			Code: ErrCodeTransitionNotPermitted,
			Msg:  "device transition not permitted",
		}
	}
	return nil
}
