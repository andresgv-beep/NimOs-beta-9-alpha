package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// dockerFilesShareName · nombre sintético (prefijo reservado "system:" para no
// colisionar con shares del usuario) de la entrada que expone la carpeta docker
// del pool en el FileManager. Admin-only. Ver filesBrowse y resolveShare.
const dockerFilesShareName = "system:docker"

func filesBrowse(w http.ResponseWriter, r *http.Request, session *DBSession) {
	shareName := r.URL.Query().Get("share")
	subPath := r.URL.Query().Get("path")
	if subPath == "" {
		subPath = "/"
	}

	if shareName == "" {
		// Return list of accessible shares (local + remote)
		sharesRaw, _ := dbSharesListRaw()
		username := session.Username
		role := session.Role
		var accessible []map[string]interface{}
		for _, s := range sharesRaw {
			perm := "none"
			if role == "admin" {
				perm = "rw"
			} else if p, ok := s.Permissions[username]; ok {
				perm = p
			}
			if perm == "rw" || perm == "ro" {
				accessible = append(accessible, map[string]interface{}{
					"name":        s.Name,
					"displayName": s.DisplayName,
					"description": s.Description,
					"permission":  perm,
					"recycleBin":  s.RecycleBin,
				})
			}
		}

		// Add remote mounted shares (admin only for now)
		// NEVER run mountpoint checks here — NFS timeouts would block the entire listing.
		// Just list what's in the DB. Actual mount status is checked when browsing.
		if role == "admin" {
			rows, qerr := db.Query(`SELECT rm.device_id, rm.share_name, rm.mount_point, bd.name
				FROM remote_mounts rm JOIN backup_devices bd ON rm.device_id = bd.id`)
			if qerr == nil {
				defer rows.Close()
				for rows.Next() {
					var devID, shareName, mountPoint, devName string
					rows.Scan(&devID, &shareName, &mountPoint, &devName)
					safeDev := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(devName, "_")
					accessible = append(accessible, map[string]interface{}{
						"name":        fmt.Sprintf("remote:%s/%s", safeDev, shareName),
						"displayName": fmt.Sprintf("%s (%s)", shareName, devName),
						"description": "Carpeta remota",
						"permission":  "rw",
						"remote":      true,
						"deviceName":  devName,
					})
				}
			}
		}

		// Carpeta Docker · config y volúmenes de cada app instalada. NO es un
		// share SMB (las apps son cajas confinadas gestionadas por la UI), pero
		// el admin SÍ debe poder navegarla desde Files para ver/editar la config
		// de cada container. Entrada sintética admin-only. El FileManager corre
		// como root → puede entrar en containers/<app> aunque sean de otro UID.
		if role == "admin" {
			if dp, derr := getDockerPath(); derr == nil && dp != "" {
				accessible = append(accessible, map[string]interface{}{
					"name":        dockerFilesShareName,
					"displayName": "Docker",
					"description": "Config y volúmenes de las apps (admin)",
					"permission":  "rw",
					"system":      true,
				})
			}
		}

		if accessible == nil {
			accessible = []map[string]interface{}{}
		}
		jsonOk(w, map[string]interface{}{"shares": accessible})
		return
	}

	share, err := resolveShare(shareName)
	if err != nil || share == nil {
		jsonError(w, 404, "Shared folder not found")
		return
	}
	if !requireShareMounted(w, share) {
		return
	}

	perm := getSharePermission(session, share)
	if perm == "none" {
		jsonError(w, 403, "Access denied")
		return
	}

	rel, err := relWithinShare(subPath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	dir, err := root.Open(rel)
	if err != nil {
		jsonError(w, 400, "Cannot read directory")
		return
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		jsonError(w, 400, "Cannot read directory")
		return
	}

	var files []map[string]interface{}
	for _, e := range entries {
		// Ocultar la papelera de reciclaje de la navegación normal: se gestiona
		// desde su propia vista, no como una carpeta más.
		if rel == "." && e.Name() == recycleBinDir {
			continue
		}
		info, err := e.Info()
		size := int64(0)
		var modified interface{}
		modified = nil
		if err == nil {
			size = info.Size()
			modified = info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
		}
		files = append(files, map[string]interface{}{
			"name":        e.Name(),
			"isDirectory": e.IsDir(),
			"size":        size,
			"modified":    modified,
		})
	}

	// Sort: directories first, then alphabetical
	sort.Slice(files, func(i, j int) bool {
		iDir := files[i]["isDirectory"].(bool)
		jDir := files[j]["isDirectory"].(bool)
		if iDir != jDir {
			return iDir
		}
		return strings.ToLower(files[i]["name"].(string)) < strings.ToLower(files[j]["name"].(string))
	})

	if files == nil {
		files = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{
		"files":      files,
		"path":       subPath,
		"share":      shareName,
		"permission": perm,
	})
}

func filesMkdir(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	dirPath := bodyStr(body, "path")
	dirName := bodyStr(body, "name")

	if shareName == "" || dirName == "" {
		jsonError(w, 400, "Missing share or name")
		return
	}
	if strings.Contains(dirName, "..") || strings.Contains(dirName, "/") || strings.Contains(dirName, "\\") {
		jsonError(w, 400, "Invalid directory name")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Shared folder not found")
		return
	}
	if !requireShareMounted(w, share) {
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	rel, err := relWithinShare(filepath.Join(dirPath, dirName))
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	if err := mkdirAllIn(root, rel, 0755); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}

func filesRename(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	oldPath := bodyStr(body, "oldPath")
	newPath := bodyStr(body, "newPath")

	if shareName == "" || oldPath == "" || newPath == "" {
		jsonError(w, 400, "Missing params")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Shared folder not found")
		return
	}
	if !requireShareMounted(w, share) {
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	relOld, err := relWithinShare(oldPath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	relNew, err := relWithinShare(newPath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if relOld == "." || relNew == "." {
		jsonError(w, 400, "Cannot rename share root")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	if err := renameIn(root, relOld, relNew); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}
