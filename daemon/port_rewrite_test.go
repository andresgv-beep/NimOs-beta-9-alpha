package main

import (
	"strings"
	"testing"
)

// ─── Nivel string · rewriteHostPortSpec ──────────────────────────────────────

func TestRewriteHostPortSpec_AllForms(t *testing.T) {
	remap := map[int]int{8080: 30000, 8096: 30001, 51413: 30002}
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"host:container", "8080:80", "30000:80", true},
		{"ip:host:container", "127.0.0.1:8080:80", "127.0.0.1:30000:80", true},
		{"0.0.0.0 prefijo", "0.0.0.0:8080:80", "0.0.0.0:30000:80", true},
		{"con /udp", "8080:80/udp", "30000:80/udp", true},
		{"con /tcp", "8080:80/tcp", "30000:80/tcp", true},
		{"escalar host=container", "8096", "30001:8096", true},
		{"peer tcp", "51413:51413", "30002:51413", true},
		{"peer udp misma var", "51413:51413/udp", "30002:51413/udp", true},
		{"host no en remap", "1234:80", "1234:80", false},
		{"host no numérico (var)", "${CODE_PORT}:8443", "${CODE_PORT}:8443", false},
		{"vacío", "", "", false},
		{"espacios", "  8080:80  ", "30000:80", true},
	}
	for _, c := range cases {
		got, ok := rewriteHostPortSpec(c.in, remap)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: rewriteHostPortSpec(%q)=(%q,%v), want (%q,%v)",
				c.name, c.in, got, ok, c.want, c.ok)
		}
	}
}

// ─── Nivel compose · rewriteComposeHostPorts ─────────────────────────────────

func TestRewriteComposeHostPorts_MultiPortTransmission(t *testing.T) {
	src := []byte(`services:
  transmission:
    image: linuxserver/transmission:latest
    ports:
      - "9091:9091"        # web
      - "51413:51413"      # peer tcp
      - "51413:51413/udp"  # peer udp
`)
	// web 9091 → 30000, peer 51413 → 30002 (tcp y udp comparten host)
	remap := map[int]int{9091: 30000, 51413: 30002}
	out, changed, err := rewriteComposeHostPorts(src, remap)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if changed != 3 { // web + peer tcp + peer udp
		t.Errorf("changed=%d, want 3", changed)
	}
	s := string(out)
	for _, want := range []string{"30000:9091", "30002:51413", "30002:51413/udp"} {
		if !strings.Contains(s, want) {
			t.Errorf("salida no contiene %q\n%s", want, s)
		}
	}
	if strings.Contains(s, `"9091:9091"`) || strings.Contains(s, `"51413:51413"`+"\n") {
		t.Errorf("la salida aún contiene un mapeo viejo\n%s", s)
	}
}

func TestRewriteComposeHostPorts_LongSyntax(t *testing.T) {
	src := []byte(`services:
  web:
    image: nginx
    ports:
      - target: 80
        published: 8080
        protocol: tcp
`)
	remap := map[int]int{8080: 30000}
	out, changed, err := rewriteComposeHostPorts(src, remap)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if changed != 1 {
		t.Errorf("changed=%d, want 1", changed)
	}
	s := string(out)
	if !strings.Contains(s, "30000") {
		t.Errorf("published no se remapeó a 30000\n%s", s)
	}
	if !strings.Contains(s, "protocol: tcp") {
		t.Errorf("protocol no se preservó\n%s", s)
	}
}

func TestRewriteComposeHostPorts_Scalar(t *testing.T) {
	src := []byte(`services:
  jellyfin:
    image: jellyfin/jellyfin
    ports:
      - 8096
`)
	remap := map[int]int{8096: 30050}
	out, changed, err := rewriteComposeHostPorts(src, remap)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if changed != 1 {
		t.Errorf("changed=%d, want 1", changed)
	}
	if !strings.Contains(string(out), "30050:8096") {
		t.Errorf("escalar no se remapeó a \"30050:8096\"\n%s", out)
	}
}

func TestRewriteComposeHostPorts_NoOps(t *testing.T) {
	src := []byte(`services:
  app:
    ports:
      - "8081:8081"
`)
	// remap vacío → sin cambios
	if _, changed, err := rewriteComposeHostPorts(src, map[int]int{}); err != nil || changed != 0 {
		t.Errorf("remap vacío: changed=%d err=%v, want 0,nil", changed, err)
	}
	// host no presente en remap → sin cambios
	if _, changed, err := rewriteComposeHostPorts(src, map[int]int{9999: 30000}); err != nil || changed != 0 {
		t.Errorf("host ausente: changed=%d err=%v, want 0,nil", changed, err)
	}
}

func TestRewriteComposeHostPorts_VarHostUntouched(t *testing.T) {
	// Si el compose ya usa ${VAR} en el host, el reescritor no lo toca
	// (lo resuelve el wiring por env en Fase 4).
	src := []byte(`services:
  code:
    ports:
      - "${CODE_PORT}:8443"
`)
	_, changed, err := rewriteComposeHostPorts(src, map[int]int{8443: 30000})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if changed != 0 {
		t.Errorf("host ${VAR} no debe remapearse, changed=%d", changed)
	}
}

func TestRewriteComposeHostPorts_PreservesIPPrefix(t *testing.T) {
	src := []byte(`services:
  app:
    ports:
      - "127.0.0.1:9091:9091"
`)
	out, changed, err := rewriteComposeHostPorts(src, map[int]int{9091: 30000})
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	if !strings.Contains(string(out), "127.0.0.1:30000:9091") {
		t.Errorf("la IP de loopback no se preservó con el host remapeado\n%s", out)
	}
}
