package main

import "testing"

func TestParseReplaceProgress(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantPct     float64
		wantRunning bool
	}{
		{"en curso 3.2%", "3.2% done, 0 write errs, 0 uncorr. read errs", 3.2, true},
		{"en curso 47%", "47.0% done, 0 write errs, 0 uncorr. read errs", 47.0, true},
		{"en curso 0%", "0.0% done, 0 write errs, 0 uncorr. read errs", 0.0, true},
		{"terminado", "Started on 1.1.2026, finished on 1.1.2026", 100, false},
		{"nunca iniciado", "Never started", 0, false},
		{"vacío", "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pct, running := parseReplaceProgress(c.out)
			if running != c.wantRunning {
				t.Errorf("running: got %v, want %v", running, c.wantRunning)
			}
			if running && pct != c.wantPct {
				t.Errorf("pct: got %v, want %v", pct, c.wantPct)
			}
		})
	}
}
