<script>
  /**
   * CPShares · Panel de Control · sección Compartidas
   * ───────────────────────────────────────────────────
   * Carpetas compartidas: listar y crear. Migrado desde Settings
   * (sección 'shares') al lenguaje visual v3.
   *
   * API:
   *   GET  /api/shares
   *   GET  /api/storage/v2/pools   (para elegir pool destino)
   *   POST /api/shares             { name, pool, smb, nfs, ftp, public }
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { StatCard, ConfirmDialog } from '$lib/ui';
  import ShareWizardModal from './ShareWizardModal.svelte';
  import ShareCard from './ShareCard.svelte';
  import ShareEditModal from './ShareEditModal.svelte';

  let shares = [];
  let pools = [];
  let loading = true;

  let wizardOpen = false;
  let shareMsg = '';
  let shareMsgError = false;

  // Carpetas huérfanas · subvolúmenes en disco sin fila en la BD (FIX-3)
  let orphans = [];
  let readopting = false;

  // Borrado de share
  let deleteTarget = null;   // nombre del share a borrar
  let deleting = false;
  let deleteRefs = [];       // apps que usan la carpeta (G3), para avisar antes de borrar

  // Edición de share
  let editTarget = null;     // objeto share a editar
  let editUsers = [];        // lista de usuarios para el modal

  async function loadShares() {
    try {
      const [rs, rp, ro] = await Promise.all([
        fetch('/api/shares', { headers: hdrs() }),
        fetch('/api/storage/v2/pools', { headers: hdrs() }),
        fetch('/api/shares/orphans', { headers: hdrs() }),
      ]);
      if (rs.ok) shares = await rs.json();
      if (rp.ok) {
        const pd = await rp.json();
        pools = pd.data || pd.pools || (Array.isArray(pd) ? pd : []);
      }
      if (ro.ok) {
        const od = await ro.json();
        orphans = od.orphans || [];
      }
    } catch {}
    loading = false;
  }

  function openWizard() {
    if (pools.length === 0) {
      shareMsg = 'Necesitas crear un pool de almacenamiento primero';
      shareMsgError = true;
      return;
    }
    shareMsg = '';
    wizardOpen = true;
  }

  function onWizardCancel() {
    wizardOpen = false;
  }

  async function onWizardCreated() {
    wizardOpen = false;
    await loadShares();
  }

  // ─── Re-adopción de carpetas huérfanas (FIX-3) ───
  async function readoptAll() {
    if (readopting || orphans.length === 0) return;
    readopting = true;
    shareMsg = '';
    try {
      const r = await fetch('/api/shares/orphans/readopt', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ all: true }),
      });
      if (r.ok) {
        await loadShares();
      } else {
        const err = await r.json().catch(() => ({}));
        shareMsg = err.error || 'No se pudieron re-adoptar las carpetas';
        shareMsgError = true;
      }
    } catch {
      shareMsg = 'Error de red al re-adoptar';
      shareMsgError = true;
    }
    readopting = false;
  }

  // ─── Borrado de share ───
  async function onCardDelete(e) {
    deleteTarget = e.detail.name;
    deleteRefs = [];
    // G3: averiguar qué apps usan la carpeta, para avisar antes de borrar.
    try {
      const r = await fetch(`/api/shares/${encodeURIComponent(deleteTarget)}/references`, { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        deleteRefs = d.apps || [];
      }
    } catch { /* sin referencias → borrado normal */ }
  }

  async function confirmDelete() {
    if (!deleteTarget || deleting) return;
    deleting = true;
    try {
      const r = await fetch(`/api/shares/${encodeURIComponent(deleteTarget)}`, {
        method: 'DELETE',
        headers: hdrs(),
      });
      if (r.ok) {
        deleteTarget = null;
        await loadShares();
      } else {
        const err = await r.json().catch(() => ({}));
        shareMsg = err.error || 'No se pudo eliminar la carpeta';
        shareMsgError = true;
        deleteTarget = null;
      }
    } catch {
      shareMsg = 'Error de red al eliminar';
      shareMsgError = true;
      deleteTarget = null;
    }
    deleting = false;
  }

  function cancelDelete() {
    deleteTarget = null;
  }

  // ─── Edición de share ───
  async function onCardEdit(e) {
    const name = e.detail.name;
    const sh = shares.find((s) => s.name === name);
    if (!sh) return;
    // Cargar usuarios si aún no se hizo
    if (editUsers.length === 0) {
      try {
        const r = await fetch('/api/users', { headers: hdrs() });
        if (r.ok) {
          const data = await r.json();
          editUsers = (Array.isArray(data) ? data : data.users || []).filter(Boolean);
        }
      } catch { /* sin usuarios */ }
    }
    editTarget = sh;
  }

  async function onEditSaved() {
    editTarget = null;
    await loadShares();
  }

  function onEditCancel() {
    editTarget = null;
  }

  $: publicCount = shares.filter((s) => s.public).length;

  onMount(loadShares);
</script>

<div class="cp-shares">
  <!-- Resumen -->
  <div class="cps-stats">
    <StatCard label="Carpetas" value={shares.length} variant="ok" tag="compartidas" />
    <StatCard label="Pools" value={pools.length} variant="info" tag="disponibles" tagVariant="info" />
    <StatCard label="Públicas" value={publicCount} variant={publicCount > 0 ? 'warn' : 'default'} tag={publicCount > 0 ? 'sin auth' : 'ninguna'} tagVariant={publicCount > 0 ? 'warn' : 'default'} />
  </div>

  {#if shareMsg}
    <div class="cps-msg" class:error={shareMsgError}>{shareMsg}</div>
  {/if}

  {#if orphans.length > 0}
    <div class="cps-orphans">
      <div class="cps-orphans-info">
        <span class="cps-orphans-title">
          {orphans.length} {orphans.length === 1 ? 'carpeta encontrada' : 'carpetas encontradas'} en disco sin registrar
        </span>
        <span class="cps-orphans-names">{orphans.map((o) => o.name).join('  ·  ')}</span>
        <span class="cps-orphans-note">
          Tienen tus datos, pero no aparecen porque no están en la base de datos.
          Re-adoptarlas las registra sin tocar los archivos ni sus permisos.
        </span>
      </div>
      <button class="cps-btn warn" on:click={readoptAll} disabled={readopting}>
        {readopting ? 'Re-adoptando…' : 'Re-adoptar'}
      </button>
    </div>
  {/if}

  <div class="cps-head">
    <span class="cps-head-lbl">Carpetas · {shares.length}</span>
    <button class="cps-btn primary" on:click={openWizard}>+ Nueva carpeta</button>
  </div>

  <!-- Lista de carpetas -->
  {#if loading}
    <div class="cps-empty">Cargando carpetas…</div>
  {:else if shares.length === 0}
    <div class="cps-empty">No hay carpetas compartidas. Crea la primera con «+ Nueva carpeta».</div>
  {:else if shares.length > 0}
    <div class="cps-list">
      {#each shares as s (s.name)}
        <ShareCard share={s} on:delete={onCardDelete} on:edit={onCardEdit} />
      {/each}
    </div>
  {/if}

  <ShareWizardModal
    open={wizardOpen}
    {pools}
    on:cancel={onWizardCancel}
    on:created={onWizardCreated}
  />

  <ConfirmDialog
    open={deleteTarget !== null}
    title="Eliminar carpeta compartida"
    message={`¿Seguro que quieres eliminar «${deleteTarget || ''}»? Se eliminará el acceso compartido. Esta acción no se puede deshacer.${deleteRefs.length ? `\n\n⚠ Esta carpeta la usan: ${deleteRefs.join(', ')}. Esas apps podrían dejar de funcionar.` : ''}`}
    confirmLabel="Eliminar"
    cancelLabel="Cancelar"
    variant="danger"
    processing={deleting}
    on:confirm={confirmDelete}
    on:cancel={cancelDelete}
  />

  <ShareEditModal
    open={editTarget !== null}
    share={editTarget}
    users={editUsers}
    on:saved={onEditSaved}
    on:cancel={onEditCancel}
  />
</div>

<style>
  .cp-shares { display: flex; flex-direction: column; gap: 16px; max-width: 820px; }

  .cps-stats {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 8px;
  }

  .cps-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .cps-msg.error { color: var(--st-crit, #ff5a5a); }

  /* Banner de carpetas huérfanas · acento ámbar de atención (ni acción ni error) */
  .cps-orphans {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 12px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--st-warn, #f5a623);
    border-left-width: 3px;
    border-radius: 6px;
  }
  .cps-orphans-info { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
  .cps-orphans-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--st-warn, #f5a623);
    font-family: var(--font-mono);
  }
  .cps-orphans-names {
    font-size: 12px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cps-orphans-note {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono);
    line-height: 1.4;
  }
  .cps-btn.warn {
    background: var(--st-warn, #f5a623);
    border-color: var(--st-warn, #f5a623);
    color: var(--bg-window, #16161a);
    font-weight: 600;
    flex-shrink: 0;
  }
  .cps-btn.warn:hover:not(:disabled) { filter: brightness(1.08); }

  .cps-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .cps-head-lbl {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.6px;
  }

  /* Lista de carpetas */
  .cps-list { display: flex; flex-direction: column; gap: 8px; }

  .cps-btn {
    padding: 9px 16px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 12px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .cps-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .cps-btn.primary {
    background: var(--nim-green, #00ff9f);
    border-color: var(--nim-green, #00ff9f);
    color: var(--bg-window, #16161a);
    font-weight: 600;
  }
  .cps-btn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .cps-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .cps-empty {
    padding: 24px;
    text-align: center;
    color: var(--fg-5, #5a5a62);
    font-size: 12px;
    font-family: var(--font-mono);
  }
</style>
