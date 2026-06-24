<script>
  /**
   * IconButton · Botón cuadrado 28x28px con bevel sm
   * ──────────────────────────────────────────────────
   * Para toolbars donde caben muchas acciones rápidas.
   * Admite símbolo Unicode o elemento en el slot.
   *
   * Variantes: default, danger
   * Tamaños:   sm (24x20), md (28x28, default)
   */
  export let variant = 'default';
  export let size = 'md';
  export let disabled = false;
  export let title = '';
  export let onClick = null;

  function handleClick(e) {
    if (disabled) return;
    if (onClick) onClick(e);
  }
</script>

<span
  class="ibtn-wrap"
  class:danger={variant === 'danger'}
  class:disabled
  class:sm={size === 'sm'}
>
  <button
    class="ibtn"
    class:sm={size === 'sm'}
    {disabled}
    {title}
    on:click={handleClick}
  >
    <slot />
  </button>
</span>

<style>
  .ibtn-wrap {
    display: inline-block;
    background: var(--border-bright);
    padding: 1px;
    cursor: pointer;
    transition: background 0.12s;
    line-height: 0;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
  }
  .ibtn-wrap:hover { background: var(--accent); }
  .ibtn-wrap.danger:hover { background: var(--crit); }
  .ibtn-wrap.disabled { background: var(--border); cursor: not-allowed; opacity: 0.5; }
  .ibtn-wrap.disabled:hover { background: var(--border); }

  .ibtn {
    width: 28px;
    height: 28px;
    background: var(--bg);
    color: var(--fg-dim);
    border: none;
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 700;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
    transition: color 0.12s;
  }
  .ibtn.sm {
    width: 22px;
    height: 20px;
    font-size: 10px;
  }
  .ibtn-wrap:hover .ibtn { color: var(--accent); }
  .ibtn-wrap.danger:hover .ibtn { color: var(--crit); }
  .ibtn-wrap.disabled .ibtn { color: var(--fg-faint); cursor: not-allowed; }
</style>
