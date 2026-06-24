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
    display: inline-block;
    background: var(--border-bright);
    padding: 1px;
    transition: background 0.12s, box-shadow 0.12s;
    cursor: pointer;
    line-height: 0;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-md)) 0,
      100% var(--bev-md),
      100% 100%,
      var(--bev-md) 100%,
      0 calc(100% - var(--bev-md))
    );
  }
  .btn-wrap.sm {
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
  }
  .btn-wrap.lg {
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-lg)) 0,
      100% var(--bev-lg),
      100% 100%,
      var(--bev-lg) 100%,
      0 calc(100% - var(--bev-lg))
    );
  }

  .btn-wrap:hover { background: var(--accent); }
  .btn-wrap:active { filter: brightness(0.85); }

  .btn {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 1px;
    text-transform: uppercase;
    padding: 7px 14px;
    background: var(--bg);
    color: var(--fg-dim);
    cursor: pointer;
    border: none;
    transition: color 0.12s, background 0.12s;
    display: inline-flex;
    align-items: center;
    gap: 8px;
    line-height: 1.2;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-md)) 0,
      100% var(--bev-md),
      100% 100%,
      var(--bev-md) 100%,
      0 calc(100% - var(--bev-md))
    );
  }
  .btn.sm {
    font-size: 9px;
    padding: 5px 11px;
    letter-spacing: 0.8px;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-sm)) 0,
      100% var(--bev-sm),
      100% 100%,
      var(--bev-sm) 100%,
      0 calc(100% - var(--bev-sm))
    );
  }
  .btn.lg {
    font-size: 11px;
    padding: 10px 20px;
    letter-spacing: 1.5px;
    clip-path: polygon(
      0 0,
      calc(100% - var(--bev-lg)) 0,
      100% var(--bev-lg),
      100% 100%,
      var(--bev-lg) 100%,
      0 calc(100% - var(--bev-lg))
    );
  }

  .btn-wrap:hover .btn { color: var(--accent); background: var(--bg); }

  .pref {
    font-size: 10px;
    opacity: 0.9;
  }

  /* ─── PRIMARY ─── */
  .btn-wrap.primary { background: var(--accent); }
  .btn-wrap.primary:hover { background: var(--ink); box-shadow: 0 0 14px var(--accent-glow); }
  .btn-wrap.primary .btn { background: var(--accent); color: var(--bg); font-weight: 700; }
  .btn-wrap.primary:hover .btn { background: var(--ink); color: var(--bg); }

  /* ─── DANGER ─── */
  .btn-wrap.danger { background: var(--crit); }
  .btn-wrap.danger .btn { background: var(--bg); color: var(--crit); }
  .btn-wrap.danger:hover .btn { background: var(--crit); color: var(--bg); }

  /* ─── INFO ─── */
  .btn-wrap.info { background: var(--info); }
  .btn-wrap.info .btn { background: var(--bg); color: var(--info); }
  .btn-wrap.info:hover .btn { background: var(--info); color: var(--bg); }

  /* ─── WARN ─── */
  .btn-wrap.warn { background: var(--warn); }
  .btn-wrap.warn .btn { background: var(--bg); color: var(--warn); }
  .btn-wrap.warn:hover .btn { background: var(--warn); color: var(--bg); }

  /* ─── DISABLED ─── */
  .btn-wrap.disabled { background: var(--border); cursor: not-allowed; }
  .btn-wrap.disabled:hover { background: var(--border); box-shadow: none; }
  .btn-wrap.disabled .btn {
    background: var(--bg-1);
    color: var(--fg-faint);
    cursor: not-allowed;
    opacity: 0.6;
  }
  .btn-wrap.disabled:hover .btn { color: var(--fg-faint); background: var(--bg-1); }
</style>
