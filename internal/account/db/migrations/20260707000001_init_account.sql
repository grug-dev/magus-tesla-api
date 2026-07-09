-- +goose Up
-- Initial account-module schema: app users (accounts) and their Tesla connections
-- (tesla_tokens, 1:N). This migration is also the single schema source sqlc reads
-- (see sqlc.yaml) — keep the DDL here, not in a separate schema.sql.
--
-- gen_random_uuid() is built into PostgreSQL core (v13+); no extension required.

CREATE TABLE accounts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email        TEXT NOT NULL,
    provider     TEXT NOT NULL,          -- e.g. 'google'
    provider_id  TEXT NOT NULL,          -- subject id from the provider
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE tesla_tokens (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tesla_email       TEXT,              -- which Tesla account this connection is for
    access_token      TEXT NOT NULL,
    refresh_token     TEXT NOT NULL,
    access_expires_at TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tesla_tokens_account_id ON tesla_tokens (account_id);

-- +goose Down
DROP TABLE IF EXISTS tesla_tokens;
DROP TABLE IF EXISTS accounts;
