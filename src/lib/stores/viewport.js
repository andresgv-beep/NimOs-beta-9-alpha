// viewport.js — Detección de viewport para decidir UI escritorio vs móvil.
//
// NimOS escritorio es un gestor de ventanas (drag/resize/dock) que no tiene
// sentido en un móvil. En su lugar, cuando el viewport es estrecho servimos
// MobileShell: una UI táctil a pantalla completa con navegación por tabs.
//
// La bifurcación se hace en +page.svelte leyendo `isMobile`. El escritorio
// no depende de este store, así que si el móvil fallara, el escritorio sigue.

import { readable } from 'svelte/store';

// Umbral: dimensión menor de pantalla por debajo de la cual tratamos
// el dispositivo como móvil (tablet de 7-8" incluida).
const MOBILE_BREAKPOINT = 768;

// ¿Es un dispositivo móvil REAL? · jun 2026
// ────────────────────────────────────────
// NO usamos window.innerWidth: encoge con el zoom del navegador/SO, así
// que hacer zoom en un escritorio lo reclasificaba como móvil (bug).
// Criterio fiel a la intención ("UI táctil"):
//   - puntero COARSE (táctil primario) → un escritorio con ratón nunca
//     entra, por mucho zoom que metas.
//   - screen.* (no innerWidth) → inmune al zoom del navegador.
//   - min(ancho, alto) → robusto a la orientación (un móvil es pequeño
//     siempre, también en horizontal).
// Único dueño de este hecho: lo consumen viewport (shell), theme.js
// (suprimir zoom en móvil) y app.html (pre-paint, copia con cross-ref).
export function isMobileDevice() {
  if (typeof window === 'undefined') return false;
  const coarse = window.matchMedia?.('(pointer: coarse)')?.matches ?? false;
  const s = window.screen;
  const screenMin = Math.min(s?.width ?? window.innerWidth, s?.height ?? window.innerHeight);
  return coarse && screenMin < MOBILE_BREAKPOINT;
}

function detect() {
  return isMobileDevice();
}

// Resuelve una CSS var de longitud a PÍXELES REALES · jun 2026
// ───────────────────────────────────────────────────────────
// getComputedStyle sobre una custom property devuelve el TOKEN tal cual
// ("3.25rem"), sin resolver la unidad — `parseInt` daría 3, no 52. Desde
// que la escala vive en el font-size raíz (Beta 9), --taskbar-height está
// en rem, así que el JS que la lee (windows.js) necesita convertirla a px
// multiplicando por el font-size raíz. Acepta rem o px.
export function cssVarPx(name, fallback = 0) {
  if (typeof document === 'undefined') return fallback;
  const root = document.documentElement;
  const raw = getComputedStyle(root).getPropertyValue(name).trim();
  if (!raw) return fallback;
  const n = parseFloat(raw);
  if (Number.isNaN(n)) return fallback;
  if (raw.endsWith('rem')) {
    const rootPx = parseFloat(getComputedStyle(root).fontSize) || 16;
    return n * rootPx;
  }
  return n; // px (o sin unidad → asumimos px)
}

// Store reactivo: se actualiza al rotar/redimensionar.
export const isMobile = readable(detect(), (set) => {
  if (typeof window === 'undefined') return;

  const update = () => set(detect());
  update();

  window.addEventListener('resize', update);
  window.addEventListener('orientationchange', update);

  return () => {
    window.removeEventListener('resize', update);
    window.removeEventListener('orientationchange', update);
  };
});

export { MOBILE_BREAKPOINT };
