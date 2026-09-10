# app — module rules

Agent-Name: app

## Doc-Pack (module)

Additive to the base Doc-Pack (repo-root `CLAUDE.md`, `AGENTS.md`, `ai/architecture.md`,
`ai/go-conventions.md`, `ai/agentic-workflow.md`) — never replacing it. This module adds
no further docs of its own: it is a pure composition layer over other modules' already-
documented ports, and has no HTML, no database, and no unit-of-measure concerns of its
own.

## Responsibility

`internal/app` is the platform's **application layer** — the module RM29 tier 7 created
to end roadmap violation #3: *"there is no application layer; `cmd/poller`'s
`reconcilingCollector` is business logic living in a `cmd/` because there is nowhere
else to put it."* It exposes exactly one public port, `Processor`, whose single method
`ProcessVehicleData` runs one full vehicle-data cycle as four named steps, in this
fixed order:

```
Scheduler ──┐
            ├──> ProcessVehicleData ──┬── Sync Fleet data              (telemetry)
API ────────┘                         ├── Process Charging data       (the T6 mirror)
                                      ├── Recalculate Analytics       (analytics)
                                      └── Measure Monthly Capacity    (charging, RM52)
```

Step 4, added by `RM52-app-add-monthly-capacity-step` tier 2, runs only on the first
calendar day of the month, in the platform's default zone, and measures the previous
month. It calls `charging.MonthlyCapacityCalculator.Calculate` — see §Public interface
and §Testing notes below.

Since `RM36-app-record-poll-run` tier 2, every invocation also measures its own
start-to-finish span and records exactly one `poll_runs` summary row via
`telemetry.RunWriter` — on every exit path, including the step-1 whole-cycle-failure
short-circuit below (`openspec/changes/RM36-app-record-poll-run/design.md` D4/D5/D6).

**Cross-module map of one cycle:** `kkpa/context/architecture/nightly-cycle.md` — every port
call, every table effect per step, and the failure blast-radius table. Read it before changing
the steps or their order; it is where the facts that span `telemetry`/`charging`/`analytics`
live, so this file does not have to restate another module's internals. (It also links a
rendered diagram; the code is the source of truth, then that guide, then the diagram.)

`Scheduler` and the future manual-rerun API (roadmap tier 8, parked) are **peer driving
adapters that CALL this port** — **neither is inside `Processor`**. `ProcessVehicleData`
has no knowledge of *when* a cycle runs or *how* it was triggered beyond the
`telemetry.TriggeredBy` value its caller passes in; it only knows *what* one cycle does.

**This module also HOSTS the scheduled driving adapter, and that is not a contradiction.**
Since RM29 tier 7's RD8 (which superseded RD5), `Scheduler`/`NewScheduler`/`Run` and the
pure `nextRun` live in `internal/app/scheduler.go`, relocated essentially verbatim from
`internal/telemetry/scheduler.go`. Read that as a **source-location** fact, not a
composition fact — the distinction the reviewer checks:

- `Processor` has **no `Scheduler` field**, and must never gain one.
- `ProcessVehicleData` **never consults a clock** and never learns its own trigger beyond
  the `telemetry.TriggeredBy` argument.
- `Scheduler` holds a `Processor` and calls it **from the outside**, exactly as it did
  from `cmd/poller`. `cmd/poller` still constructs it (`app.NewScheduler(...)`) and still
  starts it (`Run(ctx)`).

Why here rather than `cmd/poller`: `scheduler.go` is ~130 lines of timer, date and
DST-aware rollover logic, and `CLAUDE.md` §Non-negotiables says *"`cmd/` stays thin (zero
business logic)"* — putting it in `cmd/` would have refilled the very file this tier
exists to empty. The manual-rerun API (tier 8) now lives inside `cmd/poller` itself, not
in its own `cmd/` binary (`platform-add-manual-rerun-api` design.md D1): it calls
`Processor` through a `cmd/poller`-local lock, `guardedProcessor` (design.md D3), not
through code inside `internal/app`. Full reasoning for the scheduler's own placement,
including the leader's correction of its own earlier framing:
`openspec/changes/RM29-app-add-process-vehicle-data/design.md` **D3** and **D4**.

A whole-cycle failure in step 1 (fleet-data sync) skips steps 2, 3 and 4 entirely for
that invocation — the same short-circuit `cmd/poller`'s `reconcilingCollector` had
before this module existed. Every per-account/per-vehicle failure inside any step is
logged and
isolated (never fatal to the cycle) — this module changes nothing about that isolation,
it only relocated where the code lives
(`openspec/changes/RM29-app-add-process-vehicle-data/design.md` D6/D8).

## Public interface (the port)

The module's mandatory contract is a Go interface (`ai/go-conventions.md` —
interface-first):

- `Processor` — one method:
  `ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error)`.
  Generates a fresh `RunID` (`uuid.New()`) once per invocation and builds a
  `telemetry.RunContext{RunID, TriggeredBy: triggeredBy}`, passed to
  `telemetry.Collector.CollectAll`. Returns `telemetry.CycleReport` **unchanged** — this
  module introduces no new report/result type of its own
  (`RM29-app-add-process-vehicle-data` design D7: reuse over a wrapper, since nothing new
  needs structured surfacing beyond what `CollectAll` already reports).
- `NewProcessor(collector telemetry.Collector, superchargerHistoryReader
  telemetry.SuperchargerHistoryReader, runWriter telemetry.RunWriter, sessionWriter
  charging.SessionWriter, mirrorWatermarks charging.MirrorWatermarkStore,
  monthlyCapacityCalculator charging.MonthlyCapacityCalculator, acct account.Service,
  recalculator analytics.Recalculator, analyticsReader analytics.Reader, gapWriter
  analytics.GapWriter, loc *time.Location) Processor` is
  the constructor — ten public ports plus one `*time.Location`. Every argument is another module's **public port** — there is no
  `*pgxpool.Pool` parameter, and there must never be one added (see Data Ownership
  below). `runWriter` is the port `ProcessVehicleData` calls, exactly once per
  invocation after measuring the run's start-to-finish span, to record a `poll_runs`
  summary row (`RM36-app-record-poll-run` tier 2). `monthlyCapacityCalculator` is
  `charging`'s third port here (`RM52-app-add-monthly-capacity-step` tier 2, RD6/RD7):
  `ProcessVehicleData` calls its `Calculate` method once a month, on the first
  calendar day, for the previous month, always with `teslaID = nil` — never per
  vehicle, never on any other day.

- `Scheduler` + `NewScheduler(processor Processor, hour, minute int, loc *time.Location,
  cfg telemetry.Config) *Scheduler` + `(*Scheduler) Run(ctx context.Context) error` —
  the **daily scheduled driving adapter** (design.md D4). `Run` blocks, firing one
  `ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` per day at `hour:minute` in
  `loc`, logging each cycle with `telemetry.LogCycle(report, err)`, and returning
  `ctx.Err()` on cancellation without starting a new cycle. A nil `loc` falls back to
  the platform's default zone, `clock.Zone()` (`America/Bogota`) —
  `RM35-app-adopt-clock`, roadmap D4. A per-cycle error is logged, never fatal — one
  bad night must not stop the schedule.
  - The `cfg telemetry.Config` parameter exists **only for its `Clock` field** (the
    test seam). It is kept rather than narrowed to a `now func() time.Time`: design.md
    **D13** records why (minimum-diff relocation, `cmd/poller`'s single shared
    `telemetry.Config` keeps driving both the collector and the scheduler, and the
    relocated tests keep their existing construction sites). Do not narrow it
    opportunistically — that is a separate change.
  - `nextRun` stays **unexported and pure** (no clock, no sleeping) so the schedule-time
    math remains unit-testable in isolation. Keep it that way.

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3; the
tier-8 manual-rerun API lives inside `cmd/poller`, which calls `Processor`, not code
inside this module — `platform-add-manual-rerun-api` design.md D1).

## Allowed / forbidden imports

**May import** — all through the sibling's **public port only**, never its `db`
sub-package or internals:

- `internal/telemetry` — `Collector` (sync fleet data), `SuperchargerReader` (reads the
  Supercharger sessions the charging-mirror step copies), `RunContext`/`TriggeredBy`
  (the types this module builds and passes down — `telemetry` owns them because it owns
  the `poll_attempts` columns they fill), `CycleReport` (this module's own return type,
  reused unchanged), `Config` (**only** for `Scheduler`'s `Clock` seam — design.md D13;
  `telemetry` owns the type, this module neither declares nor re-exports it), and
  `LogCycle` (called by `Scheduler.Run` across the boundary; it stays in `telemetry`,
  which is why it is exported — design.md D11).
- `internal/charging` — `SessionWriter` (writes the mirrored charge sessions) and
  `MonthlyCapacityCalculator` (`RM52-app-add-monthly-capacity-step` tier 2: `Calculate`
  measures each vehicle's real pack capacity, called once a month). No new import path
  is added — this module already imports `internal/charging` for `SessionWriter`; the
  new port comes from the same package.
- `internal/analytics` — `Recalculator`, `Reader`, `GapWriter`, and the `ChargeGap`
  domain type (the analytics-recalculation step).
- `internal/clock` — `Now()` and `CalendarDay(t, loc)`, used by `recalculateAnalytics`
  to resolve "yesterday in `p.loc`" (`RM35-app-adopt-clock`) and by `NewScheduler`'s
  nil-`loc` fallback (above). `clock` imports nothing project-local, so this creates
  no cycle (`ai/architecture.md` §"Dependency direction").
- `internal/account` — `Service`: specifically `AllRegisteredVehicles`, to enumerate the
  accounts/vehicles both the charging-mirror step and the analytics-recalculation step
  need to loop over. `app` sits **above** every domain module in the call graph (only
  `cmd/` binaries call it) and `account` sits at the **bottom** already, so this is a
  forward, one-way dependency with no cycle risk
  (`ai/architecture.md` §"Dependency direction").
- `github.com/google/uuid`, stdlib (`context`, `time`, `log`). `time` is load-bearing
  twice over: the reconcile step's "yesterday in `loc`" math and `Scheduler`'s
  timer/`nextRun` logic.
- `internal/clock` — `ProcessVehicleData` reads `clock.Now()` for its `start`/`finish`
  measurement points (`RM36-app-record-poll-run` design D2/D3). Imports stdlib `time`
  and nothing else, so it creates no cycle risk, same reasoning as every other allowed
  import above.
  twice over: the `*time.Location` type threaded through `NewProcessor`/`NewScheduler`
  and `Scheduler`'s timer/`nextRun` logic. The reconcile step's "yesterday in `loc`"
  math itself now goes through `internal/clock` (above) rather than a raw
  `time.Now()`/UTC-midnight truncation.

**Must NOT import:**

- `internal/tesla` — this module never calls the Fleet API directly; `telemetry` already
  wraps every Tesla call this use case needs.
- `internal/gateway`, `html/template`, `templ` — no HTML in a domain/application module
  (`ai/architecture.md` §2).
- Any module's `db` sub-package (`telemetrydb`, `chargingdb`, `analyticsdb`,
  `accountdb`) — cross-module data flows only through public ports.
- `github.com/jackc/pgx/v5` / `pgxpool` — this module has no pool and no query of its
  own; see Data Ownership.

## Data ownership

**None.** This is the single fact that distinguishes `internal/app` from every other
domain module in this project: it owns **no table, no migration directory, no `db/`
sub-package, no sqlc entry**. This was a deliberate design reversal of roadmap decision
D2 (which originally planned to move `poll_attempts` here as `process_runs`) — the
tier-7 pre-artifacts interview found `poll_attempts` never held anything Tesla reported
in the first place, so the purity test D2 used to justify the move does not apply to it.
`poll_attempts` stays owned by `internal/telemetry`, which gained two columns
(`run_id`, `triggered_by`) instead of losing the table. Full reasoning:
`openspec/changes/RM29-app-add-process-vehicle-data/design.md` D1, and
`internal/telemetry/AGENTS.md`'s own Data Ownership section.

If a future change ever gives this module state of its own, that is a signal to revisit
whether the state actually belongs to one of the modules it composes instead — the same
question this tier's own interview asked and answered about `poll_attempts`.

## Testing notes

**This module is not test-free, and its four covered/uncovered surfaces are covered
differently. Keep them distinct — an accepted gap and a violation look identical in a
coverage delta.**

**Covered — `scheduler_test.go` (four tests).** `internal/app/scheduler_test.go` holds
`TestNextRun`, `TestScheduler_ShutsDownWithoutRunningWhenCancelled`,
`TestScheduler_NilLocationDefaultsToClockZone` and `TestScheduler_RunsAndLogsOneCycle`,
relocated from `internal/telemetry/scheduler_test.go` together with the code they cover
(RM29 tier 7, design.md **D4**, carrying RD8). They are **pre-existing coverage that
moved**, not coverage invented for that tier, so they sit outside roadmap D10's
"characterization only" bar rather than violating it. Rules for them:

- Same package (`package app`, not `package app_test`) — `nextRun` is unexported and
  `TestScheduler_NilLocationDefaultsToClockZone` reads the unexported `loc` field.
- Their two fakes satisfy **`Processor`**, not `telemetry.Collector`: one method
  recording the call and returning a canned `telemetry.CycleReport`/error. Expected
  values are pinned in design.md's Test Contract **group S** — change a value there
  before changing one here.
- They must not import `internal/tesla`. The three `TestWaitUntilOnline_*` tests that
  needed it stayed behind in `internal/telemetry`, with `wake.go`.

**Covered — `processor_test.go` (`RM36-app-record-poll-run` tier 2, five tests).**
`internal/app/processor_test.go` holds `TestBuildPollRun_SuccessfulRun` and
`TestBuildPollRun_WholeCycleFailureShape` (direct unit tests of the pure `buildPollRun`
mapping, no fakes — Test Contract fixtures P1/P2), plus
`TestProcessVehicleData_SuccessfulRunRecordsOneRow`,
`TestProcessVehicleData_WholeCycleFailureStillRecordsRow` and
`TestProcessVehicleData_RecordRunFailureDoesNotMaskCycleOutcome` (fixtures P3–P5),
which drive `ProcessVehicleData` through `NewProcessor` and the fake roster design D9
specifies (`fakeCollector`, `fakeRunWriter`, `fakeAccountEmpty`, plus a
zero-value stub per remaining collaborator). This is a **narrowly-scoped third covered
surface**, alongside `scheduler_test.go`'s four tests above: it tests the
`buildPollRun`/`recordRun` seam this tier added — the run measurement and the
`poll_runs` recording, on the success path, the step-1 whole-cycle-failure path, and the
`RecordRun`-fails-without-masking-the-cycle path — **not** `processChargingData`'s or
`recalculateAnalytics`'s own internal logic, which remains deliberately uncovered below,
unchanged by this tier. Same package (`package app`) for the same reason
`scheduler_test.go` uses it: `buildPollRun` and `recordRun` are unexported.

**Covered — `monthly_capacity_step_test.go` (`RM52-app-add-monthly-capacity-step` tier
2, eight tests).** `internal/app/monthly_capacity_step_test.go` holds
`TestMonthlyCapacityPeriod` (six table cases, A1–A6 — Test Contract Group A) and
`TestCallMonthlyCapacityCalculator_Success` /
`TestCallMonthlyCapacityCalculator_ErrorIsLoggedNotPropagated` (Group B), against a new
fake, `fakeMonthlyCapacityCalculator`, added to the same roster shape
`processor_test.go` already uses. Step 4 splits into three functions with three
different testing treatments (design.md D2's own table):

| Function | Pure? | Tested? | Why |
|---|---|---|---|
| `monthlyCapacityPeriod` | yes | **yes** — Group A | No I/O — this is where the RD6/RD7 gate logic lives (is today the 1st, and if so, which month) |
| `callMonthlyCapacityCalculator` | no (calls the port) | **yes** — Group B | No clock involved — a fake port and a fixed `period` make it deterministic |
| `runMonthlyCapacityStep` | no (reads `clock.Now()`/`clock.Zone()`) | **no** — accepted gap | Three lines of wiring: read the clock, call the pure gate, call the tested function. This is the same category of gap already accepted for `recalculateAnalytics`'s own "yesterday" line below — a direct `clock.Now()` read with no injectable seam |

The important behavior — whether today is the 1st and what period follows, and whether
the port is called correctly with its outcome logged and its errors swallowed — is
fully covered. What stays untested is three lines of composition around a real clock
read, not new logic of its own.

**Deliberately uncovered — the three orchestration steps' own internals.** The
`processChargingData` and `recalculateAnalytics` steps' own logic (the per-account/
per-vehicle loops, the session-mirroring and gap-reconciliation calls inside them) still
ship with **no offline/unit test**, and that remains a recorded choice (design.md
**D12** of tier 1, unchanged by `RM36-app-record-poll-run` tier 2's own design D9): both
are pure relocations of code that lived in `cmd/poller` and was never tested there (this
project has never had a `cmd/poller` test). Roadmap D10 does not ask a tier to invent
coverage for code that predates it and was never covered — there is no prior output to
characterize. `processor_test.go`'s fake roster makes their loops execute zero
iterations (an empty `AllRegisteredVehicles`) precisely so it can test the code wrapped
*around* calling them without also being on the hook for their own internals.

The verification signal for those two steps' own internals remains what it already was
before they had a name: the owner's own `go run ./cmd/poller --once`, whose expected log
output and `poll_attempts` spot-check are documented in design.md's Test Contract group
C. `go build ./...` and `go vet ./...` are the only automated signals that logic gets
today.

**No longer flagged as future work — the fake roster.** `internal/app/AGENTS.md`
previously flagged "offline coverage with fakes for `telemetry.Collector` /
`telemetry.SuperchargerReader` / `charging.SessionWriter` / `account.Service` /
`analytics.Recalculator` / `analytics.Reader` / `analytics.GapWriter`" as a strict
improvement available later. `RM36-app-record-poll-run` tier 2 built exactly that
roster — but only to the minimum needed for the run-recording seam (design D9), not to
exercise the two orchestration steps' own internals, which remain the uncovered surface
described just above and a candidate for a still-later change.

**Never run the suite here.** Per `CLAUDE.md` §"Builds & local checks", you write tests
and the owner runs them: `go build ./...`, `go vet ./...` and `gofmt -l` are yours (vet
compiles `_test.go`, so it catches signature drift in the relocated scheduler tests and
in `processor_test.go`'s fake roster);
`go test ./...` / `make test` / `make check` are the owner's. Tests written but not run
are **awaiting-user-verification**, never "done".

If this module ever gains a DB-backed test (it should not, per Data Ownership above —
treat that as a signal something is being added here that does not belong), follow
`ai/go-conventions.md` §Testing's `testdb.ProvisionDirs` guidance for any fixture that
needs another module's tables, exactly as `internal/analytics` and `internal/charging`
already do.
