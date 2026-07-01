package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func filesZip(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, "Invalid request")
		return
	}

	shareName := bodyStr(body, "share")
	zipName := bodyStr(body, "name")

	rawPaths, ok := body["paths"].([]interface{})
	if !ok || len(rawPaths) == 0 || shareName == "" {
		jsonError(w, 400, "Missing share or paths")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if !requireShareMounted(w, share) {
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	// Collect and validate paths (relativas al share)
	var relPaths []string
	var relNames []string
	for _, rp := range rawPaths {
		p, ok := rp.(string)
		if !ok || p == "" {
			continue
		}
		rel, err := relWithinShare(p)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		if rel == "." {
			jsonError(w, 400, "Cannot zip share root")
			return
		}
		if _, err := root.Lstat(rel); err != nil {
			jsonError(w, 404, fmt.Sprintf("Not found: %s", relBase(rel)))
			return
		}
		relPaths = append(relPaths, rel)
		relNames = append(relNames, relBase(rel))
	}

	if len(relPaths) == 0 {
		jsonError(w, 400, "No valid paths")
		return
	}

	// Determine zip file destination (same dir as first path)
	destDirRel := relDir(relPaths[0])
	if zipName == "" {
		if len(relPaths) == 1 {
			zipName = relNames[0] + ".zip"
		} else {
			zipName = "archive.zip"
		}
	}
	if !strings.HasSuffix(strings.ToLower(zipName), ".zip") {
		zipName += ".zip"
	}
	zipName = sanitizeFileName(zipName)
	zipRel := joinRel(destDirRel, zipName)

	// Avoid overwriting — add suffix if exists
	if _, err := root.Lstat(zipRel); err == nil {
		base := strings.TrimSuffix(zipName, ".zip")
		for i := 1; i < 100; i++ {
			candidate := joinRel(destDirRel, fmt.Sprintf("%s (%d).zip", base, i))
			if _, err := root.Lstat(candidate); err != nil {
				zipRel = candidate
				zipName = relBase(candidate)
				break
			}
		}
	}

	// Create zip file (vía root)
	zipFile, err := root.Create(zipRel)
	if err != nil {
		jsonError(w, 500, "Cannot create zip file")
		return
	}

	zw := zip.NewWriter(zipFile)

	var walkErr error
	for i, srcRel := range relPaths {
		baseName := relNames[i]

		entries, err := walkIn(root, srcRel)
		if err != nil {
			walkErr = err
			break
		}

		for _, e := range entries {
			// Skip symlinks (anti-fuga)
			if e.Info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			// Skip the zip file itself
			if e.Rel == zipRel {
				continue
			}
			// Skip .nimchunks
			if e.IsDir && relBase(e.Rel) == ".nimchunks" {
				continue
			}

			// Nombre de entrada dentro del zip: baseName + ruta relativa al srcRel
			var entryName string
			if e.Rel == srcRel {
				entryName = baseName
			} else {
				sub := strings.TrimPrefix(e.Rel, srcRel+"/")
				entryName = baseName + "/" + sub
			}

			if e.IsDir {
				if _, err := zw.Create(entryName + "/"); err != nil {
					walkErr = err
					break
				}
				continue
			}

			header, err := zip.FileInfoHeader(e.Info)
			if err != nil {
				walkErr = err
				break
			}
			header.Name = entryName
			header.Method = zip.Deflate

			writer, err := zw.CreateHeader(header)
			if err != nil {
				walkErr = err
				break
			}

			f, err := root.Open(e.Rel)
			if err != nil {
				walkErr = err
				break
			}
			_, err = io.Copy(writer, f)
			f.Close()
			if err != nil {
				walkErr = err
				break
			}
		}

		if walkErr != nil {
			break
		}
	}

	zw.Close()
	zipFile.Close()

	if walkErr != nil {
		root.Remove(zipRel)
		jsonError(w, 500, fmt.Sprintf("Zip failed: %v", walkErr))
		return
	}

	logMsg("zip: created %s in share %s", zipName, shareName)
	jsonOk(w, map[string]interface{}{"ok": true, "name": zipName})
}

func filesUnzip(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, "Invalid request")
		return
	}

	shareName := bodyStr(body, "share")
	filePath := bodyStr(body, "path")

	if shareName == "" || filePath == "" {
		jsonError(w, 400, "Missing share or path")
		return
	}
	if strings.Contains(filePath, "..") {
		jsonError(w, 400, "Invalid path")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if !requireShareMounted(w, share) {
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	relZip, err := relWithinShare(filePath)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	// Verify it's a zip file
	if !strings.HasSuffix(strings.ToLower(relZip), ".zip") {
		jsonError(w, 400, "Not a zip file")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	// Abrir el zip vía root (TOCTOU-safe) y leerlo como ReaderAt.
	zf, err := root.Open(relZip)
	if err != nil {
		jsonError(w, 404, "Zip file not found")
		return
	}
	zfStat, err := zf.Stat()
	if err != nil {
		zf.Close()
		jsonError(w, 500, "Cannot stat zip")
		return
	}
	zr, err := zip.NewReader(zf, zfStat.Size())
	if err != nil {
		zf.Close()
		jsonError(w, 400, fmt.Sprintf("Cannot open zip: %v", err))
		return
	}
	defer zf.Close()

	// Carpeta destino (relativa), evitando sobrescritura
	baseName := strings.TrimSuffix(relBase(relZip), ".zip")
	baseName = strings.TrimSuffix(baseName, ".ZIP")
	parentRel := relDir(relZip)
	destRel := joinRel(parentRel, baseName)

	if _, err := root.Lstat(destRel); err == nil {
		for i := 1; i < 100; i++ {
			candidate := joinRel(parentRel, fmt.Sprintf("%s (%d)", baseName, i))
			if _, err := root.Lstat(candidate); err != nil {
				destRel = candidate
				break
			}
		}
	}

	if err := mkdirAllIn(root, destRel, 0755); err != nil {
		jsonError(w, 500, "Cannot create destination folder")
		return
	}

	// Presupuesto anti zip-bomb: tope de entradas, por-fichero y total expandido.
	const (
		unzipMaxEntries    = 20000
		unzipMaxFileBytes  = 2 << 30 // 2 GiB por fichero descomprimido
		unzipMaxTotalBytes = 5 << 30 // 5 GiB total expandido en la extracción
	)
	var count, skipped int
	var totalBytes int64
	for _, f := range zr.File {
		if count >= unzipMaxEntries {
			logMsg("unzip: alcanzado el tope de %d entradas; resto omitido", unzipMaxEntries)
			break
		}
		// Defensa Zip Slip nivel 1: rechazar nombres con "..".
		// (Nivel 2: os.Root ancla la escritura al share igualmente.)
		entryRel, rerr := relWithinShare(joinRel(destRel, f.Name))
		if rerr != nil {
			skipped++
			continue
		}

		if f.FileInfo().IsDir() {
			if err := mkdirAllIn(root, entryRel, 0755); err != nil {
				skipped++
			}
			continue
		}

		// Asegurar padre
		if err := mkdirAllIn(root, relDir(entryRel), 0755); err != nil {
			skipped++
			continue
		}

		rc, err := f.Open()
		if err != nil {
			skipped++
			continue
		}
		dst, err := root.Create(entryRel)
		if err != nil {
			rc.Close()
			skipped++
			continue
		}
		// Copia ACOTADA: nunca más de unzipMaxFileBytes por fichero (pilla un
		// fichero que se expande enorme aunque el zip venga pequeño = zip bomb).
		n, copyErr := io.Copy(dst, io.LimitReader(rc, unzipMaxFileBytes+1))
		dst.Close()
		rc.Close()
		if copyErr != nil || n > unzipMaxFileBytes {
			root.Remove(entryRel)
			skipped++
			continue
		}
		totalBytes += n
		if totalBytes > unzipMaxTotalBytes {
			root.Remove(entryRel)
			logMsg("unzip: presupuesto total de %d bytes excedido; abortando extracción", int64(unzipMaxTotalBytes))
			skipped++
			break
		}
		count++
	}

	logMsg("unzip: extracted %d files (%d skipped) to %s in share %s", count, skipped, destRel, shareName)
	resp := map[string]interface{}{"ok": true, "count": count, "folder": relBase(destRel)}
	if skipped > 0 {
		resp["skipped"] = skipped
	}
	jsonOk(w, resp)
}
