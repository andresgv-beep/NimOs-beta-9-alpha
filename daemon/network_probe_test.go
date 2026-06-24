// network_probe_test.go — Tests del NetworkProbe.
//
// Cubre:
//   - Ports: sin listeners inyectados → Listening=false.
//   - Ports: con listener fn que devuelve ok=true → todo OK.
//   - Ports: con listener fn que devuelve ok=false → Listening=false.
//   - Ports: ID desconocido → Listening=false.
//   - ProbedAt usa el clock inyectado.
//   - Inputs vacíos → cero ports.
//
// El probe ya NO observa certs: TLS está delegado a Caddy y el control
// plane observa el estado de los certs vía network_exposure, no aquí.

package main

import (
	"testing"
	"time"
)

// ═════════════════════════════════════════════════════════════════════════════
// Ports
// ═════════════════════════════════════════════════════════════════════════════

func TestProbe_PortsNoListenersConfigured(t *testing.T) {
	p := NewRealNetworkProbe(NewFakeClock(time.Now()))
	res := p.Probe([]PortProbeInput{{ID: "http"}, {ID: "https"}})

	if len(res.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(res.Ports))
	}
	for _, port := range res.Ports {
		if port.Listening {
			t.Errorf("port %s: Listening=true, want false (no fn injected)", port.ID)
		}
	}
}

func TestProbe_PortsWithActiveListeners(t *testing.T) {
	p := NewRealNetworkProbe(NewFakeClock(time.Now()))
	p.HTTPListener = func() (int, string, bool) { return 8080, "0.0.0.0", true }
	p.HTTPSListener = func() (int, string, bool) { return 8443, "127.0.0.1", true }

	res := p.Probe([]PortProbeInput{{ID: "http"}, {ID: "https"}})

	if len(res.Ports) != 2 {
		t.Fatalf("got %d ports", len(res.Ports))
	}
	for _, port := range res.Ports {
		if !port.Listening {
			t.Errorf("port %s: Listening=false", port.ID)
		}
		switch port.ID {
		case "http":
			if port.Port != 8080 || port.BindAddress != "0.0.0.0" {
				t.Errorf("http: got port=%d bind=%s, want 8080/0.0.0.0", port.Port, port.BindAddress)
			}
		case "https":
			if port.Port != 8443 || port.BindAddress != "127.0.0.1" {
				t.Errorf("https: got port=%d bind=%s, want 8443/127.0.0.1", port.Port, port.BindAddress)
			}
		}
	}
}

func TestProbe_PortsListenerReturnsNotOK(t *testing.T) {
	p := NewRealNetworkProbe(NewFakeClock(time.Now()))
	p.HTTPListener = func() (int, string, bool) { return 0, "", false }

	res := p.Probe([]PortProbeInput{{ID: "http"}})

	if res.Ports[0].Listening {
		t.Error("ok=false should yield Listening=false")
	}
}

func TestProbe_PortsUnknownID(t *testing.T) {
	p := NewRealNetworkProbe(NewFakeClock(time.Now()))
	res := p.Probe([]PortProbeInput{{ID: "ftp"}})

	if res.Ports[0].Listening {
		t.Error("unknown ID should yield Listening=false")
	}
}

func TestProbe_ProbedAtUsesClock(t *testing.T) {
	frozen := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(frozen)
	p := NewRealNetworkProbe(clock)
	res := p.Probe(nil)
	if !res.ProbedAt.Equal(frozen) {
		t.Errorf("ProbedAt = %v, want %v", res.ProbedAt, frozen)
	}
}

func TestProbe_EmptyInputs(t *testing.T) {
	p := NewRealNetworkProbe(NewFakeClock(time.Now()))
	res := p.Probe(nil)
	if len(res.Ports) != 0 {
		t.Errorf("empty inputs: got %d ports, want 0", len(res.Ports))
	}
}
