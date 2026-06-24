// apps_launchable_test.go — Tests de la composición de URLs lanzables.

package main

import "testing"

func TestBuildExternalURL(t *testing.T) {
	cases := []struct {
		sub, base   string
		port        int
		landing     string
		want        string
	}{
		// Pi-hole con puerto 444 y /admin
		{"pihole", "nimosbarraca1.duckdns.org", 444, "/admin",
			"https://pihole.nimosbarraca1.duckdns.org:444/admin"},
		// Sin landing path
		{"jellyfin", "midominio.org", 444, "",
			"https://jellyfin.midominio.org:444"},
		// Puerto 443 se omite
		{"app", "dom.org", 443, "/x",
			"https://app.dom.org/x"},
		// Puerto 0 se omite
		{"app", "dom.org", 0, "",
			"https://app.dom.org"},
		// Sin subdominio → vacío
		{"", "dom.org", 444, "/x", ""},
		// Sin baseDomain → vacío
		{"app", "", 444, "/x", ""},
	}
	for _, c := range cases {
		got := buildExternalURL(c.sub, c.base, c.port, c.landing)
		if got != c.want {
			t.Errorf("buildExternalURL(%q,%q,%d,%q): got %q, want %q",
				c.sub, c.base, c.port, c.landing, got, c.want)
		}
	}
}

func TestBuildLaunchableApps(t *testing.T) {
	apps := []*DBDockerApp{
		{ID: "pihole", Name: "Pi-hole", Port: 8088, Config: `{"landing_path":"/admin"}`},
		{ID: "jellyfin", Name: "Jellyfin", Port: 8096, Config: `{}`},
		{ID: "local-only", Name: "Solo Local", Port: 9000, Config: `{}`},
	}
	exposedByID := map[string]*NetworkExposedApp{
		"pihole":   {AppID: "pihole", Subdomain: "pihole"},
		"jellyfin": {AppID: "jellyfin", Subdomain: "jellyfin"},
		// local-only NO está expuesta
	}
	result := buildLaunchableApps(apps, exposedByID, "nimosbarraca1.duckdns.org", 444)

	// pihole · expuesta, con landing_path
	if result[0].LocalPort != 8088 {
		t.Errorf("pihole local_port: got %d", result[0].LocalPort)
	}
	if result[0].LandingPath != "/admin" {
		t.Errorf("pihole landing_path: got %q", result[0].LandingPath)
	}
	if !result[0].Exposed {
		t.Error("pihole debería estar exposed")
	}
	if result[0].OpenURLExternal != "https://pihole.nimosbarraca1.duckdns.org:444/admin" {
		t.Errorf("pihole external: got %q", result[0].OpenURLExternal)
	}

	// jellyfin · expuesta, SIN landing_path
	if result[1].OpenURLExternal != "https://jellyfin.nimosbarraca1.duckdns.org:444" {
		t.Errorf("jellyfin external: got %q", result[1].OpenURLExternal)
	}
	if result[1].LandingPath != "" {
		t.Errorf("jellyfin landing_path debería ser vacío: got %q", result[1].LandingPath)
	}

	// local-only · NO expuesta
	if result[2].Exposed {
		t.Error("local-only NO debería estar exposed")
	}
	if result[2].OpenURLExternal != "" {
		t.Errorf("local-only external debería ser vacío: got %q", result[2].OpenURLExternal)
	}
	if result[2].LocalPort != 9000 {
		t.Errorf("local-only local_port: got %d", result[2].LocalPort)
	}
}
