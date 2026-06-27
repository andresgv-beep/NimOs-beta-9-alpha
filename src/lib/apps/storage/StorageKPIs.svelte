<script>
  /**
   * StorageKPIs · Banner de 4 KPIs encima del scroll de Overview.
   * ──────────────────────────────────────────────────────────────
   * Volúmenes · Discos · Capacidad · Salud
   *
   * Calcula los valores derivados internamente desde pools+disks+alerts.
   *
   * Props:
   *   · pools  — array de pools
   *   · disks  — { eligible, ... }
   *   · alerts — array de alertas
   */
  import { StatCard } from '$lib/ui';
  import { fmtBytes, usageVariant } from './formatters.js';

  export let pools = [];
  export let disks = {};
  export let alerts = [];

  $: totalDisksAssigned = pools.reduce((s, p) => s + (p.devices?.length || 0), 0);
  $: totalDisksFree = (disks.eligible?.length || 0);
  $: totalCapacity = pools.reduce((s, p) => s + (p.usage?.total_bytes || 0), 0);
  $: totalUsed = pools.reduce((s, p) => s + (p.usage?.used_bytes || 0), 0);
  $: totalFree = totalCapacity - totalUsed;
  $: overallUsagePct = totalCapacity > 0 ? Math.round((totalUsed / totalCapacity) * 100) : 0;
  $: overallHealth = pools.every(p => p.mounted && p.health?.status === 'healthy') && alerts.length === 0 ? 'ok'
                   : pools.some(p => !p.mounted || p.health?.status === 'critical') ? 'crit'
                   : 'warn';
  // usageVariant devuelve 'accent'|'warn'|'crit'; en v3 el verde es 'ok'.
  $: capVariant = usageVariant(overallUsagePct) === 'accent' ? 'ok' : usageVariant(overallUsagePct);
  // El tag de Salud debe reflejar overallHealth, no solo alerts: un pool no
  // montado o crítico NO es "sin incidencias" aunque la lista de alertas esté
  // vacía (split-brain: el valor decía CRIT y el tag 'sin incidencias').
  $: healthTag = overallHealth === 'ok'
    ? 'sin incidencias'
    : alerts.length > 0
      ? `${alerts.length} alerta${alerts.length > 1 ? 's' : ''}`
      : overallHealth === 'crit' ? 'requiere atención' : 'revisar';
</script>

<div class="st-kpis">
  <StatCard
    label="Volúmenes"
    value={pools.length}
    variant={pools.length > 0 ? 'ok' : 'warn'}
    tag={pools.length > 0 ? 'online' : 'vacío'}
    tagVariant={pools.length > 0 ? 'ok' : 'warn'}
  />
  <StatCard
    label="Discos"
    value={totalDisksAssigned + totalDisksFree}
    variant="default"
    tag={`${totalDisksAssigned} asignados · ${totalDisksFree} libres`}
    tagVariant="ok"
  />
  <StatCard
    label="Capacidad"
    value={fmtBytes(totalCapacity)}
    variant={capVariant}
    tag={totalCapacity > 0 ? `${fmtBytes(totalFree)} libres · ${overallUsagePct}%` : '—'}
    tagVariant={capVariant}
  />
  <StatCard
    label="Salud"
    value={overallHealth === 'ok' ? 'OK' : overallHealth === 'warn' ? 'WARN' : 'CRIT'}
    variant={overallHealth}
    tag={healthTag}
    tagVariant={overallHealth}
  />
</div>

<style>
  .st-kpis {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;
    flex-shrink: 0;
  }
</style>
