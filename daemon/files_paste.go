package main

import (
	"fmt"
	"net/http"
	"os"
)

func filesPaste(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	srcShareName := bodyStr(body, "srcShare")
	srcPath := bodyStr(body, "srcPath")
	destShareName := bodyStr(body, "destShare")
	destPath := bodyStr(body, "destPath")
	action := bodyStr(body, "action")

	if srcShareName == "" || srcPath == "" || destShareName == "" || destPath == "" {
		jsonError(w, 400, "Missing params")
		return
	}

	srcShare, _ := resolveShare(srcShareName)
	destShare, _ := resolveShare(destShareName)
	if srcShare == nil || destShare == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if !requireShareMounted(w, destShare) {
		return
	}

	if getSharePermission(session, destShare) != "rw" {
		jsonError(w, 403, "Write access denied on destination")
		return
	}
	srcPerm := getSharePermission(session, srcShare)
	if srcPerm == "none" {
		jsonError(w, 403, "Read access denied on source")
		return
	}

	relSrc, err := relWithinShare(srcPath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	relDest, err := relWithinShare(destPath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if relSrc == "." {
		jsonError(w, 400, "Cannot move/copy share root")
		return
	}

	srcRoot, err := openRootAt(srcShare.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open source share")
		return
	}
	defer srcRoot.Close()

	sameShare := srcShare.Path == destShare.Path
	var destRoot *os.Root
	if sameShare {
		destRoot = srcRoot
	} else {
		destRoot, err = openRootAt(destShare.Path)
		if err != nil {
			jsonError(w, 500, "Cannot open destination share")
			return
		}
		defer destRoot.Close()
	}

	srcInfo, statErr := srcRoot.Lstat(relSrc)
	if statErr != nil {
		jsonError(w, 404, "Source not found")
		return
	}

	// ── CUT (move) ──────────────────────────────────────────────────────
	if action == "cut" {
		if sameShare {
			// Mismo share/pool: rename atómico (mismo inode, instantáneo).
			if err := renameIn(srcRoot, relSrc, relDest); err != nil {
				jsonError(w, 500, err.Error())
				return
			}
			jsonOk(w, map[string]interface{}{"ok": true})
			return
		}

		// Cross-share: copia segura + borrado. Verificar espacio antes (SEC-3).
		srcSize := pasteSrcSize(srcRoot, relSrc, srcInfo)
		if !checkDestSpace(w, destShare.Path, srcSize) {
			return
		}
		if err := crossRootCopyTree(srcRoot, relSrc, destRoot, relDest); err != nil {
			// Limpieza parcial del destino ante fallo
			removeAllIn(destRoot, relDest)
			jsonError(w, 500, "Copy failed during cross-share move")
			return
		}
		if err := removeAllIn(srcRoot, relSrc); err != nil {
			logMsg("WARNING paste cut: copia OK pero borrado de origen falló: %s", err)
		}
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	// ── COPY ────────────────────────────────────────────────────────────
	srcSize := pasteSrcSize(srcRoot, relSrc, srcInfo)
	if !checkDestSpace(w, destShare.Path, srcSize) {
		return
	}
	if sameShare {
		if err := copyTreeIn(srcRoot, relSrc, relDest); err != nil {
			removeAllIn(srcRoot, relDest)
			jsonError(w, 500, "Copy failed")
			return
		}
	} else {
		if err := crossRootCopyTree(srcRoot, relSrc, destRoot, relDest); err != nil {
			removeAllIn(destRoot, relDest)
			jsonError(w, 500, "Copy failed")
			return
		}
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}

// pasteSrcSize devuelve el tamaño del origen (fichero o árbol) de forma
// TOCTOU-safe vía root, para los checks de quota. Reemplaza el shell-out a `du`.
func pasteSrcSize(root *os.Root, rel string, info os.FileInfo) int64 {
	if info.IsDir() {
		sz, err := dirSizeIn(root, rel)
		if err != nil {
			return 0
		}
		return sz
	}
	return info.Size()
}

// checkDestSpace verifica que destSharePath tenga hueco para srcSize bytes.
// Escribe el error HTTP y devuelve false si no cabe. availableBytes==-1
// (desconocido) permite la operación.
func checkDestSpace(w http.ResponseWriter, destSharePath string, srcSize int64) bool {
	availableBytes := getAvailableBytes(destSharePath)
	if availableBytes == 0 {
		jsonError(w, 507, "Disk quota exceeded — no space available on destination")
		return false
	}
	if srcSize > 0 && availableBytes > 0 && srcSize > availableBytes {
		jsonError(w, 507, fmt.Sprintf("Not enough space. Source: %s, Available: %s",
			fmtSizeFiles(srcSize), fmtSizeFiles(availableBytes)))
		return false
	}
	return true
}
