<script>
  /**
   * GameModal.svelte — Panel de Juego (openMode "game").
   *
   * Fase 1: muestra las direcciones de conexión (local + externa), el puerto,
   * y accesos a ficheros/estado. La consola RCON (Fase 2/3) se añadirá después.
   *
   * El backend (/api/apps/{id}/game-info) compone las direcciones reales
   * (IP local del NAS, dominio DuckDNS) y el puerto del juego.
   */
  import { createEventDispatcher, onMount } from 'svelte';
  import { getToken } from '$lib/stores/auth.js';

  export let app; // { id, name, icon, ... } del Launcher

  const dispatch = createEventDispatcher();

  let info = null;     // GameInfo del backend
  let loading = true;
  let loadError = '';
  let copied = '';     // qué dirección se copió ("local"|"external")

  async function loadInfo() {
    loading = true;
    loadError = '';
    try {
      const res = await fetch(`/api/apps/${app.id}/game-info`, {
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

  function close() {
    dispatch('close');
  }

  onMount(loadInfo);
</script>

<div class="gm-backdrop" on:click|self={close} role="presentation">
  <div class="gm-modal" role="dialog" aria-label="Panel de juego de {app?.name || 'servidor'}">

    <header class="gm-head">
      <div class="gm-head-l">
        <div class="gm-icon">
          {#if app?.icon && app.icon.startsWith('http')}
            <img src={app.icon} alt="" />
          {:else}
            <i class="ti ti-device-gamepad-2" aria-hidden="true"></i>
          {/if}
        </div>
        <div>
          <div class="gm-title">{app?.name || 'Servidor de juego'}</div>
          <div class="gm-sub">
            <span class="gm-dot"></span>
            servidor de juego
          </div>
        </div>
      </div>
      <button class="gm-x" on:click={close} aria-label="Cerrar">
        <i class="ti ti-x" aria-hidden="true"></i>
      </button>
    </header>

    <div class="gm-body">
      {#if loading}
        <div class="gm-loading">Cargando información del servidor…</div>
      {:else if loadError}
        <div class="gm-error">No se pudo cargar la información: {loadError}</div>
      {:else if info}

        <div class="gm-label"><i class="ti ti-broadcast" aria-hidden="true"></i> DIRECCIONES DE CONEXIÓN</div>

        {#if info.local_address}
          <div class="gm-addr gm-addr-local">
            <span class="gm-addr-tag local"><i class="ti ti-home" aria-hidden="true"></i> LOCAL</span>
            <code>{info.local_address}</code>
            <button class="gm-copy local" on:click={() => copyAddr('local', info.local_address)} aria-label="Copiar dirección local">
              <i class="ti {copied === 'local' ? 'ti-check' : 'ti-copy'}" aria-hidden="true"></i>
            </button>
          </div>
        {/if}

        {#if info.external_address}
          <div class="gm-addr gm-addr-ext">
            <span class="gm-addr-tag ext"><i class="ti ti-world" aria-hidden="true"></i> EXTERNO</span>
            <code>{info.external_address}</code>
            <button class="gm-copy ext" on:click={() => copyAddr('external', info.external_address)} aria-label="Copiar dirección externa">
              <i class="ti {copied === 'external' ? 'ti-check' : 'ti-copy'}" aria-hidden="true"></i>
            </button>
          </div>
        {/if}

        <div class="gm-hint">
          <i class="ti ti-info-circle" aria-hidden="true"></i>
          Local para tu red · Externo necesita abrir el puerto en el router
        </div>

        <div class="gm-grid">
          <div class="gm-cell">
            <div class="gm-cell-lbl">PUERTO</div>
            <div class="gm-cell-val">{info.port}{info.protocol === 'udp' ? '/udp' : ''}</div>
          </div>
          <button class="gm-cell gm-cell-btn" on:click={() => dispatch('files', { path: info.files_path })}>
            <span class="gm-cell-ico"><i class="ti ti-folder" aria-hidden="true"></i></span>
            Ficheros
          </button>
          <button class="gm-cell gm-cell-btn" on:click={() => dispatch('status', { id: app.id })}>
            <span class="gm-cell-ico"><i class="ti ti-activity" aria-hidden="true"></i></span>
            Estado
          </button>
        </div>

        {#if info.rcon_enabled}
          <div class="gm-rcon-soon">
            <i class="ti ti-terminal-2" aria-hidden="true"></i>
            Consola RCON · próximamente
          </div>
        {/if}

      {/if}
    </div>
  </div>
</div>

<style>
  .gm-backdrop {
    position: fixed; inset: 0; z-index: 1000;
    background: rgba(0,0,0,0.55);
    display: flex; align-items: center; justify-content: center;
    padding: 1rem;
  }
  .gm-modal {
    width: 560px; max-width: 100%;
    background: var(--panel, #212128);
    border: 1px solid rgba(0,255,159,0.22);
    border-radius: 12px; overflow: hidden;
    font-family: var(--font-sans);
  }
  .gm-head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 16px 20px; border-bottom: 1px solid rgba(255,255,255,0.06);
  }
  .gm-head-l { display: flex; align-items: center; gap: 12px; }
  .gm-icon {
    width: 38px; height: 38px; border-radius: 9px;
    background: var(--canvas, #16161c); border: 1px solid rgba(0,255,159,0.25);
    display: flex; align-items: center; justify-content: center;
    color: var(--signal, #00ff9f); font-size: 20px; overflow: hidden;
  }
  .gm-icon img { width: 100%; height: 100%; object-fit: cover; }
  .gm-title { font-size: 16px; font-weight: 500; color: var(--ink, #f2f2f5); }
  .gm-sub { display: flex; align-items: center; gap: 6px; margin-top: 2px; font-size: 12px; color: var(--ink-mute, #9a9aa3); }
  .gm-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--signal, #00ff9f); }
  .gm-x { background: transparent; border: none; color: var(--ink-mute, #9a9aa3); font-size: 20px; cursor: pointer; padding: 4px; }
  .gm-body { padding: 18px 20px; }
  .gm-loading, .gm-error { color: var(--ink-mute, #9a9aa3); font-size: 13px; padding: 12px 0; }
  .gm-error { color: #ff8a8a; }
  .gm-label {
    font-size: 11px; letter-spacing: 0.08em; color: var(--ink-faint, #6a6a72);
    margin-bottom: 8px; display: flex; align-items: center; gap: 6px;
  }
  .gm-addr {
    display: flex; align-items: center; gap: 8px;
    background: var(--canvas, #16161c); border-radius: 8px;
    padding: 9px 12px; margin-bottom: 8px;
  }
  .gm-addr-local { border: 1px solid rgba(77,184,255,0.3); }
  .gm-addr-ext { border: 1px solid rgba(0,255,159,0.25); }
  .gm-addr-tag { display: flex; align-items: center; gap: 5px; font-size: 11px; min-width: 54px; }
  .gm-addr-tag.local { color: var(--info, #4db8ff); }
  .gm-addr-tag.ext { color: var(--signal, #00ff9f); }
  .gm-addr code { flex: 1; font-family: var(--font-mono); font-size: 13.5px; }
  .gm-addr-local code { color: #9fd4ff; }
  .gm-addr-ext code { color: var(--signal, #00ff9f); }
  .gm-copy { border-radius: 6px; padding: 5px 9px; font-size: 12px; cursor: pointer; }
  .gm-copy.local { background: #1a2c3d; border: 1px solid rgba(77,184,255,0.35); color: #9fd4ff; }
  .gm-copy.ext { background: #14361f; border: 1px solid rgba(0,255,159,0.35); color: var(--signal, #00ff9f); }
  .gm-hint {
    font-size: 10.5px; color: var(--ink-faint, #6a6a72);
    margin-bottom: 18px; padding-left: 2px; display: flex; align-items: center; gap: 5px;
  }
  .gm-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
  .gm-cell {
    background: var(--canvas, #16161c); border: 1px solid rgba(255,255,255,0.06);
    border-radius: 8px; padding: 10px 12px;
  }
  .gm-cell-lbl { font-size: 11px; color: var(--ink-faint, #6a6a72); margin-bottom: 3px; }
  .gm-cell-val { font-size: 15px; color: var(--ink, #f2f2f5); font-family: var(--font-mono); }
  .gm-cell-btn {
    color: var(--ink-dim, #c8c8cf); font-size: 12px; cursor: pointer;
    display: flex; flex-direction: column; align-items: flex-start; gap: 3px;
  }
  .gm-cell-ico { color: var(--info, #4db8ff); font-size: 16px; }
  .gm-rcon-soon {
    margin-top: 16px; padding: 10px 12px;
    background: var(--canvas, #16161c); border: 1px dashed rgba(0,255,159,0.25);
    border-radius: 8px; font-size: 12px; color: var(--ink-mute, #9a9aa3);
    display: flex; align-items: center; gap: 6px;
  }
</style>
