-- +goose Up
-- Per-account registry of Tesla vehicles, seeded once from tesla.ListVehicles
-- (see openspec/changes/persist-tesla-vehicles). One row per (account, tesla_id);
-- re-seeding is idempotent (ON CONFLICT DO NOTHING via the query layer) and never
-- overwrites an existing vehicle's stored attributes.
--
-- tesla_id is the int64 `id` the Fleet API returns per vehicle (the canonical
-- resource id used by /api/1/vehicles/{id}/... endpoints). The volatile `state`
-- returned by the API is intentionally NOT stored — it would mislead the
-- dashboard with stale online/asleep data once Tesla is no longer called per
-- load. `vin` is indexed for future cross-account lookups but not unique
-- (a vehicle could, in principle, move between Tesla accounts).
--
-- Owned by internal/account; no other module reads this table directly.

CREATE TABLE vehicles (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tesla_id     BIGINT NOT NULL,
    vin          TEXT NOT NULL,
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, tesla_id)
);

CREATE INDEX idx_vehicles_vin ON vehicles (vin);

-- +goose Down
DROP TABLE IF EXISTS vehicles;