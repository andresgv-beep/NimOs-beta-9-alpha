# `src/lib/apps/appstore/` · Módulo frontend del AppStore

NimOS Beta 8.1.x · construido tras el cierre de Fase 2 del audit de backend.

---

## Estructura

| Archivo | Responsabilidad |
|---|---|
| `types.js` | JSDoc typedefs · contratos de catálogo, backend, operations, UI |
| `catalog.js` | Cliente del catálogo (fetch a raw.githubusercontent.com) + cache |
| `api.js` | Cliente HTTP del backend NimOS (`/api/services`, `/api/docker/*`, `/api/operations/*`) |
| `formatters.js` | Helpers de presentación + cruces catálogo ↔ instaladas + mapping de install steps |
| `*.svelte` | Componentes UI (Fases 2-6 · pendientes en Fase 1) |
| `views-styles.css` | Estilos locales del módulo (pendiente) |

## Quién llama a qué

```
AppStore.svelte (entry)
  ├── api.js::getCapabilities()  ──→  decide qué pantalla mostrar
  │
  ├── AppStoreSetup.svelte               (Fase 2 · mockups 1 y 2)
  │     └── api.js::installDockerEngine(req, { async: true })
  │           └── api.js::waitForOperation(opId, onProgress)
  │
  ├── AppStoreOverview.svelte            (Fase 3 · mockup 3)
  │     ├── catalog.js::fetchCatalog()
  │     ├── api.js::getInstalledApps()
  │     ├── formatters.js::composeAppViews(catalog, installed)
  │     └── AppCard.svelte
  │
  └── AppStoreDetail.svelte              (Fase 4 · mockup 4)
        ├── catalog.js::getCatalogApp(id)
        ├── api.js::installApp({ id, compose, ... })
        ├── api.js::pullImage(image, { async: true })  ← progress visible
        ├── api.js::uninstallApp(id, type)
        └── InstallFlow.svelte           (Fase 5 · mockup 4 · sección steps)
              └── api.js::waitForOperation + formatters.js::operationToSteps
```

## Patrón de cliente API · convención del proyecto

Replicado de `apps/storage/api.js`:
- Constante `BASE` opcional · aquí no se usa porque hay 4 prefijos distintos (`/api/services`, `/api/docker/`, `/api/operations/`, `/api/docker/pull/`).
- Helper `unwrap()` interno · normaliza respuesta v2 y errores.
- Funciones nombradas con JSDoc explícito (tipos importados de `types.js`).
- Errores se lanzan como `Error` con `.code`, `.status` y `.details`.

## Patrón de cache · catálogo

`catalog.js` cachea en memoria (TTL 5min). En caso de fallo de fetch usa cache antigua si existe (warn en consola). No persiste en localStorage porque el catálogo cambia raramente y la sesión típica de NimOS dura horas, no días.

## Endpoints async vs sync · QUÉ TIENE Y QUÉ NO

| Operación | Endpoint | Async |
|---|---|---|
| Install Docker engine | POST `/api/docker/install` | ✅ `?async=true` |
| Pull imagen | GET `/api/docker/pull/:image` | ✅ `?async=true` |
| Install app (stack) | POST `/api/docker/stack` | ❌ sync |
| Install container | POST `/api/docker/container` | ❌ sync |
| Uninstall stack | DELETE `/api/docker/stack/:id` | ❌ sync · pero el cleanup real ya es async en backend (APP-031) |
| Uninstall container | DELETE `/api/docker/container/:id` | ❌ sync · idem |
| Action (start/stop/restart) | POST `/api/docker/container/:id/:action` | ❌ sync |

**Patrón recomendado para install de apps grandes** (Plex 410MB, Immich 1.2GB...):

```js
// 1. Pull async · progress visible al user
const pull = await pullImage(app.image, { async: true });
await waitForOperation(pull.operationId, (op) => updateUI(op));

// 2. Stack deploy sync · rápido porque la imagen ya está local
await installApp({ id, compose: app.compose, name: app.name, ... });
```

Mejora futura: si el backend gana `?async=true` en `dockerStackDeploy`,
unificar a un solo flujo async. Backlog del audit.

## Limitaciones conocidas (heredadas del backend)

- **Sin detección de updates**: no hay endpoint para comparar imagen instalada vs `:latest` del registry. Decisión tomada · "Actualizaciones" no aparece en el sidebar.
- **Sin versión visible**: el catálogo no trae `version` por app. El campo es opcional en typedefs por si el catálogo gana este campo en futuro.
- **Sin progress detallado en stack deploy**: el backend hace `docker compose up -d` opaco. El "InstallFlow" de Fase 5 usa el patrón pull→deploy para tener al menos el progress del pull.
- **APP-001-B reservado**: rebuild de containers individuales devuelve 501. Solo stacks.

## Tipos JSDoc · uso

```js
// Importar en otro archivo
/** @typedef {import('./types').CatalogApp} CatalogApp */

/**
 * @param {CatalogApp} app
 * @returns {string}
 */
export function someHelper(app) { ... }
```

VS Code reconoce los typedefs y da autocompletado sin necesidad de TypeScript.

## Coordinación con backend

Las funciones de `api.js` son el ÚNICO sitio del frontend que conoce los paths HTTP del backend. Si un endpoint cambia (e.g. cuando se cierre la deprecación de `/api/installed-apps` o se promueva `/api/docker/install` a `/api/appstore/install`), este archivo es el punto único de actualización.

Los typedefs en `types.js` reflejan el shape actual del backend tras Fase 2 del audit. Si el backend cambia un campo, el cambio aquí propaga errores TypeScript-style en VS Code para todos los consumidores.
