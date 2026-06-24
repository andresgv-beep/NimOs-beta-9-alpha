// network_exposure_observer_test.go — Tests del observer de certs.
//
// El observer ahora SONDEA el TLS real (no parsea un endpoint). Mockeamos
// dos cosas: el pinger (¿Caddy vivo?) y el prober (¿qué cert sirve?).

package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// mockPinger simula el chequeo de vida del admin de Caddy.
type mockPinger struct{ pingErr error }

func (m *mockPinger) Ping(ctx context.Context) error { return m.pingErr }

// mockProber simula el sondeo TLS: devuelve un cert por dominio, o error.
type mockProber struct {
	byDomain map[string]ExposureCertStatus
	errAll   error // si != nil, todo dominio falla (sin cert servido)
	calls    []string
}

func (m *mockProber) Probe(ctx context.Context, domain string, port int) (ExposureCertStatus, error) {
	m.calls = append(m.calls, fmt.Sprintf("%s:%d", domain, port))
	if m.errAll != nil {
		return ExposureCertStatus{}, m.errAll
	}
	if st, ok := m.byDomain[domain]; ok {
		return st, nil
	}
	return ExposureCertStatus{}, fmt.Errorf("no cert for %s", domain)
}

func newTestExposureObserver(t *testing.T, pinger caddyPinger, prober certProber) (*NetworkExposureObserver, *NetworkRepo, *sqlConn, func()) {
	t.Helper()
	repo, clock, c, cleanup := newTestRepo(t)
	obs := NewNetworkExposureObserver(repo, clock, DefaultNetworkExposureObserverConfig())
	obs.pingerFor = func(adminURL string) caddyPinger { return pinger }
	obs.prober = prober
	return obs, repo, c, cleanup
}

// seedExposed configura dominio base + una app de subdominio habilitada.
func seedExposed(t *testing.T, repo *NetworkRepo, c *sqlConn, baseDomain, sub string) {
	t.Helper()
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.SaveExposureConfig(context.Background(), tx, NetworkExposureConfig{
			BaseDomain: baseDomain, Enabled: true, HTTPPort: 80, HTTPSPort: 443,
		})
	})
	app := makeExposedApp(sub, sub)
	withNetTx(t, c.db, func(tx *sql.Tx) error {
		return repo.CreateExposedApp(context.Background(), tx, app)
	})
}

func TestExposureObserver_ProbesServedCert(t *testing.T) {
	domain := "immich.x.org"
	prober := &mockProber{byDomain: map[string]ExposureCertStatus{
		// base (x.org) sin cert; la app (immich.x.org) con cert.
		domain: {Subject: domain, Issuer: "Let's Encrypt", Managed: true, DaysLeft: 67},
	}}
	obs, repo, c, cleanup := newTestExposureObserver(t, &mockPinger{}, prober)
	defer cleanup()
	seedExposed(t, repo, c, "x.org", "immich")

	if err := obs.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	snap := obs.Snapshot()
	if snap == nil || !snap.Reachable {
		t.Fatal("snapshot should be reachable")
	}
	// Encontró el cert de la app (el base x.org no tiene → no aparece).
	found := false
	for _, cert := range snap.Certs {
		if cert.Subject == domain {
			found = true
			if cert.Issuer != "Let's Encrypt" || cert.DaysLeft != 67 {
				t.Errorf("cert fields mismatch: %+v", cert)
			}
		}
	}
	if !found {
		t.Errorf("served cert for %s not in snapshot: %+v", domain, snap.Certs)
	}
	// Sondeó al puerto HTTPS configurado (443).
	if len(prober.calls) == 0 {
		t.Fatal("prober was never called")
	}
}

func TestExposureObserver_ProbeFailureMeansNoCert(t *testing.T) {
	// Caddy vivo (ping OK) pero ningún dominio sirve cert aún → Reachable
	// TRUE con certs vacíos. La app mostrará "emitiendo certificado…" — y
	// esta vez es la verdad, no el bug del endpoint muerto.
	prober := &mockProber{errAll: fmt.Errorf("connection refused")}
	obs, repo, c, cleanup := newTestExposureObserver(t, &mockPinger{}, prober)
	defer cleanup()
	seedExposed(t, repo, c, "x.org", "immich")

	if err := obs.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	snap := obs.Snapshot()
	if !snap.Reachable {
		t.Error("Reachable should be TRUE (Caddy alive, only the cert isn't served yet)")
	}
	if len(snap.Certs) != 0 {
		t.Errorf("certs should be empty when nothing is served: %+v", snap.Certs)
	}
}

func TestExposureObserver_CaddyUnreachable(t *testing.T) {
	// Ping falla → Caddy caído → Reachable false, y el prober NI se llama.
	prober := &mockProber{}
	obs, repo, c, cleanup := newTestExposureObserver(t, &mockPinger{pingErr: fmt.Errorf("refused")}, prober)
	defer cleanup()
	seedExposed(t, repo, c, "x.org", "immich")

	if err := obs.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	snap := obs.Snapshot()
	if snap == nil || snap.Reachable {
		t.Error("Reachable should be false when Caddy is down")
	}
	if len(snap.Certs) != 0 {
		t.Error("no certs when unreachable")
	}
	if len(prober.calls) != 0 {
		t.Error("prober must NOT run when Caddy is unreachable")
	}
}

func TestExposureObserver_ProbesBaseDomainToo(t *testing.T) {
	// El dominio base (panel) también se sondea: collectTLSDomains lo mete
	// primero. Si sirve cert, aparece.
	prober := &mockProber{byDomain: map[string]ExposureCertStatus{
		"x.org": {Subject: "x.org", Issuer: "Let's Encrypt", Managed: true, DaysLeft: 80},
	}}
	obs, repo, c, cleanup := newTestExposureObserver(t, &mockPinger{}, prober)
	defer cleanup()
	seedExposed(t, repo, c, "x.org", "immich")

	obs.Reconcile(context.Background())
	snap := obs.Snapshot()
	found := false
	for _, cert := range snap.Certs {
		if cert.Subject == "x.org" {
			found = true
		}
	}
	if !found {
		t.Errorf("panel (base domain) cert should be probed and listed: %+v", snap.Certs)
	}
}
