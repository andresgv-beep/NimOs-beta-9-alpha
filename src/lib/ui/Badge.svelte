<script>
  /**
   * Badge · Pill pequeño con borde para contadores/estados
   * ────────────────────────────────────────────────────────
   * Uso:
   *   <Badge>12</Badge>
   *   <Badge variant="crit">!</Badge>
   *   <Badge variant="accent">NEW</Badge>
   *
   * Variantes: default, accent, warn, crit, info, magenta, orange
   * Tamaños:   sm, md (default)
   *
   * Estilo: borde 1px, texto con color, bevel sutil en esquinas.
   */
  export let variant = 'default';
  export let size = 'md';
</script>

<span
  class="badge"
  class:accent={variant === 'accent'}
  class:warn={variant === 'warn'}
  class:crit={variant === 'crit'}
  class:info={variant === 'info'}
  class:magenta={variant === 'magenta'}
  class:orange={variant === 'orange'}
  class:sm={size === 'sm'}
>
  <slot />
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 9px;
    font-weight: 600;
    padding: 1px 6px;
    border: 1px solid var(--fg-mute);
    color: var(--fg-mute);
    letter-spacing: 0.5px;
    line-height: 1.4;
    min-width: 16px;
    text-align: center;
    font-feature-settings: "tnum";
  }
  .badge.sm {
    font-size: 8px;
    padding: 0 4px;
    min-width: 14px;
  }
  .badge.accent  { color: var(--accent);  border-color: var(--accent);  }
  .badge.warn    { color: var(--warn);    border-color: var(--warn);    }
  .badge.crit    { color: var(--crit);    border-color: var(--crit);    }
  .badge.info    { color: var(--info);    border-color: var(--info);    }
  .badge.magenta { color: var(--magenta); border-color: var(--magenta); }
  .badge.orange  { color: var(--orange);  border-color: var(--orange);  }
</style>
