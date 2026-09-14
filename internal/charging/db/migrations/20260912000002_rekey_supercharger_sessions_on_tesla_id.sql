-- +goose Up
-- A row with tesla_id IS NULL is a session whose VIN was not a registered
-- vehicle when it was mirrored. Once account_id is gone, nothing ties such a
-- row to an account and every read on this table filters tesla_id = $1, which
-- a NULL never matches. Delete first, then forbid new ones. Checked on the
-- live database before writing this: zero such rows, so this deletes nothing
-- today. It guards against a row arriving before the migration runs.
DELETE FROM charging.supercharger_sessions
WHERE tesla_id IS NULL;

-- The old rule allowed one row per session PER ACCOUNT, so two accounts
-- sharing a car could each hold a copy of the same session. UNIQUE (session_id)
-- cannot be added while such a pair exists. Keep one row per session: prefer
-- the one carrying a human's battery percentages (the only value here that
-- cannot be re-derived from telemetry), then the oldest, then the lowest id so
-- the result is deterministic. Everything else on the losing row is a mirror
-- of telemetry and is rewritten onto the survivor by the next nightly pass.
DELETE FROM charging.supercharger_sessions
WHERE id IN (
    SELECT id
      FROM (
            SELECT id,
                   ROW_NUMBER() OVER (
                       PARTITION BY session_id
                       ORDER BY (start_battery_pct IS NOT NULL
                                 OR end_battery_pct IS NOT NULL) DESC,
                                created_at ASC,
                                id ASC
                   ) AS dup_rank
              FROM charging.supercharger_sessions
           ) ranked
     WHERE dup_rank > 1
);

ALTER TABLE charging.supercharger_sessions
    ALTER COLUMN tesla_id SET NOT NULL;

-- The upsert target moves with the key. A session happened to exactly one car,
-- so one row per session id is the true rule; the source table already uses it.
ALTER TABLE charging.supercharger_sessions
    DROP CONSTRAINT supercharger_sessions_account_session_unique;

ALTER TABLE charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_session_id_unique UNIQUE (session_id);

-- Schema-qualified: an index name resolves through search_path, and goose does
-- not guarantee charging is on it.
DROP INDEX charging.idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    DROP COLUMN account_id;

-- The same read this index always served, one column narrower: tesla_id
-- equality prunes to the car, ascending charge_stop_date_time serves the
-- half-open window and the ORDER BY in the same scan, and a backward walk of
-- the same index serves the newest-first limited read.
CREATE INDEX idx_supercharger_sessions_vehicle_stop
    ON charging.supercharger_sessions (tesla_id, charge_stop_date_time);

-- +goose Down
-- NOT an undo. Rows the Up migration deleted are gone, and every surviving
-- row's account_id went with the column. Down restores the SHAPE only:
-- account_id comes back NULLABLE and empty, so the index it recreates leads on
-- a column holding nothing until something refills it.
DROP INDEX IF EXISTS charging.idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    ADD COLUMN account_id UUID;

ALTER TABLE charging.supercharger_sessions
    ALTER COLUMN tesla_id DROP NOT NULL;

ALTER TABLE charging.supercharger_sessions
    DROP CONSTRAINT supercharger_sessions_session_id_unique;

-- Over an all-NULL account_id this constraint blocks nothing: Postgres treats
-- NULLs as distinct, so every row satisfies it until the column is refilled.
ALTER TABLE charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_account_session_unique
    UNIQUE (account_id, session_id);

CREATE INDEX idx_supercharger_sessions_vehicle_stop
    ON charging.supercharger_sessions (account_id, tesla_id, charge_stop_date_time);
