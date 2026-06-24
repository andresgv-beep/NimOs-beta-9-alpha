// network_router_upnp_test.go — Tests del UPnPRouterProvider.
//
// Estrategia: el provider tiene runCmd inyectable, así que los tests
// no invocan upnpc real. Sustituimos por una función mock que devuelve
// outputs canned y registra los args para verificación.
//
// Cubrimos:
//   - Construcción y defaults.
//   - Detect: binario no instalado, router no responde, router OK.
//   - ListMappings: output vacío, varios mappings, output malformado.
//   - AddMapping: éxito, conflict detection, validación de args.
//   - RemoveMapping: éxito, idempotencia con NoSuchEntryInArray.
//   - parseUPnPList como función pura.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock cmdRunner
// ─────────────────────────────────────────────────────────────────────────────

type capturedCmd struct {
	name string
	args []string
}

type mockCmd struct {
	mu       sync.Mutex
	captured []capturedCmd
	output   []byte
	err      error
}

func (m *mockCmd) runner() cmdRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.captured = append(m.captured, capturedCmd{name: name, args: args})
		return m.output, m.err
	}
}

func (m *mockCmd) calls() []capturedCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]capturedCmd, len(m.captured))
	copy(out, m.captured)
	return out
}

// buildProvider helper con un mock cmdRunner.
func buildUPnPProvider(t *testing.T, mock *mockCmd) *UPnPRouterProvider {
	t.Helper()
	br := NewCircuitBreaker(DefaultBreakerConfig("upnp-test"))
	// Usamos "echo" como binario que sí existe (LookPath pasará en sistemas
	// con /bin/echo). El mock cmdRunner sustituye la ejecución real, por
	// tanto el nombre del binario solo importa para LookPath.
	p, err := NewUPnPRouterProvider(UPnPRouterProviderConfig{
		Breaker:  br,
		UpnpcBin: "echo",
		RunCmd:   mock.runner(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestUPnP_RequiresBreaker(t *testing.T) {
	_, err := NewUPnPRouterProvider(UPnPRouterProviderConfig{})
	if err == nil {
		t.Error("expected error without Breaker")
	}
}

func TestUPnP_DefaultsApplied(t *testing.T) {
	p, err := NewUPnPRouterProvider(UPnPRouterProviderConfig{
		Breaker: NewCircuitBreaker(DefaultBreakerConfig("x")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.upnpcBin != "upnpc" {
		t.Errorf("upnpcBin default = %q, want upnpc", p.upnpcBin)
	}
	if p.cmdTimeout == 0 {
		t.Error("cmdTimeout default not applied")
	}
	if p.runCmd == nil {
		t.Error("runCmd default not applied")
	}
}

func TestUPnP_NameIsStable(t *testing.T) {
	p := buildUPnPProvider(t, &mockCmd{})
	if p.Name() != "upnp" {
		t.Errorf("Name = %q, want upnp", p.Name())
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Detect
// ═════════════════════════════════════════════════════════════════════════════

func TestUPnP_DetectBinaryMissing(t *testing.T) {
	// Forzamos un nombre de binario que no existe.
	br := NewCircuitBreaker(DefaultBreakerConfig("x"))
	p, _ := NewUPnPRouterProvider(UPnPRouterProviderConfig{
		Breaker:  br,
		UpnpcBin: "/nonexistent/binary-that-cannot-exist-12345",
	})
	status, err := p.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Available {
		t.Error("Available should be false when binary missing")
	}
}

func TestUPnP_DetectNoRouter(t *testing.T) {
	mock := &mockCmd{
		err: errors.New("upnpc: No IGD UPnP Device found on the network"),
	}
	p := buildUPnPProvider(t, mock)

	status, err := p.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available {
		t.Error("Available should be true (binary exists)")
	}
	if status.Detected {
		t.Error("Detected should be false (router not responding)")
	}
}

func TestUPnP_DetectParsesIPs(t *testing.T) {
	mock := &mockCmd{
		output: []byte(`upnpc : miniupnpc library test client, version 2.2.1.
Local LAN ip address : 192.168.1.50
ExternalIPAddress = 80.123.45.67
 desc: http://192.168.1.1:1900/desc.xml
`),
	}
	p := buildUPnPProvider(t, mock)

	status, err := p.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Detected {
		t.Error("Detected should be true")
	}
	if status.LocalIP != "192.168.1.50" {
		t.Errorf("LocalIP = %q", status.LocalIP)
	}
	if status.ExternalIP != "80.123.45.67" {
		t.Errorf("ExternalIP = %q", status.ExternalIP)
	}

	// El raw debe incluirse para debug.
	if status.Raw == "" {
		t.Error("Raw should be populated")
	}
}

func TestUPnP_DetectSendsDashS(t *testing.T) {
	mock := &mockCmd{output: []byte("ok")}
	p := buildUPnPProvider(t, mock)
	p.Detect(context.Background())

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if len(calls[0].args) == 0 || calls[0].args[0] != "-s" {
		t.Errorf("expected -s arg, got %v", calls[0].args)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// parseUPnPList
// ═════════════════════════════════════════════════════════════════════════════

func TestParseUPnPList_TypicalOutput(t *testing.T) {
	// NOTA: descripciones SIN espacios. strings.Fields() rompe descripciones
	// con espacios — limitación conocida del parser legacy. NimOS escribe
	// descripciones de un solo token (e.g. "NimOS-HTTPS") para evitarlo.
	out := ` 0 TCP  8080->192.168.1.50:8080  'NimOS-HTTPS' '' 0
 1 UDP  5000->192.168.1.50:5000  'Tracker' '' 0
 2 TCP  443->192.168.1.51:443  'OtherDevice' '' 0
`
	mappings := parseUPnPList(out)
	if len(mappings) != 3 {
		t.Fatalf("expected 3 mappings, got %d: %+v", len(mappings), mappings)
	}

	m := mappings[0]
	if m.Protocol != "TCP" || m.ExternalPort != 8080 ||
		m.InternalIP != "192.168.1.50" || m.InternalPort != 8080 {
		t.Errorf("first mapping wrong: %+v", m)
	}
	if m.Description != "NimOS-HTTPS" {
		t.Errorf("description = %q, want 'NimOS-HTTPS'", m.Description)
	}

	if mappings[1].Protocol != "UDP" {
		t.Errorf("second protocol = %q", mappings[1].Protocol)
	}
}

func TestParseUPnPList_EmptyOutput(t *testing.T) {
	mappings := parseUPnPList("")
	if len(mappings) != 0 {
		t.Errorf("expected 0, got %d", len(mappings))
	}
}

func TestParseUPnPList_SkipsHeaders(t *testing.T) {
	out := `upnpc : miniupnpc library test client
ExternalIPAddress = 1.2.3.4
List of redirections:
 0 TCP  80->192.168.1.10:80  '' '' 0
`
	mappings := parseUPnPList(out)
	if len(mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d: %+v", len(mappings), mappings)
	}
}

func TestParseUPnPList_MalformedLinesSkipped(t *testing.T) {
	out := ` 0 TCP  notaport->192.168.1.50:8080  '' '' 0
 1 XYZ  80->192.168.1.50:80  '' '' 0
 2 TCP  8443->192.168.1.50:8443  'OK' '' 0
`
	mappings := parseUPnPList(out)
	if len(mappings) != 1 {
		t.Errorf("expected 1 valid mapping, got %d: %+v", len(mappings), mappings)
	}
	if mappings[0].ExternalPort != 8443 {
		t.Errorf("got %+v", mappings[0])
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// ListMappings
// ═════════════════════════════════════════════════════════════════════════════

func TestUPnP_ListMappings(t *testing.T) {
	mock := &mockCmd{output: []byte(` 0 TCP  8080->192.168.1.50:8080  'NimOS' '' 0
`)}
	p := buildUPnPProvider(t, mock)

	mappings, err := p.ListMappings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d", len(mappings))
	}

	calls := mock.calls()
	if len(calls) == 0 || calls[0].args[0] != "-l" {
		t.Errorf("expected -l arg, got %v", calls)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// AddMapping
// ═════════════════════════════════════════════════════════════════════════════

func TestUPnP_AddMappingSuccess(t *testing.T) {
	mock := &mockCmd{output: []byte("ok")}
	p := buildUPnPProvider(t, mock)

	err := p.AddMapping(context.Background(), RouterPortMapping{
		Protocol:     "TCP",
		ExternalPort: 8080,
		InternalIP:   "192.168.1.50",
		InternalPort: 8080,
		Description:  "NimOS",
	})
	if err != nil {
		t.Fatal(err)
	}

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	got := calls[0].args
	// Esperamos: -a 192.168.1.50 8080 8080 TCP 0 NimOS
	expected := []string{"-a", "192.168.1.50", "8080", "8080", "TCP", "0", "NimOS"}
	if !equalStringSlices(got, expected) {
		t.Errorf("args = %v, want %v", got, expected)
	}
}

func TestUPnP_AddMappingDetectsConflict(t *testing.T) {
	mock := &mockCmd{
		err:    errors.New("exit status 1"),
		output: []byte("AddPortMapping failed : 718 ConflictInMappingEntry"),
	}
	p := buildUPnPProvider(t, mock)

	err := p.AddMapping(context.Background(), RouterPortMapping{
		Protocol:     "TCP",
		ExternalPort: 80,
		InternalIP:   "192.168.1.50",
		InternalPort: 80,
	})
	if !errors.Is(err, ErrRouterConflict) {
		t.Errorf("err = %v, want ErrRouterConflict", err)
	}
}

func TestUPnP_AddMappingValidation(t *testing.T) {
	mock := &mockCmd{output: []byte("ok")}
	p := buildUPnPProvider(t, mock)

	cases := []struct {
		name string
		m    RouterPortMapping
	}{
		{"bad protocol", RouterPortMapping{Protocol: "WTF", ExternalPort: 80, InternalIP: "1.1.1.1", InternalPort: 80}},
		{"bad external port high", RouterPortMapping{Protocol: "TCP", ExternalPort: 70000, InternalIP: "1.1.1.1", InternalPort: 80}},
		{"bad external port zero", RouterPortMapping{Protocol: "TCP", ExternalPort: 0, InternalIP: "1.1.1.1", InternalPort: 80}},
		{"empty IP", RouterPortMapping{Protocol: "TCP", ExternalPort: 80, InternalIP: "", InternalPort: 80}},
		{"IP with shell metachar", RouterPortMapping{Protocol: "TCP", ExternalPort: 80, InternalIP: "192.168.1.1; rm -rf /", InternalPort: 80}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := p.AddMapping(context.Background(), c.m)
			if err == nil {
				t.Errorf("expected validation error for %+v", c.m)
			}
		})
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// RemoveMapping
// ═════════════════════════════════════════════════════════════════════════════

func TestUPnP_RemoveMappingSuccess(t *testing.T) {
	mock := &mockCmd{output: []byte("ok")}
	p := buildUPnPProvider(t, mock)

	if err := p.RemoveMapping(context.Background(), "tcp", 8080); err != nil {
		t.Fatal(err)
	}

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call")
	}
	expected := []string{"-d", "8080", "TCP"}
	if !equalStringSlices(calls[0].args, expected) {
		t.Errorf("args = %v, want %v", calls[0].args, expected)
	}
}

func TestUPnP_RemoveMappingIdempotent(t *testing.T) {
	mock := &mockCmd{
		err:    errors.New("exit status 1"),
		output: []byte("DeletePortMapping failed : 714 NoSuchEntryInArray"),
	}
	p := buildUPnPProvider(t, mock)

	err := p.RemoveMapping(context.Background(), "TCP", 8080)
	if err != nil {
		t.Errorf("expected no-error for NoSuchEntryInArray, got %v", err)
	}
}

func TestUPnP_RemoveMappingValidation(t *testing.T) {
	mock := &mockCmd{output: []byte("ok")}
	p := buildUPnPProvider(t, mock)

	cases := []struct {
		proto string
		port  int
	}{
		{"BAD", 80},
		{"TCP", 0},
		{"TCP", 70000},
	}
	for _, c := range cases {
		if err := p.RemoveMapping(context.Background(), c.proto, c.port); err == nil {
			t.Errorf("expected error for proto=%s port=%d", c.proto, c.port)
		}
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// truncateRaw
// ═════════════════════════════════════════════════════════════════════════════

func TestTruncateRaw_Cases(t *testing.T) {
	if got := truncateRaw("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 200)
	got := truncateRaw(long, 100)
	if len(got) < 100 || len(got) > 120 {
		t.Errorf("len(truncated) = %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("missing truncation marker: %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// guard contra import-unused
var _ = fmt.Sprintf
