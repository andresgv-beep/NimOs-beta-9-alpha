<script>
  /**
   * FilesGridView · vista de iconos (grid) de la app Files
   * ─────────────────────────────────────────────────────────
   * Stateless: recibe datos por props y emite eventos al padre.
   * Extraído de FileManager sin cambios de comportamiento.
   */
  import { createEventDispatcher } from 'svelte';
  import {
    fIconHtml, fDate,
    SVG_FOLDER_LOCAL, SVG_FOLDER_REMOTE,
  } from './filesStore.js';

  const dispatch = createEventDispatcher();

  export let currentShare = null;
  export let localShares = [];
  export let remoteShares = [];
  export let files = [];          // ya ordenados (sorted)
  export let selected = new Set();
  export let clipboard = null;
  export let loading = false;
  export let filePath = (f) => f.name;
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="file-grid"
  on:contextmenu={(e) => { if (!e.target.closest('.f-item') && clipboard && currentShare) { e.preventDefault(); dispatch('bgcontext', e); } }}>
  {#if !currentShare}
    {#each localShares as share}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="f-item" on:dblclick={() => dispatch('navigate', { share: share.name, path: '/' })}>
        <div class="f-icon">{@html SVG_FOLDER_LOCAL}</div>
        <div class="f-name">{share.displayName || share.name}</div>
      </div>
    {/each}
    {#each remoteShares as share}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="f-item" on:dblclick={() => dispatch('navigate', { share: share.name, path: '/' })}>
        <div class="f-icon">{@html SVG_FOLDER_REMOTE}</div>
        <div class="f-name">{share.displayName || share.name}</div>
      </div>
    {/each}
    {#if localShares.length === 0 && remoteShares.length === 0}
      <div class="f-empty f-empty-root">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
        </svg>
        <b>No hay carpetas compartidas</b>
        <span>Créala desde el Panel de Control para empezar.</span>
      </div>
    {/if}
  {:else if loading}
    <div class="f-loading"><div class="spinner"></div></div>
  {:else}
    {#each files as file, i}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="f-item"
        class:sel={selected.has(i)}
        class:cut={clipboard?.op === 'cut' && clipboard?.path === filePath(file)}
        data-idx={i}
        on:click={(e) => dispatch('select', { i, e })}
        on:dblclick={() => dispatch('open', file)}
        on:contextmenu={(e) => dispatch('context', { e, file, i })}>
        <div class="f-icon">{@html fIconHtml(file)}</div>
        <div class="f-name">{file.name}</div>
        <div class="f-date">{fDate(file.modified)}</div>
      </div>
    {/each}
    {#if files.length === 0}<div class="f-empty">Carpeta vacía</div>{/if}
  {/if}
</div>

<style>
  .file-grid {
    width: 100%;
    height: 100%;
    overflow-y: auto;
    padding: 18px 20px;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
    gap: 8px;
    align-content: start;
  }
  .f-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 11px 6px 8px;
    border-radius: 6px;
    cursor: pointer;
    border: 1px solid transparent;
    transition: background 0.12s, border-color 0.12s;
  }
  .f-item:hover { background: rgba(255,255,255,0.04); }
  .f-item.sel {
    background: var(--side-active-bg, rgba(122,158,177,0.10));
    border-color: var(--ui-select-border, rgba(122,158,177,0.35));
  }
  .f-item.cut { opacity: 0.45; }
  .f-icon {
    font-size: 48px;
    line-height: 1;
    transition: transform 0.15s;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 52px;
    height: 52px;
  }
  .f-item:hover .f-icon { transform: scale(1.07) translateY(-2px); }
  .f-icon :global(svg) { width: 100%; height: 100%; display: block; }
  .f-name {
    font-size: 13px;
    color: var(--ink, #f2f2f5);
    text-align: center;
    line-height: 1.3;
    max-width: 88px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .f-date {
    font-size: 11px;
    color: var(--ink-mute, #9a9aa3);
  }
  .f-empty {
    grid-column: 1 / -1;
    text-align: center;
    padding: 40px;
    color: var(--ink-mute, #9a9aa3);
    font-size: 12px;
  }
  .f-empty-root {
    min-height: 220px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 7px;
  }
  .f-empty-root svg { width: 34px; height: 34px; color: var(--ink-faint); margin-bottom: 4px; }
  .f-empty-root b { color: var(--ink-dim); font-size: 13px; font-weight: 600; }
  .f-empty-root span { color: var(--ink-mute); font-size: 11px; }
  .f-loading {
    grid-column: 1 / -1;
    display: flex;
    justify-content: center;
    padding: 40px;
  }
  .spinner {
    width: 20px; height: 20px;
    border-radius: 50%;
    border: 2px solid rgba(255,255,255,0.08);
    border-top-color: var(--signal, #5b8ff9);
    animation: spin 0.7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
