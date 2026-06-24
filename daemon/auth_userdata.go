package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func getUserDataPath(username string) string {
	safe := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(username, "")
	return filepath.Join(userDataDir, safe)
}

func ensureUserDataDir(username string) string {
	p := getUserDataPath(username)
	os.MkdirAll(p, 0755)
	return p
}

func getUserPreferences(username string) map[string]interface{} {
	result := map[string]interface{}{}
	// Copy defaults
	for k, v := range defaultPreferences {
		result[k] = v
	}
	// Read saved
	prefsFile := filepath.Join(getUserDataPath(username), "preferences.json")
	data, err := os.ReadFile(prefsFile)
	if err != nil {
		return result
	}
	var saved map[string]interface{}
	if json.Unmarshal(data, &saved) == nil {
		for k, v := range saved {
			result[k] = v
		}
	}
	return result
}

func saveUserPreferences(username string, prefs map[string]interface{}) error {
	dir := ensureUserDataDir(username)
	data, _ := json.MarshalIndent(prefs, "", "  ")
	return os.WriteFile(filepath.Join(dir, "preferences.json"), data, 0644)
}

func getUserPlaylist(username string) []interface{} {
	playlistFile := filepath.Join(getUserDataPath(username), "playlist.json")
	data, err := os.ReadFile(playlistFile)
	if err != nil {
		return []interface{}{}
	}
	var playlist []interface{}
	if json.Unmarshal(data, &playlist) != nil {
		return []interface{}{}
	}
	return playlist
}

func saveUserPlaylist(username string, playlist []interface{}) error {
	dir := ensureUserDataDir(username)
	data, _ := json.MarshalIndent(playlist, "", "  ")
	return os.WriteFile(filepath.Join(dir, "playlist.json"), data, 0644)
}

func handleUserRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	switch {
	// GET /api/user/preferences
	case path == "/api/user/preferences" && method == "GET":
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		prefs := getUserPreferences(session.Username)
		jsonOk(w, map[string]interface{}{"preferences": prefs})

	// PUT /api/user/preferences
	case path == "/api/user/preferences" && method == "PUT":
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		body, _ := readBody(r)
		current := getUserPreferences(session.Username)
		for k, v := range body {
			if k != "playlist" {
				current[k] = v
			}
		}
		delete(current, "playlist")
		if err := saveUserPreferences(session.Username, current); err != nil {
			jsonError(w, 500, "Failed to save preferences")
			return
		}
		jsonOk(w, map[string]interface{}{"ok": true, "preferences": current})

	// PATCH /api/user/preferences
	case path == "/api/user/preferences" && method == "PATCH":
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		body, _ := readBody(r)
		current := getUserPreferences(session.Username)
		for k, v := range body {
			if k != "playlist" {
				current[k] = v
			}
		}
		delete(current, "playlist")
		saveUserPreferences(session.Username, current)
		jsonOk(w, map[string]interface{}{"ok": true})

	// POST /api/user/wallpaper — sube un wallpaper a la galería (multipart o base64)
	case path == "/api/user/wallpaper" && method == "POST":
		uploadWallpaperHTTP(w, r)

	// GET /api/user/wallpaper/:username/:file — sirve el binario
	case strings.HasPrefix(path, "/api/user/wallpaper/") && method == "GET":
		serveUserWallpaperHTTP(w, r)

	// GET /api/user/playlist
	case path == "/api/user/playlist" && method == "GET":
		session := requireAuth(w, r)
		if session == nil {
			return
		}
		jsonOk(w, map[string]interface{}{"playlist": getUserPlaylist(session.Username)})

	// PUT /api/user/playlist
	case path == "/api/user/playlist" && method == "PUT":
		userPlaylistSave(w, r)

	// POST /api/user/playlist/add
	case path == "/api/user/playlist/add" && method == "POST":
		userPlaylistAdd(w, r)

	// DELETE /api/user/playlist/:index
	case strings.HasPrefix(path, "/api/user/playlist/") && method == "DELETE":
		userPlaylistRemove(w, r)

	default:
		// Check auth before 404 — prevents route enumeration
		if requireAuth(w, r) == nil {
			return
		}
		jsonError(w, 404, "Not found")
	}
}

func wallpaperExtFromName(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "png", "webp", "gif":
		return ext
	case "jpg", "jpeg":
		return "jpg"
	}
	return ""
}

// wallpaperExtFromContentType devuelve la extensión normalizada a partir de un MIME type, o "".
func wallpaperExtFromContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch {
	case strings.HasPrefix(ct, "image/png"):
		return "png"
	case strings.HasPrefix(ct, "image/jpeg"), strings.HasPrefix(ct, "image/jpg"):
		return "jpg"
	case strings.HasPrefix(ct, "image/webp"):
		return "webp"
	case strings.HasPrefix(ct, "image/gif"):
		return "gif"
	}
	return ""
}

func userPlaylistSave(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	playlistRaw, ok := body["playlist"]
	if !ok {
		jsonError(w, 400, "Playlist must be an array")
		return
	}
	playlist, ok := playlistRaw.([]interface{})
	if !ok {
		jsonError(w, 400, "Playlist must be an array")
		return
	}
	if err := saveUserPlaylist(session.Username, playlist); err != nil {
		jsonError(w, 500, "Failed to save playlist")
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true, "count": len(playlist)})
}

func userPlaylistAdd(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	body, _ := readBody(r)
	itemUrl := bodyStr(body, "url")
	if itemUrl == "" {
		jsonError(w, 400, "URL required")
		return
	}

	username := session.Username
	playlist := getUserPlaylist(username)

	// Check duplicates
	for _, item := range playlist {
		if m, ok := item.(map[string]interface{}); ok {
			if u, _ := m["url"].(string); u == itemUrl {
				jsonError(w, 400, "Already in playlist")
				return
			}
		}
	}

	itemType := "audio"
	if t := bodyStr(body, "type"); t == "video" {
		itemType = "video"
	}

	newItem := map[string]interface{}{
		"name":    bodyStr(body, "name"),
		"url":     itemUrl,
		"type":    itemType,
		"addedAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if d := bodyStr(body, "duration"); d != "" {
		newItem["duration"] = d
	}

	playlist = append(playlist, newItem)
	saveUserPlaylist(username, playlist)
	jsonOk(w, map[string]interface{}{"ok": true, "count": len(playlist)})
}

func userPlaylistRemove(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	// Extract index from /api/user/playlist/:index
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		jsonError(w, 400, "Invalid index")
		return
	}
	var index int
	if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &index); err != nil {
		jsonError(w, 400, "Invalid index")
		return
	}

	username := session.Username
	playlist := getUserPlaylist(username)
	if index < 0 || index >= len(playlist) {
		jsonError(w, 400, "Invalid index")
		return
	}
	playlist = append(playlist[:index], playlist[index+1:]...)
	saveUserPlaylist(username, playlist)
	jsonOk(w, map[string]interface{}{"ok": true, "count": len(playlist)})
}
