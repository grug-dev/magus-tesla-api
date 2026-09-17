-- account baseline.
--
-- One self-contained file that creates this module's whole schema. It is a
-- verbatim transcription of a pg_dump --schema-only taken on 2026-09-17, after
-- the account module's migration history was squashed.
--
-- It creates objects and reads nothing, so it depends on no other module and can
-- be applied in any order relative to them. Keep it that way: a migration here
-- must never name another module's schema.
--
-- Existing databases (dev and prod) have this version recorded as applied without
-- it ever running. So a change made HERE reaches new databases only. Anything that
-- must also reach dev and prod belongs in a later, ordinary migration.

-- +goose Up

-- SCHEMA: account
CREATE SCHEMA account;

-- TABLE: accounts
CREATE TABLE account.accounts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email text NOT NULL,
    provider text NOT NULL,
    provider_id text NOT NULL,
    display_name text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    status text DEFAULT 'Inactive'::text NOT NULL,
    CONSTRAINT accounts_status_check CHECK ((status = ANY (ARRAY['Active'::text, 'Inactive'::text])))
);

-- TABLE: settings
CREATE TABLE account.settings (
    account_id uuid NOT NULL,
    language text DEFAULT 'es'::text NOT NULL,
    theme text DEFAULT 'graphite'::text NOT NULL,
    analysis_start_date date NOT NULL
);

-- TABLE: tesla_tokens
CREATE TABLE account.tesla_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    tesla_email text,
    access_token text NOT NULL,
    refresh_token text NOT NULL,
    access_expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- TABLE: vehicles
CREATE TABLE account.vehicles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    tesla_id bigint NOT NULL,
    vin text NOT NULL,
    display_name text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    access_type text,
    exterior_color text,
    car_type text,
    status text DEFAULT 'Active'::text NOT NULL,
    CONSTRAINT vehicles_access_type_check CHECK (((access_type IS NULL) OR (access_type = ANY (ARRAY['OWNER'::text, 'DRIVER'::text])))),
    CONSTRAINT vehicles_status_check CHECK ((status = ANY (ARRAY['Active'::text, 'Inactive'::text])))
);

-- CONSTRAINT: accounts accounts_pkey
ALTER TABLE ONLY account.accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);

-- CONSTRAINT: accounts accounts_provider_provider_id_key
ALTER TABLE ONLY account.accounts
    ADD CONSTRAINT accounts_provider_provider_id_key UNIQUE (provider, provider_id);

-- CONSTRAINT: settings settings_pkey
ALTER TABLE ONLY account.settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (account_id);

-- CONSTRAINT: tesla_tokens tesla_tokens_account_id_key
ALTER TABLE ONLY account.tesla_tokens
    ADD CONSTRAINT tesla_tokens_account_id_key UNIQUE (account_id);

-- CONSTRAINT: tesla_tokens tesla_tokens_pkey
ALTER TABLE ONLY account.tesla_tokens
    ADD CONSTRAINT tesla_tokens_pkey PRIMARY KEY (id);

-- CONSTRAINT: vehicles vehicles_account_id_tesla_id_key
ALTER TABLE ONLY account.vehicles
    ADD CONSTRAINT vehicles_account_id_tesla_id_key UNIQUE (account_id, tesla_id);

-- CONSTRAINT: vehicles vehicles_pkey
ALTER TABLE ONLY account.vehicles
    ADD CONSTRAINT vehicles_pkey PRIMARY KEY (id);

-- INDEX: idx_vehicles_vin
CREATE INDEX idx_vehicles_vin ON account.vehicles USING btree (vin);

-- FK CONSTRAINT: settings settings_account_id_fkey
ALTER TABLE ONLY account.settings
    ADD CONSTRAINT settings_account_id_fkey FOREIGN KEY (account_id) REFERENCES account.accounts(id) ON DELETE CASCADE;

-- FK CONSTRAINT: tesla_tokens tesla_tokens_account_id_fkey
ALTER TABLE ONLY account.tesla_tokens
    ADD CONSTRAINT tesla_tokens_account_id_fkey FOREIGN KEY (account_id) REFERENCES account.accounts(id) ON DELETE CASCADE;

-- FK CONSTRAINT: vehicles vehicles_account_id_fkey
ALTER TABLE ONLY account.vehicles
    ADD CONSTRAINT vehicles_account_id_fkey FOREIGN KEY (account_id) REFERENCES account.accounts(id) ON DELETE CASCADE;

-- +goose Down
-- +goose StatementBegin
-- Refusing is deliberate. Reversing this file would mean dropping the whole
-- account schema and every row in it. An empty rollback would be worse: goose
-- would mark the file un-applied while every object still exists, and the next
-- forward run would fail on "relation already exists" -- which stops the web and
-- poller containers, because both wait for the migration step to succeed.
DO $$
BEGIN
    RAISE EXCEPTION 'the account baseline is not reversible: recreate the database instead of rolling it back';
END
$$;
-- +goose StatementEnd
