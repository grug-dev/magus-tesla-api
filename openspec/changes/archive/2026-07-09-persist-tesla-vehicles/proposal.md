## Why

Today the gateway calls `tesla.ListVehicles` on every dashboard load, on demand, and renders the
vendor response straight to HTML. Nothing is persisted per account. This blocks the platform's
core mission — long-term, derived metrics per vehicle — because there is no stable per-vehicle
record to attach history to, and it wastes Tesla API quota on repeated identical calls. We need a
durable, per-account vehicle registry seeded once from `tesla.ListVehicles`, so downstream
domain modules (battery, charging, …) can key history off persisted vehicles and the dashboard no
longer hits Tesla on every page view.

## What Changes

- Add a new `vehicles` table owned by `internal/account`, keyed one-to-many by `account_id`, with
  `tesla_id` (the int64 `id` returned by the Fleet API `ListVehicles`), `vin`, and `display_name`.
  The `state` field returned by the API is **not** persisted (it is volatile live state; persisting
  it would mislead the dashboard with stale "online/asleep" data).
- Enforce `UNIQUE (account_id, tesla_id)` so a vehicle is never inserted twice for the same account,
  and index `vin` for cross-account lookups later. Existing vehicles are skipped on re-seed; their
  stored attributes (`display_name`) are not overwritten ("the rest of the information should not
  change").
- Extend `account.Service` with the registry port: a read of the account's persisted vehicles, and
  a seed path that calls `tesla.ListVehicles` and inserts only vehicles not already present. The
  `internal/tesla` adapter stays stateless about identity — it is handed credentials as today and
  is **not** modified.
- Change the gateway's vehicle dashboard flow: it now reads persisted vehicles through the account
  module. It triggers a `tesla.ListVehicles` call **only when the account has no vehicles
  registered**. Once persisted, the dashboard never calls Tesla again; registration is one-time per
  account.
- The dashboard presentation model drops the `state` field (it is no longer live and is not
  persisted); the vehicle list shows `display_name` and `vin`.

**Not breaking.** No public API is removed or renamed; the change adds a table, new account
interface methods, and tightens the dashboard's data source. Affected modules: `internal/account`
(new table + interface methods), `internal/gateway` (dashboard reads from DB; drops `state`).
`internal/tesla` is touched only insofar as its output is consumed for seeding — its contract is
unchanged.

## Capabilities

### New Capabilities

- `account-vehicle-registry`: Per-account durable registry of Tesla vehicles, seeded (once) from
  `tesla.ListVehicles`, with idempotent insertion so existing vehicles are never duplicated or
  overwritten. Exposed through the `account` module's public interface; no other module may read the
  table directly.

### Modified Capabilities

- `gateway`: The Vehicle Dashboard requirement changes — the dashboard now reads persisted
  vehicles through the account module and shows `display_name` + `vin` (the `state` field is
  dropped). A Tesla `ListVehicles` call is made only when the account has no registered vehicles,
  rather than on every dashboard load.

## Impact

- **Code**
  - `internal/account/`: new `internal/account/db/migrations/*_vehicles.sql` (goose) adding the
    `vehicles` table; new queries in `internal/account/db/query.sql`
    (`InsertVehicleIfMissing`, `ListVehiclesByAccount`); regenerate `accountdb` via sqlc.
  - `internal/account/account.go` + `service.go`: new `Vehicle` domain struct and `Service` methods
    (`RegisteredVehicles(ctx, accountID)`, `EnsureVehiclesSeeded(ctx, accountID, teslaVehicles)` or
    similar). fakes in `internal/gateway/handlers/handlers_test.go` extended to satisfy the widened
    `account.Service` interface.
  - `internal/gateway/handlers/handlers.go`: `vehiclesFor` reads from `account` first; calls Tesla
    only when the registry is empty, then persists. `fragments.Vehicle` loses the `State` field.
- **Specs**: new `specs/account-vehicle-registry/spec.md`; delta to `specs/gateway/spec.md`
  (Vehicle Dashboard + presentation-model scenarios).
- **Dependencies**: none new — uses existing pgx / sqlc / goose / uuid stack.
- **Migrations**: one new up/down goose migration; requires the account init migration to be
  applied (FK `account_id → accounts(id)`).
- **Operational**: reduces Tesla Fleet API load to one `ListVehicles` call per account (lifetime of
  that registration), lowering quota use and battery/wake side-effects.