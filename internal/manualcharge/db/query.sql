-- Queries for the manualcharge module. sqlc generates package `manualchargedb` from
-- these against the schema in migrations/. No other module may import manualchargedb
-- (module-scoped DB access — ai/architecture.md §2, ai/go-conventions.md §persistence).
-- Unlike telemetry's append-only tables, manual_charge_entries is mutable: all five
-- operations (create, update, delete, list-by-vehicle, list-by-account) are live CRUD.

-- name: CreateEntry :one
-- Insert a new user-asserted charge entry. All required fields are non-nullable params;
-- optional fields use nullable params (sqlc maps them to pgtype nullable types via the
-- schema column types). RETURNING * hands back the server-assigned id, created_at, and
-- updated_at so the gateway can display the stored entry without a second round-trip.
INSERT INTO manual_charge_entries (
    account_id,
    tesla_id,
    vin,
    charged_on,
    energy_added_kwh,
    price,
    currency,
    started_at,
    ended_at,
    start_battery_pct,
    end_battery_pct,
    charging_type,
    location_kind,
    location_label,
    notes
) VALUES (
    @account_id,
    @tesla_id,
    @vin,
    @charged_on,
    @energy_added_kwh,
    @price,
    @currency,
    @started_at,
    @ended_at,
    @start_battery_pct,
    @end_battery_pct,
    @charging_type,
    @location_kind,
    @location_label,
    @notes
)
RETURNING *;

-- name: UpdateEntry :one
-- Update mutable fields of an existing charge entry. The WHERE clause scopes to
-- (id, account_id) so a user cannot update another tenant's entry even with a valid
-- UUID — cross-tenant mutation is blocked at the SQL level (design D4).
-- Immutable columns (id, account_id, tesla_id, vin, created_at) are never touched.
-- updated_at is refreshed to now() on every successful update.
UPDATE manual_charge_entries
SET
    charged_on        = @charged_on,
    energy_added_kwh  = @energy_added_kwh,
    price             = @price,
    currency          = @currency,
    started_at        = @started_at,
    ended_at          = @ended_at,
    start_battery_pct = @start_battery_pct,
    end_battery_pct   = @end_battery_pct,
    charging_type     = @charging_type,
    location_kind     = @location_kind,
    location_label    = @location_label,
    notes             = @notes,
    updated_at        = now()
WHERE id = @id
  AND account_id = @account_id
RETURNING *;

-- name: DeleteEntry :exec
-- Delete a charge entry scoped to the caller's own account. The double-scope
-- (id AND account_id) means a user cannot delete another tenant's entry even
-- if they somehow obtain a valid entry UUID — cross-tenant deletes are blocked
-- at the SQL level (design D4, intentional double-scope guard).
DELETE FROM manual_charge_entries
WHERE id = @id
  AND account_id = @account_id;

-- name: ListEntriesByVehicle :many
-- Return entries for a specific vehicle within an account, ordered newest charged
-- day first, limited to limit_count rows. Uses idx_manual_charge_entries_vehicle_time
-- (account_id, tesla_id, charged_on DESC): account_id prunes to the tenant, tesla_id
-- further narrows to one vehicle, and the DESC column means the ORDER BY is satisfied
-- by the index directly — no sort step required (design D3, Read path 1).
SELECT * FROM manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByAccount :many
-- Return all entries for a given account across all vehicles, ordered newest charged
-- day first, limited to limit_count rows. Uses idx_manual_charge_entries_account_time
-- (account_id, charged_on DESC): account_id is the single WHERE predicate and
-- charged_on DESC matches the ORDER BY, eliminating a sort step (design D3, Read path 2).
SELECT * FROM manual_charge_entries
WHERE account_id = @account_id
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByVehicleBetween :many
-- Return entries for a specific vehicle within an account whose charged_on falls
-- within [@from_date, @to_date], inclusive of both bounds, ordered newest charged
-- day first. Uses idx_manual_charge_entries_vehicle_time (account_id, tesla_id,
-- charged_on DESC) as a single index range scan: account_id and tesla_id prune to
-- the tenant and vehicle, charged_on BETWEEN walks the range, and the DESC column
-- order satisfies ORDER BY with no separate sort step (design D3). No LIMIT: the
-- caller-supplied [from, to] window is the safety bound, not a row count
-- (design D1, roadmap D9).
SELECT * FROM manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charged_on BETWEEN @from_date AND @to_date
ORDER BY charged_on DESC;
