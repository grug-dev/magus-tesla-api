Source: MAG-57 — https://linear.app/magus-monitor/issue/MAG-57/nightly-job-tests
Roadmap: openspec/roadmaps/RM62-nightly-cycle-observability.md
Tier: 1 of 3 (`RM62-app-improve-cycle-step-logging`, module `app`). No technical
dependency on tier 2 (`RM62-analytics-add-query-logging`) or tier 3
(`RM62-charging-add-query-logging`) — the roadmap ships this tier first so the
owner can debug the poller while the other two tiers are still being written.

## Why

Today the nightly poller log has one line that is wrong, and one debugging path
that does not exist.

`internal/app/processor.go`'s `recalculateAnalytics` prints its gap-reconciliation
window ONCE, before the loop over vehicles starts. Every telemetry query the log
shows right after that line comes from `Recalculator.Reconcile` — the metrics
half, not the gap half. The line looks like it owns those queries. It does not.

`.vscode/launch.json` has only one entry, for `cmd/web`. Nobody can start
`cmd/poller` under the VS Code debugger. Anyone who wants to step through the
nightly cycle has to use `fmt.Println` or read logs after the fact.

Both problems make the nightly cycle harder to understand than it needs to be —
exactly the gap MAG-57 asks this roadmap to close.

## What Changes

- **`recalculateAnalytics` logs both halves, per vehicle.** The pre-loop window
  line is removed. In its place, inside the per-vehicle loop:
  - One line before `Reconcile`: `metrics reconciliation: vehicle <id>`.
  - One line before the gap work: `gap reconciliation: vehicle <id>: <start> ->
    <end>`.

  Both keep `logging.Note` and the exact message topics the function already
  uses (`metrics reconciliation:`, `gap reconciliation:`), so grepping by topic
  still works. See design.md D1–D3.
- **`.vscode/launch.json` gains a `cmd/poller --once` entry**, mirroring the
  existing `cmd/web` entry's `envFile` handling. See design.md D4.
- **`internal/app/AGENTS.md`'s "Testing notes" section documents the new launch
  entry**, next to its existing mention of `go run ./cmd/poller --once` as the
  verification signal for the two uncovered orchestration steps. See design.md
  D5.
- **No unit tests** — roadmap Decision 1. See design.md "Tests excluded".

## Breaking

**No.** No port signature changes, no return type changes, no HTTP route, no
rendered markup, no i18n key, no database schema. `Processor.ProcessVehicleData`
keeps its exact signature and behavior — only which log lines print, and when,
changes.

## Modules Affected

- **`internal/app/`** — the owning module. `processor.go`'s `recalculateAnalytics`
  and `internal/app/AGENTS.md` change.
- **`.vscode/launch.json`** — outside `internal/`, granted to this change as an
  explicit extra path.
- No other module is touched. `internal/telemetry`, `internal/analytics`,
  `internal/charging`, `internal/account`, `internal/tesla`, `internal/gateway`
  keep every port and log line they already have.

## Database Changes

**None.** This change touches no table, no migration, no query. It is a logging
label fix plus a debugger launch config.

## Read Paths Affected

**None.** `recalculateAnalytics` is nightly-batch-only code, off the gateway's
read path. The change does not add, remove, or reorder any database call — it
only moves where an existing `logging.Note` call sits.

## Capabilities

### Added Capabilities

None.

### Modified Capabilities

- **Processing a vehicle-data cycle** — the analytics-recalculation step's log
  output is now attributable to a specific vehicle and to the correct half of
  the step. See `specs/process-vehicle-data/spec.md` `## ADDED Requirements`
  (a new requirement on an existing capability; no existing requirement's
  GIVEN/WHEN/THEN scenarios change).

### Out of scope (explicitly deferred)

Per the roadmap's own "Out of scope" list — closed as "working as designed" by
the MAG-57 analysis, not postponed:

- The wake poll interval and `ListVehicles` call pattern in `internal/telemetry`.
- Any date filter on `ChargingHistory`.
- `analytics.GapReconciliationWindow`'s value (30 days) — it is correct.
- Renaming `internal/app/processor.go` — the name is right.
- Any end-to-end or integration test of the cycle.
- Query logging for `analytics` or `charging` — those are tiers 2 and 3.

## Testing

Per roadmap Decision 1: **no unit tests in this change.** The work is a log-line
move plus a debugger launch config; the user chose not to add coverage for it.
See design.md "Tests excluded" for what this leaves uncovered and why that is
accepted.

Exact commands for the owner to run, once implementation lands:
`go build ./...`, `go vet ./...`, `gofmt -l .`. The manual smoke check is
`go run ./cmd/poller --once` (wakes the real car, makes paid Fleet API calls) —
or the new VS Code "Debug poller --once" launch entry, which runs the same
command under the debugger.
