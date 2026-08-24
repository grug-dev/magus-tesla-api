-- +goose Up
-- vehicle_metric_watermarks: one recompute cursor per (account_id, tesla_id,
-- source) -- the "how far has Reconcile already re-derived" bookkeeping for
-- internal/analytics.Recalculator.Reconcile (RM29-analytics-add-vehicle-metrics,
-- MAG-26 tier 3, design D2/D3/D4/D7). Owned by internal/analytics; no other
-- module may import the generated analyticsdb package (ai/architecture.md §2).
--
-- Lives in its own table rather than a MAX(updated_at) query over
-- vehicle_metrics (design D2, "Rejected" list): a MAX() over vehicle_metrics
-- has nothing to read before the first row is ever written for a vehicle (the
-- "zero rows yet" first-ever run), needs its own index, and a row later
-- deleted or recomputed away (e.g. its predecessor snapshot removed) would
-- silently rewind the cursor with no signal. This table stores one scalar
-- fact once, read by a single-row lookup on its own UNIQUE index.
--
-- Three independent sources, one cursor row each (design D3): 'vehicle_snapshots',
-- 'supercharger_sessions', 'manual_charge_entries' -- named after the physical
-- table each source's data lives in (self-describing, mirrors
-- charge_gaps.missing_charging_type's free-standing string-label convention;
-- no FK, just a closed-vocabulary label). manual_charge_entries is user-edited
-- at any hour; the other two move only at the nightly poll -- a shared cursor
-- would couple those very different clocks for no reason, so each source
-- advances independently and a future fourth source starts its own row at the
-- "no watermark yet" epoch (design D7) without rewinding the other three.
--
-- No FK on account_id/tesla_id, no raw_data JSONB: identical justification to
-- vehicle_metrics' own migration (20260821000001_add_vehicle_metrics.sql) --
-- a cursor is a Go-computed conclusion, not an external API response, and an
-- FK here would couple analytics migrations to the account schema
-- (ai/architecture.md §2) for a table that isn't even referencing anything
-- external -- source is a closed string label, not a reference.
CREATE TABLE vehicle_metric_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID   NOT NULL,        -- multi-tenant scope
    tesla_id          BIGINT NOT NULL,        -- which of the account's vehicles
    source            TEXT   NOT NULL CHECK (
        source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')
    ),
    source_updated_at TIMESTAMPTZ NOT NULL,   -- the max UpdatedAt/updated_at Reconcile has seen from this source, this vehicle

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique
        UNIQUE (account_id, tesla_id, source)
);

COMMENT ON TABLE vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly.';

COMMENT ON COLUMN vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/telemetry), '
    'or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, '
    'mirroring charge_gaps.missing_charging_type''s identical convention.';
COMMENT ON COLUMN vehicle_metric_watermarks.source_updated_at IS
    'The maximum UpdatedAt (vehicle_snapshots/supercharger_sessions) or updated_at '
    '(manual_charge_entries) Reconcile has observed from this source for this vehicle, as of its '
    'last run. Reconcile queries each source''s ...UpdatedSince(source_updated_at - '
    'recalcOverlap) (design D4''s 24h commit-skew guard) and advances this column only when that '
    'query returns rows -- a source with zero returned rows on a given run leaves its own '
    'watermark row untouched (design D2''s "Reconcile idempotence contract").';

-- Index Plan (design.md "Database Changes" > "Index Plan"): the sole read
-- pattern this table serves -- Reconcile's single-row watermark lookup,
-- WHERE account_id = $1 AND tesla_id = $2 AND source = $3 -- is served
-- entirely by vehicle_metric_watermarks_account_tesla_source_unique's own
-- index. No separate CREATE INDEX for this table.

-- +goose Down
DROP TABLE IF EXISTS vehicle_metric_watermarks;
