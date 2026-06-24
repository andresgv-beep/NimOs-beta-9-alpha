// port_preflight_test.go — Tests del preflight de conflictos de puerto fijo.

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDetectFixedConflicts_NoBindings(t *testing.T) {
	if got := detectFixedConflicts(nil, map[int]string{53: "adguard"}); got != nil {
		t.Fatalf("sin bindings esperaba nil, got %v", got)
	}
}

func TestDetectFixedConflicts_NoOccupancy(t *testing.T) {
	b := []PortBinding{{Declared: 53, Host: 53, Protocol: "tcp"}}
	if got := detectFixedConflicts(b, nil); got != nil {
		t.Fatalf("sin ocupación (nil) esperaba nil, got %v", got)
	}
	if got := detectFixedConflicts(b, map[int]string{}); got != nil {
		t.Fatalf("sin ocupación (vacío) esperaba nil, got %v", got)
	}
}

func TestDetectFixedConflicts_FreePort(t *testing.T) {
	// Web 8088 libre; la ocupación está en otro puerto → sin conflicto.
	b := []PortBinding{{Declared: 80, Host: 8088, Protocol: "tcp"}}
	if got := detectFixedConflicts(b, map[int]string{53: "adguard"}); got != nil {
		t.Fatalf("puerto libre no debe dar conflicto, got %v", got)
	}
}

func TestDetectFixedConflicts_DNSCollisionDedup(t *testing.T) {
	// Caso real: Pi-hole con web (libre) + 53 tcp/udp que AdGuard ya retiene.
	// Debe salir UN solo conflicto en el 53 (dedup tcp+udp).
	b := []PortBinding{
		{Declared: 80, Host: 8088, Protocol: "tcp"}, // web, libre
		{Declared: 53, Host: 53, Protocol: "tcp"},   // DNS tcp, fijo
		{Declared: 53, Host: 53, Protocol: "udp"},   // DNS udp, fijo
	}
	got := detectFixedConflicts(b, map[int]string{53: "adguard"})
	want := []PortConflict{{Port: 53, Protocol: "tcp", HeldBy: "adguard"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("esperaba un único conflicto en 53; got %v", got)
	}
}

func TestDetectFixedConflicts_MultipleDistinct(t *testing.T) {
	b := []PortBinding{
		{Declared: 53, Host: 53, Protocol: "tcp"},
		{Declared: 67, Host: 67, Protocol: "udp"},
	}
	occ := map[int]string{53: "adguard", 67: "kea"}
	got := detectFixedConflicts(b, occ)
	if len(got) != 2 {
		t.Fatalf("esperaba 2 conflictos distintos, got %d (%v)", len(got), got)
	}
}

func TestDetectFixedConflicts_IgnoreNonPositiveHost(t *testing.T) {
	b := []PortBinding{{Declared: 53, Host: 0, Protocol: "tcp"}}
	if got := detectFixedConflicts(b, map[int]string{53: "adguard"}); got != nil {
		t.Fatalf("host<=0 debe ignorarse, got %v", got)
	}
}

func TestOccupiedHostPortsBy(t *testing.T) {
	apps := []*DBDockerApp{
		{ID: "adguard", PortsJSON: `[{"declared":53,"host":53,"protocol":"tcp"},{"declared":53,"host":53,"protocol":"udp"},{"declared":80,"host":8083,"protocol":"tcp"}]`},
		nil, // debe ignorarse sin panic
		{ID: "transmission", PortsJSON: `[{"declared":9091,"host":30000,"protocol":"tcp"}]`},
	}
	got := occupiedHostPortsBy(apps)
	want := map[int]string{53: "adguard", 8083: "adguard", 30000: "transmission"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("occupiedHostPortsBy: want %v, got %v", want, got)
	}
}

func TestOccupiedHostPortsBy_FirstWinsOnDuplicate(t *testing.T) {
	// Estado inconsistente (dos apps con el mismo host) → gana la primera.
	apps := []*DBDockerApp{
		{ID: "first", PortsJSON: `[{"declared":53,"host":53,"protocol":"tcp"}]`},
		{ID: "second", PortsJSON: `[{"declared":53,"host":53,"protocol":"tcp"}]`},
	}
	got := occupiedHostPortsBy(apps)
	if got[53] != "first" {
		t.Fatalf("en duplicado debe ganar la primera; got %q", got[53])
	}
}

func TestPortConflictMessage(t *testing.T) {
	if portConflictMessage(nil) != "" {
		t.Fatal("sin conflictos debe devolver cadena vacía")
	}
	msg := portConflictMessage([]PortConflict{{Port: 53, HeldBy: "adguard"}})
	if !strings.Contains(msg, "53") || !strings.Contains(msg, "adguard") {
		t.Fatalf("el mensaje debe mencionar el puerto y la app; got %q", msg)
	}
}
