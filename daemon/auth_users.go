package main

import (
	"fmt"
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

// createUserWithSMB creates one NimOS identity and its Linux/Samba mirrors.
// A user must never be reported as created when one of those mirrors failed.
func createUserWithSMB(username, password, role, description string) error {
	hashed, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password")
	}
	if err := dbUsersCreate(username, hashed, role, description); err != nil {
		return err
	}

	createResult := handleOp(Request{Op: "user.create", Username: username})
	if !createResult.Ok {
		_ = dbUsersDelete(username)
		return fmt.Errorf("failed to create system user: %s", createResult.Error)
	}
	passwordResult := handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})
	if !passwordResult.Ok {
		_ = dbUsersDelete(username)
		if !createResult.Existed {
			handleOp(Request{Op: "user.delete", Username: username})
		}
		return fmt.Errorf("failed to create SMB credentials: %s", passwordResult.Error)
	}
	return nil
}

// setUserPasswordAndSMB keeps the NimOS and SMB passwords as one credential.
// If Samba rejects the change, restore the previous NimOS hash so the two
// authentication paths cannot silently diverge.
func setUserPasswordAndSMB(username, password string) error {
	if msg := validatePasswordStrength(password); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	current, err := dbUsersGetRaw(username)
	if err != nil {
		return err
	}
	hashed, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password")
	}
	if err := dbUsersUpdate(username, UserUpdate{Password: strPtr(hashed)}); err != nil {
		return err
	}

	result := handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})
	if !result.Ok {
		if rollbackErr := dbUsersUpdate(username, UserUpdate{Password: strPtr(current.Password)}); rollbackErr != nil {
			return fmt.Errorf("failed to update SMB password (%s) and to restore NimOS credentials (%v)", result.Error, rollbackErr)
		}
		return fmt.Errorf("failed to update SMB password: %s", result.Error)
	}
	dbSessionsDeleteByUsername(username)
	return nil
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
	if role != "user" && role != "admin" {
		jsonError(w, 400, "Role must be user or admin")
		return
	}

	if err := createUserWithSMB(username, password, role, description); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

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
	role := bodyStr(body, "role")
	if role != "" && role != "user" && role != "admin" {
		jsonError(w, 400, "Role must be user or admin")
		return
	}

	if pw := bodyStr(body, "password"); pw != "" {
		if msg := validatePasswordStrength(pw); msg != "" {
			jsonError(w, 400, msg)
			return
		}
		if err := setUserPasswordAndSMB(target, pw); err != nil {
			jsonError(w, 500, err.Error())
			return
		}
	}
	if role != "" {
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
