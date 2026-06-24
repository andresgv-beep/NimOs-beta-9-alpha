-- =============================================================================
-- NimOS Beta 8 — Network Schema
-- =============================================================================
--
-- Source of truth para el subsistema de network. Reemplaza:
--   · /var/lib/nimos/config/ddns.json
--   · /var/lib/nimos/config/remote-access.json
--   · cualquier otro JSON file del módulo network
--
-- Estas migraciones JSON → SQLite NO son automáticas (decisión v4):
-- el daemon detecta los JSONs antiguos, loguea un warning, y arranca con
-- SQLite vacío. El admin reconfigura desde UI. Ver NETWORK_MODULE_PLAN_v4.md.
--
-- Reglas:
--   1. PRAGMA foreign_keys = ON es OBLIGATORIO en la conexión.
--      Sin esto, los CASCADE/RESTRICT y la FK a nimos_secrets serían
--      decorativos.
--   2. Triple generation (desired/observed/applied) en entidades
--      reconciables (ports, ddns, certs). NO en logs, eventos, snapshots.
--      Justificación: NIMOS_DISCIPLINE.md §1 — sólo se aplica donde hay
--      convergencia real.
--   3. JSON solo aparece en campos TEXT de propósito específico
--      (snapshot_data, operation data, event details). NUNCA como
--      representación principal de una entidad.
--   4. Las invariantes críticas se garantizan al nivel del schema (CHECK,
--      UNIQUE, FK), no en Go.
--   5. nimos_secrets, nimos_breakers, nimos_capabilities NO viven aquí —
--      son del core (ver nimos_core_schema.sql).
--
-- Aplicación: idempotente (IF NOT EXISTS). Se ejecuta al arranque tras
-- initNimosCoreSchema(). Migraciones futuras vía network_schema_version
-- cuando haga falta (TODO Beta 9+).
--
-- Autor: Andrés + Claude Opus 4.7 — Mayo 2026
-- Versión: 1 (network_schema)
-- =============================================================================

PRAGMA foreign_keys = ON;

-- =============================================================================
-- 1. network_ports — Puertos del daemon (HTTP/HTTPS)
-- =============================================================================
-- IDs estables ('http', 'https') porque son singletons del daemon, no
-- entidades de usuario. Si en el futuro hay puertos por servicio, será
-- otra tabla.
--
-- Triple generation: el reconciler port_listener (tier=Critical, 60s)
-- detecta cuando observed_generation != applied_generation y rebinda el
-- listener sin reiniciar el daemon. El v3 antiguo necesitaba reiniciar
-- el Pi entero — fricción real del caso de Andrés vs Synology.

CREATE TABLE IF NOT EXISTS network_ports (
    id                   TEXT    PRIMARY KEY
        CHECK(id IN ('http', 'https')),

    port                 INTEGER NOT NULL CHECK(port > 0 AND port < 65536),
    bind_address         TEXT    NOT NULL DEFAULT '0.0.0.0',
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),

    desired_generation   INTEGER NOT NULL DEFAULT 0 CHECK(desired_generation  >= 0),
    observed_generation  INTEGER NOT NULL DEFAULT 0 CHECK(observed_generation >= 0),
    applied_generation   INTEGER NOT NULL DEFAULT 0 CHECK(applied_generation  >= 0),

    updated_at           TEXT    NOT NULL
);

-- =============================================================================
-- 2. network_ddns — Configuraciones DDNS
-- =============================================================================
-- El token NUNCA se almacena en plaintext en esta tabla. Se guarda en
-- nimos_secrets (AES-GCM) y aquí solo va el ID del secret. Si el secret
-- se borra (CASCADE), la fila DDNS se borra también — no queremos un
-- DDNS huérfano sin forma de autenticarse.

CREATE TABLE IF NOT EXISTS network_ddns (
    id                   TEXT    PRIMARY KEY,        -- UUID v4
    provider             TEXT    NOT NULL
        CHECK(provider IN ('duckdns', 'noip', 'dynu', 'freedns', 'cloudflare')),
    domain               TEXT    NOT NULL,

    -- FK a nimos_secrets. AES-GCM con master key del core.
    token_secret_id      TEXT    NOT NULL,

    enabled              INTEGER NOT NULL DEFAULT 0 CHECK(enabled     IN (0, 1)),
    auto_update          INTEGER NOT NULL DEFAULT 1 CHECK(auto_update IN (0, 1)),
    update_interval      INTEGER NOT NULL DEFAULT 900 CHECK(update_interval >= 60),

    -- Estado del último update. NULL si nunca se ejecutó.
    last_run_at          TEXT,
    last_run_result      TEXT
        CHECK(last_run_result IS NULL OR last_run_result IN ('success', 'failed', 'no_change')),
    last_ip              TEXT,

    desired_generation   INTEGER NOT NULL DEFAULT 0 CHECK(desired_generation  >= 0),
    observed_generation  INTEGER NOT NULL DEFAULT 0 CHECK(observed_generation >= 0),
    applied_generation   INTEGER NOT NULL DEFAULT 0 CHECK(applied_generation  >= 0),

    -- Un domain solo puede estar en una entrada DDNS a la vez. Cambiar
    -- provider implica borrar+crear (el token del provider viejo no
    -- sirve para el nuevo).
    UNIQUE(domain),

    FOREIGN KEY (token_secret_id) REFERENCES nimos_secrets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_network_ddns_provider ON network_ddns(provider);
CREATE INDEX IF NOT EXISTS idx_network_ddns_enabled  ON network_ddns(enabled) WHERE enabled = 1;

-- =============================================================================
-- 4. network_observed — Snapshots históricos del observer
-- =============================================================================
-- Política de escritura (NIMOS_DISCIPLINE.md §2 v2):
--   · 'periodic': cada 15 min (snapshot completo).
--   · 'event':    entre periodics, cuando observer detecta cambio
--                 significativo (ip pública cambia, cert pasa a degraded,
--                 breaker abre, listener cae).
--   · 'boot':     primer scan tras arrancar el daemon (post-mortem util).
--   · 'manual':   forzado por admin desde endpoint.
--
-- Retention (NUNCA acumular indefinido):
--   · 100 últimos snapshots, sea cual sea el type.
--   · 1 por hora durante el último día (preferir 'event' al limpiar).
--   · 1 por día durante el último mes.
--   · Borrar el resto en el cron nocturno.
--
-- snapshot_data es JSON completo de NetworkObservedSnapshot. Las
-- columnas adicionales (overall_health, public_ip, etc.) son métricas
-- pre-extraídas para queries rápidas sin parsear JSON.

CREATE TABLE IF NOT EXISTS network_observed (
    id              TEXT    PRIMARY KEY,        -- UUID v4
    generation      INTEGER NOT NULL CHECK(generation > 0),
    snapshot_at     TEXT    NOT NULL,           -- ISO 8601 UTC

    snapshot_type   TEXT    NOT NULL DEFAULT 'periodic'
        CHECK(snapshot_type IN ('periodic', 'event', 'boot', 'manual')),

    snapshot_data   TEXT    NOT NULL,            -- JSON blob completo

    -- Métricas pre-extraídas (queries sin JSON parse).
    overall_health   TEXT    NOT NULL
        CHECK(overall_health IN ('healthy', 'degraded', 'failed', 'partial', 'unknown', 'stale')),
    public_ip        TEXT,
    ddns_synced      INTEGER CHECK(ddns_synced IS NULL OR ddns_synced IN (0, 1)),
    divergence_count INTEGER NOT NULL DEFAULT 0 CHECK(divergence_count >= 0),

    scan_duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(scan_duration_ms >= 0)
);

CREATE INDEX IF NOT EXISTS idx_network_observed_at     ON network_observed(snapshot_at DESC);
CREATE INDEX IF NOT EXISTS idx_network_observed_health ON network_observed(overall_health);
CREATE INDEX IF NOT EXISTS idx_network_observed_gen    ON network_observed(generation DESC);
CREATE INDEX IF NOT EXISTS idx_network_observed_type   ON network_observed(snapshot_type);

-- =============================================================================
-- 5. network_operations — Operaciones auditables del módulo network
-- =============================================================================
-- Diseño NIMOS_DISCIPLINE inspirado: triggered_by + request_id permiten
-- correlation cross-tiers ("¿qué reconciler causó esta operación?").
--
-- triggered_by formato:
--   · 'user:<username>'         → acción manual del usuario
--   · 'reconciler:<name>'       → un reconciler ejecutando convergence
--   · 'system:boot'             → tarea de arranque
--   · 'system:scheduler'        → cron nocturno (retention, aggregation)
--
-- Si aparece una nueva categoría, ALTER TABLE con nuevo CHECK. NO usar
-- categorías ad-hoc — el CHECK las rechaza.
--
-- parent_operation: si una operación A dispara la operación B, B referencia
-- A. Útil para tracing. ON DELETE SET NULL para no borrar hijos al borrar
-- el padre (auditoría histórica sobrevive).

CREATE TABLE IF NOT EXISTS network_operations (
    id                TEXT    PRIMARY KEY,       -- UUID v4
    type              TEXT    NOT NULL,          -- 'ddns_update', 'cert_issue', etc.
    target_id         TEXT,                       -- ID de la entidad afectada (ddns_id, cert_id, ...)
    status            TEXT    NOT NULL
        CHECK(status IN ('pending', 'in_progress', 'completed', 'failed', 'rolled_back')),

    -- Ownership / correlation
    triggered_by      TEXT    NOT NULL
        CHECK(
            triggered_by LIKE 'user:%' OR
            triggered_by LIKE 'reconciler:%' OR
            triggered_by = 'system:boot' OR
            triggered_by = 'system:scheduler'
        ),
    request_id        TEXT,                       -- UUID opcional para HTTP request / batch
    parent_operation  TEXT,

    started_at        TEXT    NOT NULL,
    completed_at      TEXT,
    error             TEXT,
    error_code        TEXT,
    data              TEXT,                       -- JSON con parámetros / outputs

    FOREIGN KEY (parent_operation) REFERENCES network_operations(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_network_ops_status     ON network_operations(status);
CREATE INDEX IF NOT EXISTS idx_network_ops_started    ON network_operations(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_network_ops_triggered  ON network_operations(triggered_by);
CREATE INDEX IF NOT EXISTS idx_network_ops_request    ON network_operations(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_network_ops_type       ON network_operations(type);

-- =============================================================================
-- 6. network_events — Log auditable con dedupe + categorías indexadas
-- =============================================================================
-- ANTÍDOTOS ANTI-EXPLOSIÓN (NIMOS_DISCIPLINE.md §4 v2):
--
--   (A) Dedupe runtime (5 min):
--       Si mismo (category, event, target_id) llega en ventana 5 min,
--       NO insertamos nueva fila — incrementamos `occurrences` y
--       actualizamos `last_seen_at`. Lógica en network_events.go.
--
--   (B) Rate limit por category (10/min):
--       Bucket en memoria. Si excede, drop con métrica.
--
--   (C) Aggregation nocturna (03:00):
--       Cron comprime el día anterior. Mantiene errors+warns intactos,
--       resume eventos rutinarios en una fila por (category, event, day).
--
--   (D) Retention por nivel:
--       error: 90d / warn: 30d / info: 7d / debug: 24h.
--
--   (E) Niveles correctos: reconciler_started=debug, NO info.
--       Solo cambios reales son info+.
--
-- operation_id es nullable porque hay eventos que NO pertenecen a una
-- operación concreta (ej: observer detecta cambio espontáneo).

CREATE TABLE IF NOT EXISTS network_events (
    id           TEXT    PRIMARY KEY,           -- UUID v4
    operation_id TEXT,                           -- nullable
    timestamp    TEXT    NOT NULL,               -- ISO 8601 UTC

    category     TEXT    NOT NULL
        CHECK(category IN ('ddns', 'cert', 'port', 'upnp', 'breaker', 'observer', 'capability', 'exposure')),
    event        TEXT    NOT NULL,               -- 'update_started', 'cert_issued', etc.
    target_id    TEXT,                           -- entidad afectada (puede coincidir con operations.target_id)

    level        TEXT    NOT NULL
        CHECK(level IN ('debug', 'info', 'warn', 'error')),
    message      TEXT    NOT NULL,
    details      TEXT,                           -- JSON opcional

    -- Dedupe (DISCIPLINE §4 v2 antídoto A)
    occurrences  INTEGER NOT NULL DEFAULT 1 CHECK(occurrences >= 1),
    last_seen_at TEXT    NOT NULL,               -- ISO 8601 UTC, se actualiza al dedupe

    FOREIGN KEY (operation_id) REFERENCES network_operations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_network_events_operation ON network_events(operation_id);
CREATE INDEX IF NOT EXISTS idx_network_events_timestamp ON network_events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_network_events_category  ON network_events(category, event);
CREATE INDEX IF NOT EXISTS idx_network_events_level     ON network_events(level);

-- Índice de dedupe lookup: queremos encontrar rápido el último evento
-- de (category, event, target_id) en ventana de 5 min para incrementar
-- occurrences en vez de insertar nueva fila.
CREATE INDEX IF NOT EXISTS idx_network_events_dedupe
    ON network_events(category, event, target_id, last_seen_at DESC);

-- =============================================================================
-- network_exposed_apps — Apps expuestas a internet vía Caddy
-- =============================================================================
-- El usuario decide qué apps Docker (u otros upstreams) se exponen a
-- internet con HTTPS. NimOS NO gestiona los certs: eso lo hace Caddy
-- (solicitud + renovación + reintentos ACME). NimOS:
--   · declara el intent (esta tabla)
--   · genera la config de Caddy y la recarga vía su API admin (:2019)
--   · observa el estado real de los certs leyendo /pki/certificates de
--     Caddy (network_exposure_observer) para mostrarlo en la UI
--
-- Diseño agnóstico subdomain/path (Opción C):
--   · subdomain != '' → Caddy enruta por host: <subdomain>.<base_domain>
--   · path     != '' → Caddy enruta por path:  <base_domain><path>
--   · al menos uno de los dos debe estar presente (CHECK).
--
-- Triple generation: el reconciler aplica desired → Caddy y marca applied.
-- observed lo mueve el observer cuando detecta drift (ej. Caddy reiniciado
-- perdió una ruta, o el cert de un dominio falló).

CREATE TABLE IF NOT EXISTS network_exposed_apps (
    id              TEXT    PRIMARY KEY,        -- UUID v4
    app_id          TEXT    NOT NULL UNIQUE,    -- id del AppStore: "immich", "gitea"
    display_name    TEXT    NOT NULL DEFAULT '',-- nombre legible para la UI

    -- Enrutado (al menos uno no vacío; ver CHECK).
    subdomain       TEXT    NOT NULL DEFAULT '',-- "immich" → immich.<base_domain>
    path            TEXT    NOT NULL DEFAULT '',-- "/immich" → <base_domain>/immich

    -- Upstream al que Caddy hace reverse proxy.
    upstream_host   TEXT    NOT NULL,           -- "127.0.0.1", "192.168.1.131"
    upstream_port   INTEGER NOT NULL CHECK(upstream_port > 0 AND upstream_port <= 65535),

    enabled         INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),

    -- Triple generation (reconciable).
    desired_generation  INTEGER NOT NULL DEFAULT 0 CHECK(desired_generation  >= 0),
    observed_generation INTEGER NOT NULL DEFAULT 0 CHECK(observed_generation >= 0),
    applied_generation  INTEGER NOT NULL DEFAULT 0 CHECK(applied_generation  >= 0),

    created_at      TEXT    NOT NULL,           -- ISO 8601 UTC
    updated_at      TEXT    NOT NULL,           -- ISO 8601 UTC

    -- Al menos un método de enrutado debe estar presente.
    CHECK (subdomain != '' OR path != '')
);

CREATE INDEX IF NOT EXISTS idx_network_exposed_enabled ON network_exposed_apps(enabled) WHERE enabled = 1;
CREATE INDEX IF NOT EXISTS idx_network_exposed_app     ON network_exposed_apps(app_id);

-- =============================================================================
-- network_exposure_config — Configuración global de exposición (singleton)
-- =============================================================================
-- Una sola fila (CHECK id='singleton'), igual que un registro de settings.
-- Guarda los parámetros que comparten todas las apps expuestas:
--   · base_domain: el dominio bajo el que se montan los subdominios/paths
--     (ej. "nimosbarraca1.duckdns.org"). Vacío = exposición no configurada.
--   · caddy_admin_url: endpoint de la API admin de Caddy para recargar
--     config sin downtime (default http://127.0.0.1:2019).
--   · enabled: interruptor global. Si 0, el reconciler no expone nada
--     aunque haya apps con enabled=1 (kill-switch de exposición).
--
-- NO triple generation: es configuración, no entidad reconciable por
-- generación. El reconciler la lee como parámetro, no la converge.

CREATE TABLE IF NOT EXISTS network_exposure_config (
    id              TEXT    PRIMARY KEY DEFAULT 'singleton'
                    CHECK(id = 'singleton'),
    base_domain     TEXT    NOT NULL DEFAULT '',
    caddy_admin_url TEXT    NOT NULL DEFAULT 'http://127.0.0.1:2019',
    http_port       INTEGER NOT NULL DEFAULT 80,
    https_port      INTEGER NOT NULL DEFAULT 443,
    fw_managed_ports TEXT   NOT NULL DEFAULT '[]',
    enabled         INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0, 1)),
    updated_at      TEXT    NOT NULL
);

