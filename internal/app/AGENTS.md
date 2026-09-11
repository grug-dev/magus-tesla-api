# app — module rules

Agent-Name: app

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it, and
never restates it: the base list lives in `CLAUDE.md` alone, so a copy here cannot drift.
A dispatched worker reads: base pack + this list + this file, before any write.

*(empty — this module adds no docs of its own. It is a pure composition layer over other
modules' already-documented ports, with no HTML, no database, and no unit-of-measure
concerns.)*

## Responsibility

`internal/app` is the platform's **application layer**. It exposes exactly one public port,
`Processor`, whose single method `ProcessVehicleData` runs one full vehicle-data cycle as four
named steps, in this fixed order:

```
Scheduler ──┐
            ├──> ProcessVehicleData ──┬── Sync Fleet data              (telemetry)
API ────────┘                         ├── Process Charging data       (the mirror)
                                      ├── Recalculate Analytics       (analytics)
                                      └── Measure Monthly Capacity    (charging)
```

**Cross-module map of one cycle:** `kkpa/context/architecture/nightly-cycle.md` — every port
call, every table effect per step, and the failure blast-radius table. Read it before changing
the steps or their order. It is where the facts spanning `telemetry` / `charging` /
`analytics` live, so this file does not restate another module's internals.

**A whole-cycle failure in step 1 skips steps 2, 3 and 4 entirely** for that invocation. Every
per-account and per-vehicle failure inside any step is logged and isolated, never fatal to the
cycle.

**Every invocation records exactly one `poll_runs` row via `telemetry.RunWriter`, on every
exit path** — including the step-1 short-circuit. A failed run still leaves an all-zero-counts
trace.

### This module hosts the Scheduler, and that is not a contradiction

`Scheduler` / `NewScheduler` / `Run` and the pure `nextRun` live in `scheduler.go` here.
That is a **source-location** fact, not a composition fact. The distinction a reviewer checks:

- `Processor` has **no `Scheduler` field**, and must never gain one.
- `ProcessVehicleData` **never consults a clock** and never learns its own trigger beyond the
  `telemetry.TriggeredBy` argument its caller passes in.
- `Scheduler` holds a `Processor` and calls it **from the outside**. `cmd/poller` constructs
  and starts it.

Why here and not `cmd/poller`: `scheduler.go` is ~130 lines of timer, date and DST-aware
rollover logic, and `CLAUDE.md` requires `cmd/` to stay thin. The manual-rerun API lives
inside `cmd/poller` itself and calls `Processor` through a `cmd/poller`-local lock — not
through code in this module.
## Public interface (the port)

**Signatures live in `internal/app/app.go` — read them there.** The `NewProcessor` signature
is deliberately not copied here: it takes ten ports plus a `*time.Location`, and a copy of it
in this file has already drifted from the real one once.

- `Processor.ProcessVehicleData(ctx, triggeredBy)` generates a fresh `RunID` once per
  invocation, builds the `telemetry.RunContext`, and returns `telemetry.CycleReport`
  **unchanged**. This module introduces no report type of its own — reuse over a wrapper.
- **Every constructor argument is another module's public port. There is no `*pgxpool.Pool`
  parameter and there must never be one** — see Data ownership below.
- **The monthly capacity step is called once a month, on the first calendar day, for the
  previous month, always with `teslaID = nil`.** Never per vehicle, never on another day.
- `Scheduler.Run` blocks, firing one cycle per day at `hour:minute` in `loc`, and returns
  `ctx.Err()` on cancellation without starting a new cycle. A nil `loc` falls back to
  `clock.Zone()`. **A per-cycle error is logged, never fatal** — one bad night must not stop
  the schedule.
- **`NewScheduler`'s `cfg telemetry.Config` parameter exists only for its `Clock` field**, the
  test seam. Do not narrow it to a `now func() time.Time` opportunistically — that is a
  separate change.
- **`nextRun` stays unexported and pure** — no clock, no sleeping — so the schedule-time math
  stays unit-testable in isolation. Keep it that way.

No HTTP/JSON surface in this module (`ai/architecture.md` §3).
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

**None.** This is the single fact that distinguishes `internal/app` from every other module:
it owns **no table, no migration directory, no `db/` sub-package, no sqlc entry**.

That was a deliberate reversal. An earlier plan moved `poll_attempts` here; the interview
found that table never held anything Tesla reported, so the purity argument for moving it did
not apply. It stays owned by `internal/telemetry`, which gained two columns instead of losing
the table.

**If a future change gives this module state of its own, that is a signal to revisit whether
the state belongs to one of the modules it composes instead.**
## Testing notes

**Keep the covered and the deliberately-uncovered surfaces distinct. An accepted gap and a
violation look identical in a coverage delta.**

| Surface | State | Why |
|---|---|---|
| `nextRun`, `Scheduler` | covered | pre-existing coverage that relocated here with the code |
| `buildPollRun`, `recordRun` | covered | the run-measurement and `poll_runs` seam |
| `monthlyCapacityPeriod` | covered | pure — this is where the "is it the 1st, and which month" gate lives |
| `callMonthlyCapacityCalculator` | covered | a fake port and a fixed period make it deterministic |
| `runMonthlyCapacityStep` | **accepted gap** | three lines of wiring around a direct `clock.Now()` read, no injectable seam |
| `processChargingData`, `recalculateAnalytics` internals | **accepted gap** | pure relocations of `cmd/poller` code that was never tested there; there is no prior output to characterize |

Rules for the tests that exist:

- **Same package (`package app`, not `package app_test`).** `nextRun`, `buildPollRun`,
  `recordRun` and `Scheduler`'s `loc` field are unexported.
- **The fakes satisfy `Processor` and the collaborator ports — never `telemetry.Collector`
  directly** for the scheduler tests.
- **No test here may import `internal/tesla`.** The tests that needed it stayed in
  `internal/telemetry` with `wake.go`.
- **The fake roster exists only for the run-recording seam.** It makes the orchestration
  loops execute zero iterations, on purpose, so those tests are not on the hook for internals
  they do not cover.

The verification signal for the two uncovered steps is the owner's own
`go run ./cmd/poller --once`. `go build ./...` and `go vet ./...` are the only automated
signals that logic gets today.

**Never run the suite here.** You write tests; the owner runs them. `go build`, `go vet` and
`gofmt -l` are yours — vet compiles `_test.go`, so it catches signature drift in the fake
roster. Tests written but not run are **awaiting-user-verification**, never "done".

**If this module ever gains a DB-backed test, treat that as a signal** that something is
being added here which does not belong — see Data ownership.
