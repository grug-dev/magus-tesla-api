# Tasks — RM69-telemetry-add-collect-for-vehicles

All work is inside `internal/telemetry`. No other module is touched.

## Dependency graph

```
T1 (port + constant + collectAccount signature) ──► T2 (CollectVehicles impl)
                                                          │
T0 (offline tests, written from design.md) ──────────────┘
                                                          ├──► T3 (DB integration tests)
                                                          └──► T4 (docs)
                                                                        T5 (final verification) — depends on all
```

- **T1 → T2 is a hard chain.** `CollectVehicles` cannot compile before the
  interface method, the constant, and `collectAccount`'s new signature exist.
- **T0 is written from `design.md`'s test contract before T2's
  implementation** (contract-first authoring, `ai/go-conventions.md`
  §Testing). T0 and T2 both edit `service_test.go` / `service.go` — do them
  in sequence (T0's test file first), not as two parallel agents on the same
  files.
- **T3 (DB-backed) depends on T2** — it exercises the real `dbStore`.
- **T4 (docs) depends on T1 only** and may run in parallel with T2/T3.

---

## T0 — Offline tests for `CollectVehicles` (write first, from design.md)

Depends on: nothing (pure Go, written against the design, before T2 exists).

- [ ] 0.1 In `internal/telemetry/service_test.go`, add TR-1: vehicle 500
      registered to account A (DRIVER) and account B (OWNER), both tokened,
      vehicle online. Call `CollectVehicles(ctx, run, []int64{500})`. Assert
      exactly one `insertPollAttempt` call, `PolledByAccountID == B`,
      `Reason == ReasonOK`. Assert account A's fake token/list methods are
      never invoked.
- [ ] 0.2 Add TR-2: one account with vehicle 501 registered; call with
      `[]int64{501, 999}`. Assert exactly one `insertPollAttempt` call (for
      501), `report.Attempted == 1`, no error.
- [ ] 0.3 Add TR-3: one account owning vehicles 502 (VIN "AAA") and 503 (VIN
      "BBB"), both online; fake `ChargingHistory` returns sessions for both
      VINs. Call with `[]int64{502}`. Assert exactly one `insertPollAttempt`
      call (502 only — no WakeUp/VehicleData call for 503),
      `report.ChargingSessionsUpserted == 2`,
      `report.ChargingSessionsSkippedUnregistered == 0`.
- [ ] 0.4 Add TR-4: a fake `tesla.VehicleService` that calls `t.Fatal` if any
      method is invoked. Call `CollectVehicles(ctx, run, nil)` and again with
      `[]int64{}`. Assert both return a zero `CycleReport` (every count 0,
      including `FailuresByReason` empty) and `nil` error. Assert
      `fakeAccount.AllRegisteredVehicles` is never called for either call.
- [ ] 0.5 Add TR-6: three accounts, each owning one vehicle — 601, 602, 603.
      Call with `[]int64{601, 603}`. Assert `report.AccountsAttempted == 2`.
- [ ] 0.6 `go vet ./internal/telemetry/...` — expect it to FAIL until T1/T2
      add the method, the constant, and the signature. That failure is the
      signal the tests are real.

## T1 — Port method, constant, `collectAccount` signature

Depends on: nothing.

- [ ] 1.1 `internal/telemetry/telemetry.go`: add `CollectVehicles` to the
      `Collector` interface with the full doc comment from `design.md`
      §Signatures (D1, D3, D4, D8 — one sentence each, matching the design's
      wording).
- [ ] 1.2 `internal/telemetry/telemetry.go`: add
      `TriggeredByRetry TriggeredBy = "retry"` next to `TriggeredByScheduler`
      / `TriggeredByAPI`, with the doc comment from `design.md` §Signatures.
- [ ] 1.3 `internal/telemetry/service.go`: change `collectAccount`'s
      signature to add the `chargingScope []account.OwnedVehicle` parameter
      (`design.md` §Signatures), rename its existing `owned` parameter to
      `toCollect`, and update every reference to `owned` inside the function
      body accordingly EXCEPT the one call to `collectChargingHistory`, which
      now passes `chargingScope`. Update the function's doc comment to
      describe both parameters (`design.md` D4).
- [ ] 1.4 `go build ./internal/telemetry/...` — expect it to fail at the
      `CollectAll` call site until T2 updates it. This is expected at this
      point in the sequence.

## T2 — `CollectVehicles` implementation

Depends on: T1, and T0 must already be written.

- [ ] 2.1 `internal/telemetry/service.go`: add the private `enumerateElected`
      method exactly as `design.md` §D5 gives it.
- [ ] 2.2 `internal/telemetry/service.go`: rewrite `CollectAll` to call
      `enumerateElected` instead of inlining `electPollingVehicles`/
      `groupByAccount`, and update its `collectAccount` call site to
      `s.collectAccount(ctx, run, counted, accountID, owned, owned, &report)`.
      No other line of `CollectAll` changes.
- [ ] 2.3 `internal/telemetry/service.go`: add `CollectVehicles` exactly as
      `design.md` §Signatures gives it — the `len(teslaIDs) == 0` early
      return, the `enumerateElected` call, the `want` set, the per-account
      filter loop, and the `collectAccount` call passing `toCollect` and
      `owned` (the account's full elected list) separately.
- [ ] 2.4 Compile-time check: confirm `*service` still satisfies `Collector`
      (the existing `var _ Collector = (*service)(nil)` line needs no edit,
      but a build failure here means the new method's signature does not
      match the interface).
- [ ] 2.5 `go build ./internal/telemetry/...` and
      `go vet ./internal/telemetry/...` — expect clean, and T0's tests to
      now compile and pass.
- [ ] 2.6 Run every EXISTING `CollectAll` test in `service_test.go` (owner
      will do this via `go test`; Claude confirms via `go vet` only,
      `ai/go-conventions.md` §Testing) — no test file edit should have been
      needed for them to still make sense. If one needed an edit, that is a
      sign D4/D5's refactor changed `CollectAll` behavior — stop and report
      it rather than "fixing" the test.

## T3 — Database-backed integration tests

Depends on: T2.

- [ ] 3.1 Add `internal/telemetry/db_collect_vehicles_integration_test.go`,
      mirroring the shape of the module's existing `db_*_integration_test.go`
      files (`testdb.Provision`, real `telemetrydb.Queries`).
- [ ] 3.2 Write TR-7: call `InsertPollAttempt` directly with
      `TriggeredBy: string(telemetry.TriggeredByRetry)`, and `RecordRun` with
      the same value on a `PollRun`. Assert both succeed with no error, and
      a plain `SELECT triggered_by` on each table returns `"retry"`.
- [ ] 3.3 Write TR-8: one account, two registered vehicles seeded via direct
      SQL (this module's existing test-seeding convention — no exported
      writer exists for this). Wire the real `dbStore` with a fake `tesla`
      double (never a live Fleet API call). Call `CollectVehicles` for one of
      the two vehicles. Assert exactly one `telemetry.poll_attempts` row for
      this run with `triggered_by = 'retry'`, and exactly one
      `telemetry.vehicle_snapshots` row for the retried vehicle — none for
      the other.
- [ ] 3.4 Assert `RowsAffected()` on any fixture `UPDATE`/`DELETE` this task
      adds (`ai/go-conventions.md` §Persistence — a write matching no row
      does not error).
- [ ] 3.5 `go vet ./internal/telemetry/...` — expect clean (proves the new
      test file compiles; it is not run).

## T4 — Docs

Depends on: T1 (needs the final port shape). May run in parallel with T2/T3.

- [ ] 4.1 `internal/telemetry/AGENTS.md` — the "Public interface (the port)"
      table's `Collector` row: change `CollectAll` — … to name both methods
      (`CollectAll`, `CollectVehicles`), one line each, matching the style of
      the existing row.
- [ ] 4.2 `internal/telemetry/AGENTS.md` — add one sentence to "What the
      source does not tell you" noting `collectAccount`'s two vehicle-list
      parameters and why they can differ (D4), so a future reader does not
      "simplify" them back into one.
- [ ] 4.3 Do NOT edit `kkpa/context/architecture/nightly-cycle.md` in this
      tier — it documents callers, and this tier adds none (tier 2 does).
      Confirm this file still lists `Collector.CollectAll` as telemetry's
      only Step-1 entry point; leave it. This is intentional, not an
      oversight — see `design.md` §Docs this change invalidates.
- [ ] 4.4 Do NOT edit anything under `openspec/changes/archive/`.
      `make archive-guard` enforces it.

## T5 — Final verification

Depends on: T0–T4.

- [ ] 5.1 `go build ./...`
- [ ] 5.2 `go vet ./...`
- [ ] 5.3 `gofmt -l internal/telemetry` — expect no output.
- [ ] 5.4 `make lint`
- [ ] 5.5 `make boundary-guard`
- [ ] 5.6 `make migration-boundary-guard` — expect no diff (no migration
      added in this tier).
- [ ] 5.7 `make naming-guard`
- [ ] 5.8 `make tenancy-guard`
- [ ] 5.9 `make archive-guard`
- [ ] 5.10 Grep `internal/telemetry` for `collectAccount(` to confirm every
      call site passes two vehicle-list arguments — zero stale one-argument
      call sites left.
- [ ] 5.11 Hand the owner the suite commands — this agent never runs them:
      `make test` (disposable container), and for the new DB-backed tests
      specifically,
      `go test ./internal/telemetry/ -run 'TestCollectVehicles' -v`
      with `TEST_DATABASE_URL` unset, confirming the output says `PASS` and
      not `SKIP`. If a local Postgres is used instead, `make db-setup-test`
      first.
