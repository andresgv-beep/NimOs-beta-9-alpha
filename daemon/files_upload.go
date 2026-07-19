package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// nimChunksDir es el almacén temporal de subidas por trozos, en la raíz del
// share de destino (mismo filesystem que el fichero final → ensamblado barato).
const nimChunksDir = ".nimchunks"

// cleanupChunkDir borra los trozos de una subida y, si .nimchunks quedó
// vacío, también el propio directorio — que no se quede una carpeta
// fantasma en la raíz del share (FIX 2026-07-04: aparecía en Files y por
// Samba). El Remove del padre es best-effort: falla en silencio si otra
// subida tiene trozos dentro.
func cleanupChunkDir(root *os.Root, tmpDirRel string) {
	_ = removeAllIn(root, tmpDirRel)
	_ = root.Remove(nimChunksDir)
}

func handleFileUpload(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	// Legacy multipart upload — ONLY for small files (Notes save, config import, etc.)
	// Large files (20GB+) MUST use /api/files/upload-chunk which streams to disk
	// via io.Copy without RAM buffering. Caddy streams request bodies by default.
	if r.ContentLength > 50*1024*1024 {
		jsonError(w, 413, "File too large. Use chunked upload for files over 50MB.")
		return
	}

	// Hard limit on request body to prevent RAM abuse
	r.Body = http.MaxBytesReader(w, r.Body, 50*1024*1024)

	// Parse multipart — buffer 8MB in RAM max, rest spills to temp files
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		jsonError(w, 400, "Failed to parse upload")
		return
	}

	shareName := r.FormValue("share")
	uploadPath := r.FormValue("path")

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, 400, "No file in upload")
		return
	}
	defer file.Close()

	if shareName == "" {
		jsonError(w, 400, "Missing share")
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

	// Reject filenames with path traversal attempts in the raw input
	rawFilename := header.Filename
	if strings.Contains(rawFilename, "..") || strings.Contains(rawFilename, "/") || strings.Contains(rawFilename, "\\") {
		jsonError(w, 400, "Invalid filename")
		return
	}

	// Sanitize filename
	fileName := sanitizeFileName(rawFilename)
	if fileName == "" || len(fileName) > 255 {
		jsonError(w, 400, "Invalid filename")
		return
	}

	// Reject path traversal in upload path
	if strings.Contains(uploadPath, "..") {
		jsonError(w, 400, "Invalid upload path")
		return
	}

	sharePath := share.Path
	rel, err := relWithinShare(filepath.Join(uploadPath, fileName))
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	root, err := openRootAt(sharePath)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	// Check available space before writing
	availableBytes := getAvailableBytes(sharePath)
	fileSize := header.Size

	logMsg("upload: share=%s path=%s fileSize=%d availableBytes=%d", shareName, sharePath, fileSize, availableBytes)

	// Reject if we know the file is too big
	if fileSize > 0 && availableBytes >= 0 && fileSize > availableBytes {
		jsonError(w, 507, fmt.Sprintf("Not enough space. File: %s, Available: %s",
			fmtSizeFiles(fileSize), fmtSizeFiles(availableBytes)))
		return
	}

	// Also reject if available is 0 (quota full)
	if availableBytes == 0 {
		jsonError(w, 507, "Disk quota exceeded — no space available")
		return
	}

	// Cap write at available space
	maxWrite := availableBytes
	if maxWrite <= 0 {
		maxWrite = 500 * 1024 * 1024 // fallback 500MB if check fails
	}

	// Ensure parent dir exists (vía root, TOCTOU-safe)
	if err := mkdirAllIn(root, relDir(rel), 0755); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	dst, err := root.Create(rel)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	// Write with size limit — never write more than available space
	written, copyErr := io.CopyN(dst, file, maxWrite)
	dst.Close()

	if copyErr != nil && copyErr != io.EOF {
		// Write failed — clean up partial file
		root.Remove(rel)
		jsonError(w, 507, "Write failed — disk full or quota exceeded")
		return
	}

	// Check if the file was truncated (more data remains but we hit the limit)
	if copyErr != io.EOF {
		// We wrote maxWrite bytes but there's more data — file was too big
		root.Remove(rel)
		jsonError(w, 507, fmt.Sprintf("File too large for available space. Written: %s, Available: %s",
			fmtSizeFiles(written), fmtSizeFiles(availableBytes)))
		return
	}

	jsonOk(w, map[string]interface{}{"ok": true, "name": fileName})
}

func handleChunkedUpload(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	// Subidas grandes: este endpoint recibe trozos de 20MB y, en el ÚLTIMO
	// trozo, ensambla el fichero final concatenando todos los trozos en disco
	// (un fichero de 50GB = varios minutos de I/O ANTES de responder). Ni el
	// ReadTimeout global (30s, lectura del cuerpo del trozo — se dispararía si
	// subes por una conexión lenta) ni el WriteTimeout (120s, que cubre el
	// ensamblado previo a la respuesta) deben cortar aquí, o el fichero se
	// ensambla en disco pero el cliente ve un error y cree que la subida falló.
	// Se anulan SOLO para este handler; el resto de la API conserva sus timeouts.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	shareName := r.Header.Get("X-Share")
	uploadPath := r.Header.Get("X-Path")
	rawFilename := r.Header.Get("X-Filename")
	chunkIdx := r.Header.Get("X-Chunk-Index")
	totalChunks := r.Header.Get("X-Total-Chunks")
	totalSizeStr := r.Header.Get("X-Total-Size")

	if shareName == "" || rawFilename == "" || chunkIdx == "" || totalChunks == "" {
		jsonError(w, 400, "Missing chunk headers")
		return
	}

	idx, _ := strconv.Atoi(chunkIdx)
	total, _ := strconv.Atoi(totalChunks)
	totalSize, _ := strconv.ParseInt(totalSizeStr, 10, 64)

	if idx < 0 || total <= 0 {
		jsonError(w, 400, "Invalid chunk index/total")
		return
	}

	// Validate share
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

	// Sanitize
	fileName := sanitizeFileName(rawFilename)
	if fileName == "" || len(fileName) > 255 {
		jsonError(w, 400, "Invalid filename")
		return
	}
	if strings.Contains(uploadPath, "..") {
		jsonError(w, 400, "Invalid upload path")
		return
	}

	sharePath := share.Path
	rel, err := relWithinShare(filepath.Join(uploadPath, fileName))
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	root, err := openRootAt(sharePath)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	// On first chunk, check available space
	if idx == 0 && totalSize > 0 {
		availableBytes := getAvailableBytes(sharePath)
		if availableBytes == 0 {
			jsonError(w, 507, "Disk quota exceeded — no space available")
			return
		}
		if availableBytes > 0 && totalSize > availableBytes {
			jsonError(w, 507, fmt.Sprintf("Not enough space. File: %s, Available: %s",
				fmtSizeFiles(totalSize), fmtSizeFiles(availableBytes)))
			return
		}
	}

	// Store chunks on the destination pool (not system disk), vía root.
	tmpDirRel := joinRel(nimChunksDir, fmt.Sprintf("%x", hashStr(uploadPath+fileName)))
	if err := mkdirAllIn(root, tmpDirRel, 0755); err != nil {
		jsonError(w, 500, "Cannot create chunk dir")
		return
	}

	// Write this chunk to temp file
	chunkRel := joinRel(tmpDirRel, fmt.Sprintf("chunk_%05d", idx))
	dst, err := root.Create(chunkRel)
	if err != nil {
		jsonError(w, 500, "Cannot create chunk file")
		return
	}
	_, err = io.Copy(dst, r.Body)
	dst.Close()
	if err != nil {
		root.Remove(chunkRel)
		jsonError(w, 500, "Chunk write failed")
		return
	}

	// If this is the last chunk, assemble the file
	if idx == total-1 {
		if err := mkdirAllIn(root, relDir(rel), 0755); err != nil {
			jsonError(w, 500, err.Error())
			cleanupChunkDir(root, tmpDirRel)
			return
		}

		finalFile, err := root.Create(rel)
		if err != nil {
			jsonError(w, 500, err.Error())
			cleanupChunkDir(root, tmpDirRel)
			return
		}

		// Concatenate all chunks in order
		var writeErr error
		for i := 0; i < total; i++ {
			cfRel := joinRel(tmpDirRel, fmt.Sprintf("chunk_%05d", i))
			chunk, err := root.Open(cfRel)
			if err != nil {
				writeErr = fmt.Errorf("missing chunk %d", i)
				break
			}
			_, err = io.Copy(finalFile, chunk)
			chunk.Close()
			if err != nil {
				writeErr = fmt.Errorf("write error at chunk %d: %v", i, err)
				break
			}
		}
		finalFile.Close()

		// Cleanup temp chunks
		cleanupChunkDir(root, tmpDirRel)

		if writeErr != nil {
			root.Remove(rel)
			jsonError(w, 500, writeErr.Error())
			return
		}

		jsonOk(w, map[string]interface{}{"ok": true, "name": fileName, "assembled": true})
		return
	}

	// Not the last chunk — acknowledge
	jsonOk(w, map[string]interface{}{"ok": true, "chunk": idx})
}

func hashStr(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// GET /api/files/upload-status?share=X&path=Y&filename=Z
// Returns which chunks already exist for a partial upload (for resume).
func handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	shareName := r.URL.Query().Get("share")
	uploadPath := r.URL.Query().Get("path")
	fileName := sanitizeFileName(r.URL.Query().Get("filename"))

	if shareName == "" || fileName == "" {
		jsonError(w, 400, "Missing share or filename")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonOk(w, map[string]interface{}{"ok": true, "chunks": []int{}, "count": 0})
		return
	}
	defer root.Close()

	tmpDirRel := joinRel(nimChunksDir, fmt.Sprintf("%x", hashStr(uploadPath+fileName)))
	dir, err := root.Open(tmpDirRel)
	if err != nil {
		// No chunks exist — fresh upload
		jsonOk(w, map[string]interface{}{"ok": true, "chunks": []int{}, "count": 0})
		return
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		jsonOk(w, map[string]interface{}{"ok": true, "chunks": []int{}, "count": 0})
		return
	}

	var existing []int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "chunk_") {
			idx, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "chunk_"))
			if err == nil {
				existing = append(existing, idx)
			}
		}
	}
	sort.Ints(existing)

	jsonOk(w, map[string]interface{}{"ok": true, "chunks": existing, "count": len(existing)})
}

// POST /api/files/upload-cancel { share, path, filename }
// Cleans up partial chunks for a cancelled upload.
func handleUploadCancel(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	uploadPath := bodyStr(body, "path")
	fileName := sanitizeFileName(bodyStr(body, "filename"))

	if shareName == "" || fileName == "" {
		jsonError(w, 400, "Missing share or filename")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return
	}

	root, err := openRootAt(share.Path)
	if err != nil {
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}
	defer root.Close()

	tmpDirRel := joinRel(nimChunksDir, fmt.Sprintf("%x", hashStr(uploadPath+fileName)))
	cleanupChunkDir(root, tmpDirRel)

	jsonOk(w, map[string]interface{}{"ok": true})
}
