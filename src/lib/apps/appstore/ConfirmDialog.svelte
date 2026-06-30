<script>
  /**
   * ConfirmDialog · Modal de confirmación
   * ───────────────────────────────────────
   * Modal genérico para confirmar acciones. Soporta:
   *   - Modo simple (sí/no)
   *   - Modo "escribe palabra para confirmar" (inputConfirm)
   *   - Variantes visuales: default / warn / danger
   *
   * Uso simple:
   *   <ConfirmDialog
   *     open={true}
   *     title="¿Continuar?"
   *     message="Esta acción borrará el snapshot."
   *     variant="danger"
   *     on:confirm={handleConfirm}
   *     on:cancel={() => open = false}
   *   />
   *
   * Uso con input-to-confirm:
   *   <ConfirmDialog
   *     open={true}
   *     title="Destruir pool"
   *     message="Esta acción es irreversible."
   *     inputConfirm="data3"
   *     variant="danger"
   *     confirmLabel="Destruir pool"
   *     on:confirm={handleDestroy}
   *     on:cancel={() => open = false}
   *   />
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';

  export let open = false;
  export let title = 'Confirmar';
  export let message = '';
  export let confirmLabel = 'Confirmar';
  export let cancelLabel = 'Cancelar';
  /** 'default' | 'warn' | 'danger' */
  export let variant = 'default';
  /** Si se pasa, el usuario debe escribir exactamente esta cadena para habilitar el botón confirmar. */
  export let inputConfirm = null;
  /** Deshabilita confirmación mientras procesa (ej. llamada async) */
  export let processing = false;

  const dispatch = createEventDispatcher();

  let inputValue = '';
  let inputEl;

  $: canConfirm = !processing && (
    inputConfirm === null ||
    inputValue.trim() === inputConfirm
  );

  function handleConfirm() {
    if (!canConfirm) return;
    dispatch('confirm');
  }

  function handleCancel() {
    dispatch('cancel');
  }

  function handleBackdrop(e) {
    if (e.target === e.currentTarget) handleCancel();
  }

  function handleKeydown(e) {
    if (!open) return;
    if (e.key === 'Escape') handleCancel();
    if (e.key === 'Enter' && canConfirm && inputConfirm !== null) handleConfirm();
  }

  // Auto-focus input al abrir
  $: if (open && inputConfirm !== null) {
    setTimeout(() => inputEl?.focus(), 50);
  }
  // Reset input al cerrar
  $: if (!open) inputValue = '';

  onMount(() => window.addEventListener('keydown', handleKeydown));
  onDestroy(() => window.removeEventListener('keydown', handleKeydown));
</script>

{#if open}
  <div class="cd-backdrop" on:click={handleBackdrop} role="presentation">
    <div class="cd" role="dialog" aria-modal="true" aria-labelledby="cd-title">
      <div class="cd-inner">

        <div class="cd-head">
          <div class="cd-title variant-{variant}" id="cd-title">{title}</div>
          <button
            class="cd-close"
            on:click={handleCancel}
            title="Cerrar"
            aria-label="Cerrar"
          >×</button>
        </div>

        <div class="cd-body">
          {#if message}
            <div class="cd-message">{message}</div>
          {/if}

          <slot />

          {#if inputConfirm !== null}
            <div class="cd-confirm-label">
              Escribe <b>{inputConfirm}</b> para confirmar:
            </div>
            <input
              bind:this={inputEl}
              bind:value={inputValue}
              class="cd-confirm-input"
              class:ok={canConfirm && inputConfirm !== null}
              type="text"
              placeholder={inputConfirm}
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
              spellcheck="false"
            />
          {/if}
        </div>

        <div class="cd-foot">
          {#if cancelLabel}
            <button class="cd-btn" on:click={handleCancel} disabled={processing}>
              {cancelLabel}
            </button>
          {/if}
          <div class="cd-spacer"></div>
          {#if confirmLabel}
            <button
              class="cd-btn btn-{variant}"
              on:click={handleConfirm}
              disabled={!canConfirm}
            >
              {processing ? 'Procesando...' : confirmLabel}
            </button>
          {/if}
        </div>

      </div>
    </div>
  </div>
{/if}

<style>
  /* ═══════════════════════════════════════════════════════════════════════
     ConfirmDialog · UI v3 (mayo 2026)
     Migrado de Beta 7 a tokens v3: --canvas, --panel-elev, --line, --ink,
     --signal, --warn, --crit. API exacta preservada.
     ═══════════════════════════════════════════════════════════════════════ */

  .cd-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    backdrop-filter: blur(4px);
    -webkit-backdrop-filter: blur(4px);
    z-index: 9999;
    display: flex;
    align-items: center;
    justify-content: center;
    animation: cd-fade-in 0.15s ease-out;
  }
  @keyframes cd-fade-in {
    from { opacity: 0; }
    to   { opacity: 1; }
  }

  .cd {
    width: 460px;
    max-width: calc(100% - 40px);
    background: var(--panel-elev);
    border: 1px solid var(--line);
    border-radius: 6px;
    overflow: hidden;
    box-shadow:
      0 20px 60px rgba(0, 0, 0, 0.5),
      0 0 0 1px rgba(255, 255, 255, 0.02);
    animation: cd-scale-in 0.18s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes cd-scale-in {
    from { opacity: 0; transform: scale(0.96) translateY(8px); }
    to   { opacity: 1; transform: scale(1) translateY(0); }
  }

  .cd-inner {
    display: flex;
    flex-direction: column;
  }

  /* ─── Header ─── */
  .cd-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--sp-3, 14px) var(--sp-4, 18px);
    border-bottom: 1px solid var(--line);
    background: var(--canvas);
  }
  .cd-title {
    font-size: var(--fs-13, 13px);
    font-weight: 600;
    color: var(--ink);
    letter-spacing: 0.3px;
    text-transform: uppercase;
    font-family: var(--font-mono);
  }
  .cd-title.variant-warn   { color: var(--warn); }
  .cd-title.variant-danger { color: var(--crit); }

  .cd-close {
    background: transparent;
    border: none;
    color: var(--ink-mute);
    cursor: pointer;
    font-size: 18px;
    line-height: 1;
    padding: 4px 8px;
    border-radius: 4px;
    font-family: inherit;
    transition: color 0.12s, background 0.12s;
  }
  .cd-close:hover {
    color: var(--ink);
    background: var(--line);
  }

  /* ─── Body ─── */
  .cd-body {
    padding: var(--sp-4, 18px);
    display: flex;
    flex-direction: column;
    gap: var(--sp-3, 14px);
  }
  .cd-message {
    color: var(--ink-dim);
    font-size: var(--fs-12, 12px);
    line-height: 1.55;
  }

  .cd-confirm-label {
    color: var(--ink-mute);
    font-size: var(--fs-11, 11px);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    font-family: var(--font-mono);
  }
  .cd-confirm-label b {
    color: var(--crit);
    font-weight: 600;
  }

  .cd-confirm-input {
    width: 100%;
    background: var(--canvas);
    border: 1px solid var(--line);
    color: var(--ink);
    padding: 10px 14px;
    border-radius: var(--radius-sm, 6px);
    font-size: var(--fs-12, 12px);
    font-family: var(--font-mono);
    outline: none;
    transition: border-color 0.12s, box-shadow 0.12s;
  }
  .cd-confirm-input::placeholder {
    color: var(--ink-faint);
  }
  .cd-confirm-input:focus {
    border-color: var(--line-bright);
  }
  .cd-confirm-input.ok {
    border-color: var(--signal);
    box-shadow: 0 0 0 2px var(--signal-dim, rgba(0, 255, 159, 0.1));
  }

  /* ─── Footer ─── */
  .cd-foot {
    display: flex;
    align-items: center;
    padding: var(--sp-3, 14px) var(--sp-4, 18px);
    border-top: 1px solid var(--line);
    background: var(--canvas);
    gap: var(--sp-2, 10px);
  }
  .cd-spacer {
    flex: 1;
  }

  .cd-btn {
    padding: 8px 18px;
    background: transparent;
    border: 1px solid var(--line);
    color: var(--ink-dim);
    font-size: var(--fs-12, 12px);
    font-weight: 600;
    font-family: inherit;
    border-radius: var(--radius-sm, 6px);
    cursor: pointer;
    transition: background 0.12s, color 0.12s, border-color 0.12s, filter 0.12s;
  }
  .cd-btn:not(:disabled):hover {
    color: var(--ink);
    background: var(--line);
    border-color: var(--line-bright);
  }
  .cd-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  /* Variantes del botón confirmar */
  .cd-btn.btn-default {
    background: var(--signal);
    color: var(--canvas);
    border-color: var(--signal);
  }
  .cd-btn.btn-default:not(:disabled):hover {
    filter: brightness(1.08);
    background: var(--signal);
    color: var(--canvas);
  }

  .cd-btn.btn-warn {
    background: var(--warn);
    color: var(--canvas);
    border-color: var(--warn);
  }
  .cd-btn.btn-warn:not(:disabled):hover {
    filter: brightness(1.08);
    background: var(--warn);
    color: var(--canvas);
  }

  .cd-btn.btn-danger {
    background: var(--crit);
    color: #fff;
    border-color: var(--crit);
  }
  .cd-btn.btn-danger:not(:disabled):hover {
    filter: brightness(1.08);
    background: var(--crit);
    color: #fff;
  }
</style>
