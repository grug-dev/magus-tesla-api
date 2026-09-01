-- +goose Up
-- RM38-analytics-add-vehicle-status-columns (MAG-12 tier 1). Adds eight
-- nullable "vehicle status" observation columns to the existing
-- vehicle_metrics table (ai/architecture.md's own precomputed read model,
-- see 20260821000001_add_vehicle_metrics.sql), so this table becomes a
-- complete substitute for telemetry.Reader.LatestSnapshotsByAccount at the
-- gateway's four call sites (tier 2, a separate change, repoints them).
--
-- All eight are copied verbatim from the day's own telemetry.Snapshot by
-- Recalculate -- no re-derivation -- and are always populated regardless of
-- whether that day has a computable predecessor (design D3), unlike the
-- five pre-existing _calc columns.
--
-- NO BACKFILL (roadmap D2): a single ADD COLUMN migration, all eight
-- nullable, no DEFAULT. Every existing row keeps all eight NULL forever;
-- each vehicle's next regular nightly Reconcile populates them going
-- forward. A DEFAULT would fabricate a value for a row whose real state at
-- that time is genuinely unknown -- rejected explicitly (design D7).
ALTER TABLE vehicle_metrics
    ADD COLUMN locked                BOOLEAN,
    ADD COLUMN sentry_mode           BOOLEAN,
    ADD COLUMN car_version           TEXT,
    ADD COLUMN inside_temp_c         DOUBLE PRECISION,
    ADD COLUMN outside_temp_c        DOUBLE PRECISION,
    ADD COLUMN charging_state        TEXT,
    ADD COLUMN charge_limit_soc_pct  INTEGER,
    ADD COLUMN captured_at           TIMESTAMPTZ;

COMMENT ON COLUMN vehicle_metrics.locked IS
    'Copied verbatim from telemetry.Snapshot.Locked (no re-derivation). Always populated '
    'from the day''s own snapshot, independent of predecessor existence (design D3) -- unlike '
    'the five _calc columns, this is a raw per-day observation, not a delta. NULL means only '
    '"this row predates the RM38-analytics-add-vehicle-status-columns migration" -- no '
    'backfill was run (roadmap D2); it never means "not reported" (the source field is a '
    'plain bool, always populated once telemetry has a snapshot at all).';
COMMENT ON COLUMN vehicle_metrics.sentry_mode IS
    'Copied verbatim from telemetry.Snapshot.SentryMode (no re-derivation). Always populated '
    'from the day''s own snapshot, independent of predecessor existence (design D3). '
    'AMBIGUOUS NULL, unlike every other column added by this migration: NULL means EITHER '
    '"the vehicle did not report sentry" (SentryMode''s existing meaning on '
    'telemetry.Snapshot/vehicle_snapshots -- a *bool source field) OR "this row predates the '
    'RM38 migration" (roadmap D2, no backfill). Disambiguate via captured_at (also added by '
    'this migration): sentry_mode IS NULL AND captured_at IS NOT NULL means "not reported"; '
    'both NULL means "predates this migration" (design D8). No current consumer needs to '
    'disambiguate -- tier 2''s badge rendering (roadmap D5) treats NULL as "no badge" under '
    'either reading.';
COMMENT ON COLUMN vehicle_metrics.car_version IS
    'Copied verbatim from telemetry.Snapshot.CarVersion. Always populated from the day''s own '
    'snapshot, independent of predecessor existence (design D3). NULL means only "this row '
    'predates the RM38 migration" (roadmap D2) -- the source field is a plain string, never '
    'nil once telemetry has a snapshot.';
COMMENT ON COLUMN vehicle_metrics.inside_temp_c IS
    'Copied verbatim from telemetry.Snapshot.InsideTempC (already Celsius -- Tesla reports '
    'temps in Celsius natively, no conversion at any layer). Always populated from the day''s '
    'own snapshot, independent of predecessor existence (design D3). NULL means only "this '
    'row predates the RM38 migration" (roadmap D2).';
COMMENT ON COLUMN vehicle_metrics.outside_temp_c IS
    'Copied verbatim from telemetry.Snapshot.OutsideTempC. Same always-populated and NULL '
    'semantics as inside_temp_c above. The ticket (MAG-12) misspelled this column '
    '"outsite_temp_c" -- the correct name, matching the _c display-unit suffix convention '
    'and the source field''s own name, is outside_temp_c (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.charging_state IS
    'Copied verbatim from telemetry.Snapshot.ChargingState. Always populated from the day''s '
    'own snapshot, independent of predecessor existence (design D3). NULL means only "this '
    'row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original five '
    'columns because the gateway''s dashSubtitle needs it for the card subtitle (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.charge_limit_soc_pct IS
    'Copied verbatim from telemetry.Snapshot.ChargeLimitSocPct. Always populated from the '
    'day''s own snapshot, independent of predecessor existence (design D3). NULL means only '
    '"this row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original '
    'five columns for the Battery card''s "Limit N%" line (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.captured_at IS
    'Copied verbatim from telemetry.Snapshot.CapturedAt -- the exact capture instant, distinct '
    'from metric_date (this row''s own effective CALENDAR DAY, one day before CapturedAt''s own '
    'calendar day). Always populated from the day''s own snapshot, independent of predecessor '
    'existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap '
    'D2). Added beyond the ticket''s original five columns for the gateway''s LastUpdated/ '
    'IsStale freshness badge (roadmap D1) -- also doubles as the disambiguation proxy for '
    'sentry_mode''s ambiguous NULL (design D8).';

-- New index, added at the database design gate by the owner's explicit
-- instruction, revising design.md's original "no new index" conclusion
-- (design.md "Index Plan"). LatestMetricsByAccount orders by
-- tesla_id ASC, metric_date DESC -- a btree cannot be walked in mixed
-- direction, so against the existing all-ascending
-- vehicle_metrics_account_tesla_date_unique index Postgres would need an
-- incremental sort on top of the index scan. This index matches the
-- ORDER BY exactly, so that sort disappears. Accepted write cost: every
-- Recalculate/Reconcile UPSERT now maintains a second index -- licensed by
-- the read-heavy Performance-Profile (writes are a midnight poller; reads
-- are the dashboard hot path tier 2 puts on four pages).
CREATE INDEX idx_vehicle_metrics_latest
    ON vehicle_metrics (account_id, tesla_id, metric_date DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_vehicle_metrics_latest;

ALTER TABLE vehicle_metrics
    DROP COLUMN locked,
    DROP COLUMN sentry_mode,
    DROP COLUMN car_version,
    DROP COLUMN inside_temp_c,
    DROP COLUMN outside_temp_c,
    DROP COLUMN charging_state,
    DROP COLUMN charge_limit_soc_pct,
    DROP COLUMN captured_at;
