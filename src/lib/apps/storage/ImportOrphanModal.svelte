<script>
  /**
   * ImportOrphanModal · Adopta un filesystem BTRFS huérfano como pool managed.
   * ─────────────────────────────────────────────────────────────────────────
   * El filesystem ya existe en disco. Solo se registra en SQLite y se le
   * asigna un nombre managed. Los datos se preservan completamente.
   *
   * CRÍTICO: este flujo recupera pools tras un re-install de NimOS. La lógica
   * NO se debe modificar — solo presentación visual.
   *
   * Props:
   *   · fs            — ObservedBtrfs (uuid, label, profile, devices_online, ...)
   *   · suggestedName — nombre inicial sugerido para el input (opcional)
   *
   * Eventos:
   *   · done   — import exitoso, el padre refresca estado
   *   · cancel — usuario cierra sin importar
   *
   * NOTA Beta 8.1: usa el patrón visual del Design System (mismo lenguaje
   * que DestroyOrphanModal, con banda verde en lugar de roja porque es
   * acción no destructiva).
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import * as api from './api.js';

  export let fs;
  export let suggestedName = '';

  const dispatch = createEventDispatcher();

  // Backend exige: ^[a-z][a-z0-9_-]{1,31}$ — alineamos en frontend para
  // evitar errores semánticos opacos al usuario.
  let name = (suggestedName || '').toLowerCase();
  let nameError = '';
  let processing = false;
  let error = '';

  const RESERVED_NAMES = ['system', 'config', 'temp', 'swap', 'root', 'boot'];

  // ─── Validación en tiempo real ───
  $: {
    nameError = '';
    if (name.length > 0) {
      if (name.length > 32) {
        nameError = 'Máximo 32 caracteres.';
      } else if (name.length < 2) {
        nameError = 'Mínimo 2 caracteres.';
      } else if (!/^[a-z][a-z0-9_-]*$/.test(name)) {
        nameError = 'Debe empezar por letra · minúsculas, dígitos, - y _';
      } else if (RESERVED_NAMES.includes(name)) {
        nameError = `"${name}" es un nombre reservado.`;
      }
    }
  }

  $: canSubmit = name.length >= 2 && nameError === '' && !processing;

  function close() {
    if (processing) return;
    dispatch('cancel');
  }

  async function submit() {
    if (!fs || !name || processing) return;
    if (nameError !== '') return;
    processing = true;
    error = '';
    try {
      await api.importPool({ uuid: fs.uuid, name });
      dispatch('done');
    } catch (e) {
      error = e.message || 'Error desconocido al importar';
      processing = false;
    }
  }

  function onKeydown(e) {
    if (e.key === 'Escape') close();
  }
  onMount(() => window.addEventListener('keydown', onKeydown));
  onDestroy(() => window.removeEventListener('keydown', onKeydown));
</script>

<div class="backdrop" on:click|self={close} role="presentation"></div>

<div class="modal" role="dialog" aria-modal="true" aria-labelledby="import-orphan-title">
  <!-- HEAD -->
  <div class="modal-head">
    <div class="modal-title" id="import-orphan-title">
      Importar pool BTRFS
      <span class="modal-tag">"{fs?.label || fs?.uuid?.slice(0, 8) || 'orphan'}"</span>
    </div>
    <button class="modal-close" on:click={close} title="Cerrar" aria-label="Cerrar">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>

  <!-- Strip verde (no destructivo) -->
  <div class="modal-strip"></div>

  <!-- BODY -->
  <div class="modal-body">
    <div class="step-label">Adoptar filesystem existente</div>
    <p class="step-desc">
      Este filesystem se registrará en NimOS como un pool gestionado.
      <b>Los datos existentes se preservan</b> al 100% — solo se le asigna
      un nombre y se registra en el sistema.
    </p>

    <div class="impact-card">
      <div class="impact-row">
        <span class="k">uuid</span>
        <span class="v sm">{fs?.uuid || '—'}</span>
      </div>
      <div class="impact-row">
        <span class="k">label original</span>
        <span class="v">{fs?.label || '(sin label)'}</span>
      </div>
      <div class="impact-row">
        <span class="k">profile</span>
        <span class="v">BTRFS · {fs?.profile || 'single'}</span>
      </div>
      <div class="impact-row">
        <span class="k">discos</span>
        <span class="v">{fs?.devices_online || 0} dispositivo{fs?.devices_online === 1 ? '' : 's'}</span>
      </div>
    </div>

    <div class="field-block">
      <div class="field-label">Nombre del pool en NimOS:</div>
      <input
        class="name-input"
        class:err={nameError !== ''}
        class:ok={name.length >= 2 && nameError === ''}
        type="text"
        bind:value={name}
        on:input={(e) => { name = e.target.value.toLowerCase(); }}
        on:keydown={(e) => e.key === 'Enter' && canSubmit && submit()}
        placeholder="ej: datos, media, backup"
        autocomplete="off"
        autocorrect="off"
        autocapitalize="off"
        spellcheck="false"
        maxlength="32"
        disabled={processing}
      />
      <div class="field-hint" class:err={nameError !== ''}>
        {#if nameError}
          {nameError}
        {:else if name.length === 0}
          2-32 caracteres · empezar por letra · minúsculas, dígitos, - y _
        {:else}
          ✓ Nombre válido
        {/if}
      </div>
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
    <button class="btn-primary" on:click={submit} disabled={!canSubmit}>
      {processing ? 'Importando...' : 'Importar pool'}
    </button>
  </div>
</div>

<style>
  /* ─── Frame ─── */
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
    border-radius: 10px;
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
  .modal-close:hover {
    background: var(--side-hover);
    color: var(--ink);
  }

  /* Strip verde (acción no destructiva) */
  .modal-strip {
    height: 2px;
    background: var(--signal);
    box-shadow: 0 0 6px rgba(0, 255, 159, 0.45);
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

  /* ─── Contenido (mismo lenguaje que el resto de wizards/modals Beta 8.1) ─── */
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
    color: var(--signal);
    font-weight: 600;
    font-family: var(--font-mono);
  }

  /* Card de info */
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

  /* Input nombre + hint */
  .field-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .field-label {
    font-size: 11px;
    color: var(--ink-dim);
    font-family: var(--font-sans);
  }
  .name-input {
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
  .name-input:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .field-hint {
    font-size: 10px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    letter-spacing: 0.3px;
  }
  .field-hint.err { color: var(--crit); }

  /* Alert error de backend */
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

  /* Botones (mismos que el resto de Beta 8.1) */
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
  }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-primary:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    filter: none;
  }
</style>
