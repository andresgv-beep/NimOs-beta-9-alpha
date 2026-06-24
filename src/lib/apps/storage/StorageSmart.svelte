<script>
  /**
   * StorageSmart · Vista de diagnóstico SMART de discos.
   * ─────────────────────────────────────────────────────
   * Lista todos los discos en pools managed con su SMART status (ok/warning/
   * critical/missing). No incluye discos libres porque no están asignados.
   *
   * Props:
   *   · pools — array de pools del backend, cada uno con .devices[]
   *   · disks — { eligible, nvme, usb, ... } — solo se lee `.eligible` para
   *             detectar si hay algún disco en el sistema (empty state).
   *
   * Sin eventos: vista read-only.
   */
  import { SectionHead, EmptyState, Badge, LED, DataTable } from '$lib/ui';
  import { fmtBytes } from './formatters.js';
  import { smartVariant } from './formatters.js';
  import './views-styles.css';

  export let pools = [];
  export let disks = {};
</script>

<div class="st-section">
  <SectionHead>SMART de discos</SectionHead>

  <div class="hint-box">
    <b>SMART</b> (Self-Monitoring, Analysis and Reporting Technology) es una tecnología
    que permite a los discos auto-diagnosticarse. Un SMART status <span class="tc-accent">ok</span>
    significa que el disco no reporta errores. <span class="tc-warn">warning</span> y
    <span class="tc-crit">critical</span> requieren atención.
  </div>

  {#if pools.length === 0 && (!disks.eligible || disks.eligible.length === 0)}
    <EmptyState icon="◌" title="Sin discos" hint="No hay discos detectados en el sistema" />
  {:else}
    <DataTable cols="130px 1fr 90px 100px 130px 1fr" headers={['Dispositivo', 'Modelo', 'Capacidad', 'Pool', 'SMART', 'Notas']}>
      {#each pools as pool}
        {#each (pool.devices || []) as disk}
          <div class="dt-row">
            <span class="mono dt-trunc">{disk.current_path || '—'}</span>
            <span class="mono dt-trunc">{disk.model || '—'}</span>
            <span>{fmtBytes(disk.size_bytes) || '—'}</span>
            <span><Badge size="sm" variant="accent">{pool.name}</Badge></span>
            <span class="dt-smart">
              <LED size={7} variant={smartVariant(disk.smart_status)} />
              <span class="sm">{disk.smart_status || 'unknown'}</span>
            </span>
            <span class="tc-mute sm dt-trunc">
              {#if disk.smart_status === 'critical'}Reemplazar cuanto antes
              {:else if disk.smart_status === 'warning'}Monitorizar
              {:else if disk.smart_status === 'missing'}Disco desconectado
              {:else if disk.smart_status === 'ok'}Sin incidencias
              {:else}—{/if}
            </span>
          </div>
        {/each}
      {/each}
    </DataTable>

    <div class="todo-note">
      <b>TODO</b> · temperatura, horas de operación y errores detallados pendientes de añadir al backend.
    </div>
  {/if}
</div>

<style>
  /* Celdas con texto largo (modelo, ruta, notas): truncar con ellipsis. */
  .dt-trunc {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  /* Celda SMART: LED + texto alineados. */
  .dt-smart {
    display: flex;
    align-items: center;
    gap: 6px;
  }
</style>
