<script>
  /**
   * Network · Widget de red · NimOS Beta 8.1
   * ─────────────────────────────────────────
   * Tasas DL/UL con sparkline de histórico reciente.
   *
   * Datos: /api/network vía topic 'network' (3s). El daemon devuelve
   * un ARRAY de interfaces up: [{name, ip, speed, rxRate, txRate,
   * rxBytes, txBytes}] con las tasas YA calculadas (bytes/s).
   *
   * Decisión: si hay varias NICs activas se AGREGAN las tasas (el
   * widget mide cuánto mueve el NAS, no una interfaz). Cabecera:
   * nombre si hay una, "N ifaces" si hay más.
   *
   * El histórico de la sparkline vive en el componente (~24 muestras
   * ≈ 72s de ventana). Muere con el widget — sin estado persistente.
   *
   * Tallas: 2×1 → tasas + sparklines · 2×2 → añade IP, velocidad de
   * enlace y totales desde arranque.
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export const w = 2;
  export let h = 1;

  const data = topicStore('network');
  onMount(() => acquire('network'));

  const MAX_SAMPLES = 24;
  let samples = []; // [{rx, tx}] bytes/s agregados
  let lastSeen = null;

  $: ingest($data);
  function ingest(d) {
    if (!d || d === lastSeen || !Array.isArray(d)) return;
    lastSeen = d;
    const rx = d.reduce((a, i) => a + (i.rxRate || 0), 0);
    const tx = d.reduce((a, i) => a + (i.txRate || 0), 0);
    samples = [...samples.slice(-(MAX_SAMPLES - 1)), { rx, tx }];
  }

  $: ifaces = Array.isArray($data) ? $data : null;
  $: cur = samples.length ? samples[samples.length - 1] : null;
  $: label = !ifaces || ifaces.length === 0 ? '—'
    : ifaces.length === 1 ? ifaces[0].name
    : `${ifaces.length} ifaces`;
  $: primary = ifaces?.[0] ?? null;
  $: totRx = ifaces ? ifaces.reduce((a, i) => a + (i.rxBytes || 0), 0) : 0;
  $: totTx = ifaces ? ifaces.reduce((a, i) => a + (i.txBytes || 0), 0) : 0;

  // bytes/s → cifra + unidad legibles
  function fmtRate(b) {
    if (b == null) return ['—', ''];
    if (b >= 1073741824) return [(b / 1073741824).toFixed(1), 'GB/s'];
    if (b >= 1048576) return [(b / 1048576).toFixed(1), 'MB/s'];
    if (b >= 1024) return [(b / 1024).toFixed(0), 'KB/s'];
    return [String(b), 'B/s'];
  }
  function fmtTotal(b) {
    const TB = 1099511627776, GB = 1073741824, MB = 1048576;
    if (b >= TB) return (b / TB).toFixed(1) + ' TB';
    if (b >= GB) return (b / GB).toFixed(1) + ' GB';
    return (b / MB).toFixed(0) + ' MB';
  }

  // sparkline: path SVG normalizado al máximo de la ventana.
  // OJO: `list` viene por argumento a propósito — si la función
  // leyera `samples` del scope, la reactividad de Svelte no la
  // detectaría como dependencia y la sparkline no se redibujaría
  // jamás (bug cazado en hardware real, jun 2026).
  function spark(list, key) {
    if (list.length < 2) return { line: '', area: '' };
    const W = 100, H = 32;
    const vals = list.map(s => s[key]);
    const max = Math.max(...vals, 1);
    const step = W / (list.length - 1);
    let d = '';
    vals.forEach((v, i) => {
      const x = i * step;
      const y = H - 2 - (v / max) * (H - 6);
      d += (i ? 'L' : 'M') + x.toFixed(1) + ' ' + y.toFixed(1) + ' ';
    });
    return { line: d, area: `${d}L${W} ${H} L0 ${H} Z` };
  }
  $: dl = spark(samples, 'rx');
  $: ul = spark(samples, 'tx');
  $: dlRate = fmtRate(cur?.rx);
  $: ulRate = fmtRate(cur?.tx);
</script>

<div class="net">
  <div class="head">
    <span class="title">Red · {label}</span>
    {#if h >= 2 && primary}
      <span class="aux">{primary.speed}</span>
    {/if}
  </div>

  {#if !ifaces}
    <div class="empty">— · —</div>
  {:else if ifaces.length === 0}
    <div class="empty">sin interfaces activas</div>
  {:else}
    <div class="cols">
      <div class="col dl">
        <div class="rate"><span class="arrow">↓</span><span class="num">{dlRate[0]}</span><span class="unit">{dlRate[1]}</span></div>
        <svg class="spark" viewBox="0 0 100 32" preserveAspectRatio="none">
          <path d={dl.area} class="area" />
          <path d={dl.line} class="line" />
        </svg>
      </div>
      <div class="col ul">
        <div class="rate"><span class="arrow">↑</span><span class="num">{ulRate[0]}</span><span class="unit">{ulRate[1]}</span></div>
        <svg class="spark" viewBox="0 0 100 32" preserveAspectRatio="none">
          <path d={ul.area} class="area" />
          <path d={ul.line} class="line" />
        </svg>
      </div>
    </div>

    {#if h >= 2}
      <div class="detail">
        <div class="row"><span class="k">IP</span><span class="v">{primary?.ip ?? '—'}</span></div>
        <div class="row"><span class="k">Total ↓</span><span class="v">{fmtTotal(totRx)}</span></div>
        <div class="row"><span class="k">Total ↑</span><span class="v">{fmtTotal(totTx)}</span></div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .net {
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
    margin-bottom: 8px;
  }
  .title {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .aux {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-mute);
  }

  .cols {
    flex: 1;
    display: flex;
    gap: 12px;
    align-items: stretch;
    min-height: 0;
  }
  .col {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .rate {
    display: flex;
    align-items: baseline;
    gap: 4px;
  }
  .arrow { font-family: var(--font-mono); font-size: 11px; }
  .num { font-family: var(--font-mono); font-size: 17px; font-weight: 600; }
  .unit { font-family: var(--font-mono); font-size: 9px; color: var(--ink-faint); }
  .dl .arrow, .dl .num { color: var(--signal); }
  .ul .arrow, .ul .num { color: var(--nim-remote); }

  .spark {
    flex: 1;
    margin-top: 6px;
    min-height: 28px;
    width: 100%;
  }
  .dl .line { fill: none; stroke: var(--signal); stroke-width: 1.5; stroke-linejoin: round; stroke-linecap: round; }
  .dl .area { fill: var(--signal); opacity: 0.10; }
  .ul .line { fill: none; stroke: var(--nim-remote); stroke-width: 1.5; stroke-linejoin: round; stroke-linecap: round; }
  .ul .area { fill: var(--nim-remote); opacity: 0.10; }

  /* bloque extra de la talla 2×2 */
  .detail {
    margin-top: 10px;
    padding-top: 9px;
    border-top: 1px solid var(--line);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .row {
    display: flex;
    justify-content: space-between;
  }
  .k {
    font-family: var(--font-mono);
    font-size: 9px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .v {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-dim);
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
