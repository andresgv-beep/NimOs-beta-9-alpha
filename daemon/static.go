package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ═══════════════════════════════════
// Static file serving + Torrent proxy
// ═══════════════════════════════════

const (
	installDir = "/opt/nimos"
	distDir    = "/opt/nimos/dist"
	publicDir  = "/opt/nimos/public"
)

var mimeTypes = map[string]string{
	".html": "text/html", ".js": "application/javascript", ".css": "text/css",
	".json": "application/json", ".png": "image/png", ".jpg": "image/jpeg",
	".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml",
	".ico": "image/x-icon", ".woff": "font/woff", ".woff2": "font/woff2",
	".ttf": "font/ttf", ".webp": "image/webp", ".mp4": "video/mp4",
	".webm": "video/webm", ".map": "application/json",
}

// Serve static files from dist/ (React/Svelte frontend)
func serveStatic(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path

	// App icons from public/
	if strings.HasPrefix(urlPath, "/app-icons/") {
		iconName := filepath.Base(urlPath)
		if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+\.(svg|png|jpg|jpeg|webp|ico)$`, iconName); !matched {
			http.Error(w, "Invalid", 400)
			return
		}
		iconPath := filepath.Join(publicDir, "app-icons", iconName)
		if data, err := os.ReadFile(iconPath); err == nil {
			ext := strings.ToLower(filepath.Ext(iconName))
			w.Header().Set("Content-Type", mimeTypes[ext])
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Write(data)
			return
		}
		http.Error(w, "Not found", 404)
		return
	}

	// Try dist/ first
	if _, err := os.Stat(distDir); err == nil {
		filePath := filepath.Join(distDir, urlPath)
		if urlPath == "/" {
			filePath = filepath.Join(distDir, "index.html")
		}

		// Security: prevent path traversal
		absFile, _ := filepath.Abs(filePath)
		absDist, _ := filepath.Abs(distDir)
		if !strings.HasPrefix(absFile, absDist) {
			http.Error(w, "Forbidden", 403)
			return
		}

		// If file doesn't exist or is directory, serve index.html (SPA routing)
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			filePath = filepath.Join(distDir, "index.html")
		}

		if data, err := os.ReadFile(filePath); err == nil {
			ext := strings.ToLower(filepath.Ext(filePath))
			ct := mimeTypes[ext]
			if ct == "" {
				ct = "application/octet-stream"
			}
			cacheControl := "public, max-age=31536000, immutable"
			if ext == ".html" {
				// No caching for HTML with user-specific prefs
				cacheControl = "no-store, no-cache, must-revalidate"
				// ── Security Headers ──
				// CSP: pragmatic policy compatible with SvelteKit SPA
				// Note: Trusted Types incompatible with SvelteKit (uses innerHTML internally)
				// Note: script-src 'unsafe-inline' required for SvelteKit hydration
				// Future: migrate to nonce-based CSP when SvelteKit supports it
				w.Header().Set("Content-Security-Policy",
					"default-src 'self'; "+
						"script-src 'self' 'unsafe-inline'; "+
						"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
						"img-src 'self' data: blob: https://raw.githubusercontent.com; "+
						"connect-src 'self' https://raw.githubusercontent.com; "+
						"font-src 'self' https://fonts.gstatic.com; "+
						"frame-src 'self' http://127.0.0.1:* http://localhost:*; "+
						"frame-ancestors 'self'; "+
						"object-src 'none'; "+
						"base-uri 'self'")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("X-Frame-Options", "DENY")
				w.Header().Set("Referrer-Policy", "no-referrer")
				// Server-side prefs injection
				data = injectUserPrefs(r, data)
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Cache-Control", cacheControl)
			w.Write(data)
			return
		}
	}

	// Public dir fallback
	pubFile := filepath.Join(publicDir, urlPath)
	absPub, _ := filepath.Abs(pubFile)
	absPublic, _ := filepath.Abs(publicDir)
	if strings.HasPrefix(absPub, absPublic) {
		if data, err := os.ReadFile(pubFile); err == nil {
			ext := strings.ToLower(filepath.Ext(pubFile))
			ct := mimeTypes[ext]
			if ct == "" {
				ct = "application/octet-stream"
			}
			w.Header().Set("Content-Type", ct)
			w.Write(data)
			return
		}
	}

	http.Error(w, "Not found", 404)
}

// ═══════════════════════════════════
// Torrent proxy to NimTorrent daemon (:9091)
// ═══════════════════════════════════

func handleTorrentProxy(w http.ResponseWriter, r *http.Request) {
	// Auth check — torrent needs app access
	session := requireAppAccess(w, r, "nimtorrent")
	if session == nil {
		return
	}

	urlPath := r.URL.Path

	// Special: torrent file upload (multipart)
	if urlPath == "/api/torrent/upload" && r.Method == "POST" {
		handleTorrentUploadGo(w, r, session)
		return
	}

	// Regular proxy to NimTorrent
	daemonPath := strings.Replace(urlPath, "/api/torrent", "", 1)
	if daemonPath == "" || daemonPath == "/" {
		daemonPath = "/torrents"
	}
	// torrentd expects /torrent/add, /torrent/pause, etc.
	// but /torrents and /stats are root-level
	if daemonPath != "/torrents" && daemonPath != "/stats" && daemonPath != "/settings" && daemonPath != "/save" && !strings.HasPrefix(daemonPath, "/torrent/") {
		daemonPath = "/torrent" + daemonPath
	}

	// Read body
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))

	// ── SEGURIDAD: NimTorrent SOLO puede escribir dentro de carpetas
	// compartidas existentes, NUNCA libre al disco del sistema. ──
	// En /torrent/add el frontend manda `share` (nombre), no un path.
	// Aquí lo resolvemos al path real, verificamos permiso rw y que esté
	// en un pool montado, y reescribimos el body con el save_path real.
	// Si el share no existe / no hay permiso / no está montado → 403/404.
	if daemonPath == "/torrent/add" && len(body) > 0 {
		newBody, ok := resolveTorrentSavePath(w, session, body)
		if !ok {
			return // resolveTorrentSavePath ya escribió el error
		}
		body = newBody
	}

	// Proxy to NimTorrent
	client := &http.Client{Timeout: 30 * time.Second}
	var proxyReq *http.Request
	var err error

	targetURL := fmt.Sprintf("http://127.0.0.1:9091%s", daemonPath)
	if len(body) > 0 {
		proxyReq, err = http.NewRequest(r.Method, targetURL, strings.NewReader(string(body)))
	} else {
		proxyReq, err = http.NewRequest(r.Method, targetURL, nil)
	}
	if err != nil {
		jsonError(w, 500, "Proxy error")
		return
	}
	proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := client.Do(proxyReq)
	if err != nil {
		jsonError(w, 503, "Torrent daemon not running")
		return
	}
	defer resp.Body.Close()

	// Forward response
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// resolveTorrentSavePath toma el body JSON de /torrent/add, lee el campo
// `share` (nombre de carpeta compartida elegida en la UI), lo resuelve a su
// path real en disco y reescribe el body con un `save_path` validado.
//
// SEGURIDAD (requisito de diseño): NimTorrent jamás escribe libre al disco
// del sistema. El destino DEBE ser una carpeta compartida existente sobre la
// que el usuario tiene permiso de escritura, y que esté en un pool montado.
// Los torrents se guardan en <share>/torrents (subcarpeta creada si falta).
//
// Devuelve (nuevoBody, true) si todo valida; si no, escribe el error HTTP
// apropiado y devuelve (nil, false).
func resolveTorrentSavePath(w http.ResponseWriter, session *DBSession, body []byte) ([]byte, bool) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		jsonError(w, 400, "JSON inválido")
		return nil, false
	}

	shareName, _ := req["share"].(string)
	if shareName == "" {
		jsonError(w, 400, "Falta la carpeta de destino (share)")
		return nil, false
	}
	// Las carpetas de sistema (prefijo "system:", p.ej. la carpeta Docker que
	// se navega en Files) NUNCA son destino de descargas — meterían los
	// torrents dentro de las tripas de Docker. Se rechazan explícitamente.
	if strings.HasPrefix(shareName, "system:") {
		jsonError(w, 400, "No se puede descargar en una carpeta del sistema")
		return nil, false
	}

	// Resolver share → path real
	share, err := resolveShare(shareName)
	if err != nil || share == nil {
		jsonError(w, 404, "Carpeta compartida no encontrada")
		return nil, false
	}

	// Permiso de escritura
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Sin permiso de escritura en esa carpeta")
		return nil, false
	}

	// Debe estar en un pool montado (no en el disco de sistema).
	// Las carpetas remotas se permiten (ya validadas por el montaje NFS).
	if !share.IsRemote() && !isPathOnMountedPool(share.Path) {
		jsonError(w, 409, "La carpeta no está en un pool montado")
		return nil, false
	}

	// Destino final: <share>/torrents — confinado dentro del share.
	savePath := filepath.Join(share.Path, "torrents")
	// Defensa en profundidad: tras Join+Clean, el destino debe seguir
	// colgando del path del share (nunca escaparse con ../).
	if savePath != share.Path && !strings.HasPrefix(savePath, share.Path+string(filepath.Separator)) {
		jsonError(w, 400, "Ruta de destino inválida")
		return nil, false
	}
	os.MkdirAll(savePath, 0755)

	// Reescribir body: quitar `share`, fijar `save_path` real validado.
	delete(req, "share")
	req["save_path"] = savePath
	out, err := json.Marshal(req)
	if err != nil {
		jsonError(w, 500, "Error serializando petición")
		return nil, false
	}
	return out, true
}

// Torrent file upload: parse multipart, save .torrent to disk, forward as JSON to NimTorrent
func handleTorrentUploadGo(w http.ResponseWriter, r *http.Request, session *DBSession) {
	// Parse multipart (max 50MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		jsonError(w, 400, "Failed to parse upload")
		return
	}

	// SEGURIDAD: el destino se deriva de la carpeta compartida elegida
	// (campo `share`), nunca de un path libre. Mismas reglas que /add.
	shareName := r.FormValue("share")
	if shareName == "" {
		jsonError(w, 400, "Falta la carpeta de destino (share)")
		return
	}
	if strings.HasPrefix(shareName, "system:") {
		jsonError(w, 400, "No se puede descargar en una carpeta del sistema")
		return
	}
	share, err := resolveShare(shareName)
	if err != nil || share == nil {
		jsonError(w, 404, "Carpeta compartida no encontrada")
		return
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Sin permiso de escritura en esa carpeta")
		return
	}
	if !share.IsRemote() && !isPathOnMountedPool(share.Path) {
		jsonError(w, 409, "La carpeta no está en un pool montado")
		return
	}
	savePath := filepath.Join(share.Path, "torrents")
	if savePath != share.Path && !strings.HasPrefix(savePath, share.Path+string(filepath.Separator)) {
		jsonError(w, 400, "Ruta de destino inválida")
		return
	}
	os.MkdirAll(savePath, 0755)

	file, header, err := r.FormFile("torrent")
	if err != nil {
		jsonError(w, 400, "No .torrent file found")
		return
	}
	defer file.Close()

	// Save to temp ON THE POOL (not /var — keeps ProtectSystem=strict intact).
	// El temp y el destino comparten filesystem (mismo pool).
	tmpDir, err := torrentTmpDir()
	if err != nil {
		jsonError(w, 503, "Storage pool not mounted — cannot stage torrent")
		return
	}
	safeName := regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(header.Filename, "_")
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), safeName))

	dst, err := os.Create(tmpPath)
	if err != nil {
		jsonError(w, 500, "Failed to save temp file")
		return
	}
	io.Copy(dst, file)
	dst.Close()

	// Forward to NimTorrent as JSON
	postData, _ := json.Marshal(map[string]string{
		"file":      tmpPath,
		"save_path": savePath,
	})

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post("http://127.0.0.1:9091/torrent/add", "application/json", strings.NewReader(string(postData)))

	// Cleanup temp file
	defer os.Remove(tmpPath)

	if err != nil {
		jsonError(w, 503, "Torrent daemon not running")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if len(respBody) > 0 {
		w.Write(respBody)
	} else {
		w.Write([]byte(`{"ok":true}`))
	}
}

// injectUserPrefs reads the session cookie, loads the user's preferences,
// and injects them as a JSON tag into the HTML before </head>.
//
// SECURITY HARDENING:
// 1. Whitelist: only 16 visual keys pass through
// 2. Value validation: type + range + charset checks per key
// 3. Size limit: JSON > 8KB = use defaults (prevent DoS)
// 4. Injection method: <script type="application/json"> not window global
// 5. Go json.Marshal escapes <, >, & as \u003c etc (prevents script breakout)
// 6. Double injection guard: checks if tag already present
// 7. Safe fallback: any error = return original HTML unmodified
// 8. Telemetry: logs when prefs are discarded for security reasons

const prefsTagID = "__nimos_prefs_v1"

// safeString strips control characters and enforces max length.
func safeString(s string, maxLen int) string {
	if len(s) > maxLen {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return -1 // strip control characters
		}
		return r
	}, s)
}

func injectUserPrefs(r *http.Request, html []byte) []byte {
	// Double injection guard
	if bytes.Contains(html, []byte(prefsTagID)) {
		return html
	}

	cookie, err := r.Cookie("nimos_token")
	if err != nil || cookie.Value == "" {
		return html
	}

	session, err := dbSessionGet(sha256Hex(cookie.Value))
	if err != nil || session == nil {
		return html
	}

	allPrefs := getUserPreferences(session.Username)
	if len(allPrefs) == 0 {
		return html
	}

	// WHITELIST + VALIDATE each key
	safePrefs := map[string]interface{}{}
	discarded := 0

	// String enums
	if v, ok := allPrefs["theme"].(string); ok && (v == "dark" || v == "light") {
		safePrefs["theme"] = v
	}
	if v, ok := allPrefs["accentColor"].(string); ok {
		if s := safeString(v, 20); s != "" {
			safePrefs["accentColor"] = s
		}
	}
	if v, ok := allPrefs["taskbarSize"].(string); ok && (v == "small" || v == "medium" || v == "large") {
		safePrefs["taskbarSize"] = v
	}
	if v, ok := allPrefs["taskbarPosition"].(string); ok && (v == "bottom" || v == "top" || v == "left" || v == "right") {
		safePrefs["taskbarPosition"] = v
	}
	// Wallpaper: path only, no protocols, max 200 chars, no control chars
	if v, ok := allPrefs["wallpaper"].(string); ok {
		s := safeString(v, 200)
		if s != "" && !strings.Contains(s, "javascript:") && !strings.Contains(s, "data:") &&
			!strings.Contains(s, "<") && !strings.Contains(s, ">") {
			safePrefs["wallpaper"] = s
		} else if v != "" {
			discarded++
		}
	}
	if v, ok := allPrefs["playlistName"].(string); ok {
		if s := safeString(v, 100); s != "" {
			safePrefs["playlistName"] = s
		}
	}

	// Booleans
	for _, key := range []string{"autoHideTaskbar", "clock24", "showDesktopIcons", "showWidgets"} {
		if v, ok := allPrefs[key].(bool); ok {
			safePrefs[key] = v
		}
	}

	// Numbers with range
	for _, spec := range []struct {
		key      string
		min, max float64
	}{
		{"glowIntensity", 0, 100},
		{"textScale", 50, 200},
		{"widgetScale", 50, 200},
	} {
		if v, ok := allPrefs[spec.key].(float64); ok && v >= spec.min && v <= spec.max {
			safePrefs[spec.key] = v
		} else if ok {
			discarded++
		}
	}

	// Complex objects: visibleWidgets
	if v, ok := allPrefs["visibleWidgets"].(map[string]interface{}); ok && len(v) <= 20 {
		clean := map[string]interface{}{}
		for k, val := range v {
			if len(k) <= 30 {
				if b, ok := val.(bool); ok {
					clean[k] = b
				}
			}
		}
		safePrefs["visibleWidgets"] = clean
	}
	// widgetLayout: array de {id, col, row, size?} · validación estricta.
	// AÑADIDO jun 2026: su ausencia en esta whitelist hacía que la copia
	// inyectada llegara sin layout, el frontend la tomara por completa y
	// machacara la disposición de widgets del usuario en cada recarga.
	if v, ok := allPrefs["widgetLayout"].([]interface{}); ok && len(v) <= 64 {
		clean := []interface{}{}
		valid := true
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				valid = false
				break
			}
			id, ok := m["id"].(string)
			if !ok || safeString(id, 32) == "" {
				valid = false
				break
			}
			col, okC := m["col"].(float64)
			row, okR := m["row"].(float64)
			if !okC || !okR || col < -256 || col > 256 || row < -256 || row > 256 {
				valid = false
				break
			}
			entry := map[string]interface{}{"id": safeString(id, 32), "col": col, "row": row}
			if sz, ok := m["size"].([]interface{}); ok && len(sz) == 2 {
				sw, okW := sz[0].(float64)
				sh, okH := sz[1].(float64)
				if okW && okH && sw >= 1 && sw <= 8 && sh >= 1 && sh <= 8 {
					entry["size"] = []interface{}{sw, sh}
				}
			}
			clean = append(clean, entry)
		}
		if valid {
			safePrefs["widgetLayout"] = clean
		} else {
			discarded++
		}
	}
	// pinnedApps
	if v, ok := allPrefs["pinnedApps"].([]interface{}); ok && len(v) <= 30 {
		clean := []interface{}{}
		for _, item := range v {
			if s, ok := item.(string); ok {
				if cs := safeString(s, 50); cs != "" {
					clean = append(clean, cs)
				}
			}
		}
		safePrefs["pinnedApps"] = clean
	}
	// Widget layout: array of position objects, clamped
	if v, ok := allPrefs["widgetLayout"].([]interface{}); ok && len(v) <= 30 {
		clean := []interface{}{}
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				w := map[string]interface{}{}
				if id, ok := m["id"].(string); ok {
					if cs := safeString(id, 50); cs != "" {
						w["id"] = cs
					}
				}
				if t, ok := m["type"].(string); ok {
					if cs := safeString(t, 50); cs != "" {
						w["type"] = cs
					}
				}
				for _, posKey := range []string{"col", "row", "cols", "rows"} {
					if n, ok := m[posKey].(float64); ok && n >= 0 && n <= 50 {
						w[posKey] = n
					}
				}
				if len(w) > 0 {
					clean = append(clean, w)
				}
			}
		}
		safePrefs["widgetLayout"] = clean
	}

	if discarded > 0 {
		logMsg("prefs injection: discarded %d invalid values for user %s", discarded, session.Username)
	}

	prefsJSON, err := json.Marshal(safePrefs)
	if err != nil {
		return html
	}

	// Size limit: prevent inflated prefs from bloating the HTML
	if len(prefsJSON) > 8192 {
		logMsg("prefs injection: JSON too large (%d bytes) for user %s, skipping", len(prefsJSON), session.Username)
		return html
	}

	// Inject as <meta> tag with base64-encoded JSON — immune to CSP,
	// immune to quote/character escaping issues in HTML attributes
	b64 := base64.StdEncoding.EncodeToString(prefsJSON)
	injection := fmt.Sprintf(`<meta id="%s" content="%s">`, prefsTagID, b64)
	return bytes.Replace(html, []byte("</head>"), []byte(injection+"</head>"), 1)
}
