-- RM34-account-add-record-status: adds an explicit activation status to both
-- accounts and vehicles (design.md D1). TEXT + CHECK, not a native enum — this
-- project has no Postgres enums and access_type on this same vehicles table is
-- the direct precedent (design.md D1). Title-case values ('Active'/'Inactive')
-- are a one-off exception to this module's UPPERCASE vocabulary, by explicit
-- ticket request, so the value reads as prose in a raw psql SELECT.
--
-- accounts defaults 'Inactive' (design.md D2) — deliberately NO backfill: every
-- pre-existing account becomes Inactive the moment this migration applies. This
-- is the invite gate; see design.md D2 for the required post-deploy recovery SQL.
--
-- vehicles defaults 'Active' (design.md D3) — the OPPOSITE default. This is a
-- correctness gate, not a style choice: it prevents an infinite paid Fleet API
-- reseed loop (RegisteredVehicles would return empty forever, the gateway would
-- reseed on every dashboard load, ON CONFLICT DO NOTHING would swallow the
-- reseed silently) — see design.md D3 for the full trace.
--
-- Neither table needs a separate backfill UPDATE (design.md D8): adding a column
-- with a non-volatile DEFAULT substitutes that literal for every pre-existing row
-- at read time without rewriting the table (verified against current PostgreSQL
-- docs) — the DEFAULT clause alone realizes both D2's and D3's backfill.

-- +goose Up
ALTER TABLE accounts
    ADD COLUMN status TEXT NOT NULL DEFAULT 'Inactive'
    CHECK (status IN ('Active','Inactive'));

ALTER TABLE vehicles
    ADD COLUMN status TEXT NOT NULL DEFAULT 'Active'
    CHECK (status IN ('Active','Inactive'));

-- +goose Down
ALTER TABLE vehicles DROP COLUMN IF EXISTS status;
ALTER TABLE accounts DROP COLUMN IF EXISTS status;
