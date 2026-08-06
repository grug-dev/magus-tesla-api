-- +goose Up
-- internal/telemetry — dedupe vehicle_snapshots to at most one row per
-- (account_id, tesla_id, captured_date). SUPERSEDES the append-only /
-- immutable invariant recorded in migration 20260710000002_init_telemetry.sql
-- (design D1 of RM1-telemetry-add-nightly-snapshots): a repeat capture for
-- the SAME calendar day now REPLACES that day's row (latest capture wins)
-- instead of adding a duplicate. poll_attempts is UNCHANGED by this
-- migration and stays append-only — it exists to record every attempt
-- including failures/retries, the availability/sleep-behavior signal a
-- daily collapse would destroy (design D4).
--
-- captured_date is NOT a generated/expression column: a UNIQUE index cannot
-- depend on POLLER_TIMEZONE (a runtime env var), and an expression index
-- pinned to a fixed zone would silently diverge from whatever
-- POLLER_TIMEZONE actually is, with no error — just quietly wrong future
-- dates, recoverable only via a migration AND a full index rebuild (design
-- D2, rejected alternatives). Instead, captured_date is a plain column
-- computed in Go (snapshotFrom / dateOnly, internal/telemetry/service.go)
-- from captured_at in the poller's configured *time.Location
-- (Config.Location, falling back to time.Local) — the same "derive in Go,
-- not SQL" precedent already used by deriveEnergyKWh / deriveTotalCost in
-- this module.

ALTER TABLE vehicle_snapshots ADD COLUMN captured_date DATE;

-- Backfill existing rows. This is a ONE-TIME, point-in-time conversion, not
-- an ongoing schema dependency like the rejected expression-index
-- alternative would be — so hardcoding a literal IANA zone here is safe and
-- does not reintroduce that coupling (design D3a). 'America/Bogota' is used
-- because POLLER_TIMEZONE has never been set in this project's .env (it
-- defaults to "Local", i.e. Go's time.Local), and the deployment host's
-- system zone IS America/Bogota — so this reproduces, for every existing
-- row, exactly the date Go's time.Local would have computed for it at
-- capture time. If this migration is ever run against a deployment whose
-- historical rows were captured under a genuinely different zone, update
-- this literal accordingly before applying.
UPDATE vehicle_snapshots
SET captured_date = (captured_at AT TIME ZONE 'America/Bogota')::date;

-- Dedupe: for every (account_id, tesla_id, captured_date) group with more
-- than one row, delete every row except the one with the latest
-- captured_at (ties broken by id, for determinism) — the same "latest
-- capture wins" rule D1 gives the write path going forward (design D3).
-- Deleted rows are NOT recoverable (raw_data is lost with them); this is
-- expected and authorized. As of this migration's authoring the live DB has
-- exactly 2 duplicate pairs, both dated 2026-08-04.
DELETE FROM vehicle_snapshots a
USING vehicle_snapshots b
WHERE a.account_id = b.account_id
  AND a.tesla_id = b.tesla_id
  AND a.captured_date = b.captured_date
  AND (a.captured_at < b.captured_at
       OR (a.captured_at = b.captured_at AND a.id < b.id));

-- Conflict target for the write path's new ON CONFLICT upsert (design D1),
-- and the structural mechanism that makes "at most one row per vehicle per
-- day" an enforced invariant rather than just an application convention.
ALTER TABLE vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, captured_date);

ALTER TABLE vehicle_snapshots ALTER COLUMN captured_date SET NOT NULL;

-- Audit trail for the now-possible same-day REPLACE (design D5). DEFAULT
-- now() backfills existing rows to this migration's run time (they were
-- never actually "updated" before now — this is a documented artifact of
-- the migration, not a claim those rows were overwritten) and gives every
-- future INSERT a value for free; the write path's ON CONFLICT ... DO
-- UPDATE explicitly SETs updated_at = now() to refresh it on every same-day
-- replace (see InsertVehicleSnapshot, query.sql). Mirrors
-- supercharger_sessions.updated_at (migration 20260716000001), this
-- module's existing precedent for a mutable row's audit column.
ALTER TABLE vehicle_snapshots ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
-- NOT a full rollback of history: the duplicate rows deleted by the Up
-- migration's dedupe step are NOT recoverable (their raw_data is gone with
-- them). Down only removes this migration's schema additions; it cannot,
-- and does not attempt to, restore the deleted rows.
ALTER TABLE vehicle_snapshots DROP CONSTRAINT IF EXISTS vehicle_snapshots_account_tesla_date_unique;
ALTER TABLE vehicle_snapshots DROP COLUMN IF EXISTS updated_at;
ALTER TABLE vehicle_snapshots DROP COLUMN IF EXISTS captured_date;
