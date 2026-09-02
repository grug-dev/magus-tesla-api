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

-- Rename ALL FOUR catalog objects that still carry the old table name (roadmap D16 —
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

-- +goose Down
-- Reverse in the EXACT opposite order of Up (D7's ordering logic in reverse): undo the
-- constraint/index renames, undo the table rename, THEN SET SCHEMA public for both tables,
-- THEN drop the now-empty schema. A non-empty schema cannot be dropped without CASCADE, and
-- this ordering means CASCADE is never needed.
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
