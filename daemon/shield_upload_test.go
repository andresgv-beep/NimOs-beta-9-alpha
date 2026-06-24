package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════
// BLINDAJE · shieldIsUploadRequest · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Verifica que el skip de inspección SOLO se concede en rutas de upload
// conocidas, cerrando el hueco teórico: un atacante NO puede saltarse la
// inspección mandando Content-Type binario a un endpoint de API normal.
// Y a la vez, las subidas legítimas (Files, wallpapers, torrents, chunks)
// SÍ siguen saltándose la inspección.
// ═══════════════════════════════════════════════════════════════════════

func reqWith(path, contentType string, headers map[string]string) *http.Request {
	r := httptest.NewRequest("POST", path, nil)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestShieldIsUploadRequest_LegitimateUploads(t *testing.T) {
	// Todas estas DEBEN saltarse la inspección (return true).
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"files upload", reqWith("/api/files/upload", "multipart/form-data; boundary=x", nil)},
		{"files upload-chunk", reqWith("/api/files/upload-chunk", "application/octet-stream", nil)},
		{"chunk by headers (sin content-type)", reqWith("/api/files/upload-chunk", "", map[string]string{"X-Filename": "foo.zip", "X-Chunk-Index": "0"})},
		{"wallpaper multipart", reqWith("/api/user/wallpaper", "multipart/form-data; boundary=x", nil)},
		{"wallpaper base64-json", reqWith("/api/user/wallpaper", "application/json", nil)},
		{"torrent upload", reqWith("/api/torrent/upload", "multipart/form-data; boundary=x", nil)},
	}
	for _, c := range cases {
		if !shieldIsUploadRequest(c.req) {
			t.Errorf("%s: esperaba true (subida legítima), got false", c.name)
		}
	}
}

func TestShieldIsUploadRequest_AttackBlinded(t *testing.T) {
	// EL HUECO CERRADO: Content-Type binario en un endpoint de API normal NO
	// debe conceder skip. Antes, con octet-stream se saltaba la inspección;
	// ahora NO, porque no es una ruta de upload conocida.
	attacks := []struct {
		name string
		req  *http.Request
	}{
		{"login con octet-stream", reqWith("/api/auth/login", "application/octet-stream", nil)},
		{"pools import con zip", reqWith("/api/storage/v2/pools/import", "application/zip", nil)},
		{"endpoint api con multipart", reqWith("/api/shares/test", "multipart/form-data; boundary=x", nil)},
		{"endpoint api con image/png falso", reqWith("/api/users", "image/png", nil)},
	}
	for _, a := range attacks {
		if shieldIsUploadRequest(a.req) {
			t.Errorf("%s: esperaba false (NO es ruta de upload, debe inspeccionarse), got true — HUECO ABIERTO", a.name)
		}
	}
}

func TestShieldIsUploadRequest_NormalApiStillInspected(t *testing.T) {
	// Una llamada JSON normal a la API no es upload → se inspecciona.
	r := reqWith("/api/auth/login", "application/json", nil)
	if shieldIsUploadRequest(r) {
		t.Error("una llamada JSON normal no debería tratarse como upload")
	}
}
