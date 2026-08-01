-- internal/telemetry — vehicle_snapshots refactor:
--   ADD max_range_charge_counter (nullable int, lifetime charge-to-max-range counter from
--       charge_state.max_range_charge_counter in raw_data).
--   DROP latitude, longitude (unused typed columns; lossless in raw_data->drive_state).
--   DROP fast_charger_type (low-value free-text; lossless in raw_data->charge_state).
--
-- Nullable: pre-migration rows predate extraction and stay NULL rather than being
-- misrepresented as 0. A real reported 0 is stored as non-NULL (pointer-wrapped in
-- snapshotFrom — see DSA3/D12 convention from RM2 design.md). NULL means "not yet
-- extracted", never "counter was zero".
--
-- No new index: max_range_charge_counter is not a filter/sort column on any hot read path;
-- see Index Plan section below.
--
-- Owned by internal/telemetry; no cross-module FK. No raw_data change.

-- +goose Up

ALTER TABLE vehicle_snapshots
    ADD COLUMN max_range_charge_counter INTEGER;    -- nullable; NULL for pre-migration rows

-- One-shot backfill: populate max_range_charge_counter from raw_data for rows that
-- already have the field in their charge_state payload. Rows whose raw_data lacks the
-- path (e.g. older snapshots before Tesla started reporting it, or snapshots captured
-- before this field existed in the DTO) stay NULL — correct behavior, not a data loss.
UPDATE vehicle_snapshots
SET max_range_charge_counter =
        (raw_data -> 'charge_state' ->> 'max_range_charge_counter')::INTEGER
WHERE jsonb_typeof(raw_data -> 'charge_state' -> 'max_range_charge_counter') = 'number';

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS longitude,
    DROP COLUMN IF EXISTS fast_charger_type;

-- +goose Down

-- Re-add the dropped columns as nullable (DOWN cannot recover the original data — values
-- remain in raw_data but this migration does not back-populate them). Honestly documented:
-- a Down migration after data loss from a DROP COLUMN is best-effort schema restoration
-- only; actual historical values are not recovered here (only re-derivable from raw_data).
ALTER TABLE vehicle_snapshots
    ADD COLUMN IF NOT EXISTS latitude        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS longitude       DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS fast_charger_type TEXT;

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS max_range_charge_counter;
