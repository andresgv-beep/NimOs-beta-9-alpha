package main

import "testing"

func TestParseAppIDLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"com.nimos.app_id=gitea", "gitea"},
		{"com.nimos.managed=true,com.nimos.app_id=jellyfin,com.nimos.stack=true", "jellyfin"},
		{"com.docker.compose.project=x,com.nimos.app_id=n8n", "n8n"},
		{"com.nimos.app_id=matrix-synapse,x=y", "matrix-synapse"},
		{"com.nimos.managed=true", ""}, // sin app_id
		{"foo=bar,baz=qux", ""},
		// no confundir un label cuyo nombre CONTIENE app_id como subcadena
		{"com.nimos.app_id_extra=no,com.nimos.app_id=radarr", "radarr"},
	}
	for _, c := range cases {
		if got := parseAppIDLabel(c.in); got != c.want {
			t.Errorf("parseAppIDLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchContainerForAppID_LabelFirst(t *testing.T) {
	// El label gana aunque el NOMBRE no coincida en absoluto.
	containers := map[string]dockerContainer{
		"weird_name_xyz": {Name: "weird_name_xyz", AppID: "gitea"},
	}
	if name, c := matchContainerForAppID("gitea", containers); c == nil || name != "weird_name_xyz" {
		t.Fatalf("label-first debería matchear weird_name_xyz, got name=%q c=%v", name, c)
	}

	// Sin label → fallback por nombre (legacy / no-NimOS) sigue funcionando.
	legacy := map[string]dockerContainer{
		"jellyfin": {Name: "jellyfin"}, // AppID vacío
	}
	if name, c := matchContainerForAppID("jellyfin", legacy); c == nil || name != "jellyfin" {
		t.Fatalf("fallback por nombre debería matchear jellyfin, got name=%q", name)
	}

	// El label tiene prioridad sobre un match por NOMBRE de otro container.
	mixed := map[string]dockerContainer{
		"sonarr":      {Name: "sonarr"},                       // nombre coincide, sin label
		"sonarr_real": {Name: "sonarr_real", AppID: "sonarr"}, // este lleva el label
	}
	if name, _ := matchContainerForAppID("sonarr", mixed); name != "sonarr_real" {
		t.Fatalf("el label debería ganar sobre el nombre, got %q", name)
	}

	// Multi-servicio: todos los servicios llevan el mismo app_id. Hay una
	// TRAMPA: un container llamado literalmente "immich" pero de OTRA app. El
	// match por nombre viejo habría pillado la trampa (nombre exacto); el
	// label-first devuelve uno de immich de verdad (AppID=immich).
	multi := map[string]dockerContainer{
		"immich_server":   {Name: "immich_server", AppID: "immich"},
		"immich_postgres": {Name: "immich_postgres", AppID: "immich"},
		"immich":          {Name: "immich", AppID: "otra-app"}, // trampa
	}
	if name, c := matchContainerForAppID("immich", multi); c == nil || c.AppID != "immich" {
		t.Fatalf("multi-servicio debería devolver un container de immich (AppID=immich), got name=%q", name)
	}

	// Nada coincide → ("", nil).
	if name, c := matchContainerForAppID("noexiste", map[string]dockerContainer{"x": {Name: "x"}}); c != nil || name != "" {
		t.Fatalf("sin match debería dar (\"\", nil), got name=%q c=%v", name, c)
	}
}
