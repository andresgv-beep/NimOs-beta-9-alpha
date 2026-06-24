<script>
  /**
   * WebApp · Wrapper para apps Docker embebidas vía iframe
   * ────────────────────────────────────────────────────────
   * Carga iframe hacia puerto del contenedor Docker con el token de sesión.
   *
   * Props:
   *   - appId: string (id de la app en APP_META)
   *   - port: número (puerto donde corre el contenedor)
   *   - name: string (nombre para el titlebar)
   */
  import { getContext, onMount } from 'svelte';
  import { getToken } from '$lib/stores/auth.js';
  import IconButton from '$lib/ui/IconButton.svelte';

  export let appId = '';
  export let port = 0;
  export let name = '';
  export let externalUrl = ''; // SHIELD-P2 · URL Caddy cuando el puerto directo está cerrado

  const wc = getContext('windowControls');

  let iframeSrc = '';
  let loaded = false;

  onMount(() => {
    const host = window.location.hostname;
    const proto = window.location.protocol;
    // Token is also set as cookie for iframe sub-requests.
    iframeSrc = `${proto}//${host}:${port}`;
  });
</script>

<div class="webapp">
  <!-- Titlebar propia simplificada (el iframe ocupa todo el contenido) -->
  <div class="wa-titlebar">
    <span class="wa-tag">DOCKER</span>
    <span class="wa-name">{name || appId}</span>
    <span class="wa-port">:{port}</span>
    <span class="wa-spacer"></span>
    <IconButton size="sm" title="Recargar" onClick={() => { const el = document.querySelector(`iframe[data-appid="${appId}"]`); if (el) el.src = el.src; }}>↻</IconButton>
    <IconButton size="sm" title="Abrir en pestaña nueva" onClick={() => window.open(iframeSrc, '_blank')}>↗</IconButton>
    {#if wc}
      <button class="wa-close" on:click={wc.close} title="Cerrar">×</button>
    {/if}
  </div>

  <!-- Iframe -->
  {#if iframeSrc}
    <iframe
      data-appid={appId}
      src={iframeSrc}
      on:load={() => loaded = true}
      title={name || appId}
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads"
    ></iframe>
  {/if}

  {#if !loaded}
    <div class="wa-loading">
      <span>conectando a {name}:{port}...</span>
    </div>
  {/if}
</div>

<style>
  .webapp {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    position: relative;
    font-family: var(--font-mono);
  }
  .wa-titlebar {
    height: var(--titlebar-height);
    background: var(--bg-1);
    border-bottom: 1px solid var(--border);
    display: flex;
    align-items: center;
    padding: 0 4px 0 14px;
    gap: 10px;
    font-size: 11px;
    flex-shrink: 0;
  }
  .wa-tag {
    background: var(--info);
    color: var(--bg);
    padding: 1px 7px;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 1.5px;
  }
  .wa-name {
    color: var(--fg);
    font-weight: 500;
    letter-spacing: 0.5px;
  }
  .wa-port {
    color: var(--fg-mute);
    font-size: 10px;
  }
  .wa-spacer { flex: 1; }
  .wa-close {
    width: 34px; height: 22px;
    background: var(--bg-1);
    color: var(--fg-dim);
    border: 1px solid var(--border-bright);
    cursor: pointer;
    font-size: 13px;
    margin-left: 4px;
    transition: all 0.1s;
    clip-path: polygon(0 0, calc(100% - 6px) 0, 100% 6px, 100% 100%, 6px 100%, 0 calc(100% - 6px));
  }
  .wa-close:hover {
    color: var(--bg);
    background: var(--crit);
    border-color: var(--crit);
  }
  iframe {
    flex: 1;
    width: 100%;
    border: none;
    background: #fff;
  }
  .wa-loading {
    position: absolute;
    inset: var(--titlebar-height) 0 0 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--fg-mute);
    font-size: 11px;
    letter-spacing: 1.5px;
    background: var(--bg);
  }
</style>
