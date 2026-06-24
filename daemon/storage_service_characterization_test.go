package main

import "testing"

// ═══════════════════════════════════════════════════════════════════════
// CARACTERIZACIÓN · storage_service.go · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Captura el comportamiento actual de las funciones puras de storage_service
// ANTES de modularizar. Red de seguridad para la división en
// storage_service_*.go. (CreatePoolRequest.Validate ya tiene sus propios tests
// en storage_create_pool_validate_test.go.)
// ═══════════════════════════════════════════════════════════════════════

func TestDefaultIfEmpty_Characterization(t *testing.T) {
	if got := defaultIfEmpty("", "fb"); got != "fb" {
		t.Errorf(`defaultIfEmpty("","fb") = %q, want "fb"`, got)
	}
	if got := defaultIfEmpty("val", "fb"); got != "val" {
		t.Errorf(`defaultIfEmpty("val","fb") = %q, want "val"`, got)
	}
}

func TestServiceError_Characterization(t *testing.T) {
	e := &ServiceError{Code: "ERR_X", Msg: "algo falló"}
	if got := e.Error(); got != "ERR_X: algo falló" {
		t.Errorf("ServiceError.Error() = %q, want %q", got, "ERR_X: algo falló")
	}
	// errFromCode produce el mismo formato.
	err := errFromCode("ERR_Y", "otro")
	if err.Error() != "ERR_Y: otro" {
		t.Errorf("errFromCode.Error() = %q", err.Error())
	}
	// errFromCodeWithDetails conserva code/msg en Error() (details no aparece).
	err2 := errFromCodeWithDetails("ERR_Z", "con detalles", map[string]int{"x": 1})
	if err2.Error() != "ERR_Z: con detalles" {
		t.Errorf("errFromCodeWithDetails.Error() = %q", err2.Error())
	}
	se, ok := err2.(*ServiceError)
	if !ok || se.Details == nil {
		t.Error("errFromCodeWithDetails debería conservar Details")
	}
}

func TestResolveDevicePath_Characterization(t *testing.T) {
	// nil → "".
	if got := resolveDevicePath(nil); got != "" {
		t.Errorf("resolveDevicePath(nil) = %q, want empty", got)
	}
	// Device con ambos paths vacíos → "".
	if got := resolveDevicePath(&Device{}); got != "" {
		t.Errorf("resolveDevicePath(empty device) = %q, want empty", got)
	}
	// Paths que no existen en el filesystem → "" (devicePathExists false).
	d := &Device{ByIDPath: "/dev/disk/by-id/nonexistent-xyz", CurrentPath: "/dev/nonexistent-xyz"}
	if got := resolveDevicePath(d); got != "" {
		t.Errorf("resolveDevicePath(paths inexistentes) = %q, want empty", got)
	}
}
