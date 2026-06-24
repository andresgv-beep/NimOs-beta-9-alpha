package main

import (
	"strings"
	"testing"
)

// TestValidateInstanceID cubre la regex de validación de IDs de service
// instances. Fase 6 endureció esto de un parseo simple por '@' a una
// regex que rechaza basura como "aaa@bbb@ccc" y caracteres especiales.
func TestValidateInstanceID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		// ── ACEPTAR · IDs canónicos que el sistema genera ──
		{"docker_pool", "docker@plex_pool", false},
		{"docker_pool_dash", "docker@media-1", false},
		{"nimtorrent_simple", "nimtorrent@media", false},
		{"ssh_system", "ssh@system", false},
		{"samba_system", "samba@system", false},
		{"nfs_system", "nfs@system", false},
		{"nimbackup_system", "nimbackup@system", false},
		{"vms_system", "vms@system", false},
		{"app_with_digits", "app2@pool1", false},
		{"underscore_start", "_app@_pool", false},
		{"digits_start", "1app@2pool", false},
		{"max_letters", "abcdefghijklmnop@qrstuvwxyz", false},

		// ── RECHAZAR · formato roto ──
		{"empty", "", true},
		{"no_at", "noseparator", true},
		{"only_at", "@", true},
		{"empty_left", "@pool", true},
		{"empty_right", "app@", true},
		{"double_at", "app@pool@extra", true},
		{"triple_at", "a@b@c@d", true},

		// ── RECHAZAR · caracteres prohibidos ──
		{"uppercase", "App@pool", true},
		{"uppercase_pool", "app@Pool", true},
		{"space_left", "ap p@pool", true},
		{"space_right", "app@po ol", true},
		{"slash", "app@pool/sub", true},
		{"dot", "app@pool.name", true},
		{"emoji", "app@pool🔥", true},
		{"sql_inject", "app';--@pool", true},
		{"newline", "app\n@pool", true},
		{"tab", "app\t@pool", true},

		// ── RECHAZAR · empieza con guion (parece flag) ──
		{"dash_start_left", "-app@pool", true},
		{"dash_start_right", "app@-pool", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateInstanceID(c.id)
			if (err != nil) != c.wantErr {
				t.Errorf("validateInstanceID(%q) error = %v, wantErr = %v", c.id, err, c.wantErr)
			}
			// Si esperamos error, mensaje debe contener el ID para debug
			if c.wantErr && err != nil && !strings.Contains(err.Error(), "instance ID") {
				t.Errorf("error message should mention 'instance ID', got: %v", err)
			}
		})
	}
}
