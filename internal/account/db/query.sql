-- Queries for the account module. sqlc generates package `accountdb` from these
-- against the schema in migrations/. No other module may import accountdb.

-- name: UpsertAccountFromOAuth :one
-- Create the account for a social identity, or resolve the existing one. The
-- UNIQUE (provider, provider_id) constraint makes this idempotent per identity.
INSERT INTO accounts (email, provider, provider_id, display_name)
VALUES (@email, @provider, @provider_id, @display_name)
ON CONFLICT (provider, provider_id) DO UPDATE
SET email        = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at   = now()
RETURNING *;

-- name: GetAccountByProviderID :one
SELECT * FROM accounts
WHERE provider = @provider AND provider_id = @provider_id;

-- name: UpsertTeslaToken :one
-- One Tesla connection per account (UNIQUE account_id); reconnecting replaces it in place.
INSERT INTO tesla_tokens (account_id, tesla_email, access_token, refresh_token, access_expires_at)
VALUES (@account_id, @tesla_email, @access_token, @refresh_token, @access_expires_at)
ON CONFLICT (account_id) DO UPDATE
SET tesla_email       = EXCLUDED.tesla_email,
    access_token      = EXCLUDED.access_token,
    refresh_token     = EXCLUDED.refresh_token,
    access_expires_at = EXCLUDED.access_expires_at,
    updated_at        = now()
RETURNING *;

-- name: UpdateTeslaToken :one
-- Rotate a connection's tokens in place (used after a refresh).
UPDATE tesla_tokens
SET access_token      = @access_token,
    refresh_token     = @refresh_token,
    access_expires_at = @access_expires_at,
    updated_at        = now()
WHERE id = @id
RETURNING *;

-- name: GetLatestTeslaTokenByAccount :one
SELECT * FROM tesla_tokens
WHERE account_id = @account_id
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetLatestTeslaTokenByAccountForUpdate :one
-- Same as above but row-locked; used inside the refresh transaction so concurrent
-- refreshes of the same connection serialize and can't strand a single-use token.
SELECT * FROM tesla_tokens
WHERE account_id = @account_id
ORDER BY updated_at DESC
LIMIT 1
FOR UPDATE;

-- name: InsertVehicleIfMissing :exec
-- Idempotent per-account vehicle registration: insert a vehicle only if this
-- (account_id, tesla_id) is not already registered. Existing vehicles are left
-- untouched (display_name is NOT overwritten) — the "rest of the information
-- should not change" rule. :exec (no RETURNING) because ON CONFLICT DO NOTHING
-- yields no row on a skipped insert, and the caller re-reads the full set via
-- ListVehiclesByAccount anyway — there is nothing to return here.
-- ON CONFLICT DO NOTHING is UNCHANGED per design.md D3.
INSERT INTO vehicles (account_id, tesla_id, vin, display_name, access_type)
VALUES (@account_id, @tesla_id, @vin, @display_name, @access_type)
ON CONFLICT (account_id, tesla_id) DO NOTHING;

-- name: UpdateVehicleConfigIfEmpty :exec
-- Conditional write-back for the two static vehicle_config attributes (design.md D4). The
-- WHERE clause uses OR (not AND): a row missing only one of the two values is still eligible
-- for a self-healing write, and a row with both already captured never matches (defense in
-- depth — RD2 — independent of whatever Go-side guard the caller applies). updated_at only
-- moves when the WHERE clause actually matches a row.
UPDATE vehicles
SET exterior_color = @exterior_color,
    car_type        = @car_type,
    updated_at      = now()
WHERE account_id = @account_id
  AND tesla_id    = @tesla_id
  AND (exterior_color IS NULL OR car_type IS NULL);

-- name: ListVehiclesByAccount :many
-- All vehicles registered to an account, ordered by tesla_id for stable output.
SELECT * FROM vehicles
WHERE account_id = @account_id
ORDER BY tesla_id;

-- name: ListAllVehicles :many
-- Every registered vehicle across ALL accounts, each with its owning account_id,
-- for background collection jobs (nightly telemetry). Ordered (account_id, tesla_id)
-- for stable, testable output. No join to tesla_tokens: enumeration is decoupled
-- from connection liveness (that is the caller's job via AccessTokenFor).
SELECT account_id, tesla_id, vin, display_name, access_type, exterior_color, car_type FROM vehicles
ORDER BY account_id, tesla_id;
