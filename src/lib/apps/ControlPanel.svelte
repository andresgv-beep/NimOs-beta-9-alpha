<script>
  /**
   * ControlPanel · Panel de Control · administración del sistema
   * ─────────────────────────────────────────────────────────────
   * App de administración del NAS. Agrupa lo que antes vivía disperso en
   * NimSettings más los servicios de red que no tenían UI.
   *
   * Secciones (se irán cableando por fases — ver CONTROL-PANEL-PLAN.md):
   *   · Usuarios        (Fase 1 · desde Settings)
   *   · Compartidas     (Fase 2 · desde Settings)
   *   · Servicios       (Fase 3 · nuevo · SMB/WebDAV/SSH)
   *   · Permisos apps   (Fase 4 · desde Settings)
   *   · Portal / 2FA    (Fase 5 · desde Settings)
   *   · Actualizaciones (Fase 6 · desde Settings)
   *
   * FASE 0 (actual): andamiaje. Shell + navegación, secciones vacías con
   * placeholder. No mueve lógica todavía; Settings sigue intacto.
   */
  import AppShell from '$lib/components/AppShell.svelte';
  import CPUsers from './controlpanel/CPUsers.svelte';
  import CPShares from './controlpanel/CPShares.svelte';
  import CPTempLinks from './controlpanel/CPTempLinks.svelte';
  import CPServices from './controlpanel/CPServices.svelte';
  import CPPermissions from './controlpanel/CPPermissions.svelte';
  import CPPortal from './controlpanel/CPPortal.svelte';
  import CPUpdates from './controlpanel/CPUpdates.svelte';
  import CPMaintenance from './controlpanel/CPMaintenance.svelte';

  let active = 'users';

  const navIcon = body => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;

  const sections = [
    {
      label: 'Sistema',
      items: [
        { id: 'users', label: 'Usuarios', icon: navIcon('<circle cx="12" cy="8" r="3"/><path d="M6 19c.6-3.2 2.6-5 6-5s5.4 1.8 6 5"/>') },
        { id: 'shares', label: 'Carpetas compartidas', icon: navIcon('<path d="M3.5 6.5h6l2 2H20.5v9H3.5z"/><path d="M3.5 9h17"/>') },
        { id: 'templinks', label: 'Enlaces compartidos', icon: navIcon('<path d="M9.5 14.5l5-5"/><path d="M7.5 16.5l-1 1a3.5 3.5 0 0 1-5-5l3-3a3.5 3.5 0 0 1 5 0"/><path d="M14.5 7.5l1-1a3.5 3.5 0 0 1 5 5l-3 3a3.5 3.5 0 0 1-5 0"/>') },
        { id: 'services', label: 'Servicios', icon: navIcon('<rect x="4" y="4" width="16" height="6" rx="1"/><rect x="4" y="14" width="16" height="6" rx="1"/><path d="M8 7h.01M8 17h.01M12 7h5M12 17h5"/>') },
        { id: 'permissions', label: 'Permisos de apps', icon: navIcon('<path d="M12 3l7 3v5c0 4.3-2.8 7.7-7 10-4.2-2.3-7-5.7-7-10V6z"/><path d="M9 12l2 2 4-4"/>') },
        { id: 'portal', label: 'Portal · 2FA', icon: navIcon('<circle cx="8" cy="12" r="3"/><path d="M11 12h9M17 12v3M14 12v2"/>') },
        { id: 'updates', label: 'Actualizaciones', icon: navIcon('<path d="M12 3v12M8 11l4 4 4-4"/><path d="M5 19h14"/>') },
        { id: 'maintenance', label: 'Limpieza y mantenimiento', icon: navIcon('<path d="M14 4l6 6-8.5 8.5a3 3 0 0 1-4.2 0l-1.8-1.8a3 3 0 0 1 0-4.2z"/><path d="M12 6l6 6M4 21h9"/>') },
      ],
    },
  ];

  const meta = {
    users:       { t: 'Usuarios',         s: '· cuentas y accesos' },
    shares:      { t: 'Carpetas compartidas', s: '· acceso en red' },
    templinks:   { t: 'Enlaces compartidos', s: '· archivos temporales que compartes' },
    services:    { t: 'Servicios',        s: '· SMB · WebDAV · SSH' },
    permissions: { t: 'Permisos de apps', s: '· qué puede usar cada usuario' },
    portal:      { t: 'Portal · 2FA',     s: '· seguridad de acceso' },
    updates:     { t: 'Actualizaciones',  s: '· versión del sistema' },
    maintenance: { t: 'Limpieza y mantenimiento', s: '· tareas automáticas de higiene' },
  };
</script>

<AppShell
  appId="controlpanel"
  title="Panel de Control"
  headerIcon="⚙"
  {sections}
  bind:active
>
  <svelte:fragment slot="page-header">
    <b>{meta[active]?.t || 'Panel de Control'}</b>
    <span class="cp-sub">{meta[active]?.s || ''}</span>
  </svelte:fragment>

  <div class="cp-body">
    {#if active === 'users'}
      <CPUsers />
    {:else if active === 'shares'}
      <CPShares />
    {:else if active === 'templinks'}
      <CPTempLinks />
    {:else if active === 'services'}
      <CPServices />
    {:else if active === 'permissions'}
      <CPPermissions />
    {:else if active === 'portal'}
      <CPPortal />
    {:else if active === 'updates'}
      <CPUpdates />
    {:else if active === 'maintenance'}
      <CPMaintenance />
    {:else}
      <div class="cp-placeholder">
        <div class="cp-ph-icon"></div>
        <div class="cp-ph-title">{meta[active]?.t}</div>
        <div class="cp-ph-hint">Sección en construcción · se cableará en su fase de migración.</div>
      </div>
    {/if}
  </div>
</AppShell>

<style>
  .cp-sub {
    color: var(--fg-4, #7a7a82);
    font-size: 12px;
    font-weight: 400;
  }
  .cp-body {
    min-height: 200px;
  }
  .cp-placeholder {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 60px 24px;
    text-align: center;
  }
  .cp-ph-icon {
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: 1px solid var(--bd-3, #2a2a32);
    background: var(--bg-card, #15151a);
    margin-bottom: 6px;
  }
  .cp-ph-title {
    font-size: 14px;
    color: var(--fg-2, #d0d0d4);
    font-family: var(--font-sans);
  }
  .cp-ph-hint {
    font-size: 11px;
    color: var(--fg-5, #5a5a62);
    font-family: var(--font-sans);
    max-width: 320px;
    line-height: 1.5;
  }
</style>
