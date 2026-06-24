// network_exposure_firewall_test.go — Tests del sync de firewall.
//
// El commandRunner se mockea: no hay (ni debe haber) un ufw real en CI.
// Los tests cubren los guardarraíles de seguridad: no tocar reglas ajenas,
// jamás retirar el 22, degradación silenciosa sin ufw.

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockRunner simula ufw: guarda las llamadas y sirve salidas predefinidas.
type mockRunner struct {
	statusOut string
	statusErr error
	calls     []string
	failOn    string // si una llamada contiene esto, falla
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	m.calls = append(m.calls, call)
	if m.failOn != "" && strings.Contains(call, m.failOn) {
		return "boom", fmt.Errorf("exit status 1")
	}
	if len(args) > 0 && args[0] == "status" {
		return m.statusOut, m.statusErr
	}
	return "", nil
}

const ufwActiveHeader = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere
5000                       ALLOW       Anywhere
22/tcp (v6)                ALLOW       Anywhere (v6)
`

func TestUFW_AbsentIsNoOp(t *testing.T) {
	// ufw no instalado: Run("ufw status") falla → no se gestiona nada,
	// sin error (no es un fallo de NimOS que el host no tenga firewall).
	m := &mockRunner{statusErr: fmt.Errorf("exec: ufw: not found")}
	fw := NewUFWFirewall(m)
	managed, _, err := fw.EnsurePorts(context.Background(), []int{80, 443}, nil)
	if err != nil {
		t.Fatalf("absent ufw should not error: %v", err)
	}
	if len(managed) != 0 {
		t.Errorf("managed = %v, want empty without ufw", managed)
	}
	if len(m.calls) != 1 {
		t.Errorf("calls = %v, want only status", m.calls)
	}
}

func TestUFW_InactiveIsNoOp(t *testing.T) {
	m := &mockRunner{statusOut: "Status: inactive\n"}
	fw := NewUFWFirewall(m)
	managed, _, err := fw.EnsurePorts(context.Background(), []int{80, 443}, nil)
	if err != nil {
		t.Fatalf("inactive ufw should not error: %v", err)
	}
	if len(managed) != 0 {
		t.Errorf("managed = %v, want empty with inactive ufw", managed)
	}
}

func TestUFW_AllowsMissingPorts(t *testing.T) {
	m := &mockRunner{statusOut: ufwActiveHeader}
	fw := NewUFWFirewall(m)
	managed, changed, err := fw.EnsurePorts(context.Background(), []int{80, 444}, nil)
	if err != nil {
		t.Fatalf("EnsurePorts: %v", err)
	}
	joined := strings.Join(m.calls, " | ")
	if !strings.Contains(joined, "ufw allow 80/tcp") || !strings.Contains(joined, "ufw allow 444/tcp") {
		t.Errorf("missing allow calls: %v", m.calls)
	}
	if !changed || len(managed) != 2 {
		t.Errorf("managed = %v changed = %v, want [80 444] true", managed, changed)
	}
}

func TestUFW_SkipsExistingRules(t *testing.T) {
	// El 444 ya está permitido (regla previa, quizás del usuario): no se
	// re-ejecuta allow — idempotencia sin ruido.
	m := &mockRunner{statusOut: ufwActiveHeader + "444/tcp                    ALLOW       Anywhere\n"}
	fw := NewUFWFirewall(m)
	_, _, err := fw.EnsurePorts(context.Background(), []int{444}, []int{444})
	if err != nil {
		t.Fatalf("EnsurePorts: %v", err)
	}
	for _, c := range m.calls {
		if strings.Contains(c, "allow 444") && !strings.Contains(c, "status") {
			t.Errorf("should not re-allow existing rule: %v", m.calls)
		}
	}
}

func TestUFW_RemovesStaleManagedPort(t *testing.T) {
	// NimOS gestionaba el 443; el usuario cambió a 444 → se retira el 443
	// (es nuestro) y se añade el 444.
	m := &mockRunner{statusOut: ufwActiveHeader + "443/tcp                    ALLOW       Anywhere\n"}
	fw := NewUFWFirewall(m)
	managed, changed, err := fw.EnsurePorts(context.Background(), []int{80, 444}, []int{80, 443})
	if err != nil {
		t.Fatalf("EnsurePorts: %v", err)
	}
	joined := strings.Join(m.calls, " | ")
	if !strings.Contains(joined, "ufw delete allow 443/tcp") {
		t.Errorf("stale managed 443 should be deleted: %v", m.calls)
	}
	if !changed || len(managed) != 2 || managed[0] != 80 || managed[1] != 444 {
		t.Errorf("managed = %v, want [80 444]", managed)
	}
}

func TestUFW_NeverTouchesForeignRules(t *testing.T) {
	// El 5000 (panel) y el 22 están permitidos pero NO en prevManaged:
	// son reglas ajenas → NimOS no las toca aunque no estén en want.
	m := &mockRunner{statusOut: ufwActiveHeader}
	fw := NewUFWFirewall(m)
	_, _, err := fw.EnsurePorts(context.Background(), []int{80, 444}, nil)
	if err != nil {
		t.Fatalf("EnsurePorts: %v", err)
	}
	for _, c := range m.calls {
		if strings.Contains(c, "delete") {
			t.Errorf("no foreign rule should ever be deleted: %v", m.calls)
		}
	}
}

func TestUFW_NeverDeletesSSH(t *testing.T) {
	// GUARDARRAÍL: aunque el 22 acabara en prevManaged por cualquier bug o
	// estado corrupto, JAMÁS se retira (cortar SSH sería catastrófico).
	m := &mockRunner{statusOut: ufwActiveHeader}
	fw := NewUFWFirewall(m)
	_, _, err := fw.EnsurePorts(context.Background(), []int{80, 444}, []int{22, 80})
	if err != nil {
		t.Fatalf("EnsurePorts: %v", err)
	}
	for _, c := range m.calls {
		if strings.Contains(c, "delete") && strings.Contains(c, "22") {
			t.Errorf("SSH (22) must NEVER be deleted: %v", m.calls)
		}
	}
}

func TestUFW_AllowFailureReturnsError(t *testing.T) {
	m := &mockRunner{statusOut: ufwActiveHeader, failOn: "allow 444"}
	fw := NewUFWFirewall(m)
	_, _, err := fw.EnsurePorts(context.Background(), []int{444}, nil)
	if err == nil {
		t.Error("allow failure should surface as error (reconciler emits event)")
	}
}

func TestParseUFWAllowedPorts(t *testing.T) {
	out := ufwActiveHeader + `444/tcp                    ALLOW       Anywhere
8080                       ALLOW       192.168.1.0/24
9999/udp                   ALLOW       Anywhere
53                         DENY        Anywhere
`
	ports := parseUFWAllowedPorts(out)
	for _, want := range []int{22, 5000, 444, 8080} {
		if !ports[want] {
			t.Errorf("port %d should be parsed as allowed", want)
		}
	}
	if ports[9999] {
		t.Error("udp-only rules must not count as tcp allow")
	}
	if ports[53] {
		t.Error("DENY rules must not count as allow")
	}
}
