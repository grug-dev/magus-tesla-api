# Tasks — RM57-telemetry-rekey-supercharger-history-on-tesla-id

All work is inside `internal/telemetry`, except **T8**, which is leader-owned
and touches `internal/app` and `internal/analytics`.

## Dependency graph

```
T1 (migration) ──► T2 (queries + sqlc) ──► T3 (ports + domain type) ──► T4 (implementation)
                                                              │              │
T0 (offline tests, no deps) ──────────────────────────────────┘              │
                                                                             ├──► T5 (DB integration tests)
                                                                             ├──► T6 (test fixups in telemetry)
                                                                             └──► T8 (leader-owned: app + analytics)
T7 (docs) — depends on T3 only, runs in parallel with T5/T6/T8
                                                              T9 (final verification) — depends on all
```

Sequencing rules that are not optional:

- **T1 → T2 → T3 → T4 is a hard chain.** Go cannot compile against
  sqlc-generated types until they are regenerated, and the ports cannot be
  re-implemented until those types exist.
- **T0 is pure Go with no schema dependency** and may start immediately. But T0
  and T4 both edit `internal/telemetry/service.go` and
  `internal/telemetry/service_test.go` — run them in sequence (T0 first), never
  as two parallel agents on the same files.
- **T5 and T6 may run in parallel with each other** — disjoint files (T5 owns
  the `db_supercharger_*` integration tests, T6 owns the rest).
- **T7 (docs) touches no Go file** and may run in parallel with T4–T8.
- **T8 must land in the same wave as T4.** `go build ./...` is red outside
  `internal/telemetry` until it does (`design.md` D7).

---

## T0 — Offline tests for the skip counter

Depends on: nothing. Author these from `design.md`'s test contract **before**
writing T4's implementation.

- [ ] 0.1 In `internal/telemetry/service_test.go`, add T-1: `owned` holds only
      `VIN_A → 111`; the history holds `(VIN_A, 1)`, `(VIN_B, 2)`,
      `(VIN_A, 3)`. Assert the fake store received exactly 2 upserts (sessions
      1 and 3, both `TeslaID == 111`), `ChargingSessionsUpserted == 2`,
      `ChargingSessionsSkippedUnregistered == 1`, `ChargingFetchFailures == 0`.
- [ ] 0.2 Add T-2: `owned` empty, 2 sessions in the history. Assert 0 upserts,
      `ChargingSessionsUpserted == 0`,
      `ChargingSessionsSkippedUnregistered == 2`, no error.
- [ ] 0.3 Add T-3: `ChargingHistory` returns an error. Assert
      `ChargingFetchFailures == 1` and
      `ChargingSessionsSkippedUnregistered == 0` — the skip counter must not
      absorb a fetch failure.
- [ ] 0.4 Add T-4: registered sessions 1 and 3, unregistered session 2, store
      fails on session 1. Assert `ChargingSessionsUpserted == 1`,
      `ChargingSessionsSkippedUnregistered == 1`, no error returned.
- [ ] 0.5 In `internal/telemetry/report_test.go`, add T-5: a `CycleReport` with
      upserted 4, fetch failures 1, skipped 2 produces a line containing
      `charging_upserted=4 charging_failures=1 charging_skipped_unregistered=2`
      in that order.
- [ ] 0.6 `go vet ./internal/telemetry/...` — expect it to FAIL until T3/T4
      add the field and the guard. That failure is the signal the tests are
      real; do not weaken them to make it pass.

## T1 — Migration

Depends on: nothing.

- [ ] 1.1 Write `internal/telemetry/db/migrations/20260912000001_rekey_supercharger_history_on_tesla_id.sql`
      exactly as `design.md` §Schema gives it — Up and Down, comments included.
      Do not renumber: `20260912000001` is free across all four module
      directories (`design.md` §Makefile).
- [ ] 1.2 Do NOT touch `COMMENT ON TABLE telemetry.supercharger_history`
      (`design.md` D8), the primary key, `UNIQUE (session_id)`, or any CHECK
      constraint.
- [ ] 1.3 `make migration-guard` — expect clean.
- [ ] 1.4 Do NOT write a test for this migration. This project does not test
      migrations; the owner verifies them against the database directly.

## T2 — Queries and sqlc

Depends on: T1.

- [ ] 2.1 `internal/telemetry/db/query.sql`, `UpsertSuperchargerHistory`:
      remove `account_id` from the INSERT column list and from `VALUES`, and
      remove `account_id` from **both** `'{…}'::text[]` deny-list arrays in the
      `updated_at` CASE. Leave the load-bearing comment about the human-owned
      battery-% trio word for word. Update the comment's deny-list bucket (c)
      to stop naming `account_id`.
- [ ] 2.2 Delete the `SuperchargerHistoryByAccount` query (`design.md` D3).
- [ ] 2.3 Delete the `SuperchargerHistoryByAccountUpdatedSince` query
      (`design.md` D4).
- [ ] 2.4 `SuperchargerHistoryByVehicle`: `WHERE tesla_id = @tesla_id` only.
      Update its index comment to name
      `idx_supercharger_history_vehicle_time (tesla_id, charge_start_date_time DESC)`.
- [ ] 2.5 `SuperchargerHistoryByVehicleBetween`: drop the `account_id`
      predicate. Keep the stop-time filter, the bounds explanation and the
      "no LIMIT" note. Update the index-reuse comment to the new index shape.
- [ ] 2.6 `SuperchargerHistoryByVehicleUpdatedSince`: drop the `account_id`
      predicate. Rewrite its "Index reuse" comment — it now has an exactly
      matching index (`idx_supercharger_history_vehicle_updated`) and is no
      longer a residual-filter scan.
- [ ] 2.7 `make sqlc`. Confirm in the diff that
      `telemetrydb.SuperchargerHistory` lost `AccountID` and that its `TeslaID`
      is `int64`, not `pgtype.Int8`; and that the two deleted queries and their
      `…Params` types are gone.

## T3 — Ports and domain type

Depends on: T2.

- [ ] 3.1 `internal/telemetry/telemetry.go`: `SuperchargerHistory` drops
      `AccountID`; `TeslaID` becomes `int64`. Replace the field's
      "NULL when VIN not a current registered vehicle" comment with the real
      reason: a session is only stored for a registered vehicle.
- [ ] 3.2 Update the `SuperchargerHistory` type's doc comment: its nullable-field
      list must stop naming `tesla_id`.
- [ ] 3.3 `SuperchargerHistoryReader`: delete `SuperchargerHistoryByAccount` and
      `SuperchargerHistoryByAccountUpdatedSince`. The three survivors drop
      their `accountID` parameter.
- [ ] 3.4 Delete the doc-comment sentences that contrast the per-vehicle
      updated-since method with the removed account-wide one, and the claim
      that the account-wide method is the only one returning a NULL `tesla_id`.
- [ ] 3.5 `CycleReport`: add `ChargingSessionsSkippedUnregistered int` directly
      after `ChargingFetchFailures`, with a doc comment saying what it counts
      and why the session is not stored.

## T4 — Implementation

Depends on: T3, and T0 must already be written.

- [ ] 4.1 `internal/telemetry/reader.go`: the three surviving methods drop
      `accountID` from the signature and from the `…Params` literal; assign
      `TeslaID: teslaID` directly. Delete the two removed method
      implementations.
- [ ] 4.2 `internal/telemetry/service.go`: delete the `teslaIDToPgInt8` helper
      — T4.1 removed its last three call sites. Confirm with a repo grep before
      deleting.
- [ ] 4.3 `internal/telemetry/service.go`, `upsertSuperchargerHistory`: drop
      `AccountID` from the params literal and replace the nullable
      `pgtype.Int8` wrap with the plain `s.TeslaID`.
- [ ] 4.4 `internal/telemetry/service.go`, `collectChargingHistory`: turn the
      VIN lookup into a guard exactly as `design.md` §service.go shows —
      `continue` plus `report.ChargingSessionsSkippedUnregistered++` when the
      VIN is absent. Drop `AccountID` from the `domainSession` literal and
      assign `TeslaID: teslaID`. Update the function's doc comment: the
      sentence about storing a NULL `tesla_id` is now wrong.
- [ ] 4.5 `internal/telemetry/mapping.go`, `rowToSuperchargerHistory`: delete
      the nullable `tesla_id` block, assign `TeslaID: r.TeslaID`, drop
      `AccountID`, and remove the `pgtype.Int8 → *int64` line from the mapping
      rules comment.
- [ ] 4.6 `internal/telemetry/query_log.go`: delete the two removed decorator
      methods; drop `account=%s` from the three survivors' signatures and log
      lines; drop `account=%s` from the `upsertSuperchargerHistory` decorator
      and make its `tesla_id` a plain `%d`.
- [ ] 4.7 `internal/telemetry/report.go`, `LogCycle`: add
      `charging_skipped_unregistered=%d` right after `charging_failures=%d`,
      and update the doc comment's list of what the line carries.
- [ ] 4.8 `go build ./internal/telemetry/...` and
      `go vet ./internal/telemetry/...` — expect clean. The rest of the repo is
      still red until T8.

## T5 — Database-backed integration tests

Depends on: T4. May run in parallel with T6 — disjoint files.

- [ ] 5.1 Delete `internal/telemetry/db_supercharger_account_updated_since_integration_test.go`
      entirely (all seven tests go with the removed port, `design.md` D4).
- [ ] 5.2 `db_supercharger_integration_test.go`: drop `account_id` from every
      seed `INSERT` and every `DELETE` cleanup; re-key every read to the
      per-vehicle methods. Delete the tests that only exercised
      `SuperchargerHistoryByAccount` account scoping; replace the per-vehicle
      scoping test with T-7's exact seeds and expected order
      (`[7002, 7001]`, session 7003 absent).
- [ ] 5.3 Add T-8 to the same file (or a new
      `db_supercharger_vehicle_updated_since_integration_test.go`): the three
      `updated_at` values, the `tesla_id=222` decoy, and the three `since`
      assertions — `t2 → [7102, 7103]`, `t3 → [7103]`, `t3+1ns → empty
      non-nil`.
- [ ] 5.4 Add T-9: `EXPLAIN` the per-vehicle updated-since query, assert the
      plan names `idx_supercharger_history_vehicle_updated` and contains no
      `Sort` node.
- [ ] 5.5 `db_supercharger_between_integration_test.go`: drop the `accountID`
      argument and the `account_id` seed column; keep the stop-time semantics
      and re-assert them with T-10's seeds and expected order
      (`[7201, 7204, 7202]`).
- [ ] 5.6 `db_supercharger_battery_pct_integration_test.go`: re-key every seed
      and every read to `tesla_id`; the battery-% trio assertions are
      unchanged. Add T-11's round-trip assertion that `TeslaID` comes back as
      a plain `int64`.
- [ ] 5.7 `db_change_detection_schema_test.go`: remove `account_id` from
      `changeDetectDenyListColumns` (15 entries left, T-12) and from its
      bucket-(c) comment. Do not change the partition logic.
- [ ] 5.8 `db_change_detection_integration_test.go`: drop `account_id` from
      every seed and from the "poke every settable deny-listed column" loop
      (T-13).
- [ ] 5.9 Assert `RowsAffected()` on every fixture `UPDATE`/`DELETE` this task
      touches — a fixture write that matches no row does not error, and the
      assertions after it then pass or fail for an unrelated reason
      (`design.md` §Risks).

## T6 — Remaining test fixups inside telemetry

Depends on: T4. May run in parallel with T5.

- [ ] 6.1 Grep every `_test.go` in `internal/telemetry` for
      `supercharger_history` and for `SuperchargerHistory{` — **across line
      breaks**, not line by line: a table name and its column often sit on
      different lines, so a line-based grep finds some hits and misses others.
- [ ] 6.2 `reader_test.go`: drop `accountID` from every call and every fixture;
      drop `AccountID` from every `SuperchargerHistory` literal.
- [ ] 6.3 `query_log_test.go`: remove the two removed methods from
      `fakeQueryLogSCHReader`; update the three surviving fakes' signatures;
      rewrite the expected log strings to T-6's shape (no `account=` field).
- [ ] 6.4 `service_test.go`: any fake `store` implementation and any
      `SuperchargerHistory` literal drops `AccountID` and uses a plain
      `TeslaID`.
- [ ] 6.5 `go vet ./internal/telemetry/...` — expect clean. `go vet` compiles
      `_test.go` files, so this is what proves T0's and T5's signatures are
      right.

## T7 — Docs

Depends on: T3 (needs the final port shape). Touches no Go file — safe to run
in parallel with T4–T8.

- [ ] 7.1 `internal/telemetry/AGENTS.md` — five corrections, all named in
      `design.md` §Docs: the "sessions for VINs no longer registered get
      `tesla_id = NULL`" sentence; the upsert's refreshed-column list; the
      data-ownership line saying `supercharger_history` "still carries
      `account_id` too"; the port table's method count; the note calling
      `SuperchargerHistoryByAccountUpdatedSince` the only method that can
      return a NULL `tesla_id`.
- [ ] 7.2 `kkpa/context/architecture/telemetry-ingest-only.md` — its consumer
      table names `SuperchargerHistoryByAccount` as `internal/app`'s call.
      Replace it with `SuperchargerHistoryByVehicleUpdatedSince`, and say the
      mirror reads per vehicle.
- [ ] 7.3 `kkpa/context/architecture/telemetry-tables.md` — the closing
      "`account_id`/`tesla_id` are plain columns" line, and the
      `supercharger_history` bullet, which must now state the key:
      `tesla_id NOT NULL`, no `account_id`.
- [ ] 7.4 `kkpa/context/architecture/nightly-cycle.md` — both cells naming
      `SuperchargerHistoryByAccount` (the consumer table and the per-table
      read/write table).
- [ ] 7.5 Do NOT edit anything under `openspec/changes/archive/`. A grep for
      `account_id` will hit archived designs; those hits are the record of what
      was decided then and are not yours to fix. `make archive-guard` enforces
      it.

## T8 — Cross-module bridge (leader-owned)

Depends on: T3 (needs the final port signatures). **Not this module's sandbox —
`internal/telemetry` workers must not edit these files.** Must land in the same
wave as T4, or `go build ./...` stays red (`design.md` D7).

- [ ] 8.1 `internal/app/processor.go:223`: replace the single
      `SuperchargerHistoryByAccountUpdatedSince(ctx, v.AccountID, cursor.Add(-mirrorOverlap))`
      call with a fan-out over the account's vehicles, calling
      `SuperchargerHistoryByVehicleUpdatedSince(ctx, tid, cursor.Add(-mirrorOverlap))`
      once per distinct `tesla_id` of that account and concatenating the
      results. `MirrorWatermark`, `AdvanceMirrorWatermark`, `MirrorSessions`
      and the per-account loop keep their current account-keyed shape.
- [ ] 8.2 `internal/app/processor.go`: `charging.SessionMirror{AccountID: …}`
      takes the account from the loop variable, not from the session (the
      session no longer has one). `SessionMirror.TeslaID` is `*int64` until
      roadmap tier 2 — pass the address of the loop's `tesla_id`.
- [ ] 8.3 `internal/app/processor.go`: update the loop's own comments. The
      one explaining that the read is account-wide "so mirroring per vehicle
      would re-mirror the same sessions" is no longer true.
- [ ] 8.4 `internal/app/app.go:96`: no signature change expected — confirm the
      `telemetry.SuperchargerHistoryReader` parameter still compiles.
- [ ] 8.5 `internal/app/processor_test.go`: `fakeSuperchargerHistoryReader` and
      `stubSuperchargerHistoryReader` must match the three-method interface —
      delete the two removed methods, re-sign the survivors, and drop
      `AccountID` from the `session(...)` helper's
      `telemetry.SuperchargerHistory` literal.
- [ ] 8.6 `internal/app/processor_test.go`: the two mirror tests seed sessions
      per account today; re-shape them for the per-vehicle fan-out and keep
      the watermark assertions (zero rows leaves the cursor untouched; the new
      cursor is the highest observed `updated_at`, never `now()`).
- [ ] 8.7 `internal/analytics/db_integration_test.go`,
      `seedSuperchargerSession`: drop `account_id` from the raw `INSERT`
      column list and its parameter, and change `pgInt8FromPtr(s.TeslaID)` to
      the plain `s.TeslaID`.
- [ ] 8.8 `internal/analytics/db_integration_test.go`: every
      `telemetry.SuperchargerHistory{AccountID: …, TeslaID: &teslaIDCopy}`
      literal drops `AccountID` and passes `TeslaID` by value.
- [ ] 8.9 `go build ./internal/app/... ./internal/analytics/...` and
      `go vet` the same two packages — expect clean.

## T9 — Final verification

Depends on: T0–T8.

- [ ] 9.1 `go build ./...`
- [ ] 9.2 `go vet ./...`
- [ ] 9.3 `make migration-guard`
- [ ] 9.4 `make boundary-guard`
- [ ] 9.5 `make archive-guard`
- [ ] 9.6 `gofmt -l internal/ cmd/` — expect no output.
- [ ] 9.7 Grep the whole repo for `account_id` within 3 lines of
      `supercharger_history`, and for `SuperchargerHistoryByAccount`. Expect
      zero hits outside `openspec/changes/archive/` and the untouched historic
      migration files.
- [ ] 9.8 Hand the owner the suite commands — this agent never runs them:
      `make test` (disposable container) and, for the DB-backed telemetry
      tests specifically,
      `go test ./internal/telemetry/ -run 'TestSupercharger|TestUpsertSupercharger' -v`
      with `TEST_DATABASE_URL` unset, confirming the output says `PASS` and
      not `SKIP`. If a local Postgres is used instead, `make db-setup-test`
      first.
