<script>
  /**
   * AppStoreOverview · Vista principal del catálogo
   * ─────────────────────────────────────────────────
   * Cubre el mockup 3. Estructura:
   *
   *   ┌─────────────┬──────────────────────────────────────┐
   *   │  SIDEBAR    │  HEADER (título + counts + search)   │
   *   │             │                                      │
   *   │  Biblioteca │  ┌────┐ ┌────┐ ┌────┐ ┌────┐         │
   *   │   · Instal. │  │app │ │app │ │app │ │app │  ...    │
   *   │             │  └────┘ └────┘ └────┘ └────┘         │
   *   │  Categorías │  ┌────┐ ┌────┐ ┌────┐                │
   *   │   · Todas   │  │... │ │... │ │... │                │
   *   │   · Media   │  └────┘ └────┘ └────┘                │
   *   │   · Cloud   │                                      │
   *   │   · ...     │                                      │
   *   └─────────────┴──────────────────────────────────────┘
   *
   * Estado:
   *   - active = id de la sección activa del sidebar
   *     · "installed"           → filtra a las que tienes
   *     · "cat-all"             → todas las del catálogo
   *     · "cat-<slug>"          → filtra por slug de categoría
   *   - search = string libre · filtra por nombre y descripción
   *
   * Carga en paralelo catálogo (GitHub raw) + instaladas (/api/services
   * filtrado por type=docker-app). Si el catálogo falla pero hay caché vieja,
   * usa caché. Si falla por completo, muestra error con reintentar.
   *
   * Click en una card emite `select` con appId · padre decide (Fase 4 abrirá
   * detalle; ahora se queda en placeholder).
   */

  import { onMount, createEventDispatcher } from 'svelte';
  import AppShell from '$lib/components/AppShell.svelte';
  import AppCard from './AppCard.svelte';
  import { fetchCatalog, countByCategory, listCatalogApps, appSupportsArch } from './catalog.js';
  import { getToken } from '$lib/stores/auth.js';
  import { getInstalledApps, getUpdatesSummary } from './api.js';
  import { composeAppViews, buildSidebarSections, categoryDisplayName } from './formatters.js';

  /** @typedef {import('./types').Catalog} Catalog */
  /** @typedef {import('./types').AppView} AppView */
  /** @typedef {import('./types').InstalledApp} InstalledApp */

  const dispatch = createEventDispatcher();

  // ── Estado ────────────────────────────────────────────────────────
  /** @type {Catalog | null} */
  let catalog = null;
  let systemArch = ''; // arquitectura del sistema · oculta apps incompatibles
  /** @type {InstalledApp[]} */
  let installed = [];
  let loading = true;
  let loadError = '';

  // Active section del sidebar · default "cat-all"
  let active = 'cat-all';

  // Búsqueda libre
  let search = '';

  // Sprint Updates · IDs de apps que tienen update pendiente (en Set para
  // lookup O(1) al pasar prop hasUpdate a cada AppCard).
  /** @type {Set<string>} */
  let appsWithUpdate = new Set();
  let updatesCount = 0;

  // ── Derived ───────────────────────────────────────────────────────

  // Catálogo · entries para iterar (excluyendo isSystem)
  $: catalogEntries = catalog
    ? Object.entries(catalog.apps)
        .filter(([, app]) => !app.isSystem)
        .filter(([, app]) => appSupportsArch(app, systemArch))
        .map(([id, app]) => ({ id, app }))
    : [];

  // Counts (Biblioteca/Categorías)
  $: counts = catalog ? countByCategory(catalog) : { total: 0, byCategory: {} };
  $: categoriesMap = catalog?.categories || {};

  // Cards = cruce catálogo + instaladas
  /** @type {AppView[]} */
  $: allViews = catalog ? composeAppViews(catalogEntries, installed) : [];

  // Filtrado por sección activa + search
  /** @type {AppView[]} */
  $: visibleViews = filterViews(allViews, active, search);

  // Sidebar dinámico
  $: sidebarSections = buildSidebarSections(counts, categoriesMap, installed.length, updatesCount);

  // Header
  $: activeLabel = labelForActive(active, categoriesMap);
  $: pathSegments = active === 'installed'
    ? ['appstore', 'installed']
    : active === 'cat-all'
      ? ['appstore', 'todas']
      : ['appstore', active.replace(/^cat-/, '')];

  // ── Filtrado ───────────────────────────────────────────────────────

  /**
   * @param {AppView[]} views
   * @param {string} activeId
   * @param {string} term
   * @returns {AppView[]}
   */
  function filterViews(views, activeId, term) {
    let out = views;

    if (activeId === 'installed') {
      out = out.filter((v) => v.installed);
    } else if (activeId === 'cat-all') {
      // Todas · no filter
    } else if (activeId.startsWith('cat-')) {
      const slug = activeId.replace(/^cat-/, '');
      out = out.filter((v) => v.category === slug);
    }

    const q = term.trim().toLowerCase();
    if (q) {
      out = out.filter(
        (v) =>
          v.name.toLowerCase().includes(q) ||
          (v.description || '').toLowerCase().includes(q) ||
          v.id.toLowerCase().includes(q)
      );
    }
    return out;
  }

  /**
   * @param {string} activeId
   * @param {Object<string,string>} catsMap
   * @returns {string}
   */
  function labelForActive(activeId, catsMap) {
    if (activeId === 'installed') return 'Instaladas';
    if (activeId === 'cat-all') return 'Todas las apps';
    if (activeId.startsWith('cat-')) {
      const slug = activeId.replace(/^cat-/, '');
      return categoryDisplayName(slug, catsMap);
    }
    return '';
  }

  // ── Lifecycle ──────────────────────────────────────────────────────
  onMount(load);

  async function load() {
    loading = true;
    loadError = '';
    try {
      // Lanzar en paralelo · todas independientes
      const [cat, inst, ups] = await Promise.all([
        fetchCatalog(),
        getInstalledApps().catch((err) => {
          // Si /api/services falla, seguimos con catálogo aún · solo nos
          // perdemos el indicador "instalada" en cards.
          console.warn('[appstore/overview] getInstalledApps failed:', err);
          return [];
        }),
        // Sprint Updates · sumario para badges "NUEVA" + icono sidebar.
        // Si falla, no rompe la app · solo nos quedamos sin badges.
        getUpdatesSummary().catch((err) => {
          console.warn('[appstore/overview] getUpdatesSummary failed:', err);
          return { count: 0, apps: [] };
        }),
      ]);
      catalog = cat;
      installed = inst;
      // Arquitectura del sistema · para ocultar apps incompatibles (amd64-only
      // en un Pi arm64, etc.). Si falla, queda '' y no se oculta nada.
      try {
        const sysRes = await fetch('/api/system', {
          headers: { Authorization: `Bearer ${getToken()}` },
        });
        const sys = await sysRes.json();
        systemArch = (sys && (sys.arch || sys.data?.arch)) || '';
      } catch {
        systemArch = '';
      }
      // Construir Set para lookup O(1) en cada AppCard
      appsWithUpdate = new Set((ups.apps || []).map((a) => a.appId));
      updatesCount = ups.count || 0;
    } catch (err) {
      loadError = err?.message || String(err);
    } finally {
      loading = false;
    }
  }

  /**
   * Refresca el sumario de updates sin recargar todo el catálogo.
   * Útil tras hacer un update exitoso para que el sidebar y los badges
   * reflejen el nuevo estado (esa app ya no necesita update).
   */
  async function refreshUpdates() {
    try {
      const ups = await getUpdatesSummary();
      appsWithUpdate = new Set((ups.apps || []).map((a) => a.appId));
      updatesCount = ups.count || 0;
    } catch (err) {
      console.warn('[appstore/overview] refreshUpdates failed:', err);
    }
  }

  // ── Eventos ────────────────────────────────────────────────────────
  /** @param {CustomEvent<{appId: string}>} ev */
  function onSelect(ev) {
    dispatch('select', ev.detail);
  }
</script>

<AppShell
  appId="appstore"
  title="App Store"
  headerIcon="⊞"
  pathSegments={pathSegments}
  sections={sidebarSections}
  bind:active
  bodyPadding={false}
>
  <!-- Título en la barra (page-header), como el resto de apps (NimHealth, Files…) -->
  <svelte:fragment slot="page-header">
    <b>{activeLabel}</b>
    {#if !loading && !loadError}
      <span class="head-meta">· {visibleViews.length} de {allViews.length}{#if installed.length > 0} · {installed.length} instalada{installed.length === 1 ? '' : 's'}{/if}</span>
    {/if}
  </svelte:fragment>

  {#if loading}
    <div class="state-pane">
      <div class="loading-dot"></div>
      <div class="state-text">Cargando catálogo…</div>
    </div>
  {:else if loadError}
    <div class="state-pane">
      <div class="err-title">No se pudo cargar el catálogo</div>
      <div class="err-body">{loadError}</div>
      <button class="err-btn" on:click={load}>Reintentar</button>
    </div>
  {:else}
    <div class="overview">
      <!-- Barra de búsqueda · el título y los counts viven ahora en el page-header -->
      <div class="overview-head">
        <div class="head-search">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            type="text"
            placeholder="Buscar…"
            bind:value={search}
          />
          {#if search}
            <button class="clear-btn" on:click={() => (search = '')} title="Limpiar" type="button">×</button>
          {/if}
        </div>
      </div>

      <!-- Grid · área scrolleable separada del header fijo -->
      <div class="grid-scroll">
      {#if visibleViews.length === 0}
        <div class="grid-empty">
          {#if search.trim()}
            Ningún resultado para <b>"{search}"</b>
            <button class="link" on:click={() => (search = '')}>Limpiar búsqueda</button>
          {:else if active === 'installed'}
            No tienes apps instaladas todavía.
            <button class="link" on:click={() => (active = 'cat-all')}>Ver catálogo completo</button>
          {:else}
            Sin apps en esta categoría.
          {/if}
        </div>
      {:else}
        <div class="apps-grid">
          {#each visibleViews as view (view.id)}
            <AppCard
              app={view}
              {categoriesMap}
              hasUpdate={view.installed && appsWithUpdate.has(view.id)}
              on:select={onSelect}
            />
          {/each}
        </div>
      {/if}
      </div>
    </div>
  {/if}
</AppShell>

<style>
  /* ═══ Estados (loading/error) ═══ */
  .state-pane {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--sp-3);
    padding: var(--sp-5);
    text-align: center;
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
  .state-text {
    color: var(--ink-mute);
    font-family: var(--font-mono);
    font-size: var(--fs-11);
    letter-spacing: 0.5px;
  }
  .err-title {
    color: var(--crit);
    font-weight: 600;
    font-size: var(--fs-13);
  }
  .err-body {
    color: var(--ink-dim);
    font-family: var(--font-mono);
    font-size: var(--fs-11);
    max-width: 420px;
    word-break: break-word;
    line-height: 1.55;
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

  /* ═══ Overview ═══ */
  .overview {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    overflow: hidden;
  }

  /* Header · título + meta + search */
  .overview-head {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    flex-wrap: wrap;
    padding: var(--sp-5) var(--sp-5) var(--sp-4);
    flex-shrink: 0;
  }
  /* El scroll vive aquí · padding interno para que la barra no quede
     pegada al borde de la ventana. */
  .grid-scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 var(--sp-5) var(--sp-5);
  }
  .grid-scroll::-webkit-scrollbar { width: 8px; }
  .grid-scroll::-webkit-scrollbar-track { background: transparent; }
  .grid-scroll::-webkit-scrollbar-thumb {
    background: var(--scroll-thumb, rgba(255, 255, 255, 0.12));
    border-radius: 4px;
    border: 2px solid var(--canvas);
  }
  .head-meta {
    font-size: var(--fs-11);
    color: var(--ink-mute);
    font-variant-numeric: tabular-nums;
  }
  .head-search {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 9px;
    border-radius: var(--radius-sm);
    background: var(--panel-deep);
    border: 1px solid var(--line);
    width: 220px;
    transition: border-color 0.12s;
  }
  .head-search:focus-within {
    border-color: var(--line-bright);
  }
  .head-search svg {
    width: 11px;
    height: 11px;
    color: var(--ink-faint);
    flex-shrink: 0;
  }
  .head-search input {
    flex: 1;
    background: transparent;
    border: none;
    color: var(--ink);
    outline: none;
    font-family: inherit;
    font-size: var(--fs-11);
    min-width: 0;
  }
  .head-search input::placeholder {
    color: var(--ink-faint);
  }
  .clear-btn {
    background: none;
    border: none;
    color: var(--ink-faint);
    cursor: pointer;
    padding: 0 2px;
    font-size: 14px;
    line-height: 1;
    font-family: inherit;
  }
  .clear-btn:hover { color: var(--ink); }

  /* Grid · auto-fill responsive */
  .apps-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: 14px;
  }

  /* Empty state inline · cuando filter/search no devuelve nada */
  .grid-empty {
    padding: var(--sp-6) var(--sp-4);
    text-align: center;
    color: var(--ink-mute);
    font-size: var(--fs-12);
    border: 1px dashed var(--line);
    border-radius: var(--radius-md);
    background: var(--panel-deep);
    line-height: 1.7;
  }
  .grid-empty b { color: var(--ink); font-weight: 600; }
  .link {
    background: none;
    border: none;
    color: var(--info);
    cursor: pointer;
    font-size: var(--fs-12);
    margin-left: var(--sp-2);
    text-decoration: underline;
    font-family: inherit;
  }
  .link:hover { color: var(--ink); }
</style>
