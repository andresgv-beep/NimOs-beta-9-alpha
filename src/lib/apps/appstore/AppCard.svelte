<script>
  /**
   * AppCard · Card individual de una app del catálogo
   * ──────────────────────────────────────────────────
   * Se usa en el grid del AppStoreOverview (mockup 3) y potencialmente en
   * listas filtradas futuras. Maneja:
   *   · Render del icono desde URL (con fallback a placeholder si falla)
   *   · Indicador "instalada" (punto verde sutil esquina superior derecha)
   *   · Nombre + categoría display
   *   · Click delega via evento `select` con el appId
   *
   * El componente NO toma decisiones de filtrado · solo renderiza un AppView
   * pasado por el padre. Pasivo y reusable.
   */

  import { createEventDispatcher } from 'svelte';
  import { categoryDisplayName } from './formatters.js';

  /** @typedef {import('./types').AppView} AppView */

  /** @type {AppView} */
  export let app;
  /** Display map opcional para resolver category slug → "Multimedia" */
  /** @type {Object<string,string>} */
  export let categoriesMap = {};
  /** Sprint Updates · marca este card como "tiene actualización disponible".
   * Solo se renderiza si app.installed === true. Cuando true, muestra:
   *   - Badge azul "NUEVA" arriba izquierda
   * Cuando app.installed === true (sin importar hasUpdate), muestra:
   *   - Tic verde abajo derecha · "instalada · todo bien"
   */
  export let hasUpdate = false;

  const dispatch = createEventDispatcher();

  let iconError = false;

  function handleClick() {
    dispatch('select', { appId: app.id });
  }

  $: categoryLabel = categoryDisplayName(app.category, categoriesMap);
</script>

<button class="app-card" class:has-update={app.installed && hasUpdate} on:click={handleClick} type="button" title={app.description || app.name}>
  {#if app.installed && hasUpdate}
    <span class="update-badge" title="Actualización disponible">NUEVA</span>
  {/if}

  <div class="card-top">
    <div class="app-icon-wrap">
      {#if !iconError && app.icon}
        <img
          class="app-icon"
          src={app.icon}
          alt={app.name}
          on:error={() => (iconError = true)}
          loading="lazy"
        />
      {:else}
        <div class="app-icon-fallback" style={app.color ? `background: ${app.color}` : ''}>
          {app.name.charAt(0).toUpperCase()}
        </div>
      {/if}
    </div>
    <div class="card-info">
      <div class="app-name">{app.name}</div>
      <div class="app-category">{categoryLabel}</div>
    </div>
  </div>

  {#if app.description}
    <div class="app-desc">{app.description}</div>
  {/if}

  <div class="card-foot">
    {#if app.installed}
      <span class="foot-badge installed">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
        Instalada
      </span>
    {:else}
      <span class="foot-badge available">Disponible</span>
    {/if}
    <span class="foot-action">{app.installed ? 'Abrir →' : 'Ver →'}</span>
  </div>
</button>

<style>
  .app-card {
    background: var(--panel-deep);
    border: 1px solid var(--line);
    border-radius: var(--radius-md);
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 13px;
    cursor: pointer;
    transition: border-color 0.15s, transform 0.15s, background 0.15s;
    font-family: inherit;
    color: var(--ink);
    width: 100%;
    text-align: left;
    position: relative;
  }
  .app-card:hover {
    border-color: var(--line-bright);
    background: var(--panel);
    transform: translateY(-2px);
  }
  .app-card:focus-visible {
    outline: 1px solid var(--info);
    outline-offset: 2px;
  }

  /* Fila superior · icono + nombre/categoría */
  .card-top {
    display: flex;
    align-items: center;
    gap: 13px;
  }
  .app-icon-wrap {
    width: 52px;
    height: 52px;
    border-radius: 13px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    position: relative;
    background: var(--canvas);
    overflow: hidden;
    padding: 5px;
  }
  .app-icon {
    width: 100%;
    height: 100%;
    object-fit: contain;
    display: block;
  }
  .app-icon-fallback {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--canvas-soft);
    color: var(--ink-dim);
    font-size: 22px;
    font-weight: 600;
    font-family: var(--font-mono);
  }
  .card-info {
    flex: 1;
    min-width: 0;
  }
  .app-name {
    font-size: var(--fs-15, 15px);
    color: var(--ink);
    font-weight: 700;
    line-height: 1.3;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .app-category {
    font-size: var(--fs-11, 11px);
    color: var(--ink-mute);
    font-family: var(--font-mono);
    line-height: 1.3;
    margin-top: 2px;
  }

  /* Descripción · 2 líneas máx */
  .app-desc {
    font-size: var(--fs-12, 12px);
    color: var(--ink-faint);
    line-height: 1.45;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    min-height: 34px;
  }

  /* Footer · badge de estado + acción */
  .card-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .foot-badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 10px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    padding: 3px 8px;
    border-radius: 5px;
  }
  .foot-badge svg { width: 11px; height: 11px; }
  .foot-badge.installed {
    background: var(--signal-soft, rgba(0, 255, 159, 0.12));
    color: var(--signal);
  }
  .foot-badge.available {
    background: rgba(255, 255, 255, 0.05);
    color: var(--ink-faint);
  }
  .foot-action {
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--info);
  }

  /* Estado "tiene update" · border azul sutil */
  .app-card.has-update {
    border-color: var(--info-dim, rgba(77, 184, 255, 0.3));
  }
  .update-badge {
    position: absolute;
    top: 10px;
    right: 10px;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.6px;
    padding: 3px 7px;
    border-radius: 4px;
    background: var(--info);
    color: var(--canvas);
    font-family: var(--font-mono);
    z-index: 3;
    box-shadow: 0 0 8px var(--info-glow, rgba(77, 184, 255, 0.4));
    text-transform: uppercase;
  }
</style>
