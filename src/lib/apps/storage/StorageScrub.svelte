<script>
  /**
   * StorageScrub · Vista de scrub manual.
   * ─────────────────────────────────────
   * Lista los pools con botón "Scrub ahora". El scrub es un chequeo de
   * integridad que recorre checksums — puede tardar horas.
   *
   * Props:
   *   · pools     — array de pools del backend
   *   · scrubbing — { [poolName]: boolean } estado por pool
   *   · scrubMsg  — mensaje de feedback del último intento
   *
   * Eventos:
   *   · start — { detail: { poolName } } — el padre dispara la API call
   *             y actualiza scrubbing/scrubMsg
   */
  import { createEventDispatcher } from 'svelte';
  import { SectionHead, BevelButton, EmptyState, DataTable } from '$lib/ui';
  import { fmtBytes } from './formatters.js';
  import './views-styles.css';

  export let pools = [];
  export let scrubbing = {};
  export let scrubMsg = '';

  const dispatch = createEventDispatcher();

  function onScrub(poolName) {
    dispatch('start', { poolName });
  }
</script>

<div class="st-section">
  <SectionHead>Scrub manual</SectionHead>

  {#if pools.length === 0}
    <EmptyState icon="◇" title="Sin pools" hint="No hay pools para ejecutar scrub" />
  {:else}
    <div class="hint-box">
      <b>¿Qué es scrub?</b> Es un chequeo de integridad que recorre todos los datos del pool
      y verifica checksums. Útil mensualmente para detectar errores silenciosos.
      Puede tardar horas y el sistema irá más lento mientras corre.
    </div>

    <DataTable cols="1fr 80px 100px 140px 160px" headers={['Pool', 'Tipo', 'Tamaño', 'Último scrub', 'Acción']}>
      {#each pools as pool}
        <div class="dt-row">
          <span class="mono">{pool.name}</span>
          <span>BTRFS</span>
          <span>{fmtBytes(pool.usage?.total_bytes)}</span>
          <span class="tc-mute">—</span>
          <span>
            <BevelButton
              size="sm"
              onClick={() => onScrub(pool.name)}
              disabled={scrubbing[pool.name]}
            >
              {scrubbing[pool.name] ? '▸ Iniciando...' : '▸ Scrub ahora'}
            </BevelButton>
          </span>
        </div>
      {/each}
    </DataTable>

    {#if scrubMsg}
      <div class="msg">{scrubMsg}</div>
    {/if}
  {/if}
</div>
