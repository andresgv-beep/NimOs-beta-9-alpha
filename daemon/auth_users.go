package main

import (
	"net/http"
	"regexp"
	"strings"
)

func validatePasswordStrength(password string) string {
	if len(password) < 8 {
		return "Password must be at least 8 characters"
	}
	hasUpper := false
	hasDigit := false
	for _, c := range password {
		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		}
		if c >= '0' && c <= '9' {
			hasDigit = true
		}
	}
	if !hasUpper {
		return "Password must contain at least one uppercase letter"
	}
	if !hasDigit {
		return "Password must contain at least one number"
	}
	return ""
}

func handleUsersRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// GET /api/users — list users
	if path == "/api/users" && method == "GET" {
		session := requireAdmin(w, r)
		if session == nil {
			return
		}
		users, _ := dbUsersListRaw()
		result := make([]map[string]interface{}, len(users))
		for i, u := range users {
			result[i] = u.ToMap()
		}
		jsonOk(w, result)
		return
	}

	// POST /api/users — create user
	if path == "/api/users" && method == "POST" {
		usersCreate(w, r)
		return
	}

	// Match /api/users/:username
	userMatch := regexp.MustCompile(`^/api/users/([a-zA-Z0-9_.-]+)$`)
	matches := userMatch.FindStringSubmatch(path)
	if matches == nil {
		jsonError(w, 404, "Not found")
		return
	}
	target := strings.ToLower(matches[1])

	switch method {
	case "DELETE":
		usersDelete(w, r, target)
	case "PUT":
		usersUpdate(w, r, target)
	default:
		jsonError(w, 405, "Method not allowed")
	}
}

func usersCreate(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)
	username := strings.ToLower(strings.TrimSpace(bodyStr(body, "username")))
	password := bodyStr(body, "password")
	role := bodyStr(body, "role")
	description := bodyStr(body, "description")

	if username == "" || password == "" {
		jsonError(w, 400, "Username and password required")
		return
	}
	if !validUsernameHTTP.MatchString(username) {
		jsonError(w, 400, "Invalid username: letters, numbers and underscores only (2-32 chars)")
		return
	}
	if msg := validatePasswordStrength(password); msg != "" {
		jsonError(w, 400, msg)
		return
	}

	// Check if user exists
	if _, err := dbUsersGetRaw(username); err == nil {
		jsonError(w, 400, "User already exists")
		return
	}

	if role == "" {
		role = "user"
	}

	hashed, _ := hashPassword(password)
	if err := dbUsersCreate(username, hashed, role, description); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	// Sync Linux + Samba
	handleOp(Request{Op: "user.create", Username: username})
	handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})

	jsonOk(w, map[string]interface{}{"ok": true, "username": username})
}

func usersDelete(w http.ResponseWriter, r *http.Request, target string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	if target == session.Username {
		jsonError(w, 400, "Cannot delete yourself")
		return
	}

	if _, err := dbUsersGetRaw(target); err != nil {
		jsonError(w, 404, "User not found")
		return
	}

	dbUsersDelete(target)
	handleOp(Request{Op: "user.delete", Username: target})

	jsonOk(w, map[string]interface{}{"ok": true})
}

func usersUpdate(w http.ResponseWriter, r *http.Request, target string) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}

	if _, err := dbUsersGetRaw(target); err != nil {
		jsonError(w, 404, "User not found")
		return
	}

	body, _ := readBody(r)
	var u UserUpdate
	hasUpdates := false

	if pw := bodyStr(body, "password"); pw != "" {
		if msg := validatePasswordStrength(pw); msg != "" {
			jsonError(w, 400, msg)
			return
		}
		hashed, _ := hashPassword(pw)
		u.Password = strPtr(hashed)
		hasUpdates = true
		handleOp(Request{Op: "user.set_smb_password", Username: target, Password: pw})
	}
	if role := bodyStr(body, "role"); role != "" {
		u.Role = strPtr(role)
		hasUpdates = true
	}
	if desc := bodyStr(body, "description"); desc != "" {
		u.Description = strPtr(desc)
		hasUpdates = true
	}

	if hasUpdates {
		dbUsersUpdate(target, u)
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}
