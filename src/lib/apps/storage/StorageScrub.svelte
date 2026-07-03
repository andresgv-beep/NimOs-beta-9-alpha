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
  import { fmtBytes, fmtDate } from './formatters.js';
  import './views-styles.css';

  export let pools = [];
  export let scrubbing = {};
  export let scrubMsg = '';
  // AUDIT F8: estado real por pool desde /scrub/status — antes "Último
  // scrub" era un "—" hardcodeado y un scrub en curso era invisible.
  export let scrubStatus = {};

  const dispatch = createEventDispatcher();

  function onScrub(poolName) {
    dispatch('start', { poolName });
  }

  function lastScrubLabel(st) {
    if (!st) return '—';
    if (st.status === 'never') return 'nunca';
    if (!st.lastScrub) return '—';
    let label = fmtDate(st.lastScrub);
    if (st.status === 'canceled') label += ' · cancelado';
    return label;
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

    <DataTable cols="1fr 80px 100px 180px 170px" headers={['Pool', 'Tipo', 'Tamaño', 'Último scrub', 'Acción']}>
      {#each pools as pool}
        {@const st = scrubStatus[pool.name]}
        {@const running = st?.status === 'scrubbing'}
        <div class="dt-row">
          <span class="mono">{pool.name}</span>
          <span>BTRFS</span>
          <span>{fmtBytes(pool.usage?.total_bytes)}</span>
          <span class="tc-mute">
            {lastScrubLabel(st)}
            {#if !running && st?.errors > 0}
              <span class="scrub-errors">⚠ {st.errors} err</span>
            {/if}
          </span>
          <span>
            {#if running}
              <div class="scrub-progress" title={st.eta ? `ETA: ${st.eta}` : ''}>
                <div class="scrub-bar"><i style="width:{st.progress || 0}%"></i></div>
                <span class="scrub-pct">
                  {(st.progress ?? 0).toFixed(1)}%{st.timeLeft ? ` · quedan ${st.timeLeft}` : ''}
                </span>
              </div>
            {:else}
              <BevelButton
                size="sm"
                onClick={() => onScrub(pool.name)}
                disabled={scrubbing[pool.name]}
              >
                {scrubbing[pool.name] ? '▸ Iniciando...' : '▸ Scrub ahora'}
              </BevelButton>
            {/if}
          </span>
        </div>
      {/each}
    </DataTable>

    {#if scrubMsg}
      <div class="msg">{scrubMsg}</div>
    {/if}
  {/if}
</div>

<style>
  /* AUDIT F8: progreso del scrub en curso */
  .scrub-progress {
    display: flex;
    flex-direction: column;
    gap: 3px;
    justify-content: center;
  }
  .scrub-bar {
    height: 5px;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.08);
    overflow: hidden;
  }
  .scrub-bar i {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--signal);
    transition: width 0.8s ease;
  }
  .scrub-pct {
    font-family: var(--font-mono);
    font-size: 9.5px;
    color: var(--ink-mute);
  }
  .scrub-errors {
    margin-left: 6px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--warn);
  }
</style>
