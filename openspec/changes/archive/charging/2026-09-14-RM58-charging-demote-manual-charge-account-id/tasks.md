# Tasks — RM58-charging-demote-manual-charge-account-id

All work is inside `internal/charging`, except **T6**, which is leader-owned and
touches `internal/analytics` and `internal/gateway`.

**Unit tests: EXCLUDED.** No new test file and no new test function. Existing
tests whose signatures or expectations change are updated, and that update is
T5 (inside the module) and T6 (outside it).

## Dependency graph

```
T0 (offline grep, no deps) ───────────────────────────────┐
                                                          │
T1 (migration) ──► T2 (queries + sqlc) ──► T3 (ports) ──► T4 (implementation)
                                            │                  │
                                            │                  ├──► T5 (tests inside charging)
                                            │                  └──► T6 (leader-owned: analytics + gateway)
                                            └──► T7 (docs) — parallel with T4–T6
                                                          T8 (final verification) — depends on all
```

Sequencing rules that are not optional:

- **T1 → T2 → T3 → T4 is a hard chain.** Go cannot compile against
  sqlc-generated types until they are regenerated, and the ports cannot be
  re-implemented until those types exist.
- **T0 is a grep, with no schema dependency**, and may start immediately.
- **T5 and T6 may run in parallel** — disjoint files and disjoint modules.
- **T7 (docs) touches no Go file** and may run in parallel with T4–T6.
- **T6 must land in the same wave as T4.** `go build ./...` is red outside
  `internal/charging` until it does (`design.md` D5).

---

## T0 — Offline check

Depends on: nothing.

- [x] 0.1 Grep the module's five offline test files (`charging_test.go`,
      `entry_status_test.go`, `monthly_capacity_estimator_test.go`,
      `price_source_test.go`, `session_verifier_derivation_test.go`) for
      `AccountID`, `accountID` and `account_id`. Expect **zero** hits. If a hit
      appears, that file is not offline-only and belongs to T5 instead.
- [x] 0.2 Record in the change's notes that this change adds **no new test**, and
      why: the ticket excludes unit tests, and no pure function changes
      behaviour — the whole change is schema, queries and signatures.

## T1 — Migration

Depends on: nothing.

- [x] 1.0 **No owner pre-check query is needed, and this is a checked fact, not
      an omission.** The migration deletes no row, rewrites no value, and adds no
      constraint that an existing row could violate. `created_by_account_id`
      stays `UUID NOT NULL` with the same values it holds today. Compare RM57,
      whose de-duplication needed a count query first.
- [x] 1.1 Write
      `internal/charging/db/migrations/20260914000001_demote_manual_charge_account_id.sql`
      exactly as `design.md` §Schema gives it — Up and Down, comments included.
      Do not renumber: `20260914000001` is free across all four module
      directories (`design.md` §Makefile).
- [x] 1.2 Do NOT edit `COMMENT ON TABLE charging.manual_charge_entries`, even
      though it still says "No cross-module FK on account_id" (`design.md` D7).
- [x] 1.3 Do NOT touch the primary key, any CHECK constraint, or the generated
      column `inferred_capacity_kwh_calc`.
- [x] 1.4 Do NOT touch `charging.supercharger_sessions`,
      `charging.mirror_watermarks` or `charging.monthly_effective_capacity` in
      this file.
- [x] 1.5 `make migration-guard` — expect clean. It prints its standing warning
      about the pre-existing `20260720000001` collision in this same folder;
      that is backlog item 21 and is not this change's job.
- [x] 1.6 Do NOT write a test for the migration. This project does not test
      migrations; the owner verifies them against the database directly (T8.9).

## T2 — Queries and sqlc

Depends on: T1.

- [x] 2.1 `internal/charging/db/query.sql`, `CreateEntry`: `account_id` becomes
      `created_by_account_id` in the INSERT column list, and `@account_id`
      becomes `@created_by_account_id`. Add one sentence to the comment: the
      column records who typed the entry and is never read back as a filter.
- [x] 2.2 `UpdateEntry`: **keep the predicate**, renaming the column only —
      `WHERE id = @id AND created_by_account_id = @created_by_account_id`
      (`design.md` D4). Rewrite the comment to say three things: this is the only
      guard on the write path; it matches the account that TYPED the entry, which
      is narrower than the car the entry belongs to, so a co-owner of a shared car
      cannot yet edit it; and it is replaced by a `tesla_id` guard as soon as the
      port can carry a vehicle. The immutable-column list now reads `id`,
      `created_by_account_id`, `tesla_id`, `vin`, `created_at`. Do NOT name a
      tier, a change id, or a decision id in the comment — write the reason itself.
- [x] 2.3 `DeleteEntry`: **keep the predicate**, renaming the column only —
      `WHERE id = @id AND created_by_account_id = @created_by_account_id`. Same
      comment rewrite.
- [x] 2.4 `ListEntriesByVehicle`, `ListEntriesByVehicleBetween` and
      `ListEntriesByVehicleUpdatedSince`: drop the `account_id` predicate from
      each. Update each index comment to `(tesla_id, charged_on DESC)`, and say
      the read is car-wide — it returns entries typed by any account registered
      to that car. Keep `ListEntriesByVehicleBetween`'s "no LIMIT, the window
      bounds the result" paragraph and `ListEntriesByVehicleUpdatedSince`'s "no
      new index" paragraph, restating the latter's reason: this query orders by
      `charged_on`, so an `updated_at` index would force a sort.
- [x] 2.5 Rename `ListEntriesByAccount` to `ListEntriesByVehicles` and re-key it:
      `WHERE tesla_id = ANY(@tesla_ids::bigint[])`, keeping
      `ORDER BY charged_on DESC LIMIT @limit_count`. New comment: the caller
      supplies the vehicles it is entitled to see, the index prunes per vehicle,
      and a multi-vehicle array is expected to add a sort step. Mirror
      `internal/telemetry`'s `LatestSnapshotsByVehicles` for the parameter form.
- [x] 2.6 Do NOT touch `ListValidManualEntryCapacitiesForPeriod` — it never named
      `account_id` — nor any `supercharger_sessions`, `mirror_watermarks` or
      `monthly_effective_capacity` query.
- [x] 2.7 `make sqlc`. Confirm in the diff, against `design.md` §What `make sqlc`
      will produce: `chargingdb.ManualChargeEntry` keeps every field and renames
      only `AccountID` → `CreatedByAccountID`; **no new per-query `Row` struct
      appears** for this table; `CreateEntryParams.CreatedByAccountID` exists;
      **`UpdateEntryParams` and `DeleteEntryParams` both survive**, each with
      `AccountID` renamed to `CreatedByAccountID` and nothing else moved — the two
      write queries kept two parameters each, so neither struct collapses to a
      bare argument; `ListEntriesByVehicleParams`,
      `ListEntriesByVehicleBetweenParams` and
      `ListEntriesByVehicleUpdatedSinceParams` each lost `AccountID`; and
      `ListEntriesByVehiclesParams` carries `TeslaIds []int64` and
      `LimitCount int32`. If a `Row` struct did appear, or if either write params
      struct disappeared, stop and report — the query shape is not what the design
      says.

## T3 — Ports and domain type

Depends on: T2.

- [x] 3.1 `internal/charging/charging.go`: rename `Entry.AccountID` to
      `Entry.CreatedByAccountID`. Its comment states the rule: it records which
      account typed the entry, no read filters on it, an entry is visible to every
      account registered to its vehicle, and `Update`/`Delete` still match on it
      as the only guard they have until they can name the vehicle instead.
- [x] 3.2 `Reader`: drop `accountID` from `ListEntriesByVehicle`,
      `ListEntriesByVehicleBetween` and `ListEntriesByVehicleUpdatedSince`, and
      replace `ListEntriesByAccount` with
      `ListEntriesByVehicles(ctx context.Context, teslaIDs []int64, limit int) ([]Entry, error)`.
      Keep the vehicle id a plain `int64` — do not introduce `vehicleref.Ref`
      (`design.md` D3).
- [x] 3.3 Same file: delete every "within an account" and "excludes entries
      belonging to other accounts" sentence from the `Reader` doc comments, and
      state the car-wide rule instead.
- [x] 3.4 `ListEntriesByVehicles`' own doc comment: the caller supplies the set
      of vehicles it may see, and an empty or nil slice returns a non-nil empty
      result — an empty set must never be read as "no filter".
- [x] 3.5 `Writer.Delete` keeps its signature `Delete(ctx, accountID, id)`, and
      the argument keeps working: it is bound to `created_by_account_id` in the
      SQL. Keep the cross-account promise in the doc comment, and add that the
      guard matches the account that typed the entry — narrower than the car it
      belongs to. Same for `Update`, which scopes through
      `Entry.CreatedByAccountID`. Do NOT name a tier, a change id, or a design
      decision id in either comment — write the reason itself.
- [x] 3.6 Do NOT add an `internal/vehicleref` import to this module in this
      change, and do not change `Writer.Create` or `Writer.Update` signatures.

## T4 — Implementation

Depends on: T3.

- [x] 4.1 `internal/charging/service.go`, the `store` interface: `deleteEntry`
      keeps its shape, `deleteEntry(ctx, params chargingdb.DeleteEntryParams) error`
      — the params struct survives. `listEntriesByAccount` becomes
      `listEntriesByVehicles`, taking `chargingdb.ListEntriesByVehiclesParams`.
- [x] 4.2 Same file, `dbStore`: both methods follow the new generated shapes.
- [x] 4.3 `writerService`: `Create` binds `CreatedByAccountID: e.CreatedByAccountID`;
      `Update` renames the same field in its params literal and does NOT drop it;
      `Delete` keeps its signature and binds `CreatedByAccountID: accountID`.
- [x] 4.4 `readerService`: the four methods drop `accountID` from their signature
      and their params literal. `ListEntriesByVehicles` keeps the
      `limit <= 0 → defaultLimit` clamp the account-wide read had, and passes
      `TeslaIds: teslaIDs`.
- [x] 4.5 `rowToEntry`: map `CreatedByAccountID: r.CreatedByAccountID`. Update
      the mapping doc comment's field list.
- [x] 4.6 `go build ./internal/charging/...` and `go vet ./internal/charging/...`
      — expect clean. The rest of the repo is still red until T6.

## T5 — Test fixups inside charging

Depends on: T4. May run in parallel with T6 — disjoint modules.

- [x] 5.1 Grep every `_test.go` in `internal/charging` for
      `manual_charge_entries` **across line breaks**, not line by line: a table
      name and its column routinely sit on different lines, so a line-based grep
      finds some hits and misses others.
- [x] 5.2 `db_integration_test.go` helpers: `cleanupAccount`'s
      `DELETE ... WHERE account_id = $1` must name the new column, and `minEntry`
      must set `CreatedByAccountID`. Assert `RowsAffected()` on that fixture
      `DELETE` — a fixture write that matches no row does not error, and every
      assertion after it would then pass or fail for an unrelated reason.
      - **Leader note (deviation, accepted).** `RowsAffected()` is logged, not
        asserted. The helper has 84 callers and some clean an account that never
        wrote a row — `cleanupAccount(t, pool, ownerID, attackerID)` in the two
        cross-account tests, where the attacker is blocked by design. A non-zero
        assertion would fail those for the right reason. The checkbox's real aim,
        catching a `DELETE` that matches nothing after a rename, is met by checking
        the exec error: Postgres errors on an unknown column. Flagged to the reviewer.
- [x] 5.3 **Keep** `TestUpdate_CrossAccountIsNoOp` and
      `TestDelete_CrossAccountGuard`, and keep them passing (`design.md` §Test
      contract). The guard they protect survives this tier. Change only what the
      rename and the port change force: `tampered.AccountID` becomes
      `tampered.CreatedByAccountID`, and each read-back switches from
      `ListEntriesByAccount(ctx, ownerID, 10)` to
      `ListEntriesByVehicle(ctx, <the seed's teslaID>, 10)` — `333` for the update
      test, `555` for the delete test. Their assertions do not move. **Do not
      delete or weaken either one**: they are the proof that no commit on this
      branch has an unauthorized write path. Record in the change's notes that
      **tier 2 owns re-keying them onto `tesla_id`**, in the same change that
      moves the predicate.
- [x] 5.4 Rename and re-key the four account-wide list tests, keeping their
      assertions: `TestListByAccount_AccountIsolation` →
      `TestListByVehicles_VehicleIsolation`; `…_NewestFirst` →
      `TestListByVehicles_NewestFirst`; `…_Limit` → `TestListByVehicles_Limit`;
      `…_EmptyNonNil` → `TestListByVehicles_EmptyNonNil`. The expected values are
      in `design.md` §Test contract. `TestListByVehicles_EmptyNonNil` must also
      assert that an **empty** `[]int64{}` returns a non-nil, zero-length slice —
      never every row.
- [x] 5.5 `TestMultiTenantIsolation_NeverLeaks` becomes
      `TestSharedVehicle_ReadsAreCarWide` and asserts the opposite of what it
      asserts today: with one entry by `alice` and one by `bob` on the same
      `sharedTeslaID`, both `ListEntriesByVehicle` and `ListEntriesByVehicles`
      return **both** entries, each still carrying the `CreatedByAccountID` of
      whoever typed it.
- [x] 5.6 `TestListByVehicleBetween_AccountIsolation` becomes
      `TestListByVehicleBetween_ReturnsBothAccountsEntries`: both entries come
      back, in `charged_on DESC` order.
      `TestListByVehicleBetween_VehicleIsolation` is unchanged apart from the
      call signature.
- [x] 5.7 `db_entry_status_integration_test.go`,
      `db_promotion_price_source_integration_test.go` and
      `db_inferred_capacity_entries_integration_test.go`: mechanical only —
      re-key the `ListEntries*` calls, rename the `Entry` field. No expectation
      changes; each seeds exactly one account.
- [x] 5.8 `db_monthly_capacity_integration_test.go`: rename `account_id` in every
      raw `manual_charge_entries` seed. Leave the `supercharger_sessions` seeds
      alone. No capacity expectation changes.
- [x] 5.9 `go vet ./internal/charging/...` — expect clean. `go vet` compiles
      `_test.go` files, so this is what proves the test signatures are right.
- [x] 5.10 Do NOT add a new test file or a new test function. Unit tests are
      excluded by the ticket.

## T6 — Cross-module bridge (leader-owned)

Depends on: T3 (needs the final port signatures). **Not this module's sandbox —
`internal/charging` workers must not edit these files.** Must land in the same
wave as T4, or `go build ./...` stays red (`design.md` D5).

- [x] 6.1 `internal/analytics/reader.go:146`, `recalculate.go:163` and
      `recalculate.go:283`: drop the `accountID` argument from the three
      `ListEntries*` calls. `accountID` is still used elsewhere in each of those
      functions — check before deleting the parameter that carries it.
- [x] 6.2 `internal/analytics` tests: `fakeManualReader`
      (`reader_test.go`) re-signs its four methods and renames
      `ListEntriesByAccount` to `ListEntriesByVehicles`. Any recorded
      `gotAccountID` for these ports records the vehicle instead. Same in
      `recalculate_test.go`. Rename `account_id` in every raw
      `manual_charge_entries` seed in `db_integration_test.go`.
- [x] 6.3 `internal/gateway/handlers/external_charges.go:651` and `:845`: the two
      `ListEntriesByVehicleBetween` calls drop `uid`. Both already pass a vehicle
      resolved from the account's own vehicles, so nothing else moves.
- [x] 6.4 Same file, `fetchEntryVM`: replace `ListEntriesByAccount(ctx, uid, 0)`
      with `ListEntriesByVehicles(ctx, ids, 0)`, where `ids` are the `TeslaID`s
      of `h.acct.RegisteredVehicles(ctx, uid)` — which this function already
      calls. Move that call above the read and stop discarding its error: an
      error or an empty list must return `false`, not an unfiltered read.
- [x] 6.5 Same file, `fetchEntryTeslaIDAndChargedOn`: the same switch. It has no
      `RegisteredVehicles` call today, so add one, with the same error and
      empty-list handling.
- [x] 6.6 Same file, lines 445 and 1425: `entry.AccountID = uid` and
      `AccountID: uid` become `CreatedByAccountID`.
- [x] 6.7 `internal/gateway/handlers/external_charges_test.go`:
      `fakeChargeReader` re-signs its four methods and renames
      `ListEntriesByAccount` to `ListEntriesByVehicles`. Keep the `panic` body on
      `ListEntriesByVehicleUpdatedSince`.
- [x] 6.8 `go build ./internal/analytics/... ./internal/gateway/... ./cmd/...`
      and `go vet` the same packages — expect clean.
- [x] 6.9 Do NOT add `authorizeVehicle`, `vehicleref.All` or `vehicleref.TeslaIDs`
      here. This task only keeps the build green; roadmap tier 3 owns the
      authorization work.

## T7 — Docs

Depends on: T3 (needs the final port shape). Touches no Go file — safe to run in
parallel with T4–T6.

- [x] 7.1 `internal/charging/AGENTS.md` — three corrections, all named in
      `design.md` §Docs: §Responsibility's "enforces multi-tenant data isolation"
      claim for manual entries; §Data Ownership's rule that tenant scoping is
      "`account_id` for `manual_charge_entries`"; and §Public Interface, which
      must now state the car-wide read rule and that `created_by_account_id` is
      authorship only.
- [x] 7.2 `kkpa/context/architecture/charging-tables.md` — the
      `manual_charge_entries` section's two revisit triggers both propose an
      "`account_id`-leading index", a shape this table can no longer have. State
      the new key and the single `(tesla_id, charged_on DESC)` index.
- [x] 7.3 `kkpa/context/workflows/manual-charge-crud.md` — the `query.sql` row and
      the Update bullet name the write scope `account_id`. The scope survives this
      tier, so do not delete it: rename it to `created_by_account_id` and add that
      it is transitional, replaced by a `tesla_id` guard. Also correct the read
      side, which is car-wide now.
- [x] 7.4 `kkpa/context/architecture/charge-record-mutation.md` — the line saying
      `fetchEntryVM` calls `ListEntriesByAccount(ctx, uid, 0)`.
- [x] 7.5 `kkpa/context/use-case/charging/update-manual-charge.md` and
      `kkpa/context/use-case/charging/delete-manual-charge.md` — the call-chain
      steps, the read/write tables, and the delete guide's "100-row lookup cap"
      note, all of which name `ListEntriesByAccount`.
- [x] 7.6 Do NOT edit anything under `openspec/changes/archive/`. A grep for
      `account_id` will hit archived designs; those hits are the record of what
      was decided then and are not yours to fix. `make archive-guard` enforces it.
- [x] 7.7 Do NOT edit anything under `kkpa/context/pending-spec-to-sync/applied/`.
      Those are applied proposals, a record, not live guides.

## T8 — Final verification

Depends on: T0–T7.

- [x] 8.1 `go build ./...`
- [x] 8.2 `go vet ./...`
- [x] 8.3 `gofmt -l internal/ cmd/` — expect no output.
- [x] 8.4 Grep the whole repo for a **bare** `account_id` — one NOT preceded by
      `created_by_` — within 3 lines of `manual_charge_entries`, **across line
      breaks**, and for `ListEntriesByAccount`. Expect zero hits outside
      `openspec/changes/archive/`,
      `kkpa/context/pending-spec-to-sync/applied/`, and the untouched historic
      migration files. The `created_by_` prefix matters: hits on
      `created_by_account_id` are correct and must not be "fixed".
- [x] 8.5 `make migration-guard`, `make boundary-guard`, `make vehicleref-guard`,
      `make archive-guard` — all clean (migration-guard still prints its known
      `20260720000001` warning).
- [x] 8.6 **Hand-off, branch level — now a preference, not a hard rule.** The
      write path is authorized at every commit on this branch (`design.md` D4), so
      merging after this tier alone is safe from an authorization point of view.
      One reason to wait remains, and it is a user-visible one: until tier 3, a
      vehicle registered to two accounts shows each account the other's entries
      with edit and delete controls that fail — `Update` reports an error and
      `Delete` silently does nothing. On a car with one registered account nothing
      changes. Record this in the change's notes and let the owner decide; do not
      state it as a block.
- [x] 8.7 `openspec validate RM58-charging-demote-manual-charge-account-id --strict`
- [x] 8.8 Hand the owner the suite commands — this agent never runs them:
      `make test` (disposable container) and, for the DB-backed charging tests
      specifically,
      `go test ./internal/charging/ -run 'TestList|TestCreate|TestUpdate|TestDelete|TestSharedVehicle|TestMonthlyCapacity' -v`,
      confirming the output says `PASS` and not `SKIP`. If a local Postgres is
      used instead, `make db-setup-test` first. Also
      `go test ./internal/analytics/... ./internal/gateway/... -count=1`.
- [x] 8.9 Hand the owner the post-`migrate-up` verification queries, since this
      project verifies migrations by inspection, not by test:
      `\d charging.manual_charge_entries` (expect `created_by_account_id uuid not null`,
      no `account_id`, one index `idx_manual_charge_entries_vehicle_time` on
      `(tesla_id, charged_on DESC)`, and no
      `idx_manual_charge_entries_account_time`), plus
      `SELECT count(*) FROM charging.manual_charge_entries WHERE created_by_account_id IS NULL;`
      (expect 0) and a spot check that the row count is unchanged from before the
      migration.
