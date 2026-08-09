<script>
  /**
   * SectionHead · Título de sección (sans, peso 600 + contador mute)
   * ────────────────────────────────────────────────────
   * Uso:
   *   <SectionHead>Eventos live</SectionHead>
   *   <SectionHead count="· 142 en últimos 60s">Eventos live</SectionHead>
   */
  export let count = '';
</script>

<div class="section-head">
  <span class="title"><slot /></span>
  {#if count}<span class="count">{count}</span>{/if}
</div>

<style>
  .section-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-family: var(--font-sans);
    font-size: var(--fs-13);
    font-weight: 600;
    color: var(--ink);
    padding: 0 0 6px;
  }
  .title {
    white-space: nowrap;
  }
  .count {
    font-family: var(--font-sans);
    font-size: var(--fs-11);
    font-weight: 500;
    color: var(--ink-mute);
  }
</style>
