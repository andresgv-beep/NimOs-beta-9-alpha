# AppStore — Auditoría Arquitectónica v1

**Tipo**: Documento de auditoría técnica
**Scope**: Módulo AppStore + integración NimHealth (Beta 8.1)
**Versión**: v1 (24/05/2026)
**Autor**: Claude (co-developer) + Andrés
**Estado**: REVISIÓN — pendiente decisión de Andrés sobre orden de ejecución

---

## 0 · Resumen ejecutivo

El módulo AppStore se encuentra en **estado de migración parcial**: la arquitectura nueva (modelo tipado `DockerAppStatus`, observer reconciler, cache en memoria, repository pattern) está implementada correctamente y respeta `NIMOS_DISCIPLINE.md` v2. La generación anterior (handlers `map[string]interface{}`, `docker.json` como fuente paralela, status en archivos JSON) **convive** con la nueva sin haber sido deprecada.

El frontend `AppStore.svelte` es un stub explícito (124 líneas) pendiente de port desde Beta 7.

Se identifican **41 hallazgos** distribuidos en 9 categorías. Un único bug crítico activo (`dockerContainerRebuild` pierde flags de container). El resto son anti-patrones, patrones de disciplina faltantes, tareas pendientes del rescue plan, refinamientos y documentación.

### Severidad agregada

| Nivel | Count | Significado |
|---|---|---|
| 🔴 P0 — Crítico | 1 | Bug activo con pérdida de datos o seguridad |
| 🟠 P1 — Alto | 12 | Anti-patrón arquitectónico, contrato roto, regresión potencial |
| 🟡 P2 — Medio | 17 | Deuda técnica, calidad de código, gaps de disciplina |
| 🟢 P3 — Bajo | 11 | Documentación, refinamiento, refactor cosmético |

### Veredicto

El módulo **NO está en estado de ruina arquitectónica**. Los huesos (modelo de datos, separación de responsabilidades, patrones de disciplina v2) son correctos. La distancia entre estado actual y estado profesional es **5-7 sesiones** de trabajo ordenado.

---

## 1 · Convenciones de este documento

### IDs

Cada hallazgo tiene un ID estable `APP-NNN` para tracking. No se reusan ni se reordenan tras publicación.

### Severidad

- 🔴 **P0** — Crítico: rompe funcionalidad o introduce vulnerabilidad. Fix antes que cualquier otra cosa.
- 🟠 **P1** — Alto: contrato arquitectónico roto, anti-patrón con impacto observable, deuda con interés alto.
- 🟡 **P2** — Medio: calidad de código, gap de disciplina, refinamiento con impacto futuro.
- 🟢 **P3** — Bajo: documentación, ergonomía, cosmético.

### Esfuerzo (t-shirt size)

- **XS** — <1h, función puntual
- **S** — 1-4h, un archivo, con tests
- **M** — 4-8h, varios archivos, tests + migración
- **L** — 1-2 días, fase completa con coordinación frontend

### Tipo

`Bug` · `AntiPattern` · `MissingPattern` · `Feature` · `Refactor` · `Doc` · `Test` · `Security`

---

## 2 · Inventario de hallazgos

### Tabla maestra

| ID | Severidad | Esfuerzo | Tipo | Título |
|---|---|---|---|---|
| APP-001 | 🔴 P0 | S | Bug | `dockerContainerRebuild` pierde todas las flags del container |
| APP-010 | 🟠 P1 | M | AntiPattern | Duplicación `dockerInstalledApps` vs `getDockerAppStatuses` |
| APP-011 | 🟠 P1 | M | AntiPattern | `docker.json` es segunda fuente de verdad paralela a SQLite |
| APP-012 | 🟠 P1 | M | AntiPattern | Estado de instalación en archivos JSON (`install-{id}.json`) |
| APP-013 | 🟠 P1 | S | AntiPattern | Doble registro del Docker engine (síncrono + auto-detector) |
| APP-014 | 🟠 P1 | M | AntiPattern | `dockerInstall` bloquea handler HTTP hasta 300s |
| APP-020 | 🟠 P1 | S | MissingPattern | Sin CircuitBreaker para Docker Hub (`docker pull`) |
| APP-030 | 🟠 P1 | XS | Bug | `findPoolFromPath` asume `/nimos/pools/` sin documentar ni avisar |
| APP-031 | 🟠 P1 | XS | Bug | Race window en `dockerContainerDelete` |
| APP-033 | 🟠 P1 | S | Bug | Multi-port persiste solo el primero (asimetría stopped/running) |
| APP-040 | 🟠 P1 | L | Feature | Auto-install Docker contradice rescue plan |
| APP-044 | 🟠 P1 | L | Feature | `AppStore.svelte` es stub — port pendiente |
| APP-053 | 🟠 P1 | S | Bug | `dockerPull` síncrono bloquea HTTP por GBs |
| APP-015 | 🟡 P2 | XS | AntiPattern | Service registry hardcodea `Status/Health` sin verificar |
| APP-016 | 🟡 P2 | S | Refactor | `docker.go` mezcla firewall + drivers hardware |
| APP-017 | 🟡 P2 | XS | Refactor | Heurística stack-matching duplicada |
| APP-021 | 🟡 P2 | XS | MissingPattern | Sin CircuitBreaker para `get.docker.com` |
| APP-022 | 🟡 P2 | XS | MissingPattern | Sin CircuitBreaker para descarga de iconos |
| APP-023 | 🟡 P2 | S | MissingPattern | Sin events `appstore.*` |
| APP-024 | 🟡 P2 | S | MissingPattern | Sin SystemCapabilities (Docker version, compose v2, BuildKit) |
| APP-025 | 🟡 P2 | XS | Doc | HealthStatus rescue plan (4) inconsistente con canónico (6) |
| APP-032 | 🟡 P2 | XS | Bug | `Type` del POST público no validado |
| APP-034 | 🟡 P2 | S | Feature | Sin invalidación de cache post-install (gap UX 30s) |
| APP-041 | 🟡 P2 | S | Feature | Sin pool selector explícito al instalar Docker |
| APP-042 | 🟡 P2 | XS | Feature | Sin endpoint `PATCH /api/installed-apps/:id` para `openMode` |
| APP-043 | 🟡 P2 | S | Feature | Sin "offer switch to external" si iframe falla |
| APP-045 | 🟡 P2 | M | Feature | `catalog.json v2` no existe (sin `docker-engine` como app system) |
| APP-051 | 🟡 P2 | XS | Refactor | `bootstrapNativeApps` stale cleanup confuso |
| APP-054 | 🟡 P2 | XS | Bug | `runSafe` traga errores en `dockerInstall` (chmod/chown/setfacl) |
| APP-055 | 🟡 P2 | S | Bug | `getDockerConfigGo`/`saveDockerConfigGo` sin locking |
| APP-035 | 🟢 P3 | XS | Doc | `refreshDockerCache` solo busca primer engine "containers" |
| APP-050 | 🟢 P3 | XS | Refactor | 3 archivos duplicados en raíz vs `daemon/` |
| APP-052 | 🟢 P3 | XS | Refactor | `getAppPort` usa `context.Background()` |
| APP-060 | 🟢 P3 | S | Security | WebSocket proxy handshake manual sin sanitize CRLF |
| APP-061 | 🟢 P3 | S | Security | Cookie scoping cross-app en reverse proxy |
| APP-062 | 🟢 P3 | XS | Security | Headers passthrough sin filtro |
| APP-063 | 🟢 P3 | XS | Security | `/var/lib/docker` borrado con `rm -rf` sin backup |
| APP-070 | 🟢 P3 | XS | Doc | `service_instances.health` del engine no refleja agregate |
| APP-080 | 🟢 P3 | S | Test | Sin test de cruce `docker_apps × docker ps` con stacks reales |
| APP-081 | 🟢 P3 | XS | Test | Sin test de race en uninstall |
| APP-082 | 🟢 P3 | XS | Test | Sin test de auto-registro post-install |

---

## 3 · Detalle de hallazgos

### Categoría A — Bug crítico

#### APP-001 🔴 P0 · `dockerContainerRebuild` pierde todas las flags del container

**Archivo**: `daemon/docker.go:1209-1240`
**Esfuerzo**: S
**Tipo**: Bug

**Descripción**:

La función `dockerContainerRebuild` se anuncia como rebuild pero su implementación es:

```go
runSafe("docker", "stop", safeId)
runSafe("docker", "rm", safeId)
runSafe("docker", "run", "-d", "--name", safeId, "--restart", "unless-stopped", safeImage)
```

Esto **destruye y recrea** el container con flags por defecto. Se pierden:

- Volume mounts (`-v /pool/jellyfin:/config`)
- Variables de entorno (`-e PUID=1000 -e TZ=Europe/Madrid`)
- Port mappings (`-p 8096:8096`)
- Network attachments (`--network proxy`)
- Labels, restart policy custom, resource limits, capabilities

**Impacto**:

Un click en "Rebuild" desde la UI deja a Jellyfin sin acceso a su biblioteca, a Immich sin DB, a Vaultwarden sin vault. **Silenciosamente** (el rebuild "tiene éxito" para el frontend).

**Acción propuesta**:

Dos rutas. **A** (correcta pero costosa): leer `docker inspect` completo, reconstruir flags equivalentes, ejecutar `docker run` con todas. **B** (pragmática): para containers con stack, ejecutar `docker compose -f {stack}/docker-compose.yml up -d --force-recreate`. Para containers sueltos sin stack, deshabilitar el endpoint hasta tener implementación correcta y devolver `501 Not Implemented`.

**Recomendación**: B inmediato + A como ticket separado.

---

### Categoría B — Anti-patrones arquitectónicos

#### APP-010 🟠 P1 · Duplicación legacy/nuevo del cruce `docker_apps × docker ps`

**Archivos**:
- `daemon/docker.go:537-651` (`dockerInstalledApps`, sirve `/api/installed-apps`)
- `daemon/nimhealth_docker.go:85-213` (`getDockerAppStatuses`, alimenta cache)

**Esfuerzo**: M
**Tipo**: AntiPattern

**Descripción**:

Dos implementaciones independientes del mismo cruce:

| Aspecto | `dockerInstalledApps` (legacy) | `getDockerAppStatuses` (nuevo) |
|---|---|---|
| Tipado | `map[string]interface{}` | `[]DockerAppStatus` |
| Status normalizado | No (devuelve `"unknown"` no canónico) | Sí (`running/stopped/error`) |
| Health calculado | No | Sí (vía `ComputeDockerAggregateHealth`) |
| Heurística sufijos | Duplicada | Duplicada |
| Orphan handling | Mezcla orphans con registrados | Cuenta separadamente (correcto) |
| Endpoint consumidor | `GET /api/installed-apps` | `/api/services` vía cache |

**Impacto**: dos fuentes de verdad para "qué apps Docker hay y en qué estado". El frontend nuevo debe consumir solo la segunda; mientras la primera viva, cualquier bug fix se hace dos veces.

**Acción propuesta**:

1. Crear endpoint `GET /api/appstore/installed` que internamente llame a `getDockerAppStatuses` y devuelva el shape tipado (sin pasar por `/api/services`).
2. Marcar `/api/installed-apps` como deprecado (header `X-Deprecated`) y planificar su retirada en Beta 9.
3. Extraer la heurística sufijos/prefijos a `matchContainerByAppID(appID string, containers map[string]dockerContainer) (string, bool)` en `nimhealth_docker.go`.

---

#### APP-011 🟠 P1 · `docker.json` como segunda fuente de verdad

**Archivos**:
- `daemon/docker.go:22-45` (`getDockerConfigGo` / `saveDockerConfigGo`)
- `/var/lib/nimos/config/docker.json` (filesystem)

**Esfuerzo**: M
**Tipo**: AntiPattern

**Descripción**:

`docker.json` contiene: `installed`, `path`, `permissions`, `appPermissions`, `installedAt`. Esta misma información (excepto `appPermissions`) existe ya en SQLite (`service_instances`, `docker_apps`). Las dos fuentes pueden divergir tras crash, edición manual o concurrent writes.

`getDockerConfigGo` y `saveDockerConfigGo` no usan locking — múltiples handlers pueden race-condition-ear escrituras.

**Impacto**: comportamiento indefinido si las dos fuentes discrepan. La detección del engine depende del JSON, no de SQLite. La UI lee de uno u otro según el handler.

**Acción propuesta**:

Migrar a tablas SQLite:

```sql
-- docker_config: una sola row identificada por singleton key
CREATE TABLE IF NOT EXISTS docker_config (
    key TEXT PRIMARY KEY,            -- 'main'
    installed INTEGER NOT NULL,
    pool_name TEXT,
    data_path TEXT,
    installed_at TEXT,
    docker_version TEXT,
    compose_version TEXT
);

-- docker_permissions: matriz user → docker access
CREATE TABLE IF NOT EXISTS docker_permissions (
    username TEXT PRIMARY KEY,
    granted_at TEXT NOT NULL,
    granted_by TEXT NOT NULL
);

-- docker_app_permissions: matriz app → user
CREATE TABLE IF NOT EXISTS docker_app_permissions (
    app_id TEXT NOT NULL,
    username TEXT NOT NULL,
    PRIMARY KEY (app_id, username),
    FOREIGN KEY (app_id) REFERENCES docker_apps(id) ON DELETE CASCADE
);
```

Migration: leer `docker.json` al arranque si existe, copiar a tablas, renombrar a `docker.json.migrated`.

---

#### APP-012 🟠 P1 · Estado de instalación en archivos JSON

**Archivos**:
- `daemon/apps.go:368-451` (`nativeAppInstall`, `nativeAppInstallStatus`)
- `/var/log/nimos/install-{id}.json`

**Esfuerzo**: M
**Tipo**: AntiPattern

**Descripción**:

`nativeAppInstall` escribe un archivo JSON con el estado de instalación que el frontend pollea con `nativeAppInstallStatus`. Problemas:

- No atómico (read/write sin lock)
- No auditable (sin histórico de operaciones)
- Persistente accidentalmente (si el daemon crashea mid-install, el archivo queda `"installing"` para siempre)
- Poll-only (sin notificación push)

**Acción propuesta**:

Crear tabla `nimos_operations` (reutilizable, no específica de apps):

```sql
CREATE TABLE IF NOT EXISTS nimos_operations (
    request_id     TEXT PRIMARY KEY,         -- UUID v4 generado por backend
    operation_type TEXT NOT NULL,            -- 'app_install_native' | 'app_install_docker' | ...
    target         TEXT NOT NULL,            -- app id o equivalente
    status         TEXT NOT NULL,            -- 'pending' | 'running' | 'success' | 'failed'
    triggered_by   TEXT NOT NULL,            -- username
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    error_message  TEXT,
    log_path       TEXT                      -- opcional para logs detallados
);

CREATE INDEX idx_operations_target ON nimos_operations(target);
CREATE INDEX idx_operations_status ON nimos_operations(status);
```

El handler devuelve `request_id` al iniciar. El frontend pollea `GET /api/operations/{request_id}` o, mejor aún, recibe SSE (Server-Sent Events) push.

Esto resuelve también APP-014 (handler bloqueante) y APP-053 (dockerPull bloqueante).

---

#### APP-013 🟠 P1 · Doble registro del Docker engine

**Archivos**:
- `daemon/docker.go:831-846` (registro síncrono en `dockerInstall`)
- `daemon/nimhealth_detectors.go:116-139` (`detectDockerEngine` auto)

**Esfuerzo**: S
**Tipo**: AntiPattern

**Descripción**:

El engine `docker@{pool}` se registra en dos sitios independientes con metadata distinta:

- Síncrono (`dockerInstall`): `Status: "running", Health: "healthy"` hardcoded sin verificación
- Auto (`detectDockerEngine`): `Status: "unknown", Health: HealthUnknown` que luego corrige `reconcileOneInstance`

Si los IDs coinciden, `INSERT OR IGNORE` lo hace idempotente. Pero los IDs pueden divergir: `dockerInstall` usa `targetPool.Name` directo, mientras `detectDockerEngine` deriva el nombre del pool con `findPoolFromPath(dockerPath)`, que asume convención de path (ver APP-030).

**Impacto**: si por cualquier razón los IDs divergen, hay dos rows. El observer reconcilia ambas. El frontend ve dos Docker engines.

**Acción propuesta**:

Eliminar el registro síncrono de `dockerInstall`. Único punto: `detectDockerEngine`. Para forzar registro inmediato post-install, invocar `runAutoRegister(ctx)` explícitamente después de `dockerInstall` antes de devolver respuesta HTTP.

---

#### APP-014 🟠 P1 · `dockerInstall` bloqueante hasta 300s

**Archivo**: `daemon/docker.go:653-849`
**Esfuerzo**: M
**Tipo**: AntiPattern

**Descripción**:

`dockerInstall` ejecuta:
- Descarga script Docker (~30s)
- `bash docker-install.sh` (~120s en una Pi, ~60s en x86)
- `systemctl start docker` + verificación (~5s)
- Permisos, share creation, etc. (~5s)

Todo síncrono en el handler HTTP. Timeout interno de 300s pero clientes HTTP típicos tienen 60-120s. Si el cliente desconecta, la goroutine **sigue corriendo** sin manera de checkear estado: el handler ya devolvió error, el daemon sigue instalando, el usuario refresca y ve Docker apareciendo "solo".

**Acción propuesta**:

Convertir a async via tabla `nimos_operations` (APP-012). Handler devuelve `202 Accepted` con `request_id` inmediatamente. Goroutine actualiza la row. Frontend pollea o suscribe SSE.

---

### Categoría C — Disciplina v2: patrones faltantes

#### APP-020 🟠 P1 · CircuitBreaker para Docker Hub ausente

**Archivos afectados**:
- `daemon/docker.go:1288` (`dockerPull`)
- `daemon/docker.go:1079` (`dockerStackDeploy` → `docker compose up -d` hace pulls implícitos)

**Esfuerzo**: S
**Tipo**: MissingPattern (Disciplina §3)

**Descripción**:

`NIMOS_DISCIPLINE.md` §3 lista Docker Hub explícitamente como caso esperado de CircuitBreaker. El módulo `breaker.go` ya existe y se usa intensivamente en Network (DDNS, Let's Encrypt, UPnP). El módulo AppStore está completamente fuera de esa protección.

Sin breaker:
- Docker Hub rate-limit (100 pulls/6h sin login) devuelve error genérico al usuario sin contexto.
- Bucle de retries del cliente puede agravar el ban.
- Cada deploy que falla por rate limit aparece como "Stack deployment failed" idéntico a "compose syntax error".

**Acción propuesta**:

En boot:

```go
// daemon/docker_boot.go (nuevo)
var dockerHubBreaker *CircuitBreaker

func initDockerBreakers() {
    dockerHubBreaker = NewCircuitBreaker(DefaultBreakerConfig("dockerhub.pull"))
}
```

Envolver `dockerPull` y los pulls implícitos en `dockerStackDeploy`:

```go
err := dockerHubBreaker.Call(func() error {
    _, ok := runSafe("docker", "pull", image)
    if !ok {
        return ErrDockerHubTransient
    }
    return nil
})
```

Traducción a HealthStatus del servicio AppStore (no del breaker directamente, disciplina §8):
- `closed` → `healthy`
- `half_open` → `degraded`
- `open` → `failed` con reasonCode `dockerhub_unavailable`

---

#### APP-021 🟡 P2 · Sin CircuitBreaker para `get.docker.com`

**Archivo**: `daemon/docker.go:737`
**Esfuerzo**: XS
**Tipo**: MissingPattern

Descarga del script de instalación de Docker hace `curl` directo. Si `get.docker.com` está caído o rate-limita, falla genéricamente.

**Acción**: breaker `dockerhub.install_script` envuelve la descarga. Como esto es one-shot, basta con respeto al cooldown global (no es polling).

---

#### APP-022 🟡 P2 · Sin CircuitBreaker para descarga de iconos

**Archivo**: `daemon/apps.go:614-645` (`downloadAppIcon`)
**Esfuerzo**: XS
**Tipo**: MissingPattern

Cada install pasa URL de icono y se hace `curl` a host arbitrario. Si el host es lento o caído, bloquea la instalación 15s (timeout actual).

**Acción**: breaker por dominio (no por host puntual) o, mejor aún, mover la descarga de iconos a un job background separado del install. El install completa con icono `📦` fallback; el icono real se carga después.

---

#### APP-023 🟡 P2 · Sin events `appstore.*`

**Esfuerzo**: S
**Tipo**: MissingPattern (Disciplina §4)

Install/uninstall/start/stop son **auditables** por definición — disciplina §4 explícita. Hoy ningún handler emite event a `nimos_events`.

**Acción**: emitir events con `dedupe + rate limit` ya implementados en `network_events.go`:

| Event | Categoría | Level | Cuándo |
|---|---|---|---|
| `app_installed` | appstore | info | Tras OK de stack/container deploy |
| `app_uninstalled` | appstore | info | Tras `docker rm` exitoso |
| `app_install_failed` | appstore | warn | Si install falla |
| `app_started` | appstore | debug | Start manual desde NimHealth |
| `app_stopped` | appstore | debug | Stop manual |
| `docker_hub_unavailable` | breaker | warn | Cuando breaker abre |

Retention: `info` 7 días, `warn` 30, `error` 90 (disciplina §4).

---

#### APP-024 🟡 P2 · Sin SystemCapabilities para Docker

**Esfuerzo**: S
**Tipo**: MissingPattern (Disciplina §7)

Capabilities útiles a detectar (lazy, refresh on-demand, no polling):

| Capability | Cómo se detecta | Por qué importa |
|---|---|---|
| `docker.installed` | `docker --version` | Habilita catálogo |
| `docker.compose_v2` | `docker compose version` | Stack deploy depende de v2 |
| `docker.buildkit` | `docker info` → `Server.Plugins.Buildx` | Builds desde source |
| `docker.gpu_nvidia` | `docker info` → runtimes contiene `nvidia` | Apps con CUDA (Plex transcoding, etc.) |
| `docker.rootless` | `docker info` → SecurityOptions | Solo informativo |

**Acción**: añadir detector `detectDockerCapabilities(ctx)` en `nimos_capabilities.go`. Endpoint `GET /api/appstore/capabilities`. Refresh on-demand cuando se abre el AppStore (lazy refresh si `last_detected_at > 1h`).

---

#### APP-025 🟡 P2 · HealthStatus inconsistente entre rescue plan y canónico

**Archivos**:
- `documents/APPSTORE-RESCUE-PLAN.md:298-299` (define 4 valores: `healthy|degraded|unhealthy|idle`)
- `daemon/nimos_health.go` (canónico: 6 valores `healthy|degraded|failed|partial|unknown|stale`)

**Esfuerzo**: XS
**Tipo**: Doc

**Descripción**:

El rescue plan especifica 4 valores que no son los canónicos. `nimhealth_docker.go` ya usa los canónicos correctos en `ComputeDockerAggregateHealth` (usa `HealthHealthy`, `HealthDegraded`, `HealthFailed`). El plan necesita actualización.

**Acción**: actualizar `APPSTORE-RESCUE-PLAN.md`:
- "all stopped (engine OK)" → `healthy` (no `idle` — el engine está sano y la inactividad es intencional)
- "at least 1 stopped + others running" → `degraded` (mix)
- "1+ child status=error" → `degraded` (no `failed`, solo `failed` cuando el engine mismo está roto)
- "docker daemon down" → `failed`

---

### Categoría D — Contrato AppStore → NimHealth

#### APP-030 🟠 P1 · `findPoolFromPath` asume convención sin enforce

**Archivo**: `daemon/nimhealth_detectors.go:99-111`
**Esfuerzo**: XS
**Tipo**: Bug latente

**Descripción**:

```go
func findPoolFromPath(path string) string {
    prefix := nimosPoolsDir + "/"
    if !strings.HasPrefix(path, prefix) {
        return ""
    }
    ...
}
```

Silenciosamente devuelve `""` si el path no empieza por `/nimos/pools/`. `detectDockerEngine` con `poolName == ""` devuelve `nil`. **El engine no se registra y no hay log**.

**Acción**: en `detectDockerEngine`, loggear con nivel warn si `installed=true` pero `findPoolFromPath` falla:

```go
if installed && dockerPath != "" {
    poolName := findPoolFromPath(dockerPath)
    if poolName == "" {
        logMsg("nimhealth: Docker installed at %q but path doesn't live under %s/ — engine NOT registered. "+
            "Either move docker data to a pool or fix docker.json manually.", dockerPath, nimosPoolsDir)
        return nil
    }
    ...
}
```

Mejor todavía: añadir `pool_name` explícito a la nueva tabla `docker_config` (APP-011) en vez de re-derivar.

---

#### APP-031 🟠 P1 · Race window en `dockerContainerDelete`

**Archivo**: `daemon/docker.go:1141-1168`
**Esfuerzo**: XS
**Tipo**: Bug

**Descripción**:

```go
appsRepo.DeleteDockerApp(r.Context(), safeId)   // 1) síncrono
go func() {
    runSafe("docker", "stop", safeId)            // 2) goroutine
    runSafe("docker", "rm", safeId)
}()
```

Entre (1) y el final de (2) (puede ser hasta 10s con SIGTERM grace), el observer puede correr:
- `docker_apps` no tiene la app → no aparece en `registered`
- `docker ps` sí la tiene → cuenta como **orphan**
- `orphanCount` parpadea +1, luego -1 al siguiente tick

No rompe nada hoy, pero cuando se añadan events (APP-023), generará falsos "orphan detected".

**Acción**:

Opción A (simple): invertir orden. Stop+rm primero, DELETE row después.

Opción B (más robusta): añadir flag `deleting INTEGER DEFAULT 0` a `docker_apps`. Marcar al iniciar delete. `getDockerAppStatuses` filtra rows con `deleting=1`. Goroutine de cleanup hace DELETE real al final.

Recomiendo B porque cubre también el caso de `docker stop` tardando: el frontend ve la app desaparecer inmediatamente.

---

#### APP-032 🟡 P2 · `Type` del POST público no validado

**Archivos**:
- `daemon/apps.go:577` (POST `/api/installed-apps`)
- `daemon/db_apps.go:141` (`CreateOrUpdateDockerApp`)

**Esfuerzo**: XS
**Tipo**: Bug

**Descripción**:

```go
Type: bodyStr(body, "type"),  // lo que mande el cliente
```

Acepta cualquier string. La heurística de matching (`getDockerAppStatuses`) no usa `Type` así que no rompe ahora, pero la columna queda sucia.

**Acción**: en `CreateOrUpdateDockerApp`, validar:

```go
if typ != "container" && typ != "stack" {
    return fmt.Errorf("docker app: type must be 'container' or 'stack', got %q", typ)
}
```

---

#### APP-033 🟠 P1 · Multi-port persiste solo el primero

**Archivos**:
- `daemon/docker.go:996-1002` (`dockerContainerCreate`)
- `daemon/nimhealth_docker.go:172-175` (fallback cuando stopped)
- `daemon/apps_schema.sql:31` (columna `port INTEGER`)

**Esfuerzo**: S
**Tipo**: Bug

**Descripción**:

Apps con múltiples puertos (Transmission: 9091 web + 51413 sync, Plex: 32400 + DLNA + ...) registran solo uno. Cuando el container está **running**, `parsePorts` lee todos de `docker ps`. Cuando está **stopped**, solo devuelve uno. Asimetría observable por el frontend.

**Acción**:

```sql
ALTER TABLE docker_apps ADD COLUMN ports_json TEXT DEFAULT '[]';
```

Almacenar JSON serializado de `[]PortBinding`. `dockerContainerCreate` construye el array completo. `getDockerAppStatuses` cuando stopped deserializa y devuelve todos.

Migration: rellenar `ports_json` con `[{declared: port, host: port}]` para rows existentes.

---

#### APP-034 🟡 P2 · Sin invalidación de cache post-install

**Esfuerzo**: S
**Tipo**: Feature

**Descripción**:

Tras `dockerStackDeploy` exitoso, hasta el próximo tick del observer (hasta 30s) la app no aparece en `/api/services`. El frontend del AppStore acabaría mostrando "Jellyfin instalada" pero NimHealth no la ve. Ventana de inconsistencia visible.

**Acción**:

Función helper `forceDockerCacheRefresh(ctx)` que invoca `getDockerAppStatuses` + `ComputeDockerAggregateHealth` y reescribe la cache. Llamada desde:

- `dockerStackDeploy` después de `docker compose up -d` OK
- `dockerContainerCreate` después de `docker run` OK
- `dockerContainerDelete` después del cleanup
- `dockerContainerAction` (start/stop/restart) después de la operación

Alternativa más limpia: scheduler con `RunNow(reconciler_name)` que dispara un tick del observer fuera de su intervalo (compatible con disciplina §5 — "Critical: si hay drift detectado, ejecutar AHORA"). El install ES un drift conocido.

---

#### APP-035 🟢 P3 · `refreshDockerCache` solo encuentra primer engine

**Archivo**: `daemon/nimhealth_observer.go:155-166`
**Esfuerzo**: XS
**Tipo**: Doc

**Descripción**:

```go
for _, inst := range instances {
    if inst.AppID == "containers" {
        dockerInstanceID = inst.ID
        break
    }
}
```

Asume un único Docker engine. Rescue plan dice explícitamente "no permitir Docker en pools diferentes". Pero el código no enforce el constraint.

**Acción**: documentar el supuesto en comentario + añadir validación en `dockerInstall` que rechace si ya hay row en `docker_config`. Si el día de mañana se quiere multi-engine, hay que cambiar varios sitios coordinadamente.

---

### Categoría E — Rescue plan: tareas pendientes

#### APP-040 🟠 P1 · `dockerInstall` sigue auto-instalando

**Archivo**: `daemon/docker.go:653-849`
**Esfuerzo**: L (parte del rediseño Fase 1 frontend)
**Tipo**: Feature

**Descripción**:

Rescue plan §1 "Docker como app del catálogo" no implementado. El flujo actual descarga `get.docker.com` automáticamente sin pedir confirmación ni pool selector.

**Acción**: ver Plan de ejecución Fase 1.

---

#### APP-041 🟡 P2 · Sin pool selector explícito al instalar Docker

**Esfuerzo**: S
**Tipo**: Feature

`dockerInstall` acepta `pool` en el body pero el frontend nunca lo manda. Backend hace fallback al primario o al primero disponible.

**Acción**: parte de APP-040.

---

#### APP-042 🟡 P2 · Sin endpoint `PATCH openMode`

**Esfuerzo**: XS
**Tipo**: Feature

Rescue plan Capa 2: usuario puede cambiar `openMode` post-install desde context menu del Launcher. Hoy `open_mode` solo se setea al POST inicial.

**Acción**:

```go
// PATCH /api/installed-apps/:id
// Body: {"openMode": "internal" | "external" | "auto"}
func handlePatchInstalledApp(w http.ResponseWriter, r *http.Request, id string) {
    // ... validate ...
    app, _ := appsRepo.GetDockerApp(ctx, id)
    app.OpenMode = newMode
    appsRepo.CreateOrUpdateDockerApp(ctx, app)
}
```

---

#### APP-043 🟡 P2 · Sin "offer switch to external" si iframe falla

**Archivo**: `src/lib/apps/WebApp.svelte` (Beta 7), por migrar a Beta 8.1
**Esfuerzo**: S (frontend) + endpoint backend (APP-042)
**Tipo**: Feature

Rescue plan Capa 3: si iframe falla por X-Frame-Options o redirect, ofrecer al user marcar la app como `external` permanentemente.

**Acción**: parte de fase frontend.

---

#### APP-044 🟠 P1 · `AppStore.svelte` es stub

**Archivo**: `src/lib/apps/AppStore.svelte` (124L placeholder)
**Esfuerzo**: L
**Tipo**: Feature

**Acción**: port completo desde Beta 7 con design system v3, consumiendo `/api/services` (no `/api/installed-apps`), con pool selector, openMode configurable, breaker-aware UI (mostrar "Docker Hub unavailable" si breaker open).

---

#### APP-045 🟡 P2 · `catalog.json v2` no existe

**Esfuerzo**: M (incluye creación del repo `NimOs-appstore`)
**Tipo**: Feature

Rescue plan especifica formato v2 con `docker-engine` como app de sistema, categoría `system`, openMode por app.

**Acción**: crear repo separado `NimOs-appstore` con `catalog.json`, hosting de iconos, futuras releases controladas de Docker.

---

### Categoría F — Calidad de código

#### APP-050 🟢 P3 · Archivos duplicados en raíz del repo

**Archivos**:
- `storage_observer.go` (raíz) — duplicado de `daemon/storage_observer.go`
- `storage_observer_test.go` (raíz) — duplicado
- `storage_startup.go` (raíz) — duplicado

**Esfuerzo**: XS
**Tipo**: Refactor

**Acción**: `git rm` los 3 archivos de raíz. Verificar build no se rompe.

---

#### APP-051 🟡 P2 · `bootstrapNativeApps` stale cleanup confuso

**Archivo**: `daemon/apps.go:201-205`
**Esfuerzo**: XS
**Tipo**: Refactor

```go
staleAge := time.Since(scanStart) + 1*time.Minute
removed, err := appsRepo.DeleteStaleAutoDetected(ctx, staleAge)
```

Lógica correcta pero ilegible. La condición efectiva es "borrar autodetectadas con `last_seen_at < scanStart - 1min`", que es lo razonable. Pero el código suma `time.Since(scanStart)` (que son segundos al final del scan) y luego suma 1 minuto. Equivalente pero acertijo.

**Acción**: cambiar a cutoff fijo legible:

```go
const autoDetectStaleAfter = 5 * time.Minute

// Apps no vistas en este scan: si no se detectaron en ~5min, considerar
// que el user las desinstaló manualmente vía apt y limpiar de la DB.
removed, err := appsRepo.DeleteStaleAutoDetected(ctx, autoDetectStaleAfter)
```

---

#### APP-052 🟢 P3 · `getAppPort` usa `context.Background()`

**Archivo**: `daemon/appproxy.go:203-212`
**Esfuerzo**: XS
**Tipo**: Refactor

```go
func getAppPort(appId string) int {
    if appsRepo == nil {
        return 0
    }
    app, err := appsRepo.GetDockerApp(context.Background(), appId)
    ...
}
```

Pierde la cancelación del request. Si el cliente desconecta, la query SQLite no se cancela.

**Acción**: propagar context desde el caller. Cambio de signature: `getAppPort(ctx context.Context, appId string) int`.

---

#### APP-053 🟠 P1 · `dockerPull` síncrono

**Archivo**: `daemon/docker.go:1288-1309`
**Esfuerzo**: S
**Tipo**: Bug

Pulls de imágenes pueden tardar minutos (GBs). Handler HTTP bloquea ese tiempo. Cliente HTTP timeout → operación queda en limbo.

**Acción**: async via `nimos_operations` (APP-012). Devolver `request_id` inmediato, pull en goroutine, status pollable.

---

#### APP-054 🟡 P2 · `runSafe` traga errores en `dockerInstall`

**Archivo**: `daemon/docker.go:786-815`
**Esfuerzo**: XS
**Tipo**: Bug

Muchas llamadas `runSafe` sin chequear el `ok` (chmod, chown, setfacl, usermod). Si fallan, el sistema queda en estado inconsistente silenciosamente.

**Acción**: en `dockerInstall`, las operaciones de permisos deben ser críticas. Si `chown` falla, la share `docker-apps` no funcionará. Loggear con nivel warn + acumular en respuesta:

```go
var warnings []string
if _, ok := runSafe("chown", "root:"+shareGroup, dockerSharePath); !ok {
    warnings = append(warnings, "Failed to chown docker share directory")
    logMsg("docker: install: chown share dir failed (continuing)")
}
// ...
jsonOk(w, map[string]interface{}{"ok": true, "warnings": warnings})
```

---

#### APP-055 🟡 P2 · `docker.json` sin locking

**Archivo**: `daemon/docker.go:24-45`
**Esfuerzo**: S
**Tipo**: Bug

`getDockerConfigGo` y `saveDockerConfigGo` operan sobre archivo sin file lock ni mutex. Dos requests concurrentes que escriban → última gana, datos perdidos.

**Acción**: si APP-011 (migración a SQLite) ocurre, este se resuelve solo. Si no, añadir `sync.Mutex` global del módulo.

---

### Categoría G — Seguridad

#### APP-060 🟢 P3 · WebSocket proxy handshake manual

**Archivo**: `daemon/appproxy.go:146-200`
**Esfuerzo**: S
**Tipo**: Security

**Descripción**:

El proxy WebSocket hace handshake escribiendo HTTP a pelo:

```go
fmt.Fprintf(backendConn, "%s %s HTTP/1.1\r\n", r.Method, targetPath)
for key, values := range r.Header {
    for _, value := range values {
        fmt.Fprintf(backendConn, "%s: %s\r\n", key, value)
    }
}
```

Si un header contiene `\r\n` inyectado por el cliente, hay riesgo de HTTP smuggling.

**Acción**: validar headers antes de escribirlos (`strings.ContainsAny(value, "\r\n")` → reject) o, mejor, usar `httputil.NewSingleHostReverseProxy` con custom Director que sí soporte WebSocket upgrade vía `http.Hijacker`.

---

#### APP-061 🟢 P3 · Cookie scoping cross-app

**Archivo**: `daemon/appproxy.go:107-117`
**Esfuerzo**: S
**Tipo**: Security

Cookies de apps proxieadas se setean en el dominio de NimOS. Si app A setea cookie con `Path=/`, app B la lee.

**Acción**: rewrite cookies en respuesta — añadir `Path=/app/{appId}/` o renombrar a `{appId}_{cookieName}`.

---

#### APP-062 🟢 P3 · Headers passthrough sin filtro

**Archivo**: `daemon/appproxy.go:84-89`
**Esfuerzo**: XS
**Tipo**: Security

Todos los headers de request se pasan al backend incluyendo `Authorization`. Si el user NimOS está autenticado con un Authorization Bearer, la app interna lo recibe. Probablemente no es problema (el container es local) pero merece chequeo explícito.

**Acción**: lista blanca de headers o blacklist de `Authorization`, `Cookie` (re-añadidos selectivamente).

---

#### APP-063 🟢 P3 · `/var/lib/docker` borrado sin backup

**Archivo**: `daemon/docker.go:768`
**Esfuerzo**: XS
**Tipo**: Security

```go
runSafe("rm", "-rf", "/var/lib/docker")
```

Si Docker tenía data antes de la instalación NimOS (ej. user con Docker pre-existente probando), se pierde sin advertencia.

**Acción**: detectar si `/var/lib/docker` no está vacío antes del rm. Si no lo está, abortar instalación con error claro "/var/lib/docker contiene data, mueve o borra manualmente antes de continuar".

---

### Categoría H — Documentación

#### APP-070 🟢 P3 · Health del engine no refleja agregate en `service_instances`

**Archivos**:
- `daemon/services.go:724-729` (reconcileOneInstance escribe solo daemon health)
- `daemon/nimhealth_observer.go:228-232` (enrichDockerInstance sobreescribe en response)

**Esfuerzo**: XS
**Tipo**: Doc

**Acción**: comentario explícito en ambos lugares + en el SQL del schema:

```go
// NOTA: la columna service_instances.health para Docker engine (AppID="containers")
// refleja SOLO si el daemon Docker está activo. El health agregado (que considera
// children) se calcula on-demand en enrichDockerInstance() y se devuelve en
// /api/services. Cualquier consumidor que lea la columna directamente verá un
// valor incompleto. Use el endpoint, no la tabla.
```

---

#### APP-071 🟢 P3 · Convención `/nimos/pools/` no documentada

Convención asumida por `findPoolFromPath`, `nimosPoolsDir`, `detectDockerEngine`, varios. No hay doc.

**Acción**: sección en `README.md` o `documents/CONVENTIONS.md` (nuevo) describiendo:
- Estructura `/nimos/pools/{pool_name}/...`
- Implicaciones: pool name como path component, restricciones de chars

---

#### APP-072 🟢 P3 · Heurística stack-matching no documentada

`getDockerAppStatuses` matchea containers por sufijos `_server`, `-server`, `_app`, `-app` y luego prefijos. Casos de fallo no documentados.

**Acción**: docstring en función con tabla de casos:

```
App ID        Container name           Match
─────────────────────────────────────────────
jellyfin      jellyfin                 exact
immich        immich_server            suffix _server
homeassistant homeassistant_app        suffix _app
nextcloud     nextcloud-fpm-1          prefix nextcloud-
plex          plex                     exact

NO MATCH:
- App id "media" si container es "jellyfin" (sin relación)
- Container con suffix custom no listado
```

---

### Categoría I — Testing

#### APP-080 🟢 P3 · Sin test de cruce con stacks reales

**Esfuerzo**: S
**Tipo**: Test

Tests existen para `db_apps.go` (CRUD puro) y `breaker.go`. No hay test de `getDockerAppStatuses` con casos reales: stack Immich con 4 sub-containers, app stopped, orphan container, etc.

**Acción**: `nimhealth_docker_test.go` con mock de `runSafe("docker", "ps", "-a", ...)` que devuelva fixtures.

---

#### APP-081 🟢 P3 · Sin test de race en uninstall

**Esfuerzo**: XS

Tras implementar APP-031 (flag `deleting`), test que verifique:
- DELETE marca flag
- `getDockerAppStatuses` filtra rows con flag
- Goroutine final hace DELETE real

---

#### APP-082 🟢 P3 · Sin test de auto-registro post-install

**Esfuerzo**: XS

Test integration que:
- Setup: DB limpia, docker.json simulado con `installed=true, path="/nimos/pools/test/docker"`
- Run: `runAutoRegister(ctx)`
- Assert: `service_instances` contiene `docker@test` con `AppID=containers`

---

## 4 · Plan de ejecución

### Principios de ordenación

1. **P0 antes de todo lo demás**. Un solo item: APP-001.
2. **Limpieza de raíces antes de hojas**. APP-011/012/013 son raíces del que cuelgan otros.
3. **Contrato antes de feature**. APP-030/031/033 garantizan que cada install se registra bien — base para confiar en el frontend nuevo.
4. **Disciplina antes de catálogo**. APP-020/023/024 dan el blindaje que el catálogo controlado necesita (APP-045).

### Fases

#### Fase 0 — Hotfix (1 sesión)

Bloquea cualquier release Beta 8.1.

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-001 | Deshabilitar endpoint `rebuild` o reescribir con compose `--force-recreate` | S |
| APP-050 | `git rm` los 3 archivos duplicados en raíz | XS |
| APP-063 | Detectar `/var/lib/docker` no vacío en `dockerInstall` | XS |

**Salida**: tag `beta-8.1.1` deployable.

---

#### Fase 1 — Blindar contrato AppStore → NimHealth (1-2 sesiones)

Garantiza que cada app instalada queda correctamente registrada y observada.

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-013 | Eliminar registro síncrono en `dockerInstall`, dejar único a `detectDockerEngine` | S |
| APP-030 | Logging defensivo en `findPoolFromPath` falla | XS |
| APP-031 | Flag `deleting` en `docker_apps` + filtro en `getDockerAppStatuses` | XS |
| APP-032 | Validar `Type ∈ {container, stack}` en repo | XS |
| APP-033 | Columna `ports_json` + migration + serialize/deserialize | S |
| APP-034 | `forceDockerCacheRefresh` invocado tras deploy/delete/action | S |
| APP-080 | Tests de `getDockerAppStatuses` con fixtures | S |
| APP-082 | Test de auto-registro post-install | XS |

**Salida**: contrato install→register documentado y verificado por tests.

---

#### Fase 2 — Migración a SQLite, deprecación de legacy (2 sesiones)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-011 | Tabla `docker_config` + `docker_permissions` + `docker_app_permissions` + migration | M |
| APP-012 | Tabla `nimos_operations` + helpers | M |
| APP-014 | `dockerInstall` async usando `nimos_operations` | M |
| APP-053 | `dockerPull` async | S |
| APP-010 | Nuevo endpoint `/api/appstore/installed`, marcar `/api/installed-apps` deprecado | M |
| APP-017 | Extraer `matchContainerByAppID` a función única | XS |
| APP-055 | Locking de `docker.json` (resuelto por APP-011 si se completa) | S |

**Salida**: fuente única de verdad (SQLite), handlers async, código sin duplicación legacy.

---

#### Fase 3 — Aplicar disciplina v2 (1-2 sesiones)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-020 | Breaker `dockerhub.pull` envolviendo `dockerPull` y stack deploy | S |
| APP-021 | Breaker `dockerhub.install_script` | XS |
| APP-022 | Breaker o background job para iconos | XS |
| APP-023 | Events `appstore.*` con dedupe + rate limit | S |
| APP-024 | `detectDockerCapabilities` + endpoint capabilities | S |
| APP-025 | Update `APPSTORE-RESCUE-PLAN.md` con HealthStatus canónico | XS |

**Salida**: módulo AppStore alineado con `NIMOS_DISCIPLINE.md` v2 al completo.

---

#### Fase 4 — Frontend AppStore v3 + endpoints UX (2-3 sesiones)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-040 | Reescribir `dockerInstall` como "Docker como app del catálogo" | L |
| APP-041 | Pool selector | S |
| APP-042 | Endpoint `PATCH /api/installed-apps/:id` | XS |
| APP-043 | "Switch to external" en WebApp.svelte | S |
| APP-044 | Port completo de `AppStore.svelte` | L |
| APP-045 | `catalog.json v2` + repo `NimOs-appstore` | M |

**Salida**: AppStore funcional al nivel de Beta 7, en design system v3, con catálogo controlado.

---

#### Fase 5 — Limpieza y refinamiento (1 sesión)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-015 | Eliminar hardcode de Status/Health en service registry | XS |
| APP-016 | Extraer firewall y hardware drivers de `docker.go` | S |
| APP-035 | Comentario en `refreshDockerCache` + validación en install | XS |
| APP-051 | Cutoff fijo legible en `bootstrapNativeApps` | XS |
| APP-052 | Propagar context en `getAppPort` | XS |
| APP-054 | Logging + warnings en `dockerInstall` | XS |

**Salida**: código limpio y predecible, cohesión correcta por archivo.

---

#### Fase 6 — Seguridad (1 sesión)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-060 | Sanitize CRLF en WebSocket handshake | S |
| APP-061 | Rewrite cookies en reverse proxy | S |
| APP-062 | Filtrar `Authorization`/`Cookie` en passthrough | XS |

**Salida**: reverse proxy reforzado.

---

#### Fase 7 — Documentación y tests (0.5 sesión)

| ID | Acción | Esfuerzo |
|---|---|---|
| APP-070 | Comentarios sobre health agregado | XS |
| APP-071 | `documents/CONVENTIONS.md` con `/nimos/pools/` | XS |
| APP-072 | Docstring de `matchContainerByAppID` | XS |
| APP-081 | Test de race en uninstall | XS |

**Salida**: documentación al día, tests del flujo crítico.

---

## 5 · Matriz de dependencias

Items que tienen prerequisito explícito:

| Item | Prerequisito |
|---|---|
| APP-014 | APP-012 (necesita `nimos_operations`) |
| APP-053 | APP-012 |
| APP-031 (opción B) | Migration de schema (S por sí solo) |
| APP-034 | APP-013 (cache invalidation post-install necesita single point of registry) |
| APP-040 | APP-014 + APP-011 (cambio a async + sin docker.json) |
| APP-044 | APP-010 (frontend nuevo debe consumir endpoint nuevo) + APP-042 (openMode) |
| APP-045 | APP-040 (docker-engine como app del catálogo) |
| APP-055 | obsoleto si APP-011 |

---

## 6 · Métricas de seguimiento

Sugeridas para tracking de progreso durante ejecución:

| Métrica | Cómo se mide | Objetivo |
|---|---|---|
| Items P0 abiertos | Grep en este doc | 0 antes de Beta 8.2 |
| Items P1 abiertos | Idem | <3 antes de Beta 9 |
| Duplicación de cruce docker_apps × docker ps | `grep -rn "docker ps" daemon/` | 1 ocurrencia (en `getDockerAppStatuses`) |
| Endpoints `/api/installed-apps` activos | Curl + verificar `X-Deprecated` | 0 sin header al final de fase 2 |
| Cobertura test de `nimhealth_docker.go` | `go test -cover` | >70% |
| Líneas de `docker.go` | `wc -l` | <1000 (hoy 1501) |
| Breakers configurados en AppStore | `grep -c "NewCircuitBreaker.*dockerhub\|appstore" daemon/` | 3 (pull, install_script, icons) |

---

## 7 · Apéndices

### Apéndice A · Archivos del módulo

**Backend principal**:

| Archivo | LoC | Responsabilidad |
|---|---|---|
| `daemon/apps.go` | 646 | HTTP handlers native apps + CRUD docker apps registry |
| `daemon/apps_schema.sql` | 63 | Schema docker_apps + native_apps |
| `daemon/db_apps.go` | 458 | Repository |
| `daemon/docker.go` | 1501 | Install, stack deploy, container CRUD, permissions, firewall (out-of-place), hardware (out-of-place) |
| `daemon/appproxy.go` | 213 | Reverse proxy `/app/{id}/*` |

**Integración NimHealth**:

| Archivo | LoC | Responsabilidad |
|---|---|---|
| `daemon/models.go` (sección 500-625) | 125 | `ServiceBase`, `DockerAppStatus`, `PortBinding`, normalizers |
| `daemon/nimhealth_docker.go` | 274 | `getDockerAppStatuses`, `ComputeDockerAggregateHealth` |
| `daemon/nimhealth_observer.go` | 249 | Observer reconciler + `dockerCache` |
| `daemon/nimhealth_detectors.go` | 263 | Auto-detector de services |
| `daemon/nimhealth.go` | 423 | `/api/services` endpoint + `enrichDockerInstance` |
| `daemon/services.go` (sección 580-743) | 163 | `reconcileServices` + `reconcileOneInstance` |

**Frontend**:

| Archivo | LoC | Estado |
|---|---|---|
| `src/lib/apps/AppStore.svelte` | 124 | Stub explícito |
| Beta 7 reference | N/A | A portar |

### Apéndice B · Endpoints expuestos hoy

| Método | Path | Handler | Estado |
|---|---|---|---|
| GET | `/api/installed-apps` | `dockerInstalledApps` | A deprecar |
| POST | `/api/installed-apps` | `handleInstalledAppsRoutes` | A reemplazar |
| DELETE | `/api/installed-apps/:id` | idem | A reemplazar |
| GET | `/api/native-apps` | `handleNativeAppsRoutes` | OK |
| GET | `/api/native-apps/available` | idem | OK |
| POST | `/api/native-apps/:id/install` | `nativeAppInstall` | Async via JSON file — migrar a operations |
| GET | `/api/native-apps/:id/install-status` | `nativeAppInstallStatus` | A reemplazar |
| GET | `/api/docker/status` | `dockerStatus` | OK |
| POST | `/api/docker/install` | `dockerInstall` | Bloqueante — async |
| POST | `/api/docker/uninstall` | `dockerUninstall` | Revisar |
| POST | `/api/docker/stack` | `dockerStackDeploy` | Falta breaker |
| POST | `/api/docker/container/create` | `dockerContainerCreate` | Falta breaker + multi-port |
| POST | `/api/docker/container/:id/:action` | `dockerContainerAction` | OK |
| DELETE | `/api/docker/container/:id` | `dockerContainerDelete` | Race APP-031 |
| GET | `/api/docker/container/:id/mounts` | `dockerContainerMounts` | OK |
| POST | `/api/docker/container/:id/rebuild` | `dockerContainerRebuild` | 🔴 APP-001 |
| POST | `/api/docker/pull/{image}` | `dockerPull` | Bloqueante + sin breaker |

### Apéndice C · Tablas SQLite del módulo

Existentes:

- `docker_apps` (apps Docker instaladas — config persistente)
- `native_apps` (apps Linux nativas — autodetect + manual)
- `service_instances` + `service_dependencies` (registry de servicios)
- `app_registry` (definición de apps + permisos)

A añadir (resultado del plan):

- `docker_config` (singleton, reemplaza `docker.json`)
- `docker_permissions` (matriz user → docker)
- `docker_app_permissions` (matriz app → user)
- `nimos_operations` (jobs async con tracking)

### Apéndice D · Referencias

- `documents/APPSTORE-RESCUE-PLAN.md` — visión funcional del módulo
- `NIMOS_DISCIPLINE.md` v2 — guardrails arquitectónicos
- `documents/NIMHEALTH-UX-SPEC-v1.md` — UX de NimHealth (donde aparecen las Docker apps como children)
- `documents/NIMSHIELD-ARCHITECTURE-v3.md` — referencia de aplicación correcta de patrones disciplina

---

## 8 · Cierre

Este documento es **vivo**. Tras cada fase ejecutada:

1. Marcar items como `[CLOSED]` con commit hash
2. Anotar desviaciones del plan
3. Reabrir items si la solución introdujo regresión

Para discusión arquitectónica que afecte a este audit: actualizar `NIMOS_DISCIPLINE.md` primero, luego revalidar items aquí.

---

**FIN DEL DOCUMENTO**
