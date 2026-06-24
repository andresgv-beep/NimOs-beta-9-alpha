# NIMOS NETWORK MODULE — Plan de desarrollo v4

**Versión**: 4 (21/05/2026)
**Estado**: APROBADO · próximo módulo grande tras Storage Beta 8.1
**Cambios v3 → v4**: aplicada disciplina (NIMOS_DISCIPLINE.md v2) + estrategia de migración al lado

---

## CAMBIOS FUNDAMENTALES v3 → v4

```
v3                                       v4 (mejorado)
─────────────────────────────────────────────────────────────────────
Reconciler: 4 tiers (Crit/High/Med/Low)  3 tiers + interval ortogonal
CircuitBreaker en módulo network         /daemon/breaker.go (core global)
Tabla network_breakers                   Tabla nimos_breakers (global)
Snapshot "cada scan" (write storm)       15min snapshot + eventos puntuales
Events sin protección                    Dedupe + rate limit + aggregation
Capabilities refresh cada N min          Detect boot + refresh on-demand
Migration JSON → SQLite                  NO migration: warning + ignorar JSON
"Reescribir network.go"                  Construir AL LADO, migrar por feature
HealthStatus en breaker                  HealthStatus en servicio, breaker tiene State
Sin lock global del módulo               networkMu (como storageMu)
```

---

## ⚠ CONTEXTO CRÍTICO

```
network.go ACTUAL = 1185 líneas, NO es placeholder.

Handlers funcionales en producción:
   · handleDdnsRoutes          (DDNS update con DuckDNS, etc.)
   · handleRemoteAccessRoutes  (incluye UPnP/router completo)
   · handleSshRoutes
   · handleFtpRoutes
   · handleNfsRoutes
   · handleDnsRoutes
   · handleCertsRoutes
   · handleSmbRoutes
   · handleFirewallRoutes
   · getRouterStatus / Ports / addRouterPort / removeRouterPort / testRouterPort

Stubs / semi-stubs (NO migrar al v4):
   · handleProxyRoutes   — nginx reverse proxy semi-implementado
   · handlePortalRoutes  — literal stub (devuelve httpPort:5000 hardcoded)
   · handleWebdavRoutes  — basic start/stop nginx, incompleto

→ Estos 3 se BORRAN ENTEROS en Beta 9+ con rediseño dedicado.
→ El v4 NO los toca.
```

**Implicación arquitectónica**: el v4 NO reemplaza network.go de golpe. Se construye **AL LADO** en archivos nuevos. La migración es feature por feature, con tests E2E que validan equivalencia funcional antes de borrar el handler viejo.

---

## ESTRATEGIA DE MIGRACIÓN AL LADO

```
┌─────────────────────────────────────────────────────────────────┐
│ FASE 1: Construir capa nueva sin tocar network.go              │
│                                                                 │
│ daemon/network.go              ← 1185 líneas, INTACTO          │
│ daemon/breaker.go              ← NUEVO (core, global)          │
│ daemon/network_schema.sql      ← NUEVO                         │
│ daemon/network_repo.go         ← NUEVO                         │
│ daemon/network_reconciler.go   ← NUEVO                         │
│ daemon/network_observer.go     ← NUEVO                         │
│ daemon/network_observer_test.go← NUEVO                         │
│ daemon/nimos_secrets.go        ← NUEVO (core, global)          │
│ daemon/nimos_capabilities.go   ← NUEVO (core, global)          │
│ daemon/network_handlers_v4.go  ← NUEVOS handlers (rutas v4)    │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ FASE 2: Migrar feature por feature                              │
│                                                                 │
│ Para CADA feature (DDNS, Certs, Ports, ...):                    │
│   1. Implementar v4 en archivos nuevos                          │
│   2. Test E2E: misma input → misma output que handler viejo     │
│   3. Tests automatizados pasan                                  │
│   4. Smoke test en producción (nimosbarraca.duckdns.org)        │
│   5. Borrar la función vieja de network.go                      │
│   6. Eliminar las rutas viejas de registerNetworkRoutes()       │
│                                                                 │
│ Si el v4 NO funciona equivalente, el viejo SIGUE VIVO.          │
│ La regla es: no se borra lo que funciona hasta que el           │
│ reemplazo funciona.                                             │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ FASE 3: Limpieza final                                          │
│                                                                 │
│ Cuando todas las features migradas:                             │
│   · network.go queda solo con Proxy + Portal + WebDAV stubs    │
│   · Renombrar a network_legacy.go (o lo que decidamos)         │
│   · Beta 9+ rediseña esos 3 desde cero                          │
└─────────────────────────────────────────────────────────────────┘
```

**Migración JSON → SQLite (decisión v4)**:

```
NO se hace migración formal de network.json a SQLite.

Razón: network.json contiene configs simples (DDNS provider/token,
puertos) que el usuario puede re-introducir en 30 segundos vía UI
del v4. El esfuerzo de migration robusta (handle corrupciones,
formatos viejos, conflictos) no compensa.

POLÍTICA al boot del daemon:
   1. Detect si existe /var/lib/nimos/network.json (o similar).
   2. Si existe → log WARNING:
      "network.json detectado pero ignorado. Usar UI para
       reconfigurar DDNS y certificados."
   3. NO leer ni mover el archivo. NO borrar.
   4. Empezar con SQLite vacío.
   5. UI mostrará banner: "Configuración legacy detectada,
      por favor reconfigura tus servicios en Settings → Network."
```

---

## PRINCIPIOS APLICADOS (referencia a NIMOS_DISCIPLINE.md v2)

Cada decisión del v4 sigue una regla concreta del documento de disciplina. Las referencias inline son:

```
HealthStatus 6 estados                  → DISCIPLINE §6
Triple Generation solo en convergibles  → DISCIPLINE §1
SystemCapabilities on-demand            → DISCIPLINE §7
CircuitBreaker en core + lazy persist   → DISCIPLINE §3
Reconciler tier ≠ interval              → DISCIPLINE §5
Events anti-explosion                   → DISCIPLINE §4
Snapshots 15min + retention             → DISCIPLINE §2
HealthStatus en servicio no en state    → DISCIPLINE §8
```

Si una decisión del v4 no encaja con un §, hay que volver a DISCIPLINE o documentar la excepción. **No se inventan patrones sobre la marcha.**

---

## PATRONES TRANSVERSALES — VERSIÓN v4

### 1. HealthStatus (sin cambios respecto a v3)

```go
package nimos

type HealthStatus string

const (
    HealthHealthy  HealthStatus = "healthy"
    HealthDegraded HealthStatus = "degraded"
    HealthFailed   HealthStatus = "failed"
    HealthPartial  HealthStatus = "partial"
    HealthUnknown  HealthStatus = "unknown"
    HealthStale    HealthStatus = "stale"
)

func (h HealthStatus) Severity() int { /* ... */ }
func HealthAggregate(statuses ...HealthStatus) HealthStatus { /* ... */ }
```

### 2. Triple Generation / Convergence (sin cambios)

```go
type Convergence struct {
    Desired  int64 `json:"desired_generation"`
    Observed int64 `json:"observed_generation"`
    Applied  int64 `json:"applied_generation"`
}

func (c Convergence) IsConverged() bool { return c.Applied == c.Desired }
func (c Convergence) HasDrifted() bool  { return c.Observed != c.Applied }
func (c Convergence) IsPending() bool   { return c.Applied < c.Desired }
```

**Aplicado SOLO a**: `network_ports`, `network_ddns`, `network_certs`. NO a eventos, logs, capabilities, snapshots.

### 3. SystemCapabilities — refresh on-demand (CAMBIO v4)

```go
package nimos

// Vive en /daemon/nimos_capabilities.go (core, global).
// Network solo añade campos al struct.

type SystemCapabilities struct {
    // Network
    CertbotInstalled bool
    CertbotVersion   string
    OpenSSLInstalled bool
    UPnPClient       bool
    NFTBackend       bool
    IPTablesBackend  bool
    UFWInstalled     bool
    DigInstalled     bool
    HostInstalled    bool

    // System
    SystemdAvailable bool

    DetectedAt time.Time
}

// API:
//   GetCapabilities() *SystemCapabilities       — cache, refresca si > 1h
//   ForceRefreshCapabilities() *SystemCapabilities — manual
//
// NO hay goroutine de polling activo.
// El frontend pide /api/network/capabilities → backend decide refresh.
```

### 4. CircuitBreaker — en core, persistencia mínima (CAMBIO v4)

```go
// Vive en /daemon/breaker.go (core, global, reutilizable).
// Tabla: nimos_breakers (no network_breakers).

package nimos

type CircuitState string

const (
    CircuitClosed   CircuitState = "closed"
    CircuitOpen     CircuitState = "open"
    CircuitHalfOpen CircuitState = "half_open"
)

type CircuitBreaker struct {
    Name             string
    FailureThreshold int
    CooldownDuration time.Duration
    HalfOpenMaxCalls int

    mu          sync.Mutex
    state       CircuitState
    failures    int
    lastFailure time.Time
    nextRetry   time.Time

    // Persistencia inyectada (lazy)
    persist func(name string, state CircuitState, nextRetry time.Time) error
}

// Call ejecuta fn() respetando el estado del breaker.
// Solo persiste a SQLite cuando el state CAMBIA.
func (b *CircuitBreaker) Call(fn func() error) error { /* ... */ }

// GetState devuelve el state interno. NO devuelve HealthStatus.
// La traducción state → health la hace el observer del servicio
// que usa este breaker (DISCIPLINE §8).
func (b *CircuitBreaker) GetState() CircuitState { /* ... */ }

// Registry global de breakers (singleton):
//   nimos.RegisterBreaker(name, config) *CircuitBreaker
//   nimos.GetBreaker(name) *CircuitBreaker
//   nimos.ListBreakers() []BreakerSnapshot
//
// Al boot del daemon, el registry restaura state desde nimos_breakers:
//   · state=open  y next_retry > now  → respetar cooldown
//   · state=open  y next_retry < now  → arrancar half_open
//   · state=half_open                 → arrancar closed
```

**Casos de uso esperados** (no exclusivos de network):

```
duckdns           → network
letsencrypt       → network
ifconfig.me       → network (public IP detect)
upnp.router       → network
backup.s3         → backup (futuro)
backup.b2         → backup (futuro)
appstore.docker   → apps
notify.pushover   → notifications (futuro)
notify.telegram   → notifications (futuro)
```

### 5. Reconciler — Tier + Interval separados (CAMBIO v4)

```go
// Tier es prioridad, NO intervalo (DISCIPLINE §5 refinada).

type ReconcilerTier string

const (
    TierCritical ReconcilerTier = "critical"
    TierMedium   ReconcilerTier = "medium"
    TierLow      ReconcilerTier = "low"
)

type NamedReconciler struct {
    Name         string
    Tier         ReconcilerTier
    Interval     time.Duration  // ortogonal al Tier
    Reconciler   Reconciler

    // Si Tier=Critical y observer detecta drift → ejecutar AHORA,
    // sin esperar al próximo Interval.
    ForceOnDrift bool
}

// Asignaciones Network v4:
//
//   cert_renewer        Tier=Critical  Interval=60s    ForceOnDrift=true
//   port_listener       Tier=Critical  Interval=60s    ForceOnDrift=true
//   ddns_updater        Tier=Medium    Interval=900s   ForceOnDrift=false
//   upnp_refresh        Tier=Low       Interval=3600s  ForceOnDrift=false
//   capability_refresh  Tier=Low       Interval=86400s ForceOnDrift=false
//
// El scheduler central:
//   · Critical: bypass de cola en caso de drift.
//   · Medium: serializa con networkMu (lock del módulo).
//   · Low: posterga si CPU > X% o si hay contention.
```

---

## networkMu — LOCK GLOBAL DEL MÓDULO

```go
// Como storageMu en el módulo Storage.
// Serializa reconcilers que hacen WRITE (config, certs, DDNS update, etc.)
// para evitar race conditions y operaciones contradictorias.
//
// READ operations (observer, GET endpoints) NO toman el lock.
// Usan atomic.Pointer al ObservedSnapshot.

var networkMu sync.Mutex

// Patrón típico:
func (r *DDNSReconciler) Reconcile(ctx context.Context) error {
    networkMu.Lock()
    defer networkMu.Unlock()
    // ... operaciones write
}

// Para operaciones de muy larga duración (emisión de cert que tarda
// >30s), usar lock con timeout:
func (r *CertReconciler) Reconcile(ctx context.Context) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
    defer cancel()
    if !tryLockWithCtx(&networkMu, ctx) {
        return errLockTimeout
    }
    defer networkMu.Unlock()
    // ... cert issuance
}
```

---

## ARQUITECTURA NETWORK v4

```
┌─────────────────────────────────────────────────────────────────┐
│ CAPA 7 · HTTP API (handlers v4 en network_handlers_v4.go)       │
└────────────────────┬────────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────────┐
│ CAPA 6 · SERVICE + RECONCILERS                                  │
│ Protegido por networkMu (writes)                                │
└─┬───────────────┬───────────────┬───────────────────┬───────────┘
  │               │               │                   │
┌─▼─────────────┐ │ ┌─────────────▼─┐ ┌───────────────▼─┐
│DDNSReconciler │ │ │CertReconciler │ │PortReconciler   │
│Medium/900s    │ │ │Critical/60s   │ │Critical/60s     │
└─┬─────────────┘ │ └───────┬───────┘ └───────┬─────────┘
  │               │         │                 │
  │               │ ┌───────▼────────────┐    │
  │               │ │ Providers wrapped  │    │
  │               │ │ con CircuitBreaker │    │
  │               │ │ del registry GLOBAL│    │
  │               │ └───────┬────────────┘    │
  │               │         │                 │
┌─▼───────────────▼─────────▼─────────────────▼───────────────────┐
│ CAPA 5 · OBSERVER (atomic.Pointer + persistencia 15min)         │
│ NetworkObservedSnapshot (in-memory) + network_observed (SQLite) │
└────────────────────┬────────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────────┐
│ CAPA 4 · BACKENDS (con SystemCapabilities awareness)            │
└────────────────────┬────────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────────┐
│ CAPA 3 · EXECUTOR (mockable)                                    │
└────────────────────┬────────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────────┐
│ CAPA 2 · TIPOS + UTILITIES (HealthStatus, Convergence)          │
└────────────────────┬────────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────────┐
│ CAPA 1 · SCHEMA + REPO + SECRETS                                │
│ network_* tables + nimos_secrets (AES-GCM) + nimos_breakers     │
└─────────────────────────────────────────────────────────────────┘

CORE GLOBAL (fuera del módulo network, reutilizable):
   /daemon/breaker.go              CircuitBreaker + registry
   /daemon/nimos_secrets.go        AES-GCM secrets store
   /daemon/nimos_capabilities.go   SystemCapabilities detect
```

---

## SCHEMA SQLITE v4

```sql
-- =============================================================================
-- nimos_secrets — Tabla GLOBAL de secretos AES-GCM (no solo network)
-- Vive en core, no en módulo network.
-- =============================================================================
CREATE TABLE IF NOT EXISTS nimos_secrets (
    id              TEXT    PRIMARY KEY,
    category        TEXT    NOT NULL,        -- 'ddns_token', 'api_key', 'ssh_key'
    label           TEXT    NOT NULL,

    ciphertext      BLOB    NOT NULL,        -- AES-GCM encrypted
    nonce           BLOB    NOT NULL,        -- nonce único por secret
    key_version     INTEGER NOT NULL DEFAULT 1,

    created_at      TEXT    NOT NULL,
    last_accessed   TEXT,

    UNIQUE(category, label)
);
CREATE INDEX IF NOT EXISTS idx_secrets_category ON nimos_secrets(category);

-- =============================================================================
-- nimos_breakers — Estado GLOBAL de circuit breakers (CAMBIO v4: era network_breakers)
-- Persistencia MÍNIMA (DISCIPLINE §3 refinada): solo lo necesario para
-- respetar cooldown across daemon restart.
-- =============================================================================
CREATE TABLE IF NOT EXISTS nimos_breakers (
    name              TEXT    PRIMARY KEY,        -- 'duckdns', 'letsencrypt', etc.
    state             TEXT    NOT NULL CHECK(state IN ('closed','open','half_open')),
    next_retry_at     TEXT                        -- NULL si state=closed
);
-- NO almacenamos failure counts, histograms ni métricas aquí.
-- Si necesitamos métricas → tabla aparte (nimos_metrics) o Prometheus.

-- =============================================================================
-- nimos_capabilities — Cache de detection
-- =============================================================================
CREATE TABLE IF NOT EXISTS nimos_capabilities (
    id               TEXT PRIMARY KEY,            -- 'system' (singleton)
    detected_at      TEXT NOT NULL,
    capabilities     TEXT NOT NULL                -- JSON SystemCapabilities
);

-- =============================================================================
-- network_ports — Puertos del daemon (con triple generation)
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_ports (
    id                   TEXT    PRIMARY KEY,        -- 'http', 'https'
    port                 INTEGER NOT NULL CHECK(port > 0 AND port < 65536),
    bind_address         TEXT    NOT NULL DEFAULT '0.0.0.0',
    enabled              INTEGER NOT NULL DEFAULT 1,

    desired_generation   INTEGER NOT NULL DEFAULT 0 CHECK(desired_generation >= 0),
    observed_generation  INTEGER NOT NULL DEFAULT 0 CHECK(observed_generation >= 0),
    applied_generation   INTEGER NOT NULL DEFAULT 0 CHECK(applied_generation >= 0),

    updated_at           TEXT    NOT NULL
);

-- =============================================================================
-- network_ddns — DDNS config (tokens encrypted via nimos_secrets)
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_ddns (
    id                   TEXT    PRIMARY KEY,
    provider             TEXT    NOT NULL CHECK(provider IN ('duckdns','noip','dynu','freedns','cloudflare')),
    domain               TEXT    NOT NULL,

    token_secret_id      TEXT    NOT NULL,       -- FK a nimos_secrets.id

    enabled              INTEGER NOT NULL DEFAULT 0,
    auto_update          INTEGER NOT NULL DEFAULT 1,
    update_interval      INTEGER NOT NULL DEFAULT 900,

    last_run_at          TEXT,
    last_run_result      TEXT,
    last_ip              TEXT,

    desired_generation   INTEGER NOT NULL DEFAULT 0,
    observed_generation  INTEGER NOT NULL DEFAULT 0,
    applied_generation   INTEGER NOT NULL DEFAULT 0,

    FOREIGN KEY (token_secret_id) REFERENCES nimos_secrets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ddns_provider ON network_ddns(provider);

-- =============================================================================
-- network_certs — Certificados con convergence
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_certs (
    id                   TEXT    PRIMARY KEY,
    domain               TEXT    NOT NULL UNIQUE,

    cert_provider        TEXT    NOT NULL
        CHECK(cert_provider IN ('letsencrypt','letsencrypt_staging','zerossl','selfsigned')),
    challenge_type       TEXT
        CHECK(challenge_type IS NULL OR challenge_type IN ('http-01','dns-01')),
    dns_provider         TEXT
        CHECK(dns_provider IS NULL OR dns_provider IN ('duckdns','cloudflare','route53','dynu','porkbun')),

    fullchain_path       TEXT    NOT NULL,
    privkey_path         TEXT    NOT NULL,

    not_before           TEXT    NOT NULL,
    not_after            TEXT    NOT NULL,

    enabled              INTEGER NOT NULL DEFAULT 1,
    auto_renew           INTEGER NOT NULL DEFAULT 1,

    issued_at            TEXT    NOT NULL,
    last_renewed_at      TEXT,

    desired_generation   INTEGER NOT NULL DEFAULT 0,
    observed_generation  INTEGER NOT NULL DEFAULT 0,
    applied_generation   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_certs_domain     ON network_certs(domain);
CREATE INDEX IF NOT EXISTS idx_certs_not_after  ON network_certs(not_after);
CREATE INDEX IF NOT EXISTS idx_certs_auto_renew ON network_certs(auto_renew) WHERE auto_renew = 1;

-- =============================================================================
-- network_observed — Snapshots históricos
-- Política v4 (DISCIPLINE §2): 15min snapshot completo + eventos puntuales
-- Retention: 100 últimos / 1 por hora último día / 1 por día último mes
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_observed (
    id              TEXT    PRIMARY KEY,
    generation      INTEGER NOT NULL,
    snapshot_at     TEXT    NOT NULL,

    snapshot_type   TEXT    NOT NULL DEFAULT 'periodic'
        CHECK(snapshot_type IN ('periodic','event','boot','manual')),
    -- 'periodic' = los 15min scheduled
    -- 'event'    = cambio detectado entre periodics
    -- 'boot'     = primer scan al arrancar
    -- 'manual'   = forzado por admin

    snapshot_data   TEXT    NOT NULL,  -- JSON serializado

    -- Métricas indexadas para queries rápidas sin parsear JSON
    overall_health   TEXT    NOT NULL CHECK(overall_health IN ('healthy','degraded','failed','partial','unknown','stale')),
    public_ip        TEXT,
    ddns_synced      INTEGER,
    certs_total      INTEGER NOT NULL DEFAULT 0,
    certs_expiring   INTEGER NOT NULL DEFAULT 0,
    divergence_count INTEGER NOT NULL DEFAULT 0,
    scan_duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_observed_at     ON network_observed(snapshot_at DESC);
CREATE INDEX IF NOT EXISTS idx_observed_health ON network_observed(overall_health);
CREATE INDEX IF NOT EXISTS idx_observed_gen    ON network_observed(generation DESC);
CREATE INDEX IF NOT EXISTS idx_observed_type   ON network_observed(snapshot_type);

-- =============================================================================
-- network_operations — Con ownership formal
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_operations (
    id                TEXT    PRIMARY KEY,
    type              TEXT    NOT NULL,
    target_id         TEXT,
    status            TEXT    NOT NULL
        CHECK(status IN ('pending','in_progress','completed','failed','rolled_back')),

    triggered_by      TEXT    NOT NULL
        CHECK(triggered_by LIKE 'user:%' OR triggered_by LIKE 'reconciler:%' OR triggered_by = 'system:boot' OR triggered_by = 'system:scheduler'),
    request_id        TEXT,
    parent_operation  TEXT,

    started_at        TEXT    NOT NULL,
    completed_at      TEXT,
    error             TEXT,
    error_code        TEXT,
    data              TEXT,

    FOREIGN KEY (parent_operation) REFERENCES network_operations(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_net_ops_status    ON network_operations(status);
CREATE INDEX IF NOT EXISTS idx_net_ops_started   ON network_operations(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_net_ops_triggered ON network_operations(triggered_by);
CREATE INDEX IF NOT EXISTS idx_net_ops_request   ON network_operations(request_id);

-- =============================================================================
-- network_events — Con dedupe + rate limit + category (DISCIPLINE §4)
-- =============================================================================
CREATE TABLE IF NOT EXISTS network_events (
    id           TEXT PRIMARY KEY,
    operation_id TEXT,                       -- nullable (eventos sin operación)
    timestamp    TEXT NOT NULL,

    category     TEXT NOT NULL,              -- 'ddns', 'cert', 'port', 'upnp', 'breaker'
    event        TEXT NOT NULL,              -- 'update_started', 'update_failed', etc.
    target_id    TEXT,                       -- entity afectada (ddns id, cert id, etc.)

    level        TEXT NOT NULL CHECK(level IN ('debug','info','warn','error')),
    message      TEXT NOT NULL,
    details      TEXT,                       -- JSON

    -- Dedupe: si mismo (category, event, target_id) en ventana 5min
    -- → no se inserta otro, se incrementa este counter.
    occurrences  INTEGER NOT NULL DEFAULT 1,
    last_seen_at TEXT    NOT NULL,

    FOREIGN KEY (operation_id) REFERENCES network_operations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_events_operation  ON network_events(operation_id);
CREATE INDEX IF NOT EXISTS idx_events_timestamp  ON network_events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_category   ON network_events(category, event);
CREATE INDEX IF NOT EXISTS idx_events_level      ON network_events(level);
CREATE INDEX IF NOT EXISTS idx_events_dedupe     ON network_events(category, event, target_id, last_seen_at DESC);
```

---

## POLÍTICA DE EVENTS (DISCIPLINE §4 — anti-explosión)

```
PROBLEMA si no se hace nada:
   5 reconcilers × cada 15 min × 4 eventos/run × 365 días ≈ 700k eventos/año
   → SD card sufre, queries lentas, dashboard inutilizable.

5 ANTÍDOTOS OBLIGATORIOS:

(A) DEDUPE EN RUNTIME (ventana 5 min):
    function emitEvent(category, event, target_id, level, message, details):
        existing = SELECT id, occurrences FROM network_events
                    WHERE category = ? AND event = ? AND target_id = ?
                    AND last_seen_at > datetime('now', '-5 minutes')
                    ORDER BY last_seen_at DESC LIMIT 1
        if existing:
            UPDATE network_events
            SET occurrences = occurrences + 1,
                last_seen_at = datetime('now')
            WHERE id = existing.id
        else:
            INSERT INTO network_events (...) VALUES (...)

(B) RATE LIMIT por category:
    Bucket en memoria por category: max 10 events/min.
    Si excede → drop con métrica "events_dropped_<category>" en memoria.

(C) AGGREGATION NOCTURNA (cron a las 03:00):
    Para cada category, comprimir el día anterior:
       · Mantener: TODOS los errors y warns individuales.
       · Mantener: 1 evento de muestra por (category, event) único.
       · Resumen: "category=ddns event=update_succeeded count=96 day=2026-05-20".
       · Borrar los individuales rutinarios.

(D) RETENTION agresiva (cron nocturno):
       level=error : DELETE WHERE timestamp < now - 90 days
       level=warn  : DELETE WHERE timestamp < now - 30 days
       level=info  : DELETE WHERE timestamp < now - 7 days
       level=debug : DELETE WHERE timestamp < now - 24 hours

(E) NIVELES CORRECTOS (importante, fácil equivocarse):
    "Reconciler started"           → debug  (no info)
    "DDNS update succeeded"        → debug  (rutina)
    "DDNS IP changed: X → Y"       → info   (cambio real)
    "DDNS update failed"           → warn
    "Cert expiring < 7 days"       → warn
    "Cert expired"                 → error
    "Breaker opened: duckdns"      → warn
    "Breaker open + retry"         → debug (cada 30s = ruido)
    "Breaker closed (recovered)"   → info  (recuperación = info)
```

---

## POLÍTICA DE OBSERVED SNAPSHOTS (DISCIPLINE §2)

```
PROBLEMA si snapshot cada scan:
   Observer scan cada 30-60s × 24h × 365 días → ~525k-1M snapshots/año
   en SD card del Pi → write storm.

POLÍTICA v4:

(A) SCAN frecuente (in-memory):
    Observer corre cada 30s.
    Resultado: atomic.Pointer[NetworkObservedSnapshot] in memory.
    NO se escribe a SQLite cada scan.

(B) PERSISTENCIA periodic (cada 15 min):
    Cada 15 min: snapshot completo → network_observed (type='periodic').

(C) PERSISTENCIA por evento:
    Entre los periodics, si el observer detecta CAMBIO significativo:
       · public_ip cambió
       · cert pasó de healthy a degraded/failed
       · ddns convergence rompió
       · listener cayó
       · breaker abrió
    → escribir snapshot extra (type='event').

(D) PERSISTENCIA al boot:
    Primer scan tras arrancar el daemon: snapshot (type='boot').
    Útil para post-mortem de reinicios inesperados.

(E) RETENTION:
    Mantener los 100 más recientes (cualquier type).
    Mantener 1 por hora durante el último día (preferir 'event').
    Mantener 1 por día durante el último mes.
    Borrar el resto.

(F) SQLite settings:
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous  = NORMAL;
    PRAGMA wal_autocheckpoint = 1000;
```

---

## NetworkObservedSnapshot (sin cambios estructurales)

```go
type NetworkObservedSnapshot struct {
    Generation int64     `json:"generation"`
    Timestamp  time.Time `json:"timestamp"`

    Capabilities *SystemCapabilities `json:"capabilities"`

    OverallHealth HealthStatus `json:"overall_health"`

    PublicIP     *PublicIPObserved      `json:"public_ip"`
    DDNS         []DDNSObserved         `json:"ddns"`
    Certs        []CertObserved         `json:"certs"`
    Listeners    []ListenerObserved     `json:"listeners"`
    Router       *RouterObserved        `json:"router,omitempty"`
    PortForwards []PortForwardObserved  `json:"port_forwards"`
    Breakers     []BreakerObserved      `json:"breakers"`

    Divergences []NetworkDivergence `json:"divergences"`
    ScanDurationMs int64             `json:"scan_duration_ms"`
}

// IMPORTANTE: BreakerObserved traduce CircuitState → HealthStatus
// (DISCIPLINE §8). El breaker en sí solo expone CircuitState.
type BreakerObserved struct {
    Name      string       `json:"name"`
    State     CircuitState `json:"state"`        // del breaker
    Health    HealthStatus `json:"health"`       // traducido por observer
    NextRetry *time.Time   `json:"next_retry,omitempty"`
}
```

---

## FEATURES v4 — REARQUITECTURADAS

### F-001 — Core global + Schema + Reconciler base ⭐ FUNDACIÓN

**Coste**: ~8h
**Outputs**:
- `daemon/breaker.go` (core, global) + tests
- `daemon/nimos_secrets.go` (AES-GCM) + tests
- `daemon/nimos_capabilities.go` (detect + lazy refresh)
- `daemon/network_schema.sql` (tablas v4)
- `daemon/network_repo.go` (CRUD)
- `daemon/network_reconciler.go` (interface + scheduler con Tier + Interval)
- `daemon/network_events.go` (emit con dedupe + rate limit)
- Master key file `/var/lib/nimos/keys/master.key` (chmod 600)
- Helpers: `HealthStatus`, `Convergence`

**Migración**: cero. Si existe network.json → log warning + ignorar.
**Network.go viejo**: intacto.

### F-002 — NetworkObserver + Capabilities lazy

**Coste**: ~5h
**Outputs**:
- `daemon/network_observer.go` con `atomic.Pointer[NetworkObservedSnapshot]`
- Scan cada 30s in-memory
- Persistencia: 15min periodic + eventos + boot
- Retention job nocturno
- `SystemCapabilities` integrado en snapshot
- Endpoint v4: `GET /api/v4/network/observed`
- Endpoint v4: `GET /api/v4/network/observed/history?from=&to=`

**Network.go viejo**: intacto.

### F-003 — Puertos configurables ⭐ FRICCIÓN INMEDIATA

**Coste**: ~3h
**Outputs**:
- Reconciler `port_listener` (Critical / 60s)
- UI: cambiar puerto sin reiniciar Pi (reload graceful)
- Endpoint v4: `GET/POST /api/v4/network/ports`

**Migración network.go**:
- Una vez F-003 validado en producción, eliminar `handlePortalRoutes` (stub).
- Marcar para eliminación final en Beta 9+.

### F-004 — DDNS Reconciler + Breaker

**Coste**: ~4h
**Outputs**:
- Reconciler `ddns_updater` (Medium / 900s)
- Provider DuckDNS wrapped en `nimos.GetBreaker("duckdns")`
- Tokens encrypted via nimos_secrets
- Endpoint v4: `GET/POST/DELETE /api/v4/network/ddns`

**Migración network.go**:
- Test E2E: comparar `ddnsUpdateGo()` viejo vs reconciler v4 con mismo input.
- Equivalencia confirmada → eliminar `handleDdnsRoutes` y `ddnsUpdateGo`.
- Actualizar `registerNetworkRoutes` para quitar rutas viejas DDNS.

### F-005 — Cert + DNS Providers desacoplados + Breaker ⭐ ARQUITECTURA

**Coste**: ~7h
**Outputs**:
- Reconciler `cert_renewer` (Critical / 60s, ForceOnDrift=true)
- Interface `CertProvider` (letsencrypt, letsencrypt_staging, selfsigned)
- Interface `DNSChallengeProvider` (duckdns, cloudflare stub)
- Cada provider wrapped en su breaker del registry global
- Endpoint v4: `GET/POST /api/v4/network/certs`

**Migración network.go**:
- Tests E2E: emisión de cert self-signed y let's encrypt staging.
- Equivalencia confirmada → eliminar `handleCertsRoutes` y `parseCertbotCertificates`.

### F-006 — Diagnóstico pre-cert + Capabilities API

**Coste**: ~3h
**Outputs**:
- Endpoint v4: `GET /api/v4/network/capabilities` (refresh lazy si > 1h)
- Endpoint v4: `POST /api/v4/network/capabilities/refresh` (forzar)
- Endpoint v4: `GET /api/v4/network/diagnose/cert?domain=...`
  - Checks: domain resuelve, puerto 80 abierto, certbot instalado, etc.
  - Devuelve checklist con hints específicos por capability faltante.

**Network.go viejo**: intacto.

### F-007 — UPnP best-effort + Breaker

**Coste**: ~5h
**Outputs**:
- Reconciler `upnp_refresh` (Low / 3600s)
- `RouterProvider` interface (upnp, manual)
- Wrapped en breaker `upnp.router`
- UX honesta: "Tu router no responde a UPnP (común en Movistar/Vodafone)"
- Fallback: instrucciones manuales claras
- Endpoint v4: `GET/POST /api/v4/network/router`

**Migración network.go**:
- Tests E2E con router real (NAS producción).
- Equivalencia confirmada → eliminar `handleRemoteAccessRoutes`, `getRemoteAccessStatusGo`, `getRouterStatus`, `getRouterPorts`, `addRouterPort`, `removeRouterPort`, `testRouterPort`.

### F-008 — Polling cleanup + Event aggregation job

**Coste**: ~3h
**Outputs**:
- Cron nocturno 03:00 para aggregation + retention de events
- Cron nocturno para retention de observed snapshots
- API endpoints con `Cache-Control: max-age=60` apropiados
- Limpiar polling activo donde quedaba en código frontend

---

## ORDEN DE IMPLEMENTACIÓN v4

```
SESIÓN 1 (~8h): F-001 Core global + Schema + Reconciler base
   ⭐ FUNDACIÓN — sin esto el resto no funciona
   network.go viejo SIGUE VIVO

SESIÓN 2 (~5h): F-002 Observer + Capabilities
   ⭐ ESTADO ANTES DE ACCIONES
   network.go viejo SIGUE VIVO

SESIÓN 3 (~3h): F-003 Puertos configurables
   Fricción real (Synology vs NimOS)
   Tests E2E + smoke test producción
   → Borrar handlePortalRoutes stub

SESIÓN 4 (~4h): F-004 DDNS Reconciler + Breaker
   Equivalencia con ddnsUpdateGo viejo
   Tests E2E con DuckDNS (puede usar account de test)
   → Borrar handleDdnsRoutes + ddnsUpdateGo

SESIÓN 5 (~7h): F-005 Cert + DNS providers + Breaker
   Tests E2E self-signed + let's encrypt staging
   → Borrar handleCertsRoutes + parseCertbotCertificates

SESIÓN 6 (~3h): F-006 Diagnóstico + Capabilities API
   network.go viejo intacto (capabilities es feature nueva)

SESIÓN 7 (~5h): F-007 UPnP best-effort
   Tests con router real (nimosbarraca)
   → Borrar handleRemoteAccessRoutes y router helpers

SESIÓN 8 (~3h): F-008 Aggregation + cleanup polling

SESIÓN 9 (~3h): Tests E2E completos + documentación + review final
   Confirmar:
   · network.go solo queda con: Proxy, Portal, WebDAV, SSH, FTP, NFS, DNS, SMB, Firewall
   · De esos, los 3 stubs son para Beta 9+
   · SSH/FTP/NFS/DNS/SMB/Firewall son módulos separados (no Network), se quedan
```

**TOTAL: ~41h en 9 sesiones disciplinadas**.

```
v2: 30h
v3: 38h  (+ AES-GCM, capabilities, breakers, persistencia, categorization)
v4: 41h  (+ tier/interval split, migración al lado con tests E2E, dedupe events)

Las 3h extra v3→v4 son:
   · Tests E2E de equivalencia (cada feature)  ~1h
   · Lógica dedupe de events + aggregation     ~1h
   · Capabilities lazy refresh on-demand        ~0.5h
   · networkMu + tryLockWithCtx                 ~0.5h
```

---

## INVENTARIO network.go ACTUAL — PLAN DE MIGRACIÓN

```
HANDLER ANTIGUO                  → FEATURE v4    → ACCIÓN FINAL
─────────────────────────────────────────────────────────────────
handleDdnsRoutes (L93)           → F-004         → BORRAR tras E2E
ddnsUpdateGo (L149)              → F-004         → BORRAR tras E2E
handleRemoteAccessRoutes (L188)  → F-007         → BORRAR tras E2E
getRemoteAccessStatusGo (L368)   → F-007         → BORRAR tras E2E
handleSshRoutes (L454)           → (otro módulo) → MANTENER
handleFtpRoutes (L477)           → (otro módulo) → MANTENER
handleNfsRoutes (L501)           → (otro módulo) → MANTENER
handleDnsRoutes (L526)           → (otro módulo) → MANTENER
handleCertsRoutes (L548)         → F-005         → BORRAR tras E2E
parseCertbotCertificates (L1089) → F-005         → BORRAR tras E2E
handleProxyRoutes (L610)         → Beta 9+       → MANTENER por ahora
handlePortalRoutes (L644)        → F-003         → BORRAR tras E2E
handleWebdavRoutes (L665)        → Beta 9+       → MANTENER por ahora
handleSmbRoutes (L695)           → (otro módulo) → MANTENER
handleFirewallRoutes (L769)      → (otro módulo) → MANTENER
getFirewallRulesGo (L812)        → (otro módulo) → MANTENER
getListeningPortsGo (L817)       → (otro módulo) → MANTENER
getFirewallScanGo (L822)         → (otro módulo) → MANTENER
registerNetworkRoutes (L832)     → mixto         → ACTUALIZAR (quitar rutas v4)
getRouterStatus (L855)           → F-007         → BORRAR tras E2E
getRouterPorts (L902)            → F-007         → BORRAR tras E2E
addRouterPort (L961)             → F-007         → BORRAR tras E2E
removeRouterPort (L1008)         → F-007         → BORRAR tras E2E
testRouterPort (L1031)           → F-007         → BORRAR tras E2E

Tras Beta 8 Network completo, network.go debería quedar reducido a:
   · SSH/FTP/NFS/DNS/SMB/Firewall handlers (no son red, son shares/seguridad)
   · Stubs Proxy/Portal/WebDAV (rediseño Beta 9+)
   · ~400 líneas, no 1185
```

**Decisión pendiente para Beta 9+**: ¿SSH/FTP/NFS/DNS/SMB/Firewall se quedan donde están o salen a sus propios módulos (sharing.go, security.go)? Punto a discutir al cerrar Beta 8.

---

## CRITERIOS DE CIERRE v4

```
✓ network.go viejo intacto durante construcción
✓ Migración feature por feature con tests E2E
✓ Cada feature solo se considera migrada cuando test E2E pasa
✓ Todo en SQLite (NO JSON anywhere para datos nuevos)
✓ network.json: log warning + ignorar (NO migration)
✓ Tokens AES-GCM at-rest desde día 1
✓ Reconciler con Tier + Interval separados (3 tiers)
✓ Cert + DNS providers desacoplados, ambos en breaker
✓ NetworkObserver con atomic.Pointer + persistencia 15min + eventos
✓ Triple Generation en Ports, DDNS, Certs (solo esas 3)
✓ HealthStatus enum unificado en observed entities
✓ Breaker expone CircuitState, observer traduce a HealthStatus
✓ SystemCapabilities detect at boot + refresh on-demand (NO polling)
✓ CircuitBreaker en /daemon/breaker.go (core global, no en network)
✓ Tabla nimos_breakers (no network_breakers)
✓ Persistencia mínima: name + state + next_retry_at
✓ Events con dedupe (5min) + rate limit (10/min/cat) + aggregation
✓ networkMu serializa reconcilers en operaciones write
✓ Endpoint /api/v4/network/capabilities funcional
✓ UPnP best-effort con UX honesta
✓ Diagnóstico pre-cert estructurado
✓ DDNS auto-update funcional
✓ Puertos configurables sin reiniciar Pi
✓ Build/test/race/vet TODO VERDE
✓ Test E2E real (NAS Raspberry Pi nimosbarraca):
   · Cambiar puerto → reload sin caída
   · DDNS detecta cambio de IP y actualiza
   · Breaker abre al 5º fallo DuckDNS, cooldown 5 min, persiste cross-restart
   · Cert DNS-01 emite sin tocar router
   · Self-signed emite en 5 segundos
   · UPnP intenta y reporta razón clara si falla
   · Diagnóstico muestra checks con hints específicos por capability
   · Observer detecta cert expirando, marca degraded
   · Snapshots persistidos consultables por timestamp
   · Capability detection: instalar certbot → siguiente GET refresca → aparece
```

---

## NIMOS_PATTERNS.md — PENDIENTE

Los 9 patrones transversales que nacen en Network y se aplicarán a TODO NimOS se documentan en `NIMOS_PATTERNS.md` (a crear como tarea aparte). Lista:

```
1. HealthStatus enum unificado
2. Triple Generation (Desired/Observed/Applied)
3. SystemCapabilities detection (on-demand refresh)
4. CircuitBreaker en core (persistencia mínima)
5. Snapshot persistido en SQLite (15min + eventos + retention)
6. Reconciler con Tier + Interval ortogonales (3 tiers)
7. Triggered_by + request_id en operations
8. Events con category indexada + dedupe + rate limit + aggregation
9. Secrets table con AES-GCM (nimos_secrets, global)
```

Cada uno con: cuándo aplicar, cuándo no, código de referencia, ejemplos en NimOS, referencia a la sección de DISCIPLINE.

---

## SCOPE FUTURO (Beta 9+)

```
Inmediato Beta 9:
   · Rediseño Proxy (nginx reverse proxy completo)
   · Rediseño Portal (port config + bind addresses + HTTPS forced)
   · Rediseño WebDAV (auth integrada, no nginx genérico)

Más adelante:
   · VPN integration (WireGuard, Tailscale, ZeroTier)
   · Cloudflare/Route53 como DNSChallengeProvider funcionales
   · ZeroSSL como CertProvider alternativo
   · Bandwidth monitoring
   · Custom DNS resolver / Pi-hole integration
   · Multi-domain certs (SAN)
   · Key rotation automática para nimos_secrets
   · IPv6 first-class
   · Bonjour/mDNS advertising avanzado
   · Decisión: ¿sacar SSH/FTP/NFS/SMB a sharing.go aparte?
```

---

## CRÉDITOS

```
v3 → v4 mejorado tras tercera ronda de crítica de Andrés (21/05/2026):

  1. Reconciler 3 tiers no 4 + Tier ≠ Interval        ✅ Aplicado
  2. CircuitBreaker a /daemon/breaker.go (core)        ✅ Aplicado
  3. Tabla nimos_breakers (no network_breakers)        ✅ Aplicado
  4. Persistencia breaker MÍNIMA + lazy write          ✅ Aplicado
  5. Observed snapshot 15min + eventos + retention     ✅ Aplicado
  6. Events dedupe + rate limit + aggregation          ✅ Aplicado
  7. Capabilities refresh ON-DEMAND, no polling        ✅ Aplicado
  8. HealthStatus en servicio, no en breaker          ✅ Aplicado
  9. NO migration JSON → SQLite                        ✅ Aplicado
 10. NO borrar network.go viejo de golpe              ✅ Aplicado
 11. Migración FEATURE POR FEATURE con tests E2E      ✅ Aplicado
 12. networkMu serializa reconcilers writes            ✅ Aplicado
 13. Endpoint /api/v4/network/capabilities explícito  ✅ Aplicado en F-006
 14. 3 stubs (Proxy/Portal/WebDAV) NO migrar          ✅ Aplicado (Beta 9+)

El v4 nace de aplicar disciplina (NIMOS_DISCIPLINE.md v2) a un plan
v3 que ya estaba bien pero acoplaba conceptos (tier=interval) y
dejaba zonas grises (frecuencia de snapshot, política de events).

Resultado: módulo arquitectónicamente sólido, migrable sin downtime,
con patrones reutilizables en todo NimOS.

NimOS sigue siendo un NAS doméstico bien hecho. NO un framework.
```
