# NimOS · AppStore Audit · Cierre de Fase 2

**Fecha de cierre**: 24/05/2026
**Versión sobre la que se trabajó**: Beta 8.1
**Punto de partida**: documento [APPSTORE-AUDIT-v1.md](./APPSTORE-AUDIT-v1.md) · 41 items catalogados APP-001 a APP-082
**Estado final**: 14 items cerrados · 27 items aplazados con justificación
**Decisión de cierre**: parar aquí intencionadamente, lo crítico está blindado

---

## Resumen ejecutivo

Antes de este trabajo, el módulo AppStore era funcional pero con tres clases de problemas: un bug crítico (`dockerContainerRebuild` perdía flags silenciosamente), inconsistencias estructurales entre la fuente de verdad y el observer NimHealth, y arquitectura síncrona para operaciones que tardan minutos.

Tras seis batches a lo largo de tres fases, esos tres problemas están resueltos:

1. **El bug crítico** (APP-001) tiene tres caminos · stack se reconstruye correctamente, container devuelve 501 Not Implemented con ticket reservado para futuro, y el body de la respuesta es JSON estructurado.

2. **El contrato AppStore→NimHealth** está blindado a nivel BD + handler + observer · single source of truth, race-free uninstall, multi-port persistente, validación de transiciones de estado, cache invalidation explícita post-mutación.

3. **El patrón async** está disponible para los dos handlers críticos (install, pull) con compat 100% backward · sin flag funciona igual que antes, con `?async=true` se comporta como API moderna.

El resto del audit eran items que mejoran el código sin resolver problemas reportados. Esos quedan en backlog priorizado al final de este documento.

---

## Items cerrados (14)

### Fase 0 · Hotfix inmediato

| ID | Severidad | Descripción | Aplicado |
|---|---|---|---|
| APP-001 | 🔴 P0 | `dockerContainerRebuild` perdía flags silenciosamente | Stack usa `docker compose --force-recreate`; container devuelve 501 con ticket APP-001-B |
| APP-063 | 🟢 P3 | Falta protección contra `rm -rf /var/lib/docker` con datos ajenos | Helper `dockerVarLibHasData()` + check 409 al inicio de `dockerInstall` |
| APP-050 | 🟢 P3 | Archivos duplicados en raíz del repo | Cerrado por Andrés antes de la sesión |

### Fase 1 · Contrato AppStore→NimHealth

| ID | Severidad | Descripción | Aplicado |
|---|---|---|---|
| APP-013 | 🟠 P1 | Doble registro del Docker engine (handler + detector) | Eliminado `dbServiceRegister` síncrono; canonical único = `detectDockerEngine` via `runAutoRegister` + `reconcileServices` + `ForceDockerCacheRefresh` |
| APP-030 | 🟠 P1 | `findPoolFromPath` fallaba silenciosamente | Log explícito accionable cuando `installed=true` pero el path no resuelve |
| APP-031 | 🟠 P1 | Race entre uninstall y observer (orphan flicker) | Columna `deleting` + `MarkDockerAppDeleting` + filtros en list/count/orphan-count |
| APP-032 | 🟡 P2 | Type y OpenMode aceptaban valores arbitrarios | Validación en `CreateOrUpdateDockerApp` · enum strict |
| APP-033 | 🟠 P1 | Multi-port no persistía (solo primer port en BD) | Columna `ports_json` + método `parsedPorts()` con fallback a `Port` legacy |
| APP-034 | 🟡 P2 | Gap UX ≤30s entre install y aparición en NimHealth | `ForceDockerCacheRefresh()` invocado tras cada mutación · sync en install/create, async en action |

### Fase 2 · Endpoints + async

| ID | Severidad | Descripción | Aplicado |
|---|---|---|---|
| APP-010 | 🟠 P1 | Endpoints legacy `/api/installed-apps` y `/api/docker/installed-apps` | Headers RFC 8594 `Deprecation: true` + `Link: rel="successor-version"` apuntando a `/api/services` |
| APP-017 | 🟡 P2 | Heurística de stack matching duplicada con divergencia (3 vs 6 keywords) | Extraída a `matchContainerForAppID` + `isPossibleStackSubContainer` · single source en `nimhealth_docker.go` |
| APP-012 | 🟠 P1 | Sin tabla para tracking de operations async | Tabla `nimos_operations` + `OperationsRepo` con state machine validada + endpoints HTTP `/api/operations` |
| APP-014 | 🟠 P1 | `dockerInstall` síncrono bloqueante 30s-5min | Refactor wrapper + worker · flag `?async=true` devuelve 202 + operationId |
| APP-053 | 🟠 P1 | `dockerPull` síncrono bloqueante 10s-2min | Mismo patrón que APP-014 |

---

## Items aplazados (27) · backlog con justificación

### APP-011 + APP-055 · `docker.json` → SQLite

**Razón de aplazar**: APP-011 era el item de mayor riesgo del audit. Migrar la fuente de verdad de Docker config (filesystem → SQLite) implica importación al arranque, riesgo de divergencia transitoria, y si peta deja el módulo sin saber qué tiene instalado. El beneficio es "consistencia con el resto del módulo" — exactamente la motivación que disciplina v2 §1 desaconseja sin problema concreto que la justifique.

**Cuándo retomar**: solo si aparece un problema operacional concreto que `docker.json` no pueda resolver. Si nunca aparece, mejor no migrar.

**Items afectados**: APP-011 (P1), APP-055 (P2, bloqueado por APP-011)

### Fase 3 · CircuitBreakers + events

**Items**: APP-020, APP-021, APP-022, APP-023, APP-024, APP-025

**Razón de aplazar**: ningún cliente externo (Docker Hub, get.docker.com, registries de iconos) ha causado problemas observados en producción. Los CircuitBreaker son nice-to-have hasta que se demuestre lo contrario. Events `appstore.*` son útiles para auditoría pero no resuelven problemas hoy.

**Cuándo retomar**: si aparece una indisponibilidad de Docker Hub que cuelga el daemon, o si se necesita audit log de instalaciones por compliance.

### Fase 4 · Frontend

**Items**: APP-040, APP-041, APP-043, APP-044, APP-045

**Razón de aplazar**: delegada a la instancia paralela de Claude que está rehaciendo el frontend del AppStore. Esta sesión se centró en backend.

**Cuándo retomar**: cuando la instancia frontend tenga preguntas que requieran cambios coordinados de backend.

### Fase 5 · Limpieza interna

**Items**: APP-015, APP-016, APP-035, APP-051, APP-052, APP-054

**Razón de aplazar**: limpieza de código sin impacto funcional. Extraer firewall y hardware drivers fuera de `docker.go` (1700+ líneas), refactorizar `bootstrapNativeApps` con cutoff fijo, propagar context en `getAppPort`. Mejoras de mantenibilidad útiles pero no urgentes.

**Cuándo retomar**: cuando un cambio futuro en alguna de esas áreas se vuelva difícil por la falta de limpieza. La limpieza preventiva sin demanda específica es exactamente lo que disciplina v2 §1 desaconseja.

### Fase 6 · Seguridad reverse proxy

**Items**: APP-060, APP-061, APP-062

**Razón de aplazar**: técnicamente son cosas reales (WebSocket CRLF sanitize, cookie scoping, header filter), pero conceptualmente no son parte del audit AppStore — son hardening del reverse proxy. Conviene tratarlas como mini-tarea independiente, no como fase 6 del audit.

**Cuándo retomar**: como tarea de seguridad aparte. Probablemente antes que las fases 3 y 5 si quieres priorizar por riesgo real.

### Fase 7 · Docs + tests E2E

**Items**: APP-070, APP-071, APP-072, APP-080, APP-081, APP-082

**Razón de aplazar**: tests de cruce con `docker ps` real requieren infraestructura de mock de `runSafe` que no existe en NimOS hoy. Construirla es trabajo significativo que solo se justifica si los tests de repo dejan de capturar bugs reales.

**Cuándo retomar**: cuando aparezca un bug en producción que un test E2E hubiera capturado. Ahí construyes la infraestructura para ese bug y vas ampliando.

---

## Métricas del trabajo realizado

| Métrica | Valor |
|---|---|
| Sesiones | 1 |
| Batches aplicados | 6 (Fase 0 + Fase 1 Batch 1-2 + Fase 2 Batch 1-3) |
| Hotfixes adicionales | 1 (índice `deleting` en SQL embed) |
| Archivos modificados | 10 (`docker.go`, `apps.go`, `apps_schema.sql/.go`, `db_apps.go`, `nimhealth_docker.go`, `nimhealth_observer.go`, `db.go`, `http.go`, `nimhealth_detectors.go`) |
| Archivos creados | 6 (`docker_async.go/_test.go`, `operations_schema.sql/.go`, `db_operations.go`, `db_operations_test.go`, `operations_http.go`) |
| Tests añadidos | ~50 funciones (algunas con subtests, ~80 casos totales) |
| Migraciones SQL | 3 columnas + 1 índice + 1 tabla + 4 índices |
| Endpoints nuevos | 2 (`/api/operations`, `/api/operations/{id}`) |
| Endpoints deprecados | 2 (`/api/installed-apps GET`, `/api/docker/installed-apps`) |
| Líneas Go añadidas | ~2000 (incluye tests) |
| Líneas Go refactorizadas | ~400 |

---

## Decisiones técnicas relevantes (con justificación para la futura yo/instancia)

### Por qué flag `?async=true` y no endpoint nuevo `/api/.../async`

Endpoint separado duplica handlers de auth/validación. El flag mantiene un único endpoint canónico y deja "cómo quieres la respuesta" como metadata del request, que es donde semánticamente pertenece. Coste: añadir 5 líneas de fork al handler. Beneficio: cero duplicación.

### Por qué `OpsStatus*` y no `OpStatus*` en `db_operations.go`

`storage_types.go` ya declaraba `OpStatusPending/Failed/Cancelled` para operaciones de storage (con campos diferentes: `InProgress`, `Completed`, `RolledBack`). Prefijo `Ops*` distingue sin tocar storage. Los valores string en BD ("pending", "failed", "cancelled") sí coinciden — pero viven en tablas distintas (`storage_operations` vs `nimos_operations`), así que sin colisión real.

### Por qué deprecation headers en lugar de eliminar endpoints legacy

El frontend nuevo está en construcción en paralelo. Eliminar endpoints rompe el frontend actual antes de que el nuevo esté listo. RFC 8594 (`Deprecation: true` + `Link: rel="successor-version"`) es el standard web para migración gradual: clientes que leen headers saben qué hacer; clientes legacy siguen funcionando hasta que se elimine en una versión futura.

### Por qué el índice `idx_docker_apps_deleting` se crea en `.go` y no en `.sql`

Lección dura del hotfix post-Batch-2. En upgrades sobre BD existente, el SQL embed se ejecuta **antes** de las migrations `ALTER TABLE`. Un `CREATE INDEX` sobre columna añadida por migration falla porque la columna aún no existe. Regla general: índices sobre columnas añadidas por migration van en el `.go` de migration, no en el `.sql` embed.

### Por qué el ToMap de `DBDockerApp` mantiene el campo `port` legacy además de `ports`

Compatibilidad 100% con clientes que leen el campo singular. El array `ports` (multi) es aditivo. Cuando el frontend nuevo migre completamente, se puede deprecar `port` y luego eliminarlo. Patrón consistente con la deprecation gradual del resto del módulo.

### Por qué la API de `OperationsRepo` no expone POST/DELETE por HTTP

Las operations se crean SOLO desde dentro del backend tras validar que la operación real procede. Esto previene abuse via API (no se pueden crear ops vacías) y evita que clientes maliciosos llenen la tabla. DELETE se omite porque la limpieza es vía expiry + GC interno.

### Por qué `runWorkerAsync` usa `context.Background()` y no propaga el ctx del request

En modo async, el HTTP request termina inmediatamente con 202. Su `r.Context()` se cancela. Si el worker async lo usara, todas las operaciones `ctx`-aware (storageService, runAutoRegister, etc.) abortarían inmediatamente. Background ctx es el correcto para work que sobrevive al request HTTP.

---

## Limitaciones conocidas tras Fase 2

Cosas que el módulo AppStore NO hace tras este trabajo, documentadas para que nadie se sorprenda:

1. **`docker.json` sigue siendo la fuente de verdad del estado del Docker engine**. No hay tablas SQLite para esa config. Funciona, pero diverge del resto del módulo. Cambio aplazado (APP-011).

2. **Operations async no tienen progress reporting fino**. Los workers reportan progreso en pasos discretos (cada 10-20%), no continuo. Para Docker pull el progreso real existe en `docker pull` output pero no se parsea. Trade-off aceptado por simplicidad.

3. **Operations async no se cancelan**. El state machine reserva `OpsStatusCancelled` pero no hay endpoint ni mecanismo para invocarlo. Si un install se ejecuta, va hasta el final o falla. Caso de uso futuro.

4. **GC de `nimos_operations` no se ejecuta automáticamente**. `DeleteExpired` existe pero no hay Reconciler que lo invoque. Las ops terminadas se acumulan hasta que alguien las borre manualmente. No es urgente: ~1KB por op, con uso normal son <100/mes.

5. **Tests E2E del cruce con `docker ps` real no existen**. Los tests de repo cubren la BD, los tests de helpers cubren la lógica pura, pero ningún test simula "docker engine + apps + un container que falla y aparece en observer". Pendiente fase 7.

6. **Endpoint `dockerContainerRebuild` para containers individuales devuelve 501**. Reservado bajo ticket APP-001-B para cuando aparezca un caso de uso real. Stacks sí rebuildea correctamente.

7. **`POST /api/installed-apps` y `DELETE /api/installed-apps/:id` no están deprecados**. Solo el GET. El plan de migración para registros manuales se decide cuando el frontend nuevo esté listo.

---

## Cuándo volver al audit

Indicadores que justifican abrir items aplazados:

| Síntoma | Item aplazado a retomar |
|---|---|
| Daemon se cuelga durante install porque get.docker.com no responde | APP-020 (CircuitBreaker) |
| Compliance pide audit log de instalaciones | APP-023 (events `appstore.*`) |
| Imposible debuggear un bug porque `docker.json` y BD divergen | APP-011 (migración a SQLite) |
| Cambiar `docker.go` se vuelve frágil porque mezcla demasiados dominios | APP-015 + APP-016 (extraer drivers) |
| Bug en producción que un test E2E hubiera capturado | APP-080-082 (infra de mock + tests) |
| Vulnerabilidad reportada en reverse proxy | APP-060/061/062 (no es continuación del audit, mini-tarea aparte) |

Si ninguno de estos síntomas aparece, no hay que volver al audit. El AppStore funciona como debe.

---

## Estado del módulo AppStore tras este trabajo

**Lo que el módulo hace bien ahora**:

- Instalar Docker engine con protección de datos preexistentes (APP-063), registro canónico único (APP-013), validación de pool, gap UX cerrado (APP-034)
- Instalar apps Docker (containers y stacks) con multi-port persistente, validación de Type/OpenMode, share automático
- Desinstalar apps sin race window que cause orphan flicker en NimHealth
- Operaciones async opcionales con polling estándar (`/api/operations/{id}`)
- Cruce limpio con `docker ps` usando heurística single-source
- Headers de deprecation marcando el camino a `/api/services` como canónico

**Lo que el módulo NO necesita hacer mejor (decisión explícita)**:

- Migrar `docker.json` a SQLite (funciona como está)
- Tener events o audit log (no requerido)
- Tener CircuitBreaker sobre Docker Hub (no es problema observado)
- Tener tests E2E con docker real (caro de construir, los de repo + lógica pura cubren lo importante)

**Lo que sigue siendo trabajo del producto, no del audit**:

- Frontend del AppStore (responsabilidad de la instancia paralela)
- Hardening de seguridad del reverse proxy (mini-tarea aparte de Fase 6)
- BTRFS-only migration (otra área de NimOS, no AppStore)
- 47 bugs documentados en otras áreas

---

## Referencia rápida de los entregables de la sesión

Patches en `/mnt/user-data/outputs/` listos para `git apply`:

```
fase0-hotfix.patch              · APP-001 + APP-063
fase1-batch1.patch              · APP-013 + APP-030 + APP-032
fase1-batch2.patch              · APP-031 + APP-033 + APP-034 + refine APP-013
fase1-batch2-hotfix.patch       · fix CREATE INDEX deleting en upgrades
fase2-batch1.patch              · APP-010 + APP-017
fase2-batch2.patch              · APP-012 (con OpsStatus* prefix correcto)
fase2-batch3.patch              · APP-014 + APP-053
```

Changelog por batch:

```
FASE-0-CHANGELOG.md
FASE-1-BATCH-1-CHANGELOG.md
FASE-1-BATCH-2-CHANGELOG.md
FASE-2-BATCH-1-CHANGELOG.md
FASE-2-BATCH-2-CHANGELOG.md
FASE-2-BATCH-3-CHANGELOG.md
```

Audit original:

```
APPSTORE-AUDIT-v1.md            · 41 items APP-001 a APP-082, plan 7 fases
```

Este documento:

```
APPSTORE-AUDIT-CIERRE-FASE-2.md · balance final + backlog priorizado
```
