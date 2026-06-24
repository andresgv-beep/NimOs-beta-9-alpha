<script>
  /**
   * KeyBind · Badge de atajo de teclado
   * ─────────────────────────────────────
   * Uso: <KeyBind key="E" /> · <KeyBind key="^C" /> · <KeyBind key="/" />
   *
   * Renderiza un badge pequeño con borde que resalta en hover del padre.
   * Diseñado para ponerse junto a botones, items de sidebar, tabs, etc.
   */
  export let key = '';
  /** active: el keybind aparece resaltado (cuando el item padre está activo) */
  export let active = false;
</script>

<span class="kb" class:active>{key}</span>

<style>
  .kb {
    font-family: var(--font-mono);
    font-size: 8px;
    font-weight: 500;
    color: var(--fg-faint);
    border: 1px solid var(--border);
    padding: 0 4px;
    letter-spacing: 0.5px;
    line-height: 1.4;
    display: inline-block;
    transition: color 0.12s, border-color 0.12s;
  }
  .kb.active {
    color: var(--accent);
    border-color: var(--accent);
  }

  /* Cuando el padre tiene hover, el keybind se ilumina en accent */
  :global(*:hover > .kb:not(.active)),
  :global(.btn-wrap:hover .kb),
  :global(.sb-item:hover .kb),
  :global(.tab:hover .kb) {
    color: var(--fg-dim);
    border-color: var(--fg-dim);
  }
</style>
