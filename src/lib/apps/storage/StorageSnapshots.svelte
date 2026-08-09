<script>
  /** Vista de snapshots BTRFS existentes, en modo lectura. */
  import { createEventDispatcher, onMount } from 'svelte';
  import { SectionHead, EmptyState } from '$lib/ui';
  import { fmtDate } from './formatters.js';

  export let pools = [];
  export let snapshots = {};

  const dispatch = createEventDispatcher();

  function refresh(poolName) {
    dispatch('load', { poolName });
  }

  onMount(() => {
    for (const pool of pools) refresh(pool.name);
  });
</script>

<div class="st-section">
  <SectionHead count={pools.length ? `${pools.length} volúmenes` : ''}>
    Puntos de restauración
  </SectionHead>

  <div class="snapshot-info">
    <div class="snapshot-info-title">Snapshots BTRFS</div>
    <div class="snapshot-info-text">
      Conservan un estado de solo lectura del volumen y permiten localizar versiones
      anteriores de los datos sin duplicar inmediatamente todo su contenido.
    </div>
  </div>

  {#if pools.length === 0}
    <EmptyState
      icon="◇"
      title="Sin volúmenes configurados"
      hint="Crea un volumen para empezar a utilizar puntos de restauración."
    />
  {:else}
    <div class="pool-list">
      {#each pools as pool}
        {@const poolSnapshots = snapshots[pool.name] || []}
        <div class="pool-card">
          <div class="pool-card-head">
            <span class="pool-icon"></span>
            <div class="pool-card-ident">
              <div class="pool-card-name">{pool.name}</div>
              <div class="pool-card-meta">
                BTRFS · {pool.profile || 'single'} · {poolSnapshots.length}
                {poolSnapshots.length === 1 ? ' snapshot' : ' snapshots'}
              </div>
            </div>
            <button class="refresh-btn" on:click={() => refresh(pool.name)}>
              Actualizar
            </button>
          </div>

          <div class="pool-card-body">
            {#if poolSnapshots.length === 0}
              <EmptyState
                icon="◌"
                title="Sin puntos de restauración"
                hint="Este volumen todavía no contiene snapshots."
              />
            {:else}
              <div class="snapshot-list">
                <div class="snapshot-head">
                  <span>Nombre</span>
                  <span>Creado</span>
                </div>
                {#each poolSnapshots as snapshot}
                  <div class="snapshot-row">
                    <span class="snapshot-name">{snapshot.name || snapshot}</span>
                    <span class="snapshot-date">{snapshot.created ? fmtDate(snapshot.created) : '—'}</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .st-section {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .snapshot-info {
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-left: 3px solid var(--signal);
    border-radius: 6px;
    background: var(--panel);
  }
  .snapshot-info-title {
    margin-bottom: 4px;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 600;
  }
  .snapshot-info-text {
    max-width: 760px;
    color: var(--ink-dim);
    font-family: var(--font-sans);
    font-size: 11.5px;
    line-height: 1.55;
  }
  .pool-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .pool-card {
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--panel);
  }
  .pool-card-head {
    display: flex;
    align-items: center;
    gap: 12px;
    min-height: 52px;
    padding: 0 14px;
    border-bottom: 1px solid var(--line);
    background: var(--panel-elev);
  }
  .pool-icon {
    width: 8px;
    height: 8px;
    border-radius: 2px;
    background: var(--signal);
  }
  .pool-card-ident {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .pool-card-name {
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 13px;
    font-weight: 650;
  }
  .pool-card-meta {
    color: var(--ink-faint);
    font-family: var(--font-sans);
    font-size: 11px;
  }
  .refresh-btn {
    min-height: 30px;
    padding: 5px 11px;
    border: 1px solid var(--line-bright);
    border-radius: 4px;
    background: transparent;
    color: var(--ink-dim);
    font-family: var(--font-sans);
    font-size: 11.5px;
    cursor: pointer;
  }
  .refresh-btn:hover {
    border-color: var(--signal);
    background: var(--signal-soft);
    color: var(--ink);
  }
  .pool-card-body { padding: 12px 14px; }
  .snapshot-list {
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: 6px;
  }
  .snapshot-head,
  .snapshot-row {
    display: grid;
    grid-template-columns: minmax(160px, 1fr) 180px;
    gap: 16px;
    align-items: center;
    padding: 9px 12px;
  }
  .snapshot-head {
    color: var(--ink-faint);
    font-size: 11px;
    font-weight: 500;
  }
  .snapshot-row {
    border-top: 1px solid var(--line);
    color: var(--ink-dim);
    font-size: 12px;
  }
  .snapshot-name { color: var(--ink); }
  .snapshot-date { color: var(--ink-faint); }
</style>
