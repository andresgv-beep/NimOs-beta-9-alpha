<script>
  /**
   * ShareEditModal · Panel de Control · Carpetas compartidas
   * ─────────────────────────────────────────────────────────
   * Modal para editar una carpeta compartida existente: cuota, permisos de
   * usuarios y papelera. Una sola vista (no wizard de pasos).
   *
   * Carga los valores ACTUALES del share al abrir y al guardar envía:
   *   PUT /api/shares/{name}  { quota, permissions, recycleBin }
   * El backend hace diff de permisos (mandar el mapa final con 'none' para
   * quitar accesos). quota en bytes (0 = sin límite).
   */
  import { createEventDispatcher } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import WizardFrame from '$lib/ui/WizardFrame.svelte';

  export let open = false;
  export let share = null;     // objeto share a editar
  export let users = [];       // lista de usuarios del sistema

  const dispatch = createEventDispatcher();

  let saving = false;
  let errorMsg = '';

  // Estado editable
  let recycleBin = false;
  let perms = {};              // username -> none|ro|rw
  let quotaMode = 'unlimited'; // unlimited | limited
  let quotaValue = 500;
  let quotaUnit = 'GB';

  // Inicializar desde el share cada vez que se abre con uno nuevo
  $: if (open && share) initFromShare(share);

  let lastInitName = null;
  function initFromShare(s) {
    if (lastInitName === s.name) return; // no re-inicializar en cada reactividad
    lastInitName = s.name;

    recycleBin = !!s.recycleBin;
    errorMsg = '';

    // Permisos actuales
    const cur = s.permissions || {};
    perms = {};
    for (const u of users) {
      perms[u.username] = cur[u.username] || 'none';
    }
    // Incluir también usuarios con permiso que no estén en la lista (por si acaso)
    for (const [u, p] of Object.entries(cur)) {
      if (!(u in perms)) perms[u] = p;
    }

    // Cuota actual (bytes)
    const qb = Number(s.quota) || 0;
    if (qb > 0) {
      quotaMode = 'limited';
      const { value, unit } = fromBytes(qb);
      quotaValue = value;
      quotaUnit = unit;
    } else {
      quotaMode = 'unlimited';
      quotaValue = 500;
      quotaUnit = 'GB';
    }
  }

  // Cuando se cierra, permitir re-init la próxima vez
  $: if (!open) lastInitName = null;

  function fromBytes(b) {
    const TB = 1024 ** 4, GB = 1024 ** 3, MB = 1024 ** 2;
    if (b % TB === 0) return { value: b / TB, unit: 'TB' };
    if (b >= GB) return { value: Math.round(b / GB), unit: 'GB' };
    return { value: Math.round(b / MB), unit: 'MB' };
  }
  function toBytes(val, unit) {
    const n = Number(val) || 0;
    const mult = unit === 'TB' ? 1024 ** 4 : unit === 'GB' ? 1024 ** 3 : 1024 ** 2;
    return Math.round(n * mult);
  }
  function fmtBytes(b) {
    b = Number(b) || 0;
    if (!b) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, n = b;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }
  function initial(s) { return (s || '?').charAt(0).toUpperCase(); }

  $: quotaBytes = quotaMode === 'unlimited' ? 0 : toBytes(quotaValue, quotaUnit);

  function setPerm(username, p) {
    perms = { ...perms, [username]: p };
  }

  async function save() {
    if (saving || !share) return;
    saving = true;
    errorMsg = '';
    try {
      // Mapa de permisos final (incluye 'none' para que el backend los quite)
      const permObj = {};
      for (const [u, p] of Object.entries(perms)) {
        permObj[u] = p; // none|ro|rw — el diff del backend lo resuelve
      }

      const r = await fetch(`/api/shares/${encodeURIComponent(share.name)}`, {
        method: 'PUT',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({
          quota: quotaBytes,
          recycleBin,
          permissions: permObj,
        }),
      });
      if (!r.ok) {
        const e = await r.json().catch(() => ({}));
        throw new Error(e.error || 'No se pudo guardar');
      }
      dispatch('saved', { name: share.name });
    } catch (err) {
      errorMsg = err.message || 'Error de red';
    }
    saving = false;
  }

  function cancel() { dispatch('cancel'); }
</script>

<WizardFrame
  {open}
  title="Editar carpeta compartida"
  tag={share?.name || ''}
  tagColor="accent"
  currentStep={1}
  totalSteps={1}
  canAdvance={!saving}
  canGoBack={false}
  nextLabel={saving ? 'Guardando…' : 'Guardar cambios'}
  nextVariant="primary"
  width={560}
  on:next={save}
  on:cancel={cancel}
>
  {#if errorMsg}
    <div class="se-error">{errorMsg}</div>
  {/if}

  <!-- Cuota -->
  <div class="se-block">
    <div class="se-block-title">Cuota</div>
    <div class="se-quota-toggle">
      <button type="button" class="se-quota-opt" class:sel={quotaMode === 'unlimited'} on:click={() => (quotaMode = 'unlimited')}>
        <div class="se-qo-title">Sin límite</div>
        <div class="se-qo-desc">usa todo el pool</div>
      </button>
      <button type="button" class="se-quota-opt" class:sel={quotaMode === 'limited'} on:click={() => (quotaMode = 'limited')}>
        <div class="se-qo-title">Con cuota</div>
        <div class="se-qo-desc">límite fijo</div>
      </button>
    </div>
    {#if quotaMode === 'limited'}
      <div class="se-quota-input-wrap">
        <input class="se-input" type="number" min="1" bind:value={quotaValue} />
        <div class="se-unit-seg">
          {#each ['MB', 'GB', 'TB'] as unit}
            <button type="button" class="se-unit-btn" class:sel={quotaUnit === unit} on:click={() => (quotaUnit = unit)}>{unit}</button>
          {/each}
        </div>
      </div>
      <div class="se-quota-preview">{fmtBytes(quotaBytes)} de cuota</div>
    {/if}
  </div>

  <!-- Papelera -->
  <div class="se-block">
    <div class="se-block-title">Papelera de reciclaje</div>
    <button type="button" class="se-recycle" class:sel={recycleBin} on:click={() => (recycleBin = !recycleBin)}>
      <div class="se-rt-text">
        <div class="se-rt-title">{recycleBin ? 'Activada' : 'Desactivada'}</div>
        <div class="se-rt-desc">Los archivos borrados se mueven a una papelera en vez de eliminarse al instante</div>
      </div>
      <span class="se-rt-switch"></span>
    </button>
  </div>

  <!-- Permisos -->
  <div class="se-block">
    <div class="se-block-title">Permisos de usuarios</div>
    <div class="se-perm-list">
      {#if users.length === 0}
        <div class="se-hint">No hay usuarios para asignar.</div>
      {/if}
      {#each users as u (u.username)}
        <div class="se-perm-row">
          <span class="se-avatar">{initial(u.username)}</span>
          <div class="se-perm-user">
            <div class="se-perm-name">{u.username}</div>
            <div class="se-perm-role">{u.role || 'usuario'}</div>
          </div>
          {#if u.role === 'admin'}
            <span class="se-owner-tag">acceso total</span>
          {:else}
            <div class="se-seg">
              <button type="button" class="se-seg-btn none" class:sel={(perms[u.username] || 'none') === 'none'} on:click={() => setPerm(u.username, 'none')}>sin acceso</button>
              <button type="button" class="se-seg-btn ro" class:sel={perms[u.username] === 'ro'} on:click={() => setPerm(u.username, 'ro')}>ro</button>
              <button type="button" class="se-seg-btn rw" class:sel={perms[u.username] === 'rw'} on:click={() => setPerm(u.username, 'rw')}>rw</button>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  </div>
</WizardFrame>

<style>
  .se-error {
    background: rgba(255, 90, 90, 0.10);
    border: 1px solid rgba(255, 90, 90, 0.3);
    color: var(--st-crit, #ff5a5a);
    border-radius: 6px;
    padding: 10px 12px;
    font-size: 12px;
    margin-bottom: 14px;
    font-family: var(--font-mono, ui-monospace, monospace);
  }

  .se-block { margin-bottom: 22px; }
  .se-block:last-child { margin-bottom: 0; }
  .se-block-title {
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    font-weight: 600;
    margin-bottom: 10px;
    text-transform: uppercase;
    letter-spacing: 0.6px;
  }

  .se-input {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 10px 12px;
    color: var(--fg, #f0f0f0);
    font-size: 13px;
    font-family: inherit;
    outline: none;
    flex: 1;
  }
  .se-input:focus { border-color: var(--ui-select, #7a9eb1); }

  /* Cuota */
  .se-quota-toggle { display: flex; gap: 8px; margin-bottom: 12px; }
  .se-quota-opt {
    flex: 1; padding: 12px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px; cursor: pointer; text-align: center;
    transition: border-color 0.15s, background 0.15s;
  }
  .se-quota-opt:hover { border-color: var(--bd-3, #2a2a32); }
  .se-quota-opt.sel { border-color: rgba(0,255,159,0.4); background: rgba(0,255,159,0.06); }
  .se-qo-title { font-size: 13px; font-weight: 600; color: var(--fg, #f0f0f0); }
  .se-quota-opt.sel .se-qo-title { color: var(--nim-green, #00ff9f); }
  .se-qo-desc { font-size: 11px; color: var(--fg-4, #7a7a82); margin-top: 3px; }

  .se-quota-input-wrap { display: flex; gap: 8px; }
  .se-unit-seg {
    display: flex; gap: 2px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px; padding: 2px;
  }
  .se-unit-btn {
    padding: 0 12px; border: none; background: transparent;
    border-radius: 4px; font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px; color: var(--fg-4, #7a7a82); cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .se-unit-btn.sel { background: var(--ui-select-bg, rgba(122,158,177,0.10)); color: var(--ui-select, #7a9eb1); }
  .se-quota-preview {
    margin-top: 10px; font-size: 11px; color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono, ui-monospace, monospace);
  }

  /* Papelera */
  .se-recycle {
    display: flex; align-items: center; gap: 12px;
    padding: 13px 14px; width: 100%;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px; cursor: pointer; text-align: left;
    transition: border-color 0.15s;
  }
  .se-recycle:hover { border-color: var(--bd-3, #2a2a32); }
  .se-rt-text { flex: 1; }
  .se-rt-title { font-size: 13px; font-weight: 500; color: var(--fg-2, #d0d0d4); }
  .se-recycle.sel .se-rt-title { color: var(--nim-green, #00ff9f); }
  .se-rt-desc { font-size: 11px; color: var(--fg-4, #7a7a82); margin-top: 3px; line-height: 1.4; }
  .se-rt-switch {
    width: 34px; height: 19px; background: var(--bd-3, #2a2a32);
    border-radius: 5px; position: relative; flex-shrink: 0;
    transition: background 0.15s;
  }
  .se-rt-switch::after {
    content: ''; position: absolute; top: 2px; left: 2px;
    width: 15px; height: 15px; background: var(--fg-4, #7a7a82);
    border-radius: 4px; transition: left 0.15s, background 0.15s;
  }
  .se-recycle.sel .se-rt-switch { background: var(--nim-green, #00ff9f); }
  .se-recycle.sel .se-rt-switch::after { left: 17px; background: var(--bg-window, #16161a); }

  /* Permisos */
  .se-perm-list { display: flex; flex-direction: column; gap: 6px; }
  .se-hint { font-size: 11px; color: var(--fg-4, #7a7a82); }
  .se-perm-row {
    display: flex; align-items: center; gap: 11px;
    padding: 9px 11px; background: var(--bg-inner, #101015); border-radius: 7px;
  }
  .se-avatar {
    width: 28px; height: 28px; border-radius: 6px;
    background: var(--bg-card, #15151a); border: 1px solid var(--bd-2, #20202a);
    display: flex; align-items: center; justify-content: center;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px; color: var(--ui-select, #7a9eb1); font-weight: 600; flex-shrink: 0;
  }
  .se-perm-user { flex: 1; min-width: 0; }
  .se-perm-name { font-size: 13px; color: var(--fg-2, #d0d0d4); }
  .se-perm-role { font-size: 10px; color: var(--fg-4, #7a7a82); margin-top: 1px; }
  .se-owner-tag {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px; color: var(--nim-green, #00ff9f);
    background: rgba(0,255,159,0.10); border: 1px solid rgba(0,255,159,0.25);
    padding: 3px 8px; border-radius: 3px; text-transform: uppercase; letter-spacing: 0.5px;
  }
  .se-seg { display: flex; gap: 2px; background: var(--bg-card, #15151a); border-radius: 5px; padding: 2px; }
  .se-seg-btn {
    padding: 4px 9px; border: none; background: transparent; border-radius: 3px;
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 10px;
    color: var(--fg-4, #7a7a82); cursor: pointer; transition: background 0.12s, color 0.12s;
  }
  .se-seg-btn.sel.none { background: var(--bd-3, #2a2a32); color: var(--fg-2, #d0d0d4); }
  .se-seg-btn.sel.ro { background: rgba(77,184,255,0.15); color: var(--st-info, #4db8ff); }
  .se-seg-btn.sel.rw { background: rgba(0,255,159,0.15); color: var(--nim-green, #00ff9f); }
</style>
