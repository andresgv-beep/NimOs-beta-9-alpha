-- =============================================================================
-- NimOS Beta 8 — Core Schema (global, reutilizable por todos los módulos)
-- =============================================================================
--
-- Source of truth para las tablas globales de NimOS. NO son específicas
-- de un módulo (network, storage, apps, etc.) — viven en el core y las
-- comparten varios módulos.
--
-- Reglas:
--   1. PRAGMA foreign_keys = ON es OBLIGATORIO en la conexión.
--   2. Las tres tablas son idempotentes (IF NOT EXISTS).
--   3. NIMOS_DISCIPLINE.md §3 v2: persistencia del breaker es MÍNIMA
--      (solo name + state + next_retry_at). NO se guardan métricas aquí.
--
-- Aplicación: este script es idempotente. Se aplica en cada arranque
-- del daemon. Migraciones futuras vía schema_version cuando haga falta.
--
-- Autor: Andrés + Claude Opus 4.7 — Mayo 2026
-- Versión: 1
-- =============================================================================

PRAGMA foreign_keys = ON;

-- =============================================================================
-- nimos_secrets — Almacén GLOBAL de secretos AES-256-GCM
-- =============================================================================
-- Categorías esperadas:
--   · 'ddns_token'     (DuckDNS, NoIP, etc.)
--   · 'dns_api_key'    (Cloudflare, Route53 para DNS-01 challenge)
--   · 'backup_key'     (S3, B2 — futuro)
--   · 'app_credential' (Docker registry, etc. — futuro)
--   · 'notify_token'   (Pushover, Telegram — futuro)
--   · 'smb_password', 'ssh_key', ...
--
-- key_version:
--   · 1 por defecto, master.key actual.
--   · En el futuro, rotación gradual: la master.key vieja se mueve a
--     key_history, los secrets se re-cifran lazy y key_version sube.
--
-- Master key: /var/lib/nimos/keys/master.key (32 bytes, chmod 600).
-- Si se pierde la master key, TODOS los secrets son irrecuperables.
-- El admin es responsable del backup de ese archivo.
CREATE TABLE IF NOT EXISTS nimos_secrets (
    id              TEXT    PRIMARY KEY,
    category        TEXT    NOT NULL,
    label           TEXT    NOT NULL,

    ciphertext      BLOB    NOT NULL,
    nonce           BLOB    NOT NULL,
    key_version     INTEGER NOT NULL DEFAULT 1,

    created_at      TEXT    NOT NULL,
    last_accessed   TEXT,

    UNIQUE(category, label)
);
CREATE INDEX IF NOT EXISTS idx_nimos_secrets_category ON nimos_secrets(category);

-- =============================================================================
-- nimos_breakers — Estado MÍNIMO de circuit breakers (DISCIPLINE §3 v2)
-- =============================================================================
-- Solo lo necesario para respetar el cooldown across daemon restart:
--   · name           — identificador único del breaker
--   · state          — 'closed' / 'open' / 'half_open'
--   · next_retry_at  — válido solo si state='open'
--
-- Métricas (failure rate, total calls, etc.) NO van aquí. Si las
-- necesitamos, irán en una tabla aparte tipo nimos_metrics o un sistema
-- de telemetría dedicado.
--
-- Escritura LAZY: solo cuando el state cambia. En operación normal,
-- <10 writes/día por breaker.
CREATE TABLE IF NOT EXISTS nimos_breakers (
    name              TEXT    PRIMARY KEY,
    state             TEXT    NOT NULL CHECK(state IN ('closed','open','half_open')),
    next_retry_at     TEXT
);

-- =============================================================================
-- nimos_capabilities — Cache de detection (DISCIPLINE §7 v2)
-- =============================================================================
-- Singleton (id='system'). El daemon lo refresca al boot y luego
-- ON-DEMAND cuando el frontend pide /api/network/capabilities con
-- last_detected_at > 1h.
--
-- NUNCA polling activo periódico.
CREATE TABLE IF NOT EXISTS nimos_capabilities (
    id               TEXT PRIMARY KEY,
    detected_at      TEXT NOT NULL,
    capabilities     TEXT NOT NULL    -- JSON serializado de SystemCapabilities
);
