package main

import "testing"

func TestReservedHard_StaticAndCaddy(t *testing.T) {
	h := reservedHard(80, 444)
	for _, p := range []int{portDaemonHTTP, portNimTorrent, portNimTorrentPeer, portCaddyAdmin, 80, 444} {
		if !h[p] {
			t.Errorf("reservedHard: falta el puerto %d", p)
		}
	}
	if len(h) != 6 {
		t.Errorf("reservedHard: esperaba 6 puertos, got %d (%v)", len(h), h)
	}
}

func TestReservedHard_IgnoresNonPositiveCaddy(t *testing.T) {
	h := reservedHard(0, -1)
	// Solo los 4 estáticos; los caddy <=0 no entran.
	if len(h) != 4 {
		t.Fatalf("esperaba 4 estáticos, got %d (%v)", len(h), h)
	}
	if h[0] || h[-1] {
		t.Errorf("no debería contener puertos <= 0")
	}
}

func TestReservedHard_CaddyOn443And80Default(t *testing.T) {
	h := reservedHard(80, 443)
	if !h[80] || !h[443] {
		t.Errorf("defaults de Caddy 80/443 deberían estar reservados")
	}
}

func TestReservedSoft_Contains(t *testing.T) {
	s := reservedSoft()
	for _, p := range []int{22, 53, 67, 68, 123} {
		if !s[p] {
			t.Errorf("reservedSoft: falta %d", p)
		}
	}
	if len(s) != 5 {
		t.Errorf("reservedSoft: esperaba 5, got %d", len(s))
	}
}

func TestOccupiedHostPorts_MultiPortAndUDP(t *testing.T) {
	apps := []*DBDockerApp{
		// transmission flotado: web + peer tcp + peer udp (mismo host peer)
		{ID: "transmission", PortsJSON: `[{"Declared":9091,"Host":30001,"Protocol":"tcp"},{"Declared":51413,"Host":30002,"Protocol":"tcp"},{"Declared":51413,"Host":30002,"Protocol":"udp"}]`},
		// legacy: sin PortsJSON, cae a Port
		{ID: "jellyfin", Port: 8096},
		nil, // se ignora
	}
	occ := occupiedHostPorts(apps)
	for _, p := range []int{30001, 30002, 8096} {
		if !occ[p] {
			t.Errorf("occupiedHostPorts: falta %d (%v)", p, occ)
		}
	}
	// 30002 aparece en tcp y udp pero es un único host port
	if len(occ) != 3 {
		t.Errorf("esperaba 3 puertos host únicos, got %d (%v)", len(occ), occ)
	}
}

func TestOccupiedHostPorts_Empty(t *testing.T) {
	if occ := occupiedHostPorts(nil); len(occ) != 0 {
		t.Errorf("apps nil → ocupación vacía, got %v", occ)
	}
	// app sin puertos válidos
	occ := occupiedHostPorts([]*DBDockerApp{{ID: "x"}})
	if len(occ) != 0 {
		t.Errorf("app sin Port ni PortsJSON → vacío, got %v", occ)
	}
}

func TestIsPortFree(t *testing.T) {
	occupied := map[int]bool{8096: true}
	hard := reservedHard(80, 444) // incluye 5000, 9091, 2019, 80, 444

	cases := []struct {
		name string
		port int
		want bool
	}{
		{"libre normal", 30050, true},
		{"ocupado por app", 8096, false},
		{"reservado duro daemon", 5000, false},
		{"reservado duro torrentd", 9091, false},
		{"reservado duro caddy admin", 2019, false},
		{"reservado duro caddy https", 444, false},
		{"fuera de rango bajo", 0, false},
		{"fuera de rango alto", 70000, false},
		{"negativo", -5, false},
		// el blando 53 NO se bloquea en isPortFree (política blanda es del allocator)
		{"blando 53 no bloqueado aquí", 53, true},
	}
	for _, c := range cases {
		if got := isPortFree(c.port, occupied, hard); got != c.want {
			t.Errorf("%s: isPortFree(%d)=%v, want %v", c.name, c.port, got, c.want)
		}
	}
}

func TestInFloatPool(t *testing.T) {
	cases := []struct {
		port int
		want bool
	}{
		{29999, false},
		{30000, true},
		{45000, true},
		{59999, true},
		{60000, false},
	}
	for _, c := range cases {
		if got := inFloatPool(c.port); got != c.want {
			t.Errorf("inFloatPool(%d)=%v, want %v", c.port, got, c.want)
		}
	}
}
