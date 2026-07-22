> **Behavior-change tier, non-breaking to existing DB data (migration is defensive).** Promotes
> `location_kind` from optional → required in `manual_charge_entries` (migration) and in the
> `manualcharge.Writer` service layer (validation). No new module, no new query, no sqlc
> regeneration. The domain field `LocationKind *string` stays a pointer for gateway
> compatibility before Tier 4.
>
> **Dependencies / parallelism:**
>
> - T1 (migration) has no code dependencies — it only creates a SQL file. It can be authored
>   in parallel with T2. Applying it (`make migrate-up`) must wait until authoring is complete.
> - T2 (service validation) depends on the existing service.go structure (no migration
>   dependency for authoring; the validation runs before any DB call). Can be authored in
>   parallel with T1.
> - T3 (integration tests) depends on T1 (migration applied) and T2 (validation in place).
>   Can be authored in parallel but must run after T1 + T2 are applied.
> - T4 (verification) depends on T1, T2, T3 being complete and applied.
>
> **Leader-integrated tasks (outside the `internal/manualcharge` sandbox):**
> - Leader: run `make migrate-up` after T1 to apply the migration.
> - Leader does NOT need to run `make sqlc` — no query.sql changes, no new/changed sqlc types.

---

## T1. Migration — require location_kind NOT NULL (`internal/manualcharge/db/migrations/`) — no dependencies

- [x] T1.1 Create goose migration file
      `internal/manualcharge/db/migrations/20260720000001_require_location_kind.sql`.
      Use timestamp `20260720000001` (next sequential after `20260718000001`).
- [x] T1.2 Write `-- +goose Up` block with two statements in this exact order:
      (a) `UPDATE manual_charge_entries SET location_kind = 'OTHER' WHERE location_kind IS NULL;`
          — defensive backfill; a no-op on a freshly wiped DB but required for safety on any
          non-empty environment. Add a comment explaining the 'OTHER' choice and that the order
          (UPDATE before ALTER) is mandatory (backfill must precede the constraint or ALTER fails).
      (b) `ALTER TABLE manual_charge_entries ALTER COLUMN location_kind SET NOT NULL;`
          — promotes the column to NOT NULL. The existing
          `CHECK (location_kind IN ('HOME','WORK','OTHER'))` constraint is left untouched.
      Add NO `DEFAULT` clause to the column (design D2 — DEFAULT would mask a missing field
      instead of failing loudly).
- [x] T1.3 Write `-- +goose Down` block:
      `ALTER TABLE manual_charge_entries ALTER COLUMN location_kind DROP NOT NULL;`
      Add a comment noting that the backfill UPDATE is NOT reversed — rows that were NULL before
      the Up migration now carry 'OTHER' and this is acceptable (schema rollback, not data
      rollback).
- [ ] T1.4 Verify migration file parses: run
      `goose -dir internal/manualcharge/db/migrations postgres "$DATABASE_URL" validate`
      (or `make migrate-up` if DATABASE_URL is set). Acceptance: no goose parse errors.

---

## T2. Service validation — location_kind required in Create and Update (`internal/manualcharge/service.go`) — no dependencies (parallel with T1)

- [x] T2.1 In `writerService.Create`, add a required-field check for `e.LocationKind` BEFORE
      the `numericFromFloat64` calls and before `params` is built:
      ```go
      if e.LocationKind == nil || *e.LocationKind == "" {
          return Entry{}, errors.New("manualcharge: location_kind is required")
      }
      ```
      This check must fire before any pgtype conversion or database call. Add `"errors"` to the
      import block if not already present.
- [x] T2.2 In `writerService.Update`, add the identical check for `e.LocationKind` in the same
      position (before `numericFromFloat64` and before `params` is built):
      ```go
      if e.LocationKind == nil || *e.LocationKind == "" {
          return Entry{}, errors.New("manualcharge: location_kind is required")
      }
      ```
      Acceptance: the check appears in both `Create` and `Update`; the error message follows the
      existing `"manualcharge: ..."` prefix convention in the file.
- [x] T2.3 Confirm that `go build ./internal/manualcharge/...` passes with no errors after the
      change (no new imports beyond `"errors"` if not already present; the domain type
      `LocationKind *string` is unchanged).

---

## T3. Integration tests — location_kind required scenarios (`internal/manualcharge/db_integration_test.go`) — depends on T1 applied + T2 complete

- [x] T3.1 Add integration test: `TestCreate_RejectsNilLocationKind` — call
      `Writer.Create` with `e.LocationKind = nil`; assert the returned error is non-nil
      and contains the expected message (e.g., "location_kind is required"). Assert no row was
      inserted (confirm via `ListEntriesByAccount` returning empty slice for that account).
- [x] T3.2 Add integration test: `TestCreate_RejectsEmptyLocationKind` — call
      `Writer.Create` with `e.LocationKind = ptr("")`; assert the returned error is non-nil.
- [x] T3.3 Add integration test: `TestCreate_AcceptsHOME` — call `Writer.Create` with
      `location_kind = "HOME"`; assert no error and the returned entry has
      `LocationKind != nil && *entry.LocationKind == "HOME"`.
- [x] T3.4 Add integration test: `TestCreate_AcceptsWORK` — same pattern with `"WORK"`.
- [x] T3.5 Add integration test: `TestCreate_AcceptsOTHER` — same pattern with `"OTHER"`.
- [x] T3.6 Add integration test: `TestUpdate_RejectsNilLocationKind` — create a valid entry,
      then call `Writer.Update` with the same entry but `LocationKind = nil`; assert the
      returned error is non-nil. Assert the original row is unchanged (read back via
      `ListEntriesByAccount`).
- [x] T3.7 Add integration test: `TestUpdate_AcceptsLocationKindChange` — create an entry with
      `location_kind = "HOME"`, then update it to `location_kind = "WORK"`; assert no error and
      the returned entry has `*entry.LocationKind == "WORK"`. Assert `updated_at` advanced.
- [x] T3.8 All new integration tests MUST skip when `DATABASE_URL` is unset (use the existing
      `t.Skip` pattern already in the file). No Tesla API call fires.

---

## T4. Verification — depends on T1 applied, T2, T3 complete

- [x] V1. `go build ./...` passes — no compile errors in `internal/manualcharge` or any file
      that imports it (gateway build must still compile with `LocationKind *string` unchanged).
- [x] V2. `go vet ./...` passes with no warnings in `internal/manualcharge`.
- [x] V3. `go test ./internal/manualcharge/...` passes:
      - Existing unit tests (derived methods) still pass offline with no DB.
      - New integration tests (T3.1–T3.7) pass when `DATABASE_URL` is set; self-skip when unset.
      - No Tesla API call fires.
- [ ] V4. **DEFERRED to the user's DB wipe + re-migrate (RD3/RD4)** — no live DB in the pipeline
      session. Migration applied cleanly: `make migrate-up` runs without error; the `location_kind`
      column in `manual_charge_entries` is `NOT NULL` (verify with
      `\d manual_charge_entries` in psql or equivalent). Migration file authored + reviewed.
- [x] V5. Boundary check: `internal/manualcharge` still does NOT import `internal/tesla`,
      `internal/account`, `internal/telemetry`, or any other module's internals.
      `pgtype` does not appear in any public type, interface, or function signature.
- [x] V6. No sqlc regeneration was needed — the generated `manualchargedb` package is unchanged.
      Confirm with `git diff internal/manualcharge/db/*.go` (should show no changes).
- [x] V7. Compile-time interface assertions still hold:
      `var _ Writer = (*writerService)(nil)` and `var _ Reader = (*readerService)(nil)`
      both compile without errors (already present in service.go; verify they pass with `go build`).
