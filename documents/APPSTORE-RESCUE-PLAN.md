# AppStore — Plan de rescate

*Estado actual, problemas reales, y diseño de la solución*

---

## Estado actual (cómo funciona ahora)

### Instalación de Docker

1. Usuario abre AppStore
2. `checkDocker()` detecta que Docker no está instalado
3. **Auto-instala Docker** sin preguntar: `curl -fsSL https://get.docker.com | sh`
4. Configura `data-root` en el pool primario (o el primero disponible)
5. Crea directorios en el pool: `docker/{data,containers,stacks,volumes}`
6. Registra Docker en el service registry

**Problema:** El usuario no decide nada. Docker se descarga de internet (versión incontrolada), se instala en el pool primario automáticamente, y si algo falla el error es críptico.

### Instalación de apps

1. Frontend manda POST a `/api/docker/stack` con el compose del catálogo
2. Backend escribe `docker-compose.yml` + `.env` en `{pool}/docker/stacks/{appId}/`
3. Config de la app va a `{pool}/docker/containers/{appId}/`
4. `docker compose up -d`
5. Se registra la app con su port, icon, color, external flag

**Problema:** Todo va al pool donde Docker se instaló. No hay opción de elegir pool por app.

### Apertura de apps

Hay 3 modos de apertura, pero el usuario no controla cuál:

| Modo | Cómo se decide | Comportamiento |
|---|---|---|
| **Iframe interno** | `external: false` en catalog.json | Se abre en ventana NimOS vía reverse proxy `/app/{appId}/` |
| **Pestaña nueva** | `external: true` en catalog.json | `window.open()` directo al puerto |
| **Fallback error** | Iframe falla (timeout 10s) | Muestra error con botón "Abrir en navegador" |

**Problema:** El flag `external` está hardcodeado en el catálogo. Si una app que dice `external: false` bloquea iframes (por CSP interno, redirects, etc.), el usuario ve error y no sabe por qué. No puede elegir.

---

## Los 3 problemas a resolver

### 1. Docker: versión controlada desde tu repo

**Ahora:** `curl https://get.docker.com | sh` — descargas la última versión de Docker que puede romper cosas.

**Solución:** Empaquetar una versión testeada de Docker en tu repo de GitHub (NimOs-appstore) y que AppStore la descargue de ahí.

**Flujo propuesto:**

```
1. En tu repo NimOs-appstore/docker/ guardas:
   - docker-ce_{version}_{arch}.deb (o binarios estáticos)
   - docker-compose-plugin_{version}_{arch}.deb
   - docker-manifest.json → { version, minNimOS, sha256, files[] }

2. AppStore muestra "Docker Engine" como primera app del catálogo
   - Categoría: "Sistema" (nueva categoría)
   - Si no está instalado: las demás apps salen deshabilitadas con badge "Requiere Docker"
   - El usuario elige en qué pool instalar Docker

3. Al instalar:
   - Descarga los .deb desde tu repo (no de get.docker.com)
   - dpkg -i en orden
   - Configura daemon.json con data-root en el pool elegido
   - Inicia Docker
   - Verifica con docker info
```

**Qué cambia en el código:**

- `catalog.json` → añadir entrada `docker-engine` con categoría `system`
- `AppStore.svelte` → quitar `checkDocker()` + auto-install, mostrar Docker como app, pool selector
- `docker.go` → nueva función `dockerInstallFromRepo()` que descarga de tu GitHub en vez de get.docker.com
- Pool selector: reutilizar la lógica que ya existe en `dockerInstall()` pero haciéndola explícita en la UI

### 2. Apps: datos siempre en el pool, nunca en el sistema

**Ahora:** Ya funciona bien — `data-root` apunta al pool y los containers/stacks van a `{pool}/docker/`. El código en `dockerInstall()` ya:

- Verifica que el pool está montado (con `findmnt`)
- Crea dirs en el pool
- Pone `data-root` en daemon.json
- Borra `/var/lib/docker`
- Registra dependencia pool→docker en service registry

**Lo que falta:** Que el usuario pueda elegir pool al instalar Docker (no auto-seleccionar el primario). Ya tienes el parámetro `pool` en el body del POST pero el frontend nunca lo manda — siempre usa el primario.

**Fix en AppStore.svelte:**

```
Al instalar Docker → mostrar selector de pool (como el wizard de crear pool)
Al instalar una app → heredar el pool de Docker (no hay opción por app, sería caos)
```

### 3. Apps: el usuario decide cómo abrir

**Ahora:** `external: true/false` hardcodeado en el catálogo.

**Solución:** Tres capas de decisión:

```
Capa 1 — Catálogo: sugiere el modo por defecto
  catalog.json: "openMode": "internal" | "external" | "auto"
  - internal: proxy iframe (por defecto si no se especifica)
  - external: siempre pestaña nueva
  - auto: intenta iframe, fallback a pestaña

Capa 2 — Usuario: puede cambiar el modo después de instalar
  Click derecho en el Launcher → "Abrir siempre en pestaña nueva" / "Abrir en NimOS"
  Se guarda en la config de la app instalada (no en el catálogo)

Capa 3 — Auto-detección: si iframe falla, ofrecer cambiar
  WebApp.svelte ya detecta errores con fetch + timeout.
  En vez de solo mostrar "Abrir en navegador", añadir:
  "¿Quieres que esta app se abra siempre en pestaña nueva?"
  → Si acepta: actualizar openMode de la app a "external"
```

**Qué cambia en el código:**

- `catalog.json` → renombrar `external` a `openMode` (mantener compat con external boolean)
- Apps instaladas → guardar `openMode` en la lista de installed apps
- `Launcher.svelte` → leer `openMode` de la app instalada (no del catálogo)
- `WebApp.svelte` → en el error handler, ofrecer guardar preferencia
- Backend → nuevo endpoint `PATCH /api/docker/installed-apps/{id}` para actualizar openMode

---

## Orden de implementación

### Fase 1 — Docker como app del catálogo (lo más urgente)

1. Quitar auto-install de `checkDocker()` en AppStore.svelte
2. Añadir `docker-engine` al catálogo como app de sistema
3. Si Docker no está instalado: mostrar solo Docker en la store con pool selector
4. Si Docker está instalado: mostrar el catálogo normal

**Esto se puede hacer SIN empaquetar Docker todavía** — simplemente hacer que el usuario clicke "Instalar Docker" explícitamente y elija pool, pero seguir usando `get.docker.com` por ahora. El empaquetado viene después.

### Fase 2 — OpenMode configurable

1. Añadir `openMode` a installed apps
2. Migrar `external: true/false` → `openMode: "external"/"internal"`
3. En Launcher: leer openMode de la app instalada
4. En WebApp.svelte: ofrecer cambiar modo si falla

### Fase 3 — Docker empaquetado (cuando estabilice)

1. Descargar Docker CE + compose plugin para amd64 y arm64
2. Testear en tu NAS y en la Raspberry Pi
3. Subir a NimOs-appstore/releases/
4. Cambiar `dockerInstall()` para descargar de tu repo

---

## Catálogo propuesto (catalog.json v2)

```json
{
  "version": 2,
  "updated": "2026-04-06",
  "categories": {
    "system": "Sistema",
    "media": "Multimedia",
    "cloud": "Cloud & Sync",
    "downloads": "Descargas",
    "homelab": "Home Lab",
    "development": "Desarrollo",
    "security": "Seguridad",
    "monitoring": "Monitorización"
  },
  "apps": {
    "docker-engine": {
      "name": "Docker Engine",
      "description": "Motor de contenedores. Necesario para instalar apps.",
      "icon": "...",
      "category": "system",
      "isSystem": true,
      "requiresPool": true,
      "compose": null
    },
    "jellyfin": {
      "name": "Jellyfin",
      "openMode": "internal",
      ...
    },
    "immich": {
      "name": "Immich",
      "openMode": "external",
      ...
    },
    "homeassistant": {
      "name": "Home Assistant",
      "openMode": "auto",
      ...
    }
  }
}
```

---

## Resumen de cambios por archivo

| Archivo | Cambio |
|---|---|
| `catalog.json` (repo appstore) | Añadir docker-engine, categoría system, openMode |
| `AppStore.svelte` | Quitar auto-install, mostrar Docker como app, pool selector |
| `docker.go` | Separar `dockerInstall` en `dockerInstallFromRepo` (fase 3) |
| `Launcher.svelte` | Leer openMode de app instalada |
| `WebApp.svelte` | Ofrecer cambiar a external si iframe falla |
| `docker.go` | Endpoint PATCH para actualizar openMode |

---

## NO hacer

- No permitir instalar apps en pools diferentes al de Docker — complicaría los volumes y los paths
- No auto-detectar el modo de apertura con un health check previo — es lento y poco fiable
- No empaquetar Docker hasta que el flujo manual funcione estable
- No tocar el reverse proxy (appproxy.go) — funciona bien, el problema es de las apps no del proxy
- No crear Containers.svelte — NimHealth lo absorbe

---

## Eliminación de Containers como app

### Por qué

Containers como app separada solo haría list/start/stop de Docker containers. NimHealth ya hace exactamente eso con el service registry, y además tiene métricas, logs, dependencias y health checks. Tener dos sitios para lo mismo confunde al usuario y duplica código.

### Qué hacer

1. **Quitar `containers` de APP_META** en `apps.js` — ya no aparece en el Launcher
2. **NO borrar** el icono ni los endpoints de docker — se siguen usando desde NimHealth y AppStore
3. **NimHealth absorbe la gestión de containers** — las apps Docker se muestran como sub-servicios

### Dónde ve el usuario sus Docker apps después del cambio

| Acción | Dónde |
|---|---|
| Instalar/desinstalar apps | **AppStore** |
| Ver estado, start/stop, logs | **NimHealth** (sub-servicios de Docker) |
| Abrir la app | **Launcher** (click en el icono de la app) |
| Ver puertos expuestos | **Network** (módulo de puertos, fase 2) |

Es un flujo más limpio: AppStore instala, NimHealth gestiona, Launcher abre.

---

## NimHealth — ampliación para absorber Containers

### Lo que ya tiene (559 líneas)

- Dashboard con CPU, RAM, Disco I/O, Red en tiempo real (polling 5s)
- Lista de servicios del registry con status + health
- Detalle por servicio: pool, path, dependencias, logs, start/stop/restart
- Filtros: todos, activos, errores, alertas, por pool
- Statusbar con contadores

### Lo que le falta

NimHealth solo muestra servicios del service registry (`/api/services`): Docker engine, NimTorrent, NimBackup. No muestra las apps individuales que corren dentro de Docker (Jellyfin, Immich, etc.)

### Principio fundamental: estado runtime real, no JSON estático

`installed-apps.json` es config (qué se instaló). NO es estado (qué está corriendo).

NimHealth tiene que ser fuente de verdad real. Para cada Docker app, el estado viene de 3 fuentes cruzadas:

| Fuente | Qué aporta | Cómo |
|---|---|---|
| `docker ps --format` | Estado real del container (Up/Exited/Created) | Runtime — polling |
| `docker inspect {id}` | Puertos declarados, health check, restart policy, mounts | Runtime — bajo demanda |
| `installed-apps.json` | Ownership lógico: nombre, icono, color, openMode | Config — estático |

Nunca confiar solo en una fuente. El estado que muestra NimHealth es siempre el cruce de las tres.

### Modelo tipado (Go — models.go)

**Base unificada — todos los servicios heredan del mismo struct:**

```go
// ─── ServiceBase — modelo unificado para TODOS los servicios ─────────────────
// Cualquier cosa que NimHealth muestre hereda de aquí.
// UI recibe siempre estos campos → nunca necesita saber si es Docker o nativo.

type ServiceBase struct {
    ID     string // "docker@poolzfs1", "nimtorrent@poolzfs1", "jellyfin"
    Type   string // "system" | "docker" | "docker-app"
    Parent string // "" para servicios raíz, "docker@poolzfs1" para apps Docker
    Name   string // "Docker Engine", "NimTorrent", "Jellyfin"
    Status string // "running" | "stopped" | "error"  ← SOLO estos 3
    Health string // "healthy" | "degraded" | "unhealthy" | "idle"  ← SOLO estos 4
}
```

**Normalización de estados — mapeo cerrado, sin ambigüedad:**

```go
// normalizeDockerStatus mapea los estados de Docker a los 3 estados válidos.
// NUNCA exponer estados raw de Docker a la UI.
func normalizeDockerStatus(dockerStatus string) string {
    switch {
    case strings.Contains(dockerStatus, "Up"):
        return "running"
    case strings.Contains(dockerStatus, "Exited"),
         strings.Contains(dockerStatus, "Created"):
        return "stopped"
    default: // "Dead", "Removing", cualquier otro
        return "error"
    }
}

// normalizeDockerHealth mapea health checks de Docker a los 4 estados válidos.
func normalizeDockerHealth(inspect string) string {
    switch inspect {
    case "healthy":
        return "healthy"
    case "unhealthy":
        return "unhealthy"
    case "starting":
        return "degraded"
    default: // "none", "" — container sin health check
        return "healthy" // si no tiene check, asumimos ok si está running
    }
}
```

**Docker app — extiende ServiceBase:**

```go
// DockerAppStatus is the runtime state of an installed Docker app.
// Built by crossing docker ps + installed-apps.json.
// docker inspect solo se usa on-demand (detalle), NO en polling.
type DockerAppStatus struct {
    ServiceBase
    Ports         []PortBinding // puertos reales (puede ser múltiple)
    Image         string        // "jellyfin/jellyfin:latest"
    Icon          string        // URL del icono
    ContainerName string        // nombre real del container en Docker
    OpenMode      string        // internal | external | auto
    Uptime        string        // "2d 14h" (from docker ps)
}

// PortBinding — un puerto puede tener múltiples bindings (bridge, host, NAT)
type PortBinding struct {
    Declared int    // puerto del compose (ej: 8096)
    Host     int    // puerto en el host (ej: 8096, puede diferir)
    Protocol string // "tcp" | "udp"
}
```

**ToMap() para ambos:**

```go
func (s ServiceBase) ToMap() map[string]interface{} {
    return map[string]interface{}{
        "id": s.ID, "type": s.Type, "parent": s.Parent,
        "name": s.Name, "status": s.Status, "health": s.Health,
    }
}

func (d DockerAppStatus) ToMap() map[string]interface{} {
    m := d.ServiceBase.ToMap()
    ports := make([]map[string]interface{}, len(d.Ports))
    for i, p := range d.Ports {
        ports[i] = map[string]interface{}{
            "declared": p.Declared, "host": p.Host, "protocol": p.Protocol,
        }
    }
    m["ports"] = ports
    m["image"] = d.Image
    m["icon"] = d.Icon
    m["containerName"] = d.ContainerName
    m["openMode"] = d.OpenMode
    m["uptime"] = d.Uptime
    return m
}
```

### Health agregado del servicio Docker

El servicio `docker@pool` calcula su health a partir de sus hijos:

```
Regla:
  - Docker daemon muerto              → status: "error",   health: "unhealthy"
  - Todos los hijos running + healthy → status: "running",  health: "healthy"
  - Al menos 1 hijo error             → status: "running",  health: "degraded"
  - Al menos 1 hijo stopped (no error)→ status: "running",  health: "degraded"
  - Todos los hijos stopped            → status: "running",  health: "idle"
  - No hay hijos (Docker sin apps)     → status: "running",  health: "healthy"
```

Esto se calcula en el backend cada vez que se pide `/api/services`, no se cachea.

### Estrategia de polling — no saturar Raspberry Pi

| Operación | Cuándo | Peso |
|---|---|---|
| `docker ps -a --format` | Polling cada 5s (ligero) | ~5ms |
| `docker inspect {id}` | On-demand: cuando el usuario abre detalle | ~20ms por container |
| `docker logs {id}` | On-demand: cuando el usuario abre detalle | Variable |
| `ss -tulnp` | On-demand: cuando Network lo pide o validación pre-install | ~10ms |

El polling de NimHealth (5s) solo ejecuta `docker ps` que es una operación ligera. Todo lo pesado (inspect, logs, ss) solo se ejecuta cuando el usuario lo pide explícitamente. En Raspberry Pi esto significa ~5ms extra cada 5s, no ~200ms.

### Containers huérfanos — política definida

**Decisión: ignorar por defecto, mostrar opcionalmente.**

Un container huérfano es uno que existe en `docker ps` pero no en `installed-apps.json` (creado con `docker run` manual o por un stack que no se registró).

- `getDockerAppStatuses()` solo muestra containers que están en `installed-apps.json`
- Los sub-containers de stacks (redis, postgres, machine-learning) se filtran como ahora
- En el detalle de Docker service, añadir un contador: "3 apps instaladas · 2 containers adicionales"
- Si el usuario quiere ver los huérfanos, click en el contador → lista de containers no registrados
- NO intentar adoptar containers huérfanos automáticamente — eso es peligroso

### Endpoint ampliado

```
GET /api/services → response:
{
  services: [
    {
      id: "docker@poolzfs1",
      type: "docker",
      parent: "",
      name: "Docker Engine",
      status: "running",
      health: "degraded",          ← calculado de los hijos
      children: [
        {
          id: "jellyfin",
          type: "docker-app",
          parent: "docker@poolzfs1",
          name: "Jellyfin",
          status: "running",
          health: "healthy",
          ports: [{ declared: 8096, host: 8096, protocol: "tcp" }],
          image: "jellyfin/jellyfin:latest",
          icon: "https://...",
          containerName: "jellyfin",
          openMode: "internal",
          uptime: "3d 7h"
        },
        {
          id: "nextcloud",
          type: "docker-app",
          parent: "docker@poolzfs1",
          name: "Nextcloud",
          status: "stopped",       ← normalizado de "Exited (0)"
          health: "idle",
          ports: [{ declared: 8080, host: 8080, protocol: "tcp" }],
          ...
        }
      ],
      orphanCount: 2               ← containers no registrados
    },
    {
      id: "nimtorrent@poolzfs1",
      type: "system",
      parent: "",
      name: "NimTorrent",
      status: "running",
      health: "healthy"
    }
  ]
}
```

### Implementación backend (services.go o docker.go)

```go
func getDockerAppStatuses(dockerServiceID string) []DockerAppStatus {
    // 1. docker ps -a --format '{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}'
    //    → estado runtime real de TODOS los containers
    //    → normalizar Status con normalizeDockerStatus()
    //    → parsear Ports a []PortBinding

    // 2. installed-apps.json → config (nombre, icono, puerto declarado, openMode)

    // 3. Cruzar: para cada installed app, buscar su container en docker ps
    //    → match por container name o app id
    //    → para stacks: buscar prefijos (immich_server, immich-server, etc.)

    // 4. Si container no existe en docker ps → status: "stopped", health: "idle"
    //    (se instaló pero no arrancó o se eliminó el container manual)

    // 5. Container en docker ps pero NO en installed-apps → contar como orphan
    //    → no incluir en children, solo incrementar orphanCount

    // 6. NO ejecutar docker inspect ni ss -tulnp aquí — eso es on-demand
}
```

### Estructura de NimHealth.svelte — NO meter todo en un archivo

NimHealth va a crecer al absorber Docker. Si queda todo en un archivo se vuelve inmanejable. Estructura propuesta:

```
src/lib/apps/
  NimHealth.svelte              ← shell: sidebar + router + métricas
src/lib/components/health/
  ServiceList.svelte            ← lista de servicios con expand/collapse
  ServiceNode.svelte            ← fila individual (servicio o docker-app)
  ServiceDetail.svelte          ← vista detalle: estado, deps, logs, acciones
  MetricsRow.svelte             ← las 4 tarjetas de CPU/RAM/Disco/Red
```

Reglas:
- `NimHealth.svelte` solo orquesta: sidebar, vista activa, polling
- `ServiceList` renderiza servicios y sus children (expandibles)
- `ServiceNode` es una fila genérica que funciona para Docker engine, NimTorrent, Y Docker apps
- `ServiceDetail` muestra el detalle al hacer click — funciona igual para cualquier tipo de servicio
- Ningún componente hijo sabe si está mostrando Docker o NimTorrent — recibe datos tipados y los renderiza

### UI conceptual

```
┌─ NimHealth ─────────────────────────────────────────┐
│ Servicios                                           │
│                                                     │
│  ▼ Docker Engine        docker@poolzfs1   degraded  │
│    ├ 🎬 Jellyfin        :8096            ● running  │
│    ├ 📷 Immich          :2283            ● running  │
│    └ ☁ Nextcloud        :8080            ○ exited   │
│                                                     │
│  ● NimTorrent           torrentd@poolzfs1 ● running │
│  ● NimBackup            backup@poolzfs1   ● running │
└─────────────────────────────────────────────────────┘
```

### Port registry en Network

3 fuentes necesarias para puertos fiables:

| Fuente | Qué dice | Cómo |
|---|---|---|
| `docker inspect` | Puertos declarados en el compose | `docker inspect --format '{{.NetworkSettings.Ports}}'` |
| `ss -tulnp` | Puertos realmente escuchando en el sistema | Runtime |
| `installed-apps.json` | Qué app "posee" cada puerto | Config |

Solo cruzando las 3 tienes la verdad completa. Un puerto puede estar declarado en el compose pero no escuchando (container caído), o escuchando pero sin app registrada (container huérfano).

En NetworkPanel, el módulo de puertos mostraría:

```
Puerto    App             Estado      Origen
:8096     Jellyfin        ● activo    Docker
:8080     Nextcloud       ○ inactivo  Docker
:9091     NimTorrent      ● activo    Nativo
:5000     NimOS Daemon    ● activo    Sistema
```

Validación pre-install: antes de desplegar un stack, comprobar con `ss -tulnp` si el puerto ya está ocupado. Si lo está, advertir al usuario.

---

## Resumen de cambios por archivo (actualizado)

| Archivo | Cambio |
|---|---|
| **Backend** | |
| `models.go` | Nuevo struct `DockerAppStatus` — modelo tipado para Docker apps |
| `services.go` | Ampliar `/api/services`: inyectar Docker apps como `children`, health agregado |
| `docker.go` | Nueva función `getDockerAppStatuses()` — cruce docker ps + inspect + installed-apps. Endpoint PATCH para openMode |
| **Frontend** | |
| `catalog.json` (repo appstore) | Añadir docker-engine, categoría system, openMode por app |
| `apps.js` | Quitar `containers` de APP_META |
| `AppStore.svelte` | Quitar auto-install, mostrar Docker como app, pool selector |
| `NimHealth.svelte` | Reducir a shell: sidebar, router, métricas. Extraer componentes |
| `components/health/ServiceList.svelte` | **Nuevo** — lista de servicios con expand/collapse para Docker children |
| `components/health/ServiceNode.svelte` | **Nuevo** — fila genérica (sirve para Docker engine, apps, NimTorrent, etc.) |
| `components/health/ServiceDetail.svelte` | **Nuevo** — vista detalle: estado, deps, logs, acciones |
| `components/health/MetricsRow.svelte` | **Nuevo** — las 4 tarjetas CPU/RAM/Disco/Red (extraídas de NimHealth) |
| `Launcher.svelte` | Leer openMode de app instalada |
| `WebApp.svelte` | Ofrecer cambiar a external si iframe falla (una vez → permanente) |

---

## Orden de implementación (actualizado)

### Fase 1 — Docker explícito + modelo tipado (lo más urgente)

1. `models.go`: añadir struct `DockerAppStatus`
2. `docker.go`: implementar `getDockerAppStatuses()` — cruce runtime real (docker ps + inspect) con config (installed-apps.json)
3. `services.go`: ampliar `/api/services` para inyectar Docker apps como `children` con health agregado
4. `apps.js`: quitar `containers` de APP_META
5. `AppStore.svelte`: quitar auto-install de `checkDocker()`, mostrar Docker como app del catálogo con pool selector
6. `catalog.json`: añadir `docker-engine` como app de sistema

### Fase 2 — NimHealth refactor + OpenMode + Network

1. Extraer componentes de NimHealth: ServiceList, ServiceNode, ServiceDetail, MetricsRow
2. ServiceList: renderizar servicios con children expandibles (Docker apps)
3. ServiceNode: fila genérica — misma UI para Docker engine, Docker app, NimTorrent
4. ServiceDetail: detalle con logs de container (`docker logs`), start/stop, estado runtime
5. OpenMode configurable en installed apps + Launcher + WebApp fallback
6. Network: módulo de puertos con 3 fuentes cruzadas (docker inspect + ss -tulnp + installed-apps)
7. Validación pre-install: comprobar puerto antes de desplegar stack

### Fase 3 — Docker controlado (cuando estabilice)

1. Fijar versión de Docker con apt pinning (no empaquetar .deb)
2. Testear en x86 NAS y Raspberry Pi
3. Cambiar `dockerInstall()` para usar repo oficial con versión pinneada
