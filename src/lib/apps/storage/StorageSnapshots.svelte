<script>
  /**
   * StorageSnapshots · Vista de snapshots por pool BTRFS.
   * ──────────────────────────────────────────────────────
   * Beta 8.1: BTRFS snapshots aún NO están implementados en el backend
   * (storage_btrfs_features.go marca create/list/rollback como "pending Beta 9").
   *
   * Esta vista lista los pools BTRFS managed y muestra un mensaje claro
   * explicando que la gestión está pendiente. Cuando Beta 9 implemente
   * `btrfs subvolume snapshot/list/rollback`, el frontend ya está listo
   * para conectar — solo hay que descomentar acciones.
   *
   * Props:
   *   · pools     — array de pools del backend
   *   · snapshots — { [poolName]: [snapshot, ...] } cargados lazily (no usado todavía)
   *
   * Eventos:
   *   · load — { detail: { poolName } } — solicitar carga (no usado todavía)
   */
  import { createEventDispatcher } from 'svelte';
  import { SectionHead, EmptyState } from '$lib/ui';

  export let pools = [];
  // eslint-disable-next-line no-unused-vars
  export let snapshots = {};

  // eslint-disable-next-line no-unused-vars
  const dispatch = createEventDispatcher();

  // Beta 8.1 es BTRFS-only. Anteriormente este filtro era `pool.type === 'zfs'`
  // (código vestigial de Beta 7). Tras la eliminación de ZFS en mayo 2026 todos
  // los pools son BTRFS, así que listamos todos.
  $: btrfsPools = pools;
</script>

<div class="st-section">
  <SectionHead>Snapshots</SectionHead>

  <div class="beta9-notice">
    <div class="b9-icon">⚙</div>
    <div class="b9-text">
      <div class="b9-title">Gestión de snapshots — pendiente Beta 9</div>
      <div class="b9-desc">
        BTRFS soporta snapshots nativamente vía <span class="mono">btrfs subvolume snapshot</span>,
        pero la gestión desde NimOS está pendiente de implementarse en Beta 9.
        Por ahora puedes crear snapshots manualmente desde la terminal del NAS.
      </div>
    </div>
  </div>

  {#if pools.length === 0}
    <EmptyState
      icon="◇"
      title="Sin pools configurados"
      hint="Crea un pool BTRFS desde 'Resumen → + Nuevo volumen' para que aparezca aquí."
    />
  {:else}
    <div class="pool-list">
      {#each btrfsPools as pool}
        <div class="pool-card">
          <div class="pool-card-head">
            <span class="diamond">◆</span>
            <div class="pool-card-ident">
              <div class="pool-card-name">{pool.name}</div>
              <div class="pool-card-meta">
                BTRFS · {pool.profile || 'single'} ·
                {pool.devices?.length || 0} disco{pool.devices?.length === 1 ? '' : 's'}
              </div>
            </div>
            <button class="btn-disabled" disabled title="Disponible en Beta 9">
              + Snapshot
            </button>
          </div>

          <div class="pool-card-body">
            <EmptyState
              icon="◌"
              title="Sin snapshots"
              hint="La gestión de snapshots desde la UI llegará en Beta 9. Mientras tanto, usa `btrfs subvolume snapshot` desde SSH."
            />
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .st-section {
    padding: 0;
  }

  /* ─── Aviso superior · ámbar para indicar "no implementado todavía" ─── */
  .beta9-notice {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    background: rgba(251, 191, 36, 0.06);
    border-left: 3px solid var(--warn);
    border-radius: 4px;
    padding: 12px 14px;
    margin-bottom: 16px;
  }
  .b9-icon {
    font-size: 18px;
    color: var(--warn);
    line-height: 1;
    flex-shrink: 0;
    margin-top: 2px;
  }
  .b9-text {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .b9-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--warn);
    font-family: var(--font-sans);
  }
  .b9-desc {
    font-size: 11px;
    color: var(--ink-dim);
    line-height: 1.6;
    font-family: var(--font-sans);
  }
  .b9-desc .mono {
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-inner);
    padding: 1px 5px;
    border-radius: 3px;
    border: 1px solid var(--line);
  }

  /* ─── Lista de pools BTRFS ─── */
  .pool-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .pool-card {
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 10px;
    overflow: hidden;
  }

  .pool-card-head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--line);
  }

  .diamond {
    color: var(--signal);
    font-size: 14px;
  }

  .pool-card-ident {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .pool-card-name {
    font-size: 13px;
    color: var(--ink);
    font-weight: 600;
    font-family: var(--font-mono);
    letter-spacing: 0.3px;
  }
  .pool-card-meta {
    font-size: 10px;
    color: var(--ink-mute);
    font-family: var(--font-mono);
    letter-spacing: 0.3px;
  }

  .pool-card-body {
    padding: 20px 16px;
  }

  /* Botón "+ Snapshot" deshabilitado (placeholder Beta 9) */
  .btn-disabled {
    padding: 5px 12px;
    border-radius: 5px;
    border: 1px solid var(--line);
    background: var(--bg-inner);
    color: var(--ink-trace);
    font-size: 10px;
    font-weight: 500;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.4px;
    cursor: not-allowed;
    opacity: 0.6;
  }
</style>
