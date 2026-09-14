-- +goose Up
-- One cursor per account becomes one cursor per vehicle. An account with two
-- cars has one cursor and needs two, and the stored instant does not say how
-- far each car got — so there is no honest mapping. Copying it to both would
-- claim progress never made for at least one, and a cursor that is too far
-- ahead skips rows silently, forever.
--
-- Delete them instead. An absent cursor already means "epoch": the next
-- nightly run re-reads that vehicle's whole Supercharger history once. The
-- mirror is an idempotent upsert that only moves updated_at when a value
-- really changed, so that pass writes nothing new. The cost is one longer
-- run, not a data change.
DELETE FROM charging.mirror_watermarks;

ALTER TABLE charging.mirror_watermarks
    DROP CONSTRAINT mirror_watermarks_account_unique;

ALTER TABLE charging.mirror_watermarks
    DROP COLUMN account_id;

-- NOT NULL with no DEFAULT is only legal because the DELETE above left the
-- table empty. That is the point: there is no value a surviving row could
-- honestly take.
ALTER TABLE charging.mirror_watermarks
    ADD COLUMN tesla_id BIGINT NOT NULL;

ALTER TABLE charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_vehicle_unique UNIQUE (tesla_id);

-- +goose Down
-- NOT an undo: the cursors the Up migration deleted are gone, and so is every
-- tesla_id written after it. Down restores the shape and leaves the table
-- empty, which is the safe state — an absent cursor re-reads the full history
-- rather than skipping it.
DELETE FROM charging.mirror_watermarks;

ALTER TABLE charging.mirror_watermarks
    DROP CONSTRAINT mirror_watermarks_vehicle_unique;

ALTER TABLE charging.mirror_watermarks
    DROP COLUMN tesla_id;

ALTER TABLE charging.mirror_watermarks
    ADD COLUMN account_id UUID NOT NULL;

ALTER TABLE charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_account_unique UNIQUE (account_id);
