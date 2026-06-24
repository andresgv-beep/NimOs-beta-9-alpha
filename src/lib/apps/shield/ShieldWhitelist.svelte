<script>
  // Vista Whitelist: IPs de confianza permanente. Loopback fija.
  import { fmtAgo } from './shieldFormat.js';
  import { addWhitelist, removeWhitelist, busy } from './shieldStore.js';

  export let whitelist = [];
  export let now = Date.now();

  let wlIP = '';
  let wlNote = '';
  let wlError = '';

  async function add() {
    const ip = wlIP.trim();
    if (!ip) return;
    wlError = '';
    try {
      await addWhitelist(ip, wlNote.trim());
      wlIP = ''; wlNote = '';
    } catch (e) {
      wlError = e.message || 'No se pudo añadir';
    }
  }
</script>

<div class="wl-form">
  <input type="text" class="ip" placeholder="192.168.1.100" bind:value={wlIP} on:keydown={(e) => e.key === 'Enter' && add()} />
  <input type="text" class="note" placeholder="Nota (ej: ordenador personal)" bind:value={wlNote} on:keydown={(e) => e.key === 'Enter' && add()} />
  <button class="btn-add" on:click={add}>
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
      <line x1="12" y1="5" x2="12" y2="19"/>
      <line x1="5" y1="12" x2="19" y2="12"/>
    </svg>
    Añadir
  </button>
</div>
{#if wlError}
  <div class="ns-err">{wlError}</div>
{/if}

<div class="wl-table">
  <div class="wl-head">
    <span>IP</span>
    <span>Nota</span>
    <span>Añadida</span>
    <span style="text-align:right">Acción</span>
  </div>

  <!-- Loopback fija · el backend rechaza quitarla -->
  <div class="wl-row loopback">
    <span class="block-ip">127.0.0.1</span>
    <span class="wl-note">loopback (no removible)</span>
    <span class="wl-loopback-tag">system</span>
    <div></div>
  </div>

  {#each whitelist as wl (wl.ip)}
    <div class="wl-row">
      <span class="block-ip">{wl.ip}</span>
      <span class="wl-note">{wl.note || '—'}</span>
      <span class="block-created">{fmtAgo(wl.created_at, now, true)}</span>
      <div class="block-actions" style="justify-content:flex-end">
        <button class="icon-btn danger" title="Quitar de whitelist" disabled={$busy.has(wl.ip)} on:click={() => removeWhitelist(wl.ip)}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
          </svg>
        </button>
      </div>
    </div>
  {/each}
</div>

<style>
  .ns-err { margin: -6px 0 12px; font-size: 11px; color: var(--st-crit, #ff5a5a); font-family: var(--font-mono); }

  .wl-form { display: flex; gap: 6px; margin-bottom: 14px; background: var(--bg-card, #15151a); padding: 10px; border-radius: 8px; }
  .wl-form input { background: var(--bg-inner, #101015); border: 1px solid var(--bd-2, #20202a); border-radius: 5px; padding: 7px 10px; color: var(--fg, #f0f0f0); font-family: var(--font-mono); font-size: 11px; outline: none; }
  .wl-form input:focus { border-color: rgba(0,255,159,0.35); }
  .wl-form input.ip { width: 140px; }
  .wl-form input.note { flex: 1; font-family: inherit; }
  .btn-add { padding: 0 16px; background: var(--nim-green, #00ff9f); color: var(--bg-window, #16161a); border: none; border-radius: 5px; font-family: var(--font-mono); font-size: 10px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.6px; cursor: pointer; display: flex; align-items: center; gap: 5px; }
  .btn-add:hover { filter: brightness(1.08); }
  .btn-add svg { width: 11px; height: 11px; pointer-events: none; }

  .wl-table { background: var(--bg-card, #15151a); border-radius: 8px; overflow: hidden; }
  .wl-head { display: grid; grid-template-columns: 140px 1fr 110px 60px; gap: 12px; padding: 9px 14px; background: var(--bg-inner, #101015); border-bottom: 1px solid var(--bd-2, #20202a); font-family: var(--font-mono); font-size: 9px; color: var(--fg-5, #5a5a62); letter-spacing: 0.8px; text-transform: uppercase; font-weight: 600; }
  .wl-row { display: grid; grid-template-columns: 140px 1fr 110px 60px; gap: 12px; padding: 11px 14px; align-items: center; font-size: 11px; }
  .wl-row + .wl-row { border-top: 1px solid #1a1a20; }
  .wl-note { font-size: 11px; color: var(--fg-3, #9c9ca4); font-style: italic; }
  .wl-row.loopback { background: rgba(0, 255, 159, 0.03); }
  .wl-loopback-tag { font-family: var(--font-mono); font-size: 8px; color: var(--nim-green, #00ff9f); padding: 1px 5px; border: 1px solid rgba(0,255,159,0.3); border-radius: 3px; letter-spacing: 0.5px; text-transform: uppercase; justify-self: start; }

  .block-ip { font-family: var(--font-mono); font-size: 12px; color: var(--fg, #f0f0f0); font-variant-numeric: tabular-nums; font-weight: 500; }
  .block-created { font-family: var(--font-mono); font-size: 10px; color: var(--fg-4, #7a7a82); font-variant-numeric: tabular-nums; }
  .block-actions { display: flex; gap: 4px; }
  .icon-btn { width: 26px; height: 26px; background: transparent; border: 1px solid var(--bd-2, #20202a); border-radius: 4px; color: var(--fg-3, #9c9ca4); cursor: pointer; display: flex; align-items: center; justify-content: center; padding: 0; }
  .icon-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .icon-btn.danger:hover:not(:disabled) { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.3); }
  .icon-btn:disabled { opacity: 0.4; cursor: default; }
  .icon-btn svg { width: 11px; height: 11px; pointer-events: none; }
</style>
