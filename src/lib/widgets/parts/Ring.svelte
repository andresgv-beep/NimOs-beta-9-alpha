<script>
  /**
   * Ring · Pieza presentacional compartida (widgets)
   * ─────────────────────────────────────────────────
   * Rosco SVG con % y etiqueta dentro. NO es un widget: es una pieza
   * interna de src/lib/widgets/parts/ que usan SysMon, CPU y RAM.
   * Sin datos propios, sin stores — recibe todo por props.
   *
   * Umbrales de color (mismos en todos los roscos del sistema):
   *   < 80 → --signal · ≥ 80 → --warn · ≥ 90 → --crit
   *
   * pct null → estado skeleton ("—", rosco vacío).
   */
  export let pct = null;        // 0..100 | null (sin datos)
  export let label = '';        // CPU / RAM ...
  export let size = 78;         // diámetro px
  export let thick = 6;         // grosor del trazo

  $: r = (size - thick) / 2 - 1;
  $: circ = 2 * Math.PI * r;
  $: offset = pct == null ? circ : circ * (1 - Math.min(100, Math.max(0, pct)) / 100);
  $: color = pct == null ? 'var(--ink-trace)'
    : pct >= 90 ? 'var(--crit)'
    : pct >= 80 ? 'var(--warn)'
    : 'var(--signal)';
</script>

<div class="ring" style="width:{size}px;height:{size}px">
  <svg width={size} height={size}>
    <circle cx={size / 2} cy={size / 2} {r}
      fill="none" stroke="rgba(255,255,255,.08)" stroke-width={thick} />
    <circle cx={size / 2} cy={size / 2} {r}
      fill="none" stroke={color} stroke-width={thick} stroke-linecap="round"
      stroke-dasharray={circ} stroke-dashoffset={offset} class="arc" />
  </svg>
  <div class="val">
    <span class="num">{pct == null ? '—' : pct}{#if pct != null}<small>%</small>{/if}</span>
    {#if label}<span class="lbl">{label}</span>{/if}
  </div>
</div>

<style>
  .ring { position: relative; }
  svg { transform: rotate(-90deg); display: block; }
  .arc { transition: stroke-dashoffset 0.6s cubic-bezier(0.4, 0, 0.2, 1), stroke 0.3s ease; }

  .val {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1px;
    line-height: 1;
  }
  .num {
    font-family: var(--font-mono);
    font-size: 17px;
    font-weight: 600;
    color: var(--ink);
  }
  .num small {
    font-size: 8px;
    color: var(--ink-faint);
    margin-left: 1px;
  }
  .lbl {
    font-family: var(--font-mono);
    font-size: 7.5px;
    font-weight: 500;
    letter-spacing: 0.16em;
    color: var(--ink-mute);
    text-transform: uppercase;
  }
</style>
