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
    --badge-c: var(--ink-mute);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-sans);
    font-size: 10.5px;
    font-weight: 600;
    padding: 1px 8px;
    border: 1px solid color-mix(in srgb, var(--badge-c) 40%, transparent);
    background: color-mix(in srgb, var(--badge-c) 9%, transparent);
    border-radius: 999px;
    color: var(--badge-c);
    line-height: 1.5;
    min-width: 18px;
    text-align: center;
    font-feature-settings: "tnum";
  }
  .badge.sm {
    font-size: 10px;
    padding: 0 6px;
    min-width: 15px;
  }
  .badge.accent  { --badge-c: var(--accent);  }
  .badge.warn    { --badge-c: var(--warn);    }
  .badge.crit    { --badge-c: var(--crit);    }
  .badge.info    { --badge-c: var(--info);    }
  .badge.magenta { --badge-c: var(--magenta); }
  .badge.orange  { --badge-c: var(--orange);  }
</style>
