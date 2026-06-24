<script>
  // MobileShell — contenedor raíz de la UI móvil. Orquesta header + sección
  // activa + tab bar. Es el único punto que +page.svelte monta para móvil.
  // Cada sección es su propio componente (modular): rellenar/cambiar una no
  // afecta al resto ni al escritorio.
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { activeMobileSection, goToSection } from './mobileNav.js';
  import MobileHeader from './components/MobileHeader.svelte';
  import MobileTabBar from './components/MobileTabBar.svelte';
  import MobileHome from './sections/MobileHome.svelte';
  import MobileApps from './sections/MobileApps.svelte';
  import MobileFiles from './sections/MobileFiles.svelte';
  import MobileSystem from './sections/MobileSystem.svelte';

  let hostname = 'nimos';
  let alerts = 0;

  const SECTIONS = {
    home: MobileHome,
    apps: MobileApps,
    files: MobileFiles,
    system: MobileSystem,
  };

  $: ActiveSection = SECTIONS[$activeMobileSection] || MobileHome;

  // El botón de energía del header lleva a la sección Sistema (donde están
  // reiniciar/apagar con confirmación), en vez de actuar directo desde aquí.
  function onPower() {
    goToSection('system');
  }

  function onAlerts() {
    // Placeholder: futura bandeja de alertas. De momento no hace nada visible.
  }

  async function loadIdentity() {
    try {
      const r = await fetch('/api/system/hostname', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        if (d.hostname) hostname = d.hostname;
      }
    } catch (e) {
      // se queda con el default
    }
  }

  onMount(loadIdentity);
</script>

<div class="mobile-shell">
  <div class="m-scroll">
    <div class="m-pad">
      <MobileHeader {hostname} {alerts} on:power={onPower} on:alerts={onAlerts} />
      <svelte:component this={ActiveSection} />
    </div>
  </div>
  <MobileTabBar />
</div>

<style>
  .mobile-shell {
    position: fixed; inset: 0;
    background: var(--canvas);
    color: var(--ink);
    display: flex; flex-direction: column;
    font-family: var(--font-sans);
  }
  .m-scroll {
    flex: 1; overflow-y: auto; -webkit-overflow-scrolling: touch;
    padding-bottom: 8px;
  }
  .m-pad {
    padding: calc(8px + env(safe-area-inset-top)) 16px 8px;
    max-width: 640px; margin: 0 auto;
  }
  .m-scroll::-webkit-scrollbar { display: none; }
</style>
