// seed_files_test.go — Tests del mecanismo de sembrado de ficheros.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQbtPasswordHash_Deterministic(t *testing.T) {
	salt := []byte("0123456789abcdef") // 16 bytes
	a, err := qbtPasswordHashWithSalt("test123", salt)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := qbtPasswordHashWithSalt("test123", salt)
	if a != b {
		t.Fatalf("mismo password+salt debe dar mismo hash: %q vs %q", a, b)
	}
	// Formato: @ByteArray(<salt_b64>:<hash_b64>)
	if !strings.HasPrefix(a, "@ByteArray(") || !strings.HasSuffix(a, ")") {
		t.Fatalf("formato @ByteArray esperado, got %q", a)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(a, "@ByteArray("), ")")
	parts := strings.Split(inner, ":")
	if len(parts) != 2 {
		t.Fatalf("esperaba salt:hash, got %q", inner)
	}
}

func TestQbtPasswordHash_RandomSaltDiffers(t *testing.T) {
	a, _ := qbtPasswordHash("test123")
	b, _ := qbtPasswordHash("test123")
	if a == b {
		t.Fatal("salt aleatorio debe dar hashes distintos para el mismo password")
	}
}

func TestSubstituteSeedContent_DirectAndGenerator(t *testing.T) {
	content := `WebUI\Username={{WEBUI_USER}}
WebUI\Password_PBKDF2="{{QBT_PBKDF2:WEBUI_PASS}}"`
	values := map[string]string{"WEBUI_USER": "nimos", "WEBUI_PASS": "test123"}
	out, errs := substituteSeedContent(content, values)
	if len(errs) != 0 {
		t.Fatalf("no debería haber errores, got %v", errs)
	}
	if !strings.Contains(out, "WebUI\\Username=nimos") {
		t.Fatalf("sustitución directa falló: %q", out)
	}
	if !strings.Contains(out, `"@ByteArray(`) {
		t.Fatalf("el generador QBT_PBKDF2 no produjo @ByteArray: %q", out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("quedaron placeholders sin sustituir: %q", out)
	}
}

func TestSubstituteSeedContent_UnknownGenerator(t *testing.T) {
	out, errs := substituteSeedContent("x={{FOO:BAR}}", map[string]string{})
	if len(errs) == 0 {
		t.Fatal("un generador desconocido debe reportar error")
	}
	if !strings.Contains(out, "x=") {
		t.Fatalf("el resto debe quedar; got %q", out)
	}
}

func TestSubstituteSeedContent_MissingValueEmpty(t *testing.T) {
	out, errs := substituteSeedContent("a={{NOPE}}b", map[string]string{})
	if len(errs) != 0 {
		t.Fatalf("clave directa ausente no es error, got %v", errs)
	}
	if out != "a=b" {
		t.Fatalf("clave ausente → vacío; got %q", out)
	}
}

func TestWriteSeedFiles_WritesAndSkips(t *testing.T) {
	dir := t.TempDir()
	seeds := []SeedFile{
		{Path: "config/qBittorrent/qBittorrent.conf", Content: "user={{U}}\n", SkipIfExists: true},
	}
	writeSeedFiles(dir, seeds, map[string]string{"U": "nimos"})
	dst := filepath.Join(dir, "config/qBittorrent/qBittorrent.conf")
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("no se escribió el fichero: %v", err)
	}
	if string(b) != "user=nimos\n" {
		t.Fatalf("contenido inesperado: %q", string(b))
	}
	// skipIfExists: segunda pasada con contenido distinto NO debe pisar.
	writeSeedFiles(dir, []SeedFile{{Path: "config/qBittorrent/qBittorrent.conf", Content: "user=OTRO\n", SkipIfExists: true}}, map[string]string{})
	b2, _ := os.ReadFile(dst)
	if string(b2) != "user=nimos\n" {
		t.Fatalf("skipIfExists debió preservar el original; got %q", string(b2))
	}
}

func TestWriteSeedFiles_BlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	writeSeedFiles(dir, []SeedFile{{Path: "../escapado.conf", Content: "x"}}, map[string]string{})
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escapado.conf")); err == nil {
		t.Fatal("un path con ../ no debe escribir fuera del volumen")
	}
}

func TestParseSeedFiles(t *testing.T) {
	raw := []interface{}{
		map[string]interface{}{"path": "config/x.conf", "content": "a={{B}}", "skipIfExists": true},
		map[string]interface{}{"path": "", "content": "ignorar"}, // sin path → fuera
		"basura", // no es objeto → fuera
	}
	out := parseSeedFiles(raw)
	if len(out) != 1 {
		t.Fatalf("esperaba 1 seedFile válido, got %d (%v)", len(out), out)
	}
	if out[0].Path != "config/x.conf" || !out[0].SkipIfExists {
		t.Fatalf("parseo incorrecto: %+v", out[0])
	}
}
