-- +goose Up
-- RM41 tier 3 (telemetry-drop-estimate-columns, MAG-36): drop the two dead
-- battery-percentage ESTIMATE columns from telemetry.supercharger_history.
--
-- WHY THIS IS SAFE (roadmap D1, re-verified by this change, not assumed):
--   - Nothing in this repository has ever written either column. UpsertSuperchargerHistory's
--     INSERT column list and ON CONFLICT DO UPDATE SET clause both omit them (db/query.sql).
--     No Writer for either column, or for the human-owned trio alongside them, has ever
--     existed in this module -- the verification UI that eventually shipped a write path
--     (SessionVerifier.VerifySession) was always charging's, per RM31, never telemetry's.
--   - The estimator this pair was reserved for (a taper-curve SOC estimator RM27 originally
--     planned, plus a gateway page rendering it) was DESCOPED by the owner on 2026-08-15,
--     the same day RM27 shipped -- backlog entry 11. The estimator MAG-36 eventually shipped
--     (derivedStartBatteryPct, internal/charging/capacity.go, 2026-09-01) writes the real
--     start_battery_pct column instead of a frozen verification-time snapshot, so this
--     reserved pair's original purpose no longer applies to any code path, past or present
--     (roadmap D1/D8).
--   - This table's copy of the trio (start_battery_pct, end_battery_pct,
--     battery_pct_source) is UNCHANGED and NOT dropped by this migration -- only the two
--     _est columns go. The trio stays reserved/unwritten here exactly as before this
--     migration; only charging.supercharger_sessions' trio is ever written, by
--     SessionVerifier (RM31). Telemetry's trio remains permanently NULL, same as always.
--   - charging.supercharger_sessions' own copy of these same two columns was already
--     dropped by RM41-charging-drop-estimate-columns (tier 2, migration
--     20260903000002_drop_supercharger_est_columns.sql) -- this migration completes the
--     retirement on the telemetry side; after this migration, neither table has an
--     estimate-pair column anywhere in the platform.
--
-- DROP COLUMN also drops each column's inline CHECK constraint
-- (supercharger_history_start_battery_pct_est_check /
-- supercharger_history_end_battery_pct_est_check, renamed from the
-- supercharger_sessions_* originals by 20260903000001_move_telemetry_to_own_schema.sql)
-- and its COMMENT ON COLUMN -- both are attached to the column's own catalog entry and
-- Postgres removes them automatically. No separate DROP CONSTRAINT statement is needed or
-- correct (roadmap D2).
ALTER TABLE telemetry.supercharger_history
    DROP COLUMN start_battery_pct_est,
    DROP COLUMN end_battery_pct_est;

-- +goose Down
-- Reintroduces both columns with their original type and CHECK, matching
-- 20260815000001_add_supercharger_battery_pct.sql's original column definitions exactly
-- (the CHECK's auto-generated name will resolve against the table's CURRENT name,
-- telemetry.supercharger_history, which is correct -- the table was already moved into
-- the telemetry schema and renamed from supercharger_sessions by 20260903000001 before
-- this migration ever runs).
--
-- NOT a data-preserving reversal: every value either column ever held (always NULL, per
-- the Up comment above) is gone the moment DROP COLUMN runs. Re-adding the columns here
-- restores the SCHEMA shape only, as two new NULL columns -- there is no shadow copy
-- anywhere in this migration to restore actual prior values from. This mirrors
-- charging's own 20260903000002_drop_supercharger_est_columns.sql Down (RM41 tier 2) and
-- this module's own 20260805000001/20260806000001/20260903000001 precedent of a
-- schema-only Down.
ALTER TABLE telemetry.supercharger_history
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);
