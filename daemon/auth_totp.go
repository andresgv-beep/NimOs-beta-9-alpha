package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func generateTotpSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32Encode(b), nil
}

func generateTotp(secret string, unixTime int64) string {
	t := unixTime / 30
	timeBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timeBytes, uint64(t))

	key := base32Decode(secret)
	mac := hmac.New(sha1.New, key)
	mac.Write(timeBytes)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	code := (int(hash[offset]&0x7f) << 24) |
		(int(hash[offset+1]) << 16) |
		(int(hash[offset+2]) << 8) |
		int(hash[offset+3])
	code = code % 1000000
	return fmt.Sprintf("%06d", code)
}

func verifyTotp(secret, token string) bool {
	now := time.Now().Unix()
	for i := int64(-1); i <= 1; i++ {
		if generateTotp(secret, now+i*30) == token {
			return true
		}
	}
	return false
}

func getTotpUri(username, secret string) string {
	return fmt.Sprintf("otpauth://totp/NimOS:%s?secret=%s&issuer=NimOS&algorithm=SHA1&digits=6&period=30", username, secret)
}

// Backup codes for 2FA recovery
func generateBackupCodes(count int) []string {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		b := make([]byte, 4)
		rand.Read(b)
		codes[i] = strings.ToUpper(hex.EncodeToString(b))
	}
	return codes
}

func generateQrSvg(text string) (string, error) {
	// Try qrencode first — pipe text via stdin (no shell interpolation)
	out, ok := runSafeInput(text, "qrencode", "-t", "SVG", "-o", "-", "-m", "1")
	if ok && out != "" {
		return out, nil
	}
	// Try python3 qrcode — pipe text via stdin
	pyScript := `import qrcode,qrcode.image.svg,sys,io;data=sys.stdin.read();img=qrcode.make(data,image_factory=qrcode.image.svg.SvgPathImage,box_size=8,border=1);buf=io.BytesIO();img.save(buf);sys.stdout.buffer.write(buf.getvalue())`
	out, ok = runSafeInput(text, "python3", "-c", pyScript)
	if ok && out != "" {
		return out, nil
	}
	return "", fmt.Errorf("QR generation not available. Install qrencode: sudo apt install qrencode")
}

// POST /api/auth/2fa/setup — generate TOTP secret
func auth2faSetup(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	secret, err := generateTotpSecret()
	if err != nil {
		jsonError(w, 500, "Failed to generate secret")
		return
	}

	encrypted, err := encryptSecret(secret)
	if err != nil {
		jsonError(w, 500, "Failed to encrypt secret")
		return
	}

	username := session.Username
	dbUsersUpdate(username, UserUpdate{
		TotpSecret:  strPtr(encrypted),
		TotpEnabled: boolPtr(false),
	})

	uri := getTotpUri(username, secret)
	resp := map[string]interface{}{
		"ok":     true,
		"secret": secret,
		"uri":    uri,
	}
	// Generar QR SVG si el sistema tiene qrencode o python3-qrcode.
	// Si no está disponible, el frontend lo genera en cliente desde 'uri'.
	if qr, qerr := generateQrSvg(uri); qerr == nil && qr != "" {
		resp["qr"] = qr
	}
	jsonOk(w, resp)
}

// POST /api/auth/2fa/verify — verify code and enable 2FA
func auth2faVerify(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)
	code := bodyStr(body, "code")
	if code == "" {
		jsonError(w, 400, "Code required")
		return
	}

	username := session.Username
	user, err := dbUsersGetRaw(username)
	if err != nil {
		jsonError(w, 400, "User not found")
		return
	}

	if user.TotpSecret == "" {
		jsonError(w, 400, "No 2FA setup in progress")
		return
	}

	decrypted, err := decryptSecret(user.TotpSecret)
	if err != nil {
		jsonError(w, 500, "Decryption failed")
		return
	}
	if !verifyTotp(decrypted, code) {
		jsonError(w, 400, "Invalid code. Make sure your authenticator app is synced.")
		return
	}

	// Generate backup codes
	backupCodes := generateBackupCodes(8)
	hashedCodes := make([]interface{}, len(backupCodes))
	for i, c := range backupCodes {
		hashedCodes[i] = sha256Hex(c)
	}

	dbUsersUpdate(username, UserUpdate{
		TotpEnabled: boolPtr(true),
		BackupCodes: hashedCodes,
	})

	jsonOk(w, map[string]interface{}{
		"ok":          true,
		"message":     "2FA enabled successfully",
		"backupCodes": backupCodes,
	})
}

// POST /api/auth/2fa/disable
func auth2faDisable(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)
	password := bodyStr(body, "password")
	if password == "" {
		jsonError(w, 400, "Password required to disable 2FA")
		return
	}

	username := session.Username
	stored, err := dbUsersVerifyPassword(username)
	if err != nil || !verifyPassword(password, stored) {
		jsonError(w, 400, "Invalid password")
		return
	}

	dbUsersUpdate(username, UserUpdate{
		TotpSecret:  strPtr(""),
		TotpEnabled: boolPtr(false),
	})

	jsonOk(w, map[string]interface{}{"ok": true})
}

// GET /api/auth/2fa/status
func auth2faStatus(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	username := session.Username
	user, _ := dbUsersGetRaw(username)
	enabled := false
	if user != nil {
		enabled = user.TotpEnabled
	}
	jsonOk(w, map[string]interface{}{"enabled": enabled})
}

// POST /api/auth/2fa/qr — generate QR code SVG
func auth2faQr(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}

	body, _ := readBody(r)
	text := bodyStr(body, "text")
	if text == "" {
		jsonError(w, 400, "Text required")
		return
	}

	svg, err := generateQrSvg(text)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"svg": svg})
}
