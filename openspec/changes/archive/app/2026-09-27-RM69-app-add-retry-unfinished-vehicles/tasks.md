# Tasks — RM69-app-add-retry-unfinished-vehicles

All work is inside `internal/app`, except `T6`, which is leader-owned
`cmd/poller` wiring — see `design.md` D6.

## Blocking external dependency — read before starting

**`T4.3` and `T6.3` cannot be implemented until `internal/analytics` gains a
new, narrow, batch-shaped read** (`design.md`'s Blocker section proposes
`UnfinishedForDate(ctx, teslaIDs []int64, date time.Time) ([]int64, error)`
or an equivalent plain-`[]int64` method). This is a separate module change,
not part of this tier. Everything else below can be implemented and tested
without it.

## Dependency graph

```
T1 (nextRetryTick, pure)                         T5 (docs) — depends on T2, T3, T4
T2 (vehiclesToProcess refactor + regression)
        │
        ▼
T3 (ProcessVehicleDataForVehicles + step 4 subset)
        │
        ▼
T4 (NotDoneVehicles interface + RetryScheduler)  ◄── T4.3 blocked on the
        │                                             external analytics
        │                                             dependency above
        ▼
T6 (cmd/poller wiring — leader-owned)            ◄── T6.3 blocked on the
                                                       same dependency
        │
        ▼
T7 (final verification)
```

- **T1 has no dependency** and can start immediately, in parallel with T2.
- **T2 → T3 is a hard chain**: `ProcessVehicleDataForVehicles` calls
  `vehiclesToProcess`, which must exist and be proven behavior-preserving
  first.
- **T3 → T4 is a hard chain**: `RetryScheduler` calls
  `ProcessVehicleDataForVehicles`.
- **T4.1/T4.2 (the interface and the scheduler's own tick logic, tested with
  a fake `NotDoneVehicles`) do not depend on the blocker.** Only `T4.3`
  (wiring a real implementation) does.
- **T5 (docs) depends on T2, T3, T4** — it documents the final shape of all
  three.
- **T6 is leader-owned** (`cmd/poller` is outside this module's sandbox) and
  depends on T3 and T4 existing; `T6.3` additionally depends on the same
  external analytics dependency as `T4.3`.
- **T7 depends on everything except the blocked sub-tasks.**

---

## T1 — `nextRetryTick` (pure, write first)

Depends on: nothing.

- [x] 1.1 In `internal/app/scheduler_test.go` (or a new
      `internal/app/retry_scheduler_test.go` — pick whichever keeps
      `nextRun`'s and `nextRetryTick`'s tests next to their own function;
      record the choice in a one-line comment), write TR-1 through TR-5 from
      `design.md`'s test contract, against the not-yet-existing
      `nextRetryTick`.
- [x] 1.2 Add `retryInterval` and `retryWindowStartHour` constants and
      `nextRetryTick(now time.Time, loc *time.Location) time.Time` in
      `internal/app/scheduler.go` (or the new file from 1.1), exactly as
      `design.md` D5 gives it.
- [x] 1.3 `go build ./internal/app/...` and `go vet ./internal/app/...` —
      expect clean, TR-1..TR-5 now compiling.

## T2 — `vehiclesToProcess` shared helper (refactor, write regression tests first)

Depends on: nothing (parallel with T1).

- [x] 2.1 In `internal/app/processor_test.go`, write TR-9 (regression: `nil`
      filter reproduces today's three inline lists), TR-10 (a real filter
      narrows to the named vehicle), and TR-11 (an unregistered id in the
      filter is silently excluded) — against the not-yet-existing
      `vehiclesToProcess`.
- [x] 2.2 Add `vehiclesToProcess(ctx context.Context, filter []int64)
      ([]account.OwnedVehicle, error)` to `internal/app/processor.go`, exactly
      as `design.md` D2 gives it.
- [x] 2.3 Rewrite `processChargingData`, `recalculateAnalytics`, and
      `runMonthlyMetricsStep` to take their vehicle list as a parameter
      (produced by `vehiclesToProcess`) instead of calling
      `p.acct.AllRegisteredVehicles(ctx)` and deduping inline themselves.
      `ProcessVehicleData` calls `vehiclesToProcess(ctx, nil)` once and passes
      the result to whichever of the three need the full `OwnedVehicle`
      shape; steps that only need `[]int64` derive it with the same one-line
      loop they use today.
- [x] 2.4 Run every EXISTING test that exercises `processChargingData`,
      `recalculateAnalytics`, or `runMonthlyMetricsStep` (owner will run via
      `go test`; this task confirms via `go vet` only, per the
      Test-Execution-Policy) — no edit should have been needed for any of
      them to still pass. If one needed an edit, STOP and report it as a
      behavior change from the refactor rather than "fixing" the test
      (`design.md` D2's regression rule).
- [x] 2.5 `go build ./internal/app/...` and `go vet ./internal/app/...`.

## T3 — `ProcessVehicleDataForVehicles` + step 4 subset scoping

Depends on: T2.

- [x] 3.1 In `internal/app/processor_test.go`, write TR-12 through TR-16 from
      `design.md`'s test contract.
- [x] 3.2 Widen the `Processor` interface in `internal/app/app.go` with
      `ProcessVehicleDataForVehicles(ctx context.Context, triggeredBy
      telemetry.TriggeredBy, teslaIDs []int64) (telemetry.CycleReport, error)`,
      documented per `design.md` D1 (generic `triggeredBy`, no hardcoded
      `TriggeredByRetry`).
- [x] 3.3 Implement `ProcessVehicleDataForVehicles` in
      `internal/app/processor.go`: fresh `RunContext`, `clock.Now()` start,
      `p.collector.CollectVehicles(ctx, run, teslaIDs)` for step 1, the same
      `if err == nil { ... }` short-circuit, steps 2/3/5 called with
      `vehiclesToProcess(ctx, teslaIDs)`'s result, `recordRun` on every exit
      path — mirroring `ProcessVehicleData`'s own shape exactly, per
      `design.md`'s Overview ("share code, not just intent").
- [x] 3.4 Widen `callMonthlyCapacityCalculator`'s signature to take
      `teslaID *int64` (design.md D8); update `runMonthlyCapacityStep`'s
      existing call site to pass `nil` explicitly (no behavior change).
- [x] 3.5 Add `runMonthlyCapacityStepForVehicles(ctx context.Context,
      teslaIDs []int64)`: same `monthlyCapacityPeriod` gate, and when
      `run == true`, loop `callMonthlyCapacityCalculator(ctx, period, &id)`
      once per id in `teslaIDs`. Call it from
      `ProcessVehicleDataForVehicles` in place of `runMonthlyCapacityStep`.
      Write TR-17 and TR-18 for it in `internal/app/monthly_capacity_step_test.go`.
- [x] 3.6 `var _ Processor = (*processor)(nil)` still compiles with no edit —
      confirms the interface widening and the implementation agree.
- [x] 3.7 `go build ./internal/app/...` and `go vet ./internal/app/...`.

## T4 — `NotDoneVehicles` interface + `RetryScheduler`

Depends on: T3.

- [x] 4.1 Declare `NotDoneVehicles` in `internal/app` (new file
      `retry_scheduler.go`, or alongside `scheduler.go` — pick one and note
      the choice), exactly as `design.md` D3 gives it. No new import path.
- [x] 4.2 Implement `RetryScheduler` / `NewRetryScheduler` / `Run` per
      `design.md` D4, using `nextRetryTick` (T1) and `vehiclesToProcess(ctx,
      nil)` (T2) for the registered-vehicle enumeration. Write TR-6, TR-7,
      and TR-8 against a fake `Processor`, a fake `NotDoneVehicles`, and a
      fake clock — these do NOT depend on the external analytics dependency,
      only on the interface shape from 4.1.
- [x] 4.3 **BLOCKED on the external analytics dependency** (see the note at
      the top of this file and `design.md`'s Blocker section). No
      `internal/app` code changes here — this task exists only as a
      placeholder for the leader to re-open once
      `internal/analytics` gains the new read and `cmd/poller` can build a
      concrete `NotDoneVehicles` adapter over it (that adapter itself is
      `T6.3`, leader-owned).
- [x] 4.4 `go build ./internal/app/...` and `go vet ./internal/app/...`.

## T5 — Docs

Depends on: T2, T3, T4 (needs the final shapes).

- [x] 5.1 `internal/app/AGENTS.md` — "Responsibility": extend the five-step
      diagram's callout with the subset-cycle path and `RetryScheduler`,
      following `design.md`'s Overview.
- [x] 5.2 `internal/app/AGENTS.md` — "Public interface (the port)": add
      `ProcessVehicleDataForVehicles` and `RetryScheduler`/`NewRetryScheduler`
      /`nextRetryTick`, matching the existing entries' style (signatures live
      in the source files; this file states behavior, not the copied
      signature).
- [x] 5.3 `internal/app/AGENTS.md` — "Testing notes": add rows for
      `vehiclesToProcess`, `ProcessVehicleDataForVehicles`,
      `runMonthlyCapacityStepForVehicles`, `nextRetryTick`, and
      `RetryScheduler.Run`, following the existing covered/accepted-gap
      table's format.
- [x] 5.4 `cmd/README.md` — update the `cmd/poller` row to mention it also
      starts the retry schedule.
- [x] 5.5 Root `README.md` — update the `internal/app` "Architecture" table
      row and any prose describing the nightly cycle as the poller's only
      schedule.
- [x] 5.6 `kkpa/context/architecture/nightly-cycle.md` — add
      `RetryScheduler`/`nextRetryTick`/`ProcessVehicleDataForVehicles` to the
      "Component map"; add a note to "How maintenance works" about the
      shared `vehiclesToProcess` helper. Leave the "Port map"'s
      `NotDoneVehicles` row for whichever change (`T4.3`/`T6.3`) actually
      wires a caller — do not add a row for an interface with no
      implementation yet, mirroring tier 1's own "don't document a caller
      that doesn't exist yet" rule (`design.md` of tier 1, §"Docs this change
      invalidates"). Also corrected the existing `AllRegisteredVehicles`
      port-map row, which said "×3" — the shared `vehiclesToProcess` helper
      (T2) made this ×1 per invocation, a pre-existing line this same change
      invalidated. NOTE for the leader: this guide's own "Rendered view"
      section says its published Artifact must be refreshed whenever the
      port map changes — that refresh needs the Artifact tool, outside this
      worker's sandbox, so it is not done here.
- [x] 5.7 Do NOT edit anything under `openspec/changes/archive/`.
      `make archive-guard` enforces it.

## T6 — `cmd/poller` wiring (leader-owned, outside `internal/app`'s sandbox)

Depends on: T3, T4 (T6.3 additionally depends on the external analytics
dependency).

- [x] 6.1 `cmd/poller/rerun.go`: widen `guardedProcessor` to implement
      `ProcessVehicleDataForVehicles` with the same `TryLock`/`errCycleBusy`
      shape it already gives `ProcessVehicleData` (`design.md` D6).
- [x] 6.2 `cmd/poller/main.go`: construct `app.NewRetryScheduler(guarded,
      <NotDoneVehicles adapter>, acct, loc, tcfg)` and start
      `retryScheduler.Run(ctx)` in its own goroutine, only inside the
      `!*once` branch (never for `--once`, matching the nightly `Scheduler`'s
      own scope).
- [x] 6.3 **BLOCKED on the external analytics dependency.** Build the small
      adapter satisfying `app.NotDoneVehicles` over whatever
      `internal/analytics` read the blocker's resolution produces, and pass
      it into 6.2's `NewRetryScheduler` call. Cannot be written until that
      read exists.
- [x] 6.4 `go build ./...` and `go vet ./...` once 6.1–6.3 are complete.

## T7 — Final verification

Depends on: T1, T2, T3, T4.1/4.2, T5 (T4.3/T6 excluded — blocked).

- [x] 7.1 `go build ./...`
- [x] 7.2 `go vet ./...`
- [x] 7.3 `gofmt -l internal/app` — expect no output.
- [x] 7.4 `make lint`
- [x] 7.5 `make boundary-guard`
- [x] 7.6 `make naming-guard`
- [x] 7.7 `make vehicleref-guard` — expect it to still pass clean; this tier
      adds no call to `vehicleref.Authorize`/`vehicleref.All` anywhere (that
      is precisely the point of `design.md`'s Blocker section).
- [x] 7.8 `make archive-guard`
- [x] 7.9 Hand the owner the suite command — this agent never runs it:
      `go test ./internal/app/...` (and `make test` for the full suite).
      `internal/app` has no `TEST_DATABASE_URL`-gated tests, so no database
      setup is needed for this module's own tests.
- [x] 7.10 Record in the change (or in the follow-up that resolves the
      Blocker) once `T4.3`/`T6.3` land, and re-run 7.1–7.9 at that point —
      this change is not fully done until those two land, even though every
      other task can reach `awaiting-user-verification` independently.
