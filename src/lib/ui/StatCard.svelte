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
  /* ═══════════════════════════════════════════════════════════════
     PLANTILLA DE MIGRACIÓN A rem (Beta 9 · escalado honesto)
     ───────────────────────────────────────────────────────────────
     Convención al convertir px → escalable:
       · spacing (padding/margin/gap) → token --sp-* si el valor coincide
         con un paso (4/8/12/16/20/24/32), si no rem (valor/16).
       · font-size → token --fs-* si coincide, si no rem.
       · border-radius → token --radius-* o --bev-* si coincide, si no rem.
       · tamaños de elementos (dot, iconos) → rem, para escalar con el texto.
     SE QUEDAN EN px (NO escalan):
       · bordes y hairlines de 1-2px (engordan feo al escalar).
       · letter-spacing (detalle tipográfico sub-pixel).
       · radios muy pequeños (1-2px) donde la esquina debe seguir nítida.
     Todo lo rem cuelga del font-size raíz (= 16px · --ui-scale).
     ═══════════════════════════════════════════════════════════════ */
  .stat-card {
    background: var(--bg-card, #15151a);
    border-radius: var(--radius-md);        /* 8px */
    padding: var(--sp-3) var(--sp-3) 0.6875rem; /* 12 12 11 (11 sin token) */
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
    width: 2px;                 /* barra decorativa · hairline, NO escala */
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
    margin-bottom: var(--sp-2);  /* 8px */
    gap: var(--sp-2);            /* 8px */
  }
  .sc-lbl {
    font-size: var(--fs-10);    /* 10px */
    color: var(--ink-faint);
    font-weight: 500;
    letter-spacing: 0.6px;      /* tipográfico · NO escala */
    text-transform: uppercase;
  }
  .sc-tag {
    font-size: var(--fs-9);     /* 9px */
    color: var(--ink-faint);
    display: flex;
    align-items: center;
    gap: var(--sp-1);           /* 4px */
    font-family: var(--font-mono);
    white-space: nowrap;
  }
  .sc-dot {
    width: 0.3125rem;           /* 5px · escala con el texto */
    height: 0.3125rem;
    border-radius: 1.5px;       /* esquina nítida · NO escala */
    background: currentColor;
  }
  .t-ok   { color: var(--signal); }
  .t-info { color: var(--info); }
  .t-warn { color: var(--warn); }
  .t-crit { color: var(--crit); }

  .sc-val {
    font-size: var(--fs-22);    /* 22px */
    font-weight: 500;
    color: var(--ink);
    line-height: 1;
    letter-spacing: -0.4px;     /* tipográfico · NO escala */
    font-family: var(--font-mono);
  }
  .sc-val .sc-unit {
    font-size: var(--fs-11);    /* 11px */
    color: var(--ink-faint);
    margin-left: var(--sp-1);   /* 4px */
    font-weight: 400;
  }
  .stat-card.v-ok   .sc-val.colored { color: var(--signal); }
  .stat-card.v-info .sc-val.colored { color: var(--info); }
  .stat-card.v-warn .sc-val.colored { color: var(--warn); }
  .stat-card.v-crit .sc-val.colored { color: var(--crit); }
</style>
