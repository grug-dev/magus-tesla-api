-- +goose Up
-- RM39 tier 3 (charging-move-to-own-schema, MAG-31): move this module's two tables into a
-- dedicated `charging` Postgres schema (roadmap D1), AND rename charge_sessions to
-- supercharger_sessions (roadmap D5b) in the SAME migration.
--
-- STATEMENT ORDER IS MANDATORY (roadmap D7). Tier 3 runs BEFORE tier 4
-- (RM39-telemetry-move-to-own-schema, blocked on a separate boundary ticket, D6), so
-- internal/telemetry still owns `public.supercharger_sessions` at this point. Renaming
-- charge_sessions to the bare name `supercharger_sessions` while both tables sit in
-- `public` would collide with telemetry's table. Moving this table into the `charging`
-- schema FIRST, then renaming it there, means the two same-named tables coexist under
-- different schema qualifiers (`charging.supercharger_sessions` vs
-- `public.supercharger_sessions`) until tier 4 moves telemetry's copy too. This also frees
-- D5b from D6's block — this rename does not wait for the boundary ticket.
--
-- `ALTER TABLE … SET SCHEMA` and `ALTER TABLE … RENAME TO` / `ALTER INDEX … RENAME TO` /
-- `ALTER TABLE … RENAME CONSTRAINT` are all catalog-only operations (see this file's Index
-- Plan section for the proof: no row, index page, or constraint definition is rewritten).
-- Because migrations run as the app role (Makefile db-setup exports PGUSER=$(APP_ROLE)),
-- CREATE SCHEMA here makes that role the schema owner — no GRANT needed.
CREATE SCHEMA IF NOT EXISTS charging;

ALTER TABLE charge_sessions       SET SCHEMA charging;
ALTER TABLE manual_charge_entries SET SCHEMA charging;

ALTER TABLE charging.charge_sessions RENAME TO supercharger_sessions;

-- Rename EVERY catalog object that still carries the old table name (roadmap D16 —
-- see design.md "Rename scope"). Postgres does NOT auto-rename the index, the CHECK, the
-- implicit primary key, or the unique constraint when the table is renamed.
ALTER INDEX charging.idx_charge_sessions_vehicle_stop
    RENAME TO idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_pct_source_required
    TO supercharger_sessions_pct_source_required;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_pkey
    TO supercharger_sessions_pkey;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_account_session_unique
    TO supercharger_sessions_account_session_unique;

-- The five CHECK constraints Postgres auto-named `charge_sessions_<column>_check` from the
-- inline column CHECKs in 20260823000001 (start/end_battery_pct, their _est siblings, and
-- battery_pct_source). They are not in D16's original four-object list because that list was
-- built from the explicitly-named constraints; these were found by querying pg_constraint on
-- a migrated database. D16's rationale applies to them verbatim: a CHECK violation would
-- otherwise print a retired table name against `supercharger_sessions`. Nothing in Go, SQL or
-- docs references these names, so the rename is catalog-only and consumer-free.

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_battery_pct_source_check
    TO supercharger_sessions_battery_pct_source_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_end_battery_pct_check
    TO supercharger_sessions_end_battery_pct_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_end_battery_pct_est_check
    TO supercharger_sessions_end_battery_pct_est_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_start_battery_pct_check
    TO supercharger_sessions_start_battery_pct_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_start_battery_pct_est_check
    TO supercharger_sessions_start_battery_pct_est_check;

-- Refresh the two shipped column comments that still name the pre-rename table and its
-- pre-rename constraint. sqlc copies these into internal/charging/db/models.go verbatim,
-- so leaving them stale would regenerate the retired vocabulary into the module's
-- most-read generated file on every `make sqlc`. Re-issuing COMMENT ON here is additive
-- and reversible; editing the historic migrations that first set them is forbidden
-- (RM39 D1).
COMMENT ON COLUMN charging.supercharger_sessions.battery_pct_source IS
    'Provenance of start/end_battery_pct: user_verified (a human entered them) or '
    'polled (a future measured-SOC path, not implemented). Required whenever either '
    'percentage is set (supercharger_sessions_pct_source_required). Never ''estimated'' — '
    'an estimate is computed on read and is never persisted here.';

COMMENT ON COLUMN charging.manual_charge_entries.energy_source IS
    'Provenance of energy_added_kwh: USER when the value came from the person, ESTIMATED '
    'when this module derived it from the pack capacity and the battery delta on write '
    '(roadmap D3/D4). Always computed by internal/charging, never accepted from a caller -- '
    'the same shape charging.supercharger_sessions.battery_pct_source already uses. It '
    'exists so a future per-vehicle capacity average (backlog #18) can filter WHERE '
    'energy_source = ''USER'': inferred_capacity_kwh_calc on an ESTIMATED row returns '
    'exactly the capacity constant by algebra, so including such rows would seed that '
    'average with its own output. This fact CANNOT be reconstructed later -- once 31.00 is '
    'stored, a typed value and a derived one are indistinguishable. Not indexed: nothing '
    'predicates on it yet.';

-- +goose Down
-- Reverse in the EXACT opposite order of Up (D7's ordering logic in reverse): undo the
-- constraint/index renames, undo the table rename, THEN SET SCHEMA public for both tables,
-- THEN drop the now-empty schema. A non-empty schema cannot be dropped without CASCADE, and
-- this ordering means CASCADE is never needed.
--
-- Restore the two column comments to the exact text the historic migrations set, so the
-- reverse is byte-faithful.
COMMENT ON COLUMN charging.manual_charge_entries.energy_source IS
    'Provenance of energy_added_kwh: USER when the value came from the person, ESTIMATED '
    'when this module derived it from the pack capacity and the battery delta on write '
    '(roadmap D3/D4). Always computed by internal/charging, never accepted from a caller -- '
    'the same shape charge_sessions.battery_pct_source already uses. It exists so a future '
    'per-vehicle capacity average (backlog #18) can filter WHERE energy_source = ''USER'': '
    'inferred_capacity_kwh_calc on an ESTIMATED row returns exactly the capacity constant by '
    'algebra, so including such rows would seed that average with its own output. This fact '
    'CANNOT be reconstructed later -- once 31.00 is stored, a typed value and a derived one '
    'are indistinguishable. Not indexed: nothing predicates on it yet.';

COMMENT ON COLUMN charging.supercharger_sessions.battery_pct_source IS
    'Provenance of start/end_battery_pct: user_verified (a human entered them) or '
    'polled (a future measured-SOC path, not implemented). Required whenever either '
    'percentage is set (charge_sessions_pct_source_required). Never ''estimated'' — '
    'an estimate is computed on read and is never persisted here.';

-- Reverse the five auto-named column CHECK renames first (opposite order of Up).
ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_start_battery_pct_est_check
    TO charge_sessions_start_battery_pct_est_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_start_battery_pct_check
    TO charge_sessions_start_battery_pct_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_end_battery_pct_est_check
    TO charge_sessions_end_battery_pct_est_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_end_battery_pct_check
    TO charge_sessions_end_battery_pct_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_battery_pct_source_check
    TO charge_sessions_battery_pct_source_check;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_account_session_unique
    TO charge_sessions_account_session_unique;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_pkey
    TO charge_sessions_pkey;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_pct_source_required
    TO charge_sessions_pct_source_required;

ALTER INDEX charging.idx_supercharger_sessions_vehicle_stop
    RENAME TO idx_charge_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions RENAME TO charge_sessions;

ALTER TABLE charging.manual_charge_entries SET SCHEMA public;
ALTER TABLE charging.charge_sessions       SET SCHEMA public;

DROP SCHEMA IF EXISTS charging;
