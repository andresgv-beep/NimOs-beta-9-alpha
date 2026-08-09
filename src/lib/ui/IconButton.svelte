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
    display: inline-flex;
    cursor: pointer;
  }
  .ibtn-wrap.disabled { cursor: not-allowed; opacity: 0.5; }

  .ibtn {
    width: 28px;
    height: 28px;
    background: var(--panel-elev);
    color: var(--ink-mute);
    border: 1px solid var(--line-bright);
    border-radius: 4px;
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 700;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: color 0.12s, background 0.12s, border-color 0.12s;
  }
  .ibtn.sm {
    width: 22px;
    height: 20px;
    font-size: 10px;
  }
  .ibtn-wrap:hover .ibtn { color: var(--ink); background: #2a3440; border-color: var(--line-strong); }
  .ibtn-wrap.danger:hover .ibtn { color: #fff; background: var(--crit-dim); border-color: var(--crit); }
  .ibtn-wrap.disabled .ibtn { color: var(--ink-faint); cursor: not-allowed; }
</style>
