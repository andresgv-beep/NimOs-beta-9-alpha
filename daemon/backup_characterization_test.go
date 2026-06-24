package main

import (
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// CARACTERIZACIÓN · backup.go · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Captura el comportamiento ACTUAL de las funciones puras de backup.go ANTES
// de modularizar. Red de seguridad para la división en backup_*.go.
// ═══════════════════════════════════════════════════════════════════════

func TestParseInt_Backup_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		fb   int
		want int
	}{
		{"123", 0, 123},
		{"", 30, 30},       // vacío → fallback
		{"0", 5, 5},        // n==0 → fallback (¡ojo, "0" devuelve fallback!)
		{"abc", 7, 7},      // sin dígitos → 0 → fallback
		{"12a34", 0, 1234}, // ignora no-dígitos
		{"007", 1, 7},
	}
	for _, c := range cases {
		if got := parseInt(c.in, c.fb); got != c.want {
			t.Errorf("parseInt(%q,%d) = %d, want %d", c.in, c.fb, got, c.want)
		}
	}
}

func TestParseByteSize_Characterization(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"100", 100},
		{"1K", 1024},
		{"1KIB", 1024},
		{"2M", 2 * 1024 * 1024},
		{"1G", 1024 * 1024 * 1024},
		{"1T", 1024 * 1024 * 1024 * 1024},
		{"5GIB", 5 * 1024 * 1024 * 1024},
		{"1.5G", int64(15) * 1024 * 1024 * 1024}, // "1.5G"→dígitos 15 → 15*1G (el . se ignora)
	}
	for _, c := range cases {
		if got := parseByteSize(c.in); got != c.want {
			t.Errorf("parseByteSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseRetention_Characterization(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		in      string
		wantDur time.Duration
		wantCnt int
	}{
		{"", 30 * day, 0}, // default 30 días
		{"30d", 30 * day, 0},
		{"7d", 7 * day, 0},
		{"4w", 4 * 7 * day, 0},
		{"1m", 30 * day, 0},
		{"2m", 2 * 30 * day, 0},
	}
	for _, c := range cases {
		gotDur, gotCnt := parseRetention(c.in)
		if gotDur != c.wantDur || gotCnt != c.wantCnt {
			t.Errorf("parseRetention(%q) = (%v,%d), want (%v,%d)", c.in, gotDur, gotCnt, c.wantDur, c.wantCnt)
		}
	}
}

func TestExtractTimestamp_Characterization(t *testing.T) {
	// Nombre válido → parsea la fecha.
	got := extractTimestamp("nimbackup-20260622-153045")
	want, _ := time.Parse("20060102-150405", "20260622-153045")
	if !got.Equal(want) {
		t.Errorf("extractTimestamp válido = %v, want %v", got, want)
	}
	// Nombre sin patrón → zero time.
	if !extractTimestamp("otracosa").IsZero() {
		t.Error("extractTimestamp sin patrón debería ser zero time")
	}
	if !extractTimestamp("").IsZero() {
		t.Error("extractTimestamp vacío debería ser zero time")
	}
}

func TestSnapshotsToPrune_Characterization(t *testing.T) {
	names := []string{
		"nimbackup-20260101-000000",
		"nimbackup-20260301-000000",
		"nimbackup-20260201-000000",
	}
	// maxKeep=0 → no poda (count-based desactivado).
	if got := snapshotsToPrune(names, 0); got != nil {
		t.Errorf("maxKeep=0 debería devolver nil, got %v", got)
	}
	// maxKeep >= len → no poda.
	if got := snapshotsToPrune(names, 3); got != nil {
		t.Errorf("maxKeep>=len debería devolver nil, got %v", got)
	}
	// maxKeep=1 → poda los 2 más viejos (ene y feb), conserva mar.
	got := snapshotsToPrune(names, 1)
	if len(got) != 2 {
		t.Fatalf("maxKeep=1 debería podar 2, podó %d: %v", len(got), got)
	}
	// El más viejo (enero) debe estar entre los podados.
	if got[0] != "nimbackup-20260101-000000" {
		t.Errorf("debería podar el más viejo primero, got[0]=%q", got[0])
	}
}

func TestExtractPathSegment_Characterization(t *testing.T) {
	cases := []struct {
		path, prefix, suffix, want string
	}{
		{"/api/backup/devices/abc123/info", "/api/backup/devices/", "/info", "abc123"},
		{"/api/backup/devices/xyz/", "/api/backup/devices/", "", "xyz"},
		{"/prefix/middle/", "/prefix/", "", "middle"},
	}
	for _, c := range cases {
		if got := extractPathSegment(c.path, c.prefix, c.suffix); got != c.want {
			t.Errorf("extractPathSegment(%q,%q,%q) = %q, want %q", c.path, c.prefix, c.suffix, got, c.want)
		}
	}
}
