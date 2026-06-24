<script>
  // Vista Resumen de NimShield: KPIs del motor + severidad 24h + últimos
  // eventos críticos. Recibe status y events; deriva sus propios contadores.
  import { fmtTime, sevClass, catShort } from './shieldFormat.js';

  export let status = { enabled: false, blockedIPs: 0, honeypots: 0, rules: 0 };
  export let events = [];
  export let now = Date.now();

  $: last24h = events.filter(ev => {
    const t = Date.parse(ev.timestamp);
    return !isNaN(t) && (now - t) < 24 * 3600 * 1000;
  });
  $: sevCounts = {
    crit: last24h.filter(e => e.severity === 'critical').length,
    high: last24h.filter(e => e.severity === 'high').length,
    med: last24h.filter(e => e.severity === 'medium' || e.severity === 'low').length,
  };
  $: sevMax = Math.max(sevCounts.crit, sevCounts.high, sevCounts.med, 1);
  $: recentCritical = events.filter(e => e.severity === 'critical' || e.severity === 'high').slice(0, 5);
</script>

<div class="r-stats">
  <div class="r-stat" class:ok={status.enabled} class:crit={!status.enabled}>
    <div class="r-stat-head">
      <span class="r-stat-lbl">Engine</span>
      <span class="r-stat-tag" class:ok={status.enabled}>
        <span class="d"></span>{status.enabled ? 'activo' : 'parado'}
      </span>
    </div>
    <div class="r-stat-val mono" class:ok={status.enabled} class:crit={!status.enabled}>
      {status.enabled ? 'ON' : 'OFF'}
    </div>
  </div>
  <div class="r-stat" class:crit={(status.blockedIPs ?? 0) > 0}>
    <div class="r-stat-head">
      <span class="r-stat-lbl">IPs bloqueadas</span>
      <span class="r-stat-tag">activos</span>
    </div>
    <div class="r-stat-val mono">{status.blockedIPs ?? 0}</div>
  </div>
  <div class="r-stat info">
    <div class="r-stat-head">
      <span class="r-stat-lbl">Honeypots</span>
      <span class="r-stat-tag info"><span class="d"></span>vigilando</span>
    </div>
    <div class="r-stat-val mono">{status.honeypots ?? 0}</div>
  </div>
  <div class="r-stat">
    <div class="r-stat-head">
      <span class="r-stat-lbl">Reglas</span>
      <span class="r-stat-tag">cargadas</span>
    </div>
    <div class="r-stat-val mono">{status.rules ?? 0}</div>
  </div>
</div>

<div class="r-sec">
  <span class="r-sec-lbl">eventos por severidad<span class="ac">· últimas 24h</span></span>
</div>

<div class="sev-bars">
  <div class="sev-row">
    <div class="sev-name"><span class="sev-led crit"></span>Crítico</div>
    <div class="sev-bar"><div class="sev-fill crit" style="width:{(sevCounts.crit / sevMax) * 100}%"></div></div>
    <div class="sev-count">{sevCounts.crit}</div>
  </div>
  <div class="sev-row">
    <div class="sev-name"><span class="sev-led high"></span>Alto</div>
    <div class="sev-bar"><div class="sev-fill high" style="width:{(sevCounts.high / sevMax) * 100}%"></div></div>
    <div class="sev-count">{sevCounts.high}</div>
  </div>
  <div class="sev-row">
    <div class="sev-name"><span class="sev-led med"></span>Medio</div>
    <div class="sev-bar"><div class="sev-fill med" style="width:{(sevCounts.med / sevMax) * 100}%"></div></div>
    <div class="sev-count">{sevCounts.med}</div>
  </div>
</div>

<div class="r-sec">
  <span class="r-sec-lbl">últimos eventos críticos<span class="ac">· {recentCritical.length} más recientes</span></span>
</div>

<div class="recent-events">
  {#if recentCritical.length === 0}
    <div class="ns-msg">Sin eventos críticos. Buena señal.</div>
  {:else}
    {#each recentCritical as ev (ev.id)}
      <div class="event-row">
        <span class="event-led {sevClass[ev.severity] || 'med'}"></span>
        <span class="event-time">{fmtTime(ev.timestamp)}</span>
        <span class="event-cat {ev.category}">{catShort[ev.category] || ev.category}</span>
        <span class="event-endpoint">{ev.endpoint || '—'}</span>
        <span class="event-ip">{ev.sourceIP || '—'}</span>
        <span class="event-rule">{ev.rule || '—'}</span>
      </div>
    {/each}
  {/if}
</div>

<style>
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }
  .mono { font-family: var(--font-mono); }

  .r-stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; }
  .r-stat { background: var(--bg-card, #15151a); border-radius: 8px; padding: 12px 12px 11px; display: flex; flex-direction: column; position: relative; overflow: hidden; }
  .r-stat::before { content: ''; position: absolute; top: 0; left: 0; width: 2px; height: 100%; background: var(--stat-edge, transparent); opacity: 0.7; }
  .r-stat.ok { --stat-edge: var(--st-ok, #00ff9f); }
  .r-stat.info { --stat-edge: var(--st-info, #4db8ff); }
  .r-stat.crit { --stat-edge: var(--st-crit, #ff5a5a); }
  .r-stat-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
  .r-stat-lbl { font-size: 10px; color: var(--fg-4, #7a7a82); font-weight: 500; letter-spacing: 0.6px; text-transform: uppercase; }
  .r-stat-tag { font-size: 9px; color: var(--fg-4, #7a7a82); display: flex; align-items: center; gap: 4px; font-family: var(--font-mono); }
  .r-stat-tag .d { width: 5px; height: 5px; border-radius: 1.5px; background: var(--fg-4, #7a7a82); }
  .r-stat-tag.ok { color: var(--st-ok, #00ff9f); }
  .r-stat-tag.ok .d { background: var(--st-ok, #00ff9f); }
  .r-stat-tag.info { color: var(--st-info, #4db8ff); }
  .r-stat-tag.info .d { background: var(--st-info, #4db8ff); }
  .r-stat-val { font-size: 22px; font-weight: 500; color: var(--fg, #f0f0f0); line-height: 1; letter-spacing: -0.4px; font-family: var(--font-mono); }
  .r-stat-val.ok { color: var(--st-ok, #00ff9f); }
  .r-stat-val.crit { color: var(--st-crit, #ff5a5a); }

  .r-sec { display: flex; align-items: center; justify-content: space-between; margin-top: 24px; margin-bottom: 12px; flex-wrap: wrap; gap: 8px; }
  .r-sec-lbl { font-size: 11px; color: var(--fg-4, #7a7a82); font-weight: 500; letter-spacing: 0.6px; font-family: var(--font-mono); text-transform: uppercase; }
  .r-sec-lbl .ac { color: var(--fg-2, #d0d0d4); margin-left: 4px; }

  .sev-bars { background: var(--bg-card, #15151a); border-radius: 10px; padding: 16px 18px; display: flex; flex-direction: column; gap: 10px; }
  .sev-row { display: grid; grid-template-columns: 90px 1fr 50px; gap: 12px; align-items: center; font-size: 11px; }
  .sev-name { display: flex; align-items: center; gap: 8px; font-family: var(--font-mono); text-transform: uppercase; letter-spacing: 0.6px; color: var(--fg-3, #9c9ca4); font-size: 10px; }
  .sev-led { width: 8px; height: 8px; border-radius: 1.5px; }
  .sev-led.crit { background: var(--st-crit, #ff5a5a); }
  .sev-led.high { background: var(--st-warn, #ffc857); }
  .sev-led.med { background: var(--st-info, #4db8ff); }
  .sev-bar { height: 6px; background: var(--bd-2, #20202a); border-radius: 2px; overflow: hidden; }
  .sev-fill { height: 100%; border-radius: 2px; transition: width 0.3s; }
  .sev-fill.crit { background: var(--st-crit, #ff5a5a); }
  .sev-fill.high { background: var(--st-warn, #ffc857); }
  .sev-fill.med { background: var(--st-info, #4db8ff); }
  .sev-count { font-family: var(--font-mono); font-size: 12px; color: var(--fg, #f0f0f0); text-align: right; font-variant-numeric: tabular-nums; }

  .recent-events { display: flex; flex-direction: column; gap: 4px; }
  .event-row { background: var(--bg-card, #15151a); border-radius: 7px; padding: 10px 14px; display: grid; grid-template-columns: 8px 80px 80px 1fr auto auto; gap: 10px; align-items: center; font-size: 11px; }
  .event-led { width: 8px; height: 8px; border-radius: 1.5px; }
  .event-led.crit { background: var(--st-crit, #ff5a5a); }
  .event-led.high { background: var(--st-warn, #ffc857); }
  .event-led.med { background: var(--st-info, #4db8ff); }
  .event-time { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); font-variant-numeric: tabular-nums; }
  .event-cat { font-family: var(--font-mono); font-size: 9px; letter-spacing: 0.5px; text-transform: uppercase; font-weight: 600; padding: 2px 7px; border-radius: 3px; text-align: center; }
  .event-cat.auth { background: rgba(255,200,87,0.10); color: var(--st-warn, #ffc857); }
  .event-cat.traversal { background: rgba(255,90,90,0.10); color: var(--st-crit, #ff5a5a); }
  .event-cat.injection { background: rgba(255,90,90,0.10); color: var(--st-crit, #ff5a5a); }
  .event-cat.scan { background: rgba(77,184,255,0.10); color: var(--st-info, #4db8ff); }
  .event-cat.honeypot { background: rgba(255,156,90,0.10); color: var(--nim-folder, #ff9c5a); }
  .event-cat.system { background: rgba(122,158,177,0.10); color: var(--ui-select, #7a9eb1); }
  .event-endpoint { font-family: var(--font-mono); font-size: 11px; color: var(--fg-2, #d0d0d4); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .event-ip { font-family: var(--font-mono); font-size: 11px; color: var(--fg-3, #9c9ca4); font-variant-numeric: tabular-nums; }
  .event-rule { font-family: var(--font-mono); font-size: 9px; color: var(--fg-4, #7a7a82); padding: 1px 6px; border: 1px solid var(--bd-2, #20202a); border-radius: 3px; letter-spacing: 0.4px; text-align: center; }
</style>
