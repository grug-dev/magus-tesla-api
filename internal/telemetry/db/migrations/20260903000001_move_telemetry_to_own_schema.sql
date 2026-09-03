-- +goose Up
-- RM39 tier 4 (telemetry-move-to-own-schema, MAG-31): move this module's four tables into
-- a dedicated `telemetry` Postgres schema (roadmap D1), AND rename supercharger_sessions to
-- supercharger_history (roadmap D5a) in the SAME migration.
--
-- STATEMENT ORDER IS MANDATORY (design.md D1). Tier 3 (charging-move-to-own-schema)
-- already moved and renamed charge_sessions -> charging.supercharger_sessions, so the base
-- name `supercharger_sessions` is now charging's. This tier renames AWAY from that base
-- name into `supercharger_history`, so no collision with charging's table is possible in
-- either order -- unlike tier 3, which had to move before renaming to avoid colliding with
-- this module's own then-still-public table. The move-then-rename order is kept anyway so
-- every statement after the move addresses the table by its final schema, and so this tier
-- matches the shape of tiers 1-3.
--
-- `ALTER TABLE ... SET SCHEMA`, `ALTER TABLE ... RENAME TO`, `ALTER INDEX ... RENAME TO`
-- and `ALTER TABLE ... RENAME CONSTRAINT` are all catalog-only operations -- they update
-- pg_class.relnamespace, pg_class.relname and pg_constraint.conname only; no heap or index
-- page is rewritten (see design.md D13 Index Plan). Because migrations run as the app role
-- (Makefile db-setup exports PGUSER=$(APP_ROLE)), CREATE SCHEMA here makes that role the
-- schema owner -- no GRANT needed.
--
-- ROADMAP D12's search_path FIX IS FORBIDDEN HERE (design.md D10): a bare
-- `supercharger_sessions` resolved through a search_path that includes `charging` would
-- find CHARGING's table -- a different table with a different row population -- and
-- succeed while reading the wrong data. Every reference below is qualified explicitly.
CREATE SCHEMA IF NOT EXISTS telemetry;

ALTER TABLE vehicle_snapshots     SET SCHEMA telemetry;
ALTER TABLE supercharger_sessions SET SCHEMA telemetry;
ALTER TABLE poll_attempts         SET SCHEMA telemetry;
ALTER TABLE poll_runs             SET SCHEMA telemetry;

ALTER TABLE telemetry.supercharger_sessions RENAME TO supercharger_history;

-- Rename EVERY catalog object that still carries the old table name (design.md D7,
-- derived from the owner's pg_constraint/pg_indexes query against a migrated database --
-- NOT from reading the CREATE TABLE text). Postgres does NOT auto-rename a constraint or
-- a standalone index when its owning table is renamed.
ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_pkey
    TO supercharger_history_pkey;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_session_id_unique
    TO supercharger_history_session_id_unique;

-- The five CHECK constraints Postgres auto-named `supercharger_sessions_<column>_check`
-- from the inline column CHECKs added by 20260815000001. They exist only in pg_constraint
-- -- no grep over this repository can find them. Nothing in Go, SQL or docs references
-- these names, so the rename is catalog-only and consumer-free; renamed anyway so a CHECK
-- violation never prints a retired table name against `supercharger_history` (design.md D7).
ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_battery_pct_source_check
    TO supercharger_history_battery_pct_source_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_start_battery_pct_check
    TO supercharger_history_start_battery_pct_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_end_battery_pct_check
    TO supercharger_history_end_battery_pct_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_start_battery_pct_est_check
    TO supercharger_history_start_battery_pct_est_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_sessions_end_battery_pct_est_check
    TO supercharger_history_end_battery_pct_est_check;

-- The pkey's and the unique constraint's BACKING indexes are NOT renamed separately --
-- RENAME CONSTRAINT above already renamed both together. Only the two STANDALONE
-- CREATE INDEX objects need ALTER INDEX.
ALTER INDEX telemetry.idx_supercharger_sessions_vehicle_time
    RENAME TO idx_supercharger_history_vehicle_time;

ALTER INDEX telemetry.idx_supercharger_sessions_account_time
    RENAME TO idx_supercharger_history_account_time;

-- Refresh the shipped comments that sqlc copies verbatim into
-- internal/telemetry/db/models.go as Go doc comments. Editing the HISTORIC migrations
-- that first set this text is forbidden (roadmap D1); re-issuing COMMENT ON here is
-- additive and reversible -- a comment is catalog state, so the later statement simply
-- wins (design.md D8). Text is otherwise VERBATIM -- same wording, same R3/D6 citations,
-- same NULL conventions -- with only supercharger_sessions -> supercharger_history and
-- UpsertSuperchargerSession -> UpsertSuperchargerHistory substituted.
COMMENT ON TABLE telemetry.supercharger_history IS
    'Tesla-billed Supercharger and DC fast-charging sessions per account. '
    'Covers sessions returned by GET /api/1/dx/charging/history only (no home/AC '
    'charging). The Tesla API itself carries no battery-percentage field; '
    'start_battery_pct/end_battery_pct/battery_pct_source are a human-owned '
    'verification/override channel, and start_battery_pct_est/end_battery_pct_est '
    'are a frozen write-once snapshot of the estimate at verification time (both '
    'added by RM27 tier 1, MAG-14) -- all five excluded from the nightly UPSERT so '
    'a verified value or its snapshot is never silently overwritten (R3). '
    'Owned by internal/telemetry; no other module reads this table directly. '
    'UPSERT on session_id (not append-only): billing state is mutable post-session.';

COMMENT ON COLUMN telemetry.supercharger_history.start_battery_pct IS
    'Human-verified/override battery % at charge start (0-100). NULL = no override; '
    'reads fall back to internal/battery''s on-read estimate (R5). Excluded from '
    'UpsertSuperchargerHistory''s INSERT and ON CONFLICT DO UPDATE SET -- never '
    'auto-written by the nightly poller (R3).';

COMMENT ON COLUMN telemetry.supercharger_history.start_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'start_battery_pct was verified/overridden -- a permanent drift log entry, not a '
    'cache. Written exactly once, in the same write as the trio; NEVER refreshed '
    'again, including by a later improved taper model (staleness here is correct, '
    'not a bug -- design D6). NEVER read back into internal/battery''s live '
    'computation. Excluded from UpsertSuperchargerHistory like the trio (R3).';

COMMENT ON COLUMN telemetry.supercharger_history.end_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'end_battery_pct was verified/overridden. Same write-once, never-refreshed, '
    'never-a-cache, R3-protected semantics as start_battery_pct_est (design D6).';

-- +goose Down
-- Reverse in the EXACT opposite order of Up: this ordering is what makes CASCADE
-- unnecessary on the final DROP SCHEMA -- every object created inside the schema is moved
-- or renamed back out before the schema itself is dropped, so it is always empty by the
-- time DROP SCHEMA runs.
--
-- Down intentionally does NOT restore the superseded COMMENT ON text -- this module's
-- established precedent (20260815000001's own Down section: "Down intentionally does not
-- restore the pre-migration COMMENT ON TABLE text -- goose Down migrations in this module
-- have never restored superseded comments"). A comment constrains nothing, so leaving it
-- at its newest version is harmless.
ALTER INDEX telemetry.idx_supercharger_history_account_time
    RENAME TO idx_supercharger_sessions_account_time;

ALTER INDEX telemetry.idx_supercharger_history_vehicle_time
    RENAME TO idx_supercharger_sessions_vehicle_time;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_end_battery_pct_est_check
    TO supercharger_sessions_end_battery_pct_est_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_start_battery_pct_est_check
    TO supercharger_sessions_start_battery_pct_est_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_end_battery_pct_check
    TO supercharger_sessions_end_battery_pct_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_start_battery_pct_check
    TO supercharger_sessions_start_battery_pct_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_battery_pct_source_check
    TO supercharger_sessions_battery_pct_source_check;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_session_id_unique
    TO supercharger_sessions_session_id_unique;

ALTER TABLE telemetry.supercharger_history
    RENAME CONSTRAINT supercharger_history_pkey
    TO supercharger_sessions_pkey;

ALTER TABLE telemetry.supercharger_history RENAME TO supercharger_sessions;

ALTER TABLE telemetry.poll_runs             SET SCHEMA public;
ALTER TABLE telemetry.poll_attempts         SET SCHEMA public;
ALTER TABLE telemetry.supercharger_sessions SET SCHEMA public;
ALTER TABLE telemetry.vehicle_snapshots     SET SCHEMA public;

DROP SCHEMA IF EXISTS telemetry;
