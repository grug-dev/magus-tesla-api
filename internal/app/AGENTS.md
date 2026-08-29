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
`ProcessVehicleData` runs one full vehicle-data cycle as three named steps, in this
fixed order:

```
Scheduler ──┐
            ├──> ProcessVehicleData ──┬── Sync Fleet data       (telemetry)
API ────────┘                         ├── Process Charging data (the T6 mirror)
                                      └── Recalculate Analytics (analytics)
```

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
exists to empty. The accepted cost is that the two peer adapters are no longer symmetric
in location (the tier-8 API will live in its own `cmd/` binary): the `cmd/` rule is
written, the symmetry is taste. Full reasoning, including the leader's correction of its
own earlier framing: `openspec/changes/RM29-app-add-process-vehicle-data/design.md`
**D3** and **D4**.

A whole-cycle failure in step 1 (fleet-data sync) skips steps 2 and 3 entirely for that
invocation — the same short-circuit `cmd/poller`'s `reconcilingCollector` had before this
module existed. Every per-account/per-vehicle failure inside any step is logged and
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
- `NewProcessor(collector telemetry.Collector, superchargerReader
  telemetry.SuperchargerReader, sessionWriter charging.SessionWriter, acct
  account.Service, recalculator analytics.Recalculator, analyticsReader analytics.Reader,
  gapWriter analytics.GapWriter, loc *time.Location) Processor` is the constructor.
  Every argument is another module's **public port** — there is no `*pgxpool.Pool`
  parameter, and there must never be one added (see Data Ownership below).

- `Scheduler` + `NewScheduler(processor Processor, hour, minute int, loc *time.Location,
  cfg telemetry.Config) *Scheduler` + `(*Scheduler) Run(ctx context.Context) error` —
  the **daily scheduled driving adapter** (design.md D4). `Run` blocks, firing one
  `ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` per day at `hour:minute` in
  `loc`, logging each cycle with `telemetry.LogCycle(report, err)`, and returning
  `ctx.Err()` on cancellation without starting a new cycle. A nil `loc` falls back to
  `time.Local`. A per-cycle error is logged, never fatal — one bad night must not stop
  the schedule.
  - The `cfg telemetry.Config` parameter exists **only for its `Clock` field** (the
    test seam). It is kept rather than narrowed to a `now func() time.Time`: design.md
    **D13** records why (minimum-diff relocation, `cmd/poller`'s single shared
    `telemetry.Config` keeps driving both the collector and the scheduler, and the
    relocated tests keep their existing construction sites). Do not narrow it
    opportunistically — that is a separate change.
  - `nextRun` stays **unexported and pure** (no clock, no sleeping) so the schedule-time
    math remains unit-testable in isolation. Keep it that way.

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3; the future
tier-8 API is a separate `cmd/` binary that will call `Processor`, not code inside this
module).

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
- `internal/charging` — `SessionWriter` (writes the mirrored charge sessions).
- `internal/analytics` — `Recalculator`, `Reader`, `GapWriter`, and the `ChargeGap`
  domain type (the analytics-recalculation step).
- `internal/account` — `Service`: specifically `AllRegisteredVehicles`, to enumerate the
  accounts/vehicles both the charging-mirror step and the analytics-recalculation step
  need to loop over. `app` sits **above** every domain module in the call graph (only
  `cmd/` binaries call it) and `account` sits at the **bottom** already, so this is a
  forward, one-way dependency with no cycle risk
  (`ai/architecture.md` §"Dependency direction").
- `github.com/google/uuid`, stdlib (`context`, `time`, `log`). `time` is load-bearing
  twice over: the reconcile step's "yesterday in `loc`" math and `Scheduler`'s
  timer/`nextRun` logic.

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

**This module is not test-free, and the two halves of it are covered differently. Keep
them distinct — an accepted gap and a violation look identical in a coverage delta.**

**Covered — `scheduler_test.go` (four tests).** `internal/app/scheduler_test.go` holds
`TestNextRun`, `TestScheduler_ShutsDownWithoutRunningWhenCancelled`,
`TestScheduler_NilLocationDefaultsToLocal` and `TestScheduler_RunsAndLogsOneCycle`,
relocated from `internal/telemetry/scheduler_test.go` together with the code they cover
(RM29 tier 7, design.md **D4**, carrying RD8). They are **pre-existing coverage that
moved**, not coverage invented for that tier, so they sit outside roadmap D10's
"characterization only" bar rather than violating it. Rules for them:

- Same package (`package app`, not `package app_test`) — `nextRun` is unexported and
  `TestScheduler_NilLocationDefaultsToLocal` reads the unexported `loc` field.
- Their two fakes satisfy **`Processor`**, not `telemetry.Collector`: one method
  recording the call and returning a canned `telemetry.CycleReport`/error. Expected
  values are pinned in design.md's Test Contract **group S** — change a value there
  before changing one here.
- They must not import `internal/tesla`. The three `TestWaitUntilOnline_*` tests that
  needed it stayed behind in `internal/telemetry`, with `wake.go`.

**Deliberately uncovered — the three orchestration steps.** The top-level
`ProcessVehicleData` control flow, the charging-mirror step and the
analytics-recalculation step ship with **no offline/unit test**, and that is a recorded
choice (design.md **D12**), not an oversight: all three are pure relocations of code that
lived in `cmd/poller` and was never tested there (this project has never had a
`cmd/poller` test). Roadmap D10 does not ask a tier to invent coverage for code that
predates it and was never covered — there is no prior output to characterize.

The verification signal for those three remains what it already was before they had a
name: the owner's own `go run ./cmd/poller --once`, whose expected log output and
`poll_attempts` spot-check are documented in design.md's Test Contract group C.
`go build ./...` and `go vet ./...` are the only automated signals that logic gets today.

**Flagged for a future change, not required by this one:** giving the three steps offline
coverage with fakes for `telemetry.Collector` / `telemetry.SuperchargerReader` /
`charging.SessionWriter` / `account.Service` / `analytics.Recalculator` /
`analytics.Reader` / `analytics.GapWriter` — mirroring the fake-store pattern
`internal/telemetry/service_test.go` already uses. That is cheaper now than it looks:
the module already has a `_test.go` file and a same-package test convention to extend.
It is a strict improvement available later, not a requirement of the tier that created
this module.

**Never run the suite here.** Per `CLAUDE.md` §"Builds & local checks", you write tests
and the owner runs them: `go build ./...`, `go vet ./...` and `gofmt -l` are yours (vet
compiles `_test.go`, so it catches signature drift in the relocated scheduler tests);
`go test ./...` / `make test` / `make check` are the owner's. Tests written but not run
are **awaiting-user-verification**, never "done".

If this module ever gains a DB-backed test (it should not, per Data Ownership above —
treat that as a signal something is being added here that does not belong), follow
`ai/go-conventions.md` §Testing's `testdb.ProvisionDirs` guidance for any fixture that
needs another module's tables, exactly as `internal/analytics` and `internal/charging`
already do.
