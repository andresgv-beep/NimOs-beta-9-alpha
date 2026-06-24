// network_exposure_reconciler.go — Reconciler del subsistema de exposición.
//
// Orquesta el flujo completo que convierte el intent declarado en la DB en
// rutas activas en Caddy:
//
//   1. Lee la config global (network_exposure_config).
//   2. Si exposure está deshabilitado globalmente → carga config Caddy
//      vacía (sin rutas) y marca todas las apps como aplicadas. Kill-switch.
//   3. Si está habilitado → lee apps habilitadas, genera la config Caddy,
//      la envía vía API admin (Load), y al converger marca applied en cada
//      app pendiente.
//
// Estrategia "config completa": en cada pasada se regenera y envía TODA la
// config a Caddy (POST /load reemplaza la anterior atómicamente). No hay
// updates incrementales — es más simple y robusto, y Caddy lo soporta sin
// downtime. Coherente con el modelo declarativo del resto de v4.
//
// Tier: Medium. Si Caddy está caído, la exposición se degrada pero el
// daemon y la LAN siguen operativos.

package main

import (
	"context"
	"fmt"
	"time"
)

// NetworkExposureReconcilerConfig agrupa parámetros tunables.
type NetworkExposureReconcilerConfig struct {
	Interval time.Duration
}

// DefaultNetworkExposureReconcilerConfig devuelve la config de producción.
func DefaultNetworkExposureReconcilerConfig() NetworkExposureReconcilerConfig {
	return NetworkExposureReconcilerConfig{
		Interval: 30 * time.Second,
	}
}

// NetworkExposureReconciler implementa Reconciler.
type NetworkExposureReconciler struct {
	repo     *NetworkRepo
	secrets  *SecretsStore   // para descifrar el token DuckDNS (puede ser nil)
	firewall firewallEnsurer // abre los puertos de exposición en ufw (puede ser nil)
	emitter  *EventEmitter
	clock   Clock
	config  NetworkExposureReconcilerConfig

	// caddyClientFor crea un cliente para la URL admin dada. Inyectable
	// para tests (mock). En producción usa NewCaddyAdminClient real.
	caddyClientFor func(adminURL string) caddySyncer
}

// caddySyncer es el subconjunto del cliente Caddy que el reconciler usa.
// Interfaz para poder mockear en tests.
type caddySyncer interface {
	SyncAppRoutes(ctx context.Context, routes []caddyRoute) error
	SyncTLS(ctx context.Context, domains []string, policy caddyTLSPolicy) error
	SyncListen(ctx context.Context, httpPort, httpsPort int) error
}

// NewNetworkExposureReconciler construye el reconciler. clock nil → RealClock.
// secrets puede ser nil: la sincronización de rutas funciona igual, pero la
// gestión de certs (DNS-01) queda deshabilitada hasta tener el store.
// firewall puede ser nil: no se gestiona el firewall del host.
func NewNetworkExposureReconciler(repo *NetworkRepo, secrets *SecretsStore, firewall firewallEnsurer, emitter *EventEmitter, clock Clock, config NetworkExposureReconcilerConfig) *NetworkExposureReconciler {
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = DefaultNetworkExposureReconcilerConfig().Interval
	}
	r := &NetworkExposureReconciler{
		repo:     repo,
		secrets:  secrets,
		firewall: firewall,
		emitter:  emitter,
		clock:    clock,
		config:   config,
	}
	// Factory por defecto: cliente Caddy real.
	r.caddyClientFor = func(adminURL string) caddySyncer {
		return NewCaddyAdminClient(adminURL, nil)
	}
	return r
}

func (r *NetworkExposureReconciler) Name() string            { return "exposure_caddy" }
func (r *NetworkExposureReconciler) Tier() ReconcilerTier    { return TierMedium }
func (r *NetworkExposureReconciler) Interval() time.Duration { return r.config.Interval }

// Reconcile ejecuta una pasada de convergencia.
func (r *NetworkExposureReconciler) Reconcile(ctx context.Context) error {
	cfg, err := r.repo.GetExposureConfig(ctx)
	if err != nil {
		return fmt.Errorf("exposure reconcile: get config: %w", err)
	}

	apps, err := r.repo.ListExposedApps(ctx)
	if err != nil {
		return fmt.Errorf("exposure reconcile: list apps: %w", err)
	}

	// Determinar qué apps van a la config Caddy.
	//   - Exposure global OFF → config vacía (kill-switch), pero igualmente
	//     marcamos applied para no quedar en pending eterno.
	//   - Exposure global ON  → apps habilitadas.
	var caddyApps []*NetworkExposedApp
	if cfg.Enabled {
		for _, a := range apps {
			if a.Enabled {
				caddyApps = append(caddyApps, a)
			}
		}
	}

	client := r.caddyClientFor(cfg.CaddyAdminURL)

	// Puertos de escucha: configurables (setups donde :80/:443 están
	// ocupados por otro servicio, p.ej. un Synology en la misma máquina o
	// red con los puertos clásicos pillados). Va ANTES que las rutas para
	// que el server quede bindeado donde toca. Best-effort: si el puerto
	// está en uso, Caddy lo rechaza, emitimos evento y seguimos — el
	// server queda en los puertos anteriores, funcional.
	if err := client.SyncListen(ctx, cfg.HTTPPort, cfg.HTTPSPort); err != nil {
		r.emit(ctx, nil, "caddy_listen_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to sync Caddy listen ports (%d/%d): %v",
				cfg.HTTPPort, cfg.HTTPSPort, err))
	}

	// Firewall del host: si NimOS gestiona los puertos de Caddy, también
	// gestiona su paso por el firewall (ufw). Sin esto, cambiar el puerto
	// en la UI deja Caddy escuchando detrás de un muro. Best-effort.
	r.syncFirewall(ctx, cfg)
	// Intent TLS primero: los dominios gestionados determinan también la
	// redirección HTTP→HTTPS (redirigimos exactamente lo que tiene cert).
	domains := collectTLSDomains(cfg, caddyApps)
	token := r.duckdnsToken(ctx, cfg)
	if len(domains) > 0 && token == "" {
		// Sin token no hay DNS-01 posible: no pedimos a Caddy certs que no
		// puede validar (se quedarían reintentando contra Let's Encrypt),
		// y por tanto tampoco redirigimos a un HTTPS que no funcionaría.
		domains = []string{}
	}

	// Rutas de apps (el panel vive en el config base y NO se toca aquí),
	// precedidas del redirect HTTP→HTTPS si hay certs gestionados.
	routes := buildAppRoutes(cfg, caddyApps)
	if len(domains) > 0 {
		routes = append([]caddyRoute{buildHTTPSRedirectRoute(domains, cfg.HTTPSPort)}, routes...)
	}
	if err := client.SyncAppRoutes(ctx, routes); err != nil {
		// Caddy caído o config rechazada: degradación, no fatal. Emitimos
		// evento y NO marcamos applied (quedan pending para reintentar).
		r.emit(ctx, nil, "caddy_sync_failed", EventLevelError,
			fmt.Sprintf("Failed to sync Caddy routes: %v", err))
		return fmt.Errorf("exposure reconcile: caddy sync: %w", err)
	}

	// TLS: decirle a Caddy qué certs gestionar (automate) y cómo obtenerlos
	// (DNS-01 DuckDNS con el token del módulo DDNS). Best-effort: si falla,
	// las rutas ya están sincronizadas y el HTTP sigue funcionando — el cert
	// llegará en un ciclo posterior cuando se resuelva la causa.
	policy := buildTLSPolicy(domains, token, cfg.BaseDomain)
	if err := client.SyncTLS(ctx, domains, policy); err != nil {
		r.emit(ctx, nil, "caddy_tls_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to sync Caddy TLS automation: %v", err))
	}

	// Caddy aceptó la config. Marcar applied en cada app pendiente.
	for _, a := range apps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !a.Convergence.IsPending() {
			continue
		}
		tx, err := r.repo.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("exposure reconcile: begin tx: %w", err)
		}
		if err := r.repo.MarkExposedAppApplied(ctx, tx, a.ID); err != nil {
			_ = tx.Rollback()
			r.emit(ctx, &a.ID, "mark_applied_failed", EventLevelWarn,
				fmt.Sprintf("Failed to mark %s applied: %v", a.AppID, err))
			continue
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			continue
		}
		level := EventLevelInfo
		msg := fmt.Sprintf("App %s exposed", a.AppID)
		if !a.Enabled || !cfg.Enabled {
			msg = fmt.Sprintf("App %s unexposed", a.AppID)
		}
		r.emit(ctx, &a.ID, "exposure_applied", level, msg)
	}

	return nil
}

// emit publica un evento, ignorando errores de emisión (no deben abortar
// la reconciliación).
func (r *NetworkExposureReconciler) emit(ctx context.Context, targetID *string, event string, level EventLevel, msg string) {
	if r.emitter == nil {
		return
	}
	_, err := r.emitter.Emit(ctx, EventInput{
		Category: CategoryExposure,
		Event:    event,
		TargetID: targetID,
		Level:    level,
		Message:  msg,
	})
	if err != nil {
		logMsg("exposure reconciler: emit %s: %v", event, err)
	}
}

// duckdnsToken descifra el token DuckDNS reutilizando lo que el módulo DDNS
// ya custodia: busca la entrada DDNS del dominio base y descifra su
// TokenSecretID desde nimos_secrets. Devuelve "" si no hay entrada, no es
// duckdns, o no hay store — el caller degrada a "sin gestión de certs".
func (r *NetworkExposureReconciler) duckdnsToken(ctx context.Context, cfg NetworkExposureConfig) string {
	if r.secrets == nil || cfg.BaseDomain == "" {
		return ""
	}
	d, err := r.repo.GetDdnsByDomain(ctx, cfg.BaseDomain)
	if err != nil || d == nil || d.Provider != "duckdns" || d.TokenSecretID == "" {
		return ""
	}
	sec, err := r.secrets.GetSecret(SecretID(d.TokenSecretID))
	if err != nil {
		return ""
	}
	return string(sec.Plaintext)
}

// syncFirewall abre los puertos de exposición en el firewall del host y
// retira los que NimOS gestionaba y ya no se usan. Persiste la lista de
// puertos gestionados para que el siguiente ciclo (o un cambio de puertos)
// sepa qué reglas son nuestras. Best-effort: un fallo emite evento warn y
// no interrumpe la reconciliación.
func (r *NetworkExposureReconciler) syncFirewall(ctx context.Context, cfg NetworkExposureConfig) {
	if r.firewall == nil {
		return
	}
	want := []int{cfg.HTTPPort, cfg.HTTPSPort}
	managed, changed, err := r.firewall.EnsurePorts(ctx, want, cfg.FWManagedPorts)
	if err != nil {
		r.emit(ctx, nil, "firewall_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to sync host firewall for ports %v: %v", want, err))
		return
	}
	if !changed {
		return
	}
	tx, err := r.repo.db.BeginTx(ctx, nil)
	if err != nil {
		r.emit(ctx, nil, "firewall_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to persist firewall state: %v", err))
		return
	}
	if err := r.repo.UpdateFWManagedPorts(ctx, tx, managed); err != nil {
		_ = tx.Rollback()
		r.emit(ctx, nil, "firewall_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to persist firewall state: %v", err))
		return
	}
	if err := tx.Commit(); err != nil {
		r.emit(ctx, nil, "firewall_sync_failed", EventLevelWarn,
			fmt.Sprintf("Failed to persist firewall state: %v", err))
	}
}
