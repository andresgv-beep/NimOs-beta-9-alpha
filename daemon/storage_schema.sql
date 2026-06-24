-- =============================================================================
-- NimOS Beta 8.1 — Storage Schema
-- =============================================================================
--
-- Source of truth para todo el subsistema de storage.
-- Reemplaza el antiguo storage.json (que desaparece).
--
-- Reglas:
--   1. PRAGMA foreign_keys = ON es OBLIGATORIO en la conexión.
--      Sin esto, los CASCADE/RESTRICT son decorativos.
--   2. Toda mutación incrementa storage_metadata['global_generation'] +
--      la generation de la entidad afectada.
--   3. JSON solo aparece en campos TEXT con payload temporal de operaciones.
--      NUNCA como representación principal de una entidad.
--   4. Las invariantes críticas (1 layout-op activa por pool, 1 scrub por
--      pool, generation >= 0) se garantizan al nivel del schema, no del Go.
--
-- Aplicación: este script es idempotente (IF NOT EXISTS). Se puede ejecutar
-- al arranque del daemon en cada boot. Las migraciones futuras se gestionarán
-- por schema_version en storage_metadata.
--
-- Autor: Andrés + Claude Opus 4.7 — Mayo 2026
-- Versión: 2 (storage_version)
-- =============================================================================

-- Activar foreign keys explícitamente. Esto en realidad debe estar también
-- en la cadena de conexión Go, pero aquí lo dejamos por seguridad y
-- para que el script funcione en sqlite3 CLI.
PRAGMA foreign_keys = ON;

-- =============================================================================
-- 1. storage_metadata — Configuración global key-value
-- =============================================================================
-- Patrón clave-valor para metadata del subsistema.
--
-- KEYS VÁLIDAS EN BETA 8 (lista cerrada, documentada — no añadir
-- arbitrariamente):
--
--   'schema_version'    → '2' (versión del schema de storage)
--   'primary_pool'      → UUID del pool principal del sistema (nullable)
--   'configured_at'     → ISO 8601 del primer pool creado (nullable)
--   'global_generation' → contador entero, incrementa en cada mutación
--
-- ANTI-PATTERNS PROHIBIDOS en esta tabla:
--   - Flags de feature (foo_enabled, beta_flag) → usar campo en tabla específica
--   - Valores temporales (foo_tmp, debug_x)     → no usar metadata para esto
--   - Estado de UI                              → vive en preferences, no aquí
--
-- Si necesitas guardar algo nuevo, primero plantéate si merece su propia
-- tabla. Solo si es de verdad metadata global key-value, documenta la key
-- aquí ANTES de añadir el código que la usa.
--
-- TODO(beta9): considerar CHECK(key IN (...)) estricto si la disciplina
-- se degrada. Por ahora, disciplina por convención + comentarios.

CREATE TABLE IF NOT EXISTS storage_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Inicializar valores por defecto si no existen
INSERT OR IGNORE INTO storage_metadata (key, value) VALUES ('schema_version', '2');
INSERT OR IGNORE INTO storage_metadata (key, value) VALUES ('global_generation', '0');

-- =============================================================================
-- 2. storage_pools — Pools BTRFS del sistema
-- =============================================================================
-- Cada fila representa un filesystem BTRFS, gestionado u observado.
-- El id interno es estable (UUID); name puede cambiar sin romper FKs.

CREATE TABLE IF NOT EXISTS storage_pools (
    id            TEXT    PRIMARY KEY,                  -- UUID interno, estable
    name          TEXT    NOT NULL UNIQUE,              -- legible, mutable
    btrfs_uuid    TEXT    NOT NULL UNIQUE,              -- UUID del filesystem (blkid)
    profile       TEXT    NOT NULL
        CHECK(profile IN ('single', 'raid1', 'raid1c3', 'raid10')),
    mount_point   TEXT    NOT NULL UNIQUE,              -- /nimos/pools/<name>

    -- Role: función del pool. Beta 8 todos 'data'. Consumers futuros.
    role          TEXT    NOT NULL DEFAULT 'data'
        CHECK(role IN ('data', 'backup', 'cache', 'system')),

    -- Compression: mutable vía POST /pools/:id/set-compression.
    -- Solo afecta a archivos escritos a partir del cambio.
    -- TODO(beta9): considerar split en compression_algo + compression_level
    -- para queries más limpias. Por ahora string compuesto que coincide con
    -- el formato nativo de BTRFS (`btrfs property set compression zstd:3`).
    compression   TEXT    NOT NULL DEFAULT 'none'
        CHECK(compression IN ('none', 'lzo',
                              'zstd:1', 'zstd:3', 'zstd:5', 'zstd:9', 'zstd:15')),

    -- Control state: autoridad de NimOS sobre el pool.
    -- Beta 8 runtime usa 'managed' y 'observed'. Otros reservados para Beta 9+.
    control_state TEXT    NOT NULL DEFAULT 'managed'
        CHECK(control_state IN ('managed', 'observed', 'imported', 'foreign', 'recovery')),

    discovered_at TEXT,                                  -- ISO 8601, nullable
    created_at    TEXT    NOT NULL,                      -- ISO 8601
    generation    INTEGER NOT NULL DEFAULT 0
        CHECK(generation >= 0)
);

CREATE INDEX IF NOT EXISTS idx_pools_name          ON storage_pools(name);
CREATE INDEX IF NOT EXISTS idx_pools_btrfs_uuid    ON storage_pools(btrfs_uuid);
CREATE INDEX IF NOT EXISTS idx_pools_control_state ON storage_pools(control_state);

-- =============================================================================
-- 3. storage_devices — Discos físicos conocidos
-- =============================================================================
-- Identidad por serial (firmware, absoluto). by_id_path es identidad estable
-- pero puede variar entre controladoras SATA. current_path es CACHE runtime,
-- nunca identidad.

CREATE TABLE IF NOT EXISTS storage_devices (
    id            TEXT    PRIMARY KEY,                  -- UUID interno
    serial        TEXT    NOT NULL UNIQUE,              -- IDENTIDAD ABSOLUTA (firmware)
    by_id_path    TEXT    NOT NULL UNIQUE,              -- /dev/disk/by-id/...
    current_path  TEXT    NOT NULL,                     -- /dev/sdb (cache)
    wwn           TEXT,                                  -- nullable
    model         TEXT,
    size_bytes    INTEGER,
    last_seen_at  TEXT,                                  -- ISO 8601
    generation    INTEGER NOT NULL DEFAULT 0
        CHECK(generation >= 0)
);

CREATE INDEX IF NOT EXISTS idx_devices_serial       ON storage_devices(serial);
CREATE INDEX IF NOT EXISTS idx_devices_current_path ON storage_devices(current_path);
CREATE INDEX IF NOT EXISTS idx_devices_wwn          ON storage_devices(wwn);

-- =============================================================================
-- 4. storage_pool_devices — Relación N:M pool ↔ device
-- =============================================================================
-- Un device pertenece a 0 o 1 pool. Un pool tiene >= 1 device.

CREATE TABLE IF NOT EXISTS storage_pool_devices (
    pool_id   TEXT NOT NULL,
    device_id TEXT NOT NULL,
    added_at  TEXT NOT NULL,                            -- ISO 8601

    PRIMARY KEY (pool_id, device_id),

    -- Si se destruye el pool, las relaciones desaparecen.
    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id)
        ON DELETE CASCADE,

    -- No permitir borrar un device que está en un pool.
    FOREIGN KEY (device_id)
        REFERENCES storage_devices(id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_pool_devices_pool   ON storage_pool_devices(pool_id);
CREATE INDEX IF NOT EXISTS idx_pool_devices_device ON storage_pool_devices(device_id);

-- =============================================================================
-- 5. storage_operations — Journal de operaciones (sync + async)
-- =============================================================================
-- Toda mutación se registra aquí, sea sync (rename, set-compression) o async
-- (create_pool, replace_device). Permite auditoría, recovery tras crash y
-- activity timeline en la UI.

CREATE TABLE IF NOT EXISTS storage_operations (
    id           TEXT    PRIMARY KEY,                   -- UUID
    type         TEXT    NOT NULL,                      -- create_pool, rename_pool, etc.
    pool_id      TEXT,                                   -- nullable
    status       TEXT    NOT NULL
        CHECK(status IN ('pending', 'in_progress', 'completed', 'failed', 'rolled_back', 'cancelled')),
    started_at   TEXT    NOT NULL,                      -- ISO 8601
    completed_at TEXT,                                   -- ISO 8601, nullable
    error        TEXT,                                   -- mensaje libre, nullable
    error_code   TEXT,                                   -- código semántico (pool_observed, etc.)
    data         TEXT,                                   -- JSON payload temporal

    -- Si se destruye el pool, conservar el histórico de operaciones
    -- (pool_id queda NULL pero la operación sigue existiendo).
    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id)
        ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_operations_status     ON storage_operations(status);
CREATE INDEX IF NOT EXISTS idx_operations_pool_id    ON storage_operations(pool_id);
CREATE INDEX IF NOT EXISTS idx_operations_started_at ON storage_operations(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_operations_type       ON storage_operations(type);

-- -----------------------------------------------------------------------------
-- Invariantes de exclusión mutua, garantizadas al nivel del schema:
-- -----------------------------------------------------------------------------
--
-- INV-1: Solo una operación de layout activa por pool a la vez.
-- Operaciones de "layout" son las que modifican la estructura física del
-- pool (create, destroy, add/remove/replace device, convert profile).
-- BTRFS no permite dos de estas concurrentes en el mismo pool — el schema
-- lo refleja.
CREATE UNIQUE INDEX IF NOT EXISTS idx_one_layout_op_per_pool
    ON storage_operations(pool_id)
    WHERE status IN ('pending', 'in_progress')
      AND type IN ('create_pool', 'destroy_pool',
                   'add_device', 'remove_device', 'replace_device',
                   'convert_profile');

-- INV-2: Solo un scrub activo por pool. BTRFS lo serializa el kernel.
CREATE UNIQUE INDEX IF NOT EXISTS idx_one_scrub_per_pool
    ON storage_operations(pool_id)
    WHERE status IN ('pending', 'in_progress')
      AND type = 'start_scrub';

-- Nota: snapshots (create_snapshot, delete_snapshot) NO tienen exclusión
-- mutua porque BTRFS permite operaciones de subvolumen concurrentes con
-- ops de layout en el mismo pool.
--
-- Nota: balance_pause / balance_resume son sync y completan inmediato.
-- No necesitan exclusión.
--
-- TODO(beta9): event retention policy. Por ahora storage_events crece
-- libremente (~360KB/año en uso típico, no es problema). Cuando el volumen
-- lo justifique (Beta 10+), implementar política: conservar todos los del
-- último mes + eventos failed indefinidamente + summary del resto.

-- =============================================================================
-- 6. storage_events — Timeline detallado por operación
-- =============================================================================
-- Cada operación puede tener N eventos. Permite reconstruir el paso a paso
-- de qué ocurrió durante la operación (wipe OK, mkfs OK, mount FAILED, etc.).

CREATE TABLE IF NOT EXISTS storage_events (
    id           TEXT PRIMARY KEY,                      -- UUID
    operation_id TEXT NOT NULL,
    timestamp    TEXT NOT NULL,                         -- ISO 8601
    level        TEXT NOT NULL
        CHECK(level IN ('debug', 'info', 'warn', 'error')),
    message      TEXT NOT NULL,

    -- Si se borra la operación (raro), borrar sus eventos.
    FOREIGN KEY (operation_id)
        REFERENCES storage_operations(id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_events_operation ON storage_events(operation_id);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON storage_events(timestamp DESC);

-- =============================================================================
-- 7. storage_pool_capabilities — Capacidades soportadas por cada pool
-- =============================================================================
-- En Beta 8, todos los pools BTRFS managed se crean con el set completo.
-- En Beta 9+, ext4 external u otros tipos tendrán capabilities limitadas.

CREATE TABLE IF NOT EXISTS storage_pool_capabilities (
    pool_id    TEXT NOT NULL,
    capability TEXT NOT NULL,

    PRIMARY KEY (pool_id, capability),

    FOREIGN KEY (pool_id)
        REFERENCES storage_pools(id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_capabilities_pool ON storage_pool_capabilities(pool_id);

-- Capabilities posibles (no se valida con CHECK, son strings libres
-- para que añadir capabilities en Beta 9+ no requiera migración):
--   'snapshots', 'balance', 'replace_device', 'add_device', 'remove_device',
--   'convert_profile', 'scrub', 'compression', 'resize'

-- =============================================================================
-- 8. scrub_schedule — Planificación de scrubs automáticos por pool
-- =============================================================================
-- Background scheduler (checkAndRunScheduledScrubs) consulta esta tabla cada
-- minuto para disparar scrubs según frecuencia configurada.
--
-- Beta 8.1: tabla creada aquí (antes vivía en initScrubScheduleTable() en Go
-- que nunca se invocaba al boot — bug latente arreglado integrando el schema).
--
-- pool_name como PK: BTRFS no soporta dos schedules para el mismo pool.
-- Si se renombra el pool en el futuro, el row debe actualizarse a mano (no
-- hay FK porque scrub_schedule precede a managed/observed model).

CREATE TABLE IF NOT EXISTS scrub_schedule (
    pool_name    TEXT    PRIMARY KEY,
    enabled      INTEGER NOT NULL DEFAULT 0,
    frequency    TEXT    NOT NULL DEFAULT 'monthly',
    day_of_week  INTEGER DEFAULT 0,
    day_of_month INTEGER DEFAULT 1,
    hour         INTEGER NOT NULL DEFAULT 3,
    minute       INTEGER NOT NULL DEFAULT 0,
    last_run     TEXT,
    next_run     TEXT,
    updated_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);

