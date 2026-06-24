package main

import (
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════
// TESTS ESTRICTOS · auth.go · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// A diferencia de hardware/files/backup (caracterización de funciones puras),
// aquí testeamos CORRECCIÓN REAL de las funciones de seguridad: roundtrips de
// crypto y el vector oficial RFC 6238 para TOTP. Si estos pasan, el núcleo
// criptográfico de la autenticación es correcto. Son la red de seguridad ANTES
// de modularizar auth.go.
// ═══════════════════════════════════════════════════════════════════════

// ─── TOTP · vector oficial RFC 6238 ───
// El RFC 6238 define vectores con el secreto ASCII "12345678901234567890"
// (20 bytes). Nuestra impl hace base32Decode(secret), así que codificamos esos
// bytes a base32 y comprobamos que generamos el código oficial del RFC para un
// timestamp conocido.
//
// RFC 6238, tabla de test (SHA1, T0=0, X=30):
//
//	Time 59s    → T=1  → TOTP 94287082 → 6 dígitos "287082"
//	Time 1111111109 → TOTP 07081804 → "081804"
func TestTotp_RFC6238_Vectors(t *testing.T) {
	rfcSecret := []byte("12345678901234567890")
	secretB32 := base32Encode(rfcSecret)

	cases := []struct {
		unixTime int64
		want     string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		got := generateTotp(secretB32, c.unixTime)
		if got != c.want {
			t.Errorf("generateTotp(t=%d) = %q, want %q (vector RFC 6238)", c.unixTime, got, c.want)
		}
	}
}

func TestTotp_AlwaysSixDigits(t *testing.T) {
	secret, _ := generateTotpSecret()
	for tt := int64(0); tt < 100000; tt += 7777 {
		code := generateTotp(secret, tt)
		if len(code) != 6 {
			t.Fatalf("código TOTP no tiene 6 dígitos: %q (t=%d)", code, tt)
		}
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Fatalf("código TOTP con carácter no numérico: %q", code)
			}
		}
	}
}

// ─── base32 · roundtrip + RFC 4648 ───
func TestBase32_Roundtrip(t *testing.T) {
	inputs := []string{"", "f", "fo", "foo", "foob", "fooba", "foobar", "12345678901234567890"}
	for _, in := range inputs {
		enc := base32Encode([]byte(in))
		dec := base32Decode(enc)
		if string(dec) != in {
			t.Errorf("base32 roundtrip %q → %q → %q", in, enc, string(dec))
		}
	}
}

func TestBase32_RFC4648Vectors(t *testing.T) {
	// RFC 4648 base32 (sin padding, nuestra impl no pone '=').
	cases := []struct{ in, want string }{
		{"f", "MY"},
		{"fo", "MZXQ"},
		{"foo", "MZXW6"},
		{"foob", "MZXW6YQ"},
		{"fooba", "MZXW6YTB"},
		{"foobar", "MZXW6YTBOI"},
	}
	for _, c := range cases {
		if got := base32Encode([]byte(c.in)); got != c.want {
			t.Errorf("base32Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── Password hashing · roundtrip ───
func TestHashPassword_Roundtrip(t *testing.T) {
	pw := "Sup3rSecret!"
	stored, err := hashPassword(pw)
	if err != nil {
		t.Fatalf("hashPassword error: %v", err)
	}
	// Formato salt:hash
	if !strings.Contains(stored, ":") {
		t.Fatalf("hash sin separador salt:hash: %q", stored)
	}
	// La contraseña correcta verifica.
	if !verifyPassword(pw, stored) {
		t.Error("verifyPassword rechazó la contraseña correcta")
	}
	// Una incorrecta NO verifica.
	if verifyPassword("wrong", stored) {
		t.Error("verifyPassword aceptó una contraseña incorrecta")
	}
	// Dos hashes de la misma contraseña son distintos (salt aleatorio).
	stored2, _ := hashPassword(pw)
	if stored == stored2 {
		t.Error("dos hashes de la misma contraseña son idénticos (¿falta salt?)")
	}
}

func TestVerifyPassword_MalformedStored(t *testing.T) {
	// Entradas malformadas no deben crashear ni validar.
	for _, bad := range []string{"", "nohash", "a:b:c", ":", "xyz:nothex"} {
		if verifyPassword("whatever", bad) {
			t.Errorf("verifyPassword aceptó stored malformado: %q", bad)
		}
	}
}

// ─── Encriptado de secretos · roundtrip ───
func TestEncryptDecryptSecret_Roundtrip(t *testing.T) {
	plain := "JBSWY3DPEHPK3PXP" // un secreto TOTP de ejemplo
	enc, err := encryptSecret(plain)
	if err != nil {
		t.Fatalf("encryptSecret error: %v", err)
	}
	if enc == plain {
		t.Error("el texto cifrado es igual al plano (no se cifró)")
	}
	dec, err := decryptSecret(enc)
	if err != nil {
		t.Fatalf("decryptSecret error: %v", err)
	}
	if dec != plain {
		t.Errorf("roundtrip cifrado: %q → %q → %q", plain, enc, dec)
	}
}

// ─── validatePasswordStrength ───
func TestValidatePasswordStrength(t *testing.T) {
	cases := []struct {
		pw      string
		wantErr bool
	}{
		{"Abcd1234", false},    // ok: 8+, mayúscula, dígito
		{"short1A", true},      // < 8
		{"alllower1", true},    // sin mayúscula
		{"NoDigitsHere", true}, // sin dígito
		{"Password1", false},
	}
	for _, c := range cases {
		got := validatePasswordStrength(c.pw)
		if (got != "") != c.wantErr {
			t.Errorf("validatePasswordStrength(%q) = %q, wantErr=%v", c.pw, got, c.wantErr)
		}
	}
}

// ─── sha256Hex ───
func TestSha256Hex(t *testing.T) {
	// Vector conocido: sha256("abc")
	got := sha256Hex("abc")
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("sha256Hex(abc) = %q, want %q", got, want)
	}
}

// ─── wallpaper helpers ───
func TestWallpaperExtFromName(t *testing.T) {
	cases := map[string]string{
		"foto.png": "png", "img.JPG": "jpg", "x.jpeg": "jpg",
		"a.webp": "webp", "b.gif": "gif", "doc.pdf": "", "noext": "",
	}
	for in, want := range cases {
		if got := wallpaperExtFromName(in); got != want {
			t.Errorf("wallpaperExtFromName(%q) = %q, want %q", in, got, want)
		}
	}
}
