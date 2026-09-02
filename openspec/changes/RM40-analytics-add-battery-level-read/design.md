# Design — RM40-analytics-add-battery-level-read

## Context

`vehicle_metrics` (`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql`,
`RM29-analytics-add-vehicle-metrics`) is analytics' precomputed daily read model: one
row per `(account_id, tesla_id, metric_date)` for every day that has a
`telemetry.Snapshot`, dense (a row exists even for a predecessor-less day), written by
`Recalculator.Recalculate`/`Reconcile`, read by `Reader`. It already carries three raw
observations that are always populated regardless of predecessor existence:
`battery_level_pct INTEGER NOT NULL`, `odometer_km DOUBLE PRECISION NOT NULL`,
`battery_range_km DOUBLE PRECISION NOT NULL` (migration lines 54, 55, 56).

`RM40-gateway-drop-telemetry-dependency` (the roadmap this tier belongs to) found, by
reading the actual gateway code rather than assuming from the ticket, that exactly one
production `telemetry.Reader` call site remains in `internal/gateway/`:
`internal/gateway/handlers/history.go:321`, the battery-history chart, which reads
only three fields off `telemetry.Snapshot` — `EffectiveDate`, `BatteryLevelPct`,
`BatteryRangeKm` (roadmap findings table, D3). `internal/analytics` already stores
both value fields as `NOT NULL` original columns and already serves the gateway's two
sibling history charts (`ConsumedByDay` for the consumption chart, `OdometerDeltaByDay`
for the odometer chart) from this same table. This tier adds the fourth sibling
method, `BatteryLevelByDay`, so tier 2 (a separate change, depends on this one) can
retarget the gateway's one remaining call site here instead of `telemetry`.

This tier does not touch the gateway. It only makes the read exist; tier 2 is the only
place the call site, `Deps`/`Handler` struct fields, and the two `_test.go` fakes
change.

## Goals / Non-Goals

**Goals:**
- Add `DayBattery` (`Date time.Time`, `BatteryLevelPct int`, `BatteryRangeKm float64`)
  to `analytics.go`, completing the existing `DayConsumption`/`DayDistance`/`DayBattery`
  vocabulary (roadmap D2).
- Add `analytics.Reader.BatteryLevelByDay(ctx, accountID, teslaID, start, end)
  ([]DayBattery, error)`, mirroring `ConsumedByDay`/`OdometerDeltaByDay`'s signature
  shape, precomputed-and-sparse contract, and non-nil-empty-slice rule — with the one
  deliberate departure documented in D1 below.
- Add `VehicleMetricsBatteryByVehicleBetween` to `internal/analytics/db/query.sql`,
  regenerate `internal/analytics/db/*.go` via `sqlc generate`.
- Extend `reader.go`'s `vehicleMetricsStore` narrow consumer interface and implement
  `BatteryLevelByDay` on the concrete `reader`, with no derivation logic — pure
  row-to-domain mapping, mirroring `ConsumedByDay`'s own "no derivation logic here"
  convention.
- Settle and justify the index plan for the new read pattern, even though no database
  object changes (roadmap D4/D5 findings; `database` design gate still requires a
  stated Index Plan).
- Update `internal/analytics/AGENTS.md`'s "Public interface (the port)" section with
  the new method (`ai/go-conventions.md`/`CLAUDE.md` docs-track-structural-change).

**Non-Goals (explicitly deferred, do not implement here):**
- Consuming `BatteryLevelByDay` from the gateway, or touching any gateway file at
  all — tier 2's entire job.
- Removing `internal/telemetry`/`telemetry.Snapshot` from gateway code — tier 2.
- Any new database object (table, column, index, constraint, view, migration) —
  none is needed; see D-index below.
- Backfilling any historical row — not applicable; no row this tier could backfill is
  affected, since it reads existing `NOT NULL` columns as-is.

## Decisions

Decisions below carry the roadmap's own D1–D8 numbering where they restate a roadmap
decision verbatim (cross-referenced explicitly), plus one design-pass-local decision
(D-index) the roadmap left to this document to state formally.

### D1 (roadmap D1) — `internal/analytics` owns the read

Rejected: keeping the read on `internal/telemetry` behind a gateway-local interface.
That would satisfy `make boundary-guard`'s letter (no `internal/telemetry` import
named directly in gateway code) while leaving the gateway depending on telemetry at
runtime through an interface the guard cannot see — exactly the loophole the guard
exists to close. `analytics` already owns `vehicle_metrics` and already serves the two
sibling history charts from it, so this is the module the data lives in, not merely a
module that could be made to hold an interface over someone else's data.

### D2 (roadmap D2) — One new method on the existing `Reader` port, name and shape fixed

`BatteryLevelByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start,
end time.Time) ([]DayBattery, error)` — not a new port, not a differently-shaped
method. It mirrors `ConsumedByDay`/`OdometerDeltaByDay` exactly: same four-argument
shape (`ctx`, `accountID`, `teslaID`, `start`/`end` as whole calendar days,
UTC-midnight-represented, `end` inclusive — this platform's HTTP date-filter
convention), same precomputed-not-recomputed-on-read contract (SELECTs from
`vehicle_metrics`, no live derivation), same non-nil-empty-slice rule (`make([]DayBattery,
0, len(rows))`, never a bare `nil` return on the happy path). The result struct is
`DayBattery`, completing the closed vocabulary `DayConsumption` / `DayDistance` /
`DayBattery` — reusing the established names is deliberate: a future agent or reviewer
looks the pattern up once instead of re-inventing a fourth naming scheme.

```go
type DayBattery struct {
    Date            time.Time
    BatteryLevelPct int
    BatteryRangeKm  float64
}
```

### D3 (roadmap D3) — NO `IS NOT NULL` filter — the one deliberate departure

This is the one place `VehicleMetricsBatteryByVehicleBetween` departs from the shape
it otherwise mirrors exactly. Both sibling queries
(`VehicleMetricsConsumedByVehicleBetween`, `VehicleMetricsOdometerByVehicleBetween`)
filter `... IS NOT NULL` on their respective `_calc` column, because those columns are
genuinely `NULL` on a predecessor-less day (`vehicle_metrics`' own dense-table design,
RM29 D9) — the filter excludes a row that has no computable value to report.
`battery_level_pct` and `battery_range_km` are declared `INTEGER NOT NULL` /
`DOUBLE PRECISION NOT NULL` respectively (migration lines 54, 56) — raw per-day
observations copied verbatim from that day's own `telemetry.Snapshot`, with no
predecessor requirement at all, exactly like the three pre-existing raw observations
`vehicle_metrics` already carries unconditionally (mirroring the reasoning
`RM38-analytics-add-vehicle-status-columns` design D3 already established for its own
eight always-populated columns).

**Rejected — filter on `battery_used_pct_calc IS NOT NULL` for consistency with the
two siblings**: would silently drop every predecessor-less day (a vehicle's first
tracked day, or any day following a multi-day capture gap) from the battery chart even
though `battery_level_pct`/`battery_range_km` are fully known for that day — a
strictly worse answer with zero correctness justification, and precisely the "first
day silently has less data than every later day" asymmetry the dense-table design
(RM29 D9) exists to avoid for columns that do not require a predecessor. Since
`battery_level_pct`/`battery_range_km` are `NOT NULL`, this filter would also be
unreachable dead code in the other direction — no row in the table can ever fail an
`IS NOT NULL` check on a `NOT NULL` column — so omitting it is not just correct, it is
the only sensible choice.

### D4 (roadmap D4) — `DayBattery.Date` is a FINAL bucket key

`DayBattery.Date` is `vehicle_metrics.metric_date` — the row's own already-effective
calendar day — read back verbatim, exactly as `DayConsumption.Date` and
`DayDistance.Date` already are. A future gateway consumer (tier 2) must bucket on it
directly and must NEVER re-project it through `internal/gateway/handlers/history.go`'s
`effectiveDayUTC` — that function exists to convert a raw `captured_at` timestamp into
an effective day, a conversion `metric_date` has already had applied once, at
`Recalculate` time. Re-applying it a second time would shift the day by one, exactly
the bug class `ConsumedByDay`'s own doc comment already warns callers against for its
own `Date` field.

### D5 (roadmap D5) — no lookback in this port; the gateway's own lookback removal is tier 2's job

`ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay` all return exactly
`[start, end]` — none of the three widens its own window. The gateway's existing
`buildHistoryView`'s `readStart := start.AddDate(0, 0, -1)` one-day lookback exists
only because the current, telemetry-backed battery read has to convert a raw
`captured_at` timestamp into `EffectiveDate` at read time, which requires seeing one
extra day of raw captures. `metric_date` is already the effective day — no such
conversion happens against `vehicle_metrics` — so a caller of `BatteryLevelByDay`
needs no lookback to get an accurate `[start, end]` result. This tier does not remove
the gateway's lookback line (it is not gateway code); it is recorded here only so
tier 2 knows the removal is safe and expected once it switches call sites.

### D6 (roadmap D6) — day-coverage difference accepted, not backfilled

`vehicle_metrics` holds a row only for a day `Recalculate`/`Reconcile` has processed;
`vehicle_snapshots` (the table the gateway currently reads for this chart) holds every
raw capture the moment it lands. A day with no `vehicle_metrics` row yet (because the
nightly recalculation watermark has not caught up) yields no `DayBattery` entry for
that day — the sparse contract this port shares with its two siblings. Once tier 2
switches the gateway's read, that day renders as the chart's existing empty "no data"
bar — the same rendering a missing `vehicle_snapshots` row already produces today, so
this is not a new UI state, only a (typically single-day-lagging) shift in which table
produces it. **This is accepted, not backfilled**: in steady state, `vehicle_metrics`
coverage tracks `vehicle_snapshots` 1:1 up to the recalculator's own watermark lag, so
the visible effect is bounded to a recent day briefly showing an empty bar until the
next nightly `Reconcile` catches up. Adding a backfill tier was rejected — it would
turn a clean, no-database-object refactor into a DB-touching data change for a
cosmetic, self-healing, one-day-wide gap.

### D-index — no new index, no migration, none needed

This tier creates and alters NO database object at all: no table, no column, no
index, no constraint, no view, no migration. The three columns
`VehicleMetricsBatteryByVehicleBetween` needs — `metric_date`, `battery_level_pct`,
`battery_range_km` — already exist as original, `NOT NULL` columns from the table's
very first migration (`20260821000001_add_vehicle_metrics.sql:54,56`, plus
`metric_date` from the same `CREATE TABLE`). The query's `WHERE` clause
(`account_id = @account_id AND tesla_id = @tesla_id AND metric_date BETWEEN
@start_date AND @end_date`) is byte-identical in shape to both sibling queries'
`WHERE` clauses, differing only in the absent trailing `IS NOT NULL` predicate (D3).
It is therefore served by the exact same index those two siblings already rely on —
`vehicle_metrics_account_tesla_date_unique`, the `UNIQUE (account_id, tesla_id,
metric_date)` constraint from the original migration (line 83) — with no new index
required. See "Index Plan" below for the full justification against this project's
declared read-heavy Performance-Profile.

**Rejected — adding a new index "for symmetry" with the two siblings' own Index Plan
sections**: both siblings' Index Plans conclude the same existing index already serves
them; there is nothing for a new index to add here that isn't already true for them.
Adding one anyway would impose a write-cost tax on every `Recalculate`/`Reconcile`
UPSERT for zero read-side benefit — the opposite of what the read-heavy
Performance-Profile licenses (it licenses spending write-side cost for a read-side
gain that does not exist here).

## Database Changes (design gate — no schema change; Index Plan required regardless)

**No migration.** No `CREATE TABLE`, `ALTER TABLE`, `CREATE INDEX`, or `COMMENT ON`
statement is part of this change. `vehicle_metrics`' schema is unchanged from
`20260821000001_add_vehicle_metrics.sql`.

### Index Plan

| # | Read pattern | Served by |
|---|---|---|
| 1 | `BatteryLevelByDay`: `WHERE account_id = $1 AND tesla_id = $2 AND metric_date BETWEEN $3 AND $4`, no residual predicate (D3), `ORDER BY metric_date` | `vehicle_metrics_account_tesla_date_unique` (`UNIQUE (account_id, tesla_id, metric_date)`) — `account_id` leads (this project's "account_id is the leading index column" convention, `ai/go-conventions.md` §Read optimization), `tesla_id` narrows further, and `metric_date` both satisfies the `BETWEEN` range and the `ORDER BY` in the same forward index scan. Byte-identical index usage to `VehicleMetricsConsumedByVehicleBetween`/`VehicleMetricsOdometerByVehicleBetween`'s own proven Index Plan entries (RM29 design.md) — this query differs from theirs only in the absent trailing `IS NOT NULL` residual filter, which affects rows evaluated after the index scan, not which index serves the scan itself. |
| 2 | `Recalculate`'s UPSERT / DELETE, `ConsumedByDay`, `OdometerDeltaByDay`, `LatestMetricsByAccount` (unchanged, untouched by this tier) | Same indexes, unchanged (RM29/RM38's own Index Plans) |

**No new index is warranted.** Justified against the declared read-heavy
Performance-Profile (`CLAUDE.md` "Pipeline config"): that profile licenses spending
*write-side* cost (extra indexes, denormalization) to buy a *read-side* win when one
exists. Here no such trade is available — the existing unique constraint's index
already delivers a single ordered index scan for this exact `WHERE`/`ORDER BY` shape,
with zero residual sort (the `ORDER BY metric_date` matches the index's own trailing
column) and zero residual scan cost beyond the rows the `BETWEEN` predicate already
selects (D3's absent filter removes a per-row check, it does not add one). Adding a
second index here would only tax every nightly `Recalculate`/`Reconcile` UPSERT for a
read that is already optimal — the Performance-Profile's write-side latitude exists to
buy real read wins, not to be spent reflexively.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

No new unit tests are authored in this change (D7 — see proposal.md "Unit tests").
This section fixes the expected values a later `db_integration_test.go` addition (in
tier 1's own scope, if the implementing worker judges a DB-integration case adds real
coverage beyond what `ConsumedByDay`/`OdometerDeltaByDay`'s existing DB tests already
prove about this table) or a future consumer's test double must produce, authored
before the implementation exists — a test written after reading the implementation
would confirm what the code does rather than what this design specifies.

### Fixture RM40-A — two consecutive days, both without a predecessor requirement

`accountID = A`, `teslaID = 42`. Two `vehicle_metrics` rows exist, written by
`Recalculate` from two `telemetry.Snapshot`s where the SECOND has no locally-available
predecessor (so its `_calc` columns and `consumed_pct` are `NULL` — the identical
predecessor-less shape RM29 Fixture C already establishes for this table), while both
rows' `battery_level_pct`/`battery_range_km`/`metric_date` are fully populated as they
always are (D3):

| `metric_date` | `battery_level_pct` | `battery_range_km` | `battery_used_pct_calc` (predecessor-gated, for contrast) |
|---|---|---|---|
| `2026-08-10` | `70` | `300.0` | `5` (has a predecessor) |
| `2026-08-11` | `65` | `280.0` | `NULL` (no predecessor) |

**Expected `BatteryLevelByDay(A, 42, 2026-08-10, 2026-08-11)`:**

```go
[]DayBattery{
    {Date: 2026-08-10, BatteryLevelPct: 70, BatteryRangeKm: 300.0},
    {Date: 2026-08-11, BatteryLevelPct: 65, BatteryRangeKm: 280.0},
}
```

**Contrast with `ConsumedByDay(A, 42, 2026-08-10, 2026-08-11)`** on the identical two
rows: it returns exactly ONE entry (`2026-08-10`) — `2026-08-11` is excluded by its
`battery_used_pct_calc IS NOT NULL` filter. This is the fixture that proves D3: the
same underlying rows produce two entries from `BatteryLevelByDay` but only one from
`ConsumedByDay`, because only the latter has a predecessor requirement to enforce.

### Fixture RM40-B — a day with no `vehicle_metrics` row at all (sparse)

`accountID = A`, `teslaID = 42`. No `vehicle_metrics` row exists for `2026-08-12` (the
recalculator has not yet processed that day).

**Expected `BatteryLevelByDay(A, 42, 2026-08-12, 2026-08-12)`:** `[]DayBattery{}` — a
non-nil, empty slice, no error. This is the sparse contract (roadmap D6): absence of a
row is the only "no data" signal; the method never fabricates a zero-valued
`DayBattery` for a day with no stored row.

### Fixture RM40-C — empty window

`accountID = A`, `teslaID = 42`, `start > end` or a window containing no
`vehicle_metrics` rows for this vehicle at all (e.g. a brand-new vehicle with zero
rows yet).

**Expected `BatteryLevelByDay(A, 42, start, end)`:** `[]DayBattery{}` — a non-nil,
empty slice, no error. Mirrors `ConsumedByDay`/`OdometerDeltaByDay`'s identical
empty-window behavior.

### Tenant isolation (mirrors every other query in this module)

Given a second account `B` with its own `vehicle_metrics` row for `teslaID = 42` on a
date within `[start, end]`, `BatteryLevelByDay(A, 42, start, end)` never returns
account `B`'s row — the `account_id = @account_id` predicate scopes every result to
the caller's own tenant, identical to `ConsumedByDay`/`OdometerDeltaByDay`'s own
tenant-isolation guarantee.

## Risks / Trade-offs

- **`BatteryLevelByDay` has no caller until tier 2 lands.** Tier order is forced
  (roadmap requires tier 1 before tier 2) — this is a temporarily "dead" port method,
  not a design flaw. `go vet`/`go build` do not flag an unused exported method, so
  this carries no build-signal risk.
- **The day-coverage gap between `vehicle_metrics` and `vehicle_snapshots` (D6)** is a
  real, if narrow and self-healing, behavior change once tier 2 lands — a recent day
  can briefly render an empty bar it would not have rendered against
  `vehicle_snapshots` directly. Accepted per roadmap D6; not actionable within this
  tier since this tier introduces no gateway-visible behavior at all.
- **No new test coverage is added by this tier alone (D7).** The Test Contract above
  exists so that whichever change (this tier or a follow-up) does add DB-integration
  coverage for `BatteryLevelByDay` has fixed, pre-agreed expected values to implement
  against, rather than deriving them post hoc from the implementation.

## Verification signals

Per the Test-Execution-Policy: the implementing worker runs and reports `go build
./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, and the
standalone guards (`make ui-guard`/`make i18n-guard`/`make money-guard`/`make
tz-guard`/`make boundary-guard` are all no-ops for this tier — no gateway code, no
user-facing string, no monetary or raw-time-zone code, and no gateway `telemetry`
import is touched by a change confined to `internal/analytics/`; `make
migration-guard` is a no-op too — no migration file is added) — never `go test
./...` / `make test` / `make test-with-db` / `make check`. The owner runs `go test
./internal/analytics/...` and reports results; until then this tier's implementation
status is **awaiting-user-verification**, never "done."
