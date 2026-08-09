<script>
  /**
   * DenseTable · Tabla densa con líneas numeradas
   * ───────────────────────────────────────────────
   * Tabla genérica estilo "log de editor" o "journalctl":
   *   - Columna opcional de número de línea (01, 02, 03...)
   *   - Header sticky
   *   - Hover row con tinte
   *   - Selección múltiple opcional
   *   - Sortable columns
   *
   * Dado lo variable que es el contenido, esta primitiva solo provee la
   * estructura (header + body + grid). El consumidor pasa las filas como
   * slot, usando las clases `.tr-row` y sub-elementos según necesidad.
   *
   * Uso mínimo:
   *   <DenseTable
   *     columns="40px 1fr 120px 100px"
   *     headers={[
   *       { label: '#' },
   *       { label: 'Nombre', sortable: true, active: true, direction: 'desc' },
   *       { label: 'Tamaño', align: 'right' },
   *       { label: 'Modificado' },
   *     ]}
   *   >
   *     <div class="tr-row">...filas...</div>
   *   </DenseTable>
   */
  export let columns = '1fr';
  export let headers = [];
  export let gap = '10px';
  export let padding = '6px 12px';
  /** bordes entre filas (por defecto sí) */
  export let bordered = true;

  function handleSort(h) {
    if (!h.sortable || !h.onSort) return;
    h.onSort(h);
  }
</script>

<div class="dt" class:bordered style="--dt-cols:{columns}; --dt-gap:{gap}; --dt-pad:{padding}">
  {#if headers.length > 0}
    <div class="dt-head">
      {#each headers as h}
        <div
          class="dt-th"
          class:sortable={h.sortable}
          class:active={h.active}
          style={h.align === 'right' ? 'text-align:right' : ''}
          on:click={() => handleSort(h)}
          on:keydown={(e) => e.key === 'Enter' && handleSort(h)}
          role={h.sortable ? 'button' : null}
          tabindex={h.sortable ? 0 : null}
        >
          {h.label || ''}
          {#if h.sortable && h.active}
            <span class="sort-ic">{h.direction === 'asc' ? '▲' : '▼'}</span>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <div class="dt-body">
    <slot />
  </div>
</div>

<style>
  .dt {
    font-family: var(--font-sans);
    font-size: 12px;
    background: var(--panel);
    display: flex;
    flex-direction: column;
  }
  .dt.bordered {
    border: 1px solid var(--line);
    border-radius: 6px;
    overflow: hidden;
  }

  .dt-head {
    display: grid;
    grid-template-columns: var(--dt-cols);
    gap: var(--dt-gap);
    padding: 7px 12px;
    color: var(--ink-mute);
    letter-spacing: 0;
    font-size: 11px;
    font-weight: 600;
    border-bottom: 1px solid var(--line);
    background: var(--canvas-soft);
    position: sticky;
    top: 0;
    z-index: 10;
  }
  .dt-th {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .dt-th.sortable {
    cursor: pointer;
    transition: color 0.1s;
  }
  .dt-th.sortable:hover { color: var(--ink); }
  .dt-th.active { color: var(--signal); }
  .sort-ic { font-size: 8px; opacity: 0.7; }

  .dt-body {
    display: flex;
    flex-direction: column;
  }

  /* Estilos globales para las filas .tr-row que pasa el consumidor */
  :global(.dt-body .tr-row) {
    display: grid;
    grid-template-columns: var(--dt-cols);
    gap: var(--dt-gap);
    padding: var(--dt-pad);
    align-items: center;
    color: var(--ink-dim);
    cursor: pointer;
    transition: background 0.06s, color 0.06s;
    border-bottom: 1px solid var(--line);
    user-select: none;
  }
  :global(.dt.bordered .dt-body .tr-row:last-child) {
    border-bottom: none;
  }
  :global(.dt-body .tr-row:hover) {
    background: rgba(255, 255, 255, 0.035);
    color: var(--ink);
  }
  :global(.dt-body .tr-row.selected) {
    background: var(--signal-dim);
    color: var(--ink);
  }
  :global(.dt-body .tr-row.warn-row)  { background: rgba(255, 184, 0, 0.03); }
  :global(.dt-body .tr-row.crit-row)  { background: rgba(255, 90, 90, 0.05); }
  :global(.dt-body .tr-row.muted)     { opacity: 0.55; }
  :global(.dt-body .tr-row.muted:hover) { opacity: 1; }

  :global(.dt-body .tr-ln) {
    color: var(--ink-faint);
    text-align: right;
    font-size: 9px;
    font-feature-settings: "tnum";
  }
</style>
