// storage_executor_real_test.go — Tests unitarios del ejecutor real.
//
// Cubre las funciones puras (sin BTRFS) del RealBtrfsExecutor:
// parsing de paths, detección de partición padre, etc.
//
// Tests que requieren BTRFS real están en storage_integration_test.go
// con build tag `integration`.

package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// parentDeviceOf — núcleo del fix del boot disk guard
// ─────────────────────────────────────────────────────────────────────────────

func TestParentDeviceOfSDPartitions(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		// sd*: tradicional, dígitos al final = partición
		{"/dev/sda1", "/dev/sda"},
		{"/dev/sda10", "/dev/sda"},
		{"/dev/sdb2", "/dev/sdb"},
		{"/dev/sdz9", "/dev/sdz"},
		// sd* sin partición (disco entero): devuelve el mismo path
		{"/dev/sda", "/dev/sda"},
		{"/dev/sdb", "/dev/sdb"},
	}
	for _, c := range cases {
		got := parentDeviceOf(c.input)
		if got != c.expected {
			t.Errorf("parentDeviceOf(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestParentDeviceOfVDPartitions(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"/dev/vda1", "/dev/vda"},
		{"/dev/vda", "/dev/vda"},
		{"/dev/hda1", "/dev/hda"},
	}
	for _, c := range cases {
		got := parentDeviceOf(c.input)
		if got != c.expected {
			t.Errorf("parentDeviceOf(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestParentDeviceOfNVMePartitions(t *testing.T) {
	// El caso que el test de integración pilló: NVMe usa <disk>p<N>
	// donde <disk> también contiene dígitos (nvme0n1, nvme1n1, ...)
	cases := []struct {
		input    string
		expected string
	}{
		// NVMe con partición: pN al final del path
		{"/dev/nvme0n1p1", "/dev/nvme0n1"},
		{"/dev/nvme0n1p2", "/dev/nvme0n1"}, // el caso real de Andrés
		{"/dev/nvme1n1p3", "/dev/nvme1n1"},
		{"/dev/nvme0n1p15", "/dev/nvme0n1"},
		// NVMe disco entero (sin partición)
		{"/dev/nvme0n1", "/dev/nvme0n1"},
		{"/dev/nvme1n1", "/dev/nvme1n1"},
	}
	for _, c := range cases {
		got := parentDeviceOf(c.input)
		if got != c.expected {
			t.Errorf("parentDeviceOf(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestParentDeviceOfMMC(t *testing.T) {
	// MMC sigue la misma convención que NVMe (mmcblk0p1 → mmcblk0)
	cases := []struct {
		input    string
		expected string
	}{
		{"/dev/mmcblk0p1", "/dev/mmcblk0"},
		{"/dev/mmcblk0", "/dev/mmcblk0"},
	}
	for _, c := range cases {
		got := parentDeviceOf(c.input)
		if got != c.expected {
			t.Errorf("parentDeviceOf(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestParentDeviceOfRegressionTrimRightBug(t *testing.T) {
	// Test de regresión específico: antes del fix, parentDeviceOf hacía
	// strings.TrimRight(base, "0123456789") universalmente, lo cual
	// convertía nvme0n1p2 → nvme0n1p (mal). Verificamos que NO ocurre.
	got := parentDeviceOf("/dev/nvme0n1p2")
	if got == "/dev/nvme0n1p" {
		t.Errorf("REGRESSION: parentDeviceOf reverted to naive TrimRight (got %q)", got)
	}
	if got != "/dev/nvme0n1" {
		t.Errorf("parentDeviceOf returned wrong value: got %q, want /dev/nvme0n1", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// allDigits — helper interno
// ─────────────────────────────────────────────────────────────────────────────

func TestAllDigits(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"0", true},
		{"99999", true},
		{"", false},        // vacía no cuenta como "todo dígitos"
		{"12a", false},
		{"a12", false},
		{"1.2", false},
		{" 12", false},
	}
	for _, c := range cases {
		got := allDigits(c.input)
		if got != c.expected {
			t.Errorf("allDigits(%q) = %v, want %v", c.input, got, c.expected)
		}
	}
}
