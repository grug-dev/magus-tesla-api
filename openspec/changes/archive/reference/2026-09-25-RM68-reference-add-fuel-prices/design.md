# Design — RM68-reference-add-fuel-prices

> Numbering note: the roadmap (`openspec/roadmaps/RM68-gasoline-equivalent.md`) already
> numbers its own binding decisions **D1–D13** — settled with the user before any artifact
> was written, never re-opened here. To avoid two different things both being called "D1"
> in the same conversation, this document's own decisions — the ones this artifacts pass
> had to make to turn the tier's scope statement into a buildable table and use case — are
> numbered **TD1, TD2, …** ("tier decision").

## Context

Three facts shape everything below.

1. **This is the project's first new module in a while.** Every convention below —
   module-scoped `db/` package, one baseline migration, one `sqlc.yaml` entry, the
   `MIGRATION_MODULES` list — already has an established shape from `account`, `telemetry`,
   `charging`, and `analytics`. Nothing here is invented; it is applied to a fifth module.
2. **`analytics.vehicle_monthly_metrics` already establishes the two shapes this table
   reuses.** The `period date` month-start `CHECK (EXTRACT(day FROM period) = 1)` and the
   `NUMERIC(14,2)` money type paired with a `currency` column are both settled precedent —
   roadmap D2 names this explicitly. This table does not re-derive either choice; it copies
   them.
3. **`charging.packCapacityKWh`'s "newest measured value, or a fallback" shape is the
   nearest analog to "newest row at or before", but it is a single-row, per-vehicle lookup
   answering one question at a time.** This tier's read answers the same *kind* of question
   — resolve a value from the newest applicable row — but for up to twelve months at once,
   in one query. Roadmap D7 already settled that the query must resolve the whole window in
   SQL, not loop in Go; TD3 below is how.

## TD1 — Table shape, full column list, rationale, and index plan (database design gate)

This is the schema the reviewer approves or rejects. See "Database Changes" below for the
full `CREATE TABLE`, per-column meaning, rationale, and index plan.

**Summary of the shape:** one row per calendar month. `period` (month-start, CHECK-enforced),
`currency`, `price` — plus the module's own surrogate `id`, `created_at`, `updated_at`. No
`region`, no `product` — roadmap D3 rejected both explicitly; the rationale below restates
why for the reviewer reading this document standalone.

**Rejected alternative: no separate `id` column, `period` as the primary key.** Every other
DB-backed module in this project keys its tables on a surrogate `id uuid` and expresses
business-key uniqueness through a separate `UNIQUE` constraint (`vehicle_monthly_metrics`,
`manual_charge_entries`, `supercharger_sessions`). Breaking that convention for one table
saves nothing — `period` becoming the primary key would still need the exact same btree the
`UNIQUE (period)` constraint already builds — and it costs the "every table looks the same"
property that makes a new module cheap to read for an agent already familiar with the other
four.

## TD2 — Schema-only baseline, then one data-only migration per priced month

**One baseline migration** (`CREATE SCHEMA reference` + `CREATE TABLE
reference.fuel_prices`, no rows) **followed by two ordinary migrations, one row each**: `period = 2026-08-01, price =
16000.00` first, then `period = 2026-09-01, price = 16331.00`. Both are `COP`.

This follows directly from `ai/go-conventions.md`'s own baseline rule, already binding on
every module: *"Each module's migration history starts from one baseline — a single file
that creates that module's whole schema and reads nothing."* A baseline that also inserted a
row would not read another module's schema, but it would still not be schema-only — and a
baseline never runs on an existing database (it is stamped as already-applied), so any data
placed inside it would never reach a database that was set up before this module existed.
Every database that runs `make db-setup` after this change ships is new to `reference`
either way, so the distinction is not yet observable — but the rule is unconditional, not
conditional on whether it matters today, and it also sets the pattern correctly for the
first *real* future price migration.

**This also matches roadmap D4 by construction, not by extra effort.** D4 says prices are
loaded "by one migration per month" going forward — a whole input surface is not worth
building for twelve rows a year. Making the very first price its own migration, separate
from the baseline, means the August 2026 row is not a special case: it is migration #1 in
exactly the shape every later month's price will also take. A reader of the migrations
folder a year from now sees one schema file and one file per priced month, uniformly.

## TD3 — The window query: `generate_series` + a `LATERAL` "newest at or before" lookup

Roadmap D7 already settled that `PricesForMonths(ctx, start, end)` must resolve the whole
window in one SQL query, with the fallback applied in SQL. The concrete mechanism:

```sql
SELECT gs.period, fp.currency, fp.price
FROM generate_series(
    date_trunc('month', @start_period::date),
    date_trunc('month', @end_period::date),
    interval '1 month'
) AS gs(period)
CROSS JOIN LATERAL (
    SELECT currency, price
    FROM reference.fuel_prices
    WHERE period <= gs.period::date
    ORDER BY period DESC
    LIMIT 1
) AS fp;
```

`generate_series` walks every first-of-month between the two (already month-truncated)
bounds — at most a handful of rows for a `/vehicle-stats` window, never large. For each one,
the `LATERAL` subquery finds the newest `fuel_prices` row at or before that month, ordered
by `period DESC LIMIT 1` — exactly `packCapacityKWh`'s "newest measured value" question,
asked once per generated month instead of once per vehicle.

**Why `CROSS JOIN LATERAL`, not a `LEFT JOIN LATERAL`.** `CROSS JOIN LATERAL` behaves as an
inner join: when the subquery returns zero rows for a given `gs.period` — no `fuel_prices`
row exists at or before that month — that month drops out of the result set entirely, rather
than surviving as a row with `NULL` currency and price. This is roadmap D8's requirement
made concrete: no default price, so a month with nothing to fall back to is simply absent, never
a zero or `NULL` entry a careless caller might sum as if it were real.

**Rejected alternative: fetch the whole table, resolve the fallback in Go.** This was named
and rejected at the roadmap level (D7): it would move the fallback rule out of the module
that owns the table and into every caller, and `fuel_prices` will keep growing by roughly
twelve rows a year forever, so "fetch everything" does not even stay cheap.

**Rejected alternative: one query per month (mirroring the ticket's literal per-month
signature).** Also rejected at the roadmap level (D7) — twelve round trips per render on a
declared read-heavy, reads-mandatory-fast profile, for a query this cheap to batch.

## TD4 — Domain shape: a sparse `[]MonthPrice` slice, not a map

```go
// MonthPrice is one calendar month's resolved gasoline price: the exact row
// stored for that month, or the newest row at or before it.
type MonthPrice struct {
	Period   time.Time // first day of the calendar month
	Currency string
	Price    float64
}
```

`PricesForMonths` returns `[]MonthPrice`, ordered oldest first, mirroring
`analytics.MonthlyReader.MonthlyMetricsBetween`'s identical sparse-slice shape and its own
documented convention: *"The result is SPARSE: a month with no stored row is simply absent
... a caller must never assume a fixed entry count."* Reusing that exact shape, rather than
inventing a `map[time.Time]MonthPrice` for this one port, keeps the platform's "how a
monthly, possibly-gappy read result looks" vocabulary at one pattern instead of two — an
AI-efficiency choice as much as a taste one: a future agent reading either port already
knows the other's contract. A caller that wants map-style lookups builds that map itself
from the slice; the module does not choose that shape for it.

## TD5 — No logging decorator

Every other read port that serves a live gateway request in this project — most directly,
`analytics.MonthlyReader` — is deliberately **not** wrapped in a per-call logging decorator,
for a stated reason: it would add a log line to every page render for a caller that runs on
every one, not on a rare nightly cycle. `reference.Reader.PricesForMonths` is exactly that
kind of caller (tier 2's `/vehicle-stats` page, and only that page, ever calls it) — no
`query_log.go` file exists in this module, matching `MonthlyReader`'s precedent exactly. If
`reference` later gains a nightly-cycle caller, that caller's own port gets its own
decorator then — this decision governs only the port this tier ships.

## Database Changes

### Full schema

```sql
CREATE SCHEMA IF NOT EXISTS reference;

CREATE TABLE reference.fuel_prices (
    id         uuid            DEFAULT gen_random_uuid() NOT NULL,
    period     date            NOT NULL,
    currency   text            NOT NULL,
    price      numeric(14,2)   NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,

    CONSTRAINT fuel_prices_period_unique UNIQUE (period),
    CONSTRAINT fuel_prices_period_is_month_start CHECK (EXTRACT(day FROM period) = 1)
);
```

Six columns, one table, one schema. This is the smallest DB-backed module in the project —
deliberately: it holds exactly one external fact, nothing else.

### Column-by-column meaning

| Column | Type | Meaning |
|---|---|---|
| `id` | uuid | Surrogate key. Never referenced by any other table — matches every sibling module's convention (TD1). |
| `period` | date | First day of the calendar month this price applies to. Enforced by the CHECK; always written literally by the migration that seeds it, never computed at write time (there is no runtime writer — see roadmap D4). |
| `currency` | text | The currency `price` is expressed in. `'COP'` today and for the foreseeable future (roadmap D10 accepts the risk of a second currency going uncompared). No `DEFAULT` — every row is written by an explicit migration that always names it. |
| `price` | numeric(14,2) | The price of one gallon of regular gasoline, in `currency`. `NUMERIC(14,2)` — never a unit suffix; this is the platform's monetary exemption, paired with `currency` instead (`ai/go-conventions.md` §Coding Rules), and mirrors `vehicle_monthly_metrics`'s three `*_cost` columns exactly. |
| `created_at` | timestamptz | Set once, on the row's first `INSERT`. This table has no upsert path — every row is written once, by its own seeding migration, and never updated. |
| `updated_at` | timestamptz | Present for uniformity with every sibling table's convention. No writer in this tier ever changes an existing row, so it equals `created_at` for the life of this tier. |

### Rationale (why this design, not another)

- **No `region` column (roadmap D3).** One national price per month is what the ticket
  needs and what the source data supports — the user copies a single government figure by
  hand each time it changes (roadmap D4). A `region` column would need a value on every row
  from day one, would widen the `UNIQUE` key to `(period, region)`, and would force every
  future query — including this tier's own `PricesForMonths` — to carry a region filter for
  a distinction nothing in this platform can currently make: there is no per-vehicle
  location signal this table could key off of. Adding the column later, if a real regional
  need appears, costs one migration; carrying it unused from day one costs every reader a
  parameter it cannot answer.
- **No `product` column (roadmap D3).** Same reasoning, for grade of fuel instead of
  region: regular gasoline only, because that is the number the ticket's cost-parity
  comparison needs, and a `product` discriminator has no consumer yet.
- **The result is never written into `analytics.vehicle_monthly_metrics` (roadmap D6).**
  Two independent reasons, both already settled at the roadmap level and restated here
  because the database design gate is exactly where a reviewer would otherwise ask "why a
  second table instead of one more column on the table that already has seven months of
  cost figures":
  1. **Ownership.** `vehicle_monthly_metrics` is owned by `analytics`, and `fuel_prices`
     belongs to no vehicle — it is an external reference value. Writing a gasoline-derived
     figure into `analytics`'s table would force `analytics` to import `reference` just to
     compute one column, coupling two modules whose data has nothing to do with each other
     ( `ai/architecture.md`'s cycle-prevention reasoning: "first ask where the fact belongs,
     not how to break the cycle" applies here even with no cycle in sight — the fact belongs
     to neither module alone, so it belongs to the request that needs it, computed once, not
     stored anywhere).
  2. **Correction cost.** A stored derived value would need re-syncing every time it is
     wrong. Because a later price row shifts every earlier month that fell back to it
     (roadmap "Accepted consequences"), correcting one price would force a re-sync of every
     `vehicle_monthly_metrics` row between the last real price change and the correction —
     an unbounded backfill for what should be a one-row `UPDATE`. Computing the figure per
     request, from two small, cheap reads (`MonthlyReader.MonthlyMetricsBetween` +
     `Reader.PricesForMonths`), avoids that entirely: correcting a price is exactly the
     one-row `UPDATE` it should be, and every future render reflects it immediately.
- **`NUMERIC(14,2)`, never `double precision`, for `price` (mirrors `analytics`'s own
  settled precedent for `*_cost`).** `vehicle_monthly_metrics.ext_ac_cost` and its two
  siblings already made this exact call, for the same reason: money that will be divided
  and summed across months should not carry binary-floating-point rounding into that
  arithmetic. Starting `fuel_prices.price` at the same type costs nothing — the table has no
  rows yet — while choosing `double precision` and correcting it later would cost a
  migration.

### Index Plan

**No index beyond the `UNIQUE (period)` constraint.**

The only query this tier issues is `PricesForMonths`'s `LATERAL` subquery: `WHERE period <=
@month ORDER BY period DESC LIMIT 1`. This is precisely the access pattern
`ai/go-conventions.md`'s own rule describes: *"A `UNIQUE (a, b)` constraint already builds a
btree that serves equality on `a`, point lookups on `(a, b)`, range scans on `b` within one
`a`, and `ORDER BY b DESC` pinned to one `a`."* Here there is only one column, so the
`UNIQUE (period)` btree directly serves a range scan (`period <= X`) ordered descending with
a `LIMIT 1` — a single index probe per generated month, no sequential scan, no sort. A
second index on `period` would duplicate the constraint's own btree for no gain.

The table will hold on the order of a dozen rows a year (roadmap D4). Even an unindexed scan
would be cheap at that size — but the `UNIQUE` constraint gives the optimal access path for
free, so there is nothing further to justify.

## The Go Port

```go
// reference.go

// Reader is this module's only port. It answers "what gasoline price applies
// to each of these months" — the single external fact this module owns.
type Reader interface {
	// PricesForMonths returns one entry per month in [start, end] that
	// resolves to a stored price: the exact row for that month, or the
	// newest row at or before it. Only the year and calendar month of start
	// and end matter; any day within a month selects that whole month.
	//
	// The result is SPARSE: a month with nothing to fall back to — every
	// stored row is later than that month — is simply absent, never a zero
	// or nil-currency entry. A caller must not assume one entry per
	// requested month, and must not treat absence as a price of zero.
	PricesForMonths(ctx context.Context, start, end time.Time) ([]MonthPrice, error)
}

// MonthPrice is one calendar month's resolved gasoline price.
type MonthPrice struct {
	Period   time.Time // first day of the calendar month
	Currency string
	Price    float64
}

// NewReader constructs a Reader over this module's own database pool. The
// implementation lives in price_reader.go.
func NewReader(pool *pgxpool.Pool) Reader {
	return newReader(pool)
}
```

`price_reader.go` mirrors `internal/analytics/monthly_reader.go`'s own split exactly: a
narrow store interface over the one generated query method (so a test can fake it without a
live database), an unexported concrete type, a compile-time `var _ Reader =
(*priceReader)(nil)` assertion, and a `numericToFloat64` mapping helper converting
`pgtype.Numeric` to `float64` via `Float64Value()` — the same pattern
`analytics.float64FromPgNumeric` and `charging.pgNumericToFloat64Ptr` already use, kept
local to this module rather than shared, per the project's own "no `shared`/`common` dump"
rule.

## The SQL

```sql
-- name: PricesForMonths :many
-- Returns one row per month in [@start_period, @end_period] that resolves to
-- a stored price: the month's own row when one exists, otherwise the newest
-- row at or before it. generate_series walks every first-of-month between
-- the two (already month-truncated) bounds; the LATERAL subquery behaves as
-- an inner join, so a month with nothing to fall back to -- every stored row
-- is later than it -- drops out of the result instead of returning NULLs.
-- Both bounds are month-truncated here, in SQL, mirroring
-- charging.CapacityForMonth's and analytics.MonthlyMetricsBetween's identical
-- choice to keep month normalization out of Go and away from make tz-guard's
-- reach.
SELECT
    gs.period::date AS period,
    fp.currency,
    fp.price
FROM generate_series(
    date_trunc('month', sqlc.arg(start_period)::date),
    date_trunc('month', sqlc.arg(end_period)::date),
    interval '1 month'
) AS gs(period)
CROSS JOIN LATERAL (
    SELECT currency, price
    FROM reference.fuel_prices
    WHERE period <= gs.period::date
    ORDER BY period DESC
    LIMIT 1
) AS fp
ORDER BY gs.period;
```

## Makefile / codegen check

Checked, not assumed:

- **`MIGRATION_MODULES` order** — currently `account telemetry charging analytics`.
  `reference` is appended at the end: `account telemetry charging analytics reference`.
  The order has no correctness effect (`ai/go-conventions.md`: "the order the directories
  are applied in does not matter" — each module has its own ledger, its own schema, and its
  migrations may never name another module's schema). Appending, rather than inserting, is
  purely for a stable, minimal-diff log — every existing module's position in the printed
  order stays unchanged.
- **`make boundary-guard`** — greps `internal/gateway/**/*.go` for the `internal/telemetry`
  import path. This tier touches neither `internal/gateway` nor `internal/telemetry`. No
  effect.
- **`make naming-guard`** — fails a *new* type declaration ending in a banned generic
  suffix (`Processor`, `Manager`, `Handler`, `Helper`, `Data`, `Info`, `Object`, `Thing`).
  This tier's new type names are `Reader`, `MonthPrice`, `priceReader` (unexported,
  mirroring `monthlyReader`). None end in a banned suffix.
- **`make migration-boundary-guard`** — fails a migration that names another module's
  schema. All three new migrations (the baseline and the two seeds) name only `reference.fuel_prices`.
  No effect.
- **`make tenancy-guard`** — fails a query file outside `internal/account` that filters on
  `account_id`. `fuel_prices` has no `account_id` column and `PricesForMonths` does not
  filter on one — it needs no tenant scoping at all, since a gasoline price belongs to no
  account (roadmap D1's own framing: "belongs to no vehicle and no user"). No effect.
- **`make delta-guard`** — fails a column or field named `_calc`/`Calc` that is not a
  day-over-day delta, or a genuine delta missing the `_delta_calc` suffix. This tier adds no
  `_calc` column or field of any kind. No effect.
- **`make tz-guard`** — fails a raw `time.Now()`, a hardcoded IANA zone, or a hand-rolled
  UTC-midnight construction outside `internal/clock`. Month normalization for this tier
  happens entirely in SQL (`date_trunc`, TD3) — `price_reader.go` passes `start`/`end`
  through untouched, exactly as `monthlyReader.MonthlyMetricsBetween` already does. No
  effect.
- **`make vehicleref-guard`** — fails a call to `vehicleref.Authorize`/`vehicleref.All`
  outside `internal/vehicleref`. `PricesForMonths` takes no `vehicleref.Ref` and calls
  neither function — a gasoline price is not vehicle-owned data, so there is nothing to
  authorize (mirrors `charging.MonthlyCapacityReader.CapacityForMonth`'s identical no-`Ref`
  shape, itself justified the same way: its only caller is a page render already scoped by
  the time the gateway calls this port, not a lookup that needs its own ownership check).
- **`sqlc`** — this change's only codegen input besides the two migrations. `sqlc.yaml`
  gains one new entry: `schema: internal/reference/db/migrations`, `queries:
  internal/reference/db/query.sql`, `gen.go.package: referencedb`, `out:
  internal/reference/db`, the standard `uuid → google/uuid.UUID` override every other entry
  already carries, and one `rename:` line, `reference_fuel_price: "FuelPrice"` — every
  existing entry states its struct renames explicitly rather than trusting sqlc's default
  singularization, and this entry follows the same rule even though it has only one table.
  `make sqlc` (or `sqlc generate`) then emits
  `internal/reference/db/{models.go,query.sql.go,db.go}` from the migration and the one
  query above.
- **`db-setup` / `db-reset` role-and-ownership assumptions** — **no change needed.** Every
  module's schema is created by the *same* loop, as the *same* role: `db-setup` connects as
  `APP_ROLE` (via `PGUSER`/`PGPASSWORD`) before running `CREATE SCHEMA IF NOT EXISTS $$m` for
  every `$$m` in `MIGRATION_MODULES`, so a schema this loop creates is owned by `APP_ROLE`
  automatically — there is no separate `GRANT`/`ALTER SCHEMA ... OWNER TO` step for any
  existing module, and `reference` needs none either. `db-setup-test` re-owns every schema
  in `MIGRATION_MODULES` to `APP_ROLE` on each run for the same reason — adding `reference`
  to the list is the only change that target needs, and it already loops over
  `MIGRATION_MODULES` generically. **Finding, not an assumption**: read the `db-setup` and
  `db-setup-test` Makefile targets in full before this section was written; neither
  hardcodes a module name anywhere, so a fifth module needs no target-specific edit beyond
  the `MIGRATION_MODULES` list itself.
- **`make check` phase order** — `reference`'s guards run inside the same aggregate loops
  (`migration-boundary-guard`, `tenancy-guard`) that already iterate `MIGRATION_MODULES`;
  no guard target needs a `reference`-specific branch.

## Worked Example (replaces a Test Contract — roadmap D13 excludes unit tests)

The ticket's own measured figures for August 2026, used here so the implementation has a
concrete target to check by hand once it exists:

| Fact | Value |
|---|---|
| Distance, August 2026 | 1187.1 km |
| Charging cost, August 2026 | 364,949 COP |
| Gasoline price, seeded row `period = 2026-08-01` | 16,000 COP / gallon |
| Gasoline price, seeded row `period = 2026-09-01` | 16,331 COP / gallon |

Not this tier's own arithmetic (roadmap D11's ratio-of-sums is tier 2's job — the gateway
divides `analytics`'s distance/cost sums by this tier's price), but the number the August
row must support when tier 2 does that division:

```
gallons       = 364,949 / 16,000          ≈ 22.81
km per gallon = 1,187.1 / 22.81           ≈ 52.0
```

**What `PricesForMonths` itself must return, concretely:**

- `PricesForMonths(ctx, 2026-08-01, 2026-08-01)` → `[]MonthPrice{{Period: 2026-08-01,
  Currency: "COP", Price: 16000.00}}` — the month's own row, no fallback needed.
- `PricesForMonths(ctx, 2026-09-01, 2026-09-01)` → `[]MonthPrice{{Period: 2026-09-01,
  Currency: "COP", Price: 16331.00}}` — the month's own row. It must not return August's
  16000.
- `PricesForMonths(ctx, 2026-10-01, 2026-10-01)` → `[]MonthPrice{{Period: 2026-10-01,
  Currency: "COP", Price: 16331.00}}` — October has no row of its own; the September row is
  the newest at or before it. **`Period` in the result is `2026-10-01`** (the requested
  month), not `2026-09-01` (the row the price came from) — `generate_series` supplies the
  output `period`, the `LATERAL` subquery supplies only `currency`/`price`.
- `PricesForMonths(ctx, 2026-07-01, 2026-07-01)` → `[]MonthPrice{}` (empty slice, not an
  error, not a one-entry slice with a zero price). July 2026 is before the earliest stored
  row (August), so the `LATERAL` subquery finds nothing and, being a `CROSS JOIN LATERAL`,
  drops that month from the result entirely (roadmap D8, TD3).
- `PricesForMonths(ctx, 2026-07-01, 2026-10-01)` → `[]MonthPrice{{Period: 2026-08-01,
  Price: 16000.00, ...}, {Period: 2026-09-01, Price: 16331.00, ...}, {Period: 2026-10-01,
  Price: 16331.00, ...}}` — three entries for a four-month window, because July is absent.
  This is the concrete case a tier-2 caller must handle: a requested window and a returned
  slice can have different lengths.

## Risks

- **A missing monthly migration does not hide the tile.** A month with no row of its own
  uses the newest earlier row (TD3). So the tile keeps showing a figure, based on an old
  price. The figure goes stale; it does not go dark. That is accepted (roadmap "Accepted
  consequences").
- **Only months before 2026-08 have no price.** The earliest row is August 2026. A window
  that holds only earlier months returns an empty slice, and tier 2 hides the tile for it
  (roadmap D8).
