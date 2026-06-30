<script>
  /**
   * UpgradeRaidModal · Convierte un pool single a RAID1 añadiendo un disco.
   * ─────────────────────────────────────────────────────────────────────────
   * El caso de uso: empezaste con un disco, compraste otro, quieres espejo.
   * BTRFS lo soporta en caliente sin perder datos:
   *   1. btrfs device add  (el disco nuevo entra al pool)
   *   2. btrfs balance -dconvert=raid1 -mconvert=raid1  (replica los datos)
   *
   * El convert es ASYNC en el backend (el balance puede tardar de minutos a
   * horas según los datos). Este modal acompaña todo el proceso con polling
   * del progreso real (btrfs balance status) y no se puede cerrar a mitad
   * de conversión por accidente.
   *
   * Props:
   *   · pool          — pool managed con profile 'single'
   *   · eligibleDisks — discos libres (de /v2/disks .eligible)
   *
   * Eventos:
   *   · done   — conversión completada, el padre refresca estado
   *   · cancel — usuario cierra sin convertir (solo antes de empezar)
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import * as api from './api.js';

  export let pool;
  export let eligibleDisks = [];

  const dispatch = createEventDispatcher();

  // ─── Estado del flujo ───
  // fase: 'select' → 'adding' → 'converting' → 'done' | 'error'
  let phase = 'select';
  let selectedDisk = null;
  let error = '';
  let percent = 0;
  let balanceDetail = '';
  let pollTimer = null;

  // ¿El pool ya tiene 2+ discos? Entonces el disco nuevo es opcional
  // (se puede convertir directamente con los que tiene).
  $: poolDiskCount = pool?.devices?.length || 0;
  $: needsDisk = poolDiskCount < 2;
  $: canStart = phase === 'select' && (!needsDisk || selectedDisk !== null);
  $: busy = phase === 'adding' || phase === 'converting';

  function close() {
    if (busy) return; // no cerrar a media conversión
    stopPolling();
    dispatch('cancel');
  }

  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  async function start() {
    if (!canStart) return;
    error = '';

    try {
      // 1. Añadir el disco si hace falta / se eligió
      if (selectedDisk) {
        phase = 'adding';
        // Asegurar que el disco está registrado en storage_devices antes de
        // referenciarlo por path (un disco recién conectado puede no estarlo).
        try { await api.scanDisks(); } catch (e) { /* el scan es best-effort */ }
        const devPath = selectedDisk.path || `/dev/${selectedDisk.name}`;
        const addOp = await api.addDeviceToPool(pool.id, devPath);
        // Si el add no completó (btrfs device add falló), abortar aquí con el
        // error real en vez de encadenar un convert que vería 1 disco.
        if (addOp && addOp.status === 'failed') {
          throw new Error(addOp.error || 'No se pudo añadir el disco al pool');
        }
      }

      // 2. Lanzar la conversión (async: devuelve op in_progress al instante)
      phase = 'converting';
      percent = 0;
      await api.convertPoolProfile(pool.id, 'raid1');

      // 3. Polling del progreso hasta que el balance termine
      pollTimer = setInterval(checkProgress, 2500);
    } catch (e) {
      stopPolling();
      error = e.message || 'Error en la conversión';
      phase = 'error';
    }
  }

  async function checkProgress() {
    try {
      const st = await api.getBalanceStatus(pool.id);
      if (st?.active) {
        if (st.percent_done > percent) percent = st.percent_done;
        balanceDetail = st.detail || '';
        return; // sigue en marcha
      }

      // El balance ya no está activo → ¿completó o falló? Miramos la op.
      stopPolling();
      const ops = await api.getOperations({ poolId: pool.id, limit: 1 });
      const last = Array.isArray(ops) ? ops[0] : (ops?.operations?.[0] || null);
      if (last && last.type === 'convert_profile' && last.status === 'failed') {
        error = last.error || 'La conversión falló (ver logs del daemon)';
        phase = 'error';
        return;
      }
      percent = 100;
      phase = 'done';
    } catch (e) {
      // Error transitorio de red: no abortar el polling por un fallo puntual.
      console.warn('balance-status poll:', e);
    }
  }

  function finish() {
    stopPolling();
    dispatch('done');
  }

  function fmtSize(bytes) {
    if (!bytes) return '—';
    const gb = bytes / 1e9;
    return gb >= 1000 ? `${(gb / 1000).toFixed(1)} TB` : `${gb.toFixed(1)} GB`;
  }

  function onKeydown(e) {
    if (e.key === 'Escape') close();
  }
  onMount(() => window.addEventListener('keydown', onKeydown));
  onDestroy(() => { window.removeEventListener('keydown', onKeydown); stopPolling(); });
</script>

<div class="backdrop" on:click|self={close} role="presentation"></div>

<div class="modal" role="dialog" aria-modal="true" aria-labelledby="upgrade-raid-title">
  <!-- HEAD -->
  <div class="modal-head">
    <div class="modal-title" id="upgrade-raid-title">
      Mejorar a RAID1
      <span class="modal-tag">"{pool?.name}"</span>
    </div>
    <button class="modal-close" on:click={close} title="Cerrar" aria-label="Cerrar" disabled={busy}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>

  <div class="modal-strip"></div>

  <!-- BODY -->
  <div class="modal-body">
    {#if phase === 'select'}
      <div class="step-label">single → raid1 · espejo en caliente</div>
      <p class="step-desc">
        Los datos quedan <b>replicados en dos discos</b>: si uno falla, el pool
        sigue funcionando. La conversión ocurre en caliente, sin desmontar y
        <b>sin perder datos</b>. Puede tardar según la cantidad de datos.
      </p>

      <div class="impact-card">
        <div class="impact-row">
          <span class="k">pool</span>
          <span class="v">{pool?.name} · {fmtSize(pool?.devices?.[0]?.size_bytes)} usados {pool?.used_human || ''}</span>
        </div>
        <div class="impact-row">
          <span class="k">profile actual</span>
          <span class="v">single · sin redundancia</span>
        </div>
        <div class="impact-row">
          <span class="k">profile destino</span>
          <span class="v ok">raid1 · espejo (aguanta el fallo de 1 disco)</span>
        </div>
      </div>

      {#if needsDisk}
        <div class="field-block">
          <div class="field-label">Disco que se añadirá al espejo:</div>
          {#if eligibleDisks.length === 0}
            <div class="empty-disks">No hay discos libres disponibles.</div>
          {:else}
            <div class="disk-list">
              {#each eligibleDisks as d}
                <button
                  class="disk-option"
                  class:selected={(selectedDisk?.path || selectedDisk?.name) === (d.path || d.name)}
                  on:click={() => selectedDisk = d}
                >
                  <span class="disk-radio" aria-hidden="true"></span>
                  <span class="disk-model">{d.model || d.name || d.path}</span>
                  <span class="disk-meta">{d.path || d.device} · {fmtSize(d.size_bytes || d.size)}</span>
                </button>
              {/each}
            </div>
            <p class="disk-warning">
              ⚠ El disco seleccionado se <b>formateará</b> al entrar al pool.
            </p>
          {/if}
        </div>
      {/if}
    {/if}

    {#if phase === 'adding'}
      <div class="progress-block">
        <div class="step-label">Añadiendo disco al pool…</div>
        <div class="bar indeterminate"><div class="fill"></div></div>
      </div>
    {/if}

    {#if phase === 'converting'}
      <div class="progress-block">
        <div class="step-label">Replicando datos (balance raid1)…</div>
        <div class="bar"><div class="fill" style="width: {percent}%"></div></div>
        <div class="progress-meta">
          <span class="pct">{percent.toFixed(0)}%</span>
          {#if balanceDetail}<span class="detail">{balanceDetail}</span>{/if}
        </div>
        <p class="step-desc dim">
          Puedes cerrar la pestaña: la conversión sigue en el servidor.
          El pool permanece usable mientras tanto.
        </p>
      </div>
    {/if}

    {#if phase === 'done'}
      <div class="result ok-result">
        <div class="result-icon">✓</div>
        <div class="result-text">
          <b>{pool?.name}</b> ahora es <b>RAID1</b>. Los datos están replicados
          en {poolDiskCount >= 2 ? poolDiskCount : 2} discos.
        </div>
      </div>
    {/if}

    {#if phase === 'error'}
      <div class="result err-result">
        <div class="result-icon">✗</div>
        <div class="result-text">{error}</div>
      </div>
    {/if}
  </div>

  <!-- FOOT -->
  <div class="modal-foot">
    {#if phase === 'select'}
      <button class="btn ghost" on:click={close}>Cancelar</button>
      <button class="btn primary" disabled={!canStart} on:click={start}>
        Convertir a RAID1
      </button>
    {:else if phase === 'done'}
      <button class="btn primary" on:click={finish}>Listo</button>
    {:else if phase === 'error'}
      <button class="btn ghost" on:click={close}>Cerrar</button>
    {:else}
      <span class="foot-note">Conversión en curso — no apagues el equipo.</span>
    {/if}
  </div>
</div>

<style>
  /* ─── Frame (lenguaje del Design System, mismo que ImportOrphanModal) ─── */
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.65);
    z-index: 100;
  }
  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 101;
    width: 560px;
    background: var(--bg-window);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 24px 60px rgba(0, 0, 0, 0.55);
    display: flex;
    flex-direction: column;
    max-height: calc(100vh - 80px);
    overflow: hidden;
    animation: modalIn 0.18s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes modalIn {
    from { opacity: 0; transform: translate(-50%, -50%) translateY(-8px) scale(0.98); }
    to   { opacity: 1; transform: translate(-50%, -50%) translateY(0) scale(1); }
  }

  .modal-head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--line);
    flex-shrink: 0;
  }
  .modal-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
    letter-spacing: -0.1px;
  }
  .modal-tag {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--signal);
    margin-left: 4px;
    font-weight: 500;
  }
  .modal-close {
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
  .modal-close svg { width: 12px; height: 12px; }
  .modal-close:hover:not(:disabled) {
    background: var(--side-hover);
    color: var(--ink);
  }
  .modal-close:disabled { opacity: 0.3; cursor: not-allowed; }

  .modal-strip {
    height: 2px;
    background: var(--signal);
    box-shadow: 0 0 6px rgba(0, 255, 159, 0.45);
    flex-shrink: 0;
  }

  .modal-body {
    padding: 20px 22px;
    flex: 1;
    overflow-y: auto;
    background: var(--bg-main);
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .step-label {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 1.2px;
    text-transform: uppercase;
    color: var(--signal);
  }
  .step-desc {
    font-size: 12px;
    line-height: 1.55;
    color: var(--ink-soft, var(--ink-mute));
    margin: 0;
  }
  .step-desc.dim { color: var(--ink-mute); font-size: 11px; }
  .step-desc b { color: var(--ink); font-weight: 600; }

  /* Impact card (resumen del cambio) */
  .impact-card {
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--bg-card, rgba(255, 255, 255, 0.02));
    padding: 4px 0;
  }
  .impact-row {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 16px;
    padding: 7px 14px;
  }
  .impact-row + .impact-row { border-top: 1px solid var(--line); }
  .impact-row .k {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.6px;
    text-transform: uppercase;
    color: var(--ink-mute);
    flex-shrink: 0;
  }
  .impact-row .v { font-size: 12px; color: var(--ink); text-align: right; }
  .impact-row .v.ok { color: var(--signal); }

  /* Selección de disco */
  .field-block { display: flex; flex-direction: column; gap: 8px; }
  .field-label {
    font-size: 11px;
    font-weight: 600;
    color: var(--ink);
  }
  .disk-list { display: flex; flex-direction: column; gap: 6px; }
  .disk-option {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
    background: var(--bg-card, rgba(255, 255, 255, 0.02));
    border: 1px solid var(--line);
    border-radius: 8px;
    cursor: pointer;
    text-align: left;
    transition: border-color 0.12s, background 0.12s;
    color: var(--ink);
  }
  .disk-option:hover { border-color: var(--ink-mute); }
  .disk-option.selected {
    border-color: var(--signal);
    background: rgba(0, 255, 159, 0.05);
  }
  .disk-radio {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    border: 1.5px solid var(--ink-mute);
    flex-shrink: 0;
    transition: border-color 0.12s, box-shadow 0.12s;
  }
  .disk-option.selected .disk-radio {
    border-color: var(--signal);
    box-shadow: inset 0 0 0 3px var(--signal);
  }
  .disk-model { font-size: 12px; font-weight: 600; }
  .disk-meta {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--ink-mute);
  }
  .disk-warning {
    font-size: 11px;
    color: var(--warn, #e8b341);
    margin: 0;
  }
  .empty-disks {
    font-size: 12px;
    color: var(--ink-mute);
    padding: 14px;
    border: 1px dashed var(--line);
    border-radius: 8px;
    text-align: center;
  }

  /* Progreso */
  .progress-block { display: flex; flex-direction: column; gap: 12px; }
  .bar {
    height: 6px;
    background: var(--line);
    border-radius: 3px;
    overflow: hidden;
  }
  .bar .fill {
    height: 100%;
    background: var(--signal);
    border-radius: 3px;
    box-shadow: 0 0 8px rgba(0, 255, 159, 0.5);
    transition: width 0.6s ease;
  }
  .bar.indeterminate .fill {
    width: 35%;
    animation: slide 1.1s ease-in-out infinite alternate;
  }
  @keyframes slide {
    from { margin-left: 0; }
    to   { margin-left: 65%; }
  }
  .progress-meta {
    display: flex;
    align-items: baseline;
    gap: 12px;
  }
  .pct {
    font-family: var(--font-mono);
    font-size: 18px;
    font-weight: 700;
    color: var(--signal);
  }
  .detail {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-mute);
  }

  /* Resultado */
  .result {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 16px;
    border-radius: 8px;
    border: 1px solid var(--line);
  }
  .ok-result { border-color: rgba(0, 255, 159, 0.3); background: rgba(0, 255, 159, 0.04); }
  .err-result { border-color: rgba(255, 90, 90, 0.35); background: rgba(255, 90, 90, 0.05); }
  .result-icon {
    font-size: 18px;
    font-weight: 700;
  }
  .ok-result .result-icon { color: var(--signal); }
  .err-result .result-icon { color: #ff5a5a; }
  .result-text { font-size: 12px; line-height: 1.5; color: var(--ink); }

  /* FOOT */
  .modal-foot {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 10px;
    padding: 14px 16px;
    border-top: 1px solid var(--line);
    flex-shrink: 0;
  }
  .foot-note {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.4px;
  }
  .btn {
    font-size: 12px;
    font-weight: 600;
    padding: 7px 16px;
    border-radius: 7px;
    cursor: pointer;
    border: 1px solid var(--line);
    transition: opacity 0.12s, background 0.12s;
  }
  .btn.ghost {
    background: transparent;
    color: var(--ink-mute);
  }
  .btn.ghost:hover { color: var(--ink); background: var(--side-hover); }
  .btn.primary {
    background: var(--signal);
    border-color: var(--signal);
    color: #06140d;
  }
  .btn.primary:hover:not(:disabled) { opacity: 0.88; }
  .btn.primary:disabled { opacity: 0.35; cursor: not-allowed; }
</style>
