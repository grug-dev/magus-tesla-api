> **Post-deploy recovery — required, not optional (D2).** Immediately after `make migrate-up` runs
> this change's migration, every existing account — including the project owner's own — becomes
> `Inactive` with no automatic backfill to `Active`. Run this once, by hand, right after the
> migration:
> ```sql
> UPDATE accounts SET status = 'Active' WHERE email = 'rasputin999@gmail.com';
> ```
> Until that runs, nobody can sign in once tier 2 (`RM34-gateway-block-inactive-login`) ships.
>
> **Scope.** Additive, non-breaking-schema (but deliberately access-breaking-in-effect, see above)
> column change to both `accounts` and `vehicles` (tier 1 of roadmap
> `RM34-account-vehicle-status`). Adds `status TEXT NOT NULL CHECK (status IN
> ('Active','Inactive'))` via one goose migration — `accounts` defaults `Inactive` (D2), `vehicles`
> defaults `Active` (D3), neither needs a separate backfill `UPDATE` (design.md D8). Adds
> `StatusActive`/`StatusInactive` constants and an `Account.Status` field, filters five sqlc
> queries, and repairs the one existing test this breaks. No gateway wiring — tier 2 is a separate,
> dependent OpenSpec change.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of all Go-only changes; MAY run in
>   parallel with T2.
> - T2 (domain constants + `Account.Status` field in `account.go`) has no dependencies. Independent
>   of T1 (disjoint files) and MAY run in parallel with it.
> - T3 (sqlc query edits + regeneration) depends on T1 — the migration is sqlc's schema source, so
>   the column must exist there before `make sqlc` can produce fields that reference it. T3 is
>   independent of T2 (disjoint files: `query.sql` vs `account.go`) and MAY run in parallel with T2.
> - T4 (`service.go` mapping update) depends on **both** T2 and T3 — it maps the sqlc-generated
>   `Status` field (T3) onto the domain field added in T2.
> - T5 (existing-test repair) depends on T4.
> - T6 (`AGENTS.md` docs update) depends on T2 only — MAY run in parallel with T3/T4/T5.
> - T7 (verification) depends on T1–T6.
>
> **Leader-integrated step:** run `make sqlc` after T3.1–T3.2 (the `query.sql` edits) to regenerate
> `accountdb`. The existing `sql:` entry in `sqlc.yaml` already covers the account module; no
> structural `sqlc.yaml` change is needed. Do not hand-edit `db/models.go` or `db/query.sql.go` —
> both are sqlc-generated.

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies

- [ ] T1.1 Create `internal/account/db/migrations/<timestamp>_accounts_vehicles_add_status.sql`
      (use the next chronological timestamp after `20260813000001_accounts_add_language.sql`,
      following the module's `<YYYYMMDDHHMMSS>_<name>.sql` convention) with the exact DDL from
      `design.md` D1:

      ```sql
      -- +goose Up
      ALTER TABLE accounts
          ADD COLUMN status TEXT NOT NULL DEFAULT 'Inactive'
          CHECK (status IN ('Active','Inactive'));

      ALTER TABLE vehicles
          ADD COLUMN status TEXT NOT NULL DEFAULT 'Active'
          CHECK (status IN ('Active','Inactive'));

      -- +goose Down
      ALTER TABLE vehicles DROP COLUMN IF EXISTS status;
      ALTER TABLE accounts DROP COLUMN IF EXISTS status;
      ```

      Include a header comment referencing `design.md` D1 (TEXT+CHECK, not a native enum — mirrors
      `access_type`; title-case values by explicit ticket request), D2 (accounts default
      `Inactive`, deliberately no backfill — every existing account becomes `Inactive`), D3
      (vehicles default `Active` — the opposite default, a correctness gate preventing an infinite
      paid Fleet API reseed loop, see design.md D3 for the full trace), and D8 (the `DEFAULT`
      clause alone realizes both backfills — no separate `UPDATE` statement for either table).
      Acceptance: `goose status` shows the migration applied when `make migrate-up` runs; every
      pre-existing `accounts` row reads `status = 'Inactive'` and every pre-existing `vehicles` row
      reads `status = 'Active'` with zero rows failing the `CHECK`; `goose down` (one step) removes
      both columns without error.

## T2. Domain constants + `Account.Status` field (`internal/account/account.go`) — no dependencies, parallel-ok with T1

- [ ] T2.1 Add the closed status vocabulary to `account.go`, near the existing
      `LanguageES`/`LanguageEN` constants (mirror their doc-comment style):
      ```go
      // StatusActive and StatusInactive are the two supported record-status values —
      // the entire closed vocabulary this module accepts for both accounts and
      // vehicles (roadmap RM34 decision D1). Consumers should reference these
      // constants rather than the string literals.
      const (
          StatusActive   = "Active"
          StatusInactive = "Inactive"
      )
      ```
- [ ] T2.2 Add a `Status string` field to the `Account` struct, with a doc comment explaining the
      default and the exemption (design.md D2/D4):
      ```go
      // Status is the account's activation status: always exactly StatusActive or
      // StatusInactive. A newly provisioned account defaults to StatusInactive
      // (roadmap RM34 decision D2) and stays that way until changed by hand — there
      // is no automatic activation path. UpsertFromOAuth is the one account
      // operation NOT filtered by Status; every other account read in this module's
      // Service treats an Inactive account as though it does not exist.
      Status string
      ```
      Place it after `DisplayName` and before `CreatedAt`, matching the column order in
      `accountdb.Account` (status is added after `updated_at`... — order in the Go struct does not
      need to match column order; place it wherever reads most naturally, e.g. immediately after
      `DisplayName`).
      Acceptance: `go build ./...` fails to compile only if a later task's mapping is inconsistent
      with this field's name/type — verify after T4.

## T3. sqlc query edits + regeneration (`internal/account/db/query.sql`) — depends on T1, parallel-ok with T2

- [ ] T3.1 Add `AND status = 'Active'` to `GetAccountByProviderID`, `GetAccountLanguage`, and
      `UpdateAccountLanguage`:
      ```sql
      -- name: GetAccountByProviderID :one
      SELECT * FROM accounts
      WHERE provider = @provider AND provider_id = @provider_id AND status = 'Active';

      -- name: GetAccountLanguage :one
      SELECT language FROM accounts
      WHERE id = @id AND status = 'Active';

      -- name: UpdateAccountLanguage :exec
      UPDATE accounts
      SET language   = @language,
          updated_at = now()
      WHERE id = @id AND status = 'Active';
      ```
      Add a comment above each referencing design.md D4, and above `UpdateAccountLanguage`
      specifically note the documented no-op-on-inactive-account consequence (design.md D4's
      "Behavioral consequence" note).
- [ ] T3.2 Add `AND status = 'Active'` to `ListVehiclesByAccount` and add a `WHERE status =
      'Active'` to `ListAllVehicles`:
      ```sql
      -- name: ListVehiclesByAccount :many
      SELECT * FROM vehicles
      WHERE account_id = @account_id AND status = 'Active'
      ORDER BY tesla_id;

      -- name: ListAllVehicles :many
      SELECT account_id, tesla_id, vin, display_name, access_type, exterior_color, car_type FROM vehicles
      WHERE status = 'Active'
      ORDER BY account_id, tesla_id;
      ```
      Do **not** modify `UpsertAccountFromOAuth` or `InsertVehicleIfMissing` — both stay unfiltered
      per D4/D3, and both already pick up the new column automatically (`RETURNING *` / the column
      takes its `DEFAULT` on insert since neither query specifies `status`).
- [ ] T3.3 Run `make sqlc`. Inspect the regenerated `internal/account/db/models.go` and confirm
      `accountdb.Account.Status` is plain `string` (expected, since the column is `NOT NULL` —
      matching every other `NOT NULL TEXT` column in this module's generated code, e.g. `Email`,
      `Provider`). Report the actual generated type; do not assume it (`ai/go-conventions.md`
      §Persistence).
      Acceptance: `internal/account/db/models.go` and `internal/account/db/query.sql.go` are
      regenerated (git diff shows sqlc's own changes only — no hand edits); `go build ./...`
      succeeds on the `internal/account/db` package in isolation.

## T4. `service.go` mapping (`internal/account/service.go`) — depends on T2 and T3

- [ ] T4.1 Update `accountFromRow` to map the new field:
      ```go
      func accountFromRow(a accountdb.Account) Account {
          return Account{
              ID:          a.ID,
              Email:       a.Email,
              Provider:    a.Provider,
              ProviderID:  a.ProviderID,
              DisplayName: a.DisplayName.String,
              Status:      a.Status,
              CreatedAt:   a.CreatedAt.Time,
              UpdatedAt:   a.UpdatedAt.Time,
          }
      }
      ```
      Adjust to whatever Go type T3.3 actually confirmed (convert via `textFromString`-style
      helpers only if sqlc produced something other than plain `string` — not expected, but verify
      per T3.3).
      Acceptance: `go build ./...` and `go vet ./...` pass; `pgtype` still does not appear in any
      public type signature — it stays confined to `service.go` and the generated `accountdb`
      package, exactly as every prior persistence change in this module requires.

## T5. Existing-test repair (`internal/account/service_integration_test.go`) — depends on T4

- [ ] T5.1 Repair `TestLanguagePreference_RoundTrip` per `design.md`'s "Test Contract" section:
      immediately after provisioning the test account (`UpsertFromOAuth`) and registering its
      cleanup (`deleteAccount`), activate it with a direct SQL statement before exercising any
      language read/write:
      ```go
      if _, err := pool.Exec(ctx,
          "UPDATE accounts SET status = 'Active' WHERE id = $1", acct.ID,
      ); err != nil {
          t.Fatalf("activating test account: %v", err)
      }
      ```
      This mirrors the same test's existing out-of-band-write technique used later in its body (the
      `language = 'fr'` normalization step) — no new helper function is needed for a single call
      site. Every other assertion in the test body is unchanged (design.md "Test Contract": all
      existing expected values hold once the account is active).
      Acceptance: the test, read statically, exercises `LanguageFor`/`SetLanguage` only after the
      activation `UPDATE` — confirm by inspection since the owner runs the suite
      (`Test-Execution-Policy`).
- [ ] T5.2 Add the one new assertion design.md's "Test Contract" recommends locking in: after
      `UpsertFromOAuth` in `TestUpsertFromOAuth_Idempotent`, assert both the first and second
      returned `Account.Status == account.StatusInactive`.
      Acceptance: same as T5.1 — inspection-verified, `go vet ./...` compiles it.
- [ ] T5.3 Confirm by inspection (not execution) that no other existing test in
      `service_integration_test.go` or `service_test.go` needs a behavioral change: every vehicle
      each test creates takes the new column's `DEFAULT 'Active'`, and every vehicles-scoped query
      those tests exercise (`ListVehiclesByAccount` via `RegisteredVehicles`, `ListAllVehicles` via
      `AllRegisteredVehicles`) now includes a `status = 'Active'` predicate that matches by
      construction. List, in the final report, which test functions were reviewed and found
      unaffected.

## T6. Docs (`internal/account/AGENTS.md`) — depends on T2, parallel-ok with T3/T4/T5

- [ ] T6.1 Update `internal/account/AGENTS.md`'s "Public interface" section to note that
      `Account` now carries a `Status` field (`StatusActive`/`StatusInactive`), that
      `UpsertFromOAuth` is the one operation not filtered by it, and that every other read in this
      module's `Service` treats an `Inactive` account or vehicle as though it does not exist. Keep
      the addition to a few lines, consistent with the file's existing terse style — this is
      required by `CLAUDE.md`'s docs-track-change rule (a module's public surface changed in this
      change, not a follow-up).

## T7. Verification — depends on T1–T6

- [ ] T7.1 `go build ./...` and `go vet ./...` pass repo-wide. If the widened `Account` struct or
      the new `Service` behavior breaks compilation of a fake/double in a sibling module's test
      file (`internal/gateway`'s tests are the likely candidate, since tier 2 depends on this
      field), that is a leader-owned cross-module concern to flag — do not edit outside
      `internal/account`.
- [ ] T7.2 `gofmt -l` reports no diffs for any file this tier touched.
- [ ] T7.3 Boundary check: `internal/account` still does not import `internal/tesla` or
      `internal/gateway`; `pgtype` does not appear in any public type or interface signature; no
      file outside `internal/account` (other than a leader-owned cross-module fix per T7.1) was
      touched.
- [ ] T7.4 State in the final report, verbatim, the post-deploy recovery SQL from D2 (also at the
      top of this file) so it is not lost between this tier's completion and deployment:
      ```sql
      UPDATE accounts SET status = 'Active' WHERE email = 'rasputin999@gmail.com';
      ```
- [ ] T7.5 `openspec validate RM34-account-add-record-status --strict` passes and every tasks.md
      checkbox above reflects real completion.
- [ ] T7.6 Report the exact test-suite commands the owner must run
      (`go test ./internal/account/... -run TestLanguagePreference_RoundTrip` and the full
      `go test ./...` / `make test-with-db`) — this tier writes/repairs tests but does not execute
      them (`Test-Execution-Policy`); the owner's run is what turns T5 from
      `awaiting-user-verification` into `done`.
