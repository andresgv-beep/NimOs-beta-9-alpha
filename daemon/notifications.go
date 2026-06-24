package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════
// NimOS Notifications — Persistent notification system
// ═══════════════════════════════════════════════════════════════════

// createNotificationTable creates the notifications table in SQLite.
// Called from createTables() in db.go.
func createNotificationTable() error {
	schema := `
	CREATE TABLE IF NOT EXISTS notifications (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		type       TEXT NOT NULL DEFAULT 'info',
		category   TEXT NOT NULL DEFAULT 'notification',
		title      TEXT DEFAULT '',
		message    TEXT NOT NULL,
		read       INTEGER DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_notif_category ON notifications(category);
	CREATE INDEX IF NOT EXISTS idx_notif_created ON notifications(created_at);
	`
	_, err := db.Exec(schema)
	return err
}

// ─── Internal API (for Go modules to create notifications) ───────

// addNotification creates a notification from within the daemon.
func addNotification(ntype, category, title, message string) {
	if db == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	db.Exec(`INSERT INTO notifications (type, category, title, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		ntype, category, title, message, now)
}

// Convenience helpers for internal use
func notifSuccess(title, message string) { addNotification("success", "notification", title, message) }
func notifError(title, message string)   { addNotification("error", "notification", title, message) }
func notifWarning(title, message string) { addNotification("warning", "notification", title, message) }
func notifInfo(title, message string)    { addNotification("info", "notification", title, message) }
func notifSystem(title, message string)  { addNotification("info", "system", title, message) }
func notifSecurity(title, message string) {
	addNotification("security", "system", title, message)
}

// ─── HTTP Endpoints ──────────────────────────────────────────────

func handleNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	path := r.URL.Path
	method := r.Method

	// GET /api/notifications — list all
	if (path == "/api/notifications" || path == "/api/notifications/") && method == "GET" {
		notifList(w, r)
		return
	}

	// POST /api/notifications — create
	if (path == "/api/notifications" || path == "/api/notifications/") && method == "POST" {
		notifCreate(w, r)
		return
	}

	// DELETE /api/notifications — clear all or by category
	if (path == "/api/notifications" || path == "/api/notifications/") && method == "DELETE" {
		notifClear(w, r)
		return
	}

	// PUT /api/notifications/read-all — mark all as read
	if path == "/api/notifications/read-all" && method == "PUT" {
		db.Exec(`UPDATE notifications SET read = 1`)
		jsonOk(w, map[string]interface{}{"ok": true})
		return
	}

	// Routes with ID: /api/notifications/{id}[/read]
	trimmed := strings.TrimPrefix(path, "/api/notifications/")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 1 && parts[0] != "" {
		id, err := strconv.Atoi(parts[0])
		if err != nil {
			jsonError(w, 400, "Invalid notification ID")
			return
		}

		// DELETE /api/notifications/{id}
		if method == "DELETE" {
			db.Exec(`DELETE FROM notifications WHERE id = ?`, id)
			jsonOk(w, map[string]interface{}{"ok": true})
			return
		}

		// PUT /api/notifications/{id}/read
		if method == "PUT" && len(parts) >= 2 && parts[1] == "read" {
			db.Exec(`UPDATE notifications SET read = 1 WHERE id = ?`, id)
			jsonOk(w, map[string]interface{}{"ok": true})
			return
		}
	}

	jsonError(w, 404, "Not found")
}

// ─── Handlers ────────────────────────────────────────────────────

func notifList(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "100"
	}
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := `SELECT id, type, category, title, message, read, created_at FROM notifications`
	var args []interface{}
	if category != "" {
		query += ` WHERE category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id, read int
		var ntype, cat, title, message, createdAt string
		if err := rows.Scan(&id, &ntype, &cat, &title, &message, &read, &createdAt); err != nil {
			continue
		}
		result = append(result, map[string]interface{}{
			"id":        id,
			"type":      ntype,
			"category":  cat,
			"title":     title,
			"message":   message,
			"read":      read == 1,
			"timestamp": createdAt,
		})
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	jsonOk(w, map[string]interface{}{"notifications": result})
}

func notifCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := readBody(r)
	message := bodyStr(body, "message")
	if message == "" {
		jsonError(w, 400, "Message required")
		return
	}
	ntype := bodyStr(body, "type")
	if ntype == "" {
		ntype = "info"
	}
	category := bodyStr(body, "category")
	if category == "" {
		category = "notification"
	}
	title := bodyStr(body, "title")

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(`INSERT INTO notifications (type, category, title, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		ntype, category, title, message, now)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	jsonOk(w, map[string]interface{}{
		"ok": true,
		"notification": map[string]interface{}{
			"id":        id,
			"type":      ntype,
			"category":  category,
			"title":     title,
			"message":   message,
			"read":      false,
			"timestamp": now,
		},
	})
}

func notifClear(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category != "" {
		db.Exec(`DELETE FROM notifications WHERE category = ?`, category)
	} else {
		db.Exec(`DELETE FROM notifications`)
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}
