<script>
  /**
   * Taskbar · Barra de tareas NimOS Beta 8.1
   * ──────────────────────────────────────────
   * - Zona izquierda: logo NimOS · botón MENÚ · apps ancladas · apps abiertas
   * - Zona centro:    vacío (deja respirar el escritorio)
   * - Zona derecha:   transferencias · notificaciones · reloj · power
   *
   * Estética técnica retro NimOS:
   *   - Sin glass · sin border-radius · gradient sutil + border-top duro
   *   - LED barrita 16×2px verde luminoso bajo apps abiertas
   *   - Botón MENÚ con chaflán inferior-derecho 8px (firma NimOS)
   *   - Tooltips con chaflán técnico
   *   - Línea de glow verde sutil en el borde superior (firma del boot)
   *
   * Mantenido de Beta 8:
   *   - Logo NimOS pixelado (3 cubos blancos)
   *   - Toda la lógica de stores (windowList, pinnedApps, notifications, uploadTasks)
   *   - Anclar/desanclar via contextmenu
   *   - Restore/minimize/focus de ventanas
   */
  import { onMount, onDestroy } from 'svelte';
  import { pinnedApps, setPref, prefs } from '$lib/stores/theme.js';
  import {
    windowList, openWindow, focusWindow,
    restoreWindow, minimizeWindow, closeWindow
  } from '$lib/stores/windows.js';
  import { logout } from '$lib/stores/auth.js';
  import { APP_META } from '$lib/apps.js';
  import { unreadCount } from '$lib/stores/notifications.js';
  import { activeTasks } from '$lib/stores/uploadTasks.js';
  import Launcher from './Launcher.svelte';
  import NotificationPanel from './NotificationPanel.svelte';
  import TransferPanel from './TransferPanel.svelte';
  import AppIcon from '$lib/ui/AppIcon.svelte';

  let showLauncher = false;
  let showNotif = false;
  let showTransfers = false;

  // ─── Clock ───
  let now = new Date();
  let clockInterval;

  function updateClock() {
    now = new Date();
  }

  onMount(() => {
    updateClock();
    clockInterval = setInterval(updateClock, 1000);
    return () => clearInterval(clockInterval);
  });
  onDestroy(() => {
    if (clockInterval) clearInterval(clockInterval);
  });

  $: dd = String(now.getDate()).padStart(2, '0');
  $: MON = now.toLocaleDateString('es-ES', { month: 'short' }).replace('.', '');
  $: DOW = now.toLocaleDateString('es-ES', { weekday: 'short' }).replace('.', '');
  $: timeText = now.toLocaleTimeString('es-ES', {
    hour: '2-digit', minute: '2-digit', hour12: !$prefs.clock24,
  });

  // ─── Context menu (pin/unpin) ───
  let ctxMenu = null;

  function openCtxMenu(e, appId, win = null) {
    e.preventDefault();
    e.stopPropagation();
    ctxMenu = {
      appId,
      win,
      x: Math.min(e.clientX, window.innerWidth - 220),
      bottom: window.innerHeight - e.clientY + 8,
    };
  }
  function closeCtxMenu() { ctxMenu = null; }
  function isPinned(appId) { return $pinnedApps.includes(appId); }
  function togglePin(appId) {
    if (isPinned(appId)) setPref('pinnedApps', $pinnedApps.filter(id => id !== appId));
    else setPref('pinnedApps', [...$pinnedApps, appId]);
    closeCtxMenu();
  }

  // ─── App launch ───
  function handleAppClick(appId) {
    const meta = APP_META[appId];
    const existing = $windowList.find(w => w.appId === appId);
    if (existing) {
      if (existing.minimized) restoreWindow(existing.id);
      else focusWindow(existing.id);
    } else {
      openWindow(appId, { width: meta?.width || 800, height: meta?.height || 520 });
    }
  }
  function toggleMinimize(win) {
    if (win.minimized) restoreWindow(win.id);
    else minimizeWindow(win.id);
  }

  // ─── Apps open not pinned ───
  $: openUnpinned = $windowList.filter(w => !$pinnedApps.includes(w.appId));

  // ─── Transfers activity ───
  $: transferCount = $activeTasks.length;
</script>

<Launcher bind:visible={showLauncher} />
<NotificationPanel bind:visible={showNotif} />
<TransferPanel bind:visible={showTransfers} />

<!-- Context menu click outside -->
{#if ctxMenu}
  <div class="ctx-overlay" on:click={closeCtxMenu} role="presentation"></div>
  <div class="ctx-menu" style="left:{ctxMenu.x}px; bottom:{ctxMenu.bottom}px">
    <div class="ctx-item" on:click={() => togglePin(ctxMenu.appId)} role="button" tabindex="0">
      <span class="ctx-ic">◆</span>
      <span>{isPinned(ctxMenu.appId) ? 'Desanclar del taskbar' : 'Anclar al taskbar'}</span>
    </div>
    {#if ctxMenu.win}
      <div class="ctx-sep"></div>
      <div class="ctx-item" on:click={() => { closeWindow(ctxMenu.win.id); closeCtxMenu(); }} role="button" tabindex="0">
        <span class="ctx-ic">×</span>
        <span>Cerrar ventana</span>
      </div>
    {/if}
  </div>
{/if}

<div class="taskbar">

  <!-- ═══════════════ IZQUIERDA · LAUNCHER ═══════════════ -->
  <div class="tb-left">

    <!-- Logo NimOS · 3 cubos pixel art · ÚNICO punto de entrada al launcher -->
    <button
      class="tb-logo-btn"
      on:click={() => showLauncher = !showLauncher}
      class:active={showLauncher}
      title="Apps · NimOS"
    >
      <svg class="nimos-logo" width="28" height="28" viewBox="-15 0 200 185" fill="none" xmlns="http://www.w3.org/2000/svg">
        <rect x="5" y="45" width="80" height="80" rx="16" transform="rotate(-30 45 85)" fill="#ffffff"/>
        <rect x="108" y="12" width="60" height="60" rx="10" fill="#ffffff"/>
        <rect x="108" y="98" width="60" height="60" rx="10" fill="#ffffff"/>
      </svg>
    </button>

    <div class="tb-sep"></div>

    <!-- Apps ancladas -->
    <div class="app-row">
      {#each $pinnedApps as appId}
        {@const meta = APP_META[appId]}
        {#if meta}
          {@const existing = $windowList.find(w => w.appId === appId)}
          {@const isOpen = !!existing}
          {@const isMin  = existing?.minimized}
          {@const isFocused = isOpen && !isMin && existing?.zIndex === Math.max(...$windowList.map(w => w.zIndex))}
          <button
            class="tb-app"
            class:open={isOpen}
            class:minimized={isMin}
            class:focused={isFocused}
            on:click={() => handleAppClick(appId)}
            on:contextmenu={(e) => openCtxMenu(e, appId, existing)}
          >
            <AppIcon
              src={meta.icon}
              alt={meta.name}
              size="sm"
              fallback={meta.fallback}
              active={isOpen}
            />
            <span class="tb-tooltip">{meta.name}</span>
          </button>
        {/if}
      {/each}
    </div>

    <!-- Apps abiertas no ancladas -->
    {#if openUnpinned.length > 0}
      <div class="tb-sep"></div>
      <div class="app-row">
        {#each openUnpinned as win}
          {@const meta = APP_META[win.appId]}
          {@const gameIcon = win.gameData?.appIcon}
          {@const gameName = win.gameData?.appName}
          {@const isFocused = !win.minimized && win.zIndex === Math.max(...$windowList.map(w => w.zIndex))}
          <button
            class="tb-app open"
            class:minimized={win.minimized}
            class:focused={isFocused}
            on:click={() => toggleMinimize(win)}
            on:contextmenu={(e) => openCtxMenu(e, win.appId, win)}
          >
            <AppIcon
              src={gameIcon || meta?.icon}
              alt={gameName || meta?.name}
              size="sm"
              fallback={win.gameData ? '🎮' : meta?.fallback}
              active={!win.minimized}
            />
            <span class="tb-tooltip">{gameName || meta?.name || win.appId}</span>
          </button>
        {/each}
      </div>
    {/if}

  </div>

  <!-- ═══════════════ CENTRO · vacío, respira ═══════════════ -->
  <div class="tb-center"></div>

  <!-- ═══════════════ DERECHA · SYSTRAY ═══════════════ -->
  <div class="tb-right">

    <!-- Transferencias -->
    <button
      class="tb-tray"
      class:active={showTransfers}
      class:has-activity={transferCount > 0}
      on:click={() => { showTransfers = !showTransfers; showNotif = false; }}
      title="Transferencias"
    >
      <span class="tray-ic">⇅</span>
      {#if transferCount > 0}
        <span class="tray-badge active">{transferCount}</span>
      {/if}
    </button>

    <!-- Notificaciones -->
    <button
      class="tb-tray"
      class:active={showNotif}
      class:has-unread={$unreadCount > 0}
      on:click={() => { showNotif = !showNotif; showTransfers = false; }}
      title="Notificaciones"
    >
      <span class="tray-ic">◉</span>
      {#if $unreadCount > 0}
        <span class="tray-badge">{$unreadCount}</span>
      {/if}
    </button>

    <div class="tb-sep"></div>

    <div class="tb-clock" title={now.toLocaleString('es-ES')}>
      <span class="clock-time">{timeText}</span>
      <span class="clock-date">{DOW}, {dd} {MON}</span>
    </div>

    <!-- Cuenta · abre menú (reiniciar, cerrar sesión, etc.) -->
    <button class="tb-account" on:click={logout} title="Cuenta">
      <svg class="account-ic" viewBox="-8 0 512 512" xmlns="http://www.w3.org/2000/svg" fill="currentColor" aria-hidden="true">
        <path d="M248 8C111 8 0 119 0 256s111 248 248 248 248-111 248-248S385 8 248 8zm0 96c48.6 0 88 39.4 88 88s-39.4 88-88 88-88-39.4-88-88 39.4-88 88-88zm0 344c-58.7 0-111.3-26.6-146.5-68.2 18.8-35.4 55.6-59.8 98.5-59.8 2.4 0 4.8.4 7.1 1.1 13 4.2 26.6 6.9 40.9 6.9 14.3 0 28-2.7 40.9-6.9 2.3-.7 4.7-1.1 7.1-1.1 42.9 0 79.7 24.4 98.5 59.8C359.3 421.4 306.7 448 248 448z"></path>
      </svg>
    </button>

  </div>

</div>

<style>
  /* ═══════════════════════════════════════════════════════════
     TASKBAR · Beta 8.1 · estética técnica retro NimOS
     ═══════════════════════════════════════════════════════════ */
  .taskbar {
    position: fixed;
    left: 0; right: 0; bottom: 0;
    height: var(--taskbar-height, 2.75rem);
    /* Cristal translúcido: la barra es fija → el blur cuesta poco y
       deja translucir el wallpaper. El color/opacidad vienen del tema
       (--taskbar-bg): oscuro en dark, claro en cream. */
    background: var(--taskbar-bg, #1b1e1c);
    border-top: 1px solid var(--taskbar-border-top, rgba(255, 255, 255, 0.08));
    box-shadow: 0 -6px 18px rgba(0, 0, 0, 0.22);
    display: flex;
    align-items: stretch;
    z-index: 9000;
    font-family: var(--font-sans, Inter, sans-serif);
  }

  .tb-left, .tb-right {
    display: flex;
    align-items: center;
    padding: 0 0.375rem;
    gap: 0.3125rem;
  }
  .tb-center { flex: 1; }

  .tb-sep {
    width: 1px;
    align-self: center;
    height: 1.375rem;
    background: var(--border, #1f1f1f);
    margin: 0 0.375rem;
  }

  .app-row {
    display: flex;
    gap: 0.375rem;
  }

  /* ─── Logo NimOS · botón sin marco con drop-shadow lechoso ─── */
  .tb-logo-btn {
    width: 2.75rem;
    height: 2.25rem;
    background: transparent;
    border: none;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s;
    padding: 0;
    position: relative;
  }
  .tb-logo-btn:hover {
    background: var(--main-hover);
    border-radius: 4px;
  }
  /* Cuando el launcher está abierto · sin marco verde, sin línea, solo el logo brilla más */
  .tb-logo-btn.active {
    background: transparent;
  }
  .nimos-logo {
    /* Reposo · blanco normal, sin gradient ni glow */
    filter: none;
    transition: filter 0.18s ease;
  }
  /* Cuando el launcher está abierto · logo se ilumina con drop-shadow lechoso (firma del boot) */
  .tb-logo-btn.active .nimos-logo {
    filter: none;
  }
  /* Hover también ilumina sutilmente como preview del estado activo */
  .tb-logo-btn:hover .nimos-logo {
    filter: none;
  }

  /* ─── App icon · sin border-radius · LED bajo cuando está abierta ─── */
  .tb-app {
    position: relative;
    width: 2.75rem;
    height: 2.75rem;
    background: transparent;
    border: none;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s;
    padding: 0;
  }
  .tb-app:hover {
    background: var(--main-hover);
    border-radius: 4px;
  }
  /* AppIcon ya define width/height vía size="sm" (36px).
     NO sobreescribimos width/height aquí (rompía la proporción)
     ni añadimos drop-shadow (los SVG ya tienen su propio look). */
  /* LED barrita bajo apps abiertas · 16×2px verde luminoso */
  .tb-app.open::after {
    content: '';
    position: absolute;
    bottom: 0.125rem;
    left: 50%;
    transform: translateX(-50%);
    width: 1rem;
    height: 2px;
    background: var(--signal, #5b8ff9);
  }
  .tb-app.focused::after {
    width: 1.375rem;
  }
  .tb-app.minimized::after {
    width: 0.5rem;
    opacity: 0.4;
  }

  /* Tooltip arriba del icono · chaflán técnico */
  .tb-tooltip {
    position: absolute;
    bottom: calc(100% + 0.375rem);
    left: 50%;
    transform: translateX(-50%);
    background: var(--bg-elev, #242429);
    border: 1px solid var(--border-bright, #2a2a2a);
    padding: var(--sp-1) 0.625rem;
    font-family: var(--font-sans, Inter, sans-serif);
    font-size: var(--fs-11);
    color: var(--ink);
    letter-spacing: 0;
    font-weight: 500;
    white-space: nowrap;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.12s;
    border-radius: 4px;
  }
  .tb-app:hover .tb-tooltip {
    opacity: 1;
  }

  /* ─── Tray buttons ─── */
  .tb-tray {
    position: relative;
    width: 2.25rem;
    height: 2.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    color: var(--fg-dim, #9a9aa3);
    font-size: var(--fs-18);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .tb-tray:hover {
    background: var(--main-hover);
    color: var(--ink);
  }
  .tb-tray.active {
    background: var(--signal-soft);
    color: var(--signal, #5b8ff9);
    border-radius: 4px;
  }
  .tb-tray.has-activity .tray-ic {
    color: var(--signal, #5b8ff9);
  }
  .tray-ic {
    line-height: 1;
    filter: none;
  }

  .tray-badge {
    position: absolute;
    top: var(--sp-1);
    right: var(--sp-1);
    min-width: 0.875rem;
    height: 0.75rem;
    padding: 0 0.1875rem;
    background: var(--crit, #d76b6b);
    color: #fff;
    font-family: var(--font-mono, monospace);
    font-size: 0.53125rem;
    font-weight: 700;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
    border: 1px solid rgba(0, 0, 0, 0.6);
  }
  .tray-badge.active {
    background: var(--signal, #5b8ff9);
    color: #fff;
  }

  .tb-clock {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    padding: 0 14px;
    line-height: 1;
    cursor: pointer;
    gap: 4px;
  }
  .clock-time {
    color: var(--ink, #f0f3f7);
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.01em;
  }
  .clock-date {
    font-family: var(--font-sans, Inter, sans-serif);
    font-size: 10px;
    color: var(--ink-mute, #8f9aa8);
    letter-spacing: 0;
    font-weight: 500;
  }

  /* ─── Power ─── */
  .tb-account {
    width: 2.75rem;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    border-left: 1px solid var(--border, #1f1f1f);
    /* Blanco en dark, gris oscuro en light (definido por tema) */
    color: var(--account-ic-color, #f2f2f5);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
    margin-left: var(--sp-1);
  }
  .tb-account:hover {
    background: var(--main-hover);
    color: var(--account-ic-hover, var(--ink, #f2f2f5));
  }
  .account-ic {
    width: 1.3125rem;
    height: 1.3125rem;
    display: block;
    filter: none;
  }

  /* ═══════════════════════════════════════════════════════════
     CONTEXT MENU · estética técnica retro
     ═══════════════════════════════════════════════════════════ */
  .ctx-overlay {
    position: fixed;
    inset: 0;
    z-index: 9500;
  }
  .ctx-menu {
    position: fixed;
    min-width: 13.125rem;
    background: var(--panel-elev, #292c29);
    border: 1px solid var(--border-bright, #2a2a2a);
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.42);
    z-index: 9510;
    font-family: var(--font-sans, Inter, sans-serif);
    font-size: var(--fs-12);
    padding: var(--sp-1);
    border-radius: 6px;
  }
  .ctx-item {
    padding: var(--sp-2) var(--sp-3);
    color: var(--ink);
    display: flex;
    align-items: center;
    gap: 0.625rem;
    cursor: pointer;
    transition: background 0.08s, color 0.08s;
    letter-spacing: 0;
  }
  .ctx-item:hover {
    background: var(--signal-soft);
    color: var(--signal, #5b8ff9);
  }
  .ctx-ic {
    color: var(--fg-mute, #5a5a62);
    width: 0.875rem;
    text-align: center;
    font-size: var(--fs-11);
  }
  .ctx-item:hover .ctx-ic { color: var(--signal, #5b8ff9); }
  .ctx-sep {
    height: 1px;
    background: var(--border, #1f1f1f);
    margin: var(--sp-1) 0.125rem;
  }
</style>
