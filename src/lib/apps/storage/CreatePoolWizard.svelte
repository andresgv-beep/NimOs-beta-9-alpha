<script>
  /**
   * CreatePoolWizard · Wizard to create a new storage pool
   * ─────────────────────────────────────────────────────────
   * Beta 8.1: BTRFS-only. ZFS eliminado en Fase 5.
   *
   * 3 pasos visibles: discos → nombre → confirmación.
   * (El step interno arranca en 2 — el step 1 era "elegir BTRFS/ZFS",
   * eliminado al irse ZFS. El stepper visible muestra 1/3, 2/3, 3/3
   * via `currentStep={step - 1}` y `totalSteps={3}`).
   *
   * Filosofía: solo layouts seguros recomendados. El usuario NO elige layout,
   * se calcula automáticamente según número de discos seleccionados.
   *   1 disk → single, 2 → raid1, 3 → raid1, 4+ → raid10
   *
   * Backend:
   *   POST /api/storage/v2/pools { name, profile, disks: [paths], wipe_first }
   *
   * Validación de nombre idéntica a la del backend:
   *   ^[a-zA-Z0-9-]{1,32}$ + reserved list
   *
   * NOTA Beta 8.1: este wizard usa el frame visual nuevo (WizardFrame) del
   * Design System Beta 8.1. La LÓGICA es idéntica a la versión anterior —
   * solo cambia presentación.
   */
  import { createEventDispatcher } from 'svelte';
  import { jsonHdrs } from '$lib/stores/auth.js';
  import WizardFrame from '$lib/ui/WizardFrame.svelte';

  // capabilities prop mantenida por retrocompat con el caller, pero ignorada
  // (Beta 8: siempre BTRFS, no se ofrece elección al usuario).
  export let capabilities = { zfs: false, btrfs: true };
  export let eligibleDisks = [];
  export let pools = [];
  export let orphanFilesystems = [];

  // Helper local idéntico al de StorageApp (Bloque C3.3).
  // Determina el estado real de un disco para mostrar avisos.
  function diskStatusLocal(diskPath) {
    if (!diskPath) return { kind: 'free', label: 'disponible', variant: 'accent' };

    for (const pool of pools) {
      const poolDevices = pool.devices || [];
      for (const d of poolDevices) {
        const dPath = typeof d === 'string' ? d : (d.current_path || '');
        if (dPath === diskPath) {
          return {
            kind: 'managed',
            label: `pool ${pool.name}`,
            variant: 'success',
            tooltip: `En uso por pool "${pool.name}"`,
          };
        }
      }
    }

    for (const fs of orphanFilesystems) {
      for (const dev of (fs.devices || [])) {
        if (dev.path === diskPath) {
          return {
            kind: 'orphan',
            label: 'BTRFS huérfano',
            variant: 'warn',
            fsUuid: fs.uuid,
            fsLabel: fs.label,
            tooltip: `Tiene BTRFS no gestionado (${fs.label || fs.uuid}). ` +
                     `Datos preservables si lo importas desde "Observados".`,
          };
        }
      }
    }

    return { kind: 'free', label: 'disponible', variant: 'accent' };
  }

  const dispatch = createEventDispatcher();

  // ─── State ───
  let step = 2;                // 1 = (DEPRECATED, BTRFS-only) · 2 = discos · 3 = nombre · 4 = confirmar
  let fsType = 'btrfs';
  let selectedDisks = new Set();
  let poolName = '';
  let nameError = '';
  let processing = false;
  let errorMsg = '';

  const RESERVED_NAMES_BTRFS = ['system', 'config', 'temp', 'swap', 'root', 'boot'];
  $: reservedNames = RESERVED_NAMES_BTRFS;

  // ─── Derived ───
  $: diskCount = selectedDisks.size;
  $: layoutOptions = computeLayoutOptions(fsType, diskCount);
  // layout = la opción elegida. Si solo hay una, es esa; si hay varias (3
  // discos: raid1 vs raid1c3), la que el usuario seleccionó (chosenLayoutId).
  $: layout = pickLayout(layoutOptions, chosenLayoutId);
  $: selectedDisksArr = eligibleDisks.filter(d => selectedDisks.has(d.path || `/dev/${d.name}`));
  $: usableCapacity = computeUsableCapacity(fsType, layout, selectedDisksArr);

  // Cuando cambia el número de discos, resetear la elección al default de ese
  // conjunto de opciones (el primero marcado como recommended), para no
  // arrastrar una elección que ya no aplica.
  let chosenLayoutId = '';
  $: resetChosenOnDiskChange(layoutOptions);
  function resetChosenOnDiskChange(opts) {
    if (!opts.some(o => o.id === chosenLayoutId)) {
      const rec = opts.find(o => o.recommended) || opts[0];
      chosenLayoutId = rec ? rec.id : '';
    }
  }

  $: {
    nameError = '';
    if (poolName.length > 0) {
      if (poolName.length > 32) {
        nameError = 'Máximo 32 caracteres.';
      } else if (poolName.length < 2) {
        nameError = 'Mínimo 2 caracteres.';
      } else if (!/^[a-z][a-z0-9_-]*$/.test(poolName)) {
        nameError = 'Debe empezar por letra · minúsculas, dígitos, - y _';
      } else if (reservedNames.includes(poolName)) {
        nameError = `"${poolName}" es un nombre reservado.`;
      }
    }
  }

  $: canAdvance = processing ? false
                : step === 2 ? diskCount >= 1
                : step === 3 ? poolName.length > 0 && nameError === ''
                : step === 4 ? true
                : false;

  $: nextLabel = step === 4 ? (processing ? 'Creando...' : 'Crear pool') : 'Continuar →';
  $: nextVariant = step === 4 ? 'primary' : 'primary';

  // computeLayoutOptions devuelve la lista de layouts ofrecidos para N discos.
  // En la mayoría de casos hay una sola opción sensata. Con 3 discos hay una
  // decisión real de redundancia (raid1 = más capacidad / 1 fallo, raid1c3 =
  // más seguridad / 2 fallos) y se ofrecen ambas para que el usuario elija.
  function computeLayoutOptions(fs, n) {
    if (n < 1) return [];
    if (n === 1) return [
      { id: 'single', label: 'Single', tolerates: 0, recommended: true,
        desc: 'Sin redundancia · toda la capacidad disponible' },
    ];
    if (n === 2) return [
      { id: 'raid1', label: 'RAID1', tolerates: 1, recommended: true,
        desc: 'Cada bloque se guarda en 2 discos · si falla 1, no pierdes datos · capacidad = total / 2' },
    ];
    if (n === 3) return [
      { id: 'raid1', label: 'RAID1', tolerates: 1, recommended: true,
        desc: 'Cada bloque en 2 copias repartidas entre los 3 discos · tolera el fallo de 1 disco · más capacidad (≈ total / 2)' },
      { id: 'raid1c3', label: 'RAID1C3', tolerates: 2, recommended: false,
        desc: 'Cada bloque en 3 copias, una por disco · tolera el fallo de 2 discos · más seguridad (capacidad = total / 3)' },
    ];
    return [
      { id: 'raid10', label: 'RAID10', tolerates: 1, recommended: true,
        desc: 'Stripe + mirror · tolera el fallo de 1 disco · mejor rendimiento · capacidad = total / 2' },
    ];
  }

  // pickLayout resuelve el layout efectivo: la opción elegida por id, o la
  // recomendada/primera como fallback seguro.
  function pickLayout(opts, chosenId) {
    if (!opts || opts.length === 0) return { id: '', label: '—', tolerates: 0, desc: '' };
    return opts.find(o => o.id === chosenId)
        || opts.find(o => o.recommended)
        || opts[0];
  }

  function computeUsableCapacity(fs, lay, disks) {
    if (disks.length === 0) return 0;
    const sizes = disks.map(d => d.size || 0).filter(s => s > 0);
    if (sizes.length === 0) return 0;
    const total = sizes.reduce((a, b) => a + b, 0);
    if (lay.id === 'single')  return total;
    if (lay.id === 'raid1')   return Math.floor(total / 2);
    if (lay.id === 'raid1c3') return Math.floor(total / 3); // 3 copias
    if (lay.id === 'raid10')  return Math.floor(total / 2);
    return total;
  }

  // ─── Handlers ───
  function toggleDisk(path) {
    if (selectedDisks.has(path)) selectedDisks.delete(path);
    else selectedDisks.add(path);
    selectedDisks = selectedDisks;
  }

  function handleNext() {
    if (step === 4) {
      submitCreate();
      return;
    }
    step += 1;
    errorMsg = '';
  }

  function handleBack() {
    if (step > 2) {
      step -= 1;
      errorMsg = '';
    }
  }

  function handleCancel() {
    if (processing) return;
    dispatch('cancel');
  }

  async function unwrapV2(res, label = 'api call') {
    let body;
    try {
      body = await res.json();
    } catch {
      throw new Error(`${label}: invalid JSON response (status ${res.status})`);
    }
    if (!res.ok) {
      let code = `http_${res.status}`;
      let msg = res.statusText || 'request failed';
      let details;
      if (body?.error) {
        if (typeof body.error === 'string') {
          msg = body.error;
          code = body.error;
        } else if (typeof body.error === 'object') {
          code = body.error.code || code;
          msg = body.error.message || msg;
          details = body.error.details;
        }
      }
      const e = new Error(msg);
      e.code = code;
      e.details = details;
      throw e;
    }
    if (body && typeof body === 'object' && 'data' in body && !Array.isArray(body)) {
      return body.data;
    }
    return body;
  }

  // ─── Bloque C3.4: Estado del diálogo de doble intención ─────────────────
  let collisionDetected = null;
  let collisionAck = '';

  async function submitCreate() {
    processing = true;
    errorMsg = '';
    collisionDetected = null;
    collisionAck = '';

    const body = {
      name: poolName,
      profile: layout.id,
      disks: Array.from(selectedDisks),
    };

    try {
      const res = await fetch('/api/storage/v2/pools', {
        method: 'POST',
        headers: jsonHdrs(),
        body: JSON.stringify(body),
      });
      await unwrapV2(res, 'create pool');
      processing = false;
      dispatch('done', { poolName });
    } catch (err) {
      console.error('create pool error:', err);
      if (err.code === 'DISK_HAS_FILESYSTEM' && err.details) {
        collisionDetected = err.details;
        processing = false;
        return;
      }
      errorMsg = err.message || 'Error al crear el pool';
      processing = false;
    }
  }

  function chooseImport() {
    if (!collisionDetected) return;
    dispatch('request-import', {
      uuid: collisionDetected.fs_uuid,
      label: collisionDetected.fs_label,
      details: collisionDetected,
    });
    collisionDetected = null;
    dispatch('cancel');
  }

  async function chooseDestroyAndContinue() {
    if (collisionAck !== 'DESTRUIR') {
      errorMsg = 'Escribe "DESTRUIR" para confirmar la operación destructiva';
      return;
    }
    processing = true;
    errorMsg = '';
    try {
      for (const path of selectedDisks) {
        const wipeRes = await fetch('/api/storage/v2/wipe', {
          method: 'POST',
          headers: jsonHdrs(),
          body: JSON.stringify({ disk: path }),
        });
        await unwrapV2(wipeRes, `wipe ${path}`);
      }
      collisionDetected = null;
      collisionAck = '';
      await submitCreate();
    } catch (err) {
      errorMsg = err.message || 'Error al wipear discos';
      processing = false;
    }
  }

  function dismissCollision() {
    collisionDetected = null;
    collisionAck = '';
  }

  // ─── Helpers ───
  function fmtBytes(b) {
    if (!b || b === 0) return '0 B';
    if (b >= 1e12) return (b / 1e12).toFixed(1) + ' TB';
    if (b >= 1e9)  return (b / 1e9).toFixed(1)  + ' GB';
    if (b >= 1e6)  return (b / 1e6).toFixed(0)  + ' MB';
    return b + ' B';
  }

  function diskPath(d) {
    return d.path || `/dev/${d.name}`;
  }

  $: hasMixedSizes = (() => {
    if (selectedDisksArr.length < 2) return false;
    const sizes = selectedDisksArr.map(d => d.size || 0);
    const min = Math.min(...sizes);
    const max = Math.max(...sizes);
    return max > 0 && (max - min) / max > 0.05;
  })();
</script>

<WizardFrame
  open={true}
  title="Crear pool"
  tag={fsType ? fsType.toUpperCase() : ''}
  tagColor="accent"
  currentStep={step - 1}
  totalSteps={3}
  {canAdvance}
  canGoBack={step > 2 && !processing}
  {nextLabel}
  {nextVariant}
  cancelLabel={processing ? 'Procesando...' : 'Cancelar'}
  on:next={handleNext}
  on:back={handleBack}
  on:cancel={handleCancel}
>
  <!-- PASO 2 → Discos (visible como 1/3) -->
  {#if step === 2}
    <div class="step-label">Discos</div>
    <p class="step-desc">
      Selecciona los discos del pool. Los datos existentes en estos discos
      se <b>borrarán</b> al crear el pool. BTRFS puede mezclar capacidades
      sin desperdiciar espacio.
    </p>

    {#if eligibleDisks.length === 0}
      <div class="alert-warn">
        No hay discos libres elegibles. Ve a la vista Discos y formatea
        algún disco primero.
      </div>
    {:else}
      <div class="disk-list">
        {#each eligibleDisks as d}
          {@const path = diskPath(d)}
          {@const dStatus = diskStatusLocal(path)}
          <button
            class="disk-row"
            class:selected={selectedDisks.has(path)}
            class:has-orphan={dStatus.kind === 'orphan'}
            on:click={() => toggleDisk(path)}
            title={dStatus.tooltip || ''}
            type="button"
          >
            <div class="dr-check">
              {#if selectedDisks.has(path)}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="20 6 9 17 4 12"/>
                </svg>
              {/if}
            </div>
            <div class="dr-info">
              <div class="dr-path">{path}</div>
              <div class="dr-model">{d.model || '—'}</div>
              {#if dStatus.kind === 'orphan'}
                <div class="dr-orphan-hint">⚠ Tiene BTRFS huérfano · datos preservables</div>
              {/if}
            </div>
            <div class="dr-size">{d.sizeH || fmtBytes(d.size)}</div>
            <div class="dr-tags">
              <span class="chip chip-{d.rotational ? 'default' : 'info'}">
                {d.rotational ? 'HDD' : 'SSD'}
              </span>
              {#if dStatus.kind === 'orphan'}
                <span class="chip chip-warn">{dStatus.label}</span>
              {/if}
            </div>
          </button>
        {/each}
      </div>

      {#if [...selectedDisks].some(p => diskStatusLocal(p).kind === 'orphan')}
        <div class="alert-warn">
          <b>Atención:</b> Al menos uno de los discos seleccionados tiene
          un filesystem BTRFS no gestionado. Si continúas, esos datos se
          <b>borrarán</b> al crear el nuevo pool. Si quieres preservarlos,
          cancela y usa "Importar como pool" desde "Observados".
        </div>
      {/if}
    {/if}

    {#if diskCount > 0}
      {#if layoutOptions.length > 1}
        <!-- Varias opciones (3 discos): el usuario elige redundancia vs capacidad -->
        <div class="layout-choices">
          <span class="lc-title">Elige el nivel de protección</span>
          {#each layoutOptions as opt}
            <button
              type="button"
              class="lc-option"
              class:selected={layout.id === opt.id}
              on:click={() => chosenLayoutId = opt.id}
            >
              <div class="lc-radio" class:on={layout.id === opt.id}></div>
              <div class="lc-body">
                <div class="lc-head">
                  <span class="lc-name">{opt.label}</span>
                  <span class="lc-tol">tolera {opt.tolerates} {opt.tolerates === 1 ? 'fallo' : 'fallos'}</span>
                  {#if opt.recommended}<span class="lc-rec">recomendado</span>{/if}
                </div>
                <div class="lc-desc">{opt.desc}</div>
                <div class="lc-cap">
                  Capacidad útil ≈ <b>{fmtBytes(computeUsableCapacity(fsType, opt, selectedDisksArr))}</b>
                </div>
              </div>
            </button>
          {/each}
        </div>
      {:else}
        <!-- Una sola opción sensata: preview informativo de siempre -->
        <div class="layout-preview">
          <div class="lp-head">
            <span class="lp-label">Layout recomendado</span>
            <span class="lp-name">{layout.label}</span>
          </div>
          <div class="lp-desc">{layout.desc}</div>
          <div class="lp-cap">
            <span class="lp-cap-label">Capacidad útil estimada</span>
            <span class="lp-cap-val">{fmtBytes(usableCapacity)}</span>
          </div>
        </div>
      {/if}
    {/if}
  {/if}

  <!-- PASO 3 → Nombre (visible como 2/3) -->
  {#if step === 3}
    <div class="step-label">Nombre</div>
    <p class="step-desc">
      Dale un nombre al pool. Se usará en la ruta de montaje
      (<b>/nimos/pools/{poolName || 'nombre'}</b>) y en los shares.
      Elige algo corto y descriptivo.
    </p>

    <input
      class="name-input"
      class:err={nameError !== ''}
      class:ok={poolName.length > 0 && nameError === ''}
      type="text"
      bind:value={poolName}
      on:input={(e) => { poolName = e.target.value.toLowerCase(); }}
      placeholder="ej: datos, media, backup"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck="false"
      maxlength="32"
    />

    <div class="name-hint" class:err={nameError !== ''}>
      {#if nameError}
        {nameError}
      {:else if poolName.length === 0}
        2-32 caracteres · empezar por letra · minúsculas, dígitos, - y _
      {:else}
        ✓ Nombre válido
      {/if}
    </div>

    <div class="impact-card">
      <div class="impact-row">
        <span class="k">sistema</span>
        <span class="v">{fsType.toUpperCase()}</span>
      </div>
      <div class="impact-row">
        <span class="k">layout</span>
        <span class="v">{layout.label}</span>
      </div>
      <div class="impact-row">
        <span class="k">discos</span>
        <span class="v">{diskCount}</span>
      </div>
      <div class="impact-row">
        <span class="k">capacidad útil</span>
        <span class="v accent">{fmtBytes(usableCapacity)}</span>
      </div>
    </div>
  {/if}

  <!-- PASO 4 → Confirmación (visible como 3/3) -->
  {#if step === 4}
    <div class="step-label">Confirmación</div>
    <p class="step-desc">
      Vas a crear el pool <b>{poolName}</b> con
      <b>{diskCount}</b> disco{diskCount === 1 ? '' : 's'} en configuración
      <b>{fsType.toUpperCase()} {layout.label}</b>.
    </p>

    <div class="impact-card">
      <div class="impact-row">
        <span class="k">nombre</span>
        <span class="v">{poolName}</span>
      </div>
      <div class="impact-row">
        <span class="k">profile</span>
        <span class="v">{fsType.toUpperCase()} {layout.label}</span>
      </div>
      <div class="impact-row">
        <span class="k">capacidad usable</span>
        <span class="v accent">{fmtBytes(usableCapacity)}</span>
      </div>
      <div class="impact-row">
        <span class="k">discos ({diskCount})</span>
        <span class="v"></span>
      </div>
      <div class="disk-chips">
        {#each selectedDisksArr as d}
          <span class="disk-chip">
            <span class="cube"></span>
            {diskPath(d)} · {d.sizeH || fmtBytes(d.size)}
          </span>
        {/each}
      </div>
    </div>

    <ul class="bullets">
      <li>Los datos existentes en los discos se <b>borrarán</b></li>
      <li>El pool se montará en <b>/nimos/pools/{poolName}</b></li>
      <li>Podrás gestionar shares, snapshots y apps desde NimOS</li>
    </ul>

    <div class="alert-warn">
      Los datos existentes en estos discos se <b>borrarán</b> al crear el pool.
      Esta acción no se puede deshacer.
    </div>

    {#if errorMsg}
      <div class="alert-crit">{errorMsg}</div>
    {/if}
  {/if}
</WizardFrame>

<!--
  Bloque C3.4 — Diálogo de doble intención
  Aparece sobre el wizard cuando el backend devuelve DISK_HAS_FILESYSTEM.
-->
{#if collisionDetected}
  <div class="coll-backdrop" on:click|self={dismissCollision} role="presentation"></div>
  <div class="coll-modal" role="dialog" aria-modal="true">
    <div class="coll-head">
      <div class="coll-title">
        Filesystem detectado en el disco
        <span class="coll-tag">"{collisionDetected.disk}"</span>
      </div>
      <button class="coll-close" on:click={dismissCollision} title="Cerrar" aria-label="Cerrar">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <line x1="18" y1="6" x2="6" y2="18"/>
          <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </div>
    <div class="coll-strip"></div>

    <div class="coll-body">
      <p class="step-desc">
        El disco contiene un filesystem existente. <b>Decide qué hacer antes
        de continuar.</b>
      </p>

      <div class="impact-card">
        <div class="impact-row">
          <span class="k">tipo</span>
          <span class="v">{collisionDetected.fs_type}{collisionDetected.fs_profile ? ' · ' + collisionDetected.fs_profile : ''}</span>
        </div>
        {#if collisionDetected.fs_label}
          <div class="impact-row">
            <span class="k">label</span>
            <span class="v">{collisionDetected.fs_label}</span>
          </div>
        {/if}
        {#if collisionDetected.fs_uuid}
          <div class="impact-row">
            <span class="k">uuid</span>
            <span class="v sm">{collisionDetected.fs_uuid}</span>
          </div>
        {/if}
        {#if collisionDetected.is_managed}
          <div class="impact-row">
            <span class="k">pool gestionado</span>
            <span class="v warn">{collisionDetected.pool_name}</span>
          </div>
        {/if}
        {#if collisionDetected.observation_health}
          <div class="impact-row">
            <span class="k">estado</span>
            <span class="v">{collisionDetected.observation_health}</span>
          </div>
        {/if}
        {#if collisionDetected.size_bytes > 0}
          <div class="impact-row">
            <span class="k">capacidad</span>
            <span class="v">
              {fmtBytes(collisionDetected.size_bytes)}{collisionDetected.used_bytes > 0 ? ' · ' + fmtBytes(collisionDetected.used_bytes) + ' usados' : ''}
            </span>
          </div>
        {/if}
      </div>

      <!-- Opción 1: Importar -->
      {#if !collisionDetected.is_managed}
        <div class="coll-option coll-option-import">
          <div class="co-head">
            <span class="co-icon">⬇</span>
            <span class="co-title">Importar este pool</span>
            <span class="co-tag co-tag-accent">recomendado</span>
          </div>
          <p class="co-desc">
            Registrar el filesystem existente como pool gestionado por NimOS.
            <b>Los datos se preservan al 100%</b>.
          </p>
          <button class="btn-primary" on:click={chooseImport} disabled={processing}>
            Importar como pool
          </button>
        </div>
      {:else}
        <div class="coll-option coll-option-managed">
          <p class="co-desc">
            Este disco ya pertenece a un pool gestionado. No puedes crear otro
            pool encima sin destruir el actual primero.
          </p>
        </div>
      {/if}

      <!-- Opción 2: Destruir y continuar -->
      <div class="coll-option coll-option-destroy">
        <div class="co-head">
          <span class="co-icon co-icon-crit">⚠</span>
          <span class="co-title">Continuar destruyendo datos</span>
          <span class="co-tag co-tag-crit">irreversible</span>
        </div>
        <p class="co-desc">
          Se borrarán <b>todos los datos</b> del filesystem actual y se creará
          uno nuevo encima. Esta acción <b>no se puede deshacer</b>.
        </p>
        <div class="confirm-block">
          <div class="confirm-label">Escribe <b>DESTRUIR</b> para confirmar:</div>
          <input
            class="confirm-input"
            class:ok={collisionAck === 'DESTRUIR'}
            type="text"
            bind:value={collisionAck}
            placeholder="DESTRUIR"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="off"
            spellcheck="false"
            disabled={processing}
          />
        </div>
        <button
          class="btn-danger"
          on:click={chooseDestroyAndContinue}
          disabled={processing || collisionAck !== 'DESTRUIR'}
        >
          {processing ? 'Procesando...' : 'Destruir y crear pool nuevo'}
        </button>
      </div>

      {#if errorMsg}
        <div class="alert-crit">{errorMsg}</div>
      {/if}
    </div>

    <div class="coll-foot">
      <div class="spacer"></div>
      <button class="btn-secondary" on:click={dismissCollision} disabled={processing}>
        Cancelar
      </button>
    </div>
  </div>
{/if}

<style>
  /* ═══════════════════════════════════════════════════════════════
     CreatePoolWizard · estilos Design System Beta 8.1
     Tokens semánticos del proyecto (definidos en app.css):
       --ink, --ink-dim, --ink-mute, --ink-trace
       --bg-window, --bg-main, --bg-card, --bg-inner
       --line, --side-hover, --signal, --warn, --crit
     ═══════════════════════════════════════════════════════════════ */

  /* ─── Labels y descripciones de paso (mismo lenguaje que otros wizards) ─── */
  .step-label {
    font-size: 10px;
    color: var(--ink-trace);
    text-transform: uppercase;
    letter-spacing: 1.5px;
    font-weight: 600;
    margin-bottom: 2px;
    font-family: var(--font-sans);
  }
  .step-desc {
    font-size: 12px;
    color: var(--ink-dim);
    line-height: 1.6;
    font-family: var(--font-sans);
  }
  .step-desc :global(b) {
    color: var(--ink);
    font-weight: 600;
    font-family: var(--font-mono);
  }

  /* ─── Cards de impacto (resumen k/v) ─── */
  .impact-card {
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .impact-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    font-size: 12px;
  }
  .impact-row .k {
    color: var(--ink-mute);
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  .impact-row .v {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    text-align: right;
    word-break: break-all;
  }
  .impact-row .v.sm { font-size: 10px; }
  .impact-row .v.accent { color: var(--signal); font-weight: 600; }
  .impact-row .v.warn   { color: var(--warn);   font-weight: 600; }
  .impact-row .v.crit   { color: var(--crit);   font-weight: 600; }

  /* ─── Chips de discos (paso confirmación) ─── */
  .disk-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 4px;
  }
  .disk-chip {
    background: var(--bg-inner);
    border: 1px solid var(--line);
    border-radius: 5px;
    padding: 4px 8px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-dim);
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .disk-chip .cube {
    width: 6px;
    height: 6px;
    background: var(--nim-folder, #ff9c5a);
    border-radius: 1px;
  }

  /* ─── Alerts ─── */
  .alert-warn {
    background: rgba(251, 191, 36, 0.06);
    border-left: 3px solid var(--warn);
    padding: 10px 12px;
    border-radius: 4px;
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.5;
    font-family: var(--font-sans);
  }
  .alert-warn :global(b) { color: var(--warn); font-weight: 600; }

  .alert-crit {
    background: rgba(248, 113, 113, 0.06);
    border-left: 3px solid var(--crit);
    padding: 12px 14px;
    border-radius: 4px;
    font-size: 11px;
    color: var(--crit);
    line-height: 1.6;
    font-family: var(--font-sans);
  }

  /* ─── Bullets ─── */
  .bullets {
    list-style: none;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
  }
  .bullets li {
    font-size: 12px;
    color: var(--ink-dim);
    padding-left: 18px;
    position: relative;
    line-height: 1.5;
    font-family: var(--font-sans);
  }
  .bullets li::before {
    content: '✓';
    position: absolute;
    left: 4px;
    color: var(--signal);
    font-weight: 700;
  }
  .bullets li :global(b) { color: var(--ink); font-weight: 600; }

  /* ─── Paso 2 · Lista de discos ─── */
  .disk-list {
    display: flex;
    flex-direction: column;
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
    overflow: hidden;
  }
  .disk-row {
    display: grid;
    grid-template-columns: 22px 1fr auto auto;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
    background: transparent;
    border: none;
    border-bottom: 1px solid var(--line);
    cursor: pointer;
    text-align: left;
    font-family: var(--font-sans);
    transition: background 0.1s;
  }
  .disk-row:last-child { border-bottom: none; }
  .disk-row:hover { background: var(--side-hover); }
  .disk-row.selected { background: rgba(0, 255, 159, 0.04); }
  .disk-row.has-orphan { border-left: 3px solid var(--warn); padding-left: 11px; }

  .dr-check {
    width: 18px;
    height: 18px;
    border: 1px solid var(--line);
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--signal);
  }
  .dr-check svg { width: 12px; height: 12px; }
  .disk-row.selected .dr-check {
    border-color: var(--signal);
    background: rgba(0, 255, 159, 0.08);
  }

  .dr-info {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .dr-path {
    font-size: 12px;
    color: var(--ink);
    font-family: var(--font-mono);
    font-weight: 500;
  }
  .dr-model {
    font-size: 10px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
  }
  .dr-orphan-hint {
    font-size: 10px;
    color: var(--warn);
    font-family: var(--font-mono);
    margin-top: 2px;
  }
  .dr-size {
    font-size: 12px;
    color: var(--ink);
    font-family: var(--font-mono);
    font-weight: 500;
  }
  .dr-tags {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  /* Chip propio (sustituye a Badge para consistencia con tokens nuevos) */
  .chip {
    font-size: 9px;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: var(--font-mono);
    font-weight: 600;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    border: 1px solid var(--line);
  }
  .chip-default { color: var(--ink-mute); background: var(--bg-inner); }
  .chip-info    { color: var(--nim-remote, #4db8ff); background: rgba(77, 184, 255, 0.08); border-color: rgba(77, 184, 255, 0.2); }
  .chip-warn    { color: var(--warn);                 background: rgba(251, 191, 36, 0.08); border-color: rgba(251, 191, 36, 0.2); }

  /* ─── Layout preview (paso 2) ─── */
  .layout-preview {
    padding: 12px 14px;
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-left: 3px solid var(--signal);
    border-radius: 4px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .lp-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }
  .lp-label {
    font-size: 9px;
    color: var(--ink-trace);
    letter-spacing: 1.5px;
    text-transform: uppercase;
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .lp-name {
    font-size: 14px;
    color: var(--signal);
    font-weight: 600;
    font-family: var(--font-mono);
    letter-spacing: 0.5px;
  }
  .lp-desc {
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.5;
    font-family: var(--font-sans);
  }
  .lp-cap {
    display: flex;
    gap: 6px;
    align-items: baseline;
    padding-top: 6px;
    border-top: 1px solid var(--line);
    margin-top: 2px;
  }
  .lp-cap-label {
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.5px;
    text-transform: uppercase;
    font-family: var(--font-mono);
  }
  .lp-cap-val {
    font-size: 14px;
    color: var(--ink);
    font-weight: 700;
    font-family: var(--font-mono);
    margin-left: auto;
  }

  /* ─── Elección de layout (3 discos: raid1 vs raid1c3) ─── */
  .layout-choices {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .lc-title {
    font-size: 9px;
    color: var(--ink-trace);
    letter-spacing: 1.5px;
    text-transform: uppercase;
    font-family: var(--font-mono);
    font-weight: 600;
    margin-bottom: 2px;
  }
  .lc-option {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: 100%;
    text-align: left;
    padding: 12px 14px;
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
    cursor: pointer;
    transition: border-color 0.12s ease, background 0.12s ease;
  }
  .lc-option:hover {
    border-color: var(--ink-mute);
  }
  .lc-option.selected {
    border-color: var(--signal);
    border-left: 3px solid var(--signal);
    background: var(--bg-card-hl, var(--bg-card));
  }
  .lc-radio {
    width: 14px;
    height: 14px;
    border-radius: 50%;
    border: 2px solid var(--ink-mute);
    flex-shrink: 0;
    margin-top: 2px;
    transition: border-color 0.12s ease;
  }
  .lc-radio.on {
    border-color: var(--signal);
    background:
      radial-gradient(circle at center, var(--signal) 0 4px, transparent 5px);
  }
  .lc-body {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1;
  }
  .lc-head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    flex-wrap: wrap;
  }
  .lc-name {
    font-size: 14px;
    color: var(--signal);
    font-weight: 600;
    font-family: var(--font-mono);
    letter-spacing: 0.5px;
  }
  .lc-tol {
    font-size: 10px;
    color: var(--ink-dim);
    font-family: var(--font-mono);
    letter-spacing: 0.3px;
  }
  .lc-rec {
    font-size: 8px;
    color: var(--signal);
    border: 1px solid var(--signal);
    border-radius: 3px;
    padding: 1px 5px;
    letter-spacing: 1px;
    text-transform: uppercase;
    font-family: var(--font-mono);
  }
  .lc-desc {
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.5;
    font-family: var(--font-sans);
  }
  .lc-cap {
    font-size: 11px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    margin-top: 2px;
  }
  .lc-cap b {
    color: var(--ink);
    font-weight: 700;
  }

  /* ─── Paso 3 · Input nombre ─── */
  .name-input {
    width: 100%;
    padding: 10px 14px;
    border-radius: 6px;
    background: var(--bg-inner);
    border: 1px solid var(--line);
    color: var(--ink);
    font-size: 14px;
    font-family: var(--font-mono);
    font-weight: 500;
    letter-spacing: 0.5px;
    outline: none;
    transition: border-color 0.15s, background 0.15s;
  }
  .name-input:focus {
    border-color: var(--signal);
    background: rgba(0, 255, 159, 0.03);
  }
  .name-input.err {
    border-color: var(--crit);
    background: rgba(248, 113, 113, 0.03);
  }
  .name-input.ok {
    border-color: var(--signal);
  }

  .name-hint {
    font-size: 10px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    letter-spacing: 0.3px;
    margin-top: -8px;
  }
  .name-hint.err { color: var(--crit); }

  /* ─── Confirm input (typed-confirm) ─── */
  .confirm-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .confirm-label {
    font-size: 11px;
    color: var(--ink-dim);
    font-family: var(--font-sans);
  }
  .confirm-label :global(b) {
    color: var(--signal);
    font-family: var(--font-mono);
    font-weight: 700;
    letter-spacing: 1px;
  }
  .confirm-input {
    padding: 9px 12px;
    border-radius: 6px;
    background: var(--bg-inner);
    border: 1px solid var(--line);
    color: var(--ink);
    font-size: 13px;
    font-family: var(--font-mono);
    font-weight: 600;
    letter-spacing: 1.5px;
    outline: none;
    transition: border-color 0.2s, background 0.2s, color 0.2s;
  }
  .confirm-input:focus {
    border-color: var(--signal);
    background: rgba(0, 255, 159, 0.03);
  }
  .confirm-input.ok {
    border-color: var(--signal);
    color: var(--signal);
  }
  .confirm-input:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* ═══════════════════════════════════════════════════════════════
     Diálogo de colisión (Bloque C3.4) — modal independiente
     ═══════════════════════════════════════════════════════════════ */
  .coll-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.7);
    z-index: 200;
  }
  .coll-modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 201;
    width: 560px;
    background: var(--bg-window);
    border: 1px solid var(--line);
    border-radius: 10px;
    box-shadow: 0 24px 60px rgba(0, 0, 0, 0.55);
    display: flex;
    flex-direction: column;
    max-height: calc(100vh - 80px);
    overflow: hidden;
    animation: collIn 0.18s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes collIn {
    from { opacity: 0; transform: translate(-50%, -50%) translateY(-8px) scale(0.98); }
    to   { opacity: 1; transform: translate(-50%, -50%) translateY(0) scale(1); }
  }

  .coll-head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--line);
    flex-shrink: 0;
  }
  .coll-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
    letter-spacing: -0.1px;
  }
  .coll-tag {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--warn);
    margin-left: 4px;
    font-weight: 500;
  }
  .coll-close {
    margin-left: auto;
    width: 22px;
    height: 22px;
    background: transparent;
    border: none;
    border-radius: 4px;
    color: var(--ink-mute);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s, color 0.12s;
    padding: 0;
  }
  .coll-close svg { width: 12px; height: 12px; }
  .coll-close:hover {
    background: var(--side-hover);
    color: var(--ink);
  }

  .coll-strip {
    height: 2px;
    background: var(--warn);
    box-shadow: 0 0 6px rgba(251, 191, 36, 0.45);
    flex-shrink: 0;
  }

  .coll-body {
    padding: 20px 22px;
    flex: 1;
    overflow-y: auto;
    background: var(--bg-main);
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .coll-foot {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--line);
    flex-shrink: 0;
    background: var(--bg-window);
  }
  .spacer { flex: 1; }

  /* Opciones del diálogo */
  .coll-option {
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 12px 14px;
    background: var(--bg-card);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .coll-option-import { border-left: 3px solid var(--signal); }
  .coll-option-destroy { border-left: 3px solid var(--crit); }
  .coll-option-managed { border-left: 3px solid var(--ink-trace); }

  .co-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .co-icon {
    font-size: 14px;
    color: var(--signal);
  }
  .co-icon-crit { color: var(--crit); }
  .co-title {
    font-size: 12px;
    color: var(--ink);
    font-weight: 600;
    font-family: var(--font-sans);
  }
  .co-tag {
    margin-left: auto;
    font-size: 9px;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    font-weight: 600;
  }
  .co-tag-accent {
    background: rgba(0, 255, 159, 0.1);
    color: var(--signal);
    border: 1px solid rgba(0, 255, 159, 0.2);
  }
  .co-tag-crit {
    background: rgba(248, 113, 113, 0.1);
    color: var(--crit);
    border: 1px solid rgba(248, 113, 113, 0.2);
  }
  .co-desc {
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.5;
    font-family: var(--font-sans);
    margin: 0;
  }
  .co-desc :global(b) { color: var(--ink); font-weight: 600; }

  /* ─── Botones ─── */
  .btn-secondary {
    padding: 7px 14px;
    border-radius: 6px;
    border: 1px solid var(--line);
    background: var(--bg-card);
    color: var(--ink-dim);
    font-size: 11px;
    font-weight: 500;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .btn-secondary:hover:not(:disabled) {
    color: var(--ink);
    background: var(--side-hover);
  }
  .btn-secondary:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
  .btn-primary {
    padding: 7px 14px;
    border-radius: 6px;
    border: none;
    background: var(--signal);
    color: var(--bg-window);
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: filter 0.12s;
    align-self: flex-start;
  }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-primary:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    filter: none;
  }
  .btn-danger {
    padding: 7px 14px;
    border-radius: 6px;
    border: none;
    background: var(--crit);
    color: var(--bg-window);
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: filter 0.12s;
    align-self: flex-start;
  }
  .btn-danger:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-danger:disabled {
    opacity: 0.35;
    cursor: not-allowed;
    filter: none;
  }
</style>
