package main

import "testing"

// assertPoolWritable es la puerta de seguridad antes de escribir en un pool.
// Estos tests fijan el contrato: ante cualquier estado inseguro, devuelve error
// explicativo; solo deja pasar si el pool está montado Y en rw.

func TestAssertPoolWritable_EmptyPath(t *testing.T) {
	err := assertPoolWritableWith("", defaultPoolWritableChecks)
	if err == nil {
		t.Fatal("ruta vacía debe fallar")
	}
	if pe, ok := err.(*PoolWritableError); !ok || pe.Code != "path_invalid" {
		t.Errorf("código esperado path_invalid, got %v", err)
	}
}

func TestAssertPoolWritable_NotUnderPools(t *testing.T) {
	err := assertPoolWritableWith("/etc/passwd", defaultPoolWritableChecks)
	if err == nil {
		t.Fatal("ruta fuera de /nimos/pools debe fallar")
	}
	if pe, _ := err.(*PoolWritableError); pe == nil || pe.Code != "not_a_pool" {
		t.Errorf("código esperado not_a_pool, got %v", err)
	}
}

func TestAssertPoolWritable_NotMounted(t *testing.T) {
	// EL CASO CRÍTICO: pool no montado → escribir caería en el disco de sistema.
	checks := poolWritableChecks{
		mountedPool: func(string) bool { return false }, // NO montado
		readOnly:    func(string) bool { return false },
	}
	err := assertPoolWritableWith("/nimos/pools/data8/shares/foo", checks)
	if err == nil {
		t.Fatal("pool no montado debe fallar (evita escritura al disco de sistema)")
	}
	if pe, _ := err.(*PoolWritableError); pe == nil || pe.Code != "mount_missing" {
		t.Errorf("código esperado mount_missing, got %v", err)
	}
}

func TestAssertPoolWritable_ReadOnly(t *testing.T) {
	// Pool montado pero read-only (btrfs detectó daño) → no escribible.
	checks := poolWritableChecks{
		mountedPool: func(string) bool { return true }, // montado
		readOnly:    func(string) bool { return true }, // pero RO
	}
	err := assertPoolWritableWith("/nimos/pools/data8/shares/foo", checks)
	if err == nil {
		t.Fatal("pool read-only debe fallar")
	}
	if pe, _ := err.(*PoolWritableError); pe == nil || pe.Code != "read_only" {
		t.Errorf("código esperado read_only, got %v", err)
	}
}

func TestAssertPoolWritable_Healthy(t *testing.T) {
	// Pool montado y rw → seguro para escribir.
	checks := poolWritableChecks{
		mountedPool: func(string) bool { return true },
		readOnly:    func(string) bool { return false },
	}
	err := assertPoolWritableWith("/nimos/pools/data8/shares/foo", checks)
	if err != nil {
		t.Errorf("pool sano (montado+rw) debe ser escribible, got %v", err)
	}
}
