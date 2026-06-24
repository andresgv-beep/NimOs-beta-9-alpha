<script>
  /**
   * ExportPoolWizard · Wizard para desmontar (export) un pool BTRFS
   * ─────────────────────────────────────────────────────────────────
   * "Desmontar" en la UI; backend lo llama export (POST /v2/pool/export).
   * En Beta 8.1, export = umount + remove_from_db. El pool pasa a estado
   * "orphan filesystem" en la sección Observados — los datos siguen en
   * los discos físicos pero el pool deja de ser managed.
   *
   * Flow:
   *   1. Detección — fetch dependencias (transitorio, no visible normalmente)
   *   2. Servicios — lista con LED + botón → NimHealth; polling 3s
   *   3. Confirmación — bullets + typed-confirm "DESMONTAR"
   *
   * On confirm: POST /api/storage/v2/pool/export { name }
   * Emits 'done' on success · 'cancel' if user closes.
   *
   * NOTA Beta 8.1: este wizard usa el frame visual nuevo (WizardFrame) del
   * Design System Beta 8.1. La LÓGICA es idéntica a la versión anterior —
   * solo cambia presentación.
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import { token } from '$lib/stores/auth.js';
  import { openWindow } from '$lib/stores/windows.js';
  import WizardFrame from '$lib/ui/WizardFrame.svelte';

  export let poolName = '';

  const dispatch = createEventDispatcher();

  let step = 1;
  let loading = true;
  let deps = [];
  let pollInterval = null;
  let confirmInput = '';
  let processing = false;
  let errorMsg = '';

  // ─── Derived ───
  $: allStopped = deps.length === 0 || deps.every(d => d.status === 'stopped' || d.status === 'exited');
  $: canAdvance = step === 1 ? false
                : step === 2 ? allStopped
                : step === 3 ? confirmInput.trim() === 'DESMONTAR' && !processing
                : false;

  // ─── Fetch dependencies ───
  async function fetchDeps() {
    try {
      const res = await fetch(`/api/services/dependencies?pool=${encodeURIComponent(poolName)}`, {
        headers: { 'Authorization': `Bearer ${$token}` },
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      deps = data.dependencies || [];
      return true;
    } catch (err) {
      console.error('fetchDeps error:', err);
      errorMsg = 'No se pudo consultar dependencias del pool.';
      return false;
    }
  }

  // ─── Handlers ───
  async function handleNext() {
    if (step === 2) {
      step = 3;
      stopPolling();
      return;
    }
    if (step === 3) {
      await submitExport();
      return;
    }
  }

  function handleBack() {
    if (step === 2) return;
    if (step === 3) {
      step = deps.length > 0 ? 2 : 1;
      if (step === 2) startPolling();
    }
  }

  function handleCancel() {
    stopPolling();
    dispatch('cancel');
  }

  function openNimHealth() {
    openWindow('nimhealth');
  }

  // ─── Polling (solo paso 2) ───
  function startPolling() {
    stopPolling();
    pollInterval = setInterval(fetchDeps, 3000);
  }
  function stopPolling() {
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }
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

  // ─── Export real ───
  async function submitExport() {
    processing = true;
    errorMsg = '';
    try {
      const res = await fetch('/api/storage/v2/pool/export', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${$token}`,
        },
        body: JSON.stringify({ name: poolName }),
      });
      try {
        await unwrapV2(res, 'export');
        processing = false;
        dispatch('done');
      } catch (e) {
        if (e.code === 'services_active') {
          const services = e.details?.services || [];
          errorMsg = `Algún servicio se ha levantado: ${services.join(', ')}. Reintenta.`;
          await fetchDeps();
          step = 2;
          startPolling();
        } else {
          errorMsg = e.message || 'Error al desmontar';
        }
        processing = false;
      }
    } catch (err) {
      console.error('export error:', err);
      errorMsg = err.message || 'Error de conexión al desmontar';
      processing = false;
    }
  }

  // ─── Lifecycle ───
  onMount(async () => {
    await fetchDeps();
    loading = false;
    if (deps.length === 0) {
      step = 3;
    } else {
      step = 2;
      startPolling();
    }
  });

  onDestroy(() => {
    stopPolling();
  });

  // ─── UI helpers ───
  function statusLabel(s) {
    if (s === 'running')  return 'running';
    if (s === 'stopped')  return 'stopped';
    if (s === 'exited')   return 'stopped';
    if (s === 'starting') return 'starting';
    if (s === 'stopping') return 'stopping';
    return s || 'unknown';
  }

  function statusKind(s) {
    if (s === 'running')  return 'running';
    if (s === 'starting' || s === 'stopping') return 'transition';
    return 'stopped';
  }

  // Contadores derivados para el step 2
  $: activeCount = deps.filter(d => d.status !== 'stopped' && d.status !== 'exited').length;
</script>

<WizardFrame
  open={true}
  title="Desmontar pool"
  tag={poolName}
  tagColor="warn"
  currentStep={step === 1 ? 1 : step === 2 ? 2 : 3}
  totalSteps={3}
  {canAdvance}
  canGoBack={step === 3 && deps.length > 0 && !processing}
  nextLabel={step === 3 ? 'Desmontar pool' : 'Continuar →'}
  nextVariant={step === 3 ? 'warn' : 'warn'}
  cancelLabel={processing ? 'Procesando...' : 'Cancelar'}
  on:next={handleNext}
  on:back={handleBack}
  on:cancel={handleCancel}
>

  <!-- PASO 1 · Detección (transitorio) -->
  {#if step === 1}
    <div class="step-label">Detección</div>
    <p class="step-desc">
      Verificando qué servicios están usando el pool. Esto suele tardar
      menos de un segundo.
    </p>

    <div class="loading-box">
      <div class="spinner"></div>
      <span>Consultando daemon...</span>
    </div>

    {#if errorMsg}
      <div class="alert-crit">{errorMsg}</div>
    {/if}
  {/if}

  <!-- PASO 2 · Servicios en uso -->
  {#if step === 2}
    <div class="step-label">Servicios en uso</div>
    <p class="step-desc">
      {#if allStopped}
        Ningún servicio está usando el pool en este momento. Puedes continuar
        con el desmontaje.
      {:else}
        Detén los servicios que están usando este pool antes de desmontarlo.
        Si hay procesos activos, el desmontaje fallará.
      {/if}
    </p>

    <div class="svc-list">
      {#each deps as dep}
        {@const kind = statusKind(dep.status)}
        <div class="svc-row">
          <span class="svc-led svc-led-{kind}"></span>
          <span class="svc-name">
            {dep.app || dep.id}
            {#if dep.appId && dep.appId !== dep.app}
              <span class="svc-pool">@{poolName}</span>
            {/if}
          </span>
          <span class="svc-state svc-state-{kind}">
            {statusLabel(dep.status)}
          </span>
          {#if dep.status === 'running' || dep.status === 'starting'}
            <button class="svc-action" on:click={openNimHealth} type="button">
              → NimHealth
            </button>
          {:else}
            <span class="svc-action-empty">—</span>
          {/if}
        </div>
      {/each}
    </div>

    {#if allStopped}
      <div class="notice notice-ok">
        <b>✓ Todos los servicios detenidos.</b> Continúa cuando estés listo.
      </div>
    {:else}
      <div class="notice">
        Quedan <b>{activeCount} servicio{activeCount === 1 ? '' : 's'} activo{activeCount === 1 ? '' : 's'}</b>.
        Detenlos manualmente desde NimHealth — el wizard detectará automáticamente
        cuando estén todos parados.
      </div>
      <div class="recheck">
        <span class="recheck-spin"></span>
        <span>Re-verificando cada 3 segundos...</span>
      </div>
    {/if}

    {#if errorMsg}
      <div class="alert-crit">{errorMsg}</div>
    {/if}
  {/if}

  <!-- PASO 3 · Confirmación -->
  {#if step === 3}
    <div class="step-label">Confirmación</div>
    <p class="step-desc">
      Vas a desmontar <b>{poolName}</b>. Los datos <b>NO se borran</b>, solo
      sale del sistema gestionado de NimOS.
    </p>

    <ul class="bullets">
      <li>Los datos <b>siguen intactos</b> en los discos físicos</li>
      <li>El pool aparecerá como <b>filesystem huérfano</b> en Observados</li>
      <li>Podrás <b>reimportarlo</b> después con un solo click</li>
      <li>El acceso a <b>/nimos/pools/{poolName}</b> se cortará inmediatamente</li>
    </ul>

    <div class="confirm-block">
      <div class="confirm-label">Escribe <b>DESMONTAR</b> para confirmar:</div>
      <input
        class="confirm-input"
        class:ok={confirmInput.trim() === 'DESMONTAR'}
        type="text"
        bind:value={confirmInput}
        placeholder="DESMONTAR"
        autocomplete="off"
        autocorrect="off"
        autocapitalize="off"
        spellcheck="false"
        disabled={processing}
      />
    </div>

    {#if errorMsg}
      <div class="alert-crit">{errorMsg}</div>
    {/if}
  {/if}

</WizardFrame>

<style>
  /* ═══════════════════════════════════════════════════════════════
     ExportPoolWizard · estilos Design System Beta 8.1
     Tokens semánticos (definidos en app.css)
     ═══════════════════════════════════════════════════════════════ */

  /* ─── Labels y descripciones ─── */
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

  /* ─── Step 1 · Loading box ─── */
  .loading-box {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px 18px;
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
    font-size: 12px;
    color: var(--ink-dim);
    font-family: var(--font-mono);
  }
  .spinner {
    width: 14px;
    height: 14px;
    border: 2px solid var(--line);
    border-top-color: var(--warn);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  /* ─── Step 2 · Lista de servicios ─── */
  .svc-list {
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
    overflow: hidden;
  }
  .svc-row {
    display: grid;
    grid-template-columns: 10px 1fr auto auto;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
    border-bottom: 1px solid var(--line);
    font-size: 12px;
  }
  .svc-row:last-child { border-bottom: none; }

  .svc-led {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .svc-led-running {
    background: var(--signal);
    box-shadow: 0 0 6px rgba(0, 255, 159, 0.45);
  }
  .svc-led-transition {
    background: var(--warn);
    box-shadow: 0 0 6px rgba(251, 191, 36, 0.45);
  }
  .svc-led-stopped {
    background: var(--ink-trace);
  }

  .svc-name {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .svc-pool {
    color: var(--ink-mute);
    font-size: 10px;
    margin-left: 4px;
  }

  .svc-state {
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    font-weight: 500;
  }
  .svc-state-running    { color: var(--signal); }
  .svc-state-transition { color: var(--warn); }
  .svc-state-stopped    { color: var(--ink-mute); }

  .svc-action {
    padding: 4px 8px;
    border-radius: 4px;
    border: 1px solid var(--line);
    background: var(--bg-inner);
    color: var(--ink-dim);
    font-size: 10px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s;
  }
  .svc-action:hover {
    background: var(--side-hover);
    color: var(--ink);
    border-color: var(--warn);
  }
  .svc-action-empty {
    color: var(--ink-trace);
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 4px 8px;
  }

  /* ─── Notice (amarillo info / verde ok) ─── */
  .notice {
    background: rgba(251, 191, 36, 0.06);
    border-left: 3px solid var(--warn);
    padding: 10px 12px;
    border-radius: 4px;
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.5;
    font-family: var(--font-sans);
  }
  .notice :global(b) { color: var(--warn); font-weight: 600; }
  .notice-ok {
    background: rgba(0, 255, 159, 0.06);
    border-left-color: var(--signal);
  }
  .notice-ok :global(b) { color: var(--signal); }

  /* Re-check indicator */
  .recheck {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 10px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    padding: 2px 4px;
  }
  .recheck-spin {
    width: 8px;
    height: 8px;
    border: 1.5px solid var(--line);
    border-top-color: var(--warn);
    border-radius: 50%;
    animation: spin 1s linear infinite;
  }

  /* ─── Bullets (step 3) ─── */
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
    content: '·';
    position: absolute;
    left: 6px;
    color: var(--warn);
    font-weight: 700;
    font-size: 16px;
    line-height: 1;
  }
  .bullets li :global(b) { color: var(--ink); font-weight: 600; }

  /* ─── Confirm block (typed-confirm DESMONTAR) ─── */
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
    color: var(--warn);
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
    border-color: var(--warn);
    background: rgba(251, 191, 36, 0.03);
  }
  .confirm-input.ok {
    border-color: var(--warn);
    color: var(--warn);
  }
  .confirm-input:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* ─── Alert crítica (solo errores reales del backend) ─── */
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
</style>
