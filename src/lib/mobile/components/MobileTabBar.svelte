<script>
  // MobileTabBar — navegación inferior. Lee/escribe el store de sección activa.
  // Respeta el safe-area-inset del móvil (notch/barra inferior de iOS/Android).
  import { activeMobileSection, goToSection } from '../mobileNav.js';

  const TABS = [
    { id: 'home',   label: 'Inicio',   icon: 'home' },
    { id: 'apps',   label: 'Apps',     icon: 'grid' },
    { id: 'files',  label: 'Archivos', icon: 'folder' },
    { id: 'system', label: 'Sistema',  icon: 'cog' },
  ];
</script>

<nav class="tabbar">
  {#each TABS as tab}
    <button
      class="tab"
      class:active={$activeMobileSection === tab.id}
      on:click={() => goToSection(tab.id)}
    >
      {#if tab.icon === 'home'}
        <svg viewBox="0 0 24 24"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>
      {:else if tab.icon === 'grid'}
        <svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/></svg>
      {:else if tab.icon === 'folder'}
        <svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
      {:else}
        <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
      {/if}
      <span>{tab.label}</span>
    </button>
  {/each}
</nav>

<style>
  .tabbar {
    flex-shrink: 0;
    background: color-mix(in srgb, var(--side-bg) 96%, transparent);
    -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
    border-top: 1px solid var(--line);
    display: flex; align-items: stretch;
    padding: 8px 8px max(8px, env(safe-area-inset-bottom));
    z-index: 50;
  }
  .tab {
    flex: 1; display: flex; flex-direction: column; align-items: center; gap: 4px;
    background: none; border: none; cursor: pointer; padding: 6px 0;
    color: var(--ink-faint);
  }
  .tab.active { color: var(--signal); }
  .tab svg { width: 22px; height: 22px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .tab span { font-size: 10px; font-weight: 600; }
</style>
