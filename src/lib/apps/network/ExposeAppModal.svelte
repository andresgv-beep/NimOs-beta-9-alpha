<script>
  /**
   * ExposeAppModal · Formulario para exponer una app nueva o editar una existente.
   * ──────────────────────────────────────────────────────────────────────────────
   * Con DETECCIÓN de apps instaladas: el picker lista las apps Docker de
   * NimOS (nombre + puerto) y autocompleta app_id, upstream y sugiere el
   * subdominio — cero puertos a mano. "— personalizado —" mantiene la
   * entrada manual para servicios fuera del catálogo.
   *
   * Modo:
   *   · sin `app` (null)  → crear (expone una app nueva)
   *   · con `app`         → editar (precarga los campos)
   *
   * Props:
   *   · app        — app a editar, o null para crear
   *   · baseDomain — dominio base, para previsualizar la URL final
   *   · busy       — deshabilita el submit mientras el padre llama a la API
   *   · error      — mensaje de error del último intento (lo setea el padre)
   *
   * Eventos:
   *   · submit — { detail: { id, fields } } — id null si es creación
   *   · cancel — cierra sin guardar
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import { BevelButton, TextInput } from '$lib/ui';

  export let app = null;
  export let installedApps = []; // [{id, name, icon, port}] — detectadas
  export let baseDomain = '';
  export let httpsPort = 443;
  export let busy = false;
  export let error = '';

  const dispatch = createEventDispatcher();
  const editing = !!app;

  // ─── Estado del formulario ───
  let appId = app?.app_id || '';
  let displayName = app?.display_name || '';
  let routeMode = app?.path ? 'path' : 'subdomain'; // 'subdomain' | 'path'
  let subdomain = app?.subdomain || '';
  let path = app?.path || '';
  let upstreamHost = app?.upstream_host || '127.0.0.1';
  let upstreamPort = app?.upstream_port || '';

  // ─── Picker de apps detectadas (solo en creación) ───
  // 'custom' = entrada manual; cualquier otro valor = id de app instalada.
  let picked = 'custom';

  // slug — sugerencia de subdominio a partir del nombre (minúsculas a-z0-9).
  const slug = (name) => (name || '').toLowerCase().replace(/[^a-z0-9]/g, '');

  function pickApp(value) {
    picked = value;
    if (value === 'custom') return; // manual: campos quedan editables tal cual
    const a = installedApps.find((x) => x.id === value);
    if (!a) return;
    appId = a.id;
    displayName = a.name;
    upstreamHost = '127.0.0.1';
    upstreamPort = String(a.port);
    if (!subdomain.trim()) subdomain = slug(a.name);
  }

  $: pickedApp = picked !== 'custom' ? installedApps.find((x) => x.id === picked) : null;

  // Al llegar la lista (async): si el formulario está virgen, preseleccionar
  // la primera app detectada. El guard (appId vacío) evita pisarte si ya
  // empezaste a escribir o si re-renderiza.
  $: if (!editing && installedApps.length > 0 && picked === 'custom' && !appId.trim()) {
    pickApp(installedApps[0].id);
  }

  // ─── Validación en vivo ───
  $: portNum = parseInt(upstreamPort, 10);
  $: portValid = !isNaN(portNum) && portNum > 0 && portNum <= 65535;
  $: routeValid = routeMode === 'subdomain' ? subdomain.trim() !== '' : path.trim() !== '';
  $: canSubmit = appId.trim() !== '' && upstreamHost.trim() !== '' && portValid && routeValid && !busy;

  // ─── Previsualización de la URL final ───
  $: portPart = httpsPort && httpsPort !== 443 ? `:${httpsPort}` : '';
  $: preview = (() => {
    if (!baseDomain) return '(configura un dominio base primero)';
    if (routeMode === 'subdomain') {
      return subdomain.trim() ? `https://${subdomain.trim()}.${baseDomain}${portPart}` : `https://‹subdominio›.${baseDomain}${portPart}`;
    }
    const p = path.trim();
    const norm = p ? (p.startsWith('/') ? p : '/' + p) : '/‹ruta›';
    return `https://${baseDomain}${portPart}${norm}`;
  })();

  function submit() {
    if (!canSubmit) return;
    const fields = {
      displayName: displayName.trim() || appId.trim(),
      upstreamHost: upstreamHost.trim(),
      upstreamPort: portNum,
    };
    // Enviar solo el método de enrutado elegido; vaciar el otro.
    if (routeMode === 'subdomain') {
      fields.subdomain = subdomain.trim();
      fields.path = '';
    } else {
      fields.path = path.trim().startsWith('/') ? path.trim() : '/' + path.trim();
      fields.subdomain = '';
    }
    if (!editing) {
      fields.appId = appId.trim();
    }
    dispatch('submit', { id: app?.id || null, fields });
  }

  function onKeydown(e) {
    if (e.key === 'Escape') dispatch('cancel');
    if (e.key === 'Enter' && canSubmit) submit();
  }

  onMount(() => window.addEventListener('keydown', onKeydown));
  onDestroy(() => window.removeEventListener('keydown', onKeydown));
</script>

<div class="ex-overlay" on:click|self={() => dispatch('cancel')}>
  <div class="ex-modal">
    <div class="ex-band"></div>

    <div class="ex-head">
      <h2 class="ex-title">{editing ? 'Editar exposición' : 'Exponer app'}</h2>
      <span class="ex-sub">{editing ? appId : 'Hacer accesible un servicio desde fuera de tu red'}</span>
    </div>

    <div class="ex-body">
      {#if !editing}
        <!-- Picker: apps Docker detectadas -->
        <div class="ex-field">
          <label class="ex-label">App a exponer</label>
          <select class="ex-select mono" disabled={busy} value={picked} on:change={(e) => pickApp(e.target.value)}>
            {#each installedApps as a (a.id)}
              <option value={a.id}>{a.name} · :{a.port}</option>
            {/each}
            <option value="custom">— personalizado —</option>
          </select>
          {#if installedApps.length === 0}
            <span class="ex-hint">No se han detectado apps Docker con puerto · entrada manual.</span>
          {:else if pickedApp}
            <span class="ex-hint">Detectada de tus apps instaladas · upstream autocompletado.</span>
          {/if}
        </div>
      {/if}

      {#if editing || picked === 'custom'}
        <!-- app_id (manual) -->
        <div class="ex-field">
          <label class="ex-label">Identificador de la app</label>
          <TextInput
            value={appId}
            placeholder="ej. immich, gitea, jellyfin"
            disabled={busy || editing}
            onInput={(e) => (appId = e.target.value)}
          />
          {#if editing}<span class="ex-hint">El identificador no se puede cambiar.</span>{/if}
        </div>
      {/if}

      {#if editing || picked === 'custom'}
        <!-- display_name -->
        <div class="ex-field">
          <label class="ex-label">Nombre visible <span class="ex-opt">(opcional)</span></label>
          <TextInput
            value={displayName}
            placeholder="ej. Immich Fotos"
            disabled={busy}
            onInput={(e) => (displayName = e.target.value)}
          />
        </div>
      {/if}

      <!-- Routing mode -->
      <div class="ex-field">
        <label class="ex-label">Método de enrutado</label>
        <div class="ex-segs">
          <button class="ex-seg" class:on={routeMode === 'subdomain'} disabled={busy} on:click={() => (routeMode = 'subdomain')} type="button">
            Subdominio
          </button>
          <button class="ex-seg" class:on={routeMode === 'path'} disabled={busy} on:click={() => (routeMode = 'path')} type="button">
            Ruta
          </button>
        </div>
      </div>

      <!-- Subdomain or Path -->
      {#if routeMode === 'subdomain'}
        <div class="ex-field">
          <label class="ex-label">Subdominio</label>
          <TextInput
            value={subdomain}
            placeholder="ej. immich"
            disabled={busy}
            onInput={(e) => (subdomain = e.target.value)}
          />
        </div>
      {:else}
        <div class="ex-field">
          <label class="ex-label">Ruta</label>
          <TextInput
            value={path}
            placeholder="ej. /immich"
            disabled={busy}
            onInput={(e) => (path = e.target.value)}
          />
          <span class="ex-hint">Algunas apps requieren configuración extra para funcionar bajo una subruta.</span>
        </div>
      {/if}

      <!-- Upstream -->
      {#if !editing && pickedApp}
        <div class="ex-field">
          <label class="ex-label">Upstream</label>
          <div class="ex-upstream mono">{upstreamHost}:{upstreamPort} <span class="ex-auto">· auto</span></div>
        </div>
      {:else}
        <div class="ex-field-row">
          <div class="ex-field" style="flex:2">
            <label class="ex-label">Host del servicio</label>
            <TextInput
              value={upstreamHost}
              placeholder="127.0.0.1"
              disabled={busy}
              onInput={(e) => (upstreamHost = e.target.value)}
            />
          </div>
          <div class="ex-field" style="flex:1">
            <label class="ex-label">Puerto</label>
            <TextInput
              value={upstreamPort}
              type="text"
              placeholder="2283"
              disabled={busy}
              onInput={(e) => (upstreamPort = e.target.value)}
            />
          </div>
        </div>

        {#if upstreamPort !== '' && !portValid}
          <span class="ex-err-inline">El puerto debe estar entre 1 y 65535.</span>
        {/if}
      {/if}

      <!-- Preview -->
      <div class="ex-preview">
        <span class="ex-preview-lbl">URL pública</span>
        <span class="ex-preview-url mono">{preview}</span>
      </div>

      {#if error}
        <div class="ex-error">{error}</div>
      {/if}
    </div>

    <div class="ex-foot">
      <BevelButton size="sm" onClick={() => dispatch('cancel')} disabled={busy}>Cancelar</BevelButton>
      <BevelButton size="sm" variant="primary" onClick={submit} disabled={!canSubmit}>
        {busy ? '▸ Guardando…' : editing ? '▸ Guardar cambios' : '▸ Exponer'}
      </BevelButton>
    </div>
  </div>
</div>

<style>
  .ex-overlay {
    position: fixed; inset: 0; background: rgba(0,0,0,0.55);
    display: flex; align-items: center; justify-content: center;
    z-index: 1000; padding: 20px;
  }
  .ex-modal {
    background: var(--bg-window, #16161a); border-radius: 14px;
    width: 100%; max-width: 460px; overflow: hidden;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5), 0 0 0 1px rgba(255,255,255,0.05);
    position: relative;
  }
  .ex-band { height: 3px; background: var(--nim-green, #00ff9f); opacity: 0.8; }

  .ex-head { padding: 18px 20px 14px; display: flex; flex-direction: column; gap: 3px; }
  .ex-title { font-size: 16px; font-weight: 500; color: var(--fg, #f0f0f0); margin: 0; }
  .ex-sub { font-size: 12px; color: var(--fg-4, #7a7a82); }

  .ex-body {
    padding: 0 20px 18px; display: flex; flex-direction: column; gap: 14px;
    max-height: 60vh; overflow-y: auto;
  }
  .ex-field { display: flex; flex-direction: column; gap: 5px; }
  .ex-field-row { display: flex; gap: 12px; }
  .ex-label { font-size: 11px; color: var(--fg-3, #9c9ca4); font-weight: 500; letter-spacing: 0.3px; }
  .ex-opt { color: var(--fg-5, #5a5a62); font-weight: 400; }
  .ex-hint { font-size: 10px; color: var(--fg-4, #7a7a82); }

  .ex-segs { display: flex; gap: 6px; }
  .ex-seg {
    flex: 1; padding: 7px; border-radius: 6px; cursor: pointer;
    background: rgba(255,255,255,0.03); border: 1px solid var(--bd-3, #2a2a32);
    color: var(--fg-3, #9c9ca4); font-size: 12px; transition: all 0.12s;
  }
  .ex-seg.on {
    color: var(--nim-green, #00ff9f); border-color: rgba(0,255,159,0.4);
    background: rgba(0,255,159,0.07);
  }
  .ex-seg:disabled { opacity: 0.5; cursor: default; }

  .ex-err-inline { font-size: 11px; color: var(--st-crit, #ff5a5a); margin-top: -8px; }

  .ex-preview {
    background: var(--bg-inner, #101015); border-radius: 8px; padding: 10px 12px;
    display: flex; flex-direction: column; gap: 3px;
  }
  .ex-preview-lbl { font-size: 9px; color: var(--fg-4, #7a7a82); font-weight: 500; letter-spacing: 0.6px; text-transform: uppercase; }
  .ex-preview-url { font-size: 12px; color: var(--nim-green, #00ff9f); word-break: break-all; }

  .ex-error {
    background: rgba(255,90,90,0.08); border: 1px solid rgba(255,90,90,0.25);
    border-radius: 8px; padding: 9px 12px; font-size: 12px; color: var(--st-crit, #ff5a5a);
  }

  .ex-foot {
    padding: 14px 20px; display: flex; justify-content: flex-end; gap: 8px;
    border-top: 1px solid var(--bd, rgba(255,255,255,0.04));
  }

  .ex-select {
    width: 100%; padding: 8px 12px; border-radius: 6px; cursor: pointer;
    background: var(--bg-inner, #101015); border: 1px solid var(--bd-3, #2a2a32);
    color: var(--fg, #f0f0f0); font-size: 12.5px;
    appearance: auto;
  }
  .ex-select:focus { border-color: rgba(0,255,159,0.4); outline: none; }
  .ex-select:disabled { opacity: 0.5; cursor: default; }

  .ex-upstream {
    background: var(--bg-inner, #101015); border-radius: 6px; padding: 9px 12px;
    font-size: 12.5px; color: var(--fg-2, #c8c8cf);
  }
  .ex-auto { font-size: 10px; color: var(--fg-5, #5a5a62); }

  .mono { font-family: ui-monospace, "JetBrains Mono", monospace; }
</style>
