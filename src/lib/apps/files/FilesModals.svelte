<script>
  /**
   * FilesModals · modales de la app Files (rename · info · newFolder)
   * ──────────────────────────────────────────────────────────────────
   * Los estados se enlazan con bind: desde el padre (FileManager); los
   * inputs escriben directamente en esos objetos. Emite eventos para
   * confirmar (rename/create) y para cerrar.
   */
  import { createEventDispatcher } from 'svelte';
  import { fIcon, fExt, fmtSize, fDate } from './filesStore.js';

  const dispatch = createEventDispatcher();

  export let renameModal = null;     // { file, newName } | null
  export let infoModal = null;       // file | null
  export let newFolderModal = null;  // { name } | null
  export let currentShare = null;
  export let filePath = (f) => f.name;

  const closeRename = () => (renameModal = null);
  const closeInfo = () => (infoModal = null);
  const closeNew = () => (newFolderModal = null);
</script>

{#if renameModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="modal-overlay" on:click|self={closeRename}></div>
  <div class="modal">
    <div class="modal-header">
      <div class="modal-title">Renombrar</div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="modal-close" on:click={closeRename}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </div>
    </div>
    <div class="modal-body">
      <div class="form-field">
        <label class="form-label" for="rename-input">Nuevo nombre</label>
        <input id="rename-input" class="form-input" type="text" bind:value={renameModal.newName}
          on:keydown={(e) => { if (e.key === 'Enter') dispatch('rename'); }} />
      </div>
    </div>
    <div class="modal-footer">
      <button class="btn-secondary" on:click={closeRename}>Cancelar</button>
      <button class="btn-accent" on:click={() => dispatch('rename')}>Renombrar</button>
    </div>
  </div>
{/if}

{#if infoModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="modal-overlay" on:click|self={closeInfo}></div>
  <div class="modal">
    <div class="modal-header">
      <div class="modal-title">Información</div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="modal-close" on:click={closeInfo}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </div>
    </div>
    <div class="modal-body">
      <div class="info-icon">{fIcon(infoModal)}</div>
      <div class="info-rows">
        <div class="info-row"><span>Nombre</span><span>{infoModal.name}</span></div>
        <div class="info-row"><span>Tipo</span><span>{infoModal.isDirectory ? 'Carpeta' : fExt(infoModal.name)}</span></div>
        {#if !infoModal.isDirectory}
          <div class="info-row"><span>Tamaño</span><span>{fmtSize(infoModal.size)}</span></div>
        {/if}
        <div class="info-row"><span>Modificado</span><span>{fDate(infoModal.modified)}</span></div>
        <div class="info-row"><span>Ruta</span><span>{currentShare}{filePath(infoModal)}</span></div>
      </div>
    </div>
    <div class="modal-footer">
      <button class="btn-accent" on:click={closeInfo}>Cerrar</button>
    </div>
  </div>
{/if}

{#if newFolderModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="modal-overlay" on:click|self={closeNew}></div>
  <div class="modal">
    <div class="modal-header">
      <div class="modal-title">Nueva carpeta</div>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="modal-close" on:click={closeNew}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </div>
    </div>
    <div class="modal-body">
      <div class="form-field">
        <label class="form-label" for="newfolder-input">Nombre de la carpeta</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="newfolder-input" class="form-input" type="text" bind:value={newFolderModal.name} autofocus
          on:keydown={(e) => { if (e.key === 'Enter') dispatch('create'); if (e.key === 'Escape') closeNew(); }} />
      </div>
    </div>
    <div class="modal-footer">
      <button class="btn-secondary" on:click={closeNew}>Cancelar</button>
      <button class="btn-accent" on:click={() => dispatch('create')}>Crear</button>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.5);
    z-index: 1000;
  }
  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 1001;
    width: 380px;
    max-width: calc(100vw - 40px);
    background: var(--panel, #15151a);
    border: 1px solid var(--line-bright, #2a2a32);
    border-radius: 10px;
    overflow: hidden;
    box-shadow: 0 16px 50px rgba(0,0,0,0.6);
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 16px;
    border-bottom: 1px solid var(--line, rgba(255,255,255,0.06));
  }
  .modal-title { font-size: 13px; font-weight: 600; color: var(--ink, #f2f2f5); }
  .modal-close {
    width: 26px; height: 26px;
    display: flex; align-items: center; justify-content: center;
    border-radius: 5px; cursor: pointer; color: var(--ink-mute, #9a9aa3);
  }
  .modal-close:hover { background: rgba(255,255,255,0.06); color: var(--ink, #f2f2f5); }
  .modal-close svg { width: 15px; height: 15px; }
  .modal-body { padding: 16px; }
  .modal-footer {
    display: flex; justify-content: flex-end; gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--line, rgba(255,255,255,0.06));
  }
  .form-field { display: flex; flex-direction: column; gap: 6px; }
  .form-label {
    font-family: var(--font-mono, monospace);
    font-size: 10px; letter-spacing: 0.8px; text-transform: uppercase;
    color: var(--ink-mute, #9a9aa3);
  }
  .form-input {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--line-bright, #2a2a32);
    border-radius: 6px;
    padding: 9px 12px;
    color: var(--ink, #f2f2f5);
    font-size: 13px;
    outline: none;
  }
  .form-input:focus { border-color: var(--signal, #00ff9f); }
  .info-icon { font-size: 40px; text-align: center; margin-bottom: 14px; }
  .info-rows { display: flex; flex-direction: column; gap: 8px; }
  .info-row {
    display: flex; justify-content: space-between; gap: 16px;
    font-size: 12px;
  }
  .info-row span:first-child {
    color: var(--ink-mute, #9a9aa3);
    font-family: var(--font-mono, monospace);
    font-size: 10px; text-transform: uppercase; letter-spacing: 0.5px;
  }
  .info-row span:last-child {
    color: var(--ink, #f2f2f5);
    text-align: right;
    word-break: break-all;
  }
  .btn-secondary {
    padding: 8px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--line-bright, #2a2a32);
    border-radius: 6px;
    color: var(--ink-dim, #c8c8cf);
    font-size: 12px;
    cursor: pointer;
  }
  .btn-secondary:hover { color: var(--ink, #f2f2f5); }
  .btn-accent {
    padding: 8px 14px;
    background: var(--signal, #00ff9f);
    border: 1px solid var(--signal, #00ff9f);
    border-radius: 6px;
    color: var(--bg-window, #16161a);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-accent:hover { filter: brightness(1.08); }
</style>
