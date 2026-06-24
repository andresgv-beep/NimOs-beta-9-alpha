<script>
  // MobileApps — apps gestionables. Separa las nativas de NimOS (servicios
  // propios tipo NimTorrent/NimBackup) de las de Docker, que van agrupadas en
  // un submenú colapsable. Usa /api/services (árbol con children) y las
  // acciones /api/services/:id/:action para arrancar/parar.
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  let services = [];
  let loading = true;
  let busyId = null;
  let dockerOpen = false;

  function flatten(raw) {
    const flat = [];
    for (const svc of raw) {
      flat.push(svc);
      if (svc.children && svc.children.length) {
        for (const c of svc.children) flat.push({ ...c, _isChild: true });
      }
    }
    return flat;
  }

  async function load() {
    loading = true;
    try {
      const r = await fetch('/api/services', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        services = flatten(d.services || d || []);
      }
    } catch (e) {
      // vista degradada
    } finally {
      loading = false;
    }
  }

  function isRunning(s) {
    const st = (s.status || s.state || '').toLowerCase();
    return st === 'running' || st === 'active' || st === 'up' || s.running === true;
  }

  function isDocker(s) {
    const t = (s.type || '').toLowerCase();
    return t === 'docker' || t === 'docker-app';
  }

  // Nativas de NimOS: servicios propios gestionables (no docker, no infra de
  // sistema base como ssh/samba que el usuario no enciende/apaga aquí).
  const NIMOS_APPS = ['nimtorrent', 'nimbackup', 'nimshield', 'nimsync', 'immich'];
  function isNimApp(s) {
    if (isDocker(s)) return false;
    const id = (s.id || s.name || '').toLowerCase();
    return NIMOS_APPS.some((n) => id.includes(n));
  }

  $: nativeApps = services.filter(isNimApp);
  $: dockerApps = services.filter(isDocker);

  async function toggle(app) {
    if (busyId) return;
    busyId = app.id;
    const action = isRunning(app) ? 'stop' : 'start';
    try {
      const r = await fetch(`/api/services/${app.id}/${action}`, { method: 'POST', headers: hdrs() });
      if (r.ok) await load();
    } catch (e) {
      // sin cambios
    } finally {
      busyId = null;
    }
  }

  const COLORS = ['green', 'blue', 'violet', 'orange', 'pink', 'teal'];
  function colorFor(id) {
    let h = 0;
    for (let i = 0; i < (id || '').length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
    return COLORS[h % COLORS.length];
  }

  onMount(load);
</script>

<section class="m-section">
  <div class="section-t">
    Apps NimOS {#if !loading}· {nativeApps.filter(isRunning).length} activas{/if}
  </div>

  {#if loading}
    {#each Array(3) as _}<div class="row-skeleton"></div>{/each}
  {:else if nativeApps.length === 0}
    <div class="empty"><div class="empty-t">Sin apps de NimOS</div></div>
  {:else}
    {#each nativeApps as app (app.id)}
      <div class="app-row">
        <div class="app-row-ico {colorFor(app.id)}">
          <svg viewBox="0 0 24 24"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
        </div>
        <div class="app-row-info">
          <div class="app-row-name">{app.name || app.id}</div>
          <div class="app-row-state {isRunning(app) ? 'state-on' : 'state-off'}">
            <span class="dot"></span>{isRunning(app) ? 'corriendo' : 'detenida'}
          </div>
        </div>
        <button class="toggle {isRunning(app) ? 'on' : 'off'}" class:busy={busyId === app.id}
                on:click={() => toggle(app)} disabled={busyId === app.id}
                aria-label={isRunning(app) ? 'Detener' : 'Iniciar'}></button>
      </div>
    {/each}
  {/if}

  {#if dockerApps.length > 0}
    <button class="docker-head" on:click={() => (dockerOpen = !dockerOpen)}>
      <span class="docker-title">Docker · {dockerApps.length}</span>
      <svg class="docker-chev" class:open={dockerOpen} viewBox="0 0 24 24"><polyline points="6 9 12 15 18 9"/></svg>
    </button>

    {#if dockerOpen}
      {#each dockerApps as app (app.id)}
        <div class="app-row">
          <div class="app-row-ico {colorFor(app.id)}">
            <svg viewBox="0 0 24 24"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
          </div>
          <div class="app-row-info">
            <div class="app-row-name">{app.name || app.id}</div>
            <div class="app-row-state {isRunning(app) ? 'state-on' : 'state-off'}">
              <span class="dot"></span>{isRunning(app) ? 'corriendo' : 'detenida'}
            </div>
          </div>
          <button class="toggle {isRunning(app) ? 'on' : 'off'}" class:busy={busyId === app.id}
                  on:click={() => toggle(app)} disabled={busyId === app.id}
                  aria-label={isRunning(app) ? 'Detener' : 'Iniciar'}></button>
        </div>
      {/each}
    {/if}
  {/if}
</section>

<style>
  .m-section { padding-bottom: 8px; }
  .section-t { font-size: 11px; color: var(--ink-mute); font-family: var(--font-mono); text-transform: uppercase; letter-spacing: 0.8px; font-weight: 600; margin: 18px 2px 12px; }
  .row-skeleton { height: 68px; background: var(--bg-card); border: 1px solid var(--line); border-radius: 11px; margin-bottom: 9px; opacity: 0.4; }
  .empty { text-align: center; padding: 40px 20px; }
  .empty-t { font-size: 14px; color: var(--ink-dim); }

  .app-row { display: flex; align-items: center; gap: 13px; background: var(--bg-card); border: 1px solid var(--line); border-radius: 11px; padding: 13px 15px; margin-bottom: 9px; }
  .app-row-ico { width: 42px; height: 42px; border-radius: 11px; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .app-row-ico svg { width: 21px; height: 21px; stroke: #fff; stroke-width: 1.7; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .app-row-ico.green { background: linear-gradient(135deg, #00d98a, #00a866); }
  .app-row-ico.blue { background: linear-gradient(135deg, #4db8ff, #2a90e8); }
  .app-row-ico.violet { background: linear-gradient(135deg, #9a7aff, #6d4ae8); }
  .app-row-ico.orange { background: linear-gradient(135deg, #ffb04d, #f7913f); }
  .app-row-ico.pink { background: linear-gradient(135deg, #ff7a9a, #e8487a); }
  .app-row-ico.teal { background: linear-gradient(135deg, #2ad9c8, #1ba898); }
  .app-row-info { flex: 1; min-width: 0; }
  .app-row-name { font-size: 14px; font-weight: 600; color: var(--ink); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .app-row-state { font-size: 11px; font-family: var(--font-mono); margin-top: 2px; display: flex; align-items: center; gap: 5px; }
  .app-row-state .dot { width: 7px; height: 7px; border-radius: 2px; }
  .state-on { color: var(--signal); } .state-on .dot { background: var(--signal); }
  .state-off { color: var(--ink-faint); } .state-off .dot { background: var(--ink-trace); }

  .toggle { width: 44px; height: 25px; border-radius: 6px; position: relative; flex-shrink: 0; border: none; cursor: pointer; transition: background 0.15s; }
  .toggle.on { background: var(--signal); }
  .toggle.off { background: var(--line-bright); }
  .toggle::after { content: ''; position: absolute; top: 3px; width: 19px; height: 19px; border-radius: 4px; transition: all 0.15s; }
  .toggle.on::after { right: 3px; background: var(--bg-window); }
  .toggle.off::after { left: 3px; background: var(--ink-mute); }
  .toggle.busy { opacity: 0.5; }

  .docker-head {
    width: 100%; display: flex; align-items: center; justify-content: space-between;
    background: var(--bg-inner); border: 1px solid var(--line); border-radius: 10px;
    padding: 13px 15px; margin: 16px 0 9px; cursor: pointer;
  }
  .docker-title { font-size: 12px; font-family: var(--font-mono); color: var(--ink-dim); text-transform: uppercase; letter-spacing: 0.8px; font-weight: 600; }
  .docker-chev { width: 16px; height: 16px; stroke: var(--ink-faint); stroke-width: 2; fill: none; transition: transform 0.15s; }
  .docker-chev.open { transform: rotate(180deg); }
</style>
