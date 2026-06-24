<script>
  /**
   * StorageOverview · Vista principal de almacenamiento.
   * ─────────────────────────────────────────────────────
   * Tres secciones verticales:
   *   1. Lista de pools (expandibles, con kebab → toolbar inline)
   *   2. Observados — filesystems BTRFS huérfanos (si hay)
   *   3. Alertas del sistema (si hay)
   *
   * Estado UI propio (no leak al padre):
   *   · expandedPools — Set de pool names expandidos
   *   · kebabOpenFor  — pool name con kebab abierto (uno solo a la vez)
   *
   * Click-outside listener registrado en onMount/onDestroy cierra el kebab
   * al pulsar fuera.
   *
   * Props (datos del padre):
   *   · pools, disks, alerts, orphanFilesystems, divergences, snapshots
   *   · scanning, refreshing, scrubbing, scrubMsg
   *
   * Eventos (acciones que requieren orquestación del padre):
   *   · rescan            — re-escanea buses
   *   · create-pool       — abrir wizard
   *   · refresh-observed  — forzar re-scan del observer
   *   · scrub             { poolName } — disparar scrub
   *   · export-pool       { poolName } — abrir export wizard
   *   · import-orphan     { fs } — abrir import modal
   *   · destroy-orphan    { fs } — abrir destroy modal
   *   · load-snapshots    { poolName } — cargar snapshots lazy al expandir
   */
  import { createEventDispatcher, onMount, onDestroy } from 'svelte';
  import {
    SectionHead, Badge, LED, EmptyState, DataTable,
  } from '$lib/ui';
  import {
    fmtBytes, fmtDate, inferDiskRole,
    healthLabel, healthVariant,
    usageVariant, ledVariantForHealth, smartVariant,
  } from './formatters.js';
  import './views-styles.css';

  export let pools = [];
  export let disks = {};
  export let alerts = [];
  export let orphanFilesystems = [];
  export let divergences = [];
  export let snapshots = {};
  export let scanning = false;
  export let refreshing = false;
  export let scrubbing = {};
  export let scrubMsg = '';

  const dispatch = createEventDispatcher();

  // El backend no siempre rellena usage_percent; lo calculamos desde
  // used/total igual que las KPIs de cabecera (evita la barra a 0%).
  function poolPct(pool) {
    const total = pool?.usage?.total_bytes || 0;
    const used = pool?.usage?.used_bytes || 0;
    return total > 0 ? Math.round((used / total) * 100) : 0;
  }

  // ─── Upgrade a RAID1 (contextual) ────────────────────────────────
  // Un pool single puede subir a raid1 si hay un disco libre que añadir,
  // o si ya tiene 2+ discos (conversión directa). Solo pools managed.
  function canUpgradeToRaid1(pool) {
    if (!pool || pool.profile !== 'single') return false;
    if (pool.control_state && pool.control_state !== 'managed') return false;
    const freeDisks = disks.eligible?.length || 0;
    const poolDisks = pool.devices?.length || 0;
    return freeDisks >= 1 || poolDisks >= 2;
  }

  // ─── UI state interno ────────────────────────────────────────────
  let expandedPools = new Set();
  let kebabOpenFor = null;

  function togglePoolExpand(poolName) {
    kebabOpenFor = null;
    if (expandedPools.has(poolName)) {
      expandedPools.delete(poolName);
    } else {
      expandedPools.add(poolName);
      dispatch('load-snapshots', { poolName });
    }
    expandedPools = expandedPools; // reactivity trigger
  }

  function toggleKebab(poolName, event) {
    event.stopPropagation();
    kebabOpenFor = kebabOpenFor === poolName ? null : poolName;
  }

  // Click outside → cerrar kebab
  function onDocClick() {
    kebabOpenFor = null;
  }

  onMount(() => {
    window.addEventListener('click', onDocClick);
  });
  onDestroy(() => {
    window.removeEventListener('click', onDocClick);
  });
</script>

<!-- ══ Sección: Volúmenes (pools) ══ -->
<div class="st-section">
  <div class="section-row">
    <SectionHead count={pools.length > 0 ? `· ${pools.length} activos` : ''}>
      Volúmenes
    </SectionHead>
    <div class="section-actions">
      <button class="btn-secondary" on:click={() => dispatch('rescan')} disabled={scanning}>
        {scanning ? '▸ Escaneando...' : '↻ Escanear'}
      </button>
      <button
        class="btn-primary"
        on:click={() => dispatch('create-pool')}
        disabled={!(disks.eligible?.length > 0)}
        title={disks.eligible?.length > 0
          ? 'Crear un nuevo pool de almacenamiento'
          : 'No hay discos libres para crear un pool'}
      >
        + Nuevo volumen
      </button>
    </div>
  </div>

  {#if pools.length === 0}
    <EmptyState
      icon="◇"
      title="Sin volúmenes configurados"
      hint={orphanFilesystems.length > 0
        ? `Se detectaron ${orphanFilesystems.length} filesystem(s) huérfano(s). Puedes importarlos como pool.`
        : 'Crea un volumen nuevo para empezar.'}
    />
  {:else}
    <div class="pools">
      {#each pools as pool (pool.name)}
        <div
          class="pool"
          class:open={expandedPools.has(pool.name)}
          class:degraded={pool.health?.status === 'degraded' || pool.health?.status === 'at_risk' || pool.health?.status === 'unstable'}
          class:crit={!pool.mounted || pool.health?.status === 'critical'}
        >
          <!-- Pool header -->
          <div class="pool-head" on:click={() => togglePoolExpand(pool.name)}
               on:keydown={(e) => e.key === 'Enter' && togglePoolExpand(pool.name)}
               role="button" tabindex="0">
            <div class="pool-head-icon"></div>
            <div class="pool-ident">
              <div class="pool-name">
                {pool.name}
                {#if pool.is_primary}
                  <Badge size="sm" variant="accent">primary</Badge>
                {/if}
              </div>
              <div class="pool-meta">
                BTRFS · {pool.profile || 'single'} ·
                {pool.devices?.length || 0} disco{pool.devices?.length === 1 ? '' : 's'} ·
                {fmtBytes(pool.usage?.used_bytes)} usados
                {#if canUpgradeToRaid1(pool)}
                  <button
                    class="raid-upgrade-chip"
                    on:click|stopPropagation={() => dispatch('upgrade-raid', { pool })}
                    title="Hay disco disponible: convertir este pool a RAID1 (espejo)"
                  >⇪ raid1 disponible</button>
                {/if}
              </div>
            </div>
            <div class="pool-bar-wrap">
              <div class="cap-bar">
                <div class="cap-track">
                  <div
                    class="cap-fill {usageVariant(poolPct(pool))}"
                    style="width:{poolPct(pool)}%"
                  ></div>
                </div>
                <span class="cap-pct">{poolPct(pool)}%</span>
              </div>
            </div>
            <div class="pool-size">{fmtBytes(pool.usage?.total_bytes)}</div>
            <div class="pool-status">
              <LED size={8} variant={ledVariantForHealth(pool.health?.status)} />
            </div>
            <div class="pool-chev" class:rot={expandedPools.has(pool.name)}>›</div>

            <button
              class="pool-kebab"
              class:active={kebabOpenFor === pool.name}
              on:click={(e) => toggleKebab(pool.name, e)}
              title="Acciones"
            >⋮</button>
          </div>

          <!-- Toolbar inline de acciones -->
          {#if kebabOpenFor === pool.name}
            <div
              class="pool-actions-bar"
              on:click|stopPropagation
              on:keydown
              role="toolbar"
              aria-label="Acciones del pool {pool.name}"
              tabindex="-1"
            >
              <button class="pa-btn" disabled title="Disponible en Fase B">
                <span>Snapshot</span>
                <span class="pa-tag">Fase B</span>
              </button>
              <button
                class="pa-btn"
                on:click={() => { dispatch('scrub', { poolName: pool.name }); kebabOpenFor = null; }}
                disabled={scrubbing[pool.name]}
              >
                <span>{scrubbing[pool.name] ? 'Iniciando...' : 'Verificar integridad'}</span>
              </button>
              <button
                class="pa-btn danger"
                on:click={() => { dispatch('export-pool', { poolName: pool.name }); kebabOpenFor = null; }}
              >
                <span>Desmontar</span>
              </button>
              {#if canUpgradeToRaid1(pool)}
                <button
                  class="pa-btn"
                  on:click={() => { dispatch('upgrade-raid', { pool }); kebabOpenFor = null; }}
                >
                  <span>Mejorar a RAID1</span>
                </button>
              {/if}
            </div>
          {/if}

          <!-- Pool expanded body -->
          {#if expandedPools.has(pool.name)}
            <div class="pool-body">

              <div class="pool-info-grid">
                <div class="pig-col">
                  <div class="pig-label">Total</div>
                  <div class="pig-value">{fmtBytes(pool.usage?.total_bytes)}</div>
                </div>
                <div class="pig-col edge-ok">
                  <div class="pig-label">Usado</div>
                  <div class="pig-value tc-accent">{fmtBytes(pool.usage?.used_bytes)}</div>
                </div>
                <div class="pig-col">
                  <div class="pig-label">Libre</div>
                  <div class="pig-value">{fmtBytes(pool.usage?.available_bytes)}</div>
                </div>
                <div class="pig-col">
                  <div class="pig-label">Uso</div>
                  <div class="pig-value" class:warn={poolPct(pool) > 75} class:crit={poolPct(pool) > 90}>
                    {poolPct(pool)}%
                  </div>
                </div>
                <div class="pig-col">
                  <div class="pig-label">Health</div>
                  <div class="pig-value pig-flex">
                    <LED size={7} variant={ledVariantForHealth(pool.health?.status)} />
                    <span>{pool.health?.status || '—'}</span>
                  </div>
                </div>
                <div class="pig-col">
                  <div class="pig-label">Mount</div>
                  <div class="pig-value mono sm pig-trunc">{pool.mount_point || '—'}</div>
                </div>
              </div>

              <!-- Disk table -->
              <div class="pool-disks">
                <div class="pd-head">
                  Discos del volumen · {pool.devices?.length || 0}
                  <span class="tc-mute todo">
                    (temp y horas pendiente backend)
                  </span>
                </div>
                <DataTable cols="40px 1fr 110px 80px 80px 140px" headers={['', 'Modelo', 'Dispositivo', 'Capacidad', 'Rol', 'SMART']}>
                  {#each (pool.devices || []) as disk, i}
                    <div class="dt-row">
                      <span class="disk-idx">D{i + 1}</span>
                      <span class="mono dt-trunc">{disk.model || '—'}</span>
                      <span class="mono dt-trunc">{disk.current_path || '—'}</span>
                      <span class="disk-cap">{fmtBytes(disk.size_bytes) || '—'}</span>
                      <span>
                        <Badge size="sm" variant={inferDiskRole(pool.devices, i, pool.profile) === 'parity' ? 'warn' : 'default'}>
                          {inferDiskRole(pool.devices, i, pool.profile)}
                        </Badge>
                      </span>
                      <span class="dt-flex">
                        <LED size={7} variant={smartVariant(disk.smart_status)} />
                        <span class="tc-dim sm">{disk.smart_status || 'unknown'}</span>
                      </span>
                    </div>
                  {/each}
                </DataTable>
              </div>

              <!-- Snapshots resumen (top 5) -->
              {#if snapshots[pool.name]?.length > 0}
                <div class="pool-snapshots">
                  <div class="pd-head">
                    Snapshots · {snapshots[pool.name].length}
                  </div>
                  <div class="snap-list">
                    {#each snapshots[pool.name].slice(0, 5) as snap}
                      <div class="snap-row">
                        <span class="mono">{snap.name || snap}</span>
                        {#if snap.used}
                          <span class="tc-mute">{fmtBytes(snap.used)}</span>
                        {/if}
                        {#if snap.created}
                          <span class="tc-mute">{fmtDate(snap.created)}</span>
                        {/if}
                      </div>
                    {/each}
                    {#if snapshots[pool.name].length > 5}
                      <div class="snap-more">
                        <span class="tc-mute">+ {snapshots[pool.name].length - 5} más · ver pestaña Snapshots</span>
                      </div>
                    {/if}
                  </div>
                </div>
              {/if}

            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if scrubMsg}
    <div class="msg">{scrubMsg}</div>
  {/if}
</div>

<!-- ══ Sección: Observados (orphan BTRFS) ══ -->
{#if orphanFilesystems.length > 0}
  <div class="st-section">
    <div class="section-row">
      <SectionHead count="· {orphanFilesystems.length}">
        Observados · no gestionados
      </SectionHead>
      <div class="section-actions">
        <button class="btn-secondary" on:click={() => dispatch('refresh-observed')} disabled={refreshing}>
          {refreshing ? '▸ Actualizando...' : '↻ Refrescar'}
        </button>
      </div>
    </div>

    <div class="observed-list">
      {#each orphanFilesystems as fs (fs.uuid)}
        <div class="observed-card">
          <div class="obs-head">
            <div class="obs-title">
              <span class="obs-label">{fs.label || '(sin label)'}</span>
              <Badge size="sm" variant={healthVariant(fs.observation_health)}>
                {healthLabel(fs.observation_health)}
              </Badge>
            </div>
            <div class="obs-uuid mono tc-mute">
              UUID: {fs.uuid}
            </div>
          </div>

          <div class="obs-info">
            <div class="obs-row">
              <span class="tc-mute">Tipo:</span>
              <span class="mono">BTRFS · {fs.profile || 'single'}</span>
            </div>
            <div class="obs-row">
              <span class="tc-mute">Discos:</span>
              <span class="mono">
                {fs.devices_online}/{fs.devices_expected} online
                {#if fs.devices_missing > 0}
                  · <span class="tc-warn">faltan {fs.devices_missing}</span>
                {/if}
              </span>
            </div>
            {#if fs.size_bytes > 0}
              <div class="obs-row">
                <span class="tc-mute">Capacidad:</span>
                <span class="mono">{fmtBytes(fs.size_bytes)} · {fmtBytes(fs.used_bytes)} usados</span>
              </div>
            {/if}
            {#if fs.is_mounted}
              <div class="obs-row">
                <span class="tc-mute">Montado:</span>
                <span class="mono">{fs.mount_point}</span>
              </div>
            {:else}
              <div class="obs-row">
                <span class="tc-mute">Estado:</span>
                <span class="mono">desmontado</span>
              </div>
            {/if}
          </div>

          <div class="obs-devices">
            <div class="obs-devices-label tc-mute">Discos físicos:</div>
            <div class="obs-devices-list">
              {#each (fs.devices || []) as dev}
                <span class="mono obs-disk-pill">{dev.path}</span>
              {/each}
            </div>
          </div>

          <div class="obs-actions">
            <button
              class="btn-primary"
              on:click={() => dispatch('import-orphan', { fs })}
              disabled={fs.devices_missing > 0}
              title={fs.devices_missing > 0
                ? 'No se puede importar: faltan discos'
                : 'Importar como pool gestionado (preserva datos)'}
            >
              ⬇ Importar como pool
            </button>
            <button
              class="btn-secondary"
              on:click={() => dispatch('destroy-orphan', { fs })}
              title="DESTRUIR — borra todos los datos de los discos"
            >
              ⚠ Destruir
            </button>
          </div>
        </div>
      {/each}
    </div>

    {#if divergences.length > 0}
      <div class="divergences">
        {#each divergences.filter(d => d.severity !== 'info') as div}
          <div class="div-row" class:warn={div.severity === 'warning'} class:crit={div.severity === 'critical'}>
            <LED size={7} variant={div.severity === 'critical' ? 'crit' : 'warn'} />
            <div>
              <div>{div.detail}</div>
              {#if div.hint}
                <div class="tc-mute sm">{div.hint}</div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<!-- ══ Sección: Alertas del sistema ══ -->
{#if alerts.length > 0}
  <div class="st-section">
    <SectionHead count="· {alerts.length}">Alertas del sistema</SectionHead>
    <div class="alerts-list">
      {#each alerts as alert}
        <div class="alert-row" class:crit={alert.level === 'critical'} class:warn={alert.level === 'warning'}>
          <LED size={7} variant={alert.level === 'critical' ? 'crit' : 'warn'} />
          <div class="alert-body">
            <div class="alert-msg">{alert.message}</div>
            {#if alert.pool}
              <div class="alert-meta">
                pool: <span class="mono">{alert.pool}</span> ·
                {fmtDate(alert.timestamp)}
              </div>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
  /* CSS específico de esta vista (no usado en otras) */

  /* Pool card ───── */
  .pools {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .pool {
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 10px;
    font-family: var(--font-mono);
    transition: border-color 0.12s, background 0.12s;
    overflow: hidden;
  }
  .pool.open { border-color: rgba(255, 255, 255, 0.14); }
  .pool.degraded { border-left: 3px solid var(--warn); }
  .pool.crit { border-left: 3px solid var(--crit); }

  .pool-head {
    display: grid;
    grid-template-columns: 24px 1fr 220px 80px 18px 18px 24px;
    gap: 16px;
    align-items: center;
    padding: 12px 16px;
    cursor: pointer;
    user-select: none;
  }
  .pool-head:hover { background: var(--side-hover); }

  .pool-head-icon {
    width: 14px;
    height: 14px;
    border-radius: 4px;
    background: var(--signal, #00ff9f);
    transition: transform 0.25s cubic-bezier(0.16, 1, 0.3, 1);
  }
  .pool.open .pool-head-icon {
    transform: rotate(45deg);
  }

  .pool-ident {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .pool-name {
    font-size: 13px;
    color: var(--ink);
    font-weight: 600;
    letter-spacing: 0.3px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .pool-meta {
    font-size: 10px;
    color: var(--ink-mute);
    letter-spacing: 0.3px;
  }

  /* Chip contextual: el pool puede subir a RAID1 (hay disco libre) */
  .raid-upgrade-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-left: 8px;
    padding: 1px 8px 2px;
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.4px;
    color: var(--signal);
    background: rgba(0, 255, 159, 0.07);
    border: 1px solid rgba(0, 255, 159, 0.35);
    border-radius: 999px;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s, box-shadow 0.12s;
    vertical-align: middle;
  }
  .raid-upgrade-chip:hover {
    background: rgba(0, 255, 159, 0.14);
    border-color: var(--signal);
    box-shadow: 0 0 8px rgba(0, 255, 159, 0.25);
  }

  .pool-bar-wrap { min-width: 0; }
  .pool-size {
    font-size: 11px;
    color: var(--ink);
    text-align: right;
    font-feature-settings: "tnum";
  }
  .pool-status {
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .pool-chev {
    color: var(--ink-mute);
    font-size: 14px;
    transition: transform 0.15s;
    text-align: center;
  }
  .pool-chev.rot { transform: rotate(90deg); color: var(--signal); }

  .pool-kebab {
    width: 24px;
    height: 24px;
    background: transparent;
    border: none;
    color: var(--ink-mute);
    cursor: pointer;
    font-size: 14px;
    font-family: var(--font-mono);
    transition: color 0.12s;
  }
  .pool-kebab:hover { color: var(--signal); }
  .pool-kebab.active {
    color: var(--signal);
    background: var(--side-hover);
  }

  /* Toolbar inline ───── */
  .pool-actions-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    padding: 10px 16px;
    background: var(--bg-2);
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    font-family: var(--font-mono);
    animation: pab-in 0.15s ease-out;
  }
  @keyframes pab-in {
    from { opacity: 0; max-height: 0; padding-top: 0; padding-bottom: 0; }
    to   { opacity: 1; max-height: 60px; padding-top: 10px; padding-bottom: 10px; }
  }

  .pa-btn {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 6px 12px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    color: var(--fg-3, #9c9ca4);
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.3px;
    cursor: pointer;
    transition: all 0.12s;
  }
  .pa-btn:not(:disabled):hover {
    border-color: rgba(0, 255, 159, 0.35);
    color: var(--nim-green, #00ff9f);
    background: rgba(0, 255, 159, 0.05);
  }
  .pa-btn.danger:not(:disabled):hover {
    border-color: rgba(255, 90, 90, 0.35);
    color: var(--st-crit, #ff5a5a);
    background: rgba(255, 90, 90, 0.05);
  }
  .pa-btn:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
  .pa-tag {
    color: var(--fg-5, #5a5a62);
    font-size: 8px;
    letter-spacing: 0.8px;
    text-transform: uppercase;
    border: 1px solid var(--bd-3, #2a2a32);
    border-radius: 3px;
    padding: 1px 4px;
    margin-left: 2px;
  }

  /* Pool body ───── */
  .pool-body {
    border-top: 1px solid var(--border);
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 18px;
    background: var(--bg);
  }

  .pool-info-grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 8px;
  }
  .pig-col {
    background: var(--bg-card, #15151a);
    border-radius: 7px;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
    position: relative;
    overflow: hidden;
  }
  /* Borde de color a la izquierda (variante v3) */
  .pig-col.edge-ok::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    width: 2px;
    height: 100%;
    background: var(--st-ok, #00ff9f);
    opacity: 0.6;
  }
  .pig-label {
    font-size: 9px;
    color: var(--fg-5, #5a5a62);
    text-transform: uppercase;
    letter-spacing: 0.6px;
  }
  .pig-value {
    font-size: 14px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-feature-settings: "tnum";
  }
  .pig-value.pig-flex {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .pig-value.pig-trunc {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pig-value.mono { font-family: var(--font-mono); }
  .pig-value.sm { font-size: 11px; }
  .pig-value.tc-accent { color: var(--st-ok, #00ff9f); }
  .pig-value.warn { color: var(--warn); }
  .pig-value.crit { color: var(--crit); }

  /* Barra de capacidad v3 (cabecera del pool) */
  .cap-bar {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .cap-track {
    flex: 1;
    height: 6px;
    background: var(--bd-2, #20202a);
    border-radius: 3px;
    overflow: hidden;
  }
  .cap-fill {
    height: 100%;
    border-radius: 3px;
    background: var(--st-ok, #00ff9f);
    transition: width 0.3s;
  }
  .cap-fill.warn { background: var(--st-warn, #ffc857); }
  .cap-fill.crit { background: var(--st-crit, #ff5a5a); }
  .cap-pct {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    min-width: 34px;
    text-align: right;
  }
  /* Capacidad de disco en blanco (no apagada) */
  .disk-cap {
    color: var(--fg, #f0f0f0);
    font-feature-settings: "tnum";
  }

  /* Disk table: cabecera integrada en la card (no flotando) ───── */
  .pool-disks :global(.data-table) {
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border: 1px solid var(--bd-2, #20202a);
    border-top: none;
  }
  .pd-head {
    font-size: 10px;
    color: var(--fg-3, #9c9ca4);
    letter-spacing: 0.5px;
    font-family: var(--font-mono);
    padding: 9px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-bottom: none;
    border-radius: 8px 8px 0 0;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .pd-head .todo {
    font-size: 9px;
    color: var(--fg-5, #5a5a62);
    letter-spacing: 0.3px;
  }

  /* Snapshots list ───── */
  .snap-list {
    display: flex;
    flex-direction: column;
    gap: 1px;
    background: var(--border);
    border: 1px solid var(--border);
  }
  .snap-row {
    padding: 6px 12px;
    background: var(--bg-1);
    display: flex;
    align-items: center;
    gap: 14px;
    font-size: 10px;
  }
  .snap-more {
    padding: 6px 12px;
    background: var(--bg-2);
    font-size: 10px;
    text-align: center;
  }

  /* Observed list ───── */
  .observed-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .observed-card {
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-left: 3px solid var(--warn);
    border-radius: 10px;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .obs-head {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .obs-title {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .obs-label {
    font-weight: 600;
    color: var(--ink);
  }

  .obs-uuid {
    font-size: 11px;
  }

  .obs-info {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .obs-row {
    display: flex;
    gap: 8px;
    font-size: 13px;
  }

  .obs-row .tc-mute {
    min-width: 90px;
  }

  .obs-devices {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .obs-devices-label {
    font-size: 12px;
  }

  .obs-devices-list {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .obs-disk-pill {
    background: var(--bg-inner);
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 3px;
    font-size: 12px;
    color: var(--ink-dim);
  }

  .obs-actions {
    display: flex;
    gap: 8px;
    padding-top: 12px;
    border-top: 1px solid var(--line);
  }

  .divergences {
    margin-top: 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .div-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 12px;
    border-left: 2px solid var(--warn);
    background: var(--bg-1);
    font-size: 13px;
  }

  .div-row.crit {
    border-left-color: var(--crit);
  }

  /* Alerts ───── */
  .alerts-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .alert-row {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 10px 14px;
    background: var(--bg-1);
    border: 1px solid var(--border);
    border-left: 2px solid var(--fg-mute);
    font-family: var(--font-mono);
  }
  .alert-row.warn { border-left-color: var(--warn); background: rgba(255,184,0,0.04); }
  .alert-row.crit { border-left-color: var(--crit); background: rgba(255,90,90,0.04); }
  .alert-body {
    display: flex;
    flex-direction: column;
    gap: 3px;
    flex: 1;
    min-width: 0;
  }
  .alert-msg {
    font-size: 11px;
    color: var(--fg);
    letter-spacing: 0.3px;
  }
  .alert-meta {
    font-size: 9px;
    color: var(--fg-mute);
  }

  /* ─── Botones (Design System Beta 8.1) ─── */
  .btn-secondary {
    padding: 5px 12px;
    border-radius: 5px;
    border: 1px solid var(--line);
    background: var(--bg-card);
    color: var(--ink-dim);
    font-size: 10px;
    font-weight: 500;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.4px;
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
    padding: 5px 12px;
    border-radius: 5px;
    border: 1px solid rgba(0, 255, 159, 0.3);
    background: rgba(0, 255, 159, 0.06);
    color: var(--signal);
    font-size: 10px;
    font-weight: 600;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.4px;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-primary:hover:not(:disabled) {
    border-color: var(--signal);
    background: rgba(0, 255, 159, 0.12);
  }
  .btn-primary:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  /* Helpers de celda para DataTable v3 */
  .dt-trunc {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .dt-flex {
    display: flex;
    align-items: center;
    gap: 6px;
  }
</style>
