// storage_policy_test.go — Tests del PolicyChecker y validators de transición.
//
// Incluye un "test cartesiano exhaustivo" para cada entidad: prueba TODAS
// las combinaciones (from, to) y verifica que el resultado coincide con
// lo documentado en storage_state_machines.md.
//
// Si añades una transición al mapa permitted*Transitions pero olvidas
// añadirla al documento, este test no lo detecta (no es bidireccional).
// Pero si añades una al documento y olvidas el mapa, los tests rompen.
//
// Ejecutar:
//   go test -run TestStoragePolicy -v
//   go test -run TestStorageTransition -v

package main

import (
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// PolicyChecker.Allows
// ─────────────────────────────────────────────────────────────────────────────

// STOR-01-B: un pool en recovery permite salida (destroy/rename/control) pero
// bloquea operaciones de layout.
func TestStoragePolicyRecoveryAllowsExitBlocksLayout(t *testing.T) {
	policy := NewPolicyChecker()
	pool := &Pool{
		ID:           "prec",
		ControlState: ControlStateRecovery,
		Capabilities: []string{"add_device", "remove_device", "convert_profile"},
	}

	// Validación: ops de salida PERMITIDAS
	exitOps := []OperationType{OpTypeDestroyPool, OpTypeRenamePool, OpTypeControlChange}
	for _, op := range exitOps {
		allowed, code := policy.AllowsWithReason(pool, op)
		if !allowed {
			t.Errorf("%s en pool recovery: debería permitirse (salida), got code %q", op, code)
		}
	}

	// Errores: ops de LAYOUT BLOQUEADAS con código pool_recovery
	layoutOps := []OperationType{
		OpTypeAddDevice, OpTypeRemoveDevice, OpTypeReplaceDevice, OpTypeConvertProfile,
	}
	for _, op := range layoutOps {
		allowed, code := policy.AllowsWithReason(pool, op)
		if allowed {
			t.Errorf("%s en pool recovery: debería bloquearse (layout sobre estado inconsistente)", op)
		}
		if code != ErrCodePoolRecovery {
			t.Errorf("%s en recovery: got code %q, want %q", op, code, ErrCodePoolRecovery)
		}
	}
}

func TestStoragePolicyAllowsObservedRejected(t *testing.T) {
	policy := NewPolicyChecker()
	pool := &Pool{
		ID:           "p1",
		ControlState: ControlStateObserved,
		Capabilities: []string{"snapshots", "scrub"},
	}

	// Cualquier op de mutación debe rechazarse en observed
	ops := []OperationType{
		OpTypeDestroyPool,
		OpTypeAddDevice,
		OpTypeRemoveDevice,
		OpTypeReplaceDevice,
		OpTypeConvertProfile,
		OpTypeStartScrub,
		OpTypeRenamePool,
		OpTypeSetCompression,
		OpTypeCreateSnapshot,
	}
	for _, op := range ops {
		allowed, code := policy.AllowsWithReason(pool, op)
		if allowed {
			t.Errorf("%s on observed pool: should be rejected", op)
		}
		if code != ErrCodePoolObserved {
			t.Errorf("%s on observed: got code %q, want %q", op, code, ErrCodePoolObserved)
		}
	}
}

func TestStoragePolicyAllowsManagedWithCapabilities(t *testing.T) {
	policy := NewPolicyChecker()
	pool := &Pool{
		ID:           "p1",
		ControlState: ControlStateManaged,
		Capabilities: DefaultBtrfsManagedCapabilities(),
	}

	// Todas las ops de un pool BTRFS managed completo deben permitirse
	ops := []OperationType{
		OpTypeAddDevice,
		OpTypeRemoveDevice,
		OpTypeReplaceDevice,
		OpTypeConvertProfile,
		OpTypeStartScrub,
		OpTypeCreateSnapshot,
		OpTypeDeleteSnapshot,
		OpTypeSetCompression,
	}
	for _, op := range ops {
		allowed, code := policy.AllowsWithReason(pool, op)
		if !allowed {
			t.Errorf("%s on managed-full: rejected with %q (should pass)", op, code)
		}
	}
}

func TestStoragePolicyAllowsManagedWithoutCapability(t *testing.T) {
	policy := NewPolicyChecker()
	// Pool managed pero sin la capability "replace_device"
	pool := &Pool{
		ID:           "p1",
		ControlState: ControlStateManaged,
		Capabilities: []string{"snapshots", "scrub"}, // limitado
	}

	allowed, code := policy.AllowsWithReason(pool, OpTypeReplaceDevice)
	if allowed {
		t.Error("replace_device without capability should be rejected")
	}
	if code != ErrCodeCapabilityMissing {
		t.Errorf("got code %q, want %q", code, ErrCodeCapabilityMissing)
	}

	// Pero snapshots SÍ debe permitirse
	allowed, _ = policy.AllowsWithReason(pool, OpTypeCreateSnapshot)
	if !allowed {
		t.Error("create_snapshot WITH capability should pass")
	}
}

func TestStoragePolicyAlwaysAllowedOps(t *testing.T) {
	// Algunas ops no requieren capability si el pool es managed.
	policy := NewPolicyChecker()
	pool := &Pool{
		ID:           "p1",
		ControlState: ControlStateManaged,
		Capabilities: []string{}, // sin capabilities
	}

	alwaysAllowed := []OperationType{
		OpTypeDestroyPool,
		OpTypeRenamePool,
		OpTypeChangeRole,
	}
	for _, op := range alwaysAllowed {
		if !policy.Allows(pool, op) {
			t.Errorf("%s should be always allowed on managed pool", op)
		}
	}
}

func TestStoragePolicyNilPool(t *testing.T) {
	policy := NewPolicyChecker()
	allowed, code := policy.AllowsWithReason(nil, OpTypeAddDevice)
	if allowed {
		t.Error("nil pool should be rejected")
	}
	if code != ErrCodePoolNotFound {
		t.Errorf("nil pool: got code %q, want %q", code, ErrCodePoolNotFound)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ValidatePoolTransition
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageTransitionPoolValid(t *testing.T) {
	cases := []struct {
		from string
		to   ControlState
	}{
		{poolStateNew, ControlStateManaged},
		{poolStateNew, ControlStateObserved},
		{string(ControlStateManaged), ControlStateObserved},
		{string(ControlStateObserved), ControlStateManaged},
	}
	for _, c := range cases {
		if err := ValidatePoolTransition(c.from, c.to); err != nil {
			t.Errorf("%s → %s: should be valid, got %v", c.from, c.to, err)
		}
	}
}

func TestStorageTransitionPoolInvalid(t *testing.T) {
	cases := []struct {
		from string
		to   ControlState
	}{
		// Transiciones a estados de Beta 9+ no implementadas en runtime
		{string(ControlStateManaged), ControlStateImported},
		{string(ControlStateObserved), ControlStateForeign},
		// Estado origen desconocido
		{"completely_invalid", ControlStateManaged},
		// Mismo estado (no es transición)
		{string(ControlStateManaged), ControlStateManaged},
	}
	for _, c := range cases {
		err := ValidatePoolTransition(c.from, c.to)
		if err == nil {
			t.Errorf("%s → %s: should be invalid", c.from, c.to)
			continue
		}
		te, ok := err.(*TransitionError)
		if !ok {
			t.Errorf("%s → %s: error type not TransitionError", c.from, c.to)
			continue
		}
		if te.Code != ErrCodeTransitionNotPermitted {
			t.Errorf("%s → %s: code %q", c.from, c.to, te.Code)
		}
	}
}

// TestStorageTransitionPoolExhaustive es el test cartesiano: itera por TODAS
// las combinaciones (from, to) posibles y verifica que el resultado coincide
// con la tabla del documento storage_state_machines.md §3.2.
//
// Si la documentación y el código divergen, este test rompe.
func TestStorageTransitionPoolExhaustive(t *testing.T) {
	// Todos los posibles "from" (estados origen reconocidos)
	allFromStates := []string{
		poolStateNew,
		string(ControlStateManaged),
		string(ControlStateObserved),
		string(ControlStateImported),
		string(ControlStateForeign),
		string(ControlStateRecovery),
	}

	// Todos los posibles "to" (target states)
	allToStates := []ControlState{
		ControlStateManaged,
		ControlStateObserved,
		ControlStateImported,
		ControlStateForeign,
		ControlStateRecovery,
	}

	// Ground truth según documento: solo Beta 8 implementado
	expectedValid := map[string]map[ControlState]bool{
		poolStateNew: {
			ControlStateManaged:  true,
			ControlStateObserved: true,
		},
		string(ControlStateManaged): {
			ControlStateObserved: true,
		},
		string(ControlStateObserved): {
			ControlStateManaged: true,
		},
	}

	for _, from := range allFromStates {
		for _, to := range allToStates {
			err := ValidatePoolTransition(from, to)
			expected := expectedValid[from][to]
			gotValid := (err == nil)
			if gotValid != expected {
				t.Errorf("(%s → %s): got valid=%v, want valid=%v",
					from, to, gotValid, expected)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ValidateOperationTransition
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageTransitionOperationValid(t *testing.T) {
	cases := []struct {
		from string
		to   OperationStatus
	}{
		{opStateNew, OpStatusPending},
		{opStateNew, OpStatusInProgress},
		{string(OpStatusPending), OpStatusInProgress},
		{string(OpStatusPending), OpStatusCancelled},
		{string(OpStatusInProgress), OpStatusCompleted},
		{string(OpStatusInProgress), OpStatusFailed},
		{string(OpStatusFailed), OpStatusRolledBack},
	}
	for _, c := range cases {
		if err := ValidateOperationTransition(c.from, c.to); err != nil {
			t.Errorf("op %s → %s: should be valid, got %v", c.from, c.to, err)
		}
	}
}

func TestStorageTransitionOperationInvalid(t *testing.T) {
	cases := []struct {
		from string
		to   OperationStatus
	}{
		// Estados terminales no tienen salidas
		{string(OpStatusCompleted), OpStatusInProgress},
		{string(OpStatusCompleted), OpStatusFailed},
		{string(OpStatusRolledBack), OpStatusCompleted},
		{string(OpStatusCancelled), OpStatusPending},
		// failed → completed (no se puede "des-fallar")
		{string(OpStatusFailed), OpStatusCompleted},
		// in_progress → pending (no se retrocede)
		{string(OpStatusInProgress), OpStatusPending},
		// in_progress → cancelled (Beta 8 no soporta cancelación en curso)
		{string(OpStatusInProgress), OpStatusCancelled},
	}
	for _, c := range cases {
		if err := ValidateOperationTransition(c.from, c.to); err == nil {
			t.Errorf("op %s → %s: should be invalid", c.from, c.to)
		}
	}
}

func TestStorageTransitionOperationExhaustive(t *testing.T) {
	allStates := []string{
		opStateNew,
		string(OpStatusPending),
		string(OpStatusInProgress),
		string(OpStatusCompleted),
		string(OpStatusFailed),
		string(OpStatusRolledBack),
		string(OpStatusCancelled),
	}
	allTargets := []OperationStatus{
		OpStatusPending,
		OpStatusInProgress,
		OpStatusCompleted,
		OpStatusFailed,
		OpStatusRolledBack,
		OpStatusCancelled,
	}

	// Ground truth según documento storage_state_machines.md §4.2
	expected := map[string]map[OperationStatus]bool{
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
		// completed, rolled_back, cancelled son terminales (sin salidas)
	}

	for _, from := range allStates {
		for _, to := range allTargets {
			err := ValidateOperationTransition(from, to)
			want := expected[from][to]
			got := (err == nil)
			if got != want {
				t.Errorf("op (%s → %s): got valid=%v, want valid=%v",
					from, to, got, want)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ValidateDeviceTransition
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageTransitionDeviceValid(t *testing.T) {
	cases := []struct {
		from, to DeviceState
	}{
		{DeviceStateUnknown, DeviceStateDetected},
		{DeviceStateDetected, DeviceStateAssigned},
		{DeviceStateAssigned, DeviceStateDetected},
		{DeviceStateAssigned, DeviceStateMissing},
		{DeviceStateDetected, DeviceStateMissing},
		{DeviceStateMissing, DeviceStateAssigned},
		{DeviceStateMissing, DeviceStateDetected},
	}
	for _, c := range cases {
		if err := ValidateDeviceTransition(c.from, c.to); err != nil {
			t.Errorf("device %s → %s: should be valid, got %v", c.from, c.to, err)
		}
	}
}

func TestStorageTransitionDeviceInvalid(t *testing.T) {
	cases := []struct {
		from, to DeviceState
	}{
		// No se puede "desconocer" un device ya visto
		{DeviceStateDetected, DeviceStateUnknown},
		{DeviceStateAssigned, DeviceStateUnknown},
		{DeviceStateMissing, DeviceStateUnknown},
		// No saltar de unknown a assigned (debe pasar por detected primero)
		{DeviceStateUnknown, DeviceStateAssigned},
		// No saltar de unknown a missing
		{DeviceStateUnknown, DeviceStateMissing},
	}
	for _, c := range cases {
		if err := ValidateDeviceTransition(c.from, c.to); err == nil {
			t.Errorf("device %s → %s: should be invalid", c.from, c.to)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CapabilityFor — mapping completo
// ─────────────────────────────────────────────────────────────────────────────

func TestStorageCapabilityFor(t *testing.T) {
	cases := []struct {
		op       OperationType
		expected string
	}{
		{OpTypeAddDevice, "add_device"},
		{OpTypeRemoveDevice, "remove_device"},
		{OpTypeReplaceDevice, "replace_device"},
		{OpTypeConvertProfile, "convert_profile"},
		{OpTypeStartScrub, "scrub"},
		{OpTypeCreateSnapshot, "snapshots"},
		{OpTypeDeleteSnapshot, "snapshots"},
		{OpTypeSetCompression, "compression"},
		// Sin capability:
		{OpTypeRenamePool, ""},
		{OpTypeChangeRole, ""},
		{OpTypeDestroyPool, ""},
	}
	for _, c := range cases {
		got := CapabilityFor(c.op)
		if got != c.expected {
			t.Errorf("CapabilityFor(%s): got %q, want %q", c.op, got, c.expected)
		}
	}
}
