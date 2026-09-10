# Design — RM52-analytics-add-monthly-metrics

Required because this change touches the database (`openspec/config.yaml` design gate).
Roadmap decisions RD1–RD11 of `openspec/roadmaps/RM52-vehicle-monthly-metrics.md` are binding
here and are not re-argued. Findings F1–F10 of that same file are verified facts this design
builds on, not re-checked.

## Overview

Three pieces, all inside `internal/analytics`:

- **Schema** — the RD11 table, exactly as approved at the database design gate.
- **Pure estimator** — RD3's filter-then-median, a plain function over a slice.
- **Calculator** — the impure layer: reads other modules' ports, groups by `tesla_id` (RD5),
  calls the estimator, writes the row. Also exposes the RD1 read-back method.

## Schema (RD11 — verbatim, do not change one character)

```sql
CREATE TABLE analytics.vehicle_monthly_metrics (
    id                              UUID   PRIMARY KEY DEFAULT gen_random_uuid(),
    tesla_id                        BIGINT NOT NULL,   -- RD5: no account_id, on purpose
    effective_period                DATE   NOT NULL,   -- always the 1st of the month (RD7)

    -- Metric 1: effective pack capacity (MAG-32).
    -- NULL = too few valid samples this period (RD4). Never a guessed number.
    effective_capacity_kwh          DOUBLE PRECISION,
    effective_capacity_sample_count INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vehicle_monthly_metrics_period_is_month_start
        CHECK (EXTRACT(DAY FROM effective_period) = 1),
    UNIQUE (tesla_id, effective_period)
);
```

**Rationale (RD11, restated for a future reader).** Wide, one column per metric, not
key/value: `sqlc` gives typed Go fields, a read needs no pivot, units keep their `_kwh`
suffix, and the DDL itself lists the whole vocabulary. A key/value table would hide that
vocabulary inside the data and give every reader a pivot to re-implement. The metric column
is nullable because a row means "this vehicle-month was processed" — each metric says on its
own whether it was measurable, which is what keeps RD4's promise while still leaving room for
a future metric to be present on a row where this one is not.

**Rejected alternatives (RD11):** key/value rows (loses the typed-column benefit above); an
`account_id` column (RD5 — capacity belongs to the pack, not the account); a `raw_data`
JSONB column (this table stores a Go-computed conclusion, not a vendor payload — the mandatory-
`raw_data` rule, `ai/go-conventions.md` §persistence, does not apply here); a foreign key on
`tesla_id` (would couple `analytics` migrations to the `account` schema — no cross-module FK
anywhere in this platform, `ai/architecture.md` §2).

### Index plan (RD11 — no separate `CREATE INDEX`)

One read path exists in this tier:

```sql
SELECT effective_capacity_kwh
  FROM analytics.vehicle_monthly_metrics
 WHERE tesla_id = $1
   AND effective_period <= $2
   AND effective_capacity_kwh IS NOT NULL
 ORDER BY effective_period DESC
 LIMIT 1;
```

Equality on the leading column, then a backwards range scan on the second — exactly what the
`UNIQUE (tesla_id, effective_period)` btree already serves, with no extra index. This mirrors
`vehicle_metrics` and `charge_gaps`, which both size their own read path against their own
UNIQUE constraint's btree instead of adding a second index. The write path (one UPSERT per
vehicle per month) is far too infrequent to justify trading write cost for a read benefit no
query needs (`ai/architecture.md` §7 — read-heavy, but this write is a monthly batch job, not
even the nightly one; there is nothing to protect it from).

### Migration

New file: `internal/analytics/db/migrations/20260909000002_add_vehicle_monthly_metrics.sql`.
`20260909000001` (`charging`, `add_price_source.sql`) is the closest existing timestamp
across every module's migration directory (checked: `find internal -path
'*/db/migrations/*.sql' | xargs -n1 basename | sort | tail -20`) — `20260909000002` collides
with nothing.

Down migration: `DROP TABLE analytics.vehicle_monthly_metrics;` — mirrors every other
`analytics` migration's Down block (no data-preservation attempt on a table this change
itself creates).

Column comments (for `sqlc`, mirroring `20260908000002`'s and `20260905000001`'s style):

```sql
COMMENT ON TABLE analytics.vehicle_monthly_metrics IS
    'One row per (tesla_id, effective_period) -- a vehicle''s measured pack capacity for '
    'one calendar month, plus room for future monthly metrics as new columns (RD10). No '
    'account_id: capacity belongs to the battery pack, and two accounts can register the '
    'same car (RD5) -- rows for every owning account are pooled into one measurement '
    'before this table is written. Written by analytics.MonthlyMetricsCalculator, read by '
    'analytics.PackCapacityReader.PackCapacityKWh.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.effective_period IS
    'Always the first calendar day of the month this row measures (RD7) -- enforced by '
    'vehicle_monthly_metrics_period_is_month_start. The caller resolves the month boundary '
    'through internal/clock before calling Calculate; this module does not call clock '
    'itself and performs no zone-aware normalization of its own.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.effective_capacity_kwh IS
    'The median pack capacity, in kWh, implied by this vehicle''s valid charge records for '
    'this month (RD2 filter, RD3 median-after-gate). NULL means fewer than minSamples (3) '
    'valid records were found -- never a guessed number (RD4). A reader wanting a value for '
    'a NULL month falls back to the newest earlier NOT NULL row.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.effective_capacity_sample_count IS
    'How many valid records (RD2), after the minDeltaPct gate (RD3), fed the capacity '
    'estimate -- populated even when it is below minSamples and effective_capacity_kwh is '
    'therefore NULL, so a reader can tell "found 2, needed 3" apart from "found 0".';
```

## Pure estimator (RD3)

New file `internal/analytics/monthly_capacity.go`.

```go
// minSamples and minDeltaPct are Go constants, never database values (RD3) -- tuning
// either is a code change plus a re-run, never a migration.
const (
    minSamples  = 3
    minDeltaPct = 15
)

// capacitySample is one valid charge record's implied pack capacity, reduced to the two
// numbers the estimator needs. Built by the calculator from an Entry or a Session
// (monthly_metrics.go) -- this file has no dependency on internal/charging's types, so it
// stays a pure function testable with a plain slice (RD3, ai/go-conventions.md testing
// order).
type capacitySample struct {
    CapacityKWh float64
    DeltaPct    float64
}

// estimateEffectiveCapacityKWh applies RD3: drop any sample whose battery delta is under
// minDeltaPct, require at least minSamples of what is left, then return the median.
// sampleCount is always the count AFTER the delta gate, even when ok is false -- a caller
// can tell "found 2, needed 3" apart from "found 0" (design rationale below).
func estimateEffectiveCapacityKWh(samples []capacitySample) (kwh float64, sampleCount int, ok bool) {
    valid := make([]float64, 0, len(samples))
    for _, s := range samples {
        if s.DeltaPct >= minDeltaPct {
            valid = append(valid, s.CapacityKWh)
        }
    }
    if len(valid) < minSamples {
        return 0, len(valid), false
    }
    sort.Float64s(valid)
    n := len(valid)
    if n%2 == 1 {
        return valid[n/2], n, true
    }
    return (valid[n/2-1] + valid[n/2]) / 2, n, true
}
```

### D1 — What `sampleCount` means when `ok` is false

**Decision:** `sampleCount` is always the count of samples that passed the `minDeltaPct` gate,
returned whether or not that count reaches `minSamples`.

**Rationale:** RD11's `effective_capacity_sample_count` column has `NOT NULL DEFAULT 0` — it
is written on every row, including a row whose capacity is `NULL`. A stored `0` there would
be indistinguishable from "found nothing" when the true story might be "found 2, needed 3".
The column's own comment above states this distinction is the reason it exists.

**Rejected:** returning `sampleCount = len(samples)` (before the gate) instead. Rejected
because that count would include rows the estimator never even considered a candidate — a
number that answers a different question than "how close were we".

### D2 — Even-count median takes the average of the two middle values

**Decision:** for an even number of valid samples, the estimate is the mean of the two
middle values after sorting — the standard statistical median, not "pick the lower/upper
of the two" and not "drop one sample to force an odd count".

**Rationale:** RD3 says "median" without specifying the even-count rule, so this design
settles it explicitly rather than leaving it to whichever branch an implementer happens to
write first. The standard definition is the one a future reader will assume without being
told, and it needs no extra Go import (`sort.Float64s` plus arithmetic).

**Rejected:** dropping the newest or oldest sample to force an odd count. Rejected because it
would make the result depend on an arbitrary tie-break the design would then have to justify
on its own, for no accuracy gain over the standard average.

### D3 — `DeltaPct` is a plain, unsigned battery-percentage delta

**Decision:** `capacitySample.DeltaPct` is `float64(EndBatteryPct - StartBatteryPct)` — no
absolute value, no clamping. The calculator only ever builds a `capacitySample` from a record
whose `InferredCapacityKWhCalc` is non-nil (D4 below), and that column is itself `GENERATED
ALWAYS AS` a formula that requires `EndBatteryPct > StartBatteryPct` (`charging.go`'s own
doc comment on `InferredCapacityKWhCalc`, F1) — so every sample the estimator ever sees
already has a strictly positive delta by construction. There is nothing to clamp.

## The calculator (impure layer)

New file `internal/analytics/monthly_metrics.go`.

### D4 — Which records are "valid" (RD2), read directly off existing fields

The calculator never re-derives capacity itself. It reads the two sources' own
`InferredCapacityKWhCalc` (F1, already public) and keeps a record only when:

| Source | Port method | Keep when |
|---|---|---|
| `charging.Entry` (manual) | `Reader.ListEntriesByVehicleBetween` | `EnergySource == EnergySourceUser` AND `InferredCapacityKWhCalc != nil` |
| `charging.Session` (Supercharger) | `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween` | `Status == SessionStatusDone` AND `InferredCapacityKWhCalc != nil` |

The `InferredCapacityKWhCalc != nil` check is a second, independent guard, not redundant with
the status/source check: a `USER` entry or a `DONE` session can still have a nil
`InferredCapacityKWhCalc` (missing percentages, or a Session with no kWh fee, F1's own doc
comment) — such a record carries no implied capacity to sample at all, valid provenance or
not.

`StartBatteryPct`/`EndBatteryPct` are read from the SAME record `InferredCapacityKWhCalc`
came from (both non-nil whenever `InferredCapacityKWhCalc` is non-nil, per D3 above) —
`capacitySample.DeltaPct = float64(*EndBatteryPct - *StartBatteryPct)`.

**Rejected:** re-deriving `energy / (delta/100)` from raw fields instead of reading
`InferredCapacityKWhCalc`. Rejected because that value is already computed, generated, and
tested by `internal/charging` itself (a Postgres `GENERATED ALWAYS AS` column, F1) —
recomputing it here would duplicate a formula this module has no reason to own a second copy
of, and could silently drift from it.

### D5 — Pooling by `tesla_id` across accounts (RD5)

The calculator's top-level loop:

```
owned := account.AllRegisteredVehicles(ctx)     // F9: every vehicle, every account
byTeslaID := group(owned, func(v) { return v.TeslaID })

for teslaID, vehicles := range byTeslaID {
    var samples []capacitySample
    for _, v := range vehicles {                 // every account owning this car (RD5)
        entries  := charging.ListEntriesByVehicleBetween(ctx, v.AccountID, teslaID, from, to)
        sessions := supercharger.ListSessionsByVehicleBetween(ctx, v.AccountID, teslaID, from, to)
        samples = append(samples, samplesFrom(entries, sessions)...)   // D4's filter
    }
    kwh, count, ok := estimateEffectiveCapacityKWh(samples)
    upsert(teslaID, period, kwh, count, ok)
}
```

`from`/`to` are the calendar month `period` names — `from = period`, `to =
period.AddDate(0, 1, -1)` (the last day of that month). This is plain arithmetic on the
`period` value the caller already supplied, not a "now" or "midnight" construction — see D9
below for why this module still imports no `internal/clock`.

**Rationale:** matches RD5's own worked example exactly — grouping happens BEFORE any read,
so the stored result never depends on which account the loop visits first. Today one account
owns one car, so this costs a handful of lines and changes nothing observable; it stops a
silent, order-dependent bug the moment a second account registers the same VIN (F8).

**Rejected:** computing one row per `(account_id, tesla_id)` pair and letting the last write
win. Rejected — this is exactly the bug RD5 exists to prevent: the stored value would flip
between runs depending on map/slice iteration order, and Go does not guarantee that order.

### D6 — `teslaID *int64` narrows the vehicle set, `AllRegisteredVehicles` stays the one source

**Decision:** `Calculate`'s `teslaID *int64` parameter (roadmap's own signature) filters the
grouped-by-`tesla_id` map from `AllRegisteredVehicles` down to one entry when non-nil, before
any charging read happens. It does not add a second code path or a second account lookup.

**Rationale:** keeps exactly one way to resolve "which vehicles, which accounts" — a single
call to `AllRegisteredVehicles` (F9) followed by an in-memory filter, mirroring D5's pooling
loop with one fewer iteration rather than a structurally different function.

### D7 — Two ports, BOTH new. `analytics.Reader` is not touched.

**Decision:**

```go
// Write side -- new interface, own file, mirrors Recalculator's existing shape.
type MonthlyMetricsCalculator interface {
    Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyMetricsReport, error)
}

// Read side -- ALSO a new interface, own file. Satisfies charging.PackCapacityReader
// structurally (RD1) -- same method shape, no import of charging's interface needed.
type PackCapacityReader interface {
    PackCapacityKWh(ctx context.Context, teslaID int64, at time.Time) (float64, bool, error)
}

func NewPackCapacityReader(pool *pgxpool.Pool) PackCapacityReader
```

`analytics.Reader` gains NO new method. Its five existing methods
(`RecentEfficiency`, `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`,
`LatestMetricsByAccount`) are unchanged, byte for byte.

**Rationale.** An earlier draft of this design added `PackCapacityKWh` to `analytics.Reader`
instead. That draft was wrong, and the leader's review caught it before any code existed: it
would have broken the build in two modules tier 1 does not own.

`analytics.Reader` already has fakes typed against its exact method set, outside
`internal/analytics`:

- `internal/app/processor_test.go:275` — `var _ analytics.Reader = fakeAnalyticsReader{}`
- `internal/gateway/handlers/handlers_test.go:219` and `:1162` — two more `analytics.Reader`
  fakes

`go vet` compiles `_test.go` files (`ai/go-conventions.md` §Testing — this is exactly the
signature-drift case that rule exists to catch). Widening `analytics.Reader` by one method
means every one of those three fakes stops satisfying the interface, so `make vet` fails in
both `internal/app` and `internal/gateway` the moment tier 1 lands — two modules tier 1 is
sandboxed away from and may not touch. It is worse than a cross-tier inconvenience:
`internal/gateway` has **no tier anywhere in RM52** (the roadmap's four tiers are `analytics`,
`app`, `charging`, `platform`) — nothing later in this roadmap would ever fix a break there.

RD10's naming table listing only `analytics.MonthlyMetricsCalculator` does not mean
`PackCapacityKWh` has no port of its own — that table names things an agent would grep for
(table, port, runnable, make target, KB), not an exhaustive interface inventory, and RD1
itself only requires that `charging.PackCapacityReader` be satisfied "structurally" by
whatever `cmd/` passes into `charging` in tier 4. Structural satisfaction needs the method to
exist on SOME value `cmd/` can construct and pass — it never requires that method to live on
`analytics.Reader` specifically, or on any interface `analytics.Reader` already is.

**Rejected:** adding `PackCapacityKWh` to the existing `Reader` interface (this design's own
prior draft). Rejected for the concrete, verified reason above — it breaks two modules tier 1
cannot touch and cannot fix, one of which (`gateway`) no later RM52 tier owns either. This is
not a style preference; it is a build break outside this tier's sandbox.

**`NewMonthlyMetricsCalculator(pool *pgxpool.Pool, account vehicleLookupAll, entries
charging.Reader, sessions charging.SuperchargerSessionAnalyticsReader)
MonthlyMetricsCalculator`** and **`NewPackCapacityReader(pool *pgxpool.Pool)
PackCapacityReader`** are two separate constructors, both forward-declared in `analytics.go`
and implemented in their own files — mirrors `NewGapWriter`'s and `NewRecalculator`'s existing
forward-declaration convention exactly (`analytics.go`'s own comment on `NewGapWriter` names
this pattern: a narrow constructor returning a narrow port, one pair per concern).
`vehicleLookupAll` is a new narrow consumer interface covering only `AllRegisteredVehicles` —
this module's existing `vehicleLookup` interface (`reader.go`) covers only
`RegisteredVehicles` (the per-account method), a different method `Calculate` does not call,
so a second narrow interface is added rather than widening the first one for a consumer that
does not need it.

`PackCapacityReader`'s implementation lives in its own new file, `capacity_reader.go`, with
its own concrete type (`packCapacityReader`) and its own narrow store interface
(`monthlyMetricsStore`, one method: `LatestMeasuredPackCapacityKWh`) — it does NOT reuse
`reader.go`'s `vehicleMetricsStore` interface or `reader` struct, because it is not part of
`Reader` and has no reason to share that type's constructor or its four unrelated
dependencies (`telemetry.Reader`, `charging.SuperchargerSessionAnalyticsReader`,
`charging.Reader`, `vehicleLookup`) — `PackCapacityReader` needs only a `*pgxpool.Pool`.

### D8 — `MonthlyMetricsReport` is the calculator's own return shape, not a database row

```go
// MonthlyMetricsReport is one Calculate call's result -- our own domain model, no vendor
// or sibling-module suffix (ai/architecture.md §6).
type MonthlyMetricsReport struct {
    Period  time.Time
    Results []VehicleMonthlyMetric
}

// VehicleMonthlyMetric is one vehicle's outcome for the period.
type VehicleMonthlyMetric struct {
    TeslaID                      int64
    EffectiveCapacityKWh         *float64 // nil = fewer than minSamples valid records (RD4)
    EffectiveCapacitySampleCount int
}
```

**Rationale:** tier 4's `cmd/monthly-metrics` (RD8) needs something to print per vehicle
after a run — "how many vehicles were processed, and did each get a number." Returning this
from `Calculate` avoids a second read call right after the write one; the caller already has
what it needs. This is the only new domain type besides the two ports above — no
over-engineering beyond what RD8's stated caller (the CLI) needs.

### D9 — `Calculate` takes an already-resolved `period`; this module still owns no clock

**Decision:** `Calculate(ctx, period time.Time, teslaID *int64)` treats `period` as already
the first calendar day of the target month (`pgtype.Date`-compatible, UTC-midnight
represented, matching every other bare-date value this module already handles — e.g.
`DayConsumption.Date`). `Calculate` does not call `internal/clock`, does not compute "now",
and does not itself decide which month to process — RD7 assigns that resolution to the
*caller* (tier 2's nightly step; RD8's `cmd/monthly-metrics` for a manual run), not to this
port.

Two defenses back this contract without adding a clock dependency:
1. The DB `CHECK (EXTRACT(DAY FROM effective_period) = 1)` constraint rejects a bad write.
2. `Calculate` itself validates `period.Day() != 1` before doing any read and returns an
   error immediately — cheaper than discovering the mistake at INSERT time, after every
   charging read already ran.

**Rationale.** This mirrors the module's own existing, explicit invariant (AGENTS.md §Allowed
imports): "this module still owns no `*time.Location` of its own... every bucketing call
passes `time.UTC` explicitly, because the values being bucketed are already-normalized...
the zone that decides day boundaries is applied upstream." `Calculate` extends that same
invariant to a month boundary instead of a day boundary — the zone-aware "what is the current
month in America/Bogota" decision belongs to tier 2, which already needs `internal/clock` for
its own trigger condition (RD7), not duplicated here.

**Rejected:** having `Calculate` accept any `time.Time` and normalize it to that value's own
month-start internally (e.g. `time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)`).
Rejected for two reasons: it duplicates a normalization tier 2 already has to do correctly in
`America/Bogota` before calling this port (doing it twice, in two zones, risks the two
disagreeing on which month a boundary moment belongs to); and that exact construction shape
(`, 0, 0, 0, 0, <zone>)`) is precisely what `make tz-guard` flags outside `internal/clock`,
which would force either a `// tz:allow:` escape hatch for a normalization this module has no
reason to own, or an unwanted `internal/clock` import for a value that is already resolved by
the time it reaches this port.

### D10 — `period.AddDate(0, 1, -1)` for month-end is not a tz-guard construction

**Decision:** the calculator computes the query window's upper bound as
`period.AddDate(0, 1, -1)` — add one month, subtract one day — never a hand-rolled
`time.Date(...)` call.

**Rationale:** `AddDate` operates on the `time.Time` value the caller already supplied,
preserving its existing location and time-of-day; it is ordinary calendar arithmetic on a
given value, not a "midnight of some day" construction, so it does not match `make
tz-guard`'s grep pattern (`ai/go-conventions.md`'s tz-guard section: a trailing `, 0, 0, 0, 0,
<zone>)` shape) and needs no escape-hatch comment.

## sqlc

`internal/analytics/db/query.sql` gains two queries:

```sql
-- name: UpsertVehicleMonthlyMetric :exec
-- Upsert one vehicle_monthly_metrics row (RD11). On conflict with the
-- (tesla_id, effective_period) UNIQUE constraint, refresh the two metric columns --
-- created_at is DELIBERATELY ABSENT from the SET clause, mirroring UpsertVehicleMetric's
-- and UpsertChargeGap's identical "preserve first-write timestamp" convention.
INSERT INTO analytics.vehicle_monthly_metrics (
    tesla_id, effective_period, effective_capacity_kwh, effective_capacity_sample_count
) VALUES (
    @tesla_id, @effective_period, @effective_capacity_kwh, @effective_capacity_sample_count
)
ON CONFLICT (tesla_id, effective_period) DO UPDATE SET
    effective_capacity_kwh          = EXCLUDED.effective_capacity_kwh,
    effective_capacity_sample_count = EXCLUDED.effective_capacity_sample_count,
    updated_at                      = now();

-- name: LatestMeasuredPackCapacityKWh :one
-- The RD1 read path: the newest MEASURED (NOT NULL) capacity at or before the requested
-- moment (RD4's fallback). Returns pgx.ErrNoRows when nothing qualifies -- the Go wrapper
-- (capacity_reader.go) maps that to (0, false, nil), never an error, matching every other
-- "no data yet" contract this module already has.
SELECT effective_capacity_kwh
  FROM analytics.vehicle_monthly_metrics
 WHERE tesla_id = @tesla_id
   AND effective_period <= @at
   AND effective_capacity_kwh IS NOT NULL
 ORDER BY effective_period DESC
 LIMIT 1;
```

`sqlc.yaml`'s existing `analytics` gen block gains one `rename` entry, following the existing
per-table pattern:

```yaml
rename:
  analytics_charge_gap:                "ChargeGap"
  analytics_vehicle_metric:            "VehicleMetric"
  analytics_vehicle_metric_watermark:  "VehicleMetricWatermark"
  analytics_vehicle_monthly_metric:    "VehicleMonthlyMetric"
```

Without this, sqlc's default inflection would keep the `analytics_` schema prefix on the
generated struct name, exactly as it would for the three existing tables without their own
rename entries (confirmed by reading the three existing entries — none of this module's four
tables gets away without one).

**`@at`'s generated Go type.** Because `@at` is compared directly against `effective_period`
(a `DATE` column) with no explicit cast, sqlc infers its parameter type as `pgtype.Date`, the
same type `effective_period` itself already generates — not `pgtype.Timestamptz`.
`capacity_reader.go`'s `PackCapacityKWh(ctx, teslaID, at time.Time)` therefore converts its
own `at time.Time` argument with the SAME `dateFrom` helper (`mapping.go`) the write path
already uses for `effective_period` — no new mapping helper. A caller passing a moment that
is not already a bare calendar day gets that moment's date component taken as-is (`dateFrom`
does no zone conversion of its own, matching D9's "this module owns no clock" stance) — the
same contract `Calculate`'s own `period` argument already carries.

## Makefile and guard check, in both directions (CLAUDE.md requirement)

Checked, not assumed:

- **`db-setup`/`db-reset` role-and-ownership assumptions:** unaffected. This migration adds
  one new table inside the `analytics` schema, which the app role already owns (every prior
  `analytics` migration already established this) — no new schema, no new role, no new grant.
- **`MIGRATIONS_DIRS`:** unaffected — no directory added, removed, or reordered. This
  migration reads no other module's table (unlike tier 1 of `RM50`'s cross-schema backfill);
  ordering relative to `account`/`telemetry`/`charging` has no effect on it.
- **`make migration-guard`:** `20260909000002`'s uniqueness across every module directory is
  checked above (Schema §Migration) — no collision.
- **`make boundary-guard`:** does not apply — no `internal/gateway` file is touched by this
  tier.
- **`sqlc`:** `internal/analytics/db/query.sql` and `sqlc.yaml` both change; `make sqlc`
  regenerates `internal/analytics/db/models.go` and `query.sql.go`. No other module's sqlc
  entry is touched.

## Test Contract

Authored before implementation (`ai/go-conventions.md` §Testing) — a later worker writes test
code against this, not against whatever the implementation happens to do.

### Pure function: `estimateEffectiveCapacityKWh` (offline, `monthly_capacity_test.go`)

**Fixture 1 — poisoned rows excluded (RD2).** Input carries five samples: three genuine
`USER`/`DONE`-sourced ones with capacities `58.0, 60.0, 62.4` (all deltas well above
`minDeltaPct`), plus two poisoned ones the calculator must never even construct a
`capacitySample` for — but since this is the pure layer, this fixture instead proves the
estimator's OWN half of RD2: a `capacitySample{CapacityKWh: 62.0, DeltaPct: 20}` mixed into
the three genuine ones still gets median-ed like any other value (the estimator has no way to
know a sample is poisoned — RD2's filter is the calculator's job, D4). **Expected:** with all
four samples above `minDeltaPct`, `estimateEffectiveCapacityKWh` returns the median of all
four (`60.0` and `62.0` are the two middle values of `[58.0, 60.0, 62.0, 62.4]` → `61.0`),
`sampleCount == 4`, `ok == true`. This fixture exists to pin down that the pure layer performs
NO source filtering of its own — see Fixture 5 in `monthly_metrics_test.go` for the
calculator-level exclusion this module's design actually relies on for RD2.

**Fixture 2 — small-delta row dropped (RD3).** Input: `{60.0, 25}, {61.0, 30}, {5.0, 3}`. The
third sample's `DeltaPct` (`3`) is under `minDeltaPct` (`15`). **Expected:**
`estimateEffectiveCapacityKWh` returns the median of the remaining two (`60.0` and `61.0` →
`60.5`), `sampleCount == 2`, `ok == false` (two is under `minSamples`, three) — this single
fixture also covers the "just below minSamples" case (D1).

**Fixture 3 — exactly 2 valid rows ⇒ NULL, sample count records what was found (RD4).**
Input: `{60.0, 20}, {61.5, 40}` — both pass the delta gate. **Expected:** `ok == false`,
`sampleCount == 2`, `kwh == 0` (the zero value — callers never read `kwh` when `ok` is
false, mirroring every other `ok=false` contract in this module, e.g. `RecentEfficiency`).

**Fixture 4 — even sample count, standard median (D2).** Input: four passing samples,
`{58.0, 20}, {60.0, 25}, {62.0, 30}, {64.0, 35}`. **Expected:** `ok == true`,
`sampleCount == 4`, `kwh == 61.0` (mean of the two middle values `60.0` and `62.0`) — pins
down D2's decision so an implementer cannot substitute "pick the lower of the two" or "drop
one sample" without failing this test.

**Fixture 5 — odd sample count, single middle value.** Input: three passing samples,
`{58.0, 20}, {61.0, 25}, {70.0, 40}`. **Expected:** `ok == true`, `sampleCount == 3`,
`kwh == 61.0` (the single middle value after sorting — `70.0` does not pull the median toward
it, proving the median ignores an outlier without any outlier-specific code, matching RD3's
own stated rationale for choosing a median over a mean).

**Fixture 6 — empty input.** `estimateEffectiveCapacityKWh(nil)`. **Expected:** `ok == false`,
`sampleCount == 0`, `kwh == 0` — no panic, no divide-by-zero (guards `len(samples) == 0`
alongside every other under-`minSamples` case, no special-casing needed since `0 < minSamples`
already).

### Calculator: RD2 filter + RD5 pooling (offline, `monthly_metrics_test.go`, hand-written
fakes of `charging.Reader`, `charging.SuperchargerSessionAnalyticsReader`, and the new
`vehicleLookupAll` — mirrors `reader_test.go`'s existing fake-port pattern)

**Fixture A — a poisoned row is excluded end-to-end (RD2).** One account, one vehicle. The
fake `charging.Reader` returns two `Entry` rows for the requested month: one
`EnergySource: EnergySourceUser, InferredCapacityKWhCalc: &60.0, StartBatteryPct: &20,
EndBatteryPct: &50` (valid, delta 30) and one `EnergySource: EnergySourceEstimated,
InferredCapacityKWhCalc: &62.0, StartBatteryPct: &10, EndBatteryPct: &40` (poisoned — must be
dropped regardless of its delta). The fake `SuperchargerSessionAnalyticsReader` returns one
`Session{Status: SessionStatusDoneCalculated, InferredCapacityKWhCalc: &62.0,
StartBatteryPct: &10, EndBatteryPct: &70}` (poisoned — `DONE_CALCULATED`, not `DONE`).
**Expected:** `Calculate` upserts a row whose `EffectiveCapacitySampleCount` reflects only the
ONE valid entry surviving to the estimator (which is then itself below `minSamples`, so
`EffectiveCapacityKWh` is nil) — proving the poisoned rows never reach
`estimateEffectiveCapacityKWh` at all, not merely that they fail to change its median.

**Fixture B — an in-progress session is excluded (RD2).** Same shape as Fixture A, but the
Supercharger fixture is `Session{Status: SessionStatusInProgress, InferredCapacityKWhCalc:
nil}`. **Expected:** the session contributes nothing — `InferredCapacityKWhCalc == nil` alone
would already exclude it (D4's second guard), independent of `Status`; this fixture confirms
neither guard is load-bearing on its own being skipped by a bug in the other.

**Fixture C — pooling across two accounts owning the same `tesla_id` (RD5).** `teslaID = 123`
registered to `accountA` and `accountB` (`AllRegisteredVehicles` returns both). `accountA`'s
fake `Reader` returns two valid `USER` entries (deltas 30, 35, capacities `60.0`/`61.0`).
`accountB`'s fake `Reader` returns one valid `USER` entry (delta 40, capacity `62.0`).
**Expected:** ONE row is upserted for `teslaID = 123`, with `EffectiveCapacitySampleCount ==
3` and `EffectiveCapacityKWh == 61.0` (median of all three, pooled) — never two separate rows,
and never a result that depends on account iteration order (run the fixture with the fake's
account list reversed as a second sub-test; the stored result must be identical).

**Fixture D — `teslaID` filter narrows to one vehicle.** `AllRegisteredVehicles` returns two
vehicles under two different `tesla_id`s. `Calculate` is called with `teslaID` pointing at
only one of them. **Expected:** the returned `MonthlyMetricsReport.Results` contains exactly
one entry, for the requested vehicle; no charging read is made for the other vehicle's
account (assert on the fake's call count/log, mirroring this module's existing fake-port test
style).

**Fixture E — a thin month writes `sample_count` even though it writes no capacity.**
One vehicle, one valid entry (below `minSamples`). **Expected:** the upserted row has
`effective_capacity_kwh IS NULL` and `effective_capacity_sample_count = 1` (matches Fixture 3
of the pure-layer contract, exercised here through the full calculator path with a real
`Entry`).

### DB-integration: write-then-read round trip (`db_monthly_metrics_integration_test.go`,
`testdb.ProvisionDirs` — this module's fixtures span `account` and `charging` tables, same
reason `db_integration_test.go` already needs `ProvisionDirs` over `Provision`)

**Fixture F — full round trip.** Seed one account (`account.NewService(pool).SeedVehicles`,
the existing public writer), three `USER` manual entries via `charging.NewWriter(pool).Create`
with deltas/capacities chosen to produce a known median, all dated inside the target month.
Call `Calculate` for that month. **Expected:** `analytics.vehicle_monthly_metrics` holds
exactly one row for `(tesla_id, effective_period)` with the expected `effective_capacity_kwh`
and `effective_capacity_sample_count`. Then, via `analytics.NewPackCapacityReader(pool)`,
call `PackCapacityKWh(ctx, teslaID, at)` with `at` inside the same month. **Expected:**
returns `(expectedKWh, true, nil)`.

**Fixture G — RD4 fallback to an earlier measured month.** Seed month M with a valid
measurement (as Fixture F), and leave month M+1 completely unprocessed (no row at all — not
even a thin-month NULL row, since `Calculate` was never called for it). **Expected:**
`PackCapacityKWh(ctx, teslaID, atInsideMonthM+1)` returns `(month M's kwh, true, nil)` — the
newest row at or before the requested moment, even though it is a full calendar month
earlier.

**Fixture H — no row at all yet.** A `tesla_id` with zero `vehicle_monthly_metrics` rows.
**Expected:** `PackCapacityKWh` returns `(0, false, nil)`, never an error — this is what lets
tier 3's `charging` fall back to its existing `62.0` constant on a brand-new vehicle.

**Fixture I — a Supercharger `DONE` session contributes through the real generated column.**
Seed one Supercharger session directly (`INSERT INTO charging.supercharger_sessions ...`,
`status = 'DONE'`, both battery percentages set so Postgres's own `GENERATED ALWAYS AS`
computes a real `inferred_capacity_kwh_calc` — mirrors `db_integration_test.go`'s existing
direct-INSERT precedent for this exact table, since `SessionWriter.MirrorSessions` carries no
battery-percentage fields and `SessionStatus` is never settable through any port, F1/F3).
Combine with two `USER` manual entries in the same month so the pooled sample count reaches
`minSamples`. **Expected:** the session's own real, database-computed capacity is included in
the median — proving the calculator reads `InferredCapacityKWhCalc` off the live generated
column, not a value it re-derives itself (D4's rejected alternative).

## Files touched (for the implementing worker)

- `internal/analytics/db/migrations/20260909000002_add_vehicle_monthly_metrics.sql` — new.
- `internal/analytics/db/query.sql` — two new queries (above).
- `sqlc.yaml` — one new `rename` entry in the `analytics` gen block.
- `internal/analytics/db/models.go`, `query.sql.go` — regenerated by `make sqlc`, not
  hand-edited.
- `internal/analytics/analytics.go` — package comment fix; new `MonthlyMetricsCalculator`
  interface; new `PackCapacityReader` interface (D7 — `analytics.Reader` is NOT touched);
  new `MonthlyMetricsReport`/`VehicleMonthlyMetric` domain types;
  `NewMonthlyMetricsCalculator` and `NewPackCapacityReader` forward-declared constructors.
- `internal/analytics/monthly_capacity.go` — new: `minSamples`/`minDeltaPct` constants,
  `capacitySample`, `estimateEffectiveCapacityKWh`.
- `internal/analytics/monthly_metrics.go` — new: `vehicleLookupAll` narrow interface, the
  `monthlyMetricsCalculator` concrete type, `Calculate`'s implementation (D4/D5/D6/D9/D10).
- `internal/analytics/capacity_reader.go` — new: the `monthlyMetricsStore` narrow interface
  (`LatestMeasuredPackCapacityKWh`), the `packCapacityReader` concrete type, and
  `PackCapacityKWh`'s implementation (D7). Does not touch `reader.go`, `reader`, or
  `vehicleMetricsStore` — those are `Reader`'s own file and stay exactly as they are today.
- `internal/analytics/mapping.go` — reused as-is (`dateFrom`, `pgFloat8FromPtr`,
  `ptrFloat64FromPg`); no new helper needed.
- `internal/analytics/monthly_capacity_test.go` — new, Fixtures 1–6.
- `internal/analytics/monthly_metrics_test.go` — new, Fixtures A–E.
- `internal/analytics/db_monthly_metrics_integration_test.go` — new, Fixtures F–I.
- `internal/analytics/AGENTS.md` — document the new table, port methods, and domain types
  (see `tasks.md`).
- `kkpa/context/` — grepped for `analytics` per this change's docs task; the KB guide
  `vehicle-monthly-metrics` itself is tier 4's job (RD10/roadmap tier 4 item 5), not this
  tier's.

No file outside `internal/analytics` (plus `sqlc.yaml`, a root file whose `analytics:` gen
block is this module's own configuration) changes in this tier.
