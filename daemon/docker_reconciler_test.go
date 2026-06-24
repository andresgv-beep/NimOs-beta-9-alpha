// docker_reconciler_test.go — Tests del reconciler de Docker apps (Fase 3).
//
// Usa los inyectables listContainers + inspectContainer del reconciler para
// simular Docker sin daemon real. BD SQLite real en /tmp (vía setupTestAppsDB)
// para verificar que las rows se crean de verdad.
//
// Escenarios cubiertos:
//   - Huérfano detectado y reimportado (el caso bug Nextcloud)
//   - App ya registrada · no se toca
//   - Stack multi-container · una sola row por app_id
//   - Container con app_id vacío · se omite con log
//   - Container sin labels NimOS · nunca llega (filtrado por listNimOSContainers)
//   - Reimport preserva installed_by/installed_at de los labels
//   - Reimport con inspect fallido · usa system-recovery como fallback

package main

import (
	"context"
	"testing"
	"time"
)

// newTestReconciler crea un reconciler con fakes inyectados.
func newTestReconciler(repo *AppsRepo, containers []NimOSContainer, labels map[string]map[string]string) *DockerAppReconciler {
	return &DockerAppReconciler{
		repo:  repo,
		clock: NewFakeClock(time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)),
		listContainers: func(ctx context.Context) ([]NimOSContainer, error) {
			return containers, nil
		},
		inspectContainer: func(ctx context.Context, id string) (map[string]string, error) {
			if l, ok := labels[id]; ok {
				return l, nil
			}
			return map[string]string{}, nil
		},
	}
}

// TestReconcile_OrphanReimported · el caso central · bug Nextcloud.
// Container vivo con label managed=true pero sin row → se reimporta.
func TestReconcile_OrphanReimported(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	containers := []NimOSContainer{
		{ID: "abc123", Name: "nextcloud", AppID: "nextcloud", IsStack: true, SchemaVer: "beta_8.2"},
	}
	labels := map[string]map[string]string{
		"abc123": {
			LabelAppID:       "nextcloud",
			LabelInstalledBy: "andres",
			LabelInstalledAt: "2026-05-27T18:42:54Z",
			LabelStack:       "true",
		},
	}

	rec := newTestReconciler(repo, containers, labels)

	// Pre-condición: docker_apps vacío
	apps, _ := repo.ListDockerApps(context.Background())
	if len(apps) != 0 {
		t.Fatalf("setup: docker_apps debería estar vacío, tiene %d", len(apps))
	}

	// Ejecutar reconcile
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	// Post-condición: la app debe existir
	app, err := repo.GetDockerApp(context.Background(), "nextcloud")
	if err != nil {
		t.Fatalf("GetDockerApp tras reconcile: %v", err)
	}
	if app == nil {
		t.Fatal("La app huérfana NO se reimportó · bug Nextcloud no resuelto por reconciler")
	}
	if app.Type != "stack" {
		t.Errorf("Type = %q, want 'stack'", app.Type)
	}
	if app.InstalledBy != "andres" {
		t.Errorf("InstalledBy = %q, want 'andres' (de los labels)", app.InstalledBy)
	}
	if app.InstalledAt != "2026-05-27T18:42:54Z" {
		t.Errorf("InstalledAt = %q, want el de los labels", app.InstalledAt)
	}
}

// TestReconcile_AlreadyRegistered · app con row existente no se toca.
func TestReconcile_AlreadyRegistered(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	// Pre-registrar la app con datos "buenos"
	original := &DBDockerApp{
		ID:          "jellyfin",
		Name:        "Jellyfin Media Server",
		Image:       "jellyfin/jellyfin:latest",
		Type:        "container",
		OpenMode:    "internal",
		InstalledBy: "andres",
		InstalledAt: "2026-05-01T10:00:00Z",
	}
	if err := repo.CreateOrUpdateDockerApp(context.Background(), original); err != nil {
		t.Fatalf("setup: %v", err)
	}

	containers := []NimOSContainer{
		{ID: "xyz", Name: "jellyfin", AppID: "jellyfin", IsStack: false, SchemaVer: "beta_8.2"},
	}

	rec := newTestReconciler(repo, containers, nil)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	// La app debe conservar sus datos originales (NO sobrescrita con mínimos)
	app, _ := repo.GetDockerApp(context.Background(), "jellyfin")
	if app.Name != "Jellyfin Media Server" {
		t.Errorf("Name = %q · el reconciler sobrescribió una app ya registrada", app.Name)
	}
	if app.Image != "jellyfin/jellyfin:latest" {
		t.Errorf("Image = %q · el reconciler pisó datos buenos", app.Image)
	}
}

// TestReconcile_StackOneRowPerAppID · stack con 4 containers → 1 sola row.
func TestReconcile_StackOneRowPerAppID(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	// Immich: 4 containers, todos con app_id="immich"
	containers := []NimOSContainer{
		{ID: "c1", Name: "immich_server", AppID: "immich", IsStack: true},
		{ID: "c2", Name: "immich_postgres", AppID: "immich", IsStack: true},
		{ID: "c3", Name: "immich_redis", AppID: "immich", IsStack: true},
		{ID: "c4", Name: "immich_ml", AppID: "immich", IsStack: true},
	}

	rec := newTestReconciler(repo, containers, nil)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	apps, _ := repo.ListDockerApps(context.Background())
	if len(apps) != 1 {
		t.Fatalf("Se crearon %d rows, want 1 (stack = 1 sola app row)", len(apps))
	}
	if apps[0].ID != "immich" {
		t.Errorf("app ID = %q, want 'immich'", apps[0].ID)
	}
}

// TestReconcile_EmptyAppIDSkipped · container con app_id vacío se omite.
func TestReconcile_EmptyAppIDSkipped(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	containers := []NimOSContainer{
		{ID: "weird", Name: "weird", AppID: "", IsStack: false}, // managed pero sin app_id
	}

	rec := newTestReconciler(repo, containers, nil)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	apps, _ := repo.ListDockerApps(context.Background())
	if len(apps) != 0 {
		t.Errorf("Se creó %d rows · un container sin app_id NO debe importarse", len(apps))
	}
}

// TestReconcile_InspectFails_UsesRecoveryFallback · si inspect falla,
// usa system-recovery como installed_by.
func TestReconcile_InspectFails_UsesRecoveryFallback(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	containers := []NimOSContainer{
		{ID: "noinspect", Name: "gitea", AppID: "gitea", IsStack: false},
	}

	rec := &DockerAppReconciler{
		repo:  repo,
		clock: NewFakeClock(time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)),
		listContainers: func(ctx context.Context) ([]NimOSContainer, error) {
			return containers, nil
		},
		inspectContainer: func(ctx context.Context, id string) (map[string]string, error) {
			return nil, context.DeadlineExceeded // simula inspect fallido
		},
	}

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	app, _ := repo.GetDockerApp(context.Background(), "gitea")
	if app == nil {
		t.Fatal("La app debería importarse aunque inspect falle")
	}
	if app.InstalledBy != "system-recovery" {
		t.Errorf("InstalledBy = %q, want 'system-recovery' (fallback)", app.InstalledBy)
	}
	// installed_at debe ser el del FakeClock
	if app.InstalledAt != "2026-05-28T10:00:00Z" {
		t.Errorf("InstalledAt = %q, want el del FakeClock", app.InstalledAt)
	}
}

// TestReconcile_NoContainers · sin containers, no hace nada (early return).
func TestReconcile_NoContainers(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	rec := newTestReconciler(repo, []NimOSContainer{}, nil)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile con 0 containers debería ser no-op, dio error: %v", err)
	}
}

// TestReconcile_DeletingAppNotReimported · una app marcada deleting=1 NO
// debe reimportarse (el usuario la está desinstalando).
func TestReconcile_DeletingAppNotReimported(t *testing.T) {
	_, repo, cleanup := setupTestAppsDB(t)
	defer cleanup()

	// App registrada Y marcada deleting
	app := &DBDockerApp{
		ID: "vaultwarden", Name: "Vaultwarden", Type: "container",
		OpenMode: "internal", InstalledBy: "andres", InstalledAt: "2026-05-01T10:00:00Z",
	}
	if err := repo.CreateOrUpdateDockerApp(context.Background(), app); err != nil {
		t.Fatalf("setup create: %v", err)
	}
	if err := repo.MarkDockerAppDeleting(context.Background(), "vaultwarden"); err != nil {
		t.Fatalf("setup mark deleting: %v", err)
	}

	// El container sigue vivo mientras se desinstala
	containers := []NimOSContainer{
		{ID: "vw", Name: "vaultwarden", AppID: "vaultwarden", IsStack: false},
	}

	rec := newTestReconciler(repo, containers, nil)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile falló: %v", err)
	}

	// La app sigue existiendo (estaba en la lista IncludingDeleting, no se
	// trató como huérfano). NO debe haberse creado una segunda row ni
	// haberse "revivido".
	apps, _ := repo.ListDockerAppsIncludingDeleting(context.Background())
	count := 0
	for _, a := range apps {
		if a.ID == "vaultwarden" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("vaultwarden aparece %d veces, want 1 · el reconciler no debe duplicar apps deleting", count)
	}
}
