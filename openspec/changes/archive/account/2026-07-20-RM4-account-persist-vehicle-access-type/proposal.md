## Why

The charge-form UI (tier 4 of `openspec/roadmaps/RM4-charge-form-required-location-and-vehicle-autoselect.md`)
needs to distinguish whether the authenticated user **owns** the vehicle or is merely a **driver**
(a guest added to the vehicle). Tesla's Fleet API returns an `access_type` field per vehicle
(`OWNER` or `DRIVER`) on the `ListVehicles` response. Without persisting this value, every
charge-form load would need to re-query Tesla — at real cost (paid API, wakes the car) — or render
the wrong UI for a driver who cannot, for example, initiate charging on an owner-only feature.

Persisting `access_type` in the vehicle registry at seed time means the gateway reads it for free as
part of the already-fetched per-account vehicle list — no extra Tesla call, no N+1.

## What Changes

- **Migration** — `ALTER TABLE vehicles ADD COLUMN access_type TEXT CHECK (access_type IS NULL OR
  access_type IN ('OWNER','DRIVER'))` (nullable, no DEFAULT, no index). See `design.md` D1 and D2.
- **Domain types** — `AccessType *string` added to `account.Vehicle`, `account.OwnedVehicle`, and
  `account.SeedVehicle` in `internal/account/account.go`.
- **Persistence** — the seed INSERT (`InsertVehicleIfMissing` in `query.sql`) is extended to
  persist `access_type` from the incoming `SeedVehicle`. The read queries (`ListVehiclesByAccount`,
  `ListAllVehicles`) select and return the column; `service.go` maps it to `*string` at the
  DB→domain boundary. sqlc regenerated (`package accountdb`).
- **Boundary** — `SeedVehicle.AccessType` is the only entry point: the gateway (tier 4) will copy
  `VehicleTesla.AccessType` into `SeedVehicle.AccessType` before calling `SeedVehicles`. This
  module does NOT import `internal/tesla`; `access_type` arrives as a plain `*string`.

**Not breaking.** Adding a nullable column to `vehicles` is backward-compatible: existing rows
receive `NULL` and any code that does not pass `AccessType` in `SeedVehicle` stores `NULL` — no
panic, no error. Adding `*string` to domain types is additive (existing callers that construct a
`SeedVehicle` without `AccessType` get the zero value, `nil`, which maps to `NULL`). The
`account.Service` interface is unchanged — the signature of `SeedVehicles`, `RegisteredVehicles`,
and `AllRegisteredVehicles` is unchanged; only the returned types gain a field.

**Modules affected:**
- `internal/account` — migration, domain types, SQL queries, sqlc regeneration, service mapping,
  integration tests.
- `internal/gateway` — tier 4 of the roadmap will copy `VehicleTesla.AccessType` to
  `SeedVehicle.AccessType`; that wiring happens in tier 4, NOT in this change.

**Read path affected:** `RegisteredVehicles` and `AllRegisteredVehicles`. Both currently do
`SELECT *` (or an explicit column list) from `vehicles` scoped by `account_id`. Adding `access_type`
to those `SELECT *` queries costs nothing extra — the column is always fetched with the rest of the
row because per-account vehicle lists are small (single-digit vehicles per user). No dashboard hot
path is degraded; no new index is needed (see `design.md` D2).
