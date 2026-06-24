# NIMHEALTH v1.1 — Plan de refactor arquitectónico

**Tipo**: Documento de plan arquitectónico
**Audiencia**: Andrés + Claude (co-developer)
**Estado**: PROPUESTA · pendiente de aprobación final
**Versión**: v1.1 (21/05/2026)
**Disciplina base**: `NIMOS_DISCIPLINE.md` v2
**Versión anterior**: `NIMHEALTH-v1.md` (queda como histórico)

---

## CAMBIOS v1 → v1.1

```
Refinamientos tras discusión del 21/05/2026:

1. BOOT STORM
   v1 no abordaba el problema. v1.1 lo localiza correctamente:
   no es un problema interno de NimHealth (que solo tiene UN observer,
   no Tiers), es un problema de main.go que arranca 9 starters en t+0.
   Mitigación menor en NimHealth (sleep 8s en vez de 3s).

2. GRACE PERIOD para Docker
   v1.1 añade ventana de 90s tras boot del HOST (no del daemon)
   durante la cual containers en 'starting'/'restarting' se reportan
   como status=starting, health=unknown, reason_code=boot_grace_period.

3. DB TIMEOUTS defensivos
   v1.1: todas las queries del observer usan context.WithTimeout(2s).
   Si SQLite se bloquea, el observer falla con reason_code=db_timeout,
   no se cuelga. WAL ya mitiga lecturas vs escrituras, pero defensa
   en profundidad.

4. REASON CODES
   v1.1: enum cerrado de 8 valores que acompañan a status=unknown o
   health=degraded para que la UI explique al usuario el porqué.

5. CACHE EN MEMORIA (cambio mayor)
   v1.1 invierte el modelo. El observer es el ÚNICO que ejecuta
   docker ps -a / systemctl. El handler /api/services lee solo de
   una cache en memoria. Coste por GET: ~5ms en lugar de ~150ms.
   Esto desacopla el coste real del frecuencia de polling del frontend.

6. FRONTERA NimHealth ↔ NimMonitor
   v1.1 declara explícitamente en §9: Salud es BINARIO, Métricas son
   NUMÉRICAS. NimHealth no ejecuta docker stats. NimMonitor o panel
   de detalle on-demand se encargan de números.

7. POLLING DEL FRONTEND
   v1.1: Svelte baja de 5s a 10s. Con cache en memoria + handler de
   solo lectura, es prácticamente gratis. Bajar a 10s elimina ruido
   sin afectar percepción de tiempo real.

8. DOCKER EVENTS como v2 futura
   v1.1 deja apuntada la posibilidad de suscribirse al stream de
   docker events para invalidar cache solo en cambios reales, pero
   NO entra en v1. v1 usa poll cada 30s, suficiente para Pi 4.
```

---

## 0. RESUMEN EJECUTIVO

NimHealth funciona pero está mal arquitecturado. Vive repartido por
`services.go`, `apps.go`, `models.go`, `docker.go` y `hardware.go` sin
home claro. Su "reconciler" corre una sola vez al boot. Maneja tres
dialectos distintos de health/status. Y el handler HTTP ejecuta
`docker ps -a` en cada GET, lo que dispara 12 llamadas/minuto solo
con el frontend abierto (polling a 5s).

Este documento propone consolidarlo como un **OBSERVER en memoria**
(no un reconciler, no persistido), con home único en `nimhealth.go`,
alineado con la disciplina §1 (no triple gen para estado derivado),
§2 (no snapshots para datos trivialmente recomputables), §5 (tier+
interval), §6 (HealthStatus unificado) y §8 (HealthStatus pertenece
al servicio).

**El modelo invertido**: el observer ejecuta `docker ps -a` cada
30s y empuja a una cache en memoria. El handler `/api/services` lee
solo de cache. El frontend pollea cada 10s sin coste real.

**No** se aplica triple generation, **no** se persisten snapshots,
**no** se refactoriza `app_registry`, **no** se ejecuta `docker stats`.
Esas son decisiones explícitas con justificación, no omisiones.

---

## 1. CONTEXTO

NimOS Beta 8.1 introdujo un service registry en `services.go` para
permitir tracking de qué servicios dependen de qué pools (para destroy
seguro). En paralelo, NimHealth nació como UI Svelte para mostrar el
estado de Docker/NimTorrent/NimBackup en una sola pantalla.

Backend para esa UI se construyó **incrementalmente y por necesidad**:
primero el handler `/api/services`, luego el cruce con `docker ps`,
luego el reconcile en boot, luego el cálculo de aggregate health para
Docker. Cada pieza tiene sentido aislada. Juntas no forman un módulo
coherente.

```
HOY (estado fáctico):

  ┌─────────────────────────────────────────────────────────────────┐
  │  Frontend: NimHealth.svelte                                     │
  │       polling cada 5s ─────┐                                    │
  │                            ▼                                    │
  │                      /api/services                              │
  │                            │                                    │
  │                            ▼                                    │
  │      services.go ──┬── handleServiceRoutes()                    │
  │                    ├── dbServiceList()       ← SQLite           │
  │                    └── enriquece con ↓                          │
  │                                                                 │
  │      apps.go ──── getDockerAppStatuses()                        │
  │                            │                                    │
  │                            ▼                                    │
  │                      `docker ps -a`        ← runCmd ~100-150ms  │
  │                            │                                    │
  │                            ▼                                    │
  │      models.go ── ComputeDockerAggregateHealth()                │
  │                                                                 │
  │      docker.go ── dbServiceRegister() en install()              │
  │                                                                 │
  │      main.go ──── go reconcileServices() · ONE-SHOT en boot     │
  │                                                                 │
  │      db.go ────── migration v2 mete 'nimhealth' en app_registry │
  └─────────────────────────────────────────────────────────────────┘

  Coste real hoy:
    · 12 GET/min × ~150ms = ~1.8s CPU por minuto solo por NimHealth.
    · Si dos clientes miran, 24/min = ~3.6s CPU/min.
    · En Pi 4 con 4 cores eso es ~1.5% sostenido. No es crítico
      pero es DESPERDICIO porque la info es la misma para todos.
```

Conclusión: **NimHealth no es un módulo, es una vista que se sirve
a partir de pedazos de cinco ficheros distintos y ejecuta runCmd
por cada GET.**

---

## 2. DIAGNÓSTICO (hallazgos de la auditoría)

Resumen de los problemas, ordenados por gravedad:

```
H1  · CRÍTICO   · reconcileServices() es one-shot al boot.
                  DB queda STALE hasta el siguiente reboot.

H2  · CRÍTICO   · Doble fuente de verdad para Docker:
                  service_instances.health vs ComputeDockerAggregateHealth().
                  El handler sobrescribe la persistida cada GET.

H3  · CRÍTICO   · serviceStart/Stop persiste "running/healthy"
                  asumiendo éxito, sin verificar el estado real.

H4  · ARQ.      · Tres vocabularios distintos de health:
                  DB    : running/stopped/starting/stopping/failed/error/unknown
                          + healthy/degraded/unreachable/unknown
                  API   : running/stopped/error + healthy/degraded/unhealthy/idle
                  Disc. : healthy/degraded/failed/partial/unknown/stale  (6 oficial)
                  → ninguno coincide con ninguno.

H5  · ARQ.      · app_registry mezcla apps UI + subsistemas + servicios
                  gestionables en una sola tabla. Single-table inheritance.

H6  · ARQ.      · NimHealth no tiene home de código. Para fix de bug,
                  hay que tocar 4 ficheros.

H7  · ARQ.      · autoRegisterServices() es hardcoded NimTorrent+Docker.
                  No extensible. NimBackup y VMs quedan fuera.

H8  · RENDIM.   · Handler ejecuta docker ps -a en cada GET.
                  Con frontend a 5s = 12 runCmd/min por usuario.
                  No escala con N apps Docker porque cada app docker
                  inspect en GET sería catastrófico.

H9  · MENOR     · Reconciler en serie por instance.
                  10 servicios × ~5s timeout = hasta 150s en peor caso.

H10 · MENOR     · validateInstanceID acepta basura ("aaa@bbb@ccc" pasa).

H11 · MENOR     · Nivel "optional" de ServiceDependency declarado pero
                  no usado (anti-pattern: niveles inventados).

H12 · BUG       · getSystemdUnit() devuelve "nimos-daemon.service"
                  para nimbackup. Stop/Start de nimbackup PARARÍA el daemon.

H13 · INTEGR.   · CircuitBreaker (breaker.go) tiene CircuitState pero
                  NimHealth no traduce a HealthStatus.
                  Viola disciplina §8.
```

H8 es nuevo en v1.1 — la auditoría original de v1 no destacó el coste
del runCmd en cada GET porque no había mirado el polling del frontend.

---

## 3. PRINCIPIOS QUE APLICAN

De la disciplina, lo que es relevante para NimHealth:

```
§1  Triple Generation       → NO aplica. service_instances es estado
                              derivado, no convergente.
§2  Snapshot persistido     → NO aplica al runtime de containers.
                              Trivialmente recomputable con docker ps.
§5  Reconciler tiers        → tier=Medium, interval=30s, ortogonal.
§6  HealthStatus unificado  → SIEMPRE 6 estados. Sin dialectos paralelos.
§7  SystemCapabilities      → no aplica (NimHealth no es feature opcional).
§8  HealthStatus del SVC    → CircuitBreaker tiene state, servicio tiene health.
                              La traducción la hace el observer del servicio.

Anti-patterns relevantes:
§1  Abstracción anticipada → no inventar ServiceDetector interface vacía.
§2  Patrón global          → no aplicar triple gen porque "queda bien".
§3  Snapshot por si acaso  → NO snapshots persistidos en nimhealth.
§4  Event para cada cosa   → eventos solo en acciones auditables.
§5  Niveles por tener      → ya hay "optional" sin usar. Eliminar.
```

**Decisiones explícitas de NO aplicar**:

```
❌ NO Triple Generation (Desired/Observed/Applied).
   Justificación: el "desired" de un servicio es trivial ("debería
   estar running si está registrado"). El convergente real es systemd
   / docker, no NimOS. NimHealth solo OBSERVA, no aplica.
   La disciplina §1 excluye explícitamente "estado derivado de otras
   entidades" — y eso es exactamente lo que es service_instances.

❌ NO Snapshots persistidos en SQLite del runtime de containers.
   Justificación: docker ps -a tarda 150ms en repoblar todo. Escribir
   N filas cada 30s para 20 apps Docker es write-amplification en SD
   sin beneficio. Disciplina §2: "NO aplicar cuando la info es
   trivialmente re-computable" y "solo necesitas el último valor".

❌ NO refactor de app_registry en este sprint.
   Justificación: el campo `type` (ui/system/daemon/docker) ya
   distingue. El refactor a dos tablas separadas tiene alto coste
   (migración, FK, código) y bajo beneficio inmediato.

❌ NO CircuitBreaker para systemd/docker.
   Justificación: son servicios LOCALES, no externos. Disciplina §3
   excluye explícitamente servicios internos.

❌ NO docker stats / docker inspect en el observer loop.
   Justificación: docker stats cuesta CPU sostenido y devuelve métricas
   (números), no salud (booleanos). docker inspect cuesta lineal con N
   containers. Ambos pertenecen a NimMonitor o a panel de detalle
   on-demand, NO al observer cíclico.

❌ NO Tiers internos en NimHealth.
   Justificación: hoy son 4-5 servicios del mismo orden de magnitud.
   Inventar Tier 1/2/3 dentro de NimHealth viola disciplina §5
   (niveles inventados) y §1 (abstracción anticipada). UN observer
   itera todos. Si en el futuro un servicio cuesta órdenes de
   magnitud más, se separa entonces.

❌ NO docker events stream (v1).
   Justificación: complejidad media-alta (goroutine larga, reconnect,
   parse). v1 va con poll 30s que es trivial y suficiente para Pi 4.
   v2 puede valorar si el stream merece la pena cuando haya datos
   reales de uso.
```

---

## 4. ARQUITECTURA PROPUESTA

### 4.1 · Home único: `nimhealth.go`

```
/daemon/nimhealth.go    ← NUEVO · todo lo que es "NimHealth backend"
/daemon/services.go     ← se queda con persistencia + lifecycle
                          (dbService*, serviceStart/Stop, canDestroyPool)
```

`nimhealth.go` contiene:

```
1. HealthObserver struct          · loop Medium, interval=30s
2. dockerCache struct             · cache en memoria, RWMutex-protected
3. observeAll()                   · una pasada completa
4. observeDockerEngine()          · docker ps -a, calcula agregada, escribe cache
5. observeSystemdService(name)    · systemctl is-active
6. observeInternalService(name)   · daemon vivo = sí
7. service detectors slice        · auto-registro al boot
8. handleServiceRoutes()          · solo lectura desde cache + DB
9. handleServiceDetail()          · on-demand, sí ejecuta docker inspect
10. HealthStatus mappers          · systemd→Health, docker→Health
11. boot grace period helper      · /proc/uptime → ¿estamos en gracia?
12. reason code constants         · enum cerrado de 8 valores
```

`services.go` se queda con (capa pura de persistencia + lifecycle):

```
· createServiceRegistryTables()
· dbServiceRegister / Update / Delete / Get / List / Dependencies
· validateInstanceID / validateServicePath / validateDepType / validateRequired
· canDestroyPool / checkPoolDependencies   (para destroy de pools)
· serviceStart / serviceStop / getSystemdUnit / getServiceLogs
```

Resuelve **H6**.

### 4.2 · HealthObserver con cache en memoria

```go
type DockerAppCache struct {
    statuses    []DockerAppStatus
    orphans     int
    aggHealth   string  // healthy/degraded/failed/unknown
    updatedAt   time.Time
    initialized bool
}

type HealthObserver struct {
    interval time.Duration  // 30s
    stopCh   chan struct{}
    done     chan struct{}
    
    cacheMu  sync.RWMutex
    docker   DockerAppCache
}
```

**Lifecycle**:

```
1. NewHealthObserver(interval=30s)
2. Start(ctx) → arranca goroutine, primer tick INMEDIATO
3. Stop()     → cierra stopCh, espera done
```

**Primer tick síncrono al boot**: para que el primer GET no devuelva
"unknown" innecesariamente. Después el ticker toma el relevo.

**Boot delay del observer**: 8s (no 3s como hoy). Da tiempo a que
storage y network terminen su primer scan antes. Esto NO arregla el
boot storm general de `main.go` (9 starters concurrentes), pero
mitiga el aporte específico de NimHealth a ese storm.

**El boot storm de main.go es un problema fuera de scope de v1**
y queda apuntado en §11 (Riesgos) como mejora separada futura.

### 4.3 · observeDockerEngine() — la pieza central

```
observeDockerEngine():
  
  1. ctx, cancel := context.WithTimeout(2*time.Second)
  2. out := runCmd("docker", ["ps", "-a", "--format", "{{json .}}"], ctx)
     · si error → cache.aggHealth = "unknown", reason = "observer_timeout"
     · si docker no instalado → cache.aggHealth = "unknown", reason = "not_installed"
  
  3. parse JSON lines → []dockerContainer
  
  4. cross con docker_apps registradas en DB (igual que hoy)
     → []DockerAppStatus
  
  5. para cada DockerAppStatus:
        · NormalizeDockerStatus → status
        · NormalizeDockerHealth → health
        · si IN BOOT GRACE PERIOD + status in (starting/restarting):
             health = "unknown"
             reasonCode = "boot_grace_period"
  
  6. aggHealth = ComputeDockerAggregateHealth(statuses)
  
  7. cache.mu.Lock()
     cache.statuses = statuses
     cache.orphans = orphans
     cache.aggHealth = aggHealth
     cache.updatedAt = now
     cache.initialized = true
     cache.mu.Unlock()
  
  8. si aggHealth cambió respecto al tick anterior:
        emit event (warn si degraded/failed, info si vuelta a healthy)
```

**Coste medido en Pi 4**: ~80-150ms por tick. Con interval=30s eso
es 0.5% CPU promedio. **El coste es CONSTANTE con N apps Docker**
porque `docker ps -a` devuelve todo en una sola llamada.

### 4.4 · Handler `/api/services` — SOLO LECTURA

```
handleServiceRoutes():

  1. requireAuth
  
  2. dbServiceList(poolFilter)              ← SQLite read, ~5ms
                                              con context.WithTimeout(2s)
  
  3. para cada instance, dbServiceDependencies()    ← SQLite read
                                                     (en paralelo con errgroup
                                                      si hay > 3 instances)
  
  4. si appID == "containers":
        cache.mu.RLock()
        children = cache.statuses
        aggHealth = cache.aggHealth
        if !cache.initialized:
            health = "unknown"
            reasonCode = "initializing"
        cache.mu.RUnlock()
        
        result[i]["children"] = children
        result[i]["health"]   = aggHealth
        result[i]["reasonCode"] = reasonCode  // si aplica
  
  5. devolver JSON
```

**Cero `runCmd` en el camino del handler. Coste por GET: ~5-15ms.**

Con polling del frontend a 10s, son ~6 GET/min × 10ms = 60ms CPU/min.
Prácticamente invisible.

### 4.5 · Endpoint nuevo `/api/services/{id}/detail`

```
GET /api/services/{id}/detail

  Para servicios Docker engine o systemd: devuelve la info de
  service_instances + dependencies + (si es docker engine) la cache
  de children.
  
  Para Docker apps individuales (jellyfin, immich, etc.):
        ejecuta docker inspect <containerName> ON DEMAND
        devuelve env vars, mounts, networks, full healthcheck output,
        restart policy, etc.
  
  Este endpoint NO se llama desde el polling. Solo cuando el usuario
  pincha en la tarjeta de una app concreta en NimHealth.
  
  Coste por inspect: ~30-50ms. Aceptable porque es bajo demanda
  humana, no automático.
```

**Mantra**: el observer hace el trabajo pesado a frecuencia controlada.
El handler de polling lee de cache. El endpoint de detalle hace el
trabajo profundo solo cuando un humano lo pide.

### 4.6 · HealthStatus normalizado a los 6 oficiales

Resuelve **H4**.

```
Estados canónicos (los 6 de la disciplina §6):

  healthy   · El servicio responde y funciona como se espera.
  degraded  · Funciona pero con problemas (e.g. Docker con 1 container error).
  failed    · El servicio no funciona. Crash, dead, unreachable.
  partial   · NimHealth no usa este estado por ahora. (reservado)
  unknown   · No se ha observado o el observer no pudo determinar.
  stale     · Última observación es de hace > 5×interval (~150s).

Status (vocabulario corto, separado de health):

  running, stopped, starting, stopping, error, unknown
```

**Migración SQL** (`user_version=3`):

```sql
CREATE TABLE service_instances_v2 (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL,
    pool_name   TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      TEXT CHECK (status IN
                  ('running','stopped','starting','stopping','error','unknown'))
                  DEFAULT 'unknown',
    health      TEXT CHECK (health IN
                  ('healthy','degraded','failed','partial','unknown','stale'))
                  DEFAULT 'unknown',
    owner       TEXT DEFAULT 'system',
    config      TEXT DEFAULT '{}',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    last_observed_at TEXT,
    FOREIGN KEY (app_id) REFERENCES app_registry(id)
);

INSERT INTO service_instances_v2 SELECT
    id, app_id, pool_name, path,
    CASE status
        WHEN 'running'  THEN 'running'
        WHEN 'stopped'  THEN 'stopped'
        WHEN 'starting' THEN 'starting'
        WHEN 'stopping' THEN 'stopping'
        WHEN 'failed'   THEN 'error'
        WHEN 'error'    THEN 'error'
        ELSE 'unknown'
    END AS status,
    CASE health
        WHEN 'healthy'     THEN 'healthy'
        WHEN 'degraded'    THEN 'degraded'
        WHEN 'unreachable' THEN 'failed'
        WHEN 'unhealthy'   THEN 'failed'
        WHEN 'idle'        THEN 'unknown'
        ELSE 'unknown'
    END AS health,
    owner, config, created_at, updated_at, NULL
FROM service_instances;

DROP TABLE service_instances;
ALTER TABLE service_instances_v2 RENAME TO service_instances;
CREATE INDEX idx_si_pool   ON service_instances(pool_name);
CREATE INDEX idx_si_status ON service_instances(status);

UPDATE service_dependencies SET required='soft' WHERE required='optional';

PRAGMA user_version = 3;
```

**Campo nuevo `last_observed_at`**: necesario para calcular `stale`.
Si `now - last_observed_at > 5*interval` (~150s) → health efectivo
`stale` en el response del handler (sin modificar la DB).

### 4.7 · Service detectors — patrón ligero

Resuelve **H7**. Disciplina anti-§1: no inventamos interface, usamos
un slice de funciones.

```go
type ServiceDetector func(ctx context.Context) []ServiceInstance

var detectors = []ServiceDetector{
    detectDockerEngine,
    detectNimTorrent,
    detectNimBackup,
    detectVMs,
}

func runAutoRegister(ctx context.Context) {
    for _, d := range detectors {
        instances := d(ctx)
        for _, inst := range instances {
            dbServiceRegister(inst, computeDeps(inst))
        }
    }
}
```

**Cuando aparezca el 5º o 6º servicio** se evaluará si extraer una
interface real con dependencias declarativas. Hoy no.

### 4.8 · serviceStart / serviceStop — sin mentir

Resuelve **H3**.

```go
func serviceStart(id string) error {
    // 1. validar
    // 2. UPDATE status='starting', health='unknown'  ← intermedio honesto
    // 3. ejecutar systemctl start / docker start
    // 4. NO escribir 'running/healthy' al final.
    //    Devolver y dejar que el observer lo confirme en el siguiente tick.
}
```

**Coste UX**: el usuario ve "starting" durante 0-30s. Es honesto.
Si el servicio crashea en 200ms, el siguiente tick lo detecta y
lo marca como `error/failed`. Hoy nunca se enteraría.

Misma lógica para `serviceStop`: deja `stopping/unknown` y espera
al observer.

### 4.9 · Eventos NimHealth

Disciplina §4: solo en transiciones auditables. Con dedup (5min)
y rate limit (10/min/category).

```
✅ EMITIR evento cuando:
   · Servicio pasa a status=error (warn)
   · Servicio pasa de error a running (info)
   · Servicio detectado y registrado por detector (info)
   · Servicio orphaned y eliminado (info)
   · Docker aggregate health pasa de healthy → degraded/failed (warn)

❌ NO EMITIR evento por:
   · Cada observeAll() exitoso        (sería 2880 events/día)
   · Servicio sigue en mismo estado    (ruido)
   · Cada GET /api/services            (trivial)
   · Cache hits                        (interno, no auditable)
```

### 4.10 · Reason codes — enum cerrado

Campo opcional en el response. Presente solo si `status=unknown` o
`health` no es `healthy`. Ayuda a la UI a explicar al usuario.

```go
// nimhealth.go
const (
    ReasonInitializing      = "initializing"        // primer tick no ha corrido
    ReasonBootGracePeriod   = "boot_grace_period"   // host arrancó hace < 90s
    ReasonObserverTimeout   = "observer_timeout"    // observeService superó timeout
    ReasonDbTimeout         = "db_timeout"          // SQLite excedió 2s
    ReasonPaused            = "paused"              // usuario detuvo el servicio
    ReasonDegradedChildren  = "degraded_children"   // Docker con children en error
    ReasonStale             = "stale"               // última obs > 5×interval
    ReasonNotYetObserved    = "not_yet_observed"    // recién registrado
)
```

**Cerrado**: 8 valores. Si aparece un 9º se discute antes de añadir.
Anti-patrón habitual: dejar `reason string` libre y que cada caller
meta su frase. Acabas con N variantes de "timeout" y nadie sabe cuál
es cuál.

**Campo runtime-only**: NO se persiste en `service_instances`. Es
derivado del estado actual y de timestamps. El observer lo computa
al construir el response.

### 4.11 · DB timeouts defensivos

Todas las queries del observer y del handler usan
`context.WithTimeout(2 * time.Second)`.

```
Justificación:
   WAL ya separa lecturas de escrituras en SQLite, así que el
   escenario más probable (escritura concurrente de otro módulo)
   no debería bloquear lecturas. PERO defense in depth: si en algún
   momento alguien hace BEGIN EXCLUSIVE o si el WAL se rompe, NimHealth
   no se debe colgar. Falla rápido y emite reason_code=db_timeout.
   
   No leemos de caches en memoria de otros observers (e.g. el snapshot
   atómico del StorageObserver). Eso crearía acoplamiento cruzado
   entre módulos que la disciplina §1 desaconseja.
```

### 4.12 · Boot grace period (90s)

```go
// nimhealth.go
const dockerBootGracePeriod = 90 * time.Second

func hostUptime() time.Duration {
    data, err := os.ReadFile("/proc/uptime")
    if err != nil { return 99*time.Hour } // si no podemos leer, no aplicar gracia
    parts := strings.Fields(string(data))
    if len(parts) < 1 { return 99*time.Hour }
    secs, _ := strconv.ParseFloat(parts[0], 64)
    return time.Duration(secs * float64(time.Second))
}

func inBootGracePeriod() bool {
    return hostUptime() < dockerBootGracePeriod
}
```

**Por host uptime, no daemon uptime**: si el daemon se reinicia
solo (crash, update), los containers Docker ya llevan corriendo
horas. NO queremos otros 90s de gracia inmerecida.

**Aplicación**: durante el grace period, un container en
`restarting`/`starting` se reporta como `status=starting,
health=unknown, reason_code=boot_grace_period`. Pasados los 90s,
evaluación normal.

**Configurabilidad**: el periodo es constante en v1.
Si llega un caso real de "Plex con biblioteca grande tarda 5min en
arrancar", se valora exponerlo. NO antes (disciplina anti-§3:
configuración por si acaso).

### 4.13 · Integración con CircuitBreaker (H13) — STUB v1

Cuando NimHealth muestre estado de servicios de Network (DDNS, certs)
en el futuro, traducirá el `CircuitState` del breaker al HealthStatus:

```
breaker.closed     → service.healthy
breaker.half_open  → service.degraded
breaker.open       → service.failed
```

Disciplina §8 aplicada. Hoy en v1 queda **documentado pero no
implementado**. v2 lo activa cuando NimHealth muestre subsistemas
Network.

---

## 5. CAMBIOS A LOS FICHEROS EXISTENTES

**Salen de `services.go` y `apps.go`, entran en `nimhealth.go`**:

```
✂  reconcileServices()              → reemplazado por observeAll()
✂  autoRegisterServices()           → reemplazado por runAutoRegister()
✂  handleServiceRoutes()            → reescrito como solo lectura desde cache
✂  ComputeDockerAggregateHealth     → movida tal cual
✂  getDockerAppStatuses (apps.go)   → movida tal cual + integrada en observer
```

**Se quedan en `services.go`** (capa de persistencia + lifecycle):

```
✓  createServiceRegistryTables
✓  validateInstanceID + endurecida con regex (H10)
✓  validateServicePath / validateDepType
✓  validateRequired (sin 'optional', H11)
✓  dbService* (Register, Update, Delete, Get, List, Dependencies)
✓  canDestroyPool / checkPoolDependencies
✓  serviceStart / serviceStop (no escribe final state, H3)
✓  getSystemdUnit (bug nimbackup arreglado, H12)
✓  getServiceLogs
```

**Frontend Svelte** (`NimHealth.svelte`):

```
~  setInterval cambiado de 5000 a 10000
~  acepta los 6 nuevos health states + 6 status
~  renderiza reasonCode en estados unknown/degraded (texto secundario)
~  añade botón "Detalle" que llama a /api/services/{id}/detail (lazy)
```

---

## 6. MIGRACIÓN DE SCHEMA

Una sola migración (`user_version=3`). Detalle completo en §4.6.

Lo que añade:

```
· CHECK constraints en status y health (los 6 oficiales).
· Columna last_observed_at (TEXT, nullable).
· Mapeo CASE de valores viejos → nuevos.
· UPDATE de 'optional' → 'soft' en service_dependencies.
```

Lo que **NO añade**:

```
✗ Tabla docker_app_runtime / docker_health_snapshots.
  Justificación §3: cache en memoria, recomputable al boot.
  
✗ Tabla event log específica para NimHealth.
  Justificación: ya existe nimos_events (eventos genéricos).
```

---

## 7. PLAN DE IMPLEMENTACIÓN (fases)

Cada fase autocontenida y revertible. No hay "big bang".

```
FASE 1 · Crear nimhealth.go + reorganización (1 sesión)
  · Mover handleServiceRoutes() + ComputeDockerAggregateHealth()
    + getDockerAppStatuses() desde apps.go/services.go.
  · Sin cambio funcional. Tests existentes deben pasar.
  · Beneficio: H6 resuelto.

FASE 2 · HealthObserver + cache en memoria (1 sesión)
  · Implementar HealthObserver con interval=30s.
  · Implementar dockerCache con RWMutex.
  · Handler reescrito a solo lectura.
  · Quitar runCmd del path HTTP.
  · Quitar reconcileServices() one-shot del main.go.
  · Mantener UNA pasada síncrona al boot (observeAll síncrono).
  · Beneficio: H1, H2, H8 resueltos.

FASE 3 · Normalización HealthStatus + migration v3 (1 sesión)
  · SQL migration con CHECK constraints + mapeo CASE.
  · Actualizar UPDATE statements en services.go.
  · serviceStart/Stop honesto (no escribe estado final). H3.
  · Frontend Svelte acepta nuevo vocabulario.
  · Polling de Svelte bajado de 5s a 10s.
  · Beneficio: H3, H4 resueltos.

FASE 4 · Reason codes + grace period + db timeouts (1 sesión)
  · Implementar enum cerrado de 8 reason codes.
  · Implementar boot grace period (host uptime).
  · context.WithTimeout(2s) en todas las queries.
  · Frontend renderiza reasonCode como texto secundario.
  · Beneficio: UX significativamente mejor en estados ambiguos.

FASE 5 · Service detectors + endpoint /detail (1 sesión)
  · Slice de detectors, añadir detectNimBackup + detectVMs.
  · Sustituir autoRegisterServices() por runAutoRegister().
  · Endpoint /api/services/{id}/detail con docker inspect on-demand.
  · Frontend añade botón "Detalle" en la tarjeta de cada app.
  · Beneficio: H7 resuelto. Detalle profundo sin coste sostenido.

FASE 6 · Limpieza final (½ sesión)
  · Quitar nivel 'optional' de ServiceDependency (H11).
  · Endurecer validateInstanceID con regex (H10).
  · Arreglar bug de getSystemdUnit para nimbackup (H12).
  · errgroup en observeAll() para paralelismo bounded (H9).
  · Beneficio: H9, H10, H11, H12 resueltos.

FUTURO (no v1, posibles v2):
  · Integración con CircuitBreaker para subsistemas Network (H13).
  · Docker events stream en lugar de poll cada 30s.
  · Boot storm de main.go: staggering de starters.
  · Adaptive interval del observer (más rápido si hay actividad).
```

---

## 8. CHECKLIST DE DISCIPLINA

Verificación contra `NIMOS_DISCIPLINE.md`:

```
[✓] ¿Resuelve un problema REAL y CONCRETO?
    → 13 hallazgos documentados, 3 críticos.

[✓] ¿Puedo dar 3 ejemplos donde el observer-loop aplica?
    → Docker engine, NimTorrent, NimBackup, VMs.

[✓] ¿Puedo dar 3 ejemplos donde NO aplica?
    → app_registry (apps UI, no se observa nada).
    → Network DDNS reconciler (ya tiene su propio observer).
    → Storage devices (ya tiene su reconciler propio).

[✓] ¿Sé cuál es el coste (mental + código)?
    → ~700 líneas nuevas en nimhealth.go, ~400 reorganizadas
      desde services.go y apps.go. Una migración SQL.

[✓] ¿Es reversible si me equivoco?
    → Fase 1 (mover) trivial revertir. Fase 3 (schema) tiene rollback
      escrito. Fases 2, 4, 5, 6 son aditivas o quirúrgicas.

[✓] ¿Es comprensible para un dev nuevo en 30 minutos?
    → Sí. Un fichero (nimhealth.go), un observer, una cache, 6 estados,
      8 reason codes, 4 detectores.

[ ] ¿Tiene tests que documenten su uso correcto?
    → A escribir durante las fases. Cada fase entrega sus tests.

[✓] ¿Está documentado?
    → Este documento (NIMHEALTH-v1.1.md).
```

---

## 9. LO QUE NO HACEMOS (frontera sagrada)

Esta sección queda **en piedra** porque define la identidad de
NimHealth. Si en el futuro se viola, NimHealth deja de ser NimHealth
y se convierte en algo distinto (y peor).

```
┌─────────────────────────────────────────────────────────────────┐
│  REGLA CENTRAL:                                                 │
│                                                                 │
│  SALUD es BINARIO/ENUM (sano/degradado/muerto/...)              │
│  MÉTRICAS son NÚMERICAS (% CPU, MB RAM, MB/s red, ...)          │
│                                                                 │
│  NimHealth hace lo PRIMERO. NimMonitor hace lo SEGUNDO.         │
└─────────────────────────────────────────────────────────────────┘
```

**Cosas que NimHealth NO hace y NO hará**:

```
❌ docker stats / docker top
   Métricas en tiempo real de containers. Caro y no es salud.
   → Pertenece a NimMonitor o panel de detalle on-demand.

❌ Persistir runtime de containers Docker en SQLite.
   Cache en memoria es suficiente. Recomputable en 150ms al boot.
   → Disciplina §2. Write amplification en SD card.

❌ Mostrar gráficas de uso (CPU/RAM histórico de un servicio).
   → NimMonitor o futuro panel time-series.

❌ Logs en tiempo real / streaming.
   → El endpoint /logs ya existe pero es pull on-demand, no streaming.

❌ Restart automático de servicios caídos.
   → systemd ya lo hace. NimHealth solo observa, no actúa.

❌ Alertas push (email, telegram, webhook).
   → Eso pertenece a NimNotifications (módulo separado).
     NimHealth emite eventos a la tabla genérica, NimNotifications
     decide qué hacer con ellos.

❌ ServiceDetector interface formal.
   Slice de funciones basta para 4-5 casos. Disciplina §1.
   → Reconsiderar cuando aparezca el 6º caso con patrón claro.

❌ Tiers internos dentro de NimHealth.
   UN observer itera todos los servicios. Si alguno cuesta órdenes
   de magnitud más, se separa entonces.

❌ Adaptive interval (más rápido si hay cambios).
   Complejidad sin caso de uso documentado. Disciplina anti-§3.
   → v2 si aparece evidencia de que merece la pena.

❌ Triple generation en service_instances.
   Es estado derivado, no convergente. Disciplina §1.

❌ Tabla doctored_app_runtime / similar persistido.
   Recomputable trivialmente. Disciplina §2.

❌ Refactor de app_registry a dos tablas.
   Alto coste, bajo beneficio inmediato. Campo `type` distingue.

❌ Breakers para systemd/docker.
   Servicios LOCALES, no externos. Disciplina §3.

❌ docker inspect en el observer loop.
   On-demand SÍ, sostenido NO. Coste lineal con N apps.
```

---

## 10. ENTREGABLES

Al cerrar v1.1, el repo debe tener:

```
/daemon/nimhealth.go             · NUEVO, ~700 líneas, home único
/daemon/nimhealth_cache.go       · puede ir junto en nimhealth.go o separado
/daemon/services.go              · adelgazado a persistencia + lifecycle
/daemon/apps.go                  · sin getDockerAppStatuses
/daemon/models.go                · vocabularios normalizados
/daemon/db.go                    · migration v3
/daemon/main.go                  · sleep 8s en startup del observer
/daemon/nimhealth_test.go        · NUEVO, tests del observer y cache
/daemon/nimhealth_cache_test.go  · tests de la cache (RWMutex, invalidación)
/src/lib/apps/NimHealth.svelte   · polling 10s, reasonCode, botón Detalle
/documents/NIMHEALTH-v1.1.md     · este documento, congelado tras review
```

---

## 11. RIESGOS

```
R1 · Frontend desactualizado durante despliegue.
     Mitigación: feature flag en backend para servir vocabulario viejo
     1-2 versiones hasta confirmar que NimHealth.svelte está actualizado.

R2 · Migration v3 falla en upgrades existentes (datos raros en DB).
     Mitigación: la migration tiene CASE con fallback a 'unknown' para
     valores no contemplados. Test con DB de Beta 8.1 antes de mergear.

R3 · Observer paraliza el daemon si docker hang.
     Mitigación: context.WithTimeout(2s) en cada runCmd. Si docker
     no responde en 2s, el tick falla con reason_code=observer_timeout
     y se reintenta en el siguiente ciclo. El observer NUNCA bloquea
     más de 2s por servicio.

R4 · Stale detection (5×interval) puede dar falsos positivos si el
     daemon estuvo pausado (suspend a disco, etc.).
     Mitigación: al boot, primer tick limpia stale antes de evaluar.
     Y stale solo afecta al response, no a la DB (no UPDATE).

R5 · Cache en memoria se pierde si daemon crashea.
     Mitigación: primer tick síncrono al boot la repuebla en ~150ms.
     Durante esos 150ms, el handler devuelve reason_code=initializing.

R6 · Boot storm general de main.go (9 starters concurrentes).
     Mitigación parcial en NimHealth: sleep 8s antes del primer tick.
     Mitigación completa: staggering de starters en main.go, queda
     como mejora separada FUTURA, fuera de scope de v1.
```

---

## 12. CIERRE

Este documento queda como **propuesta final** tras dos rondas de
refinamiento. Andrés decide:

```
[ ] APROBAR íntegro → empezar Fase 1.
[ ] APROBAR con cambios → indicar cuáles.
[ ] RECHAZAR fase concreta → cuál y por qué.
[ ] CONGELAR proyecto → razón documentada.
```

Una vez aprobado, este documento queda como referencia histórica
en `/documents/` y NO se modifica. Cambios posteriores van a
`NIMHEALTH-v2.md`.

---

## 13. APÉNDICE — `docker events` como v2 futura

Docker emite un stream de eventos por cada cambio relevante en
containers (`docker events --format '{{json .}}'`). Suscribirse a
ese stream permitiría invalidar la cache solo cuando algo cambia,
en lugar de poll cada 30s.

**Ventajas potenciales**:

```
· Latencia: cambio detectado en <100ms en vez de hasta 30s.
· Coste idle: 0 runCmd cuando nada cambia (ahora 1/30s = 2880/día).
· UX: el frontend ve cambios casi instantáneos al hacer start/stop.
```

**Costes potenciales**:

```
· Complejidad: goroutine long-running con reconnect logic.
· Parseo: docker events tiene formato propio, no es JSON limpio en
  versiones viejas de Docker.
· Coupling: NimHealth pasa a depender del binary docker estando
  vivo y respondiendo (hoy poll lo mitiga: si docker peta, siguiente
  tick lo detecta).
· Tests: stream events es más difícil de mockear que runCmd.
```

**Decisión v1**: no incluir. Poll 30s es suficiente para Pi 4 y la
UX de 10-30s de latencia es perfectamente aceptable en un NAS
doméstico.

**Reconsiderar para v2 si**:
   · Aparece feedback de "NimHealth tarda mucho en reflejar cambios".
   · CPU sostenido del observer se vuelve problemático con muchas
     apps (no esperado en Pi 4 con poll 30s).
   · Surge necesidad de propagar eventos a NimNotifications con
     baja latencia.

---

**Fin del documento.**
