<script>
  /**
   * FilesListView · vista lista densa de la app Files
   * ──────────────────────────────────────────────────────
   * Tabla con columnas Nombre · Tamaño · Tipo · Modificado y cabecera
   * ordenable (estilo gestor de archivos clásico, tema NimOS).
   *
   * Stateless: recibe datos por props y emite eventos al padre
   * (FileManager mantiene el estado: selección, clipboard, navegación).
   */
  import { createEventDispatcher } from 'svelte';
  import {
    fIconHtml, fmtSize, fDate, fType,
    SVG_FOLDER_SM_LOCAL, SVG_FOLDER_SM_REMOTE,
  } from './filesStore.js';

  const dispatch = createEventDispatcher();

  export let currentShare = null;
  export let localShares = [];
  export let remoteShares = [];
  export let files = [];          // ya ordenados (sorted) por defecto del padre
  export let selected = new Set();
  export let clipboard = null;
  export let loading = false;
  export let filePath = (f) => f.name;

  // Orden local de la tabla
  let sortKey = 'name';   // 'name' | 'size' | 'type' | 'date'
  let sortDir = 1;        // 1 asc, -1 desc

  function setSort(key) {
    if (sortKey === key) sortDir = -sortDir;
    else { sortKey = key; sortDir = 1; }
  }

  // Carpetas siempre primero; dentro, por la clave elegida
  $: rows = [...files].sort((a, b) => {
    const dirDiff = (a.isDirectory ? -1 : 1) - (b.isDirectory ? -1 : 1);
    if (dirDiff !== 0) return dirDiff;
    let r = 0;
    if (sortKey === 'name') r = a.name.localeCompare(b.name);
    else if (sortKey === 'size') r = (a.size || 0) - (b.size || 0);
    else if (sortKey === 'type') r = fType(a).localeCompare(fType(b));
    else if (sortKey === 'date') r = new Date(a.modified || 0) - new Date(b.modified || 0);
    return r * sortDir;
  });

  function arrow(key) {
    if (sortKey !== key) return '';
    return sortDir === 1 ? ' ↑' : ' ↓';
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="file-list"
  on:contextmenu={(e) => { if (!e.target.closest('.fl-row') && clipboard && currentShare) { e.preventDefault(); dispatch('bgcontext', e); } }}>

  {#if !currentShare}
    <!-- Lista de shares (sin cabecera de columnas) -->
    {#each [...localShares, ...remoteShares] as share}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="fl-row fl-share" on:dblclick={() => dispatch('navigate', { share: share.name, path: '/' })}>
        <span class="fl-icon">{@html share.remote ? SVG_FOLDER_SM_REMOTE : SVG_FOLDER_SM_LOCAL}</span>
        <span class="fl-name">{share.displayName || share.name}</span>
      </div>
    {/each}

  {:else if loading}
    <div class="f-loading"><div class="spinner"></div></div>

  {:else}
    <!-- Cabecera de columnas ordenable -->
    <div class="fl-head">
      <span class="fl-h-icon"></span>
      <button class="fl-h" class:active={sortKey === 'name'} on:click={() => setSort('name')}>Nombre{arrow('name')}</button>
      <button class="fl-h r" class:active={sortKey === 'size'} on:click={() => setSort('size')}>Tamaño{arrow('size')}</button>
      <button class="fl-h" class:active={sortKey === 'type'} on:click={() => setSort('type')}>Tipo{arrow('type')}</button>
      <button class="fl-h r" class:active={sortKey === 'date'} on:click={() => setSort('date')}>Modificado{arrow('date')}</button>
    </div>

    {#each rows as file, i}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="fl-row"
        class:sel={selected.has(i)}
        class:cut={clipboard?.op === 'cut' && clipboard?.path === filePath(file)}
        data-idx={i}
        on:click={(e) => dispatch('select', { i, e })}
        on:dblclick={() => dispatch('open', file)}
        on:contextmenu={(e) => dispatch('context', { e, file, i })}>
        <span class="fl-icon">{@html fIconHtml(file, true)}</span>
        <span class="fl-name">{file.name}</span>
        <span class="fl-size">{file.isDirectory ? '—' : fmtSize(file.size)}</span>
        <span class="fl-type">{fType(file)}</span>
        <span class="fl-date">{fDate(file.modified)}</span>
      </div>
    {/each}
    {#if rows.length === 0}<div class="f-empty">Carpeta vacía</div>{/if}
  {/if}
</div>

<style>
  .file-list {
    width: 100%;
    height: 100%;
    overflow-y: auto;
    padding: 0;
    display: flex;
    flex-direction: column;
  }

  /* Cabecera de columnas */
  .fl-head {
    display: grid;
    grid-template-columns: 24px 1fr 90px 110px 150px;
    gap: 10px;
    padding: 8px 14px;
    position: sticky;
    top: 0;
    background: var(--bg-window, #16161a);
    border-bottom: 1px solid var(--bd-2, #20202a);
    z-index: 1;
  }
  .fl-h-icon { width: 18px; }
  .fl-h {
    background: none;
    border: none;
    padding: 0;
    text-align: left;
    font-family: var(--font-mono, monospace);
    font-size: 9px;
    letter-spacing: 0.8px;
    text-transform: uppercase;
    color: var(--ink-dim, #6a6a72);
    cursor: pointer;
    transition: color 0.12s;
  }
  .fl-h:hover { color: var(--ink-mute, #9a9aa3); }
  .fl-h.active { color: var(--nim-green, #00ff9f); }
  .fl-h.r { text-align: right; }

  /* Filas densas en grid alineado */
  .fl-row {
    display: grid;
    grid-template-columns: 24px 1fr 90px 110px 150px;
    gap: 10px;
    align-items: center;
    padding: 6px 14px;
    cursor: pointer;
    border: 1px solid transparent;
    border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.06));
    transition: background 0.1s;
    font-size: 12px;
    color: var(--ink, #f2f2f5);
  }
  /* La lista de shares no usa columnas: vuelve a flex simple */
  .fl-row.fl-share {
    display: flex;
    align-items: center;
    gap: 8px;
    border-radius: 5px;
  }
  .fl-row:hover { background: rgba(255,255,255,0.03); }
  .fl-row.sel {
    background: var(--side-active-bg, rgba(122,158,177,0.10));
    border-color: var(--ui-select-border, rgba(122,158,177,0.35));
  }
  .fl-row.cut { opacity: 0.45; }
  .fl-icon {
    font-size: 21px;
    flex-shrink: 0;
    width: 26px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .fl-icon :global(svg) { width: 22px; height: 22px; display: block; }
  .fl-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fl-size {
    font-size: 10px;
    color: var(--ink-mute, #9a9aa3);
    font-family: var(--font-mono, monospace);
    text-align: right;
  }
  .fl-type {
    font-size: 10px;
    color: var(--ink-dim, #6a6a72);
    font-family: var(--font-mono, monospace);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fl-date {
    font-size: 10px;
    color: var(--ink-mute, #9a9aa3);
    font-family: var(--font-mono, monospace);
    text-align: right;
  }

  .f-empty {
    padding: 40px;
    text-align: center;
    color: var(--ink-dim, #6a6a72);
    font-family: var(--font-mono, monospace);
    font-size: 12px;
  }
  .f-loading { display: flex; align-items: center; justify-content: center; padding: 60px; }
  .spinner {
    width: 24px; height: 24px;
    border: 2px solid var(--bd-2, #20202a);
    border-top-color: var(--nim-green, #00ff9f);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
