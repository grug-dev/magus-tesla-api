-- analytics baseline.
--
-- One self-contained file that creates this module's whole schema. It is a
-- verbatim transcription of a pg_dump --schema-only taken on 2026-09-17, after
-- the analytics module's migration history was squashed.
--
-- It creates objects and reads nothing, so it depends on no other module and can
-- be applied in any order relative to them. Keep it that way: a migration here
-- must never name another module's schema.
--
-- Existing databases (dev and prod) have this version recorded as applied without
-- it ever running. So a change made HERE reaches new databases only. Anything that
-- must also reach dev and prod belongs in a later, ordinary migration.

-- +goose Up

-- SCHEMA: analytics
CREATE SCHEMA IF NOT EXISTS analytics;

-- TABLE: charge_gaps
CREATE TABLE analytics.charge_gaps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id bigint NOT NULL,
    vin text NOT NULL,
    gap_date date NOT NULL,
    missing_charging_type text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT charge_gaps_missing_charging_type_check CHECK ((missing_charging_type = ANY (ARRAY['MANUAL'::text, 'SUPERCHARGER'::text])))
);

-- TABLE: vehicle_metric_watermarks
CREATE TABLE analytics.vehicle_metric_watermarks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id bigint NOT NULL,
    source text NOT NULL,
    source_updated_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT vehicle_metric_watermarks_source_check CHECK ((source = ANY (ARRAY['vehicle_snapshots'::text, 'supercharger_sessions'::text, 'manual_charge_entries'::text])))
);

-- TABLE: vehicle_metrics
CREATE TABLE analytics.vehicle_metrics (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id bigint NOT NULL,
    metric_date date NOT NULL,
    battery_level_pct integer NOT NULL,
    odometer_km double precision NOT NULL,
    battery_range_km double precision NOT NULL,
    distance_traveled_km_calc double precision,
    battery_used_pct_calc integer,
    km_per_pct_calc double precision,
    estimated_range_km_calc double precision,
    days_spanned_calc integer,
    consumed_pct double precision,
    flagged boolean NOT NULL,
    missing_charging_type text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    locked boolean,
    sentry_mode boolean,
    car_version text,
    inside_temp_c double precision,
    outside_temp_c double precision,
    charging_state text,
    charge_limit_soc_pct integer,
    captured_at timestamp with time zone,
    max_range_charge_counter integer,
    tpms_pressure_fl_psi double precision,
    tpms_pressure_fr_psi double precision,
    tpms_pressure_rl_psi double precision,
    tpms_pressure_rr_psi double precision,
    tpms_pressure_fl_psi_calc double precision,
    tpms_pressure_fr_psi_calc double precision,
    tpms_pressure_rl_psi_calc double precision,
    tpms_pressure_rr_psi_calc double precision,
    CONSTRAINT vehicle_metrics_missing_charging_type_check CHECK ((missing_charging_type = ANY (ARRAY['MANUAL'::text, 'SUPERCHARGER'::text]))),
    CONSTRAINT vehicle_metrics_missing_type_iff_flagged CHECK (((flagged AND (missing_charging_type IS NOT NULL)) OR ((NOT flagged) AND (missing_charging_type IS NULL))))
);

-- COMMENT: TABLE charge_gaps
-- +goose StatementBegin
COMMENT ON TABLE analytics.charge_gaps IS 'Nightly-detected vehicle-days whose battery math does not add up -- a charge record is missing or incomplete (RM28-telemetry-add-charge-gap-storage, MAG-15). One row per (account_id, tesla_id, gap_date): a day''s shortfall is a single aggregate observation, never split across two rows. Written by internal/battery through the GapWriter port (telemetry never calls battery). No resolved_at / soft delete: a day that stops flagging is DELETED by the next nightly reconciliation, not marked resolved -- this table is a live worklist, not an audit trail. Owned by internal/telemetry; no other module reads this table directly.';
-- +goose StatementEnd

-- COMMENT: COLUMN charge_gaps.tesla_id
COMMENT ON COLUMN analytics.charge_gaps.tesla_id IS 'Always resolved and NOT NULL: a vehicle that cannot be attributed to a currently-registered vehicle is filtered out of internal/battery''s derivation before gap detection runs, unlike supercharger_sessions.tesla_id which is nullable for exactly that unattributed case.';

-- COMMENT: COLUMN charge_gaps.missing_charging_type
-- +goose StatementBegin
COMMENT ON COLUMN analytics.charge_gaps.missing_charging_type IS 'Which charge source is suspected missing for this day (D7a): SUPERCHARGER when a Supercharger session exists that day with NULL start/end battery percentages (the exact record that needs filling is already known); MANUAL otherwise (the vehicle was charged somewhere the Tesla Fleet API does not report). Inferred by internal/battery at detection time, never user-chosen.';
-- +goose StatementEnd

-- COMMENT: COLUMN charge_gaps.created_at
COMMENT ON COLUMN analytics.charge_gaps.created_at IS 'When this (account_id, tesla_id, gap_date) was FIRST flagged. Preserved across every subsequent nightly re-upsert of the same still-flagged day -- NOT refreshed on conflict -- so it answers "how long has this been outstanding" for a future notification consumer.';

-- COMMENT: COLUMN charge_gaps.updated_at
-- +goose StatementBegin
COMMENT ON COLUMN analytics.charge_gaps.updated_at IS 'When this row was last confirmed still-flagging by a nightly run. Refreshed to now() on every UPSERT conflict; a day that stops flagging is deleted outright rather than leaving a stale updated_at behind.';
-- +goose StatementEnd

-- COMMENT: TABLE vehicle_metric_watermarks
-- +goose StatementBegin
COMMENT ON TABLE analytics.vehicle_metric_watermarks IS 'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources (design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances on its own row, never coupled to the others'' clocks. No watermark row yet for a (account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s full history in one pass. Owned by internal/analytics; no other module reads this table directly. source''s vocabulary was migrated vehicle_snapshots/supercharger_sessions/manual_charge_entries -> vehicle_snapshots/charge_sessions/manual_charge_entries by 20260828000001 (RM31-analytics-read-sessions-from-charging), then back to vehicle_snapshots/supercharger_sessions/manual_charge_entries by THIS migration (RM39-analytics-fix-watermark-vocabulary, tier 3b) once internal/charging renamed its own table to supercharger_sessions (RM39 tier 3, D5b). The reused string now names a DIFFERENT physical table (charging.supercharger_sessions) than it did before 20260828000001 (telemetry.supercharger_sessions) -- see COMMENT ON COLUMN .source for the disambiguation.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metric_watermarks.source
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source IS 'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): ''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/charging, AS OF RM39-analytics-fix-watermark-vocabulary -- this is a REUSED string; before RM31 (20260828000001) the same literal named internal/telemetry''s table instead, and until RM39 tier 4 renames that table to supercharger_history, a DIFFERENT, still-live table (public.supercharger_sessions) shares this bare name in this same database. This column never schema-qualifies its own value (RM39 tier 2 decision -- these are data, not table references), so a reader relies on this comment, internal/analytics/AGENTS.md, and recalculate.go''s sourceSuperchargerSessions constant to know which table is meant: always internal/charging''s, never internal/telemetry''s, for this column, in every era after RM31.), or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, mirroring charge_gaps.missing_charging_type''s identical convention.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metric_watermarks.source_updated_at
COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source_updated_at IS 'The maximum UpdatedAt (vehicle_snapshots/supercharger_sessions) or updated_at (manual_charge_entries) Reconcile has observed from this source for this vehicle, as of its last run. Reconcile queries each source''s ...UpdatedSince(source_updated_at - recalcOverlap) (design D4''s 24h commit-skew guard) and advances this column only when that query returns rows -- a source with zero returned rows on a given run leaves its own watermark row untouched (design D2''s "Reconcile idempotence contract").';

-- COMMENT: TABLE vehicle_metrics
-- +goose StatementBegin
COMMENT ON TABLE analytics.vehicle_metrics IS 'Precomputed daily read model for the analytics module (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). One row per (account_id, tesla_id, metric_date) for EVERY day that has a telemetry.Snapshot -- dense, mirroring vehicle_snapshots'' own grain, not a sparse subset of it (design D9, revised at the database design gate). Written exclusively by internal/analytics.Recalculator (Recalculate/Reconcile); read by internal/analytics.Reader (ConsumedByDay/OdometerDeltaByDay), both of which filter predecessor-less rows back out (design D13) to stay characterization-identical to the live-computed output this table replaces. Owned by internal/analytics; no other module reads this table directly.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.distance_traveled_km_calc
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.distance_traveled_km_calc IS 'Copied verbatim from telemetry.Snapshot.DistanceTraveledKmCalc (no re-derivation). NULL iff this row''s day has no locally-available predecessor snapshot -- the identical meaning NULL already carries on vehicle_snapshots. Raw and UNCLAMPED even when negative (a clock-skew/read anomaly); OdometerDeltaByDay applies math.Max(0, ...) on read, never on write (design D13).';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.battery_used_pct_calc
COMMENT ON COLUMN analytics.vehicle_metrics.battery_used_pct_calc IS 'Copied verbatim from telemetry.Snapshot.BatteryUsedPctCalc. NULL iff this row''s day has no predecessor. This is the canonical "has a predecessor" filter column used by both ConsumedByDay and OdometerDeltaByDay''s read-path IS NOT NULL predicate (design D13) -- either column would be an equivalent predicate (both are NULL under the identical condition, design D9), this one is picked because it is the column deriveConsumedByDay''s own existing D5a check already named.';

-- COMMENT: COLUMN vehicle_metrics.km_per_pct_calc
COMMENT ON COLUMN analytics.vehicle_metrics.km_per_pct_calc IS 'Copied verbatim from telemetry.Snapshot.KmPerPctCalc. NULL iff no predecessor OR battery_used_pct_calc <= 0 (the pre-existing, independent divisor guard -- unchanged from telemetry, not re-derived here).';

-- COMMENT: COLUMN vehicle_metrics.estimated_range_km_calc
COMMENT ON COLUMN analytics.vehicle_metrics.estimated_range_km_calc IS 'Copied verbatim from telemetry.Snapshot.EstimatedRangeKmCalc. NULL under the identical guard condition as km_per_pct_calc.';

-- COMMENT: COLUMN vehicle_metrics.days_spanned_calc
COMMENT ON COLUMN analytics.vehicle_metrics.days_spanned_calc IS 'Copied verbatim from telemetry.Snapshot.DaysSpannedCalc. NULL iff no predecessor for this day.';

-- COMMENT: COLUMN vehicle_metrics.consumed_pct
COMMENT ON COLUMN analytics.vehicle_metrics.consumed_pct IS 'The D13-corrected daily consumption figure (battery_used_pct_calc plus that day''s matched charge deltas across both sources). NULL under the identical "no predecessor" condition as battery_used_pct_calc -- there is no raw delta to correct on a predecessor-less day.';

-- COMMENT: COLUMN vehicle_metrics.flagged
COMMENT ON COLUMN analytics.vehicle_metrics.flagged IS 'NEVER NULL. false (not unknown) on a predecessor-less row: there is no gap-detection question to ask about a day with no computed consumption figure at all, so the D5/D5a flag comparison (ConsumedPct < 0, or ConsumedPct == 0 while DistanceKm exceeds the minimum flag distance) never runs for such a row -- it short-circuits to false BEFORE that comparison, on purpose. A stored 0 here would be actively wrong, not merely imprecise: a vehicle''s true first-ever tracked day is often also a normal driving day, so a stored 0 consumed_pct combined with real nonzero distance would evaluate the second D5a branch and falsely flag every vehicle''s first day as a suspected charge gap, in production, silently (design D9).';

-- COMMENT: COLUMN vehicle_metrics.missing_charging_type
COMMENT ON COLUMN analytics.vehicle_metrics.missing_charging_type IS 'Which charge source is suspected missing for a flagged day (mirrors charge_gaps'' identical column). NULL whenever flagged = false, including on a predecessor-less row -- the vehicle_metrics_missing_type_iff_flagged CHECK below enforces the pairing.';

-- COMMENT: COLUMN vehicle_metrics.locked
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.locked IS 'Copied verbatim from telemetry.Snapshot.Locked (no re-derivation). Always populated from the day''s own snapshot, independent of predecessor existence (design D3) -- unlike the five _calc columns, this is a raw per-day observation, not a delta. NULL means only "this row predates the RM38-analytics-add-vehicle-status-columns migration" -- no backfill was run (roadmap D2); it never means "not reported" (the source field is a plain bool, always populated once telemetry has a snapshot at all).';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.sentry_mode
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_metrics.sentry_mode IS 'Copied verbatim from telemetry.Snapshot.SentryMode (no re-derivation). Always populated from the day''s own snapshot, independent of predecessor existence (design D3). AMBIGUOUS NULL, unlike every other column added by this migration: NULL means EITHER "the vehicle did not report sentry" (SentryMode''s existing meaning on telemetry.Snapshot/vehicle_snapshots -- a *bool source field) OR "this row predates the RM38 migration" (roadmap D2, no backfill). Disambiguate via captured_at (also added by this migration): sentry_mode IS NULL AND captured_at IS NOT NULL means "not reported"; both NULL means "predates this migration" (design D8). No current consumer needs to disambiguate -- tier 2''s badge rendering (roadmap D5) treats NULL as "no badge" under either reading.';
-- +goose StatementEnd

-- COMMENT: COLUMN vehicle_metrics.car_version
COMMENT ON COLUMN analytics.vehicle_metrics.car_version IS 'Copied verbatim from telemetry.Snapshot.CarVersion. Always populated from the day''s own snapshot, independent of predecessor existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap D2) -- the source field is a plain string, never nil once telemetry has a snapshot.';

-- COMMENT: COLUMN vehicle_metrics.inside_temp_c
COMMENT ON COLUMN analytics.vehicle_metrics.inside_temp_c IS 'Copied verbatim from telemetry.Snapshot.InsideTempC (already Celsius -- Tesla reports temps in Celsius natively, no conversion at any layer). Always populated from the day''s own snapshot, independent of predecessor existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap D2).';

-- COMMENT: COLUMN vehicle_metrics.outside_temp_c
COMMENT ON COLUMN analytics.vehicle_metrics.outside_temp_c IS 'Copied verbatim from telemetry.Snapshot.OutsideTempC. Same always-populated and NULL semantics as inside_temp_c above. The ticket (MAG-12) misspelled this column "outsite_temp_c" -- the correct name, matching the _c display-unit suffix convention and the source field''s own name, is outside_temp_c (roadmap D1).';

-- COMMENT: COLUMN vehicle_metrics.charging_state
COMMENT ON COLUMN analytics.vehicle_metrics.charging_state IS 'Copied verbatim from telemetry.Snapshot.ChargingState. Always populated from the day''s own snapshot, independent of predecessor existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original five columns because the gateway''s dashSubtitle needs it for the card subtitle (roadmap D1).';

-- COMMENT: COLUMN vehicle_metrics.charge_limit_soc_pct
COMMENT ON COLUMN analytics.vehicle_metrics.charge_limit_soc_pct IS 'Copied verbatim from telemetry.Snapshot.ChargeLimitSocPct. Always populated from the day''s own snapshot, independent of predecessor existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original five columns for the Battery card''s "Limit N%" line (roadmap D1).';

-- COMMENT: COLUMN vehicle_metrics.captured_at
COMMENT ON COLUMN analytics.vehicle_metrics.captured_at IS 'Copied verbatim from telemetry.Snapshot.CapturedAt -- the exact capture instant, distinct from metric_date (this row''s own effective CALENDAR DAY, one day before CapturedAt''s own calendar day). Always populated from the day''s own snapshot, independent of predecessor existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original five columns for the gateway''s LastUpdated/ IsStale freshness badge (roadmap D1) -- also doubles as the disambiguation proxy for sentry_mode''s ambiguous NULL (design D8).';

-- COMMENT: COLUMN vehicle_metrics.max_range_charge_counter
COMMENT ON COLUMN analytics.vehicle_metrics.max_range_charge_counter IS 'Copied verbatim from telemetry.Snapshot.MaxRangeChargeCounter (no re-derivation, no conversion) -- the vehicle''s LIFETIME count of charges to its true 100% Maximum-Battery-Range limit, so it is monotonic across rows, not a per-day figure. Always populated from the day''s own snapshot, independent of predecessor existence, exactly like the eight columns added by 20260901000001. AMBIGUOUS NULL, like sentry_mode: NULL means EITHER "the vehicle did not report it / the snapshot predates telemetry migration 20260801000001" (the source field is itself a *int) OR "this vehicle_metrics row predates THIS migration" (no backfill was run). Disambiguate via captured_at: NULL counter with a non-NULL captured_at means "not reported". A real reported 0 is stored as 0, never as NULL -- telemetry pointer-wraps it (D12/DSA3), and 0 legitimately means "never charged to max range".';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fl_psi
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi IS 'Copied verbatim from telemetry.Snapshot.TpmsPressureFLPSI (no re-derivation, no conversion -- already PSI). A raw per-day observation, populated on EVERY row including a predecessor-less day, exactly like max_range_charge_counter and the eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means the vehicle did not report TPMS at capture, OR this row predates this migration and was not touched by the one-off backfill.';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fr_psi
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fr_psi IS 'Copied verbatim from telemetry.Snapshot.TpmsPressureFRPSI (no re-derivation, no conversion -- already PSI). A raw per-day observation, populated on EVERY row including a predecessor-less day, exactly like max_range_charge_counter and the eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means the vehicle did not report TPMS at capture, OR this row predates this migration and was not touched by the one-off backfill.';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rl_psi
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rl_psi IS 'Copied verbatim from telemetry.Snapshot.TpmsPressureRLPSI (no re-derivation, no conversion -- already PSI). A raw per-day observation, populated on EVERY row including a predecessor-less day, exactly like max_range_charge_counter and the eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means the vehicle did not report TPMS at capture, OR this row predates this migration and was not touched by the one-off backfill.';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rr_psi
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rr_psi IS 'Copied verbatim from telemetry.Snapshot.TpmsPressureRRPSI (no re-derivation, no conversion -- already PSI). A raw per-day observation, populated on EVERY row including a predecessor-less day, exactly like max_range_charge_counter and the eight RM38 status columns -- the opposite rule to the five _calc columns. NULL means the vehicle did not report TPMS at capture, OR this row predates this migration and was not touched by the one-off backfill.';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fl_psi_calc
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi_calc IS 'Derived delta: this row''s tpms_pressure_fl_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_fl_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison (RM50 roadmap RD3).';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_fr_psi_calc
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fr_psi_calc IS 'Derived delta: this row''s tpms_pressure_fr_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_fr_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison (RM50 roadmap RD3).';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rl_psi_calc
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rl_psi_calc IS 'Derived delta: this row''s tpms_pressure_rl_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_rl_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison (RM50 roadmap RD3).';

-- COMMENT: COLUMN vehicle_metrics.tpms_pressure_rr_psi_calc
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_rr_psi_calc IS 'Derived delta: this row''s tpms_pressure_rr_psi minus the previous day''s row for the same vehicle, in PSI. NULL when this day has no predecessor row, OR when either day''s own tpms_pressure_rr_psi reading is itself NULL -- never a fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees C) -- this is accepted, not a defect, and must never be "fixed" with a threshold or a target-pressure comparison (RM50 roadmap RD3).';

-- CONSTRAINT: charge_gaps charge_gaps_pkey
ALTER TABLE ONLY analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_pkey PRIMARY KEY (id);

-- CONSTRAINT: charge_gaps charge_gaps_tesla_date_unique
ALTER TABLE ONLY analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_tesla_date_unique UNIQUE (tesla_id, gap_date);

-- CONSTRAINT: vehicle_metric_watermarks vehicle_metric_watermarks_pkey
ALTER TABLE ONLY analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_pkey PRIMARY KEY (id);

-- CONSTRAINT: vehicle_metric_watermarks vehicle_metric_watermarks_tesla_source_unique
ALTER TABLE ONLY analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_tesla_source_unique UNIQUE (tesla_id, source);

-- CONSTRAINT: vehicle_metrics vehicle_metrics_pkey
ALTER TABLE ONLY analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_pkey PRIMARY KEY (id);

-- CONSTRAINT: vehicle_metrics vehicle_metrics_tesla_date_unique
ALTER TABLE ONLY analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_tesla_date_unique UNIQUE (tesla_id, metric_date);

-- INDEX: idx_vehicle_metrics_latest
CREATE INDEX idx_vehicle_metrics_latest ON analytics.vehicle_metrics USING btree (tesla_id, metric_date DESC);

-- +goose Down
-- +goose StatementBegin
-- Refusing is deliberate. Reversing this file would mean dropping the whole
-- analytics schema and every row in it. An empty rollback would be worse: goose
-- would mark the file un-applied while every object still exists, and the next
-- forward run would fail on "relation already exists" -- which stops the web and
-- poller containers, because both wait for the migration step to succeed.
DO $$
BEGIN
    RAISE EXCEPTION 'the analytics baseline is not reversible: recreate the database instead of rolling it back';
END
$$;
-- +goose StatementEnd
