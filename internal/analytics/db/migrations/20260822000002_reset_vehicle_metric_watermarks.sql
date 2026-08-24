-- +goose Up
-- internal/analytics — reset the telemetry-snapshot recompute cursor so every
-- vehicle's existing vehicle_metrics rows are rebuilt through the derivation
-- this change moves into this module (MAG-26 RM29 tier 4).
--
-- Recalculator.Reconcile (recalculate.go) treats an ABSENT watermark row as the
-- epoch (tier 3 design D7), so deleting these rows makes the next nightly run
-- query SnapshotsByVehicleUpdatedSince(epoch), see every snapshot the vehicle
-- has, and Recalculate the vehicle's whole history in one pass. No Go code and
-- no one-off binary is involved; the backfill IS the nightly path.
--
-- Only the 'vehicle_snapshots' source is reset. With that cursor at epoch the
-- affected-day span already covers the vehicle's entire history, so the other
-- two sources' contributions are recomputed on the same pass — resetting them
-- too would re-scan two sources for no additional coverage.
--
-- Data-only migration: no schema object is created, altered or dropped.
DELETE FROM vehicle_metric_watermarks
WHERE source = 'vehicle_snapshots';

-- +goose Down
-- Irreversible, and harmless. A deleted cursor row cannot be recovered, but an
-- absent watermark is DEFINED as "epoch" (tier 3 design D7) — the only
-- consequence of a rollback is that the next Reconcile backfills that vehicle's
-- history once more. There is no state to lose and nothing to undo.
SELECT 1;
