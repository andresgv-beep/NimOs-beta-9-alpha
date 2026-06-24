package main

import (
	"fmt"
	"testing"
)

func TestFolderRouteRegexes(t *testing.T) {
	// Colección
	collOK := []string{
		"/api/shares/media/folders",
		"/api/shares/media/folders/",
		"/api/shares/data-1/folders",
	}
	for _, p := range collOK {
		if folderCollectionRegex.FindStringSubmatch(p) == nil {
			t.Errorf("colección debería matchear: %q", p)
		}
	}

	// Recurso (con id)
	if m := folderResourceRegex.FindStringSubmatch("/api/shares/media/folders/abc-123-def"); m == nil {
		t.Error("recurso debería matchear con id")
	} else if m[1] != "media" || m[2] != "abc-123-def" {
		t.Errorf("grupos mal: share=%q id=%q", m[1], m[2])
	}

	// No deben matchear
	noMatch := []string{
		"/api/shares/media",             // share simple, no folders
		"/api/shares",                   // colección de shares
		"/api/shares/media/folders/a/b", // demasiado profundo
	}
	for _, p := range noMatch {
		if folderCollectionRegex.MatchString(p) || folderResourceRegex.MatchString(p) {
			t.Errorf("no debería matchear ninguna regex de folders: %q", p)
		}
	}
}

func TestMapFolderErrorToStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 200},
		{fmt.Errorf("managed folder not found"), 404},
		{fmt.Errorf("managed folder already exists"), 409},
		{fmt.Errorf("folder_not_empty"), 409},
		{fmt.Errorf("invalid folder path"), 400},
		{fmt.Errorf("folder path is required"), 400},
		{fmt.Errorf("folder must be top-level (no nested paths in v1)"), 400},
		{fmt.Errorf("folder name too long"), 400},
		{fmt.Errorf("btrfs subvolume create failed: whatever"), 500},
	}
	for _, c := range cases {
		if got := mapFolderErrorToStatus(c.err); got != c.want {
			t.Errorf("mapFolderErrorToStatus(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestParsePermissionsMap(t *testing.T) {
	// objeto válido
	in := map[string]interface{}{
		"juan": "rw",
		"ana":  "ro",
		"x":    123, // no-string, debe ignorarse
	}
	out := parsePermissionsMap(in)
	if out["juan"] != "rw" || out["ana"] != "ro" {
		t.Errorf("permisos mal parseados: %+v", out)
	}
	if _, ok := out["x"]; ok {
		t.Error("valor no-string debería ignorarse")
	}

	// nil / tipo incorrecto → mapa vacío, no panic
	if m := parsePermissionsMap(nil); len(m) != 0 {
		t.Error("nil debería dar mapa vacío")
	}
	if m := parsePermissionsMap("no soy un mapa"); len(m) != 0 {
		t.Error("string debería dar mapa vacío")
	}
}
