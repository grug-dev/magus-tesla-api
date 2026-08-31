-- Queries for the telemetry module. sqlc generates package `telemetrydb` from
-- these against the schema in migrations/. No other module may import telemetrydb
-- (module-scoped DB access — ai/architecture.md §2, ai/go-conventions.md
-- §persistence). All writes are append-only inserts (no UPDATE/DELETE): the two
-- tables are immutable history.

-- name: InsertVehicleSnapshot :exec
-- Upsert one snapshot: inserts a new row, or REPLACES the existing row for
-- the same (account_id, tesla_id, captured_date) if one already exists — the
-- newest capture for a calendar day always wins (design D1 of
-- telemetry-dedupe-daily-snapshots, which SUPERSEDES the table's prior
-- append-only invariant — migration 20260710000002 design D1 of
-- RM1-telemetry-add-nightly-snapshots). captured_date is Go-computed
-- (snapshotFrom/clock.CalendarDay, service.go) from captured_at in the poller's
-- configured timezone (design D2) — never a DB expression, because a UNIQUE
-- index cannot depend on the runtime POLLER_TIMEZONE env var.
-- Distance/range columns store DISPLAY units (km), converted exactly once at
-- capture time by calling the tesla adapter's Km() companions — never derived
-- on read (telemetry-store-display-units design D1/D3, RM7 Decision 1).
-- sentry_mode is bound as a nullable boolean (nil = vehicle did not report
-- sentry) so absent stays distinct from a reported off.
-- Source A (RM2-telemetry-add-charging-stats): the 5 charge-enrichment columns are
-- always non-NULL for rows written after the 20260716000002 migration — snapshotFrom
-- stores the actual DTO value pointer-wrapped (D12: no zero-is-absent heuristic).
-- NULL is reserved for pre-migration rows only; see design DSA1/DSA3.
-- max_range_charge_counter: nullable int, lifetime count of charges to max-range.
-- NULL for rows written before 20260801000001 migration (pre-extraction). A real 0
-- is stored as non-NULL via pointer-wrap in snapshotFrom (D12/DSA3 convention).
-- tpms_pressure_{fl,fr,rl,rr}_psi: nullable REAL, tire pressure in PSI — converted
-- exactly once at capture time from the Fleet API's native bar reading by calling
-- the tesla adapter's TpmsPressure*PSI() companions (telemetry-store-display-units
-- design D1/D3). NULL for rows written before 20260802000001 migration
-- (pre-extraction) or when the vehicle did not report TPMS. A 0.0 PSI is stored
-- non-NULL (D12/DSA3 convention). No new index: tpms columns ride along on the
-- existing heap row fetch.
-- latitude/longitude/fast_charger_type dropped in 20260801000001 — lossless in raw_data.
-- updated_at is NOT sent as a param: DEFAULT now() handles a fresh INSERT;
-- the ON CONFLICT clause explicitly refreshes it to now() on a same-day
-- replace (design D5), mirroring UpsertSuperchargerSession's own
-- `updated_at = now()`.
-- The five derived-consumption columns (distance_traveled_km_calc,
-- battery_used_pct_calc, km_per_pct_calc, estimated_range_km_calc,
-- days_spanned_calc) that used to be bound here were DROPPED by migration
-- 20260822000001 (RM29-telemetry-drop-derived-columns tier 4, MAG-26): the
-- derivation moved to internal/analytics, which computes the same figures
-- from this table's surviving raw columns (odometer_km, battery_level_pct,
-- captured_date). Nothing inside telemetry ever read them back.
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level_pct, @battery_range_km, @charging_state, @charge_limit_soc_pct,
    @odometer_km, @inside_temp_c, @outside_temp_c, @locked, @sentry_mode,
    @car_version,
    @charge_energy_added_kwh, @charger_power_kw, @charger_voltage_v,
    @charger_actual_current_a, @usable_battery_level_pct,
    @max_range_charge_counter,
    @tpms_pressure_fl_psi, @tpms_pressure_fr_psi, @tpms_pressure_rl_psi, @tpms_pressure_rr_psi,
    @captured_date
)
ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE SET
    captured_at               = EXCLUDED.captured_at,
    raw_data                  = EXCLUDED.raw_data,
    battery_level_pct         = EXCLUDED.battery_level_pct,
    battery_range_km          = EXCLUDED.battery_range_km,
    charging_state            = EXCLUDED.charging_state,
    charge_limit_soc_pct      = EXCLUDED.charge_limit_soc_pct,
    odometer_km               = EXCLUDED.odometer_km,
    inside_temp_c              = EXCLUDED.inside_temp_c,
    outside_temp_c             = EXCLUDED.outside_temp_c,
    locked                     = EXCLUDED.locked,
    sentry_mode                = EXCLUDED.sentry_mode,
    car_version                = EXCLUDED.car_version,
    charge_energy_added_kwh    = EXCLUDED.charge_energy_added_kwh,
    charger_power_kw           = EXCLUDED.charger_power_kw,
    charger_voltage_v          = EXCLUDED.charger_voltage_v,
    charger_actual_current_a   = EXCLUDED.charger_actual_current_a,
    usable_battery_level_pct   = EXCLUDED.usable_battery_level_pct,
    max_range_charge_counter   = EXCLUDED.max_range_charge_counter,
    tpms_pressure_fl_psi       = EXCLUDED.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi       = EXCLUDED.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi       = EXCLUDED.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi       = EXCLUDED.tpms_pressure_rr_psi,
    updated_at                 = now();

-- name: InsertPollAttempt :exec
-- Record one attempt per (vehicle, run), success or failure. outcome is
-- success|failure; reason is ok|asleep-timeout|unauthorized|api-error. run_id
-- correlates every vehicle's row from one app.ProcessVehicleData invocation;
-- triggered_by records what triggered that invocation (RM29-app-add-process-
-- vehicle-data design D5/D7).
INSERT INTO poll_attempts (
    account_id, tesla_id, attempted_at, outcome, reason, run_id, triggered_by
) VALUES (
    @account_id, @tesla_id, @attempted_at, @outcome, @reason, @run_id, @triggered_by
);

-- name: ListSnapshotsByVehicle :many
-- Read helper for the DATABASE_URL-gated store tests: every snapshot for one
-- vehicle, newest first. Not consumed by another module (module-scoped).
-- Explicit column list (no SELECT *) so sqlc generates a stable struct even when
-- schema evolves; latitude/longitude/fast_charger_type removed in 20260801000001.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id AND tesla_id = @tesla_id
ORDER BY captured_at DESC;

-- name: ListPollAttemptsByVehicle :many
-- Read helper for the DATABASE_URL-gated store tests: every attempt for one
-- vehicle, newest first. Not consumed by another module (module-scoped).
SELECT * FROM poll_attempts
WHERE account_id = @account_id AND tesla_id = @tesla_id
ORDER BY attempted_at DESC;

-- name: SnapshotsByVehicleSince :many
-- Return all snapshots for a single vehicle (within the given account) captured at or
-- after `since`, ordered oldest-first. Used by telemetry.Reader.SnapshotsByVehicleSince
-- to power the odometer/battery history charts (RM5 tier 1).
--
-- Index reuse (D3): the existing idx_vehicle_snapshots_vehicle_time
-- (account_id, tesla_id, captured_at) is an ASCENDING index. The query's
-- (account_id = $1 AND tesla_id = $2 AND captured_at >= $3 ORDER BY captured_at ASC)
-- is a forward range scan: the planner seeks to (account_id, tesla_id, since) and
-- reads forward in index order, satisfying both WHERE and ORDER BY with no sort step.
--
-- LIMIT 400 (D4): safety cap against an accidentally large result set if capture
-- cadence ever increases. A 30-day window returns ~30 rows under the current nightly
-- schedule — 400 comfortably exceeds any realistic dashboard window (~13 months).
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at >= @since
ORDER BY captured_at ASC
LIMIT 400;

-- name: SnapshotsByVehicleBetween :many
-- Return the snapshots for a single vehicle (within the given account) whose
-- **EffectiveDate calendar day** falls in the caller-supplied `[start, end]` window
-- inclusive, ordered oldest-first (ascending by captured_at == ascending by
-- EffectiveDate, since EffectiveDate is monotonic in CapturedAt). Used by
-- telemetry.Reader.SnapshotsByVehicleBetween to power the bounded history charts
-- (RM8 tier 1, MAG-7 date filters). `start` and `end` are whole UTC-midnight-bounded
-- calendar days; `end` is inclusive.
--
-- Bounds derivation (design D1/D5): EffectiveDate = CapturedAt.AddDate(0,0,-1), i.e.
-- EffectiveDate's UTC calendar day == CapturedAt's UTC calendar day minus 1. So
-- `EffectiveDate ∈ [start, end]` inclusive ⟺ `CapturedAt ∈ [start+1 day, end+1 day]`
-- (calendar days, inclusive both ends). Expressed as TIMESTAMPTZ predicates this is
-- the half-open range `[start_bound, end_bound)`:
--     start_bound = start + 1 calendar day   (start.AddDate(0,0,1)  — UTC midnight beginning the first eligible capture day)
--     end_bound   = end   + 2 calendar days  (end  .AddDate(0,0,2)  — UTC midnight ending the last eligible capture day, exclusive)
-- The half-open upper bound makes `end` inclusive without an off-by-one on a
-- UTC-midnight `end` instant (design D3).
-- The translation `(start, end) → (start_bound, end_bound)` lives INSIDE the dbStore
-- implementation (service.go), NOT in the public Reader method (design D5): the
-- reader/store seam passes the caller's raw `(start, end)` through unchanged, and the
-- dbStore computes and binds the two bounds as pgtype.Timestamptz here.
--
-- Why captured_at (TIMESTAMPTZ) and NOT captured_date (DATE) (design D1): captured_at
-- is the UTC capture instant EffectiveDate is derived from (rowToSnapshot:
-- CapturedAt.Time.AddDate(0,0,-1)), so the query and the mapper are consistent by
-- construction with zero timezone coupling. captured_date is Go-computed in the
-- poller's configured timezone (POLLER_TIMEZONE, currently America/Bogota, UTC−5) and
-- used solely for the (account_id, tesla_id, captured_date) dedupe UNIQUE constraint
-- (telemetry-dedupe-daily-snapshots design D2). Filtering on captured_date would
-- couple this port's correctness to the poller's timezone and break quietly the moment
-- a capture straddles UTC midnight or POLLER_TIMEZONE changes — the exact fragility
-- the dedupe change's D2 rejected. (The implicit (account_id, tesla_id, captured_date)
-- unique index would *serve* such a query, but semantic correctness disqualifies it.)
--
-- Index reuse (D2): the existing idx_vehicle_snapshots_vehicle_time
-- (account_id, tesla_id, captured_at) is an ASCENDING index. The query's
-- (account_id = $1 AND tesla_id = $2 AND captured_at >= $3 AND captured_at < $4
-- ORDER BY captured_at ASC) is a forward range scan: the planner seeks to
-- (account_id, tesla_id, start_bound) and reads forward in index order, satisfying
-- both WHERE and ORDER BY with no sort step; the upper bound `captured_at < end_bound`
-- prunes the scan in-place. This is the identical access pattern SnapshotsByVehicleSince
-- uses, with one extra range predicate — strictly cheaper than the open Since scan for
-- the same dashboard window. No new index, no migration, no new column (design D2 —
-- the `database` design-gate is NOT triggered).
--
-- LIMIT 4000 (D3, raised from 400 by RM29-telemetry-drop-derived-columns tier 4,
-- task 4.9): the original 400 was sized for the gateway's bounded ~91-row HTTP
-- window (Decision #2/#4), but this same query also backs internal/analytics'
-- Reconcile epoch backfill (roadmap D7/D8, RM29-analytics-add-vehicle-metrics),
-- which — since RM29 tier 4's watermark reset (design D3, I3) — can call
-- Recalculate over a SINGLE vehicle's ENTIRE snapshot history in one window. At
-- 400, a vehicle with more than 400 days of history had its NEWEST rows
-- silently dropped (this query is `ORDER BY captured_at ASC LIMIT 400`), and
-- Reconcile then advanced the watermark past days it never recomputed —
-- permanently wrong metrics, no error, no log line. That failure mode is
-- exactly what this port's own SnapshotPrecedingDay (above) exists to prevent
-- for the *predecessor* side; this raise closes the matching gap on the
-- *forward window* side that the watermark reset re-arms. 4000 is ~11 years at
-- the platform's one-snapshot-per-vehicle-per-day cadence — the cap remains a
-- runaway-query guard (all D3 ever claimed for it), and the bounded [start,
-- end] window supplied by the caller stays the real protection, not the LIMIT.
-- Truncation detection or pagination for a vehicle exceeding 4000 days is
-- explicitly out of scope (design.md Risks; do not add it here).
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at >= @start_bound
  AND captured_at <  @end_bound
ORDER BY captured_at ASC
LIMIT 4000;

-- name: SnapshotsByVehicleUpdatedSince :many
-- Return every snapshot for a single vehicle (within the given account) whose
-- updated_at is at or after `since`, ordered oldest-first by updated_at. Used by
-- telemetry.Reader.SnapshotsByVehicleUpdatedSince to let internal/analytics'
-- Recalculator (RM29-analytics-add-vehicle-metrics) detect which snapshots
-- changed recently -- including a same-day REPLACE via the existing UPSERT
-- (design D1 of telemetry-dedupe-daily-snapshots), which advances updated_at
-- without necessarily changing captured_at's calendar day.
--
-- Index reuse: the existing idx_vehicle_snapshots_vehicle_time
-- (account_id, tesla_id, captured_at) is NOT sorted on updated_at, so this
-- query cannot use it as a pure ORDER BY-satisfying range scan the way
-- SnapshotsByVehicleSince does on captured_at. It STILL prunes the scan to
-- this one vehicle's rows via the index's (account_id, tesla_id) leading-
-- column prefix before the updated_at predicate and sort are applied --
-- updated_at is a residual filter within that scan, per this change's
-- explicit design call (no new index; verified via EXPLAIN in the
-- DB-integration test, RM29-analytics-add-vehicle-metrics Wave 6).
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND updated_at >= @since
ORDER BY updated_at ASC;

-- name: LatestSnapshotsByAccount :many
-- Return the latest stored snapshot for each vehicle owned by the given account.
-- DISTINCT ON (tesla_id) with ORDER BY tesla_id, captured_at DESC picks the row
-- with the highest captured_at per tesla_id — one Postgres index scan, no N+1.
-- The existing (account_id, tesla_id, captured_at) index covers this query: the
-- planner satisfies the WHERE and ORDER BY in a single efficient range scan.
-- This is the batch read for the dashboard (tier 5, gateway-read-stored-vehicles);
-- it avoids the N+1 that would result from calling ListSnapshotsByVehicle per vehicle.
SELECT DISTINCT ON (tesla_id)
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id
ORDER BY tesla_id, captured_at DESC;

-- name: SnapshotPrecedingDay :one
-- Return the single most recent snapshot for a vehicle whose captured_date is
-- strictly before the given calendar day, or pgx.ErrNoRows when none exists
-- (the vehicle's first-ever snapshot). Backs telemetry.Reader.SnapshotPrecedingDay,
-- whose only consumer is internal/analytics' Recalculate: it needs the EXACT
-- predecessor, however old, because a capture gap longer than its fetch window
-- would otherwise yield a silently wrong (or silently absent) daily delta.
-- REPLACES PreviousSnapshotForVehicle (deleted by RM29-telemetry-drop-derived-columns
-- tier 4, design D8): same table, same index strategy, same LIMIT 1; only the
-- bound moved from an instant to a calendar day.
--
-- The bound is captured_date, NOT captured_at: captured_date is already the
-- poller-zone calendar day (stamped once on the write path by clock.CalendarDay), so the
-- predicate is zone-free at query time. It is exactly equivalent to the
-- captured_at < dayStart(cur.captured_at, loc) bound the deleted
-- PreviousSnapshotForVehicle query used, and it preserves that bound's purpose:
-- a same-day re-capture cannot select today's own about-to-be-replaced row as
-- its own predecessor, because that row's captured_date equals @day.
--
-- Index reuse (no new index): the planner seeks the existing
-- idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at) on its
-- two leading equality columns and walks the ascending B-tree BACKWARD to
-- satisfy ORDER BY captured_at DESC, stopping at the first row that also passes
-- the captured_date residual predicate. Because
-- vehicle_snapshots_account_tesla_date_unique allows at most ONE row per
-- (account_id, tesla_id, captured_date), and captured_date is monotone
-- non-decreasing with captured_at for a vehicle, AT MOST ONE row is skipped
-- before the first match. Verified via EXPLAIN in the DB-integration test.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id   = @account_id
  AND tesla_id     = @tesla_id
  AND captured_date < @day
ORDER BY captured_at DESC
LIMIT 1;

-- LOAD-BEARING (R3, RM27-telemetry-add-supercharger-battery-pct): start_battery_pct,
-- end_battery_pct, battery_pct_source, start_battery_pct_est, and end_battery_pct_est
-- are DELIBERATELY ABSENT from both the INSERT column list and the ON CONFLICT DO
-- UPDATE SET clause below. The first three are a human-owned verification/override
-- channel; the last two are a frozen, write-once verification-time snapshot of the
-- estimate (design D6) -- NEVER refreshed, NEVER a cache read by internal/analytics
-- (see the column comments added by migration 20260815000001). If this query touched
-- any of the five, a user's verified value or its frozen snapshot would be silently
-- overwritten by the next nightly re-upsert. A fresh INSERT leaves all five at their
-- column default (NULL); a re-upsert never assigns any of them. A future writer for
-- these columns belongs on a dedicated query on a dedicated Writer port (out of
-- scope here, backlog entry 11) -- do not "complete the pattern" by adding them here.
-- name: UpsertSuperchargerSession :exec
-- Upsert one Supercharger session. On conflict with the session_id UNIQUE constraint,
-- refresh only the mutable/derived columns (raw_data, derived fields, tesla_id,
-- updated_at). Immutable columns (session_id, account_id, vin, location name,
-- country, timestamps, billing fields, created_at) are never overwritten.
-- Design DBS3: supercharger_sessions is NOT append-only; billing state mutates
-- post-session (is_paid, invoice status change after midnight).
INSERT INTO supercharger_sessions (
    session_id, account_id, vin, tesla_id,
    site_location_name, country_code,
    charge_start_date_time, charge_stop_date_time, unlatch_date_time,
    billing_type, vehicle_make_type,
    energy_kwh, total_cost, currency, is_paid,
    raw_data
) VALUES (
    @session_id, @account_id, @vin, @tesla_id,
    @site_location_name, @country_code,
    @charge_start_date_time, @charge_stop_date_time, @unlatch_date_time,
    @billing_type, @vehicle_make_type,
    @energy_kwh, @total_cost, @currency, @is_paid,
    @raw_data
)
ON CONFLICT (session_id) DO UPDATE SET
    raw_data   = EXCLUDED.raw_data,
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = now();

-- name: SuperchargerSessionsByAccount :many
-- Return all Supercharger sessions for the given account, newest first, up to
-- limit_count rows. Uses idx_supercharger_sessions_account_time
-- (account_id, charge_start_date_time DESC) — the account_id prefix prunes to
-- the tenant; DESC order matches the ORDER BY so no sort step is needed.
-- Design DBS4 / DBS6: account-wide spend/energy dashboard access pattern.
SELECT * FROM supercharger_sessions
WHERE account_id = @account_id
ORDER BY charge_start_date_time DESC
LIMIT @limit_count;

-- name: SuperchargerSessionsByVehicle :many
-- Return Supercharger sessions for one vehicle within an account, newest first,
-- up to limit_count rows. Uses idx_supercharger_sessions_vehicle_time
-- (account_id, tesla_id, charge_start_date_time DESC) — both WHERE columns are
-- the leading index columns so the planner satisfies the filter and the ORDER BY
-- in a single range scan without a sort step.
-- Design DBS4 / DBS6: per-vehicle charging history dashboard access pattern.
SELECT * FROM supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
ORDER BY charge_start_date_time DESC
LIMIT @limit_count;

-- name: SuperchargerSessionsByVehicleBetween :many
-- Return Supercharger sessions for one vehicle within an account whose
-- charge_stop_date_time falls in the caller-supplied [start, end] window,
-- inclusive of the whole end calendar day, ordered oldest-first (ascending
-- by charge_stop_date_time). Used by
-- SuperchargerReader.SuperchargerSessionsByVehicleBetween to power RM28's
-- battery-consumed-per-day derivation (roadmap D9/D12).
--
-- Filters on charge_stop_date_time, NOT charge_start_date_time (D12): energy
-- is fully delivered at session stop, which is what end_battery_pct
-- corresponds to, so a session belongs to the day its STOP falls in even
-- when it started the day before (a session spanning midnight IS included in
-- the window containing its stop instant -- deliberate, per D12).
--
-- Bounds (start, end are whole UTC-midnight-bounded calendar days; end
-- inclusive, matching this project's platform-wide HTTP date-filter
-- convention, ai/go-conventions.md §"Read optimization"): end_bound = end +
-- 1 calendar day (computed in Go, reader.go's
-- SuperchargerSessionsByVehicleBetween, mirroring
-- Reader.SnapshotsByVehicleBetween's own bounds-translation precedent of
-- doing the day-arithmetic in Go, not in SQL) so
-- WHERE charge_stop_date_time >= start AND charge_stop_date_time < end_bound
-- includes every instant of the end calendar day without an off-by-one on a
-- UTC-midnight end value. Simpler than SnapshotsByVehicleBetween's two-sided
-- +1/+2-day shift: that method filters on captured_at to select rows by
-- their DERIVED EffectiveDate (one calendar day earlier than the row's own
-- timestamp); this method filters directly on charge_stop_date_time, which
-- already IS the value being windowed -- no EffectiveDate-style lag to
-- compensate for, so only the upper bound needs translating.
--
-- Index reuse: idx_supercharger_sessions_vehicle_time
-- (account_id, tesla_id, charge_start_date_time DESC) does NOT fully serve
-- this query -- it is sorted on charge_start_date_time, not
-- charge_stop_date_time, so the stop-time predicate cannot be satisfied as a
-- pure index range scan. It STILL prunes the scan to this one vehicle's rows
-- via its (account_id, tesla_id) leading-column prefix before the
-- stop-time filter is applied in-memory -- see design.md's Index Plan for
-- why no third, dedicated (account_id, tesla_id, charge_stop_date_time)
-- index is added in this change, and the documented fallback if per-vehicle
-- session volume ever grows enough to make that decision wrong.
--
-- No LIMIT: this is a bounded date-range query, not an unbounded "most
-- recent N" query -- the caller-supplied window is the safety bound, exactly
-- like Reader.SnapshotsByVehicleBetween's own reasoning (that method DOES
-- still add a defensive LIMIT 400 on top of its window, design D3 there; this
-- query does not add an equivalent cap -- see design.md's Index Plan for why
-- that asymmetry is deliberate, not an oversight).
SELECT * FROM supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND charge_stop_date_time >= @start
  AND charge_stop_date_time <  @end_bound
ORDER BY charge_stop_date_time ASC;

-- name: SuperchargerSessionsByVehicleUpdatedSince :many
-- Return every Supercharger session for one vehicle within an account whose
-- updated_at is at or after `since`, ordered oldest-first by updated_at. Used by
-- SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince to let
-- internal/analytics' Recalculator (RM29-analytics-add-vehicle-metrics) detect
-- which sessions changed recently -- including a billing-state revision on a
-- session weeks old (design DBS3: supercharger_sessions is not append-only;
-- is_paid / invoice status mutates post-session), whose charge_start_date_time /
-- charge_stop_date_time stay unchanged while updated_at refreshes.
--
-- Index reuse: idx_supercharger_sessions_vehicle_time
-- (account_id, tesla_id, charge_start_date_time DESC) is not sorted on
-- updated_at, so this query cannot use it as a pure ORDER BY-satisfying range
-- scan. It STILL prunes the scan to this one vehicle's rows via its
-- (account_id, tesla_id) leading-column prefix before the updated_at predicate
-- and sort are applied -- updated_at is a residual filter within that scan, per
-- this change's explicit design call (no new index; verified via EXPLAIN in the
-- DB-integration test, RM29-analytics-add-vehicle-metrics Wave 6).
SELECT * FROM supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND updated_at >= @since
ORDER BY updated_at ASC;
