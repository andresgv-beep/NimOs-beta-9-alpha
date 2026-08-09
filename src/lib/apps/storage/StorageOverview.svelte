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
    healthLabel, healthVariant, healthStatusLabel,
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

  // AUDIT (menor): preferir el usage_percent del BACKEND — la app usaba
  // Math.round y el backend trunca, así que el mismo pool podía mostrar
  // 89% aquí y 90% en el widget (±1% en los umbrales de alarma). Fallback
  // al cálculo local solo si el backend no lo rellena.
  function poolPct(pool) {
    if (pool?.usage?.usage_percent != null) return pool.usage.usage_percent;
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
    <SectionHead count={pools.length > 0 ? `${pools.length} activos` : ''}>
      Volúmenes
    </SectionHead>
    <div class="section-actions">
      <button class="btn-secondary" on:click={() => dispatch('rescan')} disabled={scanning}>
        {scanning ? 'Escaneando…' : 'Escanear'}
      </button>
      <button
        class="btn-primary"
        on:click={() => dispatch('create-pool')}
        disabled={!(disks.eligible?.length > 0)}
        title={disks.eligible?.length > 0
          ? 'Crear un nuevo pool de almacenamiento'
          : 'No hay discos libres para crear un pool'}
      >
        Nuevo volumen
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
          class:crit={pool.health?.status === 'critical' || (!pool.mounted && pool.health?.status !== 'missing')}
          class:missing={pool.health?.status === 'missing'}
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
                {#if pool.kernel_devices_missing > 0}
                  <span class="sm tc-warn mono" title="El kernel ve {pool.kernel_devices_expected} discos en este filesystem y faltan {pool.kernel_devices_missing}. Puede haber discos ausentes que NimOS no tiene registrados (p.ej. añadidos por CLI fuera de la app).">· kernel {pool.kernel_devices_online}/{pool.kernel_devices_expected} · faltan {pool.kernel_devices_missing}</span>
                {/if}
                {#if canUpgradeToRaid1(pool)}
                  <button
                    class="raid-upgrade-chip"
                    on:click|stopPropagation={() => dispatch('upgrade-raid', { pool })}
                    title="Hay disco disponible: convertir este pool a RAID1 (espejo)"
                  >RAID1 disponible</button>
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
              {#if pool.health?.status === 'missing'}
                <span class="pool-missing-tag">no detectado</span>
              {/if}
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

          {#if pool.health?.resilver_active}
            <div class="repair-bar">
              <div class="repair-bar-head">
                <span class="repair-bar-label">
                  Reparando pool · reconstruyendo redundancia
                </span>
                <span class="repair-bar-pct">
                  {(pool.health?.resilver_progress ?? 0).toFixed(1)}%
                </span>
              </div>
              <div class="repair-track">
                <div
                  class="repair-fill"
                  style="width:{pool.health?.resilver_progress ?? 0}%"
                ></div>
              </div>
              <span class="repair-hint">
                No apagues el equipo. El proceso continúa en segundo plano.
              </span>
            </div>
          {/if}

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
                  <div class="pig-label">Estado</div>
                  <div class="pig-value pig-flex">
                    <LED size={7} variant={ledVariantForHealth(pool.health?.status)} />
                    <span>{healthStatusLabel(pool.health?.status)}</span>
                  </div>
                </div>
                <div class="pig-col">
                  <div class="pig-label">Ubicación</div>
                  <div class="pig-value mono sm pig-trunc">{pool.mount_point || '—'}</div>
                </div>
              </div>

              <!-- Disk table -->
              <div class="pool-disks">
                <div class="pd-head">
                  Discos del volumen · {pool.devices?.length || 0}
                </div>
                <DataTable cols="40px 1fr 110px 80px 80px 140px" headers={['', 'Modelo', 'Dispositivo', 'Capacidad', 'Rol', 'SMART']}>
                  {#each (pool.devices || []) as disk, i}
                    <div class="dt-row">
                      <span class="disk-idx">D{i + 1}</span>
                      <span class="dt-trunc">{disk.model || '—'}</span>
                      <span class="dt-trunc">{disk.current_path || '—'}</span>
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
      <SectionHead count="{orphanFilesystems.length}">
        Volúmenes detectados
      </SectionHead>
      <div class="section-actions">
        <button class="btn-secondary" on:click={() => dispatch('refresh-observed')} disabled={refreshing}>
          {refreshing ? 'Actualizando…' : 'Actualizar'}
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
              disabled={(fs.devices_online ?? (fs.devices_expected - fs.devices_missing)) <= 0}
              title={(fs.devices_online ?? (fs.devices_expected - fs.devices_missing)) <= 0
                ? 'No se puede importar: no hay discos disponibles'
                : fs.devices_missing > 0
                  ? 'Importar en modo solo-lectura (faltan discos) — podrás recuperar tus datos y reparar el pool'
                  : 'Importar como pool gestionado (preserva datos)'}
            >
              Importar como pool
            </button>
            {#if fs.devices_missing > 0 && (fs.devices_online ?? (fs.devices_expected - fs.devices_missing)) > 0}
              <span class="obs-degraded-hint">
                Se importará en solo-lectura · faltan {fs.devices_missing} disco(s)
              </span>
            {/if}
            <button
              class="btn-secondary"
              on:click={() => dispatch('destroy-orphan', { fs })}
              title="DESTRUIR — borra todos los datos de los discos"
            >
              Destruir
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
    <SectionHead count="{alerts.length}">Alertas del sistema</SectionHead>
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
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    font-family: var(--font-sans);
    transition: border-color 0.12s, background 0.12s;
    overflow: hidden;
  }
  .pool.open { border-color: var(--line-bright); }
  .pool.degraded { border-left: 3px solid var(--warn); }
  .pool.crit { border-left: 3px solid var(--crit); }
  .pool.missing { border-left: 3px solid var(--fg-4, #7a7a82); }
  .pool-missing-tag {
    font-size: 10.5px;
    font-family: var(--font-sans);
    font-weight: 600;
    color: var(--ink-mute);
    border: 1px solid var(--line-strong);
    border-radius: 999px;
    padding: 1px 8px;
    white-space: nowrap;
  }

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
    background: var(--signal, #5b8ff9);
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
    font-size: 14px;
    color: var(--ink);
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .pool-meta {
    font-size: 12px;
    color: var(--ink-mute);
    line-height: 1.5;
  }

  /* Chip contextual: el pool puede subir a RAID1 (hay disco libre) */
  .raid-upgrade-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-left: 8px;
    padding: 1px 9px 2px;
    font-family: var(--font-sans);
    font-size: 10.5px;
    font-weight: 600;
    color: var(--signal);
    background: var(--signal-soft);
    border: 1px solid color-mix(in srgb, var(--signal) 35%, transparent);
    border-radius: 999px;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
    vertical-align: middle;
  }
  .raid-upgrade-chip:hover {
    background: var(--signal-dim);
    border-color: var(--signal);
  }

  .pool-bar-wrap { min-width: 0; }
  .pool-size {
    font-size: 12px;
    font-family: var(--font-sans);
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
    font-family: var(--font-sans);
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
    gap: 6px;
    padding: 10px 16px;
    background: var(--canvas-soft);
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
    font-family: var(--font-sans);
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
    background: var(--panel);
    border: 1px solid var(--line-bright);
    border-radius: 7px;
    color: var(--ink-dim);
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.12s;
  }
  .pa-btn:not(:disabled):hover {
    border-color: color-mix(in srgb, var(--signal) 35%, transparent);
    color: var(--signal);
    background: var(--signal-soft);
  }
  .pa-btn.danger:not(:disabled):hover {
    border-color: color-mix(in srgb, var(--crit) 35%, transparent);
    color: var(--crit);
    background: color-mix(in srgb, var(--crit) 6%, transparent);
  }
  .pa-btn:disabled {
    cursor: not-allowed;
    opacity: 0.45;
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
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
    position: relative;
    overflow: hidden;
  }
  /* Regla "menos verde": OK no pinta edge — el color queda para warn/crit */
  .pig-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--ink-faint);
  }
  .pig-value {
    font-size: 14px;
    color: var(--ink);
    font-family: var(--font-sans);
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
  .pig-value.tc-accent { color: var(--ink); }
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
    background: var(--line-bright);
    border-radius: 3px;
    overflow: hidden;
  }
  .cap-fill {
    height: 100%;
    border-radius: 3px;
    background: var(--ink-dim);
    transition: width 0.3s;
  }
  .cap-fill.warn { background: var(--warn); }
  .cap-fill.crit { background: var(--crit); }
  .cap-pct {
    font-family: var(--font-sans);
    font-size: 12px;
    color: var(--ink-mute);
    min-width: 34px;
    text-align: right;
  }
  /* Capacidad de disco en blanco (no apagada) */
  .disk-cap {
    color: var(--ink);
    font-feature-settings: "tnum";
  }

  /* Disk table: cabecera integrada en la card (no flotando) ───── */
  .pool-disks :global(.data-table) {
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border: 1px solid var(--line);
    border-top: none;
  }
  .pd-head {
    font-size: 12px;
    font-weight: 500;
    color: var(--ink-dim);
    font-family: var(--font-sans);
    padding: 9px 14px;
    background: var(--canvas-soft);
    border: 1px solid var(--line);
    border-bottom: none;
    border-radius: 8px 8px 0 0;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .pd-head .todo {
    font-size: 11px;
    font-weight: 400;
    color: var(--ink-faint);
  }

  /* Snapshots list ───── */
  .snap-list {
    display: flex;
    flex-direction: column;
    gap: 1px;
    background: var(--line);
    border: 1px solid var(--line);
    border-radius: 8px;
    overflow: hidden;
  }
  .snap-row {
    padding: 7px 12px;
    background: var(--panel);
    display: flex;
    align-items: center;
    gap: 14px;
    font-size: 12px;
  }
  .snap-more {
    padding: 7px 12px;
    background: var(--canvas-soft);
    font-size: 11px;
    text-align: center;
  }

  /* Observed list ───── */
  .observed-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .observed-card {
    background: var(--panel);
    border: 1px solid var(--line);
    border-left: 3px solid var(--warn);
    border-radius: 0 10px 10px 0;
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
    background: var(--canvas-soft);
    padding: 2px 8px;
    border: 1px solid var(--line);
    border-radius: 6px;
    font-size: 12px;
    color: var(--ink-dim);
  }

  .obs-actions {
    display: flex;
    gap: 8px;
    padding-top: 12px;
    border-top: 1px solid var(--line);
    flex-wrap: wrap;
    align-items: center;
  }

  .obs-degraded-hint {
    font-size: 0.78rem;
    color: var(--warn, #e0b341);
    opacity: 0.9;
  }

  .repair-bar {
    padding: 10px 16px 14px;
    border-top: 1px solid var(--line);
    background: var(--signal-soft);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .repair-bar-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .repair-bar-label {
    font-size: 0.82rem;
    font-weight: 500;
    color: var(--signal);
  }
  .repair-bar-pct {
    font-size: 0.82rem;
    font-variant-numeric: tabular-nums;
    font-family: var(--font-sans);
    color: var(--signal);
  }
  .repair-track {
    height: 6px;
    background: var(--line-bright);
    border-radius: 3px;
    overflow: hidden;
  }
  .repair-fill {
    height: 100%;
    background: var(--signal);
    border-radius: 3px;
    transition: width 0.6s ease;
  }
  .repair-hint {
    font-size: 0.74rem;
    color: var(--ink-mute);
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
    border-radius: 0 8px 8px 0;
    background: var(--panel);
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
    background: var(--panel);
    border: 1px solid var(--line);
    border-left: 2px solid var(--ink-faint);
    border-radius: 0 8px 8px 0;
    font-family: var(--font-sans);
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
    font-size: 13px;
    color: var(--ink);
  }
  .alert-meta {
    font-size: 11px;
    color: var(--ink-mute);
  }

  /* ─── Botones (Design System Beta 8.1) ─── */
  .btn-secondary {
    padding: 6px 14px;
    border-radius: 7px;
    border: 1px solid var(--line-bright);
    background: var(--panel);
    color: var(--ink-dim);
    font-size: 12px;
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
    padding: 6px 14px;
    border-radius: 7px;
    border: 1px solid color-mix(in srgb, var(--signal) 35%, transparent);
    background: var(--signal-soft);
    color: var(--signal);
    font-size: 12px;
    font-weight: 600;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
  }
  .btn-primary:hover:not(:disabled) {
    border-color: var(--signal);
    background: var(--signal-dim);
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
