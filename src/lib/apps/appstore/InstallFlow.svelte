<script>
  /**
   * InstallFlow · Proceso de instalación de una app del catálogo
   * ─────────────────────────────────────────────────────────────
   * Patrón SÍNCRONO simplificado (decisión tras Fase 5 hotfix):
   *
   *   1. Desplegar contenedor (sync · 30s-5min)
   *      · Una sola llamada a /api/docker/stack
   *      · Backend hace `docker compose up -d`, lo cual incluye `docker pull`
   *        automático si la imagen no es local
   *      · Sin pull explícito previo · evita dependencia de ?async=true
   *      · Barra indeterminada animada (no podemos reportar % real desde sync)
   *
   *   2. Registrar en NimHealth (~150ms)
   *      · El backend ya hace ForceDockerCacheRefresh() en el handler
   *      · Aquí solo damos pausa visual para que el user vea el último LED
   *
   * Si el browser corta la conexión durante el deploy (timeout proxy con
   * imágenes muy grandes), el backend SIGUE trabajando · al volver al detalle
   * la app aparecerá como instalada cuando capabilities/services se refresquen.
   *
   * Props:
   *   - view: AppView · app que se está instalando
   *
   * Eventos:
   *   - done · install completado con éxito
   *   - cancel · user pulsó cancelar (no aborta backend, solo cierra UI)
   */

  import { onMount, createEventDispatcher } from 'svelte';
  import { installApp } from './api.js';
  import { loadAppConfig } from './catalog.js';
  import ConfigModal from './config/ConfigModal.svelte';
  import { needsConfigModal } from './config/configSchema.js';
  import { escapeUserEnv } from './config/configSchema.js';
  import { collectSecretKeys } from './config/configSchema.js';
  import { needsDomainContext } from './config/configSchema.js';
  import { loadDomainContext, autoExposeApp } from './config/networkContext.js';
  import { extractSubdomain } from './config/autoProviders.js';

  /** @typedef {import('./types').AppView} AppView */

  /** @type {AppView} */
  export let view;

  /**
   * Modo embedded: cuando InstallFlow se renderiza dentro de AppStoreDetail,
   * el hero ya está visible arriba (en el detail). Ocultamos el nuestro para
   * evitar duplicación visual y mantenemos solo los steps + barra + acciones.
   */
  export let embedded = false;

  const dispatch = createEventDispatcher();

  // ── Estado del flow ────────────────────────────────────────────────
  /** 'config' | 'deploy' | 'register' | 'done' */
  let phase = 'deploy';
  let installError = '';

  // ── Config modal (sistema de aprovisionamiento) ────────────────────
  // Si la app declara configFields/postInstall, mostramos un modal ANTES de
  // instalar para recoger los datos (ej. Matrix SERVER_NAME, VSCode PASSWORD).
  // Los valores recogidos se mergean en el env que se manda al backend.
  let showConfig = false;
  /** env recogido del modal · se mergea sobre el del catálogo en el deploy */
  let configEnv = null;
  /** valores de postInstall recogidos (Capa 2 · admin) · de momento se guardan */
  let configPostInstall = null;
  /** config cargada desde el archivo separado (configRef) · configFields/postInstall */
  let appConfig = null;
  /** contexto de dominio cargado de Network (baseDomain, httpsPort, domains) ·
   *  solo se carga si la app tiene un campo auto:domain (ej. Matrix) */
  let domainCtx = null;

  // ── Steps visuales ─────────────────────────────────────────────────
  $: steps = computeSteps(phase);

  /**
   * @param {string} ph
   * @returns {Array<{id: string, label: string, state: 'done'|'active'|'pending', detail?: string, showBar?: boolean}>}
   */
  function computeSteps(ph) {
    const PHASES = ['deploy', 'register', 'done'];
    const idx = PHASES.indexOf(ph);
    const stateOf = (stepPhase) => {
      const stepIdx = PHASES.indexOf(stepPhase);
      if (idx > stepIdx) return 'done';
      if (idx === stepIdx) return 'active';
      return 'pending';
    };

    return [
      {
        id: 'deploy',
        label: 'Descargar e instalar',
        state: stateOf('deploy'),
        detail: ph === 'deploy' ? 'Puede tardar varios minutos según el tamaño de la imagen…' : '',
        showBar: ph === 'deploy',
      },
      {
        id: 'register',
        label: 'Registrar en NimHealth',
        state: stateOf('register'),
        detail: ph === 'register' ? 'Actualizando catálogo de servicios…' : '',
      },
    ];
  }

  // ── Lifecycle ──────────────────────────────────────────────────────
  onMount(start);

  async function start() {
    if (!view?.catalog) {
      installError = 'Datos del catálogo no disponibles';
      return;
    }
    if (!view.catalog.compose) {
      installError = 'La app no especifica compose YAML';
      return;
    }

    // Si la app tiene configRef, cargamos su archivo de config separado (lazy ·
    // solo al instalar, para no hinchar el catálogo principal). La config trae
    // configFields/postInstall. Si no hay configRef, appConfig queda null y la
    // app instala directo (comportamiento clásico).
    if (appConfig === null && view.catalog.configRef) {
      appConfig = await loadAppConfig(view.catalog.configRef);
    }

    // La fuente de config: el archivo separado (appConfig) si existe, o el
    // propio catálogo (compat · por si alguna app trae configFields inline).
    const cfgSource = appConfig || view.catalog;

    // Si la config tiene un campo auto:domain (ej. Matrix SERVER_NAME), cargamos
    // los datos reales de Network (dominio base + puerto HTTPS) para pre-rellenar
    // ese campo SIN que el usuario los escriba a mano. Reusa las APIs de Network.
    // Solo apps con auto:domain · las demás (Jellyfin) no tocan Network.
    if (domainCtx === null && needsDomainContext(cfgSource)) {
      domainCtx = await loadDomainContext();
    }

    // Si la app necesita configuración (configFields/postInstall) y todavía no
    // se ha recogido, mostramos el modal ANTES de instalar. El deploy real se
    // dispara cuando el usuario confirma (onConfigConfirm).
    if (needsConfigModal(cfgSource) && configEnv === null) {
      phase = 'config';
      showConfig = true;
      return;
    }

    phase = 'deploy';
    installError = '';

    try {
      // ── Step 1 · stack deploy sync ──
      // El backend ejecuta `docker compose up -d`, que hace pull automático
      // si la imagen no es local. Tarda 30s-5min según tamaño de la imagen
      // y velocidad de red.
      //
      // env: si el modal recogió valores (configEnv), se mergean sobre el env
      // del catálogo (los del usuario prevalecen). Si no hubo modal, se pasa
      // el del catálogo tal cual (comportamiento clásico). El backend mergea
      // todo encima de CONFIG_PATH/HOST_IP/TZ y expande ${VAR}.
      //
      // ESCAPE del $ (Pieza 2): los valores del USUARIO (configEnv) se escapan
      // ($→$$) porque docker-compose interpreta el $ del .env (un password
      // "my$ecret" llegaría como "my" sin escapar · verificado en hardware).
      // SOLO se escapa configEnv · el catalog.env lleva referencias ${VAR}
      // legítimas que NO se deben escapar (su $ es sintaxis, lo resuelve el
      // backend con expandStackEnvRefs).
      const escapedUserEnv = escapeUserEnv(configEnv || {});
      const mergedEnv = { ...(view.catalog.env || {}), ...escapedUserEnv };

      // Capa 2 · postInstall. Si la config (appConfig) declara acciones, las
      // mandamos junto con sus valores (recogidos en configPostInstall) y qué
      // claves son secretas (para que el backend las ofusque en logs).
      const cfg = appConfig || view.catalog;
      const postInstallActions = Array.isArray(cfg?.postInstall) ? cfg.postInstall : [];
      const postInstallSecretKeys = collectSecretKeys(postInstallActions);

      // Puerto efectivo · si la app declara un configField con purpose:network_port,
      // el valor que eligió el usuario en el modal manda sobre el puerto fijo del
      // catálogo. Así NimOS registra/proxya el puerto REAL elegido (estilo
      // Synology/Unraid: ves el puerto al instalar y lo cambias si quieres). Si no
      // hay campo de puerto, se usa el del catálogo (comportamiento clásico).
      const portFieldKey = (cfg.configFields || []).find(
        (f) => f.purpose === 'network_port'
      )?.key;
      const chosenPort = Number.parseInt(portFieldKey ? (configEnv || {})[portFieldKey] : '', 10);
      const effectivePort =
        Number.isFinite(chosenPort) && chosenPort > 0 ? chosenPort : view.catalog.port;

      await installApp({
        id: view.id,
        name: view.name,
        compose: view.catalog.compose,
        icon: view.icon,
        color: view.color,
        port: effectivePort,
        openMode: view.catalog.openMode || 'internal',
        external: view.catalog.openMode === 'external',
        game: view.catalog.game ? JSON.stringify(view.catalog.game) : '',
        runtimeIdentity: view.catalog.runtimeIdentity || null,
        env: mergedEnv,
        landingPath: view.catalog.landingPath || '',
        postInstall: postInstallActions,
        postInstallValues: configPostInstall || {},
        postInstallSecretKeys,
        seedFiles: Array.isArray(cfg?.seedFiles) ? cfg.seedFiles : [],
      });

      // Paso 3 · auto-exposición. Si la app tiene un campo auto:domain (Matrix),
      // el usuario eligió un dominio (ej. matrix.midominio.org). Lo registramos
      // en Network (cert + Caddy) para que aparezca con las demás apps expuestas.
      // NO se revierte la instalación si falla · la app queda instalada en local
      // y Network avisa con su cartel. Solo apps con auto:domain.
      if (needsDomainContext(cfg) && domainCtx?.baseDomain) {
        // El dominio que el usuario confirmó está en configEnv (el campo
        // auto:domain · típicamente SERVER_NAME). Buscamos el primero que encaje.
        const domainFieldKey = (cfg.configFields || []).find(
          (f) => f.auto && (f.auto.provider === 'domain' || f.auto.provider === 'domain_root')
        )?.key;
        const chosenDomain = domainFieldKey ? (configEnv || {})[domainFieldKey] : '';
        const subdomain = extractSubdomain(chosenDomain, domainCtx.baseDomain);
        if (subdomain) {
          const expo = await autoExposeApp({
            appId: view.id,
            displayName: view.name,
            subdomain,
            upstreamPort: effectivePort,
          });
          if (!expo.ok) {
            // No bloquea · solo lo registramos. Network avisa por su cuenta.
            console.warn('[appstore/install] auto-exposición falló:', expo.error);
          }
        }
      }

      // ── Step 2 · pausa visual del registro NimHealth ──
      // El backend ya hizo ForceDockerCacheRefresh en el handler · esto es
      // solo para que el user vea el último LED encenderse antes de salir.
      phase = 'register';
      await new Promise((r) => setTimeout(r, 600));

      phase = 'done';
      setTimeout(() => {
        dispatch('done');
      }, 800);
    } catch (err) {
      // Conflicto de puerto fijo (DNS :53, DHCP...) detectado por el preflight
      // del backend: el deploy se canceló ANTES de crear nada (sin red, container
      // ni registro). Mostramos el motivo claro, sin el ruido técnico.
      if (err?.code === 'port_in_use') {
        const c = err?.details?.conflicts?.[0];
        installError = c
          ? `El puerto ${c.port} ya lo usa la app «${c.held_by}». Solo una app puede usar ese puerto a la vez.`
          : (err?.message || 'Ese puerto ya está en uso por otra app.');
        console.warn('[appstore/install] conflicto de puerto fijo:', err?.details?.conflicts);
        return;
      }
      const parts = [];
      parts.push(err?.message || String(err));
      if (err?.code) parts.push(`code: ${err.code}`);
      if (err?.status) parts.push(`status: ${err.status}`);
      installError = parts.join(' · ');
      console.error('[appstore/install] failed:', err);
    }
  }

  function handleCancel() {
    dispatch('cancel');
  }

  // ── Handlers del modal de config ───────────────────────────────────
  function onConfigConfirm(e) {
    // e.detail = { env: {...}, postInstall: {...} }
    configEnv = e.detail.env || {};
    configPostInstall = e.detail.postInstall || {};
    showConfig = false;
    // Ahora sí, lanzar el deploy con el env recogido.
    start();
  }

  function onConfigCancel() {
    showConfig = false;
    dispatch('cancel');
  }

  // Contexto para resolver auto:{domain/local_ip...}. De momento toma lo que
  // haya en view; cuando se integre con Network/DDNS se rellenará con el
  // dominio real, la IP local, etc. (TODO Capa 1 · conectar con /api network).
  // Contexto para resolver auto:{domain/local_ip...}. El baseDomain y httpsPort
  // vienen de Network (domainCtx) cuando la app tiene auto:domain · así el campo
  // SERVER_NAME de Matrix se rellena con el dominio REAL configurado, no a mano.
  $: configCtx = {
    appName: view?.name || '',
    baseDomain: domainCtx?.baseDomain || '',
    httpsPort: domainCtx?.httpsPort || 443,
    localIp: view?.localIp || '',
    appPort: view?.catalog?.port || 0,
    hostname: view?.hostname || '',
    domains: domainCtx?.domains || [],
  };

  function handleRetry() {
    installError = '';
    phase = 'deploy';
    start();
  }
</script>

{#if showConfig}
  <ConfigModal
    catalog={appConfig || view.catalog}
    appName={view.name}
    appIcon={view.icon}
    ctx={configCtx}
    on:confirm={onConfigConfirm}
    on:cancel={onConfigCancel}
  />
{/if}

<div class="install-flow" class:embedded={embedded}>
  {#if !embedded}
    <!-- Hero compacto (solo en modo standalone · evita duplicar el del detail) -->
    <div class="hero">
      {#if view.icon}
        <img class="hero-icon" src={view.icon} alt={view.name} />
      {:else}
        <div class="hero-icon-fallback">{view.name.charAt(0)}</div>
      {/if}
      <div class="hero-text">
        <h1 class="hero-title">
        {#if installError}
          Instalación interrumpida
        {:else if phase === 'done'}
          ¡{view.name} instalada!
        {:else}
          Instalando {view.name}
        {/if}
      </h1>
      <div class="hero-meta">
        {view.catalog?.image || ''}
      </div>
    </div>
  </div>
  {/if}

  {#if installError}
    <div class="error-box">{installError}</div>
    <div class="actions">
      <button class="btn btn-primary" on:click={handleRetry}>Reintentar</button>
      <button class="btn btn-secondary" on:click={handleCancel}>Volver</button>
    </div>
  {:else}
    <ol class="steps">
      {#each steps as step (step.id)}
        <li class="step" class:done={step.state === 'done'} class:active={step.state === 'active'} class:pending={step.state === 'pending'}>
          <div class="step-led">
            {#if step.state === 'done'}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round">
                <polyline points="5 12 10 17 19 7" />
              </svg>
            {:else if step.state === 'active'}
              <span class="led-pulse"></span>
            {/if}
          </div>
          <div class="step-text">
            <div class="step-label">{step.label}</div>
            {#if step.detail}
              <div class="step-detail">{step.detail}</div>
            {/if}
            {#if step.showBar && step.state === 'active'}
              <!-- Barra indeterminada · sin % real porque el sync deploy
                   no reporta progreso. La animación honesta indica "trabajando". -->
              <div class="install-bar">
                <div class="install-bar-fill"></div>
              </div>
            {/if}
          </div>
        </li>
      {/each}
    </ol>

    {#if phase !== 'done'}
      <div class="actions">
        <button class="btn btn-secondary" on:click={handleCancel}>Cancelar</button>
      </div>
      <p class="hint">
        No cierres esta ventana hasta que termine.
      </p>
    {/if}
  {/if}
</div>

<style>
  .install-flow {
    height: 100%;
    overflow-y: auto;
    padding: var(--sp-5) var(--sp-5);
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    max-width: 640px;
    margin: 0 auto;
  }

  /* Modo embedded · cuando se renderiza dentro de AppStoreDetail.
     El detail ya hace el layout (padding, scroll, max-width), nosotros solo
     aportamos los steps + barra + acciones sin chrome propio. */
  .install-flow.embedded {
    height: auto;
    overflow: visible;
    padding: 0;
    max-width: none;
    margin: 0;
    gap: var(--sp-3);
  }

  /* ═══ Hero ═══ */
  .hero {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: var(--sp-2) 0;
  }
  .hero-icon, .hero-icon-fallback {
    width: 56px;
    height: 56px;
    border-radius: 12px;
    flex-shrink: 0;
  }
  .hero-icon {
    object-fit: contain;
    background: var(--canvas);
    padding: 10px;
  }
  .hero-icon-fallback {
    background: var(--canvas-soft);
    color: var(--ink-dim);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 26px;
    font-weight: 600;
    font-family: var(--font-mono);
  }
  .hero-text { flex: 1; min-width: 0; }
  .hero-title {
    font-size: var(--fs-18);
    font-weight: 600;
    color: var(--ink);
    margin: 0 0 4px;
    letter-spacing: -0.3px;
  }
  .hero-meta {
    font-size: var(--fs-11);
    color: var(--ink-mute);
    font-family: var(--font-mono);
    word-break: break-all;
  }

  /* ═══ Steps ═══ */
  .steps {
    list-style: none;
    padding: 0;
    margin: var(--sp-2) 0;
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
  }
  .step {
    display: flex;
    align-items: flex-start;
    gap: var(--sp-3);
    position: relative;
    padding-left: 4px;
  }
  .step:not(:last-child)::before {
    content: '';
    position: absolute;
    left: 14px;
    top: 28px;
    width: 1px;
    height: calc(100% + var(--sp-3) - 28px);
    background: var(--line);
  }
  .step.done:not(:last-child)::before {
    background: var(--signal);
    opacity: 0.4;
  }

  .step-led {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    border: 1.5px solid var(--ink-trace);
    background: var(--panel-elev);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    margin-top: 1px;
    color: var(--canvas);
    transition: border-color 0.2s, background 0.2s;
  }
  .step-led svg { width: 14px; height: 14px; }
  .led-pulse {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--info);
    animation: pulse-led 1.2s ease-in-out infinite;
  }
  @keyframes pulse-led {
    0%, 100% { opacity: 0.5; transform: scale(0.85); }
    50%      { opacity: 1;   transform: scale(1.15); }
  }
  .step.done .step-led {
    border-color: var(--signal);
    background: var(--signal);
  }
  .step.active .step-led {
    border-color: var(--info);
    background: var(--info-dim);
  }

  .step-text {
    flex: 1;
    min-width: 0;
    padding-top: 2px;
  }
  .step-label {
    font-size: var(--fs-13);
    font-weight: 500;
    color: var(--ink);
    line-height: 1.3;
  }
  .step.pending .step-label { color: var(--ink-mute); }
  .step-detail {
    font-size: var(--fs-11);
    color: var(--ink-mute);
    margin-top: 3px;
    font-family: var(--font-mono);
  }

  /* Barra indeterminada · gradient deslizándose */
  .install-bar {
    height: 4px;
    background: var(--panel-deep);
    border-radius: 2px;
    overflow: hidden;
    margin-top: 8px;
    max-width: 400px;
    border: 1px solid var(--line);
    position: relative;
  }
  .install-bar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 30%;
    background: linear-gradient(
      90deg,
      transparent 0%,
      var(--info) 50%,
      transparent 100%
    );
    animation: indeterminate 1.6s ease-in-out infinite;
  }
  @keyframes indeterminate {
    0%   { left: -30%; }
    100% { left: 100%; }
  }

  /* ═══ Error ═══ */
  .error-box {
    padding: var(--sp-3);
    background: var(--crit-dim);
    border: 1px solid var(--crit-border);
    border-radius: var(--radius-sm);
    color: var(--ink);
    font-size: var(--fs-12);
    font-family: var(--font-mono);
    line-height: 1.55;
    word-break: break-word;
  }

  /* ═══ Actions ═══ */
  .actions {
    display: flex;
    gap: var(--sp-2);
  }
  .btn {
    padding: 9px 18px;
    border-radius: var(--radius-sm);
    border: 1px solid transparent;
    font-size: var(--fs-12);
    font-weight: 600;
    font-family: inherit;
    cursor: pointer;
    transition: background 0.12s, color 0.12s, filter 0.12s;
  }
  .btn-primary {
    background: var(--signal);
    color: var(--canvas);
  }
  .btn-primary:hover { filter: brightness(1.08); }
  .btn-secondary {
    background: transparent;
    color: var(--ink-dim);
    border-color: var(--line);
  }
  .btn-secondary:hover {
    color: var(--ink);
    background: var(--line);
  }
  .hint {
    font-size: var(--fs-11);
    color: var(--ink-mute);
    margin: 0;
  }
</style>
