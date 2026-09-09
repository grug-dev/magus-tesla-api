-- +goose Up
-- internal/analytics -- mirror telemetry.vehicle_snapshots' four TPMS (tire-pressure
-- monitoring system) columns onto analytics.vehicle_metrics.
--
-- Same shape as max_range_charge_counter (20260905000001) and the eight RM38 status
-- observation columns (20260901000001): a RAW per-day observation copied verbatim from
-- that day's own telemetry.Snapshot by deriveVehicleMetrics, populated on EVERY row
-- including a predecessor-less one -- the opposite rule to the five _calc columns,
-- which are deltas and are NULL without a predecessor. There is nothing for a missing
-- predecessor to invalidate in a value that is simply read off the day's capture.
--
-- Names match telemetry.vehicle_snapshots' own column names exactly (fl/fr/rl/rr =
-- front-left/front-right/rear-left/rear-right) -- a closed, small vocabulary an agent
-- can reuse instead of holding a translation table in its head (RM50 design.md D1).
-- Already PSI at the source (internal/telemetry converts from the Fleet API's native
-- bar reading once, at capture time) -- this migration does no unit conversion.
--
-- NO BACKFILL for this Up block alone -- the backfill runs as a separate statement
-- below, immediately after the columns exist, per RM50 design.md Part C (a deliberate,
-- recorded deviation from the "No Cross-Module Database Access" rule -- see design.md
-- for the full rationale and the rejected watermark-reset alternative).
--
-- NO NEW INDEX (RM50 design.md D3): every column this migration adds is projected only
-- by LatestVehicleMetricsByAccount, never filtered, joined, or ordered on. An index
-- would cost write time on every nightly Recalculate/Reconcile UPSERT with no read
-- benefit -- the same reasoning 20260905000001's own "no new index" note already gives.
ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN tpms_pressure_fl_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_fr_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rl_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rr_psi DOUBLE PRECISION;

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi IS
    'Copied verbatim from telemetry.Snapshot.TpmsPressureFLPSI (no re-derivation, no '
    'conversion -- already PSI). A raw per-day observation, populated on EVERY row '
    'including a predecessor-less day, exactly like max_range_charge_counter and the '
    'eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means '
    'the vehicle did not report TPMS at capture, OR this row predates this migration and '
    'was not touched by the one-off backfill.';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fr_psi IS
    'Copied verbatim from telemetry.Snapshot.TpmsPressureFRPSI (no re-derivation, no '
    'conversion -- already PSI). A raw per-day observation, populated on EVERY row '
    'including a predecessor-less day, exactly like max_range_charge_counter and the '
    'eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means '
    'the vehicle did not report TPMS at capture, OR this row predates this migration and '
    'was not touched by the one-off backfill.';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rl_psi IS
    'Copied verbatim from telemetry.Snapshot.TpmsPressureRLPSI (no re-derivation, no '
    'conversion -- already PSI). A raw per-day observation, populated on EVERY row '
    'including a predecessor-less day, exactly like max_range_charge_counter and the '
    'eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means '
    'the vehicle did not report TPMS at capture, OR this row predates this migration and '
    'was not touched by the one-off backfill.';

COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rr_psi IS
    'Copied verbatim from telemetry.Snapshot.TpmsPressureRRPSI (no re-derivation, no '
    'conversion -- already PSI). A raw per-day observation, populated on EVERY row '
    'including a predecessor-less day, exactly like max_range_charge_counter and the '
    'eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means '
    'the vehicle did not report TPMS at capture, OR this row predates this migration and '
    'was not touched by the one-off backfill.';

-- RM50 design.md Part C -- the recorded, user-confirmed deviation from "No Cross-Module
-- Database Access": a one-off backfill, run once by goose at deploy time, never again
-- and never a Go import (make boundary-guard only greps internal/gateway/**/*.go for a
-- Go import, so this SQL file trips nothing and needs no escape hatch). Matches
-- vm.metric_date (the row's effective day) against vs.captured_date - 1 (the same
-- offset recomputed in plain SQL). A vehicle_metrics row with no matching snapshot is
-- left with all four columns still NULL -- the correct outcome, not an error. Migration
-- order (Makefile MIGRATIONS_DIRS) always applies every telemetry migration, including
-- the one that created these source columns, before any analytics migration runs
-- (design.md "Migration order check") -- no to_regclass existence guard needed.
UPDATE analytics.vehicle_metrics vm
SET
    tpms_pressure_fl_psi = vs.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi = vs.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi = vs.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi = vs.tpms_pressure_rr_psi
FROM telemetry.vehicle_snapshots vs
WHERE vm.account_id  = vs.account_id
  AND vm.tesla_id    = vs.tesla_id
  AND vm.metric_date = vs.captured_date - 1;

-- +goose Down
-- Only reverses the schema change (drop the four columns) -- mirrors 20260905000001's
-- own Down block, which does not attempt to un-backfill data either. A Down that also
-- tried to null out backfilled values would be pure churn: the columns are about to be
-- dropped anyway.
ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN tpms_pressure_fl_psi,
    DROP COLUMN tpms_pressure_fr_psi,
    DROP COLUMN tpms_pressure_rl_psi,
    DROP COLUMN tpms_pressure_rr_psi;
