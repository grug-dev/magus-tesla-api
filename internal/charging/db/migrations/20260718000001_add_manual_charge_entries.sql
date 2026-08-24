-- +goose Up
-- manual_charge_entries: user-asserted home/work/third-party charge sessions.
-- Owned by internal/manualcharge; no other module reads this table directly.
--
-- This table IS mutable: users correct hand-typed entries. UPDATE and DELETE are
-- supported via the Writer port. Unlike the append-only vehicle_snapshots /
-- poll_attempts, this table carries updated_at and the Writer port exposes full CRUD
-- (design D4).
--
-- No FK on account_id or tesla_id: a cross-module FK into the account module's tables
-- would couple manualcharge migrations to the account schema — exactly the coupling
-- ai/architecture.md §2 forbids. Referential integrity is upheld by flow: the gateway
-- (Tier 2) resolves the vehicle from the user's own registered vehicles (via the account
-- port) before calling Writer.
--
-- No raw_data JSONB: this table holds user-typed data, not an external API response.
-- The raw_data JSONB rule applies only to external API ingestion tables
-- (ai/go-conventions.md §persistence). User-typed data has no vendor payload to preserve
-- (design D5).
CREATE TABLE manual_charge_entries (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         UUID   NOT NULL,         -- multi-tenant scope
    tesla_id           BIGINT NOT NULL,         -- which of the user's vehicles
    vin                TEXT   NOT NULL,         -- durable vehicle key (survives re-registration)

    -- REQUIRED (user must supply)
    charged_on         DATE          NOT NULL,                         -- the day of the charge
    energy_added_kwh   NUMERIC(6,2)  NOT NULL CHECK (energy_added_kwh > 0),
    price              NUMERIC(14,2) NOT NULL CHECK (price >= 0),
    currency           TEXT          NOT NULL DEFAULT 'COP',

    -- OPTIONAL (user may omit)
    started_at         TIMESTAMPTZ,
    ended_at           TIMESTAMPTZ,
    start_battery_pct  SMALLINT CHECK (start_battery_pct BETWEEN 0 AND 100),
    end_battery_pct    SMALLINT CHECK (end_battery_pct   BETWEEN 0 AND 100),
    charging_type      TEXT CHECK (charging_type IN ('AC','DC')),
    location_kind      TEXT CHECK (location_kind IN ('HOME','WORK','OTHER')),
    location_label     TEXT,                                           -- free text, esp. for OTHER
    notes              TEXT,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Session time sanity: end must not precede start when both are given.
    CHECK (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at)
);

COMMENT ON TABLE manual_charge_entries IS
    'User-asserted home/work/third-party charge sessions not captured by the Tesla Fleet API. '
    'Owned by internal/manualcharge; no other module reads this table directly. '
    'Mutable table: full CRUD via Writer port (users correct hand-typed entries). '
    'No cross-module FK on account_id or tesla_id (ai/architecture.md §2). '
    'No raw_data JSONB column: user-typed data has no vendor payload to preserve '
    '(ai/go-conventions.md §persistence, design D5).';

-- Per-vehicle time-series read path (hot dashboard path: list entries for a specific
-- vehicle belonging to a specific account, ordered newest charged day first).
-- account_id leads the index (multi-tenant convention: every dashboard read scopes by
-- account first — ai/go-conventions.md §persistence, ai/architecture.md §7.3).
-- tesla_id second satisfies both WHERE filters in a range scan.
-- charged_on DESC matches ORDER BY so the planner eliminates the sort step.
-- Design D3 (Read path 1).
CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON manual_charge_entries (account_id, tesla_id, charged_on DESC);

-- Account-wide charge history read path (all entries for a given account, across all
-- vehicles, ordered newest first). account_id is the single WHERE predicate;
-- charged_on DESC eliminates the sort step. Dedicated two-column index keeps the
-- planner's choice unambiguous and cheaper than relying on the per-vehicle index prefix.
-- Design D3 (Read path 2).
CREATE INDEX idx_manual_charge_entries_account_time
    ON manual_charge_entries (account_id, charged_on DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_manual_charge_entries_account_time;
DROP INDEX IF EXISTS idx_manual_charge_entries_vehicle_time;
DROP TABLE IF EXISTS manual_charge_entries;
