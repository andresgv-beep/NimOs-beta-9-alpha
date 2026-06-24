package main

import "testing"

func TestIsSensitiveRequestHeader(t *testing.T) {
	for _, h := range []string{"Authorization", "authorization", "  AUTHORIZATION  "} {
		if !isSensitiveRequestHeader(h) {
			t.Errorf("%q debería filtrarse", h)
		}
	}
	for _, h := range []string{"Cookie", "Accept", "User-Agent", "X-Forwarded-For", ""} {
		if isSensitiveRequestHeader(h) {
			t.Errorf("%q NO debería filtrarse", h)
		}
	}
}

func TestStripNimosCookie(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"nimos_token=abc", ""},
		{"nimos_token=abc; sid=xyz", "sid=xyz"},
		{"sid=xyz; nimos_token=abc", "sid=xyz"},
		{"sid=xyz; theme=dark", "sid=xyz; theme=dark"},
		{"  nimos_token=abc  ;  sid=xyz  ", "sid=xyz"},
		{"a=1; nimos_token=abc; b=2", "a=1; b=2"},
		// no debe confundir un nombre que contiene la subcadena
		{"nimos_token_extra=1; sid=2", "nimos_token_extra=1; sid=2"},
	}
	for _, c := range cases {
		if got := stripNimosCookie(c.in); got != c.want {
			t.Errorf("stripNimosCookie(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHeaderValueHasCRLF(t *testing.T) {
	if !headerValueHasCRLF("X-Foo", "bar\r\nInjected: evil") {
		t.Error("debería detectar CRLF en value")
	}
	if !headerValueHasCRLF("X-Foo\r\n", "bar") {
		t.Error("debería detectar CRLF en key")
	}
	if !headerValueHasCRLF("/path/\nGET /evil") {
		t.Error("debería detectar LF en request line")
	}
	if headerValueHasCRLF("X-Foo", "bar baz") {
		t.Error("no debería marcar un value limpio")
	}
	if headerValueHasCRLF("/normal/path?q=1") {
		t.Error("no debería marcar una request line limpia")
	}
}

func TestScopeCookiePath(t *testing.T) {
	cases := []struct {
		name, in, appID, want string
	}{
		{"reemplaza Path existente", "sid=abc; Path=/; HttpOnly", "gitea",
			"sid=abc; Path=/app/gitea/; HttpOnly"},
		{"añade Path si no hay", "sid=abc; HttpOnly", "n8n",
			"sid=abc; HttpOnly; Path=/app/n8n/"},
		{"solo nombre=valor", "sid=abc", "jellyfin",
			"sid=abc; Path=/app/jellyfin/"},
		{"Path en minúsculas", "sid=abc; path=/foo", "radarr",
			"sid=abc; Path=/app/radarr/"},
		{"vacío pasa tal cual", "", "x", ""},
	}
	for _, c := range cases {
		if got := scopeCookiePath(c.in, c.appID); got != c.want {
			t.Errorf("%s: scopeCookiePath(%q,%q) = %q, want %q", c.name, c.in, c.appID, got, c.want)
		}
	}
}
