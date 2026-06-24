<script>
  /**
   * FilterBar · Barra de filtros v3 (tabs como pills + buscador)
   * ─────────────────────────────────────────────────────────────
   * Pestañas-pill con contador integrado y un buscador opcional a la
   * derecha. Mismo lenguaje visual que los filtros de NimShield.
   *
   * Props:
   *   tabs       — array de { id, label, count?, variant? }
   *                · count: número en la cápsula (omitir o null = sin contador)
   *                · variant: 'default' | 'crit' (pestaña roja, p.ej. Errores)
   *   active     — id de la pestaña activa (bind)
   *   search     — valor del buscador (bind). Si no se usa search, no se enlaza.
   *   searchable — muestra el buscador (default true)
   *   placeholder— texto del buscador
   *   keyHint    — atajo mostrado en el buscador (default '/')
   *
   * Eventos: usa bind:active y bind:search en el consumidor.
   *
   * Uso:
   *   <FilterBar
   *     tabs={[{id:'all',label:'Todos',count:9},
   *            {id:'err',label:'Errores',count:2,variant:'crit'}]}
   *     bind:active={filter}
   *     bind:search
   *     placeholder="Buscar servicio..."
   *   />
   */
  export let tabs = [];
  export let active = '';
  export let search = '';
  export let searchable = true;
  export let placeholder = 'Buscar...';
  export let keyHint = '/';

  let searchEl;

  // Atajo de teclado: la tecla keyHint enfoca el buscador.
  function onKeydown(e) {
    if (!searchable || !keyHint) return;
    const tag = (e.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea') return;
    if (e.key === keyHint) {
      e.preventDefault();
      searchEl?.focus();
    }
  }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="filter-bar">
  <div class="fb-tabs">
    {#each tabs as tab (tab.id)}
      <button
        class="fb-tab"
        class:active={active === tab.id}
        class:crit={tab.variant === 'crit'}
        on:click={() => (active = tab.id)}
      >
        {tab.label}
        {#if tab.count != null}
          <span class="fb-count">{tab.count}</span>
        {/if}
      </button>
    {/each}
  </div>

  {#if searchable}
    <div class="fb-search">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <circle cx="11" cy="11" r="8" />
        <line x1="21" y1="21" x2="16.65" y2="16.65" />
      </svg>
      <input
        bind:this={searchEl}
        bind:value={search}
        type="text"
        {placeholder}
      />
      {#if keyHint}<span class="fb-kb">{keyHint}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .filter-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 14px 18px;
    flex-wrap: wrap;
  }
  .fb-tabs {
    display: flex;
    gap: 4px;
    align-items: center;
  }
  .fb-tab {
    padding: 5px 11px;
    background: transparent;
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    color: var(--ink-mute);
    font-size: 10px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 6px;
    transition: color 0.12s, border-color 0.12s, background 0.12s;
  }
  .fb-tab:hover {
    color: var(--ink);
    border-color: var(--line-bright);
  }
  .fb-tab.active {
    color: var(--signal);
    border-color: rgba(0, 255, 159, 0.35);
    background: rgba(0, 255, 159, 0.06);
  }
  .fb-tab.active.crit {
    color: var(--crit);
    border-color: rgba(255, 90, 90, 0.35);
    background: rgba(255, 90, 90, 0.06);
  }
  .fb-count {
    font-size: 9px;
    font-variant-numeric: tabular-nums;
    padding: 0 5px;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.06);
    color: var(--ink-faint);
    min-width: 16px;
    text-align: center;
  }
  .fb-tab.active .fb-count {
    background: rgba(0, 255, 159, 0.15);
    color: var(--signal);
  }
  .fb-tab.active.crit .fb-count {
    background: rgba(255, 90, 90, 0.15);
    color: var(--crit);
  }

  .fb-search {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 10px;
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    background: var(--bg-inner, #101015);
    margin-left: auto;
    width: 220px;
  }
  .fb-search:focus-within {
    border-color: rgba(0, 255, 159, 0.35);
  }
  .fb-search svg {
    width: 11px;
    height: 11px;
    color: var(--ink-faint);
    flex-shrink: 0;
  }
  .fb-search input {
    flex: 1;
    min-width: 0;
    background: transparent;
    border: none;
    color: var(--ink);
    outline: none;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .fb-search input::placeholder {
    color: var(--ink-trace);
  }
  .fb-kb {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--ink-trace);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 3px;
    padding: 1px 5px;
    flex-shrink: 0;
  }
</style>
