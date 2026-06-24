<script>
  /**
   * NetworkApp · Shell del módulo Network (v4 · rediseño modular).
   * ────────────────────────────────────────────────────────────────
   * Reescrito desde cero siguiendo el patrón modular de StorageApp:
   * un shell delgado (AppShell) que orquesta secciones independientes.
   * El monolito anterior (1200 líneas con tabs Ports/Router/DDNS/Certs/
   * Proxy) se eliminó. Certs y Proxy desaparecen: ahora la exposición
   * HTTPS la gestiona Caddy a través del subsistema network_exposure.
   *
   * Secciones:
   *   · exposure — Exposición de apps (NUEVA, v4, funcional)
   *   · ddns     — DDNS (reusa NetworkDDNS.svelte, autónomo)
   *   · ports    — Puertos (placeholder · pendiente de migrar a modular)
   *   · router   — Router/UPnP (placeholder · pendiente de migrar)
   *
   * Las secciones ports/router tienen backend v4 funcionando; su UI se
   * migrará al patrón modular en sprints siguientes. No se porta el código
   * legacy: se rehará limpio (regla del proyecto).
   */
  import { onMount, onDestroy } from 'svelte';
  import AppShell from '$lib/components/AppShell.svelte';
  import { Spinner } from '$lib/ui';

  import NetworkKPIs from './network/NetworkKPIs.svelte';
  import NetworkExposure from './network/NetworkExposure.svelte';
  import ExposeAppModal from './network/ExposeAppModal.svelte';
  import NetworkDDNS from './network/NetworkDDNS.svelte';
  import * as api from './network/api.js';

  // ─── Navegación ───
  let active = 'exposure';

  const VIEW_META = {
    exposure: { title: 'Exposición', desc: 'apps accesibles desde fuera de tu red' },
    ddns: { title: 'DDNS', desc: 'dominio dinámico apuntando a tu IP' },
    ports: { title: 'Puertos', desc: 'puertos HTTP/HTTPS del daemon' },
    router: { title: 'Router', desc: 'mapeo de puertos UPnP' },
  };
  $: viewMeta = VIEW_META[active] || VIEW_META.exposure;

  // ─── Estado de Exposición ───
  let loading = true;
  let config = { base_domain: '', caddy_admin_url: '', enabled: false };
  let apps = [];
  let certs = null;
  let busy = false;
  let msg = '';

  // Modal exponer/editar
  let modalOpen = false;
  let modalApp = null;     // null = crear, app = editar
  let installedApps = [];  // apps Docker detectadas (picker del modal)
  let modalError = '';

  onMount(loadExposure);

  // ─── Polling suave ───
  // El cert de una app recién expuesta se emite a los ~30-60s y el observer
  // lo publica, pero sin esto la UI se quedaba en "emitiendo certificado…"
  // congelada hasta cerrar y reabrir la ventana. Refresco cada 15s mientras
  // la ventana viva; se salta el tick si hay una mutación en vuelo (busy)
  // para no pisar estados intermedios.
  const pollTimer = setInterval(() => {
    if (!busy && !loading) refresh();
  }, 15000);
  onDestroy(() => clearInterval(pollTimer));

  async function loadExposure() {
    loading = true;
    try {
      config = await api.getExposureConfig();
      const data = await api.listExposure();
      apps = data.apps;
      certs = data.certs;
    } catch (e) {
      msg = `Error cargando exposición: ${e.message}`;
    } finally {
      loading = false;
    }
  }

  async function refresh() {
    try {
      const data = await api.listExposure();
      apps = data.apps;
      certs = data.certs;
      // SHIELD-P2 · estado del candado de cada app (para la tarjeta)
      installedApps = await api.listInstalledApps().catch(() => installedApps);
    } catch (e) {
      msg = `Error: ${e.message}`;
    }
  }

  // SHIELD-P2 · cerrar/abrir el puerto directo de una app (recrea el stack)
  async function onLock(e) {
    busy = true;
    msg = '';
    try {
      const r = await api.setAppAccessMode(e.detail.appId, e.detail.mode);
      msg = e.detail.mode === 'caddy_only'
        ? `🔒 Puerto directo cerrado (${r.portsRewritten} binding${r.portsRewritten === 1 ? '' : 's'}) — Caddy es la única puerta.`
        : 'Puerto directo reabierto en LAN.';
      await refresh();
    } catch (err) {
      msg = `Error: ${err.message}`;
    } finally {
      busy = false;
    }
  }

  async function onSaveConfig(e) {
    busy = true;
    msg = '';
    try {
      config = await api.saveExposureConfig({
        baseDomain: e.detail.baseDomain,
        enabled: e.detail.enabled,
        httpPort: e.detail.httpPort,
        httpsPort: e.detail.httpsPort,
      });
      msg = 'Configuración guardada.';
      await refresh();
    } catch (err) {
      msg = `Error: ${err.message}`;
    } finally {
      busy = false;
    }
  }

  function onExpose() {
    modalApp = null;
    api.listInstalledApps().then((apps) => (installedApps = apps)).catch(() => (installedApps = []));
    modalError = '';
    modalOpen = true;
  }

  function onEdit(e) {
    modalApp = e.detail.app;
    modalError = '';
    modalOpen = true;
  }

  async function onModalSubmit(e) {
    busy = true;
    modalError = '';
    try {
      if (e.detail.id) {
        await api.updateExposedApp(e.detail.id, e.detail.fields,
          modalApp?.convergence?.desired_generation);
      } else {
        await api.exposeApp(e.detail.fields);
      }
      modalOpen = false;
      await refresh();
    } catch (err) {
      if (err.status === 412) {
        // Conflicto de concurrencia: la app cambió en otro sitio mientras
        // editabas. Refrescamos la lista; el modal queda abierto con tu
        // texto para que decidas sobre el estado actual.
        modalError = 'Conflicto: la app fue modificada en otro sitio. Lista actualizada — cierra y reintenta sobre el estado actual.';
        await refresh();
      } else {
        modalError = err.message;
      }
    } finally {
      busy = false;
    }
  }

  async function onToggle(e) {
    busy = true;
    try {
      await api.updateExposedApp(e.detail.app.id, { enabled: e.detail.enabled },
        e.detail.app.convergence?.desired_generation);
      await refresh();
    } catch (err) {
      if (err.status === 412) {
        msg = 'La app cambió en otro sitio — lista actualizada, reintenta.';
        await refresh();
      } else {
        msg = `Error: ${err.message}`;
      }
    } finally {
      busy = false;
    }
  }

  async function onRemove(e) {
    if (!confirm(`¿Dejar de exponer "${e.detail.app.display_name || e.detail.app.app_id}"?`)) return;
    busy = true;
    try {
      // GUARDARRAÍL SHIELD-P2: si el puerto directo está cerrado y quitas
      // la exposición, la app quedaría inaccesible por completo. Reabrimos
      // LAN primero para no dejarte fuera.
      const inst = installedApps.find((x) => x.id === e.detail.app.app_id);
      if (inst && inst.accessMode === 'caddy_only') {
        await api.setAppAccessMode(inst.id, 'lan');
        msg = 'Puerto LAN reabierto (la app dejará de estar tras Caddy).';
      }
      await api.unexposeApp(e.detail.app.id, e.detail.app.convergence?.desired_generation);
      await refresh();
    } catch (err) {
      if (err.status === 412) {
        msg = 'La app cambió en otro sitio — lista actualizada, revisa antes de quitar.';
        await refresh();
      } else {
        msg = `Error: ${err.message}`;
      }
    } finally {
      busy = false;
    }
  }
</script>

<AppShell
  appId="network"
  title="Network"
  headerIcon="N"
  pathSegments={['network', active]}
  sections={[
    {
      label: 'Conectividad',
      items: [
        {
          id: 'exposure',
          label: 'Exposición',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c1.8 1.6 2.8 3.8 2.8 6S9.8 14.4 8 14M8 2C6.2 3.6 5.2 5.8 5.2 8S6.2 14.4 8 14"/></svg>`,
        },
        {
          id: 'ddns',
          label: 'DDNS',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2v12M2 8h12"/><circle cx="8" cy="8" r="6"/></svg>`,
        },
      ],
    },
    {
      label: 'Sistema',
      items: [
        {
          id: 'ports',
          label: 'Puertos',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="12" height="6" rx="1"/><path d="M5 5V3M11 5V3"/></svg>`,
        },
        {
          id: 'router',
          label: 'Router',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="8" width="12" height="5" rx="1"/><path d="M8 8V4M5 4h6"/></svg>`,
        },
      ],
    },
  ]}
  bind:active
  bodyPadding={false}
>
  <svelte:fragment slot="page-header">
    <b>{viewMeta.title}</b>
    <span class="ph-desc">· {viewMeta.desc}</span>
  </svelte:fragment>

  {#if active === 'exposure'}
    {#if loading}
      <div class="nx-loading"><Spinner label="Cargando exposición…" /></div>
    {:else}
      <div class="nx-kpis-wrap">
        <NetworkKPIs {apps} {certs} {config} />
      </div>
      <div class="nx-scroll">
        <NetworkExposure
          {config}
          {apps}
          {certs}
          {installedApps}
          {busy}
          {msg}
          on:save-config={onSaveConfig}
          on:expose={onExpose}
          on:edit={onEdit}
          on:toggle={onToggle}
          on:remove={onRemove}
          on:lock={onLock}
          on:goto-ddns={() => (active = 'ddns')}
        />
      </div>
    {/if}
  {/if}

  {#if active === 'ddns'}
    <div class="nx-scroll">
      <NetworkDDNS />
    </div>
  {/if}

  {#if active === 'ports'}
    <div class="nx-placeholder">
      <div class="nx-ph-icon">◇</div>
      <h3>Puertos</h3>
      <p>La gestión de puertos del daemon (HTTP/HTTPS) está disponible en el backend v4.
         Su interfaz se migrará al nuevo diseño modular en una próxima actualización.</p>
    </div>
  {/if}

  {#if active === 'router'}
    <div class="nx-placeholder">
      <div class="nx-ph-icon">◇</div>
      <h3>Router</h3>
      <p>El mapeo de puertos UPnP está disponible en el backend v4.
         Su interfaz se migrará al nuevo diseño modular en una próxima actualización.</p>
    </div>
  {/if}
</AppShell>

{#if modalOpen}
  <ExposeAppModal
    app={modalApp}
    {installedApps}
    baseDomain={config.base_domain}
    httpsPort={config.https_port}
    {busy}
    error={modalError}
    on:submit={onModalSubmit}
    on:cancel={() => (modalOpen = false)}
  />
{/if}

<style>
  .nx-loading { display: flex; justify-content: center; padding: 60px 0; }
  /* Mismo respiro que el resto de apps (patrón .st-scroll de Storage):
     padding lateral estándar para no pegarse a los marcos de la ventana. */
  .nx-kpis-wrap { padding: 18px 28px 0; }
  .nx-scroll { flex: 1; overflow-y: auto; padding: 14px 28px 24px; }

  .nx-placeholder {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    text-align: center; padding: 60px 30px; color: var(--fg-4, #7a7a82);
  }
  .nx-ph-icon { font-size: 32px; color: var(--fg-5, #5a5a62); margin-bottom: 12px; }
  .nx-placeholder h3 { font-size: 15px; color: var(--fg-2, #d0d0d4); font-weight: 500; margin: 0 0 8px; }
  .nx-placeholder p { font-size: 12px; max-width: 380px; line-height: 1.5; margin: 0; }

  .ph-desc { color: var(--fg-4, #7a7a82); font-weight: 400; }
</style>
