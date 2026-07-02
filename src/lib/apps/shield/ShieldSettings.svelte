<script>
  // Vista Ajustes: política de fuerza bruta (umbrales por nivel + escalado
  // de duración) y escalado de reincidentes al firewall del kernel.
  // El borrador es local; al guardar persiste vía el store.
  import { config, configDefaults, saveConfig, status, firewallSetEnabled, behaviorSetEnabled } from './shieldStore.js';

  let draft = null;
  let saving = false;
  let msg = '';
  let fwBusy = false;
  let fwMsg = '';

  // Inicializa el borrador cuando llega la config (sin pisar ediciones).
  $: if ($config && !draft) draft = { ...$config };

  function restore() {
    if ($configDefaults) draft = { ...$configDefaults };
  }
  async function save() {
    if (saving || !draft) return;
    saving = true; msg = '';
    try {
      const saved = await saveConfig(draft);
      draft = { ...saved };
      msg = 'Guardado ✓';
      setTimeout(() => (msg = ''), 2500);
    } catch (e) {
      msg = e.message;
    } finally {
      saving = false;
    }
  }

  async function toggleFW() {
    if (fwBusy) return;
    fwBusy = true; fwMsg = '';
    try {
      await firewallSetEnabled(!$status.firewallEscalation);
      fwMsg = $status.firewallEscalation ? 'Escalado activado ✓' : 'Escalado desactivado ✓';
      setTimeout(() => (fwMsg = ''), 2500);
    } catch (e) {
      fwMsg = e.message || 'Error';
    } finally {
      fwBusy = false;
    }
  }

  let behavBusy = false;
  let behavMsg = '';
  async function toggleBehav() {
    if (behavBusy) return;
    behavBusy = true; behavMsg = '';
    try {
      await behaviorSetEnabled(!$status.behaviorEnforce);
      behavMsg = $status.behaviorEnforce ? 'Auto-bloqueo armado ✓' : 'Auto-bloqueo desarmado ✓';
      setTimeout(() => (behavMsg = ''), 2500);
    } catch (e) {
      behavMsg = e.message || 'Error';
    } finally {
      behavBusy = false;
    }
  }
</script>

{#if draft}
  <div class="set-wrap">
    <div class="set-group">
      <div class="set-group-head">Fuerza bruta · fallos tolerados</div>
      <p class="set-hint">Cuántos fallos de login admite una IP (en 5 min) antes del bloqueo, según su nivel de reputación. Una IP de confianza merece más margen ante un despiste.</p>
      <div class="set-row">
        <label>IP desconocida</label>
        <input type="number" min="1" max="100" bind:value={draft.fail_unknown} />
        <span class="set-unit">fallos</span>
      </div>
      <div class="set-row">
        <label>IP conocida</label>
        <input type="number" min="1" max="100" bind:value={draft.fail_known} />
        <span class="set-unit">fallos</span>
      </div>
      <div class="set-row">
        <label>IP habitual</label>
        <input type="number" min="1" max="100" bind:value={draft.fail_habitual} />
        <span class="set-unit">fallos</span>
      </div>
      <div class="set-row">
        <label>Racha de desconfianza</label>
        <input type="number" min="2" max="20" bind:value={draft.distrust_streak} />
        <span class="set-unit">seguidos</span>
      </div>
      <p class="set-hint">Una IP conocida que falla esta cantidad de veces SEGUIDAS entra en desconfianza (posible dispositivo robado) y se bloquea al instante.</p>
    </div>

    <div class="set-group">
      <div class="set-group-head">Duración del bloqueo · escalado por reincidencia</div>
      <p class="set-hint">Cada vez que una IP reincide, su bloqueo sube. Sin bloqueo permanente: el techo son 24h (1440 min).</p>
      <div class="set-row">
        <label>1er bloqueo</label>
        <input type="number" min="1" max="1440" bind:value={draft.block_min_1} />
        <span class="set-unit">min</span>
      </div>
      <div class="set-row">
        <label>2º bloqueo</label>
        <input type="number" min="1" max="1440" bind:value={draft.block_min_2} />
        <span class="set-unit">min</span>
      </div>
      <div class="set-row">
        <label>3er bloqueo</label>
        <input type="number" min="1" max="1440" bind:value={draft.block_min_3} />
        <span class="set-unit">min</span>
      </div>
      <div class="set-row">
        <label>4º en adelante</label>
        <input type="number" min="1" max="1440" bind:value={draft.block_min_4} />
        <span class="set-unit">min</span>
      </div>
    </div>

    <div class="set-group" class:fw-armed={$status.firewallEscalation}>
      <div class="set-group-head">Escalado a firewall · reincidentes</div>
      <p class="set-hint">
        Cuando una IP (o red IPv6) reincide — 2º bloqueo en adelante — NimShield puede añadirla
        al firewall del kernel (nftables): sus paquetes se descartan antes de tocar el sistema y
        deja de consumir recursos. Solo IPs públicas: la LAN, el loopback y cualquier red que
        contenga una IP de tu whitelist quedan fuera <b>siempre</b>. Al desactivar, la tabla del
        kernel se elimina entera. Desarmado, el log registra qué habría escalado (FW-OBSERVE).
      </p>
      <div class="set-row">
        <label>Estado</label>
        <span class="fw-state" class:on={$status.firewallEscalation}>
          {$status.firewallEscalation
            ? `ACTIVO · ${$status.firewallEntries || 0} en kernel`
            : 'DESARMADO (solo observa)'}
        </span>
        <button class="set-btn" class:fw-danger={$status.firewallEscalation} on:click={toggleFW} disabled={fwBusy}>
          {fwBusy ? '…' : $status.firewallEscalation ? 'Desarmar' : 'Armar'}
        </button>
      </div>
      {#if fwMsg}<span class="set-msg" class:err={!fwMsg.endsWith('✓')}>{fwMsg}</span>{/if}
    </div>

    <div class="set-group" class:fw-armed={$status.behaviorEnforce}>
      <div class="set-group-head">Motor de comportamiento · auto-bloqueo (Fase 2)</div>
      <p class="set-hint">
        Cada IP acumula un score de comportamiento (0–100) que agrega todas las señales del
        shield; el ataque sostenido lo hunde y el tiempo lo recupera. Armado, cruzar el umbral
        ({$status.scoreThreshold ?? 30}) bloquea automáticamente con la duración escalada por
        reincidencia. Salvaguardas: los eventos de una sesión válida y las IPs de la whitelist
        <b>jamás</b> gatillan el auto-bloqueo. Desarmado, el log registra qué habría bloqueado.
      </p>
      <div class="set-row">
        <label>Estado</label>
        <span class="fw-state" class:on={$status.behaviorEnforce}>
          {$status.behaviorEnforce ? 'ARMADO · bloqueo real' : 'DESARMADO (solo observa)'}
        </span>
        <button class="set-btn" class:fw-danger={$status.behaviorEnforce} on:click={toggleBehav} disabled={behavBusy}>
          {behavBusy ? '…' : $status.behaviorEnforce ? 'Desarmar' : 'Armar'}
        </button>
      </div>
      {#if behavMsg}<span class="set-msg" class:err={!behavMsg.endsWith('✓')}>{behavMsg}</span>{/if}
    </div>

    <p class="set-hint set-note">Las reglas duras (inyección, honeypots, path traversal, escáneres) no son configurables: son defensa innegociable para cualquier IP.</p>

    <div class="set-actions">
      {#if msg}<span class="set-msg" class:err={msg !== 'Guardado ✓'}>{msg}</span>{/if}
      <button class="set-btn ghost" on:click={restore} disabled={saving}>Restaurar por defecto</button>
      <button class="set-btn" on:click={save} disabled={saving}>{saving ? 'Guardando…' : 'Guardar'}</button>
    </div>
  </div>
{:else}
  <div class="ns-msg">Cargando configuración…</div>
{/if}

<style>
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }
  .set-wrap { display: flex; flex-direction: column; gap: 22px; max-width: 560px; }
  .set-group { border: 1px solid var(--bd-2, #20202a); border-radius: 8px; background: var(--bg-inner, #101015); padding: 16px 18px; }
  .set-group-head { font-family: var(--font-mono); font-size: 11px; letter-spacing: 0.6px; text-transform: uppercase; color: var(--nim-green, #00ff9f); margin-bottom: 6px; }
  .set-hint { font-size: 11px; color: var(--fg-4, #7a7a82); margin: 0 0 12px; line-height: 1.5; }
  .set-note { max-width: 560px; border-left: 2px solid var(--bd-3, #2a2a32); padding-left: 10px; }
  .set-row { display: flex; align-items: center; gap: 12px; padding: 6px 0; }
  .set-row label { flex: 1; font-size: 12.5px; color: var(--fg-2, #c8c8cf); }
  .set-row input { width: 80px; padding: 6px 10px; text-align: right; background: var(--bg, #0a0a0d); border: 1px solid var(--bd-3, #2a2a32); border-radius: 6px; color: var(--fg, #f0f0f0); font-family: var(--font-mono); font-size: 13px; font-variant-numeric: tabular-nums; }
  .set-row input:focus { border-color: rgba(0,255,159,0.4); outline: none; }
  .set-unit { width: 56px; font-family: var(--font-mono); font-size: 11px; color: var(--fg-5, #5a5a62); }
  .set-actions { display: flex; align-items: center; gap: 10px; justify-content: flex-end; }
  .set-msg { margin-right: auto; font-family: var(--font-mono); font-size: 12px; color: var(--nim-green, #00ff9f); }
  .set-msg.err { color: var(--st-crit, #f87171); }
  .set-btn { padding: 8px 18px; border-radius: 6px; cursor: pointer; font-size: 12.5px; font-weight: 600; border: 1px solid var(--nim-green, #00ff9f); background: rgba(0,255,159,0.1); color: var(--nim-green, #00ff9f); }
  .set-btn.ghost { border-color: var(--bd-3, #2a2a32); background: transparent; color: var(--fg-3, #b0b0b8); font-weight: 400; }
  .set-btn:disabled { opacity: 0.5; cursor: default; }
  .set-btn.fw-danger { border-color: var(--st-crit, #f87171); background: rgba(248,113,113,0.1); color: var(--st-crit, #f87171); }
  .fw-state { font-family: var(--font-mono); font-size: 11px; letter-spacing: 0.4px; color: var(--fg-4, #7a7a82); }
  .fw-state.on { color: var(--st-crit, #f87171); }
  .set-group.fw-armed { border-color: rgba(248,113,113,0.3); }
</style>
