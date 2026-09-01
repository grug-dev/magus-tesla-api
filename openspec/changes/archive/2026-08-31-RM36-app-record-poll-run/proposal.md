Source: MAG-35 — https://linear.app/magus-monitor/issue/MAG-35/poll-attemps-tracking
Roadmap: openspec/roadmaps/RM36-poll-run-tracking.md
Tier: 2 of 2 (`RM36-app-record-poll-run`, module `app`), depends on tier 1
(`RM36-telemetry-add-poll-runs`, archived), which built the `poll_runs` table and
the `telemetry.RunWriter` port this tier calls.

## Why

Tier 1 gave the platform a place to put a run-level summary — the `poll_runs` table
and the `telemetry.RunWriter.RecordRun` port — but nothing calls it yet. Nobody can
answer "did last night's run even start" from the database until something measures
one full cycle and writes the row. That something is `internal/app`: it is the only
module that spans all three steps of a cycle (sync fleet data → process charging
data → recalculate analytics) and the only place a whole-cycle failure in step 1 is
visible before it is swallowed into a short-circuited return.

This tier is deliberately small: one new constructor parameter, one refactor of
`ProcessVehicleData`'s control flow to a single measurement/record point, and one
wiring change in `cmd/poller`. It closes roadmap D1's whole justification for
reopening the "no run-level table" decision — a run that fails before touching a
single vehicle must still leave a trace — and it is the only place that justification
can be fulfilled, because `internal/app` is the only caller of both
`telemetry.Collector.CollectAll` (step 1) and, now, `telemetry.RunWriter.RecordRun`.

## What Changes

- **`ProcessVehicleData` measures the whole run** — a start timestamp taken before
  step 1, a finish timestamp taken after step 3 (or immediately after step 1's own
  failure short-circuits steps 2 and 3) — via `internal/clock.Now()`, never a raw
  `time.Now()` (this project's non-negotiable default-zone rule).
- **`ProcessVehicleData` calls `telemetry.RunWriter.RecordRun` exactly once per
  invocation**, on every exit path, including the step-1 whole-cycle-failure path —
  the case roadmap D1 exists for. A `RecordRun` failure is logged, never propagated:
  it must never mask the cycle's own success/failure outcome.
- **`CycleReport.Duration` is populated by this tier** — `ProcessVehicleData` sets it
  on the same report value it returns, so `telemetry.LogCycle`'s existing duration
  field (tier 1) finally prints a real number instead of `0s`, with **no signature
  change** to `LogCycle` and no edit to `internal/telemetry` (tier 1 design D6).
- **`NewProcessor` gains a `runWriter telemetry.RunWriter` parameter.** `cmd/poller`
  (this tier's one granted path outside `internal/app`) is rewired to construct
  `telemetry.NewRunWriter(pool)` and pass it through — its only caller, so no other
  file changes.
- **`internal/app/AGENTS.md` updated** — the "Public interface" section documents
  the new constructor parameter; "Testing notes" documents the new, narrowly-scoped
  coverage this tier adds (the run-recording seam) alongside the still-uncovered
  three orchestration steps (design D12 of tier 1, unchanged by this tier).

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key, no database
schema change (this module owns none — see "Modules Affected" below).

**Yes — internally, to `NewProcessor`'s signature**, additive by one parameter. Its
only caller, `cmd/poller/main.go`, is updated in the same change (granted path), so
nothing is left broken. `Processor`'s single method,
`ProcessVehicleData(ctx, triggeredBy) (telemetry.CycleReport, error)`, is
**unchanged** — same signature, same return type, same short-circuit contract (a
whole-cycle step-1 failure still skips steps 2 and 3; the only difference is that a
row is now recorded either way).

## Modules Affected

- **`internal/app/`** — the owning module for this entire tier. `NewProcessor`,
  `processor` (its concrete type), and `internal/app/AGENTS.md` change.
  `internal/clock` is added to the module's allowed-imports list (zero-dependency,
  no cycle risk — see design.md D3).
- **`cmd/poller/`** — the composition root; wiring only, one call updated to build
  and pass a `telemetry.RunWriter`, matching this file's own stated purpose
  ("WIRING ONLY", see its header comment).
- **`internal/telemetry/`, `internal/tesla/`, `internal/charging/`,
  `internal/analytics/`, `internal/account/`, `internal/gateway/`** — **not
  touched.** Every port this tier calls (`telemetry.RunWriter`, `telemetry.PollRun`,
  `telemetry.CycleReport.Duration`) already exists, built by tier 1.

## Database Changes

**None.** `internal/app` owns no table, no migration, no pool — this tier calls the
`telemetry.RunWriter` port tier 1 already built and gained a design gate for. If
implementation surfaces any need for a new/changed database object, that is a design
defect in this proposal and must stop for a fresh design gate, not be improvised.

## Read Paths Affected

**None.** `RecordRun` is a single `INSERT` per poller invocation (nightly or
`cmd/poller --once`), off the hot read path by construction — the same
Performance-Profile latitude tier 1 already used.

## Capabilities

### Added Capabilities

None — no new capability. This tier fulfills a capability tier 1 already declared
("Run-level poll summary storage", `specs/telemetry/spec.md`) by making the one
caller that can produce a complete `PollRun` actually call it.

### Modified Capabilities

- **Processing a vehicle-data cycle** — every cycle now also records a poll-run
  summary spanning its full measured duration, on every exit path including a
  whole-cycle failure. See `specs/process-vehicle-data/spec.md` `## ADDED
  Requirements` (a new requirement on an existing capability, not a change to any
  of the three existing requirements — none of their GIVEN/WHEN/THEN scenarios
  changes).

### Out of scope (explicitly deferred)

- **Per-vehicle poll duration** (roadmap D3, backlog — unchanged by this tier).
- **A gateway read surface for `poll_runs`** (backlog — unchanged by this tier).
- **Renaming `CycleReport.Attempted`/`.Succeeded`** (tier 1 design D8 — this tier
  reads those fields as they already are; no rename).
- **`RM35-app-adopt-clock`** (a sibling, unrelated roadmap tier that migrates this
  module's *pre-existing* `time.Now()` call sites in `recalculateAnalytics` and
  `Scheduler`'s nil-location fallback). This tier's *new* call sites use
  `internal/clock` directly from the start (design.md D2) — it does not perform
  that migration for the pre-existing sites, which remain RM35's job.

## Testing

Per the Test-Execution-Policy: this tier writes tests but does not run the suite.
`internal/app/processor_test.go` (new file, `package app`, mirroring
`scheduler_test.go`'s same-package convention in this module) covers: the pure
`report → PollRun` field mapping in isolation (no fakes needed) for both a
successful run and the step-1 whole-cycle-failure shape; and, using minimal fakes
for `telemetry.Collector`/`telemetry.RunWriter`/the five otherwise-unexercised
sibling ports, that `ProcessVehicleData` calls `RecordRun` exactly once on both the
success and the failure path, and that a `RecordRun` failure never changes
`ProcessVehicleData`'s own returned outcome. All fixtures are authored in design.md
before implementation exists (`ai/go-conventions.md` §Testing).

Exact commands for the owner to run, once implementation lands:
`go build ./...`, `go vet ./...`, `gofmt -l .` — plus, owner-only,
`go test ./internal/app/...` (offline; no `DATABASE_URL` needed — this tier adds no
DB-backed test) and the full nightly-cycle smoke check,
`go run ./cmd/poller --once`, documented in `internal/app/AGENTS.md`'s Test Contract
group C.
