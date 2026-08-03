## Why

Tesla's Fleet API `vehicle_config` sub-object carries static, immutable-per-vehicle attributes
such as `exterior_color` and `car_type`. Today these values exist only inside
`vehicle_snapshots.raw_data` JSONB — a table owned by `internal/telemetry`. The `account` module
cannot read that table without violating its own module boundary (`ai/architecture.md` §2: "no
cross-module database leaks"), so no dashboard can show a vehicle's colour or model without either
a fresh (paid, wake-inducing) Tesla call or a boundary-breaking JSONB read from another module's
table. Persisting these two values directly on the `account` module's own `vehicles` registry row
lets the registry read port (`RegisteredVehicles` / `AllRegisteredVehicles`) serve them for free —
no extra Tesla call, no cross-module access.

This is **tier 1 of 2** of roadmap `RM6-static-vehicle-config-fields`
(`openspec/roadmaps/RM6-static-vehicle-config-fields.md`), which captures the binding decisions
(RD1–RD9) agreed with the user via the `grill-me` skill at the roadmap level. This proposal and its
sibling artifacts implement RD2, RD3, and RD5 for the `account` module only. Tier 2
(`RM6-telemetry-capture-vehicle-config`, owned by `internal/telemetry`, depends on this tier) wires
the nightly collector to call the port this tier adds — that wiring is explicitly out of scope here.

## What Changes

- **Migration** — a new goose migration adds two nullable `TEXT` columns to `vehicles`:
  `exterior_color` and `car_type`. No `DEFAULT`, no `CHECK` (see `design.md` D1 for why this
  diverges from the `access_type` precedent), no new index (see `design.md` D2).
- **Domain types** — `ExteriorColor *string` and `CarType *string` added to both
  `account.Vehicle` and `account.OwnedVehicle` in `internal/account/account.go`. `NULL` /  `nil`
  means "not yet captured" (RD5).
- **New port method** — `SetVehicleConfigIfEmpty(ctx, accountID uuid.UUID, teslaID int64,
  exteriorColor, carType string) error` added to `account.Service`, backed by a new
  `UpdateVehicleConfigIfEmpty` sqlc query. The query carries `AND (exterior_color IS NULL OR
  car_type IS NULL)` (RD2) so a partially-written row self-heals and a concurrent writer cannot
  clobber an already-captured value; `updated_at` moves only when a write actually lands.
- **Read paths** — `ListVehiclesByAccount` and `ListAllVehicles` (in `query.sql`) are extended to
  project the two new columns, so `RegisteredVehicles` and `AllRegisteredVehicles` surface them
  through the existing mapping helpers (`nullableTextToPtr` / `textPtrToNullable`, reused
  unchanged from the `access_type` precedent).
- **`SeedVehicles` is unchanged.** Tesla's `ListVehicles` response (what `SeedVehicles` consumes)
  does not include `vehicle_config` — only a per-vehicle `VehicleData` call does. There is nothing
  to seed at registration time; the two columns start `NULL` and are back-filled later by tier 2's
  nightly capture.
- **Tests** — `DATABASE_URL`-gated integration round-trip tests for the new port method and both
  read paths, following the existing `TestAccessType_RoundTrip` shape.

**Not breaking.** Adding two nullable columns is backward-compatible: existing rows get `NULL`,
and any code path that does not call the new port method is unaffected. `SeedVehicles`,
`RegisteredVehicles`, and `AllRegisteredVehicles` keep their existing signatures — only the
returned `Vehicle` / `OwnedVehicle` structs gain two fields. `account.Service` gains one new
method (`SetVehicleConfigIfEmpty`); this is additive to the interface, not a signature change to
an existing method.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `account-vehicle-registry` capability only)

### Modified Capabilities

- `account-vehicle-registry`: the "Per-Account Vehicle Registry Storage" requirement gains
  `exterior_color` and `car_type` as persisted, nullable, static attributes. A new requirement is
  added covering the once-per-vehicle, conditional-update capture contract
  (`SetVehicleConfigIfEmpty`): its `IS NULL OR IS NULL` write condition (RD2), the
  NULL-means-not-yet-captured semantics (RD5), and the boundary rule that `account` never imports
  `tesla` — a caller (tier 2) supplies the observed values through the port.

## Impact

- `internal/account` — new migration, two new domain fields on two structs, one new port method,
  one new sqlc query, two edited read queries, sqlc regeneration, service mapping, integration
  tests.
- `internal/telemetry` — **not touched by this tier.** Tier 2 of the roadmap
  (`RM6-telemetry-capture-vehicle-config`) is the only consumer of the new port method; it is a
  separate, dependent OpenSpec change.

**Read path affected:** `RegisteredVehicles` and `AllRegisteredVehicles`. Both already do an
explicit-column `SELECT` scoped by `account_id` (or ordered by `account_id, tesla_id` for the
all-accounts variant); adding two nullable `TEXT` columns to that same `SELECT` costs nothing
extra — they are fetched as part of the heap row already located by the existing
`UNIQUE (account_id, tesla_id)` constraint. No new index, no dashboard hot-path degradation (see
`design.md` D2 for the full index-plan justification).
