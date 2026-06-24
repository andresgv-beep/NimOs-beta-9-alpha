<script>
  // MobileHeader — chip con nombre del NAS + acciones rápidas (red, power).
  // Recibe el hostname y emite eventos para las acciones; no contiene lógica
  // de red propia para mantenerlo tonto y reutilizable.
  import { createEventDispatcher } from 'svelte';
  const dispatch = createEventDispatcher();

  export let hostname = 'nimos';
  export let alerts = 0;
</script>

<header class="m-header">
  <div class="nas-chip">
    <span class="nas-logo">N</span>
    <span class="nas-name">{hostname}</span>
  </div>

  <button class="hdr-btn" on:click={() => dispatch('alerts')} aria-label="Alertas">
    <svg viewBox="0 0 24 24"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>
    {#if alerts > 0}<span class="hdr-badge">{alerts > 99 ? '99+' : alerts}</span>{/if}
  </button>

  <button class="hdr-btn power" on:click={() => dispatch('power')} aria-label="Energía">
    <svg viewBox="0 0 24 24"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/></svg>
  </button>
</header>

<style>
  .m-header { display: flex; align-items: center; gap: 10px; padding: 8px 0 16px; }
  .nas-chip {
    display: flex; align-items: center; gap: 9px; flex: 1;
    background: var(--bg-card); border: 1px solid var(--line);
    border-radius: 10px; padding: 9px 13px;
  }
  .nas-logo {
    width: 22px; height: 22px; background: var(--signal); border-radius: 5px;
    display: flex; align-items: center; justify-content: center;
    color: var(--bg-window); font-weight: 700; font-size: 11px;
    font-family: var(--font-mono);
  }
  .nas-name { font-size: 14px; font-weight: 700; color: var(--ink); }
  .hdr-btn {
    width: 40px; height: 40px; border-radius: 10px; flex-shrink: 0;
    background: var(--bg-card); border: 1px solid var(--line);
    display: flex; align-items: center; justify-content: center;
    color: var(--ink-dim); position: relative; cursor: pointer;
  }
  .hdr-btn:active { background: var(--main-hover); }
  .hdr-btn svg { width: 18px; height: 18px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .hdr-btn.power { color: var(--crit); }
  .hdr-badge {
    position: absolute; top: -4px; right: -4px;
    background: var(--crit); color: #fff; font-size: 9px; font-weight: 700;
    min-width: 16px; height: 16px; border-radius: 8px;
    display: flex; align-items: center; justify-content: center;
    padding: 0 4px; font-family: var(--font-mono);
  }
</style>
