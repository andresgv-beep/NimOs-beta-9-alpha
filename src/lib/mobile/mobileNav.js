// mobileNav.js — Estado de navegación de la UI móvil.
//
// Una sola fuente de verdad para qué sección está activa. MobileShell la lee
// para decidir qué componente de sección renderizar, y MobileTabBar la escribe.
// Mantenerlo en un store (y no en props) permite que cualquier sección pueda
// navegar a otra (p.ej. una acción de Inicio que lleve a Apps) sin prop-drilling.

import { writable } from 'svelte/store';

// Secciones disponibles. El orden define el de la tab bar.
export const MOBILE_SECTIONS = ['home', 'apps', 'files', 'system'];

export const activeMobileSection = writable('home');

// Petición de apertura de un share desde fuera de la sección Archivos
// (p.ej. tocar una carpeta en Inicio). MobileFiles la observa y, si trae
// un share, navega directamente a él. Se limpia tras consumirla.
export const pendingFileShare = writable(null);

export function goToSection(section) {
  if (MOBILE_SECTIONS.includes(section)) {
    activeMobileSection.set(section);
  }
}

// Abre la sección Archivos directamente dentro de un share concreto.
export function openShareInFiles(shareName) {
  pendingFileShare.set(shareName);
  activeMobileSection.set('files');
}
