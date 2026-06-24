<script>
  /**
   * StorageDisks · Vista de discos del sistema.
   * ────────────────────────────────────────────
   * Tres secciones:
   *   · Discos asignados a pools (read-only, acciones Fase B7)
   *   · Discos libres (con acción Formatear → emite 'wipe')
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
    <SectionHead count={`· ${totalDisksAssigned + totalDisksFree} detectados`}>
      Discos del sistema
    </SectionHead>
    <div class="section-actions">
      <BevelButton size="sm" onClick={() => dispatch('rescan')} disabled={scanning}>
        {scanning ? '▸ Escaneando...' : '↻ Rescan buses'}
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
        + Crear volumen
      </BevelButton>
    </div>
  </div>

  <!-- Discos asignados a pools -->
  {#if totalDisksAssigned > 0}
    <SectionHead count={`· ${totalDisksAssigned}`}>Asignados a pools</SectionHead>
    {#each pools as pool}
      <div class="pool-group">
        <div class="pool-group-head">
          <div class="pool-group-title">
            <Badge size="sm" variant="accent">{pool.name}</Badge>
            <span class="sm tc-dim">· {(pool.devices || []).length} {(pool.devices || []).length === 1 ? 'disco' : 'discos'}</span>
          </div>
          <span class="sm tc-faint mono">montado · para destruir, desmóntalo primero</span>
        </div>
        <DataTable cols="130px 1fr 90px 100px 110px 200px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Pool', 'SMART', 'Acción']}>
          {#each (pool.devices || []) as disk}
            <div class="dt-row">
              <span class="mono dt-trunc">{disk.current_path || '—'}</span>
              <span class="mono dt-trunc">{disk.model || '—'}</span>
              <span>{fmtBytes(disk.size_bytes) || '—'}</span>
              <span><Badge size="sm" variant="accent">{pool.name}</Badge></span>
              <span class="dt-flex">
                <LED size={7} variant={smartVariant(disk.smart_status)} />
                <span class="tc-dim sm">{disk.smart_status || 'unknown'}</span>
              </span>
              <span class="disk-actions">
                <button class="disk-action-btn" disabled title="Disponible en Fase B7">
                  Desasignar <span class="action-tag">B7</span>
                </button>
                <button class="disk-action-btn" disabled title="Disponible en Fase B7">
                  Reemplazar <span class="action-tag">B7</span>
                </button>
              </span>
            </div>
          {/each}
        </DataTable>
      </div>
    {/each}
  {/if}

  <!-- Discos libres -->
  <div style="margin-top:24px">
    <SectionHead count={`· ${disks.eligible?.length || 0}`}>Discos libres (elegibles)</SectionHead>
    {#if !disks.eligible || disks.eligible.length === 0}
      <EmptyState icon="◌" title="Sin discos libres" hint="Todos los discos están asignados a pools" />
    {:else}
      <DataTable cols="120px 1fr 90px 70px 100px 230px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Tipo', 'Estado', 'Acción']}>
        {#each disks.eligible as disk}
          {@const dPath = disk.path || '/dev/' + disk.name}
          {@const dStatus = diskStatus(dPath)}
          <div class="dt-row" class:has-orphan={dStatus.kind === 'orphan'}>
            <span class="mono dt-trunc">{dPath}</span>
            <span class="mono dt-trunc">{disk.model || '—'}</span>
            <span>{disk.sizeH || fmtBytes(disk.size)}</span>
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
                class="disk-action-btn primary"
                disabled
                title="Crear un volumen nuevo con este disco · Disponible en Fase B5"
              >
                + Usar en volumen <span class="action-tag">B5</span>
              </button>
              <button
                class="disk-action-btn warn"
                on:click={() => dispatch('wipe', { path: dPath })}
                title={dStatus.kind === 'orphan'
                  ? '⚠ Atención: este disco tiene datos. Formatear los borrará permanentemente.'
                  : 'Formatear disco (borra restos de formatos anteriores)'}
              >
                Formatear
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
      <SectionHead count={`· ${disks.usb.length}`}>Dispositivos USB</SectionHead>
      <DataTable cols="130px 1fr 100px 120px 130px" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Tipo', 'Estado']}>
        {#each disks.usb as disk}
          <div class="dt-row">
            <span class="mono dt-trunc">{disk.path || '/dev/' + disk.name}</span>
            <span class="mono dt-trunc">{disk.model || '—'}</span>
            <span>{disk.sizeH || fmtBytes(disk.size)}</span>
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
    padding: 9px 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-bottom: none;
    border-radius: 8px 8px 0 0;
  }
  .pool-group-title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-family: var(--font-mono);
  }
  /* La tabla de discos del grupo (DataTable) pega bajo la cabecera del grupo,
     continuando el borde lateral para formar una sola card. */
  .pool-group-head + :global(.data-table) {
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border: 1px solid var(--bd-2, #20202a);
    border-top: none;
  }

  .disk-actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    overflow: visible;
  }
  .disk-action-btn {
    padding: 3px 8px;
    font-family: var(--font-mono);
    font-size: 9px;
    letter-spacing: 0.8px;
    text-transform: uppercase;
    background: var(--bg-2);
    border: 1px solid var(--border-bright);
    color: var(--fg-dim);
    cursor: pointer;
    transition: all 0.12s;
    clip-path: polygon(
      0 0, calc(100% - 4px) 0, 100% 4px,
      100% 100%, 4px 100%, 0 calc(100% - 4px)
    );
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .disk-action-btn:hover:not(:disabled) {
    border-color: var(--accent);
    color: var(--accent);
  }
  .disk-action-btn.primary {
    border-color: var(--accent);
    color: var(--accent);
    background: var(--accent-dim, rgba(255,145,68,0.05));
  }
  .disk-action-btn.primary:hover:not(:disabled) {
    background: rgba(255, 145, 68, 0.12);
  }
  .disk-action-btn.warn {
    border-color: var(--border-bright);
    color: var(--warn);
  }
  .disk-action-btn.warn:hover:not(:disabled) {
    border-color: var(--crit);
    color: var(--crit);
    background: rgba(255, 90, 90, 0.04);
  }
  .disk-action-btn:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }
  .action-tag {
    font-size: 8px;
    color: var(--fg-faint);
    margin-left: 2px;
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
