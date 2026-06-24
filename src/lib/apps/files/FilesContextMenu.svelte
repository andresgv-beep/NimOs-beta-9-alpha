<script>
  /**
   * FilesContextMenu · menú contextual de la app Files
   * ─────────────────────────────────────────────────────
   * Stateless: recibe el estado del menú (posición + archivo) por props
   * y emite un evento `action` con el tipo de acción. El padre
   * (FileManager) despacha cada acción contra su lógica.
   *
   * Acciones: open · copy · cut · paste · download · zip · unzip ·
   *           rename · info · delete
   */
  import { createEventDispatcher } from 'svelte';
  import { isZipFile } from './filesStore.js';

  const dispatch = createEventDispatcher();

  export let menu = null;       // { x, y, file, idx } | null
  export let clipboard = null;

  const act = (type) => dispatch('action', { type, file: menu?.file });
</script>

{#if menu}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="ctx-menu" style="left:{menu.x}px;top:{menu.y}px" on:contextmenu|preventDefault>
    {#if menu.file}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('open')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        Abrir
      </div>
      <div class="ctx-sep"></div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('copy')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
        Copiar
      </div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('cut')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="6" cy="20" r="2"/><circle cx="6" cy="4" r="2"/><line x1="6" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="21" y2="21"/><line x1="6" y1="18" x2="21" y2="3"/></svg>
        Cortar
      </div>
      {#if clipboard}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="ctx-item" on:click={() => act('paste')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1"/></svg>
          Pegar
        </div>
      {/if}
      <div class="ctx-sep"></div>
      {#if !menu.file.isDirectory}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="ctx-item" on:click={() => act('download')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          Descargar
        </div>
      {/if}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('zip')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10z"/><path d="M9 9h1M12 9h1M9 12h1M12 12h1M9 15h1M12 15h1"/></svg>
        Comprimir (.zip)
      </div>
      {#if isZipFile(menu.file)}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="ctx-item" on:click={() => act('unzip')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10z"/><polyline points="9 11 12 14 15 11"/><line x1="12" y1="7" x2="12" y2="14"/></svg>
          Descomprimir
        </div>
      {/if}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('rename')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4z"/></svg>
        Renombrar
      </div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item" on:click={() => act('info')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
        Información
      </div>
      <div class="ctx-sep"></div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="ctx-item danger" on:click={() => act('delete')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6M14 11v6"/><path d="M9 6V4h6v2"/></svg>
        Eliminar
      </div>
    {:else}
      <!-- Click derecho en fondo vacío: solo pegar -->
      {#if clipboard}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="ctx-item" on:click={() => act('paste')}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1"/></svg>
          Pegar "{clipboard.file.name}"
        </div>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .ctx-menu {
    position: absolute;
    z-index: 500;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--line, rgba(255,255,255,0.08));
    border-radius: 8px;
    padding: 4px;
    min-width: 180px;
    box-shadow: 0 8px 32px rgba(0,0,0,0.5), 0 0 0 1px rgba(255,255,255,0.04);
    animation: ctxIn 0.12s ease both;
  }
  @keyframes ctxIn {
    from { opacity: 0; transform: scale(0.96) translateY(-4px); }
    to   { opacity: 1; transform: scale(1) translateY(0); }
  }
  .ctx-item {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 10px;
    border-radius: 5px;
    font-size: 12px;
    color: var(--ink-dim, #c8c8cf);
    cursor: pointer;
    transition: background 0.1s, color 0.1s;
  }
  .ctx-item svg { width: 13px; height: 13px; flex-shrink: 0; opacity: 0.7; }
  .ctx-item:hover {
    background: var(--side-active-bg, rgba(122,158,177,0.10));
    color: var(--ink, #f2f2f5);
  }
  .ctx-item:hover svg { opacity: 1; }
  .ctx-item.danger { color: var(--crit, #f87171); }
  .ctx-item.danger svg { color: var(--crit, #f87171); opacity: 0.8; }
  .ctx-item.danger:hover {
    background: rgba(248,113,113,0.10);
    color: var(--crit, #f87171);
  }
  .ctx-sep {
    height: 1px;
    background: var(--line, rgba(255,255,255,0.08));
    margin: 3px 4px;
  }
</style>
