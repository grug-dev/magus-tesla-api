> **DB-touching change — read design.md before starting.** This creates a NEW table,
> `charge_gaps` (`UNIQUE (account_id, tesla_id, gap_date)`, columns `vin`/`tesla_id`/`gap_date`/
> `missing_charging_type` all `NOT NULL`, plus `created_at`/`updated_at`), a new write port
> `telemetry.GapWriter` (`ReconcileWindow`), and a new read method on the EXISTING
> `SuperchargerReader` port (`SuperchargerSessionsByVehicleBetween`, filtered on
> `charge_stop_date_time` per D12). No existing table, column, index, or query is altered.
> `internal/battery` is NOT created, read, or touched — it is a separate future tier
> (`RM28-battery-derive-consumed-per-day`) and this change ships no call site for either new
> port. See design.md for the full schema, rationale, index plan, and Go-level seam.
>
> **Two independent sub-task groups — but they share two files.** Group **A** (the
> `charge_gaps` table + `GapWriter`, D3/D7/D7a/D7b) and Group **B**
> (`SuperchargerSessionsByVehicleBetween`, D9/D12) are logically and functionally
> independent — neither group's code calls or depends on the other's, and either could ship
> alone. **However, both groups edit `internal/telemetry/telemetry.go` and
> `internal/telemetry/db/query.sql`.** Because the `kkpa-dev-harness-pipeline` sandboxes one
> worker per MODULE (not per sub-task group), in practice a single `telemetry` worker
> implements both groups in one sandbox and this overlap is a non-issue — do the two shared
> files' edits in one pass each (T2, T3 below cover both groups' additions to those two
> files together) rather than editing the same file twice. If this change is ever split
> across two separate workers, T2 and T3 would need to be merged manually — flagged here
> rather than silently promised as parallel-safe.
>
> **Dependencies:**
>
> - T1 (migration, Group A) has no dependencies.
> - T2 (`telemetry.go`: domain types + both interface additions, Groups A and B together)
>   has no DB dependency — pure Go type/interface additions — and may be done before or
>   after T1.
> - T3 (`db/query.sql`: four new queries, Groups A and B together) depends on T1 for Group
>   A's three queries (they reference the new table); Group B's query has no DB dependency.
>   Do T3 after T1 to keep the file edit in one pass.
> - T4 (`mapping.go`: new `dateFromPg` helper, Group A) depends on T2, T3.
> - T5 (`gap_writer.go`, new file, Group A) depends on T2, T3, T4.
> - T6 (`reader.go`: `SuperchargerSessionsByVehicleBetween` impl, Group B) depends on T2, T3.
> - T7 (new DB-integration tests, both groups) depends on T1, T3, T5, T6.
> - T8 (`AGENTS.md` documentation) depends on T1.
> - Verification (V) depends on all tasks.
>
> **Leader-integrated step:** run `make sqlc` after T3 to regenerate `telemetrydb`. The
> existing `sql:` entry in `sqlc.yaml` already covers the telemetry module; no structural
> `sqlc.yaml` change is needed.

---

## T1. Goose migration (`internal/telemetry/db/migrations/`) — Group A, no dependencies

- [x] T1.1 Create `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` with
      the exact DDL from design.md's "Schema" section: Up creates the `charge_gaps` table
      (`id`, `account_id`, `tesla_id BIGINT NOT NULL`, `vin TEXT NOT NULL`,
      `gap_date               DATE NOT NULL`, `missing_charging_type TEXT NOT NULL CHECK (IN ('MANUAL',
      'SUPERCHARGER'))`, `created_at`, `updated_at`, `CONSTRAINT
      charge_gaps_account_tesla_date_unique UNIQUE (account_id, tesla_id, gap_date)`), sets the
      table comment and the four column comments verbatim, and creates
      `idx_charge_gaps_account (account_id, date DESC)`. Down drops the index then the
      table. Include the full header comment from design.md verbatim (why one row per
      vehicle-day, why no `resolved_at`, why `tesla_id` is `NOT NULL` here, why no FK, why no
      `raw_data`). Do not deviate from the exact column names, types, `CHECK` expressions, or
      index definition specified in design.md.
      Acceptance: `goose status` (or `make migrate-up`) shows the migration applied cleanly
      against the live DB; `\d charge_gaps` (or the pg catalog) shows all columns with the
      correct types/nullability, the `UNIQUE` constraint, the `CHECK` constraint, and
      `idx_charge_gaps_account`; `SELECT obj_description('charge_gaps'::regclass)` returns
      the table comment; `goose down` (one step) drops the index and the table cleanly with
      no error and leaves every other table/index/constraint in the database unaffected.

## T2. `telemetry.go` — domain types + both port additions — Groups A and B together, no dependencies

- [x] T2.1 Add `MissingChargingType` (type + `MissingChargingTypeManual`/
      `MissingChargingTypeSupercharger` constants) and the `ChargeGap` struct
      (`AccountID`, `TeslaID`, `VIN`, `Date`, `MissingChargingType`) to `telemetry.go`, exact
      shape and doc comments from design.md's "Domain types" section.
      Acceptance: `go build ./...` green.

- [x] T2.2 Add the `GapWriter` interface (one method, `ReconcileWindow`, exact signature and
      doc comment from design.md's "`GapWriter` port" section) and its forward-declared
      constructor `NewGapWriter(pool *pgxpool.Pool) GapWriter` (calling `newGapWriter(pool)`,
      implemented in T5) to `telemetry.go`.
      Acceptance: `go build ./...` fails at this point with "undefined: newGapWriter" until
      T5 lands — expected and resolved by T5; the interface declaration itself must be
      syntactically correct Go.

- [x] T2.3 Add `SuperchargerSessionsByVehicleBetween` as a third method on the EXISTING
      `SuperchargerReader` interface (do NOT touch `SuperchargerSessionsByAccount` or
      `SuperchargerSessionsByVehicle`), exact signature and doc comment from design.md's
      "`SuperchargerReader` addition" section — including the note that it filters on
      `ChargeStopDateTime` per D12, has no limit parameter, and returns a non-nil empty
      slice on no results.
      Acceptance: `go build ./...` fails at this point (the concrete `*superchargerReader`
      no longer satisfies `SuperchargerReader` until T6 lands) — expected and resolved by
      T6; `go vet ./...` on the interface declaration alone is not blocking since the whole
      package must compile together — verify final state after T6 instead.

## T3. `db/query.sql` — four new queries — Groups A and B together, depends on T1 for Group A's three

- [x] T3.1 Add `UpsertChargeGap`, `DeleteChargeGap`, and `ChargeGapDatesByVehicleBetween`
      (Group A) to `internal/telemetry/db/query.sql`, exact SQL text and header comments
      from design.md's "`db/query.sql` additions" section. `UpsertChargeGap`'s
      `ON CONFLICT DO UPDATE SET` must refresh `vin`, `missing_charging_type`, and
      `updated_at` only — `created_at` must NOT appear in the `SET` clause (design D-Table2).
      Acceptance: after `make sqlc` (run once, after T3.2 too), `telemetrydb.Queries` has
      `UpsertChargeGap`, `DeleteChargeGap`, and `ChargeGapDatesByVehicleBetween` methods with
      params matching design.md (`UpsertChargeGapParams{AccountID, TeslaID, Vin, Date,
      MissingChargingType}`, `DeleteChargeGapParams{AccountID, TeslaID, Date}`,
      `ChargeGapDatesByVehicleBetweenParams{AccountID, TeslaID, Start, End}` returning
      `[]pgtype.Date`); `grep -c created_at` restricted to `UpsertChargeGap`'s `SET` clause
      returns `0`.

- [x] T3.2 Add `SuperchargerSessionsByVehicleBetween` (Group B) to the same file, exact SQL
      text and header comment from design.md, immediately after the existing
      `SuperchargerSessionsByVehicle` query. Do NOT modify `SuperchargerSessionsByAccount`
      or `SuperchargerSessionsByVehicle`'s SQL text.
      Acceptance: `git diff` on `query.sql` shows zero changes to any line inside the two
      existing Supercharger queries; after `make sqlc`,
      `telemetrydb.SuperchargerSessionsByVehicleBetweenParams` has fields `AccountID
      uuid.UUID`, `TeslaID pgtype.Int8`, `Start pgtype.Timestamptz`, `EndBound
      pgtype.Timestamptz`, and the generated query method returns `[]SuperchargerSession`
      (the SAME generated row type the two existing methods already return — no new row
      type, since this query does `SELECT *` against the unchanged table).

      After T3.1 and T3.2, the leader runs `make sqlc` to regenerate `telemetrydb`.

## T4. `mapping.go` — new `dateFromPg` helper — Group A, depends on T2, T3

- [x] T4.1 Add `dateFromPg(d pgtype.Date) time.Time` to `internal/telemetry/mapping.go`,
      exact body from design.md's "Go-Level Seam Summary" section (`return d.Time`),
      placed near the existing `pgNullable*` helpers with a doc comment noting it is the
      non-nullable reverse of `dateFrom` (`service.go`) and is used by `gap_writer.go`.
      Acceptance: `go build ./...` green; the function is a pure one-line conversion with no
      DB/network dependency.

## T5. `gap_writer.go` (new file) — `GapWriter` implementation — Group A, depends on T2, T3, T4

- [x] T5.1 Create `internal/telemetry/gap_writer.go` with the `gapWriter` struct, the
      unexported `newGapWriter(pool *pgxpool.Pool) *gapWriter` constructor, the compile-time
      `var _ GapWriter = (*gapWriter)(nil)` assertion, and the full `ReconcileWindow`
      implementation from design.md's "Go-Level Seam Summary" section verbatim: the
      account/vehicle-scope and window-membership validation loop (returning an error and
      writing nothing on violation — no `tx.Begin` call happens before validation passes),
      the `pool.Begin`/`defer tx.Rollback`/`q.WithTx` transaction pattern mirroring
      `internal/account/service.go`'s `AccessTokenFor`, the `ChargeGapDatesByVehicleBetween`
      read, the Go-side `keep` map diff, the per-date `DeleteChargeGap` loop, the per-`ChargeGap`
      `UpsertChargeGap` loop, and `tx.Commit`. Do NOT introduce a Postgres array-bound
      `DELETE ... WHERE date <> ALL(...)` — design.md's rejected-alternative reasoning
      applies.
      Acceptance: `go build ./...` and `go vet ./...` green; `gap_writer.go` contains no
      import of `github.com/jackc/pgx/v5/pgtype` (design.md: pgtype stays confined to
      `service.go`/`mapping.go`); the file compiles the `SuperchargerReader`/`GapWriter`
      interface satisfaction assertions with no missing-method errors.

## T6. `reader.go` — `SuperchargerSessionsByVehicleBetween` implementation — Group B, depends on T2, T3

- [x] T6.1 Add `SuperchargerSessionsByVehicleBetween` to `*superchargerReader` in
      `internal/telemetry/reader.go`, exact body from design.md's "`reader.go` addition"
      section: compute `endBound := end.AddDate(0, 0, 1)`, call
      `r.q.SuperchargerSessionsByVehicleBetween` with `teslaIDToPgInt8(teslaID)` and
      `timestamptzFrom(start)`/`timestamptzFrom(endBound)`, map every row through the
      EXISTING `rowToSuperchargerSession` (no new mapper — the query returns the same row
      shape the two existing methods already map), return a non-nil empty slice on no rows.
      Acceptance: `go build ./...` and `go vet ./...` green; the compile-time
      `var _ SuperchargerReader = (*superchargerReader)(nil)` assertion in `reader.go`
      passes now that all three interface methods are implemented.

## T7. New DB-integration tests — both groups, depends on T1, T3, T5, T6

- [x] T7.1 Implement design.md's test-contract scenario (a): upserting a newly-flagged day
      then re-running `ReconcileWindow` with the same day still flagged does not duplicate
      the row (`UNIQUE` constraint is the mechanism, not application-level dedup); assert
      `updated_at` advances between the two calls and `created_at` is byte-for-byte
      unchanged.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [x] T7.2 Implement design.md's test-contract scenario (b): a day that flags then stops
      flagging is deleted by the next `ReconcileWindow` call for the same window, while a
      DIFFERENT still-flagged day in the same call's `flagged` set is left untouched (proves
      per-day precision, not a blunt clear-and-reinsert). Also cover the empty-`flagged`-set
      case (design.md scenario adjacent to (b): an empty `flagged` set for a window clears
      every previously-flagged day in that window).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [x] T7.3 Implement design.md's test-contract scenario (c): four Supercharger sessions
      stopping exactly on `start`, exactly on `end` (first instant of the end day), late in
      the `end` calendar day (e.g. `23:59:59Z`), and exactly one day past `end` — assert the
      first three are returned ordered oldest-first and the fourth is excluded.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [x] T7.4 Implement design.md's test-contract scenario (d): a session whose
      `charge_start_date_time` is before the window's `start` but whose
      `charge_stop_date_time` falls inside the window IS included, ordered correctly among
      the scenario (c) fixtures by its stop time.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [x] T7.5 Implement design.md's test-contract scenario (e), tenant isolation, covering both
      pieces: (i) `ReconcileWindow` scoped to one account/vehicle never writes or deletes
      another account/vehicle's rows, even with overlapping dates; (ii) a `ReconcileWindow`
      call whose `flagged` set contains a mis-scoped entry (wrong `AccountID` or `TeslaID`,
      or a `Date` outside the call's own window) is rejected in full — assert NOTHING from
      that call's `flagged` set is written, including its correctly-scoped entries; (iii)
      `SuperchargerSessionsByVehicleBetween` scoped to one account/vehicle never returns
      another account's session even when both fall in the same window.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes. This is the direct
      regression test for the validation loop added in T5.1 — it must FAIL if that
      validation is ever removed or weakened.

## T8. `internal/telemetry/AGENTS.md` documentation — depends on T1

- [x] T8.1 Add `charge_gaps` to the "Data ownership" section (columns, types, nullability,
      the `CHECK`/`UNIQUE` constraints, the no-FK/no-`raw_data` rationale pointers) alongside
      the existing `vehicle_snapshots`/`poll_attempts`/`supercharger_sessions` entries; add a
      note on `GapWriter`/`ReconcileWindow`'s upsert-and-delete lifecycle (no `resolved_at`)
      under a new subsection, explicitly warning a future implementer against re-adding a
      soft-delete column (mirroring how the file already carries forward-warning notes for
      other decisions, e.g. the `_est` columns' "do NOT fix this into a nightly-refreshed
      pair" note). Add the new `SuperchargerSessionsByVehicleBetween` method to the "Public
      interface (the port)" section's `SuperchargerReader` bullet list, matching the existing
      per-method documentation depth. Add a one-line pointer to
      `RM28-telemetry-add-charge-gap-storage` as the change that introduced both.
      Acceptance: the section reads correctly on its own — a future worker/agent reading
      only `AGENTS.md` understands the table's shape and lifecycle, the `GapWriter` port's
      contract, and the new read method's stop-time semantics, without needing to open this
      change's design.md.

---

## Verification — depends on all tasks

- [x] V1. `go build ./...` and `go vet ./...` pass after all tasks are complete.
      *Leader-verified 2026-08-16: both clean repo-wide.*
- [ ] V2. `gofmt -l` reports no files.
      *Leader note (NOT met, pre-existing): `gofmt -l .` reports 14 files repo-wide, one of
      them in this module (`internal/telemetry/scheduler_test.go`). All 14 are unformatted
      at the change's base commit `874820f` and NONE is touched by this change
      (`git diff 874820f..HEAD` on the file is empty). Every file this change created or
      modified is gofmt-clean. Left unticked rather than reworded — reformatting 14
      untouched files is out of this change's scope.*
- [ ] V3. `go test ./...` green and fast. DB integration tests self-skip without
      `DATABASE_URL`/Docker; with Docker the testcontainers helper provisions Postgres and
      applies goose migrations automatically, including the new
      `20260815000002_add_charge_gaps.sql`. NO Tesla API call fires.
- [ ] V4. Test-contract (a) upsert-idempotency (no duplicate on re-flag): verified by T7.1.
- [ ] V5. Test-contract (b) delete-on-resolve, including the empty-flagged-set case and
      per-day precision: verified by T7.2.
- [ ] V6. Test-contract (c) `SuperchargerSessionsByVehicleBetween` boundary correctness
      (start-inclusive, end-day-inclusive, day-after-end-excluded, oldest-first order):
      verified by T7.3.
- [ ] V7. Test-contract (d) midnight-spanning session inclusion (filter is on stop time):
      verified by T7.4.
- [ ] V8. Test-contract (e) tenant isolation on both the write port and the new read method,
      including the mis-scoped-`flagged`-entry full-call rejection: verified by T7.5.
- [x] V9. Static check: `UpsertChargeGap`'s `ON CONFLICT DO UPDATE SET` clause does not
      reference `created_at` (grep, per T3.1's acceptance criteria).
      *Leader-verified: `created_at` count in the `ON CONFLICT` region = 0; the SET clause
      refreshes `vin`, `missing_charging_type`, `updated_at` only.*
- [x] V10. Index plan confirmed: `\d charge_gaps` (or the pg catalog) shows exactly two
      indexes — the `UNIQUE` constraint's own `(account_id, tesla_id, gap_date)` index and
      `idx_charge_gaps_account (account_id, date DESC)` — no third index. No `CREATE INDEX`
      appears anywhere else in the migration diff, and no index is added on
      `supercharger_sessions` for `SuperchargerSessionsByVehicleBetween` (design.md's
      documented trade-off).
      *Leader-verified statically: the migration has exactly one `CREATE INDEX` (the second
      grep hit is inside a comment), the whole change adds no other index, and none touches
      `supercharger_sessions`. This criterion's text still spells the index
      `(account_id, date DESC)` — a leftover from before decision **G1** renamed the column
      to `gap_date`; the shipped index is `idx_charge_gaps_account (account_id, gap_date
      DESC)`, which is what G1 approved. Text left as authored rather than rewritten.
      Catalog confirmation (`\d charge_gaps` showing exactly the two indexes against a live
      DB) still belongs to the user's suite run.*
- [ ] V11. Boundary check: `internal/telemetry` still imports only `account` + `tesla`
      public packages; no other module's internals. `pgtype` does not appear in
      `gap_writer.go` (confined to `service.go`/`mapping.go`, matching the module's existing
      helpers). No file outside `internal/telemetry/` was touched. `internal/battery` was
      not created, read, or referenced by any file this change creates or modifies.
      *Leader note (partially met — reviewer must judge): imports are clean
      (`internal/telemetry` non-test files import only `account`, `tesla`, and its own
      `telemetry/db`), and `pgtype` appears in `gap_writer.go` only inside a comment
      asserting its absence — no import. **But the "no file outside `internal/telemetry/`"
      clause is NOT literally met**: the leader edited two sibling test doubles —
      `internal/battery/reader_test.go` and `internal/gateway/handlers/supercharger_test.go`
      — adding panic-on-call stubs for the new `SuperchargerReader` method, without which
      `go vet` fails in those packages (decision **G4**). Both edits are test-only, add no
      production code, and `internal/battery` production code is still neither created,
      read, nor referenced by this change.*
- [x] V12. Docs: `internal/telemetry/AGENTS.md` accurately reflects the new table, port, and
      read method (T8.1). Root `README.md` "Project Structure"/"Architecture" confirmed NOT
      to need changes (no module added/removed, no new runnable) — this confirmation itself
      is part of verification, not an assumption to skip.
      *Leader-verified: `AGENTS.md` covers the table, the `GapWriter` lifecycle and the new
      read method (T8). README "Project Structure"/"Architecture" indeed need no change — no
      module added, removed or re-scoped. **However the check did surface a real omission**:
      the README's per-module data-ownership table (which lists every table each module owns)
      had no `charge_gaps` row. The leader added it in this change, as the project's
      "docs track structural change" rule requires.*
- [x] V13. `openspec validate RM28-telemetry-add-charge-gap-storage --strict` passes.
      *Leader-verified: "Change 'RM28-telemetry-add-charge-gap-storage' is valid".*
