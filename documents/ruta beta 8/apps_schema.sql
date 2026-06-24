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
    port          INTEGER DEFAULT 0,                      -- puerto principal expuesto
    config        TEXT DEFAULT '{}',                      -- JSON: volúmenes, env, ports extra
    installed_at  TEXT NOT NULL,                          -- ISO timestamp
    installed_by  TEXT NOT NULL                           -- username (FK lógica con users)
);

CREATE INDEX IF NOT EXISTS idx_docker_apps_installed_by ON docker_apps(installed_by);

-- ─── Native apps ──────────────────────────────────────────────────────
-- Apps nativas Linux instaladas (apt packages, systemd services).
-- Pueden estar autodetectadas (CheckCommand pass) o registradas manualmente.
-- El campo `auto_detected` distingue ambos casos.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS native_apps (
    id              TEXT PRIMARY KEY,                     -- 'samba', 'transmission', 'kvm'
    name            TEXT NOT NULL,                        -- 'Samba (SMB)'
    description     TEXT DEFAULT '',
    category        TEXT DEFAULT 'system',                -- 'system', 'downloads', 'office'
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
