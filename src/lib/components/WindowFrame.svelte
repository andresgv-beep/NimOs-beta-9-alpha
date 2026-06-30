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
  style="z-index:{win.zIndex}; left:{x}px; top:{y}px; width:{w}px; height:{h}px;"
  on:mousedown={() => focusWindow(win.id)}
  role="application"
>
  <!-- Drag zone invisible en la titlebar -->
  <div
    class="drag-zone"
    on:mousedown={onTitleMouseDown}
    role="presentation"
  ></div>

  <!-- Controles de ventana · anclados a la VENTANA (no al shtml interno),
       así nunca se pierden por mucho que se encoja o por el min-width
       del contenido de la app. -->
  <div class="win-controls">
    <button class="wc-ctl min" on:click|stopPropagation={() => minimizeWindow(win.id)} title="Minimizar" aria-label="Minimizar"></button>
    <button class="wc-ctl max" on:click|stopPropagation={doMaximize} title="Maximizar" aria-label="Maximizar"></button>
    <button class="wc-ctl close" on:click|stopPropagation={() => closeWindow(win.id)} title="Cerrar" aria-label="Cerrar"></button>
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
  .window {
    position: fixed;
    display: flex;
    flex-direction: column;
    background: var(--bg-window, #16161a);
    border-radius: 6px;
    overflow: hidden;
    /* Ventana profesional: sólida + filo definido + elevación.
       Solo sombras (coste 0 en GPU). El contorno oscuro (0 0 0 1px negro)
       le da filo sobre cualquier wallpaper, claro u oscuro. */
    box-shadow:
      inset 0 1px 0 rgba(255, 255, 255, 0.08),
      0 0 0 1px rgba(0, 0, 0, 0.45),
      0 0 0 1.5px rgba(255, 255, 255, 0.07),
      0 4px 10px rgba(0, 0, 0, 0.35),
      0 14px 30px rgba(0, 0, 0, 0.45),
      0 30px 70px rgba(0, 0, 0, 0.55);
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
  .win-controls {
    position: absolute;
    top: 12px;
    right: 14px;
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .win-controls .wc-ctl {
    width: 12px;
    height: 12px;
    border-radius: 3px;
    background: var(--ctl-color, #2a2a30);
    border: none;
    cursor: pointer;
    padding: 0;
    transition: filter 0.12s, transform 0.12s, opacity 0.12s;
  }
  .win-controls .wc-ctl.min   { --ctl-color: #ffc857; }
  .win-controls .wc-ctl.max   { --ctl-color: #00ff9f; }
  .win-controls .wc-ctl.close { --ctl-color: #ff5a5a; }
  .win-controls .wc-ctl:hover  { filter: brightness(1.25); }
  .win-controls .wc-ctl:active { transform: scale(0.9); }
  .window.inactive .win-controls .wc-ctl {
    --ctl-color: var(--line-bright);
    opacity: 0.6;
  }

  /* Estado inactivo · ventana atenuada */
  .window.inactive {
    opacity: 0.92;
  }

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

  .drag-zone {
    position: absolute;
    top: 0;
    left: 0;
    right: 80px; /* deja libre la esquina de controles flotantes (top:12 right:14) */
    height: 44px; /* cubre la franja del page-header para arrastrar */
    z-index: 5;
    cursor: default;
    pointer-events: auto;
  }

  .content {
    flex: 1;
    overflow: hidden;
    min-height: 0;
    background: transparent;
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
    filter: drop-shadow(0 0 6px var(--accent-glow-soft, rgba(220, 255, 235, 0.6)));
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
