<script>
  /**
   * DataTable · Tabla densa (lenguaje visual v3)
   * ─────────────────────────────────────────────
   * Tabla con cabecera mono uppercase sobre bg-inner, filas con separador
   * fino y hover. El layout de columnas se define con `cols` (cualquier
   * valor válido de grid-template-columns). Extraída del diseño de NimShield.
   *
   * Props:
   *   cols      — grid-template-columns (ej: "14px 95px 1fr 70px")
   *   headers   — array de strings (cabeceras). Si una empieza por '>' se
   *               alinea a la derecha (ej: ">Acción").
   *   rowGap    — gap entre columnas (default '12px')
   *   hover     — filas con hover (default true)
   *   clickable — cursor pointer en filas (default false)
   *
   * Slots:
   *   default → una o más <svelte:fragment slot="row"> NO; en su lugar el
   *             consumidor itera y mete divs con class="dt-row". Para mantenerlo
   *             simple, DataTable expone el contenedor + cabecera y el cuerpo
   *             va en el slot por defecto (el consumidor renderiza las filas
   *             con el helper de clase .dt-row y .dt-cell vía :global).
   *
   * Uso típico:
   *   <DataTable cols="14px 1fr 70px" headers={['','Nombre','>Acción']}>
   *     {#each items as it}
   *       <div class="dt-row" style="--cols:14px 1fr 70px">…celdas…</div>
   *     {/each}
   *   </DataTable>
   *
   * Para evitar que el consumidor repita `cols`, DataTable inyecta la var
   * CSS --dt-cols en el contenedor y las filas la heredan.
   */
  export let cols = '1fr';
  export let headers = [];
  export let rowGap = '12px';
  export let hover = true;
  export let clickable = false;

  function isRight(h) { return typeof h === 'string' && h.startsWith('>'); }
  function label(h) { return isRight(h) ? h.slice(1) : h; }
</script>

<div
  class="data-table"
  class:hoverable={hover}
  class:clickable
  style="--dt-cols:{cols}; --dt-gap:{rowGap};"
>
  <div class="dt-head">
    {#each headers as h}
      <span class:right={isRight(h)}>{label(h)}</span>
    {/each}
  </div>
  <div class="dt-rows">
    <slot />
  </div>
</div>

<style>
  .data-table {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    overflow: hidden;
  }
  .dt-head {
    display: grid;
    grid-template-columns: var(--dt-cols);
    gap: var(--dt-gap);
    padding: 9px 14px;
    background: var(--bg-inner, #101015);
    border-bottom: 1px solid var(--bd-2, #20202a);
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-trace);
    letter-spacing: 0.8px;
    text-transform: uppercase;
    font-weight: 600;
  }
  .dt-head .right { text-align: right; }

  /* Filas: el consumidor mete .dt-row dentro del slot. Estilamos vía :global
     porque el markup de filas vive en el componente padre (slotted). */
  .dt-rows :global(.dt-row) {
    display: grid;
    grid-template-columns: var(--dt-cols);
    gap: var(--dt-gap);
    padding: 11px 14px;
    align-items: center;
    font-size: 11px;
  }
  .dt-rows :global(.dt-row + .dt-row) {
    border-top: 1px solid #1a1a20;
  }
  .hoverable .dt-rows :global(.dt-row:hover) {
    background: rgba(255, 255, 255, 0.015);
  }
  .clickable .dt-rows :global(.dt-row) {
    cursor: pointer;
    transition: background 0.1s;
  }
</style>
