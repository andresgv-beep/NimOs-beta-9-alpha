package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// ─── parseComposeBindings ────────────────────────────────────────────────────

func itoaProto(b PortBinding) string {
	return strconv.Itoa(b.Host) + "/" + b.Protocol
}

func TestParseComposeBindings_MultiPort(t *testing.T) {
	compose := `services:
  transmission:
    ports:
      - "9091:9091"
      - "51413:51413"
      - "51413:51413/udp"
`
	bs, err := parseComposeBindings(compose)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(bs) != 3 {
		t.Fatalf("esperaba 3 bindings, got %d (%+v)", len(bs), bs)
	}
	want := map[string]PortBinding{
		"9091/tcp":  {Declared: 9091, Host: 9091, Protocol: "tcp"},
		"51413/tcp": {Declared: 51413, Host: 51413, Protocol: "tcp"},
		"51413/udp": {Declared: 51413, Host: 51413, Protocol: "udp"},
	}
	got := map[string]PortBinding{}
	for _, b := range bs {
		got[itoaProto(b)] = b
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("binding %s = %+v, want %+v", k, got[k], v)
		}
	}
}

func TestParseComposeBindings_FormsAndVars(t *testing.T) {
	compose := `services:
  a:
    ports:
      - "8082:80"
      - "127.0.0.1:9000:9000"
      - 8096
      - "${CODE_PORT}:8443"
  b:
    ports:
      - target: 3000
        published: 3001
        protocol: tcp
`
	bs, err := parseComposeBindings(compose)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(bs) != 4 {
		t.Fatalf("esperaba 4 bindings (el ${VAR} se omite), got %d (%+v)", len(bs), bs)
	}
	hosts := map[int]int{}
	for _, b := range bs {
		hosts[b.Host] = b.Declared
	}
	checks := map[int]int{8082: 80, 9000: 9000, 8096: 8096, 3001: 3000}
	for h, d := range checks {
		if hosts[h] != d {
			t.Errorf("host %d → declared %d, want %d", h, hosts[h], d)
		}
	}
	if _, ok := hosts[8443]; ok {
		t.Errorf("el binding con host ${VAR} no debería parsearse")
	}
}

// ─── resolveStackHostPorts ───────────────────────────────────────────────────

func bindingsFromJSON(t *testing.T, js string) []PortBinding {
	t.Helper()
	var bs []PortBinding
	if err := json.Unmarshal([]byte(js), &bs); err != nil {
		t.Fatalf("PortsJSON inválido: %v", err)
	}
	return bs
}

func TestResolve_MainReassignedWhenReserved(t *testing.T) {
	compose := `services:
  transmission:
    ports:
      - "9091:9091"
      - "51413:51413/udp"
`
	hard := reservedHard(80, 444) // incluye 9091
	soft := reservedSoft()
	out, mainHost, pj, err := resolveStackHostPorts(compose, 9091, nil, map[int]bool{}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mainHost != floatPoolMin {
		t.Errorf("principal debía reasignarse a %d, got %d", floatPoolMin, mainHost)
	}
	if !strings.Contains(out, "30000:9091") {
		t.Errorf("compose no reescrito al puerto nuevo\n%s", out)
	}
	if !strings.Contains(out, "51413:51413/udp") {
		t.Errorf("el secundario libre no debía cambiar\n%s", out)
	}
	bs := bindingsFromJSON(t, pj)
	if len(bs) != 2 {
		t.Errorf("PortsJSON debía tener 2 bindings, got %d", len(bs))
	}
}

func TestResolve_MainKeptWhenFree(t *testing.T) {
	compose := `services:
  app:
    ports:
      - "8082:80"
`
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	out, mainHost, pj, err := resolveStackHostPorts(compose, 8082, nil, map[int]bool{}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mainHost != 8082 {
		t.Errorf("puerto libre debía conservarse, got %d", mainHost)
	}
	if out != compose {
		t.Errorf("compose no debía reescribirse si no cambia el puerto")
	}
	if bs := bindingsFromJSON(t, pj); len(bs) != 1 || bs[0].Host != 8082 {
		t.Errorf("PortsJSON inesperado: %+v", bs)
	}
}

// Fase 6 · puerto SECUNDARIO reasignado (qbit peer 6881 vs NimTorrent),
// con tcp+udp compartiendo el mismo host nuevo.
func TestResolve_SecondaryReassigned(t *testing.T) {
	compose := `services:
  qbittorrent:
    ports:
      - "8081:8081"
      - "6881:6881"
      - "6881:6881/udp"
`
	hard := reservedHard(80, 444) // incluye 6881 (NimTorrent peer)
	soft := reservedSoft()
	out, mainHost, pj, err := resolveStackHostPorts(compose, 8081, nil, map[int]bool{}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mainHost != 8081 {
		t.Errorf("web 8081 libre debía conservarse, got %d", mainHost)
	}
	// peer 6881 reasignado; tcp y udp comparten el mismo host nuevo (30000)
	if strings.Count(out, "30000:6881") != 2 {
		t.Errorf("tcp y udp del peer deben ir al MISMO host nuevo\n%s", out)
	}
	if strings.Contains(out, `"6881:6881"`) {
		t.Errorf("aún queda el peer viejo sin reasignar\n%s", out)
	}
	bs := bindingsFromJSON(t, pj)
	if len(bs) != 3 {
		t.Fatalf("PortsJSON debía tener 3 bindings, got %d", len(bs))
	}
	peers := 0
	for _, b := range bs {
		if b.Declared == 6881 && b.Host == 30000 {
			peers++
		}
	}
	if peers != 2 {
		t.Errorf("ambos peer (tcp+udp) deben ir al 30000, got %d", peers)
	}
}

// Fase 6 · el DNS (:53) NO se reasigna aunque esté ocupado (fijo por protocolo).
func TestResolve_DNSNotReassigned(t *testing.T) {
	compose := `services:
  adguard:
    ports:
      - "3000:3000"
      - "53:53/tcp"
      - "53:53/udp"
`
	hard := reservedHard(80, 444)
	soft := reservedSoft() // incluye 53
	out, mainHost, pj, err := resolveStackHostPorts(compose, 3000, nil, map[int]bool{53: true}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mainHost != 3000 {
		t.Errorf("web 3000 libre debía conservarse, got %d", mainHost)
	}
	if !strings.Contains(out, "53:53") {
		t.Errorf("el DNS 53 no debía reasignarse\n%s", out)
	}
	if strings.Contains(out, "30000:53") {
		t.Errorf("el 53 no debía moverse al pool\n%s", out)
	}
	for _, b := range bindingsFromJSON(t, pj) {
		if b.Declared == 53 && b.Host != 53 {
			t.Errorf("el binding DNS debía mantener host 53, got %d", b.Host)
		}
	}
}

func TestResolve_VarMainUntouched(t *testing.T) {
	compose := `services:
  code:
    ports:
      - "${CODE_PORT}:8443"
`
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	out, mainHost, pj, err := resolveStackHostPorts(compose, 8443, nil, map[int]bool{}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != compose || mainHost != 8443 || pj != "" {
		t.Errorf("con host ${VAR} no debía tocar nada: out cambiado=%v mainHost=%d pj=%q",
			out != compose, mainHost, pj)
	}
}

func TestResolve_NoMainPort(t *testing.T) {
	compose := `services:
  app:
    ports:
      - "8082:80"
`
	out, mainHost, pj, err := resolveStackHostPorts(compose, 0, nil, map[int]bool{}, map[int]bool{}, map[int]bool{})
	if err != nil || out != compose || mainHost != 0 || pj != "" {
		t.Errorf("sin puerto principal declarado → no-op; got mainHost=%d pj=%q err=%v", mainHost, pj, err)
	}
}

func TestResolve_StickyKept(t *testing.T) {
	compose := `services:
  app:
    ports:
      - "8082:80"
`
	hard := reservedHard(80, 444)
	soft := reservedSoft()
	// instalación previa: la app tenía host 30124 para el contenedor 80.
	prev := []PortBinding{{Declared: 80, Host: 30124, Protocol: "tcp"}}
	out, mainHost, _, err := resolveStackHostPorts(compose, 8082, prev, map[int]bool{}, hard, soft)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mainHost != 30124 {
		t.Errorf("sticky 30124 debía conservarse, got %d", mainHost)
	}
	if !strings.Contains(out, "30124:80") {
		t.Errorf("compose debía reescribirse al sticky\n%s", out)
	}
}
