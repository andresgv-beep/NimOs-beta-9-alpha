-- ═══════════════════════════════════════════════════════════════════════
-- NimOS Beta 8.1 · Apps Module · SQLite Schema
-- ═══════════════════════════════════════════════════════════════════════
--
-- Este schema gestiona dos tipos de aplicaciones del usuario:
--
--   docker_apps  → containers Docker (jellyfin, plex, sonarr, immich...)
--   native_apps  → packages del sistema (samba, kvm, transmission...)
--
-- NO se mezcla con app_registry (que gestiona apps internas del SO + permisos).
--
-- Diseñado por dominio de entidad, NO por tipo de operación.
-- Cada tabla es autocontenida y self-sufficient.
--
-- Idempotente: se puede aplicar en cada arranque sin efectos secundarios.
-- ═══════════════════════════════════════════════════════════════════════

-- ─── Docker apps ──────────────────────────────────────────────────────
-- Apps instaladas por el usuario como containers Docker.
-- Cada fila representa la CONFIGURACIÓN persistente; el ESTADO en runtime
-- se obtiene cruzándola con `docker ps -a`.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS docker_apps (
    id            TEXT PRIMARY KEY,                       -- 'jellyfin', 'plex', 'sonarr'
    name          TEXT NOT NULL,                          -- 'Jellyfin Media Server'
    icon          TEXT DEFAULT '',                        -- '/app-icons/jellyfin.svg' o emoji
    image         TEXT DEFAULT '',                        -- 'jellyfin/jellyfin:latest'
    color         TEXT DEFAULT '',                        -- '#00A4DC' hex
    type          TEXT DEFAULT 'container',               -- 'container' | 'stack'
    open_mode     TEXT DEFAULT 'internal',                -- 'internal' | 'external'
    port          INTEGER DEFAULT 0,                      -- puerto principal expuesto (compat legacy)
    ports_json    TEXT DEFAULT '[]',                      -- APP-033 · JSON array de PortBinding completo
    deleting      INTEGER DEFAULT 0,                      -- APP-031 · 1 mientras se está desinstalando
    config        TEXT DEFAULT '{}',                      -- JSON: volúmenes, env, ports extra
    installed_at  TEXT NOT NULL,                          -- ISO timestamp
    installed_by  TEXT NOT NULL                           -- username (sin FK; integridad referencial
                                                          --           mantenida por la capa de aplicación)
);

CREATE INDEX IF NOT EXISTS idx_docker_apps_installed_by ON docker_apps(installed_by);
-- idx_docker_apps_deleting se crea en apps_schema.go::initAppsSchema TRAS el
-- ALTER TABLE que añade la columna. No puede ir aquí porque en upgrades
-- desde Beta 8 pre-Batch-2 la columna aún no existe cuando se ejecuta este SQL.

-- ─── Docker app images (digest tracking for updates) ─────────────────
-- Tracking de imágenes Docker por servicio para detección de actualizaciones.
--
-- Una app Docker puede ser:
--   - container single (Jellyfin)        → 1 row (service_name = app_id)
--   - stack multi-container (Immich)     → N rows (1 por servicio: server, ml,
--                                          redis, database, etc.)
--
-- Detección de update se reduce a una query:
--   SELECT app_id, service_name FROM docker_app_images
--   WHERE local_digest != remote_digest AND remote_digest != '';
--
-- Cada row guarda dos digests SHA256:
--   - local_digest:  el que tienes instalado AHORA (de `docker image inspect`)
--   - remote_digest: el último que viste en el registro remoto
--                    (de `docker manifest inspect`)
--
-- Si difieren, hay update disponible.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS docker_app_images (
    app_id            TEXT NOT NULL,                       -- FK lógica a docker_apps.id
    service_name      TEXT NOT NULL,                       -- nombre del servicio en compose
                                                            -- ('immich-server', 'redis')
                                                            -- = app_id para single containers
    image             TEXT NOT NULL,                       -- 'ghcr.io/immich-app/immich-server:release'
    local_digest      TEXT DEFAULT '',                     -- sha256:abc... (imagen instalada)
    remote_digest     TEXT DEFAULT '',                     -- sha256:xyz... (último check remoto)
    remote_checked_at TEXT DEFAULT '',                     -- ISO timestamp del último manifest inspect
                                                            -- '' = nunca comprobado
    check_status      TEXT DEFAULT 'ok',                   -- 'ok' | 'unsupported' | 'rate_limited'
                                                            -- | 'unauthorized' | 'error'
    PRIMARY KEY (app_id, service_name)
);

CREATE INDEX IF NOT EXISTS idx_docker_app_images_app ON docker_app_images(app_id);

-- ─── Native apps ──────────────────────────────────────────────────────
-- Apps nativas Linux instaladas (apt packages, systemd services).
-- Pueden estar autodetectadas (CheckCommand pass) o registradas manualmente.
-- El campo `auto_detected` distingue ambos casos.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS native_apps (
    id              TEXT PRIMARY KEY,                     -- 'samba', 'transmission', 'kvm'
    name            TEXT NOT NULL,                        -- 'Samba (SMB)'
    description     TEXT DEFAULT '',
    category        TEXT NOT NULL,                        -- 'system', 'downloads', 'office' (default 'system' aplicado por AppsRepo)
    icon            TEXT DEFAULT '',
    color           TEXT DEFAULT '',
    port            INTEGER DEFAULT 0,
    is_desktop      INTEGER DEFAULT 0,                    -- bool: app de escritorio (libreoffice)
    is_native       INTEGER DEFAULT 1,                    -- bool: servicio systemd
    nimos_app       TEXT DEFAULT '',                      -- bridge a app NimOS (vms, downloads)
    auto_detected   INTEGER DEFAULT 0,                    -- bool: detectada vs registrada manual
    installed_at    TEXT NOT NULL,                        -- ISO timestamp
    last_seen_at    TEXT NOT NULL                         -- ISO timestamp del último escaneo
);

CREATE INDEX IF NOT EXISTS idx_native_apps_category ON native_apps(category);
CREATE INDEX IF NOT EXISTS idx_native_apps_auto ON native_apps(auto_detected);
