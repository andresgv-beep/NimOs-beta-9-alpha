<script>
  /**
   * NimShield — guardián de NimOS. Shell + router de vistas.
   * El estado y la lógica viven en shield/shieldStore.js; cada vista en su
   * propio componente bajo shield/. Este archivo solo orquesta.
   */
  import { onMount, onDestroy } from 'svelte';
  import AppShell from '$lib/components/AppShell.svelte';
  import {
    status, events, blocks, whitelist, reputation,
    loading, adminRequired, now,
    startShield, stopShield, toggleEngine,
  } from './shield/shieldStore.js';
  import ShieldOverview from './shield/ShieldOverview.svelte';
  import ShieldEvents from './shield/ShieldEvents.svelte';
  import ShieldBlocks from './shield/ShieldBlocks.svelte';
  import ShieldWhitelist from './shield/ShieldWhitelist.svelte';
  import ShieldReputation from './shield/ShieldReputation.svelte';
  import ShieldSettings from './shield/ShieldSettings.svelte';
  import ShieldIntelligence from './shield/ShieldIntelligence.svelte';

  let active = 'overview';

  $: sections = [
    {
      label: 'Vista',
      items: [
        { id: 'overview',  label: 'Resumen' },
        { id: 'events',    label: 'Eventos',   badge: $events.length || null },
        { id: 'blocks',    label: 'Bloqueos',  badge: $blocks.length || null, badgeVariant: $blocks.length ? 'crit' : 'default' },
        { id: 'whitelist', label: 'Whitelist', badge: $whitelist.length || null },
        { id: 'reputation', label: 'Reputación', badge: $reputation.length || null },
        { id: 'intel', label: 'Intelligence' },
      ],
    },
    {
      label: 'Configuración',
      items: [
        { id: 'settings', label: 'Ajustes' },
      ],
    },
  ];

  const viewTitles = {
    overview:  { t: 'Resumen',   s: '· estado del motor de defensa' },
    events:    { t: 'Eventos',   s: '· stream del motor' },
    blocks:    { t: 'Bloqueos',  s: '· IPs bloqueadas activas' },
    whitelist: { t: 'Whitelist', s: '· IPs en confianza' },
    reputation: { t: 'Reputación', s: '· IPs conocidas y su nivel' },
    intel: { t: 'Intelligence', s: '· threat feed firmado' },
    settings: { t: 'Ajustes', s: '· política de defensa' },
  };

  onMount(startShield);
  onDestroy(stopShield);
</script>

<AppShell
  appId="nimshield"
  title="NimShield"
  {sections}
  bind:active
>
  <!-- ═══ ENGINE TOGGLE · pie del sidebar ═══ -->
  <svelte:fragment slot="sidebar-foot">
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div
      class="engine-toggle"
      class:off={!$status.enabled}
      on:click={toggleEngine}
      role="switch"
      aria-checked={$status.enabled}
      tabindex="0"
      on:keydown={(e) => e.key === 'Enter' && toggleEngine()}
      title={$status.enabled ? 'Desactivar NimShield' : 'Activar NimShield'}
    >
      <div class="engine-lbl">
        <span class="engine-led" class:off={!$status.enabled}></span>
        <span>Engine</span>
      </div>
      <div class="toggle-switch" class:off={!$status.enabled}></div>
    </div>
  </svelte:fragment>

  <!-- ═══ HEADER ═══ -->
  <svelte:fragment slot="page-header">
    <b>{viewTitles[active]?.t || 'NimShield'}</b>
    <span class="ns-sub">
      {#if active === 'events'}· stream del motor · {$events.length} últimos
      {:else if active === 'blocks'}· {$blocks.length} IPs bloqueadas activas
      {:else if active === 'whitelist'}· {$whitelist.length + 1} IPs en confianza
      {:else if active === 'reputation'}· {$reputation.length} IPs conocidas
      {:else}{viewTitles[active]?.s}
      {/if}
    </span>
  </svelte:fragment>

  <div class="ns-body">
    {#if $loading}
      <div class="ns-msg">Cargando…</div>
    {:else if $adminRequired}
      <div class="ns-msg">Se requiere rol de administrador para ver NimShield.</div>
    {:else if active === 'overview'}
      <ShieldOverview status={$status} events={$events} now={$now} />
    {:else if active === 'events'}
      <ShieldEvents events={$events} />
    {:else if active === 'blocks'}
      <ShieldBlocks blocks={$blocks} now={$now} />
    {:else if active === 'whitelist'}
      <ShieldWhitelist whitelist={$whitelist} now={$now} />
    {:else if active === 'reputation'}
      <ShieldReputation reputation={$reputation} now={$now} />
    {:else if active === 'intel'}
      <ShieldIntelligence />
    {:else if active === 'settings'}
      <ShieldSettings />
    {/if}
  </div>
</AppShell>

<style>
  .ns-sub { color: var(--fg-4, #7a7a82); font-size: 12px; font-weight: 400; }
  .ns-msg { padding: 24px; text-align: center; color: var(--fg-5, #5a5a62); font-size: 12px; font-family: var(--font-mono); }

  /* ═══ ENGINE TOGGLE (sidebar foot) ═══ */
  .engine-toggle { display: flex; align-items: center; justify-content: space-between; padding: 8px 10px; background: rgba(0, 255, 159, 0.05); border: 1px solid rgba(0, 255, 159, 0.18); border-radius: 6px; cursor: pointer; transition: background 0.15s, border-color 0.15s; }
  .engine-toggle.off { background: rgba(255, 90, 90, 0.04); border-color: rgba(255, 90, 90, 0.18); }
  .engine-lbl { display: flex; align-items: center; gap: 7px; font-size: 11px; color: var(--fg-2, #d0d0d4); font-weight: 500; }
  .engine-led { width: 7px; height: 7px; border-radius: 1.5px; background: var(--st-ok, #00ff9f); box-shadow: 0 0 5px rgba(0, 255, 159, 0.4); animation: pulse 2.5s ease-in-out infinite; }
  .engine-led.off { background: var(--st-crit, #ff5a5a); box-shadow: 0 0 5px rgba(255, 90, 90, 0.4); animation: none; }
  @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.55; } }
  .toggle-switch { width: 28px; height: 16px; background: var(--nim-green, #00ff9f); border-radius: 3px; position: relative; transition: background 0.15s; flex-shrink: 0; }
  .toggle-switch::after { content: ''; position: absolute; top: 2px; right: 2px; width: 12px; height: 12px; background: var(--bg-window, #16161a); border-radius: 2px; transition: right 0.15s, left 0.15s; }
  .toggle-switch.off { background: var(--bd-3, #2a2a32); }
  .toggle-switch.off::after { right: 14px; }
</style>
