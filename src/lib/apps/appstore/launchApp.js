// launchApp.js — Lógica compartida para ABRIR una app por su URL correcta.
//
// Usado por el Launcher (cajón de apps) y por AppStoreDetail (botón Abrir) ·
// así la decisión "local vs dominio" vive en UN solo sitio (no duplicada).
//
// DOCTRINA (Andrés): el BACKEND compone la info PERSISTENTE (open_url_external
// con subdominio + dominio + puerto HTTPS + landing_path · vía /api/apps/launchable).
// El FRONTEND usa la info CONTEXTUAL (hostname/protocolo por el que entró ahora).

import { getToken } from '$lib/stores/auth.js';

/**
 * resolveLaunchUrl — decide la URL de apertura de una app.
 *
 * @param {object} app  datos del contrato launchable:
 *   · localPort        puerto del container (ej. 8088)
 *   · landingPath      ruta del panel (ej. "/admin")
 *   · openUrlExternal  URL Caddy completa (ej. "https://pihole.dom:444/admin")
 *   · exposed          si la app está expuesta en Network
 * @param {string} hostname  window.location.hostname actual
 * @param {string} protocol  window.location.protocol actual (ej. "https:")
 * @returns {string}  la URL a abrir
 *
 * Regla:
 *   · entré por DOMINIO y la app está expuesta → openUrlExternal (Caddy)
 *   · si no → protocolo + hostname actual + :localPort + landingPath (local)
 */
export function resolveLaunchUrl(app, hostname, protocol) {
  const isLocalHost =
    /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) ||
    hostname.endsWith('.local') ||
    hostname === 'localhost';

  if (!isLocalHost && app.exposed && app.openUrlExternal) {
    return app.openUrlExternal;
  }
  const landing = app.landingPath || '';
  return `${protocol}//${hostname}:${app.localPort}${landing}`;
}

/**
 * fetchLaunchable — carga la lista de apps lanzables del backend.
 * @returns {Promise<Array>}  array de apps con el contrato launchable, o []
 */
export async function fetchLaunchable() {
  try {
    const res = await fetch('/api/apps/launchable', {
      headers: { Authorization: `Bearer ${getToken()}` },
    });
    const data = await res.json();
    const list = Array.isArray(data) ? data : data.apps || [];
    return Array.isArray(list) ? list : [];
  } catch {
    return [];
  }
}

/**
 * normalizeLaunchable — mapea el DTO del backend (snake_case) a las claves que
 * usa el frontend (camelCase). Centraliza el contrato para no repetirlo.
 * @param {object} dto  una entrada del endpoint launchable
 */
export function normalizeLaunchable(dto) {
  return {
    id: dto.id,
    name: dto.name,
    icon: dto.icon || '',
    localPort: dto.local_port,
    landingPath: dto.landing_path || '',
    openUrlExternal: dto.open_url_external || '',
    exposed: dto.exposed || false,
    openMode: dto.open_mode || 'internal',
  };
}

/**
 * openApp — resuelve la URL y la abre en pestaña nueva.
 * @param {object} app  datos launchable (ya normalizados)
 */
export function openApp(app) {
  const url = resolveLaunchUrl(app, window.location.hostname, window.location.protocol);
  window.open(url, '_blank');
}
