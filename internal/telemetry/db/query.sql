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
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level, battery_range, charging_state, charge_limit_soc,
    odometer, inside_temp, outside_temp, locked, sentry_mode,
    car_version, latitude, longitude
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level, @battery_range, @charging_state, @charge_limit_soc,
    @odometer, @inside_temp, @outside_temp, @locked, @sentry_mode,
    @car_version, @latitude, @longitude
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
    car_version, latitude, longitude
FROM vehicle_snapshots
WHERE account_id = @account_id
ORDER BY tesla_id, captured_at DESC;
