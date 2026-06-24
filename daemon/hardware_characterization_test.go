package main

import "testing"

// ═══════════════════════════════════════════════════════════════════════
// CARACTERIZACIÓN · hardware.go · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Estos tests capturan el comportamiento ACTUAL de las funciones puras de
// hardware.go ANTES de modularizarlo. Sirven de red de seguridad: tras mover
// las funciones a hardware_*.go, deben seguir pasando idénticos. Si alguno
// falla tras la modularización, es que algo se movió mal.
//
// Solo se testean funciones PURAS (sin acceso a hardware real). Las que leen
// /proc, nvidia-smi o smartctl no son testeables en sandbox y se validan en
// hardware real.
// ═══════════════════════════════════════════════════════════════════════

func TestFormatBytes_Characterization(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512.0 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
		{5368709120, "5.0 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseInt64_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"123", 123},
		{"  456  ", 456}, // hace TrimSpace
		{"-789", -789},
		{"", 0},     // error → 0
		{"abc", 0},  // error → 0
		{"12.5", 0}, // no es int → 0
	}
	for _, c := range cases {
		if got := parseInt64(c.in); got != c.want {
			t.Errorf("parseInt64(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseIntDefault_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"123", 99, 123},
		{"  42 ", 99, 42},
		{"", 99, 99},  // error → default
		{"abc", 7, 7}, // error → default
		{"-5", 0, -5},
	}
	for _, c := range cases {
		if got := parseIntDefault(c.in, c.def); got != c.want {
			t.Errorf("parseIntDefault(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}

func TestParseFloat_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"3.14", 3.14},
		{"  2.5 ", 2.5},
		{"100", 100.0},
		{"", 0},    // error → 0
		{"abc", 0}, // error → 0
	}
	for _, c := range cases {
		if got := parseFloat(c.in); got != c.want {
			t.Errorf("parseFloat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseRawSmartValue_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"100", 100},
		{"36 (Min/Max 20/45)", 36}, // dígitos iniciales, corta en el espacio
		{"0", 0},
		{"abc", 0},      // sin dígitos iniciales → 0
		{"123abc", 123}, // corta al primer no-dígito
		{"45 Celsius", 45},
	}
	for _, c := range cases {
		if got := parseRawSmartValue(c.in); got != c.want {
			t.Errorf("parseRawSmartValue(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNextTempState_Characterization(t *testing.T) {
	// tempHighC=55, tempRecoverC=50, con histéresis.
	cases := []struct {
		prev string
		temp int
		want string
	}{
		{"", 40, "normal"},       // frío, sin previo → normal
		{"", 60, "high"},         // caliente → high
		{"normal", 56, "high"},   // cruza hacia arriba → high
		{"high", 52, "high"},     // en zona de histéresis (50-55), mantiene high
		{"high", 49, "normal"},   // baja de recover → normal
		{"normal", 52, "normal"}, // en histéresis viniendo de normal → normal
		{"", 52, "normal"},       // histéresis sin previo → normal
	}
	for _, c := range cases {
		if got := nextTempState(c.prev, c.temp); got != c.want {
			t.Errorf("nextTempState(%q, %d) = %q, want %q", c.prev, c.temp, got, c.want)
		}
	}
}

func TestPickMainTemp_Characterization(t *testing.T) {
	// Orden de prioridad: x86_pkg_temp, cpu-thermal, cpu, coretemp.
	if got := pickMainTemp(map[string]interface{}{"x86_pkg_temp": 45, "cpu": 50}); got != 45 {
		t.Errorf("prioridad x86_pkg_temp: got %v, want 45", got)
	}
	if got := pickMainTemp(map[string]interface{}{"cpu-thermal": 60, "cpu": 50}); got != 60 {
		t.Errorf("prioridad cpu-thermal: got %v, want 60", got)
	}
	if got := pickMainTemp(map[string]interface{}{"cpu": 55}); got != 55 {
		t.Errorf("fallback cpu: got %v, want 55", got)
	}
	if got := pickMainTemp(map[string]interface{}{}); got != nil {
		t.Errorf("mapa vacío: got %v, want nil", got)
	}
	// Un solo elemento no prioritario → lo devuelve (fallback range).
	if got := pickMainTemp(map[string]interface{}{"otra": 33}); got != 33 {
		t.Errorf("fallback range: got %v, want 33", got)
	}
}

func TestIsPhysicalInterface_Characterization(t *testing.T) {
	// Interfaces virtuales que SIEMPRE deben rechazarse.
	virtual := []string{"lo", "docker0", "br-abc123", "veth1234", "virbr0", "tun0", "tap0"}
	for _, dev := range virtual {
		if isPhysicalInterface(dev) {
			t.Errorf("isPhysicalInterface(%q) = true, want false (virtual)", dev)
		}
	}
	// Nombres físicos por patrón (no dependen de /sys, hay fallback por prefijo).
	physical := []string{"eth0", "enp3s0", "eno1", "ens33", "wlan0", "wl0"}
	for _, dev := range physical {
		if !isPhysicalInterface(dev) {
			t.Errorf("isPhysicalInterface(%q) = false, want true (físico)", dev)
		}
	}
}

func TestSmartOutputIsUsable_Characterization(t *testing.T) {
	// Vacío → no usable.
	if smartOutputIsUsable("") {
		t.Error("vacío debería ser no usable")
	}
	// Con tabla de atributos → usable.
	if !smartOutputIsUsable("foo ATTRIBUTE_NAME bar") {
		t.Error("con ATTRIBUTE_NAME debería ser usable")
	}
	// Con veredicto de salud → usable.
	if !smartOutputIsUsable("SMART overall-health self-assessment test result: PASSED") {
		t.Error("con veredicto de salud debería ser usable")
	}
	// Solo pista SAT, sin datos reales → no usable.
	if smartOutputIsUsable("Please specify device type with the -d option. Try an additional ...") {
		t.Error("solo pista SAT sin datos debería ser no usable")
	}
	// Pista SAT PERO con tabla real → usable (datos parciales válidos).
	if !smartOutputIsUsable("behind a SAT layer ... ATTRIBUTE_NAME ...") {
		t.Error("pista SAT con ATTRIBUTE_NAME debería ser usable")
	}
}
