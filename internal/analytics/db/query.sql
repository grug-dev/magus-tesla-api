-- Queries for the analytics module. sqlc generates package `analyticsdb` from
-- these against the schema in migrations/. No other module may import
-- analyticsdb (module-scoped DB access — ai/architecture.md §2,
-- ai/go-conventions.md §persistence). This is the module's first-ever
-- persistence layer (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3);
-- both tables are written exclusively by internal/analytics.Recalculator and
-- read exclusively by internal/analytics.Reader — see design.md D9–D13 and
-- the "Database Changes" section for the full schema rationale.

-- name: UpsertVehicleMetric :exec
-- Upsert one vehicle_metrics row (design D11). On conflict with
-- vehicle_metrics_account_tesla_date_unique, refresh every column
-- Recalculate/deriveVehicleMetrics computed for this day, including the
-- nullable _calc columns and consumed_pct/flagged/missing_charging_type —
-- deriveVehicleMetrics may re-run over a revised prior day (D4's 24h
-- overlap) or the vehicle_snapshots row itself may have been replaced
-- (telemetry's own same-day UPSERT), so a stale value here must not survive
-- a re-derivation. created_at is DELIBERATELY ABSENT from the SET clause —
-- it must record when this (account_id, tesla_id, metric_date) was FIRST
-- written, not the most recent recompute, mirroring UpsertChargeGap's
-- identical convention (internal/telemetry/db/query.sql).
INSERT INTO vehicle_metrics (
    account_id, tesla_id, metric_date,
    battery_level_pct, odometer_km, battery_range_km,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc,
    consumed_pct, flagged, missing_charging_type
) VALUES (
    @account_id, @tesla_id, @metric_date,
    @battery_level_pct, @odometer_km, @battery_range_km,
    @distance_traveled_km_calc, @battery_used_pct_calc, @km_per_pct_calc,
    @estimated_range_km_calc, @days_spanned_calc,
    @consumed_pct, @flagged, @missing_charging_type
)
ON CONFLICT (account_id, tesla_id, metric_date) DO UPDATE SET
    battery_level_pct         = EXCLUDED.battery_level_pct,
    odometer_km                = EXCLUDED.odometer_km,
    battery_range_km           = EXCLUDED.battery_range_km,
    distance_traveled_km_calc  = EXCLUDED.distance_traveled_km_calc,
    battery_used_pct_calc      = EXCLUDED.battery_used_pct_calc,
    km_per_pct_calc             = EXCLUDED.km_per_pct_calc,
    estimated_range_km_calc     = EXCLUDED.estimated_range_km_calc,
    days_spanned_calc           = EXCLUDED.days_spanned_calc,
    consumed_pct               = EXCLUDED.consumed_pct,
    flagged                    = EXCLUDED.flagged,
    missing_charging_type      = EXCLUDED.missing_charging_type,
    updated_at                 = now();

-- name: DeleteVehicleMetricsInRangeExcept :exec
-- Delete every vehicle_metrics row in [start, end] for this vehicle whose
-- metric_date is NOT among the dates Recalculate just produced (design D11).
-- Under the dense-table revision, "stale" means "no snapshot exists for that
-- day at all anymore" (e.g. a backfill correction removed a
-- vehicle_snapshots row) — a narrower, simpler condition than the pre-revision
-- sparse design needed, since a computable-or-not row is now produced for
-- every day that still has a snapshot.
-- An empty metric_dates array (Recalculate produced no rows at all for the
-- window, e.g. every snapshot in range was itself removed) correctly deletes
-- every existing row in range: `!= ALL('{}')` is true for every row, since
-- there are no elements to compare against.
-- Served by vehicle_metrics_account_tesla_date_unique's own index (Index Plan,
-- read pattern #3) — no separate CREATE INDEX.
DELETE FROM vehicle_metrics
WHERE account_id  = @account_id
  AND tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
  AND metric_date != ALL(@metric_dates::date[]);

-- name: VehicleMetricsConsumedByVehicleBetween :many
-- Backs analytics.Reader.ConsumedByDay (design D13). ONE OF TWO SEPARATE
-- filtered reads — deliberately NOT a shared unfiltered SELECT with
-- OdometerDeltaByVehicleBetween below. The trailing
-- "battery_used_pct_calc IS NOT NULL" excludes every predecessor-less row
-- (design D9) so this method's output stays characterization-identical to
-- today's live deriveConsumedByDay, which never emitted such a row either
-- (D5a). The filter column is battery_used_pct_calc, not consumed_pct —
-- both are NULL under the identical condition (D9), so either is an
-- equivalent predicate; this one is picked because it is the column
-- deriveConsumedByDay's own existing D5a check already named. Every row
-- that passes this filter is guaranteed (D9's "co-occur" guarantee) to also
-- have non-NULL distance_traveled_km_calc/days_spanned_calc, so the Go
-- mapping in reader.go needs no nil-check and no fallback-to-1 branch for
-- either.
-- Served by vehicle_metrics_account_tesla_date_unique's own index (Index
-- Plan, read pattern #1) — no separate CREATE INDEX. The IS NOT NULL clause
-- is a residual predicate evaluated against the already-tiny (<=
-- historyRangeMaxDays = 90 row) range-scanned result.
SELECT
    metric_date, consumed_pct, distance_traveled_km_calc, flagged,
    missing_charging_type, days_spanned_calc
FROM vehicle_metrics
WHERE account_id  = @account_id
  AND tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
  AND battery_used_pct_calc IS NOT NULL
ORDER BY metric_date;

-- name: VehicleMetricsOdometerByVehicleBetween :many
-- Backs analytics.Reader.OdometerDeltaByDay (design D13). The SECOND of the
-- two separate filtered reads (see VehicleMetricsConsumedByVehicleBetween
-- above for why these are not merged into one unfiltered query). The
-- trailing "distance_traveled_km_calc IS NOT NULL" excludes every
-- predecessor-less row for the same reason. OdometerDeltaByDay's
-- roadmap-D5 clamp (math.Max(0, ...)) is applied in Go, on this
-- already-guaranteed-non-NULL value — never in this query, and never
-- against a NULL (the filter guarantees that). The stored column itself
-- stays the raw, unclamped value.
-- Served by the identical index and residual-predicate reasoning as
-- VehicleMetricsConsumedByVehicleBetween above.
SELECT
    metric_date, odometer_km, distance_traveled_km_calc
FROM vehicle_metrics
WHERE account_id  = @account_id
  AND tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
  AND distance_traveled_km_calc IS NOT NULL
ORDER BY metric_date;

-- name: GetVehicleMetricWatermark :one
-- Single-row cursor lookup for one (account_id, tesla_id, source) — design
-- D2/D3. Returns pgx.ErrNoRows when no watermark exists yet for this source,
-- which internal/analytics.Recalculator.Reconcile treats as "epoch": the
-- source has never been reconciled for this vehicle, so it backfills the
-- vehicle's full history in one pass (design D7). Served entirely by
-- vehicle_metric_watermarks_account_tesla_source_unique's own index (Index
-- Plan, read pattern #4) — no separate CREATE INDEX.
SELECT source_updated_at
FROM vehicle_metric_watermarks
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND source     = @source;

-- name: UpsertVehicleMetricWatermark :exec
-- Advance one source's cursor for one vehicle (design D2/D3/D4). Called by
-- Reconcile only for a source whose ...UpdatedSince query returned at least
-- one row, advanced to the max UpdatedAt/updated_at observed from that
-- source on this run — a source with zero returned rows leaves its
-- watermark row untouched (Reconcile's own idempotence contract,
-- design.md's Test Contract). created_at is DELIBERATELY ABSENT from the SET
-- clause — it must record when this (account_id, tesla_id, source) cursor
-- was FIRST created, not the most recent advance, mirroring
-- UpsertChargeGap's identical convention.
INSERT INTO vehicle_metric_watermarks (
    account_id, tesla_id, source, source_updated_at
) VALUES (
    @account_id, @tesla_id, @source, @source_updated_at
)
ON CONFLICT (account_id, tesla_id, source) DO UPDATE SET
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at         = now();

-- name: UpsertChargeGap :exec
-- Upsert one flagged vehicle-day. On conflict with the
-- charge_gaps_account_tesla_date_unique constraint, refresh vin (in case the
-- vehicle's VIN changed since the day was first flagged -- cheap safety, not
-- an expected case) and missing_charging_type (the inferred type can change
-- between nightly runs if detection logic evolves, or if a Supercharger
-- session with NULL percentages later appears for a day previously inferred
-- MANUAL), and refresh updated_at to now(). created_at is DELIBERATELY
-- ABSENT from the SET clause -- design D-Table2/the table's own column
-- comment: it must record when this vehicle-day was FIRST flagged, not the
-- most recent confirmation.
INSERT INTO charge_gaps (
    account_id, tesla_id, vin, gap_date, missing_charging_type
) VALUES (
    @account_id, @tesla_id, @vin, @gap_date, @missing_charging_type
)
ON CONFLICT (account_id, tesla_id, gap_date) DO UPDATE SET
    vin                    = EXCLUDED.vin,
    missing_charging_type  = EXCLUDED.missing_charging_type,
    updated_at             = now();

-- name: DeleteChargeGap :exec
-- Delete one charge_gaps row scoped to (account_id, tesla_id, gap_date) -- a
-- point delete served by the charge_gaps_account_tesla_date_unique
-- constraint's own index (design.md Index Plan, Read path 1). Called by
-- GapWriter.ReconcileWindow for every previously-stored day in the window
-- that is no longer present in the caller's freshly-computed flagged set
-- (roadmap D7b).
DELETE FROM charge_gaps
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND gap_date   = @gap_date;

-- name: ChargeGapDatesByVehicleBetween :many
-- Return every stored charge_gaps date for one vehicle (within one account)
-- in the closed range [start, end]. Used ONLY by
-- GapWriter.ReconcileWindow's internal bookkeeping to compute which
-- previously-stored days are no longer in the caller's flagged set (and so
-- must be deleted) -- not a public read port, not consumed outside this
-- module's own write path. Single-column SELECT (gap_date only): the caller
-- already has every other field it needs for any date it decides to keep
-- (it is re-upserting from its own freshly-computed flagged set, never
-- reading this table's other columns back).
--
-- Index reuse (design.md Index Plan, Read path 1): served directly by
-- charge_gaps_account_tesla_date_unique's own (account_id, tesla_id, gap_date)
-- index as a single contiguous forward range scan -- no new index.
SELECT gap_date FROM charge_gaps
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND gap_date   >= @start
  AND gap_date   <= @end_date;
