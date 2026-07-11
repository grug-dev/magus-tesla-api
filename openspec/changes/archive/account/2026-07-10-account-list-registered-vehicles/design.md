## Context

The `account` module persists a per-account registry of Tesla vehicles in the `vehicles` table
(migration `internal/account/db/migrations/20260710000001_vehicles.sql`; queries in
`internal/account/db/query.sql`; sqlc-generated access in `db/query.sql.go`). Each row already
carries `account_id UUID NOT NULL REFERENCES accounts(id)` plus `tesla_id`, `vin`, and
`display_name`. The public port (`internal/account/account.go`) exposes only
`RegisteredVehicles(ctx, accountID) ([]Vehicle, error)` — a **per-account** read whose `Vehicle`
domain type intentionally omits the account id, because it is the gateway's per-user view.

Tier 3 of `openspec/roadmaps/nightly-vehicle-telemetry.md` (the `telemetry` collector) needs to
enumerate **every** registered vehicle across **all** accounts to know its work list, then call the
existing `AccessTokenFor(accountID)` per vehicle. It cannot read `vehicles` directly
(`ai/architecture.md` §2: "no cross-module database leaks"), and there is no port method that spans
accounts. This change adds that method.

## Goals / Non-Goals

**Goals:**
- Expose an all-accounts registered-vehicle enumeration on the `account.Service` port, each vehicle
  tagged with its owning `accountID`, so background jobs get their work list through the interface.
- Keep the existing per-account `RegisteredVehicles` and `Vehicle` type unchanged (additive only).
- Keep persistence module-scoped: one new sqlc query, no cross-module access, `pgtype`→domain
  conversion at the boundary (`ai/go-conventions.md` §persistence).

**Non-Goals:**
- Token acquisition, connection-liveness checks, or failure handling — those belong to tier 3's
  collector via `AccessTokenFor` (which already returns `ErrNoTeslaConnection`).
- Any Tesla API call, wake, write, or new migration.
- Pagination / filtering (single query returning all rows; revisit only if scale demands it — see
  Risks).

## Decisions

### D1 — New method name and shape: `AllRegisteredVehicles(ctx) ([]OwnedVehicle, error)`

The method takes only `ctx` (it spans all accounts, so no `accountID` argument) and returns a slice
of a new domain type:

```go
type OwnedVehicle struct {
    AccountID   uuid.UUID
    TeslaID     int64
    VIN         string
    DisplayName string
}
```

**Why a new `OwnedVehicle` type instead of adding `AccountID` to `Vehicle`.** `account.Vehicle` is
the per-account view the gateway renders; a caller holding one already knows the account context, so
an `AccountID` there would be redundant and, worse, tempt callers to treat a per-account vehicle as
cross-account. A distinct `OwnedVehicle` makes the cross-account semantics explicit at the type
level and keeps `Vehicle` stable. No vendor suffix — this is our own domain model
(`ai/architecture.md` §6).

**Why `AllRegisteredVehicles` as the name.** It mirrors the existing `RegisteredVehicles` verb while
signalling "all accounts". The roadmap's tier-2 row calls for "every registered vehicle across ALL
accounts (vehicle identifiers + owning `accountID`)"; this name reads as exactly that. Alternatives
considered: `RegisteredVehiclesForAllAccounts` (longer, no added clarity); `AllVehicles` (drops the
"registered" qualifier that distinguishes the persisted registry from a live Tesla listing) —
rejected.

### D2 — Enumerate ALL registered vehicles, not only connected accounts

The enumeration returns **every** registered vehicle regardless of whether its account currently has
a live/unexpired Tesla connection.

**Why.** It keeps the two modules' jobs cleanly separated and matches the roadmap's failure policy:
- account's job is to **enumerate** the registry (one simple query, no join to `tesla_tokens`, no
  token-expiry logic).
- telemetry's job is to **collect and handle failures**: for each `OwnedVehicle` it calls
  `AccessTokenFor(accountID)`; an account with no connection yields `ErrNoTeslaConnection`, and the
  roadmap's failure policy records that per-vehicle attempt (reason `unauthorized` / connection
  broken) without aborting the run. Filtering here would (a) require account to reach into token
  state and encode a liveness rule, (b) hide vehicles whose token is merely expired-but-refreshable
  (which `AccessTokenFor` would successfully refresh), and (c) silently shrink telemetry's
  `poll_attempts` coverage, which the roadmap wants as availability data.

**Alternative considered — join to `tesla_tokens` and return only connected accounts.** Rejected:
it duplicates connection logic that already lives behind `AccessTokenFor`, couples enumeration to
token liveness, and loses attempt-tracking signal. If a future need arises to skip
never-connected accounts cheaply, it can be added as a separate, explicitly-named method later
without changing this one.

### D3 — One new sqlc query; no migration; map at the boundary

Add `ListAllVehicles :many` to `internal/account/db/query.sql` selecting the columns the domain type
needs, ordered for deterministic output:

```sql
-- name: ListAllVehicles :many
-- Every registered vehicle across ALL accounts, each with its owning account_id,
-- for background collection jobs (nightly telemetry). Ordered (account_id, tesla_id)
-- for stable, testable output. No join to tesla_tokens: enumeration is decoupled
-- from connection liveness (that is the caller's job via AccessTokenFor).
SELECT account_id, tesla_id, vin, display_name FROM vehicles
ORDER BY account_id, tesla_id;
```

The `vehicles.account_id` column already exists (NOT NULL FK), so **no goose migration** is
required. `service.go` maps each generated row to `OwnedVehicle`, reusing the existing
`display_name` NULL handling (`pgtype.Text.String` is `""` when NULL, as `vehicleFromRow` already
does). `pgtype` never leaves the module. `account_id` is already `uuid.UUID` in generated code via
the project-wide sqlc override, so it maps straight through.

**Why an explicit column list, not `SELECT *`.** The domain type needs exactly these four columns;
an explicit list keeps the generated row struct minimal and the mapping obvious. (The existing
`ListVehiclesByAccount` uses `SELECT *`; the all-accounts query is new and can be tighter.)

## Risks / Trade-offs

- **Unbounded result set.** `ListAllVehicles` returns every vehicle in one slice. At the platform's
  current scale (single-digit users) this is trivial. If the fleet grows to where a single load is a
  problem, add a batched/paginated variant then — out of scope now, and premature to build.
- **Stale sqlc output if not regenerated.** After editing `query.sql`, `make sqlc` must run before
  `go build`; the build fails fast because `service.go` references the new `ListAllVehicles`
  generated method. Called out in tasks.
- **Interface widening breaks the gateway test fake.** `fakeAccount` in
  `internal/gateway/handlers/handlers_test.go` implements `account.Service`; adding a method makes
  that package fail to compile until a one-line stub is added. This is a mechanical,
  leader-integrated edit outside the account sandbox (tier 1 handled `fakeTesla` the same way).

## Migration Plan

No database migration. Steps:
1. Add `OwnedVehicle` + `AllRegisteredVehicles` to `internal/account/account.go`.
2. Add `ListAllVehicles` to `internal/account/db/query.sql`; run `make sqlc` to regenerate `accountdb`.
3. Implement `AllRegisteredVehicles` in `internal/account/service.go` with row→`OwnedVehicle` mapping.
4. Add `DATABASE_URL`-gated integration tests.
5. Leader adds the one-line `fakeAccount.AllRegisteredVehicles` stub in the gateway test file.
6. `go build ./...`, `go vet ./...`, `go test ./...` (integration tests self-skip without `DATABASE_URL`).
