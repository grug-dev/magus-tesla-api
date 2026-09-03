-- +goose Up
-- RM41 tier 4 (charging-add-session-status, MAG-45): a Supercharger session gains a
-- stored, auto-set lifecycle status, mirroring manual_charge_entries.status's shape
-- (20260829000002_add_entry_status.sql) with three codes instead of two.
--
-- WHY TEXT + CHECK, not a Postgres ENUM -- identical rationale to 20260829000002's own:
-- consistency with every other lifecycle/provenance column on these two tables
-- (manual_charge_entries.status, manual_charge_entries.energy_source,
-- supercharger_sessions.battery_pct_source), and altering an enum's value set later is
-- painful (ALTER TYPE ... ADD VALUE cannot run inside a transaction block before PG12,
-- and a value can never be removed).
--
-- WHY THE BACKFILL IS A SEPARATE STATEMENT FROM THE COLUMN DEFAULT (owner's decision,
-- confirmed twice -- see this change's design.md "Rationale" for the recompute-mismatch
-- evidence the leader showed the owner before this was confirmed). The column's own
-- DEFAULT must be 'IN_PROGRESS': every FUTURE session mirrored by
-- SessionWriter.MirrorSessions starts with no battery-percentage data at all
-- (SessionMirror carries no percentage field, structurally -- RM29 design.md D6), and
-- IN_PROGRESS is the only honest state for a session nobody has looked at yet. But the
-- owner wants every PRE-EXISTING row -- data the sync could never have supplied, entirely
-- human-reconstructed -- recorded as DONE_CALCULATED, regardless of what a recompute
-- against its actual start_battery_pct/end_battery_pct would say. Because the intended
-- DEFAULT (IN_PROGRESS, for future rows) and the intended backfill value
-- (DONE_CALCULATED, for existing rows) differ, ADD COLUMN ... DEFAULT alone cannot do
-- both jobs -- unlike 20260829000002's status/energy_source columns, whose backfill
-- value WAS their intended default. This migration needs the two-step shape
-- 20260720000001 used (add, then a separate backfill statement), not 20260829000002's
-- one-step shape.
--
-- The bare UPDATE below (no WHERE clause) runs inside this migration's own transaction,
-- immediately after the ADD COLUMN, so it touches every row that existed when this
-- migration started and none that could not yet exist (there is no concurrent writer
-- inside one transaction). It is NOT metadata-only -- unlike the ADD COLUMN ... DEFAULT
-- above it, which is catalog-only on PostgreSQL 11+, this UPDATE performs a real
-- row-by-row rewrite. Acceptable per the Performance-Profile (writes may be slower;
-- this table holds one row per Supercharger session per account -- low volume -- and
-- this runs once, at deploy time, never on a read path).
ALTER TABLE charging.supercharger_sessions
    ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS'
        CHECK (status IN ('IN_PROGRESS','DONE_CALCULATED','DONE'));

UPDATE charging.supercharger_sessions SET status = 'DONE_CALCULATED';

COMMENT ON COLUMN charging.supercharger_sessions.status IS
    'Lifecycle status of this session''s battery-percentage data, auto-computed by '
    'SessionVerifier.VerifySession on every call, never accepted from a caller: '
    'IN_PROGRESS when either start_battery_pct or end_battery_pct is NULL, '
    'DONE_CALCULATED when both are present and this call derived start_battery_pct via '
    'derivedStartBatteryPct rather than storing a caller-supplied value, DONE when both '
    'are present and start_battery_pct was supplied directly. Every row that existed '
    'before this migration was backfilled to DONE_CALCULATED unconditionally -- an '
    'owner decision about data provenance the stored percentages themselves cannot show, '
    'not a recompute of this rule against their actual values (see '
    'RM41-charging-add-session-status design.md "Rationale"). Not indexed: no read '
    'query in this tier filters, orders, or joins by it.';

-- +goose Down
-- Non-guarded, unlike 20260829000002's Down: dropping this column has no NOT NULL
-- restoration to protect against -- nothing else in the schema depends on its presence,
-- and there is no invalid intermediate state a rollback could produce. It IS lossy: the
-- DONE vs DONE_CALCULATED distinction (which start percentage was typed vs derived) is
-- not reconstructable from start_battery_pct/end_battery_pct alone -- the identical
-- limitation battery_pct_source already has for "was this pair typed or derived at
-- all" (MAG-36 design.md D1) -- but that loss needs no guard because it simply forgets
-- a fact rather than leaving the table in a state some other constraint would reject.
ALTER TABLE charging.supercharger_sessions DROP COLUMN IF EXISTS status;
