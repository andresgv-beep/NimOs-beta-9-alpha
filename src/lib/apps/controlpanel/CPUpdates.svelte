<script>
  /**
   * CPUpdates · Panel de Control · sección Actualizaciones
   * ────────────────────────────────────────────────────────
   * Versión del sistema y aplicación de updates. Migrado desde Settings
   * (sección 'updates') al lenguaje visual v3.
   *
   * API:
   *   GET  /api/updates/info   → { currentVersion, latestVersion, updateAvailable }
   *   POST /api/updates/check  → mismo shape (refresca)
   *   POST /api/updates/apply  → inicia la actualización (puede reiniciar)
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { StatCard } from '$lib/ui';

  let updateData = {};
  let checking = false;
  let applying = false;
  let loading = true;
  let msg = '';
  let msgError = false;

  $: current = updateData.currentVersion || updateData.current || updateData.version || '—';
  $: latest = updateData.latestVersion || updateData.latest || '—';
  $: available = !!updateData.updateAvailable;

  async function loadInfo() {
    try {
      const r = await fetch('/api/updates/info', { headers: hdrs() });
      if (r.ok) updateData = await r.json();
    } catch {}
    loading = false;
  }

  async function checkForUpdates() {
    if (checking) return;
    checking = true;
    msg = '';
    try {
      const r = await fetch('/api/updates/check', { method: 'POST', headers: hdrs() });
      if (r.ok) updateData = await r.json();
      else { msg = 'Error al comprobar'; msgError = true; }
    } catch { msg = 'Error de red'; msgError = true; }
    checking = false;
  }

  async function applyUpdate() {
    if (applying) return;
    if (!confirm('¿Aplicar la actualización? El sistema puede reiniciarse.')) return;
    applying = true;
    msg = '';
    try {
      const r = await fetch('/api/updates/apply', { method: 'POST', headers: hdrs() });
      if (r.ok) { msg = 'Actualización en curso…'; msgError = false; }
      else { msg = 'Error al actualizar'; msgError = true; }
    } catch { msg = 'Error de red'; msgError = true; }
    applying = false;
  }

  onMount(loadInfo);
</script>

<div class="cp-updates">
  <div class="cpu-stats">
    <StatCard label="Versión actual" value={current} variant="info" tag="instalada" tagVariant="info" />
    <StatCard label="Última versión" value={latest} variant={available ? 'warn' : 'ok'} tag={available ? 'disponible' : 'al día'} tagVariant={available ? 'warn' : 'ok'} />
  </div>

  <div class="cpu-state" class:available>
    <span class="cpu-state-dot"></span>
    {#if loading}
      Comprobando estado…
    {:else if available}
      Hay una actualización disponible
    {:else}
      El sistema está al día
    {/if}
  </div>

  <div class="cpu-actions">
    <button class="cpu-btn" on:click={checkForUpdates} disabled={checking || applying}>
      {checking ? 'Comprobando…' : 'Comprobar actualizaciones'}
    </button>
    {#if available}
      <button class="cpu-btn primary" on:click={applyUpdate} disabled={applying}>
        {applying ? 'Actualizando…' : 'Aplicar actualización'}
      </button>
    {/if}
  </div>

  {#if msg}<div class="cpu-msg" class:error={msgError}>{msg}</div>{/if}
</div>

<style>
  .cp-updates { display: flex; flex-direction: column; gap: 16px; max-width: 680px; }

  .cpu-stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }

  .cpu-state {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    font-family: var(--font-mono);
    color: var(--fg-3, #9c9ca4);
  }
  .cpu-state-dot {
    width: 7px;
    height: 7px;
    border-radius: 2px;
    background: var(--st-ok, #00ff9f);
  }
  .cpu-state.available .cpu-state-dot {
    background: var(--st-warn, #ffc857);
  }
  .cpu-state.available { color: var(--st-warn, #ffc857); }

  .cpu-actions { display: flex; gap: 8px; flex-wrap: wrap; }
  .cpu-btn {
    padding: 9px 16px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 12px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .cpu-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .cpu-btn.primary {
    background: var(--nim-green, #00ff9f);
    border-color: var(--nim-green, #00ff9f);
    color: var(--bg-window, #16161a);
    font-weight: 600;
  }
  .cpu-btn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .cpu-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .cpu-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .cpu-msg.error { color: var(--st-crit, #ff5a5a); }
</style>
