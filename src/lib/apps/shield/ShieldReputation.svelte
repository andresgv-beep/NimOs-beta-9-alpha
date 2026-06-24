<script>
  // Vista Reputación: IPs conocidas, su nivel y la acción de "olvidar".
  import { fmtAgo, repLevelMeta } from './shieldFormat.js';
  import { forgetReputation, busy } from './shieldStore.js';

  export let reputation = [];
  export let now = Date.now();

  async function forget(ip) {
    if (!confirm(`¿Olvidar la reputación de ${ip}? Volverá a trato estricto (desconocida).`)) return;
    await forgetReputation(ip);
  }
</script>

<div class="rep-intro ns-msg">
  NimShield aprende qué IPs entran con éxito habitualmente y les da más margen ante un despiste.
  Una IP conocida que falla en ráfaga pierde el margen al instante (desconfianza).
</div>
<div class="block-table">
  <div class="block-head rep-head">
    <span>IP origen</span>
    <span>Nivel</span>
    <span>Éxitos</span>
    <span>Racha</span>
    <span>Último acceso</span>
    <span style="text-align:right">Acción</span>
  </div>
  {#if reputation.length === 0}
    <div class="ns-msg">Aún no hay IPs con historial. Entra con éxito y aparecerás aquí.</div>
  {:else}
    {#each reputation as rp (rp.ip)}
      <div class="block-row rep-row">
        <span class="block-ip">{rp.ip}</span>
        <span><span class="lvl-pill {repLevelMeta[rp.level]?.cls}">{repLevelMeta[rp.level]?.label || rp.level}</span></span>
        <span class="rep-num">{rp.successCount}</span>
        <span class="rep-num" class:rep-streak={rp.failStreak >= 3}>{rp.failStreak}</span>
        <span class="block-created">{rp.lastSuccess ? fmtAgo(rp.lastSuccess, now, true) : '—'}</span>
        <div class="block-actions" style="justify-content:flex-end">
          <button class="icon-btn" title="Olvidar reputación (vuelve a trato estricto)" disabled={$busy.has(rp.ip)} on:click={() => forget(rp.ip)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 12a9 9 0 1 0 9-9 9 9 0 0 0-6.4 2.6L3 8"/>
              <line x1="3" y1="3" x2="3" y2="8"/><line x1="3" y1="8" x2="8" y2="8"/>
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
  .block-created { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); font-variant-numeric: tabular-nums; }
  .block-actions { display: flex; gap: 4px; }

  /* Reputación: misma rejilla que Bloqueos, columnas propias */
  .rep-intro { margin: 0 0 14px; }
  .rep-head, .rep-row { grid-template-columns: 130px 110px 70px 60px 1fr 70px; }
  .rep-num { font-family: var(--font-mono); font-size: 12px; color: var(--fg-3, #b0b0b8); font-variant-numeric: tabular-nums; }
  .rep-streak { color: var(--st-warn, #ffc857); font-weight: 600; }
  .lvl-pill { display: inline-block; font-family: var(--font-mono); font-size: 10px; padding: 2px 8px; border-radius: 99px; border: 1px solid var(--bd-3, #2a2a32); color: var(--fg-4, #7a7a82); letter-spacing: 0.3px; }
  .lvl-trusted { border-color: rgba(0,255,159,0.4); color: var(--nim-green, #00ff9f); background: rgba(0,255,159,0.06); }
  .lvl-known { border-color: rgba(122,158,177,0.5); color: #9bb8c7; background: rgba(122,158,177,0.08); }
  .lvl-unknown { /* neutro, hereda */ }
  .lvl-distrust { border-color: rgba(248,113,113,0.5); color: var(--st-crit, #f87171); background: rgba(248,113,113,0.08); }

  .icon-btn { width: 26px; height: 26px; background: transparent; border: 1px solid var(--bd-2, #20202a); border-radius: 4px; color: var(--fg-3, #9c9ca4); cursor: pointer; display: flex; align-items: center; justify-content: center; padding: 0; }
  .icon-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .icon-btn:disabled { opacity: 0.4; cursor: default; }
  .icon-btn svg { width: 11px; height: 11px; pointer-events: none; }
</style>
