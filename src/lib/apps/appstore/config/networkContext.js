/**
 * networkContext.js — Carga los datos de dominio desde el módulo Network.
 *
 * Cuando una app declara un campo auto:domain (ej. Matrix SERVER_NAME), el modal
 * de config necesita el dominio base y el puerto HTTPS reales (los que el usuario
 * configuró en Network), para pre-rellenar el campo SIN que el usuario los
 * escriba a mano.
 *
 * REUSA las APIs que Network ya expone (no reinventa):
 *   · /api/v4/network/ddns           → lista de dominios DDNS configurados
 *   · /api/v4/network/exposure/config → { base_domain, https_port }
 *
 * NO se llama para apps sin auto:domain (ver needsDomainContext en configSchema).
 */

import { hdrs } from '$lib/stores/auth.js';

const NET_BASE = '/api/v4/network';

/**
 * Carga el contexto de dominio para el modal de config.
 *
 * @returns {Promise<{ domains: string[], baseDomain: string, httpsPort: number }>}
 *   · domains: lista de DNS configurados (para el selector si hay varios)
 *   · baseDomain: el dominio base actual de exposición (o el único DDNS)
 *   · httpsPort: el puerto HTTPS de Caddy (ej. 444)
 * Si algo falla, devuelve valores vacíos/por defecto · el modal degrada a campo
 * manual (el usuario podrá escribir el dominio igual que antes).
 */
export async function loadDomainContext() {
  const result = { domains: [], baseDomain: '', httpsPort: 443 };

  // 1. Config de exposición · base_domain + https_port reales.
  try {
    const res = await fetch(`${NET_BASE}/exposure/config`, { headers: hdrs() });
    if (res.ok) {
      const body = await res.json();
      const config = body?.config || body?.data?.config || {};
      if (config.base_domain) result.baseDomain = config.base_domain;
      if (config.https_port) result.httpsPort = config.https_port;
    }
  } catch {
    // sin config · degradamos a manual
  }

  // 2. Lista de DDNS · para el selector (tu idea) si hay más de uno.
  try {
    const res = await fetch(`${NET_BASE}/ddns`, { headers: hdrs() });
    if (res.ok) {
      const data = await res.json();
      result.domains = (data?.ddns || data?.data?.ddns || [])
        .map((d) => d.domain)
        .filter(Boolean);
    }
  } catch {
    // sin DDNS · el selector quedará vacío, se usa baseDomain o manual
  }

  // Si no hay baseDomain configurado pero hay un único DDNS, usarlo (igual que
  // hace NetworkExposure · autoselección).
  if (!result.baseDomain && result.domains.length === 1) {
    result.baseDomain = result.domains[0];
  }

  return result;
}

/**
 * autoExposeApp — registra una app en Network tras instalarla (Paso 3).
 *
 * Reusa el endpoint de exposición de Network (el mismo que el modal manual).
 * Network emite el cert TLS y configura Caddy · la app aparece en la lista de
 * apps expuestas con las demás.
 *
 * NO revierte la instalación si falla · la app queda instalada (funciona en
 * local) aunque la exposición falle. Network ya avisa con su cartel.
 *
 * @param {object} opts
 *   · appId       id de la app (ej. "matrix-synapse")
 *   · displayName nombre visible
 *   · subdomain   subdominio (ej. "matrix") · de extractSubdomain
 *   · upstreamPort puerto del container (ej. 8008)
 * @returns {Promise<{ok: boolean, error?: string}>}
 */
export async function autoExposeApp({ appId, displayName, subdomain, upstreamPort }) {
  if (!appId || !subdomain || !upstreamPort) {
    return { ok: false, error: 'faltan datos para exponer (appId/subdomain/puerto)' };
  }
  try {
    const res = await fetch(`${NET_BASE}/exposure`, {
      method: 'POST',
      headers: { ...hdrs(), 'Content-Type': 'application/json' },
      body: JSON.stringify({
        app_id: appId,
        display_name: displayName || appId,
        subdomain: subdomain,
        path: '',
        upstream_host: '127.0.0.1',
        upstream_port: upstreamPort,
        enabled: true,
      }),
    });
    if (!res.ok) {
      return { ok: false, error: `HTTP ${res.status}` };
    }
    return { ok: true };
  } catch (e) {
    return { ok: false, error: String(e?.message || e) };
  }
}
