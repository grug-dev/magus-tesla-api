# Design — RM67-analytics-add-vehicle-monthly-metrics

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **RD1–RD10** in
> `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md`. RD1–RD10 are settled and
> binding; this document does not re-open them. D1–D8 below are the decisions this
> artifacts pass had to make to turn the tier's scope statement into a buildable
> table and use case.

## Context

Four facts shape everything below.

1. **`vehicle_metrics` already carries every input this tier needs, dense per
   day.** `metric_date`, `distance_traveled_km_calc`, and `consumed_pct` are the
   three columns this tier reads. The last two are `NULL` together, on the same
   day, whenever that day has no locally-available predecessor snapshot — this is
   an existing, documented guarantee (`internal/analytics/AGENTS.md`), not a new
   rule this tier introduces. A "computable day" is a row where both are
   non-`NULL`.
2. **`charging.MonthlyCapacityReader.CapacityForMonth`, built by tier 1, already
   answers the exact question this tier needs.** It takes any day inside a
   target month and returns three states: no row, a row with no measured
   capacity, and a row with a measured capacity — see the archived
   `RM67-charging-add-monthly-capacity-read/design.md`. This tier is that port's
   first real caller.
3. **Every column in the new table is `NOT NULL DEFAULT 0`, by roadmap decision
   (RD4).** A stored `0` can mean two different things — "we measured a genuine
   zero" or "we had nothing to measure" — and the column value alone cannot
   tell them apart. Every design choice below about count columns exists to
   recover that lost signal (RD5).
4. **A month-normalizing "midnight of a day" construction in Go is exactly what
   `make tz-guard` exists to catch.** Tier 1 already solved this once, in SQL,
   for its own read (`design.md` D4 of the archived tier 1 change). This tier
   copies that same shape rather than re-deriving it in Go.

## D1 — Table shape, full column list, rationale, and index plan (database design gate)

This is the schema the reviewer approves or rejects. See "Database Changes" below
for the full `CREATE TABLE`, per-column meaning, and the index plan.

**Summary of the shape:** one row per `(tesla_id, period)`. Three buckets — all
days, weekdays, weekends — each carrying the same four figures
(`distance_km`, `consumed_pct`, `km_per_pct_calc`, `day_count`). One shared pack
capacity pair (`capacity_kwh`, `capacity_measured`). One shared `currency`. Three
charging-source groups (`ext_ac_*`, `ext_dc_*`, `sc_*`), each with an energy
total, a cost total, a count, and a JSONB ending-battery distribution — all four
left at their zero value until tier 3.

**Rejected alternative: two migrations, one per tier.** RD1 already settled
this — one migration, all columns, now. A second migration on the same new
table adds nothing a single, wider one does not already give.

**Rejected alternative: no zero-fill contract for the `ext_*`/`sc_*` columns —
leave them out of tier 2's own `INSERT`/`UPDATE` list.** An `UPSERT` that omits
a column on `UPDATE` leaves that column's *previous* value in place, not its
default — so re-running tier 2's sync after tier 3 ships would silently
preserve tier 3's real numbers, which sounds safe until tier 2's own
`INSERT` branch is considered: a first-ever sync for a vehicle still needs
*some* value for every `NOT NULL` column, and omitting the charging columns
from the column list at all is not legal SQL for a table with no separate
default-insert path. The simplest, least surprising contract is: this tier's
`UPSERT` always sets every column, computing real values for the ones it owns
and passing the documented zero value for the ones it does not yet compute —
see D8.

## D2 — Month normalization stays in SQL, never in Go

**Decision.** `MonthlySyncer.SyncMonth(ctx, teslaID, period time.Time)` accepts
any instant inside the target calendar month, exactly like
`charging.MonthlyCapacityReader.CapacityForMonth`. Both SQL statements this
tier adds normalize `period` with `date_trunc('month', @period::date)::date` —
the read that fetches the month's `vehicle_metrics` rows, and the `UPSERT` that
stores the row, including the stored `period` column value itself (read back
via `RETURNING`, never recomputed in Go).

**Rejected alternative: normalize in Go**, e.g.
`time.Date(period.Year(), period.Month(), 1, 0, 0, 0, 0, period.Location())`.
Rejected for the same reason tier 1 rejected it: this is precisely the
five-argument `time.Date(...)` shape `make tz-guard` greps for outside
`internal/clock`. Doing the truncation in SQL, and reading the normalized value
back from `RETURNING` instead of reconstructing it, means the Go code never
builds "the first instant of this month" at all.

## D3 — One shared `currency` column; cost columns are `NUMERIC(14,2)`

**Decision (currency sharing).** The table carries a single `currency` column
(default `'COP'`, matching `charging.manual_charge_entries.currency`'s own
default), paired with all three cost columns (`ext_ac_cost`, `ext_dc_cost`,
`sc_cost`) — one currency column can pair with more than one money column in
the same row; the unit-of-measure rule requires the pairing, not a
one-to-one column count.

**Rejected alternative: a `currency` column per charging source.** Both
`charging.Entry.Currency` and `charging.Session.Currency` are optional
per-record fields, so a month could in principle mix currencies across
sources, or even within one source. A per-source currency column would only
paper over that: it still cannot represent "this source's entries used two
different currencies this month." This platform is single-tenant and
COP-only today, so the shared column is the honest shape for the data that
actually exists. **Open item for tier 3, not resolved here:** if a month ever
contains entries in more than one currency, tier 3's aggregation must pick a
rule (e.g., sum only the entries matching the stored `currency`, or convert).
That is an aggregation decision, not a schema one, and is flagged here so it
is not silently assumed away when tier 3 is built.

**Decision (cost column type — RD11, settled at the database design gate,
binding on tier 3 too).** `ext_ac_cost`, `ext_dc_cost`, and `sc_cost` are
`NUMERIC(14,2) NOT NULL DEFAULT 0`. This is a confirmed decision, not an
open question: `charging.manual_charge_entries.price`, one of this table's
two cost sources, is already `NUMERIC(14,2)`, and
`charging.supercharger_sessions.total_cost`'s own table comment already
flags `double precision` as a questionable type for money and names
reconciling it with `manual_charge_entries.price` as unfinished work in
`charging` itself. Carrying `double precision` into a brand-new table would
have imported that same unresolved question here. `vehicle_monthly_metrics`
starts empty, so the correct type is free to choose now; correcting it
after tier 3 has written real rows would cost a migration plus a backfill.

**Go-side consequence of `NUMERIC`.** `pgx` returns a `NUMERIC` column as
`pgtype.Numeric`, not `float64`. `internal/analytics/AGENTS.md` and this
module's existing convention (`internal/analytics/mapping.go`) require
`pgtype` to never leave the module's DB-facing files — the public domain
type stays a plain Go type. `VehicleMonthlyMetrics`'s `ExtACCost`,
`ExtDCCost`, and `SCCost` fields are `float64`, exactly like every other
numeric field on this struct. The conversion lives in
`internal/analytics/mapping.go`, alongside this module's other per-type
pg-conversion helper pairs (`pgFloat8FromPtr`/`ptrFloat64FromPg`,
`pgInt4FromPtr`/`ptrIntFromPg`): a new pair,
`pgNumericFromFloat64(float64) pgtype.Numeric` (write side, used when
binding `UpsertVehicleMonthlyMetric`'s three cost parameters) and
`float64FromPgNumeric(pgtype.Numeric) (float64, error)` (read side, used
when mapping `UpsertVehicleMonthlyMetric`'s `RETURNING *` row back into
`VehicleMonthlyMetrics`). The read-side helper returns an error rather than
silently truncating, because `pgtype.Numeric` can represent values
`float64` cannot exactly (e.g. `NaN`/`Infinity` encodings) — a case this
table's own writer never produces, but the conversion function does not
assume that of every future caller. `monthly_sync.go` (the only caller of
either helper) treats a conversion error the same as any other mapping
error: wrapped and returned from `SyncMonth`, never swallowed.

## D4 — Domain type: one flat struct, not a nested per-bucket type

**Decision.** `VehicleMonthlyMetrics` is one flat struct with prefixed fields
(`AllDistanceKm`, `WeekdayDistanceKm`, `WeekendDistanceKm`, …) — the same shape
`charging.Entry` and `charging.Session` already use for their own many-field
domain types, rather than a nested `Figures` sub-struct repeated three times.

**Rejected alternative: a nested `MonthlyFigures` struct embedded three
times** (`All MonthlyFigures`, `Weekday MonthlyFigures`, `Weekend
MonthlyFigures`). This reads nicely in isolation but buys nothing here: the
whole row is always read and written as one unit (one `SELECT`, one
`UPSERT`), so there is no code path that only touches one bucket and would
benefit from addressing it as `m.Weekday.DistanceKm` instead of
`m.WeekdayDistanceKm`. Introducing the nested type would be exactly the kind
of indirection the project's AI-efficiency rule warns against: it costs a
type to look up and a field-access indirection, on a surface that is not
volatile or repeated enough to earn it. Internally, the derivation code (not
exported) still uses a small unexported accumulator per bucket — see
"Derivation" below — so the repetition is not duplicated by hand three times
in the implementation; it just never becomes a public type.

## D5 — `EndingBatteryDist` is a typed struct, not a `map[string]int`

**Decision.** The JSONB ending-battery distribution decodes into:

```go
// EndingBatteryDist counts charge events by their ending battery
// percentage, in five fixed 20-point ranges. The zero value (every count 0)
// is what this tier writes for all three sources; a later change computes
// the real counts.
type EndingBatteryDist struct {
	Bucket0To20   int `json:"0-20"`
	Bucket20To40  int `json:"20-40"`
	Bucket40To60  int `json:"40-60"`
	Bucket60To80  int `json:"60-80"`
	Bucket80To100 int `json:"80-100"`
}
```

**Rejected alternative: `map[string]int`.** The bucket set is fixed and known
at compile time — a map buys nothing but the risk of a typo'd key
(`"0-19"`) compiling cleanly and silently producing a distribution with a
missing bucket. A struct makes every bucket a named field the compiler
checks. The `json` tags keep the on-the-wire shape exactly as the roadmap
names it ("the five buckets 0-20, 20-40, 40-60, 60-80, 80-100"), so tier 3's
real implementation changes no stored shape, only the numbers inside it.

## D6 — `SyncMonth` takes a bare `int64`, not a `vehicleref.Ref`

**Decision.** `MonthlySyncer.SyncMonth(ctx context.Context, teslaID int64,
period time.Time) (VehicleMonthlyMetrics, error)` — no `vehicleref.Ref`
parameter.

**Reasoning.** `vehicleref.Ref` exists to prove that a signed-in user's HTTP
request is asking about a vehicle that account actually owns
(`ai/architecture.md` §5). This port has no such caller: it is driven by the
nightly cycle (tier 4), which iterates every registered vehicle by its own
authority, exactly like `charging.MonthlyCapacityCalculator.Calculate` and
`charging.MonthlyCapacityReader.CapacityForMonth` already do with the
identical bare-`int64` shape. Adding a `Ref` parameter here would not add a
real check — nothing calls this port from a user request — it would only add
an argument every nightly caller has to fabricate.

## D7 — One `day_count` per bucket; no separate `observed_day_count` or
`efficiency_day_count`

**Decision.** Each bucket carries exactly one count column
(`all_day_count`, `weekday_day_count`, `weekend_day_count`) — the number of
*computable* days (both `distance_traveled_km_calc` and `consumed_pct`
non-`NULL`) in that bucket. There is no separate column counting raw
`vehicle_metrics` rows regardless of computability, and no separate column
counting only the days whose `consumed_pct > 0` (the subset RD2's efficiency
sum actually uses).

**Why one count is enough.** RD5 asks for "at least one count column
recording how many `vehicle_metrics` days fed the row" — `{bucket}_day_count`
is exactly that count, for each of the three buckets. A reader who sees
`all_day_count = 0` correctly concludes "nothing to trust in this row's
figures," which is the only fact RD5 requires to be recoverable.

**The accepted residual ambiguity.** A day with a computable but
non-positive `consumed_pct` (for example, a flagged day where distance was
driven but the corrected consumption reads zero) counts toward
`{bucket}_day_count` but contributes nothing to `{bucket}_km_per_pct_calc`'s
sum (RD2). So `{bucket}_day_count > 0` together with `{bucket}_km_per_pct_calc
== 0` can mean either "no days this month had positive consumption" or "zero
days is the true efficiency count was zero" is not actually reachable — the
only way `km_per_pct_calc` reads `0` is the former. This is called out
explicitly, not hidden, and mirrors this project's existing precedent for a
column whose meaning has one accepted, documented rough edge rather than a
new column added to remove it (the `tpms_pressure_*_delta_calc` columns'
ambient-temperature drift is the closest precedent: real, understood, and
deliberately not "fixed" with more columns).

**Rejected alternative: `{bucket}_efficiency_day_count`, a second count per
bucket** for the subset with `consumed_pct > 0`. This would fully remove the
ambiguity above, at the cost of three more integer columns. Rejected as
unnecessary precision for a figure whose only consumer today is a future,
unbuilt stats page (MAG-87) — adding it now would be speculative. If that
page later needs to distinguish the two cases, the column is a small,
additive migration at that point, not a redesign.

**Rejected alternative: `observed_day_count`, one column counting every
`vehicle_metrics` row for the month regardless of computability.** This
would tell a reader "the vehicle was tracked N days this month, M of which
were computable" — a real, if minor, extra fact (a vehicle's very first
tracked day is real but never computable). Rejected because it does not
change what any of the stored figures mean, and `all_day_count = 0` already
carries the actionable signal RD5 asks for: "do not trust these figures."

## D8 — The `ext_*`/`sc_*` zero-fill contract this tier writes, and tier 3 replaces

**Decision.** Every `UPSERT` this tier's `MonthlySyncer.SyncMonth` performs
sets **every** column in the table, including `ext_ac_*`, `ext_dc_*`, `sc_*`,
and `currency` — never a partial column list. For the charging-derived
columns, this tier writes the documented zero value: `0` for every numeric
column (including the three `NUMERIC(14,2)` cost columns — RD11 does not
change this contract, only the column's stored type), the zero-count
`EndingBatteryDist` for every JSONB column. Tier 3
does not change this `UPSERT`'s shape — it changes what the Go code computes
*before* calling it, replacing the zero placeholders with real aggregates
read from `charging.Reader.ListEntriesByVehicleBetween` and
`charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween`.

**Why this is safe.** Because every call always sets every column, there is
no moment where a tier-2-only sync silently coexists with tier-3-filled data
for the same row and corrupts it: a re-run of the (still tier-2-only)
`SyncMonth` on a row tier 3 has not yet touched simply writes the same zeros
again — idempotent, matching the roadmap's own "sync, not append" framing.
Once tier 3 ships, `SyncMonth` computes real values instead, and every
subsequent call — including a re-run of an old month — writes the real
figures, because the same one `UPSERT` always overwrites the whole row.

## Roadmap decisions out of scope for this tier

RD3 (the nightly-refresh cadence) and RD9's port-count constraint are tier
4's and tier 1's concerns respectively — tier 1 is already archived, and
tier 4 has not started. This tier's only echo of RD3 is that
`MonthlySyncer.SyncMonth` is designed to be called twice per vehicle per
night without side effects beyond the one row it targets (D8's idempotence).
RD10's naming table is reflected throughout: `MonthlySyncer`,
`monthlySyncer`, `VehicleMonthlyMetrics`, `EndingBatteryDist` end in neither
a banned suffix nor an unrelated generic word.

---

## Database Changes

### Full schema

```sql
CREATE TABLE analytics.vehicle_monthly_metrics (
    id                          uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id                    bigint NOT NULL,
    period                      date NOT NULL,

    all_distance_km             double precision NOT NULL DEFAULT 0,
    all_consumed_pct            double precision NOT NULL DEFAULT 0,
    all_km_per_pct_calc         double precision NOT NULL DEFAULT 0,
    all_day_count               integer NOT NULL DEFAULT 0,

    weekday_distance_km         double precision NOT NULL DEFAULT 0,
    weekday_consumed_pct        double precision NOT NULL DEFAULT 0,
    weekday_km_per_pct_calc     double precision NOT NULL DEFAULT 0,
    weekday_day_count           integer NOT NULL DEFAULT 0,

    weekend_distance_km         double precision NOT NULL DEFAULT 0,
    weekend_consumed_pct        double precision NOT NULL DEFAULT 0,
    weekend_km_per_pct_calc     double precision NOT NULL DEFAULT 0,
    weekend_day_count           integer NOT NULL DEFAULT 0,

    capacity_kwh                double precision NOT NULL DEFAULT 0,
    capacity_measured           boolean NOT NULL DEFAULT false,

    currency                    text NOT NULL DEFAULT 'COP',

    ext_ac_energy_kwh           double precision NOT NULL DEFAULT 0,
    ext_ac_cost                 numeric(14,2) NOT NULL DEFAULT 0,
    ext_ac_entry_count          integer NOT NULL DEFAULT 0,
    ext_ac_ending_battery_dist  jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    ext_dc_energy_kwh           double precision NOT NULL DEFAULT 0,
    ext_dc_cost                 numeric(14,2) NOT NULL DEFAULT 0,
    ext_dc_entry_count          integer NOT NULL DEFAULT 0,
    ext_dc_ending_battery_dist  jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    sc_energy_kwh               double precision NOT NULL DEFAULT 0,
    sc_cost                     numeric(14,2) NOT NULL DEFAULT 0,
    sc_session_count            integer NOT NULL DEFAULT 0,
    sc_ending_battery_dist      jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    created_at                  timestamp with time zone DEFAULT now() NOT NULL,
    updated_at                  timestamp with time zone DEFAULT now() NOT NULL,

    CONSTRAINT vehicle_monthly_metrics_tesla_period_unique UNIQUE (tesla_id, period),
    CONSTRAINT vehicle_monthly_metrics_period_is_month_start CHECK (EXTRACT(day FROM period) = 1)
);
```

32 columns. (The roadmap's own "Origin" section recalled an early, pre-design
estimate of "about 24" from the 2026-09-10 interview that first deferred this
table — that was a rough guess made before any schema pass, not a budget;
RD1's later full-scope decision plus RD5's count columns account for the
difference, and every column above is justified against RD1–RD10 in D1–D8.)

### Column-by-column meaning

| Column | Type | Default | Meaning |
|---|---|---|---|
| `id` | uuid | `gen_random_uuid()` | Surrogate key. Never referenced by any other table. |
| `tesla_id` | bigint | — | The vehicle. No FK — matches every other `analytics` table's convention. |
| `period` | date | — | First day of the calendar month this row summarizes. Enforced by the CHECK; always written via `date_trunc`, never by the caller. |
| `all_distance_km` | double precision | `0` | Sum of `vehicle_metrics.distance_traveled_km_calc` over every computable day in the month. |
| `all_consumed_pct` | double precision | `0` | Sum of `vehicle_metrics.consumed_pct` over every computable day in the month. |
| `all_km_per_pct_calc` | double precision | `0` | RD2's ratio: sum of distance over sum of `consumed_pct`, both restricted to computable days with `consumed_pct > 0`. `0` when no such day exists this month. |
| `all_day_count` | integer | `0` | Number of computable days in the month. `0` means "nothing above can be trusted" — the RD5 signal. |
| `weekday_distance_km` … `weekday_day_count` | (same four, same meaning) | `0` | Identical to the `all_*` four columns, restricted to days whose `period`'s date is Monday–Friday. |
| `weekend_distance_km` … `weekend_day_count` | (same four, same meaning) | `0` | Identical, restricted to Saturday/Sunday. |
| `capacity_kwh` | double precision | `0` | Copied from `charging.MonthlyCapacityReader.CapacityForMonth`. `0` whenever that port did not return a measured number. **Two-state by decision (RD12):** "no row" and "a row with no measurement" both collapse to `capacity_kwh = 0, capacity_measured = false` — the owner confirmed this loss at the database design gate; it is not an open risk. |
| `capacity_measured` | boolean | `false` | `true` only when the port returned a real, non-nil kWh figure. The RD4/RD5 signal for `capacity_kwh`, and the second half of RD12's confirmed two-state design. |
| `currency` | text | `'COP'` | The reference currency all three `*_cost` columns are expressed in (D3). |
| `ext_ac_energy_kwh` | double precision | `0` | Sum of `EnergyAddedKWh` over this month's external charge entries with `ChargingType = 'AC'`. Zero-filled by this tier; tier 3 computes it. |
| `ext_ac_cost` | `numeric(14,2)` | `0` | Sum of `Price` over the same entries. Zero-filled by this tier. `NUMERIC(14,2)` by decision (RD11) — see D3. |
| `ext_ac_entry_count` | integer | `0` | Count of the same entries — the RD5-style disambiguator for the three columns above. Zero-filled by this tier. |
| `ext_ac_ending_battery_dist` | jsonb | zero-count object | Ending-battery-percentage distribution over the same entries, five 20-point buckets. Zero-filled by this tier. |
| `ext_dc_energy_kwh`, `ext_dc_entry_count`, `ext_dc_ending_battery_dist` | (same shapes as their `ext_ac_*` counterparts) | (same defaults) | Identical to `ext_ac_*`, for `ChargingType = 'DC'` entries. |
| `ext_dc_cost` | `numeric(14,2)` | `0` | Same meaning and type as `ext_ac_cost`, for `ChargingType = 'DC'` entries. |
| `sc_energy_kwh`, `sc_session_count`, `sc_ending_battery_dist` | (same shapes as their `ext_ac_*` counterparts) | (same defaults) | Identical family, over this month's Supercharger sessions. Zero-filled by this tier. |
| `sc_cost` | `numeric(14,2)` | `0` | Same meaning and type as `ext_ac_cost`, over this month's Supercharger sessions. |
| `created_at` | timestamptz | `now()` | Set once, on the row's first `INSERT`; never refreshed on conflict. |
| `updated_at` | timestamptz | `now()` | Refreshed to `now()` on every `UPSERT`, including a call that writes identical figures. |

**What this tier fills vs. what stays at zero, restated plainly:** every
column from `all_distance_km` through `capacity_measured` holds a real,
computed number after this tier ships. `currency` holds its default,
unconditionally, until tier 3 decides otherwise. Every `ext_*`/`sc_*` column
holds its documented zero value until tier 3 ships — a `0` there is not a
bug in this tier, it is this tier's own, honest, not-yet-computed state
(D8).

### Rationale (why this design, not another)

- **One row per `(tesla_id, period)`, `UPSERT`-only, no soft delete.** Mirrors
  every sibling monthly table in this codebase
  (`charging.monthly_effective_capacity`) and the roadmap's own framing:
  "the whole table is refreshed by one use case ... running it again for the
  same pair rewrites the row."
- **`NUMERIC(14,2)` for every money column (`ext_ac_cost`, `ext_dc_cost`,
  `sc_cost`), never `double precision` (RD11, settled at the database design
  gate).** `charging.manual_charge_entries.price` — the source for
  `ext_ac_cost`/`ext_dc_cost` — is already `NUMERIC(14,2)`.
  `charging.supercharger_sessions.total_cost` — the source for `sc_cost` —
  is `double precision` today, but that column's own table comment already
  flags float as a questionable type for money and names reconciling it
  with `manual_charge_entries.price`'s `NUMERIC(14,2)` as unfinished work.
  Picking `double precision` here would have carried that same unresolved
  question into a brand-new table on day one. `vehicle_monthly_metrics`
  starts with zero rows, so the right type costs nothing to choose now;
  choosing wrong and correcting it after tier 3 has written real cost
  figures would cost a migration plus a backfill. See D3 for the read-side
  consequence: `pgtype.Numeric` never leaves the module's DB-facing files.
- **JSONB defaults are a real, fully-keyed zero-count object, not `'{}'`.** A
  reader that unconditionally looks up `dist["0-20"]` gets `0`, never a
  missing-key `nil`/zero-value surprise, on every row this table has ever
  had — including a row this tier itself writes before tier 3 exists.

### Index Plan

**No index beyond the `UNIQUE (tesla_id, period)` constraint.**

The only query this tier or the roadmap's earlier tiers issue against this
table is the `UPSERT`'s own `ON CONFLICT (tesla_id, period)` — an equality
lookup on exactly the two columns the unique constraint's btree already
serves, in the same order. `ai/go-conventions.md`'s own rule states this
directly: "a `UNIQUE (a, b)` constraint already builds a btree that serves
equality on `a`, point lookups on `(a, b)` ... A separate index next to it
repeats work the constraint already does." No `SELECT` in this tier filters,
joins, or orders by anything else on this table — there is no read port over
it yet (see `proposal.md` "Read paths affected"). Adding a speculative index
for a future stats-page read (MAG-87, not yet designed) would violate the
project's own "justify every index against a query that exists today" rule.
When that read port is designed, its own change adds whatever index its own
query pattern needs — most likely `(tesla_id, period DESC)`, mirroring
`idx_vehicle_metrics_latest`, but that decision belongs to that change, not
this one.

---

## The Go Port

`internal/analytics/analytics.go` (interface + domain types + constructor,
added alongside the existing `Reader`/`Recalculator`/`GapWriter` ports):

```go
// EndingBatteryDist counts charge events by their ending battery
// percentage, in five fixed 20-point ranges. The zero value (every count 0)
// is what this tier writes for all three charging sources; a later change
// computes the real counts from charging's own entries and sessions.
type EndingBatteryDist struct {
	Bucket0To20   int `json:"0-20"`
	Bucket20To40  int `json:"20-40"`
	Bucket40To60  int `json:"40-60"`
	Bucket60To80  int `json:"60-80"`
	Bucket80To100 int `json:"80-100"`
}

// VehicleMonthlyMetrics is one precomputed month for one vehicle -- the row
// analytics.vehicle_monthly_metrics stores under (TeslaID, Period). Every
// numeric field holds a real number, never a sentinel: an empty month
// reports zero everywhere, and each bucket's own DayCount is what tells a
// genuine zero apart from a month with nothing to compute (see the table's
// own column comments in the migration for the full contract).
//
// ExtAC*, ExtDC*, SC*, and Currency hold their documented zero value until a
// later change computes them from charging's own entries and sessions --
// every other field holds a real figure as of this port.
type VehicleMonthlyMetrics struct {
	TeslaID int64
	Period  time.Time // first day of the month

	AllDistanceKm   float64
	AllConsumedPct  float64
	AllKmPerPctCalc float64
	AllDayCount     int

	WeekdayDistanceKm   float64
	WeekdayConsumedPct  float64
	WeekdayKmPerPctCalc float64
	WeekdayDayCount     int

	WeekendDistanceKm   float64
	WeekendConsumedPct  float64
	WeekendKmPerPctCalc float64
	WeekendDayCount     int

	CapacityKWh      float64
	CapacityMeasured bool

	Currency string

	ExtACEnergyKWh         float64
	ExtACCost              float64
	ExtACEntryCount        int
	ExtACEndingBatteryDist EndingBatteryDist

	ExtDCEnergyKWh         float64
	ExtDCCost              float64
	ExtDCEntryCount        int
	ExtDCEndingBatteryDist EndingBatteryDist

	SCEnergyKWh         float64
	SCCost              float64
	SCSessionCount      int
	SCEndingBatteryDist EndingBatteryDist

	CreatedAt time.Time
	UpdatedAt time.Time
}

// MonthlySyncer derives and stores one vehicle_monthly_metrics row for one
// vehicle and one calendar month, from this module's own vehicle_metrics
// table plus the pack capacity charging measured for that month. Running it
// again for the same (teslaID, period) rewrites the row -- this is a sync,
// not an append, so any caller may re-run one month at any time to pick up
// a later edit to that month's telemetry or charging data.
type MonthlySyncer interface {
	// SyncMonth computes and upserts the row for teslaID and the calendar
	// month containing period -- only period's year and calendar month
	// matter; any day within that month gives the same result, matching
	// charging.MonthlyCapacityReader.CapacityForMonth's identical
	// any-day-in-month contract. Returns the row as stored.
	//
	// ExtAC*, ExtDC*, SC*, and Currency come back at their documented zero
	// value as of this version -- a later change fills them from charging's
	// own range reads. Every other field reflects this month's real
	// figures.
	SyncMonth(ctx context.Context, teslaID int64, period time.Time) (VehicleMonthlyMetrics, error)
}

// NewMonthlySyncer constructs a MonthlySyncer over the analytics module's
// own database pool plus the one sibling port it reads to copy the pack
// capacity. The implementation lives in monthly_sync.go. The returned value
// logs its single method -- see query_log.go.
func NewMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader) MonthlySyncer {
	return newLoggingMonthlySyncer(newMonthlySyncer(pool, capacity))
}
```

## The Pure Derivation (RD8 -- all monthly maths in Go, in one place)

`internal/analytics/monthly_figures.go` (new file, no database import --
pure, offline-testable):

```go
// monthDay is one vehicle_metrics day's contribution to a month's derived
// figures -- decoupled from analyticsdb's generated row type so this
// derivation is testable with no database. Both pointers are nil together
// on a day with no computable predecessor (the same guarantee
// vehicle_metrics.consumed_pct's own column comment documents), and this
// code relies on that: it never checks one without the other.
type monthDay struct {
	Date        time.Time
	DistanceKm  *float64
	ConsumedPct *float64
}

// bucketAccumulator sums one day-subset's distance and consumption, and
// derives the efficiency ratio only over the days whose ConsumedPct is
// positive -- averaging the daily ratios instead would let one very short,
// high-percentage day dominate the month. A day with a computable but
// zero-or-negative ConsumedPct still counts toward dayCount (it was a
// real, tracked day) but contributes nothing to the ratio, mirroring
// vehicle_metrics.km_per_pct_calc's own divisor guard.
type bucketAccumulator struct {
	distanceKm     float64
	consumedPct    float64
	dayCount       int
	effDistanceKm  float64
	effConsumedPct float64
}

func (b *bucketAccumulator) add(distanceKm, consumedPct float64) {
	b.distanceKm += distanceKm
	b.consumedPct += consumedPct
	b.dayCount++
	if consumedPct > 0 {
		b.effDistanceKm += distanceKm
		b.effConsumedPct += consumedPct
	}
}

// kmPerPct returns 0 when no day in this bucket had a positive ConsumedPct
// -- there is nothing to divide by, and 0 is this table's documented "no
// data" value for every numeric column.
func (b bucketAccumulator) kmPerPct() float64 {
	if b.effConsumedPct <= 0 {
		return 0
	}
	return b.effDistanceKm / b.effConsumedPct
}

// isWeekend reports whether d falls on Saturday or Sunday. d is a DATE with
// no time-of-day component and no zone attached to it -- this needs no
// internal/clock call, unlike bucketing a raw timestamp would.
func isWeekend(d time.Time) bool {
	wd := d.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// deriveMonthlyFigures computes every vehicle_metrics-derived field of
// VehicleMonthlyMetrics from one month's rows. A day whose DistanceKm or
// ConsumedPct is nil is skipped entirely -- it has no computable
// predecessor and contributes to no bucket's sums or counts (this is the
// only place that ambiguity is silently accepted, and it is accepted
// because vehicle_metrics itself already treats such a day as having
// nothing computable -- see AGENTS.md).
func deriveMonthlyFigures(days []monthDay) VehicleMonthlyMetrics {
	var all, weekday, weekend bucketAccumulator
	for _, d := range days {
		if d.DistanceKm == nil || d.ConsumedPct == nil {
			continue
		}
		all.add(*d.DistanceKm, *d.ConsumedPct)
		if isWeekend(d.Date) {
			weekend.add(*d.DistanceKm, *d.ConsumedPct)
		} else {
			weekday.add(*d.DistanceKm, *d.ConsumedPct)
		}
	}
	return VehicleMonthlyMetrics{
		AllDistanceKm: all.distanceKm, AllConsumedPct: all.consumedPct,
		AllKmPerPctCalc: all.kmPerPct(), AllDayCount: all.dayCount,

		WeekdayDistanceKm: weekday.distanceKm, WeekdayConsumedPct: weekday.consumedPct,
		WeekdayKmPerPctCalc: weekday.kmPerPct(), WeekdayDayCount: weekday.dayCount,

		WeekendDistanceKm: weekend.distanceKm, WeekendConsumedPct: weekend.consumedPct,
		WeekendKmPerPctCalc: weekend.kmPerPct(), WeekendDayCount: weekend.dayCount,
	}
}
```

## The SQL

`internal/analytics/db/query.sql` (two new queries):

```sql
-- name: VehicleMetricsForVehicleAndMonth :many
-- Backs MonthlySyncer.SyncMonth: the ONE query that fetches a month's
-- vehicle_metrics rows -- every subsequent figure is derived from this
-- slice in Go (monthly_figures.go), never in a second query. No IS NOT
-- NULL filter, unlike the daily Reader's own two filtered reads: this query
-- must see a predecessor-less row too, so the Go derivation can skip it and
-- still count it toward nothing, rather than the SQL silently hiding it.
-- period accepts any day inside the target month; date_trunc normalizes it
-- to the month's own bounds in SQL, matching CapacityForMonth's identical
-- any-day-in-month contract.
-- Served by an index on (tesla_id, metric_date), never a seq scan -- no
-- separate CREATE INDEX. Two indexes already lead on those columns
-- (vehicle_metrics_tesla_date_unique, idx_vehicle_metrics_latest); which one
-- the planner picks is its choice, not a contract.
SELECT
    metric_date, distance_traveled_km_calc, consumed_pct
FROM analytics.vehicle_metrics
WHERE tesla_id = @tesla_id
  AND metric_date >= date_trunc('month', @period::date)::date
  AND metric_date <  (date_trunc('month', @period::date) + interval '1 month')::date
ORDER BY metric_date;

-- name: UpsertVehicleMonthlyMetric :one
-- Upsert one vehicle_monthly_metrics row. Always sets every column --
-- including the ext_*/sc_*/currency columns this version fills with their
-- documented zero value -- so a later change can widen what the Go side
-- computes without ever widening this statement's own column list.
-- period is normalized here, in SQL, from whatever day-in-month the caller
-- passed in -- the Go caller never constructs a first-of-month value
-- itself. RETURNING * hands the normalized period, and both timestamps,
-- straight back so the Go layer never recomputes what this statement just
-- decided.
-- created_at is DELIBERATELY ABSENT from the SET clause -- it must record
-- this (tesla_id, period)'s first sync, not its latest one, mirroring
-- UpsertVehicleMetric's identical convention on the daily table.
INSERT INTO analytics.vehicle_monthly_metrics (
    tesla_id, period,
    all_distance_km, all_consumed_pct, all_km_per_pct_calc, all_day_count,
    weekday_distance_km, weekday_consumed_pct, weekday_km_per_pct_calc, weekday_day_count,
    weekend_distance_km, weekend_consumed_pct, weekend_km_per_pct_calc, weekend_day_count,
    capacity_kwh, capacity_measured, currency,
    ext_ac_energy_kwh, ext_ac_cost, ext_ac_entry_count, ext_ac_ending_battery_dist,
    ext_dc_energy_kwh, ext_dc_cost, ext_dc_entry_count, ext_dc_ending_battery_dist,
    sc_energy_kwh, sc_cost, sc_session_count, sc_ending_battery_dist
) VALUES (
    @tesla_id, date_trunc('month', @period::date)::date,
    @all_distance_km, @all_consumed_pct, @all_km_per_pct_calc, @all_day_count,
    @weekday_distance_km, @weekday_consumed_pct, @weekday_km_per_pct_calc, @weekday_day_count,
    @weekend_distance_km, @weekend_consumed_pct, @weekend_km_per_pct_calc, @weekend_day_count,
    @capacity_kwh, @capacity_measured, @currency,
    @ext_ac_energy_kwh, @ext_ac_cost, @ext_ac_entry_count, @ext_ac_ending_battery_dist,
    @ext_dc_energy_kwh, @ext_dc_cost, @ext_dc_entry_count, @ext_dc_ending_battery_dist,
    @sc_energy_kwh, @sc_cost, @sc_session_count, @sc_ending_battery_dist
)
ON CONFLICT (tesla_id, period) DO UPDATE SET
    all_distance_km             = EXCLUDED.all_distance_km,
    all_consumed_pct            = EXCLUDED.all_consumed_pct,
    all_km_per_pct_calc         = EXCLUDED.all_km_per_pct_calc,
    all_day_count               = EXCLUDED.all_day_count,
    weekday_distance_km         = EXCLUDED.weekday_distance_km,
    weekday_consumed_pct        = EXCLUDED.weekday_consumed_pct,
    weekday_km_per_pct_calc     = EXCLUDED.weekday_km_per_pct_calc,
    weekday_day_count           = EXCLUDED.weekday_day_count,
    weekend_distance_km         = EXCLUDED.weekend_distance_km,
    weekend_consumed_pct        = EXCLUDED.weekend_consumed_pct,
    weekend_km_per_pct_calc     = EXCLUDED.weekend_km_per_pct_calc,
    weekend_day_count           = EXCLUDED.weekend_day_count,
    capacity_kwh                = EXCLUDED.capacity_kwh,
    capacity_measured           = EXCLUDED.capacity_measured,
    currency                    = EXCLUDED.currency,
    ext_ac_energy_kwh           = EXCLUDED.ext_ac_energy_kwh,
    ext_ac_cost                 = EXCLUDED.ext_ac_cost,
    ext_ac_entry_count          = EXCLUDED.ext_ac_entry_count,
    ext_ac_ending_battery_dist  = EXCLUDED.ext_ac_ending_battery_dist,
    ext_dc_energy_kwh           = EXCLUDED.ext_dc_energy_kwh,
    ext_dc_cost                 = EXCLUDED.ext_dc_cost,
    ext_dc_entry_count          = EXCLUDED.ext_dc_entry_count,
    ext_dc_ending_battery_dist  = EXCLUDED.ext_dc_ending_battery_dist,
    sc_energy_kwh               = EXCLUDED.sc_energy_kwh,
    sc_cost                     = EXCLUDED.sc_cost,
    sc_session_count            = EXCLUDED.sc_session_count,
    sc_ending_battery_dist      = EXCLUDED.sc_ending_battery_dist,
    updated_at                  = now()
RETURNING *;
```

`sqlc generate` produces `analyticsdb.VehicleMetricsForVehicleAndMonthRow`
and `analyticsdb.VehicleMonthlyMetric` (via the `sqlc.yaml` rename entry
below) plus `UpsertVehicleMonthlyMetric(ctx, params) (VehicleMonthlyMetric,
error)`. The three JSONB columns generate as `[]byte`, matching this
project's existing `raw_data JSONB -> []byte` default (`sqlc.yaml`'s own
comment on the `telemetry` entry) -- no override needed. The three
`NUMERIC(14,2)` cost columns generate as `pgtype.Numeric`, sqlc/pgx's
default mapping for `NUMERIC` -- also no override needed, matching how
`internal/charging`'s own `NUMERIC(14,2)` price column already generates.
`pgtype.Numeric` is converted to and from plain `float64` only inside
`internal/analytics/mapping.go` (D3) -- it never reaches
`VehicleMonthlyMetrics` or any other public type.

`sqlc.yaml`'s existing `analytics` block gains one `rename:` entry:

```yaml
          analytics_vehicle_monthly_metric: "VehicleMonthlyMetric"
```

## Makefile / codegen check

Checked, not assumed:

- **`MIGRATIONS_DIRS` order** — unaffected. The new migration lives in
  `internal/analytics/db/migrations/`, this module's own directory, numbered
  after `20260918000001` in this module's own ledger.
- **`db-setup`/`db-reset`** — unaffected. No new role, no new ownership
  assumption; the new table lives in the `analytics` schema this module
  already owns.
- **`sqlc`** — this change's only codegen input besides the migration. The
  existing `analytics` entry in `sqlc.yaml` already points at
  `internal/analytics/db/migrations` and `internal/analytics/db/query.sql`;
  this change adds one migration file, two queries, and one `rename:` line.
  `make sqlc` regenerates `internal/analytics/db/{models.go,query.sql.go}`.
- **Every guard** — `make migration-boundary-guard`: the new migration names
  only the `analytics` schema, reads nothing else. `make tenancy-guard`: the
  new table has no `account_id` column and the new queries filter on
  `tesla_id` only. `make naming-guard`: `MonthlySyncer`, `monthlySyncer`,
  `VehicleMonthlyMetrics`, `EndingBatteryDist`, `bucketAccumulator`,
  `monthDay` end in none of the banned suffixes (RD10). `make tz-guard`: no
  `time.Now()`, no hardcoded IANA zone, and no UTC-midnight `time.Date(...)`
  construction anywhere in this change — month normalization is SQL-only
  (D2). `make delta-guard`: no new column is a day-over-day delta, so no
  `_delta_calc` naming question arises. `make vehicleref-guard`: this port
  takes no `vehicleref.Ref` (D6), and calls neither `vehicleref.Authorize`
  nor `vehicleref.All`, so the guard has nothing new to check. `make
  boundary-guard` is gateway-only and untouched. `make archive-guard`:
  no archived file is touched.
- **Finding:** the Makefile and every guard are unaffected by this change,
  beyond the ordinary `sqlc` regeneration step above. This is a finding, not
  an assumption — each guard's rule was checked against the new code shape
  before this line was written.

---

## Test Contract

Expected values authored **before** implementation
(`ai/go-conventions.md` §Testing). A later task writes the test code against
this contract, not against whatever the implementation happens to produce.

**Conventions**, mirroring `internal/analytics`'s and `internal/charging`'s
existing integration-test style: assert only against Go domain values
(`VehicleMonthlyMetrics`'s own fields), never `pgtype`. Pure-math cases
(T-PURE-1 through T-PURE-3) need no database — they call
`deriveMonthlyFigures` directly and belong in an offline unit test. The
DB-backed cases (T1 through T6) are `TEST_DATABASE_URL`-gated integration
tests using `internal/analytics`'s existing `testdb.ProvisionDirs` harness
(it must reach both `analytics`'s own migrations and `charging`'s, to seed
`monthly_effective_capacity`), seeding `vehicle_metrics` and
`monthly_effective_capacity` with direct `INSERT`s (this module's own
established precedent for a fixture with no single-row public writer).

March 2026 is this contract's month: 1 Mar 2026 is a Sunday, so the month's
weekend days are the 1st, 7th, 8th, 14th, 15th, 21st, 22nd, 28th, and 29th
(9 days); every other day of the 31 is a weekday (22 days). The fixtures
below use only a handful of these days, not the full month.

### Pure-math cases (offline, no database)

New file: `internal/analytics/monthly_figures_test.go`.

| ID | Input `[]monthDay` | Expected `VehicleMonthlyMetrics` (fields not listed are 0) | What it proves |
|---|---|---|---|
| **T-PURE-1** | `2026-03-02` (Mon, weekday): distance 100, consumed 20. `2026-03-03` (Tue, weekday): distance 50, consumed 10. `2026-03-07` (Sat, weekend): distance 80, consumed 16. `2026-03-08` (Sun, weekend): distance 40, consumed 8. `2026-03-09` (Mon, weekday): distance 15, consumed 0. | `AllDistanceKm=285, AllConsumedPct=54, AllDayCount=5, AllKmPerPctCalc=5.0` (270/54, excluding 09's 0-consumption day per RD2 -- NOT 285/54=5.2778). `WeekdayDistanceKm=165, WeekdayConsumedPct=30, WeekdayDayCount=3, WeekdayKmPerPctCalc=5.0` (150/30, excluding the 09th). `WeekendDistanceKm=120, WeekendConsumedPct=24, WeekendDayCount=2, WeekendKmPerPctCalc=5.0`. | Mixed weekday/weekend split (RD6), and RD2's exclusion rule changing the actual answer (5.0), not just the divisor's day count. |
| **T-PURE-2** | Same five days as T-PURE-1, plus `2026-03-10` (Tue, weekday): `DistanceKm=nil, ConsumedPct=nil` (no predecessor). | Identical output to T-PURE-1 -- the nil day contributes to no sum and no count. | A predecessor-less day is silently skipped, never treated as a zero-value day. |
| **T-PURE-3** | Empty slice. | Every field 0, including every `*DayCount`. | The empty-month case the DB-backed T2 wraps with a real `SyncMonth` call. |

### DB-backed cases

New file: `internal/analytics/db_monthly_sync_integration_test.go`.

| ID | Setup | Call | Expected result | What it proves |
|---|---|---|---|---|
| **T1** | `vehicle_metrics` seeded for `tesla_id = 555001` with exactly the five rows from T-PURE-1 (metric_date/distance_traveled_km_calc/consumed_pct). No `monthly_effective_capacity` row. | `SyncMonth(ctx, 555001, anyDayIn(2026, 3))` | `Period = 2026-03-01`; the twelve `All*`/`Weekday*`/`Weekend*` fields match T-PURE-1 exactly; `CapacityKWh=0, CapacityMeasured=false`; `Currency="COP"`; every `Ext*`/`SC*` field at its zero value. | The full path — SQL fetch, Go derivation, SQL upsert, SQL read-back — reproduces the pure-math answer exactly, for a month with real mixed data. |
| **T2** | No `vehicle_metrics` rows at all for `tesla_id = 555002`. No `monthly_effective_capacity` row. | `SyncMonth(ctx, 555002, anyDayIn(2026, 4))` | A row IS written: `Period = 2026-04-01`, every `*DayCount` is `0`, every figure is `0`. | RD5: an empty month still gets a row, with its counts visibly at zero, never a missing row. |
| **T3** | `monthly_effective_capacity` row for `(tesla_id=555001, effective_period=2026-03-01, effective_capacity_kwh=61.8)`, plus the same `vehicle_metrics` fixture as T1. | `SyncMonth(ctx, 555001, 2026-03-15)` (a mid-month day, not the 1st) | Same figures as T1, plus `CapacityKWh=61.8, CapacityMeasured=true`. | A measured capacity is copied through correctly, and `period` normalization (D2) works from a non-first-of-month input day. |
| **T4** | `monthly_effective_capacity` row for `(tesla_id=555003, effective_period=2026-05-01, effective_capacity_kwh=NULL)` (a thin month at the `charging` level). No `vehicle_metrics` rows. | `SyncMonth(ctx, 555003, anyDayIn(2026, 5))` | `CapacityKWh=0, CapacityMeasured=false`. | Tier 1's "row exists, no measurement" state collapses to the same analytics-level `0`/`false` as "no row at all" (T2/T5) -- RD12's confirmed two-state design, not a bug. |
| **T5** | No `monthly_effective_capacity` row at all for `tesla_id = 555004`. No `vehicle_metrics` rows. | `SyncMonth(ctx, 555004, anyDayIn(2026, 6))` | `CapacityKWh=0, CapacityMeasured=false` — identical output to T4. | Confirms T4's collapse is symmetric: both of tier 1's "not measured" states produce the same analytics row. |
| **T6** | Same fixture as T1. | `SyncMonth(ctx, 555001, 2026-03-01)` called twice in a row. | Both calls return byte-identical `VehicleMonthlyMetrics` values (except `UpdatedAt`, which advances). Exactly one row exists in `vehicle_monthly_metrics` for `(555001, 2026-03-01)` after both calls. | Idempotence — the roadmap's own "running it again rewrites the row" contract, and the `UNIQUE` constraint prevents a duplicate. |

## Risks

None beyond the ordinary risk of any new table: a future schema change to
`monthly_effective_capacity` would need `CapacityForMonth`'s caller (this
tier) re-checked alongside its own definition — already true before this
tier existed, not a new coupling this design introduces. The `ext_*`/`sc_*`
columns sitting at zero until tier 3 is a known, temporary, and explicitly
documented state (D8), not a risk to a consumer, because no `Reader` exists
over this table yet for anything to read it prematurely.
