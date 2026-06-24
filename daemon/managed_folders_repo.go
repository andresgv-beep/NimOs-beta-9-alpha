package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ═══════════════════════════════════════════════════════════════════════
// MANAGED FOLDERS · capa de datos (SQLite) · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// CRUD puro sobre managed_folders + managed_folder_permissions. Sin lógica
// de filesystem ni privilegios: solo persistencia. La coordinación
// (subvol, quota, ACL) vive en managed_folders_service.go.
//
// Espeja el estilo de dbSharesCreate / dbShareSetPermission.
// ═══════════════════════════════════════════════════════════════════════

// DBManagedFolder es la fila cruda de managed_folders.
type DBManagedFolder struct {
	ID           string
	ShareName    string
	RelPath      string
	QuotaBytes   int64
	Generation   int64
	ControlState string
	OwnerUser    string
	CreatedBy    string
	CreatedAt    string
}

// dbManagedFolderCreate inserta una nueva carpeta gestionada y devuelve su id.
// control_state arranca en 'active', generation en 0.
func dbManagedFolderCreate(shareName, relPath string, quotaBytes int64, owner, createdBy string) (string, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO managed_folders
			(id, share_name, rel_path, quota_bytes, generation, control_state, owner_user, created_by, created_at)
		 VALUES (?, ?, ?, ?, 0, 'active', ?, ?, ?)`,
		id, shareName, relPath, quotaBytes, owner, createdBy, now)
	if err != nil {
		return "", err
	}
	return id, nil
}

// dbManagedFolderGet devuelve una carpeta por id, o (nil, nil) si no existe.
func dbManagedFolderGet(id string) (*DBManagedFolder, error) {
	var f DBManagedFolder
	err := db.QueryRow(
		`SELECT id, share_name, rel_path, quota_bytes, generation, control_state, owner_user, created_by, created_at
		   FROM managed_folders WHERE id = ?`, id).
		Scan(&f.ID, &f.ShareName, &f.RelPath, &f.QuotaBytes, &f.Generation,
			&f.ControlState, &f.OwnerUser, &f.CreatedBy, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// dbManagedFolderGetByPath busca por (share_name, rel_path). Útil para validar
// duplicados en la creación. Devuelve (nil, nil) si no existe.
func dbManagedFolderGetByPath(shareName, relPath string) (*DBManagedFolder, error) {
	var f DBManagedFolder
	err := db.QueryRow(
		`SELECT id, share_name, rel_path, quota_bytes, generation, control_state, owner_user, created_by, created_at
		   FROM managed_folders WHERE share_name = ? AND rel_path = ?`, shareName, relPath).
		Scan(&f.ID, &f.ShareName, &f.RelPath, &f.QuotaBytes, &f.Generation,
			&f.ControlState, &f.OwnerUser, &f.CreatedBy, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// dbManagedFoldersListByShare devuelve todas las carpetas de un share.
func dbManagedFoldersListByShare(shareName string) ([]DBManagedFolder, error) {
	rows, err := db.Query(
		`SELECT id, share_name, rel_path, quota_bytes, generation, control_state, owner_user, created_by, created_at
		   FROM managed_folders WHERE share_name = ? ORDER BY rel_path`, shareName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBManagedFolder
	for rows.Next() {
		var f DBManagedFolder
		if err := rows.Scan(&f.ID, &f.ShareName, &f.RelPath, &f.QuotaBytes, &f.Generation,
			&f.ControlState, &f.OwnerUser, &f.CreatedBy, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// dbManagedFolderSetQuota actualiza quota_bytes e incrementa generation.
func dbManagedFolderSetQuota(id string, quotaBytes int64) error {
	_, err := db.Exec(
		`UPDATE managed_folders SET quota_bytes = ?, generation = generation + 1 WHERE id = ?`,
		quotaBytes, id)
	return err
}

// dbManagedFolderSetState fija control_state (active|deleting|error).
func dbManagedFolderSetState(id, state string) error {
	_, err := db.Exec(`UPDATE managed_folders SET control_state = ? WHERE id = ?`, state, id)
	return err
}

// dbManagedFolderBumpGeneration incrementa generation (usado tras cambios de
// permisos, donde quota_bytes no cambia).
func dbManagedFolderBumpGeneration(id string) error {
	_, err := db.Exec(`UPDATE managed_folders SET generation = generation + 1 WHERE id = ?`, id)
	return err
}

// dbManagedFolderDelete borra la carpeta y sus permisos (explícito, sin
// depender de PRAGMA foreign_keys).
func dbManagedFolderDelete(id string) error {
	if _, err := db.Exec(`DELETE FROM managed_folder_permissions WHERE folder_id = ?`, id); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM managed_folders WHERE id = ?`, id)
	return err
}

// ─── Permisos ────────────────────────────────────────────────────────────

// dbManagedFolderSetPermission fija/quita el permiso de un usuario.
// permission "none" o "" elimina la fila.
func dbManagedFolderSetPermission(folderID, username, permission string) error {
	if permission == "none" || permission == "" {
		_, err := db.Exec(
			`DELETE FROM managed_folder_permissions WHERE folder_id = ? AND username = ?`,
			folderID, username)
		return err
	}
	if permission != "rw" && permission != "ro" {
		return fmt.Errorf("invalid permission: %s", permission)
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO managed_folder_permissions (folder_id, username, permission)
		 VALUES (?, ?, ?)`, folderID, username, permission)
	return err
}

// dbManagedFolderPermissions devuelve el mapa username->permission de una carpeta.
func dbManagedFolderPermissions(folderID string) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT username, permission FROM managed_folder_permissions WHERE folder_id = ?`,
		folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	perms := map[string]string{}
	for rows.Next() {
		var u, p string
		if err := rows.Scan(&u, &p); err != nil {
			return nil, err
		}
		perms[u] = p
	}
	return perms, rows.Err()
}
