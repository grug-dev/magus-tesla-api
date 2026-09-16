-- +goose Up
-- A manual charge entry gains a provenance marker for its starting battery
-- percentage, matching the one energy_added_kwh and price already carry.
--
-- NULLABLE, NO DEFAULT -- unlike energy_source and price_source, which are
-- NOT NULL with a fallback value. A missing starting percentage has no
-- provenance to record: there is no third "absent" value that belongs in the
-- CHECK set, and a DEFAULT would falsely claim provenance for a row that
-- carries none.
--
-- BACKFILL: every existing row with a non-NULL starting percentage is set to
-- 'USER'. This is exact, not an approximation -- no derivation existed before
-- this column, so every one of those percentages was typed by a person.
--
-- TEXT + CHECK, NOT A POSTGRES ENUM: this table already models location_kind,
-- charging_type, status, energy_source and price_source the same way, and an
-- enum's value set is painful to widen later.
--
-- NO CROSS-COLUMN CHECK tying this column's nullness to start_battery_pct's.
-- The pairing is real (a NULL percentage should always have a NULL source) but
-- it is enforced in Go, at the single place that writes both columns, not in
-- the database: a same-transaction, same-statement invariant across two plain
-- columns does not need a CHECK to hold, and the CHECK expression itself would
-- have to reference two columns for a rule this module already guarantees by
-- construction.
--
-- COST: the ADD COLUMN is METADATA-ONLY on PostgreSQL 11+ -- no DEFAULT means
-- no value at all is written into existing rows by the ALTER itself. The
-- UPDATE rewrites only the rows it touches (every row with a non-NULL starting
-- percentage) and takes no stronger a lock than any ordinary UPDATE on this
-- table already takes.

ALTER TABLE charging.manual_charge_entries
    ADD COLUMN start_battery_source TEXT
        CHECK (start_battery_source IN ('USER','ESTIMATED'));

UPDATE charging.manual_charge_entries
   SET start_battery_source = 'USER'
 WHERE start_battery_pct IS NOT NULL;

COMMENT ON COLUMN charging.manual_charge_entries.start_battery_source IS
    'Provenance of start_battery_pct: USER when the person typed it, ESTIMATED '
    'when this module derived it from the energy added and the ending '
    'percentage. NULL exactly when start_battery_pct is NULL -- a missing '
    'percentage has no provenance. Always computed by internal/charging, never '
    'accepted from a caller -- the same shape energy_source and price_source '
    'already use. Historical rows were backfilled at migration time: every row '
    'with a non-NULL start_battery_pct is USER, because no derivation existed '
    'before this column. Not indexed: the one query that filters on it already '
    'scans a bounded period range with no supporting index of its own.';

-- +goose Down
-- LOSSY, not guarded -- there is no NOT NULL and no generated column here for
-- a restored constraint to violate, so DROP COLUMN always succeeds.
--
-- The loss is real but bounded and fully recomputable for every row that
-- existed at the time of this Down: a non-NULL start_battery_pct's provenance
-- is trivially 'USER' again by re-running the Up migration's own backfill
-- rule. A row whose provenance became ESTIMATED after this Up ran is
-- indistinguishable from a typed one once the column is gone -- the same
-- category of accepted loss this table's other backfilled columns already
-- carry on their own Down migrations.
ALTER TABLE charging.manual_charge_entries DROP COLUMN IF EXISTS start_battery_source;
