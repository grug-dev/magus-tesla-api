> **Additive, non-breaking column + port-method change** (tier 1 of roadmap
> `RM6-static-vehicle-config-fields`). Adds two nullable `exterior_color TEXT` / `car_type TEXT`
> columns to `vehicles` via a goose migration, surfaces them on `Vehicle` and `OwnedVehicle`, adds
> a new conditional-update port method `SetVehicleConfigIfEmpty` backed by a new
> `UpdateVehicleConfigIfEmpty` sqlc query, and extends both `ListVehiclesByAccount` and
> `ListAllVehicles` to project the two columns. sqlc regenerated (`package accountdb`).
> `SeedVehicles` is UNCHANGED — Tesla's `ListVehicles` does not return `vehicle_config`. No Tesla
> API calls, no cross-module wiring (tier 2, owned by `internal/telemetry`, is a separate,
> dependent OpenSpec change that consumes the port added here).
>
> **Dependencies / parallelism:**
>
> - T1 (goose migration) has no dependencies. It is independent of all Go changes and MAY run in
>   parallel with T2.
> - T2 (domain types + port interface signature in `account.go`) has no dependencies. It is
>   independent of T1 (disjoint files) and MAY run in parallel with it.
> - T3 (sqlc query edits + regeneration) depends on T1 (the migration is sqlc's schema source —
>   `sqlc generate` reads the goose migration files directly, so the column must exist there before
>   `make sqlc` can produce the new fields). T3 is independent of T2 (disjoint files: `query.sql`
>   vs `account.go`) and MAY run in parallel with T2.
> - T4 (`service.go` implementation + mapping) depends on **both** T2 and T3 — it implements the
>   interface method added in T2 using the sqlc-generated types produced in T3.
> - T5 (integration tests) depends on T4.
> - T6 (verification) depends on T1–T5.
>
> **Leader-integrated step:** run `make sqlc` after T3.1–T3.3 (the `query.sql` edits) to regenerate
> `accountdb`. The existing `sql:` entry in `sqlc.yaml` already covers the account module; no
> structural `sqlc.yaml` change is needed. Do not hand-edit `db/models.go` or `db/query.sql.go` —
> both are sqlc-generated.

---

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies

- [ ] T1.1 Create `internal/account/db/migrations/20260803000001_vehicles_add_config_fields.sql`
      with the exact DDL from `design.md` D1 (reproduced here for implementer convenience):

      ```sql
      -- +goose Up
      ALTER TABLE vehicles
          ADD COLUMN exterior_color TEXT,
          ADD COLUMN car_type TEXT;

      -- +goose Down
      ALTER TABLE vehicles
          DROP COLUMN IF EXISTS exterior_color,
          DROP COLUMN IF EXISTS car_type;
      ```

      Include a header comment referencing `design.md` D1 (nullable, no `DEFAULT`, no `CHECK` —
      unlike `access_type`, these are open-ended Tesla enums) and D2 (no index — neither column is
      ever a `WHERE`/`JOIN`/`ORDER BY` predicate; both are read as part of the heap row already
      located by the existing `UNIQUE (account_id, tesla_id)` constraint).
      Acceptance: `goose status` shows the migration as applied when `make migrate-up` is run; no
      existing rows fail (both columns are nullable with no `DEFAULT`); `goose down` (one step)
      removes both columns without error.

## T2. Domain types + port interface (`internal/account/account.go`) — no dependencies, parallel-ok with T1

- [ ] T2.1 Add `ExteriorColor *string` and `CarType *string` to the `Vehicle` struct, immediately
      after the existing `AccessType *string` field. Doc comment per design D4: `nil` means the
      value has not yet been captured (the vehicle predates capture, or the nightly telemetry
      collector has not yet run for it since seeding); a non-nil value is the Tesla-reported value
      verbatim (e.g. `"PearlWhite"`, `"modely"`).
- [ ] T2.2 Add `ExteriorColor *string` and `CarType *string` to the `OwnedVehicle` struct, same
      position, same doc comment.
      Note: do NOT add these fields to `SeedVehicle` — `SeedVehicles` is unchanged (proposal.md
      "What Changes"); Tesla's `ListVehicles` response does not include `vehicle_config`, so there
      is nothing for the seed path to carry.
- [ ] T2.3 Add `SetVehicleConfigIfEmpty` to the `Service` interface, with the doc comment from
      `design.md` D4:
      ```go
      // SetVehicleConfigIfEmpty persists exteriorColor and carType for the vehicle identified by
      // (accountID, teslaID), but ONLY while at least one of the two is still uncaptured. Once
      // a vehicle has both exterior_color and car_type non-NULL, subsequent calls are no-ops (the
      // underlying WHERE clause matches zero rows). On a partially-captured row the write DOES
      // rewrite both columns, including the one already set — harmless, because both values are
      // immutable and come from the same vehicle_config payload, and it is what lets a partial
      // row self-heal (design.md D4/RD2). Callers MUST pass non-empty strings; this module does not
      // reject an empty string itself (it does not import internal/tesla and treats its inputs as
      // opaque strings) — skipping the call when either observed value is empty is the caller's
      // responsibility (see the account-vehicle-registry spec delta, "Static Vehicle Config
      // Capture").
      SetVehicleConfigIfEmpty(ctx context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error
      ```
      Place it after `SeedVehicles` (the last existing method) in the interface.
      Acceptance: `go build ./...` fails at this point (no implementation yet) — expected; this
      task only changes the interface + domain structs. Confirm the failure is exactly "does not
      implement Service" pointing at the missing method, i.e. no unrelated compile errors.

## T3. sqlc query edits + regeneration (`internal/account/db/query.sql`) — depends on T1, parallel-ok with T2

- [ ] T3.1 Add a new query to `query.sql`, immediately after `InsertVehicleIfMissing` (or in a
      sensible place near the other `vehicles`-table queries):
      ```sql
      -- name: UpdateVehicleConfigIfEmpty :exec
      -- Conditional write-back for the two static vehicle_config attributes (design.md D4). The
      -- WHERE clause uses OR (not AND): a row missing only one of the two values is still eligible
      -- for a self-healing write, and a row with both already captured never matches (defense in
      -- depth — RD2 — independent of whatever Go-side guard the caller applies). updated_at only
      -- moves when the WHERE clause actually matches a row.
      UPDATE vehicles
      SET exterior_color = @exterior_color,
          car_type        = @car_type,
          updated_at      = now()
      WHERE account_id = @account_id
        AND tesla_id    = @tesla_id
        AND (exterior_color IS NULL OR car_type IS NULL);
      ```
- [ ] T3.2 Add `exterior_color, car_type` to the `ListVehiclesByAccount` query's `SELECT` list (it
      currently uses `SELECT *`, in which case the two new columns are already included
      automatically once T1 lands — verify by inspecting the query; if it uses an explicit column
      list, add both column names).
- [ ] T3.3 Add `exterior_color, car_type` to `ListAllVehicles`'s explicit `SELECT` column list
      (currently `account_id, tesla_id, vin, display_name, access_type`) — append the two new
      column names.
- [ ] T3.4 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/account/db/`. Confirm that:
      - `accountdb.Vehicle` (the sqlc row type in `models.go`) gains `ExteriorColor pgtype.Text`
        and `CarType pgtype.Text`.
      - `accountdb.ListAllVehiclesRow` gains the same two fields.
      - `accountdb.UpdateVehicleConfigIfEmptyParams` is generated with `AccountID uuid.UUID`,
        `TeslaID int64`, plus a field per bind param. **Do not assume the Go type of the two
        config bind params** — sqlc may infer `string` or `pgtype.Text` depending on how it reads
        the nullable target column. Use whatever it actually generates: if `pgtype.Text`, convert
        the method's plain `string` arguments with the existing `textFromString` helper in
        `service.go` (the same helper `DisplayName` already uses); if `string`, pass through
        directly. Report which one sqlc produced.
      No other module's generated code should change.

## T4. Service implementation (`internal/account/service.go`) — depends on T2, T3

- [ ] T4.1 Implement `SetVehicleConfigIfEmpty` on `*service`:
      ```go
      func (s *service) SetVehicleConfigIfEmpty(ctx context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error {
          if err := s.q.UpdateVehicleConfigIfEmpty(ctx, accountdb.UpdateVehicleConfigIfEmptyParams{
              AccountID:     accountID,
              TeslaID:       teslaID,
              ExteriorColor: exteriorColor,
              CarType:       carType,
          }); err != nil {
              return fmt.Errorf("setting vehicle config for tesla_id=%d: %w", teslaID, err)
          }
          return nil
      }
      ```
      Place it after `SeedVehicles` (mirroring the interface's method order from T2.3).
- [ ] T4.2 Update `vehicleFromRow` to map `v.ExteriorColor` and `v.CarType` (both `pgtype.Text`) to
      `*string` using the existing `nullableTextToPtr` helper — do NOT write a new helper:
      ```go
      ExteriorColor: nullableTextToPtr(v.ExteriorColor),
      CarType:       nullableTextToPtr(v.CarType),
      ```
- [ ] T4.3 Update `ownedVehicleFromRow` with the same two lines (same helper, same source columns
      from `accountdb.ListAllVehiclesRow`).
- [ ] T4.4 Verify `pgtype.Text` still does not appear in any public type signature — it stays
      confined to `service.go` and the sqlc-generated `accountdb` package, exactly as the
      `access_type` precedent requires.
      Acceptance: `go build ./...` and `go vet ./...` pass.

## T5. Integration tests (`internal/account/service_integration_test.go`) — depends on T4

- [ ] T5.1 Add a `DATABASE_URL`-gated test `TestSetVehicleConfigIfEmpty_RoundTrip` (self-skips when
      unset, per the module's existing pattern — mirror `TestAccessType_RoundTrip`'s setup: provision
      an account via `UpsertFromOAuth`, seed one or more vehicles via `SeedVehicles`, defer
      `deleteAccount`). Cover:
      - **Fresh capture**: a vehicle seeded with `ExteriorColor = nil, CarType = nil` (the default,
        since `SeedVehicles` never sets them); call `SetVehicleConfigIfEmpty(ctx, acct.ID, teslaID,
        "PearlWhite", "modely")`; assert `RegisteredVehicles` now returns that vehicle with
        `ExteriorColor = ptr("PearlWhite")` and `CarType = ptr("modely")`.
      - **No-op on fully-captured row**: immediately call `SetVehicleConfigIfEmpty` again on the
        same vehicle with different values (e.g. `"SolidBlack"`, `"modelx"`); assert
        `RegisteredVehicles` still returns `"PearlWhite"`/`"modely"` — the second call must not
        overwrite.
      - **Self-heal on a partially-captured row**: a partial state is UNREACHABLE through the port
        (`SetVehicleConfigIfEmpty` always sets both columns together), so construct it directly.
        The test lives in `package account`, so it can reach the pool: after seeding, run a raw
        `pool.Exec(ctx, "UPDATE vehicles SET exterior_color = $1 WHERE account_id = $2 AND
        tesla_id = $3", "PearlWhite", acct.ID, teslaID)` to leave `car_type` NULL. Then call
        `SetVehicleConfigIfEmpty(ctx, acct.ID, teslaID, "PearlWhite", "modely")` and assert BOTH
        columns are now populated — proving the `OR` condition (design.md D4) matched a row that
        an `AND` condition would have frozen. Do not skip or soften this case: it is the single
        scenario that distinguishes the chosen `OR` semantics from the rejected `AND`, so it is
        the reason the query is written the way it is.
      - **`AllRegisteredVehicles` surfaces the same two fields** correctly for both the captured
        and not-yet-captured vehicle in the same account (mirror `TestAccessType_RoundTrip`'s
        `AllRegisteredVehicles` section).
      - **nil round-trip**: a vehicle with never-called config capture returns
        `ExteriorColor = nil, CarType = nil` from both `RegisteredVehicles` and
        `AllRegisteredVehicles` (not an error, not empty string).
      Acceptance: test self-skips when `DATABASE_URL` is unset; passes with it set (or via the
      testcontainers auto-provisioned Postgres per the module's `AGENTS.md` testing notes); no
      Tesla API call fires.

## T6. Verification — depends on T1–T5

- [ ] T6.1 `go build ./...` and `go vet ./...` pass.
- [ ] T6.2 `go test ./...` green and fast; account integration tests self-skip without
      `DATABASE_URL` (and pass with it set, or with Docker running for the testcontainers path); no
      Tesla API call fires from the test run.
- [ ] T6.3 Boundary check: `internal/account` still does not import `internal/tesla`;
      `pgtype` does not appear in any public type or interface; `SeedVehicle` is UNCHANGED (no
      `ExteriorColor`/`CarType` field added to it); `SeedVehicles`'s signature and `ON CONFLICT DO
      NOTHING` semantics are UNCHANGED.
- [ ] T6.4 `openspec validate RM6-account-add-vehicle-config-fields --strict` passes and every
      tasks.md checkbox above reflects real completion.
