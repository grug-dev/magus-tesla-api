Source: MAG-57 — https://linear.app/magus-monitor/issue/MAG-57/nightly-job-tests
Roadmap: openspec/roadmaps/RM62-nightly-cycle-observability.md
Tier: 2 of 4 (`RM62-app-improve-cycle-step-logging` already archived; `RM62-charging-add-
query-logging` and `RM62-platform-add-nightly-cycle-diagram` remain). No technical
dependency on the other tiers — the roadmap orders them app → analytics → charging →
platform only because the user wanted the debug setup first.

## Why

`internal/analytics` has zero `logging.Note` calls. `internal/telemetry` already has
`query_log.go`, four decorators that log every call through its ports. Reading a
`cmd/poller --once` log today shows every telemetry query, but nothing `analytics` does
in step 3 of the nightly cycle: which window `Recalculate` derives from, how many days
`ConsumedByDay` found flagged, or what `ReconcileWindow` wrote to `charge_gaps`. The
user's goal is to read one log and see which date ranges analytics passes to telemetry —
today that connection is invisible, because nothing marks which telemetry query line
belongs to which analytics call.

## What Changes

- **New file `internal/analytics/query_log.go`** — three explicit, non-embedding logging
  decorators, one per public port, mirroring `internal/telemetry/query_log.go`'s shape
  exactly (compile-time assertion per decorator, no interface embedding):
  - `loggingReader` wraps the public `Reader` interface (4 methods). Only `ConsumedByDay`
    — the one method the nightly cycle actually calls (`internal/app/processor.go`'s gap
    step) — logs. The other three (`OdometerDeltaByDay`, `BatteryLevelByDay`,
    `LatestMetricsForVehicles`) are dashboard-only reads, called from the gateway's
    request path, never from the poller; they stay silent pass-throughs so this
    decorator satisfies the full `Reader` interface without adding a log line to every
    page load. See design.md D1.
  - `loggingRecalculator` wraps the public `Recalculator` interface (2 methods,
    `Recalculate` and `Reconcile`) — both log, both entirely on the write side the
    nightly cycle (and the manual-charge handler) drive.
  - `loggingGapWriter` wraps the public `GapWriter` interface (1 method,
    `ReconcileWindow`) — logs, the nightly cycle's only write to `charge_gaps`.
  - All lines go through `internal/logging.Note`, the platform `[Type] [Method] message`
    format, keeping the message topic `analytics query:`.
- **Three one-line production wiring changes** (no interface changes, no new exported
  symbols beyond what already exists): `NewReader` (`reader.go`), `NewRecalculator`
  (`recalculate.go`), `NewGapWriter` (`analytics.go`) each wrap their concrete
  implementation in the matching decorator before returning it — exactly where
  `internal/telemetry`'s three matching constructors do it. Both `cmd/poller` and
  `cmd/web` call these same constructors directly, so both get the decorators with no
  `cmd/` change at all.
- **No unit tests** — roadmap Decision 1. The compile-time assertion per decorator is
  the safety net: a method added to a port without a matching override here fails to
  build, not silently skips logging.

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key, no schema object.
`internal/analytics`'s public port surface (`Reader`, `Recalculator`, `GapWriter`) gains
no method and loses no method — every signature is unchanged.

**No — internally.** `NewReader`/`NewRecalculator`/`NewGapWriter` keep their existing
signatures; only the concrete value they construct and return changes (now
decorator-wrapped), invisible to every caller because callers already depend on the
interface, never the concrete type.

**Yes — behavior-visible only in logs.** Every call through the four newly-logging
methods (`ConsumedByDay`, `Recalculate`, `Reconcile`, `ReconcileWindow`) now prints one
`logging.Note` line it did not print before. This is the entire point of the tier and
produces no other observable change. No behavior, return value, or error changes for
any caller.

## Modules Affected

- **`internal/analytics/`** — the sole owning module for this tier. New file
  (`query_log.go`), three one-line wiring edits (`reader.go`, `recalculate.go`,
  `analytics.go`).
- **`internal/telemetry/`, `internal/charging/`, `internal/account/`, `internal/tesla/`**
  — **not touched.** `internal/charging`'s own query logging is tier 3 of this roadmap.
- **`internal/app/`** — **not touched.** Tier 1 (already archived) already labels the
  per-vehicle window this tier's `ConsumedByDay`/`ReconcileWindow` lines sit under.
- **`internal/gateway/`** — not touched, and not reachable on this change's own new log
  lines from a request: `LatestMetricsForVehicles` is the only `Reader` method the
  gateway calls (`ai/architecture.md` §7's boundary-guard resolution routes gateway
  reads through `analytics.Reader`), and it is one of the three silent methods (D1) —
  so no gateway page load gains a new log line from this change.
- No `cmd/` file changes. `cmd/poller/main.go` and `cmd/web/main.go` already call
  `analytics.NewReader`/`NewRecalculator`/`NewGapWriter` directly; both inherit the
  decorators automatically once those constructors wrap their return values.

## Database Changes

**None.** No table, column, index, constraint, view, or migration is added, changed, or
dropped. Per the dispatch's own note, the `database` design gate does not apply to this
tier.

**Makefile/guard impact — checked, none found.** No new `MIGRATIONS_DIRS` entry, no
`sqlc` regeneration, no new guard needed. `make logging-guard` already covers this file
the same way it covers every other `internal/` file: a raw stdlib `log` call outside
`internal/logging` fails it, and this change makes none — every line goes through
`logging.Note`.

## Read Paths Affected

**None functionally.** Every decorated method's existing behavior, return value, and
error are unchanged — the decorator logs and delegates, nothing more.
`loggingReader.ConsumedByDay` is the only decorated method reachable from the gateway's
own read path indirectly (through `analytics.Reader`, which the gateway depends on per
`ai/architecture.md` §7) — but the gateway never calls `ConsumedByDay` itself, only
`LatestMetricsForVehicles` (a silent method, D1), so no gateway request gains a new log
line or a new query. `Recalculate`/`Reconcile`/`ReconcileWindow` execute on the nightly
batch and the manual-charge write handler, both already off the gateway's hot read path.
`logging.Note`'s underlying `log.Printf` is not a hot-path-performance concern at this
module's call volume (one nightly cycle, occasional manual-charge saves).

## Capabilities

### Added Capabilities

- **Query argument logging** — every call through `internal/analytics`'s nightly-path
  ports (`Reader.ConsumedByDay`, `Recalculator.Recalculate`, `Recalculator.Reconcile`,
  `GapWriter.ReconcileWindow`) logs its identifying and date-filtering arguments, and,
  for the one read method, the row and flagged count returned. See
  `specs/analytics/spec.md`.

### Modified Capabilities

None — no existing requirement's behavior changes for a caller.

### Out of scope (explicitly deferred)

- **`internal/charging` query logging.** Tier 3 of this roadmap
  (`RM62-charging-add-query-logging`).
- **Logging the three dashboard-only `Reader` methods** (`OdometerDeltaByDay`,
  `BatteryLevelByDay`, `LatestMetricsForVehicles`). See design.md D1 for why: they run on
  the gateway's request path, and logging them would add a line to every page load —
  not what this ticket asked for, which is nightly-cycle observability.
- **Fixing `Recalculate`'s own internal call from `Reconcile`.** `recalculator.Reconcile`
  calls its own `Recalculate` method directly on the concrete type, not through the
  decorated `Recalculator` interface, so that internal call does not itself print a
  second `analytics query: Recalculate ...` line. See design.md D3 for why this is
  accepted, not fixed.

## Testing

Per the Test-Execution-Policy and roadmap Decision 1: **no unit tests in this change.**
The compile-time assertion per decorator (`var _ Reader = (*loggingReader)(nil)`, etc.)
is the safety net — a port method added later without a matching override fails to
build.

Exact commands for the owner to run, once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
make lint
```

Owner-only manual smoke check: `go run ./cmd/poller --once` (wakes the real car, paid
Fleet API calls) — confirming the log shows, per vehicle, an `analytics query:` line
before `Reconcile`'s own effects, then `ConsumedByDay`'s and `ReconcileWindow`'s
`analytics query:` lines under the gap-reconciliation half — matching design.md's Test
Contract.
