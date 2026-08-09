<script>
  /**
   * EmptyState · Estado vacío coherente
   * ─────────────────────────────────────
   * Uso:
   *   <EmptyState icon="⇅" title="Sin transferencias" hint="Las descargas y subidas aparecerán aquí" />
   */
  export let icon = '◌';
  export let title = 'Nada aquí';
  export let hint = '';
</script>

<div class="empty">
  <div class="icon">{icon}</div>
  <div class="title">{title}</div>
  {#if hint}<div class="hint">{hint}</div>{/if}
  <slot />
</div>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    padding: 40px 20px;
    font-family: var(--font-sans);
    text-align: center;
    flex: 1;
  }
  .icon {
    width: 44px; height: 44px;
    border: 1px solid var(--line-bright);
    border-radius: 6px;
    background: var(--canvas-soft);
    color: var(--ink-mute);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 18px;
  }
  .title {
    font-size: 13px;
    color: var(--ink-dim);
    letter-spacing: 0;
    font-weight: 600;
  }
  .hint {
    font-size: 12px;
    color: var(--ink-mute);
    letter-spacing: 0;
    max-width: 300px;
    line-height: 1.5;
  }
</style>
