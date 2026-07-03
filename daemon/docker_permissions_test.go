package main

import "testing"

// TestDockerAppUserAllowed · la mitad docker.json del check de acceso del
// app proxy (APP-063). getDockerConfigGo es una var precisamente para poder
// inyectarla aquí sin tocar disco.
func TestDockerAppUserAllowed(t *testing.T) {
	orig := getDockerConfigGo
	defer func() { getDockerConfigGo = orig }()
	getDockerConfigGo = func() map[string]interface{} {
		return map[string]interface{}{
			"appPermissions": map[string]interface{}{
				"jellyfin": []interface{}{"maria", "pepe"},
				"plex":     []interface{}{},
			},
		}
	}

	cases := []struct {
		user, app string
		want      bool
	}{
		{"maria", "jellyfin", true},
		{"pepe", "jellyfin", true},
		{"intruso", "jellyfin", false}, // sin grant
		{"maria", "plex", false},       // lista vacía
		{"maria", "inexistente", false},
		{"", "jellyfin", false},
	}
	for _, c := range cases {
		if got := dockerAppUserAllowed(c.user, c.app); got != c.want {
			t.Errorf("dockerAppUserAllowed(%q, %q) = %v, want %v", c.user, c.app, got, c.want)
		}
	}
}
