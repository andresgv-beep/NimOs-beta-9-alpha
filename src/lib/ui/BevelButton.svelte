<script>
  /**
   * BevelButton · Botón sistema D con bevel clip-path
   * ────────────────────────────────────────────────────
   * Usa el patrón wrap + btn con polygon compartido.
   * El wrap tiene el color del borde; el btn el fondo.
   *
   * Variantes:
   *   - default  → borde gris, texto dim, hover accent
   *   - primary  → fondo accent sólido, glow en hover
   *   - danger   → borde rojo, fill rojo en hover
   *   - info     → borde azul info
   *   - warn     → borde ámbar
   *
   * Tamaños:
   *   - sm (bevel 6px, padding pequeño)
   *   - md (default, bevel 10px)
   *   - lg (bevel 12px, padding grande)
   *
   * Props extra:
   *   - disabled
   *   - keyHint: string — muestra un <KeyBind> al final
   *   - iconPrefix: string — icono corto antes del texto (p.ej. "▸", "↑")
   */
  import KeyBind from './KeyBind.svelte';

  export let variant = 'default';
  export let size = 'md';
  export let disabled = false;
  export let keyHint = '';
  export let iconPrefix = '';
  export let type = 'button';
  export let title = '';

  /** Click handler forwarded to the inner <button>. */
  export let onClick = null;

  function handleClick(e) {
    if (disabled) return;
    if (onClick) onClick(e);
  }
</script>

<span
  class="btn-wrap"
  class:primary={variant === 'primary'}
  class:danger={variant === 'danger'}
  class:info={variant === 'info'}
  class:warn={variant === 'warn'}
  class:disabled
  class:sm={size === 'sm'}
  class:lg={size === 'lg'}
>
  <button
    class="btn"
    class:sm={size === 'sm'}
    class:lg={size === 'lg'}
    {type}
    {disabled}
    {title}
    on:click={handleClick}
  >
    {#if iconPrefix}<span class="pref">{iconPrefix}</span>{/if}
    <slot />
    {#if keyHint}<KeyBind key={keyHint} />{/if}
  </button>
</span>

<style>
  .btn-wrap {
    display: inline-flex;
    cursor: pointer;
  }

  .btn {
    font-family: var(--font-sans);
    font-size: 12.5px;
    font-weight: 600;
    letter-spacing: 0;
    padding: 8px 14px;
    background: var(--panel-elev);
    color: var(--ink-dim);
    cursor: pointer;
    border: 1px solid var(--line-bright);
    border-radius: 4px;
    transition: color 0.12s, background 0.12s, border-color 0.12s;
    display: inline-flex;
    align-items: center;
    gap: 8px;
    line-height: 1.2;
  }
  .btn.sm {
    font-size: 11.5px;
    padding: 6px 10px;
  }
  .btn.lg {
    font-size: 14px;
    padding: 10px 18px;
  }
  .btn-wrap:hover .btn { color: var(--ink); background: #2a3440; border-color: var(--line-strong); }
  .btn-wrap:active .btn { background: #202832; }

  .pref {
    font-size: 10px;
    opacity: 0.9;
  }

  /* ─── PRIMARY ─── */
  .btn-wrap.primary .btn { background: var(--signal); border-color: var(--signal); color: #fff; }
  .btn-wrap.primary:hover .btn { background: var(--signal-hover); border-color: var(--signal-hover); color: #fff; }

  /* ─── DANGER ─── */
  .btn-wrap.danger .btn { border-color: var(--crit-border); color: var(--crit); }
  .btn-wrap.danger:hover .btn { background: var(--crit-dim); border-color: var(--crit); color: #fff; }

  /* ─── INFO ─── */
  .btn-wrap.info .btn { border-color: var(--info-border); color: var(--info); }
  .btn-wrap.info:hover .btn { background: var(--info-dim); border-color: var(--info); color: var(--ink); }

  /* ─── WARN ─── */
  .btn-wrap.warn .btn { border-color: var(--warn-border); color: var(--warn); }
  .btn-wrap.warn:hover .btn { background: var(--warn-dim); border-color: var(--warn); color: var(--ink); }

  /* ─── DISABLED ─── */
  .btn-wrap.disabled { cursor: not-allowed; }
  .btn-wrap.disabled .btn {
    background: var(--canvas-soft);
    color: var(--ink-faint);
    border-color: var(--line);
    cursor: not-allowed;
    opacity: 0.6;
  }
  .btn-wrap.disabled:hover .btn { color: var(--ink-faint); background: var(--canvas-soft); }
</style>
