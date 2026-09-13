-- +goose Up
-- A row with tesla_id IS NULL is a session whose VIN is not a registered
-- vehicle. Once account_id is gone, nothing ties such a row to an account and
-- no query can reach it again. Delete first, then forbid new ones. Checked on
-- the live database before writing this: zero such rows, so this deletes
-- nothing today. It is a guard against a row arriving before the migration
-- runs.
DELETE FROM telemetry.supercharger_history
WHERE tesla_id IS NULL;

ALTER TABLE telemetry.supercharger_history
    ALTER COLUMN tesla_id SET NOT NULL;

-- Schema-qualified: an index name resolves through search_path, and goose
-- does not guarantee telemetry is on it.
DROP INDEX telemetry.idx_supercharger_history_vehicle_time;
DROP INDEX telemetry.idx_supercharger_history_account_time;
DROP INDEX telemetry.idx_supercharger_history_account_updated;

ALTER TABLE telemetry.supercharger_history
    DROP COLUMN account_id;

-- Per-vehicle session list, newest first. tesla_id equality and
-- charge_start_date_time DESC together serve the WHERE and the ORDER BY in
-- one range scan, with no sort step.
CREATE INDEX idx_supercharger_history_vehicle_time
    ON telemetry.supercharger_history (tesla_id, charge_start_date_time DESC);

-- Per-vehicle "changed since" read, which bounds the nightly mirror.
-- tesla_id prunes to the car; updated_at ascending satisfies both the
-- >= predicate and the ORDER BY, so the planner needs no sort step.
CREATE INDEX idx_supercharger_history_vehicle_updated
    ON telemetry.supercharger_history (tesla_id, updated_at);

-- +goose Down
-- NOT an undo. Rows the Up migration deleted are gone, and every surviving
-- row's account_id went with the column. Down restores the SHAPE only:
-- account_id comes back NULLABLE and empty, so the three account-scoped
-- indexes it recreates index nothing until something refills the column.
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_vehicle_updated;
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_vehicle_time;

ALTER TABLE telemetry.supercharger_history
    ADD COLUMN account_id UUID;

ALTER TABLE telemetry.supercharger_history
    ALTER COLUMN tesla_id DROP NOT NULL;

CREATE INDEX idx_supercharger_history_vehicle_time
    ON telemetry.supercharger_history (account_id, tesla_id, charge_start_date_time DESC);

CREATE INDEX idx_supercharger_history_account_time
    ON telemetry.supercharger_history (account_id, charge_start_date_time DESC);

CREATE INDEX idx_supercharger_history_account_updated
    ON telemetry.supercharger_history (account_id, updated_at);
