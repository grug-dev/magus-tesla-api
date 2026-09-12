-- +goose Up
-- Collapse any (tesla_id, captured_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row with the latest captured_at, ties
-- broken by id -- the same "newest capture wins" rule the write path
-- already applies one column narrower: a same-day re-capture REPLACES the
-- existing row. On a database where poll election already suppressed every
-- duplicate-writer case, this deletes zero rows; it exists so the migration
-- is still safe against a database migrated before that Go change deploys.
DELETE FROM telemetry.vehicle_snapshots a
USING telemetry.vehicle_snapshots b
WHERE a.tesla_id = b.tesla_id
  AND a.captured_date = b.captured_date
  AND (a.captured_at < b.captured_at
       OR (a.captured_at = b.captured_at AND a.id < b.id));

ALTER TABLE telemetry.vehicle_snapshots
    DROP CONSTRAINT vehicle_snapshots_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and
-- goose does not guarantee telemetry is on it.
DROP INDEX telemetry.idx_vehicle_snapshots_vehicle_time;

-- account_id is NOT dropped yet, only made nullable, and the write path stops
-- filling it. A 2026-09-08 analytics backfill migration still joins this table
-- on account_id. Migrations are applied one module directory at a time --
-- account, telemetry, charging, analytics -- so on a fresh database every
-- telemetry migration runs before that analytics one. Dropping the column here
-- makes the analytics migration fail on any new database, including every test
-- container. The column is dropped once migrations are applied in date order
-- across modules; until then it holds NULL for rows written from now on.
ALTER TABLE telemetry.vehicle_snapshots
    ALTER COLUMN account_id DROP NOT NULL;

ALTER TABLE telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_tesla_date_unique UNIQUE (tesla_id, captured_date);

-- poll_attempts.account_id is NOT dropped -- it is renamed. It has never
-- been a co-identity column (the row's identity is (vehicle, run)); it
-- records which account's token paid for this Fleet API call. That fact
-- only becomes reliable once poll election (Part 1) removes the possibility
-- of two accounts both attempting the same vehicle in one cycle.
ALTER TABLE telemetry.poll_attempts
    RENAME COLUMN account_id TO polled_by_account_id;

-- +goose Down
-- NOT a full rollback of history: rows deleted by the Up migration's
-- collapse step are NOT recoverable (their raw_data is gone with them).
-- Down only reverses this migration's own schema changes.
ALTER TABLE telemetry.poll_attempts
    RENAME COLUMN polled_by_account_id TO account_id;

ALTER TABLE telemetry.vehicle_snapshots
    DROP CONSTRAINT vehicle_snapshots_tesla_date_unique;

-- account_id is not re-added: Up only relaxed it to NULLABLE, it was never
-- dropped. It is not restored to NOT NULL either, because rows written while
-- Up was in effect hold NULL and there is nothing to backfill them with.

CREATE INDEX idx_vehicle_snapshots_vehicle_time
    ON telemetry.vehicle_snapshots (account_id, tesla_id, captured_at);

ALTER TABLE telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, captured_date);
