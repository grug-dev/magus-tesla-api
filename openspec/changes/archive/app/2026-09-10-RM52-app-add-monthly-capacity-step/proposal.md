# Proposal — RM52-app-add-monthly-capacity-step

Source: MAG-32 — https://linear.app/magus-monitor/issue/MAG-32/vehicle-monthly-metrics-new-table
Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md` — **tier 2 of 3**, module `app`,
implementing roadmap decisions **RD6, RD7** verbatim (and passing `teslaID = nil` per tier 1's own
`Calculate` contract, RD8's "no `-tesla-id`" case). RD1–RD5, RD9–RD14 belong to tier 1 (done) or
tier 3 and are not re-opened here.

Design gate: **NOT tripped.** This change adds no table, column, index, or migration. It calls one
existing port (`charging.MonthlyCapacityCalculator`, built by tier 1) from one new step in
`internal/app`. `openspec/config.yaml` §design's database gate does not apply.

Unit tests: **included** — `internal/app`'s standing testing convention
(`internal/app/AGENTS.md` §Testing notes) covers new orchestration seams with fakes, same as
`RM36-app-record-poll-run` tier 2 did for `buildPollRun`/`recordRun`. Expected values are fixed in
design.md §Test Contract **before** implementation, per `ai/go-conventions.md` §Testing authoring
order.

The interview of record is the roadmap's own `grill-me` session, 2026-09-10. This tier does not
re-run it and does not re-open RD1–RD14.

---

## Why

`internal/charging` now measures each vehicle's real pack capacity once tier 1's job runs
(`charging.MonthlyCapacityCalculator.Calculate`) — but nothing calls it yet. The ticket's own
requirement is that this measurement **runs automatically, after the nightly cycle finishes**.

`internal/app.ProcessVehicleData` already IS "the nightly cycle" — it runs fleet-data sync, then
charging-data processing, then analytics recalculation, once per night. Adding a fourth step here
makes "runs after the nightly job finishes" true by construction, not by a second, separately
scheduled job that could drift out of order.

## What Changes

- **CHANGED** — `internal/app/app.go`: `NewProcessor` gains one new parameter,
  `monthlyCapacityCalculator charging.MonthlyCapacityCalculator`, placed with `charging`'s other
  two ports (`sessionWriter`, `mirrorWatermarks`) — the existing parameter grouping is by owning
  module, so this keeps `charging`'s three ports together. `Processor`'s own doc comment gains one
  line noting the fourth step and its "first day of month only" condition.
- **CHANGED** — `internal/app/processor.go`:
  - `processor` struct gains one field, `monthlyCapacityCalculator charging.MonthlyCapacityCalculator`.
  - `ProcessVehicleData`'s `if err == nil { ... }` block gains one call, `p.runMonthlyCapacityStep(ctx)`,
    after `p.recalculateAnalytics(ctx)` — so step 4 follows the same short-circuit as steps 2 and 3
    (RD6): a step-1 whole-cycle failure skips it too.
  - **ADDED** — `monthlyCapacityPeriod(now time.Time, loc *time.Location) (period time.Time, run bool)`:
    a pure function, no clock read of its own, mirroring `scheduler.go`'s `nextRun` (design.md
    §"Date/clock design"). Decides whether today is the first day of the month in `loc` and, if so,
    returns the previous month's first instant.
  - **ADDED** — `(*processor) runMonthlyCapacityStep(ctx context.Context)`: reads `clock.Now()` and
    `clock.Zone()` (RD7 — **not** `p.loc`, see design.md D1), calls `monthlyCapacityPeriod`, and on
    `run == true` calls `p.callMonthlyCapacityCalculator(ctx, period)`.
  - **ADDED** — `(*processor) callMonthlyCapacityCalculator(ctx context.Context, period time.Time)`:
    calls `p.monthlyCapacityCalculator.Calculate(ctx, period, nil)` and logs the outcome. An error
    is logged, never fatal — the same "errors are logged, isolated" pattern `processChargingData`
    and `recalculateAnalytics` already use.
- **CHANGED** — `cmd/poller/main.go`: one new argument at the `app.NewProcessor` call site,
  `charging.NewMonthlyCapacityCalculator(pool)`, in the same position `app.go`'s new parameter
  takes.
- **CHANGED** — `internal/app/processor_test.go`: the fake roster gains
  `fakeMonthlyCapacityCalculator` (records calls, configurable report/error); `newTestProcessor`
  passes it; the existing step-1-whole-cycle-failure test (Fixture P4) gains an assertion that it
  is never called.
- **ADDED** — `internal/app/monthly_capacity_step_test.go`: offline unit tests for
  `monthlyCapacityPeriod` (the pure gate, several calendar cases including a zone-crossing one) and
  `callMonthlyCapacityCalculator` (success and error, via the fake).
- **CHANGED** — `internal/app/AGENTS.md`: §Responsibility (the fourth step, one sentence),
  §Public interface (the constructor's new parameter), §Allowed/forbidden imports (`charging.
  MonthlyCapacityCalculator` added to the existing `internal/charging` bullet), §Testing notes (the
  new pure function and the new fake).
- **UNCHANGED** — every existing `Processor` method's signature, `Scheduler`, `nextRun`, and every
  step 1–3 behavior. No database object of any kind is added, changed, or read directly by this
  module — this step only calls tier 1's public port.

**Out of scope, deliberately:** the `cmd/monthly-capacity` runnable, the `make` target, and the
docs/KB updates that name it (tier 3, `platform`). This change does not touch `internal/gateway`,
`internal/charging`'s own files, or any migration.

## Breaking?

**NO.** `Processor`'s single method, `ProcessVehicleData`, keeps its exact signature and return
type. `NewProcessor` gains one parameter — every existing call site (`cmd/poller/main.go`, this
change's own updated call, and every test's `newTestProcessor` helper) is updated in the same
change, so nothing outside this change can observe a half-migrated signature. No exported type is
removed or renamed.

## Modules affected

- **`app`** — owner. New step, new pure function, new private step functions, the constructor's
  new parameter, tests, `AGENTS.md`.
- **`cmd/poller`** — one new argument at one call site (wiring only, no new logic — `cmd/` stays
  thin, `CLAUDE.md` §Non-negotiables).
- **`charging`** — **not affected by this tier.** Tier 1 already built and archived
  `MonthlyCapacityCalculator`; this tier only calls it.
- **`gateway`**, **`analytics`**, **`telemetry`**, **`account`** — **not affected.** None of their
  ports, types, or imports change.

## Read paths affected

Per `openspec/config.yaml` §proposal (any change touching the database must name the read paths).
**This change touches no database directly** — it adds no query, table, column, or index of its
own. The one database effect is indirect: once a month, `charging.MonthlyCapacityCalculator.
Calculate` runs (tier 1's own two batch `SELECT`s and one `UPSERT`, already justified in tier 1's
design.md §Index Plan as a low-frequency batch job with no read-latency budget to protect). This
tier changes neither that query shape nor its frequency claim — it is still exactly once a month,
now driven by the nightly processor instead of by hand.

## Impact

- **Affected spec:** `process-vehicle-data` (existing capability, from `RM29-app-add-process-
  vehicle-data` and `RM36-app-record-poll-run`) — one requirement **MODIFIED** (three steps become
  four), one requirement **MODIFIED** (the short-circuit covers the fourth step too), one
  requirement **ADDED** (the first-day-of-month, previous-month gate).
- **Affected code:** `internal/app/app.go`, `internal/app/processor.go`,
  `internal/app/processor_test.go` (changed), `internal/app/monthly_capacity_step_test.go` (new),
  `internal/app/AGENTS.md`, `cmd/poller/main.go`.
- **Design gate: not tripped.** No database object of any kind is added or changed by this tier.
- **`MIGRATIONS_DIRS` order check:** not applicable — this tier adds no migration.
- **Deferred, explicitly NOT in scope:** the `cmd/monthly-capacity` runnable and its `make` target
  (tier 3), any gateway surface (roadmap RD8 — none is planned, ever, for this feature).

## Modules affected — summary table

| Module | Change |
|---|---|
| `app` | Owner. New step, new functions, constructor parameter, tests, docs. |
| `cmd/poller` | One new argument at one call site. |
| `charging` | None in this tier — tier 1 already built the port this tier calls. |
| `gateway` | None, ever, per roadmap RD8. |
