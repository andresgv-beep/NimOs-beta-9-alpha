# NIMHEALTH · Integration v1.2 — Plan correctivo

**Tipo**: Documento de plan correctivo
**Audiencia**: Andrés + Claude
**Estado**: APROBADO · listo para implementar
**Versión**: v1.2 (24/05/2026)
**Disciplina base**: `NIMOS_DISCIPLINE.md` v2
**Versiones anteriores**: `NIMHEALTH-INTEGRATION-v1.md`, `v1.1.md` (históricos)
**Antecedentes**: NIMHEALTH-v1.0 / v1.1 / v1.2 (sprint inicial) — superados por descubrimiento de arquitectura previa

---

## CAMBIOS v1.1 → v1.2

```
Decisiones tras revisión arquitectónica (24/05/2026):

ANDRÉS señaló 3 riesgos arquitectónicos visibles:

  · app_registry como tabla universal
  · Lógica runtime/observer/DB mezclada en mindset SQLite-centric
  · Boot storm potencial · 9 starters concurrentes en main.go

CLAUDE inicialmente propuso ampliar el sprint con ServiceRepo
y separación de tablas. Tras autocrítica honesta, decidió RECHAZAR
esa expansión por scope creep autoinfligido.

Decisión consolidada: NO se añade NADA al sprint. Las 3 deudas
se documentan en §9 con criterios objetivos para actuar.

  · NO ServiceRepo solo para NimHealth (sería incoherencia activa:
    NimHealth con Repo, otros módulos con acceso directo a SQLite).
    Si se adopta el patrón, debe ser sprint propio que migre TODOS
    los consumidores de service_instances a la vez.

  · NO separación de app_registry. Deuda estática, no crece con
    el tiempo. Coste real 2-3 sprints; coste de no actuar hoy: cero.

  · NO stagger de starters en main.go. Sospecha sin métricas.
    Antes de proponer solución, medir en Pi 4 real.

Resultado: sprint INTEGRATION sigue siendo 8 pasos. +1 sección §9.
```

---

## CAMBIOS v1 → v1.1

```
Refinamiento tras revisión técnica (24/05/2026):

Q-001 · Conformidad con HealthStatus enum global

Aclaración del Paso 5 (migration v3). v1 proponía mapear
'incomplete' → 'partial' en CASE. Eso era mapeo LOSSY:

  · incomplete = devices físicos del array faltan (RAID degraded)
  · partial    = devices presentes pero el FS no monta (estado raro)

Son distinciones reales que el código de storage_btrfs_probe.go
mantiene (computeObservationHealth). Mapear uno a otro perdería
información.

Verificación en código (v1.1 evidencia con grep):
  · HealthIncomplete se usa SOLO en storage_btrfs_probe.go
  · NimHealth (este sprint) NUNCA genera 'incomplete'
  · Storage NO persiste el campo en SQLite (campo runtime, no DB)
  · network_observed schema CHECK acepta 6 valores: healthy,
    degraded, failed, partial, unknown, stale (NO incluye incomplete)

Decisión consolidada (sección §4.7 nueva):

  · NimHealth usa los MISMOS 6 valores que network_observed.
    Homogeneidad entre módulos persistidos.
  · NO se añade 'incomplete' al CHECK de service_instances.
  · NO se hace mapeo 'incomplete → partial' en migration v3.
  · Si por casualidad apareciera 'incomplete' en la DB vieja
    (caso límite de mezcla histórica), el CASE lo mapea a
    'unknown' como neutralización defensiva, no a 'partial'.
  · La constante HealthIncomplete del enum global queda
    disponible para Storage en API/runtime sin imponer su
    aceptación en otros módulos.

Por qué NO se cambia el CHECK de network_observed para añadir
'incomplete':
  · Network módulo distinto · no es scope NimHealth (disciplina §1)
  · Cero callers de Network generan 'incomplete' hoy
  · Si Storage decide persistir 'incomplete' en el futuro, será
    un sprint propio que afecte solo a Storage's schema

Por qué NO se construyen CHECKs dinámicos desde Go:
  · Los schemas SQL están embebidos con //go:embed (strings
    estáticos en binario, leídos al boot)
  · Construir CHECK desde Go rompería el patrón establecido y
    obligaría a refactor de los 3 módulos que usan //go:embed
  · El test de sincronía Go↔SQL sería deuda equivalente a tener
    la lista en dos sitios

Esta decisión NO toca código fuera del scope de NimHealth.
Cero deuda nueva. Cero mapeo lossy. Documentado para futuras
referencias.
```

---

## 0. RESUMEN EJECUTIVO

El sprint NIMHEALTH-v1.2 (Fases 1–6) se ejecutó sobre un zip **viejo**
de Beta 8.1 que NO incluía el módulo Network v4 ni el archivo
`nimos_health.go`. En consecuencia, ese sprint:

- Reinventó un `HealthObserver` ad-hoc con su propia goroutine y
  `Start/Stop`, en vez de implementar la interfaz `Reconciler` ya
  existente.
- Usó `string` literal (`"healthy"`, `"degraded"`, ...) para health,
  en vez del tipo formal `HealthStatus` con sus constantes
  `HealthHealthy`, `HealthDegraded`, etc.
- Documentó esa decisión como "no hay infra previa" — eso era falso,
  yo no la leí. Mea culpa.

Este documento corrige el rumbo. **No es Fase 7**. Es un sprint
correctivo aparte, llamado **Integration v1**, cuyo objetivo es
integrar el trabajo útil de Fases 1–6 con la arquitectura real,
descartando lo que es reinvención.

**No** se tira todo lo anterior — ~70% del trabajo se mantiene
(cache, reason codes, grace period, detectores, migration v3,
regex, partición HTTP/observer/detectors). **Sí** se reescribe el
~30% que reinventaba infraestructura existente.

---

## 1. AUDITORÍA · qué hay en Beta 8.1 latest

### 1.1 Infraestructura existente que mi Fase 2 reinventó

```
nimos_health.go            · type HealthStatus = string
                             constantes: HealthHealthy, HealthDegraded,
                             HealthFailed, HealthPartial, HealthIncomplete,
                             HealthUnknown, HealthStale

network_reconciler.go      · interface Reconciler {
                                Name() string
                                Tier() ReconcilerTier
                                Interval() time.Duration
                                Reconcile(ctx context.Context) error
                             }
                           · ReconcilerScheduler { Register, Start, Stop, RunOnce }
                           · ReconcilerTier { TierCritical, TierMedium }

storage_clock.go           · interface Clock { Now, NewTicker }
                             RealClock (producción), FakeClock (tests)

network_events.go          · EventEmitter con Emit() · dedup + rate limit
```

### 1.2 Patrón consagrado en network/storage

Cada módulo:

1. Define su `*Observer` o `*Reconciler` como tipo
2. Implementa `Reconciler`: `Name() / Tier() / Interval() / Reconcile(ctx)`
3. Se registra en un `ReconcilerScheduler` durante init
4. Usa `Clock` inyectable (no `time.Now()` directo) para que tests
   con `FakeClock` funcionen
5. Usa `EventEmitter` para eventos auditables
6. Snapshot atómico (`atomic.Pointer[T]`) para lecturas lock-free
   desde handlers HTTP

### 1.3 Patrón canónico de boot

```go
// network_boot.go ejemplifica el patrón:
func initNetworkModule() error {
    clock := NewRealClock()
    networkRepo = NewNetworkRepo(db, clock)
    networkEventEmitter = NewEventEmitter(db, clock, ...)
    networkProbe = NewRealNetworkProbe()
    networkObserver, _ = NewNetworkObserver(repo, emitter, probe, clock, ...)
    networkReconcilers = NewReconcilerScheduler(clock)
    networkReconcilers.Register(networkObserver)
    networkReconcilers.Register(networkDDNSReconciler)
    // ...
    return nil
}
// main.go:
networkReconcilers.Start(context.Background())
```

### 1.4 Qué archivos del sprint anterior aplican LIMPIO a Beta 8.1 latest

Verificado vía `diff` con el zip original viejo:

| Archivo                     | Sin cambios entre zips | Aplica limpio |
|-----------------------------|------------------------|---------------|
| `services.go`               | ✓                      | ✓             |
| `apps.go`                   | ✓                      | ✓             |
| `models.go`                 | ✓                      | ✓             |
| `db.go`                     | ✓                      | ✓             |
| `NimHealth.svelte`          | (no verificado)        | probable      |
| `main.go`                   | **✗ cambió**           | **conflicto** |
| `nimhealth.go` (nuevo)      | n/a                    | requiere rework |

### 1.5 main.go cambió

```
+39 líneas: bootstrap completo de Network module v4 entre líneas 702–741.
La línea 765 del original viejo (donde puse mi `startHealthObserver`)
ahora es la línea 806 del nuevo.
```

Mi cambio en `main.go` no se aplica con `patch` automático. Se aplicará
manualmente en este sprint, con el observer ya como Reconciler.

---

## 2. CLASIFICACIÓN del trabajo Fases 1–6

| Pieza                                | Veredicto       | Razón |
|--------------------------------------|-----------------|-------|
| Crear `nimhealth.go` como home único | ✓ MANTENER      | Disciplina H6 sigue válida |
| Mover `handleServiceRoutes` aquí     | ✓ MANTENER      | Mismo motivo |
| Mover `ComputeDockerAggregateHealth` | ✓ MANTENER      | Mismo motivo |
| Mover `getDockerAppStatuses`         | ✓ MANTENER      | Mismo motivo |
| `DockerAppCache` con `sync.RWMutex`  | ⚠ ADAPTAR       | Mantener idea, usar `atomic.Pointer` (patrón Beta 8.1) |
| `HealthObserver` con goroutine ad-hoc| ✗ REINVENTAR    | Existe `Reconciler` + `ReconcilerScheduler` |
| `startHealthObserver()` + `sleep 8s` | ✗ REINVENTAR    | `Scheduler.Start(ctx)` ya lo hace |
| `dbServiceListWithTimeout` wrapper   | ⚠ REVISAR       | Buena idea, pero ver si conflicta con patrón Repo |
| Reason codes (9 constantes)          | ✓ MANTENER      | No existe equivalente, valor real |
| Boot grace period (`hostUptime`)     | ✓ MANTENER      | No existe equivalente, valor real |
| 7 detectores + slice                 | ✓ MANTENER      | No existe equivalente, resuelve H7 |
| Migration v3 (CHECK constraints SQL) | ✓ MANTENER      | Valor real, además SQLite enforce |
| `validateInstanceID` regex + tests   | ✓ MANTENER      | Resuelve H10 |
| `serviceStart/Stop` honesto          | ✓ MANTENER      | Resuelve H3 |
| `getSystemdUnit` extendido + guard   | ✓ MANTENER      | Resuelve H12 |
| Paralelización `reconcileServices`   | ⚠ REUBICAR      | La lógica vivirá DENTRO de `Reconcile()` del observer |
| Frontend `NimHealth.svelte` cambios  | ✓ MANTENER      | Polling 10s + reason codes |
| Tests `services_test.go`             | ✓ MANTENER      | 31 subtests útiles |

**Resumen**: ~70% se mantiene. ~30% se reescribe (observer ad-hoc → Reconciler).

---

## 3. PLAN DE INTEGRACIÓN · pasos pequeños

Cada paso es **autocontenido**: termina con build limpio + vet + tests
no-DB pasando. Si algo se rompe, se revierte UN paso, no todo el sprint.

### Paso 1 · Aplicar archivos sin conflicto

```
Archivos: services.go, apps.go, models.go, db.go, NimHealth.svelte,
          services_test.go (nuevo)
Acción: copiar tal cual desde sprint anterior a la versión nueva.
Riesgo: ninguno · sin cambios entre zips.
Build check: sí.
```

### Paso 2 · Crear `nimhealth.go` adaptado

Equivalente a las Fases 1+3 del sprint anterior:

```
· Home único de NimHealth backend
· HTTP handlers de /api/services (con reason codes inline)
· ComputeDockerAggregateHealth (con vocabulario HealthStatus)
· getDockerAppStatuses + parsing helpers
· dockerContainer struct
· extractUptime, parsePorts
· Reason codes (9 constantes)
· Boot grace period (hostUptime, inBootGracePeriod)
· DockerAppCache con sync.RWMutex (mantenemos esto, ver §4.1)
```

NO incluye: observer, Start/Stop, detectores, dbServiceListWithTimeout.
Esos van en pasos siguientes.

Build check: sí.

### Paso 3 · Reescribir observer como `Reconciler`

Crear archivo nuevo `nimhealth_observer.go`:

```go
// NimHealthObserver implementa la interfaz Reconciler.
type NimHealthObserver struct {
    clock   Clock
    config  NimHealthObserverConfig
}

func NewNimHealthObserver(clock Clock, config NimHealthObserverConfig) *NimHealthObserver

func (o *NimHealthObserver) Name() string              { return "nimhealth_observer" }
func (o *NimHealthObserver) Tier() ReconcilerTier      { return TierMedium }
func (o *NimHealthObserver) Interval() time.Duration   { return o.config.Interval }
func (o *NimHealthObserver) Reconcile(ctx context.Context) error {
    // 1. runAutoRegister(ctx)        ← detectores
    // 2. reconcileAllInstances(ctx)  ← reconciliación paralela
    // 3. refreshDockerCache(ctx)     ← cache para handler
    return nil
}
```

Lo que ESTO sustituye del sprint anterior:

```
✂ HealthObserver struct
✂ NewHealthObserver
✂ Start() / Stop()
✂ startHealthObserver() con sleep
✂ stopHealthObserver()
✂ healthObserver variable global
✂ goroutine ad-hoc
```

El sleep de 8s desaparece — el scheduler arranca cuando main.go llama
`Start(ctx)`, que ya está ordenado tras todos los demás bootstraps.
Si quisiéramos retrasarlo más, sería un `time.Sleep` en el callsite
de `Start`, no del observer.

Build check: sí.

### Paso 4 · Detectores en `nimhealth_detectors.go`

Mover los 7 detectores + slice + `runAutoRegister` del sprint anterior
a un archivo separado (cumple H6 del documento original + corrige
el creep de 1206 LOC que detectó Andrés).

```
nimhealth_detectors.go
  ~ 300 LOC
  · type registrableService struct
  · type ServiceDetector func
  · var detectors = []ServiceDetector{...}
  · runAutoRegister(ctx)
  · helpers systemdUnitExists, findPoolFromPath
  · 7 funciones detect*
```

Build check: sí.

### Paso 5 · Migration v3

Reaplicar la migration v3 del sprint anterior a `db.go`. CHECK constraints
SQL con los strings de los 6 valores oficiales (los mismos que
`network_observed`):

```sql
status TEXT CHECK (status IN
    ('running','stopped','starting','stopping','error','unknown'))
    DEFAULT 'unknown',
health TEXT CHECK (health IN
    ('healthy','degraded','failed','partial','unknown','stale'))
    DEFAULT 'unknown',
```

Mapeo CASE de valores antiguos (sin pérdida semántica):

```sql
CASE health
    WHEN 'healthy'     THEN 'healthy'
    WHEN 'degraded'    THEN 'degraded'
    WHEN 'unreachable' THEN 'failed'   -- era un fallo operacional
    WHEN 'unhealthy'   THEN 'failed'   -- sinónimo
    WHEN 'idle'        THEN 'healthy'  -- engine OK sin actividad ≠ failure
    WHEN 'failed'      THEN 'failed'
    WHEN 'partial'     THEN 'partial'
    WHEN 'stale'       THEN 'stale'
    WHEN 'incomplete'  THEN 'unknown'  -- neutralización: NimHealth no usa
                                       -- 'incomplete' (es de storage runtime).
                                       -- Si aparece en DB vieja por mezcla
                                       -- histórica, no perdemos semántica
                                       -- mapeando a 'unknown'.
    ELSE 'unknown'
END
```

**NO se mapea `incomplete → partial`**. Razón: son semánticamente
distintos (ver §CAMBIOS v1→v1.1 y §4.7). NimHealth no genera ni
acepta `incomplete`; si por casualidad apareciera en una fila
heredada de algún módulo experimental, se neutraliza a `unknown`.

`HealthStatus` (definido en `nimos_health.go`) sigue declarando
las 7 constantes globales — `HealthIncomplete` queda disponible
para que Storage la use en código de runtime sin imponer su
aceptación en este CHECK.

Build check: sí.

### Paso 6 · Wire-up en `main.go`

Reemplazar la goroutine one-shot por:

```go
// Crear scheduler propio de NimHealth (o reutilizar networkReconcilers).
// Decisión: scheduler propio, para no acoplar NimHealth a Network.
nimhealthScheduler = NewReconcilerScheduler(NewRealClock())
nimhealthObserver = NewNimHealthObserver(NewRealClock(), DefaultNimHealthConfig())
if err := nimhealthScheduler.Register(nimhealthObserver); err != nil {
    return fmt.Errorf("register nimhealth observer: %w", err)
}
if err := nimhealthScheduler.Start(context.Background()); err != nil {
    return fmt.Errorf("start nimhealth scheduler: %w", err)
}
```

Y borrar la goroutine vieja:

```go
go func() {
    time.Sleep(3 * time.Second)
    reconcileServices()    // ← se elimina
}()
```

Build check: sí.

### Paso 7 · Tests

Añadir `nimhealth_test.go` con tests sin DB:

```
· TestComputeDockerAggregateHealth (10+ subtests con vocabulario nuevo)
· TestInBootGracePeriod (con osReadFile mockeado)
· TestDockerAppCache_ConcurrentAccess (RWMutex stress test)
· TestNimHealthObserver_ReconcilerInterface (Name/Tier/Interval no panic)
· TestNimHealthObserver_ReconcileNoOp (con DB nil, debe devolver error limpio)
```

Build check + tests check: sí.

### Paso 8 · Frontend `NimHealth.svelte`

Aplicar los cambios de Fases 3+4 del sprint anterior:

```
· Polling 5s → 10s
· Helper reasonCodeText
· Tooltip con punto reasonCode en la lista
· Fila "motivo" en panel de detalle
· CSS .reason-dot
```

Build check: n/a (svelte build no se valida en sandbox).

---

## 4. DECISIONES TÉCNICAS

### 4.1 · ¿Cache con `sync.RWMutex` o `atomic.Pointer[DockerAppCache]`?

`NetworkObserver` usa `atomic.Pointer[ObserverSnapshot]` para snapshot
lock-free. **Mi `DockerAppCache` usa `sync.RWMutex`**.

Trade-off:
- `atomic.Pointer`: lectura sin lock, ideal para handler HTTP. Pero
  cada update reemplaza la pointer entera — no se pueden mutar campos
  individuales.
- `sync.RWMutex`: lecturas concurrentes con RLock, escrituras puntuales
  con Lock. Más flexible para updates parciales.

**Decisión**: cambiar a `atomic.Pointer[DockerAppCache]` por
homogeneidad con NetworkObserver. El handler hace **read** lock-free,
el observer **publish** un snapshot completo cada tick. Esto requiere
hacer `DockerAppCache` inmutable tras publicación (no se mutan campos
después de `Store`).

Beneficio adicional: tests concurrent-access son triviales.

### 4.2 · ¿Scheduler propio o reutilizar `networkReconcilers`?

**Decisión: scheduler propio `nimhealthScheduler`**.

Razón: la responsabilidad de NimHealth es independiente del módulo
Network. Acoplarlo significaría que un cambio en Network puede
afectar a NimHealth (e.g., si Network reinicia su scheduler). Tener
scheduler propio respeta la disciplina §1 (no acoplamiento sin razón).

Coste: 3 líneas extra de boot.

### 4.3 · ¿Clock inyectable?

**Sí**. Patrón Beta 8.1 lo exige. `NewNimHealthObserver(clock Clock, ...)`.
Tests pueden inyectar `FakeClock` y avanzar tiempo manualmente.

### 4.4 · ¿EventEmitter para eventos NimHealth?

**Por ahora no**. El sprint anterior emitía via `addNotification(...)`
(función pre-existente). Mantener eso. Migrar a `EventEmitter` sería
un sprint aparte que toca también notificaciones de storage.

### 4.5 · `dbServiceListWithTimeout` ¿se queda?

**Sí, por ahora**. Razón: el patrón Beta 8.1 nuevo (`NetworkRepo`)
acepta `context.Context` con timeouts. El módulo NimHealth usa funciones
viejas (`dbServiceList` sin context) que se usan en 30+ callsites del
repo. Migrar todo el daemon a contexts es trabajo para otro sprint.

Por defensa: el wrapper `dbServiceListWithTimeout` con goroutine+select
queda como mitigación local en `nimhealth.go`.

### 4.6 · Partición de archivos

El sprint anterior dejó `nimhealth.go` en 1206 LOC. **Excesivo**.
Crítica justa de Andrés. La partición en este sprint:

```
nimhealth.go              ~ 350 LOC · HTTP handlers + reason codes + grace
nimhealth_observer.go     ~ 250 LOC · Reconciler implementation + cache
nimhealth_detectors.go    ~ 300 LOC · 7 detectores + helpers
nimhealth_docker.go       ~ 250 LOC · getDockerAppStatuses + parsing
nimhealth_test.go         ~ 200 LOC · tests sin DB
```

Cada uno tiene una responsabilidad clara. Total ~1350 LOC
(ligeramente más que antes por boilerplate Reconciler), pero
distribuido y testeable.

### 4.7 · Conformidad con `HealthStatus` enum global

`nimos_health.go` define 7 constantes globales:

```
HealthHealthy, HealthDegraded, HealthFailed, HealthPartial,
HealthIncomplete, HealthUnknown, HealthStale
```

`network_observed` schema (módulo Network ya consolidado) usa
CHECK con 6 valores · NO incluye `incomplete`. NimHealth seguirá
esa misma convención:

```
service_instances.health CHECK IN
    ('healthy','degraded','failed','partial','unknown','stale')
```

**Por qué 6 y no 7**:

1. NimHealth no tiene caller que genere `incomplete`. Es un valor
   que solo emite `storage_btrfs_probe.go::computeObservationHealth()`
   cuando faltan devices físicos de un array — un caso de Storage,
   no de servicios.
2. Storage no persiste el valor (campo runtime devuelto en API).
3. Network ya estableció el precedente con 6. Mantener homogeneidad
   entre módulos persistidos reduce confusión.
4. Si en el futuro algún módulo necesitara persistir `incomplete`,
   sería su propio CHECK extendido — no afecta a NimHealth.

**Lo que NO hacemos** (justificación explícita):

```
❌ NO añadir 'incomplete' al CHECK de NimHealth.
   Sin caller que lo genere · CHECK más permisivo sin razón.

❌ NO añadir 'incomplete' al CHECK de network_observed.
   No es scope · módulo distinto · disciplina §1.

❌ NO construir CHECK dinámicamente desde Go via helper
   ValidHealthStatuses().
   Los schemas viven en .sql embebidos con //go:embed.
   Refactorizar eso afecta a 3 módulos. Coste real >> beneficio.

❌ NO mapear 'incomplete' → 'partial' en migration v3.
   Mapeo LOSSY · son distinciones reales:
     incomplete = devices físicos faltan (RAID degradado estructural)
     partial    = devices presentes pero el FS no monta (operacional)
   En lugar de eso: 'incomplete' → 'unknown' (neutralización
   defensiva, no afirma una semántica que NimHealth no entiende).
```

`HealthIncomplete` queda en el enum global disponible para Storage
runtime. No impone su presencia en CHECKs de tablas que no la usan.

---

## 5. CONTRATO DE CALIDAD

A diferencia del sprint anterior, cada Paso 1-8 cierra con:

```
[✓] go build ./... limpio
[✓] go vet ./... sin output
[✓] gofmt -e -l (los archivos tocados) sin output
[✓] tests no-DB que cubren la pieza · pasan
[✓] explicación de qué se hizo y qué NO
```

Si algún check falla, el paso se revierte. No se acumula deuda
"silenciosa" para limpiar después.

---

## 6. LO QUE EXPLÍCITAMENTE NO HACEMOS

```
❌ NO triple-generation (Desired/Observed/Applied) en service_instances.
   El estado es derivado de systemd/docker. NimOS no decide qué
   "debería" estar corriendo. Disciplina §1.

❌ NO snapshots persistidos en SQLite del runtime de containers.
   Cache en memoria. Recomputable en ~150ms al boot. Disciplina §2.

❌ NO docker stats / docker inspect en el observer loop.
   Pertenece a NimMonitor o panel detalle on-demand.

❌ NO refactor de app_registry a dos tablas.
   El campo `type` ya distingue. Coste alto, beneficio bajo.

❌ NO migrar todo el repo a context.Context con timeout en queries.
   Wrapper local es suficiente para NimHealth.

❌ NO EventEmitter para NimHealth en este sprint.
   addNotification queda. Migración futura, no scope.

❌ NO endpoint /api/services/{id}/detail (Fase 5 original).
   No es necesario para Task-Manager UX. Si aparece necesidad real
   de "ver detalle profundo", se añade después.

❌ NO botón "Detalle" en frontend.
   Por la misma razón.
```

---

## 7. RIESGOS

```
R1 · Migration v3 falla en una DB de producción con datos raros.
     Mitigación: la migration tiene CASE con fallback a 'unknown'.
     Es transaccional (BEGIN; ...; COMMIT;) — falla atómico, sin
     dejar el sistema en estado intermedio.

R2 · Scheduler de NimHealth y de Network compiten por CPU al
     mismo tiempo.
     Mitigación: intervals distintos (Network=60s, NimHealth=30s).
     Worst case overlap: pocos segundos cada par de minutos.
     Pi 4 con 4 cores aguanta.

R3 · El reconciler ya no tiene "primer tick síncrono" al boot.
     Mitigación: la primera vez que el frontend pide /api/services
     dentro de los primeros 30s, recibe children=[] con
     reasonCode=initializing. El usuario ve "Inicializando NimHealth"
     en lugar de pantalla vacía. UX aceptable.

R4 · El cambio de RWMutex a atomic.Pointer rompe sutilmente algún
     consumer que espera mutación in-place.
     Mitigación: no hay consumer fuera del observer y handler propios.
     Code review verifica esto antes de mergear.
```

---

## 9. DEUDA TÉCNICA IDENTIFICADA (consciente)

Tres riesgos arquitectónicos detectados durante la revisión que NO
se abordan en este sprint, con criterio objetivo para decidir cuándo
sí actuar.

```
D-001 · app_registry como tabla universal

    Severidad         : BAJA
    Crece con tiempo  : NO (deuda estática)
    Síntoma actual    : Ninguno · queries funcionan
    Señal para actuar : Queries lentas o lógica de filtrado retorcida
                        con >30 entradas en el registry.
                        O: aparición de un 5º tipo (UI/system/daemon/
                        docker + nuevo) que no quepa en `type`.
    Coste estimado    : 2-3 sprints (migrations + FK + 4 módulos
                        consumidores)
    Acción ahora      : Ninguna · documentar y olvidar hasta señal


D-002 · Acceso directo a SQLite desde lógica de observer

    Severidad         : MEDIA
    Crece con tiempo  : SÍ (cada módulo nuevo que se acople empeora)
    Síntoma actual    : NimHealth observer llama a dbServiceList/
                        dbServiceGet directamente · Network módulo
                        ya tiene NetworkRepo · inconsistencia
    Señal para actuar : Cuando se decida adoptar patrón Repo en
                        TODOS los módulos consumidores de
                        service_instances (NimHealth, Storage,
                        AppStore, Launcher, posibles futuros)
    Coste estimado    : 1 sprint propio (sin contar adopción en
                        módulos legacy, que se hace en paralelo)
    Acción ahora      : NO añadir ServiceRepo solo para NimHealth ·
                        sería incoherencia activa, dos formas
                        paralelas de acceder a la misma tabla


D-003 · Boot storm potencial · 9 starters concurrentes en main.go

    Severidad         : DESCONOCIDA (sin métricas reales)
    Crece con tiempo  : SÍ (cada nuevo módulo añade un starter)
    Síntoma actual    : Sospecha · 9 `go func()` en t≈0 sin orden
                        ni stagger · en Pi 4 con SD lenta podría
                        causar IO storm + timeouts falsos
    Señal para actuar : MEDIR primero en Pi 4 modelo bajo:
                        · Tiempo de boot completo del daemon
                        · Aparición de logs "degraded/timeout"
                          durante los primeros 60s
                        · Latencia de SD durante boot
                        Si las 3 métricas muestran problema real:
                        sprint propio de BootScheduler con tiers
    Coste estimado    : Sprint propio (~3-5 días) ·
                        BootScheduler análogo al ReconcilerScheduler
    Acción ahora      : NO improvisar solución · documentar sospecha
                        Cuando se aborde, añadir mediciones reales
                        ANTES de proponer arquitectura
```

**Por qué documento estas tres y no las soluciono**:

Aplicando disciplina §1 (no abstracción anticipada) y §scope creep:
expandir el sprint INTEGRATION para tocar estos tres puntos sería
exactamente el error que el sprint inicial (Fases 1-6) cometió.
La forma honesta de tratarlos es:

1. Reconocer que existen (esta sección)
2. Dejar criterio objetivo para futuros (señal para actuar)
3. NO actuar hasta que se cumpla la señal
4. Cuando se cumpla, abrir documento de plan propio

Si en Beta 9 o Beta 10 estas deudas siguen aquí sin actuar, será
porque las señales NO se han disparado. Eso significa que la decisión
de no tocarlas fue correcta. Si una señal se dispara y la deuda se
queda olvidada, este documento debe servir de recordatorio.

---

## 10. CIERRE

Tras este sprint:

```
[✓] H1-H4, H6-H12 resueltos
[✓] HealthStatus formal usado correctamente
[✓] Patrón Reconciler aplicado · homogeneidad con Network module
[✓] nimhealth.go partido en 4 archivos
[✓] Tests sin DB añadidos
[✓] Plan documentado ANTES de implementar
[✓] Cada paso verificado independientemente
[✓] Sprint inicial (v1.0-v1.2) queda como histórico
[✓] Deuda técnica identificada en §9 con criterios objetivos
[✓] Este documento queda como referencia: NIMHEALTH-INTEGRATION-v1.2.md
```

Decisión: aprobar e implementar paso a paso, o ajustar antes.

```
[ ] APROBAR íntegro → arrancar Paso 1
[ ] APROBAR con cambios → indicar cuáles
[ ] RECHAZAR un punto concreto → cuál y por qué
```

Una vez aprobado, este documento queda congelado en `/documents/`.
Cualquier cambio posterior va a un v2.

---

**Fin del documento.**
