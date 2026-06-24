<script>
  /**
   * Services · Widget de servicios (NimHealth) · NimOS Beta 8.1
   * ────────────────────────────────────────────────────────────
   * Cubo de estado (esquinas redondeadas, lenguaje del logo) + nombre
   * por servicio. LO QUE FALLA SUBE ARRIBA SOLO: orden por gravedad,
   * caídos primero, en lectura por columna.
   *
   * Datos: /api/services vía topic 'services' (10s).
   * Forma: { services: [{id, appId, status, health, ...}] }
   *   status ∈ running|stopped|starting|stopping|failed|error|unknown
   *   health ∈ healthy|degraded|unreachable|unknown
   *
   * Gravedad → cubo:
   *   crit (rojo, pulsa): status failed|error
   *   warn (ámbar): status stopped|stopping|starting|unknown
   *                 o health degraded|unreachable
   *   ok  (verde): el resto
   *
   * Tallas → columnas de 4 en paralelo (decisión jun 2026):
   *   1×1 → 1 columna × 4 · 2×1 → 2 columnas × 4 ·
   *   2×2 → 2 columnas × 10. Relleno por columna: la 1ª columna son
   *   los primeros (graves arriba-izquierda). Si hay más: "+N".
   *
   * Estado sano (lo normal): N/N grande y silencio — un widget de
   * salud que grita cuando todo va bien está mal diseñado.
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export let w = 2;
  export let h = 1;

  const data = topicStore('services');
  onMount(() => acquire('services'));

  function sev(s) {
    if (s.status === 'failed' || s.status === 'error') return 0; // crit
    if (
      s.status === 'stopped' || s.status === 'stopping' ||
      s.status === 'starting' || s.status === 'unknown' ||
      s.health === 'degraded' || s.health === 'unreachable'
    ) return 1; // warn
    return 2; // ok
  }
  const SEV_CLASS = ['crit', 'warn', ''];

  $: services = Array.isArray($data?.services) ? $data.services : null;
  $: sorted = services
    ? [...services].sort((a, b) => sev(a) - sev(b) || String(a.appId).localeCompare(String(b.appId)))
    : [];
  $: nCrit = sorted.filter(s => sev(s) === 0).length;
  $: nWarn = sorted.filter(s => sev(s) === 1).length;
  $: nOk = sorted.length - nCrit - nWarn;
  $: allOk = services && sorted.length > 0 && nCrit === 0 && nWarn === 0;

  // Regla de llenado (decisión jun 2026): la 1ª columna se llena
  // HASTA EL TOPE (4 en altura 1, 8 en altura 2) y solo entonces se
  // salta a la siguiente. Nada de balancear.
  $: rows = h >= 2 ? 8 : 4;
  $: cols = w >= 2 ? 2 : 1;
  $: visible = sorted.slice(0, rows * cols);
  $: extra = Math.max(0, sorted.length - visible.length);
  // `list` por argumento: lección de reactividad de Network.svelte
  $: colChunks = chunkCols(visible, rows);
  function chunkCols(list, n) {
    const out = [];
    for (let i = 0; i < list.length; i += n) out.push(list.slice(i, i + n));
    return out;
  }
</script>

<div class="svcs">
  <div class="head">
    <span class="title">{w >= 2 ? 'Servicios' : 'SVC'}</span>
    <span class="sum">
      {#if !services}—
      {:else if sorted.length === 0}0
      {:else}
        {#if nCrit > 0}<b class="c">{nCrit}{w >= 2 ? ' caído' + (nCrit > 1 ? 's' : '') : ''}</b>{/if}
        {#if nCrit > 0 && (nWarn > 0 || nOk > 0)}<span class="sep">·</span>{/if}
        {#if nWarn > 0}<b class="w">{nWarn}{w >= 2 ? ' parado' + (nWarn > 1 ? 's' : '') : ''}</b>{/if}
        {#if nWarn > 0 && nOk > 0}<span class="sep">·</span>{/if}
        {#if nOk > 0}<b class="g">{nOk} OK</b>{/if}
      {/if}
    </span>
  </div>

  {#if !services}
    <div class="empty">— · —</div>
  {:else if sorted.length === 0}
    <div class="empty">sin servicios</div>
  {:else if allOk}
    <div class="all-ok">
      <div class="big">{sorted.length}/{sorted.length}</div>
      <div class="lbl">{w >= 2 ? 'todos operativos' : 'OK'}</div>
    </div>
  {:else}
    <div class="list">
      <div class="cols">
        {#each colChunks as chunk, i (i)}
          <div class="colwrap">
            {#each chunk as s (s.id)}
              <div class="svc">
                <span class="cube {SEV_CLASS[sev(s)]}"></span>
                <span class="sn" class:hot={sev(s) === 0}>{s.appId || s.id}</span>
              </div>
            {/each}
          </div>
        {/each}
        {#if cols === 2 && colChunks.length === 1}
          <div class="colwrap"></div>
        {/if}
      </div>
    </div>
    {#if extra > 0}
      <div class="more">+{extra} más</div>
    {/if}
  {/if}
</div>

<style>
  .svcs {
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
    gap: 8px;
  }
  .title {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .sum {
    font-family: var(--font-mono);
    font-size: 9px;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }
  .sum b { font-weight: 600; }
  .sum .c { color: var(--crit); }
  .sum .w { color: var(--warn); }
  .sum .g { color: var(--signal); }
  .sum .sep { color: var(--ink-trace); margin: 0 3px; }

  /* columnas en paralelo · la 1ª se llena al tope y luego la 2ª.
     El bloque entero se centra en vertical; las columnas quedan
     alineadas arriba entre sí (la 2ª empieza a la altura de la 1ª). */
  .list {
    flex: 1;
    display: flex;
    flex-direction: column;
    justify-content: center;
    min-height: 0;
  }
  .cols {
    display: flex;
    gap: 14px;
    align-items: flex-start;
  }
  .colwrap {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .svc {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }

  /* cubo de estado · esquinas redondeadas a lo NimOS */
  .cube {
    flex: none;
    width: 9px;
    height: 9px;
    border-radius: 3px;
    background: var(--signal);
    box-shadow: 0 0 7px var(--signal-glow);
  }
  .cube.warn { background: var(--warn); box-shadow: 0 0 7px var(--warn); }
  .cube.crit {
    background: var(--crit);
    box-shadow: 0 0 7px var(--crit);
    animation: svc-pulse 1.6s ease-in-out infinite;
  }
  @keyframes svc-pulse { 50% { opacity: 0.35; } }

  .sn {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sn.hot { color: var(--ink); }

  /* estado sano · discreto */
  .all-ok {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
  }
  .all-ok .big {
    font-family: var(--font-mono);
    font-size: 22px;
    font-weight: 600;
    color: var(--signal);
  }
  .all-ok .lbl {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-mute);
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .more {
    font-family: var(--font-mono);
    font-size: 8.5px;
    color: var(--ink-faint);
    text-align: center;
    padding-top: 4px;
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
