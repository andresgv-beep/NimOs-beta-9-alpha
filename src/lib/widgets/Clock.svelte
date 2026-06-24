<script>
  /**
   * Clock · Widget de reloj · NimOS Beta 8.1
   * ─────────────────────────────────────────
   * IMPLEMENTACIÓN DE REFERENCIA del contrato de widget
   * (documents/WIDGETS-SYSTEM.md). Todo widget nuevo copia esta forma.
   *
   *   - Props: solo `w`/`h` (talla en celdas). Nada del grid.
   *   - Sin chrome: el frame lo pone WidgetLayer.
   *   - Sin datos backend (topic null en catálogo) → no usa widgetData.
   *   - Limpieza: el intervalo muere con el componente.
   *
   * Tallas: 1×1 (hora + fecha apiladas) · 2×1 (hora grande + bloque
   * de fecha al lado). Sin segundos por diseño: los dos puntos
   * parpadeando ya dicen "vivo"; el tick de 1s solo existe para que
   * el cambio de minuto sea inmediato.
   */
  import { onMount } from 'svelte';

  export let w = 1;
  export const h = 1; // no usado aún (única altura: 1) · contrato

  let now = new Date();

  onMount(() => {
    const t = setInterval(() => { now = new Date(); }, 1000);
    return () => clearInterval(t);
  });

  $: hh = String(now.getHours()).padStart(2, '0');
  $: mm = String(now.getMinutes()).padStart(2, '0');
  $: dateStr = now
    .toLocaleDateString(undefined, { day: '2-digit', month: 'short', year: 'numeric' })
    .replace(/\./g, '')
    .toUpperCase();
  $: dowStr = now.toLocaleDateString(undefined, { weekday: 'long' });
</script>

{#if w >= 2}
  <!-- 2×1 · hora protagonista + bloque fecha -->
  <div class="clock wide">
    <div class="time xl">{hh}<span class="col">:</span>{mm}</div>
    <div class="datecol">
      <div class="dow">{dowStr}</div>
      <div class="date">{dateStr}</div>
    </div>
  </div>
{:else}
  <!-- 1×1 · apilado -->
  <div class="clock">
    <div class="time">{hh}<span class="col">:</span>{mm}</div>
    <div class="date">{dateStr}</div>
    <div class="dow">{dowStr}</div>
  </div>
{/if}

<style>
  .clock {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 12px;
    user-select: none;
  }
  .clock.wide {
    flex-direction: row;
    gap: 18px;
  }

  .time {
    font-family: var(--font-mono);
    font-size: 30px;
    font-weight: 600;
    letter-spacing: -0.01em;
    line-height: 1;
    color: var(--ink);
  }
  .time.xl { font-size: 44px; }

  .col {
    color: var(--signal);
    animation: clock-blink 1.1s steps(1) infinite;
  }
  @keyframes clock-blink {
    50% { opacity: 0.25; }
  }

  .date {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.05em;
    margin-top: 9px;
  }
  .dow {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--signal);
    text-transform: uppercase;
    letter-spacing: 0.18em;
    margin-top: 3px;
  }

  /* en 2×1 el bloque de fecha va en columna, sin márgenes apilados */
  .wide .datecol {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
    text-align: left;
  }
  .wide .date, .wide .dow { margin-top: 0; }
  .wide .dow { font-size: 10px; }
  .wide .date { font-size: 11px; }
</style>
