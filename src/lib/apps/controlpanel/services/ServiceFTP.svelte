<script>
  /**
   * ServiceFTP · Panel de Control · página de FTP (vsftpd)
   * ────────────────────────────────────────────────────────
   * Backend solo expone status/start/stop (sin config), así que es simple.
   *   GET  /api/ftp/status → { installed, running }
   *   POST /api/ftp/start | stop
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  export let host = '';

  let status = { installed: true, running: false, loading: true };
  let busy = false;
  let msg = '';
  let msgError = false;

  async function load() {
    try {
      const r = await fetch('/api/ftp/status', { headers: hdrs() });
      if (r.ok) { const d = await r.json(); status = { ...status, ...d, loading: false }; }
      else status.loading = false;
    } catch { status.loading = false; }
  }

  async function toggleService() {
    if (busy) return;
    busy = true; msg = '';
    const action = status.running ? 'stop' : 'start';
    try {
      const r = await fetch(`/api/ftp/${action}`, { method: 'POST', headers: hdrs() });
      if (!r.ok) { const e = await r.json().catch(() => ({})); msg = e.error || 'Error'; msgError = true; }
    } catch { msg = 'Error de red'; msgError = true; }
    setTimeout(load, 600);
    busy = false;
  }

  onMount(load);
</script>

<div class="svc-pane">
  <div class="sp-bar">
    <div class="sp-status">
      <span class="sp-led" class:on={status.running}></span>
      {#if status.loading}comprobando…
      {:else if !status.installed}vsftpd no instalado
      {:else}{status.running ? 'activo' : 'detenido'} · puerto 21{/if}
    </div>
    <button class="sp-toggle" class:on={status.running} disabled={busy || !status.installed} on:click={toggleService} title={status.running ? "Detener" : "Iniciar"}>
      <span class="sp-toggle-thumb"></span>
    </button>
  </div>

  {#if msg}<div class="sp-msg" class:error={msgError}>{msg}</div>{/if}

  <div class="sp-lan">Acceso: <b>ftp://{host || 'tu-nas'}</b> · puerto 21</div>

  {#if status.running}
    <div class="sp-note">⚠ FTP transmite credenciales sin cifrar. Úsalo solo en redes de confianza; para acceso remoto prefiere SMB o SFTP.</div>
  {/if}

  <div class="sp-info">
    FTP clásico (vsftpd) para clientes que lo requieran. La configuración avanzada se gestiona
    desde el sistema; aquí solo se controla el arranque del servicio.
  </div>
</div>

<style>
  .svc-pane { display: flex; flex-direction: column; gap: 16px; }
  .sp-bar { display: flex; align-items: center; justify-content: space-between; }
  .sp-status { display: flex; align-items: center; gap: 10px; font-size: 12px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .sp-led { width: 9px; height: 9px; border-radius: 2.5px; background: var(--fg-5, #5a5a62); }
  .sp-led.on { background: var(--st-ok, #00ff9f); }
  .sp-toggle { width: 40px; height: 20px; background: var(--bg-inner, #101015); border: 1px solid var(--bd-2, #20202a); border-radius: 5px; position: relative; cursor: pointer; padding: 0; flex-shrink: 0; }
  .sp-toggle-thumb { position: absolute; top: 2px; left: 2px; width: 14px; height: 14px; background: var(--fg-5, #5a5a62); border-radius: 3px; transition: left 0.15s, background 0.15s; }
  .sp-toggle.on { background: rgba(0,255,159,0.12); border-color: rgba(0,255,159,0.4); }
  .sp-toggle.on .sp-toggle-thumb { left: 22px; background: var(--nim-green, #00ff9f); }
  .sp-toggle:disabled { opacity: 0.5; cursor: not-allowed; }
  .sp-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .sp-msg.error { color: var(--st-crit, #ff5a5a); }
  .sp-lan { background: var(--bg-inner, #101015); border: 1px solid var(--bd-2, #20202a); border-radius: 8px; padding: 12px 14px; font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .sp-lan b { color: var(--st-info, #4db8ff); }
  .sp-note { font-size: 10px; color: var(--st-warn, #ffc857); font-family: var(--font-mono); line-height: 1.5; background: rgba(255,200,87,0.06); border: 1px solid rgba(255,200,87,0.2); border-radius: 6px; padding: 10px 12px; }
  .sp-info { font-size: 11px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono); line-height: 1.6; }
</style>
