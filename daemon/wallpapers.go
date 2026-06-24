package main

// ═══════════════════════════════════════════════════════════════════
// Wallpapers · galería de fondos de escritorio
// ───────────────────────────────────────────────────────────────────
// Cada usuario puede tener VARIOS wallpapers subidos (galería "Mis fondos"),
// no solo uno. Se guardan en <userdata>/<user>/wallpapers/<id>.<ext>.
//
// Wallpapers del sistema (predeterminados): /usr/share/nimos/wallpapers/*.
//
// Endpoints (registrados en http.go):
//   GET    /api/wallpapers              → { system:[{url,name}], user:[{url,name,id}] }
//   POST   /api/user/wallpaper          → sube uno nuevo a la galería (multipart o base64)
//   DELETE /api/wallpapers/:id          → borra un wallpaper del usuario
//   GET    /api/user/wallpaper/:user/:file → sirve el binario
// ═══════════════════════════════════════════════════════════════════

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const systemWallpaperDir = "/usr/share/nimos/wallpapers"

// userWallpaperDir devuelve (y crea) la carpeta de wallpapers de un usuario.
func userWallpaperDir(username string) string {
	p := filepath.Join(getUserDataPath(username), "wallpapers")
	os.MkdirAll(p, 0755)
	return p
}

var wallpaperFileRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+\.(png|jpg|jpeg|webp|gif)$`)

// wallpaperEntry es un elemento de la galería.
type wallpaperEntry struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// listWallpapersHTTP · GET /api/wallpapers
func listWallpapersHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	system := []wallpaperEntry{}
	if entries, err := os.ReadDir(systemWallpaperDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !wallpaperFileRegex.MatchString(e.Name()) {
				continue
			}
			system = append(system, wallpaperEntry{
				URL:  "/api/wallpaper/system/" + e.Name(),
				Name: e.Name(),
			})
		}
	}

	user := []wallpaperEntry{}
	dir := userWallpaperDir(session.Username)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !wallpaperFileRegex.MatchString(e.Name()) {
				continue
			}
			id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			user = append(user, wallpaperEntry{
				URL:  fmt.Sprintf("/api/user/wallpaper/%s/%s", session.Username, e.Name()),
				Name: e.Name(),
				ID:   id,
			})
		}
	}

	jsonOk(w, map[string]interface{}{"system": system, "user": user})
}

// uploadWallpaperHTTP · POST /api/user/wallpaper
// Acepta multipart/form-data (binario, recomendado) o base64-en-JSON (legacy).
// Guarda con un ID único — NO sobreescribe wallpapers anteriores.
func uploadWallpaperHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	const maxWallpaperBytes = 25 * 1024 * 1024 // 25MB límite real

	var imgData []byte
	var ext string

	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxWallpaperBytes); err != nil {
			jsonError(w, 400, "Could not parse upload")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			jsonError(w, 400, "No image file provided")
			return
		}
		defer file.Close()

		data, err := io.ReadAll(io.LimitReader(file, maxWallpaperBytes+1))
		if err != nil {
			jsonError(w, 400, "Could not read image")
			return
		}
		if len(data) > maxWallpaperBytes {
			jsonError(w, 400, "Image too large (max 25MB)")
			return
		}

		ext = wallpaperExtFromName(header.Filename)
		if ext == "" {
			ext = wallpaperExtFromContentType(header.Header.Get("Content-Type"))
		}
		if ext == "" {
			jsonError(w, 400, "Unsupported image format (png, jpg, webp, gif)")
			return
		}
		imgData = data

	} else {
		body, _ := readBody(r)
		dataStr := bodyStr(body, "data")
		if dataStr == "" {
			jsonError(w, 400, "No image data provided")
			return
		}
		wpRegex := regexp.MustCompile(`^data:image/(png|jpeg|jpg|webp|gif);base64,(.+)$`)
		matches := wpRegex.FindStringSubmatch(dataStr)
		if matches == nil {
			jsonError(w, 400, "Invalid image format")
			return
		}
		ext = matches[1]
		if ext == "jpeg" {
			ext = "jpg"
		}
		data, err := decodeBase64(matches[2])
		if err != nil || len(data) > maxWallpaperBytes {
			jsonError(w, 400, "Image too large (max 25MB)")
			return
		}
		imgData = data
	}

	// ID único por timestamp — permite múltiples wallpapers en la galería.
	id := fmt.Sprintf("wp_%d", time.Now().UnixNano())
	fileName := fmt.Sprintf("%s.%s", id, ext)
	dir := userWallpaperDir(session.Username)
	fullPath := filepath.Join(dir, fileName)

	if err := os.WriteFile(fullPath, imgData, 0644); err != nil {
		jsonError(w, 500, "Could not save wallpaper")
		return
	}

	wallpaperURL := fmt.Sprintf("/api/user/wallpaper/%s/%s", session.Username, fileName)

	// Auto-seleccionar el recién subido como wallpaper activo.
	current := getUserPreferences(session.Username)
	current["wallpaper"] = wallpaperURL
	saveUserPreferences(session.Username, current)

	jsonOk(w, map[string]interface{}{"ok": true, "url": wallpaperURL, "id": id})
}

// deleteWallpaperHTTP · DELETE /api/wallpapers/:id
func deleteWallpaperHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/wallpapers/")
	id = strings.Trim(id, "/")
	// El id puede venir URL-encoded o incluir extensión; quedarnos solo con el stem seguro.
	id = strings.TrimSuffix(id, filepath.Ext(id))
	if id == "" || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
		jsonError(w, 400, "Invalid wallpaper id")
		return
	}

	dir := userWallpaperDir(session.Username)
	deleted := false
	deletedURL := ""
	for _, ext := range []string{"png", "jpg", "jpeg", "webp", "gif"} {
		p := filepath.Join(dir, fmt.Sprintf("%s.%s", id, ext))
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err == nil {
				deleted = true
				deletedURL = fmt.Sprintf("/api/user/wallpaper/%s/%s.%s", session.Username, id, ext)
			}
			break
		}
	}

	if !deleted {
		jsonError(w, 404, "Wallpaper not found")
		return
	}

	// Si el wallpaper borrado era el activo, volver al fondo por defecto.
	current := getUserPreferences(session.Username)
	if cur, ok := current["wallpaper"].(string); ok && cur == deletedURL {
		current["wallpaper"] = ""
		saveUserPreferences(session.Username, current)
	}

	jsonOk(w, map[string]interface{}{"ok": true})
}

// serveUserWallpaperHTTP · GET /api/user/wallpaper/:user/:file
// Sin auth — se carga como <img src> sin header Authorization.
func serveUserWallpaperHTTP(w http.ResponseWriter, r *http.Request) {
	rx := regexp.MustCompile(`^/api/user/wallpaper/([a-zA-Z0-9_-]+)/([a-zA-Z0-9_-]+\.(png|jpg|jpeg|webp|gif))$`)
	m := rx.FindStringSubmatch(r.URL.Path)
	if m == nil {
		jsonError(w, 404, "Wallpaper not found")
		return
	}
	username, file, ext := m[1], m[2], m[3]

	wpPath := filepath.Join(userWallpaperDir(username), file)
	serveWallpaperFile(w, wpPath, ext)
}

// serveSystemWallpaperHTTP · GET /api/wallpaper/system/:file
func serveSystemWallpaperHTTP(w http.ResponseWriter, r *http.Request) {
	rx := regexp.MustCompile(`^/api/wallpaper/system/([a-zA-Z0-9_-]+\.(png|jpg|jpeg|webp|gif))$`)
	m := rx.FindStringSubmatch(r.URL.Path)
	if m == nil {
		jsonError(w, 404, "Wallpaper not found")
		return
	}
	file, ext := m[1], m[2]
	serveWallpaperFile(w, filepath.Join(systemWallpaperDir, file), ext)
}

func serveWallpaperFile(w http.ResponseWriter, path, ext string) {
	data, err := os.ReadFile(path)
	if err != nil {
		jsonError(w, 404, "Wallpaper not found")
		return
	}
	mimeTypes := map[string]string{
		"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
		"webp": "image/webp", "gif": "image/gif",
	}
	w.Header().Set("Content-Type", mimeTypes[ext])
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}

// handleWallpapersRoutes · dispatcher para /api/wallpapers y /api/wallpapers/:id
func handleWallpapersRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/wallpapers" && r.Method == "GET":
		listWallpapersHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/wallpapers/") && r.Method == "DELETE":
		deleteWallpaperHTTP(w, r)
	default:
		if requireAuth(w, r) == nil {
			return
		}
		jsonError(w, 404, "Not found")
	}
}
