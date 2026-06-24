package main

import "testing"

// ═══════════════════════════════════════════════════════════════════════
// CARACTERIZACIÓN · files.go · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Captura el comportamiento ACTUAL de las funciones puras de files.go ANTES de
// modularizar. Red de seguridad: deben pasar idénticos tras mover las funciones
// a files_*.go. Solo funciones puras (las que tocan filesystem/HTTP se validan
// en hardware).
// ═══════════════════════════════════════════════════════════════════════

func TestSanitizeFileName_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"foo.txt", "foo.txt"},
		{"", ""},
		{".", ""},
		{"..", ""},
		{"/etc/passwd", "passwd"},  // filepath.Base
		{"../../secret", "secret"}, // Base de "../../secret" = "secret"
		{`a/b\c:d*e?f"g<h>i|j`, "b_c_d_e_f_g_h_i_j"}, // Base en Linux no parte por \; peligrosos→_
		{"file\x00name.txt", "filename.txt"},         // null byte fuera
		{"normal_file-123.md", "normal_file-123.md"},
	}
	for _, c := range cases {
		got := sanitizeFileName(c.in)
		if got != c.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeFileName_DangerousChars(t *testing.T) {
	// Un nombre sin separadores de ruta pero con caracteres peligrosos:
	// se reemplazan por "_". (filepath.Base no los toca porque no hay "/").
	got := sanitizeFileName("bad:name*?.txt")
	want := "bad_name__.txt"
	if got != want {
		t.Errorf("sanitizeFileName(dangerous) = %q, want %q", got, want)
	}
}

func TestParseHumanBytesFiles_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"-", 0},
		{"none", 0},
		{"1024B", 1024},
		{"1KiB", 1024},
		{"1MiB", 1024 * 1024},
		{"1GiB", 1024 * 1024 * 1024},
		{"1TiB", 1024 * 1024 * 1024 * 1024},
		{"1.5GiB", int64(1.5 * 1024 * 1024 * 1024)},
		{"  2KiB  ", 2048},
		{"100", 100}, // sin sufijo, multiplier 1
	}
	for _, c := range cases {
		got := parseHumanBytesFiles(c.in)
		if got != c.want {
			t.Errorf("parseHumanBytesFiles(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFmtSizeFiles_Characterization(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 KB"},
		{500, "0 KB"},          // 500/1e3 = 0.5 → "%.0f" redondea a 0
		{1500, "2 KB"},         // 1500/1e3 = 1.5 → "%.0f" = 2
		{1000000, "1 MB"},      // 1e6 → "1 MB"
		{1500000, "2 MB"},      // 1.5e6 → "%.0f" = 2
		{1000000000, "1.0 GB"}, // 1e9 → "1.0 GB"
		{2500000000, "2.5 GB"},
	}
	for _, c := range cases {
		got := fmtSizeFiles(c.in)
		if got != c.want {
			t.Errorf("fmtSizeFiles(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashStr_Characterization(t *testing.T) {
	// hash polinómico h = h*31 + c. Verificamos valores concretos y estabilidad.
	cases := []struct {
		in   string
		want uint32
	}{
		{"", 0},
		{"a", 97},          // 'a' = 97
		{"ab", 97*31 + 98}, // 3105
		{"abc", (97*31+98)*31 + 99},
	}
	for _, c := range cases {
		got := hashStr(c.in)
		if got != c.want {
			t.Errorf("hashStr(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	// Determinista: misma entrada, mismo hash.
	if hashStr("nimos") != hashStr("nimos") {
		t.Error("hashStr no es determinista")
	}
}
