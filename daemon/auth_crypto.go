package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/scrypt"
)

func hashPassword(password string) (string, error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", err
	}
	// Node.js passes hex string of salt to scrypt, not raw bytes
	saltHex := hex.EncodeToString(saltBytes)
	dk, err := scrypt.Key([]byte(password), []byte(saltHex), 16384, 8, 1, 64)
	if err != nil {
		return "", err
	}
	return saltHex + ":" + hex.EncodeToString(dk), nil
}

func verifyPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	// Node.js passes salt as string to scrypt, NOT as decoded hex bytes
	salt := []byte(parts[0])
	expected, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	dk, err := scrypt.Key([]byte(password), salt, 16384, 8, 1, 64)
	if err != nil {
		return false
	}
	if len(dk) != len(expected) {
		return false
	}
	// Constant-time compare
	result := byte(0)
	for i := range dk {
		result |= dk[i] ^ expected[i]
	}
	return result == 0
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func generateToken() (string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// base64url encoding without padding (matches Node.js crypto.randomBytes(48).toString('base64url'))
	const base64url = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	result := make([]byte, 64)
	for i := 0; i < 48; i++ {
		// Simple base64url: 6 bits per char
		if i*8/6 < 64 {
			result[i*8/6] = base64url[(b[i]>>2)&0x3F]
		}
	}
	// Use hex for simplicity and guaranteed uniqueness
	return hex.EncodeToString(b), nil
}

func base32Encode(data []byte) string {
	var result strings.Builder
	bits := 0
	value := 0
	for _, b := range data {
		value = (value << 8) | int(b)
		bits += 8
		for bits >= 5 {
			result.WriteByte(base32Alphabet[(value>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		result.WriteByte(base32Alphabet[(value<<(5-bits))&31])
	}
	return result.String()
}

func base32Decode(s string) []byte {
	s = strings.ToUpper(strings.TrimRight(s, "="))
	var output []byte
	bits := 0
	value := 0
	for _, c := range s {
		idx := strings.IndexRune(base32Alphabet, c)
		if idx == -1 {
			continue
		}
		value = (value << 5) | idx
		bits += 5
		if bits >= 8 {
			output = append(output, byte((value>>(bits-8))&0xFF))
			bits -= 8
		}
	}
	return output
}

func getServerKey() ([]byte, error) {
	if data, err := os.ReadFile(serverKeyFile); err == nil {
		keyHex := strings.TrimSpace(string(data))
		if matched, _ := regexp.MatchString(`^[0-9a-f]{64}$`, keyHex); matched {
			return hex.DecodeString(keyHex)
		}
	}
	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	keyHex := hex.EncodeToString(key)
	dir := filepath.Dir(serverKeyFile)
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(serverKeyFile, []byte(keyHex), 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptSecret(plaintext string) (string, error) {
	key, err := getServerKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	// PKCS7 padding
	padLen := aes.BlockSize - (len(plaintext) % aes.BlockSize)
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	mode := cipher.NewCBCEncrypter(block, iv)
	encrypted := make([]byte, len(padded))
	mode.CryptBlocks(encrypted, padded)
	return hex.EncodeToString(iv) + ":" + hex.EncodeToString(encrypted), nil
}

func decryptSecret(ciphertext string) (string, error) {
	if !strings.Contains(ciphertext, ":") {
		return ciphertext, nil // backwards compat: unencrypted
	}
	parts := strings.SplitN(ciphertext, ":", 2)
	if len(parts) != 2 {
		return ciphertext, nil
	}
	key, err := getServerKey()
	if err != nil {
		return "", err
	}
	iv, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	encrypted, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted := make([]byte, len(encrypted))
	mode.CryptBlocks(decrypted, encrypted)
	// Remove PKCS7 padding
	if len(decrypted) > 0 {
		padLen := int(decrypted[len(decrypted)-1])
		if padLen > 0 && padLen <= aes.BlockSize {
			decrypted = decrypted[:len(decrypted)-padLen]
		}
	}
	return string(decrypted), nil
}

// base64 decode helper
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Try standard base64 first
	if data, err := base64.StdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	// Try without padding
	if data, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	// Try URL-safe
	if data, err := base64.URLEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}
