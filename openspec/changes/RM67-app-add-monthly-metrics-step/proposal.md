# Proposal — RM67-app-add-monthly-metrics-step

Source: MAG-73 — https://linear.app/magus-monitor/issue/MAG-73/analytics-monthly-metrics-table-distance-efficiency-and-copied

Roadmap: `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md` — **tier 4 of 4**, module `app`,
the last tier. It implements roadmap decision **RD3** (refresh every night, current AND previous
month) plus **RD6** and **RD10**, which constrain how this tier is built. RD1, RD2, RD4, RD5, RD7,
RD8, RD9 belong to tiers 1-3 (all archived) and are not re-opened here.

Design gate: **NOT tripped.** This change adds no table, column, index, or migration. It calls one
existing port, `analytics.MonthlySyncer` (built and archived by tier 2/3), from one new step in
`internal/app`. `openspec/config.yaml` §design's database gate does not apply.

Unit tests: **included** — `internal/app`'s standing testing convention
(`internal/app/AGENTS.md` §Testing notes) covers new orchestration seams with fakes, the same shape
`RM52-app-add-monthly-capacity-step` used for step 4. Expected values are fixed in design.md
§Test Contract **before** implementation, per `ai/go-conventions.md` §Testing authoring order.

The interview of record is the roadmap's own `grill-me` session, 2026-09-18. This tier does not
re-run it and does not re-open RD1-RD10.

---

## Why

`analytics.MonthlySyncer.SyncMonth(ctx, teslaID, period)` exists and is finished — but nothing
calls it yet. The ticket's own requirement is that this sync **runs automatically, every night**,
for the current month and the previous month, for every vehicle (RD3 — superseding the ticket's
original "first day of the month only" text).

`internal/app.ProcessVehicleData` already IS "the nightly cycle" — it runs fleet-data sync, then
charging-data processing, then analytics recalculation, then monthly capacity measurement, once
per night. Adding a fifth step here makes "runs every night" true by construction, the same way
step 4 made "runs after the nightly job finishes" true by construction.

## What Changes

- **CHANGED** — `internal/app/app.go`: `NewProcessor` gains one new parameter,
  `monthlySyncer analytics.MonthlySyncer`, placed with `analytics`'s other three ports
  (`recalculator`, `analyticsReader`, `gapWriter`) — the existing parameter grouping is by owning
  module, so this keeps `analytics`'s four ports together. `Processor`'s own doc comment (and the
  package doc comment) gain a fifth step in the diagram.
- **CHANGED** — `internal/app/processor.go`:
  - `processor` struct gains one field, `monthlySyncer analytics.MonthlySyncer`.
  - `ProcessVehicleData`'s `if err == nil { ... }` block gains one call,
    `p.runMonthlyMetricsStep(ctx)`, after `p.runMonthlyCapacityStep(ctx)` — step 5 follows the same
    short-circuit as steps 2-4: a step-1 whole-cycle failure skips it too. Running step 5 AFTER
    step 4 is load-bearing on the first day of the month: step 4 has just measured the previous
    month's capacity, and step 5's previous-month sync copies that same figure the same night (see
    design.md Context fact 2).
  - **ADDED** — `monthlyMetricsPeriods(now time.Time, loc *time.Location) (current, previous
    time.Time)`: a pure function, no clock read of its own, mirroring `monthlyCapacityPeriod`'s and
    `nextRun`'s shape. Unlike `monthlyCapacityPeriod`, it has no "should I run" gate — RD3 runs it
    every night — and always returns both months' first instants.
  - **ADDED** — `(*processor) runMonthlyMetricsStep(ctx context.Context)`: reads `clock.Now()` and
    `clock.Zone()` (the platform zone, same as step 4 — RD3 says "the month gate uses the platform
    zone through `internal/clock`"), calls `monthlyMetricsPeriods`, enumerates registered vehicles
    the same way `processChargingData`/`recalculateAnalytics` already do (deduplicated by
    `TeslaID`), and for each vehicle calls the use case for the current month, then the previous
    month.
  - **ADDED** — `(*processor) callMonthlySyncer(ctx context.Context, teslaID int64, period
    time.Time)`: calls `p.monthlySyncer.SyncMonth(ctx, teslaID, period)` and logs the outcome. An
    error is logged, never fatal — the same "errors are logged, isolated" pattern every other step
    in this file uses. Isolated per `(vehicle, period)` pair: one vehicle's failure never blocks
    another vehicle, and a vehicle's current-month failure never blocks its own previous-month
    call.
- **CHANGED** — `cmd/poller/main.go` (leader-owned, granted path): one new argument at the
  `app.NewProcessor` call site, `analytics.NewMonthlySyncer(pool, charging.NewMonthlyCapacityReader(pool),
  chargingReader, sessionAnalyticsReader)` — the last two ports are already built locally in that
  file for `analytics.NewRecalculator`, so only `charging.NewMonthlyCapacityReader(pool)` is a new
  construction.
- **CHANGED** — `internal/app/processor_test.go`: the fake roster gains `fakeMonthlySyncer`
  (records every `(teslaID, period)` it was called with, configurable per-call error);
  `newTestProcessor` passes it; the existing step-1-whole-cycle-failure test gains an assertion
  that it is never called.
- **ADDED** — `internal/app/monthly_metrics_step_test.go`: offline unit tests for
  `monthlyMetricsPeriods` (the pure period function, several calendar cases including a
  zone-crossing one) and `callMonthlySyncer` (success and error, via the fake).
- **CHANGED** — `internal/app/AGENTS.md`: §Responsibility (the fifth step, one sentence),
  §Public interface (the constructor's new parameter), §Allowed/forbidden imports (`analytics.
  MonthlySyncer` added to the existing `internal/analytics` bullet), §Testing notes (the new pure
  function and the new fake).
- **CHANGED** — `internal/app/app.go`'s package doc comment: the four-step diagram becomes five
  steps.
- **CHANGED** — `kkpa/context/architecture/nightly-cycle.md` (app worker, granted path): the KB
  guide for this exact cycle names four steps throughout; it gains a fifth-step section, an updated
  glossary line, an updated port map row, and an updated table-effects row.
- **CHANGED** — root `README.md` (leader-owned): the `internal/app` Architecture table row names
  "four named steps" and describes step 4; it becomes five, with one sentence on step 5.
- **UNCHANGED** — every existing `Processor` method's signature (`ProcessVehicleData`'s own
  signature and return type), `Scheduler`, `nextRun`, and every step 1-4 behavior. No database
  object of any kind is added, changed, or read directly by this module — this step only calls
  tier 2/3's already-built public port.

**Out of scope, deliberately:** any change to `internal/analytics` or `internal/charging` — both
already built and archived everything this tier calls. Any gateway surface — MAG-87 (the read/UI
side) is explicitly out of this roadmap's scope.

## Breaking?

**NO.** `Processor`'s single method, `ProcessVehicleData`, keeps its exact signature and return
type. `NewProcessor` gains one parameter — every existing call site (`cmd/poller/main.go`, this
change's own updated call, and every test's `newTestProcessor` helper) is updated in the same
change, so nothing outside this change can observe a half-migrated signature. No exported type is
removed or renamed.

## Modules affected

- **`app`** — owner. New step, new pure function, new private step functions, the constructor's
  new parameter, tests, `AGENTS.md`, package doc comment.
- **`cmd/poller`** — one new argument at one call site (wiring only, no new logic — `cmd/` stays
  thin, `CLAUDE.md` §Non-negotiables). **Leader-owned** — `cmd/` is outside this worker's sandbox.
- **`analytics`** — **not affected by this tier.** Tiers 2 and 3 already built and archived
  `MonthlySyncer`; this tier only calls it.
- **`charging`** — **not affected by this tier.** Tier 1 already built and archived
  `MonthlyCapacityReader`; `cmd/poller` only constructs it.
- **`gateway`**, **`telemetry`**, **`account`** — **not affected.** None of their ports, types, or
  imports change.

## Read paths affected

Per `openspec/config.yaml` §proposal (any change touching the database must name the read paths).
**This change touches no database directly** — it adds no query, table, column, or index of its
own. The one database effect is indirect: every night, `analytics.MonthlySyncer.SyncMonth` runs
twice per vehicle (already justified in tier 2's design.md as a nightly-batch write with no
read-latency budget to protect, per the project's read-heavy `Performance-Profile`). This tier
changes neither that query shape nor its frequency claim within a single call — it only decides how
many times and for which periods the call happens each night.

## Impact

- **Affected spec:** `process-vehicle-data` (existing capability, from `RM29-app-add-process-
  vehicle-data`, `RM36-app-record-poll-run`, `RM52-app-add-monthly-capacity-step`) — one
  requirement **MODIFIED** (four steps become five), one requirement **MODIFIED** (the
  short-circuit covers the fifth step too), one requirement **ADDED** (the every-night,
  two-period sync).
- **Affected code:** `internal/app/app.go`, `internal/app/processor.go`,
  `internal/app/processor_test.go` (changed), `internal/app/monthly_metrics_step_test.go` (new),
  `internal/app/AGENTS.md`, `cmd/poller/main.go`, `kkpa/context/architecture/nightly-cycle.md`,
  root `README.md`.
- **Design gate: not tripped.** No database object of any kind is added or changed by this tier.
- **`MIGRATIONS_DIRS` order check:** not applicable — this tier adds no migration.
- **Deferred, explicitly NOT in scope:** any gateway/read surface for the new table (MAG-87,
  a separate future ticket).

## Modules affected — summary table

| Module | Change |
|---|---|
| `app` | Owner. New step, new functions, constructor parameter, tests, docs. |
| `cmd/poller` | One new argument at one call site. Leader-owned. |
| `analytics` | None in this tier — tiers 2/3 already built the port this tier calls. |
| `charging` | None in this tier — tier 1 already built the port `cmd/poller` constructs. |
| `gateway` | None, ever, for this roadmap. |
