# Design — RM69-analytics-add-unfinished-for-date

## Overview

`vehicle_metrics` already holds one row per `(tesla_id, metric_date)` for
every day this module has finished computing — the roadmap's own plain
definition of "done" (D1) is exactly "a row exists for `metric_date = local
today − 1`". This tier adds the one read tier 2's retry schedule needs to
ask that question in bulk, once every 30 minutes, for every registered
vehicle: `UnfinishedForDate`. It reads only what already exists — no new
table, column, index, or migration.

## Decisions

### D1 — One new method on the existing `Reader` port, not a new port

```go
UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error)
```

joins `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, and
`LatestMetricsForVehicles` on `Reader`. Rejected: a new, separate interface
(e.g. `UnfinishedReader`) for this one method. `Reader` already owns
`vehicle_metrics`, already exposes a `LatestMetricsForVehicles` method whose
shape is "batch of vehicles in, one derived fact out", and this project has
direct precedent for adding a differently-scoped batch method straight onto
an existing port rather than growing a second interface for it: tier 1 of
this same roadmap added `CollectVehicles` directly onto `telemetry.Collector`
next to `CollectAll`, specifically because both express "the same kind of
work", not because their argument shapes matched. A second interface here
would force `cmd/poller`'s composition root to hold two analytics reader
values instead of one for no benefit — nothing about `UnfinishedForDate`
needs its own constructor, its own pool wiring, or its own decorator file;
it reuses every one of those `Reader` already has.

### D2 — The vehicle set is a plain `[]int64`, not `vehicleref.Ref`

Restates roadmap D12 for this module's own artifact. `vehicleref.Ref` proves
that one signed-in user's HTTP request names a vehicle that user's account
actually owns — `vehicleref.Authorize`/`vehicleref.All` are the only ways to
build one, and `make vehicleref-guard` forbids calling either anywhere
outside `internal/vehicleref` itself, a `_test.go` file, or the gateway's own
`authorizeVehicle`. `UnfinishedForDate`'s caller (tier 2's `RetryScheduler`)
has no signed-in user and no single account — it walks every registered
vehicle across every account on a timer, exactly the shape
`LatestMetricsForVehicles` was never meant to serve and exactly the shape
`telemetry.Collector.CollectVehicles` (this roadmap's tier 1) already
established the plain-`int64` precedent for. A `Ref` built anywhere in this
call chain would have no "this account's own list" to prove membership
against — it would not add a real check, only a shape that looks like one.

**Consequence for this method's own semantics**: it performs no ownership or
registration check of its own. An id that is not currently registered to
any account, or was never a real vehicle, still counts as "unfinished" — it
has no `vehicle_metrics` row, so it cannot be distinguished from a real,
currently-offline vehicle by this query alone. This is intentional: the
caller (tier 2) already gets its candidate set from
`account.AllRegisteredVehicles`, so a garbage id never reaches this method
in practice, and this method does not need to re-derive that guarantee to
stay correct — it answers exactly the question its name asks ("does a row
exist for this id and date"), nothing more.

### D3 — One query for the whole set: `unnest(...)` + `NOT EXISTS`

```sql
-- name: UnfinishedVehicleIDsForDate :many
SELECT DISTINCT t.tesla_id
FROM unnest(@tesla_ids::bigint[]) AS t(tesla_id)
WHERE NOT EXISTS (
    SELECT 1 FROM analytics.vehicle_metrics vm
    WHERE vm.tesla_id = t.tesla_id AND vm.metric_date = @metric_date
)
ORDER BY t.tesla_id;
```

Never a per-vehicle loop calling a single-id query `F` times — this project's
read-optimization rule is explicit and project-wide (`ai/go-conventions.md`
§Read optimization: "Write batch reads at the module interface level ...
never per-entity helpers that the caller must loop over"). `unnest` turns
the caller-supplied array into a row set Postgres can drive a `NOT EXISTS`
subquery from directly, one existence probe per candidate id, in a single
round trip to the database regardless of how many ids the caller passes.

### D4 — Duplicate input ids are deduplicated, never repeated in the output

`SELECT DISTINCT` on the outer query. `unnest` preserves every element of
the input array, including a repeat, so without `DISTINCT` a duplicated
input id that turns out unfinished would appear twice in the result. Nothing
about "which ids are unfinished" is a multiset question — it is a set
question — so the caller (a set of `tesla_id`s to retry) is simpler to
consume when a duplicate on the way in can never produce a duplicate on the
way out. Rejected: leaving dedup to the caller. Tier 2's own candidate list
already comes from a distinct-by-`TeslaID` helper, but nothing about this
port's own contract should quietly rely on every future caller doing the
same dedup work correctly.

### D5 — Empty input: no query, empty non-nil result, nil error

```go
func (r *reader) UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error) {
	if len(teslaIDs) == 0 {
		return []int64{}, nil
	}
	...
}
```

Mirrors `telemetry.Collector.CollectVehicles`'s own empty-input short-circuit
(tier 1, design D8) and this port's own established non-nil-empty-slice
convention. An empty `unnest(ARRAY[]::bigint[])` would legally return zero
rows and cost nothing either way, but skipping the round trip entirely is
strictly cheaper and needs no database at all to verify by test — the
empty-input case becomes a pure offline test against a fake store that fails
if it is ever called (see "Test contract" below).

### D6 — Output order: ascending by `tesla_id`

Unlike `LatestMetricsForVehicles`'s explicitly unspecified order, this
method's result is sorted (`ORDER BY t.tesla_id`). The result feeds directly
into a second `Processor` call (`ProcessVehicleDataForVehicles`, tier 2) and
into test assertions and log lines — a stable, deterministic order costs one
`ORDER BY` on an already-small result set (bounded by the platform's total
registered-vehicle count, never a per-account subset) and removes an entire
class of flaky-test and non-reproducible-log risk for free.

### D7 — `UnfinishedForDate` logs, like `ConsumedByDay`

`query_log.go`'s `loggingReader` today logs only `ConsumedByDay` — the one
`Reader` method the nightly poller itself calls — and leaves the other three
(dashboard-only) methods as silent pass-throughs, explicitly to avoid a log
line on every HTTP page load. `UnfinishedForDate` is the second method with a
poller caller (tier 2's `RetryScheduler`, on a 30-minute timer, not a page
load), so it follows `ConsumedByDay`'s side of that split, not the
dashboard-only side. It logs AFTER delegating, exactly like `ConsumedByDay`,
so the logged counts reflect the real result:

```go
func (l *loggingReader) UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error) {
	result, err := l.inner.UnfinishedForDate(ctx, teslaIDs, date)
	logging.Note("Reader", "UnfinishedForDate", "analytics query: date=%s requested=%d unfinished=%d",
		date.UTC().Format("2006-01-02"), len(teslaIDs), len(result))
	return result, err
}
```

The file's own top-of-file comment ("Only `ConsumedByDay` ... logs. The
other three methods ... are silent pass-throughs") must be updated to name
both logging methods and the now-two remaining silent ones
(`OdometerDeltaByDay`, `BatteryLevelByDay`) — `LatestMetricsForVehicles`
stays silent (dashboard-only, no poller caller).

### D8 — Widening `Reader` is a cross-module compile risk, not only an analytics change

`RM40-analytics-add-battery-level-read` already hit this once: adding a
method to `Reader` broke two other modules' test doubles that implement the
full interface, because `go vet ./...` compiles every package's `_test.go`
files. Grepping this repo today finds the same four files still declaring a
fake `Reader`: `internal/app/processor_test.go`,
`internal/gateway/handlers/handlers_test.go`,
`internal/gateway/handlers/history_test.go`, and
`internal/gateway/handlers/external_charges_test.go`. Each needs a
one-method stub added (returning `nil, nil` or panicking on an unexpected
call, matching that file's own existing convention for an unused method) or
the whole repo fails to `go vet`. This is outside `internal/analytics`'s own
sandbox and is recorded as a leader-owned task in `tasks.md`, exactly as
`RM40`'s design.md recorded it as a leader fix in the same wave.

## Schema

**No database object changes in this tier.** No new table, column, index,
constraint, or view, and no migration. `vehicle_metrics.tesla_id` and
`.metric_date` already exist as `NOT NULL` original columns from the
table's baseline migration
(`internal/analytics/db/migrations/20260917000001_baseline.sql`), and the
table's `vehicle_metrics_tesla_date_unique` constraint
(`UNIQUE (tesla_id, metric_date)`, same migration, line 224) already gives
Postgres exactly the index this query's `NOT EXISTS` needs.

### Index Plan

| # | Read pattern | Served by |
|---|---|---|
| 1 | `UnfinishedVehicleIDsForDate`: for each unnested `tesla_id`, `EXISTS (... WHERE tesla_id = t.tesla_id AND metric_date = @metric_date)` | `vehicle_metrics_tesla_date_unique` (`UNIQUE (tesla_id, metric_date)`) — an equality lookup on both columns of a two-column unique index is the cheapest possible probe that index supports: one B-tree descent per candidate id, no heap access needed beyond the existence check itself. |
| 2 | `LatestMetricsForVehicles`, `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, `Recalculate`'s UPSERT/DELETE (unchanged, untouched by this tier) | Same indexes, unchanged (RM29/RM38/RM40 design.md) |

**No new index is warranted.** The declared read-heavy Performance-Profile
licenses spending write-side cost (a new index, denormalization) only when
it buys a real read-side win. Here the existing unique constraint already
serves the query with the cheapest access pattern Postgres has for an
equality probe — adding a second index on the same two columns in the same
order would duplicate work the constraint's own index already does
(`ai/go-conventions.md` §Read optimization: "A `UNIQUE (a, b)` constraint
already builds a btree that serves equality on `a`, point lookups on `(a,
b)` ... a separate index next to it repeats work the constraint already
does"), taxing every `Recalculate`/`Reconcile` UPSERT for zero read benefit.

## Signatures

`internal/analytics/analytics.go` — `Reader` gains one method, placed
immediately after `LatestMetricsForVehicles`:

```go
type Reader interface {
	// ... ConsumedByDay, OdometerDeltaByDay, BatteryLevelByDay,
	// LatestMetricsForVehicles unchanged ...

	// UnfinishedForDate returns, from teslaIDs, exactly the ids that have NO
	// vehicle_metrics row for date -- "not yet done" for that calendar day.
	// Backs the nightly poller's periodic retry: a returned id is a vehicle
	// this platform has not finished processing for date; an id absent
	// from the result already has a row and needs no retry.
	//
	// Takes teslaIDs as a plain []int64, not []vehicleref.Ref, unlike
	// LatestMetricsForVehicles above: the caller is a server-side job that
	// walks every registered vehicle across every account on a timer, with
	// no signed-in user and no single account to prove ownership against
	// (design.md D2). This method performs no ownership or registration
	// check of its own -- an id with no vehicle_metrics row counts as
	// unfinished even if it is not currently registered to any account.
	//
	// date is compared against vehicle_metrics.metric_date, this
	// platform's bare-calendar-date representation (UTC midnight) -- pass
	// the same representation ConsumedByDay/OdometerDeltaByDay's start/end
	// already use.
	//
	// A tesla_id repeated in teslaIDs appears at most once in the result
	// (design.md D4). If teslaIDs is empty, this returns an empty
	// (non-nil) slice and a nil error WITHOUT querying the database
	// (design.md D5). Otherwise it still never returns a bare nil on the
	// happy path, matching every other method on this port. Order of the
	// returned slice is ascending by tesla_id (design.md D6), unlike
	// LatestMetricsForVehicles's unspecified order.
	UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error)
}
```

`internal/analytics/db/query.sql` — new query (see D3 for the full SQL and
its doc comment content).

`internal/analytics/reader.go` — `vehicleMetricsStore` gains one method:

```go
type vehicleMetricsStore interface {
	// ... three existing methods unchanged ...
	UnfinishedVehicleIDsForDate(ctx context.Context, arg analyticsdb.UnfinishedVehicleIDsForDateParams) ([]int64, error)
}
```

`*analyticsdb.Queries` satisfies it automatically, exactly like this
interface's other three methods — no adapter needed. Concrete implementation:

```go
func (r *reader) UnfinishedForDate(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error) {
	if len(teslaIDs) == 0 {
		return []int64{}, nil
	}
	ids, err := r.metrics.UnfinishedVehicleIDsForDate(ctx, analyticsdb.UnfinishedVehicleIDsForDateParams{
		TeslaIds:   teslaIDs,
		MetricDate: dateFrom(date),
	})
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}
```

`dateFrom` is `mapping.go`'s existing helper (`pgtype.Date{Time: t, Valid:
true}`) — no new conversion helper needed. `ids` can come back `nil` from
the generated method when zero rows match (mirrors
`ChargeGapDatesByVehicleBetween`'s own generated shape), so the nil-guard
keeps the non-nil-empty-slice contract on the "every vehicle is done" path
too, not only the empty-input path.

`internal/analytics/query_log.go` — see D7 for the `loggingReader` addition
and the top-of-file comment update.

## Docs this change invalidates

- `internal/analytics/AGENTS.md` — the "Public interface (the port)" table's
  `Reader` row lists four methods; it must list five. Task in `tasks.md`.
- `kkpa/context/entities/vehicle-metrics/guide.md` — its `Reader` port
  bullet (currently: "`ConsumedByDay`, `OdometerDeltaByDay`,
  `BatteryLevelByDay`, `LatestMetricsForVehicles`") and its
  `internal/analytics/analytics.go` file-map row both name every `Reader`
  method by hand and must gain `UnfinishedForDate`. Task in `tasks.md`.
- `kkpa/context/architecture/nightly-cycle.md` — its "Port map" section
  gains a row for `UnfinishedForDate` once tier 2 wires the `NotDoneVehicles`
  adapter that actually calls it; that edit belongs to tier 2 (already
  recorded in tier 2's own `design.md` "Docs this change invalidates"), not
  here — this tier adds no caller.

None of these are edited until the corresponding task lands.

## Test contract — expected values, authored before the implementation

All fixtures use tesla_id values in the `920xxx` range to avoid colliding
with fixtures other test files in this package already own (mirrors this
module's existing per-file id-range convention, e.g.
`db_gap_writer_integration_test.go`'s `910xxx` range).

### Offline (pure Go, no database) — write this first

`reader_test.go`, using a fake `vehicleMetricsStore`:

- **TR-1**: `UnfinishedForDate(ctx, nil, anyDate)` and
  `UnfinishedForDate(ctx, []int64{}, anyDate)` both return `[]int64{}` (a
  non-nil, empty slice) and a nil error. The fake store's
  `UnfinishedVehicleIDsForDate` is never called — fails the test via
  `t.Fatal` if it is. Proves design.md D5's "no query on empty input" claim,
  which a database-backed test cannot observe directly.

### Database-backed (`TEST_DATABASE_URL`-gated) — write these last

New file `db_unfinished_integration_test.go`, seeding `analytics.vehicle_metrics`
directly (mirrors `db_monthly_sync_integration_test.go`'s
`seedMonthlyVehicleMetric` — no public writer creates a bare
`vehicle_metrics` row on demand). Fixture, date `D = 2026-09-20`:

| `tesla_id` | Row for `D − 1` (2026-09-19) | Row for `D` (2026-09-20) |
|---|---|---|
| 920001 | yes | yes |
| 920002 | yes | no |
| 920003 | no | no |

**TR-2**: `UnfinishedForDate(ctx, []int64{920001, 920002, 920003}, D)` →
`[]int64{920002, 920003}` (ascending, design.md D6). 920001 has a row for
`D` and is excluded; 920002 and 920003 do not and are both included —
920003 despite never having any row at all, proving design.md D2's "no row
means unfinished, full stop" rule.

**TR-3 (duplicates)**: `UnfinishedForDate(ctx, []int64{920002, 920002,
920003}, D)` → `[]int64{920002, 920003}` — each id once, not twice (design.md
D4).

**TR-4 (unregistered/unknown id)**: `UnfinishedForDate(ctx, []int64{920001,
920099}, D)` → `[]int64{920099}` — an id with no stored row of any kind is
reported unfinished exactly like one that has rows for other days but not
`D` (920099 never appears in the seeded table at all).

**TR-5 (every vehicle done)**: `UnfinishedForDate(ctx, []int64{920001}, D)`
→ `[]int64{}` — a non-nil, empty slice, not a bare `nil`, on the "nothing to
retry" path (design.md "Signatures", the `ids == nil` guard).

## Risks

- **Widening `Reader` breaks four out-of-module test fakes until they are
  patched (D8).** This tier's own `go vet ./...` inside
  `internal/analytics/` stays green, but a repo-wide `go vet ./...` will not
  until `internal/app/processor_test.go`,
  `internal/gateway/handlers/handlers_test.go`,
  `internal/gateway/handlers/history_test.go`, and
  `internal/gateway/handlers/external_charges_test.go` each gain a stub for
  the new method. `tasks.md` records this as a leader-owned task, mirroring
  how `RM40-analytics-add-battery-level-read` handled the identical fallout
  the last time this port grew.
- **`UnfinishedForDate` has no caller until tier 2 lands.** Tier order is
  forced by the roadmap (tier 2 depends on this tier); this is a
  temporarily uncalled port method, not a design flaw — `go build ./...`
  stays green because no production code is missing a method.
