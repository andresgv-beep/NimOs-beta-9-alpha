<script>
  /**
   * Desktop · Contenedor raíz de la UI de NimOS Beta 8.1
   * ──────────────────────────────────────────────────────
   * - Wallpaper NimOS blueprint (grid doble verde + halos)
   * - Logo NimOS abajo derecha (encima del reloj del taskbar)
   * - Ventanas flotantes
   * - Taskbar inferior
   * - Listener de notificaciones + SMART check al login
   */
  import { onMount, onDestroy } from 'svelte';
  import { windowList } from '$lib/stores/windows.js';
  import { prefs } from '$lib/stores/theme.js';
  import {
    loadNotifications, notifications
  } from '$lib/stores/notifications.js';
  import { hdrs } from '$lib/stores/auth.js';
  import Taskbar from './Taskbar.svelte';
  import WindowFrame from './WindowFrame.svelte';
  import WidgetLayer from './WidgetLayer.svelte';
  import { APP_META } from '$lib/apps.js';
  import NimosLogo from '$lib/ui/NimosLogo.svelte';

  let pollInterval;

  onMount(() => {
    loadNotifications();
    checkSmartOnLogin();
    pollInterval = setInterval(pollNotifications, 30000);
    return () => clearInterval(pollInterval);
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
  });

  async function pollNotifications() {
    await loadNotifications();
  }

  async function checkSmartOnLogin() {
    try {
      // Auth por cookie HttpOnly (hdrs centralizado, token solo en memoria).
      // NO leer localStorage: reintroducirlo reabre el robo de sesión vía XSS.
      const r = await fetch('/api/disks/smart/summary', {
        headers: hdrs(),
      });
      const d = await r.json();
      if (d.worstStatus === 'critical' || d.worstStatus === 'warning') {
        const badDisks = (d.disks || []).filter(dk => dk.status !== 'ok');
        const names = badDisks.map(dk => dk.name).join(', ');
        const isCritical = d.worstStatus === 'critical';
        notifications.update(n => [{
          id: 'smart-login-' + Date.now(),
          type: isCritical ? 'error' : 'warning',
          category: 'system',
          title: isCritical ? 'Disco en riesgo de fallo' : 'Disco requiere atención',
          message: `SMART detecta problemas en: ${names}. Revisa Storage → Salud.`,
          timestamp: new Date().toISOString(),
          read: false,
        }, ...n]);
      }
    } catch {}
  }

  // Wallpaper custom del user · override del default NimOS
  $: hasCustomWallpaper = !!$prefs.wallpaper;
</script>

<div
  class="desktop"
  class:has-custom-wallpaper={hasCustomWallpaper}
  style={hasCustomWallpaper
    ? `background-image: url('${$prefs.wallpaper}')`
    : ''
  }
>
  <!-- Logo NimOS · marca de sistema · solo visible con wallpaper default -->
  {#if !hasCustomWallpaper}
    <div class="desktop-brand">
      <NimosLogo size={44} />
    </div>
  {/if}

  <!-- Capa de widgets · sobre wallpaper, bajo ventanas -->
  <WidgetLayer />

  <!-- Ventanas flotantes -->
  {#each $windowList as win (win.id)}
    {#if !win.minimized || APP_META[win.appId]?.keepAlive}
      <WindowFrame {win} />
    {/if}
  {/each}

  <!-- Taskbar inferior -->
  <Taskbar />
</div>

<style>
  /* ═══════════════════════════════════════════════════════════
     DESKTOP · contenedor raíz con wallpaper NimOS
     ═══════════════════════════════════════════════════════════ */
  .desktop {
    position: fixed;
    inset: 0;
    background: var(--wallpaper);
    background-attachment: fixed;
    overflow: hidden;
  }

  /* Cuando el usuario pone wallpaper custom · imagen externa */
  .desktop.has-custom-wallpaper {
    background-size: cover;
    background-position: center;
    background-repeat: no-repeat;
    background-color: var(--canvas, #050505);
  }

  /* ═══════════════════════════════════════════════════════════
     LOGO NIMOS · marca de sistema abajo derecha
     · Solo visible con wallpaper default (no custom image)
     · Encima del reloj del taskbar
     ═══════════════════════════════════════════════════════════ */
  .desktop-brand {
    position: absolute;
    bottom: calc(var(--taskbar-height, 52px) + 24px);
    right: 32px;
    z-index: 1;
    pointer-events: none;
    opacity: 0.48;
  }
</style>
