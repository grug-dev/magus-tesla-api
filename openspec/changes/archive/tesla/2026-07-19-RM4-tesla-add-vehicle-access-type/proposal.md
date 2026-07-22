## Why

Tier 1 of the `RM4-charge-form-required-location-and-vehicle-autoselect` roadmap. The
manual-charge form needs to auto-select the correct vehicle for the logged-in user. When
an account has more than one vehicle, auto-selection requires knowing which vehicle the
user _owns_ vs. which they only have _driver_ access to. Tesla's `GET /api/1/vehicles`
endpoint already returns a per-vehicle `access_type` field (`"OWNER"` or `"DRIVER"`), but
the current `VehicleTesla` DTO in `internal/tesla/types.go` does not decode it — the field
is silently dropped on unmarshal.

This tier adds `AccessType string` to `VehicleTesla` so `ListVehicles` surfaces the field
to every downstream consumer. Later tiers (account persistence, gateway auto-select) depend
on this typed field being available first.

## What Changes

- **`VehicleTesla.AccessType string` added to `internal/tesla/types.go`** — json tag
  `access_type`. The field is a plain `string`: the adapter decodes whatever Tesla sends and
  does not validate or normalise the enum (`"OWNER"` / `"DRIVER"` / empty). Enum
  validation is a downstream concern (the `account` tier's responsibility).
- **`internal/tesla/AGENTS.md` "Public interface" note updated** — the DTO shape note under
  the Other public symbols list is updated to reflect that `VehicleTesla` now carries
  `AccessType`.
- **Unit test added to `internal/tesla/vehicles_test.go`** — an offline decode test
  (httptest) asserts that a canned `/api/1/vehicles` response containing
  `"access_type": "OWNER"` on a vehicle is correctly decoded into `VehicleTesla.AccessType`.

## Capabilities

### Modified Capabilities

- `tesla`: **Vehicle Inventory Listing** — `ListVehicles` now surfaces the per-vehicle
  `access_type` field decoded from Tesla's response. No other method, type, or interface
  signature changes.

### New Capabilities

None.

## Breaking Change

**NO.** Adding a new field to an existing struct is backward-compatible in Go. Existing
callers that do not read `AccessType` are unaffected; the zero value (`""`) is returned for
any vehicle where Tesla omits the field. No interface signature changes. No other module
needs updating as part of this tier.

## Modules Affected

- `tesla` (owner) — `types.go`, `vehicles_test.go`, `AGENTS.md`. No other files.

## Raw-Explorer Sync Rule

This change does **NOT add a new Fleet API method**. `ListVehicles` already exists and
already calls `GET /api/1/vehicles`; only the DTO struct gains a new decoded field.
Therefore the raw-explorer sync rule (`CLAUDE.md` §Tesla API Exploration) does **NOT
apply**: no new `Raw*` sibling is created in `raw.go` and no change to
`cmd/explore-tesla-api` is required. A reviewer must not flag a missing raw sibling for
this change.

## Hot Read Path Impact

None. `VehicleTesla` is a DTO struct; adding a field does not affect any DB query or
database read path. The performance profile (read-heavy) is satisfied: this change has zero
impact on any read path.

## Design Gate

Not triggered. This change introduces no database object — no table, no column, no
migration, no index. The `database` design gate does not apply.
