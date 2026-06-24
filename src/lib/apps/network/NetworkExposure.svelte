<script>
  /**
   * NetworkExposure · Sección de exposición de apps a internet (v4).
   * ─────────────────────────────────────────────────────────────────
   * Muestra y gestiona qué apps se exponen a internet con HTTPS vía Caddy.
   *
   * Dos bloques:
   *   1. Config global: dominio base + interruptor de exposición.
   *   2. Lista de apps expuestas: cada una con su ruta, upstream, estado
   *      del cert (observado de Caddy) y acciones (pausar/editar/quitar).
   *
   * El componente es "tonto" respecto a la red: recibe datos por props y
   * emite eventos al padre (NetworkApp), que es quien llama a la API.
   *
   * Props:
   *   · config     — { base_domain, caddy_admin_url, enabled }
   *   · apps       — array de apps expuestas
   *   · certs      — snapshot del observer { reachable, certs:[...] } | null
   *   · busy       — bool, deshabilita acciones mientras hay una en curso
   *   · msg        — feedback del último intento
   *
   * Eventos:
   *   · save-config  — { detail: { baseDomain, enabled } }
   *   · expose       — (abre modal en el padre)
   *   · toggle       — { detail: { app, enabled } }
   *   · edit         — { detail: { app } }
   *   · remove       — { detail: { app } }
   */
  import { createEventDispatcher, onMount } from 'svelte';
  import { token } from '$lib/stores/auth.js';
  import { SectionHead, BevelButton, Badge, TextInput, EmptyState, LED } from '$lib/ui';
  import { fullDomainFor, appURL, certForApp, appState } from './api.js';

  export let config = { base_domain: '', caddy_admin_url: '', enabled: false };
  export let apps = [];
  export let certs = null;
  export let installedApps = []; // SHIELD-P2 · para el estado del candado
  export let busy = false;
  export let msg = '';

  const dispatch = createEventDispatcher();

  // Estado local del formulario de config (editable antes de guardar).
  let domainInput = config.base_domain || '';
  let enabledInput = config.enabled || false;
  let httpPortInput = String(config.http_port || 80);
  let httpsPortInput = String(config.https_port || 443);

  // ─── Dominios DDNS registrados (para el selector de dominio base) ───
  // El módulo DDNS ya conoce los dominios del usuario; los leemos para no
  // obligar a reescribirlos a mano (evita typos y "¿qué pongo aquí?").
  let ddnsDomains = [];
  let ddnsLoading = true;
  let manualDomain = false; // true = el usuario eligió escribir uno a mano

  onMount(loadDdnsDomains);

  async function loadDdnsDomains() {
    ddnsLoading = true;
    try {
      const r = await fetch('/api/v4/network/ddns', {
        headers: $token ? { Authorization: `Bearer ${$token}` } : {},
      });
      if (r.ok) {
        const data = await r.json();
        ddnsDomains = (data.ddns || [])
          .map((d) => d.domain)
          .filter(Boolean);
        // Autoseleccionar si solo hay uno y aún no hay dominio configurado.
        if (ddnsDomains.length === 1 && !config.base_domain) {
          domainInput = ddnsDomains[0];
        }
        // Si el dominio configurado no está entre los DDNS, asumimos manual.
        if (config.base_domain && !ddnsDomains.includes(config.base_domain)) {
          manualDomain = true;
        }
      }
    } catch (e) {
      // sin DDNS disponible — el usuario podrá escribir manual
    } finally {
      ddnsLoading = false;
    }
  }

  // Resincronizar inputs si cambia la config desde el padre (tras guardar).
  $: if (config) {
    domainInput = config.base_domain || '';
    enabledInput = config.enabled || false;
    httpPortInput = String(config.http_port || 80);
    httpsPortInput = String(config.https_port || 443);
  }

  $: dirty = domainInput !== (config.base_domain || '') ||
    enabledInput !== (config.enabled || false) ||
    httpPortInput !== String(config.http_port || 80) ||
    httpsPortInput !== String(config.https_port || 443);
  $: httpPortNum = parseInt(httpPortInput, 10);
  $: httpsPortNum = parseInt(httpsPortInput, 10);
  $: portsValid =
    !isNaN(httpPortNum) && httpPortNum >= 1 && httpPortNum <= 65535 &&
    !isNaN(httpsPortNum) && httpsPortNum >= 1 && httpsPortNum <= 65535 &&
    httpPortNum !== httpsPortNum;
  $: caddyReachable = certs ? certs.reachable : null;

  // ─── Tira de estado del acceso externo ───
  // Una línea densa: qué piezas están OK y cuál es el siguiente paso. El
  // único que NimOS no puede verificar es el router (forward externo) —
  // cuando todo lo demás está verde, la tira recuerda esa regla exacta.
  $: validCerts = (certs && certs.certs ? certs.certs.length : 0);
  $: strip = [
    { k: 'dominio', ok: !!config.base_domain },
    { k: 'exposición', ok: !!config.enabled },
    { k: 'caddy', ok: caddyReachable === true },
    { k: 'certs', ok: validCerts > 0 },
  ];
  $: stripMissing = strip.find((i) => !i.ok);
  $: extPort = config.https_port && config.https_port !== 443 ? `:${config.https_port}` : '';

  function saveConfig() {
    if (!portsValid) return;
    dispatch('save-config', {
      baseDomain: domainInput.trim(),
      enabled: enabledInput,
      httpPort: httpPortNum,
      httpsPort: httpsPortNum,
    });
  }

  function badgeVariantFor(kind) {
    switch (kind) {
      case 'exposed': return 'accent';
      case 'paused': return 'default';
      case 'applying': return 'info';
      case 'cert_warn': return 'orange';
      case 'cert_pending': return 'warn';
      default: return 'default';
    }
  }

  function ledVariantFor(kind) {
    switch (kind) {
      case 'exposed': return 'ok';
      case 'paused': return 'muted';
      case 'applying': return 'info';
      case 'cert_warn': return 'warn';
      case 'cert_pending': return 'warn';
      default: return 'muted';
    }
  }
</script>

<div class="nx-section">
  <!-- ── Tira de estado del acceso externo ── -->
  <div class="nx-strip">
    {#each strip as it (it.k)}
      <span class="nx-pill mono" class:ok={it.ok}>{it.k}</span>
    {/each}
    {#if stripMissing}
      <span class="nx-strip-hint mono warn">falta: {stripMissing.k}</span>
    {:else}
      <span class="nx-strip-hint mono">router: abre {config.https_port || 443}/tcp → esta máquina · https://{config.base_domain}{extPort}</span>
    {/if}
  </div>

  <!-- ── Config global ── -->
  <SectionHead>Configuración global</SectionHead>

  <div class="nx-config">
    <div class="nx-config-row">
      <label class="nx-label" for="nx-domain">Dominio base</label>
      <div class="nx-field">
        {#if ddnsLoading}
          <div class="nx-domain-loading">Cargando dominios…</div>
        {:else if ddnsDomains.length === 0 && !manualDomain}
          <!-- Sin dominios DDNS registrados: avisar y enlazar al tab DDNS -->
          <div class="nx-domain-warn">
            <span class="nx-warn-icon">!</span>
            <div class="nx-warn-text">
              No hay ningún dominio registrado.
              <button class="nx-link" on:click={() => dispatch('goto-ddns')}>Registra un dominio DDNS primero</button>
              o
              <button class="nx-link" on:click={() => (manualDomain = true)}>escríbelo a mano</button>.
            </div>
          </div>
        {:else if manualDomain}
          <!-- Modo manual: input de texto libre + volver al selector -->
          <TextInput
            value={domainInput}
            placeholder="ej. minas.duckdns.org"
            disabled={busy}
            onInput={(e) => (domainInput = e.target.value)}
          />
          {#if ddnsDomains.length > 0}
            <span class="nx-hint">
              <button class="nx-link" on:click={() => { manualDomain = false; domainInput = ddnsDomains[0]; }}>
                ← Elegir de mis dominios registrados
              </button>
            </span>
          {/if}
        {:else}
          <!-- Dropdown con dominios DDNS registrados -->
          <select
            class="nx-domain-select"
            bind:value={domainInput}
            disabled={busy}
          >
            {#each ddnsDomains as d}
              <option value={d}>{d}</option>
            {/each}
          </select>
          <span class="nx-hint">
            Dominio registrado en DDNS · <button class="nx-link" on:click={() => (manualDomain = true)}>escribir otro a mano</button>
          </span>
        {/if}
      </div>
    </div>

    <div class="nx-config-row">
      <span class="nx-label">Exposición</span>
      <div class="nx-field">
        <button
          class="nx-toggle"
          class:on={enabledInput}
          disabled={busy}
          on:click={() => (enabledInput = !enabledInput)}
          type="button"
        >
          <span class="nx-toggle-knob"></span>
          <span class="nx-toggle-text">{enabledInput ? 'ACTIVADA' : 'DESACTIVADA'}</span>
        </button>
        <span class="nx-hint">
          Interruptor maestro. Si está desactivado, ninguna app se expone aunque esté marcada.
        </span>
      </div>
    </div>

    <div class="nx-config-row">
      <span class="nx-label">Puertos de Caddy</span>
      <div class="nx-field">
        <div class="nx-ports">
          <div class="nx-port">
            <span class="nx-port-lbl mono">HTTP</span>
            <TextInput
              value={httpPortInput}
              placeholder="80"
              size="sm"
              disabled={busy}
              onInput={(e) => (httpPortInput = e.target.value)}
            />
          </div>
          <div class="nx-port">
            <span class="nx-port-lbl mono">HTTPS</span>
            <TextInput
              value={httpsPortInput}
              placeholder="443"
              size="sm"
              disabled={busy}
              onInput={(e) => (httpsPortInput = e.target.value)}
            />
          </div>
        </div>
        {#if !portsValid}
          <span class="nx-err">Puertos 1-65535, y distintos entre sí.</span>
        {:else}
          <span class="nx-hint">Cámbialos si :80/:443 están ocupados por otro equipo o servicio (ej. un Synology). Los certificados (DNS-01) funcionan en cualquier puerto.</span>
        {/if}
      </div>
    </div>

    {#if dirty}
      <div class="nx-config-save">
        <BevelButton size="sm" variant="primary" onClick={saveConfig} disabled={busy || !portsValid}>
          {busy ? '▸ Guardando…' : '▸ Guardar configuración'}
        </BevelButton>
      </div>
    {/if}
  </div>

  <!-- ── Banner de Caddy no alcanzable ── -->
  {#if caddyReachable === false}
    <div class="nx-banner warn">
      <LED variant="warn" size={8} />
      <span>Caddy no responde. Las apps no se están sirviendo. Comprueba que el servicio Caddy esté activo.</span>
    </div>
  {/if}

  <!-- ── Lista de apps ── -->
  <div class="nx-apps-head">
    <SectionHead>Apps expuestas · {apps.length}</SectionHead>
    <BevelButton size="sm" variant="primary" onClick={() => dispatch('expose')} disabled={busy || !config.base_domain}>
      + Exponer app
    </BevelButton>
  </div>

  {#if !config.base_domain}
    <div class="nx-banner info">
      <span>Configura primero un dominio base para poder exponer apps.</span>
    </div>
  {/if}

  {#if apps.length === 0}
    <EmptyState icon="◇" title="Ninguna app expuesta" hint="Pulsa «Exponer app» para hacer accesible un servicio desde fuera de tu red." />
  {:else}
    <div class="nx-apps">
      {#each apps as app (app.id)}
        {@const cert = certForApp(app, config.base_domain, certs)}
        {@const state = appState(app, cert, caddyReachable)}
        <div class="nx-app" class:paused={!app.enabled}>
          <div class="nx-app-mark"></div>

          <div class="nx-app-body">
            <div class="nx-app-top">
              <span class="nx-app-name">{app.display_name || app.app_id}</span>
              <span class="nx-app-badge">
                <LED variant={ledVariantFor(state.kind)} size={7} pulse={state.kind === 'applying'} />
                <Badge variant={badgeVariantFor(state.kind)} size="sm">{state.label}</Badge>
              </span>
            </div>

            <div class="nx-app-route mono">
              {#if config.base_domain}
                {@const url = appURL(app, config.base_domain, config.https_port, app.landing_path)}
                <a class="nx-app-url" href={url} target="_blank" rel="noopener noreferrer">{url}</a>
              {:else}
                <span class="nx-app-nodomain">(sin dominio base)</span>
              {/if}
              <span class="nx-arrow">→</span>
              {app.upstream_host}:{app.upstream_port}
            </div>

            {#if cert}
              <div class="nx-app-cert">
                <span class="nx-cert-icon">🔒</span>
                <span class="mono">{cert.issuer || 'cert'}</span>
                {#if typeof cert.days_left === 'number'}
                  <span class="nx-cert-sep">·</span>
                  <span class:nx-cert-warn={cert.days_left < 15}>expira en {cert.days_left}d</span>
                {/if}
              </div>
            {/if}
          </div>

          <div class="nx-app-actions">
            <button class="nx-act" disabled={busy} on:click={() => dispatch('toggle', { app, enabled: !app.enabled })}>
              {app.enabled ? 'Pausar' : 'Activar'}
            </button>
            {#if app.enabled}
              {@const inst = installedApps.find((x) => x.id === app.app_id)}
              {#if inst && inst.accessMode === 'caddy_only'}
                <button class="nx-act nx-locked" disabled={busy} title="El puerto directo está cerrado (bind 127.0.0.1) — solo se llega vía Caddy. Click para reabrir en LAN."
                  on:click={() => dispatch('lock', { appId: inst.id, mode: 'lan' })}>🔒 solo Caddy</button>
              {:else if inst}
                <button class="nx-act" disabled={busy} title="Cerrar el puerto directo de la LAN: NimOS recrea el stack con bind 127.0.0.1 y Caddy queda como única puerta."
                  on:click={() => dispatch('lock', { appId: inst.id, mode: 'caddy_only' })}>Cerrar LAN</button>
              {/if}
            {/if}
            <button class="nx-act" disabled={busy} on:click={() => dispatch('edit', { app })}>Editar</button>
            <button class="nx-act danger" disabled={busy} on:click={() => dispatch('remove', { app })}>Quitar</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if msg}
    <div class="nx-msg">{msg}</div>
  {/if}
</div>

<style>
  .nx-section { display: flex; flex-direction: column; }

  .nx-strip {
    display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
    padding: 10px 14px; margin-bottom: 16px;
    border: 1px solid var(--bd, rgba(255,255,255,0.06)); border-radius: 6px;
    background: var(--bg-inner, #101015);
  }
  .nx-pill {
    font-size: 11px; padding: 2px 9px; border-radius: 99px;
    border: 1px solid var(--bd-3, #2a2a32); color: var(--fg-4, #7a7a82);
  }
  .nx-pill.ok {
    border-color: rgba(0,255,159,0.4); color: var(--nim-green, #00ff9f);
    background: rgba(0,255,159,0.06);
  }
  .nx-strip-hint { margin-left: auto; font-size: 11px; color: var(--fg-4, #7a7a82); }
  .nx-strip-hint.warn { color: var(--st-warn, #ffc857); }

  /* ── Config ── */
  .nx-config {
    background: var(--bg-card, #15151a);
    border-radius: 10px;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-bottom: 8px;
  }
  .nx-config-row { display: flex; gap: 16px; align-items: flex-start; }
  .nx-label {
    font-size: 11px; color: var(--fg-3, #9c9ca4); font-weight: 500;
    letter-spacing: 0.4px; min-width: 110px; padding-top: 8px;
  }
  .nx-field { flex: 1; display: flex; flex-direction: column; gap: 5px; }
  .nx-hint { font-size: 11px; color: var(--fg-4, #7a7a82); }

  /* Selector de dominio DDNS */
  .nx-domain-select {
    width: 100%;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 7px;
    padding: 9px 12px;
    color: var(--fg, #f0f0f0);
    font-family: ui-monospace, monospace;
    font-size: 13px;
    outline: none;
    cursor: pointer;
    transition: border-color 0.12s;
  }
  .nx-domain-select:focus { border-color: rgba(0,255,159,0.35); }
  .nx-domain-select:disabled { opacity: 0.5; cursor: not-allowed; }

  .nx-domain-loading {
    font-size: 12px; color: var(--fg-4, #7a7a82);
    font-family: ui-monospace, monospace; padding: 9px 0;
  }

  /* Aviso "sin dominios DDNS" */
  .nx-domain-warn {
    display: flex; gap: 10px; align-items: flex-start;
    padding: 11px 13px; border-radius: 8px;
    background: rgba(255,200,87,0.06); border: 1px solid rgba(255,200,87,0.22);
  }
  .nx-warn-icon {
    width: 18px; height: 18px; border-radius: 4px; flex-shrink: 0;
    background: var(--st-warn, #ffc857); color: var(--bg-window, #16161a);
    display: flex; align-items: center; justify-content: center;
    font-weight: 700; font-size: 12px; font-family: ui-monospace, monospace;
  }
  .nx-warn-text { font-size: 12px; color: var(--fg-2, #d0d0d4); line-height: 1.5; }

  .nx-link {
    background: none; border: none; padding: 0; cursor: pointer;
    color: var(--nim-green, #00ff9f); font-size: inherit; font-family: inherit;
    text-decoration: underline; text-underline-offset: 2px;
  }
  .nx-link:hover { filter: brightness(1.15); }

  .nx-toggle {
    display: inline-flex; align-items: center; gap: 9px; align-self: flex-start;
    padding: 6px 12px 6px 7px; border-radius: 20px; cursor: pointer;
    background: rgba(255,255,255,0.04); border: 1px solid var(--bd-3, #2a2a32);
    font-family: ui-monospace, monospace; font-size: 11px; letter-spacing: 0.5px;
    color: var(--fg-3, #9c9ca4); transition: all 0.15s;
  }
  .nx-toggle-knob {
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--fg-4, #7a7a82); transition: all 0.15s;
  }
  .nx-toggle.on { color: var(--nim-green, #00ff9f); border-color: rgba(0,255,159,0.4); background: rgba(0,255,159,0.08); }
  .nx-toggle.on .nx-toggle-knob { background: var(--nim-green, #00ff9f); transform: translateX(2px); }
  .nx-toggle:disabled { opacity: 0.5; cursor: default; }

  .nx-config-save { display: flex; justify-content: flex-end; }

  .nx-app-url { color: var(--nim-remote, #4db8ff); text-decoration: none; }
  .nx-app-url:hover { text-decoration: underline; }
  .nx-app-nodomain { color: var(--fg-4, #7a7a82); }

  .nx-ports { display: flex; gap: 14px; }
  .nx-port { display: flex; align-items: center; gap: 8px; }
  .nx-port-lbl { font-size: 10px; color: var(--fg-4, #7a7a82); letter-spacing: 0.5px; }
  .nx-port :global(input) { width: 80px; }
  .nx-err { font-size: 11px; color: var(--st-crit, #ff5a5a); }

  /* ── Banners ── */
  .nx-banner {
    display: flex; align-items: center; gap: 10px;
    padding: 10px 14px; border-radius: 8px; font-size: 12px;
    margin: 10px 0; color: var(--fg-2, #d0d0d4);
  }
  .nx-banner.warn { background: rgba(255,200,87,0.08); border: 1px solid rgba(255,200,87,0.25); }
  .nx-banner.info { background: rgba(77,184,255,0.07); border: 1px solid rgba(77,184,255,0.2); }

  /* ── Apps ── */
  .nx-apps-head { display: flex; align-items: center; justify-content: space-between; margin-top: 18px; }
  .nx-apps { display: flex; flex-direction: column; gap: 8px; margin-top: 4px; }

  .nx-app {
    display: grid; grid-template-columns: 4px 1fr auto; gap: 14px;
    background: var(--bg-card, #15151a); border-radius: 10px;
    padding: 14px 16px; align-items: center;
  }
  .nx-app.paused { opacity: 0.6; }
  .nx-app-mark {
    width: 4px; align-self: stretch; border-radius: 2px;
    background: var(--nim-green, #00ff9f);
  }
  .nx-app.paused .nx-app-mark { background: var(--fg-5, #5a5a62); }

  .nx-app-body { display: flex; flex-direction: column; gap: 5px; min-width: 0; }
  .nx-app-top { display: flex; align-items: center; gap: 10px; }
  .nx-app-name { font-size: 14px; color: var(--fg, #f0f0f0); font-weight: 500; }
  .nx-app-badge { display: inline-flex; align-items: center; gap: 6px; }

  .nx-app-route {
    font-size: 11px; color: var(--fg-3, #9c9ca4);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .nx-arrow { color: var(--fg-5, #5a5a62); margin: 0 4px; }

  .nx-app-cert { display: flex; align-items: center; gap: 6px; font-size: 11px; color: var(--fg-3, #9c9ca4); }
  .nx-cert-icon { font-size: 10px; }
  .nx-cert-sep { color: var(--fg-5, #5a5a62); }
  .nx-cert-warn { color: var(--st-warn, #ffc857); }

  .nx-app-actions { display: flex; gap: 6px; flex-shrink: 0; }
  .nx-locked {
    border-color: var(--nim-green, #00ff9f) !important;
    color: var(--nim-green, #00ff9f) !important;
    background: rgba(0, 255, 159, 0.06);
  }
  .nx-act {
    font-family: ui-monospace, monospace; font-size: 10px; padding: 5px 10px;
    background: transparent; color: var(--fg-3, #9c9ca4);
    border: 1px solid var(--bd-3, #2a2a32); border-radius: 5px; cursor: pointer;
    letter-spacing: 0.3px; transition: all 0.12s;
  }
  .nx-act:hover:not(:disabled) { border-color: #4a4a52; color: var(--fg-2, #d0d0d4); }
  .nx-act.danger:hover:not(:disabled) { border-color: var(--st-crit, #ff5a5a); color: var(--st-crit, #ff5a5a); }
  .nx-act:disabled { opacity: 0.4; cursor: default; }

  .nx-msg {
    margin-top: 12px; font-size: 12px; color: var(--fg-3, #9c9ca4);
    font-family: ui-monospace, monospace;
  }
</style>
