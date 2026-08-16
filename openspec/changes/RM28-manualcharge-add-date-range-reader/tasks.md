> **Additive change, no DB-touching artifact beyond a reused index — read design.md
> before starting.** This adds exactly one new `Reader` method
> (`ListEntriesByVehicleBetween`), one new query, and one new sqlc-generated params
> type. **No migration, no new table, no new column, no new index** — design.md's
> decision D3 shows the existing `idx_manual_charge_entries_vehicle_time (account_id,
> tesla_id, charged_on DESC)` already covers the query as a single index range scan.
> `ListEntriesByVehicle`, `ListEntriesByAccount`, and the entire `Writer` port are
> untouched. `internal/battery` (this method's only intended caller, tier 3 of RM28) is
> NOT created, read, or touched here.
>
> **Dependencies:**
>
> - T1 (`manualcharge.go`: `Reader` interface addition) has no dependencies.
> - T2 (`db/query.sql`: new query + `sqlc generate`) has no dependencies — pure SQL, no
>   dependency on T1's Go interface. **Parallel-safe with T1** (disjoint files).
> - T3 (`service.go`: `store` interface, `dbStore` delegation, `readerService`
>   implementation) depends on T1 (needs the `Reader` signature to implement against)
>   and T2 (needs `manualchargedb.ListEntriesByVehicleBetweenParams` to exist after
>   `sqlc generate`).
> - T4 (DB-integration tests, `db_integration_test.go`) depends on T3 (calls the real
>   `Reader` through `manualcharge.NewReader`).
> - T5 (`AGENTS.md` — Public Interface code block) depends on T1 (copies the final
>   interface signature).
> - Verification (V) depends on all tasks.
>
> **Leader-integrated step:** run `sqlc generate` (or `make sqlc`) as part of T2, after
> adding the query. The existing `sql:` entry in `sqlc.yaml` already covers the
> `manualcharge` module; no structural `sqlc.yaml` change is needed.

---

## T1. `manualcharge.go` — `Reader` interface addition — no dependencies

- [x] T1.1 Add `ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID,
      teslaID int64, from, to time.Time) ([]Entry, error)` to the `Reader` interface,
      with the doc comment from design.md's "Go-Level Surface" section (states: filters
      on `charged_on` inclusive of both bounds — D5/roadmap D9/D12; ordered `charged_on
      DESC` — D2; non-nil empty slice on no match — D4; no `limit` parameter — D1/roadmap
      D9).
      Acceptance: `go build ./...` fails (interface method has no implementation yet) —
      this is expected until T3 lands; `gofmt -l internal/manualcharge/manualcharge.go`
      reports no formatting issues.

## T2. `db/query.sql` — new query + sqlc regeneration — no dependencies (parallel-safe with T1)

- [x] T2.1 Add the `ListEntriesByVehicleBetween` query to
      `internal/manualcharge/db/query.sql`, exactly as specified in design.md's
      "`db/query.sql` addition" (name `ListEntriesByVehicleBetween`, `:many`, filters
      `account_id = @account_id AND tesla_id = @tesla_id AND charged_on BETWEEN
      @from_date AND @to_date`, `ORDER BY charged_on DESC`, no `LIMIT`). Include the
      header comment explaining the index-scan justification (D3) and the no-`LIMIT`
      rationale (D1).
      Acceptance: query added verbatim per design.md; no existing query in the file is
      modified.
- [x] T2.2 Run `sqlc generate` (or `make sqlc`) to regenerate the `manualchargedb`
      package.
      Acceptance: `internal/manualcharge/db/` gains
      `manualchargedb.ListEntriesByVehicleBetweenParams` (fields `AccountID uuid.UUID`,
      `TeslaID int64`, `FromDate pgtype.Date`, `ToDate pgtype.Date` — design D6) and a
      `(*Queries) ListEntriesByVehicleBetween(ctx, params) ([]ManualChargeEntry, error)`
      method; no existing generated method or type changes shape; `go build ./...`
      still fails at this point only because `service.go`/`manualcharge.go` don't yet
      reference the new generated symbols in a way that satisfies the interface — that's
      expected until T3.

## T3. `service.go` — store interface, `dbStore` delegation, `readerService` implementation — depends on T1, T2

- [x] T3.1 Add `listEntriesByVehicleBetween(ctx context.Context, params
      manualchargedb.ListEntriesByVehicleBetweenParams) ([]manualchargedb.ManualChargeEntry,
      error)` to the unexported `store` interface.
      Acceptance: interface compiles once T3.2 provides an implementation.
- [x] T3.2 Add the `dbStore` delegation method (one-line passthrough to
      `d.q.ListEntriesByVehicleBetween`, mirroring the five existing `dbStore` methods
      exactly).
      Acceptance: `dbStore` satisfies the updated `store` interface.
- [x] T3.3 Add `readerService.ListEntriesByVehicleBetween`, exactly as specified in
      design.md's "Go-Level Surface" section: builds `ListEntriesByVehicleBetweenParams`
      via `dateFromTime(from)` / `dateFromTime(to)` (no new conversion helper), calls
      `r.store.listEntriesByVehicleBetween`, wraps any error with `"manualcharge: list
      entries by vehicle between: %w"`, maps every row through the existing
      `rowToEntry`, and returns `make([]Entry, 0, len(rows))` (never nil) on the
      zero-rows path.
      Acceptance: `var _ Reader = (*readerService)(nil)` (already present in the file)
      compiles; `go build ./...` succeeds; `go vet ./...` reports no issues.

## T4. DB-integration tests (`db_integration_test.go`) — depends on T3

- [ ] T4.1 `TestListByVehicleBetween_InclusiveBounds` — design.md Test Contract (a):
      entries at `charged_on = from`, a mid-window date, and `charged_on = to`; call
      `ListEntriesByVehicleBetween(ctx, A, V, from, to)`; assert all 3 are returned,
      including the entries dated exactly `from` and exactly `to`.
- [ ] T4.2 `TestListByVehicleBetween_ExcludesOutsideBounds` — design.md Test Contract
      (b): same setup as T4.1 plus entries at `from - 1 day` and `to + 1 day`; assert the
      result is still exactly the 3 in-window entries — neither out-of-bound entry
      appears.
- [ ] T4.3 `TestListByVehicleBetween_NewestFirst` — design.md Test Contract (c): the
      3-entry window from T4.1, inserted out of date order; assert
      `result[0].ChargedOn >= result[1].ChargedOn >= result[2].ChargedOn` (same
      assertion style as `TestListByVehicle_NewestFirst`).
- [ ] T4.4 `TestListByVehicleBetween_EmptyNonNil` — design.md Test Contract (d): a
      vehicle/account with no entries in the queried window; assert `err == nil`,
      `result != nil`, `len(result) == 0`.
- [ ] T4.5 `TestListByVehicleBetween_AccountIsolation` — design.md Test Contract (e):
      two accounts `A`/`B`, same `teslaID = V`, each with an entry at the same
      `charged_on` inside the queried window; call scoped to `A`; assert exactly 1
      result and it belongs to `A`.
- [ ] T4.6 `TestListByVehicleBetween_VehicleIsolation` — design.md Test Contract (f): one
      account `A`, two vehicles `V1`/`V2`, each with an entry at the same `charged_on`
      inside the queried window; call scoped to `V1`; assert exactly 1 result and it has
      `TeslaID == V1`.
      Acceptance (all of T4): all six tests compile (`go vet ./...` passes, since vet
      compiles `_test.go` files); tests are written against `DATABASE_URL`-gated /
      testcontainers-provisioned `TestMain` per the module's existing pattern — no new
      test infrastructure needed; `pgtype` never appears in any assertion (module
      convention). **Per the Test-Execution-Policy, these tests are written but NOT run
      by the worker — status is `awaiting-user-verification`, not `done`, until the
      owner runs `go test ./...` (or `make test`) and reports the result.**

## T5. `AGENTS.md` — Public Interface documentation — depends on T1

- [x] T5.1 Update the `## Public Interface` code block in
      `internal/manualcharge/AGENTS.md` to include the new
      `ListEntriesByVehicleBetween` method signature in the `Reader` interface,
      matching T1's final signature exactly (docs-track-change rule, `CLAUDE.md`).
      Acceptance: the code block in `AGENTS.md` and the real `Reader` interface in
      `manualcharge.go` never drift — a diff of the two shows identical method
      signatures.

## T6. Cross-module fake stubs (LEADER-OWNED, appended after wave 1) — depends on T1

Adding a method to the shared `manualcharge.Reader` port breaks every hand-rolled test
fake of it. Two exist outside this module's sandbox, so the leader owns the fix
(`ai/architecture.md`: a worker never edits outside its module; the leader owns
cross-module integration). Discovered by the wave-1 worker and verified by the leader
via `go vet ./...`.

- [x] T6.1 `internal/battery/reader_test.go` — add `ListEntriesByVehicleBetween` to
      `fakeManualReader`, panicking like the file's existing `ListEntriesByAccount` stub
      (`RecentEfficiency` must not call it).
- [x] T6.2 `internal/gateway/handlers/charges_test.go` — add
      `ListEntriesByVehicleBetween` to `fakeChargeReader`, returning `f.entries, f.err`
      like the file's two existing stubs (no charge handler calls it).
      Acceptance (T6): `go vet ./...` passes repo-wide — it compiles `_test.go` files, so
      it is the signal that proves both fakes satisfy the extended port again.

## Verification — depends on all tasks

- [ ] V.1 `go build ./...` succeeds.
- [ ] V.2 `go vet ./...` succeeds (compiles all `_test.go` files, including T4's new
      tests, catching any signature drift).
- [ ] V.3 `gofmt -l` reports no files needing formatting across all files touched by
      this change.
- [ ] V.4 `openspec validate RM28-manualcharge-add-date-range-reader --strict` passes.
- [ ] V.5 Hand off to the owner: exact command to run and report on —
      `go test ./internal/manualcharge/...` (or `make test` / `make test-with-db` for
      the full suite). Not run by the worker per the Test-Execution-Policy.
