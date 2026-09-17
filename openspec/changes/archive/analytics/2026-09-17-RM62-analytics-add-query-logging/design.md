# Design — RM62-analytics-add-query-logging

## Context

`internal/analytics` exposes three public ports (`internal/analytics/analytics.go`):

- **`Reader`** — 4 methods: `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`,
  `LatestMetricsForVehicles`. Implemented by `*reader` (`reader.go`), backed by the
  module's own `vehicle_metrics` table.
- **`Recalculator`** — 2 methods: `Recalculate`, `Reconcile`. Implemented by
  `*recalculator` (`recalculate.go`), the module's write path — it reads
  `telemetry.Reader`, `charging.SuperchargerSessionAnalyticsReader`, `charging.Reader`,
  and writes `vehicle_metrics`.
- **`GapWriter`** — 1 method: `ReconcileWindow`. Implemented by `*gapWriter`
  (`gap_writer.go`), a transactional UPSERT+DELETE over `charge_gaps`.

Two composition roots construct all three: `cmd/poller/main.go` (lines ~120–140) and
`cmd/web/main.go` (lines ~69–80). Both call `analytics.NewReader(pool)`,
`analytics.NewRecalculator(pool, telemetryReader, sessionAnalyticsReader,
chargingReader)`, and — `cmd/poller` only — `analytics.NewGapWriter(pool)`.

Read directly from `internal/app/processor.go` (the nightly cycle's own
`recalculateAnalytics`, already updated by the archived tier 1 of this roadmap): for
each distinct vehicle, the poller calls `p.recalculator.Reconcile(ctx, v.TeslaID)`
first, then `p.analyticsReader.ConsumedByDay(ctx, v.TeslaID, start, end)`, builds a
`flagged []analytics.ChargeGap` slice from the result, then calls
`p.gapWriter.ReconcileWindow(ctx, v.TeslaID, start, end, flagged)`. `Recalculate` is
never called directly from the poller — only `Reconcile` is, which derives its own
window from watermarks and calls `Recalculate` internally. `Recalculate` IS called
directly from `internal/gateway/handlers/external_charges.go`'s manual-charge write
path, with an explicit `[start, end]`, after a `charging.Writer` save succeeds.

`OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles` have no poller
caller at all — grep of `internal/app/processor.go` and `cmd/poller/main.go` confirms
it. They exist to serve `internal/gateway`'s dashboard/history pages.

## Goals / Non-Goals

**Goals:**
- Every call to `Recalculator.Recalculate`, `Recalculator.Reconcile`,
  `GapWriter.ReconcileWindow`, and `Reader.ConsumedByDay` prints one `logging.Note` line
  naming its identifying and date-filtering arguments (and, for `ConsumedByDay`, the row
  and flagged count returned).
- Zero behavior change: every decorated method's return value and error are passed
  through unaltered.
- Wiring lives inside the module's own constructors, so `cmd/poller` and `cmd/web` both
  get the decorators automatically, with no `cmd/` edit.

**Non-goals (explicitly out of scope for this tier):**
- Logging `Reader.OdometerDeltaByDay`, `Reader.BatteryLevelByDay`,
  `Reader.LatestMetricsForVehicles` — see D1.
- `internal/charging` query logging (tier 3 of this roadmap).
- Any schema, table, column, index, or migration change (there is none in this tier).
- Structured logging / `slog` adoption (`ai/go-conventions.md` §Logging — stdlib `log`
  only, project-wide, unrelated to this tier).

## Decisions

### D1 — `loggingReader` logs only `ConsumedByDay`; its other 3 methods are silent pass-throughs

The dispatch is explicit: "the nightly cycle's own read and write path is the
priority." Grepping every caller of `analytics.Reader` in the repo shows exactly one
method reached from the poller: `ConsumedByDay`, called once per vehicle by
`internal/app/processor.go`'s gap-reconciliation half. The other three methods —
`OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles` — have zero poller
callers; every caller of them lives in `internal/gateway/handlers/`, serving a
dashboard or history page on a live HTTP request.

`loggingReader` implements the full 4-method `Reader` interface (required for `var _
Reader = (*loggingReader)(nil)` and to be assignable everywhere a `Reader` is expected),
but only `ConsumedByDay` calls `logging.Note`; the other three delegate to `inner` with
no logging at all, each with a short comment stating why (dashboard-only, off the
nightly path — the comment states this fact itself, not a decision ID).

**Why not log all 4:** this module's `Reader` is on the gateway's hot read path for the
three silent methods (`ai/architecture.md` §7's read-heavy Performance-Profile — the
gateway reaches `analytics.Reader.LatestMetricsForVehicles` on nearly every page,
`OdometerDeltaByDay`/`BatteryLevelByDay` on the history page). Logging all 4 would add
one `log.Printf` to every such request for information this ticket never asked for —
the same reasoning the roadmap's own tier 3 row gives for `internal/charging`'s 8 ports
("logging all eight adds gateway-request noise to every page load, which is not what
this ticket asked for"). `ConsumedByDay` alone is safe to log unconditionally: its only
caller anywhere in the repo is the nightly poller.

**Rejected alternative — a second, request-scoped decorator wrapping only the 3
dashboard methods, disabled in production.** Adds a second type and a second wiring
point for a capability nobody asked for. If a future ticket wants dashboard read
logging, it should log those 3 methods explicitly then, with its own reasoning for the
extra request-path cost — not inherit an always-on decision made for a different goal.

### D2 — `loggingRecalculator` and `loggingGapWriter` log every method on their port

Unlike `Reader`, `Recalculator` and `GapWriter` have no method outside the nightly/
manual-write path. `Recalculate` is the manual-charge handler's write call and the
implementation `Reconcile` calls internally (D3); `Reconcile` is the poller's only entry
point into this port; `ReconcileWindow` is `GapWriter`'s single method, called once per
vehicle per nightly run and nowhere else. There is no "off-path" method here to keep
silent — every method on both ports already IS the priority path, so both decorators
log 100% of their methods, mirroring `internal/telemetry/query_log.go`'s
`loggingRunWriter` (1/1 logged, a pure write port with no off-path method either).

### D3 — `Reconcile`'s internal call to `Recalculate` bypasses this decorator; accepted, not fixed

`recalculator.Reconcile` (the concrete type in `recalculate.go`) calls `r.Recalculate(
...)` directly — a method call on the same concrete `*recalculator` value, not through
the `Recalculator` interface. `loggingRecalculator` only sees a call when something
holds it as a `Recalculator` interface value and calls a method on that interface value.
The decorator wraps the OUTSIDE of the port; `Reconcile`'s internal call to
`Recalculate` never leaves the concrete type, so it never re-enters the decorator.

Consequence: a poller run logs one `analytics query: Reconcile tesla_id=%d` line, but
NOT a second `analytics query: Recalculate tesla_id=%d start=%s end=%s` line for the
window `Reconcile` derived and passed to its own internal `Recalculate` call. That
derived window is still visible — indirectly — because `Recalculate` calls
`telemetry.Reader.SnapshotsByVehicleBetween(ctx, teslaID, lookbackStart, end.AddDate(0,
0, 1))`, and `internal/telemetry`'s own `loggingReader` (already shipped, tier 1 of
`RM44-telemetry-add-query-logging`) logs THAT call with the concrete `start`/`end` it
received. A reader of the log sees `[Recalculator] [Reconcile] analytics query:
tesla_id=...` immediately followed by `[Reader] [SnapshotsByVehicleBetween] telemetry
query: tesla_id=... start=... end=... rows=...` — the second line IS the date range
`Reconcile`'s internal `Recalculate` call passed to telemetry, which is exactly the
user's stated goal ("read one poller log and see which date ranges analytics passes to
telemetry"). No second analytics-side line is needed to satisfy that goal; it would only
duplicate information already on the very next line.

**Rejected alternative — restructure `Reconcile` to call `Recalculate` through the
`Recalculator` interface field on `*recalculator` itself**, so the decorator would see
the internal call. Rejected: `*recalculator` has no interface-typed field pointing at
itself — adding one only to make a self-call re-enter a wrapper is circular and adds a
field with no other purpose. The telemetry-side log line already gives the reader the
same information for free.

### D4 — Log line arguments and delegation order (Test Contract)

All lines use the message topic `analytics query:`. `tesla_id` prints via `%d`
(`int64`); calendar days (`start`/`end`) print `2006-01-02` — they are already whole
UTC-midnight-bounded days, matching `ConsumedByDay`'s own doc comment and
`internal/telemetry/query_log.go`'s identical date formatting for
`SnapshotsByVehicleBetween`.

| Type | Method | Delegates | Format | Args |
|---|---|---|---|---|
| `Reader` | `ConsumedByDay` | after (need `rows`/`flagged`) | `analytics query: tesla_id=%d start=%s end=%s rows=%d flagged=%d` | `teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result), flaggedCount` |
| `Recalculator` | `Recalculate` | before (write; line appears even on failure) | `analytics query: tesla_id=%d start=%s end=%s` | `teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02")` |
| `Recalculator` | `Reconcile` | before (write; no window known outside — D3) | `analytics query: tesla_id=%d` | `teslaID` |
| `GapWriter` | `ReconcileWindow` | before (write; line appears even if the pre-transaction validation in `gap_writer.go` rejects the call) | `analytics query: tesla_id=%d start=%s end=%s flagged=%d` | `teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(flagged)` |

`flaggedCount` is a small local count of `result[i].Flagged == true`, computed with a
loop right before the `logging.Note` call — no existing helper does this today
(`consumed.go` and `reader.go` were checked), so the decorator computes it inline.

`ConsumedByDay` logs AFTER delegating to `inner`, mirroring `internal/telemetry`'s
`loggingReader` — `rows`/`flagged` need the actual result, and `result` is used exactly
as `inner` returned it (nil/empty slice on error), so the logged counts are accurate
even on an error path. The three write methods log BEFORE delegating, mirroring
`internal/telemetry`'s `loggingStore`/`loggingRunWriter` — their arguments are known
upfront, and the line must still appear if the write itself fails.

### D5 — File layout and wiring, mirroring `internal/telemetry/query_log.go` exactly

One new file, `internal/analytics/query_log.go`, holding all three decorators — this
tier is one cohesive feature ("instrument the module's nightly-path seams"), the same
reasoning `internal/telemetry/query_log.go`'s own header comment gives for grouping its
four decorators in one file.

Each decorator:
- Implements its wrapped interface EXPLICITLY, never by embedding (RD4 — embedding
  would let a future interface method be satisfied silently by promotion, so that call
  would never be logged; explicit implementation turns a missed override into a compile
  error).
- Has exactly one field, `inner <Interface>`, set by an unexported `newLogging*(inner
  <Interface>) *logging*` constructor.
- Carries a compile-time assertion: `var _ Reader = (*loggingReader)(nil)`, `var _
  Recalculator = (*loggingRecalculator)(nil)`, `var _ GapWriter =
  (*loggingGapWriter)(nil)`.

Wiring — one line changed in each of three existing constructors:

```go
// reader.go, NewReader — BEFORE:
func NewReader(pool *pgxpool.Pool) Reader {
	return &reader{metrics: analyticsdb.New(pool)}
}
// AFTER:
func NewReader(pool *pgxpool.Pool) Reader {
	return newLoggingReader(&reader{metrics: analyticsdb.New(pool)})
}
```

```go
// recalculate.go, NewRecalculator — BEFORE:
func NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger charging.SuperchargerSessionAnalyticsReader, manual charging.Reader) Recalculator {
	return &recalculator{
		pool:         pool,
		q:            analyticsdb.New(pool),
		telemetry:    telemetryReader,
		supercharger: supercharger,
		manual:       manual,
	}
}
// AFTER: wrap the returned struct literal in newLoggingRecalculator(...).
```

```go
// analytics.go, NewGapWriter — BEFORE:
func NewGapWriter(pool *pgxpool.Pool) GapWriter {
	return newGapWriter(pool)
}
// AFTER:
func NewGapWriter(pool *pgxpool.Pool) GapWriter {
	return newLoggingGapWriter(newGapWriter(pool))
}
```

Because both `cmd/poller/main.go` and `cmd/web/main.go` call these three exported
constructors directly (`design.md` Context above), neither file needs to change — both
binaries get the decorated instances the next time they build.

### D6 — No behavior change

Every decorator's method body is: (optionally) log, then `return l.inner.Method(...)`
unchanged, or `result, err := l.inner.Method(...)` followed by logging and `return
result, err` unchanged. No decorator inspects, retries, short-circuits, or transforms a
result or error. A caller holding a `Reader`/`Recalculator`/`GapWriter` value cannot
observe any difference in return value, error, or side effect from this change — the
only observable difference is the new log lines.

## Test Contract — expected `cmd/poller --once` log for one vehicle

Authored before the implementation exists, per `ai/go-conventions.md` "contract-first
authoring." This is the acceptance condition the owner checks by eye (no automated test
enforces it — see "Tests excluded" below).

Assumes tier 1 of this roadmap already landed (it has — `internal/app/processor.go`
already logs per-vehicle labels) and tier 3 (`internal/charging` query logging) has
NOT landed yet — so the two `charging` calls `Reconcile` makes
(`ListSessionsByVehicleUpdatedSince`, `ListEntriesByVehicleUpdatedSince`) print nothing,
same as today. For one vehicle, `tesla_id=3744325961659064`, a run where `Reconcile`
finds new telemetry data and re-derives the window `2026-09-14` to `2026-09-17`, and the
gap step's own trailing window is `2026-08-17` to `2026-09-16` with one flagged day:

```
[Processor] [recalculateAnalytics] metrics reconciliation: vehicle 3744325961659064
[Recalculator] [Reconcile] analytics query: tesla_id=3744325961659064
[Reader] [SnapshotsByVehicleUpdatedSince] telemetry query: tesla_id=3744325961659064 since=2026-09-15T00:00:00Z rows=1
[Reader] [SnapshotsByVehicleBetween] telemetry query: tesla_id=3744325961659064 start=2026-09-14 end=2026-09-17 rows=3
[Reader] [SnapshotPrecedingDay] telemetry query: tesla_id=3744325961659064 day=2026-09-14 found=true
[Processor] [recalculateAnalytics] gap reconciliation: vehicle 3744325961659064: 2026-08-17 -> 2026-09-16
[Reader] [ConsumedByDay] analytics query: tesla_id=3744325961659064 start=2026-08-17 end=2026-09-16 rows=30 flagged=1
[GapWriter] [ReconcileWindow] analytics query: tesla_id=3744325961659064 start=2026-08-17 end=2026-09-16 flagged=1
```

Three lines are pre-existing (`[Processor]` twice, all three `[Reader]` telemetry
lines) — this change adds exactly three new lines per vehicle: `[Recalculator]
[Reconcile]`, `[Reader] [ConsumedByDay]`, `[GapWriter] [ReconcileWindow]`. If `Reconcile`
finds no new data from any of its three sources, it returns early without calling
`Recalculate` — the log then shows only the `[Recalculator] [Reconcile] analytics
query: tesla_id=%d` line and none of the three `[Reader]` telemetry lines beneath it,
still followed by the gap step's two lines (the gap step always runs after `Reconcile`
succeeds, regardless of whether `Reconcile` found new data).

## Tests excluded (roadmap Decision 1)

This change adds no test file. The roadmap's own reasoning: the compile-time assertion
per decorator (`var _ Reader = (*loggingReader)(nil)`, etc.) already guarantees every
port method is implemented — a method added later without a matching override is a
build error, not a silent gap. There is no formatting logic complex enough to warrant a
characterization test beyond that; `internal/logging.Note`'s own test
(`internal/logging/logging_test.go`) already verifies the `[Type] [Method] message`
format every new call here reuses unchanged.

What this leaves uncovered: whether each decorator's arguments actually match this
design's table (D4) — nothing catches a typo'd field name or a swapped argument order at
build time, only at read time. The owner's manual `cmd/poller --once` smoke check
against the Test Contract above is the verification signal.

## Database

**None.** This change touches no table, column, index, constraint, view, or migration.
`internal/analytics` owns `vehicle_metrics`, `vehicle_metric_watermarks`, and
`charge_gaps` (`internal/analytics/AGENTS.md` "Data ownership") and this change does not
alter that ownership or any column.

## Knowledge base

Checked `kkpa/context/architecture/nightly-cycle.md` for a fact this change makes
false. It documents step 3's port map (`analytics.Recalculator.Reconcile`,
`analytics.Reader.ConsumedByDay`, `analytics.GapWriter.ReconcileWindow`) and which
tables each read/write touches — both unchanged by this change, since no port
signature, return type, or caller changes. It makes no claim about whether analytics
logs its own queries. No KB update is needed.
