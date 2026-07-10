## 1. Database Migration

- [x] 1.1 Create goose migration `internal/account/db/migrations/20260710000001_vehicles.sql` with `CREATE TABLE vehicles` (id UUID PK default gen_random_uuid, account_id UUID NOT NULL FK accounts(id) ON DELETE CASCADE, tesla_id BIGINT NOT NULL, vin TEXT NOT NULL, display_name TEXT, created_at/updated_at TIMESTAMPTZ default now()), `UNIQUE (account_id, tesla_id)`, `INDEX (vin)`, and a matching `-- +goose Down` `DROP TABLE IF EXISTS vehicles`
- [x] 1.2 Verify the migration applies and rolls back (`goose … up` / `… down`) against a scratch Postgres

## 2. sqlc Queries + Regeneration

- [x] 2.1 Add `InsertVehicleIfMissing` query (`:copyfrom`-or-`:exec` style per vehicle, or `:one` loop): `INSERT INTO vehicles (account_id, tesla_id, vin, display_name) VALUES ($1,$2,$3,$4) ON CONFLICT (account_id, tesla_id) DO NOTHING`
- [x] 2.2 Add `ListVehiclesByAccount` query (`:many`): `SELECT * FROM vehicles WHERE account_id = $1 ORDER BY tesla_id`
- [x] 2.3 Generated `internal/account/db` kept in sync by hand (`models.go` `Vehicle` struct, `query.sql.go` `InsertVehicleIfMissing`+`ListVehiclesByAccount`); verified by `go build ./...` and live-DB integration tests. NOTE: user should run `sqlc generate` once to normalize the hand-edited generated files (output is identical). No cross-module package imports `accountdb`.

## 3. account Module Interface + Implementation

- [x] 3.1 Add a `Vehicle` domain struct to `internal/account/account.go` (fields: `TeslaID int64`, `VIN string`, `DisplayName string`) with NO `state` field
- [x] 3.2 Add to the `account.Service` interface: `RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]Vehicle, error)` and `SeedVehicles(ctx, accountID, []SeedVehicle) ([]Vehicle, error)` (use a small mapped DTO `SeedVehicle{TeslaID, VIN, DisplayName}` so `account` does not import `tesla`)
- [x] 3.3 Implement both methods in `internal/account/service.go` (`SeedVehicles` inserts vehicles not present via `InsertVehicleIfMissing`, then returns `RegisteredVehicles`; convert `pgtype`/DB rows to the domain `Vehicle` at the boundary)
- [x] 3.4 Add integration tests in `internal/account/service_integration_test.go` covering: insert-when-missing, idempotent re-seed-when-present (no overwrite), one new among existing, and empty-then-filled read (guarded by `DATABASE_URL`)

## 4. Gateway Wiring

- [x] 4.1 Update `internal/gateway/handlers/handlers.go vehiclesFor`: call `h.acct.RegisteredVehicles(ctx, uid)` first; if non-empty, map to `fragments.Vehicle` (display_name + vin, no state) and return; if empty, resolve token via `AccessTokenFor`, handle `ErrNoTeslaConnection`/`ErrUnauthorized` as today, call `tesla.ListVehicles`, then call `h.acct.SeedVehicles` and render the seeded set
- [x] 4.2 Update `internal/gateway/templates/fragments/vehicles_templ.go` + template to stop rendering the `State` field (show display name + VIN only). NOTE: `vehicles_templ.go` (generated) was hand-edited to match; user should run `templ generate` to normalize (identical output expected).
- [x] 4.3 Update `fragments.Vehicle` struct: remove the `State` field
- [x] 4.4 Extend `fakeAccount` in `internal/gateway/handlers/handlers_test.go` to satisfy the widened `account.Service` interface (add `RegisteredVehicles`/`SeedVehicles`), and add/update tests: registered→renders-without-Tesla-call, empty→calls-Tesla-then-seeds, no-state-rendered, and the existing connect/unauthorized/empty-notice scenarios still pass
- [x] 4.5 Run `go test ./...` and the account integration tests with `DATABASE_URL` set; fix any provider/consumer breakage from the dropped `State`

## 5. Spec / Docs Sync

- [x] 5.1 Confirm `openspec/specs/account-vehicle-registry/spec.md` and the `gateway` delta are coherent with the implemented behavior; reconcile naming (e.g. final method names) between code and spec
- [x] 5.2 Run `openspec status --change persist-tesla-vehicles` and ensure all tasks are checked as completed before requesting verification/archive