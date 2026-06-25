// boot.go — Secuencia de arranque del daemon, en fases nombradas.
//
// Extraído de main() (refactor 11/06/2026). El ORDEN de las fases es parte del
// contrato: cada función documenta qué necesita de las anteriores. main() las
// llama en secuencia; mover una fase de sitio es una decisión arquitectónica,
// no una limpieza.
//
//	bootCore     → DB + schemas + módulos storage/network (síncrono, aborta si falla)
//	bootHTTP     → herramientas hardware + servidor HTTP + mantenimiento
//	bootStorage  → reconciliación de montaje + recovery + monitoring (síncrono)
//	bootServices → schedulers y observers en background (backup, salud, shield…)
//
// D-003 (RESUELTO 11/06/2026): los RunOnce iniciales de NimHealth y del
// DockerAppReconciler esperaban sleeps fijos (8s/12s) "cediendo al boot storm".
// Eran adivinanzas calibradas a un hardware concreto: en un Pi lento Docker
// podía no estar listo aún (pasada inicial contra un daemon a medias) y en
// hardware rápido se desperdiciaban segundos. Ahora ambos esperan a la
// CONDICIÓN real — el daemon de Docker responde (`docker info`) — con
// waitForCondition: polling con backoff + timeout de seguridad de 60s tras el
// cual corren igualmente (mismo comportamiento degradado que antes). Si Docker
// ni siquiera está instalado, corren al instante: sus pasadas ya manejan la
// ausencia de Docker con gracia.
package main

import (
	"context"
	"os"
	"time"
)

// ═══════════════════════════════════
// Espera por condición (sustituto de los sleeps de D-003)
// ═══════════════════════════════════

// waitForCondition hace polling de check() hasta que devuelve true o se agota
// el timeout. Devuelve true si la condición se cumplió. El interval crece con
// backoff suave (x1.5, techo 5s) para no machacar el sistema durante el boot.
//
// Diseñada para dependencias de arranque: "espera a que X responda, pero no
// esperes para siempre". El caller decide qué hacer si devuelve false (lo
// normal: continuar en modo degradado y loguear).
func waitForCondition(ctx context.Context, name string, timeout time.Duration, check func() bool) bool {
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond
	for {
		if check() {
			return true
		}
		if time.Now().After(deadline) {
			logMsg("boot: waitForCondition(%s) agotó timeout de %s — continuando en modo degradado", name, timeout)
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}
		if interval < 5*time.Second {
			interval = time.Duration(float64(interval) * 1.5)
		}
	}
}

// dockerDaemonReady comprueba que el daemon de Docker RESPONDE (no solo que el
// binario existe — eso es isDockerInstalledGo). `docker info` falla mientras
// dockerd está arrancando, que es exactamente la ventana que queremos esperar.
func dockerDaemonReady() bool {
	_, ok := runSafe("docker", "info", "--format", "{{.ServerVersion}}")
	return ok
}

// ═══════════════════════════════════
// Fase 1 · Core: DB, schemas y módulos
// ═══════════════════════════════════

// bootCore inicializa la base de datos y los módulos que DEBEN existir antes
// de servir nada: storage (schema + repo/policy + recovery de ops huérfanas) y
// network (core schema + network schema + singletons + reconcilers).
//
// Cualquier fallo aquí aborta el daemon: un módulo inicializado a medias deja
// la DB en estado inconsistente. openDB() y su defer db.Close() viven en
// main() porque el defer debe sobrevivir a esta función.
func bootCore() {
	// Migrate from JSON files (first run only)
	migrateFromJSON()

	// Initialize Beta 8 storage schema (idempotent)
	// see docs/storage_invariants.md#5
	if err := initStorageSchema(); err != nil {
		logMsg("ERROR: cannot initialize storage schema: %v", err)
		os.Exit(1)
	}
	logMsg("Storage schema (Beta 8) ready")

	// Initialize Beta 8 storage module (Repo + Policy singletons)
	if err := initStorageModule(); err != nil {
		logMsg("ERROR: cannot initialize storage module: %v", err)
		os.Exit(1)
	}

	// Beta 8 storage startup tasks: recovery de operations huérfanas
	// y boot reconciliation de devices. Best-effort; los fallos se
	// loggean pero no abortan el daemon.
	runStorageStartupTasks(context.Background())

	// Beta 8.1 v4 · Network module bootstrap.
	//
	// Orden estricto:
	//   1. nimos_core_schema  (secrets + breakers + capabilities globales)
	//   2. network_schema     (ports/ddns/certs/observed/operations/events)
	//   3. initNetworkModule  (singletons NetworkRepo + EventEmitter + Scheduler)
	//
	// Si cualquier paso falla, abortamos: el módulo network no puede
	// inicializarse parcialmente sin dejar la DB en estado inconsistente.
	if err := initNimosCoreSchema(db); err != nil {
		logMsg("ERROR: cannot initialize nimos core schema: %v", err)
		os.Exit(1)
	}
	if err := initNetworkSchema(db); err != nil {
		logMsg("ERROR: cannot initialize network schema: %v", err)
		os.Exit(1)
	}
	if err := initNetworkModule(); err != nil {
		logMsg("ERROR: cannot initialize network module: %v", err)
		os.Exit(1)
	}

	// Arrancar el scheduler de reconcilers del módulo network. El
	// contexto es Background — el scheduler vivirá lo que el proceso.
	// Si el daemon muere, las goroutines terminan con el proceso.
	//
	// El observer se ejecutará cada 60s (DefaultObserverConfig) detectando
	// drift de ports/certs. F-004+ añadirán más reconcilers (DDNS, certs).
	if err := networkReconcilers.Start(context.Background()); err != nil {
		logMsg("ERROR: cannot start network reconcilers: %v", err)
		os.Exit(1)
	}

	// Arrancar el retention runner (F-008): goroutine que ejecuta las
	// purgas de retention 1 vez al día (03:00 UTC). NO bloquea boot —
	// la primera pasada se ejecuta en el próximo 03:00 UTC. Si quieres
	// disparar una pasada manual, llama networkRetentionRunner.RunOnce(ctx).
	networkRetentionRunner.Start(context.Background())

	// Fix B (NETWORK-POOL-DESIGN.md) · Migración del pool de direcciones de
	// Docker para instalaciones existentes: si el daemon.json no tiene
	// default-address-pools, lo añade (no destructivo, idempotente) y avisa de
	// que hace falta reiniciar Docker. NO reinicia Docker solo. Best-effort.
	ensureDockerAddressPool()

	// Beta 8.1 · Apps bootstrap: escanea apps native ya instaladas en el
	// sistema (samba, kvm, transmission...) y las registra en native_apps
	// con auto_detected=1. Las apps desinstaladas manualmente se purgan.
	//
	// Async + best-effort: si alguna app tiene un CheckCommand lento, no
	// queremos bloquear el arranque del HTTP server. Tampoco abortamos si
	// el bootstrap falla — el resto del daemon sigue funcionando normal.
	go func() {
		bootstrapCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		bootstrapNativeApps(bootstrapCtx)
	}()
}

// ═══════════════════════════════════
// Fase 2 · HTTP: API server y mantenimiento
// ═══════════════════════════════════

// bootHTTP detecta herramientas de hardware, levanta el servidor HTTP (bind
// loopback por defecto — ver http.go) y arranca el subsistema de
// mantenimiento. Requiere bootCore (los handlers leen de los repos).
func bootHTTP() {
	detectHardwareTools()
	startHTTPServer()
	startRateLimitCleanup()

	// Subsistema de mantenimiento (Fase 1): init tablas + registro de tareas.
	startMaintenance()
	maintenanceManager.Register(&torrentTmpSweepTask{})
	maintenanceManager.Register(&orphanDirSweepTask{})
	maintenanceManager.Register(&appUIDsHygieneTask{})
	maintenanceManager.Register(&dockerImagePruneTask{})
	maintenanceManager.Register(&dockerNetworkPruneTask{})
	startMaintenanceScheduler()
}

// ═══════════════════════════════════
// Fase 3 · Storage: montaje, recovery y monitoring
// ═══════════════════════════════════

// bootStorage reconcilia el estado de montaje de los pools y arranca el
// monitoring. El ORDEN INTERNO es crítico y está heredado tal cual del main()
// original (comentarios FIRST/THEN preservados).
func bootStorage() {
	// Beta 8: arrancar el reconciler en background.
	// Desactivable con NIMOS_NO_STORAGE_SCHEDULER=1 para debugging
	// o despliegues controlados.
	if os.Getenv("NIMOS_NO_STORAGE_SCHEDULER") != "1" {
		StartStorageScheduler(context.Background())
	} else {
		logMsg("Storage scheduler disabled by NIMOS_NO_STORAGE_SCHEDULER=1")
	}

	// Fase 7 Bloque C1 · Storage Observer
	// Mantiene un cache in-memory del observed state (BTRFS detectados,
	// devices físicos, divergencias vs managed). Loop periódico 60s +
	// triggers desde ops internas (notifyStorageChanged).
	//
	// Desactivable con NIMOS_NO_STORAGE_OBSERVER=1 para debugging.
	// Diseño completo en docs/storage_observer_design.md.
	if os.Getenv("NIMOS_NO_STORAGE_OBSERVER") != "1" {
		globalObserver = NewStorageObserver(60 * time.Second)
		globalObserver.Start()
		logMsg("Storage observer started (interval=60s)")
	} else {
		logMsg("Storage observer disabled by NIMOS_NO_STORAGE_OBSERVER=1")
	}

	// FIRST: Reconciliar estado de montaje antes de que nada toque storage.
	// Fase R1: monta pools no montados, desapila capas, reubica pools en
	// sitio equivocado (/media/ por udisks2), detecta read-only.
	// Sustituye al antiguo btrfsAutoMountOnStartup (que solo montaba a ciegas).
	if mr, err := reconcileMountState(context.Background()); err != nil {
		logMsg("startup: reconcileMountState error: %v", err)
		// Fallback: el auto-mount simple de siempre, por si reconcile falla
		btrfsAutoMountOnStartup()
	} else if mr.Failed > 0 {
		logMsg("startup: WARNING — %d pools no se pudieron reconciliar (ver logs)", mr.Failed)
	}
	startupStorage()

	// R3: limpiar mount-points huérfanos al arrancar (carpetas de pools
	// destruidos que quedaron en /nimos/pools/). Tras reconcileMountState,
	// así los pools válidos ya están montados y NO se confunden con huérfanos.
	cleanOrphanPoolDirs()

	// STOR-06: consumir el journal de wipe al arrancar. Si un wipe se
	// interrumpió por un crash, lo reporta y limpia (el wipe es re-ejecutable).
	journalRecoverOnBoot()

	// STOR-01-A: detectar drift de layout (BD vs realidad BTRFS) tras un crash
	// durante una op de layout. Solo detecta y marca el pool en recovery; no
	// toca el layout. Requiere pools montados → va tras reconcileMountState.
	if ld, err := detectLayoutDrift(context.Background()); err != nil {
		logMsg("startup: detectLayoutDrift error: %v", err)
	} else if ld.Drifted > 0 {
		logMsg("startup: %d pools con drift de layout marcados en recovery", ld.Drifted)
	}

	// THEN: Start monitoring (cleanOrphanMountPoints runs here, AFTER pools are mounted)
	startStorageMonitoring()
	// Beta 8: ZFS scheduler removed. BTRFS scrub scheduling is handled by
	// startScrubScheduler() in storage_btrfs_features.go.
}

// ═══════════════════════════════════
// Fase 4 · Services: schedulers y observers en background
// ═══════════════════════════════════

// bootServices arranca todos los schedulers de fondo. Requiere bootStorage
// (varios leen pools montados) y bootHTTP (NimShield protege ese servidor).
func bootServices() {
	// ── NORMA 1 de Docker · su data-root manda ─────────────────────────────
	// Antes de dejar que Docker opere: si su data-root apunta a un pool que NO
	// está montado, Docker escribiría en el disco de sistema (catastrófico en
	// Pi: llena la SD y tumba el SO). Lo detenemos hasta que el usuario corrija
	// el pool. NimHealth lo rearrancará cuando el pool vuelva (ver más abajo).
	if isDockerInstalledGo() {
		if !ensureDockerSafeOrStop() {
			logMsg("bootServices: Docker en pausa de seguridad — su pool no está montado. Se rearrancará cuando el pool esté disponible.")
		}
	}

	// Start backup scheduler
	startBackupScheduler()
	startAutoDiscovery()
	//startWGTunnel()
	// Remount remote NFS shares in background — don't block daemon startup
	// If a remote host is unreachable, NFS mount can take minutes to timeout
	go remountAllOnStartup()

	// NimHealth observer · Reconciler scheduler propio (no acoplado a
	// networkReconcilers · disciplina §1). Ver INTEGRATION-v1.2 §4.2.
	//
	// El scheduler arranca el observer en el primer tick del Interval()
	// (30s default). Para tener cache poblada antes, RunOnce() al boot en
	// goroutine, esperando a la dependencia REAL (D-003 resuelto): su
	// refreshDockerCache hace `docker ps`, así que esperamos a que dockerd
	// responda. Sin Docker instalado corre al instante (la pasada maneja la
	// ausencia con gracia).
	nimhealthScheduler = NewReconcilerScheduler(NewRealClock())
	nimhealthObserver := NewNimHealthObserver(NewRealClock(), DefaultNimHealthConfig())
	if err := nimhealthScheduler.Register(nimhealthObserver); err != nil {
		logMsg("nimhealth: register observer failed: %v", err)
	} else if err := nimhealthScheduler.Start(context.Background()); err != nil {
		logMsg("nimhealth: scheduler start failed: %v", err)
	}
	go func() {
		if isDockerInstalledGo() {
			waitForCondition(context.Background(), "nimhealth→dockerd", 60*time.Second, dockerDaemonReady)
		}
		if err := nimhealthScheduler.RunOnce(context.Background(), nimhealthObserver.Name()); err != nil {
			logMsg("nimhealth: initial RunOnce failed: %v", err)
		}
	}()

	// Docker app reconciler (Beta 8.2 · Fase 3) · red de seguridad contra
	// inconsistencias BD↔Docker (bug Nextcloud). Detecta containers con label
	// com.nimos.managed=true sin row en docker_apps y los reimporta.
	//
	// Scheduler propio (mismo patrón que NimHealth). RunOnce al arranque
	// esperando a que dockerd responda (D-003 resuelto) · así un huérfano de
	// la sesión anterior se rescata nada más arrancar, con Docker YA listo.
	dockerAppScheduler = NewReconcilerScheduler(NewRealClock())
	dockerAppReconciler := NewDockerAppReconciler(appsRepo, NewRealClock())
	if err := dockerAppScheduler.Register(dockerAppReconciler); err != nil {
		logMsg("docker_reconciler: register failed: %v", err)
	} else if err := dockerAppScheduler.Start(context.Background()); err != nil {
		logMsg("docker_reconciler: scheduler start failed: %v", err)
	}
	go func() {
		if isDockerInstalledGo() {
			waitForCondition(context.Background(), "docker_reconciler→dockerd", 60*time.Second, dockerDaemonReady)
		}
		if err := dockerAppScheduler.RunOnce(context.Background(), dockerAppReconciler.Name()); err != nil {
			logMsg("docker_reconciler: initial RunOnce failed: %v", err)
		}
	}()

	// Start scrub scheduler — checks every 60s if a scheduled verification is due
	go startScrubScheduler()

	// Start SMART monitor — checks disk health every 30 min, alerts on changes
	go startSmartMonitor()

	// Start config backup — saves NimOS config to each pool every 30 min
	// This enables pool restore after system disk failure + NimOS reinstall
	go startConfigBackupLoop()

	// Start NimShield security engine — honeypots, rules, blocklist
	go startShieldEngine()
	go startShieldCleanup()
}
