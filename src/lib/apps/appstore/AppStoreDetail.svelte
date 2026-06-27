<script>
  /**
   * AppStoreDetail · Vista detalle de una app del catálogo · Fase 7
   * ─────────────────────────────────────────────────────────────────
   * Diseño tipo "store profesional" inspirado en Apple AppStore:
   *
   *   ┌─────────────────────────────────────────────────────────┐
   *   │ ← Volver al catálogo                                    │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  ┌────┐                                                 │
   *   │  │icon│   Jellyfin                  [Instalar/Abrir]   │
   *   │  │    │   Multimedia                                   │
   *   │  └────┘   :8096 · Docker · Multi-servicio              │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  [InstallFlow embedded · solo durante install]          │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  Capturas                                               │
   *   │  [screenshot1] [screenshot2] [screenshot3]              │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  Acerca de                                              │
   *   │  Servidor multimedia gratuito y open source...          │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  Información técnica                                    │
   *   │  Imagen Docker:  jellyfin/jellyfin:latest               │
   *   │  Puerto:         :8096                                  │
   *   │  Servicios:      jellyfin (1)                           │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  Credenciales por defecto (si aplica)                   │
   *   │  Usuario:  admin        [copiar]                        │
   *   │  Pass:     ••••••••     [ojo] [copiar]                  │
   *   ├─────────────────────────────────────────────────────────┤
   *   │  [Desinstalar] (solo si instalada · sutil, abajo)       │
   *   └─────────────────────────────────────────────────────────┘
   *
   * Arquitectura de responsabilidades:
   *   AppStore Detail   · descubrir, instalar, desinstalar, credenciales
   *   NimHealth Task Mgr · ciclo de vida runtime (start/stop/logs/métricas)
   *
   * Por eso NO incluye botones Detener/Iniciar. El user gestiona el runtime
   * en NimHealth. AppStore mantiene "Abrir" porque es acto de consumo del
   * app, no de gestión.
   */

  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { openWindow } from '$lib/stores/windows.js';
  import ConfirmDialog from '$lib/ui/ConfirmDialog.svelte';
  import { fetchCatalog } from './catalog.js';
  import { getInstalledApps, uninstallApp, checkAppUpdates, updateApp, checkAppBroken, repairApp } from './api.js';
  import { fetchLaunchable, normalizeLaunchable, openApp } from './launchApp.js';
  import InstallFlow from './InstallFlow.svelte';
  import {
    composeAppViews,
    formatStatus,
    formatHealth,
    statusTone,
    formatPort,
    categoryDisplayName,
    extractComposeServices,
  } from './formatters.js';

  /** @typedef {import('./types').AppView} AppView */
  /** @typedef {import('./types').Catalog} Catalog */

  /** ID de la app a mostrar */
  export let appId = '';

  const dispatch = createEventDispatcher();

  // ── Estado ────────────────────────────────────────────────────────
  /** @type {Catalog | null} */
  let catalog = null;
  /** @type {AppView | null} */
  let view = null;
  let launchInfo = null; // datos de /api/apps/launchable para abrir esta app
  // Estado "roto": contenedor que existe pero no arranca (capa/imagen perdida).
  let brokenInfo = null; // { broken: bool, reason: string } | null
  let repairProcessing = false;
  let repairError = '';
  let loading = true;
  let loadError = '';
  let iconError = false;
  let lightboxIdx = null; // índice (en loadedList) de la captura ampliada; null = cerrado

  // Capturas realmente cargadas, en orden · sobre las que navega el lightbox.
  $: loadedList = visibleShots.filter((n) => loadedShots.has(n));

  function openLightbox(n) { lightboxIdx = loadedList.indexOf(n); }
  function closeLightbox() { lightboxIdx = null; }
  function prevShot() {
    if (loadedList.length) lightboxIdx = (lightboxIdx - 1 + loadedList.length) % loadedList.length;
  }
  function nextShot() {
    if (loadedList.length) lightboxIdx = (lightboxIdx + 1) % loadedList.length;
  }

  /** Modo: 'detail' (normal) | 'installing' (con InstallFlow inline activo) */
  let mode = 'detail';

  // Credenciales · UI state
  let passwordVisible = false;
  let copyFeedback = ''; // mensaje breve tras copiar

  // Confirm dialog · uninstall
  let confirmUninstallOpen = false;
  let uninstallProcessing = false;
  let actionError = '';
  /** Modo de desinstalación seleccionado · 'soft' (default · preserva datos) | 'wipe' (destructivo) */
  let uninstallMode = 'soft';
  /**
   * Fase del flujo de desinstalación dentro del modal:
   *   'choice'  · pantalla inicial con las dos opciones (default)
   *   'running' · cleanup en progreso (poll a /api/services hasta que desaparezca)
   *   'done'    · cleanup completado, mostrar "Cerrar"
   *   'error'   · falló algo, mostrar mensaje
   */
  let uninstallPhase = 'choice';
  let uninstallErr = '';
  /** ID del setInterval del polling · permite cancelarlo si user cierra */
  let uninstallPollId = null;

  // ─── Sprint Updates · estado del update ──────────────────────────
  /** ¿La app tiene update disponible? Se calcula al cargar el detail. */
  let hasUpdate = false;
  /** Detalles por servicio · solo informativo, lo recogemos por si añadimos UI granular. */
  /** @type {Array<{name: string, image: string, updateAvailable: boolean}>} */
  let updateServices = [];
  /** Modal de update · fases similar a uninstall */
  let updateDialogOpen = false;
  /**
   * 'confirm'  · pantalla inicial con explicación + botón Actualizar
   * 'running'  · compose pull + up -d corriendo (típicamente 30s-2min)
   * 'done'     · update OK · botón Cerrar
   * 'error'    · falló · mensaje + botón Cerrar
   */
  let updatePhase = 'confirm';
  let updateErr = '';

  // Screenshots · intentamos cargar 1..6, oculta automáticamente las que fallan.
  // El repo del catálogo guarda screenshots en /screenshots/{appId}/N.{ext}
  // Soportamos varias extensiones (png, webp, jpg, jpeg): para cada slot se
  // prueban en orden hasta que una carga · así se pueden mezclar formatos y
  // usar webp (más ligero) sin obligar a convertir todo a png.
  /** @type {number[]} */
  let visibleShots = [1, 2, 3, 4, 5, 6];
  /** Extensiones a probar por slot, en orden de preferencia. */
  const shotExtensions = ['png', 'webp', 'jpg', 'jpeg'];
  /** @type {Set<number>} · slots que han agotado TODAS las extensiones sin cargar */
  let failedShots = new Set();
  /** @type {Set<number>} · solo las imágenes que se han cargado satisfactoriamente */
  let loadedShots = new Set();
  /** @type {Map<number, number>} · slot → índice de extensión que se está probando ahora */
  let shotExtIdx = new Map();
  /** @type {Map<number, string>} · slot → extensión que cargó OK (para el carrusel) */
  let shotExt = new Map();

  // URL del probe de un slot · usa la extensión que toca probar ahora.
  function shotProbeUrl(n) {
    const idx = shotExtIdx.get(n) ?? 0;
    return `${screenshotBaseUrl}/${n}.${shotExtensions[idx]}`;
  }
  // URL final de un slot ya cargado · usa la extensión que funcionó.
  function shotUrl(n) {
    return `${screenshotBaseUrl}/${n}.${shotExt.get(n) ?? 'png'}`;
  }

  // ── Lifecycle ──────────────────────────────────────────────────────
  onMount(load);
  onDestroy(stopUninstallPolling);

  $: if (appId) load();

  async function load() {
    loading = true;
    loadError = '';
    iconError = false;
    passwordVisible = false;
    copyFeedback = '';
    visibleShots = [1, 2, 3, 4, 5, 6];
    failedShots = new Set();
    loadedShots = new Set();
    shotExtIdx = new Map();
    shotExt = new Map();
    // Sprint Updates · reset
    hasUpdate = false;
    updateServices = [];
    // Estado "roto" · reset
    brokenInfo = null;
    repairError = '';

    try {
      const [cat, installed] = await Promise.all([
        fetchCatalog(),
        getInstalledApps().catch(() => []),
      ]);
      catalog = cat;
      const entry = cat.apps[appId];
      if (!entry) {
        throw new Error(`App "${appId}" no encontrada en el catálogo.`);
      }
      const views = composeAppViews([{ id: appId, app: entry }], installed);
      view = views[0];

      // Cargar datos de apertura (launchable) si la app está instalada · para
      // que el botón Abrir use la URL correcta (local vs dominio + landing_path).
      launchInfo = null;
      if (view?.installed) {
        const list = await fetchLaunchable();
        const dto = list.find((a) => a.id === appId);
        if (dto) {
          launchInfo = normalizeLaunchable(dto);
        }
      }

      // Sprint Updates · si la app está instalada y es un stack, comprobar
      // si tiene update disponible. Solo lee BD (cache 6h), no llama al
      // registry · es muy rápido (<100ms). El user no nota nada.
      if (view?.installed) {
        try {
          const res = await checkAppUpdates(appId);
          hasUpdate = res.updateAvailable;
          updateServices = res.services;
        } catch (err) {
          // Si falla, no rompe el detail · solo no aparece el botón Actualizar
          console.warn('[appstore/detail] checkAppUpdates failed:', err);
        }
      }

      // ¿El contenedor está ROTO (existe pero no arranca)? Solo tiene sentido
      // mirarlo si NO está corriendo. checkAppBroken es tolerante a fallos.
      if (view?.installed && view.status !== 'running') {
        brokenInfo = await checkAppBroken(appId);
      }
    } catch (err) {
      loadError = err?.message || String(err);
      view = null;
    } finally {
      loading = false;
    }
  }

  // ── Derived ────────────────────────────────────────────────────────
  $: tone = view ? statusTone(view.status, view.health) : 'muted';
  $: services = view?.catalog?.compose ? extractComposeServices(view.catalog.compose) : [];
  $: isMultiService = services.length > 1;
  $: categoryLabel = view ? categoryDisplayName(view.category, catalog?.categories || {}) : '';

  // Credentials con resolveEnvRef
  $: credentials = (() => {
    const c = view?.catalog?.credentials;
    if (!c) return null;
    const env = view?.catalog?.env || {};
    return {
      username: c.username || null,
      password: c.password
        ? c.password
        : c.passwordKey
          ? (env[c.passwordKey] || `\${${c.passwordKey}}`)
          : null,
      passwordIsTemplate: !c.password && !!c.passwordKey,
    };
  })();

  // URL base de screenshots · catálogo público
  $: screenshotBaseUrl = `https://raw.githubusercontent.com/andresgv-beep/NimOs-appstore/main/screenshots/${appId}`;

  // Hay app instalada Y corriendo (para botón "Abrir" funcional)
  $: canOpen = view?.installed && view.status === 'running';
  // Contenedor ROTO: existe pero no arranca (capa/imagen perdida) → "Reparar".
  $: needsRepair = !!(view?.installed && brokenInfo?.broken);
  // Hay app instalada pero parada (botón "Abrir" deshabilitado + nota). Excluye
  // el caso "roto", que tiene su propio botón "Reparar".
  $: isStopped = view?.installed && view.status !== 'running' && !needsRepair;

  // Puerto del badge superior · si la app está instalada, el EFECTIVO (registrado
  // por el Port Allocator, que puede diferir del default del catálogo cuando hubo
  // reasignación de puerto). Si no está instalada, el del catálogo (el que usaría).
  // El campo "Puerto" de info técnica sigue mostrando el default del contenedor.
  $: badgePort =
    (view?.installed ? (launchInfo?.localPort || view?.runtime?.ports?.[0]?.host) : 0) ||
    view?.catalog?.port;

  // ── Acciones ───────────────────────────────────────────────────────
  function handleBack() {
    dispatch('back');
  }

  function handleInstall() {
    if (!view) return;
    mode = 'installing';
  }

  async function handleInstallDone() {
    mode = 'detail';
    await new Promise((r) => setTimeout(r, 600));
    await load();
    if (view && !view.installed) {
      await new Promise((r) => setTimeout(r, 1000));
      await load();
    }
  }

  function handleInstallCancel() {
    mode = 'detail';
  }

  // Reparar contenedor roto · recrea desde compose (re-pull + recreate) y recarga.
  // Puede tardar minutos (descarga de imagen). La config de la app se conserva.
  async function handleRepair() {
    if (!view?.installed || repairProcessing) return;
    repairProcessing = true;
    repairError = '';
    try {
      await repairApp(appId);
      // Recargar para reflejar el nuevo estado (debería pasar a 'running').
      await load();
    } catch (err) {
      repairError = err?.message || String(err);
    } finally {
      repairProcessing = false;
    }
  }

  function handleOpen() {
    if (!view?.installed) return;
    // launchInfo viene de /api/apps/launchable (cargado en load()). Tiene
    // open_url_external (compuesto por el backend) + local_port + landing_path.
    // openApp decide local vs dominio según cómo entró el usuario · misma
    // lógica que el Launcher (módulo compartido launchApp.js · sin iframe).
    if (launchInfo) {
      openApp(launchInfo);
      return;
    }
    // Fallback · si no hay launchInfo (app sin puerto o sin datos), avisar.
    const port = view.runtime?.ports?.[0]?.host || view.catalog?.port;
    if (!port) {
      actionError = 'La app no tiene puerto expuesto · no se puede abrir.';
      return;
    }
    const host = window.location.hostname;
    const proto = window.location.protocol;
    const landingPath = view.catalog?.landingPath || '';
    window.open(`${proto}//${host}:${port}${landingPath}`, '_blank');
  }

  function handleUninstallClick() {
    if (!view?.installed || uninstallProcessing) return;
    uninstallMode = 'soft'; // siempre arrancar en modo seguro
    uninstallPhase = 'choice';
    uninstallErr = '';
    confirmUninstallOpen = true;
  }

  async function handleUninstallConfirm() {
    if (!view?.installed) return;
    if (uninstallPhase === 'done') {
      // Si ya terminó · al pulsar "Cerrar" volvemos al catálogo
      stopUninstallPolling();
      confirmUninstallOpen = false;
      dispatch('back');
      return;
    }
    if (uninstallPhase === 'error') {
      // Cerrar tras error · queda en el detail
      stopUninstallPolling();
      confirmUninstallOpen = false;
      return;
    }
    if (uninstallPhase !== 'choice') return;

    uninstallProcessing = true;
    uninstallPhase = 'running';
    uninstallErr = '';
    try {
      await uninstallApp(view.id, 'stack', { wipe: uninstallMode === 'wipe' });
      // Backend respondió OK · el cleanup corre en background. Hacemos polling
      // a getInstalledApps hasta que la app deje de aparecer (cleanup completado).
      startUninstallPolling();
    } catch (err) {
      uninstallPhase = 'error';
      uninstallErr = err?.message || String(err);
      uninstallProcessing = false;
    }
  }

  function handleUninstallCancel() {
    if (uninstallProcessing && uninstallPhase === 'running') {
      // Si ya está corriendo, no permitir cancelar · solo en choice/done/error
      return;
    }
    stopUninstallPolling();
    confirmUninstallOpen = false;
    // Si estaba en estado 'done' al cerrar, volvemos al catálogo (la app ya
    // no existe, no tiene sentido seguir en el detail)
    if (uninstallPhase === 'done') {
      dispatch('back');
    }
  }

  /**
   * Polling a getInstalledApps cada segundo hasta que la app deje de aparecer.
   * Cuando desaparece → cleanup completado → fase 'done'.
   * Timeout de seguridad a los 30s · si la app sigue presente, mostramos error.
   */
  function startUninstallPolling() {
    if (!view) return;
    const targetId = view.id;
    const startTime = Date.now();
    const TIMEOUT_MS = 30000;

    uninstallPollId = setInterval(async () => {
      try {
        const installed = await getInstalledApps();
        const stillThere = installed.some((a) => a?.id === targetId);
        if (!stillThere) {
          // Desapareció · cleanup terminado
          stopUninstallPolling();
          uninstallPhase = 'done';
          uninstallProcessing = false;
          return;
        }
        // Sigue ahí · verificar timeout
        if (Date.now() - startTime > TIMEOUT_MS) {
          stopUninstallPolling();
          uninstallPhase = 'error';
          uninstallErr = 'El proceso está tardando más de lo esperado. Revisa los logs del daemon.';
          uninstallProcessing = false;
        }
      } catch (err) {
        // Error en el polling · no abortamos, reintentamos en el siguiente tick
        // (los errores transitorios de red no deben romper el flujo)
      }
    }, 1000);
  }

  function stopUninstallPolling() {
    if (uninstallPollId !== null) {
      clearInterval(uninstallPollId);
      uninstallPollId = null;
    }
  }

  // ─── Sprint Updates · flujo de actualización ────────────────────

  function handleUpdateClick() {
    if (!view?.installed || !hasUpdate) return;
    updatePhase = 'confirm';
    updateErr = '';
    updateDialogOpen = true;
  }

  /**
   * Confirma el update · llama al endpoint POST /api/docker/app/<id>/update
   * que ejecuta `docker compose pull && up -d`. Síncrono (típicamente 30s-2min).
   *
   * Tras éxito:
   *   - Recargamos el detail (load) para refrescar el estado · digests locales
   *     ahora coinciden con remotos, hasUpdate vuelve a false.
   *   - El botón "Actualizar" desaparece del hero, el badge sidebar también.
   */
  async function handleUpdateConfirm() {
    if (!view?.installed) return;
    if (updatePhase === 'done') {
      // Botón "Cerrar" tras éxito · refresh + cerrar modal
      updateDialogOpen = false;
      // Recargar el detail para que el estado refleje "ya actualizada"
      await load();
      return;
    }
    if (updatePhase === 'error') {
      updateDialogOpen = false;
      return;
    }
    if (updatePhase !== 'confirm') return;

    updatePhase = 'running';
    updateErr = '';
    try {
      await updateApp(view.id);
      updatePhase = 'done';
    } catch (err) {
      updatePhase = 'error';
      updateErr = err?.message || String(err);
    }
  }

  function handleUpdateCancel() {
    if (updatePhase === 'running') return; // no cancelable mid-update
    updateDialogOpen = false;
    if (updatePhase === 'done') {
      // Si cierra por X tras done · igual recargamos para reflejar estado
      load();
    }
  }

  // Credentials · copiar al clipboard
  async function copyToClipboard(value, label) {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      copyFeedback = `${label} copiado`;
      setTimeout(() => { copyFeedback = ''; }, 1500);
    } catch (err) {
      copyFeedback = 'Error al copiar';
      setTimeout(() => { copyFeedback = ''; }, 2000);
    }
  }

  // Screenshots · una extensión falló para este slot. Probamos la siguiente;
  // si ya no quedan, el slot se marca como fallido definitivamente.
  function handleShotError(n) {
    const idx = shotExtIdx.get(n) ?? 0;
    if (idx + 1 < shotExtensions.length) {
      shotExtIdx.set(n, idx + 1);
      shotExtIdx = shotExtIdx; // forzar reactividad → reintenta con la sig. ext
    } else {
      failedShots.add(n);
      failedShots = failedShots;
    }
  }

  // Screenshots · marca un slot como cargado y recuerda con qué extensión.
  function handleShotLoad(n) {
    const idx = shotExtIdx.get(n) ?? 0;
    shotExt.set(n, shotExtensions[idx]);
    loadedShots.add(n);
    loadedShots = loadedShots;
  }

  // ¿Hay al menos un screenshot que ha cargado?
  $: hasAnyScreenshot = visibleShots.some((n) => loadedShots.has(n));
</script>

{#if loading}
  <div class="detail-state">
    <div class="loading-dot"></div>
    <div class="state-text">Cargando detalle…</div>
  </div>
{:else if loadError}
  <div class="detail-state">
    <div class="err-title">No se pudo cargar el detalle</div>
    <div class="err-body">{loadError}</div>
    <button class="btn btn-secondary" on:click={handleBack}>← Volver</button>
  </div>
{:else if view}
  <div class="detail-wrap">
    <!-- Barra superior · back + título · fila fija FUERA del scroll -->
    <div class="detail-bar">
      <button class="back-btn" on:click={handleBack} type="button">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="15 18 9 12 15 6" />
        </svg>
        Volver al catálogo
      </button>
      <span class="detail-bar-title">{view.name}</span>
    </div>

    <div class="detail-scroll">
    <!-- HERO horizontal -->
    <section class="hero">
      <div class="hero-icon" style={view.color ? `background: ${view.color}; box-shadow: 0 0 32px ${view.color}33` : ''}>
        {#if !iconError && view.icon}
          <img src={view.icon} alt={view.name} on:error={() => (iconError = true)} />
        {:else}
          <span class="hero-icon-fallback">{view.name.charAt(0).toUpperCase()}</span>
        {/if}
      </div>

      <div class="hero-info">
        <h1 class="hero-name">{view.name}</h1>
        <div class="hero-cat">
          {categoryLabel}
          {#if view.catalog?.official} · <span class="badge-official">Oficial</span>{/if}
        </div>
        <div class="hero-tags">
          {#if badgePort}
            <span class="tag tag-port">{formatPort(badgePort)}</span>
          {/if}
          <span class="tag">Docker</span>
          {#if isMultiService}
            <span class="tag">Multi-servicio</span>
          {/if}
          {#if view.installed}
            <span class="tag tag-status" class:ok={tone === 'ok'} class:warn={tone === 'warn'} class:crit={tone === 'crit'}>
              <span class="status-dot"></span>
              {formatStatus(view.status)}
              {#if view.health && view.health !== 'unknown'} · {formatHealth(view.health)}{/if}
            </span>
          {/if}
        </div>
      </div>

      <div class="hero-action">
        {#if mode === 'installing'}
          <button class="btn btn-primary" disabled>
            <span class="spinner"></span>
            Instalando…
          </button>
        {:else if !view.installed}
          <button class="btn btn-primary" on:click={handleInstall}>
            Instalar {view.name}
          </button>
        {:else}
          <!-- Instalada · botón "Actualizar" si hay update + botón Abrir -->
          {#if hasUpdate}
            <button class="btn btn-update" on:click={handleUpdateClick} title="Actualizar a la última versión">
              <svg viewBox="0 0 1024 1024" fill="currentColor" width="14" height="14" aria-hidden="true">
                <path d="M512 1024C229.23 1024 0 794.77 0 512S229.23 0 512 0s512 229.23 512 512-229.23 512-512 512zm95.731-219.947c62.403-20.276 114.032-58.693 150.859-107.357a15.793 15.793 0 004.801-13.823 15.302 15.302 0 00-.062-.473l-.008-.039a15.75 15.75 0 00-8.378-11.488l-44.709-32.893a15.837 15.837 0 00-13.75-4.587c-.124.014-.249.029-.373.046l-.058.014a15.88 15.88 0 00-11.478 8.236c-25.776 33.881-61.624 60.563-105.31 74.758-113.432 36.856-234.791-24.722-271.525-137.777s25.253-234.206 138.685-271.062c106.335-34.55 218.904 15.82 262.966 115.175l-71.623-.803c-7.187-1.066-14.189 2.885-16.982 9.581a15.743 15.743 0 004.748 18.601l120.91 126.41c2.999 3.135 7.161 4.899 11.51 4.879s8.502-1.823 11.485-4.986L890.37 448.206c5.265-4.193 7.303-11.242 5.082-17.573a20.516 20.516 0 00-.119-.318 15.889 15.889 0 00-.947-2.099l-.031-.073c-3.116-5.729-9.448-8.951-15.937-8.108l-71.678-.494-.892-2.744C753.5 255.685 579.61 167.45 417.952 219.976S167.478 446.095 219.826 607.207c52.348 161.112 226.238 249.347 387.896 196.821l.008.027z"/>
              </svg>
              Actualizar
            </button>
          {/if}
          {#if canOpen}
            <button class="btn btn-primary" on:click={handleOpen}>
              Abrir {view.name}
            </button>
          {:else if needsRepair}
            <button class="btn btn-primary" on:click={handleRepair} disabled={repairProcessing}
                    title="Recrea el contenedor reusando su configuración">
              {#if repairProcessing}
                <span class="spinner"></span>
                Reparando…
              {:else}
                Reparar contenedor
              {/if}
            </button>
          {:else if isStopped}
            <button class="btn btn-primary" disabled title="Inicia el contenedor desde NimHealth">
              Abrir {view.name}
            </button>
          {/if}
        {/if}
      </div>
    </section>

    {#if isStopped}
      <div class="hint-row">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        La app está detenida. Inicia el contenedor desde <strong>NimHealth → Task Manager</strong>.
      </div>
    {/if}

    {#if needsRepair}
      <div class="hint-row">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        El contenedor está dañado{#if brokenInfo?.reason} · {brokenInfo.reason}{/if}. Pulsa <strong>Reparar contenedor</strong> para recrearlo conservando tu configuración.
      </div>
    {/if}

    {#if repairError}
      <div class="error-row">No se pudo reparar: {repairError}</div>
    {/if}

    {#if actionError}
      <div class="error-row">{actionError}</div>
    {/if}

    <!-- INSTALL FLOW (embedded inline · NO sustituye el hero) -->
    {#if mode === 'installing'}
      <section class="install-section">
        <InstallFlow view={view} on:done={handleInstallDone} on:cancel={handleInstallCancel} embedded={true} />
      </section>
    {/if}

    <!-- Preload silencioso · imágenes invisibles fuera del DOM visible para
         disparar load/error sin parpadeo. Cuando una carga OK, se renderiza
         en el carrusel visible de abajo. -->
    <div class="shot-probes" aria-hidden="true">
      {#each visibleShots as n (n)}
        {#if !loadedShots.has(n) && !failedShots.has(n)}
          {#key shotExtIdx.get(n) ?? 0}
            <img
              src={shotProbeUrl(n)}
              alt=""
              on:load={() => handleShotLoad(n)}
              on:error={() => handleShotError(n)}
            />
          {/key}
        {/if}
      {/each}
    </div>

    <!-- SCREENSHOTS · carrusel horizontal con scroll snap -->
    {#if hasAnyScreenshot}
      <section class="section">
        <h2 class="section-title">Capturas</h2>
        <div class="screenshots">
          {#each visibleShots as n (n)}
            {#if loadedShots.has(n)}
              <div
                class="shot"
                role="button"
                tabindex="0"
                title="Ampliar"
                on:click={() => openLightbox(n)}
                on:keydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openLightbox(n); } }}
              >
                <img
                  src={shotUrl(n)}
                  alt="{view.name} captura {n}"
                  loading="lazy"
                />
              </div>
            {/if}
          {/each}
        </div>
      </section>
    {/if}

    <!-- DESCRIPCIÓN -->
    {#if view.description}
      <section class="section">
        <h2 class="section-title">Acerca de</h2>
        <p class="description">{view.description}</p>
      </section>
    {/if}

    <!-- INFO TÉCNICA · cards grid 2 columnas estilo mockup -->
    <section class="section">
      <h2 class="section-title">Información técnica</h2>
      <div class="info-grid">
        <!-- Items pequeños primero (llenan parejas naturales) -->
        <div class="info-card">
          <span class="info-card-k">Imagen Docker</span>
          <code class="info-card-v">{view.catalog?.image || '—'}</code>
        </div>
        <div class="info-card">
          <span class="info-card-k">Puerto</span>
          <code class="info-card-v">{view.catalog?.port ? formatPort(view.catalog.port) : '—'}</code>
        </div>
        {#if view.catalog?.openMode}
          <div class="info-card">
            <span class="info-card-k">Modo de apertura</span>
            <code class="info-card-v">{view.catalog.openMode}</code>
          </div>
        {/if}
        {#if services.length === 1}
          <div class="info-card">
            <span class="info-card-k">Servicio</span>
            <code class="info-card-v">{services[0]}</code>
          </div>
        {/if}
        {#if view.installed && view.runtime?.containerName}
          <div class="info-card">
            <span class="info-card-k">Container</span>
            <code class="info-card-v">{view.runtime.containerName}</code>
          </div>
        {/if}

        <!-- Items wide al final (ocupan ambas columnas) -->
        {#if services.length > 1}
          <div class="info-card info-card-wide">
            <span class="info-card-k">Servicios ({services.length})</span>
            <div class="info-card-chips">
              {#each services as svc}
                <code class="service-chip">{svc}</code>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    </section>

    <!-- CREDENCIALES por defecto · solo apps SIN config (las que tienen
         configRef piden el password en el modal de instalación · no hay
         "credencial por defecto" que mostrar · sería engañoso). -->
    {#if credentials && (credentials.username || credentials.password) && !view?.catalog?.configRef}
      <section class="section">
        <h2 class="section-title">Credenciales por defecto</h2>
        <div class="creds-block">
          {#if credentials.username}
            <div class="cred-row">
              <span class="cred-k">Usuario</span>
              <code class="cred-v">{credentials.username}</code>
              <button class="cred-btn" on:click={() => copyToClipboard(credentials.username, 'Usuario')} title="Copiar usuario" type="button">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
                </svg>
              </button>
            </div>
          {/if}
          {#if credentials.password}
            <div class="cred-row">
              <span class="cred-k">Contraseña</span>
              <code class="cred-v" class:masked={!passwordVisible}>
                {#if passwordVisible}
                  {credentials.password}
                {:else}
                  {'•'.repeat(Math.min(credentials.password.length, 12))}
                {/if}
              </code>
              <button class="cred-btn" on:click={() => passwordVisible = !passwordVisible} title={passwordVisible ? 'Ocultar' : 'Mostrar'} type="button">
                {#if passwordVisible}
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/>
                    <line x1="1" y1="1" x2="23" y2="23"/>
                  </svg>
                {:else}
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
                    <circle cx="12" cy="12" r="3"/>
                  </svg>
                {/if}
              </button>
              <button class="cred-btn" on:click={() => copyToClipboard(credentials.password, 'Contraseña')} title="Copiar contraseña" type="button">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
                </svg>
              </button>
              {#if credentials.passwordIsTemplate}
                <span class="cred-note">se genera al instalar</span>
              {/if}
            </div>
          {/if}
          <div class="creds-hint">
            Cambia la contraseña tras el primer inicio de sesión por seguridad.
          </div>
        </div>
        {#if copyFeedback}
          <div class="copy-feedback">{copyFeedback}</div>
        {/if}
      </section>
    {/if}

    <!-- DESINSTALAR · solo si instalada · sutil al final -->
    {#if view.installed && mode === 'detail'}
      <section class="section uninstall-section">
        <button class="btn btn-danger-soft" on:click={handleUninstallClick} disabled={uninstallProcessing} type="button">
          Desinstalar {view.name}
        </button>
        <p class="uninstall-hint">
          Podrás elegir si conservar los datos o eliminarlos por completo.
        </p>
      </section>
    {/if}
  </div>

    {#if lightboxIdx !== null}
      <div
        class="lightbox"
        role="button"
        tabindex="-1"
        on:click={(e) => { if (e.target === e.currentTarget) closeLightbox(); }}
        on:keydown={(e) => { if (e.key === 'Escape' || e.key === 'Enter') closeLightbox(); }}
      >
        {#if loadedList.length > 1}
          <button class="lb-nav lb-prev" on:click={prevShot} aria-label="Captura anterior" type="button">‹</button>
        {/if}
        <img class="lightbox-img" src={shotUrl(loadedList[lightboxIdx])} alt="Captura ampliada" />
        {#if loadedList.length > 1}
          <button class="lb-nav lb-next" on:click={nextShot} aria-label="Captura siguiente" type="button">›</button>
        {/if}
        <span class="lightbox-close" aria-hidden="true">×</span>
      </div>
    {/if}
  </div>
{/if}

<svelte:window on:keydown={(e) => {
  if (lightboxIdx === null) return;
  if (e.key === 'Escape') closeLightbox();
  else if (e.key === 'ArrowLeft') prevShot();
  else if (e.key === 'ArrowRight') nextShot();
}} />

<!-- Confirm dialog uninstall · multi-fase con feedback en tiempo real -->
{#if view}
  <ConfirmDialog
    bind:open={confirmUninstallOpen}
    title="Desinstalar {view.name}"
    message={uninstallPhase === 'choice' ? 'Elige cómo desinstalar:' : ''}
    confirmLabel={
      uninstallPhase === 'choice'
        ? (uninstallMode === 'wipe' ? 'Desinstalar y borrar' : 'Desinstalar')
        : uninstallPhase === 'done'
          ? 'Cerrar'
          : uninstallPhase === 'error'
            ? 'Cerrar'
            : ''
    }
    cancelLabel={uninstallPhase === 'choice' ? 'Cancelar' : ''}
    variant={uninstallPhase === 'error' ? 'danger' : (uninstallPhase === 'done' ? 'default' : 'danger')}
    processing={uninstallPhase === 'running'}
    on:confirm={handleUninstallConfirm}
    on:cancel={handleUninstallCancel}
  >
    {#if uninstallPhase === 'choice'}
      <div class="uninstall-modes">
        <label class="mode-option" class:selected={uninstallMode === 'soft'}>
          <input type="radio" name="uninstall-mode" value="soft" bind:group={uninstallMode} />
          <div class="mode-content">
            <div class="mode-title">
              Desinstalar <span class="mode-badge mode-badge-ok">recomendado</span>
            </div>
            <div class="mode-desc">
              El contenedor se elimina. Los datos (configuración, biblioteca, BD)
              se conservan. Si reinstalas más tarde, todo vuelve donde estaba.
            </div>
          </div>
        </label>

        <label class="mode-option" class:selected={uninstallMode === 'wipe'}>
          <input type="radio" name="uninstall-mode" value="wipe" bind:group={uninstallMode} />
          <div class="mode-content">
            <div class="mode-title mode-title-danger">
              Desinstalar y borrar todos los datos
            </div>
            <div class="mode-desc">
              Eliminación completa · <strong>NO se puede deshacer</strong>.
              Pierdes biblioteca, configuración y base de datos.
            </div>
          </div>
        </label>
      </div>
    {:else if uninstallPhase === 'running'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-active"></span>
          <span class="uninstall-step-label">
            {uninstallMode === 'wipe' ? 'Eliminando contenedor y datos…' : 'Eliminando contenedor (datos preservados)…'}
          </span>
        </div>
        <div class="uninstall-hint-running">
          Esto puede tardar unos segundos. No cierres el modal.
        </div>
      </div>
    {:else if uninstallPhase === 'done'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-ok"></span>
          <span class="uninstall-step-label">
            {view.name} desinstalada{uninstallMode === 'wipe' ? ' · datos borrados' : ' · datos preservados'}
          </span>
        </div>
        {#if uninstallMode === 'soft'}
          <div class="uninstall-hint-running">
            Los datos siguen disponibles para una futura reinstalación.
          </div>
        {/if}
      </div>
    {:else if uninstallPhase === 'error'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-crit"></span>
          <span class="uninstall-step-label">No se pudo completar la desinstalación</span>
        </div>
        <div class="uninstall-err">{uninstallErr}</div>
      </div>
    {/if}
  </ConfirmDialog>
{/if}

<!-- Sprint Updates · modal de actualización · choice/running/done/error -->
{#if view}
  <ConfirmDialog
    bind:open={updateDialogOpen}
    title="Actualizar {view.name}"
    message={updatePhase === 'confirm' ? '' : ''}
    confirmLabel={
      updatePhase === 'confirm' ? 'Actualizar ahora'
      : updatePhase === 'done' ? 'Cerrar'
      : updatePhase === 'error' ? 'Cerrar'
      : ''
    }
    cancelLabel={updatePhase === 'confirm' ? 'Cancelar' : ''}
    variant={updatePhase === 'error' ? 'danger' : 'default'}
    processing={updatePhase === 'running'}
    on:confirm={handleUpdateConfirm}
    on:cancel={handleUpdateCancel}
  >
    {#if updatePhase === 'confirm'}
      <div class="update-confirm">
        <div class="update-confirm-row">
          <span class="update-confirm-k">App</span>
          <span class="update-confirm-v">{view.name}</span>
        </div>
        <div class="update-confirm-row">
          <span class="update-confirm-k">Servicios con update</span>
          <span class="update-confirm-v">
            {updateServices.filter((s) => s.updateAvailable).length} de {updateServices.length}
          </span>
        </div>
        <p class="update-confirm-hint">
          NimOS ejecutará <code>docker compose pull</code> y <code>up -d</code>.
          Los datos se conservan · solo se reemplazan los contenedores.
          Puede tardar de 30 segundos a 2 minutos.
        </p>
      </div>
    {:else if updatePhase === 'running'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-active"></span>
          <span class="uninstall-step-label">Descargando nueva versión y reiniciando…</span>
        </div>
        <div class="uninstall-hint-running">
          Apps grandes (Immich, Nextcloud) pueden tardar 5-15 min. No cierres el modal.
        </div>
      </div>
    {:else if updatePhase === 'done'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-ok"></span>
          <span class="uninstall-step-label">
            {view.name} actualizada correctamente
          </span>
        </div>
        <div class="uninstall-hint-running">
          La app está corriendo con la última versión disponible.
        </div>
      </div>
    {:else if updatePhase === 'error'}
      <div class="uninstall-progress">
        <div class="uninstall-step">
          <span class="uninstall-led led-crit"></span>
          <span class="uninstall-step-label">No se pudo completar la actualización</span>
        </div>
        <div class="uninstall-err">{updateErr}</div>
      </div>
    {/if}
  </ConfirmDialog>
{/if}

<style>
  /* ═══ Estados loading/error ═══ */
  .detail-state {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--sp-3);
    padding: var(--sp-5);
    text-align: center;
  }
  .loading-dot {
    width: 8px; height: 8px;
    border-radius: 50%;
    background: var(--signal);
    animation: pulse 1.4s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 0.3; transform: scale(0.9); }
    50%      { opacity: 1;   transform: scale(1.1); }
  }
  .state-text { color: var(--ink-mute); font-family: var(--font-mono); font-size: var(--fs-11); }
  .err-title { color: var(--crit); font-weight: 600; font-size: var(--fs-13); }
  .err-body { color: var(--ink-dim); font-family: var(--font-mono); font-size: var(--fs-11); max-width: 420px; word-break: break-word; }

  /* ═══ Scroll container ═══ */
  /* Wrapper · barra fija + scroll. Igual que AppShell: la barra vive FUERA
     del scroll para que la barra de scroll no la cruce. */
  .detail-wrap {
    height: 100%;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    position: relative;
  }
  .detail-scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0;
    width: 100%;
  }
  /* Barra superior · fila fija (no sticky). Sin z-index alto: debe quedar
     BAJO la .drag-zone de WindowFrame (z-index 5) para poder arrastrar la
     ventana desde la barra. */
  .detail-bar {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 24px;
    background: var(--canvas);
    border-bottom: 1px solid var(--line);
  }
  /* El back-btn se "perfora" por encima de la drag-zone para ser clickable;
     su SVG no captura el click (si no, lo come la drag-zone). El resto de la
     barra (vacío + título) queda bajo la drag-zone → arrastrar funciona. */
  .detail-bar .back-btn { position: relative; z-index: 6; }
  .detail-bar .back-btn svg,
  .detail-bar .back-btn svg * { pointer-events: none; }
  .detail-bar-title {
    font-size: var(--fs-14, 14px);
    font-weight: 600;
    color: var(--ink-mute);
  }
  /* Hijos del scroll: max 920px centrado · EXCEPTO el back-btn (que tiene su propio ancho natural) */
  /* La barra va a ancho completo (sticky); el resto del contenido
     centrado con padding lateral, ya que el scroll no lo aporta. */
  .detail-scroll > *:not(.detail-bar) {
    max-width: 920px;
    margin-left: auto;
    margin-right: auto;
    width: 100%;
    padding-left: var(--sp-5);
    padding-right: var(--sp-5);
  }
  .detail-scroll > .hero { padding-top: var(--sp-5); }
  /* Separación entre secciones */
  .detail-scroll > *:not(.detail-bar) + *:not(.detail-bar) {
    margin-top: var(--sp-5);
  }
  /* Aire al final del scroll */
  .detail-scroll::after {
    content: '';
    display: block;
    height: var(--sp-6);
  }

  /* ═══ Back button ═══ */
  .back-btn {
    background: var(--panel-elev);
    border: 1px solid var(--line);
    color: var(--ink-dim);
    cursor: pointer;
    font-size: var(--fs-12);
    font-family: inherit;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 14px;
    border-radius: var(--radius-sm);
    transition: background 0.12s, color 0.12s, border-color 0.12s;
    flex-shrink: 0;
  }
  .back-btn:hover {
    color: var(--ink);
    background: var(--line);
    border-color: var(--line-bright);
  }
  .back-btn svg { width: 13px; height: 13px; }

  /* ═══ HERO horizontal ═══ */
  .hero {
    display: flex;
    align-items: center;
    gap: var(--sp-4);
    padding: var(--sp-3) 0;
  }
  .hero-icon {
    width: 96px;
    height: 96px;
    border-radius: 22px;
    background: var(--canvas);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    overflow: hidden;
    position: relative;
    transition: box-shadow 0.3s;
  }
  .hero-icon img {
    width: 64px;
    height: 64px;
    object-fit: contain;
  }
  .hero-icon-fallback {
    color: var(--ink);
    font-size: 40px;
    font-weight: 700;
    font-family: var(--font-mono);
  }
  .hero-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .hero-name {
    font-size: var(--fs-22);
    font-weight: 600;
    color: var(--ink);
    margin: 0;
    letter-spacing: -0.4px;
    line-height: 1.1;
  }
  .hero-cat {
    font-size: var(--fs-12);
    color: var(--ink-mute);
  }
  .badge-official {
    color: var(--signal);
    font-family: var(--font-mono);
    font-size: var(--fs-10);
    letter-spacing: 0.5px;
    text-transform: uppercase;
  }
  .hero-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 4px;
  }
  .tag {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 2px 9px;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: 999px;
    font-size: var(--fs-10);
    color: var(--ink-dim);
    font-family: var(--font-mono);
    line-height: 1.5;
  }
  .tag-port {
    color: var(--info);
    border-color: var(--info);
    background: var(--info-dim, var(--panel-deep));
  }
  .tag-status .status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--ink-faint);
  }
  .tag-status.ok .status-dot { background: var(--signal); box-shadow: 0 0 4px var(--signal-glow); }
  .tag-status.warn .status-dot { background: var(--warn); box-shadow: 0 0 4px var(--warn-glow); }
  .tag-status.crit .status-dot { background: var(--crit); box-shadow: 0 0 4px var(--crit-glow); }

  .hero-action {
    flex-shrink: 0;
    display: flex;
    gap: 8px;
    align-items: center;
  }

  /* sprint Updates · botón "Actualizar" en azul · llama la atención sin gritar */
  .btn-update {
    background: var(--info);
    color: var(--canvas);
    border: 1px solid var(--info);
    padding: 9px 16px;
    border-radius: var(--radius-sm);
    font-size: var(--fs-12);
    font-weight: 600;
    font-family: inherit;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: filter 0.12s, transform 0.08s;
    box-shadow: 0 0 12px var(--info-glow, rgba(77, 184, 255, 0.3));
  }
  .btn-update:hover {
    filter: brightness(1.1);
  }
  .btn-update:active {
    transform: scale(0.97);
  }
  .btn-update svg {
    flex-shrink: 0;
  }

  /* ═══ Hint stop · NimHealth para gestionar ═══ */
  .hint-row {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: 10px 14px;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    color: var(--ink-dim);
    font-size: var(--fs-12);
  }
  .hint-row svg { width: 16px; height: 16px; color: var(--warn); flex-shrink: 0; }
  .hint-row strong { color: var(--ink); font-weight: 600; }

  /* ═══ Error row inline ═══ */
  .error-row {
    padding: 8px 12px;
    background: var(--crit-dim);
    border: 1px solid var(--crit-border);
    border-radius: var(--radius-sm);
    color: var(--ink);
    font-size: var(--fs-11);
    font-family: var(--font-mono);
    word-break: break-word;
  }

  /* ═══ Section common ═══ */
  .section {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
  }
  .section-title {
    font-size: var(--fs-13);
    font-weight: 600;
    color: var(--ink);
    margin: 0;
    text-transform: uppercase;
    letter-spacing: 0.7px;
    color: var(--ink-mute);
  }

  /* ═══ Install section (embedded) ═══ */
  .install-section {
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
    padding: var(--sp-4);
  }

  /* ═══ Screenshots · carrusel horizontal con scroll snap ═══ */
  .shot-probes {
    position: absolute;
    width: 0;
    height: 0;
    overflow: hidden;
    pointer-events: none;
    opacity: 0;
  }
  .screenshots {
    display: flex;
    gap: var(--sp-3);
    overflow-x: auto;
    overflow-y: hidden;
    scroll-snap-type: x mandatory;
    scroll-behavior: smooth;
    padding: 0 0 12px;
    /* Permite "salir" del padding del scroll a izquierda/derecha al hacer scroll */
    margin: 0 calc(var(--sp-5) * -1);
    padding-left: var(--sp-5);
    padding-right: var(--sp-5);
    scrollbar-width: thin;
    scrollbar-color: var(--line-bright) transparent;
  }
  .screenshots::-webkit-scrollbar { height: 8px; }
  .screenshots::-webkit-scrollbar-track {
    background: transparent;
    border-radius: 4px;
  }
  .screenshots::-webkit-scrollbar-thumb {
    background: var(--line-bright);
    border-radius: 4px;
  }
  .screenshots::-webkit-scrollbar-thumb:hover {
    background: var(--ink-faint);
  }
  .shot {
    flex: 0 0 auto;
    width: 340px;
    aspect-ratio: 16 / 10;
    border-radius: var(--radius-md);
    overflow: hidden;
    background: var(--canvas);
    border: 1px solid var(--line);
    cursor: zoom-in;
    transition: transform 0.15s, border-color 0.15s;
    scroll-snap-align: start;
  }
  .shot:hover {
    transform: translateY(-2px);
    border-color: var(--line-bright);
  }
  .shot img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  /* ═══ Lightbox · captura ampliada (cubre solo la ventana) ═══ */
  .lightbox {
    position: absolute;
    inset: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--sp-5);
    background: rgba(0, 0, 0, 0.82);
    cursor: zoom-out;
  }
  .lightbox-img {
    max-width: 94%;
    max-height: 94%;
    object-fit: contain;
    border-radius: var(--radius-md);
    box-shadow: 0 16px 56px rgba(0, 0, 0, 0.6);
  }
  .lightbox-close {
    position: absolute;
    top: 12px;
    left: 18px;
    font-size: 30px;
    line-height: 1;
    color: var(--ink, #f2f2f5);
    opacity: 0.65;
    pointer-events: none;
    user-select: none;
  }
  /* Flechas de navegación · blancas, a los lados */
  .lb-nav {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    width: 44px;
    height: 68px;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 38px;
    line-height: 1;
    color: #fff;
    background: rgba(0, 0, 0, 0.32);
    border: none;
    border-radius: var(--radius-md);
    cursor: pointer;
    opacity: 0.85;
    transition: opacity 0.12s, background 0.12s;
    user-select: none;
  }
  .lb-nav:hover { opacity: 1; background: rgba(0, 0, 0, 0.55); }
  .lb-prev { left: 16px; }
  .lb-next { right: 16px; }
  /* Responsive · screenshots más pequeñas en móvil */
  @media (max-width: 640px) {
    .shot { width: 260px; }
  }

  /* ═══ Description ═══ */
  .description {
    color: var(--ink-dim);
    font-size: var(--fs-13);
    line-height: 1.65;
    margin: 0;
  }

  /* ═══ Info técnica · cards individuales en grid 2 columnas (mockup style) ═══ */
  .info-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-auto-flow: row dense; /* rellena huecos automáticamente */
    gap: var(--sp-2);
    padding: var(--sp-2) 0;
  }
  .info-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-3);
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
    padding: 12px 14px;
    min-height: 48px;
  }
  /* Servicios multi y Container: ocupan ambas columnas */
  .info-card-wide {
    grid-column: 1 / -1;
    align-items: center;
    flex-wrap: wrap;
  }
  .info-card-k {
    color: var(--ink-mute);
    font-size: var(--fs-11);
    flex-shrink: 0;
  }
  .info-card-v {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: var(--fs-11);
    word-break: break-all;
    text-align: right;
  }
  .info-card-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
  }
  .service-chip {
    color: var(--ink-dim);
    background: var(--panel-deep);
    padding: 3px 9px;
    border-radius: 4px;
    font-family: var(--font-mono);
    font-size: var(--fs-11);
  }

  /* ═══ Credenciales · card sólida con filas separadas (estilo mockup) ═══ */
  .creds-block {
    display: flex;
    flex-direction: column;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
    overflow: hidden;
  }
  .cred-row {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: 16px 18px;
    font-size: var(--fs-12);
    border-bottom: 1px solid var(--line);
  }
  .cred-row:last-of-type {
    border-bottom: none;
  }
  .cred-k {
    color: var(--ink-mute);
    min-width: 100px;
    font-size: var(--fs-10);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .cred-v {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: var(--fs-12);
    flex: 1;
    min-width: 0;
    word-break: break-all;
    background: transparent;
    padding: 0;
  }
  .cred-v.masked {
    letter-spacing: 2px;
  }
  .cred-btn {
    background: transparent;
    border: 1px solid var(--line);
    color: var(--ink-mute);
    border-radius: 4px;
    padding: 4px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: color 0.12s, background 0.12s, border-color 0.12s;
  }
  .cred-btn:hover {
    color: var(--ink);
    background: var(--line);
    border-color: var(--line-bright);
  }
  .cred-btn svg { width: 12px; height: 12px; }
  .cred-note {
    color: var(--info);
    font-size: var(--fs-10);
    font-style: italic;
  }
  .creds-hint {
    color: var(--ink-mute);
    font-size: var(--fs-11);
    padding: 14px 18px;
    border-top: 1px solid var(--line);
  }
  .copy-feedback {
    font-size: var(--fs-11);
    color: var(--signal);
    padding: 4px 0;
    text-align: right;
    font-family: var(--font-mono);
  }

  /* ═══ Uninstall section · sutil al final ═══ */
  .uninstall-section {
    margin-top: var(--sp-3);
    padding-top: var(--sp-4);
    border-top: 1px solid var(--line);
    align-items: flex-start;
  }
  .uninstall-hint {
    color: var(--ink-mute);
    font-size: var(--fs-11);
    margin: 0;
  }

  /* ═══ Botones ═══ */
  .btn {
    padding: 10px 22px;
    border-radius: var(--radius-sm);
    border: 1px solid transparent;
    font-size: var(--fs-12);
    font-weight: 600;
    font-family: inherit;
    cursor: pointer;
    transition: filter 0.12s, background 0.12s, color 0.12s, border-color 0.12s;
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
  .btn-primary {
    background: var(--signal);
    color: var(--canvas);
  }
  .btn-primary:not(:disabled):hover { filter: brightness(1.08); }
  .btn-secondary {
    background: transparent;
    color: var(--ink-dim);
    border-color: var(--line);
  }
  .btn-secondary:not(:disabled):hover {
    color: var(--ink);
    background: var(--line);
  }
  .btn-danger-soft {
    background: transparent;
    color: var(--crit);
    border-color: var(--crit-border);
    padding: 8px 16px;
    font-size: var(--fs-12);
  }
  .btn-danger-soft:not(:disabled):hover {
    background: var(--crit-dim);
  }

  /* Spinner inline para botón "Instalando..." */
  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid currentColor;
    border-top-color: transparent;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }
  /* ═══ Modal uninstall · opciones radio ═══ */
  .uninstall-modes {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .mode-option {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .mode-option:hover {
    border-color: var(--line-bright);
  }
  .mode-option.selected {
    border-color: var(--signal);
    background: var(--canvas);
    box-shadow: 0 0 0 1px var(--signal);
  }
  .mode-option input[type="radio"] {
    accent-color: var(--signal);
    margin-top: 4px;
    flex-shrink: 0;
    cursor: pointer;
  }
  .mode-content {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1;
    min-width: 0;
  }
  .mode-title {
    color: var(--ink);
    font-size: var(--fs-12);
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .mode-title-danger {
    color: var(--crit);
  }
  .mode-badge {
    font-size: var(--fs-10);
    padding: 2px 8px;
    border-radius: 4px;
    font-family: var(--font-mono);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }
  .mode-badge-ok {
    background: var(--signal-dim, rgba(0, 255, 159, 0.12));
    color: var(--signal);
  }
  .mode-desc {
    color: var(--ink-dim);
    font-size: var(--fs-11);
    line-height: 1.5;
  }
  .mode-desc strong {
    color: var(--crit);
    font-weight: 600;
  }

  /* ═══ Progreso de uninstall (running / done / error) ═══ */
  .uninstall-progress {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 8px 0;
  }
  .uninstall-step {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
  }
  .uninstall-step-label {
    color: var(--ink);
    font-size: var(--fs-12);
    font-weight: 500;
  }
  .uninstall-led {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
    background: var(--ink-faint);
    transition: background 0.2s, box-shadow 0.2s;
  }
  .uninstall-led.led-active {
    background: var(--info);
    box-shadow: 0 0 8px var(--info-glow, rgba(77, 184, 255, 0.5));
    animation: led-pulse 1.2s ease-in-out infinite;
  }
  .uninstall-led.led-ok {
    background: var(--signal);
    box-shadow: 0 0 8px var(--signal-glow, rgba(0, 255, 159, 0.5));
  }
  .uninstall-led.led-crit {
    background: var(--crit);
    box-shadow: 0 0 8px var(--crit-glow, rgba(255, 90, 90, 0.5));
  }
  @keyframes led-pulse {
    0%, 100% { opacity: 1; transform: scale(1); }
    50%      { opacity: 0.55; transform: scale(0.88); }
  }
  .uninstall-hint-running {
    color: var(--ink-mute);
    font-size: var(--fs-11);
    text-align: center;
    padding-top: 4px;
  }
  .uninstall-err {
    color: var(--crit);
    font-size: var(--fs-11);
    font-family: var(--font-mono);
    padding: 10px 14px;
    background: var(--crit-dim, rgba(255, 90, 90, 0.08));
    border: 1px solid var(--crit-border, rgba(255, 90, 90, 0.3));
    border-radius: var(--radius-sm);
    word-break: break-word;
  }
  /* ═══ Sprint Updates · modal de update · vista 'confirm' ═══ */
  .update-confirm {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .update-confirm-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: 12px 14px;
    background: var(--canvas);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
  }
  .update-confirm-k {
    color: var(--ink-mute);
    font-size: var(--fs-11);
  }
  .update-confirm-v {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: var(--fs-12);
    font-weight: 600;
  }
  .update-confirm-hint {
    color: var(--ink-dim);
    font-size: var(--fs-11);
    line-height: 1.55;
    padding: 0 2px;
    margin: 4px 0 0;
  }
  .update-confirm-hint code {
    background: var(--panel-deep);
    padding: 2px 6px;
    border-radius: 3px;
    font-family: var(--font-mono);
    font-size: var(--fs-10);
    color: var(--info);
  }
</style>
