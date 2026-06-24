<script>
  /**
   * StripeProgressBar · Barra con rayas diagonales animadas
   * ─────────────────────────────────────────────────────────
   * Barra lisa (no segmentada) con patrón de stripes a 45° moviéndose.
   * Usada en: Files (transfers embebidas), Transferencias, uploads en general.
   *
   * Props:
   *   - percent:  0-100
   *   - variant:  'accent' | 'info' | 'warn' | 'crit'
   *   - animated: boolean (default true) — si false, stripes quietas
   *   - height:   altura en px (default 8)
   *   - showLabel: boolean — muestra "X%" flotando encima (centrado)
   */
  export let percent = 0;
  export let variant = 'accent';
  export let animated = true;
  export let height = 8;
  export let showLabel = false;

  $: safe = Math.max(0, Math.min(100, percent));
</script>

<div class="bar" style="--bar-h:{height}px">
  <div
    class="fill"
    class:info={variant === 'info'}
    class:warn={variant === 'warn'}
    class:crit={variant === 'crit'}
    class:paused={!animated}
    style="width:{safe}%"
  ></div>
  {#if showLabel}<span class="label">{safe.toFixed(0)}%</span>{/if}
</div>

<style>
  .bar {
    position: relative;
    height: var(--bar-h, 8px);
    background: var(--bg-2);
    border: 1px solid var(--border);
    overflow: hidden;
  }
  .fill {
    position: absolute;
    left: 0; top: 0; bottom: 0;
    background: var(--accent);
    transition: width 0.3s ease-out;
  }
  .fill.info { background: var(--info); }
  .fill.warn { background: var(--warn); }
  .fill.crit { background: var(--crit); }

  .fill::after {
    content: '';
    position: absolute;
    inset: 0;
    background: repeating-linear-gradient(
      45deg,
      rgba(0, 0, 0, 0.25) 0,
      rgba(0, 0, 0, 0.25) 3px,
      transparent 3px,
      transparent 6px
    );
    animation: stripe-move 0.8s linear infinite;
  }
  .fill.paused::after {
    animation-play-state: paused;
  }

  .label {
    position: absolute;
    left: 50%;
    top: 50%;
    transform: translate(-50%, -50%);
    font-family: var(--font-mono);
    font-size: 9px;
    font-weight: 700;
    color: var(--ink);
    letter-spacing: 0.5px;
    mix-blend-mode: difference;
    pointer-events: none;
  }
</style>
