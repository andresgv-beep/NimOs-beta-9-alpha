package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCheckRequestBody_DetectsInjection — N2: payload malicioso en el body JSON
// debe detectarse (query/path no lo verían).
func TestCheckRequestBody_DetectsInjection(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"xss in json field", `{"name":"<script>alert(1)</script>"}`},
		{"sqli in json field", `{"user":"admin' OR '1'='1"}`},
		{"encoded xss in body", `{"q":"%3Cscript%3E"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/x", strings.NewReader(c.body))
			r.ContentLength = int64(len(c.body))
			if rule := checkRequestBody(r); rule == "" {
				t.Errorf("expected detection for body %q, got none", c.body)
			}
		})
	}
}

// TestCheckRequestBody_ReInjects — N2 crítico: tras inspeccionar, el handler
// downstream debe poder leer el MISMO body. Si esto falla, todos los POST se
// rompen.
func TestCheckRequestBody_ReInjects(t *testing.T) {
	body := `{"username":"alice","password":"s3cret"}`
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	r.ContentLength = int64(len(body))

	_ = checkRequestBody(r)

	// El handler debe leer exactamente lo mismo.
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading re-injected body: %v", err)
	}
	if string(got) != body {
		t.Errorf("re-injected body mismatch:\n got: %q\nwant: %q", string(got), body)
	}
}

// TestCheckRequestBody_CleanBodyPasses — un POST legítimo no debe marcarse y
// debe conservar su body.
func TestCheckRequestBody_CleanBodyPasses(t *testing.T) {
	body := `{"name":"Documentos","quota":"50GB"}`
	r := httptest.NewRequest("POST", "/api/shares", strings.NewReader(body))
	r.ContentLength = int64(len(body))
	if rule := checkRequestBody(r); rule != "" {
		t.Errorf("clean body flagged as %q", rule)
	}
	got, _ := io.ReadAll(r.Body)
	if string(got) != body {
		t.Errorf("clean body not preserved: %q", string(got))
	}
}

// TestCheckRequestBody_OversizeSkipped — un body por encima del cap no se
// inspecciona y NO se trunca para el handler (se deja el stream intacto).
func TestCheckRequestBody_OversizeSkipped(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("x"))
	r.ContentLength = shieldMaxBodyInspect + 1
	if rule := checkRequestBody(r); rule != "" {
		t.Errorf("oversize body should be skipped, got rule %q", rule)
	}
}

// TestCheckRequestBody_NoBody — GET/bodyless no debe romper.
func TestCheckRequestBody_NoBody(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/x", nil)
	if rule := checkRequestBody(r); rule != "" {
		t.Errorf("bodyless request should be inert, got %q", rule)
	}
}

// TestHoneypotPrefix_NoAppCollision — N4: una ruta bajo /app/ no debe activar
// honeypot por prefijo aunque contenga un substring tipo honeypot.
func TestHoneypotPrefix_NoAppCollision(t *testing.T) {
	// /app/... nunca es honeypot por prefijo.
	r := httptest.NewRequest("GET", "/app/myapp/console/page", nil)
	if checkHoneypotPathOnly(r) {
		t.Error("/app/ route must not trigger honeypot prefix match")
	}
	// Un prefijo de ataque real SÍ debe activar.
	r2 := httptest.NewRequest("GET", "/cgi-bin/evil.sh", nil)
	if !checkHoneypotPathOnly(r2) {
		t.Error("/cgi-bin/ should trigger honeypot prefix match")
	}
}

// checkHoneypotPathOnly replica la decisión de match de checkHoneypot SIN
// efectos secundarios (no bloquea IP, no emite eventos), para test puro.
func checkHoneypotPathOnly(r *http.Request) bool {
	path := strings.ToLower(r.URL.Path)
	if _, ok := honeypotPaths[path]; ok {
		return true
	}
	if !strings.HasPrefix(path, "/app/") {
		for hPath := range honeypotPrefixes {
			if strings.HasPrefix(path, hPath) {
				return true
			}
		}
	}
	return false
}

// TestMatchesPatterns_Normalization — N5: evasión por comentario SQL inline y
// whitespace alternativo debe caer.
func TestMatchesPatterns_Normalization(t *testing.T) {
	pats := []string{"union select"}
	evasions := []string{
		"UNION SELECT",
		"UN/**/ION SELECT",
		"union/**/select",
		"union\tselect",
		"union   select",
	}
	for _, e := range evasions {
		if !matchesPatterns(e, pats) {
			t.Errorf("expected %q to match 'union select' after normalization", e)
		}
	}
	// No falsos positivos en texto benigno.
	if matchesPatterns("my favourite band is union", pats) {
		t.Error("benign text should not match")
	}
}

// TestCheckRequestBody_SkipsFileUploads — un upload legítimo (p.ej. un .zip con
// código Go dentro) NO debe inspeccionarse por inyección: su cuerpo es binario
// arbitrario y dispararía falsos positivos. Regresión del autobloqueo al dueño
// subiendo ficheros.
func TestCheckRequestBody_SkipsFileUploads(t *testing.T) {
	// Cuerpo que SÍ casaría con patrones de inyección si se inspeccionara
	// (código Go con SQL y exec, como el que viaja dentro de un .zip).
	malicious := `package main; db.Query("SELECT * FROM users WHERE x="); exec.Command("sh","-c","rm")`

	cases := []struct {
		name    string
		path    string
		headers map[string]string
	}{
		{"chunk upload por ruta", "/api/files/upload-chunk", map[string]string{"X-Filename": "NimOs.zip", "X-Chunk-Index": "0"}},
		{"multipart upload por ruta", "/api/files/upload", map[string]string{"Content-Type": "multipart/form-data; boundary=xyz"}},
		{"zip por content-type", "/api/x", map[string]string{"Content-Type": "application/zip"}},
		{"octet-stream", "/api/x", map[string]string{"Content-Type": "application/octet-stream"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", c.path, strings.NewReader(malicious))
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if rule := checkRequestBody(r); rule != "" {
				t.Errorf("upload legítimo marcado como %q (falso positivo)", rule)
			}
			// el cuerpo debe seguir disponible para el handler
			if b, _ := io.ReadAll(r.Body); string(b) != malicious {
				t.Error("el cuerpo no se re-inyectó tras la (no) inspección")
			}
		})
	}
}

// TestCheckRequestBody_StillDetectsAPIInjection — el arreglo de uploads no debe
// debilitar la detección real: inyección en JSON de API sigue cazándose.
func TestCheckRequestBody_StillDetectsAPIInjection(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/shares", strings.NewReader(`{"name":"'; DROP TABLE users;--"}`))
	r.Header.Set("Content-Type", "application/json")
	if rule := checkRequestBody(r); rule == "" {
		t.Error("inyección SQL real en API NO detectada (regresión de seguridad)")
	}
}
