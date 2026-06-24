package main

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// STOR-01-A · Tests de detección de drift de layout
// Cubre validación (detecta drift real) y errores (entradas anómalas que NO
// deben producir falsos positivos).
// ─────────────────────────────────────────────────────────────────────────────

func TestLayoutHasDrifted_Validation(t *testing.T) {
	cases := []struct {
		name       string
		expected   string
		real       string
		wantDrift  bool
	}{
		// ── Validación: drift real detectado ──
		{"raid1 a raid10 (balance interrumpido)", "raid1", "raid10", true},
		{"single a raid1", "single", "raid1", true},
		{"raid1 a raid1c3", "raid1", "raid1c3", true},
		{"raid10 a single (shrink interrumpido)", "raid10", "single", true},

		// ── Sin drift: coinciden ──
		{"raid1 == raid1", "raid1", "raid1", false},
		{"single == single", "single", "single", false},
		{"case-insensitive RAID1 == raid1", "raid1", "RAID1", false},
		{"con espacios", "raid1", "  raid1  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := layoutHasDrifted(c.expected, c.real)
			if got != c.wantDrift {
				t.Errorf("layoutHasDrifted(%q,%q) = %v (%q), want %v",
					c.expected, c.real, got, reason, c.wantDrift)
			}
		})
	}
}

func TestLayoutHasDrifted_ErrorsNoFalsePositive(t *testing.T) {
	// CRÍTICO: estos casos anómalos NUNCA deben marcar drift (falso positivo).
	// Un falso positivo marcaría un pool sano en recovery sin razón.
	cases := []struct {
		name     string
		expected string
		real     string
	}{
		{"real vacío (comando btrfs falló)", "raid1", ""},
		{"real solo espacios", "raid1", "   "},
		{"expected vacío (pool sin profile en BD)", "", "raid1"},
		{"ambos vacíos", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := layoutHasDrifted(c.expected, c.real)
			if got {
				t.Errorf("layoutHasDrifted(%q,%q) = true (%q), want false — FALSO POSITIVO peligroso",
					c.expected, c.real, reason)
			}
		})
	}
}

func TestReadRealDataProfile_ParsesDataLine(t *testing.T) {
	// Verifica que parseProfileFromDfLine (reusada por readRealDataProfile)
	// extrae el profile de la línea Data correctamente.
	cases := map[string]string{
		"Data, RAID1: total=1.00GiB, used=408.00KiB":    "raid1",
		"Data, single: total=1.00GiB, used=408.00KiB":   "single",
		"Data, RAID10: total=2.00GiB, used=1.00GiB":     "raid10",
		"Metadata, RAID1: total=1.00GiB, used=128.00KiB": "raid1", // también parsea, aunque readReal solo mira Data
	}
	for line, want := range cases {
		if got := parseProfileFromDfLine(line); got != want {
			t.Errorf("parseProfileFromDfLine(%q) = %q, want %q", line, got, want)
		}
	}
}
