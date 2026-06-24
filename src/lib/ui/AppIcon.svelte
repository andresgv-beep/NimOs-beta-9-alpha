<script>
  /**
   * AppIcon · Icono de app adaptativo (Beta 8.1)
   * ──────────────────────────────────────────────
   * Renderiza el icono SVG correspondiente al tema actual.
   *
   * Acepta `src` en dos formatos:
   *
   *   1. Nombre lógico (Beta 8.1+): "storage", "files", "network", ...
   *      → resuelve a /icons/<theme>/<name>.svg
   *      → cambia automáticamente al cambiar el tema
   *
   *   2. Ruta directa (retrocompat): "/icons/storage.png", "/foo/bar.svg"
   *      → se usa tal cual (apps Docker custom, paths absolutos, etc.)
   *
   * Uso:
   *   <AppIcon src="storage" alt="Storage" />
   *   <AppIcon src="/icons/custom.png" alt="Custom" />
   *   <AppIcon src="nimhealth" alt="NimHealth" size="lg" active />
   *
   * Tamaños:
   *   xs ·  32px · Tablas / filas densas
   *   sm ·  36px · Taskbar
   *   md ·  52px · Launcher
   *   lg ·  80px · Launcher grande
   */
  import { currentTheme } from '$lib/stores/theme.js';

  export let src = '';
  export let alt = '';
  /** xs | sm | md | lg */
  export let size = 'md';
  /** Estado activo (app abierta/seleccionada) · se mantiene por compat, efecto visual sutil */
  export let active = false;
  /** Fallback: letra o glyph para mostrar si src falla o no se provee */
  export let fallback = '';

  let imgFailed = false;
  function handleError() { imgFailed = true; }

  // Mapa de tema → carpeta de iconos.
  // dark   → /icons/dark/
  // cream  → /icons/light/  (cream usa los SVG light)
  // futuro → /icons/<theme>/ si se añaden más temas
  function themeFolder(theme) {
    if (theme === 'cream' || theme === 'light') return 'light';
    return 'dark';
  }

  // Resuelve la ruta final del icono.
  // · Si src empieza con '/' o 'http' → ruta directa (retrocompat)
  // · Si src es un identificador simple → /icons/<theme>/<src>.svg
  $: resolvedSrc = (() => {
    if (!src) return '';
    if (src.startsWith('/') || src.startsWith('http')) return src;
    const folder = themeFolder($currentTheme);
    return `/icons/${folder}/${src}.svg`;
  })();

  // Cuando cambia el tema, reseteamos el flag de fallo
  // para reintentar cargar el nuevo SVG.
  $: if (resolvedSrc) imgFailed = false;
</script>

<div class="app-icon-frame size-{size}" class:active>
  {#if resolvedSrc && !imgFailed}
    <img class="app-icon-img" src={resolvedSrc} {alt} on:error={handleError} draggable="false" />
  {:else}
    <div class="app-icon-fallback" aria-label={alt}>
      {fallback || (alt ? alt.charAt(0).toUpperCase() : '◆')}
    </div>
  {/if}
</div>

<style>
  .app-icon-frame {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: transform 0.1s;
    flex-shrink: 0;
    line-height: 0; /* evita espacio fantasma debajo del img */
  }

  .app-icon-img {
    width: 100%;
    height: 100%;
    object-fit: contain;
    display: block;
  }

  .app-icon-fallback {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-sans);
    font-weight: 600;
    color: var(--accent);
    background: rgba(255, 255, 255, 0.06);
    border-radius: var(--radius-md);
    text-transform: uppercase;
  }

  /* ─── TAMAÑOS (standard de UI moderna) ─── */
  /* xs · 32px · Tablas, filas densas, NimHealth rows */
  .size-xs { width: 2rem; height: 2rem; }
  .size-xs .app-icon-fallback { font-size: var(--fs-12); border-radius: var(--radius-sm); }

  /* sm · 36px · Taskbar (standard Windows 10 / KDE / GNOME) */
  .size-sm { width: 2.25rem; height: 2.25rem; }
  .size-sm .app-icon-fallback { font-size: var(--fs-14); border-radius: var(--radius-md); }

  /* md · 48px · Launcher (standard Windows 11 / KDE / ChromeOS) */
  .size-md { width: 3rem; height: 3rem; }
  .size-md .app-icon-fallback { font-size: 1.25rem; border-radius: var(--bev-md); }

  /* lg · 80px · Header de detalle (NimHealth selected service, etc.) */
  .size-lg { width: 5rem; height: 5rem; }
  .size-lg .app-icon-fallback { font-size: 1.75rem; border-radius: var(--radius-lg); }
</style>
