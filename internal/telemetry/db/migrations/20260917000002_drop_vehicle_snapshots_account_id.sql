-- Drop telemetry.vehicle_snapshots.account_id.
--
-- The column stopped being written when snapshots were re-keyed on tesla_id, and
-- it could not be dropped then: an analytics migration joined on it, and analytics
-- runs after telemetry, so dropping it broke every fresh database. The squash
-- removed that migration, so the column can finally go.
--
-- This is a normal migration and NOT part of the baseline, on purpose. Existing
-- databases have the baseline recorded as applied without it ever running, so a
-- column merely left out of the baseline would survive on dev and prod while a new
-- database lacked it. The two would then never again produce the same pg_dump, and
-- comparing them is how this project proves a schema change is correct.
--
-- No index and no constraint references the column: the primary key is on id and
-- the unique constraint is on (tesla_id, captured_date). So nothing is rebuilt and
-- no read plan changes.

-- +goose Up
ALTER TABLE telemetry.vehicle_snapshots
    DROP COLUMN account_id;

-- +goose Down

-- Restored as nullable with no index, which is exactly what it was before the drop.
-- The values are not restored: which account polled a given row is not recoverable
-- from this table, and no code has read the column since the re-key.
ALTER TABLE telemetry.vehicle_snapshots
    ADD COLUMN account_id uuid;
