// network_boot.go — Inicialización del módulo network Beta 8.1 v4.
//
// Centraliza el arranque del nuevo stack network (Repo + EventEmitter +
// Scheduler + Observer + Secrets + DDNS) en una sola función llamada
// desde main.go.
//
// Orden de arranque del módulo:
//   1. openDB                   ← db.go
//   2. initNimosCoreSchema      ← nimos_core_schema.go (tablas globales)
//   3. initNetworkSchema        ← network_schema.go (tablas network_*)
//   4. initNetworkModule        ← este archivo
//
// Tras este punto, los siguientes singletons quedan disponibles:
//   - networkRepo:           CRUD de Ports/Ddns/Certs + audit tables
//   - networkEventEmitter:   Emit() con dedupe + rate limit
//   - networkSecretsStore:   Cifrado de tokens DDNS y similares
//   - networkProbe:          lee realidad del sistema (puertos, certs)
//   - networkObserver:       singleton observer; registrado en scheduler
//   - networkDDNSReconciler: reconciler DDNS con providers registrados
//   - networkReconcilers:    scheduler con observer + ddns_updater
//
// El scheduler NO se arranca automáticamente — el caller (main.go)
// debe invocar networkReconcilers.Start(ctx) cuando esté listo.

package main

import (
	"context"
	"fmt"
)

// Singletons globales del módulo network.
var (
	networkRepo               *NetworkRepo
	networkEventEmitter       *EventEmitter
	networkSecretsStore       *SecretsStore
	networkCapabilities       *CapabilitiesStore
	networkProbe              NetworkProbe
	networkObserver           *NetworkObserver
	networkDDNSReconciler     *DDNSReconciler
	networkRouterProvider     RouterProvider
	networkRouterReconciler   *RouterReconciler
	networkExposureReconciler *NetworkExposureReconciler
	networkRetentionRunner    *RetentionRunner
	networkReconcilers        *ReconcilerScheduler
)

// initNetworkModule inicializa el módulo network v4.
// Debe llamarse DESPUÉS de initNimosCoreSchema() e initNetworkSchema()
// (tablas creadas) y ANTES de cualquier código que use los singletons.
func initNetworkModule() error {
	if db == nil {
		return fmt.Errorf("initNetworkModule: db is nil (call openDB first)")
	}

	// Clock real para producción. Tests inyectan FakeClock vía construcción
	// directa de los structs.
	clock := NewRealClock()

	networkRepo = NewNetworkRepo(db, clock)
	networkEventEmitter = NewEventEmitter(db, clock, DefaultEventEmitterConfig())

	// SecretsStore: carga (o crea) la master key desde el path canónico.
	store, err := NewSecretsStore(db, DefaultMasterKeyPath, clock)
	if err != nil {
		return fmt.Errorf("initNetworkModule: build secrets store: %w", err)
	}
	networkSecretsStore = store

	// CapabilitiesStore — detección on-demand de binarios del sistema
	// (certbot, openssl, dig, upnpc, nft/iptables...). El detector real
	// ejecuta LookPath y subprocesos, así que NO refrescamos en boot
	// para no añadir latencia al arranque. El primer GET refresca si
	// nunca se detectó.
	caps, err := NewCapabilitiesStore(db, clock, RealDetect)
	if err != nil {
		return fmt.Errorf("initNetworkModule: build capabilities store: %w", err)
	}
	networkCapabilities = caps

	// Probe real. Las funciones HTTPListener/HTTPSListener se inyectarán
	// cuando F-003 wirees el HTTP server — hasta entonces, el probe
	// reporta los listeners como no-listening, lo que es seguro: el
	// observer NO marcará drift porque los ports aún tienen applied=0.
	networkProbe = NewRealNetworkProbe(clock)

	obs, err := NewNetworkObserver(networkRepo, networkEventEmitter,
		networkProbe, clock, DefaultObserverConfig())
	if err != nil {
		return fmt.Errorf("initNetworkModule: build observer: %w", err)
	}
	networkObserver = obs

	// DDNS Reconciler con sus providers.
	ddnsRec, err := NewDDNSReconciler(networkRepo, networkSecretsStore,
		networkEventEmitter, clock, DefaultDDNSReconcilerConfig())
	if err != nil {
		return fmt.Errorf("initNetworkModule: build ddns reconciler: %w", err)
	}
	networkDDNSReconciler = ddnsRec

	// DuckDNS update provider (para el reconciler DDNS).
	duckUpdateBreaker := NewCircuitBreaker(DefaultBreakerConfig("ddns.duckdns"))
	duckUpdateProvider, err := NewDuckDNSProvider(DuckDNSProviderConfig{
		Breaker: duckUpdateBreaker,
	})
	if err != nil {
		return fmt.Errorf("initNetworkModule: build duckdns provider: %w", err)
	}
	networkDDNSReconciler.RegisterProvider(duckUpdateProvider)

	// Scheduler con observer + ddns reconciler.
	networkReconcilers = NewReconcilerScheduler(clock)
	if err := networkReconcilers.Register(networkObserver); err != nil {
		return fmt.Errorf("initNetworkModule: register observer: %w", err)
	}
	if err := networkReconcilers.Register(networkDDNSReconciler); err != nil {
		return fmt.Errorf("initNetworkModule: register ddns reconciler: %w", err)
	}

	// Router provider (UPnP) y reconciler. Best-effort: si upnpc no
	// está instalado o no hay router UPnP en la red, el reconciler
	// emite warn y sigue funcionando.
	upnpBreaker := NewCircuitBreaker(DefaultBreakerConfig("upnp.router"))
	upnpProvider, err := NewUPnPRouterProvider(UPnPRouterProviderConfig{
		Breaker: upnpBreaker,
	})
	if err != nil {
		return fmt.Errorf("initNetworkModule: build upnp provider: %w", err)
	}
	networkRouterProvider = upnpProvider

	routerRec, err := NewRouterReconciler(networkRepo, networkEventEmitter,
		clock, networkRouterProvider, DefaultRouterReconcilerConfig())
	if err != nil {
		return fmt.Errorf("initNetworkModule: build router reconciler: %w", err)
	}
	networkRouterReconciler = routerRec
	if err := networkReconcilers.Register(networkRouterReconciler); err != nil {
		return fmt.Errorf("initNetworkModule: register router reconciler: %w", err)
	}

	// Exposure reconciler (Caddy) + observer de certs. El reconciler aplica
	// el intent (apps expuestas) a Caddy vía su API admin; el observer lee
	// /pki/certificates para que la UI muestre el estado de los certs. Ambos
	// best-effort: si Caddy no está corriendo, degradan sin tumbar el módulo.
	networkExposureReconciler = NewNetworkExposureReconciler(networkRepo,
		networkSecretsStore, NewUFWFirewall(nil), networkEventEmitter, clock,
		DefaultNetworkExposureReconcilerConfig())
	if err := networkReconcilers.Register(networkExposureReconciler); err != nil {
		return fmt.Errorf("initNetworkModule: register exposure reconciler: %w", err)
	}

	networkExposureObserver = NewNetworkExposureObserver(networkRepo,
		clock, DefaultNetworkExposureObserverConfig())
	if err := networkReconcilers.Register(networkExposureObserver); err != nil {
		return fmt.Errorf("initNetworkModule: register exposure observer: %w", err)
	}

	// Retention runner. NO se arranca aquí: el caller (main.go) llama
	// .Start(ctx) tras inicializar todo lo demás. Mantenemos el patrón
	// del scheduler para consistencia.
	retentionRunner, err := NewRetentionRunner(networkRepo, networkEventEmitter,
		clock, DefaultRetentionRunnerConfig())
	if err != nil {
		return fmt.Errorf("initNetworkModule: build retention runner: %w", err)
	}
	networkRetentionRunner = retentionRunner

	// Verificación defensiva: probar una query trivial contra las tablas
	// network_*. Si el schema no está creado o la conexión está rota,
	// queremos saberlo aquí, no en el primer request HTTP.
	if _, err := networkRepo.CountObservedSnapshots(context.Background()); err != nil {
		return fmt.Errorf("initNetworkModule: defensive query failed: %w", err)
	}

	logMsg("Network module v4 ready (6 reconcilers + retention runner)")
	return nil
}
