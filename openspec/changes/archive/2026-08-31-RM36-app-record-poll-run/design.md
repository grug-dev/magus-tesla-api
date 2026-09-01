# Design — RM36-app-record-poll-run

## Context

`openspec/roadmaps/RM36-poll-run-tracking.md` settles seven decisions (D1–D7) with
the user before either tier's artifacts existed. Tier 1 (`RM36-telemetry-add-poll-runs`,
archived) built everything this tier calls: the `poll_runs` table, `telemetry.PollRun`,
`telemetry.RunWriter`/`NewRunWriter`, and the `CycleReport.Duration` field (tier 1
design D6, left zero by `CollectAll`, documented as "populated only by the caller").
This design does not re-litigate any of that; it specifies the one thing tier 1 could
not build itself — the caller that measures a whole run and calls `RecordRun`.

Two constraints shape every decision below:

1. **This tier is sandboxed to `internal/app`, plus `cmd/poller` as a granted path.**
   `internal/telemetry`, `internal/tesla`, and every other module are unchanged —
   every port this tier needs (`telemetry.RunWriter`, `telemetry.PollRun`,
   `CycleReport.Duration`) already exists.
2. **`internal/app` owns no data.** No `*pgxpool.Pool` parameter is added anywhere in
   this module, ever (`internal/app/AGENTS.md` "Data ownership"). This tier calls
   `telemetry.RunWriter` exactly as the module already calls `charging.SessionWriter`
   and `analytics.GapWriter` — a port, not a database.

## Goals / Non-Goals

**Goals:** every `ProcessVehicleData` invocation records exactly one `poll_runs` row,
including the step-1 whole-cycle-failure path; the run's true start-to-finish
duration is measured and surfaces on the returned `CycleReport` so `LogCycle`'s
existing duration field (tier 1) prints a real number; a `RecordRun` failure never
changes `ProcessVehicleData`'s own returned outcome; `internal/clock` is used for
every new timestamp this tier reads, never a raw `time.Now()`.

**Non-goals:** anything inside `internal/telemetry` (the counting decorator, the
account counters, `poll_runs`' schema — all tier 1, unchanged); migrating this
module's **pre-existing** `time.Now()` call sites (`processor.go`'s
`recalculateAnalytics` and `scheduler.go`'s nil-location fallback) to `internal/clock`
— that is the separate, already-roadmapped `RM35-app-adopt-clock` tier, and pulling
it into this change would silently expand scope past MAG-35's own ask; a gateway read
surface for `poll_runs` (backlog, tier 1's own "Out of scope"); per-vehicle duration
(roadmap D3, backlog).

## Decisions

### D1 — `NewProcessor` gains `runWriter telemetry.RunWriter`, placed after `superchargerReader`

```go
func NewProcessor(
	collector telemetry.Collector,
	superchargerReader telemetry.SuperchargerReader,
	runWriter telemetry.RunWriter,
	sessionWriter charging.SessionWriter,
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	loc *time.Location,
) Processor
```

Grouped with the module's other two `telemetry`-owned ports (`collector`,
`superchargerReader`) rather than appended at the end: a reader skimming the
parameter list groups by owning module already (telemetry, then charging, then
account, then analytics, then the bare `loc`), and `runWriter` is telemetry's third
port here. `cmd/poller/main.go` is this constructor's only call site in the whole
repository (verified — `grep -rn "NewProcessor("` returns exactly two lines: the
declaration and this one call), so reordering costs one file, already a granted path.

Rejected — appending `runWriter` as the last parameter before `loc`: marginally
smaller diff at the call site (no need to shift `sessionWriter`'s position down),
but breaks the existing module-grouping the parameter list already has, making the
list harder to skim for the next reader — an AI-efficiency cost (a small, closed
vocabulary — "read top to bottom, ports grouped by owner" — beats optimizing one
diff at the expense of that pattern).

### D2 — New timestamps in this tier come from `internal/clock.Now()`, never `time.Now()`; no injectable clock seam is added to `Processor`

`ProcessVehicleData` measures `start := clock.Now()` before step 1 and
`finish := clock.Now()` at the single point (D4 below) that covers every exit path.

This project's non-negotiable is unconditional: "never a raw `time.Now()`... obtained
only through `internal/clock`" (`ai/go-conventions.md` §Coding Rules, restated in
`CLAUDE.md` §Non-negotiables — binding "even if you haven't opened the conventions
file"). `internal/app` does have two **pre-existing** call sites that still read raw
`time.Now()` (`processor.go`'s `recalculateAnalytics`, and `scheduler.go`'s
`NewScheduler`'s `nil loc` fallback to `time.Local`) — but those are exactly the two
line items `RM35-app-adopt-clock` (a sibling roadmap tier, currently `[ ]` pending in
`openspec/roadmaps/RM35-timezone-centralization.md`) already exists to migrate. Their
being not-yet-migrated is that tier's accepted, temporary gap — the go-conventions
doc says so explicitly ("This becomes enforceable repo-wide once
`RM35-timezone-centralization` tiers 2–6 adopt `clock` at every current call site").
It licenses *leaving an old violation in place until its own tier*, not *adding a
new one*: a fresh call site this tier introduces has no such grandfathering, and
`internal/clock` costs nothing to reach for (stdlib-only, zero cycle risk — D3
below), so there is no reason to write a second call this roadmap will have to
migrate later.

**No clock seam is added to `NewProcessor`/`processor`,** unlike `Scheduler`'s own
`now func() time.Time` field (`cfg.Clock`, `scheduler.go`). That seam exists to make
`Scheduler`'s *schedule-tick* math (`nextRun`) unit-testable without waiting for a
real day to pass — a genuinely different problem (comparing a computed time against
a fixed target) from this tier's problem (measuring an elapsed duration around a
call). The Test Contract below (D8/D9) needs only that `FinishedAt.Sub(StartedAt)`
equals the reported duration and both timestamps fall inside a wall-clock bracket the
test itself takes before and after calling `ProcessVehicleData` — real `clock.Now()`
already satisfies that without any injection. Adding a seam here for a test that does
not need one is exactly the over-abstraction `CLAUDE.md`'s AI-efficiency principle
warns against ("indirection itself costs tokens to resolve... add a wrapper only
where it buys change-locality on a volatile or repeated surface").

Rejected — reusing `Scheduler`'s `cfg telemetry.Config`/`Clock` seam inside
`Processor` too: `Config.Clock` is a **Scheduler-only** seam by design (tier 1 of
RM29's own D13, carried into this module's AGENTS.md: "kept... for its `Clock`
field... the test seam" — scoped explicitly to `Scheduler`). Threading it into
`Processor` as well would give `NewProcessor` a tenth parameter for a problem its
Test Contract does not have.

### D3 — `internal/clock` is added to `internal/app/AGENTS.md`'s allowed-imports list

`internal/clock` imports stdlib `time` and nothing else (`internal/clock/AGENTS.md`
"The one import rule"), so importing it from `internal/app` creates no cycle risk —
the same reasoning every other allowed import in this module's AGENTS.md already
states for itself. This is a one-line addition to the "May import" bullet list,
implemented as part of the AGENTS.md task in tasks.md Wave 3 (doc tasks), not a
separate change.

### D4 — `ProcessVehicleData` is refactored to one measurement/record point covering every exit path, by turning the early-return short-circuit into a guarded fall-through

**Current shape** (pre-this-tier):

```go
func (p *processor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	run := telemetry.RunContext{RunID: uuid.New(), TriggeredBy: triggeredBy}

	report, err := p.collector.CollectAll(ctx, run) // step 1
	if err != nil {
		return report, err // early return — steps 2/3 skipped
	}

	p.processChargingData(ctx)  // step 2
	p.recalculateAnalytics(ctx) // step 3

	return report, nil
}
```

**New shape:**

```go
func (p *processor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	run := telemetry.RunContext{RunID: uuid.New(), TriggeredBy: triggeredBy}
	start := clock.Now()

	report, err := p.collector.CollectAll(ctx, run) // step 1
	if err == nil {
		p.processChargingData(ctx)  // step 2
		p.recalculateAnalytics(ctx) // step 3
	}

	finish := clock.Now()
	report.Duration = finish.Sub(start)
	p.recordRun(ctx, run, report, start, finish)

	return report, err
}
```

The `if err != nil { return ... }` early return becomes `if err == nil { step2; step3 }`
— **identical short-circuit behavior** (steps 2 and 3 run if and only if step 1
succeeded; the roadmap's own tier-2 scope line and `internal/app/AGENTS.md`'s
documented short-circuit are both preserved verbatim, this is a control-flow
reshaping, not a behavior change to the three-step contract), but now both paths fall
through to one `finish`/`report.Duration`/`recordRun`/`return` tail instead of each
having its own. This is what makes "start before step 1, end after step 3 —
including on the step-1 whole-cycle-failure path" (roadmap's tier-2 scope line)
correct by construction rather than by two call sites staying in sync by hand.

Rejected — recording separately at both the early-return point and the final return
(two `recordRun` calls, one per path): the project already has a named pattern for
exactly this failure mode — roadmap D6's "both entry points are covered by
construction, not by duplicated code" (about `cmd/poller`'s nightly vs. `--once`
paths, solved by both calling one `ProcessVehicleData`). The identical argument
applies one level down: two call sites for one concern is a future edit that can
touch one and silently miss the other. Rejected — a `defer` closure capturing `report`
by reference: Go's defer evaluates its call's *arguments* at defer-time, not at
return-time, so a naive `defer p.recordRun(ctx, run, report, start, ...)` would
capture `report`'s zero-value snapshot from before `CollectAll` even runs; making
that correct needs a closure with its own named-return trickery, which is strictly
more indirection than the guarded fall-through above for the same result — rejected
on the same over-abstraction grounds as D2's rejected clock seam.

### D5 — A `RecordRun` failure is logged, never propagated; `ProcessVehicleData`'s returned `(report, err)` is exactly what step 1 (and, if it ran, steps 2/3) produced

```go
func (p *processor) recordRun(ctx context.Context, run telemetry.RunContext, report telemetry.CycleReport, start, finish time.Time) {
	if err := p.runWriter.RecordRun(ctx, buildPollRun(run, report, start, finish)); err != nil {
		log.Printf("poll run: recording run %s: %v", run.RunID, err)
	}
}
```

`recordRun` returns nothing and cannot influence `ProcessVehicleData`'s own return
values — the compiler enforces this, not just convention. This mirrors the exact
"errors are logged, never fatal" pattern this file already documents for
`processChargingData`/`recalculateAnalytics` (see their own doc comments), extended
to the new recording step for the same reason: a `RecordRun` hiccup is an
observability-write failure, not a cycle failure, and `cmd/poller`'s `--once` path
treats a non-nil `ProcessVehicleData` error as fatal (`log.Fatalf` → exit 1) — a
`RecordRun` error must never take the whole process down over a cycle that may have
fully succeeded.

Rejected — wrapping/aggregating `RecordRun`'s error into the returned `err`
(`errors.Join` or similar): this would report a purely observational write failure
as a whole-cycle failure to every consumer of `ProcessVehicleData`'s return value —
`cmd/poller --once`'s exit code, `Scheduler.Run`'s "is tonight's cycle logged as
failed" line — for a cycle whose actual three steps may have succeeded completely.
Rejected — retrying `RecordRun` before giving up: no retry policy exists anywhere
else in this file for a post-cycle write (the charging-mirror and gap-reconciliation
steps do not retry either), and a duplicate `RecordRun` call would collide with tier
1's own no-upsert design (D11 of tier 1's design.md — a second call for the same
`run_id` fails loudly on the primary key by design), so a naive retry-on-any-error
would misfire against tier 1's own duplicate-detection contract.

### D6 — `report.Duration` is set unconditionally, on the same `CycleReport` value returned to the caller, before `recordRun` and before the function returns

```go
finish := clock.Now()
report.Duration = finish.Sub(start)
p.recordRun(ctx, run, report, start, finish)
return report, err
```

This is tier 1 design D6's own contract, executed by this tier: "Tier 2 sets
`report.Duration = end.Sub(start)` on the same `CycleReport` value it already holds,
then calls `telemetry.LogCycle(report, err)` exactly as today." Both of
`ProcessVehicleData`'s callers (`cmd/poller/main.go`'s `--once` branch,
`Scheduler.Run`'s tick handler) already call `telemetry.LogCycle(report, err)` on the
value this method returns — neither needs to change, and `LogCycle`'s printed
`duration=%s` field (tier 1) goes from always printing `0s` to printing the real
measured duration the moment this tier ships, with zero edits to
`internal/telemetry`.

### D7 — `buildPollRun` is a pure, dependency-free function; the field mapping is by-field, not by-name, mirroring tier 1's own D8 correction

```go
// buildPollRun maps one measured ProcessVehicleData invocation onto the
// telemetry.PollRun tier 1's RunWriter.RecordRun persists. Pure: no I/O, no
// clock read of its own — start/finish are supplied by the caller so this
// function is directly unit-testable without any of Processor's collaborator
// ports (design D9, Test Contract fixtures P1/P2).
func buildPollRun(run telemetry.RunContext, report telemetry.CycleReport, start, finish time.Time) telemetry.PollRun {
	return telemetry.PollRun{
		RunID:                    run.RunID,
		TriggeredBy:              run.TriggeredBy,
		StartedAt:                start,
		FinishedAt:               finish,
		DurationSeconds:          finish.Sub(start).Seconds(),
		AccountsAttempted:        report.AccountsAttempted,
		AccountsSucceeded:        report.AccountsSucceeded,
		AccountsFailed:           report.AccountsFailed,
		VehiclesAttempted:        report.Attempted,
		VehiclesSucceeded:        report.Succeeded,
		FailuresAsleepTimeout:    report.FailuresByReason[telemetry.ReasonAsleepTimeout],
		FailuresUnauthorized:     report.FailuresByReason[telemetry.ReasonUnauthorized],
		FailuresAPIError:         report.FailuresByReason[telemetry.ReasonAPIError],
		TeslaAPICalls:            report.TeslaAPICalls,
		ChargingSessionsUpserted: report.ChargingSessionsUpserted,
		ChargingFetchFailures:    report.ChargingFetchFailures,
		ConfigCaptureFailures:    report.ConfigCaptureFailures,
	}
}
```

`VehiclesAttempted`/`VehiclesSucceeded` read `report.Attempted`/`.Succeeded` **by
field**, not by name — the two Go identifiers were never renamed (tier 1 design D8:
renaming them would have broken `internal/app/scheduler_test.go`, a file tier 1 could
not touch), while `poll_runs.vehicles_attempted`/`.vehicles_succeeded` **are** named
that way in the schema. This function is exactly the seam tier 1's D8 anticipated:
"`RecordRun`'s caller maps `PollRun.VehiclesAttempted = report.Attempted` by field,
not by name, same as it already must for every other `CycleReport` → `PollRun`
field." `report.FailuresByReason[telemetry.ReasonX]` reads safely off a `nil` map
(Go map reads return the zero value for a missing key, including on a `nil` map) —
load-bearing for Fixture P2 below, where `CollectAll`'s early-return branch may hand
back a `CycleReport{}` whose `FailuresByReason` field was never initialized.

Extracted as a **free function**, not a `*processor` method: it touches no `processor`
field (no `p.loc`, no port), so giving it a receiver would be a receiver that exists
only to be ignored — the free-function form documents that fact and, more
practically, is directly callable from a test with zero setup (D9).

### D8 — Test file: `internal/app/processor_test.go`, `package app` (same-package, mirroring `scheduler_test.go`)

Same-package (`package app`, not `package app_test`) for the same reason
`scheduler_test.go` already chose it in this module (`internal/app/AGENTS.md`
"Covered" section): `buildPollRun` and `recordRun` are unexported, and Fixtures
P1/P2 below call `buildPollRun` directly — a black-box `app_test` package could not
reach it without exporting a symbol whose only consumer would be a test.

### D9 — Fakes needed for Fixtures P3–P5: one per `Processor` collaborator, most of them no-op stubs made safe by an empty `AllRegisteredVehicles`

`ProcessVehicleData`'s steps 2 and 3 both begin by calling
`p.acct.AllRegisteredVehicles(ctx)` and looping over the result
(`processor.go`'s `processChargingData`/`recalculateAnalytics`, both unchanged by
this tier). A fake `account.Service` whose `AllRegisteredVehicles` returns `(nil,
nil)` makes both loops execute zero iterations — so `p.sessionWriter`,
`p.recalculator`, `p.analyticsReader`, `p.gapWriter`, and
`p.superchargerReader` (only reachable from inside that same per-account loop) are
never actually invoked in Fixtures P3–P5. They still need concrete types satisfying
their full interfaces — Go's interface satisfaction is a compile-time requirement
independent of whether a test path reaches a given method — so each gets a trivial
stub returning zero values, following the same "fake whose only job is to compile
and stay out of the way" shape `internal/telemetry/service_test.go`'s existing fakes
already use for methods a given fixture does not exercise.

Fakes, one per collaborator interface (all in `processor_test.go`):

- `fakeCollector` (`telemetry.Collector`) — one method, `CollectAll`, records the
  `RunContext` it was called with and returns a configurable `(CycleReport, error)`.
- `fakeRunWriter` (`telemetry.RunWriter`) — one method, `RecordRun`, records the
  `PollRun` it was called with, increments a call counter, and returns a
  configurable `error`.
- `fakeAccountEmpty` (`account.Service`, 9 methods) — `AllRegisteredVehicles` returns
  `(nil, nil)` and records whether it was called (Fixture P4 asserts it is **not**
  called on the whole-cycle-failure path, proving the short-circuit still holds); the
  other 8 methods (`UpsertFromOAuth`, `SaveTeslaTokens`, `AccessTokenFor`,
  `RegisteredVehicles`, `SeedVehicles`, `SetVehicleConfigIfEmpty`, `LanguageFor`,
  `SetLanguage`) are unreachable given the empty vehicle list and stub to their
  types' zero values.
- `fakeSuperchargerReader` (`telemetry.SuperchargerReader`, 5 methods),
  `fakeSessionWriter` (`charging.SessionWriter`, 1 method), `fakeRecalculator`
  (`analytics.Recalculator`, 2 methods), `fakeAnalyticsReader` (`analytics.Reader`,
  3 methods), `fakeGapWriter` (`analytics.GapWriter`, 1 method) — every method
  unreachable in these fixtures (same reasoning), each stubs to its zero value.

This is the exact fake roster `internal/app/AGENTS.md`'s own "Testing notes" already
flagged as a strict improvement available later ("giving the three steps offline
coverage with fakes... mirroring the fake-store pattern
`internal/telemetry/service_test.go` already uses... cheaper now than it looks"),
built here to the minimum needed for the run-recording seam — not to exercise steps
2/3's own internals, which remain deliberately uncovered per tier 1's own D12
(unchanged: this tier adds no test of `processChargingData`'s or
`recalculateAnalytics`'s internal logic, only of the code wrapped around calling
them).

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

### Fixture P1 — `buildPollRun`, a representative successful run

**Input:**

```go
run := telemetry.RunContext{RunID: fixedRunID, TriggeredBy: telemetry.TriggeredByScheduler}
report := telemetry.CycleReport{
	Attempted: 3, Succeeded: 2,
	FailuresByReason:         map[telemetry.Reason]int{telemetry.ReasonUnauthorized: 1},
	AccountsAttempted:        2,
	AccountsSucceeded:        1,
	AccountsFailed:           1,
	TeslaAPICalls:            5,
	ChargingSessionsUpserted: 4,
	ChargingFetchFailures:    0,
	ConfigCaptureFailures:    1,
}
start := time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC)
finish := start.Add(2500 * time.Millisecond)
```

**Expected `buildPollRun(run, report, start, finish)`:**

```go
telemetry.PollRun{
	RunID: fixedRunID, TriggeredBy: telemetry.TriggeredByScheduler,
	StartedAt: start, FinishedAt: finish, DurationSeconds: 2.5,
	AccountsAttempted: 2, AccountsSucceeded: 1, AccountsFailed: 1,
	VehiclesAttempted: 3, VehiclesSucceeded: 2,
	FailuresAsleepTimeout: 0, FailuresUnauthorized: 1, FailuresAPIError: 0,
	TeslaAPICalls: 5,
	ChargingSessionsUpserted: 4, ChargingFetchFailures: 0, ConfigCaptureFailures: 1,
}
```

### Fixture P2 — `buildPollRun`, the step-1 whole-cycle-failure shape (mirrors tier 1's own Fixture 5b)

**Input:** `run` as above; `report := telemetry.CycleReport{}` (the zero value —
`CollectAll`'s early-return branch, unchanged by this tier, per tier 1 Fixture 4);
`start := time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC)`;
`finish := start.Add(150 * time.Millisecond)`.

**Expected:** every count field `0`; `FailuresAsleepTimeout`/`Unauthorized`/`APIError`
all `0` (reading a `nil` `FailuresByReason` map, not a panic); `StartedAt = start`,
`FinishedAt = finish`, `DurationSeconds = 0.15`; `RunID`/`TriggeredBy` copied through
unchanged. This is the exact all-zero-counts shape tier 1's Fixture 5b already proved
`RunWriter.RecordRun` accepts — this fixture proves tier 2 *builds* that shape
correctly from a real `CollectAll` failure return.

### Fixture P3 — `ProcessVehicleData`, a successful 3-step run records exactly one `poll_runs` row

**Setup:** `fakeCollector` returns `(telemetry.CycleReport{Attempted: 1, Succeeded: 1,
FailuresByReason: map[telemetry.Reason]int{}, TeslaAPICalls: 2}, nil)`;
`fakeAccountEmpty`; `fakeRunWriter` returns `nil`.

**Steps:** `before := time.Now(); report, err := processor.ProcessVehicleData(ctx,
telemetry.TriggeredByScheduler); after := time.Now()`.

**Expected:**
- `err == nil`; `report.Attempted == 1`, `.Succeeded == 1` (the fake's report,
  unmodified except for `.Duration`).
- `report.Duration >= 0`.
- `fakeRunWriter.got.StartedAt` and `.FinishedAt` both fall within `[before, after]`
  (the wall-clock bracket the test itself measured around the call), and
  `fakeRunWriter.got.DurationSeconds` equals
  `fakeRunWriter.got.FinishedAt.Sub(fakeRunWriter.got.StartedAt).Seconds()` — proving
  `report.Duration` and the recorded `PollRun`'s two timestamps are mutually
  consistent, not just independently plausible.
- `fakeRunWriter.calls == 1`.
- `fakeRunWriter.got.RunID` equals `fakeCollector.gotRun.RunID` — the **same** run
  identity flows through both `CollectAll` and `RecordRun` (proves `run` is not
  regenerated between steps).
- `fakeRunWriter.got.VehiclesAttempted == 1`, `.VehiclesSucceeded == 1`,
  `.TeslaAPICalls == 2` — the same by-field mapping Fixture P1 already pins.
- `fakeAccountEmpty`'s `AllRegisteredVehicles` **was** called (steps 2/3 ran, per the
  short-circuit contract — a successful step 1 must not skip them).

### Fixture P4 — `ProcessVehicleData`, the step-1 whole-cycle-failure path still records a row (the case roadmap D1 exists for)

**Setup:** `fakeCollector` returns `(telemetry.CycleReport{}, errBoom)` where
`errBoom` is a distinct sentinel error; `fakeAccountEmpty`; `fakeRunWriter` returns
`nil`.

**Expected:**
- `err == errBoom` — the exact error `CollectAll` produced, unwrapped and unchanged.
- `report` is the zero-value report with `.Duration` set (mirrors Fixture P2's shape).
- `fakeRunWriter.calls == 1` — the row is recorded despite the failure.
- `fakeRunWriter.got` has every count field `0` (Fixture P2's exact shape) and
  non-zero `StartedAt`/`FinishedAt`/`DurationSeconds`.
- `fakeAccountEmpty`'s `AllRegisteredVehicles` was **not** called — steps 2/3 did not
  run, proving the pre-existing short-circuit survives the D4 control-flow reshape
  byte-for-byte.

### Fixture P5 — a `RecordRun` failure never masks the cycle's own outcome

**Setup:** `fakeCollector` returns `(telemetry.CycleReport{Attempted: 1, Succeeded: 1,
FailuresByReason: map[telemetry.Reason]int{}}, nil)`; `fakeAccountEmpty`;
`fakeRunWriter` returns a distinct sentinel error `errRunWriterDown`.

**Expected:**
- `err == nil` — `ProcessVehicleData`'s own returned error is **unaffected** by
  `RecordRun`'s failure; the cycle itself succeeded and must be reported as such.
- `report.Attempted == 1`, `.Succeeded == 1` — the collector's real outcome,
  untouched.
- `fakeRunWriter.calls == 1` — the attempt to record was still made (not skipped
  pre-emptively).

## Risks / Trade-offs

- **`RecordRun`'s own failure is only logged, never surfaced to any caller of
  `ProcessVehicleData`.** This is D5's deliberate choice (an observability write must
  not become a reported cycle failure), but it does mean a persistently broken
  `RunWriter` (e.g. a schema drift, a connection-pool exhaustion) would only be
  visible in application logs, never in `ProcessVehicleData`'s own return value or in
  `poll_attempts`/`poll_runs` themselves (the one table it would populate is exactly
  the one failing to receive rows). Accepted: this mirrors every other post-cycle
  write in this file (`processChargingData`, `recalculateAnalytics`) and a repeatedly
  failing write is exactly the kind of operational problem application logs exist to
  surface.
- **`internal/app/AGENTS.md`'s root README.md sibling row for `poll_attempts`**
  (line ~283 of the repo root `README.md`, in the `internal/telemetry` table block)
  still says "per-run facts come from `GROUP BY run_id`" — a description tier 1 left
  unedited when it *added* a separate `poll_runs` row rather than correcting the
  older one. That line is now stale (this tier's whole point is that `poll_runs`
  answers "per-run facts" directly, no `GROUP BY` needed), but it describes
  `internal/telemetry`'s table, not `internal/app`'s public surface, and fixing it
  is not among the artifacts this tier's own scope line names. Flagged here,
  deliberately left unscheduled in tasks.md rather than silently expanded into this
  tier's work (this project's owner has previously asked to keep scope to the
  literal ticket rather than grow it mid-design) — a one-line follow-up fix
  available the moment anyone touches that file next.

## Verification signals

Per the Test-Execution-Policy: this pass writes the tests above but does not run
them. Owner-run commands once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
go test ./internal/app/...   # owner-run; fully offline, no DATABASE_URL needed
go run ./cmd/poller --once   # smoke check: confirm a poll_runs row lands and the
                              # log line's duration=%s is non-zero
```
