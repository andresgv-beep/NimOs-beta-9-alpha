<script>
  // Vista Bloqueos: IPs bloqueadas activas con countdown y acciones.
  import { fmtExpires, expiresSoon, fmtAgo } from './shieldFormat.js';
  import { unblock, whitelistFromBlock, busy } from './shieldStore.js';

  export let blocks = [];
  export let now = Date.now();
</script>

<div class="block-table">
  <div class="block-head">
    <span>IP origen</span>
    <span>Motivo</span>
    <span>Regla</span>
    <span>Expira en</span>
    <span>Hace</span>
    <span style="text-align:right">Acción</span>
  </div>
  {#if blocks.length === 0}
    <div class="ns-msg">No hay IPs bloqueadas. Todo tranquilo.</div>
  {:else}
    {#each blocks as b (b.ip)}
      <div class="block-row">
        <span class="block-ip">{b.ip}</span>
        <span class="block-reason" title={b.reason}>{b.reason || '—'}</span>
        <span class="block-rule">{b.rule || '—'}</span>
        <span class="block-expires" class:short={expiresSoon(b.expiresAt, now)}>{fmtExpires(b.expiresAt, now)}</span>
        <span class="block-created">{fmtAgo(b.createdAt, now)}</span>
        <div class="block-actions" style="justify-content:flex-end">
          <button class="icon-btn ok" title="Añadir a whitelist (desbloquea)" disabled={$busy.has(b.ip)} on:click={() => whitelistFromBlock(b.ip)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
          <button class="icon-btn" title="Desbloquear ahora" disabled={$busy.has(b.ip)} on:click={() => unblock(b.ip)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
              <path d="M7 11V7a5 5 0 0 1 9.9-1"/>
            </svg>
          </button>
        </div>
      </div>
    {/each}
  {/if}
</div>

<style>
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }
  .block-table { background: var(--bg-card, #15151a); border-radius: 8px; overflow: hidden; }
  .block-head { display: grid; grid-template-columns: 120px 1fr 80px 90px 90px 100px; gap: 12px; padding: 9px 14px; background: var(--bg-inner, #101015); border-bottom: 1px solid var(--bd-2, #20202a); font-family: var(--font-mono); font-size: 9px; color: var(--fg-5, #5a5a62); letter-spacing: 0.8px; text-transform: uppercase; font-weight: 600; }
  .block-row { display: grid; grid-template-columns: 120px 1fr 80px 90px 90px 100px; gap: 12px; padding: 11px 14px; align-items: center; font-size: 11px; }
  .block-row + .block-row { border-top: 1px solid #1a1a20; }
  .block-ip { font-family: var(--font-mono); font-size: 12px; color: var(--fg, #f0f0f0); font-variant-numeric: tabular-nums; font-weight: 500; }
  .block-reason { font-size: 11px; color: var(--fg-3, #9c9ca4); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .block-rule { font-family: var(--font-mono); font-size: 9px; color: var(--fg-3, #9c9ca4); padding: 1px 6px; border: 1px solid var(--bd-2, #20202a); border-radius: 3px; letter-spacing: 0.4px; text-align: center; }
  .block-expires { font-family: var(--font-mono); font-size: 10px; color: var(--st-warn, #ffc857); font-variant-numeric: tabular-nums; }
  .block-expires.short { color: var(--st-crit, #ff5a5a); }
  .block-created { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); font-variant-numeric: tabular-nums; }
  .block-actions { display: flex; gap: 4px; }
  .icon-btn { width: 26px; height: 26px; background: transparent; border: 1px solid var(--bd-2, #20202a); border-radius: 4px; color: var(--fg-3, #9c9ca4); cursor: pointer; display: flex; align-items: center; justify-content: center; padding: 0; }
  .icon-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .icon-btn.ok:hover:not(:disabled) { color: var(--st-ok, #00ff9f); border-color: rgba(0,255,159,0.3); }
  .icon-btn:disabled { opacity: 0.4; cursor: default; }
  .icon-btn svg { width: 11px; height: 11px; pointer-events: none; }
</style>
