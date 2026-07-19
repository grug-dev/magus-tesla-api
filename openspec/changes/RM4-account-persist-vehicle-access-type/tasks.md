> **Backward-compatible, non-breaking change** (tier 2 of roadmap
> `RM4-charge-form-required-location-and-vehicle-autoselect`). Adds a nullable `access_type TEXT`
> column to `vehicles` via a goose migration, surfaces it on all three domain types, and plumbs it
> through the seed INSERT and both read queries. sqlc regenerated (`package accountdb`). No Tesla
> API calls, no cross-module wiring, no interface-signature changes.
>
> **Dependencies / parallelism:**
> - Task 1 (migration) has no dependencies. It is independent of all Go changes.
> - Task 2 (domain types) has no dependencies. It is independent of Task 1 and MAY run in parallel.
> - Task 3 (sqlc query changes + regeneration) depends on Task 1 (schema must exist for sqlc to
>   validate) — in practice sqlc is run after the migration is written but before the DB is migrated;
>   the migration file is the schema source. Task 3 is independent of Task 2 (disjoint files:
>   `query.sql` vs `account.go`) and MAY run in parallel with Task 2.
> - Task 4 (service.go mapping) depends on **both** Task 2 and Task 3 — it references the domain
>   types from Task 2 and the generated `accountdb` params/rows from Task 3.
> - Task 5 (integration tests) depends on Task 4.
> - Task 6 (verification) depends on Tasks 1–5.

## 1. Goose migration — no dependencies

- [ ] 1.1 Create `internal/account/db/migrations/20260720000001_vehicles_add_access_type.sql`
      containing a goose `-- +goose Up` that runs:
      ```sql
      ALTER TABLE vehicles
          ADD COLUMN access_type TEXT
          CHECK (access_type IS NULL OR access_type IN ('OWNER','DRIVER'));
      ```
      and a `-- +goose Down` that runs `ALTER TABLE vehicles DROP COLUMN IF EXISTS access_type;`.
      Add a comment referencing `design.md` D1 (nullable, CHECK constraint, no DEFAULT, no index).

## 2. Domain types (`internal/account/account.go`) — independent of 1, parallel-ok

- [ ] 2.1 Add `AccessType *string` to the `Vehicle` struct. Document with a comment: `nil` means
      the value was not captured at seed time (predates this change or caller supplied nil); `"OWNER"`
      or `"DRIVER"` reflect Tesla's per-vehicle access type.
- [ ] 2.2 Add `AccessType *string` to the `OwnedVehicle` struct with the same doc comment.
- [ ] 2.3 Add `AccessType *string` to the `SeedVehicle` struct. Document: the gateway (tier 4) will
      set this from `VehicleTesla.AccessType`; callers that do not supply it leave it `nil`, which
      is stored as `NULL`.
      Note: Do NOT add `AccessType` to the `Service` interface signatures — `SeedVehicles`,
      `RegisteredVehicles`, and `AllRegisteredVehicles` are unchanged.

## 3. sqlc queries + regeneration (`internal/account/db/query.sql`) — depends on 1 (schema source), parallel-ok with 2

- [ ] 3.1 Update `InsertVehicleIfMissing` to add `access_type` to the INSERT column list and the
      `VALUES` bind variables (`@access_type`). The `ON CONFLICT DO NOTHING` clause is UNCHANGED.
- [ ] 3.2 Confirm `ListVehiclesByAccount` selects `access_type`. If it uses `SELECT *`, the new
      column is automatically included — verify; if an explicit column list exists, add `access_type`.
- [ ] 3.3 Confirm `ListAllVehicles` includes `access_type`. The query currently selects an explicit
      list (`account_id, tesla_id, vin, display_name`) — add `access_type` to that list.
- [ ] 3.4 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/account/db/`. Confirm that
      `InsertVehicleIfMissingParams` gains an `AccessType pgtype.Text` field, that
      `Vehicle` (the sqlc row type) gains `AccessType pgtype.Text`, and that
      `ListAllVehiclesRow` gains `AccessType pgtype.Text`. No other module's generated code should change.

## 4. Service mapping (`internal/account/service.go`) — depends on 2 and 3

- [ ] 4.1 Add two private helper functions for nullable TEXT ↔ `*string` conversion at the
      DB→domain boundary (these are NEW because the existing `textFromString` maps `string → pgtype.Text`
      treating `""` as NULL, which is wrong for `*string`):
      ```go
      // nullableTextToPtr maps a nullable pgtype.Text to *string; nil when not valid.
      func nullableTextToPtr(t pgtype.Text) *string { ... }
      // textPtrToNullable maps a *string to pgtype.Text; invalid (NULL) when nil.
      func textPtrToNullable(s *string) pgtype.Text { ... }
      ```
- [ ] 4.2 Update `vehicleFromRow` to map `v.AccessType` (pgtype.Text) to `*string` using
      `nullableTextToPtr`.
- [ ] 4.3 Update `ownedVehicleFromRow` to map `v.AccessType` (pgtype.Text) to `*string` using
      `nullableTextToPtr`.
- [ ] 4.4 Update `SeedVehicles` — the `InsertVehicleIfMissingParams` literal gains
      `AccessType: textPtrToNullable(v.AccessType)`. No other logic changes (ON CONFLICT DO NOTHING
      is preserved exactly).
- [ ] 4.5 Verify that `pgtype.Text` does not appear in any public type signature — it must be
      confined to the `service.go` boundary helpers and the sqlc-generated `accountdb` package.

## 5. Integration tests (`internal/account/service_integration_test.go`) — depends on 4

- [ ] 5.1 Add `DATABASE_URL`-gated integration coverage (self-skips when unset, per the module's
      existing pattern) for `access_type` round-trip:
      - Seed a vehicle with `AccessType = ptr("OWNER")` → `RegisteredVehicles` returns
        `AccessType = ptr("OWNER")`.
      - Seed a vehicle with `AccessType = ptr("DRIVER")` → `RegisteredVehicles` returns
        `AccessType = ptr("DRIVER")`.
      - Seed a vehicle with `AccessType = nil` → `RegisteredVehicles` returns `AccessType = nil`.
      - `AllRegisteredVehicles` surfaces `access_type` correctly for all three cases.
      - Re-seeding an existing vehicle with a different `access_type` leaves the stored value
        unchanged (idempotency of ON CONFLICT DO NOTHING).
      - Seeding with an invalid `access_type` (e.g. `ptr("ADMIN")`) produces a DB constraint error.

## 6. Verification — depends on 1–5

- [ ] 6.1 `go build ./...` and `go vet ./...` pass.
- [ ] 6.2 `go test ./...` green and fast; account integration tests self-skip without `DATABASE_URL`
      (and pass with it set); no Tesla API call fires from the test run.
- [ ] 6.3 `openspec validate --strict RM4-account-persist-vehicle-access-type` passes and every
      tasks.md checkbox above reflects real completion.
