// storage_executor_real_test.go — Tests unitarios del ejecutor real.
//
// Cubre las funciones puras (sin BTRFS) del RealBtrfsExecutor:
// parsing de paths, detección de partición padre, etc.
//
// Tests que requieren BTRFS real están en storage_integration_test.go
// con build tag `integration`.

package main

import (
	"fmt"
	"testing"
)

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
		{"", false}, // vacía no cuenta como "todo dígitos"
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

// ─────────────────────────────────────────────────────────────────────────────
// metadataProfileFor — la metadata nunca con menos redundancia que el pool
// ─────────────────────────────────────────────────────────────────────────────

func TestMetadataProfileFor(t *testing.T) {
	cases := []struct {
		data    Profile
		devices int
		want    string
	}{
		// El bug: convertir a single con varios discos dejaba metadata single.
		{ProfileSingle, 2, "raid1"},
		{ProfileSingle, 5, "raid1"},
		{ProfileSingle, 1, "dup"},
		{ProfileRaid1, 2, "raid1"},
		{ProfileRaid1c3, 3, "raid1c3"},
		{ProfileRaid10, 4, "raid10"},
	}
	for _, c := range cases {
		if got := metadataProfileFor(c.data, c.devices); got != c.want {
			t.Errorf("metadataProfileFor(%s, %d) = %q, want %q", c.data, c.devices, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseBtrfsDeviceStatsOutput — claves = nombres de CollectDiagnostics
// ─────────────────────────────────────────────────────────────────────────────

func statsBlock(dev string, write, read, corruption int) string {
	return fmt.Sprintf("[%[1]s].write_io_errs    %[2]d\n[%[1]s].read_io_errs     %[3]d\n"+
		"[%[1]s].flush_io_errs    0\n[%[1]s].corruption_errs  %[4]d\n[%[1]s].generation_errs  0\n",
		dev, write, read, corruption)
}

func TestParseBtrfsDeviceStatsOutput_KeysMatchDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want map[string]DiskStatus
	}{
		{
			// El bug: nvme0n1 → "nvme0n", y CollectDiagnostics busca "nvme0n1".
			"nvme", statsBlock("/dev/nvme0n1", 0, 0, 3) + statsBlock("/dev/nvme1n1", 0, 2, 0),
			map[string]DiskStatus{
				"nvme0n1": {ChecksumErrors: 3},
				"nvme1n1": {ReadErrors: 2},
			},
		},
		{
			"sata", statsBlock("/dev/sda", 1, 0, 0) + statsBlock("/dev/sdb", 0, 0, 0),
			map[string]DiskStatus{"sda": {WriteErrors: 1}, "sdb": {}},
		},
		{
			// loop49 y loop50 colapsaban en "loop": el segundo pisaba al primero.
			"loop", statsBlock("/dev/loop49", 0, 0, 1) + statsBlock("/dev/loop50", 0, 0, 0),
			map[string]DiskStatus{"loop49": {ChecksumErrors: 1}, "loop50": {}},
		},
		{
			"dm", statsBlock("/dev/dm-0", 0, 0, 5) + statsBlock("/dev/dm-1", 0, 0, 0),
			map[string]DiskStatus{"dm-0": {ChecksumErrors: 5}, "dm-1": {}},
		},
		{
			// Dos particiones del mismo disco: se suman, no se pisan.
			"particiones", statsBlock("/dev/sdc1", 0, 0, 2) + statsBlock("/dev/sdc2", 0, 0, 1) +
				statsBlock("/dev/nvme2n1p3", 4, 0, 0),
			map[string]DiskStatus{"sdc": {ChecksumErrors: 3}, "nvme2n1": {WriteErrors: 4}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseBtrfsDeviceStatsOutput(c.out)
			if len(got) != len(c.want) {
				t.Fatalf("claves: got %v, want %v", got, c.want)
			}
			for k, w := range c.want {
				if g, ok := got[k]; !ok || g != w {
					t.Errorf("%s: got %+v (presente=%v), want %+v", k, g, ok, w)
				}
			}
		})
	}
}
