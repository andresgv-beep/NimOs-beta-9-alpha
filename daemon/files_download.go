package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func handleFileDownload(w http.ResponseWriter, r *http.Request) {
	// CRIT-008: Try one-time download token first (short-lived, no browser history leak)
	dlToken := r.URL.Query().Get("dl")
	if dlToken != "" {
		username, role, dlShare, dlPath, err := dbDownloadTokenConsume(dlToken)
		if err != nil {
			jsonError(w, 401, "Invalid or expired download token")
			return
		}
		// Token is valid and consumed — serve the file
		share, _ := resolveShare(dlShare)
		if share == nil {
			jsonError(w, 404, "Share not found")
			return
		}
		// SEC-2: preservar el role del token para que un admin conserve su
		// acceso rw automático (antes se descartaba y caía al map de perms).
		tempSession := &DBSession{Username: username, Role: role}
		if getSharePermission(tempSession, share) == "none" {
			jsonError(w, 403, "Access denied")
			return
		}
		rel, pathErr := relWithinShare(dlPath)
		if pathErr != nil {
			jsonError(w, 400, pathErr.Error())
			return
		}
		root, oerr := openRootAt(share.Path)
		if oerr != nil {
			jsonError(w, 500, "Cannot open share")
			return
		}
		defer root.Close()
		serveFileDownload(w, r, root, rel)
		return
	}

	// Fallback: Auth via session token (legacy — will be removed)
	token := r.URL.Query().Get("token")
	if token == "" {
		token = getBearerToken(r)
	}
	if token == "" {
		jsonError(w, 401, "Not authenticated")
		return
	}
	hashed := sha256Hex(token)
	session, err := dbSessionGet(hashed)
	if err != nil {
		jsonError(w, 401, "Not authenticated")
		return
	}

	shareName := r.URL.Query().Get("share")
	filePath := r.URL.Query().Get("path")
	if shareName == "" || filePath == "" {
		jsonError(w, 400, "Missing params")
		return
	}

	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Share not found")
		return
	}
	if getSharePermission(session, share) == "none" {
		jsonError(w, 403, "Access denied")
		return
	}

	rel, pathErr := relWithinShare(filePath)
	if pathErr != nil {
		jsonError(w, 400, pathErr.Error())
		return
	}

	root, oerr := openRootAt(share.Path)
	if oerr != nil {
		jsonError(w, 500, "Cannot open share")
		return
	}
	defer root.Close()

	serveFileDownload(w, r, root, rel)
}

// serveFileDownload sends a file to the client with appropriate headers.
// Opera vía os.Root: rel es la ruta relativa al share, ya validada.
func serveFileDownload(w http.ResponseWriter, r *http.Request, root *os.Root, rel string) {
	stat, err := root.Stat(rel)
	if err != nil {
		jsonError(w, 404, "File not found")
		return
	}

	fileName := relBase(rel)
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext != "" {
		ext = ext[1:] // remove dot
	}

	mimeTypes := map[string]string{
		"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif",
		"webp": "image/webp", "svg": "image/svg+xml", "bmp": "image/bmp", "ico": "image/x-icon",
		"mp4": "video/mp4", "webm": "video/webm", "ogg": "video/ogg", "mov": "video/quicktime",
		"mkv": "video/x-matroska", "avi": "video/x-msvideo", "ogv": "video/ogg",
		"mp3": "audio/mpeg", "wav": "audio/wav", "flac": "audio/flac", "aac": "audio/aac",
		"m4a": "audio/mp4", "wma": "audio/x-ms-wma", "opus": "audio/opus",
		"pdf": "application/pdf",
		"txt": "text/plain", "md": "text/plain", "log": "text/plain", "csv": "text/plain",
		"json": "application/json", "xml": "text/xml", "yml": "text/yaml", "yaml": "text/yaml",
		"js": "text/javascript", "jsx": "text/javascript", "ts": "text/javascript",
		"py": "text/plain", "sh": "text/plain", "css": "text/css", "html": "text/html",
		"c": "text/plain", "cpp": "text/plain", "h": "text/plain", "java": "text/plain",
		"rs": "text/plain", "go": "text/plain", "rb": "text/plain", "php": "text/plain",
		"sql": "text/plain", "toml": "text/plain", "ini": "text/plain", "conf": "text/plain",
		"srt": "text/plain", "sub": "text/plain", "ass": "text/plain", "vtt": "text/vtt",
		"zip": "application/zip", "tar": "application/x-tar", "gz": "application/gzip",
		"7z": "application/x-7z-compressed", "rar": "application/x-rar-compressed",
	}

	contentType := "application/octet-stream"
	if ct, ok := mimeTypes[ext]; ok {
		contentType = ct
	}
	isDownload := contentType == "application/octet-stream"

	// Range request support (audio/video seeking)
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		re := regexp.MustCompile(`bytes=(\d+)-(\d*)`)
		m := re.FindStringSubmatch(rangeHeader)
		if m != nil {
			start, _ := strconv.ParseInt(m[1], 10, 64)
			end := stat.Size() - 1
			if m[2] != "" {
				end, _ = strconv.ParseInt(m[2], 10, 64)
			}
			chunkSize := end - start + 1

			f, err := root.Open(rel)
			if err != nil {
				jsonError(w, 500, "Cannot open file")
				return
			}
			defer f.Close()
			f.Seek(start, 0)

			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, stat.Size()))
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", chunkSize))
			w.WriteHeader(206)
			io.CopyN(w, f, chunkSize)
			return
		}
	}

	// Full file
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Accept-Ranges", "bytes")
	if isDownload {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	}
	w.WriteHeader(200)

	f, err := root.Open(rel)
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(w, f)
}
