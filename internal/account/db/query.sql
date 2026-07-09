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
