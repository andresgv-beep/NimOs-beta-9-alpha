package main

// ═══════════════════════════════════════════════════════════════════════
// db_app_images_test.go · Tests del AppImagesRepo
// ───────────────────────────────────────────────────────────────────────
// Sprint Updates · 25/05/2026
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"testing"
	"time"
)

// setupAppImagesRepoTest reutiliza setupTestAppsDB pero devuelve también el
// AppImagesRepo construido sobre la misma conexión.
func setupAppImagesRepoTest(t *testing.T) (*AppImagesRepo, func()) {
	t.Helper()
	conn, _, cleanup := setupTestAppsDB(t)
	return NewAppImagesRepo(conn), cleanup
}

func TestAppImagesRepo_UpsertLocalDigest(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	err := repo.UpsertLocalDigest(context.Background(),
		"jellyfin", "jellyfin", "jellyfin/jellyfin:latest", "sha256:v1")
	if err != nil {
		t.Fatalf("UpsertLocalDigest: %v", err)
	}

	imgs, err := repo.GetByApp(context.Background(), "jellyfin")
	if err != nil {
		t.Fatalf("GetByApp: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	img := imgs[0]
	if img.LocalDigest != "sha256:v1" || img.RemoteDigest != "sha256:v1" {
		t.Errorf("digests: got local=%q remote=%q, want both sha256:v1", img.LocalDigest, img.RemoteDigest)
	}
	if img.CheckStatus != "ok" {
		t.Errorf("check_status: got %q, want 'ok'", img.CheckStatus)
	}
	if img.RemoteCheckedAt == "" {
		t.Errorf("remote_checked_at no debería estar vacío tras UpsertLocalDigest")
	}
}

func TestAppImagesRepo_UpsertLocalDigestIdempotent(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	// Upsert 2 veces con distintos digests
	if err := repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "jellyfin/jellyfin:latest", "sha256:v1"); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if err := repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "jellyfin/jellyfin:latest", "sha256:v2"); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	imgs, err := repo.GetByApp(ctx, "jellyfin")
	if err != nil {
		t.Fatalf("GetByApp: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("Upsert no es idempotente: got %d rows, want 1", len(imgs))
	}
	if imgs[0].LocalDigest != "sha256:v2" {
		t.Errorf("último Upsert no aplicado: got %q, want sha256:v2", imgs[0].LocalDigest)
	}
}

func TestAppImagesRepo_UpdateRemoteDigest(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	// Set inicial: ambos digests iguales (sin update)
	if err := repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "jellyfin/jellyfin:latest", "sha256:old"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Simular check remoto que encuentra nueva versión
	if err := repo.UpdateRemoteDigest(ctx, "jellyfin", "jellyfin", "sha256:new", "ok"); err != nil {
		t.Fatalf("UpdateRemoteDigest: %v", err)
	}

	imgs, _ := repo.GetByApp(ctx, "jellyfin")
	img := imgs[0]
	if img.LocalDigest != "sha256:old" {
		t.Errorf("local_digest cambió tras UpdateRemote: got %q", img.LocalDigest)
	}
	if img.RemoteDigest != "sha256:new" {
		t.Errorf("remote_digest no actualizado: got %q", img.RemoteDigest)
	}
	if !img.HasUpdate() {
		t.Errorf("HasUpdate() debería ser true cuando local != remote")
	}
}

func TestAppImagesRepo_UpdateLocalDigest(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "jellyfin/jellyfin:latest", "sha256:v1")
	repo.UpdateRemoteDigest(ctx, "jellyfin", "jellyfin", "sha256:v2", "ok")

	// Simular completar update · local pasa a ser igual que remoto
	if err := repo.UpdateLocalDigest(ctx, "jellyfin", "jellyfin", "sha256:v2"); err != nil {
		t.Fatalf("UpdateLocalDigest: %v", err)
	}

	imgs, _ := repo.GetByApp(ctx, "jellyfin")
	if imgs[0].HasUpdate() {
		t.Errorf("HasUpdate() debería ser false tras UpdateLocal igualando ambos")
	}
}

func TestAppImagesRepo_DeleteByApp(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	// Crear Immich con 3 servicios
	repo.UpsertLocalDigest(ctx, "immich", "server", "img1", "sha256:a")
	repo.UpsertLocalDigest(ctx, "immich", "ml", "img2", "sha256:b")
	repo.UpsertLocalDigest(ctx, "immich", "redis", "img3", "sha256:c")
	// Y Jellyfin aparte
	repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "img4", "sha256:d")

	if err := repo.DeleteByApp(ctx, "immich"); err != nil {
		t.Fatalf("DeleteByApp: %v", err)
	}

	// Immich · 0 rows
	imgs, _ := repo.GetByApp(ctx, "immich")
	if len(imgs) != 0 {
		t.Errorf("Immich tras delete: got %d rows, want 0", len(imgs))
	}
	// Jellyfin · sigue ahí
	imgs, _ = repo.GetByApp(ctx, "jellyfin")
	if len(imgs) != 1 {
		t.Errorf("Jellyfin tras delete de Immich: got %d, want 1", len(imgs))
	}
}

func TestAppImagesRepo_ListAppsWithUpdates(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()

	// 3 apps con distintos estados
	// 1. Immich · 2 de 3 servicios con update
	repo.UpsertLocalDigest(ctx, "immich", "server", "img1", "sha256:a-old")
	repo.UpsertLocalDigest(ctx, "immich", "ml", "img2", "sha256:b-old")
	repo.UpsertLocalDigest(ctx, "immich", "redis", "img3", "sha256:c")
	repo.UpdateRemoteDigest(ctx, "immich", "server", "sha256:a-new", "ok")
	repo.UpdateRemoteDigest(ctx, "immich", "ml", "sha256:b-new", "ok")
	// redis sin update

	// 2. Jellyfin · 1 servicio con update
	repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "img4", "sha256:old")
	repo.UpdateRemoteDigest(ctx, "jellyfin", "jellyfin", "sha256:new", "ok")

	// 3. Plex · sin updates
	repo.UpsertLocalDigest(ctx, "plex", "plex", "img5", "sha256:current")

	apps, err := repo.ListAppsWithUpdates(ctx)
	if err != nil {
		t.Fatalf("ListAppsWithUpdates: %v", err)
	}

	if len(apps) != 2 {
		t.Fatalf("esperaba 2 apps con updates (immich, jellyfin), got %d", len(apps))
	}

	// Encontrar Immich
	var immich *AppUpdateSummary
	for i := range apps {
		if apps[i].AppID == "immich" {
			immich = &apps[i]
			break
		}
	}
	if immich == nil {
		t.Fatal("immich no aparece en resultados")
	}
	if immich.ServicesTotal != 3 {
		t.Errorf("immich servicesTotal: got %d, want 3", immich.ServicesTotal)
	}
	if immich.ServicesWithUpdate != 2 {
		t.Errorf("immich servicesWithUpdate: got %d, want 2", immich.ServicesWithUpdate)
	}
}

func TestAppImagesRepo_CountAppsWithUpdates(t *testing.T) {
	repo, cleanup := setupAppImagesRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	// Sin updates inicialmente
	count, err := repo.CountAppsWithUpdates(ctx)
	if err != nil {
		t.Fatalf("CountAppsWithUpdates: %v", err)
	}
	if count != 0 {
		t.Errorf("BD vacía: got %d, want 0", count)
	}

	// Añadir 2 apps con update
	repo.UpsertLocalDigest(ctx, "jellyfin", "jellyfin", "img1", "sha256:old")
	repo.UpdateRemoteDigest(ctx, "jellyfin", "jellyfin", "sha256:new", "ok")
	repo.UpsertLocalDigest(ctx, "immich", "server", "img2", "sha256:old")
	repo.UpdateRemoteDigest(ctx, "immich", "server", "sha256:new", "ok")

	count, _ = repo.CountAppsWithUpdates(ctx)
	if count != 2 {
		t.Errorf("Con 2 apps en update: got %d, want 2", count)
	}
}

func TestAppImage_HasUpdate(t *testing.T) {
	cases := []struct {
		name    string
		local   string
		remote  string
		wantHas bool
	}{
		{"both empty", "", "", false},
		{"only local", "sha256:abc", "", false}, // sin remoto, no se puede afirmar
		{"only remote", "", "sha256:abc", true}, // local vacío != remote
		{"equal", "sha256:abc", "sha256:abc", false},
		{"different", "sha256:abc", "sha256:xyz", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img := AppImage{LocalDigest: c.local, RemoteDigest: c.remote}
			if got := img.HasUpdate(); got != c.wantHas {
				t.Errorf("HasUpdate(): got %v, want %v", got, c.wantHas)
			}
		})
	}
}

func TestAppImage_NeedsRemoteCheck(t *testing.T) {
	ttl := 6 * time.Hour
	now := time.Now().UTC()

	cases := []struct {
		name      string
		checkedAt string
		status    string
		want      bool
	}{
		{"never checked", "", "ok", true},
		{"recent check", now.Add(-1 * time.Hour).Format(time.RFC3339), "ok", false},
		{"old check", now.Add(-7 * time.Hour).Format(time.RFC3339), "ok", true},
		{"unsupported · skip", now.Add(-100 * time.Hour).Format(time.RFC3339), "unsupported", false},
		{"unauthorized · skip", "", "unauthorized", false},
		{"malformed date · recheck", "not-a-date", "ok", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img := AppImage{RemoteCheckedAt: c.checkedAt, CheckStatus: c.status}
			if got := img.NeedsRemoteCheck(ttl); got != c.want {
				t.Errorf("NeedsRemoteCheck(): got %v, want %v", got, c.want)
			}
		})
	}
}
