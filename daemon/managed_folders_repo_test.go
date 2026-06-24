package main

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

// setupManagedFoldersDB inicializa el `db` global con el schema completo del
// daemon en una BD temporal, e inserta un share padre (FK). Devuelve cleanup.
func setupManagedFoldersDB(t *testing.T) func() {
	t.Helper()

	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name())
	tmpDB := "/tmp/nimos_mf_test_" + safeName + ".db"
	os.Remove(tmpDB)

	conn, err := sql.Open("sqlite", tmpDB+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}

	prev := db
	db = conn
	if err := createTables(); err != nil {
		t.Fatalf("createTables: %v", err)
	}

	// Share padre para satisfacer la FK de managed_folders.
	_, err = db.Exec(
		`INSERT INTO shares (name, display_name, description, path, volume, pool, created_by, created_at)
		 VALUES ('media','Media','', '/nimos/pools/tank/shares/media','tank','tank','admin','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert share: %v", err)
	}

	return func() {
		conn.Close()
		os.Remove(tmpDB)
		db = prev
	}
}

func TestManagedFolder_CreateAndGet_RoundTrip(t *testing.T) {
	defer setupManagedFoldersDB(t)()

	id, err := dbManagedFolderCreate("media", "proyectos", 50<<30, "juan", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == "" {
		t.Fatal("id vacío")
	}

	f, err := dbManagedFolderGet(id)
	if err != nil || f == nil {
		t.Fatalf("get: %v (f=%v)", err, f)
	}
	if f.ShareName != "media" || f.RelPath != "proyectos" {
		t.Errorf("campos mal: %+v", f)
	}
	if f.QuotaBytes != 50<<30 {
		t.Errorf("quota = %d, want %d", f.QuotaBytes, 50<<30)
	}
	if f.OwnerUser != "juan" || f.CreatedBy != "admin" {
		t.Errorf("owner/createdBy mal: %s / %s", f.OwnerUser, f.CreatedBy)
	}
	if f.ControlState != "active" {
		t.Errorf("control_state = %q, want active", f.ControlState)
	}
	if f.Generation != 0 {
		t.Errorf("generation = %d, want 0", f.Generation)
	}
}

func TestManagedFolder_GetByPath(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	dbManagedFolderCreate("media", "fotos", 0, "ana", "admin")

	f, err := dbManagedFolderGetByPath("media", "fotos")
	if err != nil || f == nil {
		t.Fatalf("getByPath: %v", err)
	}
	// inexistente → (nil, nil)
	none, err := dbManagedFolderGetByPath("media", "noexiste")
	if err != nil {
		t.Fatalf("getByPath inexistente devolvió error: %v", err)
	}
	if none != nil {
		t.Error("getByPath inexistente debería ser nil")
	}
}

func TestManagedFolder_SetQuotaBumpsGeneration(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	id, _ := dbManagedFolderCreate("media", "backups", 0, "admin", "admin")

	if err := dbManagedFolderSetQuota(id, 100<<30); err != nil {
		t.Fatalf("setQuota: %v", err)
	}
	f, _ := dbManagedFolderGet(id)
	if f.QuotaBytes != 100<<30 {
		t.Errorf("quota = %d, want %d", f.QuotaBytes, 100<<30)
	}
	if f.Generation != 1 {
		t.Errorf("generation = %d, want 1 tras set_quota", f.Generation)
	}
}

func TestManagedFolder_Permissions(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	id, _ := dbManagedFolderCreate("media", "compartido", 0, "admin", "admin")

	if err := dbManagedFolderSetPermission(id, "juan", "rw"); err != nil {
		t.Fatalf("setPerm rw: %v", err)
	}
	if err := dbManagedFolderSetPermission(id, "ana", "ro"); err != nil {
		t.Fatalf("setPerm ro: %v", err)
	}

	perms, err := dbManagedFolderPermissions(id)
	if err != nil {
		t.Fatalf("perms: %v", err)
	}
	if perms["juan"] != "rw" || perms["ana"] != "ro" {
		t.Errorf("perms mal: %+v", perms)
	}

	// none elimina
	dbManagedFolderSetPermission(id, "juan", "none")
	perms, _ = dbManagedFolderPermissions(id)
	if _, ok := perms["juan"]; ok {
		t.Error("juan debería haber sido eliminado con 'none'")
	}

	// permiso inválido rechazado
	if err := dbManagedFolderSetPermission(id, "x", "admin"); err == nil {
		t.Error("permiso inválido debería dar error")
	}
}

func TestManagedFolder_DeleteCascadesPermissions(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	id, _ := dbManagedFolderCreate("media", "tmp", 0, "admin", "admin")
	dbManagedFolderSetPermission(id, "juan", "rw")

	if err := dbManagedFolderDelete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	f, _ := dbManagedFolderGet(id)
	if f != nil {
		t.Error("carpeta debería haber sido borrada")
	}
	perms, _ := dbManagedFolderPermissions(id)
	if len(perms) != 0 {
		t.Errorf("permisos deberían haberse borrado, quedan %d", len(perms))
	}
}

func TestManagedFolder_UniqueConstraint(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	if _, err := dbManagedFolderCreate("media", "dup", 0, "admin", "admin"); err != nil {
		t.Fatalf("primera creación: %v", err)
	}
	if _, err := dbManagedFolderCreate("media", "dup", 0, "admin", "admin"); err == nil {
		t.Error("segunda creación con mismo (share,rel_path) debería fallar por UNIQUE")
	}
}

func TestManagedFolder_StateTransitions(t *testing.T) {
	defer setupManagedFoldersDB(t)()
	id, _ := dbManagedFolderCreate("media", "estado", 0, "admin", "admin")

	if err := dbManagedFolderSetState(id, "deleting"); err != nil {
		t.Fatalf("setState: %v", err)
	}
	f, _ := dbManagedFolderGet(id)
	if f.ControlState != "deleting" {
		t.Errorf("control_state = %q, want deleting", f.ControlState)
	}
}
