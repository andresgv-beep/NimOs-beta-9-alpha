<script>
  /**
   * StorageScrub · Vista de scrub manual.
   * ─────────────────────────────────────
   * Lista los volúmenes y permite iniciar su verificación de integridad.
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
    if (st.status === 'never') return 'Nunca';
    if (!st.lastScrub) return '—';
    let label = fmtDate(st.lastScrub);
    if (st.status === 'canceled') label += ' · cancelado';
    return label;
  }
</script>

<div class="st-section">
  <SectionHead>Verificación de integridad</SectionHead>

  {#if pools.length === 0}
    <EmptyState icon="◇" title="Sin volúmenes" hint="No hay volúmenes que se puedan verificar" />
  {:else}
    <div class="hint-box">
      <b>Comprobación de datos.</b> Recorre el volumen y valida la integridad de la
      información almacenada. Puede tardar varias horas y reducir temporalmente el rendimiento.
    </div>

    <DataTable cols="minmax(150px, 1fr) 110px 190px 190px" headers={['Volumen', 'Tamaño', 'Última verificación', '>Acción']}>
      {#each pools as pool}
        {@const st = scrubStatus[pool.name]}
        {@const running = st?.status === 'scrubbing'}
        <div class="dt-row">
          <span class="volume-name">{pool.name}</span>
          <span>{fmtBytes(pool.usage?.total_bytes)}</span>
          <span class="tc-mute">
            {lastScrubLabel(st)}
            {#if !running && st?.errors > 0}
              <span class="scrub-errors">{st.errors} errores</span>
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
                {scrubbing[pool.name] ? 'Iniciando…' : 'Iniciar verificación'}
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
  .volume-name {
    color: var(--ink);
    font-weight: 600;
  }
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
    font-family: var(--font-sans);
    font-size: 10.5px;
    color: var(--ink-mute);
  }
  .scrub-errors {
    margin-left: 6px;
    font-family: var(--font-sans);
    font-size: 10.5px;
    color: var(--warn);
  }
</style>
