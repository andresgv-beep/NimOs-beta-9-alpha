<script>
  /**
   * AppStoreSetup · Empty states del AppStore
   * ──────────────────────────────────────────
   * Cubre los dos casos en los que el AppStore NO puede mostrar el catálogo:
   *
   *   1. Sin pool montado → mockup "1-sin-pool.html"
   *      CTA: abrir Storage app · botón "Reintentar" para revalidar.
   *
   *   2. Pool OK pero Docker no instalado → mockup "2-sin-docker.html"
   *      Lista pools disponibles, user elige, instala Docker engine async.
   *      Durante el install muestra progress y termina llamando onReady().
   *
   * Decisión arquitectónica · esta pantalla vive FUERA de AppShell. Razón:
   * los mockups muestran ventana sin sidebar (la prop showSidebar={false}
   * existe en AppShell v3.1 pero su CSS está marcado TODO). Hasta que el
   * shell sin sidebar tenga estilo canónico, este componente provee su
   * propio chrome sencillo · el mismo contenedor que cualquier app pero
   * sin la columna izquierda.
   *
   * Props:
   *   - capabilities: AppStoreCapabilities  · estado actual del sistema
   *   - onReady: () => void                 · invocado cuando el setup
   *                                            ha terminado y AppStore puede
   *                                            mostrar el catálogo
   *
   * Estados internos:
   *   - viewMode: 'no-pool' | 'no-docker' | 'installing'
   *   - installing: bool · true durante install + polling
   *   - currentOp: Operation | null · última lectura del polling
   *   - installError: string · mensaje si install failed
   */

  import { onMount, getContext } from 'svelte';
  import { openWindow, closeWindow } from '$lib/stores/windows.js';
  import { getCapabilities, installDockerEngine } from './api.js';

  /** @typedef {import('./types').AppStoreCapabilities} AppStoreCapabilities */
  /** @typedef {{ name: string, fsType?: string, layout?: string, freeBytes?: number }} PoolSummary */

  /** @type {AppStoreCapabilities | null} */
  export let capabilities = null;
  /** @type {() => void} */
  export let onReady = () => {};

  // WindowFrame expone setContext('windowControls') con close/minimize/maximize
  // que delegan al store de windows. Lo capturamos para hacer funcional el
  // botón de cerrar del titlebar custom de este setup.
  const wc = getContext('windowControls');

  function handleClose() {
    if (wc && typeof wc.close === 'function') {
      wc.close();
    }
  }

  // ── Estado interno ─────────────────────────────────────────────────
  let viewMode = 'no-pool'; // 'no-pool' | 'no-docker' | 'installing'
  /** @type {PoolSummary[]} */
  let pools = [];
  let selectedPool = '';
  let installing = false;
  let installError = '';
  let retryLoading = false;

  // ── Lifecycle ──────────────────────────────────────────────────────
  onMount(() => {
    applyCapabilities(capabilities);
  });

  // ── Capabilities → modo de vista ───────────────────────────────────
  /**
   * Decide qué vista mostrar a partir de capabilities.
   * Para 'no-docker' también pre-llena la lista de pools desde /api/services.
   *
   * @param {AppStoreCapabilities | null} caps
   */
  async function applyCapabilities(caps) {
    if (!caps) {
      // Sin capabilities aún · pedir antes
      caps = await getCapabilities();
    }
    if (!caps.hasPool) {
      viewMode = 'no-pool';
      return;
    }
    if (!caps.dockerInstalled) {
      viewMode = 'no-docker';
      // Cargar pools para que el user elija
      await loadPools();
      return;
    }
    // Si llegamos aquí es que capabilities ya está OK · avisar
    onReady();
  }

  /**
   * Carga la lista de pools disponibles desde Storage v2.
   * Soporta dos shapes posibles de la respuesta:
   *   { data: [...] }       (patrón v2 canonical)
   *   { pools: [...] }      (algunos endpoints anidan así)
   *   [...] directo         (fallback defensivo)
   */
  async function loadPools() {
    try {
      const res = await fetch('/api/storage/v2/pools', {
        credentials: 'include',
      });
      if (!res.ok) {
        pools = [];
        return;
      }
      const body = await res.json();
      // Tres formatos posibles · probamos en orden.
      let list = null;
      if (Array.isArray(body)) {
        list = body;
      } else if (body && Array.isArray(body.data)) {
        list = body.data;
      } else if (body && Array.isArray(body.pools)) {
        list = body.pools;
      } else if (body?.data && Array.isArray(body.data.pools)) {
        list = body.data.pools;
      }
      pools = list || [];
      if (pools.length > 0) selectedPool = pools[0].name;
    } catch (err) {
      console.warn('[appstore/setup] failed to load pools:', err);
      pools = [];
    }
  }

  // ── Acciones ───────────────────────────────────────────────────────

  /**
   * "Sin pool" · Reintenta capabilities tras (idealmente) crear el pool.
   */
  async function handleRetry() {
    retryLoading = true;
    try {
      const caps = await getCapabilities();
      capabilities = caps;
      await applyCapabilities(caps);
    } catch (err) {
      console.error('[appstore/setup] retry failed:', err);
    } finally {
      retryLoading = false;
    }
  }

  /**
   * "Sin pool" · Abre la app Storage para que el user cree un pool.
   */
  function handleOpenStorage() {
    openWindow('storage');
  }

  /**
   * "Sin Docker" · Lanza el install de Docker engine en el pool elegido.
   *
   * Síncrono · el backend tarda 3-7 minutos. El componente muestra una
   * barra de progreso indeterminada (estética) mientras espera. Sin
   * polling porque no hay operation tracking para este install.
   *
   * Si el fetch falla por red/timeout, mostramos error y permitimos
   * reintentar. Si el navegador o proxy corta la conexión pero el
   * backend completa de todas formas, al recargar AppStore las
   * capabilities reflejarán que Docker está OK.
   */
  async function handleInstallDocker() {
    if (!selectedPool) return;
    installing = true;
    installError = '';
    viewMode = 'installing';

    try {
      await installDockerEngine({ pool: selectedPool });
      // Backend confirmó install OK · pequeña pausa antes de salir a capabilities
      setTimeout(() => {
        onReady();
      }, 600);
    } catch (err) {
      const parts = [];
      parts.push(err?.message || String(err));
      if (err?.code) parts.push(`code: ${err.code}`);
      if (err?.status) parts.push(`status: ${err.status}`);
      installError = parts.join(' · ');
      console.error('[appstore/setup] install failed:', err);
      installing = false;
    }
  }

  /**
   * Tras error · permite volver a la pantalla "no-docker" para reintentar.
   */
  function handleRetryAfterError() {
    installError = '';
    currentOp = null;
    viewMode = 'no-docker';
  }

  // ── Formato ────────────────────────────────────────────────────────
  function formatBytes(bytes) {
    if (!bytes || bytes < 0) return '—';
    const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let i = 0;
    let v = bytes;
    while (v >= 1024 && i < u.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
  }
</script>

<div class="setup-window">
  <!-- Titlebar de la ventana · solo botón cerrar funcional · los otros eran decorativos -->
  <div class="setup-titlebar">
    <button
      type="button"
      class="ctl ctl-close"
      title="Cerrar"
      aria-label="Cerrar"
      on:click={handleClose}
    ></button>
  </div>

  <div class="setup-content">
    {#if viewMode === 'no-pool'}
      <!-- ═══ Empty state · sin pool ═══ -->
      <div class="card">
        <div class="card-icon icon-accent">
          <!-- Icono pool/disco · stroke 1.6 consistente con resto -->
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <ellipse cx="12" cy="5" rx="9" ry="3" />
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
          </svg>
        </div>

        <h1>Necesitas un pool de almacenamiento</h1>
        <p>
          AppStore usa contenedores Docker que necesitan persistir datos.
          Crea primero un <b>pool BTRFS</b> desde Storage donde se instalarán
          los contenedores, volúmenes y configuraciones.
        </p>

        <div class="actions">
          <button class="btn btn-primary" on:click={handleOpenStorage}>
            Abrir Storage
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"
                 stroke-width="2.5" stroke-linecap="round" class="btn-arrow">
              <path d="M5 12h14" />
              <path d="M12 5l7 7-7 7" />
            </svg>
          </button>
          <button
            class="btn btn-secondary"
            on:click={handleRetry}
            disabled={retryLoading}
          >
            {retryLoading ? 'Comprobando…' : 'Reintentar'}
          </button>
        </div>
      </div>

    {:else if viewMode === 'no-docker'}
      <!-- ═══ Empty state · sin Docker ═══ -->
      <div class="card card-wide">
        <div class="card-icon icon-info">
          <!-- Icono caja/container · stroke 1.6 -->
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <rect x="4" y="8" width="16" height="10" rx="2" />
            <line x1="2" y1="22" x2="22" y2="22" />
            <line x1="8" y1="12" x2="8.01" y2="12" />
            <line x1="12" y1="12" x2="12.01" y2="12" />
            <line x1="16" y1="12" x2="16.01" y2="12" />
          </svg>
        </div>

        <h1>Instalar Docker Engine</h1>
        <p>
          Las apps se ejecutan en contenedores aislados.
          Elige el pool donde se instalarán Docker y sus datos.
          <b>Nada se guarda en el disco del sistema.</b>
        </p>

        <div class="field">
          <label class="field-label">Pool de almacenamiento</label>
          {#if pools.length === 0}
            <div class="pool-empty">
              No se han encontrado pools disponibles.
              <button class="link" on:click={handleRetry}>Reintentar</button>
            </div>
          {:else}
            <div class="pool-list">
              {#each pools as p (p.name)}
                <button
                  class="pool-option"
                  class:selected={selectedPool === p.name}
                  on:click={() => (selectedPool = p.name)}
                  type="button"
                >
                  <span class="pool-radio"></span>
                  <span class="pool-info">
                    <span class="pool-name">{p.name}</span>
                    <span class="pool-meta">
                      {p.fsType || 'btrfs'}{p.layout ? ' · ' + p.layout : ''}
                    </span>
                  </span>
                  <span class="pool-size">
                    {p.freeBytes != null ? formatBytes(p.freeBytes) + ' libres' : ''}
                  </span>
                </button>
              {/each}
            </div>
          {/if}
        </div>

        <button
          class="btn btn-primary btn-block"
          on:click={handleInstallDocker}
          disabled={!selectedPool || installing}
        >
          {selectedPool ? `Instalar Docker en ${selectedPool}` : 'Selecciona un pool'}
        </button>

        <p class="hint">
          La instalación puede tardar unos minutos. AppStore se cargará cuando termine.
        </p>
      </div>

    {:else if viewMode === 'installing'}
      <!-- ═══ Instalando · sin progreso numérico (sync install) ═══ -->
      <div class="card card-wide">
        <div class="card-icon icon-info">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <rect x="4" y="8" width="16" height="10" rx="2" />
            <line x1="2" y1="22" x2="22" y2="22" />
            <line x1="8" y1="12" x2="8.01" y2="12" />
            <line x1="12" y1="12" x2="12.01" y2="12" />
            <line x1="16" y1="12" x2="16.01" y2="12" />
          </svg>
        </div>

        {#if installError}
          <h1 class="title-error">La instalación falló</h1>
          <div class="error-box">{installError}</div>
          <button class="btn btn-secondary" on:click={() => { installError = ''; viewMode = 'no-docker'; }}>
            Volver
          </button>
        {:else}
          <h1>Instalando Docker Engine</h1>
          <p class="install-msg">
            Descargando e instalando · puede tardar varios minutos.
          </p>

          <!-- Barra con stripes animadas · indeterminada (no hay % real) -->
          <div class="install-bar-wrap">
            <div class="install-bar">
              <div class="install-bar-fill"></div>
            </div>
          </div>

          <p class="hint">
            No cierres esta ventana. Al terminar, AppStore continuará solo.
          </p>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  /* ═══════════════════════════════════════════════════════════════════
     Shell minimalista · sin AppShell porque mockup es ventana sin sidebar.
     Sigue las variables canonical del proyecto · NO inventa tokens.
     ═══════════════════════════════════════════════════════════════════ */
  .setup-window {
    background: var(--panel-elev);
    min-height: 100%;
    display: flex;
    flex-direction: column;
    position: relative;
    color: var(--ink);
    font-family: var(--font-sans);
  }

  /* Titlebar absoluto · réplica fiel del patrón de AppShell */
  .setup-titlebar {
    position: absolute;
    top: var(--sp-3);
    right: var(--sp-4);
    display: flex;
    gap: 6px;
    z-index: 10;
  }
  .ctl {
    width: 12px;
    height: 12px;
    border-radius: 3px;
    background: var(--crit);
    cursor: pointer;
    border: none;
    padding: 0;
    transition: filter 0.12s, transform 0.08s;
  }
  .ctl:hover {
    filter: brightness(1.15);
  }
  .ctl:active {
    transform: scale(0.92);
  }
  .ctl-close { background: var(--crit); }

  /* Contenido centrado · padding generoso */
  .setup-content {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--sp-6) var(--sp-5);
  }

  /* ═══ Card central · dimensiones generosas para que no parezca perdido ═══ */
  .card {
    width: 100%;
    max-width: 560px;
    text-align: center;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-4);
  }
  .card-wide {
    max-width: 620px;
  }

  .card-icon {
    width: 88px;
    height: 88px;
    border-radius: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    margin-bottom: var(--sp-2);
  }
  .card-icon svg {
    width: 40px;
    height: 40px;
  }
  .icon-accent {
    background: var(--signal-soft);
    color: var(--signal);
  }
  .icon-info {
    background: var(--info-dim);
    color: var(--info);
  }

  .card h1 {
    font-size: var(--fs-18);
    font-weight: 600;
    color: var(--ink);
    letter-spacing: -0.3px;
    margin: 0;
  }
  .card h1.title-error {
    color: var(--crit);
  }
  .card p {
    font-size: var(--fs-13);
    color: var(--ink-dim);
    line-height: 1.6;
    max-width: 480px;
    margin: 0;
  }
  .card p b {
    color: var(--ink);
    font-weight: 600;
  }
  .card .hint {
    font-size: var(--fs-11);
    color: var(--ink-mute);
    margin-top: var(--sp-1);
  }

  /* ═══ Field (pool list) ═══ */
  .field {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: var(--sp-2);
    text-align: left;
  }
  .field-label {
    font-size: var(--fs-11);
    color: var(--ink-dim);
    font-weight: 500;
  }

  .pool-empty {
    padding: var(--sp-4);
    text-align: center;
    color: var(--ink-mute);
    font-size: var(--fs-11);
    border: 1px dashed var(--line);
    border-radius: var(--radius-md);
  }
  .pool-empty .link {
    background: none;
    border: none;
    color: var(--info);
    cursor: pointer;
    font-size: var(--fs-11);
    margin-left: var(--sp-1);
    text-decoration: underline;
    font-family: inherit;
  }

  .pool-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .pool-option {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: var(--sp-3);
    border-radius: var(--radius-sm);
    border: 1px solid var(--line);
    background: var(--panel-deep);
    cursor: pointer;
    transition: border-color 0.12s, background 0.12s;
    text-align: left;
    font-family: inherit;
    color: var(--ink);
  }
  .pool-option:hover {
    border-color: var(--line-bright);
  }
  .pool-option.selected {
    border-color: var(--info-border);
    background: var(--info-dim);
  }
  .pool-radio {
    width: 14px;
    height: 14px;
    border: 1.5px solid var(--ink-faint);
    border-radius: 50%;
    flex-shrink: 0;
    position: relative;
    transition: border-color 0.12s;
  }
  .pool-option.selected .pool-radio {
    border-color: var(--info);
  }
  .pool-option.selected .pool-radio::after {
    content: '';
    position: absolute;
    inset: 3px;
    border-radius: 50%;
    background: var(--info);
  }
  .pool-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .pool-name {
    font-size: var(--fs-13);
    color: var(--ink);
    font-weight: 500;
  }
  .pool-meta {
    font-size: var(--fs-11);
    color: var(--ink-mute);
  }
  .pool-size {
    font-size: var(--fs-11);
    color: var(--ink-dim);
    font-variant-numeric: tabular-nums;
  }

  /* ═══ Botones ═══ */
  .actions {
    display: flex;
    gap: var(--sp-2);
    margin-top: var(--sp-2);
  }
  .btn {
    padding: 9px 18px;
    border-radius: var(--radius-sm);
    border: 1px solid transparent;
    font-size: var(--fs-12);
    font-weight: 600;
    font-family: inherit;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: filter 0.12s, background 0.12s, color 0.12s;
  }
  .btn-block {
    width: 100%;
    justify-content: center;
  }
  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    filter: none;
  }
  .btn-primary {
    background: var(--signal);
    color: var(--canvas);
  }
  .btn-primary:not(:disabled):hover {
    filter: brightness(1.08);
  }
  .btn-secondary {
    background: transparent;
    color: var(--ink-dim);
    border: 1px solid var(--line);
  }
  .btn-secondary:not(:disabled):hover {
    color: var(--ink);
    background: var(--line);
  }
  .btn-arrow {
    width: 11px;
    height: 11px;
  }

  /* ═══ Install · barra indeterminada ═══ */
  .install-msg {
    color: var(--ink) !important;
    min-height: 1.2em;
  }
  .install-bar-wrap {
    width: 100%;
    max-width: 380px;
    padding: 0 var(--sp-1);
  }
  .install-bar {
    height: 6px;
    background: var(--panel-deep);
    border-radius: 3px;
    overflow: hidden;
    border: 1px solid var(--line);
    position: relative;
  }
  .install-bar-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 30%;
    background: linear-gradient(
      90deg,
      transparent 0%,
      var(--signal) 50%,
      transparent 100%
    );
    animation: indeterminate 1.6s ease-in-out infinite;
  }
  @keyframes indeterminate {
    0%   { left: -30%; }
    100% { left: 100%; }
  }

  /* ═══ Error box ═══ */
  .error-box {
    width: 100%;
    max-width: 380px;
    padding: var(--sp-3);
    background: var(--crit-dim);
    border: 1px solid var(--crit-border);
    border-radius: var(--radius-sm);
    color: var(--ink-dim);
    font-size: var(--fs-11);
    font-family: var(--font-mono);
    line-height: 1.55;
    text-align: left;
    word-break: break-word;
  }
</style>
