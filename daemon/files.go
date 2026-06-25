package main

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ═══════════════════════════════════
// File Manager HTTP handlers
// ═══════════════════════════════════

func handleFilesRoutes(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	method := r.Method

	// Upload and download are special (binary, streaming)
	if urlPath == "/api/files/upload" && method == "POST" {
		handleFileUpload(w, r)
		return
	}
	if urlPath == "/api/files/upload-chunk" && method == "POST" {
		handleChunkedUpload(w, r)
		return
	}
	if urlPath == "/api/files/upload-status" && method == "GET" {
		handleUploadStatus(w, r)
		return
	}
	if urlPath == "/api/files/upload-cancel" && method == "POST" {
		handleUploadCancel(w, r)
		return
	}
	if strings.HasPrefix(urlPath, "/api/files/download") && method == "GET" {
		handleFileDownload(w, r)
		return
	}
	// CRIT-008: Generate short-lived download token (replaces session token in URLs)
	if urlPath == "/api/files/download-token" && method == "POST" {
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		body, _ := readBody(r)
		share := bodyStr(body, "share")
		path := bodyStr(body, "path")
		if share == "" || path == "" {
			jsonError(w, 400, "share and path required")
			return
		}
		token, err := dbDownloadTokenCreate(session.Username, session.Role, share, path)
		if err != nil {
			jsonError(w, 500, "Failed to create download token")
			return
		}
		jsonOk(w, map[string]interface{}{"token": token})
		return
	}

	session := requireAuth(w, r)
	if session == nil {
		return
	}

	switch {
	case urlPath == "/api/files/recyclebin/list" && method == "GET":
		recycleBinListHTTP(w, r, session)
	case urlPath == "/api/files/recyclebin/restore" && method == "POST":
		recycleBinRestoreHTTP(w, r, session)
	case urlPath == "/api/files/recyclebin/delete" && method == "POST":
		recycleBinDeleteHTTP(w, r, session)
	case urlPath == "/api/files/recyclebin/empty" && method == "POST":
		recycleBinEmptyHTTP(w, r, session)
	case strings.HasPrefix(urlPath, "/api/files") && method == "GET":
		filesBrowse(w, r, session)
	case urlPath == "/api/files/mkdir" && method == "POST":
		filesMkdir(w, r, session)
	case urlPath == "/api/files/delete" && method == "POST":
		filesDelete(w, r, session)
	case urlPath == "/api/files/rename" && method == "POST":
		filesRename(w, r, session)
	case urlPath == "/api/files/paste" && method == "POST":
		filesPaste(w, r, session)
	case urlPath == "/api/files/zip" && method == "POST":
		filesZip(w, r, session)
	case urlPath == "/api/files/unzip" && method == "POST":
		filesUnzip(w, r, session)
	default:
		jsonError(w, 404, "Not found")
	}
}

// ═══════════════════════════════════
// Permission helpers
// ═══════════════════════════════════

func getSharePermission(session *DBSession, share *ResolvedShare) string {
	// Remote shares: admin gets rw (NFS mount is already authenticated)
	if share.IsRemote() {
		if session.Role == "admin" {
			return "rw"
		}
		return "ro"
	}
	if session.Role == "admin" {
		return "rw"
	}
	if share.Permissions != nil {
		if p, ok := share.Permissions[session.Username]; ok {
			return p
		}
	}
	return "none"
}

// resolveShare looks up a share first in the local DB, then in remote_mounts.
// Returns a share-like map with at least "name" and "path" fields.
func resolveShare(name string) (*ResolvedShare, error) {
	// Try local DB first
	share, err := dbSharesGetRaw(name)
	if err == nil && share != nil {
		return &ResolvedShare{
			Name:        share.Name,
			DisplayName: share.DisplayName,
			Path:        share.Path,
			Pool:        share.Pool,
			Permissions: share.Permissions,
		}, nil
	}

	// Try remote mounts — name format: "remote:<device>/<share>"
	if strings.HasPrefix(name, "remote:") {
		parts := strings.SplitN(strings.TrimPrefix(name, "remote:"), "/", 2)
		if len(parts) == 2 {
			rows, err := db.Query(`SELECT rm.mount_point, rm.share_name, bd.name
				FROM remote_mounts rm JOIN backup_devices bd ON rm.device_id = bd.id`)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var mountPoint, shareName, devName string
					rows.Scan(&mountPoint, &shareName, &devName)
					safeDev := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(devName, "_")
					if safeDev == parts[0] && shareName == parts[1] {
						return &ResolvedShare{
							Name:        name,
							DisplayName: fmt.Sprintf("%s (%s)", shareName, devName),
							Path:        mountPoint,
							Pool:        "remote",
							Remote:      &RemoteInfo{Host: devName, DeviceName: safeDev},
						}, nil
					}
				}
			}
		}
	}

	// Carpeta Docker sintética (admin-only · ver filesBrowse). Apunta SOLO a
	// docker/containers (config/volumen de cada app), NO a la raíz docker: así
	// el usuario ve una carpeta por app y NUNCA las tripas internas de Docker
	// (data-root/overlayfs, containerd, stacks). La jaula openRootAt confina el
	// browse a este subdir → ni las ve ni puede salir a ellas. El gate admin lo
	// aplica getSharePermission (Permissions nil + no-admin → none) → 403.
	if name == dockerFilesShareName {
		dp, derr := getDockerPath()
		if derr != nil || dp == "" {
			return nil, fmt.Errorf("docker path not available: %v", derr)
		}
		pool := ""
		if rel := strings.TrimPrefix(dp, nimosPoolsDir+"/"); rel != dp {
			if i := strings.IndexByte(rel, '/'); i > 0 {
				pool = rel[:i]
			}
		}
		return &ResolvedShare{
			Name:        name,
			DisplayName: "Docker",
			Path:        strings.TrimRight(dp, "/") + "/containers",
			Pool:        pool,
		}, nil
	}

	return nil, fmt.Errorf("share not found: %s", name)
}

// isPathOnMountedPool checks that the path is actually on a mounted pool,
// not on the root filesystem. This prevents writes to the system disk
// when a pool is destroyed but shares still exist in the DB.
func isPathOnMountedPool(path string) bool {
	if path == "" {
		return false
	}
	// Debe estar bajo /nimos/pools/
	if !strings.HasPrefix(path, nimosPoolsDir+"/") {
		return false
	}

	// INVARIANTE FUNDAMENTAL DE NimOS: si el pool no está montado, NUNCA se
	// escribe — los datos jamás deben caer al disco de sistema. (Regression
	// 13/06: este check usaba `findmnt --target path`, que RESUELVE HACIA ARRIBA
	// hasta el primer mount existente. Con el pool desmontado, ese mount es `/`
	// (sda2) → devolvía el source de la raíz y, según la jerarquía, podía pasar
	// el filtro → archivos al disco de sistema. Fix: comprobar que el MOUNTPOINT
	// DEL POOL es un punto de montaje real del kernel, sin resolución hacia arriba.)
	//
	// El pool es /nimos/pools/<nombre>; extraemos ese segundo nivel exacto.
	poolMount := poolMountFromPath(path)
	if poolMount == "" {
		return false
	}

	// `mountpoint -q <ruta>` pregunta al kernel: ¿es ESTA ruta exacta un punto
	// de montaje? No resuelve hacia arriba. Si el pool no está montado → false.
	if _, ok := runSafe("mountpoint", "-q", poolMount); !ok {
		return false
	}
	return true
}

// poolMountFromPath devuelve el mountpoint del pool (/nimos/pools/<nombre>) que
// contiene `path`, o "" si path no está bajo /nimos/pools/. Función pura: NO
// extrae más allá del segundo nivel, así una ruta profunda dentro del pool
// resuelve al mountpoint del pool, no a un subdirectorio.
func poolMountFromPath(path string) string {
	if !strings.HasPrefix(path, nimosPoolsDir+"/") {
		return ""
	}
	rest := strings.TrimPrefix(path, nimosPoolsDir+"/")
	poolName := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		poolName = rest[:i]
	}
	if poolName == "" {
		return ""
	}
	return nimosPoolsDir + "/" + poolName
}

// requireShareMounted checks if a share's pool is mounted, returns error response if not
func requireShareMounted(w http.ResponseWriter, share *ResolvedShare) bool {
	// Remote shares: quick check — try to stat the directory (non-blocking)
	// Don't use mountpoint command which can hang on dead NFS
	if share.IsRemote() {
		done := make(chan bool, 1)
		go func() {
			_, err := os.Stat(share.Path)
			done <- (err == nil)
		}()
		select {
		case ok := <-done:
			if ok {
				return true
			}
		case <-time.After(2 * time.Second):
			// Timed out — NFS is dead
		}
		jsonError(w, 503, "Remote share not available — device may be offline")
		return false
	}
	if err := assertPoolWritable(share.Path); err != nil {
		jsonError(w, 503, "Storage no disponible: "+err.Error())
		return false
	}
	return true
}

// ═══════════════════════════════════
// GET /api/files?share=name&path=/subdir
// ═══════════════════════════════════

// ═══════════════════════════════════
// POST /api/files/mkdir
// ═══════════════════════════════════

// ═══════════════════════════════════
// POST /api/files/delete
// ═══════════════════════════════════

func filesDelete(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	filePath := bodyStr(body, "path")

	if shareName == "" || filePath == "" {
		jsonError(w, 400, "Missing share or path")
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

	// Camino TOCTOU-safe: ruta relativa + os.Root anclado al share.
	rel, err := relWithinShare(filePath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if rel == "." {
		jsonError(w, 400, "Cannot delete share root")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	if _, serr := root.Lstat(rel); serr != nil {
		jsonError(w, 404, "File not found")
		return
	}

	// Si el share tiene papelera activada, MOVER a .papelera/ en vez de borrar.
	// (Si lo borrado ya está dentro de la papelera, moveToRecycleBin lo elimina
	// definitivamente.)
	if isRecycleBinEnabled(shareName) {
		if err := moveToRecycleBin(root, rel); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "recycled": true})
		return
	}

	if err := removeAllIn(root, rel); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}

// ═══════════════════════════════════
// POST /api/files/rename
// ═══════════════════════════════════

// ═══════════════════════════════════
// POST /api/files/paste (copy or move)
// ═══════════════════════════════════

// ═══════════════════════════════════
// POST /api/files/upload (multipart)
// ═══════════════════════════════════

// ═══════════════════════════════════
// POST /api/files/upload-chunk (streaming chunked upload)
// ═══════════════════════════════════
//
// Receives file in chunks. Each request sends one chunk with headers:
//   X-Share, X-Path, X-Filename, X-Chunk-Index, X-Total-Chunks, X-Total-Size
// Body = raw binary chunk data (no multipart)

// ═══════════════════════════════════
// POST /api/files/zip — compress selected files/folders into a .zip
// ═══════════════════════════════════
// Body: { share, paths: ["/file1", "/dir1", ...], name?: "archive.zip" }
// Creates the zip in the same directory as the first path.

// ═══════════════════════════════════
// POST /api/files/unzip — extract a .zip file
// ═══════════════════════════════════
// Body: { share, path: "/path/to/file.zip" }
// Extracts into a folder with the same name (without .zip) in the same directory.

// ═══════════════════════════════════
// GET /api/files/download?share=...&path=...&token=...
// ═══════════════════════════════════
