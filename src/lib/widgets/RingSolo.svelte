<script>
  /**
   * RingSolo · Widget 1×1 de métrica única (CPU o RAM)
   * ───────────────────────────────────────────────────
   * UN componente para los DOS widgets del catálogo ('cpu' y 'ram'):
   * la métrica llega como prop estática declarada en el catálogo
   * (campo `props: { metric }` → WidgetLayer la pasa tal cual).
   *
   * Comparte topic 'system' con SysMon: activar CPU+RAM+Sistema
   * sigue siendo UN solo polling (refcount en widgetData).
   */
  import { onMount } from 'svelte';
  import { topicStore, acquire } from '$lib/stores/widgetData.js';
  import Ring from './parts/Ring.svelte';

  export const w = 1; // talla única · contrato
  export const h = 1;
  export let metric = 'cpu'; // 'cpu' | 'ram' · viene del catálogo

  const data = topicStore('system');
  onMount(() => acquire('system'));

  $: cpu = $data?.cpu ?? null;
  $: mem = $data?.memory ?? null;

  $: pct = metric === 'cpu' ? (cpu?.percent ?? null) : (mem?.percent ?? null);
  $: sub = metric === 'cpu'
    ? (cpu ? `${cpu.cores}c · ${(cpu.load1 ?? 0).toFixed(2)}` : ' ')
    : (mem ? `${mem.usedGB} / ${mem.totalGB} GB` : ' ');
</script>

<div class="solo">
  <Ring {pct} label={metric.toUpperCase()} size={86} />
  <div class="sub">{sub}</div>
</div>

<style>
  .solo {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 7px;
    padding: 12px;
    user-select: none;
  }
  .sub {
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--ink-faint);
    letter-spacing: 0.03em;
    min-height: 11px;
  }
</style>
