<script>
  /**
   * Launcher · Menú de inicio NimOS Beta 8.1 · Estilo W11
   * ──────────────────────────────────────────────────────
   * Se abre desde el logo NimOS del taskbar.
   *
   * Estética:
   *   - Anclado al taskbar pero separado (bottom: 12px, left: 12px)
   *   - Esquinas redondeadas 14px (no chaflán, no tan agresivo)
   *   - Search arriba con prompt `$` verde
   *   - Apps en grid 6 columnas verticales (icono grande + nombre)
   *   - Agrupadas inline en secciones: "Sistema NimOS" y "Aplicaciones"
   *   - Sin sidebar de categorías (ruido visual innecesario)
   *   - Sin "Recomendado" ni "Anclados" (duplican escritorio y taskbar)
   *   - Footer con usuario + botón power
   *
   * Lógica preservada (sin cambios):
   *   - APP_META + listAllApps de $lib/apps.js
   *   - fetch /api/my-apps · permisos de usuario
   *   - fetch /api/docker/installed-apps · apps Docker
   *   - openWindow + windowList de $lib/stores/windows.js
   *   - Search por nombre/id
   *   - Keyboard: Esc cierra · Enter abre primera
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

  // ─── Fade de scroll (arriba/abajo) ───
  // Mostramos un degradado en cada borde solo cuando hay contenido oculto
  // en esa dirección, para que el corte se lea como "hay más, scrollea"
  // en vez de parecer recortado por el marco.
  let scrollEl;
  let atTop = true;
  let atBottom = false;

  function updateScrollFades() {
    if (!scrollEl) return;
    const { scrollTop, scrollHeight, clientHeight } = scrollEl;
    atTop = scrollTop <= 1;
    atBottom = scrollTop + clientHeight >= scrollHeight - 1;
  }

  $: if (visible) {
    loadDockerApps();
    loadMyApps();
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

  $: systemApps = listAllApps()
    .map(a => ({ ...a, isSystem: true }))
    .filter(a => !a.hidden && canAccess(a.id));

  // Apps NimOS de usuario (Notes, NimTorrent…) → van a "Aplicaciones"
  $: nimUserApps = systemApps.filter(a => a.category === 'app');

  // Sección "Sistema NimOS": solo piezas del sistema y utilidades core
  $: sysApps = systemApps.filter(a =>
    a.category === 'system' || a.category === 'utilities'
  );

  // Sección "Aplicaciones": apps NimOS de usuario + apps Docker
  $: dkApps = [...nimUserApps, ...dockerApps.filter(a => canAccess(a.id))];

  // Sin buscador: se muestran todas directamente
  $: filteredSys = sysApps;
  $: filteredDk  = dkApps;

  $: openAppIds = new Set($windowList.map(w => w.appId));

  // Recalcular los fades cuando el menú se abre o cambia el contenido
  // (al cargar dockerApps cambia la altura → puede aparecer/quitarse scroll).
  $: if (visible && (filteredSys || filteredDk)) {
    tick().then(updateScrollFades);
  }

  function launch(app) {
    visible = false;
    if (app.openMode === 'game') {
      // Servidor de juego · abre una VENTANA de NimOS (movible, con barra de
      // título y cerrar) con el Panel de Juego dentro. No es webapp, no abre
      // navegador. WindowFrame renderiza GamePanel cuando hay gameData.
      openWindow(app.id, { width: 600, height: 540 }, {
        gameData: { appId: app.id, appName: app.name, appIcon: app.icon },
      });
      return;
    }
    if (app.isWebApp) {
      // openApp decide local vs dominio (módulo compartido launchApp.js · la
      // misma lógica que usa AppStoreDetail). El backend ya compuso
      // open_url_external; aquí solo se elige según cómo entró el usuario.
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
      const first = filteredSys[0] || filteredDk[0];
      if (first) launch(first);
    }
  }

  function handlePower() {
    visible = false;
    logout();
  }

  $: userName = $user?.username || 'usuario';
  $: userInitial = userName.charAt(0).toUpperCase();
</script>

<svelte:window on:keydown={handleKeydown} />

{#if visible}
  <div class="overlay" on:click={() => visible = false} role="presentation"></div>

  <div class="start-menu" on:click|stopPropagation role="presentation" class:fade-top={!atTop} class:fade-bottom={!atBottom}>

    <!-- ─── Scrollable content ─── -->
    <div class="sm-content" bind:this={scrollEl} on:scroll={updateScrollFades}>

      {#if filteredSys.length > 0}
        <div class="sm-section-head">
          <span>Sistema NimOS</span>
          <span class="count">{filteredSys.length}</span>
        </div>

        <div class="sm-grid">
          {#each filteredSys as app}
            <button
              class="app-tile"
              on:click={() => launch(app)}
              title={app.name}
            >
              <div class="app-tile-ico sys">
                <AppIcon src={app.icon} alt={app.name} fallback={app.fallback || '📦'} size="md" />
              </div>
              <span class="app-tile-name">{app.name}</span>
              {#if openAppIds.has(app.id)}
                <span class="app-tile-running"></span>
              {/if}
            </button>
          {/each}
        </div>
      {/if}

      {#if filteredSys.length > 0 && filteredDk.length > 0}
        <div class="sm-divider"></div>
      {/if}

      {#if filteredDk.length > 0}
        <div class="sm-section-head">
          <span>Aplicaciones</span>
          <span class="count">{filteredDk.length}</span>
        </div>

        <div class="sm-grid">
          {#each filteredDk as app}
            <button
              class="app-tile"
              on:click={() => launch(app)}
              title={app.name}
            >
              <div class="app-tile-ico dk">
                <AppIcon src={app.icon} alt={app.name} fallback={app.fallback || '📦'} size="md" />
              </div>
              <span class="app-tile-name">{app.name}</span>
              {#if openAppIds.has(app.id) || app.running}
                <span class="app-tile-running"></span>
              {/if}
            </button>
          {/each}
        </div>
      {/if}

      {#if filteredSys.length === 0 && filteredDk.length === 0}
        <div class="empty">
          <div class="empty-ic">◌</div>
          <div class="empty-msg">Sin apps disponibles</div>
        </div>
      {/if}

    </div>

    <!-- ─── Bottom · User + Power ─── -->
    <div class="sm-footer">
      <div class="sm-user" role="button" tabindex="0">
        <div class="sm-user-avatar">{userInitial}</div>
        <div class="sm-user-info">
          <span class="sm-user-name">{userName}</span>
          <span class="sm-user-status">online</span>
        </div>
      </div>
      <button
        class="sm-power"
        on:click={handlePower}
        title="Cerrar sesión"
      >⏻</button>
    </div>

  </div>
{/if}

<style>
  /* ═══════════════════════════════════════════════════════════
     OVERLAY · captura click para cerrar
     ═══════════════════════════════════════════════════════════ */
  .overlay {
    position: fixed;
    inset: 0;
    background: transparent;
    z-index: 9100;
  }

  /* ═══════════════════════════════════════════════════════════
     START MENU · estilo W11, anclado al taskbar con separación
     ═══════════════════════════════════════════════════════════ */
  .start-menu {
    position: fixed;
    bottom: calc(var(--taskbar-height, 44px) + 12px);
    left: 12px;
    width: 640px;
    height: 600px;
    max-height: calc(100vh - var(--taskbar-height, 44px) - 24px);
    background: rgba(20, 20, 26, 0.72);
    backdrop-filter: blur(22px) saturate(1.3);
    -webkit-backdrop-filter: blur(22px) saturate(1.3);
    border: 1px solid rgba(255, 255, 255, 0.10);
    border-radius: 14px;
    z-index: 9200;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
    box-shadow:
      0 12px 40px rgba(0, 0, 0, 0.5),
      0 0 0 1px rgba(0, 255, 159, 0.04);
    animation: menu-in 0.2s cubic-bezier(0.2, 0, 0, 1.1);
  }

  @keyframes menu-in {
    from { opacity: 0; transform: translateY(20px); }
    to   { opacity: 1; transform: translateY(0); }
  }

  /* ─── Scrollable content ─── */
  .sm-content {
    flex: 1;
    overflow-y: auto;
    padding: 18px 22px 18px;
  }

  /* Fades de scroll · pistas visuales de contenido oculto.
     Se anclan a la ventana y se activan con .fade-top / .fade-bottom.
     pointer-events:none para no bloquear clicks sobre los iconos. */
  .start-menu::before,
  .start-menu::after {
    content: '';
    position: absolute;
    left: 1px;
    right: 6px; /* deja ver la scrollbar */
    height: 38px;
    pointer-events: none;
    z-index: 5;
    opacity: 0;
    transition: opacity 0.18s ease;
  }
  .start-menu::before {
    top: 1px;
    border-radius: 14px 14px 0 0;
    background: linear-gradient(to bottom, rgba(20, 20, 26, 0.95), transparent);
  }
  .start-menu::after {
    /* justo encima del footer (altura aprox. del sm-footer) */
    bottom: 57px;
    background: linear-gradient(to top, rgba(20, 20, 26, 0.95), transparent);
  }
  .start-menu.fade-top::before { opacity: 1; }
  .start-menu.fade-bottom::after { opacity: 1; }

  .sm-content::-webkit-scrollbar { width: 5px; }
  .sm-content::-webkit-scrollbar-track { background: transparent; }
  .sm-content::-webkit-scrollbar-thumb {
    background: var(--line);
    border-radius: 3px;
  }

  .sm-section-head {
    font-size: 10px;
    color: var(--ink-faint);
    letter-spacing: 1.5px;
    font-weight: 600;
    text-transform: uppercase;
    padding: 10px 4px 12px;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .sm-section-head .count {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--ink-trace);
    font-weight: 500;
    letter-spacing: 0.3px;
    text-transform: none;
  }

  /* App grid · 6 columns */
  .sm-grid {
    display: grid;
    grid-template-columns: repeat(5, 1fr);
    gap: 8px;
    margin-bottom: 10px;
  }

  .app-tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    padding: 18px 6px 14px;
    border-radius: 10px;
    cursor: pointer;
    transition: all 0.12s;
    position: relative;
    background: transparent;
    border: none;
    color: inherit;
    font-family: inherit;
  }
  .app-tile:hover {
    background: rgba(255, 255, 255, 0.04);
  }
  .app-tile:hover .app-tile-ico {
    transform: scale(1.05);
  }
  .app-tile:focus-visible {
    outline: none;
    background: var(--ui-select-bg, rgba(122, 158, 177, 0.12));
  }

  .app-tile-ico {
    width: 60px;
    height: 60px;
    border-radius: 13px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #fff;
    transition: transform 0.15s;
    flex-shrink: 0;
    overflow: hidden;
  }
  /* Sin recuadro: el icono respira a sangre, como en móvil/macOS */

  /* El AppIcon usa size="md" (48px) por defecto; dentro del tile lo dejamos
     crecer a toda la celda (60px) sin alterar size-md en el resto de la app. */
  .app-tile-ico :global(.app-icon-frame) {
    width: 100%;
    height: 100%;
  }
  .app-tile-ico.sys,
  .app-tile-ico.dk {
    background: transparent;
    border: none;
  }

  .app-tile-name {
    font-size: 10.5px;
    color: var(--ink-dim);
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

  /* Running indicator dot */
  .app-tile-running {
    position: absolute;
    top: 8px;
    right: 14px;
    width: 6px;
    height: 6px;
    background: var(--signal);
    border-radius: 50%;
    box-shadow: 0 0 4px var(--signal);
  }

  /* Divider between sections */
  .sm-divider {
    height: 1px;
    background: var(--line));
    margin: 8px 4px;
  }

  /* Empty state */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 40px 20px;
    gap: 12px;
    color: var(--ink-faint);
  }
  .empty-ic {
    font-size: 32px;
    opacity: 0.5;
  }
  .empty-msg {
    font-size: 12px;
    text-align: center;
  }

  /* ─── Bottom · User + Power ─── */
  .sm-footer {
    border-top: 1px solid var(--line));
    padding: 10px 14px;
    display: flex;
    align-items: center;
    gap: 10px;
    background: rgba(0, 0, 0, 0.2);
  }
  .sm-user {
    display: flex;
    align-items: center;
    gap: 10px;
    flex: 1;
    padding: 6px 8px;
    border-radius: 5px;
    cursor: pointer;
    transition: background 0.1s;
  }
  .sm-user:hover {
    background: rgba(255, 255, 255, 0.03);
  }
  .sm-user-avatar {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    background: var(--signal);
    color: var(--panel);
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 12px;
    flex-shrink: 0;
  }
  .sm-user-info {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .sm-user-name {
    font-size: 12.5px;
    color: var(--ink);
    font-weight: 500;
  }
  .sm-user-status {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px;
    color: var(--signal);
    letter-spacing: 0.3px;
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .sm-user-status::before {
    content: '';
    width: 4px;
    height: 4px;
    background: var(--signal);
    border-radius: 50%;
    box-shadow: 0 0 3px var(--signal);
  }

  .sm-power {
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 5px;
    cursor: pointer;
    color: var(--ink-mute);
    font-size: 14px;
    transition: all 0.12s;
    border: 1px solid transparent;
    background: transparent;
  }
  .sm-power:hover {
    color: var(--crit);
    background: rgba(255, 90, 90, 0.06);
    border-color: rgba(255, 90, 90, 0.2);
  }
</style>
