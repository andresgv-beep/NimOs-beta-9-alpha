package main

import "testing"

func TestInterpretSubvolumeShow(t *testing.T) {
	cases := []struct {
		name string
		out  string
		ok   bool
		want bool
	}{
		{"subvolumen real (ok + info)", "containers/gitea\n\tName: gitea\n\tUUID: ...", true, true},
		{"dir plano (show falla)", "ERROR: not a subvolume", false, false},
		{"path inexistente (falla)", "", false, false},
		{"ok pero salida vacía (raro)", "   ", true, false},
		{"ok con info mínima", "x", true, true},
	}
	for _, c := range cases {
		if got := interpretSubvolumeShow(c.out, c.ok); got != c.want {
			t.Errorf("%s: interpretSubvolumeShow(%q,%v) = %v, want %v", c.name, c.out, c.ok, got, c.want)
		}
	}
}
