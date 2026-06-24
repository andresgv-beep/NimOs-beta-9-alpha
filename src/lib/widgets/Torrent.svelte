<script>
  /**
   * Torrent · Widget NimTorrent · NimOS Beta 8.1
   * ─────────────────────────────────────────────
   * Tareas de torrentd con la filosofía del escritorio: lo que
   * requiere atención sube arriba (error → descargando → pausado →
   * sembrando).
   *
   * Datos: /api/torrent/torrents vía topic 'torrent' (5s) — el
   * proxy Go a torrentd que ya usa la app NimTorrent. Agregados
   * (activos, rates) calculados en cliente, sin segundo poll.
   * Forma: [{hash, name, state, progress(0..1), download_rate,
   *          upload_rate, total_done, total_wanted, peers, paused}]
   *
   * Tallas: 2×1 → 2 tareas (lo que cabe digno) · 2×2 → 5 con dato
   * extra por fila (GB hechos/total). Más tareas → "+N".
   * torrentd caído → el proxy falla → store en null → skeleton.
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export const w = 2;
  export let h = 1;

  const data = topicStore('torrent');
  onMount(() => acquire('torrent'));

  function rank(t) {
    if (t.state === 'error') return 0;
    if (t.state === 'downloading' && !t.paused) return 1;
    if (t.paused || t.state === 'paused') return 2;
    return 3; // seeding / finished
  }

  $: torrents = Array.isArray($data) ? $data : null;
  $: sorted = torrents
    ? [...torrents].sort((a, b) => rank(a) - rank(b) || (b.progress || 0) - (a.progress || 0))
    : [];
  $: nActive = sorted.filter(t => rank(t) <= 1).length;
  $: aggDl = sorted.reduce((a, t) => a + (t.download_rate || 0), 0);
  $: aggUl = sorted.reduce((a, t) => a + (t.upload_rate || 0), 0);

  $: maxRows = h >= 2 ? 5 : 2;
  $: visible = sorted.slice(0, maxRows);
  $: extra = Math.max(0, sorted.length - maxRows);

  function fmtRate(b) {
    if (!b || b < 1) return '0 B/s';
    if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MB/s';
    if (b >= 1024) return (b / 1024).toFixed(0) + ' KB/s';
    return Math.round(b) + ' B/s';
  }
  function fmtGB(b) {
    if (b == null) return '—';
    return (b / 1073741824).toFixed(1);
  }
  function fmtETA(t) {
    if (t.state === 'seeding') return '∞';
    if (t.state === 'error') return 'error';
    if (t.paused || t.state === 'paused') return 'pausa';
    const remaining = (t.total_wanted || 0) - (t.total_done || 0);
    if (remaining <= 0) return '—';
    if (!t.download_rate || t.download_rate < 1) return '∞';
    let secs = Math.round(remaining / t.download_rate);
    if (secs >= 86400) return Math.floor(secs / 86400) + 'd';
    if (secs >= 3600) return Math.floor(secs / 3600) + 'h';
    if (secs >= 60) return Math.floor(secs / 60) + 'm';
    return secs + 's';
  }
  function pct(t) { return Math.round((t.progress || 0) * 100); }
  function isDone(t) { return rank(t) === 3; }
  // estado → color de la BARRA (el texto va siempre en blanco):
  // azul descargando · ámbar pausa · verde completo · rojo error
  function barState(t) {
    if (t.state === 'error') return 'err';
    if (isDone(t)) return 'done';
    if (t.paused || t.state === 'paused') return 'paused';
    return 'dl';
  }
</script>

<div class="tor">
  <div class="head">
    <span class="count">{torrents ? nActive : '—'}<small>activos</small></span>
    <span class="agg">
      <span class="d">↓ {torrents ? fmtRate(aggDl) : '—'}</span>
      <span class="u">↑ {torrents ? fmtRate(aggUl) : '—'}</span>
    </span>
  </div>

  {#if !torrents}
    <div class="empty">— · —</div>
  {:else if sorted.length === 0}
    <div class="empty">sin torrents</div>
  {:else}
    <div class="list">
      {#each visible as t (t.hash)}
        <div class="row">
          <div class="tn">{t.name}</div>
          <div class="bar">
            <i class={barState(t)} style="width:{pct(t)}%"></i>
          </div>
          <div class="meta">
            {#if isDone(t)}
              <span class="ok">completo{#if h >= 2 && t.peers != null} · {t.peers} peers{/if}</span>
              <span class="seed">↑ sembrando</span>
            {:else}
              <span>{pct(t)}%{#if h >= 2} · {fmtGB(t.total_done)}/{fmtGB(t.total_wanted)} GB{/if} · ↓ {fmtRate(t.download_rate)}</span>
              <span class="eta">ETA {fmtETA(t)}</span>
            {/if}
          </div>
        </div>
      {/each}
      {#if extra > 0}
        <div class="more">+{extra} más</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .tor {
    height: 100%;
    display: flex;
    flex-direction: column;
    padding: 13px 14px;
    user-select: none;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-bottom: 8px;
    margin-bottom: 8px;
    border-bottom: 1px solid var(--line);
  }
  .count {
    font-family: var(--font-mono);
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
    line-height: 1;
  }
  .count small {
    font-size: 8.5px;
    color: var(--ink-mute);
    font-weight: 400;
    margin-left: 4px;
  }
  .agg {
    font-family: var(--font-mono);
    font-size: 9px;
    display: flex;
    gap: 8px;
  }
  .agg .d { color: var(--signal); }
  .agg .u { color: var(--nim-remote); }

  .list {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 8px;
    justify-content: center;
    min-height: 0;
  }
  .tn {
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--ink); /* SIEMPRE blanco · el estado lo dice la barra */
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    margin-bottom: 4px;
  }

  .bar {
    height: 4px;
    border-radius: 4px;
    background: rgba(255, 255, 255, 0.06);
    overflow: hidden;
  }
  .bar i {
    display: block;
    height: 100%;
    border-radius: 4px;
    transition: width 0.5s cubic-bezier(0.4, 0, 0.2, 1), background 0.3s ease;
  }
  .bar i.dl {
    background: linear-gradient(90deg, var(--nim-remote), #2e9fe6);
    box-shadow: 0 0 8px rgba(77, 184, 255, 0.35);
  }
  .bar i.paused {
    background: linear-gradient(90deg, var(--warn), #f59e0b);
    box-shadow: 0 0 8px rgba(251, 191, 36, 0.3);
  }
  .bar i.done {
    background: linear-gradient(90deg, var(--signal), hsl(155, 100%, 42%));
    box-shadow: 0 0 8px var(--signal-glow);
  }
  .bar i.err {
    background: linear-gradient(90deg, var(--crit), #ef4444);
    box-shadow: 0 0 8px var(--crit);
  }

  .meta {
    display: flex;
    justify-content: space-between;
    font-family: var(--font-mono);
    font-size: 8px;
    color: var(--ink-faint);
    margin-top: 4px;
    gap: 8px;
  }
  .meta .eta { color: var(--signal); font-weight: 600; }
  .meta .ok { color: var(--signal); }
  .meta .seed { color: var(--nim-remote); }

  .more {
    font-family: var(--font-mono);
    font-size: 8.5px;
    color: var(--ink-faint);
    text-align: center;
  }
  .empty {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-faint);
  }
</style>
