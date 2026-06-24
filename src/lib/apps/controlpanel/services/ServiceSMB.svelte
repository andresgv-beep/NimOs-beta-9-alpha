<script>
  /**
   * ServiceSMB · Panel de Control · página dedicada de SMB / Samba
   * ────────────────────────────────────────────────────────────────
   * Gestión completa de SMB: toggle del servicio, config del servidor,
   * exposición de carpetas en la LAN y contraseña SMB por usuario.
   *
   * API:
   *   GET  /api/smb/status  → { installed, running, version, config, port }
   *   POST /api/smb/config  { workgroup, serverString, ... }
   *   POST /api/smb/apply | start | stop | restart
   *   POST /api/smb/set-password { username, password }
   *   GET  /api/shares      → carpetas (con flag .smb)
   *   PUT  /api/smb/share/:name  ← NOTA: el backend aún NO materializa esto
   *
   * ⚠ La exposición de carpetas se muestra y guarda el flag, pero el daemon
   *   todavía no genera los bloques en smb.conf (endpoint vacío). Marcado
   *   en la UI como "pendiente de aplicar en backend".
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  export let host = '';

  let status = { installed: true, running: false, version: '', port: 445, loading: true };
  let config = { workgroup: 'WORKGROUP', serverString: 'NimOS NAS' };
  let shares = [];
  let busy = false;
  let savingCfg = false;
  let msg = '';
  let msgError = false;

  // contraseña
  let pwUser = '';
  let pwPass = '';
  let pwBusy = false;

  async function load() {
    try {
      const [rs, rsh] = await Promise.all([
        fetch('/api/smb/status', { headers: hdrs() }),
        fetch('/api/shares', { headers: hdrs() }),
      ]);
      if (rs.ok) {
        const d = await rs.json();
        status = { ...status, ...d, loading: false };
        if (d.config) config = { workgroup: 'WORKGROUP', serverString: 'NimOS NAS', ...d.config };
      } else status.loading = false;
      if (rsh.ok) shares = await rsh.json();
    } catch { status.loading = false; }
  }

  async function toggleService() {
    if (busy) return;
    busy = true; msg = '';
    const action = status.running ? 'stop' : 'start';
    try {
      const r = await fetch(`/api/smb/${action}`, { method: 'POST', headers: hdrs() });
      if (!r.ok) { const e = await r.json().catch(() => ({})); msg = e.error || 'Error'; msgError = true; }
    } catch { msg = 'Error de red'; msgError = true; }
    setTimeout(load, 600);
    busy = false;
  }

  async function restart() {
    if (busy) return;
    busy = true; msg = '';
    try { await fetch('/api/smb/restart', { method: 'POST', headers: hdrs() }); } catch {}
    setTimeout(load, 600);
    busy = false;
  }

  async function saveConfig() {
    if (savingCfg) return;
    savingCfg = true; msg = '';
    try {
      const r1 = await fetch('/api/smb/config', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      });
      if (r1.ok) {
        await fetch('/api/smb/apply', { method: 'POST', headers: hdrs() });
        msg = 'Configuración guardada y aplicada';
        msgError = false;
      } else {
        const e = await r1.json().catch(() => ({}));
        msg = e.error || 'Error al guardar';
        msgError = true;
      }
    } catch { msg = 'Error de red'; msgError = true; }
    savingCfg = false;
  }

  // Exposición de carpeta (optimista · backend pendiente)
  async function toggleExpose(share) {
    share.smb = !share.smb;
    shares = shares;
    try {
      await fetch('/api/smb/share/' + encodeURIComponent(share.name), {
        method: 'PUT',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ smb: share.smb }),
      });
    } catch {}
  }

  async function setPassword() {
    if (!pwUser || !pwPass || pwBusy) return;
    pwBusy = true; msg = '';
    try {
      const r = await fetch('/api/smb/set-password', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: pwUser, password: pwPass }),
      });
      if (r.ok) { msg = `Contraseña SMB actualizada para ${pwUser}`; msgError = false; pwUser = ''; pwPass = ''; }
      else { const e = await r.json().catch(() => ({})); msg = e.error || 'Error'; msgError = true; }
    } catch { msg = 'Error de red'; msgError = true; }
    pwBusy = false;
  }

  onMount(load);
</script>

<div class="svc-pane">
  <!-- Barra de estado + toggle (el título lo pone el contenedor) -->
  <div class="sp-bar">
    <div class="sp-status">
      <span class="sp-led" class:on={status.running}></span>
      {#if status.loading}comprobando…
      {:else if !status.installed}Samba no instalado
      {:else}{status.running ? 'activo' : 'detenido'} · puerto {status.port || 445}{status.version ? ` · v${status.version}` : ''}{/if}
    </div>
    <button class="sp-toggle" class:on={status.running} disabled={busy || !status.installed} on:click={toggleService} title={status.running ? "Detener" : "Iniciar"}>
      <span class="sp-toggle-thumb"></span>
    </button>
  </div>

  {#if msg}<div class="sp-msg" class:error={msgError}>{msg}</div>{/if}

  <!-- Acceso LAN -->
  <div class="sp-lan">
    Acceso desde la red local: <b>\\{host || 'tu-nas'}</b> · puerto {status.port || 445}
  </div>

  <!-- Carpetas expuestas -->
  <div class="sp-sect">
    <div class="sp-st">Carpetas expuestas en la red</div>
    <div class="sp-hint">Elige qué carpetas compartidas son accesibles por SMB en tu red local.</div>
    <div class="sp-note">⚠ La aplicación efectiva en el servidor SMB está pendiente en el backend; el ajuste se guarda pero aún no regenera la config de Samba.</div>
    {#if shares.length === 0}
      <div class="sp-empty">No hay carpetas compartidas. Crea alguna en «Compartidas».</div>
    {:else}
      <div class="sp-folders">
        {#each shares as s (s.name)}
          <div class="sp-folder">
            <div class="sp-fic">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
              </svg>
            </div>
            <div class="sp-fid">
              <div class="sp-fnm">{s.displayName || s.name}</div>
              <div class="sp-fpath">{s.pool || '—'}</div>
            </div>
            <span class="sp-ftag" class:ro={s.readOnly}>{s.readOnly ? 'solo lectura' : 'lectura/escritura'}</span>
            <button class="sp-mini" class:on={s.smb} on:click={() => toggleExpose(s)} title={s.smb ? 'Dejar de exponer en SMB' : 'Exponer en SMB'}>
              <span class="sp-mini-thumb"></span>
            </button>
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Config servidor -->
  <div class="sp-sect">
    <div class="sp-st">Configuración del servidor</div>
    <div class="sp-grid2">
      <div class="sp-field">
        <label class="sp-lbl" for="smb-wg">Workgroup</label>
        <input id="smb-wg" class="sp-input" bind:value={config.workgroup} />
      </div>
      <div class="sp-field">
        <label class="sp-lbl" for="smb-name">Nombre visible</label>
        <input id="smb-name" class="sp-input" bind:value={config.serverString} />
      </div>
    </div>
    <div class="sp-actions">
      <button class="sp-btn primary" on:click={saveConfig} disabled={savingCfg}>
        {savingCfg ? 'Aplicando…' : 'Guardar y aplicar'}
      </button>
      <button class="sp-btn" on:click={restart} disabled={busy || !status.running}>Reiniciar servicio</button>
    </div>
  </div>

  <!-- Contraseña -->
  <div class="sp-sect">
    <div class="sp-st">Contraseña SMB de usuario</div>
    <div class="sp-actions">
      <input class="sp-input grow" type="text" placeholder="usuario" bind:value={pwUser} autocomplete="off" />
      <input class="sp-input grow" type="password" placeholder="nueva contraseña" bind:value={pwPass} autocomplete="new-password" />
      <button class="sp-btn primary" on:click={setPassword} disabled={pwBusy || !pwUser || !pwPass}>
        {pwBusy ? 'Guardando…' : 'Aplicar'}
      </button>
    </div>
    <div class="sp-hint">Samba usa contraseñas propias por usuario, separadas de la del sistema.</div>
  </div>
</div>

<style>
  .svc-pane { display: flex; flex-direction: column; gap: 18px; max-width: 860px; }

  /* Barra de estado */
  .sp-bar { display: flex; align-items: center; justify-content: space-between; }
  .sp-status { display: flex; align-items: center; gap: 10px; font-size: 12px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .sp-led { width: 9px; height: 9px; border-radius: 2.5px; background: var(--fg-5, #5a5a62); }
  .sp-led.on { background: var(--st-ok, #00ff9f); }
  .sp-toggle {
    width: 40px; height: 20px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    position: relative;
    cursor: pointer;
    padding: 0;
    flex-shrink: 0;
  }
  .sp-toggle-thumb {
    position: absolute; top: 2px; left: 2px;
    width: 14px; height: 14px;
    background: var(--fg-5, #5a5a62);
    border-radius: 3px;
    transition: left 0.15s, background 0.15s;
  }
  .sp-toggle.on { background: rgba(0,255,159,0.12); border-color: rgba(0,255,159,0.4); }
  .sp-toggle.on .sp-toggle-thumb { left: 22px; background: var(--nim-green, #00ff9f); }
  .sp-toggle:disabled { opacity: 0.5; cursor: not-allowed; }

  .sp-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .sp-msg.error { color: var(--st-crit, #ff5a5a); }

  .sp-lan {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    padding: 12px 14px;
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    font-family: var(--font-mono);
  }
  .sp-lan b { color: var(--st-info, #4db8ff); }

  .sp-sect { display: flex; flex-direction: column; gap: 10px; }
  .sp-st {
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    text-transform: uppercase;
    letter-spacing: 0.6px;
    font-family: var(--font-mono);
  }
  .sp-hint { font-size: 10px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono); line-height: 1.5; }
  .sp-note {
    font-size: 10px;
    color: var(--st-warn, #ffc857);
    font-family: var(--font-mono);
    line-height: 1.5;
    background: rgba(255,200,87,0.06);
    border: 1px solid rgba(255,200,87,0.2);
    border-radius: 6px;
    padding: 8px 12px;
  }
  .sp-empty { font-size: 11px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono); padding: 8px 0; }

  /* Carpetas */
  .sp-folders { display: flex; flex-direction: column; gap: 6px; }
  .sp-folder {
    display: flex; align-items: center; gap: 12px;
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 12px 14px;
  }
  .sp-fic {
    width: 30px; height: 30px;
    border-radius: 7px;
    background: rgba(0,255,159,0.08);
    border: 1px solid rgba(0,255,159,0.2);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--nim-green, #00ff9f);
  }
  .sp-fic svg { width: 15px; height: 15px; }
  .sp-fid { flex: 1; min-width: 0; }
  .sp-fnm { font-size: 13px; color: var(--fg, #f0f0f0); font-family: var(--font-mono); font-weight: 600; }
  .sp-fpath { font-size: 11px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono); margin-top: 2px; }
  .sp-ftag {
    font-size: 9px; font-family: var(--font-mono);
    padding: 2px 7px; border-radius: 3px;
    border: 1px solid var(--bd-2, #20202a);
    color: var(--fg-5, #5a5a62);
    flex-shrink: 0;
  }
  .sp-ftag.ro { color: var(--st-warn, #ffc857); border-color: rgba(255,200,87,0.3); }
  .sp-mini {
    width: 36px; height: 18px;
    background: var(--bg-window, #16161a);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 4px;
    position: relative;
    cursor: pointer;
    padding: 0;
    flex-shrink: 0;
  }
  .sp-mini-thumb {
    position: absolute; top: 2px; left: 2px;
    width: 12px; height: 12px;
    background: var(--fg-5, #5a5a62);
    border-radius: 2px;
    transition: left 0.15s, background 0.15s;
  }
  .sp-mini.on { background: rgba(0,255,159,0.12); border-color: rgba(0,255,159,0.4); }
  .sp-mini.on .sp-mini-thumb { left: 20px; background: var(--nim-green, #00ff9f); }

  /* Form */
  .sp-grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .sp-field { display: flex; flex-direction: column; gap: 5px; }
  .sp-lbl {
    font-size: 10px; color: var(--fg-4, #7a7a82);
    text-transform: uppercase; letter-spacing: 0.6px;
    font-family: var(--font-mono);
  }
  .sp-input {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 8px 11px;
    color: var(--fg, #f0f0f0);
    font-size: 12px;
    font-family: var(--font-mono);
    outline: none;
  }
  .sp-input:focus { border-color: rgba(0,255,159,0.35); }
  .sp-input.grow { flex: 1; min-width: 120px; }

  .sp-actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .sp-btn {
    padding: 8px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 11px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .sp-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .sp-btn.primary {
    background: var(--nim-green, #00ff9f);
    border-color: var(--nim-green, #00ff9f);
    color: var(--bg-window, #16161a);
    font-weight: 600;
  }
  .sp-btn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .sp-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
