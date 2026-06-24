<script>
  /**
   * AppStore · Entry point del módulo
   * ──────────────────────────────────
   * Router de las cuatro pantallas del módulo:
   *
   *   1. Sin pool        → AppStoreSetup (mockup 1)
   *   2. Sin Docker      → AppStoreSetup (mockup 2)
   *   3. Catálogo        → AppStoreOverview (mockup 3 · Fase 3 ✓)
   *   4. Detalle de app  → AppStoreDetail (mockup 4 · Fase 4 ✓)
   *
   * El estado de navegación dentro del módulo es muy simple:
   *   - selectedAppId  · si null, muestra catálogo; si tiene valor, detalle.
   *
   * La transición catálogo ↔ detalle ocurre dentro de la misma ventana
   * (modelo "una ventana por app" del desktop NimOS). El botón "Volver"
   * del detalle limpia selectedAppId.
   */

  import { onMount } from 'svelte';
  import { getCapabilities } from './appstore/api.js';
  import AppStoreSetup from './appstore/AppStoreSetup.svelte';
  import AppStoreOverview from './appstore/AppStoreOverview.svelte';
  import AppStoreDetail from './appstore/AppStoreDetail.svelte';

  /** @typedef {import('./appstore/types').AppStoreCapabilities} AppStoreCapabilities */

  /** @type {AppStoreCapabilities | null} */
  let capabilities = null;
  let loading = true;
  let loadError = '';

  /** ID de la app seleccionada · null = vista catálogo */
  let selectedAppId = null;

  onMount(loadCapabilities);

  async function loadCapabilities() {
    loading = true;
    loadError = '';
    try {
      capabilities = await getCapabilities();
    } catch (err) {
      loadError = err?.message || String(err);
      capabilities = null;
    } finally {
      loading = false;
    }
  }

  async function handleSetupReady() {
    await loadCapabilities();
  }

  /** @param {CustomEvent<{appId: string}>} ev */
  function handleSelectApp(ev) {
    selectedAppId = ev.detail.appId;
  }

  function handleBackToCatalog() {
    selectedAppId = null;
  }
</script>

{#if loading}
  <div class="appstore-loading">
    <div class="loading-dot"></div>
    <div class="loading-text">Cargando…</div>
  </div>
{:else if loadError}
  <div class="appstore-error">
    <div class="err-title">No se pudo cargar el AppStore</div>
    <div class="err-body">{loadError}</div>
    <button class="err-btn" on:click={loadCapabilities}>Reintentar</button>
  </div>
{:else if !capabilities?.hasPool || !capabilities?.dockerInstalled}
  <!-- Setup · sin pool o sin Docker -->
  <AppStoreSetup {capabilities} onReady={handleSetupReady} />
{:else if selectedAppId}
  <!-- Detalle · Fase 4 ✓ -->
  <AppStoreDetail appId={selectedAppId} on:back={handleBackToCatalog} />
{:else}
  <!-- Catálogo · Fase 3 ✓ -->
  <AppStoreOverview on:select={handleSelectApp} />
{/if}

<style>
  /* ═══ Loading ═══ */
  .appstore-loading {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--sp-3);
    background: var(--panel-elev);
    color: var(--ink-mute);
    font-family: var(--font-mono);
    font-size: var(--fs-11);
  }
  .loading-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--signal);
    animation: pulse 1.4s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 0.3; transform: scale(0.9); }
    50%      { opacity: 1;   transform: scale(1.1); }
  }
  .loading-text { letter-spacing: 0.5px; }

  /* ═══ Error fatal ═══ */
  .appstore-error {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--sp-3);
    padding: var(--sp-5);
    background: var(--panel-elev);
    color: var(--ink);
    text-align: center;
  }
  .err-title {
    font-size: var(--fs-13);
    font-weight: 600;
    color: var(--crit);
  }
  .err-body {
    font-size: var(--fs-11);
    color: var(--ink-dim);
    font-family: var(--font-mono);
    max-width: 420px;
    line-height: 1.55;
    word-break: break-word;
  }
  .err-btn {
    margin-top: var(--sp-2);
    padding: 8px 16px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--line);
    background: transparent;
    color: var(--ink-dim);
    font-size: var(--fs-12);
    font-family: inherit;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .err-btn:hover {
    color: var(--ink);
    background: var(--line);
  }
</style>
