
<script>
  /**
   * StorageDisks · Vista de discos del sistema.
   * ────────────────────────────────────────────
   * Tres secciones:
   *   · Discos asignados a volúmenes
   *   · Discos disponibles (con acción de limpieza confirmada)
   *   · USB si hay (read-only)
   *
   * Props:
   *   · pools             — array de pools del backend
   *   · disks             — { eligible, usb, nvme, ... }
   *   · orphanFilesystems — array de ObservedBtrfs no-managed (para diskStatus)
   *   · scanning          — bool, true mientras corre el rescan
   *
   * Eventos:
   *   · rescan      — solicitar re-escaneo de buses
   *   · create-pool — abrir wizard de creación
   *   · wipe        — { detail: { path } } — abrir dialog de wipe en el padre
   */
  import { createEventDispatcher } from 'svelte';
  import { SectionHead, BevelButton, EmptyState, Badge, LED, DataTable } from '$lib/ui';
  import { fmtBytes, smartVariant } from './formatters.js';
  import './views-styles.css';

  export let pools = [];
  export let disks = {};
  export let orphanFilesystems = [];
  export let scanning = false;

  const dispatch = createEventDispatcher();

  $: totalDisksAssigned = pools.reduce((s, p) => s + (p.devices?.length || 0), 0);
  $: totalDisksFree = (disks.eligible?.length || 0);

  // diskStatus — cruza el path con managed pools y observed orphans.
  // No es pura globalmente (depende de pools/orphanFilesystems), pero sí
  // dentro de este componente (solo lee props). Vive aquí porque solo
  // esta vista la usa.
  function diskStatus(diskPath) {
    if (!diskPath) return { kind: 'free', label: 'disponible', variant: 'accent' };

    for (const pool of pools) {
      for (const d of (pool.devices || [])) {
        const dPath = typeof d === 'string' ? d : (d.current_path || '');
        if (dPath === diskPath) {
          return {
            kind: 'managed',
            label: `pool ${pool.name}`,
            variant: 'success',
            poolName: pool.name,
            tooltip: `Disco en uso por el pool gestionado "${pool.name}"`,
          };
        }
      }
    }

    for (const fs of orphanFilesystems) {
      for (const dev of (fs.devices || [])) {
        if (dev.path === diskPath) {
          return {
            kind: 'orphan',
            label: 'BTRFS huérfano',
            variant: 'warn',
            fsUuid: fs.uuid,
            fsLabel: fs.label,
            tooltip: `Tiene un filesystem BTRFS no gestionado ` +
                     `(label: ${fs.label || 'sin label'}, UUID: ${fs.uuid}). ` +
                     `Importable desde sección Observados.`,
          };
        }
      }
    }

    return {
      kind: 'free',
      label: 'disponible',
      variant: 'accent',
      tooltip: 'Disco limpio, listo para crear un nuevo pool',
    };
  }
</script>

<div class="st-section">
  <div class="section-row">
    <SectionHead count={`${totalDisksAssigned + totalDisksFree} detectados`}>
      Discos del sistema
    </SectionHead>
    <div class="section-actions">
      <BevelButton size="sm" onClick={() => dispatch('rescan')} disabled={scanning}>
        {scanning ? 'Actualizando…' : 'Actualizar lista'}
      </BevelButton>
      <BevelButton
        variant="primary"
        size="sm"
        onClick={() => dispatch('create-pool')}
        disabled={!(disks.eligible?.length > 0)}
        title={disks.eligible?.length > 0
          ? 'Crear un nuevo pool con los discos libres'
          : 'No hay discos libres para crear un pool'}
      >
        Crear volumen
      </BevelButton>
    </div>
  </div>

  <!-- Discos asignados a pools -->
  {#if totalDisksAssigned > 0}
    <SectionHead count={`${totalDisksAssigned}`}>Discos en uso</SectionHead>
    {#each pools as pool}
      <div class="pool-group">
        <div class="pool-group-head">
          <div class="pool-group-title">
            <span class="pool-name">{pool.name}</span>
            <span class="pool-device-count">{(pool.devices || []).length} {(pool.devices || []).length === 1 ? 'disco' : 'discos'}</span>
            {#if pool.kernel_devices_missing > 0}
              <span
                class="pool-warning"
                title="El kernel ve {pool.kernel_devices_expected} discos en este filesystem y faltan {pool.kernel_devices_missing}. Puede haber discos ausentes que NimOS no tiene registrados (p.ej. añadidos por CLI fuera de la app)."
              >Faltan {pool.kernel_devices_missing} discos</span>
            {/if}
          </div>
          <span class:mounted={pool.mounted} class="pool-state">
            <span class="pool-state-dot"></span>
            {pool.mounted ? 'Montado' : 'Desmontado'}
          </span>
        </div>
        {#if pool.health?.resilver_active}
          <div class="pool-rebuild">
            Reconstruyendo redundancia · {(pool.health?.resilver_progress ?? 0).toFixed(1)}%
          </div>
        {/if}
        <DataTable cols="130px minmax(130px, 1fr) 100px 120px 112px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'SMART', '>Acción']}>
          {#each (pool.devices || []) as disk}
            <div class="dt-row">
              <span class="device-path dt-trunc">{disk.current_path || '—'}</span>
              <span class="dt-trunc">{disk.model || '—'}</span>
              <span>{fmtBytes(disk.size_bytes) || '—'}</span>
              <span class="dt-flex">
                <LED size={7} variant={smartVariant(disk.smart_status)} />
                <span class="tc-dim sm">{disk.smart_status || 'unknown'}</span>
              </span>
              <span class="disk-actions">
                {#if (disks.eligible?.length || 0) > 0}
                  <button
                    class="row-action"
                    on:click={() => dispatch('replace-device', { pool, disk })}
                    title={disk.smart_status === 'missing'
                      ? 'Reemplazar este disco que falta por uno libre (repara el pool)'
                      : 'Reemplazar este disco por uno libre'}
                  >
                    Reemplazar
                  </button>
                {:else}
                  <span class="no-action" title="No hay discos libres para reemplazar">—</span>
                {/if}
              </span>
            </div>
          {/each}
        </DataTable>
      </div>
    {/each}
  {/if}

  <!-- Discos libres -->
  <div style="margin-top:24px">
    <SectionHead count={`${disks.eligible?.length || 0}`}>Discos disponibles</SectionHead>
    {#if !disks.eligible || disks.eligible.length === 0}
      <EmptyState icon="◌" title="Sin discos libres" hint="Todos los discos están asignados a pools" />
    {:else}
      <DataTable cols="120px minmax(130px, 1fr) 90px 70px 110px 112px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Tipo', 'Estado', '>Acción']}>
        {#each disks.eligible as disk}
          {@const dPath = disk.path || '/dev/' + disk.name}
          {@const dStatus = diskStatus(dPath)}
          <div class="dt-row" class:has-orphan={dStatus.kind === 'orphan'}>
            <span class="device-path dt-trunc">{dPath}</span>
            <span class="dt-trunc">{disk.model || '—'}</span>
            <span>{fmtBytes(disk.size)}</span>
            <span>
              <Badge size="sm" variant={disk.rotational ? 'default' : 'info'}>
                {disk.rotational ? 'HDD' : 'SSD'}
              </Badge>
            </span>
            <span title={dStatus.tooltip || ''}>
              <Badge size="sm" variant={dStatus.variant}>
                {dStatus.label}
              </Badge>
              {#if dStatus.kind === 'orphan'}
                <div class="disk-orphan-hint tc-mute sm">
                  Datos preservables · ver Observados
                </div>
              {/if}
            </span>
            <span class="disk-actions">
              <button
                class="row-action danger"
                on:click={() => dispatch('wipe', { path: dPath, serial: disk.serial })}
                title={dStatus.kind === 'orphan'
                  ? 'Este disco tiene datos. Limpiarlo los borrará permanentemente.'
                  : 'Borrar particiones y restos de formatos anteriores'}
              >
                Limpiar disco
              </button>
            </span>
          </div>
        {/each}
      </DataTable>
    {/if}
  </div>

  <!-- USB si hay -->
  {#if disks.usb?.length > 0}
    <div style="margin-top:24px">
      <SectionHead count={`${disks.usb.length}`}>Dispositivos USB</SectionHead>
      <DataTable cols="130px 1fr 100px 120px 130px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Tipo', 'Estado']}>
        {#each disks.usb as disk}
          <div class="dt-row">
            <span class="device-path dt-trunc">{disk.path || '/dev/' + disk.name}</span>
            <span class="dt-trunc">{disk.model || '—'}</span>
            <span>{fmtBytes(disk.size)}</span>
            <span><Badge size="sm" variant="warn">USB</Badge></span>
            <span><Badge size="sm">externo</Badge></span>
          </div>
        {/each}
      </DataTable>
    </div>
  {/if}
</div>

<style>
  /* CSS específico de esta vista (no usado en otras → no va a views-styles.css) */

  .pool-group {
    margin-bottom: 18px;
  }
  .pool-group-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 48px;
    padding: 0 14px;
    background: var(--panel-elev, #252d38);
    border: 1px solid var(--bd-2, #20202a);
    border-bottom: none;
    border-radius: 8px 8px 0 0;
  }
  .pool-group-title {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .pool-name {
    color: var(--ink, #e7ebf0);
    font-size: 13px;
    font-weight: 650;
  }
  .pool-device-count {
    color: var(--ink-faint, #788392);
    font-size: 12px;
  }
  .pool-warning {
    padding: 3px 7px;
    border: 1px solid var(--warn-border, rgba(255, 184, 0, 0.35));
    border-radius: 4px;
    color: var(--warn, #ffc857);
    background: var(--warn-dim, rgba(255, 184, 0, 0.07));
    font-size: 11px;
  }
  .pool-state {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    color: var(--ink-faint, #788392);
    font-size: 11.5px;
    white-space: nowrap;
  }
  .pool-state-dot {
    width: 7px;
    height: 7px;
    border-radius: 2px;
    background: var(--ink-mute, #596372);
  }
  .pool-state.mounted { color: var(--ink-dim, #a8b0bc); }
  .pool-state.mounted .pool-state-dot { background: var(--signal, #5b8ff9); }
  .pool-rebuild {
    padding: 8px 12px;
    border-inline: 1px solid var(--bd-2, #20202a);
    color: var(--warn, #ffc857);
    background: var(--warn-dim, rgba(255, 184, 0, 0.05));
    font-size: 11.5px;
  }
  /* La tabla de discos del grupo (DataTable) pega bajo la cabecera del grupo,
     continuando el borde lateral para formar una sola card. */
  .pool-group > :global(.data-table) {
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border: 1px solid var(--bd-2, #20202a);
    border-top: none;
  }

  .disk-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: 0;
  }
  .row-action {
    min-height: 30px;
    padding: 5px 10px;
    border: 1px solid var(--line-bright, #3b4654);
    border-radius: 4px;
    background: transparent;
    color: var(--ink-dim, #a8b0bc);
    font-family: var(--font-sans);
    font-size: 11.5px;
    font-weight: 550;
    line-height: 1;
    white-space: nowrap;
    cursor: pointer;
    transition: color 0.12s, background 0.12s, border-color 0.12s;
  }
  .row-action:hover {
    border-color: var(--signal, #5b8ff9);
    background: var(--signal-soft, rgba(91, 143, 249, 0.08));
    color: var(--ink, #e7ebf0);
  }
  .row-action.danger {
    color: var(--ink-dim, #a8b0bc);
  }
  .row-action.danger:hover {
    border-color: var(--crit, #ff6464);
    background: var(--crit-dim, rgba(255, 90, 90, 0.08));
    color: var(--crit, #ff6464);
  }
  .no-action {
    padding-right: 10px;
    color: var(--ink-mute, #596372);
  }
  .device-path {
    color: var(--ink, #e7ebf0);
    font-family: var(--font-sans);
    font-feature-settings: "tnum";
  }

  /* Bloque C3.3: indicadores en lista de discos */
  :global(.dt-row.has-orphan) {
    border-left: 2px solid var(--warn);
  }

  .disk-orphan-hint {
    margin-top: 2px;
    font-size: 11px;
    line-height: 1.3;
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
