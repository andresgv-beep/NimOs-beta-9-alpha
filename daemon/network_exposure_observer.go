// network_exposure_observer.go — Observa el estado de los certs de Caddy.
//
// NimOS NO gestiona los certs (eso lo hace Caddy: solicitud, renovación y
// reintentos ACME). Pero para dar buena UX, el control plane DEBE saber el
// estado de los certs y mostrarlo al usuario: "immich.dominio · válido ·
// expira en 67 días". Sin esto, el usuario no sabría si su HTTPS funciona
// hasta que algo se rompe.
//
// Este observer consulta periódicamente GET /pki/certificates de la API
// admin de Caddy, parsea la respuesta y publica un snapshot atómico
// (lock-free para los handlers HTTP). Si Caddy no responde, el snapshot
// queda marcado como "desconocido" — no es un fallo crítico.
//
// El snapshot se sirve por el endpoint de exposición para que la UI pinte
// el estado de cada cert junto a su app.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// ExposureCertStatus describe el estado de un cert gestionado por Caddy,
// derivado de /pki/certificates. Es lo que la UI muestra.
type ExposureCertStatus struct {
	Subject  string    `json:"subject"`   // dominio principal del cert
	Issuer   string    `json:"issuer"`    // "Let's Encrypt", etc.
	NotAfter time.Time `json:"not_after"` // expiración
	Managed  bool      `json:"managed"`   // gestionado por Caddy (ACME)
	DaysLeft int       `json:"days_left"` // días hasta expirar (puede ser <0)
}

// ExposureCertSnapshot es el último resultado del observer.
type ExposureCertSnapshot struct {
	ObservedAt time.Time            `json:"observed_at"`
	Reachable  bool                 `json:"reachable"` // ¿respondió Caddy?
	Certs      []ExposureCertStatus `json:"certs"`
}

// caddyCertFetcher es el subconjunto del cliente Caddy que el observer usa.
// Interfaz para mockear en tests.
//
// Ping y FetchCertificates responden preguntas DISTINTAS:
//
//	· Ping → ¿está vivo el admin de Caddy? (fuente de verdad de Reachable)
//	· FetchCertificates → ¿qué certs hay? (puede fallar con Caddy vivo,
//	  p.ej. mientras el endpoint de certs no exista o no haya certs aún)
type caddyPinger interface {
	// Ping → ¿está vivo el admin de Caddy? (fuente de verdad de Reachable)
	Ping(ctx context.Context) error
}

// certProber sondea el cert TLS que Caddy sirve REALMENTE para un dominio.
// Caddy no expone un endpoint que liste sus certs ACME, así que en vez de
// preguntar, MIRAMOS: abrimos una conexión TLS y leemos el cert servido.
// Eso refleja exactamente lo que recibe el usuario. Interfaz para mockear.
type certProber interface {
	Probe(ctx context.Context, domain string, port int) (ExposureCertStatus, error)
}

// NetworkExposureObserver consulta Caddy y mantiene un snapshot atómico.
type NetworkExposureObserver struct {
	repo   *NetworkRepo
	clock  Clock
	config NetworkExposureObserverConfig

	// pingerFor crea un pinger para la URL admin dada. Inyectable.
	pingerFor func(adminURL string) caddyPinger
	// prober sondea el cert TLS servido por Caddy. Inyectable en tests.
	prober certProber

	last atomic.Pointer[ExposureCertSnapshot]
}

// NetworkExposureObserverConfig agrupa parámetros tunables.
type NetworkExposureObserverConfig struct {
	Interval time.Duration
}

// DefaultNetworkExposureObserverConfig devuelve config de producción.
func DefaultNetworkExposureObserverConfig() NetworkExposureObserverConfig {
	return NetworkExposureObserverConfig{
		// 60s: el sondeo TLS es local y barato (un handshake por dominio),
		// y así la UI refleja un cert recién emitido en ≤1 min en vez de
		// hasta 5. Los certs cambian lento, pero el FEEDBACK debe ser rápido.
		Interval: 60 * time.Second,
	}
}

// NewNetworkExposureObserver construye el observer. clock nil → RealClock.
func NewNetworkExposureObserver(repo *NetworkRepo, clock Clock, config NetworkExposureObserverConfig) *NetworkExposureObserver {
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = DefaultNetworkExposureObserverConfig().Interval
	}
	o := &NetworkExposureObserver{
		repo:   repo,
		clock:  clock,
		config: config,
	}
	o.pingerFor = func(adminURL string) caddyPinger {
		return NewCaddyAdminClient(adminURL, nil)
	}
	o.prober = tlsCertProber{}
	return o
}

func (o *NetworkExposureObserver) Name() string            { return "exposure_certs" }
func (o *NetworkExposureObserver) Tier() ReconcilerTier    { return TierLow }
func (o *NetworkExposureObserver) Interval() time.Duration { return o.config.Interval }

// Snapshot devuelve el último estado observado, o nil si nunca corrió.
// Lectura lock-free — apta para handlers HTTP.
func (o *NetworkExposureObserver) Snapshot() *ExposureCertSnapshot {
	return o.last.Load()
}

// Reconcile (nombre por la interfaz Reconciler) ejecuta una observación.
// Consulta Caddy, parsea certs, publica snapshot. Nunca devuelve error
// fatal: si Caddy no responde, publica un snapshot "no alcanzable".
func (o *NetworkExposureObserver) Reconcile(ctx context.Context) error {
	cfg, err := o.repo.GetExposureConfig(ctx)
	if err != nil {
		// Sin config no podemos saber la URL admin. Snapshot vacío.
		o.publish(&ExposureCertSnapshot{ObservedAt: o.clock.Now().UTC(), Reachable: false})
		return nil
	}

	// 1) ¿Está vivo Caddy? Ping al admin (GET /config/). Solo si esto falla
	//    marcamos Reachable=false — Caddy caído de verdad.
	if perr := o.pingerFor(cfg.CaddyAdminURL).Ping(ctx); perr != nil {
		o.publish(&ExposureCertSnapshot{ObservedAt: o.clock.Now().UTC(), Reachable: false})
		return nil
	}

	// 2) Caddy vive. Sondear el TLS REAL de cada dominio gestionado: abrimos
	//    una conexión TLS (SNI = dominio) al puerto HTTPS de Caddy y leemos
	//    el cert servido. Es la única forma fiable de saber qué sirve Caddy
	//    (no hay endpoint que liste los certs ACME). Un dominio sin cert aún
	//    simplemente no aparece → su app mostrará "emitiendo", ahora con razón.
	apps, _ := o.repo.ListExposedApps(ctx)
	enabled := make([]*NetworkExposedApp, 0, len(apps))
	for _, a := range apps {
		if a.Enabled {
			enabled = append(enabled, a)
		}
	}
	domains := collectTLSDomains(cfg, enabled)

	certs := make([]ExposureCertStatus, 0, len(domains))
	for _, d := range domains {
		st, err := o.prober.Probe(ctx, d, cfg.HTTPSPort)
		if err != nil {
			continue // sin cert servido todavía para ese dominio
		}
		certs = append(certs, st)
	}

	o.publish(&ExposureCertSnapshot{
		ObservedAt: o.clock.Now().UTC(),
		Reachable:  true,
		Certs:      certs,
	})
	return nil
}

func (o *NetworkExposureObserver) publish(s *ExposureCertSnapshot) {
	o.last.Store(s)
}

// ─────────────────────────────────────────────────────────────────────────────
// Sondeo TLS real
// ─────────────────────────────────────────────────────────────────────────────

// tlsCertProber abre una conexión TLS a 127.0.0.1:port con SNI=domain y lee
// el certificado que Caddy sirve. InsecureSkipVerify: no validamos la cadena
// (es loopback y solo queremos LEER el cert); la validez la derivamos de
// NotAfter. Refleja exactamente lo que un cliente externo recibiría.
type tlsCertProber struct{}

func (tlsCertProber) Probe(ctx context.Context, domain string, port int) (ExposureCertStatus, error) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 4 * time.Second},
		Config: &tls.Config{
			ServerName:         domain,
			InsecureSkipVerify: true,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return ExposureCertStatus{}, err
	}
	defer conn.Close()

	peers := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return ExposureCertStatus{}, fmt.Errorf("no peer certificate served for %s", domain)
	}
	leaf := peers[0]
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	return ExposureCertStatus{
		Subject:  domain,
		Issuer:   issuer,
		NotAfter: leaf.NotAfter,
		Managed:  true,
		DaysLeft: int(time.Until(leaf.NotAfter).Hours() / 24),
	}, nil
}
