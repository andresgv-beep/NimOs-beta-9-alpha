<script>
  /**
   * WindowFrame · Marco de ventana NimOS Beta 8.1
   * ───────────────────────────────────────────────
   * Envuelve cada app abierta, maneja drag, resize, maximize.
   * El chrome de titlebar lo pone AppShell por dentro — WindowFrame
   * solo es el contenedor flotante con bordes técnicos NimOS.
   *
   * Estética según mockup validado nimos-window-shell:
   *   - Border-radius 14px · esquinas redondeadas suaves
   *   - Sin border, sin bisel · solo box-shadow 1px como borde
   *   - Sombra ambiental 0 12px 40px (no técnica/dura)
   *   - Estado activo · ventana al 100%
   *   - Estado inactivo · atenuación sutil (opacity 0.92)
   *
   * Lógica preservada (sin cambios):
   *   - Drag desde drag-zone invisible en titlebar
   *   - Resize desde handle en esquina inferior-derecha
   *   - Maximize con cálculo de viewport y ui-zoom
   *   - Focus management con z-index
   *   - Carga lazy de apps con dynamic import
   *   - Context windowControls para AppShell
   */
  import { onMount, onDestroy, tick, setContext } from 'svelte';
  import {
    closeWindow, focusWindow, minimizeWindow, maximizeWindow,
    updateWindowPos, getWindowPos, windowList, refitWindow,
  } from '$lib/stores/windows.js';
  import { APP_META } from '$lib/apps.js';

  export let win;

  $: meta = win.gameData
    ? { name: win.gameData.appName || 'Servidor de juego', fallback: '🎮' }
    : (APP_META[win.appId] || { name: win.appId, fallback: '📦' });

  // ¿Esta ventana es la del foco? (zIndex más alto entre las no minimizadas)
  $: isFocused = !win.minimized && win.zIndex === Math.max(
    ...$windowList.filter(w => !w.minimized).map(w => w.zIndex),
    0
  );

  // Expose window controls vía context a AppShell
  setContext('windowControls', {
    close:    () => closeWindow(win.id),
    minimize: () => minimizeWindow(win.id),
    maximize: () => doMaximize(),
    getWin:   () => win,
  });

  let x = 0, y = 0, w = 800, h = 520;

  // Reflow al cambiar el viewport (escala de SO, mover de monitor):
  // re-encaja esta ventana en los nuevos límites. Mantiene su tamaño
  // si cabe; lo recorta si ya no. Las maximizadas siguen al viewport.
  // Coalesce con rAF (las ráfagas de resize del SO no provocan thrash)
  // y NO pelea con un drag/resize manual en curso.
  let removeViewportListener = null;

  onMount(async () => {
    await tick();
    const p = getWindowPos(win.id);
    x = p.x; y = p.y; w = p.width; h = p.height;

    let raf = 0;
    const onViewportChange = () => {
      if (dragging || resizing || raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        const r = refitWindow(win.id, win.maximized);
        if (r) { x = r.x; y = r.y; w = r.width; h = r.height; }
      });
    };
    window.addEventListener('resize', onViewportChange, { passive: true });
    removeViewportListener = () => {
      window.removeEventListener('resize', onViewportChange);
      if (raf) cancelAnimationFrame(raf);
    };
  });

  onDestroy(() => {
    if (removeViewportListener) removeViewportListener();
  });

  // ─── Drag ───
  let dragging = false;
  let dragOffset = { x: 0, y: 0 };

  function onTitleMouseDown(e) {
    if (e.target.closest('.wc-ctl') || e.target.closest('.wc-bar')) return;
    // v3.1 fix: cubre cualquier hijo del slot titlebar-actions
    // (button, input, select, span clickable, etc.) — no solo <button>
    if (e.target.closest('.tb-actions')) return;
    if (win.maximized) return;
    focusWindow(win.id);
    dragging = true;
    dragOffset = { x: e.clientX - x, y: e.clientY - y };
    window.addEventListener('mousemove', onDrag);
    window.addEventListener('mouseup', onDragEnd);
  }

  function onDrag(e) {
    if (!dragging) return;
    x = e.clientX - dragOffset.x;
    y = Math.max(0, e.clientY - dragOffset.y);
    updateWindowPos(win.id, { x, y });
  }

  function onDragEnd() {
    dragging = false;
    window.removeEventListener('mousemove', onDrag);
    window.removeEventListener('mouseup', onDragEnd);
  }

  // ─── Resize ───
  let resizing = false;
  let resizeStart = { mx: 0, my: 0, w: 0, h: 0 };

  function onResizeMouseDown(e) {
    if (win.maximized) return;
    e.stopPropagation();
    resizing = true;
    resizeStart = { mx: e.clientX, my: e.clientY, w, h };
    window.addEventListener('mousemove', onResize);
    window.addEventListener('mouseup', onResizeEnd);
  }

  function onResize(e) {
    if (!resizing) return;
    w = Math.max(400, resizeStart.w + (e.clientX - resizeStart.mx));
    h = Math.max(300, resizeStart.h + (e.clientY - resizeStart.my));
    updateWindowPos(win.id, { width: w, height: h });
  }

  function onResizeEnd() {
    resizing = false;
    window.removeEventListener('mousemove', onResize);
    window.removeEventListener('mouseup', onResizeEnd);
  }

  // ─── Maximize ───
  function doMaximize() {
    maximizeWindow(win.id);
    tick().then(() => {
      const p = getWindowPos(win.id);
      x = p.x; y = p.y; w = p.width; h = p.height;
    });
  }
</script>

<div
  class="window"
  class:maximized={win.maximized}
  class:dragging
  class:inactive={!isFocused}
  class:minimized={win.minimized}
  style="z-index:{win.zIndex}; left:{x}px; top:{y}px; width:{w}px; height:{h}px;"
  on:mousedown={() => focusWindow(win.id)}
  role="application"
>
  <div class="window-titlebar" on:mousedown={onTitleMouseDown} on:dblclick={doMaximize} role="presentation">
    <div class="window-identity">
      <span class="window-mark" aria-hidden="true"></span>
      <span class="window-title">{meta.name}</span>
    </div>
    <div class="win-controls">
      <button class="wc-ctl min" on:click|stopPropagation={() => minimizeWindow(win.id)} title="Minimizar" aria-label="Minimizar">
        <span aria-hidden="true">−</span>
      </button>
      <button class="wc-ctl max" on:click|stopPropagation={doMaximize} title={win.maximized ? 'Restaurar' : 'Maximizar'} aria-label={win.maximized ? 'Restaurar' : 'Maximizar'}>
        <span aria-hidden="true">{win.maximized ? '❐' : '□'}</span>
      </button>
      <button class="wc-ctl close" on:click|stopPropagation={() => closeWindow(win.id)} title="Cerrar" aria-label="Cerrar">
        <span aria-hidden="true">×</span>
      </button>
    </div>
  </div>

  <!-- App content — el .content ocupa toda la ventana, incluyendo titlebar -->
  <div class="content">
    {#if win.gameData}
      {#await import('$lib/apps/GamePanel.svelte') then module}
        <svelte:component
          this={module.default}
          appId={win.gameData.appId}
          appName={win.gameData.appName}
          appIcon={win.gameData.appIcon}
        />
      {/await}
    {:else if win.isWebApp && win.webAppPort}
      {#await import('$lib/apps/WebApp.svelte') then module}
        <svelte:component
          this={module.default}
          appId={win.appId}
          port={win.webAppPort}
          name={win.webAppName}
        />
      {/await}
    {:else if win.appId === 'files'}
      {#await import('$lib/apps/FileManager.svelte') then module}
        <svelte:component
          this={module.default}
          initialShare={win.filesTarget?.share || null}
          initialPath={win.filesTarget?.path || '/'}
        />
      {/await}
    {:else if win.appId === 'mediaplayer'}
      {#await import('$lib/apps/MediaPlayer.svelte') then module}
        <svelte:component
          this={module.default}
          initialShare={win.mediaTarget?.share || null}
          initialPath={win.mediaTarget?.path || null}
        />
      {/await}
    {:else if win.appId === 'nimsettings'}
      {#await import('$lib/apps/Settings.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'controlpanel'}
      {#await import('$lib/apps/ControlPanel.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'storage'}
      {#await import('$lib/apps/StorageApp.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'network'}
      {#await import('$lib/apps/NetworkApp.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'nimtorrent'}
      {#await import('$lib/apps/NimTorrent.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'appstore'}
      {#await import('$lib/apps/AppStore.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'nimbackup'}
      {#await import('$lib/apps/NimBackup.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'notes'}
      {#await import('$lib/apps/Notes.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'nimhealth'}
      {#await import('$lib/apps/NimHealth.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'nimshield'}
      {#await import('$lib/apps/NimShield.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else if win.appId === 'terminal'}
      {#await import('$lib/apps/Terminal.svelte') then module}
        <svelte:component this={module.default} />
      {/await}
    {:else}
      <div class="placeholder">
        <span class="ph-ic">{meta.fallback}</span>
        <p>{meta.name}</p>
        <small>Coming soon</small>
      </div>
    {/if}
  </div>

  {#if !win.maximized}
    <div class="resize-handle" on:mousedown={onResizeMouseDown} role="presentation"></div>
  {/if}
</div>

<style>
  /* ═══════════════════════════════════════════════════════════
     WINDOW FRAME · estética técnica retro NimOS Beta 8.1
     ═══════════════════════════════════════════════════════════
     · Bisel inferior-derecho 22px (firma macro)
     · Borde duro técnico · sombra hard 5px + glow lechoso
     · Sin backdrop-filter · sin border-radius
     · Estados activa/inactiva con atenuación sutil
     ═══════════════════════════════════════════════════════════ */
  /* Minimizada (solo apps keepAlive, p.ej. MediaPlayer): oculta pero VIVA —
     el componente sigue montado y la reproducción no se corta. Las apps
     normales no llegan aquí (Desktop no las renderiza si están minimizadas). */
  .window.minimized {
    display: none;
  }

  .window {
    position: fixed;
    display: flex;
    flex-direction: column;
    background: var(--window-bg, #222422);
    border: 1px solid var(--window-border, #414641);
    border-radius: 6px;
    overflow: hidden;
    /* Ventana profesional: sólida + filo definido + elevación.
       Solo sombras (coste 0 en GPU). El contorno oscuro (0 0 0 1px negro)
       le da filo sobre cualquier wallpaper, claro u oscuro. */
    box-shadow: var(--window-shadow, 0 18px 48px rgba(0, 0, 0, 0.46));
    color: var(--ink);
    transition: opacity 0.15s ease;
    animation: win-in 0.32s cubic-bezier(0.16, 1, 0.3, 1) both;
    will-change: transform;
  }

  .window.dragging { user-select: none; }

  /* ═══ Controles de ventana · anclados a la VENTANA real ═══
     Al vivir en .window (que tiene overflow:hidden y es el contenedor
     que se redimensiona), siempre quedan arriba-derecha visibles, por
     mucho que el contenido interno tenga su propio min-width. */
  .window-titlebar {
    height: 42px;
    flex: 0 0 42px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-left: 14px;
    background: var(--panel-elev);
    border-bottom: 1px solid var(--line, #343834);
    cursor: default;
    z-index: 100;
  }
  .window-identity {
    display: flex;
    align-items: center;
    gap: 9px;
    min-width: 0;
  }
  .window-mark {
    width: 8px;
    height: 8px;
    border-radius: 2px;
    background: var(--signal, #5b8ff9);
  }
  .window-title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--ink-dim, #c4ccd6);
    font: 500 12.5px/1 var(--font-sans);
  }
  .win-controls {
    align-self: stretch;
    display: flex;
    align-items: stretch;
  }
  .win-controls .wc-ctl {
    width: 42px;
    height: 100%;
    border-radius: 0;
    background: transparent;
    border: none;
    border-left: 1px solid transparent;
    color: var(--ink-mute, #8f9aa8);
    cursor: pointer;
    padding: 0;
    font: 400 18px/1 var(--font-sans);
    transition: background 0.12s ease, color 0.12s ease;
  }
  .win-controls .wc-ctl span { display: block; transform: translateY(-1px); }
  .win-controls .wc-ctl:hover {
    background: var(--main-hover);
    color: var(--ink, #f0f3f7);
  }
  .win-controls .wc-ctl.close:hover { background: #c94c57; color: #fff; }
  .win-controls .wc-ctl:active { background: var(--side-active-bg); }

  /* Estado inactivo · ventana atenuada */
  .window.inactive { opacity: 0.96; border-color: var(--line, #343834); }
  .window.inactive .window-titlebar { background: var(--canvas-soft); }

  /* Ventana maximizada · sin border-radius, ocupa todo.
     Sin `zoom` el espacio de coordenadas es honesto: 100vw/100vh ya
     son píxeles reales, no hace falta dividir por --ui-zoom. */
  .window.maximized {
    border-radius: 0 !important;
    box-shadow: none !important;
    left: 0 !important;
    top: 0 !important;
    width: 100vw !important;
    height: calc(100vh - var(--taskbar-height, 3.25rem)) !important;
  }

  .content {
    flex: 1;
    overflow: hidden;
    min-height: 0;
    background: var(--main-bg, #222422);
  }

  /* Placeholder · cuando se abre un app sin módulo todavía */
  .placeholder {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--ink-mute, #9a9aa3);
    background: transparent;
    font-family: var(--font-sans);
  }
  .ph-ic {
    font-size: 48px;
    opacity: 0.85;
    filter: none;
  }
  .placeholder p {
    font-size: 15px;
    font-weight: 500;
    color: var(--ink, #f2f2f5);
    letter-spacing: -0.2px;
  }
  .placeholder small {
    font-size: 10px;
    color: var(--ink-mute, #9a9aa3);
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }

  /* ═══════════════════════════════════════════════════════════
     RESIZE HANDLE · área clickable en esquina inferior-derecha
     ═══════════════════════════════════════════════════════════ */
  .resize-handle {
    position: absolute;
    bottom: 0;
    right: 0;
    width: 16px;
    height: 16px;
    cursor: nwse-resize;
    z-index: 10;
  }

  @keyframes win-in {
    from { opacity: 0; transform: scale(0.98) translateY(6px); }
    to   { opacity: 1; transform: scale(1) translateY(0); }
  }
</style>
