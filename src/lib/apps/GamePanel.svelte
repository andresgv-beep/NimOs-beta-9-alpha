<script>
  /**
   * GamePanel.svelte — Panel de Juego (openMode "game").
   *
   * Se renderiza DENTRO de una ventana de NimOS (lo monta WindowFrame cuando
   * la ventana lleva gameData). Es el panel de administración de un servidor
   * de juego (Minecraft, etc.) que NO es una webapp: no se abre en navegador,
   * el cliente del juego conecta directo a host:puerto.
   *
   * Muestra:
   *   · Direcciones de conexión (local + externa), compuestas por el backend.
   *   · Puerto del juego + accesos a Ficheros y Estado.
   *   · Consola RCON interactiva (input + historial + comandos rápidos),
   *     conectada al servidor real vía el daemon.
   *
   * Datos: GET /api/apps/{id}/game-info   ·   RCON: POST /api/apps/{id}/rcon
   */
  import { onMount, tick } from 'svelte';
  import { getToken } from '$lib/stores/auth.js';
  import { openWindow } from '$lib/stores/windows.js';
  import AppShell from '$lib/components/AppShell.svelte';

  export let appId;
  export let appName = 'Servidor de juego';
  export let appIcon = '';

  let info = null;
  let loading = true;
  let loadError = '';
  let copied = '';

  // Consola RCON
  let rconInput = '';
  let rconHistory = []; // [{ cmd, response, error }]
  let rconBusy = false;
  let rconOutEl;

  const QUICK_CMDS = ['list', 'time set day', 'weather clear', 'save-all'];

  async function loadInfo() {
    loading = true;
    loadError = '';
    try {
      const res = await fetch(`/api/apps/${appId}/game-info`, {
        headers: { Authorization: `Bearer ${getToken()}` },
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      info = data?.data || data;
    } catch (err) {
      loadError = err?.message || String(err);
    } finally {
      loading = false;
    }
  }

  async function copyAddr(which, text) {
    try {
      await navigator.clipboard.writeText(text);
      copied = which;
      setTimeout(() => { if (copied === which) copied = ''; }, 1500);
    } catch {}
  }

  // Abre Files en la carpeta del server, vía el share que la contiene
  // (el backend lo resuelve en game-info). Si no hay share que la cubra,
  // info.files_share viene vacío y el botón está deshabilitado.
  function openFiles() {
    if (!info?.files_share) return;
    openWindow('files', { width: 1000, height: 640 }, {
      filesTarget: { share: info.files_share, path: info.files_rel_path || '/' },
    });
  }

  async function sendRcon(cmd) {
    const command = (cmd ?? rconInput).trim();
    if (!command || rconBusy) return;
    rconBusy = true;
    rconInput = '';
    // Añadir el comando al historial de inmediato (respuesta pendiente).
    const entry = { cmd: command, response: '', error: false, pending: true };
    rconHistory = [...rconHistory, entry];
    await scrollOut();
    try {
      const res = await fetch(`/api/apps/${appId}/rcon`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${getToken()}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ command }),
      });
      const data = await res.json().catch(() => ({}));
      const payload = data?.data || data;
      if (!res.ok) {
        entry.response = payload?.error || `Error HTTP ${res.status}`;
        entry.error = true;
      } else {
        entry.response = payload?.response ?? '(sin respuesta)';
      }
    } catch (err) {
      // Error de red real: el endpoint RCON existe en el daemon, así que esto
      // solo salta si no se pudo contactar con el servidor (no porque falte).
      entry.response = 'No se pudo contactar con el servidor. Revisa la conexión e inténtalo de nuevo.';
      entry.error = true;
    } finally {
      entry.pending = false;
      rconHistory = [...rconHistory]; // forzar reactividad
      rconBusy = false;
      await scrollOut();
    }
  }

  async function scrollOut() {
    await tick();
    if (rconOutEl) rconOutEl.scrollTop = rconOutEl.scrollHeight;
  }

  function onInputKey(e) {
    if (e.key === 'Enter') sendRcon();
  }

  onMount(loadInfo);
</script>

<AppShell
  appId={appId}
  title={appName}
  headerIcon="🎮"
  pathSegments={['juegos', appId]}
  showSidebar={false}
>
  <!-- Title bar estándar · mismo patrón que Files / Storage -->
  <svelte:fragment slot="page-header">
    <b>{appName}</b>
    <span class="ph-desc">· servidor de juego</span>
  </svelte:fragment>

  <div class="gp">

    <!-- Cabecera -->
    <header class="gp-head">
      <div class="gp-head-icon">
        {#if appIcon && appIcon.startsWith('http')}
          <img src={appIcon} alt="" />
        {:else}
          <i class="ti ti-device-gamepad-2" aria-hidden="true"></i>
        {/if}
      </div>
      <div class="gp-head-txt">
        <div class="gp-head-title">{appName}</div>
        <div class="gp-head-sub"><span class="gp-dot"></span> servidor de juego</div>
      </div>
    </header>

    {#if loading}
      <div class="gp-loading">Cargando información del servidor…</div>
    {:else if loadError}
      <div class="gp-error">No se pudo cargar la información: {loadError}</div>
    {:else if info}

      <!-- Direcciones -->
      <div class="gp-label"><i class="ti ti-broadcast" aria-hidden="true"></i> DIRECCIONES DE CONEXIÓN</div>

      {#if info.local_address}
        <div class="gp-addr gp-addr-local">
          <span class="gp-addr-tag local"><i class="ti ti-home" aria-hidden="true"></i> LOCAL</span>
          <code>{info.local_address}</code>
          <button class="gp-copy local" on:click={() => copyAddr('local', info.local_address)}>
            <i class="ti {copied === 'local' ? 'ti-check' : 'ti-copy'}" aria-hidden="true"></i>
            <span>{copied === 'local' ? 'Copiado' : 'Copiar'}</span>
          </button>
        </div>
      {/if}

      {#if info.external_address}
        <div class="gp-addr gp-addr-ext">
          <span class="gp-addr-tag ext"><i class="ti ti-world" aria-hidden="true"></i> EXTERNO</span>
          <code>{info.external_address}</code>
          <button class="gp-copy ext" on:click={() => copyAddr('external', info.external_address)}>
            <i class="ti {copied === 'external' ? 'ti-check' : 'ti-copy'}" aria-hidden="true"></i>
            <span>{copied === 'external' ? 'Copiado' : 'Copiar'}</span>
          </button>
        </div>
      {/if}

      <div class="gp-hint">
        <i class="ti ti-info-circle" aria-hidden="true"></i>
        Local para tu red · Externo necesita abrir el puerto en el router
      </div>

      <!-- Puerto / Ficheros / Estado -->
      <div class="gp-grid">
        <div class="gp-cell">
          <div class="gp-cell-lbl">PUERTO</div>
          <div class="gp-cell-val">{info.port}{info.protocol === 'udp' ? '/udp' : ''}</div>
        </div>
        <!-- Ficheros: abre Files en la carpeta del server, vía el share que la
             contiene. Si ningún share la cubre, se deshabilita solo (honesto). -->
        <button
          class="gp-cell gp-cell-btn"
          on:click={openFiles}
          disabled={!info.files_share}
          title={info.files_share
            ? 'Abrir la carpeta del servidor en Ficheros'
            : 'Ningún recurso compartido contiene la carpeta del servidor'}
        >
          <span class="gp-cell-ico"><i class="ti ti-folder" aria-hidden="true"></i></span>
          <span class="gp-cell-txt">Ficheros</span>
        </button>
        <button class="gp-cell gp-cell-btn" on:click={() => openWindow('nimhealth', { width: 1080, height: 680 })}>
          <span class="gp-cell-ico"><i class="ti ti-activity" aria-hidden="true"></i></span>
          <span class="gp-cell-txt">Estado</span>
        </button>
      </div>

      <!-- Consola RCON -->
      {#if info.rcon_enabled}
        <div class="gp-label gp-label-rcon"><i class="ti ti-terminal-2" aria-hidden="true"></i> CONSOLA RCON</div>
        <div class="gp-console">
          <div class="gp-out" bind:this={rconOutEl}>
            {#if rconHistory.length === 0}
              <div class="gp-out-empty">Escribe un comando para administrar el servidor.</div>
            {:else}
              {#each rconHistory as h}
                <div class="gp-cmd">&gt; {h.cmd}</div>
                {#if h.pending}
                  <div class="gp-resp gp-resp-pending">…</div>
                {:else}
                  <div class="gp-resp" class:gp-resp-err={h.error}>{h.response}</div>
                {/if}
              {/each}
            {/if}
          </div>
          <div class="gp-input-row">
            <span class="gp-prompt">&gt;</span>
            <input
              class="gp-input"
              type="text"
              bind:value={rconInput}
              on:keydown={onInputKey}
              placeholder="escribe un comando (list, day, gamemode...)"
              disabled={rconBusy}
            />
            <button class="gp-send" on:click={() => sendRcon()} disabled={rconBusy} aria-label="Enviar comando">
              <i class="ti ti-arrow-right" aria-hidden="true"></i>
            </button>
          </div>
        </div>

        <!-- Comandos rápidos -->
        <div class="gp-quick">
          {#each QUICK_CMDS as qc}
            <button class="gp-qbtn" on:click={() => sendRcon(qc)} disabled={rconBusy}>{qc}</button>
          {/each}
        </div>
      {/if}

    {/if}
  </div>
</AppShell>

<style>
  .gp {
    padding: 18px 22px 24px;
    width: 100%;
  }

  /* Cabecera */
  .gp-head {
    display: flex; align-items: center; gap: 12px;
    margin-bottom: 16px; padding-bottom: 14px;
    border-bottom: 1px solid var(--line, rgba(255,255,255,0.06));
  }
  .gp-head-icon {
    width: 42px; height: 42px; border-radius: 10px; flex-shrink: 0;
    background: var(--canvas, #16161c); border: 1px solid rgba(0,255,159,0.25);
    display: flex; align-items: center; justify-content: center;
    color: var(--signal, #00ff9f); font-size: 22px; overflow: hidden;
  }
  .gp-head-icon img { width: 100%; height: 100%; object-fit: cover; }
  .gp-head-txt { min-width: 0; }
  .gp-head-title { font-size: 16px; font-weight: 500; color: var(--ink, #f2f2f5); }
  .gp-head-sub { display: flex; align-items: center; gap: 6px; margin-top: 2px; font-size: 12px; color: var(--ink-mute, #9a9aa3); }
  .gp-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--signal, #00ff9f); flex-shrink: 0; }

  .gp-loading, .gp-error { color: var(--ink-mute, #9a9aa3); font-size: 13px; padding: 12px 0; }
  .gp-error { color: #ff8a8a; }

  .gp-label {
    font-size: 11px; letter-spacing: 0.08em; color: var(--ink-faint, #6a6a72);
    margin-bottom: 8px; display: flex; align-items: center; gap: 6px;
  }
  .gp-label-rcon { margin-top: 10px; }

  /* Direcciones */
  .gp-addr {
    display: flex; align-items: center; gap: 10px;
    background: var(--canvas, #16161c); border-radius: 8px;
    padding: 9px 12px; margin-bottom: 8px;
  }
  .gp-addr-local { border: 1px solid rgba(77,184,255,0.3); }
  .gp-addr-ext { border: 1px solid rgba(0,255,159,0.25); }
  .gp-addr-tag { display: flex; align-items: center; gap: 5px; font-size: 11px; min-width: 64px; flex-shrink: 0; }
  .gp-addr-tag.local { color: var(--info, #4db8ff); }
  .gp-addr-tag.ext { color: var(--signal, #00ff9f); }
  .gp-addr code {
    flex: 1; min-width: 0; font-family: var(--font-mono); font-size: 13.5px;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .gp-addr-local code { color: #9fd4ff; }
  .gp-addr-ext code { color: var(--signal, #00ff9f); }
  .gp-copy {
    display: flex; align-items: center; gap: 5px; flex-shrink: 0;
    border-radius: 6px; padding: 6px 11px; font-size: 12px; cursor: pointer;
    font-family: var(--font-sans); white-space: nowrap;
  }
  .gp-copy.local { background: #1a2c3d; border: 1px solid rgba(77,184,255,0.35); color: #9fd4ff; }
  .gp-copy.ext { background: #14361f; border: 1px solid rgba(0,255,159,0.35); color: var(--signal, #00ff9f); }
  .gp-copy:hover { filter: brightness(1.2); }

  .gp-hint {
    font-size: 10.5px; color: var(--ink-faint, #6a6a72);
    margin-bottom: 18px; padding-left: 2px; display: flex; align-items: center; gap: 5px;
  }

  /* Grid puerto/ficheros/estado */
  .gp-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
  .gp-cell {
    background: var(--canvas, #16161c); border: 1px solid rgba(255,255,255,0.06);
    border-radius: 8px; padding: 10px 12px; min-width: 0;
  }
  .gp-cell-lbl { font-size: 11px; color: var(--ink-faint, #6a6a72); margin-bottom: 3px; }
  .gp-cell-val { font-size: 15px; color: var(--ink, #f2f2f5); font-family: var(--font-mono); }
  .gp-cell-btn {
    color: var(--ink-dim, #c8c8cf); font-size: 12px; cursor: pointer;
    display: flex; flex-direction: column; align-items: flex-start; gap: 4px;
  }
  .gp-cell-btn:hover { border-color: rgba(0,255,159,0.25); }
  .gp-cell-btn:disabled { opacity: 0.45; cursor: not-allowed; }
  .gp-cell-btn:disabled:hover { border-color: rgba(255,255,255,0.06); }
  .gp-cell-ico { color: var(--info, #4db8ff); font-size: 18px; }

  /* Consola RCON */
  .gp-console {
    background: #0e0e13; border: 1px solid rgba(255,255,255,0.07);
    border-radius: 8px; overflow: hidden;
  }
  .gp-out {
    height: 170px; overflow-y: auto; padding: 12px;
    font-family: var(--font-mono); font-size: 12.5px; line-height: 1.7;
  }
  .gp-out-empty { color: var(--ink-faint, #6a6a72); }
  .gp-cmd { color: var(--signal, #00ff9f); }
  .gp-resp { color: var(--ink-dim, #c8c8cf); margin-bottom: 6px; white-space: pre-wrap; word-break: break-word; }
  .gp-resp-err { color: #ff8a8a; }
  .gp-resp-pending { color: var(--ink-faint, #6a6a72); }
  .gp-input-row {
    display: flex; align-items: center; gap: 8px;
    border-top: 1px solid rgba(255,255,255,0.07); padding: 8px 10px;
    background: var(--canvas, #16161c);
  }
  .gp-prompt { color: var(--signal, #00ff9f); font-family: var(--font-mono); font-size: 13px; flex-shrink: 0; }
  .gp-input {
    flex: 1; min-width: 0; background: transparent; border: none;
    color: var(--ink, #f2f2f5); font-family: var(--font-mono); font-size: 13px; outline: none;
  }
  .gp-input::placeholder { color: var(--ink-faint, #6a6a72); }
  .gp-send {
    flex-shrink: 0; background: #14361f; border: 1px solid rgba(0,255,159,0.35);
    color: var(--signal, #00ff9f); border-radius: 6px; padding: 6px 12px;
    font-size: 13px; cursor: pointer;
  }
  .gp-send:hover { filter: brightness(1.2); }
  .gp-send:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Comandos rápidos */
  .gp-quick { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 12px; }
  .gp-qbtn {
    background: var(--canvas, #16161c); border: 1px solid rgba(255,255,255,0.1);
    color: var(--ink-mute, #9a9aa3); border-radius: 6px; padding: 5px 10px;
    font-size: 11px; cursor: pointer; font-family: var(--font-mono);
  }
  .gp-qbtn:hover { border-color: rgba(0,255,159,0.3); color: var(--signal, #00ff9f); }
  .gp-qbtn:disabled { opacity: 0.5; cursor: not-allowed; }

  /* Responsive · ventana estrecha */
  @media (max-width: 580px) {
    .gp-grid { grid-template-columns: 1fr; }
    .gp-addr { flex-wrap: wrap; }
    .gp-addr code { flex-basis: 100%; order: 3; }
    .gp-copy span { display: none; }
  }
</style>
