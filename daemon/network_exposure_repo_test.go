// network_exposure_repo_test.go — Tests del repo de network_exposed_apps.
//
// Cubre:
//   - Create + Get roundtrip (subdomain y path).
//   - Validaciones: app_id vacío, sin subdomain/path, puerto inválido.
//   - UNIQUE(app_id) rechaza duplicados.
//   - List / ListEnabled.
//   - Triple generation: Create→pending, Update→pending, MarkApplied→converged,
//     RecordObserved→drifted.
//   - Delete + NotFound.

package main

import (
	"context"
	"errors"
	"database/sql"
	"testing"
)

func makeExposedApp(appID, subdomain string) *NetworkExposedApp {
	return &NetworkExposedApp{
		AppID:        appID,
		DisplayName:  appID,
		Subdomain:    subdomain,
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 2283,
		Enabled:      true,
	}
}

func TestExposed_CreateAndGet(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})

	if app.ID == "" {
		t.Fatal("ID not generated")
	}
	if app.Convergence.Desired != 1 || app.Convergence.Applied != 0 {
		t.Errorf("convergence after create = %+v, want desired=1 applied=0", app.Convergence)
	}

	got, err := repo.GetExposedApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GetExposedApp: %v", err)
	}
	if got.AppID != "immich" || got.Subdomain != "immich" || got.UpstreamPort != 2283 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if !got.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestExposed_CreateWithPathOnly(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := &NetworkExposedApp{
		AppID:        "gitea",
		Path:         "/gitea",
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 3000,
		Enabled:      true,
	}
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})
	got, err := repo.GetExposedAppByAppID(context.Background(), "gitea")
	if err != nil {
		t.Fatalf("GetExposedAppByAppID: %v", err)
	}
	if got.Path != "/gitea" || got.Subdomain != "" {
		t.Errorf("path-only mismatch: %+v", got)
	}
}

func TestExposed_CreateRejectsNoRoute(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := &NetworkExposedApp{
		AppID:        "bad",
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 8080,
	}
	tx, _ := c.db.Begin()
	err := repo.CreateExposedApp(context.Background(), tx, app)
	_ = tx.Rollback()
	if err == nil {
		t.Error("should reject app with no subdomain and no path")
	}
}

func TestExposed_CreateRejectsBadPort(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := &NetworkExposedApp{
		AppID:        "bad",
		Subdomain:    "bad",
		UpstreamHost: "127.0.0.1",
		UpstreamPort: 0,
	}
	tx, _ := c.db.Begin()
	err := repo.CreateExposedApp(context.Background(), tx, app)
	_ = tx.Rollback()
	if err == nil {
		t.Error("should reject invalid port 0")
	}
}

func TestExposed_DuplicateAppIDRejected(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, makeExposedApp("immich", "immich"))
	})

	tx, _ := c.db.Begin()
	err := repo.CreateExposedApp(context.Background(), tx, makeExposedApp("immich", "immich2"))
	_ = tx.Rollback()
	if err == nil {
		t.Error("UNIQUE(app_id) should reject duplicate")
	}
}

func TestExposed_ListAndListEnabled(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, makeExposedApp("immich", "immich"))
	})
	disabled := makeExposedApp("gitea", "gitea")
	disabled.Enabled = false
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, disabled)
	})

	all, err := repo.ListExposedApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("ListExposedApps = %d, want 2", len(all))
	}

	enabled, err := repo.ListEnabledExposedApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].AppID != "immich" {
		t.Errorf("ListEnabledExposedApps = %+v, want only immich", enabled)
	}
}

func TestExposed_TripleGenerationFlow(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})

	// Tras crear: pending (applied=0 < desired=1).
	pending, _ := repo.ListPendingExposedApps(context.Background())
	if len(pending) != 1 {
		t.Fatalf("after create, pending = %d, want 1", len(pending))
	}

	// MarkApplied → converged.
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.MarkExposedAppApplied(context.Background(), tx, app.ID)
	})
	pending, _ = repo.ListPendingExposedApps(context.Background())
	if len(pending) != 0 {
		t.Errorf("after MarkApplied, pending = %d, want 0", len(pending))
	}

	// Update → pending de nuevo (desired sube). Generación actual del GET.
	app.UpstreamPort = 9999
	cur, _ := repo.GetExposedApp(context.Background(), app.ID)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateExposedAppConfig(context.Background(), tx, app, cur.Convergence.Desired)
	})
	pending, _ = repo.ListPendingExposedApps(context.Background())
	if len(pending) != 1 {
		t.Errorf("after Update, pending = %d, want 1", len(pending))
	}

	// MarkApplied otra vez, luego RecordObserved → drifted.
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.MarkExposedAppApplied(context.Background(), tx, app.ID)
	})
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.RecordExposedAppObserved(context.Background(), tx, app.ID)
	})
	drifted, _ := repo.ListDriftedExposedApps(context.Background())
	if len(drifted) != 1 {
		t.Errorf("after RecordObserved, drifted = %d, want 1", len(drifted))
	}
}

func TestExposed_UpdatePersistsFields(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})

	app.DisplayName = "Immich Photos"
	app.UpstreamPort = 2284
	app.Enabled = false
	cur, _ := repo.GetExposedApp(context.Background(), app.ID)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateExposedAppConfig(context.Background(), tx, app, cur.Convergence.Desired)
	})

	got, _ := repo.GetExposedApp(context.Background(), app.ID)
	if got.DisplayName != "Immich Photos" || got.UpstreamPort != 2284 || got.Enabled {
		t.Errorf("update not persisted: %+v", got)
	}
}

func TestExposed_DeleteAndNotFound(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})
	cur, _ := repo.GetExposedApp(context.Background(), app.ID)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.DeleteExposedApp(context.Background(), tx, app.ID, cur.Convergence.Desired)
	})

	_, err := repo.GetExposedApp(context.Background(), app.ID)
	if err != ErrExposedAppNotFound {
		t.Errorf("after delete, err = %v, want ErrExposedAppNotFound", err)
	}

	// Delete inexistente → NotFound.
	tx, _ := c.db.Begin()
	err = repo.DeleteExposedApp(context.Background(), tx, "no-existe", 1)
	_ = tx.Rollback()
	if err != ErrExposedAppNotFound {
		t.Errorf("delete missing, err = %v, want ErrExposedAppNotFound", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CRIT-1 · Candado optimista (If-Match / desired_generation)
// ─────────────────────────────────────────────────────────────────────────────

func TestExposed_UpdateWithStaleGenerationConflicts(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})
	cur, _ := repo.GetExposedApp(context.Background(), app.ID)

	// Cliente A actualiza con la generación correcta → OK, desired sube.
	app.DisplayName = "A"
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateExposedAppConfig(context.Background(), tx, app, cur.Convergence.Desired)
	})
	after, _ := repo.GetExposedApp(context.Background(), app.ID)
	if after.Convergence.Desired != cur.Convergence.Desired+1 {
		t.Fatalf("desired = %d, want %d", after.Convergence.Desired, cur.Convergence.Desired+1)
	}

	// Cliente B llega con la generación VIEJA (su vista es anterior al
	// update de A) → conflicto, y la fila NO cambia.
	app.DisplayName = "B"
	tx, _ := c.db.Begin()
	err := repo.UpdateExposedAppConfig(context.Background(), tx, app, cur.Convergence.Desired)
	_ = tx.Rollback()
	if !errors.Is(err, ErrExposedAppConflict) {
		t.Fatalf("stale update err = %v, want ErrExposedAppConflict", err)
	}
	got, _ := repo.GetExposedApp(context.Background(), app.ID)
	if got.DisplayName != "A" {
		t.Errorf("row changed despite conflict: DisplayName = %q, want \"A\"", got.DisplayName)
	}
}

func TestExposed_DeleteWithStaleGenerationConflicts(t *testing.T) {
	repo, _, c, cleanup := newTestRepo(t)
	defer cleanup()

	app := makeExposedApp("immich", "immich")
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})
	cur, _ := repo.GetExposedApp(context.Background(), app.ID)

	// Alguien actualiza entre medias → la vista del que borra queda vieja.
	app.DisplayName = "cambiada"
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.UpdateExposedAppConfig(context.Background(), tx, app, cur.Convergence.Desired)
	})

	// Borrado con la generación vieja → conflicto y la app SIGUE existiendo.
	tx, _ := c.db.Begin()
	err := repo.DeleteExposedApp(context.Background(), tx, app.ID, cur.Convergence.Desired)
	_ = tx.Rollback()
	if !errors.Is(err, ErrExposedAppConflict) {
		t.Fatalf("stale delete err = %v, want ErrExposedAppConflict", err)
	}
	if _, err := repo.GetExposedApp(context.Background(), app.ID); err != nil {
		t.Errorf("app should still exist after conflicted delete: %v", err)
	}

	// Con la generación fresca → borra de verdad.
	fresh, _ := repo.GetExposedApp(context.Background(), app.ID)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.DeleteExposedApp(context.Background(), tx, app.ID, fresh.Convergence.Desired)
	})
	if _, err := repo.GetExposedApp(context.Background(), app.ID); !errors.Is(err, ErrExposedAppNotFound) {
		t.Errorf("after fresh delete, err = %v, want NotFound", err)
	}
}
