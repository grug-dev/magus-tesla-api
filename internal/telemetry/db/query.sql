-- Queries for the telemetry module. sqlc generates package `telemetrydb` from
-- these against the schema in migrations/. No other module may import telemetrydb
-- (module-scoped DB access — ai/architecture.md §2, ai/go-conventions.md
-- §persistence). All writes are append-only inserts (no UPDATE/DELETE): the two
-- tables are immutable history.

-- name: InsertVehicleSnapshot :exec
-- Insert one immutable snapshot row. Distance/range columns are stored API-native
-- (miles); km is derived on read by the domain type's Km() companions, never a
-- column. sentry_mode is bound as a nullable boolean (nil = vehicle did not
-- report sentry) so absent stays distinct from a reported off.
-- Source A (RM2-telemetry-add-charging-stats): the 6 charge-enrichment columns are
-- always non-NULL for rows written after the 20260716000002 migration — snapshotFrom
-- stores the actual DTO value pointer-wrapped (D12: no zero-is-absent heuristic).
-- NULL is reserved for pre-migration rows only; see design DSA1/DSA3.
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level, battery_range, charging_state, charge_limit_soc,
    odometer, inside_temp, outside_temp, locked, sentry_mode,
    car_version, latitude, longitude,
    charge_energy_added, charger_power, charger_voltage,
    charger_actual_current, usable_battery_level, fast_charger_type
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level, @battery_range, @charging_state, @charge_limit_soc,
    @odometer, @inside_temp, @outside_temp, @locked, @sentry_mode,
    @car_version, @latitude, @longitude,
    @charge_energy_added, @charger_power, @charger_voltage,
    @charger_actual_current, @usable_battery_level, @fast_charger_type
);

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
SELECT * FROM vehicle_snapshots
WHERE account_id = @account_id AND tesla_id = @tesla_id
ORDER BY captured_at DESC;

-- name: ListPollAttemptsByVehicle :many
-- Read helper for the DATABASE_URL-gated store tests: every attempt for one
-- vehicle, newest first. Not consumed by another module (module-scoped).
SELECT * FROM poll_attempts
WHERE account_id = @account_id AND tesla_id = @tesla_id
ORDER BY attempted_at DESC;

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
    battery_level, battery_range, charging_state, charge_limit_soc,
    odometer, inside_temp, outside_temp, locked, sentry_mode,
    car_version, latitude, longitude,
    charge_energy_added, charger_power, charger_voltage,
    charger_actual_current, usable_battery_level, fast_charger_type
FROM vehicle_snapshots
WHERE account_id = @account_id
ORDER BY tesla_id, captured_at DESC;

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
