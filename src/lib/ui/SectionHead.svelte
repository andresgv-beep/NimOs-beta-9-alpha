<script>
  /**
   * SectionHead · Heading asimétrico estilo /etc/motd
   * ────────────────────────────────────────────────────
   * Uso:
   *   <SectionHead>Eventos live</SectionHead>
   *   <SectionHead count="· 142 en últimos 60s">Eventos live</SectionHead>
   *
   * Renderiza:
   *   ── EVENTOS LIVE ──────── · 142 en últimos 60s
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
    align-items: center;
    gap: 10px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: 2px;
    padding: 0 0 6px;
  }
  .section-head::before {
    content: '──';
    color: var(--fg-faint);
  }
  .title {
    white-space: nowrap;
  }
  .section-head::after {
    content: '';
    flex: 1;
    height: 1px;
    background: linear-gradient(
      to right,
      var(--border) 0%,
      var(--border) 70%,
      transparent 100%
    );
  }
  .count {
    color: var(--accent);
    font-weight: 600;
    margin-left: -4px;
    order: 2;
  }
  .section-head::after { order: 3; }
</style>
