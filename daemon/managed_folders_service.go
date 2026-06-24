package main

import (
	"context"
	"fmt"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// MANAGED FOLDERS · service · NimOS Beta 8.1 (Fase 3, paso 3)
// ═══════════════════════════════════════════════════════════════════════
//
// Orquesta las ops privilegiadas (folder.* en handleOp) + la persistencia
// (managed_folders_repo.go). Espeja el estilo de shares_service.go.
//
// PRINCIPIO TRANSACCIONAL (creación):
//   - El filesystem se toca PRIMERO (crear subvol, quota, ACLs).
//   - La DB se persiste AL FINAL, sólo si todo lo anterior fue bien.
//   - Si algo falla, ROLLBACK = borrar el subvol (un único folder.delete).
//     Borrar el subvol se lleva consigo quota y ACLs, así que el rollback es
//     atómico y fiable. NUNCA se persiste en DB un estado a medias.
//   - Sin warnings silenciosos: cualquier fallo aborta con error claro.
// ═══════════════════════════════════════════════════════════════════════

// ManagedFolderView es la vista enriquecida de una carpeta gestionada,
// con datos de DB + uso real del filesystem.
type ManagedFolderView struct {
	ID           string            `json:"id"`
	ShareName    string            `json:"shareName"`
	RelPath      string            `json:"relPath"`
	QuotaBytes   int64             `json:"quotaBytes"`
	UsedBytes    int64             `json:"usedBytes"`
	Generation   int64             `json:"generation"`
	ControlState string            `json:"controlState"`
	Permissions  map[string]string `json:"permissions"`
	OwnerUser    string            `json:"ownerUser"`
	CreatedBy    string            `json:"createdBy"`
	CreatedAt    string            `json:"createdAt"`
}

// CreateManagedFolderInput agrupa los parámetros de creación.
type CreateManagedFolderInput struct {
	ShareName   string
	RelPath     string
	QuotaBytes  int64
	Permissions map[string]string // username -> rw|ro
	OwnerUser   string
	CreatedBy   string
}

// CreateManagedFolder crea una carpeta gestionada de forma transaccional.
func CreateManagedFolder(ctx context.Context, in CreateManagedFolderInput) (*ManagedFolderView, error) {
	// Step 1 — Validaciones
	if err := checkShareName(in.ShareName); err != nil {
		return nil, err
	}
	if err := checkFolderRelPath(in.RelPath); err != nil {
		return nil, err
	}
	if in.OwnerUser == "" {
		in.OwnerUser = in.CreatedBy
	}

	// Step 2 — No debe existir ya (ni en DB ni en disco)
	if existing, _ := dbManagedFolderGetByPath(in.ShareName, in.RelPath); existing != nil {
		return nil, fmt.Errorf("managed folder already exists")
	}

	// Step 3 — Crear subvol + quota (op privilegiada con cinturón de quota).
	createRes := handleOp(Request{
		Op:         "folder.create",
		ShareName:  in.ShareName,
		RelPath:    in.RelPath,
		QuotaBytes: in.QuotaBytes,
	})
	if !createRes.Ok {
		return nil, fmt.Errorf("failed to create folder: %s", createRes.Error)
	}

	// A partir de aquí, cualquier fallo dispara ROLLBACK (borrar subvol).
	rollback := func(reason string) {
		logMsg("CreateManagedFolder: ROLLBACK (%s) — borrando subvol %s/%s", reason, in.ShareName, in.RelPath)
		handleOp(Request{Op: "folder.delete", ShareName: in.ShareName, RelPath: in.RelPath})
	}

	// Step 4 — Aplicar permisos (ACL POSIX). Si alguno falla, rollback.
	for user, perm := range in.Permissions {
		op := ""
		switch perm {
		case "rw":
			op = "folder.set_perm_rw"
		case "ro":
			op = "folder.set_perm_ro"
		default:
			continue // none/"" → no se aplica nada
		}
		res := handleOp(Request{Op: op, ShareName: in.ShareName, RelPath: in.RelPath, Username: user})
		if !res.Ok {
			rollback(fmt.Sprintf("perm %s para %s falló: %s", perm, user, res.Error))
			return nil, fmt.Errorf("failed to apply permission for %s: %s", user, res.Error)
		}
	}

	// Step 5 — Persistir en DB (AL FINAL). Si falla, rollback.
	id, err := dbManagedFolderCreate(in.ShareName, in.RelPath, in.QuotaBytes, in.OwnerUser, in.CreatedBy)
	if err != nil {
		rollback(fmt.Sprintf("dbManagedFolderCreate falló: %v", err))
		return nil, fmt.Errorf("failed to persist managed folder: %w", err)
	}

	// Step 6 — Persistir permisos en DB (best-effort: el FS ya es la verdad).
	for user, perm := range in.Permissions {
		if perm == "rw" || perm == "ro" {
			if perr := dbManagedFolderSetPermission(id, user, perm); perr != nil {
				logMsg("CreateManagedFolder: WARNING no se pudo persistir permiso %s=%s: %v", user, perm, perr)
			}
		}
	}

	logMsg("CreateManagedFolder: creada %s/%s (id=%s quota=%d)", in.ShareName, in.RelPath, id, in.QuotaBytes)
	return buildManagedFolderView(in.ShareName, id)
}

// UpdateManagedFolder cambia quota y/o permisos de una carpeta existente.
// quotaBytes nil = no tocar quota. perms nil = no tocar permisos.
func UpdateManagedFolder(ctx context.Context, folderID string, quotaBytes *int64, perms map[string]string) (*ManagedFolderView, error) {
	folder, err := dbManagedFolderGet(folderID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, fmt.Errorf("managed folder not found")
	}

	// Quota
	if quotaBytes != nil {
		res := handleOp(Request{
			Op:         "folder.set_quota",
			ShareName:  folder.ShareName,
			RelPath:    folder.RelPath,
			QuotaBytes: *quotaBytes,
		})
		if !res.Ok {
			return nil, fmt.Errorf("failed to set quota: %s", res.Error)
		}
		if err := dbManagedFolderSetQuota(folderID, *quotaBytes); err != nil {
			return nil, fmt.Errorf("quota applied but DB update failed: %w", err)
		}
	}

	// Permisos: diff contra lo que hay en DB.
	if perms != nil {
		oldPerms, _ := dbManagedFolderPermissions(folderID)
		if err := applyFolderPermissionDiff(folder.ShareName, folder.RelPath, folderID, oldPerms, perms); err != nil {
			return nil, err
		}
		dbManagedFolderBumpGeneration(folderID)
	}

	return buildManagedFolderView(folder.ShareName, folderID)
}

// applyFolderPermissionDiff aplica el diff de permisos (old∪new) tanto en el
// filesystem (ACL) como en la DB. Espeja applyPermissionDiff de shares.
func applyFolderPermissionDiff(shareName, relPath, folderID string, oldPerms, newPerms map[string]string) error {
	// Union de usuarios.
	users := map[string]bool{}
	for u := range oldPerms {
		users[u] = true
	}
	for u := range newPerms {
		users[u] = true
	}

	for u := range users {
		newP := newPerms[u] // "" si se quita
		op := ""
		switch newP {
		case "rw":
			op = "folder.set_perm_rw"
		case "ro":
			op = "folder.set_perm_ro"
		default:
			op = "folder.remove_perm"
		}
		res := handleOp(Request{Op: op, ShareName: shareName, RelPath: relPath, Username: u})
		if !res.Ok {
			return fmt.Errorf("failed to apply permission for %s: %s", u, res.Error)
		}
		if err := dbManagedFolderSetPermission(folderID, u, newP); err != nil {
			logMsg("applyFolderPermissionDiff: WARNING DB perm %s=%s: %v", u, newP, err)
		}
	}
	return nil
}

// DeleteManagedFolder borra la carpeta (rechaza si no está vacía, vía la op).
func DeleteManagedFolder(ctx context.Context, folderID string) error {
	folder, err := dbManagedFolderGet(folderID)
	if err != nil {
		return err
	}
	if folder == nil {
		return fmt.Errorf("managed folder not found")
	}

	dbManagedFolderSetState(folderID, "deleting")

	res := handleOp(Request{Op: "folder.delete", ShareName: folder.ShareName, RelPath: folder.RelPath})
	if !res.Ok {
		// Si era por no estar vacía, devolver el estado a active.
		dbManagedFolderSetState(folderID, "active")
		if res.Error == "folder_not_empty" {
			return fmt.Errorf("folder_not_empty")
		}
		return fmt.Errorf("failed to delete folder: %s", res.Error)
	}

	if err := dbManagedFolderDelete(folderID); err != nil {
		return fmt.Errorf("subvol deleted but DB cleanup failed: %w", err)
	}
	logMsg("DeleteManagedFolder: borrada %s/%s", folder.ShareName, folder.RelPath)
	return nil
}

// ListManagedFolders devuelve las carpetas de un share, enriquecidas con uso.
func ListManagedFolders(ctx context.Context, shareName string) ([]ManagedFolderView, error) {
	rows, err := dbManagedFoldersListByShare(shareName)
	if err != nil {
		return nil, err
	}
	out := make([]ManagedFolderView, 0, len(rows))
	for _, r := range rows {
		v, verr := buildManagedFolderView(shareName, r.ID)
		if verr != nil {
			logMsg("ListManagedFolders: WARNING no se pudo enriquecer %s: %v", r.ID, verr)
			continue
		}
		out = append(out, *v)
	}
	return out, nil
}

// buildManagedFolderView arma la vista enriquecida: DB + permisos + uso real
// del subvolumen (btrfs qgroup show, mejor esfuerzo).
func buildManagedFolderView(shareName, folderID string) (*ManagedFolderView, error) {
	f, err := dbManagedFolderGet(folderID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("managed folder not found")
	}
	perms, _ := dbManagedFolderPermissions(folderID)
	if perms == nil {
		perms = map[string]string{}
	}

	v := &ManagedFolderView{
		ID:           f.ID,
		ShareName:    f.ShareName,
		RelPath:      f.RelPath,
		QuotaBytes:   f.QuotaBytes,
		UsedBytes:    0,
		Generation:   f.Generation,
		ControlState: f.ControlState,
		Permissions:  perms,
		OwnerUser:    f.OwnerUser,
		CreatedBy:    f.CreatedBy,
		CreatedAt:    f.CreatedAt,
	}

	// Uso real: best-effort vía qgroup. No fatal si falla.
	if sharePath, perr := getManagedSharePath(shareName); perr == nil {
		folderPath := sharePath + "/" + f.RelPath
		v.UsedBytes = folderUsedBytes(folderPath)
	}
	return v, nil
}

// folderUsedBytes obtiene los bytes usados por el subvolumen vía qgroup show.
// Best-effort: devuelve 0 si no se puede determinar.
func folderUsedBytes(folderPath string) int64 {
	opts := CmdOptions{Timeout: 10 * time.Second}
	res, err := runCmd("btrfs", []string{"qgroup", "show", "-f", "--raw", folderPath}, opts)
	if err != nil || res.Code != 0 {
		return 0
	}
	return parseQgroupReferenced(res.Stdout)
}
