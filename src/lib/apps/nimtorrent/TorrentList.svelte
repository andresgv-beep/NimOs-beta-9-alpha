<script>
  import { createEventDispatcher } from 'svelte';
  import { formatBytes, formatRate, formatEta, percent, visualState } from './formatters.js';

  export let torrents = [];
  export let selectedHash = null;
  export let loading = false;
  export let error = '';

  const dispatch = createEventDispatcher();
</script>

<section class="list" aria-label="Lista de torrents">
  <div class="head" aria-hidden="true">
    <span>Nombre y progreso</span><span>Tamaño</span><span>Descarga</span><span>Subida</span><span>Conexiones</span><span>Tiempo</span>
  </div>
  <div class="body">
    {#if loading}
      <div class="message">Cargando descargas…</div>
    {:else if error}
      <div class="message error">{error}</div>
    {:else if torrents.length === 0}
      <div class="message">No hay descargas en esta vista.</div>
    {:else}
      {#each torrents as torrent (torrent.hash)}
        {@const state = visualState(torrent)}
        <button class:selected={torrent.hash === selectedHash} class="row" on:click={() => dispatch('select', torrent.hash)}>
          <div class="identity">
            <span class="state {state}"></span>
            <div class="name-block">
              <span class="name">{torrent.name}</span>
              <div class="progress"><span class="fill {state}" style="width:{percent(torrent.progress)}%"></span></div>
            </div>
          </div>
          <span>{formatBytes(torrent.total_wanted)}</span>
          <span class:accent={torrent.download_rate > 0}>{formatRate(torrent.download_rate)}</span>
          <span>{formatRate(torrent.upload_rate)}</span>
          <span>{torrent.peers ?? 0} peers</span>
          <span>{formatEta(torrent)}</span>
        </button>
      {/each}
    {/if}
  </div>
</section>

<style>
  .list { min-height: 0; display: flex; flex-direction: column; overflow: hidden; background: transparent; }
  .head, .row { display: grid; grid-template-columns: minmax(240px, 2fr) 90px 100px 90px 100px 100px; gap: 14px; align-items: center; }
  .head { padding: 11px 20px; color: var(--ink-mute); font-size: 11px; border-bottom: 1px solid var(--line); background: color-mix(in srgb, var(--bg-card) 55%, transparent); }
  .body { min-height: 0; overflow: auto; }
  .row { width: 100%; padding: 13px 20px; border: 0; border-bottom: 1px solid var(--line); background: transparent; color: var(--ink-dim); font: inherit; font-size: 12px; text-align: left; cursor: pointer; font-variant-numeric: tabular-nums; }
  .row:last-child { border-bottom: 0; }
  .row:hover { background: var(--side-hover); }
  .row.selected { background: var(--ui-select-bg); box-shadow: inset 3px 0 var(--signal); }
  .identity { min-width: 0; display: flex; align-items: center; gap: 10px; }
  .state { width: 7px; height: 7px; border-radius: 2px; flex: 0 0 auto; background: var(--ink-mute); }
  .state.downloading, .state.checking { background: var(--signal); }
  .state.seeding { background: var(--info, #55b7f3); }
  .state.error { background: var(--crit); }
  .name-block { min-width: 0; flex: 1; display: grid; gap: 6px; }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink); font-weight: 600; }
  .progress { height: 3px; overflow: hidden; background: var(--line); border-radius: 2px; }
  .fill { display: block; height: 100%; background: var(--signal); }
  .fill.seeding { background: var(--info, #55b7f3); }
  .fill.paused { background: var(--ink-mute); }
  .fill.error { background: var(--crit); }
  .accent { color: var(--signal); }
  .message { height: 100%; min-height: 170px; display: grid; place-items: center; color: var(--ink-mute); font-size: 13px; }
  .message.error { color: var(--crit); }
  @media (max-width: 1050px) {
    .head, .row { grid-template-columns: minmax(220px, 2fr) 90px 90px 90px; }
    .head span:nth-child(5), .head span:nth-child(6), .row > span:nth-child(5), .row > span:nth-child(6) { display: none; }
  }
</style>
