<script>
  /**
   * Storage · Widget de pools · NimOS Beta 8.1 (rediseño jun 2026)
   * ──────────────────────────────────────────────────────────────
   * Ficha por pool con cards-caja (Usado / Disponible / Tipo / Salud).
   *
   * Dos densidades según talla, porque la ficha "holgada" no cabe en
   * un 2×1 real (144px → presupuesto ~118px útiles):
   *   - 2×1 (compacto): cabecera en UNA línea (estado · nombre · cap),
   *     cards bajas (~52px) con número + sub. UN pool.
   *   - 2×2 (holgado): cabecera del widget + nombre en líneas propias,
   *     cards altas. Varios pools apilados con scroll.
   *
   * config.pools: [] o ausente = auto (todos / el primero según talla).
   *
   * Datos: /api/storage/v2/pools vía topic 'storage' (15s). Forma
   * ENVUELTA { data:[Pool] }. Pool: { name, profile, mounted,
   *   usage:{usage_percent,used_bytes,available_bytes,total_bytes},
   *   health:{status} }.
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export const w = 2;
  export let h = 1;
  export let config = {};

  const data = topicStore('storage');
  onMount(() => acquire('storage'));

  $: allPools = Array.isArray($data?.data) ? $data.data : null;
  $: multi = h >= 2;

  // Modelo multi-instancia: cada caja muestra el pool de config.pool.
  // Compatibilidad: config.pools (lista) y ausencia = auto.
  $: wanted = config?.pool ? [config.pool]
    : Array.isArray(config?.pools) ? config.pools
    : [];
  $: shown = (() => {
    if (!allPools) return null;
    let sel = wanted.length
      ? allPools.filter(p => wanted.includes(p.name))
      : allPools;
    if (!multi) sel = sel.slice(0, 1);
    return sel;
  })();

  $: anyBad = (allPools || []).some(p => healthClass(p) !== 'ok');

  // Estados reales del daemon (HealthStatus en nimos_health.go):
  // healthy | degraded | failed | partial | incomplete | unknown | stale
  function healthClass(p) {
    const s = p?.health?.status;
    if (s == null) return 'ok';            // aún sin dato → no alarmar (skeleton)
    if (!p?.mounted || s === 'failed') return 'crit';
    if (s === 'healthy') return 'ok';
    // degraded | partial | incomplete | stale | unknown | cualquier otro
    return 'warn';
  }
  function barClass(pct) {
    if (pct >= 90) return 'crit';
    if (pct >= 80) return 'hot';
    return '';
  }
  function split(b) {
    if (b == null) return { n: '—', u: '' };
    const TB = 1099511627776, GB = 1073741824;
    if (b >= TB) return { n: (b / TB).toFixed(1), u: 'TB' };
    return { n: (b / GB).toFixed(0), u: 'GB' };
  }
  function fmtBytes(b) {
    const s = split(b);
    return s.u ? `${s.n} ${s.u}` : s.n;
  }
</script>

<div class="storage" class:big={multi}>
  {#if multi}
    <div class="head">
      <span class="title">Almacenamiento</span>
      <span class="aux" class:bad={anyBad}>
        {#if !allPools}—{:else if allPools.length === 0}sin pools{:else if anyBad}atención{:else}OK{/if}
      </span>
    </div>
  {/if}

  {#if !shown}
    <div class="empty">— · —</div>
  {:else if shown.length === 0}
    <div class="empty">sin pool seleccionado</div>
  {:else}
    <div class="list" class:scroll={multi}>
      {#each shown as p (p.id ?? p.name)}
        {@const pct = p.usage?.usage_percent ?? 0}
        {@const hc = healthClass(p)}
        {@const used = split(p.usage?.used_bytes)}
        {@const avail = split(p.usage?.available_bytes)}
        <div class="pool">
          {#if multi}
            <!-- 2×2: nombre + capacidad en su propia línea -->
            <div class="pool-head">
              <span class="name">{p.name}{#if !p.mounted}<small> · sin montar</small>{/if}</span>
              <span class="cap">{fmtBytes(p.usage?.used_bytes)} / {fmtBytes(p.usage?.total_bytes)}</span>
            </div>
          {:else}
            <!-- 2×1: cabecera compacta en una sola línea -->
            <div class="pool-head compact">
              <span class="badge {hc}">{hc === 'ok' ? 'OK' : 'ATENCIÓN'}</span>
              <span class="name">{p.name}</span>
              <span class="cap">{fmtBytes(p.usage?.used_bytes)} / {fmtBytes(p.usage?.total_bytes)}</span>
            </div>
          {/if}

          <div class="bar"><i class={barClass(pct)} style="width:{pct}%"></i></div>

          <div class="cards">
            <div class="c">
              <span class="c-label">Usado</span>
              <span class="c-num">{used.n}<small>{used.u}</small></span>
              <span class="c-sub accent">{pct}%</span>
            </div>
            <div class="c">
              <span class="c-label">Disponible</span>
              <span class="c-num">{avail.n}<small>{avail.u}</small></span>
              <span class="c-sub accent">{100 - pct}%</span>
            </div>
            <div class="c">
              <span class="c-label">Tipo</span>
              <span class="c-num">BTRFS</span>
              <span class="c-sub accent">{p.profile ?? '—'}</span>
            </div>
            <div class="c">
              <span class="c-label">Salud</span>
              <span class="c-shield {hc}" aria-hidden="true">
                {#if hc === 'ok'}
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                    <path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6Z" />
                    <path d="M9 12l2 2 4-4" />
                  </svg>
                {:else if hc === 'crit'}
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                    <path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6Z" />
                    <path d="M9.5 9.5l5 5M14.5 9.5l-5 5" />
                  </svg>
                {:else}
                  <!-- warning: triángulo de alerta -->
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round">
                    <path d="M12 4 22 19 2 19 Z" />
                    <path d="M12 10v4M12 17h.01" />
                  </svg>
                {/if}
              </span>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .storage {
    height: 100%;
    display: flex;
    flex-direction: column;
    padding: 12px 14px;
    user-select: none;
  }
  .storage.big { padding: 15px 17px; }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 14px;
  }
  .title {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .aux {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--signal);
  }
  .aux.bad { color: var(--warn); }

  .list {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-height: 0;
    justify-content: center;
  }
  .list.scroll { overflow-y: auto; justify-content: flex-start; padding-right: 4px; }
  .list.scroll::-webkit-scrollbar { width: 5px; }
  .list.scroll::-webkit-scrollbar-thumb { background: var(--line-bright); border-radius: 3px; }

  .pool { flex-shrink: 0; }

  /* ── Cabecera 2×2 (dos bloques) ── */
  .pool-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 9px;
  }
  .name {
    font-family: var(--font-mono);
    font-size: 16px;
    color: var(--ink);
  }
  .name small { font-size: 10px; color: var(--warn); }
  .cap {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--ink-mute);
  }

  /* ── Cabecera 2×1 (una línea compacta) ── */
  .pool-head.compact {
    align-items: center;
    gap: 10px;
    margin-bottom: 7px;
  }
  .pool-head.compact .badge {
    font-family: var(--font-mono);
    font-size: 8px;
    letter-spacing: 0.1em;
    color: var(--signal);
    border: 1px solid var(--signal-dim);
    border-radius: var(--radius-sm);
    padding: 1px 5px;
    flex-shrink: 0;
  }
  .pool-head.compact .badge.warn { color: var(--warn); border-color: var(--warn); }
  .pool-head.compact .badge.crit { color: var(--crit); border-color: var(--crit); }
  .pool-head.compact .name { font-size: 14px; flex: 1; }
  .pool-head.compact .cap { font-size: 10.5px; }

  .bar {
    height: 6px;
    border-radius: 4px;
    background: rgba(255, 255, 255, 0.07);
    overflow: hidden;
    margin-bottom: 10px;
  }
  .big .bar { height: 8px; border-radius: 5px; margin-bottom: 14px; }
  .bar i {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: linear-gradient(90deg, var(--signal), hsl(155, 100%, 42%));
    box-shadow: 0 0 12px var(--signal-glow);
    transition: width 0.6s cubic-bezier(0.4, 0, 0.2, 1);
  }
  .bar i.hot  { background: linear-gradient(90deg, var(--warn), #f59e0b); box-shadow: 0 0 12px var(--warn); }
  .bar i.crit { background: linear-gradient(90deg, var(--crit), #ef4444); box-shadow: 0 0 12px var(--crit); }

  .cards {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 7px;
  }
  .big .cards { gap: 9px; }
  .c {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 3px;
    padding: 7px 5px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: var(--bev-md);
    min-height: 52px;
  }
  .big .c { gap: 5px; padding: 11px 6px; min-height: 78px; }

  .c-label {
    font-family: var(--font-mono);
    font-size: 7.5px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .big .c-label { font-size: 8.5px; letter-spacing: 0.12em; }
  .c-num {
    font-family: var(--font-mono);
    font-size: 15px;
    font-weight: 600;
    color: var(--ink);
    line-height: 1;
  }
  .big .c-num { font-size: 19px; }
  .c-num small {
    font-size: 9px;
    font-weight: 500;
    color: var(--ink-mute);
    margin-left: 2px;
  }
  .big .c-num small { font-size: 11px; }
  .c-sub {
    font-family: var(--font-mono);
    font-size: 8.5px;
    color: var(--ink-faint);
  }
  .big .c-sub { font-size: 10px; }
  .c-sub.accent { color: var(--warn); }

  .c-shield { display: flex; align-items: center; justify-content: center; }
  .c-shield svg { width: 16px; height: 16px; }
  .big .c-shield svg { width: 22px; height: 22px; }
  .c-shield.ok   { color: var(--signal); }
  .c-shield.warn { color: var(--warn); }
  .c-shield.crit { color: var(--crit); }

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
