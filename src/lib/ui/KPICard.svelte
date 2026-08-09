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
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    position: relative;
    font-family: var(--font-sans);
    background: var(--bg-card, #202731);
    border: 1px solid var(--line);
    border-radius: 6px;
  }

  .kpi::before, .kpi::after {
    content: none;
  }

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
    font-size: 11px;
    color: var(--ink-mute);
    letter-spacing: 0;
    font-weight: 600;
  }

  .kpi-state {
    font-size: 10.5px;
    letter-spacing: 0;
    color: var(--ink-mute);
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
    font-family: var(--font-sans);
    font-size: 24px;
    color: var(--ink);
    font-weight: 600;
    letter-spacing: -0.4px;
    font-feature-settings: "tnum";
  }
  .kpi-value.accent { color: var(--signal); }
  .kpi-value.warn   { color: var(--warn); }
  .kpi-value.crit   { color: var(--crit); }
  .kpi-value.info   { color: var(--info); }

  .kpi-unit {
    font-size: 10px;
    color: var(--ink-mute);
  }

  .kpi-delta {
    font-size: 9px;
    color: var(--ink-mute);
    margin-left: auto;
    letter-spacing: 0;
  }
  .kpi-delta.up   { color: var(--crit); }
  .kpi-delta.down { color: var(--signal); }
</style>
