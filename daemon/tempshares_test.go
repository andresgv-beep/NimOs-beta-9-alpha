package main

import (
	"strings"
	"testing"
)

func TestTempShareTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := tempShareNewToken()
		if err != nil {
			t.Fatalf("tempShareNewToken: %v", err)
		}
		if len(tok) != tempShareTokenLen {
			t.Fatalf("longitud %d, esperada %d", len(tok), tempShareTokenLen)
		}
		if !isBase62(tok) {
			t.Fatalf("token no base62: %q", tok)
		}
		if seen[tok] {
			t.Fatalf("token repetido en 100 iteraciones: %q", tok)
		}
		seen[tok] = true
	}
}

func TestIsBase62(t *testing.T) {
	cases := map[string]bool{
		"abcDEF1234": true,
		"abc-EF1234": false,
		"abc/EF1234": false,
		"abc EF1234": false,
		"":           true, // vacío no tiene chars inválidos; la longitud se valida aparte
	}
	for in, want := range cases {
		if got := isBase62(in); got != want {
			t.Errorf("isBase62(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTempShareValidateTTL(t *testing.T) {
	if _, err := tempShareValidateTTL(0.5); err == nil {
		t.Error("ttl 0.5h debería fallar (mínimo 1h)")
	}
	if _, err := tempShareValidateTTL(721); err == nil {
		t.Error("ttl 721h debería fallar (máximo 720h)")
	}
	exp, err := tempShareValidateTTL(24)
	if err != nil {
		t.Fatalf("ttl 24h válido falló: %v", err)
	}
	if exp <= 0 {
		t.Error("expiración no positiva")
	}
}

func TestTempShareSlots(t *testing.T) {
	tok := "testtok123"
	defer func() { // limpiar estado global entre tests
		tempShareSlotsMu.Lock()
		delete(tempShareSlots, tok)
		tempShareSlotsMu.Unlock()
	}()

	// Sin límite: siempre entra
	for i := 0; i < 10; i++ {
		if !tempShareAcquireSlot(tok, 0) {
			t.Fatal("max=0 debería ser ilimitado")
		}
	}

	// Límite 2: dos entran, el tercero no
	if !tempShareAcquireSlot(tok, 2) || !tempShareAcquireSlot(tok, 2) {
		t.Fatal("los dos primeros slots deberían entrar")
	}
	if tempShareAcquireSlot(tok, 2) {
		t.Fatal("el tercer slot debería rechazarse")
	}
	tempShareReleaseSlot(tok, 2)
	if !tempShareAcquireSlot(tok, 2) {
		t.Fatal("tras liberar, debería entrar de nuevo")
	}
	// Liberar todo y comprobar que el mapa queda limpio
	tempShareReleaseSlot(tok, 2)
	tempShareReleaseSlot(tok, 2)
	tempShareSlotsMu.Lock()
	_, exists := tempShareSlots[tok]
	tempShareSlotsMu.Unlock()
	if exists {
		t.Error("el token debería eliminarse del mapa al llegar a 0")
	}
}

func TestTempShareHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:                "512 B",
		2048:               "2.0 KB",
		5 * 1024 * 1024:    "5.0 MB",
		1024 * 1024 * 1024: "1.0 GB",
	}
	for in, want := range cases {
		if got := tempShareHumanSize(in); got != want {
			t.Errorf("tempShareHumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTempSharePageTemplateRenders(t *testing.T) {
	var sb strings.Builder
	err := tempSharePageTpl.Execute(&sb, tempSharePageData{
		Title: "x.pdf", FileName: "x.pdf", SizeHuman: "1.0 MB",
		Remaining: "23h 5m", NeedsPassword: true, Downloads: 3,
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "x.pdf") || !strings.Contains(out, "password") {
		t.Error("la página no contiene lo esperado")
	}
	// Variante error
	sb.Reset()
	if err := tempSharePageTpl.Execute(&sb, tempSharePageData{
		Title: "Enlace caducado", Message: "expiró", IsError: true,
	}); err != nil {
		t.Fatalf("template error-variant: %v", err)
	}
	if !strings.Contains(sb.String(), "Enlace caducado") {
		t.Error("la variante de error no renderiza el título")
	}
}
