// types.js — JSDoc typedefs del módulo AppStore.
//
// El proyecto NimOS Beta 8.1 es JavaScript puro (no TypeScript). Usamos
// JSDoc para documentar shapes y obtener autocompletado en VS Code sin
// añadir un step de build de TS.
//
// Los typedefs aquí definidos se importan en otros archivos del módulo
// con `/** @typedef {import('./types').CatalogApp} CatalogApp */` y luego
// se usan como tipos en JSDoc de funciones.
//
// IMPORTANTE: si el contrato del backend cambia (e.g. backend audit cierra
// items que mutan el shape de ServiceBase), este archivo es el ÚNICO sitio
// a tocar. El resto del módulo importa de aquí.

// ═══════════════════════════════════════════════════════════════════════
// CATÁLOGO · shapes del repo nimbusos-appstore (raw.githubusercontent.com)
// ═══════════════════════════════════════════════════════════════════════

/**
 * Una app individual del catálogo, tal como aparece en catalog.json.
 *
 * @typedef {Object} CatalogApp
 * @property {string} name                      Display name ("Plex", "Jellyfin")
 * @property {string} description               Descripción para listado/detalle
 * @property {string} icon                      URL absoluta del SVG en GitHub raw
 * @property {string} category                  Slug de categoría ("media", "cloud", ...)
 * @property {number} [port]                    Puerto principal expuesto
 * @property {string} image                     Imagen Docker (ej "jellyfin/jellyfin:latest")
 * @property {boolean} [official]               Marca de catálogo oficial
 * @property {string} [compose]                 docker-compose.yml completo como string
 * @property {string} [openMode]                "internal" | "external"
 * @property {boolean} [isSystem]               true para docker-engine (no es app instalable normal)
 * @property {boolean} [requiresPool]           true si necesita pool montado
 * @property {Object<string,string>} [volumes]  config/cache/etc → mount paths internos
 * @property {string[]} [mediaVolumes]          Mount paths que el user puede asociar a shares
 * @property {Object<string,string>} [env]      Variables de entorno
 * @property {AppCredentials} [credentials]     Usuario/contraseña por defecto si aplica
 * @property {string} [color]                   Hex color de marca
 * @property {string} [version]                 Versión opcional (no siempre presente)
 */

/**
 * Credenciales por defecto de una app (cuando aplica).
 *
 * @typedef {Object} AppCredentials
 * @property {string} [username]      Usuario por defecto (filebrowser, grafana...)
 * @property {string} [password]      Contraseña hardcoded (apps que la traen fija)
 * @property {string} [passwordKey]   Nombre de la variable de entorno que la contiene
 *                                    (apps que generan password al primer arranque)
 */

/**
 * El catálogo completo descargado del repo.
 *
 * @typedef {Object} Catalog
 * @property {number} version                          Schema version del catálogo (no de las apps)
 * @property {string} updated                          ISO date del último update
 * @property {Object<string,string>} categories        Map slug → display name ("media" → "Multimedia")
 * @property {Object<string,CatalogApp>} apps          Map id → CatalogApp
 */

// ═══════════════════════════════════════════════════════════════════════
// BACKEND · shapes de /api/services y endpoints docker
// ═══════════════════════════════════════════════════════════════════════

/**
 * Una app Docker instalada, tal como la devuelve `/api/services` con type="docker-app".
 *
 * Espejo del struct DockerAppStatus del backend (daemon/models.go).
 *
 * @typedef {Object} InstalledApp
 * @property {string} id                            App ID ("jellyfin", "plex")
 * @property {string} type                          Siempre "docker-app"
 * @property {string} parent                        Service ID del Docker engine padre
 * @property {string} name                          Display name
 * @property {string} status                        "running" | "stopped" | "error" | "unknown"
 * @property {string} health                        "healthy" | "degraded" | "failed" | "unknown"
 * @property {string} [image]                       Imagen Docker
 * @property {string} [icon]                        URL del icono (cacheado o catálogo)
 * @property {string} [containerName]               Nombre del container real (incluye sufijos de stack)
 * @property {string} [openMode]                    "internal" | "external"
 * @property {string} [uptime]                      "3 hours", "2 days" — parseado del Status raw
 * @property {PortBinding[]} [ports]                Multi-port (APP-033) · canonical
 */

/**
 * Binding de un puerto del container.
 *
 * @typedef {Object} PortBinding
 * @property {number} host                          Puerto host (mapeado, donde el cliente conecta)
 * @property {number} declared                      Puerto interno del container
 * @property {string} [protocol]                    "tcp" | "udp" (default "tcp")
 */

/**
 * Status del Docker engine en /api/services.
 *
 * @typedef {Object} DockerEngineStatus
 * @property {string} id                            "containers" (AppID del engine)
 * @property {string} type                          "docker-engine"
 * @property {string} status                        "running" | "stopped" | "unknown" | "not-installed"
 * @property {string} health                        Health agregado de sus children
 * @property {InstalledApp[]} [children]            Apps Docker bajo este engine
 * @property {number} [orphanCount]                 Containers sin app registrada
 */

/**
 * Capacidades del sistema · derivadas de /api/services para decidir qué pantalla mostrar.
 *
 * @typedef {Object} AppStoreCapabilities
 * @property {boolean} hasPool                      ¿Hay al menos un pool BTRFS montado?
 * @property {boolean} dockerInstalled              ¿Docker engine instalado?
 * @property {boolean} dockerRunning                ¿Docker engine corriendo ahora?
 * @property {boolean} hasPermission                ¿La session actual puede gestionar Docker?
 */

// ═══════════════════════════════════════════════════════════════════════
// OPERATIONS · async ops del backend (Fase 2 Batch 2)
// ═══════════════════════════════════════════════════════════════════════

/**
 * Operación async tracked en nimos_operations.
 *
 * Espejo del struct DBOperation.ToMap() del backend.
 *
 * @typedef {Object} Operation
 * @property {string} id                            "op_<unix>_<8hex>"
 * @property {string} type                          "docker.install" | "docker.pull"
 * @property {string} status                        "pending" | "running" | "succeeded" | "failed" | "cancelled"
 * @property {number} progress                      0..100
 * @property {string} [message]                     Descripción del paso actual
 * @property {string} createdAt                     ISO timestamp
 * @property {string} [startedAt]                   ISO · vacío si nunca llegó a running
 * @property {string} [finishedAt]                  ISO · vacío si no terminó
 * @property {string} [error]                       Mensaje si status="failed"
 * @property {string} [resultRaw]                   JSON string del resultado (succeeded only)
 */

// ═══════════════════════════════════════════════════════════════════════
// REQUEST BODIES · payloads que enviamos al backend
// ═══════════════════════════════════════════════════════════════════════

/**
 * Body para POST /api/docker/install (instalar Docker engine).
 *
 * @typedef {Object} InstallEngineRequest
 * @property {string} pool                          Nombre del pool destino
 * @property {string[]} [permissions]               Usernames con permiso para Docker
 */

/**
 * Body para POST /api/docker/stack (deploy de app desde catálogo).
 *
 * IMPORTANTE: el backend acepta `external: bool`, NO `openMode: string`.
 * Internamente convierte a openMode al guardar en BD. El cliente api.js
 * acepta ambos por compatibilidad pero `external` es lo canónico.
 *
 * @typedef {Object} InstallStackRequest
 * @property {string} id                            App ID
 * @property {string} name                          Display name (registrado en BD)
 * @property {string} compose                       docker-compose.yml como string
 * @property {Object<string,string>} [env]          Variables de entorno (sobrescriben CONFIG_PATH y HOST_IP auto-inyectadas)
 * @property {string} [icon]                        URL del icono
 * @property {string} [color]                       Hex color
 * @property {number} [port]                        Puerto principal (legacy compat · canonical es ports[])
 * @property {boolean} [external]                   true → openMode="external" en BD
 * @property {string} [openMode]                    Alias legacy · se traduce a external internamente
 */

/**
 * Body para POST /api/docker/container (container individual sin compose).
 *
 * @typedef {Object} InstallContainerRequest
 * @property {string} id
 * @property {string} name
 * @property {string} image                         "jellyfin/jellyfin:latest"
 * @property {string} [icon]
 * @property {string} [color]
 * @property {string} [openMode]
 * @property {Object<string,string|number>} [ports] Map host:container ("8096": 8096)
 * @property {Object<string,string>} [env]
 * @property {Object<string,string>} [volumes]
 */

// ═══════════════════════════════════════════════════════════════════════
// UI · shapes derivados (no llegan del backend, los compone el frontend)
// ═══════════════════════════════════════════════════════════════════════

/**
 * App lista para renderizar en una card del grid.
 *
 * Es el cruce entre CatalogApp (qué se puede instalar) e InstalledApp
 * (qué está instalado ahora). Se compone con formatters.composeAppView().
 *
 * @typedef {Object} AppView
 * @property {string} id
 * @property {string} name
 * @property {string} description
 * @property {string} icon
 * @property {string} category
 * @property {string} [color]
 * @property {boolean} installed                    ¿Está instalada ahora?
 * @property {string} [status]                      Si installed: "running" | "stopped" | ...
 * @property {string} [health]                      Si installed
 * @property {CatalogApp} catalog                   Referencia al catálogo original
 * @property {InstalledApp} [runtime]               Referencia al estado runtime si instalada
 */

/**
 * Paso visual del install-flow (mockup 4).
 *
 * Mapping desde Operation.progress/message a steps con LED done/active/pending.
 *
 * @typedef {Object} InstallStep
 * @property {string} id                            Slug del paso ("docker-check", "pull-image")
 * @property {string} label                         Texto user-facing
 * @property {"done"|"active"|"pending"} state
 * @property {string} [timing]                      "12.4s" cuando done
 */

// ═══════════════════════════════════════════════════════════════════════
// CATEGORÍAS · canonical del catálogo
// ═══════════════════════════════════════════════════════════════════════

/**
 * Slugs canónicos de categorías. Coinciden con catalog.categories keys.
 *
 * @typedef {"media"|"cloud"|"downloads"|"homelab"|"development"|"security"|"monitoring"|"system"} CategorySlug
 */

// Export "vacío" — JSDoc typedefs viven en comentarios.
// Esta línea evita warnings de "module without exports" en algunos linters.
export {};
