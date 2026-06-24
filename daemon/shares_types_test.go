// shares_types_test.go
//
// Tests de los helpers puros del módulo Shares:
//   · parseHumanBytes — convierte "1.5GiB", "500MB" → bytes
//   · classifyExt    — clasifica extensión en categoría
//   · emptyCategoryStats — devuelve map con shape estable
//
// No requieren SQLite, BTRFS ni HTTP. Funciones puras → tests rápidos.

package main

import (
	"testing"
)

// ─── parseHumanBytes ────────────────────────────────────────────────────

func TestParseHumanBytes_EmptyAndSpecial(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"", 0},
		{"-", 0},
		{"none", 0},
		{"  ", 0},
	}
	for _, c := range cases {
		got := parseHumanBytes(c.input)
		if got != c.want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseHumanBytes_RawNumber(t *testing.T) {
	// Sin unidad = bytes raw
	got := parseHumanBytes("1024")
	if got != 1024 {
		t.Errorf("parseHumanBytes(\"1024\") = %d, want 1024", got)
	}
}

func TestParseHumanBytes_AllUnits(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"1K", 1024},
		{"1KB", 1024},
		{"1KiB", 1024},
		{"1M", 1024 * 1024},
		{"1MB", 1024 * 1024},
		{"1MiB", 1024 * 1024},
		{"1G", 1024 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"1GiB", 1024 * 1024 * 1024},
		{"1T", 1024 * 1024 * 1024 * 1024},
		{"1TB", 1024 * 1024 * 1024 * 1024},
		{"1TiB", 1024 * 1024 * 1024 * 1024},
	}
	for _, c := range cases {
		got := parseHumanBytes(c.input)
		if got != c.want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseHumanBytes_Decimals(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"1.5GiB", int64(1.5 * 1024 * 1024 * 1024)},
		{"0.5MB", int64(0.5 * 1024 * 1024)},
		{"2.5KiB", int64(2.5 * 1024)},
	}
	for _, c := range cases {
		got := parseHumanBytes(c.input)
		if got != c.want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseHumanBytes_LocaleComma(t *testing.T) {
	// Algunos locales usan coma como separador decimal
	got := parseHumanBytes("1,5GiB")
	want := int64(1.5 * 1024 * 1024 * 1024)
	if got != want {
		t.Errorf("parseHumanBytes(\"1,5GiB\") = %d, want %d (locale comma)", got, want)
	}
}

func TestParseHumanBytes_Whitespace(t *testing.T) {
	// Tolerar espacios: btrfs output puede tener "1.50GiB " o "  1G"
	cases := []struct {
		input string
		want  int64
	}{
		{"  1024", 1024},
		{"1024  ", 1024},
		{"  1GiB  ", 1024 * 1024 * 1024},
	}
	for _, c := range cases {
		got := parseHumanBytes(c.input)
		if got != c.want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

// ─── classifyExt ────────────────────────────────────────────────────────

func TestClassifyExt_Categories(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		// Video
		{".mp4", "video"},
		{".mkv", "video"},
		{".avi", "video"},
		// Image
		{".jpg", "image"},
		{".png", "image"},
		{".webp", "image"},
		// Music
		{".mp3", "music"},
		{".flac", "music"},
		// Document
		{".pdf", "document"},
		{".docx", "document"},
		{".md", "document"},
		// Archive
		{".zip", "archive"},
		{".tar", "archive"},
		// Code
		{".go", "code"},
		{".py", "code"},
		{".svelte", "code"},
		// Other (desconocidas)
		{".xyz", "other"},
		{"", "other"},
		{".unknown", "other"},
	}
	for _, c := range cases {
		got := classifyExt(c.ext)
		if got != c.want {
			t.Errorf("classifyExt(%q) = %q, want %q", c.ext, got, c.want)
		}
	}
}

func TestClassifyExt_CaseInsensitive(t *testing.T) {
	// Linux es case-sensitive en filenames, pero clasificación NO debería serlo
	cases := []struct {
		ext  string
		want string
	}{
		{".MP4", "video"},
		{".JPG", "image"},
		{".PDF", "document"},
		{".Go", "code"},
	}
	for _, c := range cases {
		got := classifyExt(c.ext)
		if got != c.want {
			t.Errorf("classifyExt(%q) = %q, want %q (case insensitive)", c.ext, got, c.want)
		}
	}
}

// ─── emptyCategoryStats ─────────────────────────────────────────────────

func TestEmptyCategoryStats_ShapeStable(t *testing.T) {
	// Garantiza que la respuesta TIENE TODAS las categorías,
	// aunque sea con valor 0. Frontend depende del shape estable.
	stats := emptyCategoryStats()

	expectedKeys := []string{"video", "image", "document", "music", "archive", "code", "other"}
	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("emptyCategoryStats missing key %q", key)
		}
	}

	if len(stats) != len(expectedKeys) {
		t.Errorf("emptyCategoryStats has %d keys, want %d", len(stats), len(expectedKeys))
	}
}

func TestEmptyCategoryStats_AllZero(t *testing.T) {
	stats := emptyCategoryStats()
	for k, v := range stats {
		if v != 0 {
			t.Errorf("emptyCategoryStats[%q] = %d, want 0", k, v)
		}
	}
}

// ─── Extension lists are not nil ────────────────────────────────────────

func TestExtensionLists_NonEmpty(t *testing.T) {
	// Regresión: si alguien borra las listas accidentalmente, los tests fallan
	if len(videoExts) == 0 {
		t.Error("videoExts is empty")
	}
	if len(imageExts) == 0 {
		t.Error("imageExts is empty")
	}
	if len(musicExts) == 0 {
		t.Error("musicExts is empty")
	}
	if len(documentExts) == 0 {
		t.Error("documentExts is empty")
	}
	if len(archiveExts) == 0 {
		t.Error("archiveExts is empty")
	}
	if len(codeExts) == 0 {
		t.Error("codeExts is empty")
	}
}
