// storage_references_test.go — Resolver de referencias (qué usa un pool/share).

package main

import "testing"

func shareWith(name, pool string, apps ...string) DBShare {
	s := DBShare{Name: name, Pool: pool}
	for _, a := range apps {
		s.AppPermissions = append(s.AppPermissions, AppPermission{AppId: a, Permission: "rw"})
	}
	return s
}

func refContains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// Un pool con dos shares; las apps de ambas se agregan sin duplicar.
func TestResolvePoolReferences_SharesAndApps(t *testing.T) {
	all := []DBShare{
		shareWith("data11", "data8", "jellyfin", "sonarr"),
		shareWith("musica", "data8", "jellyfin"), // jellyfin repetida
		shareWith("otra", "data9", "transmission"),
	}
	ref := resolvePoolReferences("data8", all)

	if len(ref.Shares) != 2 || !refContains(ref.Shares, "data11") || !refContains(ref.Shares, "musica") {
		t.Errorf("shares del pool data8 mal: %+v", ref.Shares)
	}
	if len(ref.Apps) != 2 || !refContains(ref.Apps, "jellyfin") || !refContains(ref.Apps, "sonarr") {
		t.Errorf("apps esperadas [jellyfin, sonarr] sin duplicar; got %+v", ref.Apps)
	}
	if refContains(ref.Apps, "transmission") {
		t.Error("transmission es de data9, no debe aparecer en data8")
	}
}

// Pool sin shares → referencias vacías (no nil, para JSON limpio).
func TestResolvePoolReferences_Empty(t *testing.T) {
	ref := resolvePoolReferences("vacio", []DBShare{shareWith("x", "otro")})
	if ref.Shares == nil || ref.Apps == nil {
		t.Error("Shares/Apps deben ser slices vacíos, no nil")
	}
	if len(ref.Shares) != 0 || len(ref.Apps) != 0 {
		t.Errorf("pool sin shares no debe tener referencias; got %+v", ref)
	}
}

// Share usada por varias apps, sin duplicar.
func TestResolveShareReferences_Apps(t *testing.T) {
	ref := resolveShareReferences(shareWith("data11", "data8", "jellyfin", "sonarr", "jellyfin"))
	if len(ref.Apps) != 2 {
		t.Errorf("apps sin duplicar esperadas (2); got %+v", ref.Apps)
	}
	if ref.Share != "data11" {
		t.Errorf("share name mal: %q", ref.Share)
	}
}

// Share sin apps → lista vacía, no nil.
func TestResolveShareReferences_None(t *testing.T) {
	ref := resolveShareReferences(shareWith("sola", "data8"))
	if ref.Apps == nil || len(ref.Apps) != 0 {
		t.Errorf("share sin apps → []; got %+v", ref.Apps)
	}
}
