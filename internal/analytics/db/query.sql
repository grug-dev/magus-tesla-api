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
-- vehicle_metrics_tesla_date_unique, refresh every column
-- Recalculate/deriveVehicleMetrics computed for this day, including the
-- nullable _calc columns and consumed_pct/flagged/missing_charging_type —
-- deriveVehicleMetrics may re-run over a revised prior day (D4's 24h
-- overlap) or the vehicle_snapshots row itself may have been replaced
-- (telemetry's own same-day UPSERT), so a stale value here must not survive
-- a re-derivation. created_at is DELIBERATELY ABSENT from the SET clause —
-- it must record when this (tesla_id, metric_date) was FIRST
-- written, not the most recent recompute, mirroring UpsertChargeGap's
-- identical convention (internal/telemetry/db/query.sql). tesla_id alone
-- names one vehicle uniquely, so it carries the row's full identity.
-- The eight RM38 status columns (locked, sentry_mode, car_version,
-- inside_temp_c, outside_temp_c, charging_state, charge_limit_soc_pct,
-- captured_at) are ordinary refreshed columns like every other non-
-- created_at column above -- copied verbatim from the day's own
-- telemetry.Snapshot on EVERY re-derivation, regardless of predecessor
-- existence (design D1/D3 of RM38-analytics-add-vehicle-status-columns).
-- max_range_charge_counter follows that same rule: another raw observation
-- copied verbatim from the day's own snapshot, refreshed on every
-- re-derivation, populated with or without a predecessor.
-- The four tpms_pressure_*_psi columns (RM50-analytics-add-tire-pressure-columns)
-- follow the identical rule: raw observations copied verbatim from the day's own
-- telemetry.Snapshot, refreshed on every re-derivation, populated with or without
-- a predecessor (design.md D2).
-- The four tpms_pressure_*_psi_delta_calc columns (renamed from
-- tpms_pressure_*_psi_calc so every day-over-day delta on this table shares
-- one naming convention) are DELTAS, not raw observations: refreshed on
-- every re-derivation like every other column above, but NULL whenever this
-- day has no predecessor at all, OR either day's own raw wheel reading is
-- itself NULL -- the same rule distance_traveled_km_calc and its siblings
-- follow, never the raw-TPMS rule the four columns above it follow.
-- distance_traveled_km_delta_calc, consumed_pct_delta_calc and
-- km_per_pct_delta_calc are the three newest deltas, following the
-- identical refresh-and-nullability rule: NULL whenever this day was the
-- first day a recalculation pass considered, or when either side of the
-- subtraction is itself NULL.
INSERT INTO analytics.vehicle_metrics (
    tesla_id, metric_date,
    battery_level_pct, odometer_km, battery_range_km,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    efficiency_range_100_pct_km_calc, days_spanned_calc,
    distance_traveled_km_delta_calc, consumed_pct_delta_calc, km_per_pct_delta_calc,
    consumed_pct, flagged, missing_charging_type,
    locked, sentry_mode, car_version, inside_temp_c, outside_temp_c,
    charging_state, charge_limit_soc_pct, captured_at,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    tpms_pressure_fl_psi_delta_calc, tpms_pressure_fr_psi_delta_calc, tpms_pressure_rl_psi_delta_calc, tpms_pressure_rr_psi_delta_calc
) VALUES (
    @tesla_id, @metric_date,
    @battery_level_pct, @odometer_km, @battery_range_km,
    @distance_traveled_km_calc, @battery_used_pct_calc, @km_per_pct_calc,
    @efficiency_range_100_pct_km_calc, @days_spanned_calc,
    @distance_traveled_km_delta_calc, @consumed_pct_delta_calc, @km_per_pct_delta_calc,
    @consumed_pct, @flagged, @missing_charging_type,
    @locked, @sentry_mode, @car_version, @inside_temp_c, @outside_temp_c,
    @charging_state, @charge_limit_soc_pct, @captured_at,
    @max_range_charge_counter,
    @tpms_pressure_fl_psi, @tpms_pressure_fr_psi, @tpms_pressure_rl_psi, @tpms_pressure_rr_psi,
    @tpms_pressure_fl_psi_delta_calc, @tpms_pressure_fr_psi_delta_calc, @tpms_pressure_rl_psi_delta_calc, @tpms_pressure_rr_psi_delta_calc
)
ON CONFLICT (tesla_id, metric_date) DO UPDATE SET
    battery_level_pct         = EXCLUDED.battery_level_pct,
    odometer_km                = EXCLUDED.odometer_km,
    battery_range_km           = EXCLUDED.battery_range_km,
    distance_traveled_km_calc  = EXCLUDED.distance_traveled_km_calc,
    battery_used_pct_calc      = EXCLUDED.battery_used_pct_calc,
    km_per_pct_calc             = EXCLUDED.km_per_pct_calc,
    efficiency_range_100_pct_km_calc     = EXCLUDED.efficiency_range_100_pct_km_calc,
    days_spanned_calc           = EXCLUDED.days_spanned_calc,
    distance_traveled_km_delta_calc = EXCLUDED.distance_traveled_km_delta_calc,
    consumed_pct_delta_calc          = EXCLUDED.consumed_pct_delta_calc,
    km_per_pct_delta_calc            = EXCLUDED.km_per_pct_delta_calc,
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
    tpms_pressure_fl_psi        = EXCLUDED.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi        = EXCLUDED.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi        = EXCLUDED.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi        = EXCLUDED.tpms_pressure_rr_psi,
    tpms_pressure_fl_psi_delta_calc   = EXCLUDED.tpms_pressure_fl_psi_delta_calc,
    tpms_pressure_fr_psi_delta_calc   = EXCLUDED.tpms_pressure_fr_psi_delta_calc,
    tpms_pressure_rl_psi_delta_calc   = EXCLUDED.tpms_pressure_rl_psi_delta_calc,
    tpms_pressure_rr_psi_delta_calc   = EXCLUDED.tpms_pressure_rr_psi_delta_calc,
    updated_at                 = now();

-- name: LatestVehicleMetricsByVehicles :many
-- Backs analytics.Reader.LatestMetricsForVehicles -- the analytics-owned
-- equivalent of telemetry.Reader.LatestSnapshotsByVehicles, mirroring its
-- exact DISTINCT ON shape: tesla_id membership narrows to the given vehicle
-- set, then (tesla_id, metric_date) lets Postgres pick the highest
-- metric_date row per tesla_id in one ordered index scan. The caller
-- already proved the requesting account owns every id in the set before
-- calling -- this query trusts the given identifiers and re-checks nothing.
-- Served by idx_vehicle_metrics_latest (tesla_id, metric_date DESC) --
-- its ORDER BY match eliminates the incremental sort an all-ascending
-- index would otherwise force. `= ANY(...)` on a small array still uses
-- this index one element at a time, in index order, so DISTINCT ON needs
-- no extra sort node.
-- max_range_charge_counter joined the projection for the dashboard's
-- "100% Charges" tile: it is one more nullable raw observation on the same
-- latest row, so it adds a column to an existing read, not a second query --
-- and no index, since it appears in no WHERE/ORDER BY.
-- RM50-analytics-add-tire-pressure-columns widens this SELECT by six more
-- columns: the four new tpms_pressure_*_psi raw observations, plus two
-- pre-existing columns (distance_traveled_km_calc, consumed_pct) gaining
-- their first consumer on this read port. All six are PROJECTED ONLY -- none
-- appears in a WHERE, JOIN, or ORDER BY -- so idx_vehicle_metrics_latest
-- still serves this query exactly as before; no index change (design.md D3).
-- A later widening renamed the four tpms_pressure_*_psi_calc columns to
-- tpms_pressure_*_psi_delta_calc and added three more day-over-day deltas
-- (distance_traveled_km_delta_calc, consumed_pct_delta_calc,
-- km_per_pct_delta_calc) to this SELECT. Same conclusion as above --
-- PROJECTED ONLY, never a WHERE/JOIN/ORDER BY predicate, so
-- idx_vehicle_metrics_latest still serves this query unchanged; no index
-- change.
SELECT DISTINCT ON (tesla_id)
    tesla_id, battery_level_pct, battery_range_km, odometer_km,
    inside_temp_c, outside_temp_c, locked, sentry_mode, car_version,
    charging_state, charge_limit_soc_pct, captured_at,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    distance_traveled_km_calc, consumed_pct, km_per_pct_calc,
    distance_traveled_km_delta_calc, consumed_pct_delta_calc, km_per_pct_delta_calc,
    tpms_pressure_fl_psi_delta_calc, tpms_pressure_fr_psi_delta_calc, tpms_pressure_rl_psi_delta_calc, tpms_pressure_rr_psi_delta_calc
FROM analytics.vehicle_metrics
WHERE tesla_id = ANY(@tesla_ids::bigint[])
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
-- Served by an index on (tesla_id, metric_date), never a seq scan -- no
-- separate CREATE INDEX. Two indexes lead on those columns and either can
-- serve this range: vehicle_metrics_tesla_date_unique ascending,
-- idx_vehicle_metrics_latest backward. Which one the planner picks is its
-- choice, not a contract.
DELETE FROM analytics.vehicle_metrics
WHERE tesla_id    = @tesla_id
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
-- Served by an index on (tesla_id, metric_date), never a seq scan -- no
-- separate CREATE INDEX. Two indexes lead on those columns and either can
-- serve this range: vehicle_metrics_tesla_date_unique ascending,
-- idx_vehicle_metrics_latest backward. Which one the planner picks is its
-- choice, not a contract. The IS NOT NULL clause is a residual predicate evaluated
-- against the already-tiny (<= historyRangeMaxDays = 90 row) range-scanned
-- result.
SELECT
    metric_date, consumed_pct, distance_traveled_km_calc, flagged,
    missing_charging_type, days_spanned_calc
FROM analytics.vehicle_metrics
WHERE tesla_id    = @tesla_id
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
WHERE tesla_id    = @tesla_id
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
-- Served by an index on (tesla_id, metric_date), never a seq scan -- no
-- separate CREATE INDEX. Two indexes lead on those columns and either can
-- serve this range: vehicle_metrics_tesla_date_unique ascending,
-- idx_vehicle_metrics_latest backward. Which one the planner picks is its
-- choice, not a contract; byte-identical index usage to its two siblings, differing
-- only in the absent residual predicate.
SELECT
    metric_date, battery_level_pct, battery_range_km
FROM analytics.vehicle_metrics
WHERE tesla_id    = @tesla_id
  AND metric_date BETWEEN @start_date AND @end_date
ORDER BY metric_date;

-- name: GetVehicleMetricWatermark :one
-- Single-row cursor lookup for one (tesla_id, source) -- tesla_id alone
-- names one vehicle uniquely. Returns pgx.ErrNoRows when no watermark
-- exists yet for this source, which
-- internal/analytics.Recalculator.Reconcile treats as "epoch": the source
-- has never been reconciled for this vehicle, so it backfills the vehicle's
-- full history in one pass. Served entirely by
-- vehicle_metric_watermarks_tesla_source_unique's own index -- a point
-- lookup, no separate CREATE INDEX.
SELECT source_updated_at
FROM analytics.vehicle_metric_watermarks
WHERE tesla_id   = @tesla_id
  AND source     = @source;

-- name: UpsertVehicleMetricWatermark :exec
-- Advance one source's cursor for one vehicle. Called by Reconcile only for
-- a source whose ...UpdatedSince query returned at least one row, advanced
-- to the max UpdatedAt/updated_at observed from that source on this run —
-- a source with zero returned rows leaves its watermark row untouched
-- (Reconcile's own idempotence contract). created_at is DELIBERATELY
-- ABSENT from the SET clause — it must record when this (tesla_id, source)
-- cursor was FIRST created, not the most recent advance, mirroring
-- UpsertChargeGap's identical convention.
INSERT INTO analytics.vehicle_metric_watermarks (
    tesla_id, source, source_updated_at
) VALUES (
    @tesla_id, @source, @source_updated_at
)
ON CONFLICT (tesla_id, source) DO UPDATE SET
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at         = now();

-- name: UpsertChargeGap :exec
-- Upsert one flagged vehicle-day. tesla_id already names one vehicle
-- uniquely, so the row's identity key is (tesla_id, gap_date) alone --
-- account_id added no isolation beyond that. On conflict with the
-- charge_gaps_tesla_date_unique constraint, refresh vin (in case the
-- vehicle's VIN changed since the day was first flagged -- cheap safety, not
-- an expected case) and missing_charging_type (the inferred type can change
-- between nightly runs if detection logic evolves, or if a Supercharger
-- session with NULL percentages later appears for a day previously inferred
-- MANUAL), and refresh updated_at to now(). created_at is DELIBERATELY
-- ABSENT from the SET clause -- it must record when this vehicle-day was
-- FIRST flagged, not the most recent confirmation.
INSERT INTO analytics.charge_gaps (
    tesla_id, vin, gap_date, missing_charging_type
) VALUES (
    @tesla_id, @vin, @gap_date, @missing_charging_type
)
ON CONFLICT (tesla_id, gap_date) DO UPDATE SET
    vin                    = EXCLUDED.vin,
    missing_charging_type  = EXCLUDED.missing_charging_type,
    updated_at             = now();

-- name: DeleteChargeGap :exec
-- Delete one charge_gaps row scoped to (tesla_id, gap_date) -- a point
-- delete served by the charge_gaps_tesla_date_unique constraint's own
-- index. Called by GapWriter.ReconcileWindow for every previously-stored
-- day in the window that is no longer present in the caller's
-- freshly-computed flagged set.
DELETE FROM analytics.charge_gaps
WHERE tesla_id = @tesla_id
  AND gap_date = @gap_date;

-- name: ChargeGapDatesByVehicleBetween :many
-- Return every stored charge_gaps date for one vehicle in the closed range
-- [start, end]. Used ONLY by GapWriter.ReconcileWindow's internal
-- bookkeeping to compute which previously-stored days are no longer in the
-- caller's flagged set (and so must be deleted) -- not a public read port,
-- not consumed outside this module's own write path. Single-column SELECT
-- (gap_date only): the caller already has every other field it needs for
-- any date it decides to keep (it is re-upserting from its own
-- freshly-computed flagged set, never reading this table's other columns
-- back).
--
-- Index reuse: served directly by charge_gaps_tesla_date_unique's own
-- (tesla_id, gap_date) index as a single contiguous forward range scan --
-- no new index.
SELECT gap_date FROM analytics.charge_gaps
WHERE tesla_id = @tesla_id
  AND gap_date >= @start
  AND gap_date <= @end_date;

-- name: VehicleMetricsForVehicleAndMonth :many
-- Backs MonthlySyncer.SyncMonth: the ONE query that fetches a month's
-- vehicle_metrics rows -- every subsequent figure is derived from this
-- slice in Go (monthly_figures.go), never in a second query. No IS NOT
-- NULL filter, unlike the daily Reader's own two filtered reads: this query
-- must see a predecessor-less row too, so the Go derivation can skip it and
-- still count it toward nothing, rather than the SQL silently hiding it.
-- period accepts any day inside the target month; date_trunc normalizes it
-- to the month's own bounds in SQL, matching CapacityForMonth's identical
-- any-day-in-month contract.
-- Served by an index on (tesla_id, metric_date), never a seq scan -- no
-- separate CREATE INDEX. Two indexes already lead on those columns
-- (vehicle_metrics_tesla_date_unique, idx_vehicle_metrics_latest); which one
-- the planner picks is its choice, not a contract.
SELECT
    metric_date, distance_traveled_km_calc, consumed_pct,
    battery_range_km, battery_level_pct
FROM analytics.vehicle_metrics
WHERE tesla_id = @tesla_id
  AND metric_date >= date_trunc('month', @period::date)::date
  AND metric_date <  (date_trunc('month', @period::date) + interval '1 month')::date
ORDER BY metric_date;

-- name: UpsertVehicleMonthlyMetric :one
-- Upsert one vehicle_monthly_metrics row. Always sets every column --
-- including the ext_*/sc_*/currency columns this version fills with their
-- documented zero value -- so a later change can widen what the Go side
-- computes without ever widening this statement's own column list.
-- period is normalized here, in SQL, from whatever day-in-month the caller
-- passed in -- the Go caller never constructs a first-of-month value
-- itself. RETURNING * hands the normalized period, and both timestamps,
-- straight back so the Go layer never recomputes what this statement just
-- decided.
-- created_at is DELIBERATELY ABSENT from the SET clause -- it must record
-- this (tesla_id, period)'s first sync, not its latest one, mirroring
-- UpsertVehicleMetric's identical convention on the daily table.
INSERT INTO analytics.vehicle_monthly_metrics (
    tesla_id, period,
    all_distance_km, all_consumed_pct, all_km_per_pct_calc, all_day_count,
    weekday_distance_km, weekday_consumed_pct, weekday_km_per_pct_calc, weekday_day_count,
    weekend_distance_km, weekend_consumed_pct, weekend_km_per_pct_calc, weekend_day_count,
    capacity_kwh, capacity_measured, currency,
    tesla_range_100_pct_km_calc,
    ext_ac_energy_kwh, ext_ac_cost, ext_ac_entry_count, ext_ac_ending_battery_dist,
    ext_dc_energy_kwh, ext_dc_cost, ext_dc_entry_count, ext_dc_ending_battery_dist,
    sc_energy_kwh, sc_cost, sc_session_count, sc_ending_battery_dist
) VALUES (
    @tesla_id, date_trunc('month', @period::date)::date,
    @all_distance_km, @all_consumed_pct, @all_km_per_pct_calc, @all_day_count,
    @weekday_distance_km, @weekday_consumed_pct, @weekday_km_per_pct_calc, @weekday_day_count,
    @weekend_distance_km, @weekend_consumed_pct, @weekend_km_per_pct_calc, @weekend_day_count,
    @capacity_kwh, @capacity_measured, @currency,
    @tesla_range_100_pct_km_calc,
    @ext_ac_energy_kwh, @ext_ac_cost, @ext_ac_entry_count, @ext_ac_ending_battery_dist,
    @ext_dc_energy_kwh, @ext_dc_cost, @ext_dc_entry_count, @ext_dc_ending_battery_dist,
    @sc_energy_kwh, @sc_cost, @sc_session_count, @sc_ending_battery_dist
)
ON CONFLICT (tesla_id, period) DO UPDATE SET
    all_distance_km             = EXCLUDED.all_distance_km,
    all_consumed_pct            = EXCLUDED.all_consumed_pct,
    all_km_per_pct_calc         = EXCLUDED.all_km_per_pct_calc,
    all_day_count               = EXCLUDED.all_day_count,
    weekday_distance_km         = EXCLUDED.weekday_distance_km,
    weekday_consumed_pct        = EXCLUDED.weekday_consumed_pct,
    weekday_km_per_pct_calc     = EXCLUDED.weekday_km_per_pct_calc,
    weekday_day_count           = EXCLUDED.weekday_day_count,
    weekend_distance_km         = EXCLUDED.weekend_distance_km,
    weekend_consumed_pct        = EXCLUDED.weekend_consumed_pct,
    weekend_km_per_pct_calc     = EXCLUDED.weekend_km_per_pct_calc,
    weekend_day_count           = EXCLUDED.weekend_day_count,
    capacity_kwh                = EXCLUDED.capacity_kwh,
    capacity_measured           = EXCLUDED.capacity_measured,
    currency                    = EXCLUDED.currency,
    tesla_range_100_pct_km_calc = EXCLUDED.tesla_range_100_pct_km_calc,
    ext_ac_energy_kwh           = EXCLUDED.ext_ac_energy_kwh,
    ext_ac_cost                 = EXCLUDED.ext_ac_cost,
    ext_ac_entry_count          = EXCLUDED.ext_ac_entry_count,
    ext_ac_ending_battery_dist  = EXCLUDED.ext_ac_ending_battery_dist,
    ext_dc_energy_kwh           = EXCLUDED.ext_dc_energy_kwh,
    ext_dc_cost                 = EXCLUDED.ext_dc_cost,
    ext_dc_entry_count          = EXCLUDED.ext_dc_entry_count,
    ext_dc_ending_battery_dist  = EXCLUDED.ext_dc_ending_battery_dist,
    sc_energy_kwh               = EXCLUDED.sc_energy_kwh,
    sc_cost                     = EXCLUDED.sc_cost,
    sc_session_count            = EXCLUDED.sc_session_count,
    sc_ending_battery_dist      = EXCLUDED.sc_ending_battery_dist,
    updated_at                  = now()
RETURNING *;

-- name: VehicleMonthlyMetricsForVehicleBetween :many
-- Backs MonthlyReader.MonthlyMetricsBetween: the stats page's ONE read over
-- the precomputed monthly table. SELECT * so sqlc returns the shared
-- VehicleMonthlyMetric model type, which monthlyMetricsFromRow already maps
-- -- a narrowed column list would emit a second, per-query Row type and force
-- a duplicate mapper for the same row.
-- start_period/end_period accept any day inside their month; date_trunc
-- normalizes both ends in SQL, matching VehicleMetricsForVehicleAndMonth's
-- and CapacityForMonth's identical any-day-in-month contract, so no caller
-- ever builds a first-of-month value itself.
-- Both bounds are inclusive of their whole month, matching the platform's
-- HTTP date-filter convention (end inclusive).
-- Served by vehicle_monthly_metrics_tesla_period_unique (tesla_id, period),
-- never a seq scan -- no separate CREATE INDEX.
SELECT * FROM analytics.vehicle_monthly_metrics
WHERE tesla_id = @tesla_id
  AND period >= date_trunc('month', @start_period::date)::date
  AND period <= date_trunc('month', @end_period::date)::date
ORDER BY period;
