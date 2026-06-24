<script>
  /**
   * FilesRecycleBin · vista de papelera dentro de la app Files
   * ───────────────────────────────────────────────────────────
   * Lista los archivos borrados de un share (que tenga papelera activada),
   * con selección múltiple y acciones: restaurar, eliminar definitivo, vaciar.
   *
   * API:
   *   GET  /api/files/recyclebin/list?share=X
   *   POST /api/files/recyclebin/restore  {share, ids:[...]}
   *   POST /api/files/recyclebin/delete   {share, ids:[...]}
   *   POST /api/files/recyclebin/empty    {share}
   */
  import { createEventDispatcher, onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  export let share;   // nombre del share

  const dispatch = createEventDispatcher();

  let items = [];
  let totalBytes = 0;
  let loading = true;
  let errorMsg = '';
  let selected = new Set();
  let busy = false;
  let confirmAction = null; // 'empty' | 'delete' | null

  $: selectedCount = selected.size;
  $: allSelected = items.length > 0 && selected.size === items.length;

  onMount(load);

  async function load() {
    loading = true;
    errorMsg = '';
    selected = new Set();
    try {
      const r = await fetch(`/api/files/recyclebin/list?share=${encodeURIComponent(share)}`, { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        items = d.items || [];
        totalBytes = d.totalBytes || 0;
      } else {
        errorMsg = 'No se pudo cargar la papelera';
      }
    } catch {
      errorMsg = 'Error de red';
    }
    loading = false;
  }

  function toggleSel(id) {
    const s = new Set(selected);
    if (s.has(id)) s.delete(id); else s.add(id);
    selected = s;
  }
  function toggleAll() {
    if (allSelected) selected = new Set();
    else selected = new Set(items.map((i) => i.id));
  }

  async function restoreSelected() {
    if (selected.size === 0 || busy) return;
    busy = true;
    try {
      const r = await fetch('/api/files/recyclebin/restore', {
        method: 'POST', headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ share, ids: [...selected] }),
      });
      if (r.ok) {
        await load();
        dispatch('changed'); // avisar a Files por si cambió el contenido
      } else errorMsg = 'No se pudo restaurar';
    } catch { errorMsg = 'Error de red'; }
    busy = false;
  }

  async function deleteSelected() {
    if (selected.size === 0 || busy) return;
    busy = true;
    confirmAction = null;
    try {
      const r = await fetch('/api/files/recyclebin/delete', {
        method: 'POST', headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ share, ids: [...selected] }),
      });
      if (r.ok) await load();
      else errorMsg = 'No se pudo eliminar';
    } catch { errorMsg = 'Error de red'; }
    busy = false;
  }

  async function emptyAll() {
    if (busy) return;
    busy = true;
    confirmAction = null;
    try {
      const r = await fetch('/api/files/recyclebin/empty', {
        method: 'POST', headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ share }),
      });
      if (r.ok) await load();
      else errorMsg = 'No se pudo vaciar';
    } catch { errorMsg = 'Error de red'; }
    busy = false;
  }

  function fmtBytes(b) {
    b = Number(b) || 0;
    if (!b) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, n = b;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }
  function fmtDate(iso) {
    if (!iso) return '';
    try { return new Date(iso).toLocaleString(); } catch { return iso; }
  }
</script>

<div class="rb">
  <div class="rb-head">
    <button class="rb-back" on:click={() => dispatch('close')} title="Volver a archivos">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
      Volver
    </button>
    <div class="rb-title">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>
      Papelera · {share}
    </div>
    <div class="rb-meta">{items.length} {items.length === 1 ? 'elemento' : 'elementos'} · {fmtBytes(totalBytes)}</div>
  </div>

  {#if errorMsg}
    <div class="rb-error">{errorMsg}</div>
  {/if}

  <!-- Barra de acciones -->
  {#if items.length > 0}
    <div class="rb-actions">
      <label class="rb-selall">
        <input type="checkbox" checked={allSelected} on:change={toggleAll} />
        {selectedCount > 0 ? `${selectedCount} seleccionados` : 'Seleccionar todo'}
      </label>
      <div class="rb-spacer"></div>
      <button class="rb-btn" disabled={selectedCount === 0 || busy} on:click={restoreSelected}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9 9 0 0 0-6.4 2.6L3 8"/><polyline points="3 3 3 8 8 8"/></svg>
        Restaurar
      </button>
      <button class="rb-btn danger" disabled={selectedCount === 0 || busy} on:click={() => confirmAction = 'delete'}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
        Eliminar
      </button>
      <button class="rb-btn danger-outline" disabled={busy} on:click={() => confirmAction = 'empty'}>
        Vaciar papelera
      </button>
    </div>
  {/if}

  <!-- Lista -->
  {#if loading}
    <div class="rb-empty">Cargando…</div>
  {:else if items.length === 0}
    <div class="rb-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" style="width:40px;height:40px;opacity:0.3"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
      <div>La papelera está vacía</div>
    </div>
  {:else}
    <div class="rb-list">
      {#each items as it (it.id)}
        <div class="rb-row" class:sel={selected.has(it.id)}>
          <input type="checkbox" checked={selected.has(it.id)} on:change={() => toggleSel(it.id)} />
          <div class="rb-icon" class:dir={it.isDir}>
            {#if it.isDir}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
            {:else}
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
            {/if}
          </div>
          <div class="rb-info">
            <div class="rb-name">{it.name}</div>
            <div class="rb-orig">desde /{it.original}</div>
          </div>
          <div class="rb-size">{fmtBytes(it.sizeBytes)}</div>
          <div class="rb-date">{fmtDate(it.deletedAt)}</div>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Confirmaciones -->
  {#if confirmAction}
    <div class="rb-confirm-backdrop" on:click={() => confirmAction = null}>
      <div class="rb-confirm" on:click|stopPropagation>
        <div class="rb-confirm-title">
          {confirmAction === 'empty' ? 'Vaciar la papelera' : `Eliminar ${selectedCount} elemento${selectedCount === 1 ? '' : 's'}`}
        </div>
        <div class="rb-confirm-msg">
          {confirmAction === 'empty'
            ? `Se eliminarán definitivamente todos los elementos (${fmtBytes(totalBytes)}). Esta acción no se puede deshacer.`
            : 'Se eliminarán definitivamente. Esta acción no se puede deshacer.'}
        </div>
        <div class="rb-confirm-actions">
          <button class="rb-btn" on:click={() => confirmAction = null}>Cancelar</button>
          <button class="rb-btn danger" on:click={confirmAction === 'empty' ? emptyAll : deleteSelected}>
            {confirmAction === 'empty' ? 'Vaciar' : 'Eliminar'}
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .rb { display: flex; flex-direction: column; height: 100%; }
  .rb-head { display: flex; align-items: center; gap: 14px; padding: 4px 0 14px; flex-wrap: wrap; }
  .rb-back {
    display: flex; align-items: center; gap: 5px;
    background: transparent; border: none; cursor: pointer;
    color: var(--fg-3, #9c9ca4); font-size: 12px; font-family: inherit; padding: 4px 6px;
  }
  .rb-back:hover { color: var(--fg, #f0f0f0); }
  .rb-back svg { width: 14px; height: 14px; }
  .rb-title {
    display: flex; align-items: center; gap: 8px;
    font-size: 15px; font-weight: 600; color: var(--fg, #f0f0f0);
  }
  .rb-title svg { width: 17px; height: 17px; color: var(--fg-3, #9c9ca4); }
  .rb-meta { font-size: 11px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono, ui-monospace, monospace); margin-left: auto; }

  .rb-error {
    background: rgba(255,90,90,0.10); border: 1px solid rgba(255,90,90,0.3);
    color: var(--st-crit, #ff5a5a); border-radius: 6px; padding: 8px 12px;
    font-size: 12px; margin-bottom: 12px;
  }

  .rb-actions {
    display: flex; align-items: center; gap: 8px;
    padding: 10px 12px; margin-bottom: 8px;
    background: var(--bg-card, #15151a); border-radius: 8px;
  }
  .rb-selall { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--fg-3, #9c9ca4); cursor: pointer; }
  .rb-spacer { flex: 1; }
  .rb-btn {
    display: flex; align-items: center; gap: 6px;
    padding: 7px 13px; border-radius: 6px;
    border: 1px solid var(--bd-2, #20202a); background: var(--bg-inner, #101015);
    color: var(--fg-2, #d0d0d4); font-size: 12px; font-family: inherit; cursor: pointer;
    transition: border-color 0.12s, color 0.12s;
  }
  .rb-btn svg { width: 13px; height: 13px; }
  .rb-btn:hover:not(:disabled) { border-color: var(--bd-3, #2a2a32); }
  .rb-btn:disabled { opacity: 0.4; cursor: default; }
  .rb-btn.danger:hover:not(:disabled) { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.3); }
  .rb-btn.danger-outline { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.25); }
  .rb-btn.danger-outline:hover:not(:disabled) { background: rgba(255,90,90,0.08); }

  .rb-list { display: flex; flex-direction: column; gap: 3px; overflow-y: auto; }
  .rb-row {
    display: flex; align-items: center; gap: 11px;
    padding: 9px 12px; border-radius: 7px;
    background: var(--bg-card, #15151a);
    transition: background 0.12s;
  }
  .rb-row.sel { background: var(--ui-select-bg, rgba(122,158,177,0.10)); }
  .rb-icon { color: var(--fg-4, #7a7a82); display: flex; }
  .rb-icon.dir { color: var(--ui-select, #7a9eb1); }
  .rb-icon svg { width: 18px; height: 18px; }
  .rb-info { flex: 1; min-width: 0; }
  .rb-name { font-size: 13px; color: var(--fg, #f0f0f0); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .rb-orig { font-size: 10px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono, ui-monospace, monospace); margin-top: 1px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .rb-size { font-size: 11px; color: var(--fg-4, #7a7a82); font-family: var(--font-mono, ui-monospace, monospace); white-space: nowrap; }
  .rb-date { font-size: 11px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono, ui-monospace, monospace); white-space: nowrap; min-width: 130px; text-align: right; }

  .rb-empty {
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px;
    padding: 60px 20px; color: var(--fg-4, #7a7a82); font-size: 13px;
  }

  .rb-confirm-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.5);
    display: flex; align-items: center; justify-content: center; z-index: 1000;
  }
  .rb-confirm {
    background: var(--bg-window, #16161a); border: 1px solid var(--bd-3, #2a2a32);
    border-radius: 12px; padding: 20px; width: 380px; max-width: 90vw;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5);
  }
  .rb-confirm-title { font-size: 15px; font-weight: 600; color: var(--fg, #f0f0f0); margin-bottom: 8px; }
  .rb-confirm-msg { font-size: 12px; color: var(--fg-3, #9c9ca4); line-height: 1.5; margin-bottom: 18px; }
  .rb-confirm-actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
