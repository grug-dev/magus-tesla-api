-- Queries for the account module. sqlc generates package `accountdb` from these
-- against the schema in migrations/. No other module may import accountdb.

-- name: UpsertAccountFromOAuth :one
-- Create the account for a social identity, or resolve the existing one. The
-- UNIQUE (provider, provider_id) constraint makes this idempotent per identity.
INSERT INTO account.accounts (email, provider, provider_id, display_name)
VALUES (@email, @provider, @provider_id, @display_name)
ON CONFLICT (provider, provider_id) DO UPDATE
SET email        = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at   = now()
RETURNING *;

-- name: GetAccountByProviderID :one
-- Filtered by status = 'Active' (design.md D4, RM34): an Inactive account is
-- invisible to every account read except UpsertAccountFromOAuth.
SELECT * FROM account.accounts
WHERE provider = @provider AND provider_id = @provider_id AND status = 'Active';

-- name: UpsertTeslaToken :one
-- One Tesla connection per account (UNIQUE account_id); reconnecting replaces it in place.
INSERT INTO account.tesla_tokens (account_id, tesla_email, access_token, refresh_token, access_expires_at)
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
UPDATE account.tesla_tokens
SET access_token      = @access_token,
    refresh_token     = @refresh_token,
    access_expires_at = @access_expires_at,
    updated_at        = now()
WHERE id = @id
RETURNING *;

-- name: GetLatestTeslaTokenByAccount :one
-- Gated by the owning account's status via EXISTS (design.md D14/D15, RM34): an
-- Inactive account's token is invisible here, same as its vehicles. EXISTS (not a
-- JOIN) keeps `SELECT *` scoped to tesla_tokens alone, so the sqlc-generated row
-- struct is unchanged (D14).
SELECT * FROM account.tesla_tokens
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = tesla_tokens.account_id AND a.status = 'Active')
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetLatestTeslaTokenByAccountForUpdate :one
-- Same as above but row-locked; used inside the refresh transaction so concurrent
-- refreshes of the same connection serialize and can't strand a single-use token.
-- Gated by the owning account's status via EXISTS (design.md D14/D15, RM34): a
-- revoked (Inactive) account can no longer burn a single-use refresh token.
SELECT * FROM account.tesla_tokens
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = tesla_tokens.account_id AND a.status = 'Active')
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
INSERT INTO account.vehicles (account_id, tesla_id, vin, display_name, access_type)
VALUES (@account_id, @tesla_id, @vin, @display_name, @access_type)
ON CONFLICT (account_id, tesla_id) DO NOTHING;

-- name: UpdateVehicleConfigIfEmpty :exec
-- Conditional write-back for the two static vehicle_config attributes (design.md D4). The
-- WHERE clause uses OR (not AND): a row missing only one of the two values is still eligible
-- for a self-healing write, and a row with both already captured never matches (defense in
-- depth — RD2 — independent of whatever Go-side guard the caller applies). updated_at only
-- moves when the WHERE clause actually matches a row.
UPDATE account.vehicles
SET exterior_color = @exterior_color,
    car_type        = @car_type,
    updated_at      = now()
WHERE account_id = @account_id
  AND tesla_id    = @tesla_id
  AND (exterior_color IS NULL OR car_type IS NULL);

-- name: ListVehiclesByAccount :many
-- All vehicles registered to an account, ordered by tesla_id for stable output.
-- Filtered by status = 'Active' (design.md D4, RM34): an Inactive vehicle is
-- excluded from this read. Also gated by the owning account's status via EXISTS
-- (design.md D14/D15, RM34): a deactivated user's stateless cookie session must
-- not keep rendering real vehicles. EXISTS (not a JOIN) keeps `SELECT *` scoped
-- to vehicles alone, so the sqlc-generated row struct is unchanged (D14).
SELECT * FROM account.vehicles
WHERE account_id = @account_id AND status = 'Active'
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = vehicles.account_id AND a.status = 'Active')
ORDER BY tesla_id;

-- name: GetAccountSettings :one
-- The per-request read path: every preference (language, theme,
-- analysis_start_date) in a single query — design.md D7's answer to the
-- roadmap's binding "one query, both values" constraint, extended by
-- RM49 D4 to a third column at zero extra query cost. account_id is the
-- table's own PK, so this is a plain PK lookup, no secondary index
-- (design.md D9; RM49 design.md D3).
-- Gated by the owning account's status via EXISTS (design.md D10, carrying
-- forward RM34 D14/D15): an Inactive account's preferences are not readable —
-- the read behaves as though no such account exists. EXISTS (not a JOIN) keeps
-- the row shape (language, theme, analysis_start_date) unaffected by the gate.
SELECT language, theme, analysis_start_date FROM account.settings
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');

-- name: UpdateAccountLanguage :exec
-- Persists an explicit language switch. Vocabulary validation happens in the Go
-- caller (Service.SetLanguage) before this query runs — see design.md D5 for why
-- there is no CHECK constraint doing this at the DB layer instead.
-- Gated by the owning account's status via EXISTS (design.md D10, carrying
-- forward RM34 D14/D15): against an Inactive account this matches zero rows and
-- is a silent no-op (Postgres does not error on an UPDATE matching zero rows,
-- and SetLanguage does not inspect affected-row count) — documented
-- consequence, not a bug (design.md D4).
UPDATE account.settings
SET language = @language
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');

-- name: UpdateAccountTheme :exec
-- Persists an explicit theme switch. Vocabulary validation happens in the Go
-- caller (Service.SetTheme) before this query runs — see design.md D5 for why
-- there is no CHECK constraint doing this at the DB layer instead. Mirrors
-- UpdateAccountLanguage exactly, including the same EXISTS gate (design.md D10).
UPDATE account.settings
SET theme = @theme
WHERE account_id = @account_id
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = settings.account_id AND a.status = 'Active');

-- name: InsertSettingsIfMissing :exec
-- Creates the settings row for a newly provisioned account, called inside the
-- same transaction as UpsertAccountFromOAuth (design.md D3). ON CONFLICT DO
-- NOTHING matters because UpsertFromOAuth is also the resolve-existing-account
-- path: a returning user must never have a real settings row silently reset
-- (including its analysis_start_date).
-- Deliberately NOT gated by account status, mirroring UpsertAccountFromOAuth's
-- own exemption (design.md D10) — a brand-new account has no status concern yet.
-- analysis_start_date is supplied by the Go caller via internal/clock
-- (RM49 design.md D5) — never CURRENT_DATE or a column DEFAULT, both of
-- which would resolve in the database session's time zone.
INSERT INTO account.settings (account_id, analysis_start_date)
VALUES (@account_id, @analysis_start_date)
ON CONFLICT (account_id) DO NOTHING;

-- name: ListAllVehicles :many
-- Every registered vehicle across ALL accounts, each with its owning account_id,
-- for background collection jobs (nightly telemetry). Ordered (account_id, tesla_id)
-- for stable, testable output. No join to tesla_tokens: enumeration is decoupled
-- from connection liveness (that is the caller's job via AccessTokenFor).
-- Filtered by status = 'Active' (design.md D4, RM34): an Inactive vehicle is
-- excluded regardless of its owning account's status. Additionally gated by the
-- owning account's status via EXISTS (design.md D14/D15, RM34): this is the
-- primary fix for the nightly poller spending billed Fleet API calls (and waking
-- cars) on vehicles owned by a deactivated account. EXISTS (not a JOIN) keeps the
-- explicit column list scoped to vehicles alone, so the sqlc-generated row struct
-- is unchanged (D14).
SELECT account_id, tesla_id, vin, display_name, access_type, exterior_color, car_type FROM account.vehicles
WHERE status = 'Active'
  AND EXISTS (SELECT 1 FROM account.accounts a WHERE a.id = vehicles.account_id AND a.status = 'Active')
ORDER BY account_id, tesla_id;
