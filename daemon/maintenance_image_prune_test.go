package main

import "testing"

func TestParseDockerSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0B", 0},
		{"500B", 500},
		{"1.5kB", 1500},
		{"1.234GB", 1234000000},
		{"500MB", 500000000},
		{"2TB", 2000000000000},
		{"  1.5GB  ", 1500000000},
		{"basura", 0},
		{"", 0},
		{"100", 0}, // sin unidad → no parseable
	}
	for _, c := range cases {
		if got := parseDockerSize(c.in); got != c.want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseDockerPruneOutput(t *testing.T) {
	out := `Deleted Images:
deleted: sha256:aaaaaaaaaaaa
deleted: sha256:bbbbbbbbbbbb
untagged: foo:latest

Total reclaimed space: 1.5GB`
	deleted, bytes := parseDockerPruneOutput(out)
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if bytes != 1500000000 {
		t.Errorf("bytes = %d, want 1500000000", bytes)
	}

	// Nada que borrar.
	none := "Total reclaimed space: 0B"
	d2, b2 := parseDockerPruneOutput(none)
	if d2 != 0 || b2 != 0 {
		t.Errorf("vacío: got (%d,%d), want (0,0)", d2, b2)
	}

	// Salida vacía total.
	d3, b3 := parseDockerPruneOutput("")
	if d3 != 0 || b3 != 0 {
		t.Errorf("output vacío: got (%d,%d), want (0,0)", d3, b3)
	}
}
