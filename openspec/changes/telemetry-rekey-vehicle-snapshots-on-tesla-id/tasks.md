# Tasks — telemetry-rekey-vehicle-snapshots-on-tesla-id

All work in this file is inside `internal/telemetry`, except T8 (explicitly
leader-owned, `internal/analytics`). Sequential dependency chain for the
schema half (migration → queries/sqlc → ports → implementation → test
fixups), because Go cannot compile against `sqlc`-generated types until they
are regenerated, and the ports cannot be re-implemented until the generated
types exist. Poll election (T1) is pure Go with no schema dependency, but it
and the implementation task (T5) both edit `internal/telemetry/service.go` —
sequence them (T1 before T5), do not run as two parallel agents on the same
file. Docs (T7) is independent of T6 once T5 is settled and may run in
parallel with it.

## T1 — Poll election

Depends on: nothing.

- [x] 1.1 In `internal/telemetry/service.go`, add `electPollingVehicles`
      exactly as specified in `design.md` Part 1: sorts its input by
      `AccountID` (raw byte comparison, not string formatting), then a single
      pass that prefers `OWNER`, keeps the first-seen (lowest `AccountID`)
      candidate on any tie, and never drops a `tesla_id`. Add the small
      `isOwner` helper. Import `bytes` and `sort`.
- [x] 1.2 Change `CollectAll` to call `elected := electPollingVehicles(vehicles)`
      immediately after `AllRegisteredVehicles`, and pass `elected` (not
      `vehicles`) into `groupByAccount`. Do not change `groupByAccount` or
      `collectAccount` themselves.
- [x] 1.3 Update `CollectAll`'s doc comment (and `groupByAccount`'s, if it
      still says "buckets the flat cross-account vehicle list") to say the
      list it buckets is the elected subset, not every registered vehicle.
- [x] 1.4 `go build ./internal/telemetry/...` and `go vet ./internal/telemetry/...`
      — expect these to still pass; T1 changes no type signature any test
      depends on.

## T2 — Migration

Depends on: nothing.

- [x] 2.1 Write `internal/telemetry/db/migrations/20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`
      (renamed from the `...0001` filename `design.md` specifies — see note
      below) with the SQL content exactly as specified in `design.md`'s
      "Migration SQL": the duplicate-collapse `DELETE` (keyed on
      `tesla_id, captured_date`, latest `captured_at` wins),
      `DROP CONSTRAINT vehicle_snapshots_account_tesla_date_unique`,
      `DROP INDEX telemetry.idx_vehicle_snapshots_vehicle_time`,
      `DROP COLUMN account_id`, `ADD CONSTRAINT vehicle_snapshots_tesla_date_unique
      UNIQUE (tesla_id, captured_date)`, then `ALTER TABLE telemetry.poll_attempts
      RENAME COLUMN account_id TO polled_by_account_id`, plus the
      `-- +goose Down` half.
- [x] 2.2 Do not touch any existing migration file — historic migrations are
      never edited (`ai/go-conventions.md`). Confirmed: no existing file was
      modified, only the new file was created (then renamed to `...0002`).
- [x] 2.3 Run `make migration-guard`. `design.md`'s literal filename
      (`20260911000001_...`) collided with `internal/analytics`'s own
      same-day pilot migration
      (`internal/analytics/db/migrations/20260911000001_rekey_charge_gaps_on_tesla_id.sql`)
      — the guard failed with "duplicate migration version number(s) across
      modules." Fixed per the guard's own instruction: renumbered the
      telemetry file to `...0002` (SQL content unchanged). Guard now passes
      clean: "migration-guard: no duplicate version numbers across 4 module
      dirs" (plus a pre-existing, unrelated backlog warning about
      `20260720000001`).

## T3 — Queries + sqlc

Depends on: T2 (schema must exist before `sqlc generate` can validate the
queries against it).

- [ ] 3.1 In `internal/telemetry/db/query.sql`, remove `account_id` from
      `InsertVehicleSnapshot`'s column list, `VALUES` list, and change its
      `ON CONFLICT` target to `(tesla_id, captured_date)`.
- [ ] 3.2 Remove `account_id` from `SnapshotsByVehicleSince`'s `SELECT`
      column list and `WHERE` clause.
- [ ] 3.3 Remove `account_id` from `SnapshotsByVehicleBetween`'s `SELECT`
      column list and `WHERE` clause.
- [ ] 3.4 Remove `account_id` from `SnapshotsByVehicleUpdatedSince`'s
      `SELECT` column list and `WHERE` clause.
- [ ] 3.5 Remove `account_id` from `SnapshotPrecedingDay`'s `SELECT` column
      list and `WHERE` clause.
- [ ] 3.6 Rename `InsertPollAttempt`'s bound column/parameter from
      `account_id`/`@account_id` to `polled_by_account_id`/`@polled_by_account_id`.
      Do not drop it — this query keeps its account argument, only renamed.
- [ ] 3.7 Rename `LatestSnapshotsByAccount` to `LatestSnapshotsByVehicles`:
      replace `WHERE account_id = @account_id` with
      `WHERE tesla_id = ANY(@tesla_ids::bigint[])`. Keep the `DISTINCT ON
      (tesla_id) ... ORDER BY tesla_id, captured_at DESC` shape unchanged.
- [ ] 3.8 Rewrite each of the six edited queries' doc comments so none of
      them cite `design D1`/`D2`/`D3`/`D4`/`D5` from the now-archived,
      frozen `telemetry-dedupe-daily-snapshots`/`RM8`/`RM29` design docs, and
      none of them name the old constraint/index this migration retired —
      replace each citation with the reason itself, following this change's
      own `design.md` D-INDEX/D-MIGRATION sections for the substance. Do not
      cite this change's own name or any decision ID in the new comment text
      (`ai/go-conventions.md`'s code-comment rule).
- [ ] 3.9 Run `make sqlc` (or `sqlc generate`). Confirm
      `InsertVehicleSnapshotParams`, `SnapshotsByVehicleSinceParams`,
      `SnapshotsByVehicleBetweenParams`, `SnapshotsByVehicleUpdatedSinceParams`,
      `SnapshotPrecedingDayParams` no longer have an `AccountID` field;
      confirm `InsertPollAttemptParams` has `PolledByAccountID` instead of
      `AccountID`; confirm a new `LatestSnapshotsByVehiclesParams` (or
      equivalent single-parameter signature — record whatever sqlc actually
      generates for the `bigint[]` binding) exists in place of
      `LatestSnapshotsByAccountParams`.

## T4 — Ports and domain types

Depends on: T3 (needs the regenerated `telemetrydb` types to know the exact
generated field names to reference).

- [ ] 4.1 In `internal/telemetry/telemetry.go`, remove `AccountID
      uuid.UUID` from `Snapshot` (design.md D-SNAPSHOT-FIELD) and update its
      doc comment.
- [ ] 4.2 In the same file, rename `Attempt.AccountID` to
      `Attempt.PolledByAccountID` (design.md D-ATTEMPT-FIELD) and update its
      doc comment to describe it as "the account whose credentials performed
      this attempt," dropping any wording implying it is the vehicle's
      single owning account.
- [ ] 4.3 Change `Reader`'s five methods to the signatures in `design.md`:
      `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`,
      `SnapshotsByVehicleUpdatedSince`, `SnapshotPrecedingDay` each drop
      `accountID uuid.UUID`; `LatestSnapshotsByAccount` renames to
      `LatestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64)
      ([]Snapshot, error)`. Update each method's doc comment: remove the
      "defense-in-depth tenant isolation" paragraph naming `account_id`;
      keep everything else (window semantics, ordering, empty-result
      contract, index reuse) updated to the new predicate.
- [ ] 4.4 Mirror the same five signature/name changes on the unexported
      `store` interface in `service.go` (`latestSnapshotsByAccount` →
      `latestSnapshotsByVehicles(ctx, teslaIDs []int64)`, and the other four
      drop `accountID`).
- [ ] 4.5 `go build ./internal/telemetry/...` — expect this to fail until T5
      lands (the concrete implementations and every test file still
      reference the old shapes); expected at this point, not a regression.

## T5 — Implementation

Depends on: T4, T1 (T1 already edited `service.go`; land this after it to
avoid a merge conflict on the same file, even though the two changes are
logically independent).

- [ ] 5.1 In `service.go`, drop `accountID uuid.UUID` from `snapshotFrom`'s
      parameter list; update its call site in `attemptVehicle` to drop the
      `v.AccountID` argument.
- [ ] 5.2 In `mapping.go`'s `rowToSnapshot`, drop the `AccountID: r.AccountID,`
      line.
- [ ] 5.3 In `service.go`'s `dbStore.insertSnapshot`, drop
      `AccountID: s.AccountID,` from the `InsertVehicleSnapshotParams`
      literal.
- [ ] 5.4 In `service.go`'s `record()`, build
      `Attempt{PolledByAccountID: accountID, ...}` instead of
      `Attempt{AccountID: accountID, ...}`.
- [ ] 5.5 In `service.go`'s `dbStore.insertPollAttempt`, map
      `PolledByAccountID: a.PolledByAccountID,` using whatever field name
      `sqlc generate` actually produced for the renamed column (confirmed in
      T3.9) — do not guess it before that step ran.
- [ ] 5.6 Update `dbStore`'s four `snapshotsByVehicle*`/`snapshotPrecedingDay`
      methods and `latestSnapshotsByVehicles` to drop/replace `accountID`
      in their own signatures and in the `telemetrydb.*Params` literals they
      build, using T3.9's confirmed generated field/parameter names.
- [ ] 5.7 Update `reader.go`'s five pass-through methods to the new
      signatures (thin delegation to the renamed/resigned `store` methods —
      no logic change).
- [ ] 5.8 In `query_log.go`: update `loggingStore`'s and `loggingReader`'s
      method signatures to match. `insertSnapshot`'s log line drops
      `account=%s`. `insertPollAttempt`'s log line reads
      `a.PolledByAccountID` (keep the label `account=%s` or change it to
      `polled_by_account=%s` — either is fine). `LatestSnapshotsByVehicles`'s
      log line reports the requested `tesla_ids` instead of `account=%s`.
- [ ] 5.9 `go build ./internal/telemetry/...` and `go vet
      ./internal/telemetry/...` — expect these to still fail until T6 also
      lands (the package's own test files still reference the old shapes);
      expected at this point, not a regression to fix here.

## T6 — Telemetry's own test fixups (not new tests — see design.md D-TESTFIX)

Depends on: T5.

- [ ] 6.1 `db_read_integration_test.go`: drop `AccountID:` from every
      `Snapshot{...}` literal; drop the `accountID`/`.AccountID` argument
      from every `SnapshotsByVehicleSince`/`Between`/`UpdatedSince`/
      `SnapshotPrecedingDay`/`LatestSnapshotsByAccount` call, renaming the
      last to `LatestSnapshotsByVehicles` with a `[]int64` argument; delete
      or rewrite any assertion reading `.AccountID` off a returned
      `Snapshot`.
- [ ] 6.2 `db_integration_test.go`: drop `AccountID:` from every
      `Snapshot{...}` literal; update `Attempt{AccountID: ...}` to
      `Attempt{PolledByAccountID: ...}`.
- [ ] 6.3 `db_preceding_snapshot_integration_test.go`: drop `AccountID:`
      from every `Snapshot{...}` literal; drop the `accountID` argument from
      every `SnapshotPrecedingDay`/`reader.SnapshotPrecedingDay` call.
- [ ] 6.4 `db_sourcea_integration_test.go`: drop `AccountID:` from every
      `Snapshot{...}` literal.
- [ ] 6.5 `db_tpms_integration_test.go`: drop `AccountID:` from every
      `Snapshot{...}` literal.
- [ ] 6.6 `query_log_test.go`: drop `AccountID:` from every `Snapshot{...}`
      literal; update `Attempt{AccountID: ...}` to
      `Attempt{PolledByAccountID: ...}`; update `fakeQueryLogStore` to
      implement all five renamed/resigned `store` methods; update any
      `LatestSnapshotsByAccount` call to `LatestSnapshotsByVehicles`.
- [ ] 6.7 `reader_test.go`: drop `AccountID:` from every `Snapshot{...}`
      literal; update `fakeReadStore`, `fakeHistoryStore`, `fakeBetweenStore`
      to each implement all five renamed/resigned `store` methods; update
      every call passing an `accountID` argument to the new signatures, and
      every `LatestSnapshotsByAccount` call to `LatestSnapshotsByVehicles`.
- [ ] 6.8 `service_test.go`: update `fakeStore` to implement all five
      renamed/resigned `store` methods; delete the `got.AccountID != acctID`
      assertion (around the existing line ~404) — the field no longer
      exists, and the same check's `got.TeslaID` comparison already covers
      vehicle identity.
- [ ] 6.9 `snapshot_from_test.go`: drop the leading `uuid.New()` argument
      from all four `snapshotFrom(...)` calls.
- [ ] 6.10 `go build ./internal/telemetry/...` and `go vet
      ./internal/telemetry/...` — expect clean now.

## T7 — Docs sweep

Depends on: T5 (needs the final schema/signature shape to describe
accurately). Independent of T6; may run in parallel with it.

- [ ] 7.1 `internal/telemetry/AGENTS.md`: update the "Data ownership" table's
      `vehicle_snapshots` row grain from "one row per (account, vehicle,
      `captured_date`)" to "one row per (vehicle, `captured_date`)". Check
      the "Rules that bind across all four" bullet naming
      `account_id`/`tesla_id` and update it to describe `vehicle_snapshots`
      as keyed on `tesla_id` alone — do not change anything said about
      `supercharger_history`, which still carries `account_id`. Add a short
      note (under "Responsibility" or its own subsection) that one account
      is elected to poll each vehicle before `CollectAll` groups by account
      (prefer OWNER, never skip), pointing at `electPollingVehicles`'s own
      doc comment rather than restating the full rule.
- [ ] 7.2 This change's `specs/telemetry/spec.md` delta (already written as
      part of this change's artifacts) is synced into
      `openspec/specs/telemetry/spec.md` at archive time via the normal
      OpenSpec sync step — no separate task here, but confirm at archive
      time that the sync added the new "Poll Account Election" requirement
      and rewrote the five modified requirements, and did not touch
      "Module-Scoped Database Schema" or any Supercharger-session
      requirement (design.md explains why those stay as written).
- [ ] 7.3 `kkpa/context/architecture/telemetry-ingest-only.md`: update the
      "Same-day captures dedupe" bullet in "Conventions & gotchas" from
      `` `UNIQUE (account_id, tesla_id, captured_date)` `` to
      `` `UNIQUE (tesla_id, captured_date)` ``, keeping the rest of the
      sentence ("latest wins; repeated same-day collection is not duplicate
      data"). This is the actual location the ticket's "Docs" section meant
      by "spec.md's Same-day captures dedupe requirement" — see design.md's
      "Ticket-vs-reality correction" note. Grep the rest of this KB file for
      any other stale `account_id`/`LatestSnapshotsByAccount` mention this
      change invalidates (the consumer-map rows naming call methods) and fix
      what is actually found — do not assume the sweep is limited to the one
      bullet already named.

## T8 — Cross-module caller (leader-owned, `internal/analytics`)

Depends on: T4 (needs the final `Reader` signatures). **Not this worker's
sandbox — `internal/telemetry` workers must not edit these files.**

- [ ] 8.1 `internal/analytics/reader.go:136`: drop the `accountID` argument
      from the `r.telemetry.SnapshotsByVehicleSince(ctx, accountID, teslaID,
      since)` call, keeping `teslaID` and `since`.
- [ ] 8.2 `internal/analytics/recalculate.go:104`: drop the `accountID`
      argument from the `r.telemetry.SnapshotsByVehicleBetween(...)` call.
- [ ] 8.3 `internal/analytics/recalculate.go:123`: drop the `accountID`
      argument from the `r.telemetry.SnapshotPrecedingDay(...)` call.
- [ ] 8.4 `internal/analytics/recalculate.go:269`: drop the `accountID`
      argument from the `r.telemetry.SnapshotsByVehicleUpdatedSince(...)`
      call.
- [ ] 8.5 `internal/analytics`'s own `Recalculate`/`Reconcile` functions and
      their `accountID` parameter are UNCHANGED — they still need it to
      reach the charge sources (design.md D-SCOPE / proposal.md Non-goals).
      Do not remove it.
- [ ] 8.6 `internal/analytics/reader_test.go`: fix the mechanical fallout of
      8.1 in this file's own fixtures/assertions.
- [ ] 8.7 `internal/analytics/reader_test.go`'s `fakeTelemetryReader`
      (found by this design, not in the original dispatch — design.md
      "Correction to the dispatch"): add a
      `LatestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64)
      ([]telemetry.Snapshot, error)` method (mirroring the existing
      `LatestSnapshotsByAccount` stub's `panic(...)` body — it is not
      actually called by anything in this module, same as today) so the
      fake keeps satisfying `telemetry.Reader`.
- [ ] 8.8 `internal/analytics/recalculate_test.go`: fix the mechanical
      fallout of 8.2–8.4 in this file's own fixtures/assertions.
- [ ] 8.9 `internal/analytics/consumption_test.go`: fix the mechanical
      fallout of 8.1–8.4 in this file's own fixtures/assertions, if any
      (per the dispatch's verified facts — confirm scope on inspection).
- [ ] 8.10 `go build ./internal/analytics/...` and `go vet
      ./internal/analytics/...` — expect clean.

## T9 — Final verification

Depends on: T1–T8.

- [ ] 9.1 `go build ./...`
- [ ] 9.2 `go vet ./...`
- [ ] 9.3 `make migration-guard`
- [ ] 9.4 `make boundary-guard`
- [ ] 9.5 Grep the whole repo for `account_id` scoped to `vehicle_snapshots` /
      `Snapshot` / `LatestSnapshotsByAccount` and confirm zero remaining
      hits outside the immutable `openspec/changes/archive/` tree and the
      untouched historic migration files. Separately grep for
      `poll_attempts.account_id` / `Attempt{AccountID` and confirm zero
      hits outside the same exclusions (every live reference should now say
      `polled_by_account_id` / `PolledByAccountID`).
- [ ] 9.6 Confirm `internal/gateway/` still has zero real
      `internal/telemetry` imports (`make boundary-guard` already covers
      this, but the ticket's own framing — "still no gateway consumer" —
      makes it worth stating as its own check here).
- [ ] 9.7 Suite commands for the owner to run (Claude does not run these):
      `go test ./internal/telemetry/... ./internal/analytics/...` (or
      `make test` / `make test-with-db` for the full suite).
