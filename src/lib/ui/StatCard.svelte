<script>
  /**
   * StatCard · Tarjeta de estadística (lenguaje visual v3)
   * ──────────────────────────────────────────────────────
   * Card con borde de color a la izquierda (edge), cabecera con etiqueta
   * uppercase + tag opcional (con dot), y valor grande en mono. Extraída
   * del diseño de NimShield para unificar todas las apps.
   *
   * Props:
   *   label    — etiqueta superior (uppercase)
   *   value    — valor principal (texto o número)
   *   unit     — unidad pequeña tras el valor (opcional)
   *   variant  — color del borde y del valor: 'default'|'ok'|'info'|'warn'|'crit'
   *   tag      — texto del tag superior derecho (opcional)
   *   tagVariant — color del tag y su dot: 'default'|'ok'|'info'|'warn'|'crit'
   *   tagDot   — si el tag lleva el cuadradito de color delante (default true)
   *   valueColored — si el valor toma el color de `variant` (default true)
   *
   * Slot por defecto: contenido extra bajo el valor (sparkline, etc.).
   */
  export let label = '';
  export let value = '';
  export let unit = '';
  export let variant = 'default';
  export let tag = '';
  export let tagVariant = 'default';
  export let tagDot = true;
  export let valueColored = true;
</script>

<div class="stat-card v-{variant}">
  <div class="sc-head">
    <span class="sc-lbl">{label}</span>
    {#if tag}
      <span class="sc-tag t-{tagVariant}">
        {#if tagDot && tagVariant !== 'default'}<span class="sc-dot"></span>{/if}
        {tag}
      </span>
    {/if}
  </div>
  <div class="sc-val" class:colored={valueColored}>
    {value}{#if unit}<span class="sc-unit">{unit}</span>{/if}
  </div>
  <slot />
</div>

<style>
  .stat-card {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 12px 12px 11px;
    display: flex;
    flex-direction: column;
    position: relative;
    overflow: hidden;
  }
  .stat-card::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    width: 2px;
    height: 100%;
    background: var(--stat-edge, transparent);
    opacity: 0.7;
  }
  .v-ok   { --stat-edge: var(--signal); }
  .v-info { --stat-edge: var(--info); }
  .v-warn { --stat-edge: var(--warn); }
  .v-crit { --stat-edge: var(--crit); }

  .sc-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
    gap: 8px;
  }
  .sc-lbl {
    font-size: 10px;
    color: var(--ink-faint);
    font-weight: 500;
    letter-spacing: 0.6px;
    text-transform: uppercase;
  }
  .sc-tag {
    font-size: 9px;
    color: var(--ink-faint);
    display: flex;
    align-items: center;
    gap: 4px;
    font-family: var(--font-mono);
    white-space: nowrap;
  }
  .sc-dot {
    width: 5px;
    height: 5px;
    border-radius: 1.5px;
    background: currentColor;
  }
  .t-ok   { color: var(--signal); }
  .t-info { color: var(--info); }
  .t-warn { color: var(--warn); }
  .t-crit { color: var(--crit); }

  .sc-val {
    font-size: 22px;
    font-weight: 500;
    color: var(--ink);
    line-height: 1;
    letter-spacing: -0.4px;
    font-family: var(--font-mono);
  }
  .sc-val .sc-unit {
    font-size: 11px;
    color: var(--ink-faint);
    margin-left: 4px;
    font-weight: 400;
  }
  .stat-card.v-ok   .sc-val.colored { color: var(--signal); }
  .stat-card.v-info .sc-val.colored { color: var(--info); }
  .stat-card.v-warn .sc-val.colored { color: var(--warn); }
  .stat-card.v-crit .sc-val.colored { color: var(--crit); }
</style>
