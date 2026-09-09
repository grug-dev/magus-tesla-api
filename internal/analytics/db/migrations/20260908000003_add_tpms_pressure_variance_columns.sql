-- +goose Up
-- internal/analytics -- four day-over-day tyre-pressure delta columns
-- (RM50-analytics-add-tire-pressure-variance design.md Part A). Each is one
-- wheel's day's raw pressure minus the immediately preceding day's raw
-- pressure for the same wheel and vehicle, computed in Go by
-- consumption.go's deriveConsumption (design D1) and persisted by
-- Recalculate on every UPSERT.
--
-- NOT the same shape as tpms_pressure_fl_psi (tier 1, 20260908000002): that
-- column is a raw observation, always populated once a predecessor-less
-- guard is not in play; these four are DELTAS, following the same NULL rule
-- as distance_traveled_km_calc -- NULL when the day has no predecessor at
-- all, OR when either day's own raw wheel reading is itself NULL (design
-- D2, two independent conditions). Never a fabricated 0.
--
-- This delta partly reflects ambient air temperature change (about 1 PSI
-- per 5.5 degrees C), not only a genuine pressure change. That is accepted,
-- not a defect (roadmap RD3) -- no threshold or target-pressure comparison
-- may be added to "correct" it.
--
-- NO NEW INDEX (design D3): every column here is projected only by
-- LatestVehicleMetricsByAccount, never filtered, joined, or ordered on.
ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN tpms_pressure_fl_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_fr_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rl_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rr_psi_calc DOUBLE PRECISION;

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi_calc IS
    'Derived delta: this row''s tpms_pressure_fl_psi minus the previous day''s row for '
    'the same vehicle, in PSI. NULL when this day has no predecessor row, OR when '
    'either day''s own tpms_pressure_fl_psi reading is itself NULL -- never a '
    'fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees '
    'C) -- this is accepted, not a defect, and must never be "fixed" with a threshold '
    'or a target-pressure comparison (RM50 roadmap RD3).';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fr_psi_calc IS
    'Derived delta: this row''s tpms_pressure_fr_psi minus the previous day''s row for '
    'the same vehicle, in PSI. NULL when this day has no predecessor row, OR when '
    'either day''s own tpms_pressure_fr_psi reading is itself NULL -- never a '
    'fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees '
    'C) -- this is accepted, not a defect, and must never be "fixed" with a threshold '
    'or a target-pressure comparison (RM50 roadmap RD3).';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rl_psi_calc IS
    'Derived delta: this row''s tpms_pressure_rl_psi minus the previous day''s row for '
    'the same vehicle, in PSI. NULL when this day has no predecessor row, OR when '
    'either day''s own tpms_pressure_rl_psi reading is itself NULL -- never a '
    'fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees '
    'C) -- this is accepted, not a defect, and must never be "fixed" with a threshold '
    'or a target-pressure comparison (RM50 roadmap RD3).';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rr_psi_calc IS
    'Derived delta: this row''s tpms_pressure_rr_psi minus the previous day''s row for '
    'the same vehicle, in PSI. NULL when this day has no predecessor row, OR when '
    'either day''s own tpms_pressure_rr_psi reading is itself NULL -- never a '
    'fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees '
    'C) -- this is accepted, not a defect, and must never be "fixed" with a threshold '
    'or a target-pressure comparison (RM50 roadmap RD3).';

-- Part B (design.md) -- a one-time backfill via a self-join, not a
-- cross-schema read: this migration reads only analytics.vehicle_metrics,
-- joined against itself. Tier 1 already copied the raw tpms_pressure_*_psi
-- values onto every row of this same table, so the predecessor's raw
-- reading is already sitting in the row this join reaches. No other
-- module's schema is touched, so this is NOT a boundary deviation and needs
-- no // boundary:allow: comment (make boundary-guard does not grep
-- migration files at all).
--
-- A row whose previous day is missing: the FROM ... WHERE join finds no
-- matching prev row, so the UPDATE never touches that row -- its four new
-- columns stay NULL (their post-ADD-COLUMN default). Correct per design D2,
-- condition 1 -- not an error, not a 0.
--
-- A row whose previous day exists but is missing one wheel's raw reading:
-- Postgres arithmetic on a NULL operand always yields NULL, so that wheel's
-- _calc column becomes NULL automatically, with no CASE needed -- matching
-- design D2, condition 2, with the exact same rule the Go implementation
-- uses.
--
-- No to_regclass existence guard: analytics.vehicle_metrics is this
-- migration's own table, always present by construction.
UPDATE analytics.vehicle_metrics vm
SET
    tpms_pressure_fl_psi_calc = vm.tpms_pressure_fl_psi - prev.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi_calc = vm.tpms_pressure_fr_psi - prev.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi_calc = vm.tpms_pressure_rl_psi - prev.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi_calc = vm.tpms_pressure_rr_psi - prev.tpms_pressure_rr_psi
FROM analytics.vehicle_metrics prev
WHERE prev.account_id  = vm.account_id
  AND prev.tesla_id    = vm.tesla_id
  AND prev.metric_date = vm.metric_date - 1;

-- +goose Down
-- Only reverses the schema change (drop the four columns) -- mirrors tier
-- 1's own Down block and 20260905000001's, neither of which attempts to
-- un-backfill data. A Down that also tried to null out backfilled values
-- would be pure churn: the columns are about to be dropped anyway.
ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN tpms_pressure_fl_psi_calc,
    DROP COLUMN tpms_pressure_fr_psi_calc,
    DROP COLUMN tpms_pressure_rl_psi_calc,
    DROP COLUMN tpms_pressure_rr_psi_calc;
