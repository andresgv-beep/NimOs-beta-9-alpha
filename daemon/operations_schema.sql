-- operations_schema.sql · Beta 8.1.x · APP-012
--
-- Tabla nimos_operations · async operations tracking
--
-- Modelo de uso (a partir de Fase 2 Batch 3):
--
--   Handler HTTP recibe operación lenta (install, pull, snapshot...).
--   1. Repo.Create(type, createdBy) → row con status=pending, id devuelto.
--   2. go func() { Repo.MarkRunning(id); ... trabajo ...;
--                  Repo.UpdateProgress(id, %, msg);
--                  Repo.MarkSucceeded(id, result) | MarkFailed(id, err) }()
--   3. Handler devuelve 202 Accepted con {operationId, pollUrl}.
--   4. Cliente hace GET /api/operations/{id} hasta status terminal.
--
-- Estados (state machine):
--
--   pending  ─▶  running  ─┬─▶  succeeded
--                          ├─▶  failed
--                          └─▶  cancelled (futuro · no implementado)
--
--   pending también puede ir a failed/cancelled si nunca llegó a empezar.
--
-- Garbage collection:
--
--   Las operations finalizadas (succeeded/failed/cancelled) tienen
--   expires_at = finished_at + 24h. Tras expiry, son candidatas para
--   GC manual o automático. No se borran automáticamente en este batch;
--   el endpoint GET devuelve igual si la row existe.

CREATE TABLE IF NOT EXISTS nimos_operations (
    id          TEXT PRIMARY KEY,                       -- 'op_1716567890_a3f9'
    type        TEXT NOT NULL,                          -- 'docker.install' | 'docker.pull' | 'backup.snapshot'
    status      TEXT NOT NULL DEFAULT 'pending',        -- 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'
    progress    INTEGER DEFAULT 0,                      -- 0..100
    message     TEXT DEFAULT '',                        -- última descripción de paso ('Pulling image...')
    result_json TEXT DEFAULT '',                        -- JSON serializado · resultado al completar (formato libre por tipo)
    error       TEXT DEFAULT '',                        -- mensaje si status='failed'
    created_at  TEXT NOT NULL,                          -- ISO timestamp
    started_at  TEXT DEFAULT '',                        -- ISO timestamp cuando pasó a running
    finished_at TEXT DEFAULT '',                        -- ISO timestamp al terminar (success | fail | cancel)
    expires_at  TEXT DEFAULT '',                        -- ISO timestamp para GC (suele ser finished_at + 24h)
    created_by  TEXT NOT NULL                           -- username · para autorización (creador o admin pueden GET)
);

-- Índices para queries comunes:
CREATE INDEX IF NOT EXISTS idx_nimos_operations_type       ON nimos_operations(type);
CREATE INDEX IF NOT EXISTS idx_nimos_operations_status     ON nimos_operations(status);
CREATE INDEX IF NOT EXISTS idx_nimos_operations_created_by ON nimos_operations(created_by);
CREATE INDEX IF NOT EXISTS idx_nimos_operations_expires_at ON nimos_operations(expires_at);
