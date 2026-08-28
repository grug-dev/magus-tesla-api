-- +goose Up
-- internal/analytics — RM31-analytics-read-sessions-from-charging (MAG-19 tier 3).
--
-- vehicle_metric_watermarks.source is a closed-vocabulary label naming the physical
-- table each of Reconcile's three independent cursors tracks (see this table's own
-- migration, 20260821000002_add_vehicle_metric_watermarks.sql). As of this change,
-- internal/analytics no longer reads internal/telemetry.supercharger_sessions for the
-- Supercharger path -- it reads internal/charging.charge_sessions instead (roadmap
-- Decision 1/8). Leaving the label as 'supercharger_sessions' would let it name a
-- table this module no longer reads, defeating the column's own purpose.
--
-- The 'supercharger_sessions' cursor rows are RESET TO EPOCH (DELETEd), not renamed
-- in place -- roadmap Decision 10, reconfirmed by the owner at the design gate on
-- 2026-08-28. An absent watermark row is DEFINED as the epoch by
-- Recalculator.Reconcile's own watermark() method (tier 3 design D7 of
-- RM29-analytics-add-vehicle-metrics), so the next nightly Reconcile for each
-- affected vehicle backfills that source's entire history from charge_sessions in
-- one pass -- no separate backfill migration or one-off binary, exactly the
-- 20260822000002_reset_vehicle_metric_watermarks.sql precedent.
--
-- This is nearly free, not merely correct: charging.MirrorSessions sets
-- updated_at = now() UNCONDITIONALLY on every nightly upsert into charge_sessions,
-- the same "last mirror pass touched this row" meaning
-- telemetry.supercharger_sessions.updated_at already carried -- so the resulting
-- full re-read touches nothing the vehicle's data hasn't already been reconciled
-- against in substance (design.md Sec 2b). DELETE, not UPDATE, is still the right
-- call: carrying the cursor value forward would make the migration's correctness
-- depend on the nightly mirror having actually run without a gap -- an operational
-- fact this migration cannot verify, and a stalled mirror would strand a
-- carried-over cursor with nothing able to detect it. A single redundant backfill
-- pass is the cheaper, self-correcting failure mode (design.md Sec 2b/2c).
--
-- Only the supercharger_sessions -> charge_sessions source is reset here;
-- vehicle_snapshots and manual_charge_entries rows are untouched.
--
-- Ordering (design.md Sec 2d): DROP the constraint before the DELETE (uniform with
-- every other step of this migration, though a DELETE cannot itself violate a CHECK
-- constraint); ADD the new constraint only once every remaining row already
-- satisfies it, so the ADD cannot fail.
ALTER TABLE vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM vehicle_metric_watermarks
WHERE source = 'supercharger_sessions';

ALTER TABLE vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries'));

COMMENT ON TABLE vehicle_metric_watermarks IS
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

COMMENT ON COLUMN vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''charge_sessions'' (internal/charging, as '
    'of RM31-analytics-read-sessions-from-charging -- previously ''supercharger_sessions'' in '
    'internal/telemetry), or ''manual_charge_entries'' (internal/charging). No FK -- a '
    'free-standing string label, mirroring charge_gaps.missing_charging_type''s identical '
    'convention.';

-- +goose Down
-- IRREVERSIBLE for the deleted rows, and HARMLESS -- exact precedent:
-- 20260822000002_reset_vehicle_metric_watermarks.sql's own Down. A DELETE cannot be
-- undone by an UPDATE (there is no row left to update, and no source_updated_at
-- value recorded anywhere to restore even if there were). The only consequence of
-- rolling back is that the next Reconcile for an affected vehicle backfills the
-- supercharger_sessions/charge_sessions source once more from epoch -- an absent
-- watermark row is DEFINED as the epoch (tier 3 design D7), so there is no state to
-- lose and nothing to actually undo, only a redundant recompute pass.
--
-- What Down DOES restore: the CHECK constraint's old vocabulary and the original
-- table/column COMMENTs, so a rollback leaves the schema exactly as it was before Up
-- -- only the deleted rows themselves are unrecoverable, and their absence degrades
-- to "epoch," never to an error.
--
-- THE DELETE BELOW IS LOAD-BEARING, NOT TIDYING. Restoring the old vocabulary while a
-- single source = 'charge_sessions' row exists makes the ADD CONSTRAINT fail with
-- SQLSTATE 23514, leaving the table with NO constraint at all. And such rows are the
-- normal case, not an edge case: Reconcile calls advanceWatermark(..., 
-- sourceChargeSessions, ...) on every pass (recalculate.go), so the first nightly run
-- after Up creates them. Without this DELETE the Down is unrunnable in production from
-- that moment on. It mirrors Up's own DELETE exactly -- each direction clears the rows
-- written under the vocabulary the other direction retires -- and rests on the same D7
-- argument: an absent cursor IS the epoch, so the cost is one redundant backfill pass.
-- Found by this change's own T1 round-trip test, which is why T1 asserts the round trip
-- rather than only the forward migration.
ALTER TABLE vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM vehicle_metric_watermarks
WHERE source = 'charge_sessions';

ALTER TABLE vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries'));

COMMENT ON TABLE vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly.';

COMMENT ON COLUMN vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/telemetry), '
    'or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, '
    'mirroring charge_gaps.missing_charging_type''s identical convention.';
