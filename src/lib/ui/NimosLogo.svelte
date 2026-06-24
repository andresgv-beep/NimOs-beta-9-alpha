<script>
  /**
   * NimosLogo · Logo NimOS reutilizable
   * ───────────────────────────────────
   * Componente que renderiza el logo completo de NimOS (3 cubos + texto).
   * Pensado para usarse en: wallpaper, splash, login, settings, about, footers...
   *
   * Props:
   *   size: cubos en px (default 60)
   *   showText: si muestra "NimOs" al lado (default true)
   *   textSize: tamaño del texto en px (default proporcional a size · 0.8)
   *   glow: aplicar drop-shadow lechoso (default true)
   *   variant: 'white' | 'mono' | 'flat' (default 'white')
   *     - white: cubos con gradient blanco→gris (firma del wallpaper)
   *     - mono: cubos blancos planos (firma del taskbar)
   *     - flat: cubos sin glow, color sólido
   *
   * Uso:
   *   <NimosLogo />                          // Logo completo medio
   *   <NimosLogo size={120} />               // Logo grande
   *   <NimosLogo showText={false} />         // Solo los 3 cubos
   *   <NimosLogo variant="mono" size={28} /> // Mini, sólido (taskbar)
   */

  export let size = 60;
  export let showText = true;
  export let textSize = null;
  export let glow = true;
  export let variant = 'white'; // 'white' | 'mono' | 'flat'

  // Auto-calcular textSize si no se pasa
  $: finalTextSize = textSize ?? Math.round(size * 0.8);
  $: gap = Math.round(size * 0.1);

  // Identificador único para el gradient (evita colisiones si hay múltiples logos en la página)
  const gradId = 'nimos-cube-grad-' + Math.random().toString(36).slice(2, 9);
</script>

<div
  class="nimos-logo"
  class:has-glow={glow}
  style="gap:{gap}px"
>
  <div class="cubes" style="width:{size}px; height:{size}px">
    <svg viewBox="-15 0 200 185" fill="none" xmlns="http://www.w3.org/2000/svg">
      {#if variant === 'white'}
        <defs>
          <linearGradient id={gradId} x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stop-color="#ffffff" stop-opacity="1"/>
            <stop offset="50%" stop-color="#f5f5f5" stop-opacity="1"/>
            <stop offset="100%" stop-color="#dddddd" stop-opacity="1"/>
          </linearGradient>
        </defs>
        <rect x="5" y="45" width="80" height="80" rx="16" transform="rotate(-30 45 85)" fill="url(#{gradId})"/>
        <rect x="108" y="12" width="60" height="60" rx="10" fill="url(#{gradId})"/>
        <rect x="108" y="98" width="60" height="60" rx="10" fill="url(#{gradId})"/>
      {:else}
        <rect x="5" y="45" width="80" height="80" rx="16" transform="rotate(-30 45 85)" fill="currentColor"/>
        <rect x="108" y="12" width="60" height="60" rx="10" fill="currentColor"/>
        <rect x="108" y="98" width="60" height="60" rx="10" fill="currentColor"/>
      {/if}
    </svg>
  </div>

  {#if showText}
    <div
      class="text"
      style="font-size:{finalTextSize}px; letter-spacing:{Math.round(finalTextSize * -0.04)}px"
    >NimOs</div>
  {/if}
</div>

<style>
  .nimos-logo {
    display: inline-flex;
    align-items: center;
    line-height: 1;
    color: #ffffff;
  }
  .nimos-logo.has-glow {
    filter:
      drop-shadow(0 0 18px rgba(220, 255, 235, 0.35))
      drop-shadow(0 0 4px rgba(255, 255, 255, 0.25));
  }

  .cubes {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .cubes svg {
    width: 100%;
    height: 100%;
    display: block;
  }

  .text {
    font-family: var(--font-sans, 'Inter', system-ui, sans-serif);
    font-weight: 700;
    color: #ffffff;
    line-height: 1;
  }
</style>
