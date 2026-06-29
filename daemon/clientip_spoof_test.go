package main

import (
	"net/http/httptest"
	"testing"
)

// #1: clientIP no debe dejarse engañar por un X-Forwarded-For spoofeado.
// Caddy añade la IP real al FINAL; el cliente controla lo de la izquierda.
func TestClientIP_XFFSpoofRejected(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		xri    string
		xff    string
		want   string
	}{
		{
			name:   "X-Real-IP gana (Caddy lo fija con el peer real)",
			remote: "127.0.0.1:54321",
			xri:    "203.0.113.5",
			xff:    "8.8.8.8, 203.0.113.5", // el cliente metió 8.8.8.8 a la izquierda
			want:   "203.0.113.5",
		},
		{
			name:   "sin X-Real-IP, XFF rightmost (el que añade Caddy)",
			remote: "127.0.0.1:54321",
			xff:    "8.8.8.8, 203.0.113.7", // atacante spoofea 8.8.8.8
			want:   "203.0.113.7",          // NimOS debe tomar el último
		},
		{
			name:   "XFF de un solo elemento (lo pone Caddy)",
			remote: "::1:54321",
			xff:    "198.51.100.9",
			want:   "198.51.100.9",
		},
		{
			name:   "conexión NO loopback: se ignora XFF del todo",
			remote: "203.0.113.200:443",
			xff:    "8.8.8.8", // intento de spoof directo, sin pasar por Caddy
			want:   "203.0.113.200",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remote
			if c.xri != "" {
				r.Header.Set("X-Real-IP", c.xri)
			}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			got := clientIP(r)
			if got != c.want {
				t.Errorf("clientIP = %q, want %q (spoof no mitigado)", got, c.want)
			}
		})
	}
}

// El ataque concreto del informe: presentarse como una IP whitelisteada.
func TestClientIP_CannotSpoofWhitelist(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	// atacante intenta colar una IP "de confianza" a la izquierda del XFF
	r.Header.Set("X-Forwarded-For", "10.0.0.99, 203.0.113.66")
	got := clientIP(r)
	if got == "10.0.0.99" {
		t.Fatal("VULNERABLE: el atacante pudo spoofear la IP de la izquierda")
	}
	if got != "203.0.113.66" {
		t.Errorf("clientIP=%q, esperado la IP real 203.0.113.66", got)
	}
}
