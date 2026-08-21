-- +goose Up
-- vehicle_metrics: the analytics module's own precomputed daily read model --
-- one row per (account_id, tesla_id, metric_date), written by
-- internal/analytics.Recalculator (RM29-analytics-add-vehicle-metrics, MAG-26
-- tier 3, openspec/roadmaps/RM29-modular-monolith-boundaries.md). Replaces the
-- live, on-every-read derivation internal/analytics.ConsumedByDay used to run
-- over internal/telemetry/internal/charging data directly (roadmap D1:
-- "Analytics is a precomputed read model"). Owned by internal/analytics; no
-- other module may import the generated analyticsdb package
-- (ai/architecture.md §2).
--
-- DENSE TABLE, revised at the database design gate (owner's explicit,
-- one-item change to an otherwise-approved sparse design; design.md D9's
-- "Nullability" subsection carries the full rationale). A row is written for
-- EVERY calendar day that has a telemetry.Snapshot, whether or not that day
-- has a locally-available predecessor snapshot -- 1:1 with vehicle_snapshots'
-- own grain, mirroring that table's own nullability exactly. A sparse table
-- (no row at all for a predecessor-less day) would be safe for this tier's
-- two consumers (ConsumedByDay/OdometerDeltaByDay, both of which already
-- skip that day, design D5a) but a footgun for any FUTURE raw-observation
-- consumer re-pointed at this table per roadmap D1: a sparse table would make
-- a vehicle's first day silently vanish with no NULL to detect and no error.
-- The accepted cost: existing and new readers must explicitly handle NULL on
-- the five _calc columns and consumed_pct -- see design.md D13, enforced in
-- Go by internal/analytics.ConsumedByDay/OdometerDeltaByDay's own
-- "IS NOT NULL" filter (query.sql), not by this migration.
--
-- No FK on account_id/tesla_id: a cross-module FK from analytics into the
-- account module's tables would couple analytics migrations to the account
-- schema -- exactly the coupling ai/architecture.md §2 forbids. Referential
-- integrity is upheld by flow: the only writer (Recalculate, always called
-- with an (accountID, teslaID) pair already resolved from
-- account.RegisteredVehicles/AllRegisteredVehicles) never invents an identity
-- pair. Mirrors charge_gaps' and manual_charge_entries'/supercharger_sessions'
-- identical precedent (internal/telemetry/db/migrations/
-- 20260815000002_add_charge_gaps.sql:40-51).
--
-- No raw_data JSONB: this table stores a Go-computed conclusion
-- (internal/analytics' own derivation over already-stored telemetry/charging
-- data), not an external API response. The raw_data JSONB mandate
-- (ai/go-conventions.md §persistence) applies only to tables ingesting an
-- external API payload directly -- mirrors charge_gaps' and
-- manual_charge_entries' identical "no raw_data" precedent for their own
-- non-vendor data.
CREATE TABLE vehicle_metrics (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             UUID   NOT NULL,        -- multi-tenant scope
    tesla_id               BIGINT NOT NULL,        -- which of the account's vehicles
    metric_date            DATE   NOT NULL,        -- the row's own effective calendar day (never re-attributed, design D-B3/D12)

    -- Duplicated raw observations (roadmap D1 -- intentional, design D9).
    -- Always present, independent of whether this day has a predecessor --
    -- sourced verbatim from the day's own telemetry.Snapshot.
    battery_level_pct      INTEGER          NOT NULL,
    odometer_km            DOUBLE PRECISION NOT NULL,
    battery_range_km       DOUBLE PRECISION NOT NULL,

    -- The five columns tier 4 will drop from vehicle_snapshots, copied
    -- verbatim from telemetry.Snapshot's already-computed pointer fields --
    -- NO re-derivation happens here (design D9: telemetry already computes
    -- these at write time in service.go's deriveConsumption).
    -- NULLABLE, mirroring vehicle_snapshots' own nullability EXACTLY: NULL
    -- means "no predecessor exists for this row's day" -- the identical
    -- meaning NULL already carries on vehicle_snapshots today. A row exists
    -- for every day with a snapshot, whether or not it has a predecessor
    -- (dense -- not a sparse subset of vehicle_snapshots' grain).
    distance_traveled_km_calc DOUBLE PRECISION,  -- NULL iff no predecessor for this day
    battery_used_pct_calc     INTEGER,           -- NULL iff no predecessor for this day
    km_per_pct_calc            DOUBLE PRECISION, -- NULL iff no predecessor OR battery_used_pct_calc <= 0 (divisor guard, unchanged from telemetry)
    estimated_range_km_calc    DOUBLE PRECISION, -- NULL under the identical divisor-guard condition as km_per_pct_calc
    days_spanned_calc          INTEGER,          -- NULL iff no predecessor for this day

    -- D13-corrected consumption (DayConsumption's existing fields, now
    -- persisted instead of recomputed on every read).
    consumed_pct            DOUBLE PRECISION,     -- NULL iff no predecessor (there is no raw delta to correct)
    flagged                 BOOLEAN NOT NULL,     -- NEVER NULL: false (not unknown) on a predecessor-less row -- see column comment below
    missing_charging_type   TEXT CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER')),

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vehicle_metrics_account_tesla_date_unique
        UNIQUE (account_id, tesla_id, metric_date),
    CONSTRAINT vehicle_metrics_missing_type_iff_flagged CHECK (
        (flagged AND missing_charging_type IS NOT NULL) OR
        (NOT flagged AND missing_charging_type IS NULL)
    )
);

COMMENT ON TABLE vehicle_metrics IS
    'Precomputed daily read model for the analytics module (RM29-analytics-add-vehicle-metrics, '
    'MAG-26 tier 3). One row per (account_id, tesla_id, metric_date) for EVERY day that has a '
    'telemetry.Snapshot -- dense, mirroring vehicle_snapshots'' own grain, not a sparse subset of '
    'it (design D9, revised at the database design gate). Written exclusively by '
    'internal/analytics.Recalculator (Recalculate/Reconcile); read by internal/analytics.Reader '
    '(ConsumedByDay/OdometerDeltaByDay), both of which filter predecessor-less rows back out '
    '(design D13) to stay characterization-identical to the live-computed output this table '
    'replaces. Owned by internal/analytics; no other module reads this table directly.';

COMMENT ON COLUMN vehicle_metrics.distance_traveled_km_calc IS
    'Copied verbatim from telemetry.Snapshot.DistanceTraveledKmCalc (no re-derivation). NULL iff '
    'this row''s day has no locally-available predecessor snapshot -- the identical meaning NULL '
    'already carries on vehicle_snapshots. Raw and UNCLAMPED even when negative (a clock-skew/read '
    'anomaly); OdometerDeltaByDay applies math.Max(0, ...) on read, never on write (design D13).';
COMMENT ON COLUMN vehicle_metrics.battery_used_pct_calc IS
    'Copied verbatim from telemetry.Snapshot.BatteryUsedPctCalc. NULL iff this row''s day has no '
    'predecessor. This is the canonical "has a predecessor" filter column used by both '
    'ConsumedByDay and OdometerDeltaByDay''s read-path IS NOT NULL predicate (design D13) -- either '
    'column would be an equivalent predicate (both are NULL under the identical condition, design '
    'D9), this one is picked because it is the column deriveConsumedByDay''s own existing D5a check '
    'already named.';
COMMENT ON COLUMN vehicle_metrics.km_per_pct_calc IS
    'Copied verbatim from telemetry.Snapshot.KmPerPctCalc. NULL iff no predecessor OR '
    'battery_used_pct_calc <= 0 (the pre-existing, independent divisor guard -- unchanged from '
    'telemetry, not re-derived here).';
COMMENT ON COLUMN vehicle_metrics.estimated_range_km_calc IS
    'Copied verbatim from telemetry.Snapshot.EstimatedRangeKmCalc. NULL under the identical guard '
    'condition as km_per_pct_calc.';
COMMENT ON COLUMN vehicle_metrics.days_spanned_calc IS
    'Copied verbatim from telemetry.Snapshot.DaysSpannedCalc. NULL iff no predecessor for this day.';
COMMENT ON COLUMN vehicle_metrics.consumed_pct IS
    'The D13-corrected daily consumption figure (battery_used_pct_calc plus that day''s matched '
    'charge deltas across both sources). NULL under the identical "no predecessor" condition as '
    'battery_used_pct_calc -- there is no raw delta to correct on a predecessor-less day.';
COMMENT ON COLUMN vehicle_metrics.flagged IS
    'NEVER NULL. false (not unknown) on a predecessor-less row: there is no gap-detection question '
    'to ask about a day with no computed consumption figure at all, so the D5/D5a flag comparison '
    '(ConsumedPct < 0, or ConsumedPct == 0 while DistanceKm exceeds the minimum flag distance) '
    'never runs for such a row -- it short-circuits to false BEFORE that comparison, on purpose. '
    'A stored 0 here would be actively wrong, not merely imprecise: a vehicle''s true first-ever '
    'tracked day is often also a normal driving day, so a stored 0 consumed_pct combined with real '
    'nonzero distance would evaluate the second D5a branch and falsely flag every vehicle''s first '
    'day as a suspected charge gap, in production, silently (design D9).';
COMMENT ON COLUMN vehicle_metrics.missing_charging_type IS
    'Which charge source is suspected missing for a flagged day (mirrors charge_gaps'' identical '
    'column). NULL whenever flagged = false, including on a predecessor-less row -- the '
    'vehicle_metrics_missing_type_iff_flagged CHECK below enforces the pairing.';

-- Index Plan (design.md "Database Changes" > "Index Plan"): every read pattern
-- this tier declares is served by this table's own UNIQUE constraint index --
-- NO separate CREATE INDEX, mirroring charge_gaps' "Read path 1" reasoning
-- exactly (20260815000002_add_charge_gaps.sql:105-112).
--
--   1. Gateway history charts / analytics.Reader:
--      WHERE account_id = $1 AND tesla_id = $2 AND metric_date BETWEEN $3 AND $4
--        [AND <col> IS NOT NULL]   -- ConsumedByDay's/OdometerDeltaByDay's D13
--                                     filter, a residual predicate evaluated
--                                     against the already-tiny (<= 90 row)
--                                     range-scanned result -- no index of its own.
--      ORDER BY metric_date
--      -- served by vehicle_metrics_account_tesla_date_unique's own index:
--      -- account_id leads, satisfying this project's "account_id is the
--      -- leading index column" convention (ai/go-conventions.md
--      -- §Read optimization) for free.
--   2. Recalculate's UPSERT conflict target (account_id, tesla_id, metric_date)
--      -- the ON CONFLICT target IS the constraint's own index. Same index as #1.
--   3. Recalculate's DELETE-of-stale-rows scan,
--      WHERE account_id = $1 AND tesla_id = $2 AND metric_date BETWEEN $3 AND $4
--      -- same index as #1.
--
-- Deliberately NOT added: a dedicated (account_id, metric_date DESC) index for
-- a hypothetical account-wide, all-vehicles query (the pattern charge_gaps'
-- own idx_charge_gaps_account exists to serve for ITS future notification
-- consumer). No consumer in this tier issues that query -- both
-- OdometerDeltaByDay and ConsumedByDay are always called with a resolved
-- teslaID. Per the read-heavy Performance-Profile's own framing ("index
-- aggressively... never at the cost of...") the aggressive-indexing license is
-- bounded by DECLARED read patterns, not speculative ones -- an unused index
-- would slow every Recalculate/Reconcile UPSERT for no current benefit. Add
-- one in a later tier if an account-wide consumer appears, the same way
-- charge_gaps added its own for exactly that reason.

-- +goose Down
DROP TABLE IF EXISTS vehicle_metrics;
