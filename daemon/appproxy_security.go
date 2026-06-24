package main

import "strings"

// Helpers de seguridad del reverse proxy de apps (appproxy.go).
// Items del audit AppStore · Fase 6 (mini-tarea de hardening del proxy):
//   APP-060 · sanitize CRLF en el handshake WebSocket manual
//   APP-061 · cookie scoping cross-app (confinar Set-Cookie al path de la app)
//   APP-062 · no filtrar la auth de NimOS (Authorization + cookie nimos_token)
//             al backend de la app.
//
// Todo aquí es lógica PURA y testeable · el wiring vive en appproxy.go.

// nimosSessionCookie · nombre de la cookie de sesión de NimOS (ver auth.go).
const nimosSessionCookie = "nimos_token"

// isSensitiveRequestHeader · ¿este header de request NO debe reenviarse al
// backend de la app? (APP-062) Authorization es la credencial de NimOS: la app
// local detrás del proxy ya está protegida por la auth de NimOS (la sesión se
// validó antes de llegar aquí) y no debe ver el token. PURA.
func isSensitiveRequestHeader(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "Authorization")
}

// stripNimosCookie · quita SOLO la cookie de sesión de NimOS (nimos_token) de un
// header Cookie, conservando el resto (las cookies propias de la app). (APP-062)
// PURA. Devuelve "" si tras quitarla no queda nada (el caller omite el header).
func stripNimosCookie(cookieHeader string) string {
	if cookieHeader == "" {
		return ""
	}
	parts := strings.Split(cookieHeader, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		name := trimmed
		if i := strings.IndexByte(trimmed, '='); i >= 0 {
			name = strings.TrimSpace(trimmed[:i])
		}
		if name == nimosSessionCookie {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}

// headerValueHasCRLF · detecta inyección CRLF en lo que se va a escribir a pelo
// en el handshake WebSocket (key, value, o la request line). (APP-060) Un '\r'
// o '\n' permite HTTP request smuggling → el caller rechaza/omite. PURA.
func headerValueHasCRLF(parts ...string) bool {
	for _, p := range parts {
		if strings.ContainsAny(p, "\r\n") {
			return true
		}
	}
	return false
}

// scopeCookiePath · reescribe un valor Set-Cookie para confinar la cookie al
// path del proxy de esa app (/app/{appId}/), evitando que la cookie de la app A
// sea visible para la app B o NimOS. (APP-061) PURA. Si ya trae Path, lo
// reemplaza; si no, lo añade.
func scopeCookiePath(setCookie, appID string) string {
	if setCookie == "" {
		return setCookie
	}
	scoped := "/app/" + appID + "/"
	attrs := strings.Split(setCookie, ";")
	hadPath := false
	for i, a := range attrs {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a)), "path=") {
			attrs[i] = " Path=" + scoped
			hadPath = true
		}
	}
	out := strings.Join(attrs, ";")
	if !hadPath {
		out += "; Path=" + scoped
	}
	return out
}
