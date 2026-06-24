<script>
  /**
   * NotificationPanel · Panel flotante al clicar campana del taskbar
   * ──────────────────────────────────────────────────────────────────
   * Ancla: esquina inferior derecha, encima de la campana.
   * Muestra lista de notificaciones con acciones rápidas.
   */
  import {
    notifications, markRead, markAllRead,
    dismissNotification, clearAll
  } from '$lib/stores/notifications.js';
  import SectionHead from '$lib/ui/SectionHead.svelte';
  import EmptyState from '$lib/ui/EmptyState.svelte';

  export let visible = false;

  function handleKeydown(e) {
    if (visible && e.key === 'Escape') visible = false;
  }

  function formatTs(ts) {
    if (!ts) return '';
    const d = new Date(ts);
    const now = new Date();
    const diffMs = now - d;
    const diffMin = Math.floor(diffMs / 60000);
    if (diffMin < 1) return 'ahora';
    if (diffMin < 60) return `hace ${diffMin}m`;
    const diffH = Math.floor(diffMin / 60);
    if (diffH < 24) return `hace ${diffH}h`;
    return d.toLocaleDateString('es-ES', { day: '2-digit', month: 'short' });
  }

  $: unread    = $notifications.filter(n => !n.read);
  $: readList  = $notifications.filter(n =>  n.read);
</script>

<svelte:window on:keydown={handleKeydown} />

{#if visible}
  <div class="overlay" on:click={() => visible = false} role="presentation"></div>

  <div class="panel" on:click|stopPropagation role="presentation">
    <div class="panel-inner">

    <!-- Header -->
    <div class="panel-header">
      <div class="panel-title">
        <span class="panel-ic">◉</span>
        <span>Notificaciones</span>
        {#if unread.length > 0}
          <span class="count">· <b>{unread.length}</b> sin leer</span>
        {/if}
      </div>
      <div class="panel-actions">
        {#if unread.length > 0}
          <button class="mini-btn" on:click={markAllRead} title="Marcar todas leídas">
            <span>◎ todas</span>
          </button>
        {/if}
        {#if $notifications.length > 0}
          <button class="mini-btn" on:click={clearAll} title="Limpiar todas">
            <span>⎚ limpiar</span>
          </button>
        {/if}
        <button class="mini-btn close" on:click={() => visible = false}>×</button>
      </div>
    </div>

    <!-- Body -->
    <div class="panel-body">

      {#if $notifications.length === 0}
        <EmptyState icon="◌" title="Todo en orden" hint="Las notificaciones del sistema aparecerán aquí" />
      {:else}

        {#if unread.length > 0}
          <div class="sect-head">
            <span>──</span>
            <span class="lbl">Sin leer</span>
            <span class="ct">· {unread.length}</span>
          </div>
          {#each unread as n}
            <div
              class="notif"
              class:critical={n.type === 'error' || n.type === 'critical' || n.type === 'security'}
              class:warning={n.type === 'warning'}
              class:success={n.type === 'success'}
              on:click={() => markRead(n.id)}
              on:keydown={(e) => e.key === 'Enter' && markRead(n.id)}
              role="button"
              tabindex="0"
            >
              <div class="notif-dot"></div>
              <div class="notif-body">
                {#if n.title}<div class="notif-title">{n.title}</div>{/if}
                <div class="notif-msg">{n.message}</div>
                <div class="notif-meta">
                  <span class="notif-ts">{formatTs(n.timestamp)}</span>
                  {#if n.category}
                    <span class="notif-cat">· {n.category}</span>
                  {/if}
                </div>
              </div>
              <button
                class="notif-x"
                on:click|stopPropagation={() => dismissNotification(n.id)}
                title="Descartar"
              >×</button>
            </div>
          {/each}
        {/if}

        {#if readList.length > 0}
          <div class="sect-head">
            <span>──</span>
            <span class="lbl">Anteriores</span>
            <span class="ct">· {readList.length}</span>
          </div>
          {#each readList.slice(0, 20) as n}
            <div class="notif read">
              <div class="notif-body">
                {#if n.title}<div class="notif-title">{n.title}</div>{/if}
                <div class="notif-msg">{n.message}</div>
                <div class="notif-meta">
                  <span class="notif-ts">{formatTs(n.timestamp)}</span>
                  {#if n.category}
                    <span class="notif-cat">· {n.category}</span>
                  {/if}
                </div>
              </div>
              <button
                class="notif-x"
                on:click={() => dismissNotification(n.id)}
                title="Descartar"
              >×</button>
            </div>
          {/each}
        {/if}

      {/if}

    </div>

    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 9100;
  }

  .panel {
    position: fixed;
    right: 16px;
    bottom: calc(var(--taskbar-height) + 12px);
    width: 420px;
    max-height: 72vh;
    background: var(--border-bright);
    padding: 1px;
    box-shadow: 0 0 20px rgba(0, 255, 159, 0.06);
    clip-path: polygon(
      0 0,
      100% 0,
      100% calc(100% - 14px),
      calc(100% - 14px) 100%,
      0 100%
    );
    display: flex;
    flex-direction: column;
    font-family: var(--font-mono);
    z-index: 9200;
    animation: panel-in 0.18s cubic-bezier(0.16, 1, 0.3, 1) both;
  }

  .panel-inner {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    background: var(--glass-bg);
    backdrop-filter: blur(24px) saturate(140%);
    -webkit-backdrop-filter: blur(24px) saturate(140%);
    clip-path: polygon(
      0 0,
      100% 0,
      100% calc(100% - 13px),
      calc(100% - 13px) 100%,
      0 100%
    );
  }
  @keyframes panel-in {
    from { opacity: 0; transform: translateY(10px) scale(0.98); }
    to   { opacity: 1; transform: translateY(0) scale(1); }
  }

  .panel-header {
    display: flex;
    align-items: center;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    background: rgba(20, 20, 20, 0.4);
    flex-shrink: 0;
    gap: 10px;
  }
  .panel-title {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 10px;
    color: var(--ink);
    letter-spacing: 2px;
    text-transform: uppercase;
    font-weight: 600;
    flex: 1;
  }
  .panel-ic {
    width: 18px; height: 18px;
    border: 1px solid var(--accent);
    color: var(--accent);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 10px;
    clip-path: polygon(0 0, calc(100% - 3px) 0, 100% 3px, 100% 100%, 3px 100%, 0 calc(100% - 3px));
  }
  .count {
    font-weight: 400;
    letter-spacing: 0.5px;
    text-transform: none;
    color: var(--fg-dim);
    font-size: 9px;
  }
  .count b { color: var(--accent); }

  .panel-actions {
    display: flex;
    gap: 5px;
  }
  .mini-btn {
    padding: 4px 8px;
    background: rgba(10, 10, 10, 0.5);
    border: 1px solid var(--border);
    color: var(--fg-dim);
    font-family: inherit;
    font-size: 9px;
    letter-spacing: 0.5px;
    cursor: pointer;
    transition: all 0.1s;
    clip-path: polygon(0 0, calc(100% - 3px) 0, 100% 3px, 100% 100%, 3px 100%, 0 calc(100% - 3px));
  }
  .mini-btn:hover { border-color: var(--accent); color: var(--accent); }
  .mini-btn.close {
    width: 24px;
    padding: 0;
    font-size: 13px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .mini-btn.close:hover { border-color: var(--crit); color: var(--crit); }

  .panel-body {
    flex: 1;
    overflow-y: auto;
    padding: 8px 0;
  }

  .sect-head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 10px 14px 6px;
    font-size: 9px;
    color: var(--fg-mute);
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
  .sect-head .lbl { letter-spacing: 1.5px; }
  .sect-head .ct { color: var(--accent); }

  .notif {
    display: grid;
    grid-template-columns: 10px 1fr 24px;
    gap: 10px;
    align-items: flex-start;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    transition: background 0.08s;
  }
  .notif:hover { background: rgba(255, 255, 255, 0.02); }
  .notif.read { opacity: 0.6; }
  .notif.critical { background: rgba(255, 90, 90, 0.04); }
  .notif.warning  { background: rgba(255, 184, 0, 0.03); }

  .notif-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 4px var(--accent-glow);
    margin-top: 4px;
  }
  .notif.critical .notif-dot { background: var(--crit); box-shadow: 0 0 4px rgba(255, 90, 90, 0.4); }
  .notif.warning .notif-dot  { background: var(--warn); box-shadow: 0 0 4px rgba(255, 184, 0, 0.4); }
  .notif.success .notif-dot  { background: var(--accent); }
  .notif.read .notif-dot { display: none; }
  .notif.read { grid-template-columns: 1fr 24px; }

  .notif-body {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
  }
  .notif-title {
    font-size: 10.5px;
    color: var(--ink);
    font-weight: 600;
    letter-spacing: 0.3px;
  }
  .notif-msg {
    font-size: 10px;
    color: var(--fg-dim);
    line-height: 1.5;
    word-wrap: break-word;
  }
  .notif-meta {
    display: flex;
    gap: 4px;
    font-size: 9px;
    color: var(--fg-faint);
    letter-spacing: 0.5px;
    margin-top: 2px;
    font-feature-settings: "tnum";
  }

  .notif-x {
    width: 20px; height: 20px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--fg-mute);
    font-size: 13px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: all 0.1s;
  }
  .notif-x:hover { border-color: var(--crit); color: var(--crit); }
</style>
