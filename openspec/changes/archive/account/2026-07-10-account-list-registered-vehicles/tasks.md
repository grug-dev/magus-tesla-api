> **Additive, non-breaking change** (tier 2 of `openspec/roadmaps/nightly-vehicle-telemetry.md`).
> No goose migration: `vehicles.account_id` already exists (NOT NULL FK). Persistence stays
> module-scoped (`internal/account/db`); convert `pgtype` → domain at the boundary
> (`ai/go-conventions.md` §persistence). No Tesla API calls, no writes.
>
> **Dependencies / parallelism:**
> - 1 (domain type + interface method) has no dependencies.
> - 2 (sqlc query + regen) is independent of 1 — disjoint files (`db/query.sql` + generated
>   `db/`) — and MAY run in parallel with 1.
> - 3 (service implementation + mapping) depends on **both** 1 and 2 (it references the interface
>   from 1 and the generated `ListAllVehicles` from 2).
> - 4 (integration tests) depends on 3.
> - 5 (gateway test-fake stub) depends on 1 — OUTSIDE the account sandbox, leader-integrated.
> - 6 (verification) depends on 1–5.

## 1. Domain type + interface method (`internal/account/account.go`) — no dependencies

- [x] 1.1 Add an `OwnedVehicle` domain struct to `internal/account/account.go` with fields
      `AccountID uuid.UUID`, `TeslaID int64`, `VIN string`, `DisplayName string` — no vendor
      suffix, no `state` field. Document that it is the cross-account view (distinct from the
      per-account `Vehicle`), carrying its owning account id.
- [x] 1.2 Add `AllRegisteredVehicles(ctx context.Context) ([]OwnedVehicle, error)` to the
      `account.Service` interface, with a doc comment stating it returns every registered vehicle
      across ALL accounts (empty, not error, when none), for background collection jobs, and that
      it does not check Tesla-connection liveness (the caller resolves tokens via `AccessTokenFor`).

## 2. sqlc query + regeneration (`internal/account/db/`) — independent of 1, parallel-ok

- [x] 2.1 Add `ListAllVehicles :many` to `internal/account/db/query.sql`:
      `SELECT account_id, tesla_id, vin, display_name FROM vehicles ORDER BY account_id, tesla_id;`
      with a comment noting it spans all accounts and does NOT join `tesla_tokens` (enumeration is
      decoupled from connection liveness).
- [x] 2.2 Regenerate the module-scoped `accountdb` package with `make sqlc` (or `sqlc generate`).
      Confirm no cross-module package imports `accountdb` and that only `internal/account/db` changed.

## 3. Service implementation + mapping (`internal/account/service.go`) — depends on 1 and 2

- [x] 3.1 Implement `AllRegisteredVehicles` in `internal/account/service.go`: call
      `s.q.ListAllVehicles(ctx)`, map each generated row to `OwnedVehicle` at the DB→domain
      boundary (reuse the `display_name` NULL→`""` handling as `vehicleFromRow` does; `account_id`
      is already `uuid.UUID` via the sqlc override). Return an empty (non-nil-safe) slice, never a
      nil error, when there are no rows. Wrap DB errors with context (`fmt.Errorf(... %w ...)`),
      matching the existing methods. Do NOT let `pgtype` leak into the returned type.

## 4. Integration tests (`internal/account/service_integration_test.go`) — depends on 3

- [x] 4.1 Add `DATABASE_URL`-gated integration coverage (self-skips when unset, per the module's
      existing test pattern) for `AllRegisteredVehicles`: (a) empty DB → empty result, no error;
      (b) one account with two vehicles → both returned, each tagged with that account's id;
      (c) two accounts each with vehicles → all returned, each tagged with the correct owning
      account id; assert `tesla_id`, `vin`, `display_name` round-trip and NULL `display_name` maps
      to `""`.

## 5. Gateway test-fake stub (`internal/gateway/handlers/handlers_test.go`) — depends on 1; OUTSIDE the account sandbox, leader-integrated

- [x] 5.1 Add a one-line `fakeAccount.AllRegisteredVehicles(context.Context) ([]account.OwnedVehicle, error)`
      stub so the widened `account.Service` interface still compiles (mechanical; production gateway
      code does not call it). Same cross-module pattern tier 1 used for `fakeTesla`.

## 6. Verification — depends on 1–5

- [x] 6.1 `go build ./...` and `go vet ./...` pass.
- [x] 6.2 `go test ./...` green and fast; account integration tests self-skip without `DATABASE_URL`
      (and pass with it set); no Tesla API call fires from the test run.
- [x] 6.3 `openspec validate account-list-registered-vehicles --strict` passes and every tasks.md
      checkbox reflects real completion.
