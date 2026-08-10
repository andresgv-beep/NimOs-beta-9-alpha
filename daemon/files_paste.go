package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
)

type pasteRenameFunc func(*os.Root, string, string) error
type pasteRemoveFunc func(*os.Root, string) error

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
	if action != "copy" && action != "cut" {
		jsonError(w, 400, "Invalid paste action")
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
	if action == "cut" && srcPerm != "rw" {
		jsonError(w, 403, "Write access denied on source")
		return
	}
	if action == "cut" && !requireShareMounted(w, srcShare) {
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
	if _, destErr := destRoot.Lstat(relDest); destErr == nil {
		jsonError(w, http.StatusConflict, "Destination already exists")
		return
	} else if !errors.Is(destErr, os.ErrNotExist) {
		jsonError(w, 500, "Cannot inspect destination")
		return
	}

	// ── CUT (move) ──────────────────────────────────────────────────────
	if action == "cut" {
		copied, moveErr := movePasteEntry(
			srcRoot, relSrc, srcInfo, destRoot, relDest, destShare.Path, sameShare,
			renameIn, removeAllIn,
		)
		if moveErr != nil {
			if copied {
				// La copia de destino está completa. No se elimina: podría ser la
				// única copia íntegra si el borrado del árbol origen quedó a medias.
				logMsg("WARNING paste cut parcial: destino completo, origen no retirado: %s", moveErr)
				jsonResponse(w, http.StatusConflict, map[string]interface{}{
					"ok": false, "partial": true, "copied": true,
					"error": "El archivo se copió, pero no se pudo eliminar el original. No se ha perdido ningún dato.",
				})
				return
			}
			removeAllIn(destRoot, relDest)
			jsonError(w, 500, moveErr.Error())
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "fallbackCopy": copied})
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

// movePasteEntry mueve una entrada sin asumir que "mismo share" implica
// "mismo filesystem". renameat es el camino rápido y atómico; EXDEV activa
// copia completa + borrado. copied=true junto a err significa que el destino
// quedó completo pero no se pudo retirar el origen: nunca debe borrarse esa
// copia de destino porque protege frente a un borrado parcial del árbol fuente.
func movePasteEntry(
	srcRoot *os.Root,
	relSrc string,
	srcInfo os.FileInfo,
	destRoot *os.Root,
	relDest string,
	destSharePath string,
	sameRoot bool,
	renameFn pasteRenameFunc,
	removeFn pasteRemoveFunc,
) (copied bool, err error) {
	if sameRoot {
		if renameErr := renameFn(srcRoot, relSrc, relDest); renameErr == nil {
			return false, nil
		} else if !errors.Is(renameErr, syscall.EXDEV) {
			return false, renameErr
		}
	}

	// Las primitivas de copia no siguen symlinks por seguridad. En un move no
	// podemos simplemente omitirlos y borrar después el árbol original: eso
	// perdería esas entradas. Rechazamos la operación antes de crear el destino.
	if err := ensureMoveTreeCopyable(srcRoot, relSrc); err != nil {
		return false, err
	}

	srcSize := pasteSrcSize(srcRoot, relSrc, srcInfo)
	if spaceErr := destSpaceError(destSharePath, srcSize); spaceErr != nil {
		return false, spaceErr
	}

	if sameRoot {
		err = copyTreeIn(srcRoot, relSrc, relDest)
	} else {
		err = crossRootCopyTree(srcRoot, relSrc, destRoot, relDest)
	}
	if err != nil {
		return false, fmt.Errorf("copy failed during move: %w", err)
	}
	if err := removeFn(srcRoot, relSrc); err != nil {
		return true, fmt.Errorf("source cleanup failed after verified copy: %w", err)
	}
	return true, nil
}

func ensureMoveTreeCopyable(root *os.Root, rel string) error {
	entries, err := walkIn(root, rel)
	if err != nil {
		return fmt.Errorf("cannot inspect source before move: %w", err)
	}
	for _, entry := range entries {
		if entry.Info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("move blocked: source contains symbolic link %q", entry.Rel)
		}
	}
	return nil
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
	if err := destSpaceError(destSharePath, srcSize); err != nil {
		jsonError(w, 507, err.Error())
		return false
	}
	return true
}

func destSpaceError(destSharePath string, srcSize int64) error {
	availableBytes := getAvailableBytes(destSharePath)
	if availableBytes == 0 {
		return errors.New("Disk quota exceeded — no space available on destination")
	}
	if srcSize > 0 && availableBytes > 0 && srcSize > availableBytes {
		return fmt.Errorf("Not enough space. Source: %s, Available: %s",
			fmtSizeFiles(srcSize), fmtSizeFiles(availableBytes))
	}
	return nil
}
