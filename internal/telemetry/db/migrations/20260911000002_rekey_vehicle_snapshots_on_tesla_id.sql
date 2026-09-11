-- +goose Up
-- Collapse any (tesla_id, captured_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row with the latest captured_at, ties
-- broken by id -- the same "newest capture wins" rule the write path
-- already applies one column narrower (20260805000001's own dedupe, design
-- D1: a same-day re-capture REPLACES the existing row). On a database where
-- poll election (Part 1 of this change) has already suppressed every
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

ALTER TABLE telemetry.vehicle_snapshots
    DROP COLUMN account_id;

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

-- account_id comes back NULLABLE, not NOT NULL: the DROP COLUMN in Up threw
-- the values away, so there is nothing to backfill a NOT NULL constraint
-- with on a populated table -- the same limitation every DROP COLUMN in
-- this codebase's migrations has on Down (mirrors the pilot's own Down).
ALTER TABLE telemetry.vehicle_snapshots
    ADD COLUMN account_id UUID;

CREATE INDEX idx_vehicle_snapshots_vehicle_time
    ON telemetry.vehicle_snapshots (account_id, tesla_id, captured_at);

ALTER TABLE telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, captured_date);
