<script>
  /**
   * TransferPanel · Popover de Transferencias
   * ───────────────────────────────────────────
   * Sale al clicar el icono ⇅ del systray.
   * Lista compacta de transferencias activas/pausadas/completas/errores con tabs.
   *
   * Layout: 480x560 fijo, anclado en esquina inferior derecha.
   */
  import {
    uploadTasks, activeTasks,
    cancelTask, removeTask, clearDone,
    pauseTask, resumeTask
  } from '$lib/stores/uploadTasks.js';
  import StripeProgressBar from '$lib/ui/StripeProgressBar.svelte';
  import EmptyState from '$lib/ui/EmptyState.svelte';
  import Tab from '$lib/ui/Tab.svelte';

  export let visible = false;

  let currentTab = 'active'; // 'active' | 'done' | 'error'

  $: active    = $uploadTasks.filter(t => ['uploading', 'paused', 'queued'].includes(t.status));
  $: done      = $uploadTasks.filter(t => t.status === 'done');
  $: errors    = $uploadTasks.filter(t => t.status === 'error');

  $: current =
    currentTab === 'active' ? active :
    currentTab === 'done'   ? done   :
    errors;

  $: totalSpeed = $activeTasks.reduce((s, t) => s + (t.speed || 0), 0);

  function fmtSize(bytes) {
    if (!bytes && bytes !== 0) return '—';
    if (bytes >= 1e9) return (bytes / 1e9).toFixed(2) + ' GB';
    if (bytes >= 1e6) return (bytes / 1e6).toFixed(1) + ' MB';
    if (bytes >= 1e3) return (bytes / 1e3).toFixed(0) + ' KB';
    return bytes + ' B';
  }

  function fmtSpeed(bps) {
    if (!bps || bps <= 0) return '—';
    if (bps >= 1e6) return (bps / 1e6).toFixed(1) + ' MB/s';
    if (bps >= 1e3) return (bps / 1e3).toFixed(0) + ' KB/s';
    return Math.round(bps) + ' B/s';
  }

  function fmtEta(task) {
    if (task.status !== 'uploading' || !task.speed || task.speed <= 0) return '—';
    const remaining = (task.size || 0) * (1 - (task.progress || 0) / 100);
    const secs = remaining / task.speed;
    if (secs > 3600) return Math.floor(secs / 3600) + 'h ' + Math.floor((secs % 3600) / 60) + 'm';
    if (secs > 60)   return Math.floor(secs / 60) + ':' + String(Math.floor(secs % 60)).padStart(2, '0');
    return Math.floor(secs) + 's';
  }

  function dirSymbol(task) {
    if (task.status === 'done')   return '✓';
    if (task.status === 'error')  return '✗';
    if (task.status === 'paused') return '❙❙';
    // type puede ser 'upload' | 'download' según implementación
    return task.type === 'download' ? '↓' : '↑';
  }

  function dirVariant(task) {
    if (task.status === 'done')   return 'ok';
    if (task.status === 'error')  return 'err';
    if (task.status === 'paused') return 'pause';
    return task.type === 'download' ? 'down' : 'up';
  }

  function barVariant(task) {
    if (task.status === 'done')   return 'accent';
    if (task.status === 'error')  return 'crit';
    if (task.status === 'paused') return 'warn';
    return task.type === 'download' ? 'accent' : 'info';
  }

  function handleKeydown(e) {
    if (visible && e.key === 'Escape') visible = false;
  }

  function togglePause(t) {
    if (t.status === 'uploading')      pauseTask(t.id);
    else if (t.status === 'paused')    resumeTask(t.id);
  }
</script>

<svelte:window on:keydown={handleKeydown} />

{#if visible}
  <div class="overlay" on:click={() => visible = false} role="presentation"></div>

  <div class="panel" on:click|stopPropagation role="presentation">
    <div class="panel-inner">

    <!-- Header -->
    <div class="panel-header">
      <div class="panel-title">
        <span class="panel-ic">⇅</span>
        <span>Transferencias</span>
      </div>
      {#if active.length > 0}
        <div class="summary">
          <span class="v">{active.length}</span>
          <span>activas</span>
          <span class="sep">·</span>
          <span class="v">{fmtSpeed(totalSpeed)}</span>
        </div>
      {/if}
      <button class="close-btn" on:click={() => visible = false} title="Cerrar">×</button>
    </div>

    <!-- Tabs -->
    <div class="tabs-row">
      <Tab active={currentTab === 'active'} onClick={() => currentTab = 'active'}>
        Activas
        {#if active.length > 0}<span class="tn accent">{active.length}</span>{/if}
      </Tab>
      <Tab active={currentTab === 'done'} onClick={() => currentTab = 'done'}>
        Completadas
        {#if done.length > 0}<span class="tn">{done.length}</span>{/if}
      </Tab>
      <Tab active={currentTab === 'error'} onClick={() => currentTab = 'error'} hasError={errors.length > 0}>
        Errores
        {#if errors.length > 0}<span class="tn crit">{errors.length}</span>{/if}
      </Tab>
    </div>

    <!-- Actions bar -->
    {#if currentTab === 'done' && done.length > 0}
      <div class="actions-row">
        <button class="mini-btn" on:click={() => clearDone()}>⎚ Limpiar completadas</button>
      </div>
    {/if}

    <!-- List -->
    <div class="list">
      {#if current.length === 0}
        <EmptyState
          icon={currentTab === 'error' ? '✓' : '⇅'}
          title={currentTab === 'active' ? 'Sin transferencias activas' : currentTab === 'done' ? 'Sin transferencias completadas' : 'Sin errores'}
          hint={currentTab === 'active' ? 'Las subidas y bajadas aparecerán aquí' : ''}
        />
      {:else}
        {#each current as t}
          <div
            class="tr-row"
            class:error={t.status === 'error'}
            class:paused={t.status === 'paused'}
            class:done={t.status === 'done'}
          >
            <div class="tr-top">
              <span class="dir {dirVariant(t)}">{dirSymbol(t)}</span>
              <span class="name" title={t.name}>{t.name || 'sin nombre'}</span>
              <span class="size">
                {#if t.status === 'done'}
                  {fmtSize(t.size)}
                {:else}
                  {fmtSize((t.size || 0) * (t.progress || 0) / 100)}
                  <span class="tot">/ {fmtSize(t.size)}</span>
                {/if}
              </span>
            </div>

            {#if t.status !== 'done'}
              <div class="progress-row">
                <StripeProgressBar
                  percent={t.progress || 0}
                  variant={barVariant(t)}
                  animated={t.status === 'uploading'}
                  height={6}
                />
                <span class="pct" class:pause={t.status === 'paused'} class:err={t.status === 'error'}>
                  {(t.progress || 0).toFixed(0)}%
                </span>
                <span class="eta">{fmtEta(t)}</span>
                {#if t.status !== 'error'}
                  <button class="iconbtn" on:click={() => togglePause(t)} title={t.status === 'paused' ? 'Reanudar' : 'Pausar'}>
                    {t.status === 'paused' ? '▸' : '❙❙'}
                  </button>
                {:else}
                  <button class="iconbtn" on:click={() => resumeTask(t.id)} title="Reintentar">↻</button>
                {/if}
                <button class="iconbtn danger" on:click={() => cancelTask(t.id)} title="Cancelar">×</button>
              </div>
            {/if}

            <div class="meta">
              {#if t.path}
                <span class="path">→ {t.path}</span>
              {:else}
                <span class="path"></span>
              {/if}
              {#if t.status === 'uploading'}
                <span class="speed {dirVariant(t)}">{fmtSpeed(t.speed)}</span>
              {:else if t.status === 'done' && t.completedAt}
                <span class="tstamp">{new Date(t.completedAt).toLocaleString('es-ES')}</span>
              {:else if t.status === 'paused'}
                <span class="speed">pausada</span>
              {:else if t.status === 'error' && t.error}
                <span class="err-msg">{t.error}</span>
              {/if}
              {#if t.status === 'done'}
                <button class="iconbtn" on:click={() => removeTask(t.id)} title="Quitar del historial">×</button>
              {/if}
            </div>
          </div>
        {/each}
      {/if}
    </div>

    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed; inset: 0; z-index: 9100;
  }

  .panel {
    position: fixed;
    right: 16px;
    bottom: calc(var(--taskbar-height) + 12px);
    width: 480px;
    height: 560px;
    max-height: 80vh;
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
    gap: 10px;
    flex-shrink: 0;
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
    font-weight: 700;
    clip-path: polygon(0 0, calc(100% - 3px) 0, 100% 3px, 100% 100%, 3px 100%, 0 calc(100% - 3px));
  }
  .summary {
    display: flex;
    gap: 6px;
    font-size: 9px;
    color: var(--fg-mute);
    letter-spacing: 0.6px;
    text-transform: none;
  }
  .summary .v { color: var(--fg-dim); font-weight: 500; }
  .summary .sep { color: var(--fg-faint); }

  .close-btn {
    width: 22px; height: 22px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--fg-dim);
    font-size: 13px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: all 0.1s;
    clip-path: polygon(0 0, calc(100% - 4px) 0, 100% 4px, 100% 100%, 4px 100%, 0 calc(100% - 4px));
  }
  .close-btn:hover { border-color: var(--crit); color: var(--crit); }

  .tabs-row {
    display: flex;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
    padding: 0 4px;
    flex-shrink: 0;
  }
  .tn {
    font-size: 8px;
    color: var(--fg-faint);
    border: 1px solid var(--border);
    padding: 0 3px;
    letter-spacing: 0.5px;
  }
  .tn.accent { color: var(--accent); border-color: var(--accent); }
  .tn.crit   { color: var(--crit);   border-color: var(--crit);   }

  .actions-row {
    display: flex;
    gap: 4px;
    padding: 8px 12px;
    background: rgba(15, 15, 15, 0.5);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .mini-btn {
    padding: 5px 9px;
    background: var(--bg-1);
    border: 1px solid var(--border);
    color: var(--fg-dim);
    font-family: inherit;
    font-size: 9px;
    letter-spacing: 0.8px;
    text-transform: uppercase;
    cursor: pointer;
    transition: all 0.1s;
    clip-path: polygon(0 0, calc(100% - 4px) 0, 100% 4px, 100% 100%, 4px 100%, 0 calc(100% - 4px));
  }
  .mini-btn:hover { border-color: var(--accent); color: var(--accent); }

  .list {
    flex: 1;
    overflow-y: auto;
  }

  .tr-row {
    padding: 9px 14px;
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 5px;
    cursor: default;
    transition: background 0.06s;
  }
  .tr-row:hover { background: rgba(255, 255, 255, 0.02); }
  .tr-row.error { background: rgba(255, 90, 90, 0.04); }
  .tr-row.error:hover { background: rgba(255, 90, 90, 0.08); }

  .tr-top {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 10px;
  }
  .dir {
    width: 14px;
    text-align: center;
    font-size: 11px;
    flex-shrink: 0;
    font-weight: 700;
  }
  .dir.up    { color: var(--info); }
  .dir.down  { color: var(--accent); }
  .dir.ok    { color: var(--accent); }
  .dir.err   { color: var(--crit); }
  .dir.pause { color: var(--warn); }

  .name {
    flex: 1;
    color: var(--ink);
    font-size: 10.5px;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    letter-spacing: 0.2px;
  }
  .size {
    color: var(--fg-dim);
    font-size: 9px;
    font-feature-settings: "tnum";
    flex-shrink: 0;
    letter-spacing: 0.5px;
  }
  .size .tot { color: var(--fg-mute); }

  .progress-row {
    display: grid;
    grid-template-columns: 1fr 50px 50px 22px 22px;
    gap: 7px;
    align-items: center;
  }
  .pct {
    color: var(--ink);
    font-weight: 600;
    text-align: right;
    font-feature-settings: "tnum";
    font-size: 9.5px;
    letter-spacing: 0.3px;
  }
  .pct.pause { color: var(--warn); }
  .pct.err   { color: var(--crit); }

  .eta {
    color: var(--fg-dim);
    text-align: right;
    font-feature-settings: "tnum";
    font-size: 9px;
  }

  .iconbtn {
    width: 20px; height: 18px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--fg-dim);
    font-family: inherit;
    font-size: 9px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: all 0.1s;
    clip-path: polygon(0 0, calc(100% - 3px) 0, 100% 3px, 100% 100%, 3px 100%, 0 calc(100% - 3px));
  }
  .iconbtn:hover { border-color: var(--accent); color: var(--accent); }
  .iconbtn.danger:hover { border-color: var(--crit); color: var(--crit); }

  .meta {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 9px;
    color: var(--fg-mute);
    letter-spacing: 0.4px;
    padding-left: 22px;
  }
  .path {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .speed {
    color: var(--fg-dim);
    font-feature-settings: "tnum";
    flex-shrink: 0;
  }
  .speed.up   { color: var(--info); }
  .speed.down { color: var(--accent); }
  .tstamp { color: var(--fg-faint); }
  .err-msg { color: var(--crit); opacity: 0.85; }
</style>
