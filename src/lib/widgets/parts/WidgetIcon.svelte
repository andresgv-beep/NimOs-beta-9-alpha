<script>
  /**
   * WidgetIcon · iconos SVG inline para el catálogo de widgets.
   * Sin dependencia externa: trazo currentColor, hereda el color del
   * contenedor. `name` mapea a los iconos del catálogo (index.js).
   */
  export let name = 'cpu';
  export let size = 18;
</script>

<svg
  width={size} height={size} viewBox="0 0 24 24"
  fill="none" stroke="currentColor" stroke-width="1.6"
  stroke-linecap="square" stroke-linejoin="miter"
>
  {#if name === 'cpu'}
    <rect x="7" y="7" width="10" height="10" />
    <rect x="10" y="10" width="4" height="4" />
    <path d="M9 7V4M12 7V4M15 7V4M9 20v-3M12 20v-3M15 20v-3M7 9H4M7 12H4M7 15H4M20 9h-3M20 12h-3M20 15h-3" />
  {:else if name === 'ram'}
    <rect x="3" y="8" width="18" height="8" />
    <path d="M7 16v3M12 16v3M17 16v3M7 11v2M10.5 11v2M14 11v2M17 11v2" />
  {:else if name === 'hdd'}
    <!-- disco duro: cuerpo + plato + LED -->
    <rect x="3" y="5" width="18" height="14" rx="1" />
    <circle cx="12" cy="12" r="4.5" />
    <circle cx="12" cy="12" r="1" />
    <path d="M17 16.5h0.01" />
  {:else if name === 'activity'}
    <path d="M2 12h4l2.5-6 4 12L15 9l1.5 3H22" />
  {:else if name === 'clock'}
    <circle cx="12" cy="12" r="8" />
    <path d="M12 8v4l3 2" />
  {:else if name === 'services'}
    <rect x="3" y="4" width="18" height="6" rx="1" />
    <rect x="3" y="14" width="18" height="6" rx="1" />
    <path d="M7 7h0.01M7 17h0.01" />
  {:else if name === 'download'}
    <path d="M12 3v12M7 11l5 5 5-5M4 20h16" />
  {:else if name === 'folder'}
    <path d="M3 7a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1Z" />
  {:else if name === 'shield'}
    <path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6Z" />
    <path d="M9 12l2 2 4-4" />
  {:else}
    <rect x="4" y="4" width="16" height="16" rx="2" />
  {/if}
</svg>
