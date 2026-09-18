-- Adds three day-over-day travel-progress delta columns to
-- analytics.vehicle_metrics, and renames the four existing tyre-pressure
-- delta columns so all seven share one `_delta_calc` naming convention.
--
-- The four tpms_pressure_*_psi_calc columns already hold correct
-- day-over-day PSI deltas -- only their name was wrong. RENAME COLUMN is a
-- catalog-only change (no table rewrite, no data loss), so this migration
-- renames rather than drops and re-adds.

-- +goose Up

ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN distance_traveled_km_delta_calc double precision,
    ADD COLUMN consumed_pct_delta_calc double precision,
    ADD COLUMN km_per_pct_delta_calc double precision;

ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fl_psi_calc TO tpms_pressure_fl_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fr_psi_calc TO tpms_pressure_fr_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rl_psi_calc TO tpms_pressure_rl_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rr_psi_calc TO tpms_pressure_rr_psi_delta_calc;

-- COMMENT: COLUMN vehicle_metrics.distance_traveled_km_delta_calc
COMMENT ON COLUMN analytics.vehicle_metrics.distance_traveled_km_delta_calc IS 'Day-over-day change in distance_traveled_km_calc: this row''s value minus the value on the row the recalculation loop built immediately before it. NULL when this day is the first day of a recalculation pass (no in-loop predecessor for that pass), or when either day''s own distance_traveled_km_calc is itself NULL.';

-- COMMENT: COLUMN vehicle_metrics.consumed_pct_delta_calc
COMMENT ON COLUMN analytics.vehicle_metrics.consumed_pct_delta_calc IS 'Day-over-day change in consumed_pct: this row''s value minus the value on the row the recalculation loop built immediately before it. NULL when this day is the first day of a recalculation pass (no in-loop predecessor for that pass), or when either day''s own consumed_pct is itself NULL.';

-- COMMENT: COLUMN vehicle_metrics.km_per_pct_delta_calc
COMMENT ON COLUMN analytics.vehicle_metrics.km_per_pct_delta_calc IS 'Day-over-day change in km_per_pct_calc: this row''s value minus the value on the row the recalculation loop built immediately before it. NULL when this day is the first day of a recalculation pass (no in-loop predecessor for that pass), or when either day''s own km_per_pct_calc is itself NULL.';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fl_psi_delta_calc
-- Same text the column already carried under its old name. The rename
-- changes which name the comment hangs on, not what it says.
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi_delta_calc IS 'Derived delta: this row''s tpms_pressure_fl_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_fl_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fr_psi_delta_calc
-- Carried forward unchanged, same as tpms_pressure_fl_psi_delta_calc above.
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fr_psi_delta_calc IS 'Derived delta: this row''s tpms_pressure_fr_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_fr_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rl_psi_delta_calc
-- Carried forward unchanged, same as tpms_pressure_fl_psi_delta_calc above.
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rl_psi_delta_calc IS 'Derived delta: this row''s tpms_pressure_rl_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_rl_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rr_psi_delta_calc
-- Carried forward unchanged, same as tpms_pressure_fl_psi_delta_calc above.
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rr_psi_delta_calc IS 'Derived delta: this row''s tpms_pressure_rr_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_rr_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison.';
-- +goose StatementEnd

-- Backfill: the three new columns for pre-existing rows. A self-join on
-- (tesla_id, metric_date - 1), served by vehicle_metrics_tesla_date_unique
-- (index-nested-loop, one pass over the table). A row with no stored
-- predecessor day gets no match and stays NULL; a row whose predecessor is
-- itself missing one of these figures yields NULL automatically, since
-- Postgres arithmetic on a NULL operand is always NULL.
UPDATE analytics.vehicle_metrics vm
SET
    distance_traveled_km_delta_calc = vm.distance_traveled_km_calc - prev.distance_traveled_km_calc,
    consumed_pct_delta_calc         = vm.consumed_pct - prev.consumed_pct,
    km_per_pct_delta_calc           = vm.km_per_pct_calc - prev.km_per_pct_calc
FROM analytics.vehicle_metrics prev
WHERE prev.tesla_id    = vm.tesla_id
  AND prev.metric_date = vm.metric_date - 1;

-- +goose Down
-- Reverses the rename and drops the three new columns; never un-backfills
-- (mirrors this table's other _delta_calc migrations, which only reverse
-- schema, not data).
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fl_psi_delta_calc TO tpms_pressure_fl_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fr_psi_delta_calc TO tpms_pressure_fr_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rl_psi_delta_calc TO tpms_pressure_rl_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rr_psi_delta_calc TO tpms_pressure_rr_psi_calc;

ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN distance_traveled_km_delta_calc,
    DROP COLUMN consumed_pct_delta_calc,
    DROP COLUMN km_per_pct_delta_calc;
