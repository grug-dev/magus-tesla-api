## Why

The nightly telemetry collector (tier 3 of `openspec/roadmaps/nightly-vehicle-telemetry.md`)
must enumerate **every** registered vehicle across **all** accounts so it can, per vehicle,
resolve a Tesla access token via the existing `account.AccessTokenFor(accountID)` and fetch a
snapshot. Today the account port only exposes `RegisteredVehicles(ctx, accountID)` — a
**per-account** read — and the `account.Vehicle` domain type carries **no account id**, because
it is deliberately the per-account view the gateway uses. A background job therefore has no way
to list all work items without either looping accounts it cannot enumerate, or reaching into the
account module's `vehicles` table directly (forbidden by `ai/architecture.md` §2). This change
adds an all-accounts enumeration to the account port so background collection jobs get their
work list through the interface, never the tables.

## What Changes

- Add a new **all-accounts** read to the `account.Service` port,
  `AllRegisteredVehicles(ctx) ([]OwnedVehicle, error)`, returning every registered vehicle across
  every account, each tagged with its owning `accountID`.
- Add a small domain type `OwnedVehicle{AccountID uuid.UUID; TeslaID int64; VIN, DisplayName string}`
  rather than putting `accountID` on the existing `account.Vehicle` (which stays the per-account
  view). Domain types carry no vendor suffix (`ai/architecture.md` §6).
- Add one new sqlc query in the module-scoped `internal/account/db` layer that selects every
  vehicle joined to its `account_id`, ordered for stable output. **No migration is needed** — the
  `vehicles` table already has a NOT NULL `account_id` column (see
  `internal/account/db/migrations/20260710000001_vehicles.sql`).
- Convert the generated `pgtype`/DB rows to the `OwnedVehicle` domain type at the module's
  DB→domain mapping boundary (`ai/go-conventions.md` §persistence), so `pgtype` never leaks out of
  the module.

Enumeration returns **all** registered vehicles, not only those whose account currently has a live
Tesla connection. Deciding "has a usable token" and handling expired/revoked tokens is tier 3's job
via `AccessTokenFor` (which already returns `ErrNoTeslaConnection`); account's job is only to
enumerate. Rationale is recorded in `design.md`.

**Not breaking.** No public method, type, or query is removed or renamed; the change only **adds**
a `Service` method, a domain type, and a query. The `internal/tesla` adapter is untouched — it stays
stateless about identity. Because the change widens the `account.Service` interface, the gateway's
test-only fake (`fakeAccount` in `internal/gateway/handlers/handlers_test.go`) needs a one-line
stub added so the package still compiles; this is a mechanical, leader-integrated cross-module edit
outside the account sandbox (the same pattern tier 1 used for `fakeTesla`). No production gateway
code changes.

Affected modules: `internal/account` (new port method + domain type + query). Downstream consumer:
the tier 3 `telemetry` module, which is not built by this change.

## Capabilities

### Modified Capabilities

- `account`: The account port gains an all-accounts registered-vehicle enumeration, returning each
  vehicle with its owning account id, so background collection jobs can list every vehicle across
  every account without reading the account module's tables.

## Impact

- **Code**
  - `internal/account/account.go`: new `OwnedVehicle` domain struct and a new `AllRegisteredVehicles(ctx)`
    method on the `Service` interface.
  - `internal/account/db/query.sql`: new `ListAllVehicles :many` query selecting `account_id, tesla_id,
    vin, display_name` across all rows, ordered `account_id, tesla_id`. Regenerate `accountdb` via
    `make sqlc`.
  - `internal/account/service.go`: implement `AllRegisteredVehicles`, mapping each DB row to
    `OwnedVehicle` at the boundary (reuse the `NULL display_name → ""` handling already used by
    `vehicleFromRow`).
  - `internal/account/service_integration_test.go`: `DATABASE_URL`-gated integration coverage
    (empty DB, single account, multiple accounts each with vehicles).
  - `internal/gateway/handlers/handlers_test.go` (**outside the account sandbox — leader-integrated**):
    one-line `fakeAccount.AllRegisteredVehicles` stub so the widened interface still compiles.
- **Specs**: delta to `specs/account/spec.md` (new "All-Accounts Registered Vehicle Enumeration"
  requirement).
- **Dependencies**: none new — existing pgx / sqlc / uuid stack.
- **Migrations**: none — `vehicles.account_id` already exists.
- **Operational**: read-only; no Tesla API calls, no writes. Enables tier 3's collection run.
