## Context

The gateway currently calls `tesla.ListVehicles` on every dashboard/fragment render
(`internal/gateway/handlers/handlers.go:88 vehiclesFor`) and maps the vendor DTOs straight to the
presentation model — nothing is persisted. The platform's mission (long-term historical +
derived metrics per vehicle) needs a stable per-vehicle record keyed per account; and repeated
identical Tesla calls waste quota and risk waking vehicles.

Existing conventions this design follows:
- Persistence is **module-scoped**: each module owns its DB tables + `sqlc` package, accessed
  solely via its public `Service` interface (see `ai/architecture.md` §2, and `sqlc.yaml` which
  generates one package per module and forbids cross-module imports). `internal/account` already
  owns `accounts` and `tesla_tokens`; adding `vehicles` there matches the established pattern.
- Migrations are goose files in the module's `db/migrations/` and **are** the single schema source
  sqlc parses (see `internal/account/db/migrations/20260707000001_init_account.sql`).
- Domain structs (`account.Account`, `account.TeslaTokens`) expose plain Go types; the `pgtype`
  boundary is converted inside `internal/account/service.go` (`timestamp`/`textFromString` helpers).
- The `internal/tesla` adapter is stateless about identity — credentials are passed per call; it is
  **not** changed by this design.
- The gateway holds no DB access and orchestrates only via interfaces.

Stakeholders: gateway (consumer), future domain modules (battery/charging/…) that will FK history
off persisted vehicles, ops (reduced Tesla quota).

## Goals / Non-Goals

**Goals:**
- Persist each Tesla vehicle per account: `tesla_id`, `vin`, `display_name`.
- Make insertion idempotent per `(account_id, tesla_id)`; never duplicate or overwrite an existing
  vehicle's stored attributes.
- Trigger `tesla.ListVehicles` only when the account has no vehicles registered; thereafter the
  dashboard reads from the DB.
- Keep the `account` module as the single owner of the table; gateway reaches it only via
  `account.Service`.
- Leave `internal/tesla`'s contract untouched.

**Non-Goals:**
- Persisting volatile vehicle `state` (online/asleep). Dropped from the persistence model and from
  the dashboard presentation model.
- Any re-sync path / manual "refresh vehicles" action. Registration is one-time per account; a
  future change may add re-sync.
- Cross-account VIN aggregation / dedup. `vin` is indexed for later but no cross-account unique
  constraint or query is added now.
- Attaching history/metrics to vehicles. Downstream modules will reference `vehicles` later.

## Decisions

### Decision 1: Place the `vehicles` table in `internal/account`, not a new module
**Choice:** Add the table + queries + interface methods to `internal/account`.
**Rationale:** `account` already owns per-account identity and the Tesla connection, and is the
only module the gateway depends on for account-scoped data. A 1:N `vehicles` table is a natural
sibling of `tesla_tokens`. It keeps `internal/tesla` stateless and the gateway a pure orchestrator.
Reusing the old `internal/vehicle` package name is explicitly avoided (AGENTS.md notes that package
was deliberately replaced by the `tesla` adapter).
**Alternatives considered:**
- A new `internal/fleet`/`internal/garage` module — cleaner separation of identity vs assets, but
  adds a new module boundary, a new `sqlc.yaml` entry, and a new wiring point now for a vehicle set
  that is tightly coupled to the account's Tesla connection. Revisit when a vehicle-centred domain
  accrues enough behaviour to earn its own module.

### Decision 2: Dedup key is `UNIQUE (account_id, tesla_id)`; `vin` indexed, not unique-constrained
**Choice:** `tesla_id` is the int64 `id` the Fleet API returns in `VehicleTesla.ID`
(`internal/tesla/types.go:27`). The table has `UNIQUE (account_id, tesla_id)` and a plain index on
`vin`.
**Rationale:** `tesla_id` is the API's own stable identifier within an account; uniqueness scoped to
the account matches reality (the id is account-scoped). `vin` is the cross-account physical
identifier, so it is indexed for the future cross-account lookup without imposing a constraint the
registrar flow would have to work around.
**Alternatives considered:**
- `UNIQUE` on `vin` globally — more rigorous, but a vehicle moving between Tesla accounts (rare but
  possible) would conflict; out of scope for the current one-account seeding flow.
- `tesla_vehicle_id` (`vehicle_id`) as the key — it is the hardware/VIN-linked id; `id` is the
  canonical Fleet API resource id used by `VehicleData`/`WakeUp` (`internal/tesla/vehicles.go:21`,
  `:31`), so `id` is the right reference for any future per-vehicle API call.

### Decision 3: Idempotent insert via `INSERT ... ON CONFLICT DO NOTHING`
**Choice:** The seed query is `INSERT INTO vehicles (account_id, tesla_id, vin, display_name) … ON
CONFLICT (account_id, tesla_id) DO NOTHING`. Existing vehicles are not updated — "the rest of the
information should not change."
**Rationale:** Matches the user's requirement that existing vehicles are not overwritten; simplest
correct SQL; no read-then-write race. `display_name` is fixed at first seed.
**Alternatives considered:** `ON CONFLICT DO UPDATE` to refresh `display_name` — explicitly rejected;
it contradicts the stated "do not change existing info" rule.

### Decision 4: Do not persist or display volatile `state`
**Choice:** The `vehicles` table has no `state` column; `fragments.Vehicle` loses the `State` field;
the dashboard shows `display_name` + `vin` only.
**Rationale:** Once Tesla is no longer called on every load, any persisted `state` would be stale
and mislead users about online/asleep status. The cleanest contract is to drop state entirely from
the persisted + presented model. A future live-state feature can re-fetch without re-listing.
**Alternatives considered:** Persist state at seed time and label it "last seen" — rejected to avoid
implying liveness; the dashboard has no live data once registration is one-time.

### Decision 5: One registration window per account
**Choice:** The gateway's `vehiclesFor` calls `account` to read registered vehicles; **only** if the
returned set is empty does it obtain the access token (via `account.AccessTokenFor`), call
`tesla.ListVehicles`, and persist results through `account`. Subsequent loads short-circuit on the
DB read and never reach Tesla.
**Rationale:** Exactly the trigger rule the user specified, and it cuts Tesla quota to one
`ListVehicles` per account for the lifetime of that registration.
**Alternatives considered:** A manual refresh action / auto re-sync on errors — out of scope; a
future change.

### Decision 6: Seeding owned by `account`, gateway stays stateless w.r.t. DB
**Choice:** `account.Service` gains two methods (naming indicative):
- `RegisteredVehicles(ctx, accountID) ([]Vehicle, error)` — reads persisted vehicles.
- `SeedVehicles(ctx, accountID, teslaVehicles []tesla.VehicleTesla) ([]Vehicle, error)` — inserts
  any not already present; returns the resulting registered set.

The gateway calls `RegisteredVehicles`; if empty it calls Tesla then `SeedVehicles`. The gateway
never imports `accountdb`.
**Rationale:** Keeps the gateway a pure orchestrator (architecture §2) and the table owner as the
sole accessor (architecture §2 "no cross-module database leaks"). Passing `tesla.VehicleTesla`
into `account` is acceptable — `account` already depends on `internal/auth` for token refresh; a
one-way DTO passed in from the adapter does not invert the adapter's statelessness. (If preferred,
`SeedVehicles` can instead take a small mapped slice of `{TeslaID, VIN, DisplayName}` from the
gateway to keep `account` free of a `tesla` import. This is a minor wiring choice; the spec is
agnostic.)

## Risks / Trade-offs

- **Stale vehicle list if Tesla changes it after registration** → consciously accepted (non-goal); a
  future re-sync change can compare persisted vs freshly-listed and add/remove.
- **A vehicle the user sells/disconnects stays in the registry** → accepted for now; no removal path
  is in scope. Mitigation: a later change may add a "prune" on explicit reconnect.
- **Gateway calling Tesla inside the dashboard request only when empty keeps a (rare) synchronous
  Tesla wait in that first load** → acceptable; it is the same cost as today's every-load call, and
  happens at most once per account.
- **`account` module's surface grows beyond pure identity** → trade-off vs a new module (Decision 1);
  revisit when vehicle-centric domain behaviour accrues.
- **`INSERT … ON CONFLICT DO NOTHING` silently skips new fields** → `display_name` change is
  ignored; acceptable per "should not change" rule, but should be documented in the spec as a
  scenario.
- **Migration ordering** → the new migration depends on `accounts`; reuse the goose date-prefix
  convention (`internal/account/db/migrations/2026070x*`). Down drops `vehicles` only.

## Migration Plan

1. Add goose migration `…_vehicles.sql` (Up: `CREATE TABLE vehicles` with FK and unique/index;
   Down: `DROP TABLE IF EXISTS vehicles`).
2. Add `query.sql` entries (`InsertVehicleIfMissing`, `ListVehiclesByAccount`).
3. Regenerate `accountdb` (`sqlc generate`) per `sqlc.yaml`.
4. Extend `account.Service` + update fakes (`internal/gateway/handlers/handlers_test.go`).
5. Update `vehiclesFor` and `fragments.Vehicle`.
6. Run `go test ./...` and integration tests with `DATABASE_URL`.
**Rollback:** revert the migration (`goose … down`) drops the table; revert code; legacy behavior is
restored (every-load `ListVehicles`). No data loss for users beyond the cached registry.

## Open Questions

- Whether `account.SeedVehicles` should accept `tesla.VehicleTesla` or a mapped DTO — resolved during
  implementation; design recommends the mapped DTO to keep `account` free of a `tesla` import.
- Exact method names (`RegisteredVehicles` vs `ListVehicles`, `EnsureVehicles`) — resolved at
  implementation; spec uses indicative names.