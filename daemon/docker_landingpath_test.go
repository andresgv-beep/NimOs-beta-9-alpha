// docker_landingpath_test.go — Tests de persistencia del landing_path en config.

package main

import "testing"

func TestBuildAppConfigJSON(t *testing.T) {
	// Con landing_path → lo incluye.
	got := buildAppConfigJSON("/admin")
	if got != `{"landing_path":"/admin"}` {
		t.Errorf("con landing_path: got %q", got)
	}
	// Sin landing_path → JSON vacío (no mete clave vacía).
	got = buildAppConfigJSON("")
	if got != `{}` {
		t.Errorf("sin landing_path: got %q", got)
	}
}

func TestLandingPathFromConfig(t *testing.T) {
	cases := []struct {
		config string
		want   string
	}{
		{`{"landing_path":"/admin"}`, "/admin"},
		{`{}`, ""},
		{``, ""},
		{`{"otra_cosa":"x"}`, ""},
		{`{"landing_path":"/dashboard","permissions":[]}`, "/dashboard"},
		{`no es json`, ""},
	}
	for _, c := range cases {
		got := landingPathFromConfig(c.config)
		if got != c.want {
			t.Errorf("landingPathFromConfig(%q): got %q, want %q", c.config, got, c.want)
		}
	}
}

// Round-trip: build → from devuelve lo mismo.
func TestLandingPath_RoundTrip(t *testing.T) {
	for _, lp := range []string{"/admin", "/dashboard", ""} {
		cfg := buildAppConfigJSON(lp)
		got := landingPathFromConfig(cfg)
		if got != lp {
			t.Errorf("round-trip %q → %q → %q", lp, cfg, got)
		}
	}
}
