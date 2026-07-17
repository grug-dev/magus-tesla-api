-- +goose Up
-- supercharger_sessions: Tesla-billed Supercharger and DC fast-charging sessions
-- per account. Owned by internal/telemetry; no other module reads this table directly.
--
-- This table is NOT append-only (unlike vehicle_snapshots / poll_attempts). Charging
-- session data is mutable post-session: billing finalizes over time (is_paid,
-- invoices, fee status change after session ends). Re-fetching nightly therefore
-- UPSERTs on session_id rather than blindly inserting (design DBS3).
--
-- No FK on account_id or tesla_id: a cross-module FK from supercharger_sessions into
-- the account module's accounts/vehicles tables would couple telemetry migrations to
-- the account schema — exactly the coupling ai/architecture.md §2 forbids. Referential
-- integrity is upheld by the flow: the only writer resolves account_id and tesla_id
-- from account.AllRegisteredVehicles immediately before writing. tesla_id is NULL when
-- the session's VIN does not match any currently-registered vehicle (sold/removed car).
--
-- Covers Supercharger / DC fast-charging sessions returned by
-- GET /api/1/dx/charging/history only; no home/AC charging.
CREATE TABLE supercharger_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id              BIGINT NOT NULL,          -- Tesla's globally-unique session id
    account_id              UUID NOT NULL,            -- owning account (multi-tenant scope)
    vin                     TEXT NOT NULL,            -- durable vehicle key from the session
    tesla_id                BIGINT,                   -- resolved from account's owned vehicles; NULL when VIN is not a current registered vehicle
    site_location_name      TEXT NOT NULL,
    country_code            TEXT NOT NULL,
    charge_start_date_time  TIMESTAMPTZ NOT NULL,
    charge_stop_date_time   TIMESTAMPTZ NOT NULL,
    unlatch_date_time       TIMESTAMPTZ,              -- nullable: not always present in Tesla response

    billing_type            TEXT NOT NULL,
    vehicle_make_type       TEXT NOT NULL,

    -- DERIVED AT WRITE TIME (computed from fees[]; refreshed on upsert)
    energy_kwh              DOUBLE PRECISION,         -- sum of usageBase + usageTier1..4 where lower(uom) = 'kwh'; NULL if no kWh fee
    total_cost              DOUBLE PRECISION,         -- sum of totalDue over all fees; NULL if fees empty
    currency                TEXT,                     -- currencyCode from first fee; NULL if fees empty
    is_paid                 BOOLEAN,                  -- logical AND of every fee's isPaid; NULL if no fees

    raw_data                JSONB NOT NULL,           -- whole session object (fees[] + invoices[]) lossless

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- first-seen; never updated
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- refreshed on every upsert

    -- Dedup / upsert target: session_id is Tesla's globally unique session id.
    -- Also serves as a point-lookup index for direct session_id queries.
    CONSTRAINT supercharger_sessions_session_id_unique UNIQUE (session_id)
);

COMMENT ON TABLE supercharger_sessions IS
    'Tesla-billed Supercharger and DC fast-charging sessions per account. '
    'Covers sessions returned by GET /api/1/dx/charging/history only '
    '(no home/AC charging, no battery percentage). '
    'Owned by internal/telemetry; no other module reads this table directly. '
    'UPSERT on session_id (not append-only): billing state is mutable post-session.';

-- Per-vehicle time-series read path (main per-vehicle dashboard: show sessions for
-- this vehicle, newest first). account_id leads the index (multi-tenant convention:
-- every dashboard read scopes by account first — ai/go-conventions.md §persistence,
-- ai/architecture.md §7.3). tesla_id second lets the planner satisfy both the WHERE
-- filter and the ORDER BY in a single range scan without a sort step.
CREATE INDEX idx_supercharger_sessions_vehicle_time
    ON supercharger_sessions (account_id, tesla_id, charge_start_date_time DESC);

-- Account-wide spend/energy dashboard (all sessions for this account, newest first).
-- Also covers (account_id, vin) orphan fallback via the account_id prefix — VIN can
-- be added as an optional post-filter without an index miss (account_id prunes to a
-- small set). charge_start_date_time DESC matches the ORDER BY so no sort step.
CREATE INDEX idx_supercharger_sessions_account_time
    ON supercharger_sessions (account_id, charge_start_date_time DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_supercharger_sessions_account_time;
DROP INDEX IF EXISTS idx_supercharger_sessions_vehicle_time;
DROP TABLE IF EXISTS supercharger_sessions;
