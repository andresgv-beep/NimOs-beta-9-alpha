<script>
  /**
   * CPServices · Panel de Control · sección Servicios
   * ───────────────────────────────────────────────────
   * Barra de pestañas (SMB · SSH · FTP · WebDAV) y debajo la página del
   * servicio activo. Cada página es un componente propio bajo services/.
   *
   * El estado (LED de cada pestaña) se sondea aquí para dar feedback sin
   * tener que entrar en cada uno. Respuestas del daemon: JSON plano.
   */
  import { onMount, onDestroy } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import ServiceSMB from './services/ServiceSMB.svelte';
  import ServiceSSH from './services/ServiceSSH.svelte';
  import ServiceFTP from './services/ServiceFTP.svelte';
  import ServiceWebDAV from './services/ServiceWebDAV.svelte';

  const tabs = [
    { id: 'smb',    label: 'SMB' },
    { id: 'ssh',    label: 'SSH' },
    { id: 'ftp',    label: 'FTP' },
    { id: 'webdav', label: 'WebDAV' },
  ];

  let active = 'smb';
  let host = '';
  // estado por servicio para el LED de la pestaña: 'on' | 'off' | null(desconocido)
  let state = { smb: null, ssh: null, ftp: null, webdav: 'off' };
  let pollTimer = null;

  async function probe() {
    try {
      const [rsmb, rssh, rftp] = await Promise.all([
        fetch('/api/smb/status', { headers: hdrs() }),
        fetch('/api/ssh/status', { headers: hdrs() }),
        fetch('/api/ftp/status', { headers: hdrs() }),
      ]);
      if (rsmb.ok) { const d = await rsmb.json(); state.smb = d.running ? 'on' : 'off'; }
      if (rssh.ok) { const d = await rssh.json(); state.ssh = d.running ? 'on' : 'off'; }
      if (rftp.ok) { const d = await rftp.json(); state.ftp = d.running ? 'on' : 'off'; }
      state = state;
    } catch {}
  }

  onMount(() => {
    host = window.location.hostname || 'tu-nas';
    probe();
    pollTimer = setInterval(probe, 8000);
  });
  onDestroy(() => clearInterval(pollTimer));
</script>

<div class="cp-services">
  <!-- Barra de pestañas -->
  <div class="svc-tabs">
    {#each tabs as t}
      <button class="svc-tab" class:active={active === t.id} on:click={() => active = t.id}>
        <span class="svc-tab-led" class:on={state[t.id] === 'on'} class:unknown={state[t.id] === null}></span>
        {t.label}
      </button>
    {/each}
  </div>

  <!-- Página del servicio activo -->
  <div class="svc-content">
    {#if active === 'smb'}
      <ServiceSMB {host} />
    {:else if active === 'ssh'}
      <ServiceSSH {host} />
    {:else if active === 'ftp'}
      <ServiceFTP {host} />
    {:else if active === 'webdav'}
      <ServiceWebDAV />
    {/if}
  </div>
</div>

<style>
  .cp-services { display: flex; flex-direction: column; gap: 18px; }

  .svc-tabs {
    display: flex;
    gap: 4px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    padding: 4px;
  }
  .svc-tab {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 8px 16px;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--fg-4, #7a7a82);
    font-size: 12px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .svc-tab:hover { color: var(--fg-3, #9c9ca4); }
  .svc-tab.active {
    background: var(--bg-card, #15151a);
    color: var(--fg, #f0f0f0);
  }
  .svc-tab-led {
    width: 7px;
    height: 7px;
    border-radius: 2px;
    background: var(--fg-5, #5a5a62);
  }
  .svc-tab-led.on { background: var(--st-ok, #00ff9f); }
  .svc-tab-led.unknown { background: var(--bd-3, #2a2a32); }

  .svc-content { min-height: 200px; }
</style>
