-- +goose Up
-- internal/analytics — RM39 tier 3b (RM39-analytics-fix-watermark-vocabulary, MAG-31).
--
-- vehicle_metric_watermarks.source is a closed-vocabulary label naming the physical
-- table each of Reconcile's three independent cursors tracks (see this table's own
-- migration, 20260821000002_add_vehicle_metric_watermarks.sql, and the prior rewrite,
-- 20260828000001_migrate_vehicle_metric_watermarks_source.sql). RM39 tier 3
-- (RM39-charging-move-to-own-schema, archived) renamed internal/charging's Supercharger
-- table from charge_sessions to supercharger_sessions (roadmap D5b). Leaving this
-- column's vocabulary at 'charge_sessions' would let it keep naming a table that no
-- longer exists under that name -- defeating the column's own documented purpose.
--
-- The 'charge_sessions' cursor rows are RESET TO EPOCH (DELETEd), not renamed in place --
-- roadmap D8/D15, mirroring 20260828000001's own precedent exactly. An absent watermark
-- row is DEFINED as the epoch by Recalculator.Reconcile's own watermark() method (design
-- D7 of RM29-analytics-add-vehicle-metrics), so the next nightly Reconcile for each
-- affected vehicle backfills that source's entire history from charging.supercharger_
-- sessions in one pass -- no separate backfill migration or one-off binary, and no
-- vehicle_metrics/charge_gaps row is touched, only this bookkeeping cursor.
--
-- DELETE, not UPDATE: carrying the cursor value forward would make the migration's
-- correctness depend on charging's own mirror pass having run without a gap between the
-- rename landing and this migration applying -- an operational fact this migration cannot
-- verify. A single redundant backfill pass on the next Reconcile is the cheaper,
-- self-correcting failure mode (exact precedent: 20260828000001's own reasoning).
--
-- Reused-string note (design.md "Reused-String Ambiguity"): 'supercharger_sessions' is
-- the SAME literal this column held before 20260828000001, when it named
-- internal/telemetry's table instead. No row currently holds that value -- the CHECK
-- constraint has forbidden it since 20260828000001 landed -- so this is not a data
-- collision. It IS a live documentation concern until roadmap tier 4 renames telemetry's
-- table to supercharger_history: until then, two different tables in this database
-- (public.supercharger_sessions and charging.supercharger_sessions) share this bare name,
-- and this column's own value cannot schema-qualify itself (tier 2 decision: these are
-- data, not table references). See this file's COMMENT ON column.source below, and
-- design.md, for the disambiguation a reader must rely on instead.
--
-- Only vehicle_metric_watermarks.charge_sessions rows are affected; vehicle_snapshots and
-- manual_charge_entries rows are untouched.
--
-- Ordering (mirrors 20260828000001 exactly): DROP the constraint before the DELETE
-- (uniform with every other step, though a DELETE cannot itself violate a CHECK
-- constraint); ADD the new constraint only once every remaining row already satisfies it,
-- so the ADD cannot fail. Every reference below is schema-qualified
-- (analytics.vehicle_metric_watermarks) -- unlike 20260828000001, which predates tier 2's
-- schema move and correctly stays bare for its own historical moment (D1/D9: historic
-- migrations are never edited).
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'charge_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries'));

COMMENT ON TABLE analytics.vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly. source''s vocabulary was migrated vehicle_snapshots/supercharger_sessions/manual_'
    'charge_entries -> vehicle_snapshots/charge_sessions/manual_charge_entries by '
    '20260828000001 (RM31-analytics-read-sessions-from-charging), then back to '
    'vehicle_snapshots/supercharger_sessions/manual_charge_entries by THIS migration '
    '(RM39-analytics-fix-watermark-vocabulary, tier 3b) once internal/charging renamed its own '
    'table to supercharger_sessions (RM39 tier 3, D5b). The reused string now names a DIFFERENT '
    'physical table (charging.supercharger_sessions) than it did before 20260828000001 '
    '(telemetry.supercharger_sessions) -- see COMMENT ON COLUMN .source for the disambiguation.';

COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/charging, '
    'AS OF RM39-analytics-fix-watermark-vocabulary -- this is a REUSED string; before RM31 '
    '(20260828000001) the same literal named internal/telemetry''s table instead, and until '
    'RM39 tier 4 renames that table to supercharger_history, a DIFFERENT, still-live table '
    '(public.supercharger_sessions) shares this bare name in this same database. This column '
    'never schema-qualifies its own value (RM39 tier 2 decision -- these are data, not table '
    'references), so a reader relies on this comment, internal/analytics/AGENTS.md, and '
    'recalculate.go''s sourceSuperchargerSessions constant to know which table is meant: always '
    'internal/charging''s, never internal/telemetry''s, for this column, in every era after RM31.), '
    'or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, '
    'mirroring charge_gaps.missing_charging_type''s identical convention.';

-- +goose Down
-- IRREVERSIBLE for the deleted rows, and HARMLESS -- exact precedent: 20260828000001's own
-- Down, and before it 20260822000002_reset_vehicle_metric_watermarks.sql's. A DELETE
-- cannot be undone by an UPDATE (there is no row left to update, and no source_updated_at
-- value recorded anywhere to restore even if there were). The only consequence of rolling
-- back is that the next Reconcile for an affected vehicle backfills the charging-sourced
-- watermark once more from epoch -- an absent watermark row is DEFINED as the epoch
-- (design D7), so there is no state to lose and nothing to actually undo, only a
-- redundant recompute pass.
--
-- What Down DOES restore: the CHECK constraint's old vocabulary and the original
-- table/column COMMENTs, so a rollback leaves the schema exactly as it was before Up --
-- only the deleted rows themselves are unrecoverable, and their absence degrades to
-- "epoch," never to an error.
--
-- THE DELETE BELOW IS LOAD-BEARING, NOT TIDYING -- identical reasoning to
-- 20260828000001's own Down. Restoring the old vocabulary while a single
-- source = 'supercharger_sessions' row exists makes the ADD CONSTRAINT fail with
-- SQLSTATE 23514, leaving the table with NO constraint at all. Such rows are the normal
-- case, not an edge case: Reconcile calls advanceWatermark(..., sourceSuperchargerSessions,
-- ...) on every pass (recalculate.go), so the first nightly run after Up creates them.
-- Without this DELETE the Down is unrunnable in production from that moment on. It
-- mirrors Up's own DELETE exactly -- each direction clears the rows written under the
-- vocabulary the other direction retires. Verified by this change's own T1 round-trip
-- test (design.md §5), exactly as 20260828000001's own T1 test first found this.
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'supercharger_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries'));

COMMENT ON TABLE analytics.vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, charge_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly. source''s vocabulary was migrated from supercharger_sessions to charge_sessions '
    'by RM31-analytics-read-sessions-from-charging (MAG-19 tier 3) when the Supercharger read '
    'moved from internal/telemetry to internal/charging; existing supercharger_sessions cursor '
    'rows were reset to epoch (DELETEd), not renamed in place (roadmap Decision 10; design.md '
    'Sec 2b/2c of that change).';

COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''charge_sessions'' (internal/charging, as '
    'of RM31-analytics-read-sessions-from-charging -- previously ''supercharger_sessions'' in '
    'internal/telemetry), or ''manual_charge_entries'' (internal/charging). No FK -- a '
    'free-standing string label, mirroring charge_gaps.missing_charging_type''s identical '
    'convention.';
