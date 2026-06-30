<script>
  /**
   * DestroyOrphanModal · Destruye un filesystem BTRFS huérfano (irreversible).
   * ──────────────────────────────────────────────────────────────────────────
   * Itera sobre los devices del filesystem y aplica `wipe(force=true)` a cada
   * uno. El force=true es necesario porque el preflight bloquea wipe sobre
   * discos con BTRFS — pero el usuario ha confirmado tipeando "DESTRUIR".
   *
   * NUNCA toca pools managed: el padre solo abre este modal sobre orphans.
   *
   * Props:
   *   · fs — ObservedBtrfs (uuid, label, used_bytes, devices)
   *
   * Eventos:
   *   · done   — destrucción completa, el padre refresca estado
   *   · cancel — usuario cierra sin destruir
   *
   * NOTA Beta 8.1: este modal usa el patrón visual nuevo del Design System
   * (tokens --ink, --bg-window, --signal/--crit, etc.). La LÓGICA es idéntica
   * a la versión anterior — solo cambia presentación.
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import { fmtBytes } from './formatters.js';
  import * as api from './api.js';

  export let fs;

  const dispatch = createEventDispatcher();

  let confirmText = '';
  let processing = false;
  let error = '';

  function close() {
    if (processing) return;
    dispatch('cancel');
  }

  async function submit() {
    if (!fs) return;
    if (confirmText !== 'DESTRUIR') {
      error = 'Escribe "DESTRUIR" para confirmar';
      return;
    }
    processing = true;
    error = '';
    try {
      const paths = (fs.devices || []).map(d => d.path).filter(Boolean);
      for (const path of paths) {
        await api.wipeDisk(path, { force: true });
      }
      dispatch('done');
    } catch (e) {
      error = e.message || 'Error desconocido al destruir';
      processing = false;
    }
  }

  function onKeydown(e) {
    if (e.key === 'Escape') close();
  }
  onMount(() => window.addEventListener('keydown', onKeydown));
  onDestroy(() => window.removeEventListener('keydown', onKeydown));

  $: deviceList = (fs?.devices || []).map(d => d.path).filter(Boolean);
</script>

<div class="backdrop" on:click|self={close} role="presentation"></div>

<div class="modal" role="dialog" aria-modal="true" aria-labelledby="orphan-destroy-title">
  <!-- HEAD -->
  <div class="modal-head">
    <div class="modal-title" id="orphan-destroy-title">
      Destruir filesystem
      <span class="modal-tag">"{fs?.label || fs?.uuid?.slice(0, 8) || 'orphan'}"</span>
    </div>
    <button class="modal-close" on:click={close} title="Cerrar" aria-label="Cerrar">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>

  <!-- Barra fija crit (no hay pasos pero el patrón visual lo conserva) -->
  <div class="modal-strip"></div>

  <!-- BODY -->
  <div class="modal-body">
    <div class="step-label">Confirmación destructiva</div>
    <p class="step-desc">
      Vas a <b>destruir permanentemente</b> el filesystem BTRFS y todos sus datos.
      Los discos quedarán vacíos. Esta operación <b>no se puede deshacer</b>.
    </p>

    <div class="impact-card">
      <div class="impact-row">
        <span class="k">filesystem</span>
        <span class="v">{fs?.label || '(sin label)'}</span>
      </div>
      <div class="impact-row">
        <span class="k">uuid</span>
        <span class="v sm">{fs?.uuid || '—'}</span>
      </div>
      {#if fs?.used_bytes > 0}
        <div class="impact-row">
          <span class="k">datos</span>
          <span class="v crit">{fmtBytes(fs.used_bytes)} se perderán</span>
        </div>
      {/if}
      <div class="impact-row">
        <span class="k">discos afectados</span>
        <span class="v">{deviceList.join(', ') || '—'}</span>
      </div>
    </div>

    <div class="confirm-block">
      <div class="confirm-label">
        Escribe <b>DESTRUIR</b> para confirmar:
      </div>
      <input
        class="confirm-input"
        class:ok={confirmText === 'DESTRUIR'}
        type="text"
        bind:value={confirmText}
        placeholder="DESTRUIR"
        autocomplete="off"
        autocorrect="off"
        autocapitalize="off"
        spellcheck="false"
        disabled={processing}
      />
    </div>

    {#if error}
      <div class="alert-crit">{error}</div>
    {/if}
  </div>

  <!-- FOOT -->
  <div class="modal-foot">
    <div class="spacer"></div>
    <button class="btn-secondary" on:click={close} disabled={processing}>
      Cancelar
    </button>
    <button
      class="btn-danger"
      on:click={submit}
      disabled={processing || confirmText !== 'DESTRUIR'}
    >
      {processing ? 'Destruyendo...' : 'Destruir'}
    </button>
  </div>
</div>

<style>
  /* ─── Frame (mismo lenguaje que WizardFrame pero sin progress bar) ─── */
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

  /* HEAD */
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
    color: var(--crit);
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
  .modal-close:hover {
    background: var(--side-hover);
    color: var(--ink);
  }

  /* Strip fija (mismo look que el progress bar pero sin animación) */
  .modal-strip {
    height: 2px;
    background: var(--crit);
    box-shadow: 0 0 6px rgba(248, 113, 113, 0.45);
    flex-shrink: 0;
  }

  /* BODY */
  .modal-body {
    padding: 20px 22px;
    flex: 1;
    overflow-y: auto;
    background: var(--bg-main);
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  /* FOOT */
  .modal-foot {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--line);
    flex-shrink: 0;
    background: var(--bg-window);
  }
  .spacer { flex: 1; }

  /* ─── Contenido (mismo lenguaje semántico que el Design System Beta 8.1) ─── */
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
  .impact-row .v.crit { color: var(--crit); font-weight: 600; }

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
    color: var(--crit);
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
    border-color: var(--crit);
    background: rgba(248, 113, 113, 0.04);
  }
  .confirm-input.ok {
    border-color: var(--crit);
    color: var(--crit);
  }
  .confirm-input:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* Botones — mismos que WizardFrame para consistencia */
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
  }
  .btn-danger:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-danger:disabled {
    opacity: 0.35;
    cursor: not-allowed;
    filter: none;
  }
</style>
