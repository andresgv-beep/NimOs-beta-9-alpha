<script>
  // MobileStatCard — tarjeta de estado genérica. Recibe un título, un icono
  // opcional, un badge (temperatura/salud) y una lista de métricas con barra.
  // Se usa en Inicio para Sistema y Volúmenes; reutilizable en otras vistas.
  export let title = '';
  export let icon = 'server';      // 'server' | 'disk'
  export let badge = null;          // { text, variant: 'warn'|'crit'|'ok' }
  // metrics: [{ label, value, pct, variant: 'info'|'green'|'orange' }]
  export let metrics = [];
</script>

<div class="scard">
  <div class="scard-head">
    <div class="scard-title">
      {#if icon === 'disk'}
        <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><circle cx="12" cy="12" r="3"/></svg>
      {:else}
        <svg viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/></svg>
      {/if}
      {title}
    </div>
    {#if badge}
      <span class="badge {badge.variant || 'warn'}">{badge.text}</span>
    {/if}
  </div>

  {#each metrics as m}
    <div class="metric">
      <div class="metric-top">
        <span class="metric-k">{m.label}</span>
        <span class="metric-v {m.variant || 'info'}">{m.value}</span>
      </div>
      <div class="bar"><div class="bar-fill {m.variant || 'info'}" style="width:{Math.max(0, Math.min(100, m.pct))}%"></div></div>
    </div>
  {/each}

  <slot />
</div>

<style>
  .scard { background: var(--bg-card); border: 1px solid var(--line); border-radius: 12px; padding: 14px; height: 100%; box-sizing: border-box; }
  .scard-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
  .scard-title { display: flex; align-items: center; gap: 7px; font-size: 13px; font-weight: 600; color: var(--ink); }
  .scard-title svg { width: 16px; height: 16px; stroke: var(--info); stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .badge { font-size: 11px; font-weight: 700; padding: 2px 8px; border-radius: 5px; font-family: var(--font-mono); }
  .badge.warn { background: var(--warn); color: var(--bg-window); }
  .badge.crit { background: var(--crit); color: #fff; }
  .badge.ok { background: var(--signal); color: var(--bg-window); }

  .metric { margin-bottom: 11px; }
  .metric:last-child { margin-bottom: 0; }
  .metric-top { display: flex; justify-content: space-between; font-size: 11px; margin-bottom: 5px; }
  .metric-k { color: var(--ink-mute); }
  .metric-v { font-family: var(--font-mono); font-weight: 600; color: var(--info); }
  .metric-v.green { color: var(--signal); }
  .metric-v.orange { color: var(--warn); }
  .bar { height: 5px; background: var(--bg-inner); border-radius: 2px; overflow: hidden; }
  .bar-fill { height: 100%; background: var(--info); border-radius: 2px; transition: width 0.4s ease; }
  .bar-fill.green { background: var(--signal); }
  .bar-fill.orange { background: var(--warn); }
</style>
