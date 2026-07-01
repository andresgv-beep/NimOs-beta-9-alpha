// network_ddns_duckdns_test.go — Tests del DuckDNSProvider.
//
// Estrategia:
//   - httptest.Server emula DuckDNS. Captura query params para verificar
//     que mandamos `domains=`, `token=`, `ip=` correctos.
//   - Breaker real (no mock) — F-001 ya lo prueba; aquí verificamos que
//     el provider lo integra correctamente.
//   - No usar dominios reales: todos los tests usan "test" o variaciones.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// duckDNSServer construye un httptest.Server que responde con `response`
// y captura las queries recibidas para verificación. handler opcional
// permite respuestas dinámicas.
func duckDNSServer(t *testing.T, response string, statusCode int) (*httptest.Server, *struct {
	mu      sync.Mutex
	domains string
	token   string
	ip      string
	verbose string
	count   int
}) {
	t.Helper()
	captured := &struct {
		mu      sync.Mutex
		domains string
		token   string
		ip      string
		verbose string
		count   int
	}{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.mu.Lock()
		captured.domains = r.URL.Query().Get("domains")
		captured.token = r.URL.Query().Get("token")
		captured.ip = r.URL.Query().Get("ip")
		captured.verbose = r.URL.Query().Get("verbose")
		captured.count++
		captured.mu.Unlock()

		w.WriteHeader(statusCode)
		w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

// buildProvider crea un provider con breaker fresco y endpoint apuntando
// al server de test.
func buildProvider(t *testing.T, endpoint string) *DuckDNSProvider {
	t.Helper()
	breaker := NewCircuitBreaker(DefaultBreakerConfig("duckdns-test"))
	p, err := NewDuckDNSProvider(DuckDNSProviderConfig{
		Breaker:  breaker,
		Endpoint: endpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ═════════════════════════════════════════════════════════════════════════════
// Construction
// ═════════════════════════════════════════════════════════════════════════════

func TestDuckDNS_NewRequiresBreaker(t *testing.T) {
	_, err := NewDuckDNSProvider(DuckDNSProviderConfig{})
	if err == nil {
		t.Error("expected error when Breaker is nil")
	}
}

func TestDuckDNS_NewDefaultsHTTPClient(t *testing.T) {
	b := NewCircuitBreaker(DefaultBreakerConfig("x"))
	p, err := NewDuckDNSProvider(DuckDNSProviderConfig{Breaker: b})
	if err != nil {
		t.Fatal(err)
	}
	if p.httpClient == nil {
		t.Error("default http client not assigned")
	}
	if p.httpClient.Timeout == 0 {
		t.Error("default client should have a timeout")
	}
}

func TestDuckDNS_NewDefaultsEndpoint(t *testing.T) {
	b := NewCircuitBreaker(DefaultBreakerConfig("x"))
	p, _ := NewDuckDNSProvider(DuckDNSProviderConfig{Breaker: b})
	if p.endpoint != defaultDuckDNSEndpoint {
		t.Errorf("endpoint = %q, want %q", p.endpoint, defaultDuckDNSEndpoint)
	}
}

func TestDuckDNS_NameIsStable(t *testing.T) {
	b := NewCircuitBreaker(DefaultBreakerConfig("x"))
	p, _ := NewDuckDNSProvider(DuckDNSProviderConfig{Breaker: b})
	if p.Name() != "duckdns" {
		t.Errorf("Name = %q, want duckdns", p.Name())
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// duckdnsSubdomain — validación
// ═════════════════════════════════════════════════════════════════════════════

func TestDuckDNS_SubdomainExtraction(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"test", "test", false},
		{"test.duckdns.org", "test", false},
		{"TEST.DuckDNS.org", "test", false}, // case insensitive
		{"my-host", "my-host", false},
		{"my-host.duckdns.org", "my-host", false},
		{"", "", true},
		{".duckdns.org", "", true},
		{"-bad", "", true}, // leading hyphen
		{"bad-", "", true}, // trailing hyphen
		{"has space", "", true},
		{"has.dot", "", true}, // dots inside subdomain (we already stripped suffix)
		{"has/slash", "", true},
		{"with$symbol", "", true},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got, err := duckdnsSubdomain(c.input)
			if (err != nil) != c.wantErr {
				t.Errorf("err=%v wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Update — escenarios principales
// ═════════════════════════════════════════════════════════════════════════════

func TestDuckDNS_UpdateSuccess(t *testing.T) {
	srv, captured := duckDNSServer(t, "OK", http.StatusOK)
	p := buildProvider(t, srv.URL)

	res, err := p.Update(context.Background(), "test.duckdns.org", "secret-token-x")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res == nil || res.RawResponse != "OK" {
		t.Errorf("result = %+v", res)
	}

	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.domains != "test" {
		t.Errorf("domains query = %q, want test", captured.domains)
	}
	if captured.token != "secret-token-x" {
		t.Errorf("token query = %q", captured.token)
	}
	if captured.ip != "" {
		t.Errorf("ip query = %q, want empty (auto-detect)", captured.ip)
	}
}

func TestDuckDNS_UpdateVerboseExtractsIP(t *testing.T) {
	// Con verbose=true DuckDNS responde "OK\n<ip>\n<UPDATED|NOCHANGE>".
	srv, captured := duckDNSServer(t, "OK\n162.213.65.160\nUPDATED", http.StatusOK)
	p := buildProvider(t, srv.URL)

	res, err := p.Update(context.Background(), "test.duckdns.org", "secret-token-x")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res == nil {
		t.Fatal("result nil")
	}
	if res.NewIP != "162.213.65.160" {
		t.Errorf("NewIP = %q, want 162.213.65.160", res.NewIP)
	}
	if !res.Changed {
		t.Errorf("Changed = false, want true (UPDATED)")
	}
	if res.NoChange {
		t.Errorf("NoChange = true, want false")
	}

	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.verbose != "true" {
		t.Errorf("verbose query = %q, want true", captured.verbose)
	}
}

func TestDuckDNS_UpdateVerboseNoChange(t *testing.T) {
	srv, _ := duckDNSServer(t, "OK\n162.213.65.160\nNOCHANGE", http.StatusOK)
	p := buildProvider(t, srv.URL)

	res, err := p.Update(context.Background(), "test.duckdns.org", "secret-token-x")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.NewIP != "162.213.65.160" {
		t.Errorf("NewIP = %q, want 162.213.65.160", res.NewIP)
	}
	if !res.NoChange {
		t.Errorf("NoChange = false, want true")
	}
	if res.Changed {
		t.Errorf("Changed = true, want false (NOCHANGE)")
	}
}

func TestDuckDNS_UpdateRejectsEmptySecret(t *testing.T) {
	// El server no debería ni recibir la petición — fail-fast antes.
	srv, captured := duckDNSServer(t, "OK", http.StatusOK)
	p := buildProvider(t, srv.URL)

	_, err := p.Update(context.Background(), "test", "")
	if !errors.Is(err, ErrDDNSAuthFailed) {
		t.Errorf("err = %v, want ErrDDNSAuthFailed", err)
	}
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.count != 0 {
		t.Error("provider should not call server with empty secret")
	}
}

func TestDuckDNS_UpdateRejectsInvalidDomain(t *testing.T) {
	srv, _ := duckDNSServer(t, "OK", http.StatusOK)
	p := buildProvider(t, srv.URL)

	for _, bad := range []string{"", "-bad", "has space"} {
		_, err := p.Update(context.Background(), bad, "tok")
		if err == nil {
			t.Errorf("domain %q: expected error", bad)
		}
	}
}

func TestDuckDNS_UpdateReturnsAuthFailedOnKO(t *testing.T) {
	srv, _ := duckDNSServer(t, "KO", http.StatusOK)
	p := buildProvider(t, srv.URL)

	_, err := p.Update(context.Background(), "test", "bad-token")
	if !errors.Is(err, ErrDDNSAuthFailed) {
		t.Errorf("err = %v, want ErrDDNSAuthFailed", err)
	}
}

func TestDuckDNS_KOdoesNotOpenBreaker(t *testing.T) {
	// El breaker se abre tras N fallos transient. KO no debería contar.
	srv, _ := duckDNSServer(t, "KO", http.StatusOK)
	p := buildProvider(t, srv.URL)

	// Llamar muchas veces — el breaker no debería abrirse.
	for i := 0; i < 20; i++ {
		_, err := p.Update(context.Background(), "test", "bad")
		if !errors.Is(err, ErrDDNSAuthFailed) {
			t.Fatalf("call %d: expected ErrDDNSAuthFailed, got %v", i, err)
		}
	}
	// El breaker sigue cerrado (apto para tráfico).
	if state := p.breaker.GetState(); state != CircuitClosed {
		t.Errorf("breaker = %v after many KO, want closed", state)
	}
}

func TestDuckDNS_TransientOpensBreaker(t *testing.T) {
	// Server siempre 500 → transient → breaker eventualmente abre.
	srv, _ := duckDNSServer(t, "boom", http.StatusInternalServerError)
	p := buildProvider(t, srv.URL)

	threshold := DefaultBreakerConfig("x").FailureThreshold
	for i := 0; i < threshold+2; i++ {
		_, err := p.Update(context.Background(), "test", "tok")
		if err == nil {
			t.Errorf("call %d: expected error", i)
		}
	}
	if state := p.breaker.GetState(); state != CircuitOpen {
		t.Errorf("breaker = %v after %d transient failures, want open", state, threshold+2)
	}
}

func TestDuckDNS_OpenBreakerReturnsTransient(t *testing.T) {
	srv, _ := duckDNSServer(t, "boom", http.StatusInternalServerError)
	p := buildProvider(t, srv.URL)

	// Forzar apertura.
	threshold := DefaultBreakerConfig("x").FailureThreshold
	for i := 0; i < threshold+1; i++ {
		_, _ = p.Update(context.Background(), "test", "tok")
	}

	// La siguiente llamada el breaker la rechaza sin tocar el server.
	_, err := p.Update(context.Background(), "test", "tok")
	if !errors.Is(err, ErrDDNSTransient) {
		t.Errorf("err = %v, want ErrDDNSTransient (breaker open)", err)
	}
}

func TestDuckDNS_UnexpectedResponseNotTransient(t *testing.T) {
	srv, _ := duckDNSServer(t, "what is this", http.StatusOK)
	p := buildProvider(t, srv.URL)

	_, err := p.Update(context.Background(), "test", "tok")
	if err == nil {
		t.Fatal("expected error")
	}
	// No es transient (no abre breaker) y no es auth.
	if errors.Is(err, ErrDDNSTransient) || errors.Is(err, ErrDDNSAuthFailed) {
		t.Errorf("unexpected sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Errorf("err message = %q", err.Error())
	}

	// Verificar que el breaker sigue cerrado tras varias respuestas raras.
	for i := 0; i < 10; i++ {
		_, _ = p.Update(context.Background(), "test", "tok")
	}
	if state := p.breaker.GetState(); state != CircuitClosed {
		t.Errorf("breaker = %v after unexpected responses, want closed", state)
	}
}

func TestDuckDNS_ContextCancellation(t *testing.T) {
	// Server que tarda mucho → cancelar el contexto debería cortarlo.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	p := buildProvider(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Update(ctx, "test", "tok")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if elapsed > time.Second {
		t.Errorf("Update took %v, expected fast cancellation", elapsed)
	}
}

func TestDuckDNS_SecretNotInError(t *testing.T) {
	// Defensa básica: si la respuesta es rara, el error NO debe contener
	// el token en plaintext.
	srv, _ := duckDNSServer(t, "weird response with garbage", http.StatusOK)
	p := buildProvider(t, srv.URL)

	secret := "highly-sensitive-token-12345"
	_, err := p.Update(context.Background(), "test", secret)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks secret: %v", err)
	}
}
