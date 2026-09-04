-- +goose Up
-- RM41 tier 2 (charging-drop-estimate-columns, MAG-36): drop the two dead
-- battery-percentage ESTIMATE columns from charging.supercharger_sessions.
--
-- WHY THIS IS SAFE (roadmap D1, re-verified by this change, not assumed):
--   - Nothing in this repository has ever written either column. MirrorSuperchargerSession's
--     INSERT column list and ON CONFLICT DO UPDATE SET clause both omit them (db/query.sql);
--     VerifySuperchargerSession's SET clause omits them too. SessionMirror has no field for
--     either, so the nightly sync could not write one even if this exclusion were ever
--     accidentally reversed (RM29 design.md D6 "protection by compile error").
--   - The ONE place either column was ever written is the one-time backfill statement inside
--     the historic 20260823000001_add_charge_sessions.sql migration (never edited — RM39 D1),
--     which copied s.start_battery_pct_est/s.end_battery_pct_est FROM
--     telemetry.supercharger_history AT THAT MIGRATION'S APPLY TIME. Telemetry's own copies
--     are equally permanently NULL (telemetry/db/query.sql's own UPSERT excludes them by the
--     identical guarding-comment pattern), so that backfill only ever copied NULL into NULL.
--     There is no code path, past or present, that put a non-NULL value in either column.
--   - The estimator originally intended to eventually populate them
--     (RM27 D11 deferred it) shipped 2026-09-01 as charging-add-derived-start-battery-pct,
--     writing the real start_battery_pct column instead of a frozen snapshot. The columns
--     are permanently unreachable dead schema, not merely currently-unused schema.
--
-- DROP COLUMN also drops each column's inline CHECK constraint
-- (supercharger_sessions_start_battery_pct_est_check /
-- supercharger_sessions_end_battery_pct_est_check, renamed from the charge_sessions_*
-- originals by 20260902000003) and its COMMENT ON COLUMN — both are attached to the
-- column's own catalog entry and Postgres removes them automatically. No separate
-- DROP CONSTRAINT statement is needed or correct (roadmap D2).
ALTER TABLE charging.supercharger_sessions
    DROP COLUMN start_battery_pct_est,
    DROP COLUMN end_battery_pct_est;

-- +goose Down
-- Reintroduces both columns with their original type and CHECK, matching
-- 20260823000001_add_charge_sessions.sql's original column definitions exactly
-- (the CHECK's auto-generated name will resolve against the table's CURRENT name,
-- supercharger_sessions, which is correct — the table was already renamed by
-- 20260902000003 before this migration ever runs).
--
-- NOT a data-preserving reversal: every value either column ever held (always NULL,
-- per the Up comment above) is gone the moment DROP COLUMN runs. Re-adding the
-- columns here restores the SCHEMA shape only, as two new NULL columns — there is
-- no shadow copy anywhere in this migration to restore actual prior values from.
-- This is consistent with every historic Down in this module (compare
-- 20260902000003's own Down comment) and is not a regression introduced by this
-- migration.
ALTER TABLE charging.supercharger_sessions
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);
