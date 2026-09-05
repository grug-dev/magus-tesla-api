-- +goose Up
-- internal/analytics — mirror telemetry.vehicle_snapshots.max_range_charge_counter
-- onto analytics.vehicle_metrics.
--
-- Same shape as the eight RM38 status observation columns
-- (20260901000001_add_vehicle_status_columns.sql): a RAW per-day observation copied
-- verbatim from that day's own telemetry.Snapshot by deriveVehicleMetrics, populated on
-- EVERY row including a predecessor-less one -- the opposite rule to the five _calc
-- columns, which are deltas and are NULL without a predecessor. There is nothing for a
-- missing predecessor to invalidate in a value that is simply read off the day's capture.
--
-- No unit suffix, deliberately: this is a COUNT, not a unit-bearing measurement, so the
-- _km/_c/_pct convention (CLAUDE.md "Display units") does not apply. Its sibling
-- charge_limit_soc_pct carries _pct because that one really is a percentage.
--
-- NO BACKFILL, mirroring RM38 roadmap D2: nullable, no DEFAULT. Every existing row keeps
-- the column NULL forever; each vehicle's next nightly Reconcile populates it going
-- forward. A DEFAULT of 0 would be a fabricated claim -- "this vehicle was never charged
-- to max range" -- about a day whose real counter is genuinely unknown.
--
-- NO NEW INDEX: nothing reads this column yet. It is written by UpsertVehicleMetric and
-- appears in no WHERE, JOIN, or ORDER BY. An index would be pure write cost on the
-- nightly Recalculate/Reconcile path with no read to pay for it. Revisit only if a read
-- port later filters or sorts on it.
ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN max_range_charge_counter INTEGER;

COMMENT ON COLUMN analytics.vehicle_metrics.max_range_charge_counter IS
    'Copied verbatim from telemetry.Snapshot.MaxRangeChargeCounter (no re-derivation, no '
    'conversion) -- the vehicle''s LIFETIME count of charges to its true 100% '
    'Maximum-Battery-Range limit, so it is monotonic across rows, not a per-day figure. '
    'Always populated from the day''s own snapshot, independent of predecessor existence, '
    'exactly like the eight columns added by 20260901000001. AMBIGUOUS NULL, like '
    'sentry_mode: NULL means EITHER "the vehicle did not report it / the snapshot predates '
    'telemetry migration 20260801000001" (the source field is itself a *int) OR "this '
    'vehicle_metrics row predates THIS migration" (no backfill was run). Disambiguate via '
    'captured_at: NULL counter with a non-NULL captured_at means "not reported". A real '
    'reported 0 is stored as 0, never as NULL -- telemetry pointer-wraps it (D12/DSA3), '
    'and 0 legitimately means "never charged to max range".';

-- +goose Down
ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN max_range_charge_counter;
