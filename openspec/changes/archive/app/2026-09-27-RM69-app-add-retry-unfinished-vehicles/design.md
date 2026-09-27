# Design — RM69-app-add-retry-unfinished-vehicles

## Overview

Tier 1 gave `internal/telemetry` a way to run step 1 (snapshot + Supercharger
history) for a chosen set of vehicles. This tier builds the caller: a second
schedule, next to the nightly one, that finds vehicles still not done for
yesterday and runs the full five-step cycle again, scoped to those vehicles.

Two things must never drift from the nightly path: the five-step order and
short-circuit rule, and every existing per-step behavior. So this design
shares code between `ProcessVehicleData` and the new subset method, the same
way tier 1 shared `enumerateElected` between `CollectAll` and `CollectVehicles`
rather than writing two similar-looking cycles that slowly diverge.

## Blocker — a dependency this tier cannot resolve on its own

**Read first.** This is the one part of the roadmap's own tier prompt that
explicitly anticipated needing a decision here: *"Read the done state only
through the analytics public port (`LatestMetricsForVehicles` or a narrow
sibling in analytics — decide in design.md; a new analytics method is a
separate module change and must be raised, not written)."*

### Why `analytics.Reader.LatestMetricsForVehicles` does not fit

Its signature is `LatestMetricsForVehicles(ctx, refs []vehicleref.Ref)
([]VehicleStatus, error)`. A `vehicleref.Ref` can only be built by
`vehicleref.Authorize`, `vehicleref.All`, or a `_test.go` file — and
`make vehicleref-guard` fails the build if `vehicleref.Authorize` or
`vehicleref.All` is called anywhere in `internal/` outside `internal/vehicleref`
itself, a `_test.go` file, or the gateway's `authorizeVehicle` helper.
`internal/app` is none of those three.

This is not only a guard technicality. `vehicleref.Ref`'s own contract is "this
id was checked against **the requesting account's** own ownership list" — it
answers a per-user, per-HTTP-request question. The retry schedule has no
requesting account: it walks every registered vehicle across every account,
exactly like `account.AllRegisteredVehicles`, `electPollingVehicles`, and
`groupByAccount` already do in plain `int64` for the same reason. Tier 1's own
design.md (D2) made exactly this call for `CollectVehicles`: *"A `Ref` would
have nothing to authorize against here: there is no 'this account's owned
list' to check the id against."* The same reasoning applies one level up, in
the caller this tier adds.

### Why looping a single-vehicle `Reader` method instead is also rejected

Two of `Reader`'s other three methods, `ConsumedByDay` and
`OdometerDeltaByDay`, take a plain `teslaID int64` — no `vehicleref.Ref`. Both
are unsuitable for a different reason: both filter out a day whose row has no
computable predecessor (the `IS NOT NULL` filter their own doc comments
describe). A vehicle's first tracked day, or any day right after a capture
gap, would show as "not done" even though its `vehicle_metrics` row exists —
a false retry, contradicting roadmap D1's plain definition of "done" (a row
exists with `metric_date = local today − 1`, full stop).

The fourth method, `BatteryLevelByDay`, has no such filter and would answer
the question correctly — a non-empty result for `(teslaID, day, day)` means a
`vehicle_metrics` row exists for that exact day. But it is scoped to one
vehicle, so using it would mean one query per **registered** vehicle, every 30
minutes, from a caller — `internal/app`'s retry schedule — that has no
account to scope the loop by. `ai/go-conventions.md`'s read-optimization rule
is explicit and project-wide, not telemetry-specific: *"Write batch reads at
the module interface level ... never per-entity helpers ... that the caller
must loop over."* It would also overload a port whose own doc ties it to the
gateway's battery chart, coupling an unrelated internal check to a page's
contract.

### Conclusion

A new, narrow, batch-shaped analytics read is needed — the "narrow sibling"
the roadmap named as the second option. Proposed shape, for the leader's or
`internal/analytics`'s own worker's reference (**not designed or implemented
in this change**):

```go
// Somewhere in internal/analytics, exact placement and name are that
// module's own decision:
UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error)
```

Plain `[]int64` in and out, mirroring `telemetry.Collector.CollectVehicles`'s
own plain-`int64` convention for the identical reason: no single requesting
account exists to build a `vehicleref.Ref` against. It reads
`vehicle_metrics` for `teslaIDs` where `metric_date = date`, and returns the
`teslaIDs` with no matching row — an indexed, single-query batch read, cheap
enough to run every 30 minutes.

**This tier's own design below assumes this capability exists**, reached
through a small consumer-side interface `internal/app` declares for itself
(D3) — the architecture's own prescribed pattern for exactly this situation
(`ai/architecture.md` §"Dependency direction and import cycles": *"Declare
the interface in the package that needs it, and wire the concrete type in at
`cmd/` startup"*). Whoever implements the real analytics method never has to
know `internal/app`'s interface exists; `cmd/poller`'s composition root wires
a small adapter between the two.

**One alternative was considered and rejected**: `make vehicleref-guard`'s own
grep pattern only scans `internal/`, so `cmd/poller/main.go` (outside
`internal/`) could technically call `vehicleref.All` and
`LatestMetricsForVehicles` itself, bypassing the guard by file location
alone. Rejected for the same semantic reason as above, not the guard: there is
still no single requesting account at `cmd/poller`'s composition root, so a
`Ref` built there would carry no real ownership proof — it would defeat the
whole point of the package, guard or no guard.

**Until the new analytics capability exists, task group `T-analytics-dep` in
`tasks.md` is a hard blocker on the retry-selection tasks.** Every other task
in this tier can be implemented and tested independently of it.

## Decisions

### D1 — One new `Processor` method, sharing code with `ProcessVehicleData`

```go
ProcessVehicleDataForVehicles(ctx context.Context, triggeredBy telemetry.TriggeredBy, teslaIDs []int64) (telemetry.CycleReport, error)
```

joins `ProcessVehicleData` on `Processor`. Not a second port: both express the
same "run the cycle" concept, and a second port would force every caller that
might need either shape to hold two interfaces (mirrors tier 1's D1 for
`CollectVehicles`/`Collector`). It takes `triggeredBy` generically, the same
as `ProcessVehicleData` — it does not itself require or inspect
`telemetry.TriggeredByRetry`; the retry scheduler is expected to pass it, the
same way tier 1's `CollectVehicles` does not itself require
`TriggeredByRetry`.

### D2 — A shared filtering helper replaces three separate inline enumerations

Today `processChargingData`, `recalculateAnalytics`, and `runMonthlyMetricsStep`
each call `p.acct.AllRegisteredVehicles(ctx)` and dedupe by `TeslaID`
themselves, with three slightly different result shapes. This design extracts
one private helper:

```go
// vehiclesToProcess returns the distinct-by-TeslaID list of vehicles this
// invocation processes, in the order AllRegisteredVehicles returns them.
// filter is nil for a full nightly cycle (every registered vehicle); for a
// subset cycle it is the caller-supplied tesla_id set, and a registered
// vehicle whose TeslaID is not in filter is excluded. One function, called by
// every step of both ProcessVehicleData and ProcessVehicleDataForVehicles, so
// a future change to the dedup or filter rule can never apply to one path
// and not the other — the same reason tier 1 shared enumerateElected between
// CollectAll and CollectVehicles.
func (p *processor) vehiclesToProcess(ctx context.Context, filter []int64) ([]account.OwnedVehicle, error)
```

`processChargingData`, `recalculateAnalytics`, and `runMonthlyMetricsStep` are
rewritten to take their vehicle list as a parameter instead of fetching it
themselves, with `ProcessVehicleData` calling `vehiclesToProcess(ctx, nil)`
and `ProcessVehicleDataForVehicles` calling
`vehiclesToProcess(ctx, teslaIDs)`. This is a behavior-preserving refactor for
`ProcessVehicleData` — `nil` means no filtering, the identical result the
three inline versions already produced. Every existing test for these three
functions is expected to pass unmodified; if one needs an edit, that is a
signal the refactor changed nightly behavior, and the task should stop and
report it rather than "fixing" the test (mirrors tier 1's TR-5 regression
rule).

A caller that only needs `[]int64` (steps 2 and 5) derives it from the
returned `[]account.OwnedVehicle` with a one-line loop, exactly as those steps
already do today — this design does not add a second shared shape.

### D3 — `NotDoneVehicles`: a consumer-side interface declared in `internal/app`

```go
// NotDoneVehicles answers "which of these registered vehicles have not yet
// finished producing their metrics for date" — the retry schedule's only way
// to ask that question. Declared here, not in internal/analytics: this
// module has no single requesting account to build a vehicleref.Ref against
// (see design.md's Blocker section), so it asks for exactly the narrow shape
// it needs and lets cmd/poller wire in whatever implements it.
type NotDoneVehicles interface {
	NotDone(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error)
}
```

This is the architecture's own prescribed shape for a cross-module need that
does not fit the provider's existing port (`ai/architecture.md` §"Dependency
direction and import cycles"). `internal/app` adds no new import path for
this — the interface is declared and consumed entirely inside this module;
only `cmd/poller`'s composition root needs to know what concrete type
satisfies it.

### D4 — `RetryScheduler`: a second, independent schedule

```go
type RetryScheduler struct {
	processor Processor
	notDone   NotDoneVehicles
	acct      account.Service
	loc       *time.Location
	now       func() time.Time
}

func NewRetryScheduler(processor Processor, notDone NotDoneVehicles, acct account.Service, loc *time.Location, cfg telemetry.Config) *RetryScheduler
```

Mirrors `Scheduler`/`NewScheduler`'s own shape exactly: a nil `loc` falls back
to `clock.Zone()`, `cfg.Clock` is the same test seam `NewScheduler` already
uses. `RetryScheduler` holds `acct` directly (the same port `Processor`
already composes over) rather than asking `Processor` for the registered
vehicle list — `Processor` exposes no such method, and adding one only for
this caller would widen the port for a need internal to this same module.

`RetryScheduler.Run` blocks like `Scheduler.Run`, using `nextRetryTick`
instead of `nextRun`. On each tick:

1. Fetch every registered vehicle, deduplicated by `TeslaID` (same shape
   `vehiclesToProcess(ctx, nil)` produces — reused directly rather than
   duplicated, see D2).
2. Compute `cutoff := clock.CalendarDay(s.now(), s.loc).AddDate(0, 0, -1)` —
   "yesterday" in the retry schedule's own configured zone, the identical
   `internal/clock` pattern `recalculateAnalytics` already uses for the same
   question (roadmap D6/D18's "the poller's own zone, not UTC").
3. Call `s.notDone.NotDone(ctx, allTeslaIDs, cutoff)`.
4. **If the result is empty, call nothing and log nothing at the cycle level**
   (roadmap D8) — the tick is a no-op. A per-tick debug line MAY note "every
   vehicle done", but no `Processor` call happens and no `poll_runs` row is
   written.
5. Otherwise call `s.processor.ProcessVehicleDataForVehicles(ctx,
   telemetry.TriggeredByRetry, notDoneIDs)` and log the outcome with
   `telemetry.LogCycle`, exactly as `Scheduler.Run` already does for the
   nightly tick.

A `NotDone` error is logged and the tick is skipped, the same "log, don't
crash the schedule" rule `Scheduler.Run` already applies to a `Processor`
error — a bad tick must never stop the next one.

### D5 — `nextRetryTick`: pure, mirrors `nextRun`

```go
const (
	retryInterval        = 30 * time.Minute
	retryWindowStartHour = 4 // 04:00 — 30 minutes after the default nightly
	                          // hour, so the nightly run has room to start
	                          // before the first retry tick could ever fire.
)

func nextRetryTick(now time.Time, loc *time.Location) time.Time
```

Same purity contract as `nextRun` (no clock, no sleeping) — the schedule math
stays unit-testable in isolation. Returns the next tick **strictly after**
`now`, at a 30-minute cadence anchored to `retryWindowStartHour:00`, computed
in `loc`. `now` before 04:00 rolls forward to today's 04:00. A `now` at or
after the last tick that fits before midnight (23:30) rolls forward to
**tomorrow's** 04:00, never to a tick at or after midnight — this is the
"until the end of the local day" half of roadmap D5, expressed the same way
`nextRun` already expresses "once a day": by always returning the true next
occurrence, with the caller's `select`/timer loop the only thing that decides
when it fires.

### D6 — The lock is `cmd/poller`'s job, not this module's

Roadmap D7 ("one lock for every cycle") is satisfied by `cmd/poller` widening
its existing `guardedProcessor` (in `rerun.go`) to implement
`ProcessVehicleDataForVehicles` with the same `TryLock`/`errCycleBusy` shape
it already gives `ProcessVehicleData`, and wiring `RetryScheduler` with that
same `guarded` value it already hands the nightly `Scheduler` and the rerun
API. `internal/app` needs no lock of its own — `RetryScheduler` calls
whatever `Processor` it is given, exactly like `Scheduler` already does, and
serialization is a property of which concrete `Processor` the composition
root hands it. This is the `T-cmd-poller` task group in `tasks.md`,
leader-owned.

### D7 — An empty retry set is the scheduler's job to catch, not the Processor's

`ProcessVehicleDataForVehicles` does **not** special-case an empty `teslaIDs`
the way tier 1's `CollectVehicles` defensively no-ops on an empty input. Every
member of the `ProcessVehicleData*` family records exactly one `poll_runs` row
on every exit path — that is the existing, well-established contract (`spec
process-vehicle-data` — "Every Cycle Records A Poll Run Summary"), and adding
a second exception to it here, on top of the one step-1 failure already has,
would make that invariant harder to reason about for no benefit: roadmap D8's
"writes nothing" guarantee is instead enforced once, in `RetryScheduler`,
which simply never makes the call when `NotDone` returns an empty set (D4
step 4). One mechanism, one place, matching the roadmap's own framing of the
guarantee as the **scheduler's** behavior, not the cycle's.

### D8 — Step 4 (monthly capacity), scoped to the retry set, per roadmap D3

`callMonthlyCapacityCalculator` gains a `teslaID *int64` parameter (it always
hardcodes `nil` today):

```go
func (p *processor) callMonthlyCapacityCalculator(ctx context.Context, period time.Time, teslaID *int64)
```

`runMonthlyCapacityStep` (nightly) passes `nil`, unchanged. A new
`runMonthlyCapacityStepForVehicles(ctx, teslaIDs []int64)` reuses the same
pure `monthlyCapacityPeriod` gate, and — only when it returns `run = true` —
loops once per id in `teslaIDs`, calling
`callMonthlyCapacityCalculator(ctx, period, &id)`. This is the same
single-vehicle scoping `charging.MonthlyCapacityCalculator.Calculate` already
supports for `cmd/monthly-capacity -tesla-id`; looping here is bounded by the
retry set's size (typically zero or one), not the whole fleet, so it does not
reintroduce the "re-pool every vehicle's rows N times" cost the KB warns
against for the nightly path.

**Worth recording, not worth re-opening**: step 4 measures the **previous**
calendar month from data that has nothing to do with tonight's snapshot, so a
vehicle retried only because tonight's step 1 failed gains nothing from
step 4 running again for it — the nightly pass already measured that vehicle
(whole-fleet, `teslaID = nil`) regardless of whether its own step 1 succeeded.
Implemented anyway, exactly as roadmap D3 states ("every step is limited to
the retried cars"), because `Calculate` upserts and is idempotent — a repeat
call is harmless, just a few redundant rows written on the rare day this
fires (the first of the month) for the rare vehicle still not done four hours
later.

### D9 — Step 5 (monthly metrics) needs no signature change

`callMonthlySyncer(ctx, teslaID int64, period time.Time)` is already scoped to
one vehicle — `runMonthlyMetricsStep` already loops it over every registered
vehicle today. Scoping to the retry set is exactly D2's refactor: the loop's
input list shrinks from `vehiclesToProcess(ctx, nil)` to
`vehiclesToProcess(ctx, teslaIDs)`; the loop body is untouched.

### D10 — No new import path in `internal/app`

`internal/app` already imports `internal/analytics`, `internal/account`,
`internal/telemetry`, `internal/charging`, `internal/clock`, `internal/logging`
(see `internal/app/AGENTS.md` §"Allowed/forbidden imports"). This tier adds no
new import path — `NotDoneVehicles` (D3) is declared and consumed entirely
inside this module; the concrete type that satisfies it lives at
`cmd/poller`'s composition root, which already imports every module it needs.

## Schema

**No database object changes in this tier.** `internal/app` owns no table, no
migration, no pool (unchanged — see `internal/app/AGENTS.md` §"Data
ownership"). The blocked dependency's own schema question (an index over
`vehicle_metrics(tesla_id, metric_date)` for the new analytics read) belongs
to whichever change implements it, not this one — that table already exists,
and a covering index for it, if one is needed, is `internal/analytics`'s own
call to make and justify against its own read patterns.

## Makefile, guards, and sqlc — checked, unaffected

No new migration, no new module, no new `sqlc.yaml` entry, no new `make`
target. `internal/app` still has no `db/` directory. The existing
`vehicleref-guard` is the guard this design is built around (see Blocker); no
change to the guard itself is proposed or needed. `naming-guard`: 
`ProcessVehicleDataForVehicles`, `RetryScheduler`, `NotDoneVehicles`, and
`nextRetryTick` each carry a domain word and are not on the banned-suffix
list.

## Docs this change invalidates

- `internal/app/AGENTS.md` — "Responsibility" (the five-step diagram gains a
  "subset cycle" callout), "Public interface" (the new `Processor` method and
  `RetryScheduler`), "Testing notes" (new covered/accepted-gap rows for the
  new functions). Task in `tasks.md`.
- `cmd/README.md` — the `cmd/poller` row's description of what the binary
  starts.
- Root `README.md` — the `internal/app` "Architecture" table row, and any
  prose describing the nightly cycle as the poller's only schedule.
- `kkpa/context/architecture/nightly-cycle.md` — the "Component map" gains
  `RetryScheduler`/`nextRetryTick`/`ProcessVehicleDataForVehicles`; the "Port
  map" gains a row once the `NotDoneVehicles` adapter exists; "How maintenance
  works" gains a note on the shared `vehiclesToProcess` helper.

None of these are edited until the corresponding task lands; `tasks.md`
tracks each one so it is not silently dropped.

## Test contract — expected values, authored before the implementation

All tests here are offline (pure Go / fakes), no database — `internal/app`
has no DB-backed tests by its own standing convention (`internal/app/AGENTS.md`
§"Testing notes": *"If this module ever gains a DB-backed test, treat that as
a signal that something is being added here which does not belong."*).
Roadmap D11's "DB integration in the final wave" is satisfied at the roadmap
level by tier 1's telemetry DB tests (already archived) and, once it exists,
by whichever change implements the Blocker's new analytics method — neither
is this tier's own test file.

### `nextRetryTick` (new `retry_scheduler_test.go`, or alongside `scheduler_test.go`)

- **TR-1**: `nextRetryTick(03:10, loc)` → today `04:00`.
- **TR-2**: `nextRetryTick(04:00 exactly, loc)` → today `04:30` (strictly
  after `now`, same rule as `nextRun`).
- **TR-3**: `nextRetryTick(23:40, loc)` → tomorrow `04:00` (no tick left
  today).
- **TR-4**: `nextRetryTick(23:30 exactly, loc)` → tomorrow `04:00` (23:30 is
  itself the last in-window tick; the next one is strictly after it, and
  00:00 is out of window).
- **TR-5**: same wall-clock inputs computed in two different `*time.Location`
  values with different UTC offsets produce ticks that differ by exactly that
  offset when converted to UTC — proves the window is evaluated in `loc`, not
  UTC (mirrors `nextRun`'s own DST/non-local-zone test).

### `RetryScheduler.Run`, one tick (fakes: `Processor`, `NotDoneVehicles`, `account.Service`)

- **TR-6**: `NotDone` returns an empty slice → the fake `Processor` is never
  called (fails the test via `t.Fatal` if it is) — proves roadmap D8's
  "writes nothing" half.
- **TR-7**: `NotDone` returns `{7}` out of a registered set of `{5, 7, 9}` →
  the fake `Processor.ProcessVehicleDataForVehicles` is called exactly once,
  with `teslaIDs == []int64{7}` and `triggeredBy == telemetry.TriggeredByRetry`.
- **TR-8**: `NotDone` returns an error → the fake `Processor` is never called,
  the tick is skipped, and a second tick (simulated) still runs normally —
  proves one bad tick never stops the schedule, mirroring `Scheduler.Run`'s
  existing `Processor`-error handling.

### `vehiclesToProcess` (extends `processor_test.go`)

- **TR-9**: `filter == nil` returns the identical distinct-by-`TeslaID` list
  the three existing inline implementations produce today, over the same
  fixture data those functions' current tests already use — this is the
  regression check for D2's refactor.
- **TR-10**: `filter == []int64{7}` over a registered set of `{5, 7, 9}`
  returns only vehicle 7's `account.OwnedVehicle` entry.
- **TR-11**: `filter` containing an id absent from the registered set is
  silently excluded from the result — no error, no entry (mirrors tier 1's
  TR-2 for `CollectVehicles`).

### `ProcessVehicleDataForVehicles` (extends `processor_test.go`)

- **TR-12**: calls `p.collector.CollectVehicles(ctx, run, teslaIDs)` — never
  `CollectAll` — with the exact `teslaIDs` slice passed in.
- **TR-13**: a step-1 error (fake `CollectVehicles` returns non-nil) skips
  steps 2–5, exactly like `ProcessVehicleData`'s own step-1 short-circuit —
  fakes for steps 2–5 fail the test via `t.Fatal` if called.
- **TR-14**: steps 2, 3, and 5's fakes each receive exactly the filtered
  vehicle list `vehiclesToProcess(ctx, teslaIDs)` would produce for the given
  fixture — not the full registered set.
- **TR-15**: `recordRun` is called exactly once regardless of outcome, with
  `run.TriggeredBy` equal to whatever was passed to
  `ProcessVehicleDataForVehicles` (the caller's choice, not a hardcoded
  `TriggeredByRetry` — D1).
- **TR-16 (regression)**: every existing `ProcessVehicleData` test in
  `processor_test.go` passes unmodified after D2's refactor — the proof that
  `vehiclesToProcess(ctx, nil)` reproduces the three inline implementations it
  replaces (same rule as tier 1's TR-5).

### Step 4 for a vehicle subset

- **TR-17**: on a day `monthlyCapacityPeriod` gates to `run = false`,
  `runMonthlyCapacityStepForVehicles` calls the fake
  `MonthlyCapacityCalculator` zero times, for any non-empty `teslaIDs`.
- **TR-18**: on the first of the month, `runMonthlyCapacityStepForVehicles(ctx,
  []int64{5, 7})` calls the fake calculator exactly twice, once with
  `teslaID == &5` and once with `teslaID == &7`, both for the same computed
  previous-month `period`.

## Risks

- **The Blocker is the load-bearing risk of this whole tier.** Every task that
  is not explicitly in `T-analytics-dep`'s dependency chain in `tasks.md` can
  proceed without it; the retry-selection wiring itself cannot compile or be
  meaningfully tested until the new analytics capability exists somewhere for
  `cmd/poller` to adapt into `NotDoneVehicles`.
- **D2's refactor touches three existing, tested functions at once.** TR-9 and
  TR-16 exist specifically so a regression here — a changed dedup order, a
  dropped vehicle, a changed VIN — fails loudly rather than silently changing
  nightly behavior for every vehicle, not just retried ones.
- **`callMonthlyCapacityCalculator`'s new `*int64` parameter is easy to leave
  `nil` by mistake in the subset path**, which would silently turn a scoped
  retry into a second whole-fleet capacity measurement on the first of the
  month. TR-18 is written specifically so this fails visibly (wrong call
  count, wrong `teslaID` values) rather than passing by coincidence.
