<script>
  // Vista Intelligence: estado del threat feed firmado + estadísticas de
  // observación + el interruptor para pasar de observar a bloquear en duro.
  import { intel, intelSetEnforce, intelRefreshNow, intelRollback } from './shieldStore.js';

  let busy = false;
  let msg = '';

  async function action(fn, label) {
    if (busy) return;
    busy = true; msg = '';
    try {
      await fn();
      msg = label + ' ✓';
      setTimeout(() => (msg = ''), 2500);
    } catch (e) {
      msg = e.message || 'Error';
    } finally {
      busy = false;
    }
  }

  function fmtDate(s) {
    if (!s) return '—';
    const d = new Date(s);
    return isNaN(d) ? '—' : d.toLocaleString('es-ES', { hour12: false });
  }
</script>

{#if !$intel}
  <div class="ns-msg">Cargando inteligencia…</div>
{:else if !$intel.loaded}
  <div class="ns-msg">El threat feed aún no se ha cargado. Pulsa "Actualizar ahora" para descargarlo.</div>
  <div class="intel-actions">
    <button class="ibtn" disabled={busy} on:click={() => action(intelRefreshNow, 'Actualizado')}>Actualizar ahora</button>
  </div>
{:else}
  <div class="intel-intro">
    NimShield Intelligence bloquea IPs ya conocidas por atacar, antes de que lleguen.
    El feed va firmado y se verifica criptográficamente. Arranca en <b>modo observación</b>:
    registra qué bloquearía sin bloquear, para que midas el impacto antes de activarlo.
  </div>

  <!-- KPIs -->
  <div class="kpis">
    <div class="kpi">
      <div class="kpi-lbl">Feed</div>
      <div class="kpi-val mono">v{$intel.feed_version}</div>
      <div class="kpi-tag">{$intel.source}</div>
    </div>
    <div class="kpi">
      <div class="kpi-lbl">IPs indexadas</div>
      <div class="kpi-val mono">{$intel.prefixes.toLocaleString('es-ES')}</div>
      <div class="kpi-tag">en vigilancia</div>
    </div>
    <div class="kpi" class:obs={!$intel.enforce_active}>
      <div class="kpi-lbl">Observadas</div>
      <div class="kpi-val mono">{$intel.observed_total.toLocaleString('es-ES')}</div>
      <div class="kpi-tag">habría bloqueado</div>
    </div>
    <div class="kpi" class:active={$intel.enforce_active}>
      <div class="kpi-lbl">Bloqueadas</div>
      <div class="kpi-val mono">{$intel.blocked_total.toLocaleString('es-ES')}</div>
      <div class="kpi-tag">en duro</div>
    </div>
  </div>

  <!-- Estado del modo -->
  <div class="mode-box" class:enforcing={$intel.enforce_active}>
    <div class="mode-head">
      <span class="mode-led" class:on={$intel.enforce_active}></span>
      <span class="mode-title">
        {$intel.enforce_active ? 'BLOQUEO ACTIVO' : 'MODO OBSERVACIÓN'}
      </span>
    </div>
    <p class="mode-desc">
      {#if $intel.enforce_active}
        El feed está bloqueando IPs maliciosas en duro. La whitelist siempre tiene prioridad.
      {:else}
        El feed solo observa: registra coincidencias sin bloquear. Revisa las "Observadas"
        arriba; si el impacto en tu tráfico es razonable, activa el bloqueo.
      {/if}
    </p>
    <button
      class="ibtn primary"
      class:danger={$intel.enforce_active}
      disabled={busy}
      on:click={() => action(() => intelSetEnforce(!$intel.enforce_active),
                             $intel.enforce_active ? 'Bloqueo desactivado' : 'Bloqueo activado')}
    >
      {$intel.enforce_active ? 'Volver a solo observar' : 'Activar bloqueo en duro'}
    </button>
  </div>

  <!-- Detalle + acciones -->
  <div class="detail">
    <div class="drow"><span>Generado</span><span class="mono">{fmtDate($intel.generated_at)}</span></div>
    <div class="drow"><span>Cargado</span><span class="mono">{fmtDate($intel.loaded_at)}</span></div>
    <div class="drow"><span>Fuente actual</span><span class="mono">{$intel.source}</span></div>
  </div>

  <div class="intel-actions">
    {#if msg}<span class="imsg" class:err={!msg.endsWith('✓')}>{msg}</span>{/if}
    <button class="ibtn ghost" disabled={busy} on:click={() => action(intelRollback, 'Rollback hecho')}>Rollback a versión previa</button>
    <button class="ibtn" disabled={busy} on:click={() => action(intelRefreshNow, 'Actualizado')}>Actualizar ahora</button>
  </div>
{/if}

<style>
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }
  .mono { font-family: var(--font-mono); }

  .intel-intro { font-size: 12px; color: var(--fg-3, #9c9ca4); line-height: 1.6; margin-bottom: 18px; max-width: 640px; }
  .intel-intro b { color: var(--nim-green, #00ff9f); }

  .kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin-bottom: 16px; }
  .kpi { background: var(--bg-card, #15151a); border-radius: 8px; padding: 12px 14px; position: relative; overflow: hidden; }
  .kpi::before { content: ''; position: absolute; top: 0; left: 0; width: 2px; height: 100%; background: var(--bd-3, #2a2a32); }
  .kpi.obs::before { background: var(--st-info, #4db8ff); }
  .kpi.active::before { background: var(--st-crit, #ff5a5a); }
  .kpi-lbl { font-size: 10px; color: var(--fg-4, #7a7a82); text-transform: uppercase; letter-spacing: 0.6px; margin-bottom: 6px; }
  .kpi-val { font-size: 22px; color: var(--fg, #f0f0f0); line-height: 1; letter-spacing: -0.4px; }
  .kpi-tag { font-size: 9px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono); margin-top: 5px; text-transform: uppercase; letter-spacing: 0.4px; }

  .mode-box { background: var(--bg-card, #15151a); border: 1px solid var(--bd-2, #20202a); border-radius: 10px; padding: 16px 18px; margin-bottom: 16px; }
  .mode-box.enforcing { border-color: rgba(255,90,90,0.3); background: rgba(255,90,90,0.03); }
  .mode-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
  .mode-led { width: 8px; height: 8px; border-radius: 2px; background: var(--st-info, #4db8ff); box-shadow: 0 0 6px rgba(77,184,255,0.5); }
  .mode-led.on { background: var(--st-crit, #ff5a5a); box-shadow: 0 0 6px rgba(255,90,90,0.5); }
  .mode-title { font-family: var(--font-mono); font-size: 12px; font-weight: 700; letter-spacing: 0.8px; color: var(--fg, #f0f0f0); }
  .mode-desc { font-size: 11.5px; color: var(--fg-3, #9c9ca4); line-height: 1.5; margin: 0 0 14px; max-width: 600px; }

  .detail { background: var(--bg-inner, #101015); border-radius: 8px; padding: 4px 14px; margin-bottom: 16px; }
  .drow { display: flex; justify-content: space-between; padding: 9px 0; font-size: 11.5px; border-bottom: 1px solid #1a1a20; }
  .drow:last-child { border-bottom: none; }
  .drow span:first-child { color: var(--fg-4, #7a7a82); }
  .drow span:last-child { color: var(--fg-2, #d0d0d4); }

  .intel-actions { display: flex; align-items: center; gap: 10px; justify-content: flex-end; }
  .imsg { margin-right: auto; font-family: var(--font-mono); font-size: 12px; color: var(--nim-green, #00ff9f); }
  .imsg.err { color: var(--st-crit, #ff5a5a); }
  .ibtn { padding: 8px 16px; border-radius: 6px; cursor: pointer; font-size: 12px; font-weight: 600; border: 1px solid var(--bd-3, #2a2a32); background: transparent; color: var(--fg-2, #d0d0d4); }
  .ibtn:hover:not(:disabled) { border-color: var(--fg-4, #7a7a82); }
  .ibtn:disabled { opacity: 0.5; cursor: default; }
  .ibtn.primary { border-color: var(--nim-green, #00ff9f); background: rgba(0,255,159,0.1); color: var(--nim-green, #00ff9f); }
  .ibtn.primary.danger { border-color: var(--st-crit, #ff5a5a); background: rgba(255,90,90,0.1); color: var(--st-crit, #ff5a5a); }
  .ibtn.ghost { color: var(--fg-4, #7a7a82); }
</style>
