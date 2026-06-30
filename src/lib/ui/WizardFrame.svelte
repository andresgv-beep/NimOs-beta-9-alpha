<script>
  /**
   * WizardFrame · Frame para wizards multi-paso (Beta 8.1 design system).
   * ────────────────────────────────────────────────────────────────────
   * Reemplazo de WizardModal usando tokens semánticos del Design System
   * (--ink, --bg-window, --signal, etc.) en lugar de los legacy
   * (--fg, --bg, --accent...).
   *
   * Mantiene la MISMA API que WizardModal para que la migración sea
   * un solo cambio de import en cada wizard:
   *
   *   - import WizardModal from '$lib/ui/WizardModal.svelte';
   *   + import WizardFrame from '$lib/ui/WizardFrame.svelte';
   *
   * Mientras la migración esté en curso, WizardModal sigue activo para
   * los wizards aún no migrados (Create, Export). Cuando los 3 estén
   * migrados se elimina WizardModal y este se renombra.
   *
   * Props (idénticas a WizardModal):
   *   · open         — boolean, controla visibilidad (default true)
   *   · title        — string en el head
   *   · tag          — string opcional al lado del título (ej. "data1")
   *   · tagColor     — 'accent' | 'warn' | 'danger' — color del tag
   *   · currentStep  — número de paso actual (1-indexed)
   *   · totalSteps   — total de pasos del flujo
   *   · canAdvance   — bool, habilita botón Continuar
   *   · canGoBack    — bool, habilita botón Atrás
   *   · nextLabel    — texto del botón principal
   *   · cancelLabel  — texto del botón cancelar
   *   · nextVariant  — 'primary' | 'warn' | 'danger' — color botón principal
   *   · width        — ancho en px (default 560)
   *
   * Eventos:
   *   · next   — usuario pulsa Continuar/acción principal
   *   · back   — usuario pulsa Atrás
   *   · cancel — usuario pulsa Cancelar o tecla Escape o click backdrop
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';

  export let open = false;
  export let title = 'Wizard';
  export let tag = '';
  export let tagColor = 'accent';
  export let currentStep = 1;
  export let totalSteps = 3;
  export let canAdvance = true;
  export let canGoBack = true;
  export let nextLabel = 'Continuar →';
  export let cancelLabel = 'Cancelar';
  export let nextVariant = 'primary';
  export let width = 560;

  const dispatch = createEventDispatcher();

  $: isFirstStep = currentStep <= 1;
  $: progressPercent = totalSteps > 0 ? (currentStep / totalSteps) * 100 : 0;
  $: progressColor = nextVariant === 'danger' ? 'var(--crit)'
                   : nextVariant === 'warn'   ? 'var(--warn)'
                   : 'var(--signal)';

  function handleNext()   { if (canAdvance) dispatch('next'); }
  function handleBack()   { if (canGoBack && !isFirstStep) dispatch('back'); }
  function handleCancel() { dispatch('cancel'); }

  // Cierre por tecla Escape
  function onKeydown(e) {
    if (!open) return;
    if (e.key === 'Escape') handleCancel();
  }

  onMount(() => { window.addEventListener('keydown', onKeydown); });
  onDestroy(() => { window.removeEventListener('keydown', onKeydown); });
</script>

{#if open}
  <div class="backdrop" on:click|self={handleCancel} role="presentation"></div>

  <div class="wiz" style="width: {width}px" role="dialog" aria-modal="true" aria-labelledby="wiz-title">
    <!-- HEAD: título + tag + stepper + close -->
    <div class="wiz-head">
      <div class="wiz-title" id="wiz-title">
        {title}
        {#if tag}
          <span class="wiz-tag wiz-tag-{tagColor}">"{tag}"</span>
        {/if}
      </div>
      <div class="wiz-step">Paso <span class="cur">{currentStep}</span>/{totalSteps}</div>
      <button class="wiz-close" on:click={handleCancel} title="Cerrar" aria-label="Cerrar">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <line x1="18" y1="6" x2="6" y2="18"/>
          <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </div>

    <!-- PROGRESS -->
    <div class="wiz-progress">
      <div class="wiz-progress-bar" style="width: {progressPercent}%; background: {progressColor}; box-shadow: 0 0 6px {progressColor};"></div>
    </div>

    <!-- BODY · contenido inyectado vía slot -->
    <div class="wiz-body">
      <slot />
    </div>

    <!-- FOOT: back / spacer / cancel + next -->
    <div class="wiz-foot">
      <button class="btn-secondary" on:click={handleBack} disabled={!canGoBack || isFirstStep}>
        ← Atrás
      </button>
      <div class="wiz-spacer"></div>
      <button class="btn-secondary" on:click={handleCancel}>
        {cancelLabel}
      </button>
      <button class="btn-{nextVariant}" on:click={handleNext} disabled={!canAdvance}>
        {nextLabel}
      </button>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.65);
    z-index: 100;
  }

  .wiz {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 101;
    background: var(--bg-window);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 24px 60px rgba(0, 0, 0, 0.55);
    display: flex;
    flex-direction: column;
    max-height: calc(100vh - 80px);
    overflow: hidden;
    animation: wizIn 0.18s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes wizIn {
    from { opacity: 0; transform: translate(-50%, -50%) translateY(-8px) scale(0.98); }
    to   { opacity: 1; transform: translate(-50%, -50%) translateY(0) scale(1); }
  }

  /* HEAD */
  .wiz-head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--line);
    flex-shrink: 0;
  }
  .wiz-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
    letter-spacing: -0.1px;
  }
  .wiz-tag {
    font-family: var(--font-mono);
    font-size: 11px;
    margin-left: 4px;
    font-weight: 500;
  }
  .wiz-tag-accent { color: var(--signal); }
  .wiz-tag-warn   { color: var(--warn); }
  .wiz-tag-danger { color: var(--crit); }

  .wiz-step {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.5px;
  }
  .wiz-step .cur {
    color: var(--ink);
    font-weight: 600;
  }

  .wiz-close {
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
  .wiz-close svg { width: 12px; height: 12px; }
  .wiz-close:hover {
    background: var(--side-hover);
    color: var(--ink);
  }

  /* PROGRESS */
  .wiz-progress {
    height: 2px;
    background: var(--line);
    position: relative;
    overflow: hidden;
    flex-shrink: 0;
  }
  .wiz-progress-bar {
    position: absolute;
    left: 0; top: 0; bottom: 0;
    transition: width 0.3s cubic-bezier(0.16, 1, 0.3, 1);
  }

  /* BODY */
  .wiz-body {
    padding: 20px 22px;
    flex: 1;
    overflow-y: auto;
    background: var(--bg-main);
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  /* FOOT */
  .wiz-foot {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--line);
    flex-shrink: 0;
    background: var(--bg-window);
  }
  .wiz-spacer { flex: 1; }

  /* Botones — usan tokens semánticos del design system */
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
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .btn-primary:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-primary:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    filter: none;
  }

  .btn-warn {
    padding: 7px 14px;
    border-radius: 6px;
    border: none;
    background: var(--warn);
    color: var(--bg-window);
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: filter 0.12s;
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .btn-warn:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-warn:disabled {
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
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .btn-danger:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-danger:disabled {
    opacity: 0.35;
    cursor: not-allowed;
    filter: none;
  }
</style>
