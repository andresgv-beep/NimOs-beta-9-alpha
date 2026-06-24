<script>
  /**
   * ShareWizardModal · Panel de Control · Carpetas compartidas
   * ──────────────────────────────────────────────────────────
   * Wizard en ventana modal para crear una carpeta compartida (share).
   * 3 pasos: Carpeta (nombre/pool/descripción/papelera) · Permisos · Cuota.
   *
   * Reutiliza WizardFrame del Design System (backdrop, stepper, footer, ESC).
   *
   * API:
   *   POST  /api/shares            { name, pool, description, quotaBytes }
   *   PUT   /api/shares/{name}     { recycleBin, permissions }  (post-creación)
   *   GET   /api/users             (lista de usuarios para permisos)
   *
   * NOTA: el backend de creación sólo acepta name/pool/description/quotaBytes.
   * Protocolos (SMB/NFS/FTP) y papelera/distribución aún NO tienen cableado de
   * creación; se muestran en la UI pero su activación real será el paso natural
   * tras integrar esta UI (se aplican vía PUT donde el backend lo soporte).
   */
  import { createEventDispatcher, onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import WizardFrame from '$lib/ui/WizardFrame.svelte';

  export let open = false;
  export let pools = [];

  const dispatch = createEventDispatcher();

  // ─── Estado del formulario ───
  let step = 1;
  const totalSteps = 3;

  let form = newForm();
  let users = [];
  let saving = false;
  let errorMsg = '';

  // Cuota
  let quotaMode = 'limited'; // 'unlimited' | 'limited'
  let quotaValue = 500;
  let quotaUnit = 'GB';      // MB | GB | TB

  function newForm() {
    return {
      name: '',
      pool: '',
      description: '',
      recycleBin: true,
      // permisos: { username: 'none' | 'ro' | 'rw' }
      perms: {},
    };
  }

  function resetWizard() {
    step = 1;
    form = newForm();
    form.pool = pools[0]?.name || '';
    quotaMode = 'limited';
    quotaValue = 500;
    quotaUnit = 'GB';
    errorMsg = '';
    saving = false;
  }

  // Al abrir (flanco cerrado→abierto): resetear y recargar usuarios.
  // El modal se mantiene montado en el DOM (CPShares usa open={...}, no {#if}),
  // así que sin esto conservaría el estado de la última carpeta creada
  // (paso 3, nombre, cuota…) al reabrirlo. Bloque único para detectar el
  // flanco sin depender del orden de evaluación de los reactivos.
  let prevOpen = false;
  $: {
    if (open && !prevOpen) {
      resetWizard();
      loadUsers();
    }
    prevOpen = open;
  }

  async function loadUsers() {
    try {
      const r = await fetch('/api/users', { headers: hdrs() });
      if (r.ok) {
        const data = await r.json();
        users = (Array.isArray(data) ? data : data.users || []).filter(Boolean);
      }
    } catch { /* sin usuarios, el paso permisos queda vacío salvo creador */ }
  }

  onMount(() => {
    resetWizard();
    loadUsers();
  });

  // ─── Derivados ───
  $: selectedPool = pools.find((p) => p.name === form.pool) || null;
  $: mountPreview = form.name && form.pool
    ? `/nimos/pools/${form.pool}/shares/${form.name}`
    : '—';

  $: quotaBytes = quotaMode === 'unlimited'
    ? 0
    : toBytes(quotaValue, quotaUnit);

  function toBytes(val, unit) {
    const n = Number(val) || 0;
    const mult = unit === 'TB' ? 1024 ** 4 : unit === 'GB' ? 1024 ** 3 : 1024 ** 2;
    return Math.round(n * mult);
  }

  function fmtBytes(b) {
    if (!b) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, n = b;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }

  // ─── Validación por paso ───
  $: nameValid = /^[a-zA-Z0-9_-]+$/.test(form.name);
  $: canAdvance =
    step === 1 ? (nameValid && !!form.pool)
    : step === 3 ? !saving
    : true;

  $: nextLabel = step === totalSteps ? (saving ? 'Creando…' : 'Crear carpeta') : 'Continuar →';

  // ─── Navegación ───
  function handleNext() {
    if (step < totalSteps) { step += 1; return; }
    submit();
  }
  function handleBack() { if (step > 1) step -= 1; }
  function handleCancel() {
    dispatch('cancel');
  }

  // ─── Permisos: ciclo none → ro → rw ───
  function setPerm(username, perm) {
    form.perms = { ...form.perms, [username]: perm };
  }

  // ─── Crear ───
  async function submit() {
    if (saving || !nameValid || !form.pool) return;
    saving = true;
    errorMsg = '';
    try {
      // 1) Crear el share (campos que el backend acepta en creación)
      const createRes = await fetch('/api/shares', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: form.name,
          pool: form.pool,
          description: form.description,
          quotaBytes,
        }),
      });
      if (!createRes.ok) {
        const e = await createRes.json().catch(() => ({}));
        throw new Error(e.error || 'No se pudo crear la carpeta');
      }

      // 2) Aplicar permisos vía PUT (sólo si hay alguno asignado)
      const assigned = Object.entries(form.perms)
        .filter(([, p]) => p === 'ro' || p === 'rw');
      if (assigned.length > 0) {
        const permObj = {};
        for (const [u, p] of assigned) permObj[u] = p;
        await fetch(`/api/shares/${encodeURIComponent(form.name)}`, {
          method: 'PUT',
          headers: { ...hdrs(), 'Content-Type': 'application/json' },
          body: JSON.stringify({ permissions: permObj, recycleBin: form.recycleBin }),
        }).catch(() => { /* permisos best-effort; el share ya existe */ });
      }

      dispatch('created', { name: form.name });
    } catch (err) {
      errorMsg = err.message || 'Error de red';
      // volver al paso 1 si el error es de creación
      step = 1;
    }
    saving = false;
  }

  // Iniciales para avatar
  function initial(name) { return (name || '?').charAt(0).toUpperCase(); }
</script>

<WizardFrame
  {open}
  title="Nueva carpeta compartida"
  tag={form.name}
  tagColor="accent"
  currentStep={step}
  {totalSteps}
  {canAdvance}
  canGoBack={step > 1}
  {nextLabel}
  nextVariant="primary"
  width={560}
  on:next={handleNext}
  on:back={handleBack}
  on:cancel={handleCancel}
>
  {#if errorMsg}
    <div class="sw-error">{errorMsg}</div>
  {/if}

  <!-- ─── PASO 1 · Carpeta ─── -->
  {#if step === 1}
    <div class="sw-sub">Define el nombre y el pool de destino</div>

    <div class="sw-field">
      <div class="sw-label">Nombre de la carpeta</div>
      <input
        class="sw-input"
        type="text"
        placeholder="proyectos"
        bind:value={form.name}
        autofocus
      />
      {#if form.name && !nameValid}
        <div class="sw-hint err">Solo letras, números, guion y guion bajo</div>
      {:else}
        <div class="sw-hint">Se creará en <span class="path">{mountPreview}</span></div>
      {/if}
    </div>

    <div class="sw-field">
      <div class="sw-label">Pool de destino</div>
      <div class="sw-pool-grid">
        {#each pools as p (p.name)}
          <button
            type="button"
            class="sw-pool-opt"
            class:sel={form.pool === p.name}
            on:click={() => (form.pool = p.name)}
          >
            <span class="sw-pool-radio"></span>
            <span class="sw-pool-cube"></span>
            <div class="sw-pool-info">
              <div class="sw-pool-name">{p.name}</div>
              <div class="sw-pool-meta">{p.profile || 'btrfs'}</div>
            </div>
            {#if p.free_bytes || p.available_bytes}
              <span class="sw-pool-free">{fmtBytes(p.free_bytes || p.available_bytes)} libres</span>
            {/if}
          </button>
        {/each}
      </div>
      {#if pools.length === 0}
        <div class="sw-hint err">No hay pools disponibles. Crea uno primero.</div>
      {/if}
    </div>

    <div class="sw-field">
      <div class="sw-label">Descripción <span class="opt">· opcional</span></div>
      <input class="sw-input" type="text" placeholder="Carpeta de proyectos del equipo" bind:value={form.description} />
    </div>

    <button type="button" class="sw-recycle" class:sel={form.recycleBin} on:click={() => (form.recycleBin = !form.recycleBin)}>
      <div class="sw-rt-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>
        </svg>
      </div>
      <div class="sw-rt-text">
        <div class="sw-rt-title">Papelera de reciclaje</div>
        <div class="sw-rt-desc">Los archivos borrados se mueven a una papelera en lugar de eliminarse al instante</div>
      </div>
      <span class="sw-rt-switch"></span>
    </button>
  {/if}

  <!-- ─── PASO 2 · Permisos ─── -->
  {#if step === 2}
    <div class="sw-sub">Asigna quién puede acceder y con qué permiso</div>
    <div class="sw-perm-intro">
      El creador tiene acceso completo automáticamente. Asigna permisos al resto:
      <b class="ro">RO</b> solo lectura · <b class="rw">RW</b> lectura y escritura.
    </div>

    <div class="sw-perm-list">
      {#if users.length === 0}
        <div class="sw-hint">No hay otros usuarios. Podrás asignar permisos más tarde.</div>
      {/if}
      {#each users as u (u.username)}
        <div class="sw-perm-row">
          <div class="sw-perm-avatar">{initial(u.username)}</div>
          <div class="sw-perm-user">
            <div class="sw-perm-name">{u.username}</div>
            <div class="sw-perm-role">{u.role || 'usuario'}</div>
          </div>
          {#if u.role === 'admin'}
            <span class="sw-perm-owner-tag">acceso total</span>
          {:else}
            <div class="sw-perm-seg">
              <button type="button" class="sw-seg-btn none" class:sel={(form.perms[u.username] || 'none') === 'none'} on:click={() => setPerm(u.username, 'none')}>sin acceso</button>
              <button type="button" class="sw-seg-btn ro" class:sel={form.perms[u.username] === 'ro'} on:click={() => setPerm(u.username, 'ro')}>ro</button>
              <button type="button" class="sw-seg-btn rw" class:sel={form.perms[u.username] === 'rw'} on:click={() => setPerm(u.username, 'rw')}>rw</button>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- ─── PASO 3 · Cuota ─── -->
  {#if step === 3}
    <div class="sw-sub">Limita el espacio que puede ocupar esta carpeta</div>

    <div class="sw-quota-toggle">
      <button type="button" class="sw-quota-opt" class:sel={quotaMode === 'unlimited'} on:click={() => (quotaMode = 'unlimited')}>
        <div class="sw-quota-opt-title">Sin límite</div>
        <div class="sw-quota-opt-desc">usa todo el pool</div>
      </button>
      <button type="button" class="sw-quota-opt" class:sel={quotaMode === 'limited'} on:click={() => (quotaMode = 'limited')}>
        <div class="sw-quota-opt-title">Con cuota</div>
        <div class="sw-quota-opt-desc">límite fijo</div>
      </button>
    </div>

    {#if quotaMode === 'limited'}
      <div class="sw-field">
        <div class="sw-label">Tamaño máximo</div>
        <div class="sw-quota-input-wrap">
          <input class="sw-input" type="number" min="1" bind:value={quotaValue} placeholder="500" />
          <div class="sw-quota-unit-seg">
            {#each ['MB', 'GB', 'TB'] as unit}
              <button type="button" class="sw-unit-btn" class:sel={quotaUnit === unit} on:click={() => (quotaUnit = unit)}>{unit}</button>
            {/each}
          </div>
        </div>
      </div>

      <div class="sw-quota-preview">
        <div class="sw-quota-preview-text">
          <span><span class="em">{fmtBytes(quotaBytes)}</span> de cuota</span>
          {#if selectedPool}
            <span>pool {selectedPool.name}</span>
          {/if}
        </div>
      </div>
    {:else}
      <div class="sw-hint">Esta carpeta podrá crecer hasta llenar el pool {form.pool}.</div>
    {/if}
  {/if}
</WizardFrame>

<style>
  /* Tipos y tokens heredados del Design System v3 (variables globales). */
  .sw-error {
    background: rgba(255, 90, 90, 0.10);
    border: 1px solid rgba(255, 90, 90, 0.3);
    color: var(--st-crit, #ff5a5a);
    border-radius: 6px;
    padding: 10px 12px;
    font-size: 12px;
    margin-bottom: 14px;
    font-family: var(--font-mono, ui-monospace, monospace);
  }

  .sw-sub {
    font-size: 12px;
    color: var(--fg-3, #9c9ca4);
    margin-bottom: 18px;
  }

  .sw-field { margin-bottom: 18px; }
  .sw-label {
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    margin-bottom: 7px;
    font-weight: 500;
  }
  .sw-label .opt { color: var(--fg-5, #5a5a62); font-weight: 400; }

  .sw-input {
    width: 100%;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 10px 12px;
    color: var(--fg, #f0f0f0);
    font-size: 13px;
    font-family: inherit;
    transition: border-color 0.15s;
  }
  .sw-input:focus {
    outline: none;
    border-color: var(--ui-select, #7a9eb1);
  }

  .sw-hint {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    margin-top: 6px;
  }
  .sw-hint.err { color: var(--st-crit, #ff5a5a); }
  .sw-hint .path,
  .path {
    font-family: var(--font-mono, ui-monospace, monospace);
    color: var(--ui-select, #7a9eb1);
  }

  /* Pool grid */
  .sw-pool-grid { display: flex; flex-direction: column; gap: 8px; }
  .sw-pool-opt {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    cursor: pointer;
    text-align: left;
    width: 100%;
    transition: border-color 0.15s, background 0.15s;
  }
  .sw-pool-opt:hover { border-color: var(--bd-3, #2a2a32); }
  .sw-pool-opt.sel {
    border-color: var(--ui-select-border, rgba(122,158,177,0.35));
    background: var(--ui-select-bg, rgba(122,158,177,0.10));
  }
  .sw-pool-radio {
    width: 16px; height: 16px;
    border-radius: 50%;
    border: 2px solid var(--bd-3, #2a2a32);
    flex-shrink: 0;
    position: relative;
    transition: border-color 0.15s;
  }
  .sw-pool-opt.sel .sw-pool-radio { border-color: var(--ui-select, #7a9eb1); }
  .sw-pool-opt.sel .sw-pool-radio::after {
    content: '';
    position: absolute; inset: 3px;
    border-radius: 50%;
    background: var(--ui-select, #7a9eb1);
  }
  .sw-pool-cube {
    width: 22px; height: 22px;
    border-radius: 4px;
    background: linear-gradient(135deg, var(--nim-green, #00ff9f), rgba(0,255,159,0.4));
    flex-shrink: 0;
    opacity: 0.85;
  }
  .sw-pool-info { flex: 1; min-width: 0; }
  .sw-pool-name { font-size: 13px; font-weight: 600; color: var(--fg, #f0f0f0); }
  .sw-pool-meta {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono, ui-monospace, monospace);
    margin-top: 1px;
  }
  .sw-pool-free {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono, ui-monospace, monospace);
    white-space: nowrap;
  }

  /* Papelera toggle */
  .sw-recycle {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 13px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    cursor: pointer;
    text-align: left;
    width: 100%;
    transition: border-color 0.15s;
  }
  .sw-recycle:hover { border-color: var(--bd-3, #2a2a32); }
  .sw-rt-icon { color: var(--fg-4, #7a7a82); flex-shrink: 0; margin-top: 1px; }
  .sw-rt-icon svg { width: 18px; height: 18px; }
  .sw-recycle.sel .sw-rt-icon { color: var(--nim-green, #00ff9f); }
  .sw-rt-text { flex: 1; }
  .sw-rt-title { font-size: 13px; font-weight: 500; color: var(--fg-2, #d0d0d4); }
  .sw-rt-desc { font-size: 11px; color: var(--fg-4, #7a7a82); margin-top: 3px; line-height: 1.4; }
  .sw-rt-switch {
    width: 34px; height: 19px;
    background: var(--bd-3, #2a2a32);
    border-radius: 5px;
    position: relative;
    flex-shrink: 0;
    margin-top: 2px;
    transition: background 0.15s;
  }
  .sw-rt-switch::after {
    content: '';
    position: absolute; top: 2px; left: 2px;
    width: 15px; height: 15px;
    background: var(--fg-4, #7a7a82);
    border-radius: 4px;
    transition: left 0.15s, background 0.15s;
  }
  .sw-recycle.sel .sw-rt-switch { background: var(--nim-green, #00ff9f); }
  .sw-recycle.sel .sw-rt-switch::after { left: 17px; background: var(--bg-window, #16161a); }

  /* Permisos */
  .sw-perm-intro {
    font-size: 12px;
    color: var(--fg-3, #9c9ca4);
    line-height: 1.5;
    margin-bottom: 16px;
    padding: 10px 12px;
    background: var(--bg-inner, #101015);
    border-radius: 6px;
  }
  .sw-perm-intro b.ro { color: var(--st-info, #4db8ff); }
  .sw-perm-intro b.rw { color: var(--nim-green, #00ff9f); }

  .sw-perm-list { display: flex; flex-direction: column; gap: 6px; }
  .sw-perm-row {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 11px;
    background: var(--bg-inner, #101015);
    border-radius: 7px;
  }
  .sw-perm-avatar {
    width: 28px; height: 28px;
    border-radius: 6px;
    background: var(--bg-card, #15151a);
    border: 1px solid var(--bd-2, #20202a);
    display: flex; align-items: center; justify-content: center;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px;
    color: var(--ui-select, #7a9eb1);
    font-weight: 600;
    flex-shrink: 0;
  }
  .sw-perm-user { flex: 1; min-width: 0; }
  .sw-perm-name { font-size: 13px; color: var(--fg-2, #d0d0d4); }
  .sw-perm-role { font-size: 10px; color: var(--fg-4, #7a7a82); margin-top: 1px; }
  .sw-perm-owner-tag {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px;
    color: var(--nim-green, #00ff9f);
    background: rgba(0,255,159,0.10);
    border: 1px solid rgba(0,255,159,0.25);
    padding: 3px 8px;
    border-radius: 3px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .sw-perm-seg {
    display: flex;
    gap: 2px;
    background: var(--bg-card, #15151a);
    border-radius: 5px;
    padding: 2px;
  }
  .sw-seg-btn {
    padding: 4px 9px;
    border: none;
    background: transparent;
    border-radius: 3px;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--fg-4, #7a7a82);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .sw-seg-btn.sel.none { background: var(--bd-3, #2a2a32); color: var(--fg-2, #d0d0d4); }
  .sw-seg-btn.sel.ro { background: rgba(77,184,255,0.15); color: var(--st-info, #4db8ff); }
  .sw-seg-btn.sel.rw { background: rgba(0,255,159,0.15); color: var(--nim-green, #00ff9f); }

  /* Cuota */
  .sw-quota-toggle { display: flex; gap: 8px; margin-bottom: 18px; }
  .sw-quota-opt {
    flex: 1;
    padding: 13px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 8px;
    cursor: pointer;
    text-align: center;
    transition: border-color 0.15s, background 0.15s;
  }
  .sw-quota-opt:hover { border-color: var(--bd-3, #2a2a32); }
  .sw-quota-opt.sel {
    border-color: rgba(0,255,159,0.4);
    background: rgba(0,255,159,0.06);
  }
  .sw-quota-opt-title { font-size: 13px; font-weight: 600; color: var(--fg, #f0f0f0); }
  .sw-quota-opt.sel .sw-quota-opt-title { color: var(--nim-green, #00ff9f); }
  .sw-quota-opt-desc { font-size: 11px; color: var(--fg-4, #7a7a82); margin-top: 3px; }

  .sw-quota-input-wrap { display: flex; gap: 8px; align-items: stretch; }
  .sw-quota-input-wrap .sw-input { flex: 1; }
  .sw-quota-unit-seg {
    display: flex;
    gap: 2px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 2px;
  }
  .sw-unit-btn {
    padding: 0 12px;
    border: none;
    background: transparent;
    border-radius: 4px;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .sw-unit-btn.sel { background: var(--ui-select-bg, rgba(122,158,177,0.10)); color: var(--ui-select, #7a9eb1); }

  .sw-quota-preview {
    margin-top: 14px;
    padding: 12px 14px;
    background: var(--bg-inner, #101015);
    border-radius: 7px;
  }
  .sw-quota-preview-text {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono, ui-monospace, monospace);
  }
  .sw-quota-preview-text .em { color: var(--fg-2, #d0d0d4); font-weight: 600; }
</style>
