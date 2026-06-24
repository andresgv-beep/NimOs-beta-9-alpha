// formatters.js — Helpers de formato y cruces para el módulo AppStore.
//
// Funciones puras, sin side effects. Reusan tipos del módulo y se importan
// desde los componentes Svelte. Mantener este archivo libre de fetch y de
// referencias a Svelte permite testearlo fácil.

/** @typedef {import('./types').CatalogApp} CatalogApp */
/** @typedef {import('./types').InstalledApp} InstalledApp */
/** @typedef {import('./types').AppView} AppView */
/** @typedef {import('./types').Operation} Operation */
/** @typedef {import('./types').InstallStep} InstallStep */
/** @typedef {import('./types').PortBinding} PortBinding */

// ═══════════════════════════════════════════════════════════════════════
// Composición · CatalogApp + InstalledApp → AppView (para cards del grid)
// ═══════════════════════════════════════════════════════════════════════

/**
 * Cruza el catálogo con las apps instaladas para producir el array que la UI
 * pinta. Cada AppView tiene `installed`, `status`, `health` derivados.
 *
 * @param {Array<{id: string, app: CatalogApp}>} catalogEntries
 * @param {InstalledApp[]} installedApps
 * @returns {AppView[]}
 */
export function composeAppViews(catalogEntries, installedApps) {
  const byId = new Map();
  for (const inst of installedApps || []) {
    byId.set(inst.id, inst);
  }
  /** @type {AppView[]} */
  const out = [];
  for (const { id, app } of catalogEntries) {
    const runtime = byId.get(id);
    out.push({
      id,
      name: app.name,
      description: app.description,
      icon: app.icon,
      category: app.category,
      color: app.color,
      installed: !!runtime,
      status: runtime?.status,
      health: runtime?.health,
      catalog: app,
      runtime,
    });
  }
  return out;
}

// ═══════════════════════════════════════════════════════════════════════
// Format helpers · presentación user-facing
// ═══════════════════════════════════════════════════════════════════════

/**
 * Texto user-facing para un status de InstalledApp.
 *
 * @param {string} [status]
 * @returns {string}
 */
export function formatStatus(status) {
  switch (status) {
    case 'running':
      return 'Activa';
    case 'stopped':
      return 'Detenida';
    case 'error':
      return 'Error';
    case 'restarting':
      return 'Reiniciando';
    case 'unknown':
      return 'Desconocido';
    default:
      return status || '—';
  }
}

/**
 * Texto user-facing para un health.
 *
 * @param {string} [health]
 * @returns {string}
 */
export function formatHealth(health) {
  switch (health) {
    case 'healthy':
      return 'OK';
    case 'degraded':
      return 'Degradado';
    case 'failed':
      return 'Fallo';
    case 'starting':
      return 'Iniciando';
    case 'unknown':
      return 'Desconocido';
    default:
      return health || '—';
  }
}

/**
 * "Token" de color para status/health · útil para LEDs/badges.
 * Devuelve uno de: 'ok' | 'warn' | 'crit' | 'info' | 'muted'.
 *
 * @param {string} [status]
 * @param {string} [health]
 * @returns {"ok"|"warn"|"crit"|"info"|"muted"}
 */
export function statusTone(status, health) {
  if (status === 'error' || health === 'failed') return 'crit';
  if (health === 'degraded') return 'warn';
  if (status === 'running' && (health === 'healthy' || !health)) return 'ok';
  if (status === 'restarting' || health === 'starting') return 'info';
  return 'muted';
}

/**
 * Formato de port para tags · ":32400", ":8096/udp" cuando aplica.
 *
 * @param {PortBinding | number | undefined} portOrBinding
 * @returns {string}
 */
export function formatPort(portOrBinding) {
  if (portOrBinding == null) return '';
  if (typeof portOrBinding === 'number') return `:${portOrBinding}`;
  const { host, protocol } = portOrBinding;
  if (host == null) return '';
  return protocol && protocol !== 'tcp' ? `:${host}/${protocol}` : `:${host}`;
}

/**
 * Display name de una categoría.
 *
 * @param {string} slug
 * @param {Object<string, string>} [categoriesMap] Map del catálogo
 * @returns {string}
 */
export function categoryDisplayName(slug, categoriesMap) {
  if (!slug) return '';
  if (categoriesMap && categoriesMap[slug]) return categoriesMap[slug];
  // Fallback: capitalizar el slug
  return slug.charAt(0).toUpperCase() + slug.slice(1);
}

// ═══════════════════════════════════════════════════════════════════════
// Install steps · mapping Operation → InstallStep[]
// ═══════════════════════════════════════════════════════════════════════
//
// Plantilla canónica para el install del Docker engine. Los porcentajes
// coinciden con `updateOpProgressSafe()` del backend (docker.go::
// runDockerInstallWork) tal como quedaron en Fase 2 Batch 3.
//
// Si el backend cambia los porcentajes o mensajes, este archivo es el ÚNICO
// sitio a tocar en el frontend.

/**
 * Steps del install de Docker engine. Mapping fijo backend→UI.
 *
 * @type {Array<{ id: string, label: string, progressAt: number }>}
 */
export const DOCKER_ENGINE_INSTALL_STEPS = [
  { id: 'check-env', label: 'Verificar entorno', progressAt: 0 },
  { id: 'locate-pool', label: 'Localizar pool', progressAt: 10 },
  { id: 'prepare-dirs', label: 'Preparar directorios', progressAt: 20 },
  { id: 'install-engine', label: 'Instalar Docker Engine', progressAt: 30 },
  { id: 'start-service', label: 'Arrancar servicio', progressAt: 80 },
  { id: 'create-share', label: 'Crear share docker-apps', progressAt: 90 },
  { id: 'register', label: 'Registrar servicio', progressAt: 95 },
];

/**
 * Steps del install de una app del catálogo · cuando hacemos `pullImage` async
 * antes del `dockerStackDeploy` síncrono.
 *
 * @type {Array<{ id: string, label: string, progressAt: number }>}
 */
export const APP_INSTALL_STEPS = [
  { id: 'pull-image', label: 'Descargar imagen', progressAt: 0 },
  { id: 'deploy-stack', label: 'Desplegar contenedor', progressAt: 100 }, // sync, marcado al final
];

/**
 * Convierte una Operation en el array de steps con estado done/active/pending.
 *
 * El "active" es el step cuyo `progressAt` es <= operation.progress pero el
 * siguiente step tiene progressAt > operation.progress. Los anteriores son
 * "done", los posteriores son "pending". Si la operation está succeeded,
 * todos son "done".
 *
 * @param {Operation} op
 * @param {Array<{ id: string, label: string, progressAt: number }>} steps
 * @returns {InstallStep[]}
 */
export function operationToSteps(op, steps) {
  if (!op || !Array.isArray(steps) || steps.length === 0) return [];

  const isFailed = op.status === 'failed' || op.status === 'cancelled';
  const isSucceeded = op.status === 'succeeded';

  /** @type {InstallStep[]} */
  const out = [];
  for (let i = 0; i < steps.length; i++) {
    const cur = steps[i];
    const next = steps[i + 1];
    let state;
    if (isSucceeded) {
      state = 'done';
    } else if (isFailed) {
      // Conservar los anteriores como done, el activo como fallido (lo
      // marcamos como 'active' y el componente decide cómo pintarlo · el
      // tipo aquí no incluye 'failed' porque la rama failed se gestiona
      // con un mensaje aparte en la UI).
      if (cur.progressAt <= op.progress && (!next || next.progressAt > op.progress)) {
        state = 'active';
      } else if (cur.progressAt < op.progress) {
        state = 'done';
      } else {
        state = 'pending';
      }
    } else if (op.progress >= (next?.progressAt ?? Infinity)) {
      state = 'done';
    } else if (cur.progressAt <= op.progress) {
      state = 'active';
    } else {
      state = 'pending';
    }
    out.push({
      id: cur.id,
      label: cur.label,
      state,
      // El timing real lo añade la UI según wall-clock si se quiere; el
      // backend no envía duraciones por step.
    });
  }
  return out;
}

// ═══════════════════════════════════════════════════════════════════════
// YAML helpers · parseo muy ligero del campo `compose` del catálogo
// ═══════════════════════════════════════════════════════════════════════

/**
 * Extrae los nombres de servicios de un docker-compose.yml string.
 *
 * NO es un parser de YAML completo · solo identifica entries con indentación
 * "  servicename:" bajo el bloque `services:`. Suficiente para mostrar la
 * lista en "Información técnica" y derivar `isMultiService`.
 *
 * @param {string} composeYaml
 * @returns {string[]}
 */
export function extractComposeServices(composeYaml) {
  if (!composeYaml || typeof composeYaml !== 'string') return [];
  const lines = composeYaml.split('\n');
  let inServices = false;
  /** @type {string[]} */
  const services = [];
  for (const line of lines) {
    if (/^services:\s*$/.test(line)) {
      inServices = true;
      continue;
    }
    if (!inServices) continue;
    if (/^[a-zA-Z]/.test(line)) break; // otro bloque top-level
    const m = line.match(/^ {2}([a-zA-Z][a-zA-Z0-9_-]*):\s*$/);
    if (m) services.push(m[1]);
  }
  return services;
}

/**
 * Resuelve un valor de la app a env var, para apps que usan `passwordKey`:
 *   "{ADMIN_PASSWORD}" → busca app.env.ADMIN_PASSWORD si existe
 *
 * Devuelve el valor literal si no es referencia, o el resuelto si lo es.
 *
 * @param {string} value
 * @param {Object<string,string>} [env]
 * @returns {string}
 */
export function resolveEnvRef(value, env) {
  if (!value || typeof value !== 'string' || !env) return value || '';
  const m = value.match(/^\$\{?([A-Z_][A-Z0-9_]*)\}?$/);
  if (m && env[m[1]] != null) return String(env[m[1]]);
  return value;
}

// ═══════════════════════════════════════════════════════════════════════
// Sidebar · counts derivados para la sección Categorías del mockup 3
// ═══════════════════════════════════════════════════════════════════════

/**
 * Construye los items del sidebar (Biblioteca + Categorías) tal como espera
 * AppShell.sections.
 *
 * Decisión de Fase 1: "Actualizaciones" NO se incluye (acordado · sin
 * detección de updates en backend, mostrar contador sería vaporware).
 *
 * @param {{ total: number, byCategory: Object<string, number> }} counts
 * @param {Object<string, string>} categoriesMap        slug → display name del catálogo
 * @param {number} installedCount
 * @returns {Array<{ label: string, items: Array<{ id: string, label: string, badge?: number }> }>}
 */
/**
 * Construye las secciones del sidebar a partir de counts + estado del usuario.
 *
 * @param {{ total: number, byCategory: Object<string, number> }} counts
 * @param {Object<string, string>} categoriesMap        slug → display name del catálogo
 * @param {number} installedCount
 * @param {number} [updatesCount=0] · sprint Updates · número de apps con update pendiente
 * @returns {Array<{ label: string, items: Array<{ id: string, label: string, badge?: number, iconAfter?: string }> }>}
 */
export function buildSidebarSections(counts, categoriesMap, installedCount, updatesCount = 0) {
  // SVG inline · color via fill explícito. Solo aparece cuando hay updates
  // pendientes (icono azul refresh). Si todo al día, no mostramos nada · más
  // limpio que verde permanente al lado del contador.
  // Tamaño 14px controlado por .sb-icon-after en AppShell.
  const updateIcon = updatesCount > 0
    ? `<svg viewBox="0 0 1024 1024" fill="var(--info)" xmlns="http://www.w3.org/2000/svg">
        <path d="M512 1024C229.23 1024 0 794.77 0 512S229.23 0 512 0s512 229.23 512 512-229.23 512-512 512zm95.731-219.947c62.403-20.276 114.032-58.693 150.859-107.357a15.793 15.793 0 004.801-13.823 15.302 15.302 0 00-.062-.473l-.008-.039a15.75 15.75 0 00-8.378-11.488l-44.709-32.893a15.837 15.837 0 00-13.75-4.587c-.124.014-.249.029-.373.046l-.058.014a15.88 15.88 0 00-11.478 8.236c-25.776 33.881-61.624 60.563-105.31 74.758-113.432 36.856-234.791-24.722-271.525-137.777s25.253-234.206 138.685-271.062c106.335-34.55 218.904 15.82 262.966 115.175l-71.623-.803c-7.187-1.066-14.189 2.885-16.982 9.581a15.743 15.743 0 004.748 18.601l120.91 126.41c2.999 3.135 7.161 4.899 11.51 4.879s8.502-1.823 11.485-4.986L890.37 448.206c5.265-4.193 7.303-11.242 5.082-17.573a20.516 20.516 0 00-.119-.318 15.889 15.889 0 00-.947-2.099l-.031-.073c-3.116-5.729-9.448-8.951-15.937-8.108l-71.678-.494-.892-2.744C753.5 255.685 579.61 167.45 417.952 219.976S167.478 446.095 219.826 607.207c52.348 161.112 226.238 249.347 387.896 196.821l.008.027z"/>
      </svg>`
    : undefined;

  const sections = [
    {
      label: 'Biblioteca',
      items: [
        {
          id: 'installed',
          label: 'Instaladas',
          badge: installedCount,
          // iconAfter · sprint Updates · icono renderizado DESPUÉS del badge.
          // Solo aparece si hay updates pendientes (sino, sidebar limpio).
          iconAfter: updateIcon,
        },
      ],
    },
  ];

  // Categorías · "Todas" arriba, resto ordenadas por count desc.
  const catItems = [{ id: 'cat-all', label: 'Todas', badge: counts.total }];

  const sortedCats = Object.entries(counts.byCategory || {})
    .sort((a, b) => b[1] - a[1]);
  for (const [slug, count] of sortedCats) {
    catItems.push({
      id: `cat-${slug}`,
      label: categoriesMap?.[slug] || slug,
      badge: count,
    });
  }
  sections.push({ label: 'Categorías', items: catItems });

  return sections;
}
