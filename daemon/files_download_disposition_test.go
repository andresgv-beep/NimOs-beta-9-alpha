// files_download_disposition_test.go — Verifica que la acción "Descargar"
// (?download=1) fuerza Content-Disposition: attachment aunque el fichero sea
// reproducible (audio/vídeo), y que sin el flag el media se sirve inline (para
// "Abrir"/preview y el streaming del futuro MediaPlayer).

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveFromTempDir crea un fichero temporal y lo sirve por serveFileDownload
// con la query dada, devolviendo la respuesta grabada.
func serveFromTempDir(t *testing.T, fileName, query string) *httptest.ResponseRecorder {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("datos de prueba"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	r := httptest.NewRequest("GET", "/api/files/download?"+query, nil)
	w := httptest.NewRecorder()
	serveFileDownload(w, r, root, fileName)
	return w
}

func TestServeFileDownload_MediaInlineByDefault(t *testing.T) {
	// mp4 sin ?download=1 → NO adjunto (inline, reproducible en pestaña /
	// seekable para el reproductor).
	w := serveFromTempDir(t, "peli.mp4", "")
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("un media sin download=1 no debe ser adjunto; Content-Disposition=%q", cd)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}
}

func TestServeFileDownload_MediaForcedAttachment(t *testing.T) {
	// mp3 con ?download=1 → adjunto (descarga real, no reproducir).
	w := serveFromTempDir(t, "cancion.mp3", "download=1")
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "cancion.mp3") {
		t.Errorf("con download=1 el media debe ser adjunto; Content-Disposition=%q", cd)
	}
}

func TestServeFileDownload_UnknownTypeAlwaysAttachment(t *testing.T) {
	// Tipo desconocido → adjunto siempre, con o sin flag (comportamiento previo).
	for _, q := range []string{"", "download=1"} {
		w := serveFromTempDir(t, "backup.bin", q)
		if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("tipo desconocido (query=%q) debe ser adjunto; Content-Disposition=%q", q, cd)
		}
	}
}

func TestServeFileDownload_RangeForcedAttachment(t *testing.T) {
	// Un descargador con Range + download=1 también recibe adjunto (206).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "peli.mp4"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	r := httptest.NewRequest("GET", "/api/files/download?download=1", nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	serveFileDownload(w, r, root, "peli.mp4")

	if w.Code != http.StatusPartialContent {
		t.Fatalf("Range debe dar 206, dio %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Range con download=1 debe ser adjunto; Content-Disposition=%q", cd)
	}
}
