package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ═══════════════════════════════════
// Constants
// ═══════════════════════════════════

const (
	maxLoginAttempts = 5
	lockoutDuration  = 15 * 60 * 1000 // 15 min in ms
	serverKeyFile    = "/var/lib/nimos/config/.server_key"
	userDataDir      = "/var/lib/nimos/userdata"
)

var validUsernameHTTP = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{1,31}$`)

// ═══════════════════════════════════
// Rate limiting
// ═══════════════════════════════════

type rateLimitEntry struct {
	count       int
	lastAttempt int64
	lockedUntil int64
}

var (
	rateLimits   = map[string]*rateLimitEntry{}
	rateLimitsMu sync.Mutex
)

// ═══════════════════════════════════
// Password hashing (scrypt — compatible with Node.js)
// ═══════════════════════════════════

// ═══════════════════════════════════
// Token generation
// ═══════════════════════════════════

// ═══════════════════════════════════
// TOTP (2FA) — Google Authenticator compatible
// ═══════════════════════════════════

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// ═══════════════════════════════════
// TOTP secret encryption (AES-256-CBC, compatible with Node.js)
// ═══════════════════════════════════

// ═══════════════════════════════════
// QR code generation (uses qrencode CLI)
// ═══════════════════════════════════

// ═══════════════════════════════════
// User data helpers (preferences, playlists, wallpapers)
// ═══════════════════════════════════

var defaultPreferences = map[string]interface{}{
	"theme":            "dark",
	"accentColor":      "orange",
	"glowIntensity":    float64(50),
	"taskbarSize":      "medium",
	"taskbarPosition":  "bottom",
	"autoHideTaskbar":  false,
	"clock24":          true,
	"showDesktopIcons": true,
	"textScale":        float64(100),
	"wallpaper":        "",
	"showWidgets":      true,
	"widgetScale":      float64(100),
	"visibleWidgets": map[string]interface{}{
		"system": true, "network": true, "disk": true, "notifications": true,
	},
	"pinnedApps":   []interface{}{"files", "appstore", "settings"},
	"playlist":     []interface{}{},
	"playlistName": "Mi Lista",
}

// ═══════════════════════════════════
// Auth HTTP handlers
// ═══════════════════════════════════

func handleAuthRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	switch {
	// GET /api/auth/status
	case path == "/api/auth/status" && method == "GET":
		authStatus(w, r)

	// POST /api/auth/setup
	case path == "/api/auth/setup" && method == "POST":
		authSetup(w, r)

	// POST /api/auth/login
	case path == "/api/auth/login" && method == "POST":
		authLogin(w, r)

	// POST /api/auth/logout
	case path == "/api/auth/logout" && method == "POST":
		authLogout(w, r)

	// GET /api/auth/me
	case path == "/api/auth/me" && method == "GET":
		authMe(w, r)

	// POST /api/auth/change-password
	case path == "/api/auth/change-password" && method == "POST":
		authChangePassword(w, r)

	// POST /api/auth/2fa/setup
	case path == "/api/auth/2fa/setup" && method == "POST":
		auth2faSetup(w, r)

	// POST /api/auth/2fa/verify
	case path == "/api/auth/2fa/verify" && method == "POST":
		auth2faVerify(w, r)

	// POST /api/auth/2fa/disable
	case path == "/api/auth/2fa/disable" && method == "POST":
		auth2faDisable(w, r)

	// GET /api/auth/2fa/status
	case path == "/api/auth/2fa/status" && method == "GET":
		auth2faStatus(w, r)

	// POST /api/auth/2fa/qr
	case path == "/api/auth/2fa/qr" && method == "POST":
		auth2faQr(w, r)

	default:
		jsonError(w, 404, "Not found")
	}
}

// GET /api/auth/status — is setup done?
func authStatus(w http.ResponseWriter, r *http.Request) {
	users, _ := dbUsersListRaw()
	hostname, _ := os.Hostname()
	jsonOk(w, map[string]interface{}{
		"setup":    len(users) > 0,
		"hostname": hostname,
	})
}

// POST /api/auth/setup — create initial admin account
func authSetup(w http.ResponseWriter, r *http.Request) {
	users, _ := dbUsersListRaw()
	if len(users) > 0 {
		jsonError(w, 400, "Setup already completed")
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	username := strings.ToLower(strings.TrimSpace(bodyStr(body, "username")))
	password := bodyStr(body, "password")

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

	hashed, err := hashPassword(password)
	if err != nil {
		jsonError(w, 500, "Failed to hash password")
		return
	}

	if err := dbUsersCreate(username, hashed, "admin", "System administrator"); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	// Create Linux user + Samba password via daemon ops
	handleOp(Request{Op: "user.create", Username: username})
	handleOp(Request{Op: "user.set_smb_password", Username: username, Password: password})

	// Create default volume directory
	os.MkdirAll(filepath.Join(nimosRoot, "volumes", "volume1"), 0755)

	// Auto-login
	token, _ := generateToken()
	hToken := sha256Hex(token)
	dbSessionCreate(hToken, username, "admin", clientIP(r))

	// SECURITY: Set session cookie with proper flags
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "nimos_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})

	jsonOk(w, map[string]interface{}{
		"ok":    true,
		"token": token,
		"user":  map[string]string{"username": username, "role": "admin"},
	})
}

// POST /api/auth/login
func authLogin(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	username := strings.ToLower(strings.TrimSpace(bodyStr(body, "username")))
	password := bodyStr(body, "password")
	totpCode := bodyStr(body, "totpCode")

	if username == "" || password == "" {
		jsonError(w, 400, "Username and password required")
		return
	}

	ip := clientIP(r)

	// Rate limiting
	if ok, msg := checkRateLimit("ip:" + ip); !ok {
		jsonError(w, 429, msg)
		return
	}
	if ok, msg := checkRateLimit("user:" + username); !ok {
		jsonError(w, 429, msg)
		return
	}

	// Verify credentials
	// IMPORTANT: Always run bcrypt comparison even if user doesn't exist
	// This prevents timing attacks that reveal valid usernames
	storedPwd, err := dbUsersVerifyPassword(username)
	if err != nil {
		// User doesn't exist — run a dummy bcrypt to equalize timing
		verifyPassword(password, "$2a$10$0000000000000000000000uDummyHashToPreventTimingAttack0000")
		recordFailedAttempt("ip:" + ip)
		recordFailedAttempt("user:" + username)
		ShieldAuthFail(ip, username, r.UserAgent())
		jsonError(w, 401, "Invalid credentials")
		return
	}
	if !verifyPassword(password, storedPwd) {
		recordFailedAttempt("ip:" + ip)
		recordFailedAttempt("user:" + username)
		ShieldAuthFail(ip, username, r.UserAgent())
		jsonError(w, 401, "Invalid credentials")
		return
	}

	// Get user for role and 2FA check
	user, err := dbUsersGetRaw(username)
	if err != nil {
		jsonError(w, 500, "User lookup failed")
		return
	}

	// Check 2FA
	if user.TotpSecret != "" && user.TotpEnabled {
		if totpCode == "" {
			jsonOk(w, map[string]interface{}{
				"requires2FA": true,
				"message":     "Two-factor authentication code required",
			})
			return
		}
		decrypted, err := decryptSecret(user.TotpSecret)
		if err != nil {
			jsonError(w, 500, "2FA decryption failed")
			return
		}
		if !verifyTotp(decrypted, totpCode) {
			// Check backup codes
			backupValid := false
			if user.BackupCodes != nil {
				inputHash := sha256Hex(strings.ToUpper(totpCode))
				for i, c := range user.BackupCodes {
					if cs, ok := c.(string); ok && cs == inputHash {
						// Remove used backup code
						codes := append(user.BackupCodes[:i], user.BackupCodes[i+1:]...)
						dbUsersUpdate(username, UserUpdate{BackupCodes: codes})
						backupValid = true
						break
					}
				}
			}
			if !backupValid {
				recordFailedAttempt("ip:" + ip)
				recordFailedAttempt("user:" + username)
				jsonError(w, 401, "Invalid 2FA code")
				return
			}
		}
	}

	clearFailedAttempts("ip:" + ip)
	clearFailedAttempts("user:" + username)

	// Reputación: login exitoso → +1 y borra la racha de fallos de esta IP.
	ShieldAuthSuccess(ip)

	token, _ := generateToken()
	hToken := sha256Hex(token)
	dbSessionCreate(hToken, username, user.Role, ip)

	// SECURITY: Set session cookie from backend with proper flags
	// HttpOnly prevents XSS from stealing the cookie
	// Secure ensures it's only sent over HTTPS
	// SameSite=Strict prevents CSRF
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "nimos_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400, // 24h matches session expiry
	})

	jsonOk(w, map[string]interface{}{
		"ok":    true,
		"token": token,
		"user":  map[string]string{"username": username, "role": user.Role},
	})
}

// POST /api/auth/logout
func authLogout(w http.ResponseWriter, r *http.Request) {
	token := getBearerToken(r)
	if token != "" {
		dbSessionDelete(sha256Hex(token))
	}
	// Clear session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "nimos_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	jsonOk(w, map[string]interface{}{"ok": true})
}

// GET /api/auth/me
func authMe(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	jsonOk(w, map[string]interface{}{
		"user": map[string]string{
			"username": session.Username,
			"role":     session.Role,
		},
	})
}

// POST /api/auth/change-password
func authChangePassword(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	newPassword := bodyStr(body, "newPassword")
	currentPassword := bodyStr(body, "currentPassword")
	targetUser := bodyStr(body, "targetUser")

	if newPassword == "" {
		jsonError(w, 400, "New password required")
		return
	}
	if msg := validatePasswordStrength(newPassword); msg != "" {
		jsonError(w, 400, msg)
		return
	}

	sessionUser := session.Username
	sessionRole := session.Role

	editUser := sessionUser
	if targetUser != "" && sessionRole == "admin" {
		editUser = targetUser
	}

	// Non-admin or self-change: require current password
	if targetUser == "" || targetUser == sessionUser {
		stored, err := dbUsersVerifyPassword(editUser)
		if err != nil || !verifyPassword(currentPassword, stored) {
			jsonError(w, 400, "Current password is incorrect")
			return
		}
	}

	hashed, err := hashPassword(newPassword)
	if err != nil {
		jsonError(w, 500, "Failed to hash password")
		return
	}
	dbUsersUpdate(editUser, UserUpdate{Password: strPtr(hashed)})

	// Invalidate all sessions for this user
	dbSessionsDeleteByUsername(editUser)

	// Update Samba password
	handleOp(Request{Op: "user.set_smb_password", Username: editUser, Password: newPassword})

	jsonOk(w, map[string]interface{}{"ok": true})
}

// ═══════════════════════════════════
// User preference / playlist / wallpaper routes
// ═══════════════════════════════════

// ═══════════════════════════════════
// Users management (admin CRUD)
// ═══════════════════════════════════
