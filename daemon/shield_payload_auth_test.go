// shield_payload_auth_test.go — Verifica la degradación de los matches de
// payload cuando la petición trae SESIÓN VÁLIDA.
//
// Contexto: los patrones de payload son heurísticos y el tráfico legítimo del
// dueño los pisa (backtick en un rename, "$(" en un compose, "-- " en
// markdown). Antes, un solo match de INJ-002 bloqueaba la IP 24h aunque la
// petición viniera del propio admin logueado → auto-lockout del NAS. Ahora:
//   · ANÓNIMO: sin cambios — INJ-002 instantáneo, INJ-001/CSP-001 acumulan.
//   · CON SESIÓN: evento + score (Fase 1), sin bloqueo por IP.

package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// drainShieldEvents vacía el canal global de eventos (buffered) para que el
// test pueda inspeccionar exactamente los suyos.
func drainShieldEvents() {
	for {
		select {
		case <-shieldEvents:
		default:
			return
		}
	}
}

func TestShieldMiddleware_INJ002_AnonymousInstantBlock(t *testing.T) {
	defer setupShieldTest(t)()
	ip := "203.0.113.20"

	r := httptest.NewRequest("GET", "/api/x?cmd=$(reboot)", nil)
	r.RemoteAddr = ip + ":54321"
	w := httptest.NewRecorder()

	handled := shieldMiddleware(w, r)
	if !handled {
		t.Fatal("INJ-002 anónimo debe cortarse en el middleware")
	}
	if blocked, _ := shieldIsBlocked(ip); !blocked {
		t.Fatal("INJ-002 anónimo debe bloquear la IP al instante (comportamiento histórico)")
	}
}

func TestShieldMiddleware_INJ002_AuthenticatedDegradesToEvent(t *testing.T) {
	defer setupShieldTest(t)()
	ip := "203.0.113.21"

	// Tabla de sesiones con el schema real de db.go (setupShieldTest no la
	// crea) y una sesión válida; la request presenta el token en crudo y el
	// server guarda su SHA-256, igual que auth.go.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			ip TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	rawToken := "test-token-degradacion"
	if err := dbSessionCreate(sha256Hex(rawToken), "admin", "admin", ip); err != nil {
		t.Fatalf("creando sesión de test: %v", err)
	}

	drainShieldEvents()

	// El clásico falso positivo: un rename con backtick en el nombre.
	body := `{"from":"/mnt/pool/docs/AC` + "`" + `DC.txt","to":"/mnt/pool/docs/ACDC.txt"}`
	r := httptest.NewRequest("POST", "/api/files/rename", strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.RemoteAddr = ip + ":54321"
	r.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	handled := shieldMiddleware(w, r)
	if handled {
		t.Fatal("con sesión válida la petición NO debe cortarse (falso positivo del dueño)")
	}
	if blocked, reason := shieldIsBlocked(ip); blocked {
		t.Fatalf("con sesión válida NO debe bloquearse la IP (bloqueada por: %s)", reason)
	}

	// Pero el evento SÍ debe emitirse, marcado como autenticado. Se busca el
	// INJ-002 entre lo que haya en el canal (otro subsistema, p.ej. intel,
	// podría haber emitido algo entremedias).
	found := false
	for !found {
		select {
		case ev := <-shieldEvents:
			if rule, _ := ev.Details["rule"].(string); rule == "INJ-002" {
				found = true
				if !eventIsAuthenticated(ev) {
					t.Error("el evento degradado debe llevar authenticated=true")
				}
			}
		default:
			t.Fatal("el match degradado debe seguir emitiendo el evento INJ-002 (score + registro)")
		}
	}
}

func TestProcessRules_AuthenticatedInjectionDoesNotAccumulate(t *testing.T) {
	defer setupShieldTest(t)()
	ip := "203.0.113.22"

	// 5 matches de SQLi AUTENTICADOS: muy por encima del umbral anónimo (3),
	// no deben bloquear.
	ev := ShieldEvent{
		Category: "injection", SourceIP: ip, Endpoint: "/api/notes",
		Details: map[string]interface{}{"rule": "INJ-001", "authenticated": true},
	}
	for i := 0; i < 5; i++ {
		processRules(ev)
	}
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("inyección autenticada no debe acumular hacia bloqueo por IP")
	}

	// Sanidad: el mismo volumen ANÓNIMO sí bloquea (sin regresión).
	anon := ShieldEvent{
		Category: "injection", SourceIP: ip, Endpoint: "/api/notes",
		Details: map[string]interface{}{"rule": "INJ-001"},
	}
	for i := 0; i < 3; i++ {
		processRules(anon)
	}
	if blocked, _ := shieldIsBlocked(ip); !blocked {
		t.Fatal("inyección anónima debe seguir acumulando y bloqueando (regresión de seguridad)")
	}
}

func TestProcessRules_AuthenticatedTraversalDoesNotAccumulate(t *testing.T) {
	defer setupShieldTest(t)()
	ip := "203.0.113.23"

	ev := ShieldEvent{
		Category: "traversal", SourceIP: ip, Endpoint: "/api/files",
		Details: map[string]interface{}{"rule": "TRAV-001", "authenticated": true},
	}
	for i := 0; i < 5; i++ {
		processRules(ev)
	}
	if blocked, _ := shieldIsBlocked(ip); blocked {
		t.Fatal("traversal autenticado no debe acumular hacia bloqueo por IP")
	}

	anon := ShieldEvent{
		Category: "traversal", SourceIP: ip, Endpoint: "/api/files",
		Details: map[string]interface{}{"rule": "TRAV-001"},
	}
	for i := 0; i < 3; i++ {
		processRules(anon)
	}
	if blocked, _ := shieldIsBlocked(ip); !blocked {
		t.Fatal("traversal anónimo debe seguir bloqueando (regresión de seguridad)")
	}
}
