<script>
  import { createEventDispatcher } from 'svelte';
  import { formatBytes, formatRate, formatEta, percent, visualState, stateLabels } from './formatters.js';
  export let torrent = null;
  export let busy = false;
  const dispatch = createEventDispatcher();
</script>

<section class="detail" aria-label="Detalle del torrent">
  {#if torrent}
    {@const state = visualState(torrent)}
    <header>
      <div class="heading">
        <div class="eyebrow"><span class="dot {state}"></span>{stateLabels[torrent.state] || torrent.state}</div>
        <h2>{torrent.name}</h2>
        <p>{torrent.save_path || 'Destino no disponible'}</p>
      </div>
      <div class="actions">
        <button on:click={() => dispatch('toggle', torrent)} disabled={busy}>{torrent.paused || torrent.state === 'paused' ? 'Reanudar' : 'Pausar'}</button>
        <button class="danger" on:click={() => dispatch('remove', torrent)} disabled={busy}>Quitar</button>
      </div>
    </header>

    <div class="progress-head"><strong>{percent(torrent.progress)}%</strong><span>{formatBytes(torrent.total_done)} de {formatBytes(torrent.total_wanted)}</span></div>
    <div class="progress"><span class="fill {state}" style="width:{percent(torrent.progress)}%"></span></div>

    <div class="metrics">
      <div><span>Descarga</span><strong class="accent">{formatRate(torrent.download_rate)}</strong></div>
      <div><span>Subida</span><strong>{formatRate(torrent.upload_rate)}</strong></div>
      <div><span>Conexiones</span><strong>{torrent.peers ?? 0} <small>peers</small></strong></div>
      <div><span>Tiempo restante</span><strong>{formatEta(torrent)}</strong></div>
    </div>
    <div class="hash"><span>Identificador</span><code>{torrent.hash}</code></div>
  {/if}
</section>

<style>
  .detail { min-height: 210px; padding: 18px 20px; border-top: 1px solid var(--line); background: color-mix(in srgb, var(--bg-card) 42%, transparent); overflow: auto; }
  header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; }
  .heading { min-width: 0; }
  .eyebrow { display: flex; align-items: center; gap: 7px; color: var(--ink-mute); font-size: 11px; margin-bottom: 5px; }
  .dot { width: 7px; height: 7px; border-radius: 2px; background: var(--ink-mute); }
  .dot.downloading, .dot.checking { background: var(--metric); }
  .dot.seeding { background: var(--info, #55b7f3); }
  .dot.error { background: var(--crit); }
  h2 { margin: 0; color: var(--ink); font-size: 16px; line-height: 1.35; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  p { margin: 5px 0 0; color: var(--ink-mute); font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .actions { display: flex; gap: 8px; flex: 0 0 auto; }
  button { padding: 7px 13px; border: 1px solid var(--line); border-radius: 6px; background: var(--bg-window); color: var(--ink-dim); font: inherit; font-size: 11px; cursor: pointer; }
  button:hover:not(:disabled) { color: var(--ink); background: var(--side-hover); }
  button.danger:hover:not(:disabled) { color: var(--crit); border-color: color-mix(in srgb, var(--crit) 45%, transparent); }
  button:disabled { opacity: .45; cursor: default; }
  .progress-head { display: flex; justify-content: space-between; align-items: baseline; margin-top: 20px; font-size: 11px; color: var(--ink-mute); }
  .progress-head strong { color: var(--metric); font-size: 18px; }
  .progress { height: 5px; margin-top: 7px; overflow: hidden; border-radius: 3px; background: var(--line); }
  .fill { display: block; height: 100%; background: var(--metric); }
  .fill.seeding { background: var(--info, #55b7f3); }
  .fill.paused { background: var(--ink-mute); }
  .fill.error { background: var(--crit); }
  .metrics { display: grid; grid-template-columns: repeat(4, 1fr); margin-top: 18px; border: 1px solid var(--line); border-radius: 7px; overflow: hidden; }
  .metrics > div { padding: 12px 14px; border-right: 1px solid var(--line); display: grid; gap: 6px; }
  .metrics > div:last-child { border: 0; }
  .metrics span, .hash span { color: var(--ink-mute); font-size: 10px; }
  .metrics strong { color: var(--ink); font-size: 13px; font-variant-numeric: tabular-nums; }
  .metrics strong.accent { color: var(--metric); }
  small { color: var(--ink-mute); font-size: 10px; font-weight: 400; }
  .hash { display: flex; align-items: center; gap: 14px; margin-top: 14px; min-width: 0; }
  code { color: var(--ink-dim); font-size: 10px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  @media (max-width: 900px) { .metrics { grid-template-columns: repeat(2, 1fr); } .metrics > div:nth-child(2) { border-right: 0; } .metrics > div:nth-child(-n+2) { border-bottom: 1px solid var(--line); } }
</style>
