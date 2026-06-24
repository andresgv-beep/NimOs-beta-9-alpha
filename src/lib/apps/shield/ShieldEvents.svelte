<script>
  // Vista Eventos: stream del motor con filtros (severidad, categoría, IP).
  // Los filtros son estado local de la vista.
  import { fmtTime, sevClass, catShort } from './shieldFormat.js';

  export let events = [];

  let sevFilter = 'all';
  let catFilter = 'all';
  let ipSearch = '';

  $: filteredEvents = events.filter(ev =>
    (sevFilter === 'all' || ev.severity === sevFilter) &&
    (catFilter === 'all' || ev.category === catFilter) &&
    (!ipSearch.trim() || (ev.sourceIP || '').includes(ipSearch.trim()))
  );
</script>

<div class="filters">
  <div class="filter-group">
    <span class="filter-lbl">sev</span>
    <button class="pill" class:active={sevFilter === 'all'} on:click={() => sevFilter = 'all'}>Todos</button>
    <button class="pill crit" class:active={sevFilter === 'critical'} on:click={() => sevFilter = 'critical'}>Crítico</button>
    <button class="pill warn" class:active={sevFilter === 'high'} on:click={() => sevFilter = 'high'}>Alto</button>
    <button class="pill" class:active={sevFilter === 'medium'} on:click={() => sevFilter = 'medium'}>Medio</button>
  </div>
  <div class="filter-group">
    <span class="filter-lbl">cat</span>
    <button class="pill" class:active={catFilter === 'all'} on:click={() => catFilter = 'all'}>Todas</button>
    <button class="pill" class:active={catFilter === 'auth'} on:click={() => catFilter = 'auth'}>Auth</button>
    <button class="pill" class:active={catFilter === 'honeypot'} on:click={() => catFilter = 'honeypot'}>Honeypot</button>
    <button class="pill" class:active={catFilter === 'injection'} on:click={() => catFilter = 'injection'}>Injection</button>
    <button class="pill" class:active={catFilter === 'traversal'} on:click={() => catFilter = 'traversal'}>Traversal</button>
    <button class="pill" class:active={catFilter === 'scan'} on:click={() => catFilter = 'scan'}>Scan</button>
  </div>
  <div class="search-box">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
      <circle cx="11" cy="11" r="8"/>
      <line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
    <input type="text" placeholder="filtrar por IP…" bind:value={ipSearch} />
  </div>
</div>

<div class="evt-table">
  <div class="evt-head">
    <span></span>
    <span>Timestamp</span>
    <span>Categoría</span>
    <span>IP origen</span>
    <span>Endpoint</span>
    <span>Method</span>
    <span>Regla</span>
  </div>
  {#if filteredEvents.length === 0}
    <div class="ns-msg">{events.length === 0 ? 'Sin eventos registrados.' : 'Ningún evento coincide con el filtro.'}</div>
  {:else}
    {#each filteredEvents as ev (ev.id)}
      <div class="evt-row">
        <span class="event-led {sevClass[ev.severity] || 'med'}"></span>
        <span class="event-time">{fmtTime(ev.timestamp)}</span>
        <span class="event-cat {ev.category}">{catShort[ev.category] || ev.category}</span>
        <span class="event-ip">{ev.sourceIP || '—'}</span>
        <span class="event-endpoint">{ev.endpoint || '—'}</span>
        <span class="evt-method">{ev.method || '—'}</span>
        <span class="event-rule">{ev.rule || '—'}</span>
      </div>
    {/each}
  {/if}
</div>

<style>
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }

  .filters { display: flex; align-items: center; gap: 8px; margin-bottom: 14px; flex-wrap: wrap; }
  .filter-group { display: flex; gap: 4px; align-items: center; }
  .filter-lbl { font-family: var(--font-mono); font-size: 9px; color: var(--fg-5, #5a5a62); text-transform: uppercase; letter-spacing: 0.8px; margin-right: 4px; }
  .pill { padding: 4px 9px; background: transparent; border: 1px solid var(--bd-2, #20202a); border-radius: 4px; color: var(--fg-3, #9c9ca4); font-size: 10px; font-family: var(--font-mono); text-transform: uppercase; letter-spacing: 0.4px; cursor: pointer; display: flex; align-items: center; gap: 4px; }
  .pill:hover { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .pill.active { color: var(--nim-green, #00ff9f); border-color: rgba(0,255,159,0.35); background: rgba(0,255,159,0.06); }
  .pill.crit.active { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.35); background: rgba(255,90,90,0.06); }
  .pill.warn.active { color: var(--st-warn, #ffc857); border-color: rgba(255,200,87,0.35); background: rgba(255,200,87,0.06); }

  .search-box { display: flex; align-items: center; gap: 6px; padding: 4px 10px; border: 1px solid var(--bd-2, #20202a); border-radius: 5px; background: var(--bg-inner, #101015); margin-left: auto; width: 200px; }
  .search-box svg { width: 11px; height: 11px; color: var(--fg-4, #7a7a82); flex-shrink: 0; }
  .search-box input { flex: 1; min-width: 0; background: transparent; border: none; color: var(--fg, #f0f0f0); outline: none; font-family: var(--font-mono); font-size: 11px; }
  .search-box input::placeholder { color: var(--fg-5, #5a5a62); }

  .evt-table { background: var(--bg-card, #15151a); border-radius: 8px; overflow: hidden; }
  .evt-head { display: grid; grid-template-columns: 14px 95px 90px 130px 1fr 60px 70px; gap: 12px; padding: 9px 14px; background: var(--bg-inner, #101015); border-bottom: 1px solid var(--bd-2, #20202a); font-family: var(--font-mono); font-size: 9px; color: var(--fg-5, #5a5a62); letter-spacing: 0.8px; text-transform: uppercase; font-weight: 600; }
  .evt-row { display: grid; grid-template-columns: 14px 95px 90px 130px 1fr 60px 70px; gap: 12px; padding: 8px 14px; align-items: center; font-size: 11px; transition: background 0.1s; }
  .evt-row + .evt-row { border-top: 1px solid #1a1a20; }
  .evt-row:hover { background: rgba(255,255,255,0.015); }
  .evt-method { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); }

  .event-led { width: 8px; height: 8px; border-radius: 1.5px; }
  .event-led.crit { background: var(--st-crit, #ff5a5a); }
  .event-led.high { background: var(--st-warn, #ffc857); }
  .event-led.med { background: var(--st-info, #4db8ff); }
  .event-time { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); font-variant-numeric: tabular-nums; }
  .event-cat { font-family: var(--font-mono); font-size: 9px; letter-spacing: 0.5px; text-transform: uppercase; font-weight: 600; padding: 2px 7px; border-radius: 3px; text-align: center; }
  .event-cat.auth { background: rgba(255,200,87,0.10); color: var(--st-warn, #ffc857); }
  .event-cat.traversal { background: rgba(255,90,90,0.10); color: var(--st-crit, #ff5a5a); }
  .event-cat.injection { background: rgba(255,90,90,0.10); color: var(--st-crit, #ff5a5a); }
  .event-cat.scan { background: rgba(77,184,255,0.10); color: var(--st-info, #4db8ff); }
  .event-cat.honeypot { background: rgba(255,156,90,0.10); color: var(--nim-folder, #ff9c5a); }
  .event-cat.system { background: rgba(122,158,177,0.10); color: var(--ui-select, #7a9eb1); }
  .event-endpoint { font-family: var(--font-mono); font-size: 11px; color: var(--fg-2, #d0d0d4); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .event-ip { font-family: var(--font-mono); font-size: 11px; color: var(--fg-3, #9c9ca4); font-variant-numeric: tabular-nums; }
  .event-rule { font-family: var(--font-mono); font-size: 9px; color: var(--fg-4, #7a7a82); padding: 1px 6px; border: 1px solid var(--bd-2, #20202a); border-radius: 3px; letter-spacing: 0.4px; text-align: center; }
</style>
