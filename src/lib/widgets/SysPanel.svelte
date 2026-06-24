<script>
  /**
   * SysPanel · Widget Sistema 2×2 · NimOS Beta 8.1
   * ───────────────────────────────────────────────
   * Panel completo de sistema: roscos CPU/RAM (con icono dentro) +
   * fila Temp CPU / Load Avg + Uptime al pie. Es el rediseño de la
   * familia "sistema" (junio 2026); RingSolo (1×1) y SysMon (2×1)
   * siguen vivos y se rediseñarán aparte.
   *
   * AUTOCONTENIDO por decisión: rosco propio aquí dentro en vez de
   * reusar parts/Ring.svelte, para no arrastrar este lenguaje a los
   * widgets viejos antes de tocarlos. Cuando los tres compartan
   * estética, se extrae la pieza común.
   *
   * Talla FIJA 2×2 (contrato). Datos: /api/hardware/stats vía
   * widgetData (topic 'system', 3s, compartido por refcount con
   * SysMon/RingSolo). El daemon añade `temp` y `uptime` al mismo
   * endpoint → cero polling extra.
   * Forma: { cpu:{percent,cores,load1}, memory:{percent,usedGB,
   *          totalGB}, temp:Number, uptime:String }
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';

  export const w = 2; // talla única · contrato
  export const h = 2;

  const data = topicStore('system');
  onMount(() => acquire('system'));

  $: cpu = $data?.cpu ?? null;
  $: mem = $data?.memory ?? null;
  $: temp = $data?.temp ?? null;
  $: uptime = $data?.uptime ?? null;

  // ── Roscos ──
  const SIZE = 96, THICK = 7;
  const R = (SIZE - THICK) / 2 - 1;
  const CIRC = 2 * Math.PI * R;

  function arc(pct) {
    return pct == null ? CIRC : CIRC * (1 - Math.min(100, Math.max(0, pct)) / 100);
  }
  function color(pct) {
    return pct == null ? 'var(--ink-trace)'
      : pct >= 90 ? 'var(--crit)'
      : pct >= 80 ? 'var(--warn)'
      : 'var(--signal)';
  }

  // ── Estado térmico · umbrales Raspberry Pi (throttle ~80 °C) ──
  $: tempState = temp == null ? null
    : temp >= 80 ? { txt: 'crítico', cls: 'crit' }
    : temp >= 70 ? { txt: 'alto',    cls: 'warn' }
    : { txt: 'normal', cls: 'ok' };
</script>

<div class="panel">
  <div class="head">
    <span class="title">Sistema</span>
    <span class="aux">{cpu ? `load ${(cpu.load1 ?? 0).toFixed(2)}` : '—'}</span>
  </div>

  <!-- ── Roscos CPU / RAM ── -->
  <div class="rings">
    <div class="ring-wrap">
      <div class="ring" style="width:{SIZE}px;height:{SIZE}px">
        <svg width={SIZE} height={SIZE}>
          <circle cx={SIZE / 2} cy={SIZE / 2} r={R}
            fill="none" stroke="rgba(255,255,255,.08)" stroke-width={THICK} />
          <circle cx={SIZE / 2} cy={SIZE / 2} r={R}
            fill="none" stroke={color(cpu?.percent)} stroke-width={THICK}
            stroke-linecap="round" stroke-dasharray={CIRC}
            stroke-dashoffset={arc(cpu?.percent ?? null)} class="track" />
        </svg>
        <div class="val">
          <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            stroke-width="1.6" stroke-linecap="square">
            <rect x="7" y="7" width="10" height="10" />
            <rect x="10" y="10" width="4" height="4" />
            <path d="M9 7V4M12 7V4M15 7V4M9 20v-3M12 20v-3M15 20v-3M7 9H4M7 12H4M7 15H4M20 9h-3M20 12h-3M20 15h-3" />
          </svg>
          <span class="num">{cpu?.percent ?? '—'}{#if cpu}<small>%</small>{/if}</span>
          <span class="lbl">CPU</span>
        </div>
      </div>
      <div class="ring-sub">{cpu ? `${cpu.cores} cores` : ' '}</div>
    </div>

    <div class="ring-wrap">
      <div class="ring" style="width:{SIZE}px;height:{SIZE}px">
        <svg width={SIZE} height={SIZE}>
          <circle cx={SIZE / 2} cy={SIZE / 2} r={R}
            fill="none" stroke="rgba(255,255,255,.08)" stroke-width={THICK} />
          <circle cx={SIZE / 2} cy={SIZE / 2} r={R}
            fill="none" stroke={color(mem?.percent)} stroke-width={THICK}
            stroke-linecap="round" stroke-dasharray={CIRC}
            stroke-dashoffset={arc(mem?.percent ?? null)} class="track" />
        </svg>
        <div class="val">
          <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            stroke-width="1.6" stroke-linecap="square">
            <rect x="3" y="8" width="18" height="8" />
            <path d="M7 16v3M12 16v3M17 16v3M7 11v2M10.5 11v2M14 11v2M17 11v2" />
          </svg>
          <span class="num">{mem?.percent ?? '—'}{#if mem}<small>%</small>{/if}</span>
          <span class="lbl">RAM</span>
        </div>
      </div>
      <div class="ring-sub">{mem ? `${mem.usedGB}/${mem.totalGB} GB` : ' '}</div>
    </div>
  </div>

  <!-- ── Fila Temp / Load ── -->
  <div class="metrics">
    <div class="cell">
      <span class="m-label">Temp CPU</span>
      <div class="m-row">
        <svg class="m-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
          stroke-width="1.6" stroke-linecap="square">
          <path d="M10 4a2 2 0 0 1 4 0v9.3a4.5 4.5 0 1 1-4 0Z" />
          <path d="M12 9v6" />
        </svg>
        <span class="m-val">{temp == null ? '—' : `${temp}°C`}</span>
      </div>
      <span class="m-sub {tempState?.cls ?? ''}">{tempState?.txt ?? ' '}</span>
    </div>
    <div class="cell">
      <span class="m-label">Load Avg</span>
      <div class="m-row">
        <svg class="m-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
          stroke-width="1.6" stroke-linecap="square">
          <path d="M2 12h4l2.5-6 4 12L15 9l1.5 3H22" />
        </svg>
        <span class="m-val">{cpu ? (cpu.load1 ?? 0).toFixed(2) : '—'}</span>
      </div>
      <span class="m-sub">{cpu ? `${cpu.cores} cores` : ' '}</span>
    </div>
  </div>

  <!-- ── Uptime ── -->
  <div class="uptime">
    <span class="m-label">Uptime</span>
    <span class="up-val">{uptime ?? '—'}</span>
  </div>
</div>

<style>
  .panel {
    height: 100%;
    display: flex;
    flex-direction: column;
    padding: 14px 16px;
    user-select: none;
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }
  .title {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .aux {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.06em;
    color: var(--signal);
  }

  /* ── Roscos ── */
  .rings {
    display: flex;
    gap: 30px;
    align-items: center;
    justify-content: center;
    padding: 4px 0 12px;
  }
  .ring-wrap {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }
  .ring { position: relative; }
  .ring svg { transform: rotate(-90deg); display: block; }
  .track {
    transition: stroke-dashoffset 0.6s cubic-bezier(0.4, 0, 0.2, 1), stroke 0.3s ease;
  }
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
  .ico {
    width: 16px;
    height: 16px;
    color: var(--ink-mute);
    margin-bottom: 3px;
  }
  .num {
    font-family: var(--font-mono);
    font-size: 19px;
    font-weight: 600;
    color: var(--ink);
  }
  .num small {
    font-size: 8.5px;
    color: var(--ink-faint);
    margin-left: 1px;
  }
  .lbl {
    font-family: var(--font-mono);
    font-size: 8px;
    font-weight: 500;
    letter-spacing: 0.16em;
    color: var(--ink-mute);
    text-transform: uppercase;
    margin-top: 1px;
  }
  .ring-sub {
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--ink-faint);
    letter-spacing: 0.03em;
    min-height: 12px;
  }

  /* ── Fila Temp / Load ── */
  .metrics {
    display: grid;
    grid-template-columns: 1fr 1fr;
    border-top: 1px solid rgba(255, 255, 255, 0.07);
    padding-top: 12px;
  }
  .cell {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }
  .cell + .cell {
    border-left: 1px solid rgba(255, 255, 255, 0.07);
  }
  .m-label {
    font-family: var(--font-mono);
    font-size: 8px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--ink-faint);
  }
  .m-row {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .m-ico {
    width: 18px;
    height: 18px;
    color: var(--ink-mute);
  }
  .m-val {
    font-family: var(--font-mono);
    font-size: 18px;
    font-weight: 600;
    color: var(--ink);
    line-height: 1;
  }
  .m-sub {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-faint);
    min-height: 12px;
  }
  .m-sub.ok   { color: var(--signal); }
  .m-sub.warn { color: var(--warn); }
  .m-sub.crit { color: var(--crit); }

  /* ── Uptime ── */
  .uptime {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 4px;
    border-top: 1px solid rgba(255, 255, 255, 0.07);
    margin-top: 12px;
  }
  .up-val {
    font-family: var(--font-mono);
    font-size: 14px;
    font-weight: 600;
    color: var(--ink);
    letter-spacing: 0.04em;
  }
</style>
