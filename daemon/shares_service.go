package main

// ═══════════════════════════════════════════════════════════════════════
// SHARES SERVICE · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Esta capa contiene la lógica de negocio del módulo Shares.
// Llamada desde shares_http.go. Llama a:
//   · storageService.ListPools()       (capa storage para resolver pool)
//   · dbShares* (models.go)             (persistencia SQLite)
//   · runCmd (executor low-level)       (operaciones BTRFS / setfacl)
//   · handleOp (request handler legacy)  (operaciones de permisos)
//
// Separación de responsabilidades:
//   · http.go    → parseo de request + auth + jsonError
//   · service.go → validación de dominio + ops BTRFS + persistencia
//   · types.go   → estructuras de datos + helpers puros
//
// Esta capa NO conoce http.ResponseWriter ni *http.Request.
// ═══════════════════════════════════════════════════════════════════════

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// POOL RESOLUTION · resuelve nombre de pool a *Pool del stack storage V2
// ═══════════════════════════════════════════════════════════════════════

// resolveSharePool busca un pool por nombre en el stack V2 (SQLite + BTRFS).
// Si poolName está vacío, devuelve el primer pool managed disponible.
//
// Nota: el frontend manda el `name` del pool (legible), no el `id` UUID.
// Esto es coherente con la UX (el usuario ve "data", no "550e8400-...").
func resolveSharePool(ctx context.Context, poolName string) (*Pool, error) {
	if storageService == nil {
		return nil, fmt.Errorf("storage service not initialized")
	}
	pools, err := storageService.ListPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing pools: %w", err)
	}
	if len(pools) == 0 {
		return nil, fmt.Errorf("no storage pools available. Create a pool in Storage Manager first")
	}
	if poolName != "" {
		for _, p := range pools {
			if p.Name == poolName {
				return p, nil
			}
		}
		return nil, fmt.Errorf("pool '%s' not found", poolName)
	}
	// Sin pool específico → primer pool managed
	for _, p := range pools {
		if p.ControlState == ControlStateManaged {
			return p, nil
		}
	}
	return pools[0], nil
}

// ═══════════════════════════════════════════════════════════════════════
// CREATE SHARE · lógica completa de creación
// ═══════════════════════════════════════════════════════════════════════

// CreateShareInput agrupa los parámetros de creación.
type CreateShareInput struct {
	Name        string
	Description string
	PoolName    string
	QuotaBytes  int64
	CreatedBy   string
}

// CreateShareResult lo que devolvemos al cliente tras crear.
type CreateShareResult struct {
	Name string
	Path string
	Pool string
}

// shareNameRegex valida caracteres permitidos en nombre de share.
var shareNameRegex = regexp.MustCompile(`[^a-zA-Z0-9_\- ]`)

// validateShareName valida nombre + devuelve safeName (lowercase, dashes).
// Retorna error con mensaje user-friendly si es inválido.
func validateShareName(name string) (safeName string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("Folder name required")
	}
	if len(name) > 64 {
		return "", fmt.Errorf("Folder name too long (max 64 characters)")
	}
	if shareNameRegex.MatchString(name) {
		return "", fmt.Errorf("Name can only contain letters, numbers, spaces, -, _")
	}
	return strings.ToLower(strings.ReplaceAll(name, " ", "-")), nil
}

// CreateShare crea un nuevo share:
//   1. Valida nombre + comprueba que no exista
//   2. Resuelve pool destino
//   3. Verifica que el pool está montado
//   4. Crea subvolumen BTRFS
//   5. Aplica quota qgroup (si > 0)
//   6. Llama a handleOp share.create (permisos filesystem)
//   7. setfacl para usuario nimos (apps internas)
//   8. Registra en SQLite con permisos rw para creator
//
// Errores devueltos son user-friendly (van directos a la UI).
func CreateShare(ctx context.Context, input CreateShareInput) (*CreateShareResult, error) {
	// Step 1 — Validar nombre
	safeName, err := validateShareName(input.Name)
	if err != nil {
		return nil, err
	}

	// Step 2 — Comprobar que no existe
	if existing, _ := dbSharesGetRaw(safeName); existing != nil {
		return nil, fmt.Errorf("Shared folder already exists")
	}

	// Step 3 — Resolver pool destino
	pool, err := resolveSharePool(ctx, input.PoolName)
	if err != nil {
		return nil, err
	}
	mountPoint := pool.MountPoint
	volumeName := pool.Name

	// Step 4 — Verificar pool montado
	if !isPathOnMountedPool(mountPoint) {
		return nil, fmt.Errorf("Storage pool is not mounted. Check Storage Manager for pool status.")
	}

	folderPath := filepath.Join(mountPoint, "shares", safeName)

	// Step 5 — Crear subvolumen BTRFS (si no existe ya)
	if err := createBtrfsSubvolIfMissing(folderPath, input.QuotaBytes); err != nil {
		logMsg("ERROR share.create BTRFS subvolume '%s': %s", folderPath, err)
		return nil, fmt.Errorf("Failed to create BTRFS subvolume: %s", err)
	}

	// Step 6 — Crear permisos filesystem vía daemon ops
	daemonResult := handleOp(Request{
		Op:        "share.create",
		ShareName: safeName,
		PoolPath:  mountPoint,
	})
	if !daemonResult.Ok {
		logMsg("ERROR share.create handleOp failed for '%s': %s", safeName, daemonResult.Error)
		return nil, fmt.Errorf("Failed to create share: %s", daemonResult.Error)
	}

	// Step 7 — ACL para usuario nimos (NimTorrent etc. necesitan escribir)
	aclOpts := CmdOptions{Timeout: 5 * time.Second}
	runCmd("setfacl", []string{"-m", "u:nimos:rwx", folderPath}, aclOpts)
	runCmd("setfacl", []string{"-d", "-m", "u:nimos:rwx", folderPath}, aclOpts)

	// Step 8 — Registrar en SQLite
	if err := dbSharesCreate(safeName, input.Name, input.Description, folderPath, volumeName, volumeName, input.CreatedBy); err != nil {
		logMsg("ERROR dbSharesCreate '%s': %s", safeName, err)
		return nil, err
	}

	// Permiso rw para el creador
	dbShareSetPermission(safeName, input.CreatedBy, "rw")

	return &CreateShareResult{
		Name: safeName,
		Path: folderPath,
		Pool: volumeName,
	}, nil
}

// createBtrfsSubvolIfMissing crea el subvolumen BTRFS si no existe.
// Si ya existe (caso: DB perdida pero datos quedaron), lo respeta.
// Aplica quota qgroup si quotaBytes > 0.
func createBtrfsSubvolIfMissing(folderPath string, quotaBytes int64) error {
	opts := CmdOptions{Timeout: 15 * time.Second}

	existing, _ := runCmd("btrfs", []string{"subvolume", "show", folderPath}, opts)
	if existing.Stdout != "" && existing.Code == 0 {
		// Ya existe → respetar
		return nil
	}

	// Garantizar que el directorio padre existe (ej: /nimos/pools/X/shares).
	// btrfs subvolume create falla con "Could not open: No such file or directory"
	// si el padre no existe. mkdir -p es idempotente.
	parentDir := filepath.Dir(folderPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", parentDir, err)
	}

	// Crear subvolumen (auto-mounted al estar dentro del filesystem BTRFS)
	_, err := runCmd("btrfs", []string{"subvolume", "create", folderPath}, opts)
	if err != nil {
		return err
	}
	logMsg("Created BTRFS subvolume '%s'", folderPath)

	// Aplicar quota si se especificó
	if quotaBytes > 0 {
		quotaStr := fmt.Sprintf("%d", quotaBytes)
		res, qerr := runCmd("btrfs", []string{"qgroup", "limit", quotaStr, folderPath}, opts)
		if qerr != nil || res.Code != 0 {
			// No es fatal para la creación del subvol (ya existe), pero se
			// registra de verdad en vez de tragarlo. El caller decide.
			logMsg("WARNING: btrfs qgroup limit %d en '%s' falló: %v (stderr: %s) — ¿quota habilitada en el pool?",
				quotaBytes, folderPath, qerr, res.Stderr)
		} else {
			logMsg("Set BTRFS quota %d bytes on subvolume '%s'", quotaBytes, folderPath)
		}
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════
// UPDATE SHARE · campos parciales, quota, permisos, app permissions
// ═══════════════════════════════════════════════════════════════════════

// UpdateShareInput agrupa los cambios a aplicar.
// Todos los campos son punteros para distinguir "no enviado" de "vacío".
type UpdateShareInput struct {
	Description    *string
	RecycleBin     *bool
	Quota          *int64                  // nil = no tocar; 0 = quitar quota; >0 = nuevo límite
	Permissions    map[string]string       // username → "rw"|"ro"|"none"; nil = no tocar
	AppPermissions []map[string]interface{} // []{appId, uid, permission}; nil = no tocar
}

// UpdateShare aplica cambios parciales a un share existente.
// Coordina:
//   1. Update SQLite (description, recycleBin)
//   2. Update quota BTRFS (qgroup limit)
//   3. Diff y aplicación de permisos de usuario
//   4. Diff y aplicación de permisos de app
func UpdateShare(ctx context.Context, target string, input UpdateShareInput) error {
	share, err := dbSharesGetRaw(target)
	if err != nil || share == nil {
		return fmt.Errorf("Shared folder not found")
	}

	// Step 1 — Campos simples en SQLite
	if input.Description != nil || input.RecycleBin != nil {
		su := ShareUpdate{
			Description: input.Description,
			RecycleBin:  input.RecycleBin,
		}
		dbSharesUpdate(target, su)
	}

	// Step 2 — Quota BTRFS
	if input.Quota != nil {
		if err := updateBtrfsQuota(ctx, share, target, *input.Quota); err != nil {
			logMsg("WARNING: updateBtrfsQuota failed for '%s': %s", target, err)
			// No fatal · seguimos con el resto
		}
	}

	// Step 3 — Permisos de usuario
	if input.Permissions != nil {
		applyPermissionDiff(target, share.Permissions, input.Permissions)
	}

	// Step 4 — Permisos de app
	if input.AppPermissions != nil {
		applyAppPermissionDiff(target, share.AppPermissions, input.AppPermissions)
	}

	return nil
}

// updateBtrfsQuota aplica un nuevo límite qgroup al subvolumen.
// quotaBytes == 0 → eliminar quota.
// quotaBytes > 0  → aplicar nuevo límite.
func updateBtrfsQuota(ctx context.Context, share *DBShare, target string, quotaBytes int64) error {
	sharPool := share.Pool
	if sharPool == "" {
		sharPool = share.Volume
	}
	pool, err := resolveSharePool(ctx, sharPool)
	if err != nil {
		return err
	}

	subvolPath := filepath.Join(pool.MountPoint, "shares", target)
	opts := CmdOptions{Timeout: 10 * time.Second}

	// Garantizar que la quota está habilitada en el pool. Sin esto,
	// `qgroup limit` falla con "quotas not enabled" (idempotente si ya está).
	if err := ensureBtrfsQuotaEnabled(pool.MountPoint); err != nil {
		return fmt.Errorf("no se pudo habilitar quota en el pool %s: %w", pool.Name, err)
	}

	if quotaBytes > 0 {
		res, err := runCmd("btrfs", []string{"qgroup", "limit", fmt.Sprintf("%d", quotaBytes), subvolPath}, opts)
		if err != nil || res.Code != 0 {
			return fmt.Errorf("btrfs qgroup limit %d en %s falló: %v (stderr: %s)", quotaBytes, subvolPath, err, res.Stderr)
		}
		logMsg("Updated BTRFS quota to %d bytes on '%s'", quotaBytes, subvolPath)
	} else {
		res, err := runCmd("btrfs", []string{"qgroup", "limit", "none", subvolPath}, opts)
		if err != nil || res.Code != 0 {
			return fmt.Errorf("btrfs qgroup limit none en %s falló: %v (stderr: %s)", subvolPath, err, res.Stderr)
		}
		logMsg("Removed BTRFS quota on '%s'", subvolPath)
	}
	return nil
}

// applyPermissionDiff calcula y aplica el delta de permisos de usuario.
// Para cada usuario presente en oldPerms o newPerms:
//   · Si oldPerm == newPerm: skip
//   · Si newPerm == "none" o vacío: handleOp share.remove_user
//   · Si newPerm == "rw":  handleOp share.add_user_rw
//   · Si newPerm == "ro":  handleOp share.add_user_ro
// Y siempre persiste en SQLite con dbShareSetPermission.
func applyPermissionDiff(target string, oldPerms, newPerms map[string]string) {
	if oldPerms == nil {
		oldPerms = map[string]string{}
	}

	// Universo de usuarios = old ∪ new
	allUsers := map[string]bool{}
	for u := range oldPerms {
		allUsers[u] = true
	}
	for u := range newPerms {
		allUsers[u] = true
	}

	for username := range allUsers {
		oldPerm := oldPerms[username]
		newPerm := newPerms[username]
		if newPerm == "" {
			newPerm = "none"
		}
		if oldPerm == newPerm {
			continue
		}

		switch newPerm {
		case "none":
			handleOp(Request{Op: "share.remove_user", ShareName: target, Username: username})
		case "rw":
			handleOp(Request{Op: "share.add_user_rw", ShareName: target, Username: username})
		case "ro":
			handleOp(Request{Op: "share.add_user_ro", ShareName: target, Username: username})
		}

		dbShareSetPermission(target, username, newPerm)
	}
}

// applyAppPermissionDiff coordina cambios en permisos de apps:
//   1. Apps en oldPerms pero NO en newPerms → remove
//   2. Apps en newPerms → add (o re-add si cambió permission)
func applyAppPermissionDiff(target string, oldApps []AppPermission, newApps []map[string]interface{}) {
	// 1. Eliminar apps viejas no presentes en la nueva lista
	for _, oldApp := range oldApps {
		found := false
		for _, na := range newApps {
			if uid, err := checkUid(na["uid"]); err == nil && uid == oldApp.Uid {
				found = true
				break
			}
		}
		if !found {
			handleOp(Request{Op: "share.remove_app", ShareName: target, AppId: oldApp.AppId, Uid: oldApp.Uid})
		}
	}

	// 2. Añadir/actualizar nuevas apps
	for _, na := range newApps {
		perm, _ := na["permission"].(string)
		appId, _ := na["appId"].(string)
		if uid, err := checkUid(na["uid"]); err == nil && perm != "" {
			handleOp(Request{Op: "share.add_app", ShareName: target, AppId: appId, Uid: uid, Permission: perm})
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════
// DELETE SHARE · destruye subvolumen + permisos + DB
// ═══════════════════════════════════════════════════════════════════════

// DeleteShare elimina un share por completo:
//   1. Remove permisos filesystem (handleOp share.delete)
//   2. Destruye subvolumen BTRFS (con todos los datos)
//   3. Elimina de SQLite
//
// El paso 2 puede fallar (subvolumen ya eliminado, error de FS). En ese
// caso se LOG warning y se sigue con el paso 3 para mantener consistencia.
// (DB sin filesystem es mejor que filesystem sin DB.)
func DeleteShare(ctx context.Context, target string) error {
	share, _ := dbSharesGetRaw(target)
	if share == nil {
		return fmt.Errorf("Shared folder not found")
	}

	// Step 1 — Remover permisos filesystem
	handleOp(Request{Op: "share.delete", ShareName: target})

	// Step 2 — Destruir subvolumen BTRFS (best-effort)
	sharPool := share.Pool
	if sharPool == "" {
		sharPool = share.Volume
	}
	if pool, err := resolveSharePool(ctx, sharPool); err == nil {
		subvolPath := filepath.Join(pool.MountPoint, "shares", target)
		destroyBtrfsSubvolIfExists(subvolPath, target)
	}

	// Step 3 — Eliminar de SQLite
	dbSharesDelete(target)
	return nil
}

// destroyBtrfsSubvolIfExists elimina un subvolumen BTRFS si existe.
// Best-effort: si falla, solo log warning, no error.
func destroyBtrfsSubvolIfExists(subvolPath, shareName string) {
	opts := CmdOptions{Timeout: 15 * time.Second}

	existing, _ := runCmd("btrfs", []string{"subvolume", "show", subvolPath}, opts)
	if existing.Code != 0 {
		return // No existe, nada que hacer
	}

	_, delErr := runCmd("btrfs", []string{"subvolume", "delete", subvolPath}, opts)
	if delErr != nil {
		logMsg("WARNING: failed to delete BTRFS subvolume '%s': %s", subvolPath, delErr)
		return
	}
	logMsg("Deleted BTRFS subvolume '%s' for share '%s'", subvolPath, shareName)
}

// ═══════════════════════════════════════════════════════════════════════
// LIST / ENRICH · obtener shares + enriquecer con datos filesystem
// ═══════════════════════════════════════════════════════════════════════

// ListShares devuelve todos los shares con sus ShareView enriquecidos.
// Si filterByUser != "", filtra a los que ese usuario tiene permiso.
// Para admin: devuelve todos (filterByUser == "").
//
// El "enriquecimiento" añade datos runtime que no están en SQLite:
//   · Quota / Used / Available (BTRFS qgroup + df)
//   · FileStats por categoría
//   · PoolType + MountPoint
func ListShares(ctx context.Context, filterByUser string) ([]ShareView, error) {
	dbShares, err := dbSharesListRaw()
	if err != nil {
		return nil, err
	}

	views := buildShareViews(ctx, dbShares)

	if filterByUser == "" {
		return views, nil
	}

	// Filtrar: solo shares donde este usuario tiene permiso
	filtered := make([]ShareView, 0, len(views))
	for _, v := range views {
		if perm, ok := v.Permissions[filterByUser]; ok && (perm == "rw" || perm == "ro") {
			filtered = append(filtered, v)
		}
	}
	return filtered, nil
}

// buildShareViews enriquece []DBShare a []ShareView leyendo datos runtime
// del filesystem para cada share: quota qgroup, uso, available, file stats.
//
// Tolerancia: si un pool desapareció o un subvolumen no responde,
// devuelve el ShareView con solo los datos de SQLite y campos runtime a 0.
// La UI debe distinguir "0 = sin datos" de "0 = vacío" pero esto es
// preferible a fallar el listado completo.
func buildShareViews(ctx context.Context, dbShares []DBShare) []ShareView {
	opts := CmdOptions{Timeout: 5 * time.Second}
	views := make([]ShareView, 0, len(dbShares))

	for _, s := range dbShares {
		v := ShareView{DBShare: s}

		sharPool := s.Pool
		if sharPool == "" {
			sharPool = s.Volume
		}
		if sharPool == "" || s.Name == "" {
			views = append(views, v)
			continue
		}

		pool, err := resolveSharePool(ctx, sharPool)
		if err != nil {
			// Pool desaparecido → share huérfano, sin metadata fs
			views = append(views, v)
			continue
		}

		subvolPath := filepath.Join(pool.MountPoint, "shares", s.Name)
		v.PoolType = "btrfs"
		v.MountPoint = subvolPath

		// BTRFS qgroup info
		enrichWithBtrfsQuota(&v, subvolPath, opts)

		// Available space
		enrichWithDfAvailable(&v, subvolPath, opts)

		// File stats por categoría
		v.FileStats = getFileStatsByCategory(subvolPath)

		views = append(views, v)
	}

	return views
}

// enrichWithBtrfsQuota parsea `btrfs subvolume show` y rellena Quota+Used.
// Si falla silenciosamente, deja los campos a 0.
func enrichWithBtrfsQuota(v *ShareView, subvolPath string, opts CmdOptions) {
	res, err := runCmd("btrfs", []string{"subvolume", "show", subvolPath}, opts)
	if err != nil {
		return
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Limit referenced:") {
			valStr := strings.TrimSpace(strings.TrimPrefix(line, "Limit referenced:"))
			if valStr != "-" && valStr != "none" {
				v.Quota = parseHumanBytes(valStr)
			}
		}
		if strings.HasPrefix(line, "Usage referenced:") {
			valStr := strings.TrimSpace(strings.TrimPrefix(line, "Usage referenced:"))
			v.Used = parseHumanBytes(valStr)
		}
	}
}

// enrichWithDfAvailable rellena v.Available desde `df -B1 --output=avail`.
func enrichWithDfAvailable(v *ShareView, subvolPath string, opts CmdOptions) {
	res, err := runCmd("df", []string{"-B1", "--output=avail", subvolPath}, opts)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) > 1 {
		fmt.Sscanf(strings.TrimSpace(lines[1]), "%d", &v.Available)
	}
}
