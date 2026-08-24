-- +goose Up
-- charge_gaps: nightly-detected vehicle-days whose battery math does not add
-- up -- a charge record is missing or incomplete (D3/D7/D7a/D7b of
-- RM28-telemetry-add-charge-gap-storage, MAG-15,
-- openspec/roadmaps/RM28-battery-consumed-graph.md). Owned by
-- internal/telemetry; written through the GapWriter port by internal/battery
-- (D3 -- battery derives the day's consumption and detects the gap, telemetry
-- only stores the conclusion; telemetry never calls battery, so the one-way
-- battery -> telemetry dependency this module's design already assumes is
-- preserved, not inverted). Read by a future notification feature -- out of
-- scope in this change; no read port for this table exists yet.
--
-- ONE ROW PER VEHICLE-DAY, not one row per (day, missing_charging_type): a
-- day's corrected battery-consumed figure is a single aggregate number
-- (battery_used_pct_calc + sum of that day's charge deltas across BOTH
-- sources, roadmap D13) -- when it does not add up, that is one observed
-- shortfall, not two independently-observed ones. A per-type key would let
-- one date carry both a MANUAL and a SUPERCHARGER row, but the two sources
-- cannot actually be told apart from a single combined shortfall; a second
-- row would be a fabricated finding, not an observed one.
-- missing_charging_type therefore describes WHICH source the one row's gap
-- is attributed to (D7a), not part of the row's identity.
--
-- NO resolved_at / soft delete: the write port (GapWriter.ReconcileWindow,
-- D7b) UPSERTs every day that still flags and DELETES every previously-
-- stored day, within the window it just recomputed, that no longer flags.
-- Fixing a charge entry clears the row on the very next nightly run with no
-- extra wiring. This table is a live worklist ("what is outstanding right
-- now"), not an audit trail of resolved gaps -- see design.md's D-Table2 for
-- the full rationale and the rejected resolved_at alternative.
--
-- tesla_id is NOT NULL here even though it is NULLABLE on
-- supercharger_sessions.tesla_id: a session/vehicle that cannot be
-- attributed to a currently-registered vehicle is filtered out of
-- internal/battery's derivation before gap detection ever runs, so it can
-- never produce a charge_gaps row with an unresolved vehicle -- every row
-- this table will ever hold already has a resolved tesla_id by construction
-- (design.md D-Table3).
--
-- No FK on account_id or tesla_id: a cross-module FK from charge_gaps into
-- the account module's accounts/vehicles tables would couple telemetry
-- migrations to the account schema -- exactly the coupling
-- ai/architecture.md §2 forbids. Referential integrity is upheld by flow,
-- mirroring manual_charge_entries'/supercharger_sessions' identical
-- precedent (internal/manualcharge/db/migrations/
-- 20260718000001_add_manual_charge_entries.sql:10-14,
-- internal/telemetry/db/migrations/
-- 20260716000001_add_supercharger_sessions.sql:10-14): the only writer
-- (internal/battery, via cmd/poller, D4/D4a) resolves account_id/tesla_id
-- from account.AllRegisteredVehicles before ever calling
-- GapWriter.ReconcileWindow.
--
-- No raw_data JSONB: this table stores a Go-computed conclusion
-- (internal/battery's derivation), not an external API response. The
-- raw_data JSONB rule (ai/go-conventions.md §persistence) applies only to
-- tables that ingest an external API payload -- mirrors
-- manual_charge_entries' identical "no raw_data" precedent for its own
-- user-typed, non-vendor data (design D5 of that migration).
CREATE TABLE charge_gaps (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             UUID NOT NULL,             -- multi-tenant scope
    tesla_id               BIGINT NOT NULL,            -- which of the account's vehicles; never NULL (see header)
    vin                    TEXT NOT NULL,              -- durable vehicle key, recorded at detection time
    gap_date               DATE NOT NULL,              -- the flagged calendar day (plain DATE -- no time-of-day component)
    missing_charging_type  TEXT NOT NULL CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER')),

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),  -- when this vehicle-day was FIRST flagged
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),  -- refreshed every nightly run this day is RE-flagged

    CONSTRAINT charge_gaps_account_tesla_date_unique UNIQUE (account_id, tesla_id, gap_date)
);

COMMENT ON TABLE charge_gaps IS
    'Nightly-detected vehicle-days whose battery math does not add up -- a charge '
    'record is missing or incomplete (RM28-telemetry-add-charge-gap-storage, MAG-15). '
    'One row per (account_id, tesla_id, gap_date): a day''s shortfall is a single '
    'aggregate observation, never split across two rows. Written by '
    'internal/battery through the GapWriter port (telemetry never calls battery). '
    'No resolved_at / soft delete: a day that stops flagging is DELETED by the next '
    'nightly reconciliation, not marked resolved -- this table is a live worklist, '
    'not an audit trail. Owned by internal/telemetry; no other module reads this '
    'table directly.';

COMMENT ON COLUMN charge_gaps.tesla_id IS
    'Always resolved and NOT NULL: a vehicle that cannot be attributed to a '
    'currently-registered vehicle is filtered out of internal/battery''s derivation '
    'before gap detection runs, unlike supercharger_sessions.tesla_id which is '
    'nullable for exactly that unattributed case.';
COMMENT ON COLUMN charge_gaps.missing_charging_type IS
    'Which charge source is suspected missing for this day (D7a): SUPERCHARGER when '
    'a Supercharger session exists that day with NULL start/end battery percentages '
    '(the exact record that needs filling is already known); MANUAL otherwise (the '
    'vehicle was charged somewhere the Tesla Fleet API does not report). Inferred by '
    'internal/battery at detection time, never user-chosen.';
COMMENT ON COLUMN charge_gaps.created_at IS
    'When this (account_id, tesla_id, gap_date) was FIRST flagged. Preserved across every '
    'subsequent nightly re-upsert of the same still-flagged day -- NOT refreshed on '
    'conflict -- so it answers "how long has this been outstanding" for a future '
    'notification consumer.';
COMMENT ON COLUMN charge_gaps.updated_at IS
    'When this row was last confirmed still-flagging by a nightly run. Refreshed to '
    'now() on every UPSERT conflict; a day that stops flagging is deleted outright '
    'rather than leaving a stale updated_at behind.';

-- Read path 1 (nightly reconciliation, GapWriter.ReconcileWindow): the write
-- port needs "every existing charge_gaps row for this vehicle whose date
-- falls in [start, end]" to compute what to delete, and every UPSERT
-- conflicts on exactly this constraint. This is the SAME index Postgres
-- builds automatically to enforce charge_gaps_account_tesla_date_unique --
-- (account_id, tesla_id, gap_date) -- so it costs nothing beyond what the
-- constraint already requires; no separate CREATE INDEX for this path. See
-- design.md's Index Plan for the full read-pattern justification.
--
-- Read path 2 (future notification: "outstanding gaps for one account", no
-- tesla_id predicate) is served by this dedicated index instead: account_id
-- leads (multi-tenant convention: every dashboard read in this project scopes
-- by account first -- ai/go-conventions.md §persistence, ai/architecture.md
-- §7.3), and gap_date DESC anticipates a newest-first ordering, matching every
-- other time-ordered index in this module (idx_supercharger_sessions_account_time,
-- idx_manual_charge_entries_account_time). See design.md's Index Plan for why
-- the UNIQUE constraint's own index does not serve this second pattern as
-- efficiently.
CREATE INDEX idx_charge_gaps_account
    ON charge_gaps (account_id, gap_date DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_charge_gaps_account;
DROP TABLE IF EXISTS charge_gaps;
