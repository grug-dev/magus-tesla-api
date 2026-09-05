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
-- The eight RM38 status columns (locked, sentry_mode, car_version,
-- inside_temp_c, outside_temp_c, charging_state, charge_limit_soc_pct,
-- captured_at) are ordinary refreshed columns like every other non-
-- created_at column above -- copied verbatim from the day's own
-- telemetry.Snapshot on EVERY re-derivation, regardless of predecessor
-- existence (design D1/D3 of RM38-analytics-add-vehicle-status-columns).
-- max_range_charge_counter follows that same rule: another raw observation
-- copied verbatim from the day's own snapshot, refreshed on every
-- re-derivation, populated with or without a predecessor.
INSERT INTO analytics.vehicle_metrics (
    account_id, tesla_id, metric_date,
    battery_level_pct, odometer_km, battery_range_km,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc,
    consumed_pct, flagged, missing_charging_type,
    locked, sentry_mode, car_version, inside_temp_c, outside_temp_c,
    charging_state, charge_limit_soc_pct, captured_at,
    max_range_charge_counter
) VALUES (
    @account_id, @tesla_id, @metric_date,
    @battery_level_pct, @odometer_km, @battery_range_km,
    @distance_traveled_km_calc, @battery_used_pct_calc, @km_per_pct_calc,
    @estimated_range_km_calc, @days_spanned_calc,
    @consumed_pct, @flagged, @missing_charging_type,
    @locked, @sentry_mode, @car_version, @inside_temp_c, @outside_temp_c,
    @charging_state, @charge_limit_soc_pct, @captured_at,
    @max_range_charge_counter
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
    locked                     = EXCLUDED.locked,
    sentry_mode                = EXCLUDED.sentry_mode,
    car_version                = EXCLUDED.car_version,
    inside_temp_c               = EXCLUDED.inside_temp_c,
    outside_temp_c              = EXCLUDED.outside_temp_c,
    charging_state              = EXCLUDED.charging_state,
    charge_limit_soc_pct        = EXCLUDED.charge_limit_soc_pct,
    captured_at                 = EXCLUDED.captured_at,
    max_range_charge_counter    = EXCLUDED.max_range_charge_counter,
    updated_at                 = now();

-- name: LatestVehicleMetricsByAccount :many
-- Backs analytics.Reader.LatestMetricsByAccount (design D5/D6 of
-- RM38-analytics-add-vehicle-status-columns) -- the analytics-owned
-- equivalent of telemetry.Reader.LatestSnapshotsByAccount, mirroring its
-- exact DISTINCT ON shape: account_id equality narrows to one tenant's
-- rows, then (tesla_id, metric_date) lets Postgres pick the highest
-- metric_date row per tesla_id in one ordered index scan.
-- Served by idx_vehicle_metrics_latest (account_id, tesla_id,
-- metric_date DESC) -- added at the database design gate specifically to
-- match this query's ORDER BY exactly, eliminating the incremental sort
-- the existing all-ascending vehicle_metrics_account_tesla_date_unique
-- index would otherwise force (design.md "Index Plan", revised).
-- max_range_charge_counter joined the projection for the dashboard's
-- "100% Charges" tile: it is one more nullable raw observation on the same
-- latest row, so it adds a column to an existing read, not a second query --
-- and no index, since it appears in no WHERE/ORDER BY.
SELECT DISTINCT ON (tesla_id)
    tesla_id, battery_level_pct, battery_range_km, odometer_km,
    inside_temp_c, outside_temp_c, locked, sentry_mode, car_version,
    charging_state, charge_limit_soc_pct, captured_at,
    max_range_charge_counter
FROM analytics.vehicle_metrics
WHERE account_id = @account_id
ORDER BY tesla_id, metric_date DESC;

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
DELETE FROM analytics.vehicle_metrics
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
FROM analytics.vehicle_metrics
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
FROM analytics.vehicle_metrics
WHERE account_id  = @account_id
  AND tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
  AND distance_traveled_km_calc IS NOT NULL
ORDER BY metric_date;

-- name: VehicleMetricsBatteryByVehicleBetween :many
-- Backs analytics.Reader.BatteryLevelByDay
-- (RM40-analytics-add-battery-level-read design.md D2/D3). UNLIKE the two
-- filtered reads above (VehicleMetricsConsumedByVehicleBetween,
-- VehicleMetricsOdometerByVehicleBetween), this query carries NO trailing
-- "IS NOT NULL" predicate -- the one deliberate departure from the pattern
-- it otherwise mirrors exactly (design.md D3). battery_level_pct and
-- battery_range_km are declared NOT NULL: raw per-day observations copied
-- verbatim from that day's own telemetry.Snapshot, with no predecessor
-- requirement at all, unlike battery_used_pct_calc/
-- distance_traveled_km_calc which are genuinely NULL on a predecessor-less
-- day (design D9). Adding an IS NOT NULL filter here would silently exclude
-- a vehicle's first tracked day (or any day following a capture gap) from
-- the battery chart even though both values are fully known for that day --
-- a strictly worse answer, and on a NOT NULL column the filter could never
-- exclude a row anyway, so omitting it is not an oversight.
-- Served by vehicle_metrics_account_tesla_date_unique's own index (design.md
-- Index Plan #1) -- no separate CREATE INDEX; byte-identical index usage to
-- its two siblings, differing only in the absent residual predicate.
SELECT
    metric_date, battery_level_pct, battery_range_km
FROM analytics.vehicle_metrics
WHERE account_id  = @account_id
  AND tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
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
FROM analytics.vehicle_metric_watermarks
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
INSERT INTO analytics.vehicle_metric_watermarks (
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
INSERT INTO analytics.charge_gaps (
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
DELETE FROM analytics.charge_gaps
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
SELECT gap_date FROM analytics.charge_gaps
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND gap_date   >= @start
  AND gap_date   <= @end_date;
