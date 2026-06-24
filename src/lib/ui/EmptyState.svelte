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
    font-family: var(--font-mono);
    text-align: center;
    flex: 1;
  }
  .icon {
    width: 44px; height: 44px;
    border: 1px solid var(--border-bright);
    color: var(--fg-mute);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 18px;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
  }
  .title {
    font-size: 11px;
    color: var(--fg-dim);
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
  .hint {
    font-size: 10px;
    color: var(--fg-mute);
    letter-spacing: 0.5px;
    max-width: 300px;
    line-height: 1.5;
  }
</style>
