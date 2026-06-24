// docker_access_mode_test.go — Tests del motor de reescritura de ports
// (SHIELD-P2). La función es pura: YAML entra, YAML sale. Cubrimos todas
// las formas de publicar puertos que docker compose acepta, los dos
// sentidos (candado y vuelta a LAN), e idempotencia.

package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const composeFixture = `# stack generado por NimOS
services:
  navidrome:
    image: deluan/navidrome:latest
    ports:
      - "4533:4533"
    volumes:
      - ./data:/data # datos de la app
  scrobbler:
    image: example/sidecar
    ports:
      - "8080:80/tcp"
      - 9090
      - "0.0.0.0:7070:70"
      - target: 443
        published: 8443
    environment:
      - TZ=Europe/Madrid
`

func TestRewriteComposePorts_Loopback(t *testing.T) {
	out, changed, err := rewriteComposePorts([]byte(composeFixture), true)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if changed != 5 {
		t.Errorf("changed = %d, want 5 (todas las publicaciones)", changed)
	}
	s := string(out)
	for _, want := range []string{
		`"127.0.0.1:4533:4533"`,
		`"127.0.0.1:8080:80/tcp"`,
		`"127.0.0.1:9090:9090"`,
		`"127.0.0.1:7070:70"`, // 0.0.0.0 explícito → forzado a loopback
		`host_ip: "127.0.0.1"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	// El YAML resultante debe seguir siendo compose válido y los datos
	// no-ports intactos.
	var doc map[string]interface{}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	if !strings.Contains(s, "TZ=Europe/Madrid") || !strings.Contains(s, "./data:/data") {
		t.Error("non-port content was altered")
	}
}

func TestRewriteComposePorts_LoopbackIsIdempotent(t *testing.T) {
	once, _, err := rewriteComposePorts([]byte(composeFixture), true)
	if err != nil {
		t.Fatal(err)
	}
	_, changed, err := rewriteComposePorts(once, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Errorf("second pass changed = %d, want 0 (idempotente)", changed)
	}
}

func TestRewriteComposePorts_BackToLAN(t *testing.T) {
	locked, _, err := rewriteComposePorts([]byte(composeFixture), true)
	if err != nil {
		t.Fatal(err)
	}
	out, changed, err := rewriteComposePorts(locked, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed == 0 {
		t.Fatal("unlock should change entries")
	}
	s := string(out)
	if strings.Contains(s, "127.0.0.1") {
		t.Errorf("loopback binds should be gone:\n%s", s)
	}
	if !strings.Contains(s, `"4533:4533"`) || !strings.Contains(s, `"8080:80/tcp"`) {
		t.Errorf("original publications not restored:\n%s", s)
	}
}

func TestRewriteComposePorts_RespectsForeignIPs(t *testing.T) {
	// Un bind a una IP explícita distinta (VLAN del usuario) NO se toca al
	// volver a LAN — es intención suya, no nuestro candado.
	src := []byte("services:\n  app:\n    ports:\n      - \"192.168.50.10:8080:80\"\n")
	out, changed, err := rewriteComposePorts(src, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Errorf("foreign IP bind must not be touched on unlock (changed=%d)", changed)
	}
	if !strings.Contains(string(out), "192.168.50.10:8080:80") {
		t.Error("foreign IP bind lost")
	}
	// ...pero el candado SÍ lo fuerza a loopback (cerrar es cerrar TODO).
	out, changed, _ = rewriteComposePorts(src, true)
	if changed != 1 || !strings.Contains(string(out), `"127.0.0.1:8080:80"`) {
		t.Errorf("lock should force loopback even on explicit IPs: %s", out)
	}
}

func TestRewriteComposePorts_EnvVarsSurvive(t *testing.T) {
	// Puertos con variables (${PORT}:80) — el prefijo se antepone sin
	// romper la interpolación de compose.
	src := []byte("services:\n  app:\n    ports:\n      - \"${APP_PORT}:80\"\n")
	out, changed, err := rewriteComposePorts(src, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 || !strings.Contains(string(out), `"127.0.0.1:${APP_PORT}:80"`) {
		t.Errorf("env var publication mishandled: %s", out)
	}
}

func TestRewriteComposePorts_NoServicesNoOp(t *testing.T) {
	src := []byte("version: \"3\"\nvolumes:\n  data:\n")
	out, changed, err := rewriteComposePorts(src, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 || string(out) != string(src) {
		t.Error("compose without services must pass through untouched")
	}
}

func TestRewriteComposePorts_PreservesComments(t *testing.T) {
	out, _, err := rewriteComposePorts([]byte(composeFixture), true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# stack generado por NimOS") || !strings.Contains(s, "# datos de la app") {
		t.Errorf("comments must survive the rewrite:\n%s", s)
	}
}
