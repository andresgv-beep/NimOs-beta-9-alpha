// catalog.js — Cliente del catálogo de apps (repo NimOs-appstore en GitHub).
//
// El catálogo NO es servido por el daemon NimOS. Vive como JSON estático en
// raw.githubusercontent.com y el frontend lo descarga directamente desde el
// navegador. El daemon solo tiene los CSP headers en static.go que permiten
// el origen (img-src + connect-src).
//
// Decisión arquitectónica · "single source of truth":
//   - Si en el futuro hace falta caché en backend (e.g. rate limits, offline),
//     se añade un proxy en el daemon. Hoy no aplica.
//   - El frontend cachea en memoria durante la sesión + Cache-Control del browser.
//   - Versión del catálogo (campo `version` del JSON) permite invalidar caché
//     si el shape del catálogo cambia.
//
// /** @typedef {import('./types').Catalog} Catalog */
// /** @typedef {import('./types').CatalogApp} CatalogApp */

const CATALOG_URL =
  'https://raw.githubusercontent.com/andresgv-beep/NimOs-appstore/main/catalog.json';

// Cache en memoria (vive lo que dure la página).
// Si necesitas invalidar manualmente: fetchCatalog({ force: true }).
let _cache = null;
let _cacheTime = 0;
const CACHE_TTL_MS = 5 * 60 * 1000; // 5 minutos

/**
 * Descarga el catálogo desde GitHub raw.
 *
 * Estrategia:
 *  1. Si hay cache fresca (< 5min) y no se pide force, devolver cache.
 *  2. Fetch al raw URL. Si OK → actualizar cache, devolver.
 *  3. Si fetch falla y hay cache antigua → devolver cache antigua + log warning.
 *  4. Si fetch falla y no hay cache → lanzar error.
 *
 * @param {Object} [opts]
 * @param {boolean} [opts.force]  Saltarse la cache aunque esté fresca
 * @param {AbortSignal} [opts.signal]  Para cancelar
 * @returns {Promise<Catalog>}
 */
export async function fetchCatalog({ force = false, signal } = {}) {
  const now = Date.now();
  if (!force && _cache && now - _cacheTime < CACHE_TTL_MS) {
    return _cache;
  }

  try {
    const res = await fetch(CATALOG_URL, {
      signal,
      // No mandar credentials · es un GET público a GitHub
      credentials: 'omit',
    });
    if (!res.ok) {
      throw new Error(`catalog fetch failed: HTTP ${res.status}`);
    }
    const data = await res.json();
    if (!data || typeof data !== 'object' || !data.apps) {
      throw new Error('catalog response invalid (no apps key)');
    }
    _cache = data;
    _cacheTime = now;
    return data;
  } catch (err) {
    if (_cache) {
      console.warn(
        '[appstore/catalog] fetch failed, using stale cache:',
        err.message
      );
      return _cache;
    }
    throw err;
  }
}

/**
 * Versión sincrónica · devuelve la cache si existe, null si no se ha hecho fetch.
 * Útil para componentes que ya saben que `fetchCatalog()` se llamó antes
 * (e.g. desde un padre que hizo await).
 *
 * @returns {Catalog | null}
 */
export function getCachedCatalog() {
  return _cache;
}

/**
 * Obtiene una app específica del catálogo.
 *
 * @param {string} appId
 * @param {Catalog} [catalog]  Opcional · si lo pasas, no hace fetch
 * @returns {Promise<CatalogApp | null>}
 */
export async function getCatalogApp(appId, catalog) {
  const cat = catalog || (await fetchCatalog());
  return cat.apps[appId] || null;
}

/**
 * appSupportsArch — ¿la app es compatible con la arquitectura del sistema?
 *
 * El catálogo puede declarar `arch` (array, ej. ["amd64"] o ["amd64","arm64"]).
 * Si la app NO declara `arch`, se asume compatible con todo (compat hacia atrás:
 * las apps existentes sin el campo siguen apareciendo en cualquier arquitectura).
 *
 * @param {object} app   la entrada del catálogo
 * @param {string} arch  la arquitectura del sistema ("amd64"|"arm64"|...)
 * @returns {boolean}    true si es compatible (o si no declara arch, o si no
 *   sabemos la arquitectura del sistema · en la duda, mostrar)
 */
export function appSupportsArch(app, arch) {
  if (!app || !Array.isArray(app.arch) || app.arch.length === 0) {
    return true; // sin campo arch → multi-arch (compat hacia atrás)
  }
  if (!arch) {
    return true; // no sabemos la arch del sistema → no ocultar (precaución)
  }
  return app.arch.includes(arch);
}

/**
 * Lista de apps de una categoría.
 *
 * Si categorySlug es "all" o vacío, devuelve TODAS.
 * Si se pasa `arch`, oculta las apps incompatibles con esa arquitectura.
 *
 * @param {string} categorySlug
 * @param {Catalog} [catalog]
 * @param {string} [arch]  arquitectura del sistema · oculta las incompatibles
 * @returns {Promise<Array<{id: string, app: CatalogApp}>>}
 */
export async function listCatalogApps(categorySlug, catalog, arch) {
  const cat = catalog || (await fetchCatalog());
  const entries = Object.entries(cat.apps);
  const filter = categorySlug && categorySlug !== 'all';

  /** @type {Array<{id: string, app: CatalogApp}>} */
  const out = [];
  for (const [id, app] of entries) {
    if (app.isSystem) continue; // docker-engine se gestiona aparte
    if (filter && app.category !== categorySlug) continue;
    if (!appSupportsArch(app, arch)) continue; // ocultar incompatibles con la arch
    out.push({ id, app });
  }
  // Sort por nombre · case-insensitive
  out.sort((a, b) =>
    a.app.name.localeCompare(b.app.name, undefined, { sensitivity: 'base' })
  );
  return out;
}

/**
 * Counts por categoría. Útil para los badges del sidebar (mockup 3).
 *
 * @param {Catalog} catalog
 * @returns {{ total: number, byCategory: Object<string, number> }}
 */
export function countByCategory(catalog) {
  /** @type {Object<string, number>} */
  const byCategory = {};
  let total = 0;
  for (const [, app] of Object.entries(catalog.apps)) {
    if (app.isSystem) continue;
    total++;
    byCategory[app.category] = (byCategory[app.category] || 0) + 1;
  }
  return { total, byCategory };
}

/**
 * Resolver el URL del icono. Si el catálogo trae icon absoluto, lo devuelve.
 * Si trae un path relativo, lo prefija con el repo raw.
 *
 * En la práctica todas las apps del catálogo tienen icon absoluto, pero
 * este helper protege de futuros cambios.
 *
 * @param {CatalogApp} app
 * @returns {string}
 */
export function iconUrl(app) {
  if (!app || !app.icon) return '';
  if (app.icon.startsWith('http://') || app.icon.startsWith('https://')) {
    return app.icon;
  }
  // Relativo · prefijar con repo raw
  const cleaned = app.icon.replace(/^\.?\//, '');
  return `https://raw.githubusercontent.com/andresgv-beep/NimOs-appstore/main/${cleaned}`;
}

/**
 * ¿Esta app es multi-servicio? Heurística simple: el campo `compose` contiene
 * más de un servicio definido al nivel `services:` del YAML.
 *
 * Esto NO parsea YAML — solo cuenta líneas que matchean el patrón de service
 * declaration. Es suficiente para mostrar el tag "Multi-servicio" del mockup.
 *
 * @param {CatalogApp} app
 * @returns {boolean}
 */
export function isMultiService(app) {
  if (!app || !app.compose) return false;
  const compose = app.compose;
  // Buscar el bloque `services:` y contar entries con indentación de 2 espacios
  // seguidos por nombre y `:`. Patrón muy permisivo, suficiente para el tag.
  const lines = compose.split('\n');
  let inServices = false;
  let count = 0;
  for (const line of lines) {
    if (line.match(/^services:\s*$/)) {
      inServices = true;
      continue;
    }
    if (!inServices) continue;
    // Otro bloque top-level rompe services:
    if (line.match(/^[a-zA-Z]/)) break;
    // Servicio dentro de services: 2 espacios + identificador + ':'
    if (line.match(/^ {2}[a-zA-Z][a-zA-Z0-9_-]*:\s*$/)) {
      count++;
      if (count > 1) return true;
    }
  }
  return false;
}

// ─────────────────────────────────────────────────────────────────────────
// Config de apps (sistema de aprovisionamiento) · archivos separados
// ─────────────────────────────────────────────────────────────────────────
//
// Para no hinchar catalog.json, la config de cada app (configFields,
// postInstall) vive en un archivo aparte bajo main/configs/<archivo>.json.
// El catálogo solo lleva una referencia: "configRef": "configs/vscode.config.json".
// Las apps simples (Jellyfin) no tienen configRef · no cargan nada.

const CONFIG_BASE_URL =
  'https://raw.githubusercontent.com/andresgv-beep/NimOs-appstore/main/';

// Cache de configs por ref (viven lo que dure la página).
const _configCache = new Map();

/**
 * Carga la config de una app desde su archivo separado (configRef).
 *
 * @param {string} configRef  Ruta relativa en el repo, ej. "configs/vscode.config.json".
 * @param {Object} [opts]
 * @param {AbortSignal} [opts.signal]
 * @returns {Promise<object|null>}  El objeto de config (configVersion,
 *   configFields, postInstall...) o null si no hay ref / falla la carga.
 */
export async function loadAppConfig(configRef, { signal } = {}) {
  if (!configRef || typeof configRef !== 'string') return null;

  if (_configCache.has(configRef)) {
    return _configCache.get(configRef);
  }

  const url = CONFIG_BASE_URL + configRef.replace(/^\/+/, '');
  try {
    const res = await fetch(url, { signal, credentials: 'omit' });
    if (!res.ok) {
      console.warn(`[appstore] config fetch failed para ${configRef}: HTTP ${res.status}`);
      return null;
    }
    const data = await res.json();
    if (!data || typeof data !== 'object') {
      console.warn(`[appstore] config inválida para ${configRef}`);
      return null;
    }
    _configCache.set(configRef, data);
    return data;
  } catch (err) {
    // Cancelación legítima · no es un error que reportar.
    if (err?.name === 'AbortError') return null;
    console.warn(`[appstore] no se pudo cargar config ${configRef}:`, err);
    return null;
  }
}
