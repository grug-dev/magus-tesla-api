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
-- (snapshotFrom/dateOnly, service.go) from captured_at in the poller's
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
-- distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
-- estimated_range_km_calc, days_spanned_calc: five nullable derived-consumption
-- columns computed in Go by deriveConsumption (service.go) BEFORE this query
-- runs and bound as ordinary params, exactly like every other typed column
-- (telemetry-add-derived-consumption-columns design D3/D6/D8). NULL means "no
-- predecessor exists" (D8) or, for the two efficiency columns only, a
-- zero/negative battery-used divisor (D2). They are included in the ON
-- CONFLICT DO UPDATE SET below so a same-day re-capture recomputes and
-- refreshes them identically to every other column — this is the fix for the
-- same-day-recapture staleness bug (design D6).
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level_pct, @battery_range_km, @charging_state, @charge_limit_soc_pct,
    @odometer_km, @inside_temp_c, @outside_temp_c, @locked, @sentry_mode,
    @car_version,
    @charge_energy_added_kwh, @charger_power_kw, @charger_voltage_v,
    @charger_actual_current_a, @usable_battery_level_pct,
    @max_range_charge_counter,
    @tpms_pressure_fl_psi, @tpms_pressure_fr_psi, @tpms_pressure_rl_psi, @tpms_pressure_rr_psi,
    @captured_date,
    @distance_traveled_km_calc, @battery_used_pct_calc, @km_per_pct_calc,
    @estimated_range_km_calc, @days_spanned_calc
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
    distance_traveled_km_calc  = EXCLUDED.distance_traveled_km_calc,
    battery_used_pct_calc      = EXCLUDED.battery_used_pct_calc,
    km_per_pct_calc            = EXCLUDED.km_per_pct_calc,
    estimated_range_km_calc    = EXCLUDED.estimated_range_km_calc,
    days_spanned_calc          = EXCLUDED.days_spanned_calc,
    updated_at                 = now();

-- name: InsertPollAttempt :exec
-- Record one attempt per (vehicle, run), success or failure. outcome is
-- success|failure; reason is ok|asleep-timeout|unauthorized|api-error.
INSERT INTO poll_attempts (
    account_id, tesla_id, attempted_at, outcome, reason
) VALUES (
    @account_id, @tesla_id, @attempted_at, @outcome, @reason
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
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
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
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
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
-- LIMIT 400 (D3, parity with SnapshotsByVehicleSince's D4): safety cap against an
-- accidentally large result set if capture cadence ever increases. The HTTP contract
-- (Decision #2) caps the window at <= 90 days; the gateway's 1-day lookback (Decision
-- #4) adds one day, so the realistic max return is ~91 rows (one nightly snapshot per
-- calendar day under the current cadence). 400 comfortably exceeds that without being
-- so large it re-introduces the unbounded-scan risk the cap exists to prevent; the
-- bounded window itself (not the LIMIT) is the real protection on this path.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at >= @start_bound
  AND captured_at <  @end_bound
ORDER BY captured_at ASC
LIMIT 400;

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
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
FROM vehicle_snapshots
WHERE account_id = @account_id
ORDER BY tesla_id, captured_at DESC;

-- name: PreviousSnapshotForVehicle :one
-- Return the single most recent snapshot for a vehicle strictly before the
-- given instant, or pgx.ErrNoRows when none exists (the vehicle's
-- first-ever snapshot — design D8/D10 of telemetry-add-derived-consumption-columns).
-- Callers pass dayStart(capturedAt, loc) as `before` (design D7) — the LOCAL
-- calendar-day start, not the incoming snapshot's own captured_at — so a
-- same-day re-capture cannot select today's own (about-to-be-replaced) row
-- as its own predecessor.
-- Backward scan of the existing idx_vehicle_snapshots_vehicle_time
-- (account_id, tesla_id, captured_at) index (design D7): the planner seeks
-- to (account_id, tesla_id, before) and walks the ascending B-tree in
-- reverse to satisfy ORDER BY captured_at DESC, stopping after the first
-- matching row via LIMIT 1 — no new index.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at < @before
ORDER BY captured_at DESC
LIMIT 1;

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
