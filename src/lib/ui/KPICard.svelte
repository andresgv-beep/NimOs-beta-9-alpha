<script>
  /**
   * KPICard · Celda de estadística con corner brackets HUD
   * ────────────────────────────────────────────────────────
   * Composición típica: NimHealth, NimShield, Storage dashboards.
   *
   * Uso:
   *   <KPICard
   *     label="CPU"
   *     value="4.2"
   *     unit="% · load 0.34"
   *     state="12 cores"
   *     stateVariant="ok"
   *     delta="▼ 0.8"
   *     deltaVariant="down"
   *     sparkData={cpuHistory}
   *     sparkVariant="accent"
   *     valueVariant="accent"
   *   />
   *
   * Props:
   *   - label:         string — header pequeño arriba-izquierda
   *   - value:         string/number — valor grande
   *   - valueVariant:  'default' | 'accent' | 'warn' | 'crit' | 'info'
   *   - unit:          string — sufijo del valor
   *   - state:         string — texto pequeño arriba-derecha junto al LED
   *   - stateVariant:  'ok' | 'warn' | 'crit' | 'off'
   *   - delta:         string — "▲ 3%", "▼ 0.8"
   *   - deltaVariant:  'default' | 'up' (crit) | 'down' (accent)
   *   - sparkData:     number[]
   *   - sparkVariant:  same as Sparkline.variant
   *   - sparkFilled:   boolean
   *   - bracketVariant: 'accent' | 'warn' | 'crit' | 'info' — color de brackets
   */
  import LED from './LED.svelte';
  import Sparkline from './Sparkline.svelte';

  export let label = '';
  export let value = '';
  export let valueVariant = 'default';
  export let unit = '';
  export let state = '';
  export let stateVariant = 'ok';
  export let delta = '';
  export let deltaVariant = 'default';
  export let sparkData = null;
  export let sparkVariant = 'accent';
  export let sparkFilled = false;
  export let bracketVariant = 'accent';
</script>

<div
  class="kpi"
  class:kpi-warn={bracketVariant === 'warn'}
  class:kpi-crit={bracketVariant === 'crit'}
  class:kpi-info={bracketVariant === 'info'}
>
  <div class="kpi-head">
    <span class="kpi-label">{label}</span>
    {#if state}
      <span class="kpi-state">
        <LED variant={stateVariant} size={6} />
        <span>{state}</span>
      </span>
    {/if}
  </div>

  <div class="kpi-row">
    <span
      class="kpi-value"
      class:accent={valueVariant === 'accent'}
      class:warn={valueVariant === 'warn'}
      class:crit={valueVariant === 'crit'}
      class:info={valueVariant === 'info'}
    >{value}</span>
    {#if unit}<span class="kpi-unit">{unit}</span>{/if}
    {#if delta}
      <span
        class="kpi-delta"
        class:up={deltaVariant === 'up'}
        class:down={deltaVariant === 'down'}
      >{delta}</span>
    {/if}
  </div>

  {#if sparkData && sparkData.length > 0}
    <Sparkline data={sparkData} variant={sparkVariant} filled={sparkFilled} />
  {/if}
</div>

<style>
  .kpi {
    padding: 14px 18px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    position: relative;
    font-family: var(--font-mono);
  }

  /* ── Corner brackets HUD ── */
  .kpi::before, .kpi::after {
    content: '';
    position: absolute;
    width: 10px;
    height: 10px;
    border-color: var(--accent);
    opacity: 0.4;
    transition: opacity 0.2s;
  }
  .kpi::before {
    top: 6px; left: 6px;
    border-top: 1px solid; border-left: 1px solid;
  }
  .kpi::after {
    bottom: 6px; right: 6px;
    border-bottom: 1px solid; border-right: 1px solid;
  }
  .kpi:hover::before, .kpi:hover::after { opacity: 1; }

  .kpi.kpi-warn::before, .kpi.kpi-warn::after {
    border-color: var(--warn); opacity: 0.6;
  }
  .kpi.kpi-crit::before, .kpi.kpi-crit::after {
    border-color: var(--crit); opacity: 0.7;
  }
  .kpi.kpi-info::before, .kpi.kpi-info::after {
    border-color: var(--info); opacity: 0.5;
  }

  .kpi-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .kpi-label {
    font-size: 9px;
    color: var(--fg-mute);
    text-transform: uppercase;
    letter-spacing: 1.8px;
  }

  .kpi-state {
    font-size: 9px;
    letter-spacing: 1px;
    color: var(--fg-dim);
    display: flex;
    align-items: center;
    gap: 5px;
  }

  .kpi-row {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }

  .kpi-value {
    font-size: 22px;
    color: var(--ink);
    font-weight: 600;
    letter-spacing: 0.5px;
    font-feature-settings: "tnum";
  }
  .kpi-value.accent { color: var(--accent); }
  .kpi-value.warn   { color: var(--warn); }
  .kpi-value.crit   { color: var(--crit); }
  .kpi-value.info   { color: var(--info); }

  .kpi-unit {
    font-size: 10px;
    color: var(--fg-mute);
  }

  .kpi-delta {
    font-size: 9px;
    color: var(--fg-dim);
    margin-left: auto;
    letter-spacing: 0.5px;
  }
  .kpi-delta.up   { color: var(--crit); }
  .kpi-delta.down { color: var(--accent); }
</style>
