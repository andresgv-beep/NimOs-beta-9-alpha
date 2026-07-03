<script>
  /**
   * Launcher · Launcher a pantalla completa NimOS Beta 9 · estilo Ubuntu 25/26
   * ──────────────────────────────────────────────────────────────────────────
   * Se abre desde el logo NimOS del taskbar. Cubre toda la pantalla con el
   * wallpaper difuminado detrás.
   *
   * Estética:
   *   - Overlay full-screen con backdrop-blur (activities-style)
   *   - Buscador centrado arriba (foco automático, filtra en vivo)
   *   - Grid ÚNICO plano con TODAS las apps (sistema + docker juntas)
   *   - Paginación horizontal con puntos (rueda del ratón / clic en punto)
   *   - Iconos grandes a sangre + nombre debajo, puntito si está abierta
   *   - Esquina: usuario + botón power · pista de "esc"
   *
   * Lógica preservada:
   *   - APP_META + listAllApps de $lib/apps.js
   *   - fetch /api/my-apps · permisos de usuario
   *   - fetchLaunchable · apps Docker
   *   - openWindow + windowList de $lib/stores/windows.js
   *   - Keyboard: Esc cierra · Enter abre la primera coincidencia
   */
  import { tick } from 'svelte';
  import { APP_META, listAllApps } from '$lib/apps.js';
  import { openWindow, windowList } from '$lib/stores/windows.js';
  import { getToken, logout, user } from '$lib/stores/auth.js';
  import AppIcon from '$lib/ui/AppIcon.svelte';
  import { fetchLaunchable, normalizeLaunchable, openApp } from '$lib/apps/appstore/launchApp.js';

  export let visible = false;

  let dockerApps = [];
  let allowedApps = null;

  // ─── Búsqueda ───
  let query = '';
  let searchEl;

  // ─── Paginación ───
  let page = 0;
  // Tamaño de la ventana → columnas/filas adaptativas
  let winW = 1280;
  let winH = 800;
  // Celda ≈ 116px ancho, 148px alto (icono + nombre + gap)
  $: cols = Math.max(4, Math.min(9, Math.floor((winW * 0.82) / 148)));
  $: rows = Math.max(2, Math.min(6, Math.floor((winH - 300) / 150)));
  $: pageSize = cols * rows;

  $: if (visible) {
    loadDockerApps();
    loadMyApps();
  }

  // Al abrir: reset de estado + foco en el buscador
  $: if (visible) {
    query = '';
    page = 0;
    tick().then(() => searchEl && searchEl.focus());
  }

  async function loadMyApps() {
    try {
      const res = await fetch('/api/my-apps', {
        headers: { 'Authorization': `Bearer ${getToken()}` },
      });
      const data = await res.json();
      allowedApps = data.apps;
    } catch {
      allowedApps = 'all';
    }
  }

  async function loadDockerApps() {
    try {
      const list = await fetchLaunchable();
      dockerApps = list.map((app) => {
        const n = normalizeLaunchable(app);
        return {
          ...n,
          icon: n.icon || '📦',
          fallback: '📦',
          port: n.localPort,
          isWebApp: true,
          category: 'docker',
          description: 'app docker',
        };
      });
    } catch {}
  }

  function canAccess(appId) {
    if (allowedApps === 'all') return true;
    if (Array.isArray(allowedApps)) return allowedApps.includes(appId);
    return true;
  }

  // Grid ÚNICO plano: apps de sistema + usuario + docker, todas juntas.
  $: systemApps = listAllApps()
    .map(a => ({ ...a, isSystem: true }))
    .filter(a => !a.hidden && canAccess(a.id));

  $: allApps = [...systemApps, ...dockerApps.filter(a => canAccess(a.id))];

  // Filtro de búsqueda (nombre o id, sin acentos/caso)
  function norm(s) {
    return (s || '').toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '');
  }
  $: q = norm(query.trim());
  $: filtered = q
    ? allApps.filter(a => norm(a.name).includes(q) || norm(a.id).includes(q))
    : allApps;

  // Páginas
  $: pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  $: safePage = Math.min(page, pageCount - 1);
  $: pageApps = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);

  // Al cambiar la búsqueda, volver a la primera página
  $: if (q !== undefined) page = 0;

  $: openAppIds = new Set($windowList.map(w => w.appId));

  function goPage(i) {
    page = Math.max(0, Math.min(pageCount - 1, i));
  }

  function onWheel(e) {
    if (pageCount <= 1) return;
    if (e.deltaY > 0 || e.deltaX > 0) goPage(safePage + 1);
    else if (e.deltaY < 0 || e.deltaX < 0) goPage(safePage - 1);
  }

  function launch(app) {
    visible = false;
    if (app.openMode === 'game') {
      openWindow(app.id, { width: 600, height: 540 }, {
        gameData: { appId: app.id, appName: app.name, appIcon: app.icon },
      });
      return;
    }
    if (app.isWebApp) {
      openApp(app);
      return;
    } else {
      const meta = APP_META[app.id];
      openWindow(app.id, { width: meta?.width || 800, height: meta?.height || 520 });
    }
  }

  function handleKeydown(e) {
    if (!visible) return;
    if (e.key === 'Escape') {
      visible = false;
    } else if (e.key === 'Enter') {
      const first = filtered[0];
      if (first) launch(first);
    } else if (e.key === 'ArrowRight' && !query) {
      goPage(safePage + 1);
    } else if (e.key === 'ArrowLeft' && !query) {
      goPage(safePage - 1);
    }
  }

  function handlePower() {
    visible = false;
    logout();
  }

  $: userName = $user?.username || 'usuario';
  $: userInitial = userName.charAt(0).toUpperCase();
</script>

<svelte:window on:keydown={handleKeydown} bind:innerWidth={winW} bind:innerHeight={winH} />

{#if visible}
  <div class="launcher" on:click={() => (visible = false)} on:wheel={onWheel} role="presentation">

    <!-- ─── Buscador ─── -->
    <div class="lx-search-wrap" on:click|stopPropagation role="presentation">
      <div class="lx-search">
        <svg class="lx-search-ic" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="11" cy="11" r="7" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          class="lx-search-input"
          bind:this={searchEl}
          bind:value={query}
          type="text"
          placeholder="Buscar aplicaciones…"
          spellcheck="false"
          autocomplete="off"
        />
      </div>
    </div>

    <!-- ─── Grid de apps · clic en vacío cierra (los tiles paran con su launch) ─── -->
    <div class="lx-stage" role="presentation">
      {#if pageApps.length > 0}
        <div class="lx-grid" style="grid-template-columns: repeat({cols}, 116px);">
          {#each pageApps as app (app.id)}
            <button class="app-tile" on:click={() => launch(app)} title={app.name}>
              <div class="app-tile-ico">
                <AppIcon src={app.icon} alt={app.name} fallback={app.fallback || '📦'} size="md" />
              </div>
              <span class="app-tile-name">{app.name}</span>
              {#if openAppIds.has(app.id) || app.running}
                <span class="app-tile-running"></span>
              {/if}
            </button>
          {/each}
        </div>
      {:else}
        <div class="empty">
          <div class="empty-ic">◌</div>
          <div class="empty-msg">
            {q ? `Sin resultados para “${query.trim()}”` : 'Sin apps disponibles'}
          </div>
        </div>
      {/if}
    </div>

    <!-- ─── Puntos de paginación ─── -->
    {#if pageCount > 1}
      <div class="lx-dots" on:click|stopPropagation role="presentation">
        {#each Array(pageCount) as _, i}
          <button
            class="lx-dot"
            class:active={i === safePage}
            on:click={() => goPage(i)}
            aria-label={`Página ${i + 1}`}
          ></button>
        {/each}
      </div>
    {/if}

    <!-- ─── Esquina inferior · usuario + power ─── -->
    <div class="lx-user" on:click|stopPropagation role="presentation">
      <div class="lx-user-avatar">{userInitial}</div>
      <div class="lx-user-info">
        <span class="lx-user-name">{userName}</span>
        <span class="lx-user-status">online</span>
      </div>
      <button class="lx-power" on:click={handlePower} title="Cerrar sesión">⏻</button>
    </div>

    <button class="lx-close" on:click|stopPropagation={() => (visible = false)} title="Cerrar" aria-label="Cerrar">
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
        <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
      </svg>
    </button>

  </div>
{/if}

<style>
  /* ═══════════════════════════════════════════════════════════
     LAUNCHER · overlay a pantalla completa (activities-style)
     ═══════════════════════════════════════════════════════════ */
  .launcher {
    position: fixed;
    inset: 0;
    z-index: 9200;
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 7vh 4vw 4vh;
    background: rgba(12, 12, 16, 0.55);
    backdrop-filter: blur(30px) saturate(1.25);
    -webkit-backdrop-filter: blur(30px) saturate(1.25);
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
    animation: lx-in 0.22s cubic-bezier(0.2, 0, 0, 1.1);
  }

  @keyframes lx-in {
    from { opacity: 0; transform: scale(1.03); }
    to   { opacity: 1; transform: scale(1); }
  }

  /* ─── Buscador ─── */
  .lx-search-wrap {
    width: 100%;
    display: flex;
    justify-content: center;
    margin-bottom: 5vh;
    flex-shrink: 0;
  }
  .lx-search {
    width: min(460px, 82vw);
    height: 46px;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 16px;
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 6px;
    transition: border-color 0.15s, background 0.15s;
  }
  .lx-search:focus-within {
    border-color: rgba(0, 255, 159, 0.35);
    background: rgba(255, 255, 255, 0.08);
  }
  .lx-search-ic { color: var(--ink-faint, #7a7a82); flex-shrink: 0; }
  .lx-search:focus-within .lx-search-ic { color: var(--signal, #00ff9f); }
  .lx-search-input {
    flex: 1;
    background: transparent;
    border: none;
    outline: none;
    color: var(--ink, #e8e8ea);
    font-family: inherit;
    font-size: 14px;
    letter-spacing: 0.2px;
  }
  .lx-search-input::placeholder { color: var(--ink-faint, #6a6a72); }

  /* ─── Grid ─── */
  .lx-stage {
    flex: 1;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 0;
  }
  .lx-grid {
    display: grid;
    gap: 34px 26px;
    justify-content: center;
  }

  .app-tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
    width: 116px;
    padding: 14px 6px;
    border-radius: 14px;
    cursor: pointer;
    position: relative;
    background: transparent;
    border: none;
    color: inherit;
    font-family: inherit;
    transition: background 0.12s;
  }
  .app-tile:hover { background: rgba(255, 255, 255, 0.06); }
  .app-tile:hover .app-tile-ico { transform: scale(1.06); }
  .app-tile:focus-visible {
    outline: none;
    background: rgba(0, 255, 159, 0.1);
  }

  .app-tile-ico {
    width: 76px;
    height: 76px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #fff;
    transition: transform 0.15s;
    flex-shrink: 0;
    overflow: hidden;
  }
  .app-tile-ico :global(.app-icon-frame) {
    width: 100%;
    height: 100%;
  }

  .app-tile-name {
    font-size: 12px;
    color: var(--ink-dim, #c4c4cc);
    text-align: center;
    font-weight: 400;
    line-height: 1.2;
    letter-spacing: 0.1px;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    width: 100%;
  }

  .app-tile-running {
    position: absolute;
    top: 8px;
    right: 26px;
    width: 6px;
    height: 6px;
    background: var(--signal, #00ff9f);
    border-radius: 50%;
    box-shadow: 0 0 5px var(--signal, #00ff9f);
  }

  /* ─── Puntos de paginación ─── */
  .lx-dots {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 3vh;
    flex-shrink: 0;
  }
  .lx-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    padding: 0;
    border: none;
    cursor: pointer;
    background: rgba(255, 255, 255, 0.22);
    transition: background 0.15s, width 0.2s;
  }
  .lx-dot:hover { background: rgba(255, 255, 255, 0.4); }
  .lx-dot.active {
    width: 22px;
    border-radius: 4px;
    background: var(--signal, #00ff9f);
  }

  /* ─── Empty ─── */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 14px;
    color: var(--ink-faint, #6a6a72);
  }
  .empty-ic { font-size: 40px; opacity: 0.5; }
  .empty-msg { font-size: 13px; text-align: center; }

  /* ─── Esquina · usuario + power ─── */
  .lx-user {
    position: absolute;
    left: 24px;
    bottom: 22px;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 8px;
    border-radius: 6px;
  }
  .lx-user-avatar {
    width: 30px;
    height: 30px;
    border-radius: 50%;
    background: var(--signal, #00ff9f);
    color: #0d0d11;
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 12px;
    flex-shrink: 0;
  }
  .lx-user-info { display: flex; flex-direction: column; gap: 1px; }
  .lx-user-name { font-size: 13px; color: var(--ink, #e8e8ea); font-weight: 500; }
  .lx-user-status {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px;
    color: var(--signal, #00ff9f);
    letter-spacing: 0.3px;
  }
  .lx-power {
    width: 32px;
    height: 32px;
    margin-left: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 6px;
    cursor: pointer;
    color: var(--ink-mute, #9a9aa3);
    font-size: 14px;
    border: 1px solid transparent;
    background: transparent;
    transition: all 0.12s;
  }
  .lx-power:hover {
    color: var(--crit, #ff5a5a);
    background: rgba(255, 90, 90, 0.08);
    border-color: rgba(255, 90, 90, 0.25);
  }

  /* ─── Botón cerrar (arriba-derecha) ─── */
  .lx-close {
    position: absolute;
    top: 22px;
    right: 24px;
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 6px;
    cursor: pointer;
    color: var(--ink-mute, #9a9aa3);
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.1);
    transition: all 0.12s;
  }
  .lx-close:hover {
    color: var(--crit, #ff5a5a);
    background: rgba(255, 90, 90, 0.1);
    border-color: rgba(255, 90, 90, 0.3);
  }
</style>
