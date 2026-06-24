<script>
  /**
   * SysMon · Widget Sistema 2×1 (CPU + RAM + temp) · NimOS Beta 8.1
   * ───────────────────────────────────────────────────────────────
   * Layout ancho: roscos CPU/RAM a la izquierda (cada uno con su dato:
   * núcleos / memoria), separador, y temperatura de CPU destacada a la
   * derecha aprovechando el ancho del 2×1. El `load` va en la cabecera.
   *
   * Datos: /api/hardware/stats vía widgetData (3s, compartido por
   * refcount con CPU/RAM/SysPanel). El endpoint incluye `temp`.
   * Forma: { cpu:{percent,cores,load1}, memory:{percent,usedGB,
   *          totalGB}, temp:Number }
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';
  import Ring from './parts/Ring.svelte';

  export const w = 2; // talla única · contrato
  export const h = 1;

  const data = topicStore('system');
  onMount(() => acquire('system'));

  $: cpu = $data?.cpu ?? null;
  $: mem = $data?.memory ?? null;
  $: temp = $data?.temp ?? null;

  // Estado térmico · umbrales Raspberry Pi (throttle ~80 °C)
  $: tempState = temp == null ? null
    : temp >= 80 ? { txt: 'crítico', cls: 'crit' }
    : temp >= 70 ? { txt: 'alto',    cls: 'warn' }
    : { txt: 'normal', cls: 'ok' };
</script>

<div class="sysmon">
  <div class="head">
    <span class="title">Sistema</span>
    <span class="aux">{cpu ? `load ${(cpu.load1 ?? 0).toFixed(2)}` : '—'}</span>
  </div>

  <div class="body">
    <div class="rings">
      <div class="wrap">
        <Ring pct={cpu?.percent ?? null} label="CPU" size={66} thick={5} />
        <div class="sub">{cpu ? `${cpu.cores} cores` : ' '}</div>
      </div>
      <div class="wrap">
        <Ring pct={mem?.percent ?? null} label="RAM" size={66} thick={5} />
        <div class="sub">{mem ? `${mem.usedGB}/${mem.totalGB}G` : ' '}</div>
      </div>
    </div>

    <div class="divider"></div>

    <div class="temp">
      <span class="t-label">Temp CPU</span>
      <div class="t-row">
        <svg class="t-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
          stroke-width="1.6" stroke-linecap="square">
          <path d="M10 4a2 2 0 0 1 4 0v9.3a4.5 4.5 0 1 1-4 0Z" />
          <path d="M12 9v6" />
        </svg>
        <span class="t-val">{temp == null ? '—' : `${temp}°`}</span>
      </div>
      <span class="t-sub {tempState?.cls ?? ''}">{tempState?.txt ?? ' '}</span>
    </div>
  </div>
</div>

<style>
  .sysmon {
    height: 100%;
    display: flex;
    flex-direction: column;
    padding: 13px 15px;
    user-select: none;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 6px;
  }
  .title {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .aux {
    font-family: var(--font-mono);
    font-size: 9px;
    letter-spacing: 0.06em;
    color: var(--signal);
  }
  .body {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .rings {
    flex: 1;
    display: flex;
    gap: 18px;
    align-items: center;
    justify-content: center;
  }
  .wrap {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 5px;
  }
  .sub {
    font-family: var(--font-mono);
    font-size: 8.5px;
    color: var(--ink-faint);
    letter-spacing: 0.02em;
    min-height: 10px;
  }
  .divider {
    width: 1px;
    align-self: stretch;
    margin: 8px 0;
    background: rgba(255, 255, 255, 0.08);
  }
  .temp {
    width: 86px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }
  .t-label {
    font-family: var(--font-mono);
    font-size: 8px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .t-row {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .t-ico {
    width: 16px;
    height: 16px;
    color: var(--ink-mute);
  }
  .t-val {
    font-family: var(--font-mono);
    font-size: 22px;
    font-weight: 600;
    color: var(--ink);
    line-height: 1;
  }
  .t-sub {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-faint);
    min-height: 11px;
  }
  .t-sub.ok   { color: var(--signal); }
  .t-sub.warn { color: var(--warn); }
  .t-sub.crit { color: var(--crit); }
</style>
