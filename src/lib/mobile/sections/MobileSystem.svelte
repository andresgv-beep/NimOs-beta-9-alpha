<script>
  // MobileSystem — accesos a áreas de gestión + energía (reiniciar/apagar).
  // Las áreas de gestión completas viven en el escritorio; aquí damos las
  // acciones de energía (con confirmación) y enlaces informativos. Se irá
  // rellenando con vistas móviles propias de cada área cuando hagan falta.
  import { jsonHdrs } from '$lib/stores/auth.js';
  import { logout } from '$lib/stores/auth.js';

  let confirming = null; // 'reboot' | 'shutdown' | null
  let busy = false;
  let result = '';

  async function doPower(action) {
    busy = true;
    result = '';
    try {
      const r = await fetch(`/api/system/${action}`, { method: 'POST', headers: jsonHdrs() });
      result = r.ok
        ? (action === 'reboot' ? 'Reiniciando NimOS…' : 'Apagando el NAS…')
        : 'No se pudo ejecutar la acción.';
    } catch (e) {
      result = 'Error de red al enviar la acción.';
    } finally {
      busy = false;
      confirming = null;
    }
  }

  const AREAS = [
    { icon: 'net',   name: 'Red y exposición', desc: 'DDNS · puertos · dominios' },
    { icon: 'users', name: 'Usuarios',         desc: 'cuentas y permisos' },
    { icon: 'broom', name: 'Limpieza',         desc: 'temporales · cachés · logs' },
    { icon: 'refresh', name: 'Actualizaciones', desc: 'versión del sistema' },
  ];
</script>

<section class="m-section">
  <div class="section-t">Sistema</div>

  {#each AREAS as a}
    <div class="sys-action info">
      <div class="sys-ico">
        {#if a.icon === 'net'}
          <svg viewBox="0 0 24 24"><path d="M5 12.55a11 11 0 0 1 14.08 0"/><path d="M1.42 9a16 16 0 0 1 21.16 0"/><path d="M8.53 16.11a6 6 0 0 1 6.95 0"/><line x1="12" y1="20" x2="12.01" y2="20"/></svg>
        {:else if a.icon === 'users'}
          <svg viewBox="0 0 24 24"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
        {:else if a.icon === 'broom'}
          <svg viewBox="0 0 24 24"><path d="M19 5l-7 7"/><path d="M12 12l-3 7-4-4 7-3z"/></svg>
        {:else}
          <svg viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
        {/if}
      </div>
      <div class="sys-info">
        <div class="sys-name">{a.name}</div>
        <div class="sys-desc">{a.desc}</div>
      </div>
      <span class="sys-tag">escritorio</span>
    </div>
  {/each}

  <div class="section-t">Sesión</div>
  <button class="sys-action" on:click={logout}>
    <div class="sys-ico"><svg viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg></div>
    <div class="sys-info"><div class="sys-name">Cerrar sesión</div><div class="sys-desc">salir de NimOS</div></div>
    <svg class="sys-chev" viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
  </button>

  <div class="section-t">Energía</div>

  {#if result}
    <div class="power-result">{result}</div>
  {/if}

  <button class="sys-action danger" on:click={() => (confirming = 'reboot')} disabled={busy}>
    <div class="sys-ico danger"><svg viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg></div>
    <div class="sys-info"><div class="sys-name">Reiniciar NimOS</div><div class="sys-desc">reinicio del sistema</div></div>
  </button>

  <button class="sys-action danger" on:click={() => (confirming = 'shutdown')} disabled={busy}>
    <div class="sys-ico danger"><svg viewBox="0 0 24 24"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/></svg></div>
    <div class="sys-info"><div class="sys-name">Apagar</div><div class="sys-desc">apagar el NAS</div></div>
  </button>
</section>

{#if confirming}
  <div class="confirm-backdrop" on:click|self={() => (confirming = null)} role="presentation">
    <div class="confirm-sheet">
      <div class="confirm-title">
        {confirming === 'reboot' ? '¿Reiniciar NimOS?' : '¿Apagar el NAS?'}
      </div>
      <div class="confirm-desc">
        {confirming === 'reboot'
          ? 'El sistema se reiniciará y no estará disponible unos minutos.'
          : 'El NAS se apagará. Tendrás que encenderlo físicamente para volver.'}
      </div>
      <div class="confirm-actions">
        <button class="cbtn" on:click={() => (confirming = null)}>Cancelar</button>
        <button class="cbtn danger" on:click={() => doPower(confirming)} disabled={busy}>
          {busy ? '…' : (confirming === 'reboot' ? 'Reiniciar' : 'Apagar')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .m-section { padding-bottom: 8px; }
  .section-t { font-size: 11px; color: var(--ink-mute); font-family: var(--font-mono); text-transform: uppercase; letter-spacing: 0.8px; font-weight: 600; margin: 18px 2px 12px; }

  .sys-action { display: flex; align-items: center; gap: 13px; background: var(--bg-card); border: 1px solid var(--line); border-radius: 11px; padding: 15px; margin-bottom: 9px; width: 100%; text-align: left; cursor: pointer; }
  .sys-action.info { cursor: default; }
  button.sys-action:active { background: var(--main-hover); }
  .sys-ico { width: 38px; height: 38px; border-radius: 10px; background: var(--bg-inner); display: flex; align-items: center; justify-content: center; color: var(--ink-dim); flex-shrink: 0; }
  .sys-ico.danger { background: rgba(248,113,113,0.12); color: var(--crit); }
  .sys-ico svg { width: 19px; height: 19px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .sys-info { flex: 1; }
  .sys-name { font-size: 14px; font-weight: 600; color: var(--ink); }
  .sys-desc { font-size: 11px; color: var(--ink-mute); margin-top: 1px; }
  .sys-tag { font-size: 9px; font-family: var(--font-mono); color: var(--ink-faint); text-transform: uppercase; letter-spacing: 0.5px; border: 1px solid var(--line); padding: 2px 7px; border-radius: 4px; }
  .sys-chev { width: 16px; height: 16px; stroke: var(--ink-faint); stroke-width: 2; fill: none; flex-shrink: 0; }

  .power-result { background: var(--signal-soft, rgba(0,255,159,0.08)); border: 1px solid rgba(0,255,159,0.25); color: var(--signal); font-size: 12px; font-family: var(--font-mono); padding: 11px 14px; border-radius: 8px; margin-bottom: 10px; }

  .confirm-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.6); -webkit-backdrop-filter: blur(3px); backdrop-filter: blur(3px); display: flex; align-items: flex-end; z-index: 100; }
  .confirm-sheet { width: 100%; background: var(--bg-window); border-top: 1px solid var(--line-bright); border-radius: 18px 18px 0 0; padding: 22px 20px calc(22px + env(safe-area-inset-bottom)); }
  .confirm-title { font-size: 17px; font-weight: 700; color: var(--ink); margin-bottom: 8px; }
  .confirm-desc { font-size: 13px; color: var(--ink-mute); line-height: 1.5; margin-bottom: 20px; }
  .confirm-actions { display: flex; gap: 10px; }
  .cbtn { flex: 1; padding: 13px; border-radius: 10px; font-size: 14px; font-weight: 600; cursor: pointer; border: 1px solid var(--line-bright); background: transparent; color: var(--ink-dim); }
  .cbtn.danger { background: var(--crit); border-color: var(--crit); color: #fff; }
  .cbtn:disabled { opacity: 0.5; }
</style>
