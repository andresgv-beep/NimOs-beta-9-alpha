// network_ddns_duckdns.go — Proveedor DDNS para DuckDNS.
//
// Endpoint:
//   https://www.duckdns.org/update?domains=<sub>&token=<tok>&ip=
//
// Respuestas conocidas (texto plano):
//   - "OK"   → update aceptado. DuckDNS NO indica si la IP cambió o no.
//   - "KO"   → error (token inválido, subdominio no propio, malformado).
//   - Cualquier otra cosa → tratada como respuesta inesperada → error
//     no-auth (no abre breaker, pero falla la operación).
//
// El parámetro `ip=` vacío fuerza a DuckDNS a usar la IP de origen del
// request — exactamente lo que queremos (no necesitamos detectar la IP
// pública nosotros).
//
// Diseño:
//   - El provider NO conoce la DB.
//   - Recibe el secret (token) como argumento. El reconciler lo descifra
//     y lo pasa. El provider no lo persiste ni lo loguea.
//   - Wrapped en CircuitBreaker (uno por instancia del provider).
//   - http.Client con timeout corto: DuckDNS es muy rápido cuando
//     funciona; si tarda >10s es síntoma de degradación.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// DuckDNSProvider
// ─────────────────────────────────────────────────────────────────────────────

// DuckDNSProvider implementa DDNSProvider para www.duckdns.org.
type DuckDNSProvider struct {
	httpClient *http.Client
	breaker    *CircuitBreaker

	// endpoint es el base URL del API. Configurable para tests
	// (httptest server en lugar del real). En producción siempre es
	// https://www.duckdns.org/update.
	endpoint string
}

// DuckDNSProviderConfig agrupa parámetros del constructor. Defaults vía
// DefaultDuckDNSConfig().
type DuckDNSProviderConfig struct {
	// HTTPClient se inyecta para tests. Si nil, se crea uno con timeout
	// de 10s y sin redirects automáticos.
	HTTPClient *http.Client

	// Breaker es obligatorio. El caller (boot del módulo network) lo
	// crea y lo registra en el registry global de breakers.
	Breaker *CircuitBreaker

	// Endpoint permite override del URL (tests). Si vacío, usa el real.
	Endpoint string
}

// defaultDuckDNSEndpoint es el URL real del API. Constante interna —
// los callers no lo deberían tocar excepto en tests.
const defaultDuckDNSEndpoint = "https://www.duckdns.org/update"

// NewDuckDNSProvider construye un provider listo para usar. Requiere
// un breaker (no creamos uno por defecto para forzar que el caller lo
// registre conscientemente en el registry global).
func NewDuckDNSProvider(cfg DuckDNSProviderConfig) (*DuckDNSProvider, error) {
	if cfg.Breaker == nil {
		return nil, errors.New("NewDuckDNSProvider: Breaker is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 10 * time.Second,
			// No queremos redirects: DuckDNS responde directo, y si nos
			// redirige es síntoma de algo raro (MITM, proxy intercept).
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultDuckDNSEndpoint
	}
	return &DuckDNSProvider{
		httpClient: cfg.HTTPClient,
		breaker:    cfg.Breaker,
		endpoint:   cfg.Endpoint,
	}, nil
}

// Name implementa DDNSProvider.
func (p *DuckDNSProvider) Name() string { return "duckdns" }

// Update implementa DDNSProvider. Llama al API real envuelto en el breaker.
//
// Convención del dominio:
//   - DuckDNS acepta el subdominio sin sufijo: "foo" para "foo.duckdns.org".
//   - Aceptamos ambas formas: si el caller pasa "foo.duckdns.org", lo
//     reducimos a "foo". Esto evita errores comunes del usuario.
func (p *DuckDNSProvider) Update(ctx context.Context, domain, secret string) (*DDNSUpdateResult, error) {
	subdomain, err := duckdnsSubdomain(domain)
	if err != nil {
		return nil, err
	}
	if secret == "" {
		return nil, ErrDDNSAuthFailed
	}

	// Construir URL.
	q := url.Values{}
	q.Set("domains", subdomain)
	q.Set("token", secret)
	q.Set("ip", "")          // forzar a DuckDNS a detectar la IP del cliente
	q.Set("verbose", "true") // pedir respuesta extendida: "OK\n<ip>\n<changed>"
	reqURL := p.endpoint + "?" + q.Encode()

	// Realizar la petición envuelta en el breaker.
	var result *DDNSUpdateResult
	var providerErr error

	breakerErr := p.breaker.Call(func() error {
		body, fetchErr := p.fetch(ctx, reqURL)
		if fetchErr != nil {
			// fetch ya clasifica como ErrDDNSTransient cuando aplica.
			// Para el breaker, todo error es contador de fallos.
			providerErr = fetchErr
			return fetchErr
		}
		// Parse de respuesta.
		// Con verbose=true DuckDNS responde en varias líneas:
		//   OK\n<ip>\n<UPDATED|NOCHANGE>     (éxito)
		//   KO                                 (auth/token inválido)
		// Sin verbose respondería solo "OK"/"KO" — toleramos ambos.
		trimmed := strings.TrimSpace(body)
		lines := strings.Split(trimmed, "\n")
		status := strings.TrimSpace(lines[0])

		switch status {
		case "OK":
			newIP := ""
			noChange := false
			if len(lines) >= 2 {
				newIP = strings.TrimSpace(lines[1])
			}
			if len(lines) >= 3 {
				noChange = strings.EqualFold(strings.TrimSpace(lines[2]), "NOCHANGE")
			}
			result = &DDNSUpdateResult{
				NewIP:       newIP, // IP confirmada por DuckDNS (verbose)
				Changed:     newIP != "" && !noChange,
				NoChange:    noChange,
				RawResponse: trimmed,
			}
			return nil
		case "KO":
			// Auth failure: NO cuenta como fallo del breaker (el provider
			// está vivo, son las credenciales). Pero sí lo señalamos como
			// error para que el reconciler clasifique.
			providerErr = ErrDDNSAuthFailed
			return nil // ← nil al breaker para no incrementar failures
		default:
			// Respuesta inesperada. No es fallo de red, no es auth — es
			// algo raro. NO contamos como breaker failure tampoco.
			providerErr = fmt.Errorf("duckdns: unexpected response %q", trimmed)
			return nil
		}
	})

	// breakerErr puede ser ErrCircuitOpen (el breaker rechazó la
	// llamada antes de ejecutarla).
	if errors.Is(breakerErr, ErrCircuitOpen) {
		return nil, ErrDDNSTransient
	}
	if providerErr != nil {
		return nil, providerErr
	}
	if breakerErr != nil {
		// breakerErr no nil y providerErr nil: el fn devolvió error
		// transient pero providerErr no lo capturó (no debería pasar
		// con la lógica actual, pero defensivo).
		return nil, ErrDDNSTransient
	}
	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internals
// ─────────────────────────────────────────────────────────────────────────────

// fetch ejecuta la petición HTTP. Clasifica errores en
// ErrDDNSTransient (red, 5xx, timeout) o devuelve otro error si es
// algo inesperado (no debería pasar con DuckDNS).
func (p *DuckDNSProvider) fetch(ctx context.Context, reqURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("duckdns: build request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		// Cualquier fallo de transporte (DNS, conexión, timeout) →
		// transient. El breaker lo contará y eventualmente abrirá.
		return "", ErrDDNSTransient
	}
	defer resp.Body.Close()

	// 5xx → transient (DuckDNS está caído pero podría volver).
	if resp.StatusCode >= 500 {
		return "", ErrDDNSTransient
	}
	// 4xx → respuesta del provider, lo dejamos pasar para que el caller
	// vea el body. DuckDNS usa 200 incluso para errores (responde "KO"),
	// así que un 4xx aquí sería raro.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", ErrDDNSTransient
	}
	if resp.StatusCode >= 400 {
		// Inesperado pero no auth: respuesta del servidor con body.
		// Lo tratamos como error no-transient (no abre breaker).
		return "", fmt.Errorf("duckdns: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// duckdnsSubdomain extrae el subdominio del formato aceptado por DuckDNS.
// Acepta tanto "foo" como "foo.duckdns.org". Devuelve error si el
// dominio no tiene la pinta esperada.
func duckdnsSubdomain(domain string) (string, error) {
	d := strings.TrimSpace(strings.ToLower(domain))
	if d == "" {
		return "", errors.New("duckdns: empty domain")
	}
	d = strings.TrimSuffix(d, ".duckdns.org")
	// El subdominio debe ser un identificador válido: solo
	// alfanuméricos y guiones (regla de DNS estándar). Validación
	// permisiva pero rechaza espacios, slashes, etc.
	if d == "" {
		return "", errors.New("duckdns: empty subdomain")
	}
	for _, c := range d {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			return "", fmt.Errorf("duckdns: invalid subdomain character %q", c)
		}
	}
	if d[0] == '-' || d[len(d)-1] == '-' {
		return "", errors.New("duckdns: subdomain cannot start or end with hyphen")
	}
	return d, nil
}
