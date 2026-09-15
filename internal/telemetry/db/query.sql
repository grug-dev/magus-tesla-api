-- Queries for the telemetry module. sqlc generates package `telemetrydb` from
-- these against the schema in migrations/. No other module may import telemetrydb
-- (module-scoped DB access — ai/architecture.md §2, ai/go-conventions.md
-- §persistence). All writes are append-only inserts (no UPDATE/DELETE): the two
-- tables are immutable history.

-- name: InsertVehicleSnapshot :exec
-- Upsert one snapshot: inserts a new row, or REPLACES the existing row for
-- the same (tesla_id, captured_date) if one already exists — a later
-- same-day capture is fresher information about that day, so it wins.
-- captured_date is Go-computed (snapshotFrom/clock.CalendarDay, service.go)
-- from captured_at in the platform's default zone — never a DB expression,
-- since a UNIQUE index cannot depend on a runtime env var.
-- Distance/range columns store DISPLAY units (km), converted exactly once at
-- capture time by calling the tesla adapter's Km() companions — never
-- derived on read.
-- sentry_mode is bound as a nullable boolean (nil = vehicle did not report
-- sentry) so absent stays distinct from a reported off.
-- The charge-enrichment, max_range_charge_counter and tpms_pressure_*
-- columns are nullable: snapshotFrom pointer-wraps the actual DTO value, so
-- a real 0 (or 0.0 PSI) is stored non-NULL and NULL means the vehicle did
-- not report the field, or the row predates that column's extraction. No
-- new index for the tpms columns — they ride along on the existing heap
-- row fetch.
-- latitude/longitude/fast_charger_type are not extracted columns — lossless
-- in raw_data only.
-- updated_at is NOT sent as a param: DEFAULT now() handles a fresh INSERT;
-- the ON CONFLICT clause explicitly refreshes it to now() on a same-day
-- replace, mirroring UpsertSuperchargerHistory's own `updated_at = now()`.
-- The five derived-consumption columns (distance_traveled_km_calc,
-- battery_used_pct_calc, km_per_pct_calc, estimated_range_km_calc,
-- days_spanned_calc) are not bound here: internal/analytics computes them
-- from this table's surviving raw columns (odometer_km, battery_level_pct,
-- captured_date) and never reads them back from here.
INSERT INTO telemetry.vehicle_snapshots (
    tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date
) VALUES (
    @tesla_id, @captured_at, @raw_data,
    @battery_level_pct, @battery_range_km, @charging_state, @charge_limit_soc_pct,
    @odometer_km, @inside_temp_c, @outside_temp_c, @locked, @sentry_mode,
    @car_version,
    @charge_energy_added_kwh, @charger_power_kw, @charger_voltage_v,
    @charger_actual_current_a, @usable_battery_level_pct,
    @max_range_charge_counter,
    @tpms_pressure_fl_psi, @tpms_pressure_fr_psi, @tpms_pressure_rl_psi, @tpms_pressure_rr_psi,
    @captured_date
)
ON CONFLICT (tesla_id, captured_date) DO UPDATE SET
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
-- correlates every vehicle's row from one collection cycle; triggered_by
-- records what triggered that cycle. polled_by_account_id is the account
-- whose credentials made this attempt — not necessarily the vehicle's only
-- registered account, since one account is elected to poll each vehicle.
INSERT INTO telemetry.poll_attempts (
    polled_by_account_id, tesla_id, attempted_at, outcome, reason, run_id, triggered_by
) VALUES (
    @polled_by_account_id, @tesla_id, @attempted_at, @outcome, @reason, @run_id, @triggered_by
);

-- name: SnapshotsByVehicleSince :many
-- Return all snapshots for a single vehicle captured at or after `since`,
-- ordered oldest-first. Used by telemetry.Reader.SnapshotsByVehicleSince to
-- power the odometer/battery history charts.
--
-- Index reuse: the (tesla_id, captured_date) UNIQUE index prunes the scan to
-- this vehicle's rows on the tesla_id equality; captured_at then applies as
-- a residual filter and sort over that already one-row-per-day-at-most set.
--
-- LIMIT 400: safety cap against an accidentally large result set if capture
-- cadence ever increases. A 30-day window returns ~30 rows under the current
-- nightly schedule — 400 comfortably exceeds any realistic dashboard window
-- (~13 months).
SELECT
    id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM telemetry.vehicle_snapshots
WHERE tesla_id   = @tesla_id
  AND captured_at >= @since
ORDER BY captured_at ASC
LIMIT 400;

-- name: SnapshotsByVehicleBetween :many
-- Return the snapshots for a single vehicle whose **EffectiveDate calendar
-- day** falls in the caller-supplied `[start, end]` window inclusive,
-- ordered oldest-first (ascending by captured_at == ascending by
-- EffectiveDate, since EffectiveDate is monotonic in CapturedAt). Used by
-- telemetry.Reader.SnapshotsByVehicleBetween to power the bounded history
-- charts. `start` and `end` are whole UTC-midnight-bounded calendar days;
-- `end` is inclusive.
--
-- Bounds derivation: EffectiveDate = CapturedAt.AddDate(0,0,-1), i.e.
-- EffectiveDate's UTC calendar day == CapturedAt's UTC calendar day minus 1. So
-- `EffectiveDate ∈ [start, end]` inclusive ⟺ `CapturedAt ∈ [start+1 day, end+1 day]`
-- (calendar days, inclusive both ends). Expressed as TIMESTAMPTZ predicates this is
-- the half-open range `[start_bound, end_bound)`:
--     start_bound = start + 1 calendar day   (start.AddDate(0,0,1)  — UTC midnight beginning the first eligible capture day)
--     end_bound   = end   + 2 calendar days  (end  .AddDate(0,0,2)  — UTC midnight ending the last eligible capture day, exclusive)
-- The half-open upper bound makes `end` inclusive without an off-by-one on a
-- UTC-midnight `end` instant.
-- The translation `(start, end) → (start_bound, end_bound)` lives INSIDE the
-- dbStore implementation (service.go), NOT in the public Reader method: the
-- reader/store seam passes the caller's raw `(start, end)` through unchanged,
-- and the dbStore computes and binds the two bounds as pgtype.Timestamptz here.
--
-- Why captured_at (TIMESTAMPTZ) and NOT captured_date (DATE): captured_at is
-- the UTC capture instant EffectiveDate is derived from (rowToSnapshot:
-- CapturedAt.Time.AddDate(0,0,-1)), so the query and the mapper are consistent
-- by construction with zero timezone coupling. captured_date is Go-computed in
-- the platform's default zone and used solely for the (tesla_id, captured_date)
-- dedupe UNIQUE constraint. Filtering on captured_date would couple this
-- port's correctness to that zone and break quietly the moment a capture
-- straddles UTC midnight or the zone changes. (The (tesla_id, captured_date)
-- unique index would *serve* such a query, but semantic correctness
-- disqualifies it.)
--
-- Index reuse: the (tesla_id, captured_date) UNIQUE index prunes the scan to
-- this vehicle's rows on the tesla_id equality; the residual
-- (captured_at >= start_bound AND captured_at < end_bound ORDER BY
-- captured_at ASC) filter and sort run over that already-small,
-- one-row-per-day set — the same access pattern SnapshotsByVehicleSince
-- uses, with one extra range predicate.
--
-- LIMIT 4000: this query also backs internal/analytics' Reconcile epoch
-- backfill, which can call Recalculate over a SINGLE vehicle's ENTIRE
-- snapshot history in one window. A cap too low would silently drop the
-- newest rows (this query is `ORDER BY captured_at ASC LIMIT n`), and
-- Reconcile would then advance its watermark past days it never recomputed
-- — permanently wrong metrics, no error, no log line. 4000 is ~11 years at
-- the platform's one-snapshot-per-vehicle-per-day cadence, comfortably above
-- any real vehicle's history — the cap is a runaway-query guard; the bounded
-- [start, end] window supplied by the caller is the real protection, not the
-- LIMIT. Truncation detection or pagination for a vehicle exceeding 4000
-- days is explicitly out of scope.
SELECT
    id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM telemetry.vehicle_snapshots
WHERE tesla_id   = @tesla_id
  AND captured_at >= @start_bound
  AND captured_at <  @end_bound
ORDER BY captured_at ASC
LIMIT 4000;

-- name: SnapshotsByVehicleUpdatedSince :many
-- Return every snapshot for a single vehicle whose updated_at is at or after
-- `since`, ordered oldest-first by updated_at. Used by
-- telemetry.Reader.SnapshotsByVehicleUpdatedSince to let internal/analytics'
-- Recalculator detect which snapshots changed recently -- including a
-- same-day REPLACE via the existing UPSERT, which advances updated_at
-- without necessarily changing captured_at's calendar day.
--
-- Index reuse: the (tesla_id, captured_date) UNIQUE index is not sorted on
-- updated_at, so this query cannot use it as a pure ORDER BY-satisfying
-- range scan the way SnapshotsByVehicleSince does on captured_at. It STILL
-- prunes the scan to this one vehicle's rows via the tesla_id equality
-- before the updated_at predicate and sort are applied -- updated_at is a
-- residual filter within that scan.
SELECT
    id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM telemetry.vehicle_snapshots
WHERE tesla_id   = @tesla_id
  AND updated_at >= @since
ORDER BY updated_at ASC;

-- name: LatestSnapshotsByVehicles :many
-- Return the latest stored snapshot for each vehicle in the given batch of
-- tesla_ids, regardless of which account registered them.
-- DISTINCT ON (tesla_id) with ORDER BY tesla_id, captured_at DESC picks the row
-- with the highest captured_at per tesla_id — one Postgres index scan, no N+1.
-- The (tesla_id, captured_date) UNIQUE index covers the tesla_id filter; the
-- planner satisfies the WHERE and ORDER BY in a single efficient scan.
-- This is the batch read for the dashboard: it avoids the N+1 that would
-- result from reading each vehicle's snapshots separately.
SELECT DISTINCT ON (tesla_id)
    id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM telemetry.vehicle_snapshots
WHERE tesla_id = ANY(@tesla_ids::bigint[])
ORDER BY tesla_id, captured_at DESC;

-- name: SnapshotPrecedingDay :one
-- Return the single most recent snapshot for a vehicle whose captured_date is
-- strictly before the given calendar day, or pgx.ErrNoRows when none exists
-- (the vehicle's first-ever snapshot). Backs telemetry.Reader.SnapshotPrecedingDay,
-- whose only consumer is internal/analytics' Recalculate: it needs the EXACT
-- predecessor, however old, because a capture gap longer than its fetch window
-- would otherwise yield a silently wrong (or silently absent) daily delta.
--
-- The bound is captured_date, NOT captured_at: captured_date is already the
-- platform-zone calendar day (stamped once on the write path by
-- clock.CalendarDay), so the predicate is zone-free at query time. This
-- preserves the bound's purpose: a same-day re-capture cannot select today's
-- own about-to-be-replaced row as its own predecessor, because that row's
-- captured_date equals @day.
--
-- Index reuse (no new index): the (tesla_id, captured_date) UNIQUE index is
-- the exact predicate pair, in index order — tesla_id equality plus a
-- captured_date bound, walked backward to satisfy ORDER BY captured_date DESC.
-- Because the UNIQUE constraint allows at most ONE row per
-- (tesla_id, captured_date), and captured_date is monotone non-decreasing
-- with captured_at for a vehicle, AT MOST ONE row is skipped before the
-- first match.
SELECT
    id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM telemetry.vehicle_snapshots
WHERE tesla_id     = @tesla_id
  AND captured_date < @day
ORDER BY captured_date DESC
LIMIT 1;

-- LOAD-BEARING (R3, RM27-telemetry-add-supercharger-battery-pct): start_battery_pct,
-- end_battery_pct, and battery_pct_source are DELIBERATELY ABSENT from both the INSERT
-- column list and the ON CONFLICT DO UPDATE SET clause below. They are a human-owned
-- verification/override channel; the nightly sync must never write, clear or overwrite
-- one. If this query touched any of the three, a user's verified value would be
-- silently overwritten by the next nightly re-upsert. A fresh INSERT leaves all three
-- at their column default (NULL); a re-upsert never assigns any of them. A future
-- writer for these columns belongs on a dedicated query on a dedicated Writer port
-- (out of scope here, backlog entry 11) -- do not "complete the pattern" by adding
-- them here. The two frozen estimate columns formerly also excluded here as a
-- write-once verification-time snapshot pair were dropped from the table entirely
-- by RM41-telemetry-drop-estimate-columns -- there is no longer a column to guard.
-- name: UpsertSuperchargerHistory :exec
-- Upsert one Supercharger session. On conflict with the session_id UNIQUE constraint,
-- refresh only six mutable columns: raw_data, energy_kwh, total_cost, currency,
-- is_paid, tesla_id.
--
-- updated_at only advances when the row's real data changed. The governing
-- rule: the comparison below covers EXACTLY the columns the SET clause writes.
-- If you add a column to SET, remove it from the deny-list. If you add a
-- column the SET clause does NOT write, add it to the deny-list. Breaking this
-- rule is loud, not silent: a forgotten column makes updated_at advance every
-- night, which is easy to notice, never the other way around.
--
-- The deny-list has three groups, all excluded from the comparison:
--   1. Bookkeeping (id, created_at, updated_at) -- not session data. id never
--      differs on a conflict; created_at is write-once and EXCLUDED.created_at
--      is a fresh default value, not the row's real one; updated_at is the
--      column this CASE computes, so comparing it would be circular.
--   2. Human-owned (start_battery_pct, end_battery_pct, battery_pct_source) --
--      a person sets these by hand. They are never in the INSERT list, so
--      EXCLUDED always has them NULL. Without this exclusion, a poller re-sync
--      would see "set on disk" vs "NULL incoming" and wrongly call that a
--      change, overwriting the human's work.
--   3. Write-once (session_id, vin, site_location_name, country_code,
--      charge_start_date_time, charge_stop_date_time, unlatch_date_time,
--      billing_type, vehicle_make_type) -- written once on INSERT, never
--      refreshed by this SET clause. If Tesla later sends a different value
--      for one of these, the stored value stays as-is by design, and
--      EXCLUDED can differ from it forever. Comparing these columns would
--      make updated_at advance every single night, forever, for any row with
--      such a gap -- the exact bug this query exists to fix, just moved to a
--      different set of columns.
--
-- Design DBS3: supercharger_history is NOT append-only; billing state mutates
-- post-session (is_paid, invoice status change after midnight). Full rationale:
-- openspec/changes/RM44-telemetry-add-change-detecting-upsert/design.md D1/D2.
INSERT INTO telemetry.supercharger_history (
    session_id, vin, tesla_id,
    site_location_name, country_code,
    charge_start_date_time, charge_stop_date_time, unlatch_date_time,
    billing_type, vehicle_make_type,
    energy_kwh, total_cost, currency, is_paid,
    raw_data
) VALUES (
    @session_id, @vin, @tesla_id,
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
    updated_at = CASE
        WHEN to_jsonb(supercharger_history.*) - '{id,session_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]
             IS DISTINCT FROM
             to_jsonb(EXCLUDED.*) - '{id,session_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]
        THEN now()
        ELSE supercharger_history.updated_at
    END;

-- name: SuperchargerHistoryByVehicleUpdatedSince :many
-- Return every Supercharger session for one vehicle whose updated_at is at
-- or after `since`, ordered oldest-first by updated_at. Used by
-- SuperchargerHistoryReader.SuperchargerHistoryByVehicleUpdatedSince to let
-- internal/analytics' Recalculator (RM29-analytics-add-vehicle-metrics) and
-- internal/app's nightly Supercharger mirror detect which sessions changed
-- recently -- including a billing-state revision on a session weeks old
-- (design DBS3: supercharger_history is not append-only; is_paid / invoice
-- status mutates post-session), whose charge_start_date_time /
-- charge_stop_date_time stay unchanged while updated_at refreshes.
--
-- Index: idx_supercharger_history_vehicle_updated (tesla_id, updated_at).
-- It matches this query exactly -- tesla_id prunes to the vehicle, and
-- updated_at ASC satisfies both the range predicate and the ORDER BY in one
-- index scan, with no sort step. No residual filter, and no sort node.
SELECT * FROM telemetry.supercharger_history
WHERE tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY updated_at ASC;

-- name: InsertPollRun :exec
-- Inserts one poll_runs row. Called exactly once per app.ProcessVehicleData
-- invocation via telemetry.RunWriter.RecordRun (design D3/D11/D12). Never an
-- upsert: a duplicate run_id is a caller bug and must fail loudly on the
-- PRIMARY KEY, not be silently absorbed.
INSERT INTO telemetry.poll_runs (
    run_id, triggered_by, started_at, finished_at, duration_seconds,
    accounts_attempted, accounts_succeeded, accounts_failed,
    vehicles_attempted, vehicles_succeeded,
    failures_asleep_timeout, failures_unauthorized, failures_api_error,
    tesla_api_calls,
    charging_sessions_upserted, charging_fetch_failures, config_capture_failures
) VALUES (
    @run_id, @triggered_by, @started_at, @finished_at, @duration_seconds,
    @accounts_attempted, @accounts_succeeded, @accounts_failed,
    @vehicles_attempted, @vehicles_succeeded,
    @failures_asleep_timeout, @failures_unauthorized, @failures_api_error,
    @tesla_api_calls,
    @charging_sessions_upserted, @charging_fetch_failures, @config_capture_failures
);
