# Design — RM67-charging-add-monthly-capacity-read

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change only —
> distinct from the roadmap's own **RD1–RD10** in
> `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md` (cited as **RD1**, …). RD1–RD10
> are settled and binding; this document does not re-open them. **D1–D3 answer the two
> design questions the leader's dispatch left open** for this tier: the return shape, and
> whether to expose `candidate_count`/`sample_count`. **D4–D6 are the smaller decisions this
> artifacts pass had to make** to turn the tier's scope statement into a buildable change.

## Context

Five facts about the existing schema and code shape everything below.

1. **`monthly_effective_capacity` already exists, with the exact shape this tier reads.**
   RM52 tier 1 created the table, its `UNIQUE (tesla_id, effective_period)` constraint, and
   one read seam: `LatestMeasuredCapacity` — the newest row whose
   `effective_capacity_kwh IS NOT NULL`, feeding `packCapacityKWh`'s energy-derivation math.
   This tier adds a second, different read. Neither the table, the migration, nor
   `LatestMeasuredCapacity` changes.

2. **The two reads answer different questions, and must stay different.** `packCapacityKWh`
   asks "what capacity should today's math use" — it wants the best available answer and is
   allowed to skip a thin or missing recent month in favor of an older measured one.
   Tier 2's `analytics.vehicle_monthly_metrics` asks "what did `charging` measure for THIS
   exact month" — including a thin month (write `0`, per roadmap RD4) or a month with no row
   at all (also write `0`, but for a different reason). Answering tier 2's question by
   reusing `LatestMeasuredCapacity` would silently substitute a different month's number,
   or hide a thin month behind an unrelated earlier measurement — wrong on both counts. The
   two reads are kept as two separate queries and two separate Go methods for exactly this
   reason (D2 below).

3. **The unique constraint already builds the index this query needs.** `ai/go-conventions.md`'s
   own rule: a `UNIQUE (a, b)` constraint's btree already serves equality lookups on `(a, b)`
   together. This tier's query filters on exactly `tesla_id` and `effective_period` by
   equality — the same two columns, the same order, as
   `monthly_effective_capacity_tesla_id_effective_period_key`. Adding a second index would
   duplicate work that constraint already does. No index is added (D5).

4. **A month-normalizing "midnight of a day" construction in Go is exactly what `tz-guard`
   exists to catch.** The tier's own scope note asks for a Test Contract case where the
   caller passes a date that is not the first of the month. Normalizing that in Go — building
   a `time.Date(y, m, 1, 0, 0, 0, 0, loc)` outside `internal/clock` — is precisely the
   "hand-rolled midnight-of-a-day construction" `make tz-guard`'s own grep flags
   (`ai/go-conventions.md` §"The platform's default time zone"). This tier does the
   normalization in SQL instead (D4), which sidesteps the guard rather than earning an
   escape-hatch comment for something genuinely simple.

5. **`estimateEffectiveCapacity`/`median`'s internal counts are gate-quality signals, not
   facts about a month's answer.** `candidate_count` and `sample_count` describe how the
   estimator arrived at `effective_capacity_kwh` for a month — evidence found, evidence
   surviving the delta gate. They are documented as internal to that computation
   (`internal/charging/AGENTS.md` §"The monthly-capacity estimator keeps its gate and its
   method as separate functions"). Roadmap RD5 gives tier 2 its own, unrelated count — how
   many `vehicle_metrics` days fed its row — which answers tier 2's own "is this an empty
   month" question without needing charging's internal ones (D3).

## D1 — Return shape: `(capacityKWh *float64, found bool, err error)`, no new struct type

**Decision.** `MonthlyCapacityReader.CapacityForMonth` returns three plain values, not a
struct:

```go
CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (capacityKWh *float64, found bool, err error)
```

**The three states the caller must tell apart, and how each maps:**

| State | `found` | `capacityKWh` |
|---|---|---|
| No `monthly_effective_capacity` row for this vehicle and month | `false` | always `nil` |
| A row exists, but the month was too thin to measure (RD4's thin month — `effective_capacity_kwh IS NULL`) | `true` | `nil` |
| A row exists with a measured capacity | `true` | non-`nil` |

**Why a triple return, not a struct.** Go's own `v, ok := m[k]` idiom is exactly this
shape — a value plus an existence flag — and every Go reader (and every agent that has ever
read Go) recognizes it without opening a type definition. A new struct
(`MonthlyCapacity{Found bool; KWh *float64}`) would hold exactly these two fields and
nothing else: it adds a type name to look up, a doc comment to read, and an import for
every caller, to carry information three return values already carry directly. This is the
over-abstraction `CLAUDE.md`'s AI-efficiency rule warns against — indirection that does not
buy change-locality on a volatile surface. If a second field is ever needed (D3 revisits
this if a real consumer appears), a struct becomes the right call at that point, not before.

**Why not reuse the existing two-state pointer pattern.** `packCapacityKWh`'s own seam
(`latestMeasuredCapacity(ctx, teslaID) (*float64, error)`) uses `nil` alone to mean "no
measured row" — a legitimate two-state shape, because that read has only two states to
report. This tier's read has three, and collapsing "no row" and "row, NULL capacity" onto
the same `nil` would erase exactly the distinction the leader's dispatch requires: the
tier-2 caller's column is `NOT NULL DEFAULT 0` (RD4), and a `0` written for a genuinely
absent month is table stakes, but conflating "absent" with "measured-thin" would still be a
readability loss for anyone auditing that table later, even though both currently resolve to
the same stored `0` in tier 2's design. `found` keeps that distinction visible in the port's
own contract, not only in tier 2's private bookkeeping — cheap to keep now, expensive to
recover later once the code that calls this port is written against a two-state shape.

## D2 — A new port and a new query, never a widened `LatestMeasuredCapacity`

**Decision.** `LatestMeasuredCapacity` (the SQL query) and `packCapacityKWh`'s seam are
**unchanged**. `CapacityForMonth` is a new method on a new interface,
`MonthlyCapacityReader`, backed by a new query, `EffectiveCapacityForPeriod`.

**Rejected alternative: add a `period` parameter to `LatestMeasuredCapacity` and let `NULL`
mean "no period given, use latest."** This was the most tempting shortcut — one query
instead of two. It was rejected because it conflates two callers with genuinely different
contracts behind one signature: `packCapacityKWh`'s caller always wants "latest measured,
skip thin months", while tier 2 always wants "this exact month, thin or absent included."
A single query parameterized by an optional month would need its own conditional logic
(`WHERE effective_period = @month OR (@month IS NULL AND ...)`) that this codebase has no
existing idiom for — `monthly_capacity.go`'s own doc comment on `Calculate` states plainly
that this project has "no existing precedent for a nullable-filter SQL idiom
(`sqlc.narg`/`sqlc.arg` do not appear anywhere in this project)" and declines to introduce
one for a once-a-month job. Introducing one here, for a second once-a-month-per-vehicle
read, would be the same over-abstraction for the same reason. Two small, single-purpose
queries are also each independently indexable and independently testable — Context fact 2
above is the deeper reason two questions get two answers, not just two SQL statements.

## D3 — Do NOT expose `candidate_count` / `sample_count` to the caller

**Decision.** `CapacityForMonth` returns only the capacity. `candidate_count` and
`sample_count` stay in `monthly_effective_capacity`, read by nothing outside
`internal/charging`.

**Reasoning.**

- **Tier 2 already has its own count, for a different question.** Roadmap RD5: "tier 2
  always writes a row and records a count of how much data fed it" — that count is over
  `vehicle_metrics` rows (analytics' own table), not over `charging`'s manual entries and
  Supercharger sessions. `charging`'s `sample_count` says how much evidence *this module's
  own estimator* found for a capacity figure; it says nothing about how much
  `vehicle_metrics` data tier 2 had for the same month. The two counts answer different
  "is this month real" questions for different tables. Handing tier 2 charging's count would
  not save it from building its own — RD5 already commits it to that — so it would be a
  second, unused count sitting on tier 2's copy for no consumer.
- **These counts are documented as internal to the estimator, not as facts about its
  output.** `internal/charging/AGENTS.md`: `estimateEffectiveCapacity` is "the gate" that
  produces `sample_count` as a byproduct of deciding what counts as evidence — a detail of
  *how* `charging` measured the capacity, not *what* the capacity is. Exposing it to another
  module would leak an implementation detail of the gate-then-median algorithm across the
  module boundary this whole roadmap exists to keep clean (roadmap "Scope note").
- **No consumer asks for it.** The roadmap's tier-2 proposal prompt describes copying "the
  capacity charging measured for that month" — singular, the capacity. Nothing in RD1–RD10
  or the tier-2/tier-3 proposal prompts reads `charging`'s own count. Adding fields nothing
  reads is the same over-abstraction D1 declines for the return shape, applied to the query's
  `SELECT` list instead: `EffectiveCapacityForPeriod` selects only `effective_capacity_kwh`.
- **Revisit trigger, recorded so this is not re-argued blind.** If a future consumer needs
  to show *how confident* a copied capacity is (e.g. a UI badge on the stats page tier 2
  feeds — MAG-87, explicitly out of this roadmap's scope), extending `CapacityForMonth`'s
  return (or adding a sibling method) to include `sampleCount int` is a small, additive
  change at that point — a `SELECT` column and a return value, not a redesign. Do not add it
  speculatively now.

## D4 — Month normalization happens in SQL, never in Go

**Decision.** `CapacityForMonth(ctx, teslaID, month time.Time)` accepts any instant inside
the target calendar month — the caller does not need to pre-normalize to the first of the
month. The query does the normalization:

```sql
WHERE tesla_id = @tesla_id
  AND effective_period = date_trunc('month', @month::date)::date
```

**Rejected alternative: normalize in Go before binding the query parameter**, e.g.
`time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())`. Rejected for two
reasons. First, Context fact 4 above: this exact shape — a five-zero `time.Date(...)`
call — is what `make tz-guard` greps for outside `internal/clock`, so this would either fail
the guard or need a `// tz:allow:` escape hatch for something that has a guard-clean answer
already. Second, even setting the guard aside, pushing the truncation into Postgres means
the Go code never has to reconstruct "the first instant of this month" at all — it hands
the caller's own `time.Time` straight to `pgtype.Date{Time: month, Valid: true}`
(`dateFromTime`'s own existing shape, reused as-is — no new mapping helper), and lets the
database, which already owns the `effective_period` column's own `CHECK (day = 1)`
constraint, be the single place that knows what "the month" means for this table.

## D5 — No new index

**Decision.** `EffectiveCapacityForPeriod` adds no index. It reuses
`monthly_effective_capacity_tesla_id_effective_period_key`, the `UNIQUE (tesla_id,
effective_period)` constraint RM52 tier 1 already created.

**Index Plan (`openspec/config.yaml` §design requirement).** The query's `WHERE` clause is
an equality match on `(tesla_id, effective_period)` — the exact leading columns, in the
exact order, of the unique constraint's own btree. A btree on `(a, b)` serves an equality
lookup on `(a, b)` directly; this is the same reasoning `ai/go-conventions.md` already states
for this project's convention against redundant indexes ("A `UNIQUE (a, b)` constraint
already builds a btree that serves equality on `a`, point lookups on `(a, b)` ..."). The
`date_trunc(...)` call wraps the *parameter*, not the indexed column — `effective_period`
itself is compared bare, so the expression is fully sargable and the existing index is used
unchanged. No `EXPLAIN` surprises are expected or were found: this is a single-row point
lookup on a table with one row per vehicle per month, checked against a unique index.

## D6 — Whether to update the table's SQL comment

The table's `COMMENT ON TABLE charging.monthly_effective_capacity` (baseline migration)
says *"Owned exclusively by internal/charging; no other module reads this table directly."*
This is now stale in the strictest sense — a lawful, indirect read exists as of this tier,
through `charging`'s own port.

**Decision: leave the comment unchanged.** The comment's claim is still true at the level
that matters: **no other module reads this table's rows directly** — no other module
imports `chargingdb`, opens a connection to the `charging` schema, or writes SQL naming
`monthly_effective_capacity`. `internal/analytics` (tier 2) will call
`charging.MonthlyCapacityReader.CapacityForMonth`, a Go method on a public port — this is
exactly what "owned exclusively... no other module reads this table directly" is
*supposed* to still guarantee even after this tier lands: ownership and access control stay
with `charging`; a sibling module gets a value, never a row, a connection, or a query it
wrote itself. Editing the comment to add a caveat about "except through a port" would be
true but would weaken a sentence whose whole job is to stop a future agent from reaching for
`chargingdb` directly from another module — the comment's audience is someone about to violate
the boundary, not someone auditing what already respects it. This is a considered
"unchanged", not an oversight: recorded here so a future reviewer does not need to re-derive
it.

## Roadmap decisions out of scope for this tier

RD1, RD2, RD6, RD7, RD8 are tier 2/tier 3 concerns (the new `analytics` table's shape and
math) and are not implemented here. RD3 is tier 4's nightly-trigger concern; this tier's
only echo of it is that the new port's caller will be nightly-triggered, which is why it
gets a logging decorator (proposal.md, mirroring `MirrorWatermarkStore`/
`MonthlyCapacityCalculator`'s existing treatment in `query_log.go`). RD9's port-count
constraint is satisfied — this is the one port RD9 names. RD10's naming table is reflected
throughout: `MonthlyCapacityReader` ends in neither a banned suffix nor an unrelated
generic word.

---

## The Query

```sql
-- name: EffectiveCapacityForPeriod :one
-- MonthlyCapacityReader.CapacityForMonth's own read (RM67 tier 1). A DIFFERENT
-- question from LatestMeasuredCapacity above: this returns the row for the
-- EXACT calendar month containing @month, thin or absent included -- never
-- the newest non-NULL row across every month. Callers may pass any instant
-- inside the target month; date_trunc normalizes it to the month's first day
-- in SQL (matching effective_period's own CHECK (day = 1)) rather than in
-- Go, which would need a hand-rolled UTC-midnight construction outside
-- internal/clock. effective_capacity_kwh comes back SQL NULL for a thin
-- month (RD4) -- :one means "no row" surfaces as pgx.ErrNoRows, which the Go
-- caller translates to found=false, distinct from a found row whose capacity
-- is NULL.
SELECT effective_capacity_kwh
  FROM charging.monthly_effective_capacity
 WHERE tesla_id = @tesla_id
   AND effective_period = date_trunc('month', @month::date)::date;
```

sqlc generates `chargingdb.EffectiveCapacityForPeriodParams{TeslaID int64, Month pgtype.Date}`
and a `EffectiveCapacityForPeriod(ctx, params) (pgtype.Float8, error)` method against the
existing table — no new model, since the table's Go model (`chargingdb.MonthlyEffectiveCapacity`)
already exists and this query selects a single already-mapped column type.

## The Go Port

`internal/charging/charging.go` (interface + constructor):

```go
// MonthlyCapacityReader is this module's second read port over
// monthly_effective_capacity (RM67 tier 1), alongside packCapacityKWh's own
// unexported LatestMeasuredCapacity seam. It answers a different question:
// what did this exact vehicle and month measure, not what should today's
// math use. See design.md D2 for why these stay two separate reads.
type MonthlyCapacityReader interface {
	// CapacityForMonth returns the measured pack capacity for teslaID for
	// the calendar month containing month -- only month's year and calendar
	// month matter; any day within that month gives the same result.
	//
	// found is false when no monthly_effective_capacity row exists yet for
	// this vehicle and month -- capacityKWh is then always nil.
	// found is true and capacityKWh is nil when a row exists but that
	// month's evidence was too thin to measure a capacity (a NULL
	// effective_capacity_kwh -- roadmap RD4's "thin month").
	// found is true and capacityKWh is non-nil for a month with a measured
	// capacity.
	CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (capacityKWh *float64, found bool, err error)
}

// NewMonthlyCapacityReader constructs a MonthlyCapacityReader backed by the
// given pgxpool. The implementation lives in monthly_capacity_reader.go where
// the chargingdb generated package is used. This is the only publicly
// exported factory function for this port. The returned value logs its
// single method -- see query_log.go.
func NewMonthlyCapacityReader(pool *pgxpool.Pool) MonthlyCapacityReader {
	return newLoggingMonthlyCapacityReader(newMonthlyCapacityReader(pool))
}
```

`internal/charging/monthly_capacity_reader.go` (new file, implementation):

```go
package charging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// monthlyCapacityReader is the concrete implementation of
// MonthlyCapacityReader. A small unexported struct over chargingdb.Queries,
// mirroring sessionReader's and mirrorWatermarkStore's own shape.
type monthlyCapacityReader struct {
	q *chargingdb.Queries
}

func newMonthlyCapacityReader(pool *pgxpool.Pool) *monthlyCapacityReader {
	return &monthlyCapacityReader{q: chargingdb.New(pool)}
}

var _ MonthlyCapacityReader = (*monthlyCapacityReader)(nil)

// CapacityForMonth implements MonthlyCapacityReader. See the interface doc
// comment (charging.go) for the full contract.
func (r *monthlyCapacityReader) CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (*float64, bool, error) {
	v, err := r.q.EffectiveCapacityForPeriod(ctx, chargingdb.EffectiveCapacityForPeriodParams{
		TeslaID: teslaID,
		Month:   pgtype.Date{Time: month, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("charging: reading monthly capacity for tesla_id %d: %w", teslaID, err)
	}
	return pgFloat8ToFloat64Ptr(v), true, nil
}
```

Reuses the existing `pgFloat8ToFloat64Ptr` helper (`service.go`) — no new mapping helper
needed.

`internal/charging/query_log.go` (new decorator, appended to the file's existing five):

```go
// --- loggingMonthlyCapacityReader ---

// loggingMonthlyCapacityReader wraps the public MonthlyCapacityReader port.
// Its only caller is the nightly cycle (roadmap RD3), the same class of
// caller MirrorWatermarkStore and MonthlyCapacityCalculator are already
// logged for in this file.
type loggingMonthlyCapacityReader struct {
	inner MonthlyCapacityReader
}

func newLoggingMonthlyCapacityReader(inner MonthlyCapacityReader) *loggingMonthlyCapacityReader {
	return &loggingMonthlyCapacityReader{inner: inner}
}

var _ MonthlyCapacityReader = (*loggingMonthlyCapacityReader)(nil)

func (l *loggingMonthlyCapacityReader) CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (*float64, bool, error) {
	capacityKWh, found, err := l.inner.CapacityForMonth(ctx, teslaID, month)
	logging.Note("MonthlyCapacityReader", "CapacityForMonth",
		"charging query: tesla_id=%d month=%s found=%t capacity_kwh=%v",
		teslaID, month.Format("2006-01"), found, capacityKWh)
	return capacityKWh, found, err
}
```

## Makefile / codegen check

Checked, not assumed:

- **`MIGRATIONS_DIRS` order** — unaffected. No migration is added; `charging`'s directory
  and version ledger are untouched.
- **`db-setup`/`db-reset`** — unaffected. No schema change, no new role or ownership
  assumption.
- **`sqlc`** — this change's only codegen input. `sqlc.yaml`'s existing `charging` entry
  (`schema: internal/charging/db/migrations`, `queries: internal/charging/db/query.sql`)
  already covers the new query with no edit — a query file addition, not a new module
  needing its own `sql:` entry. `make sqlc` regenerates
  `chargingdb.EffectiveCapacityForPeriodParams`/`EffectiveCapacityForPeriod` against the
  existing `monthly_effective_capacity` model; nothing else in `chargingdb` changes.
- **Every guard** — `make migration-boundary-guard` has nothing new to check (no
  migration). `make tenancy-guard` has nothing new to check (the new query filters on
  `tesla_id`, not `account_id` — this table has no `account_id` column at all). `make
  naming-guard` — `MonthlyCapacityReader` and `monthlyCapacityReader` end in `Reader`, not
  a banned suffix (RD10). `make tz-guard` — the new code contains no `time.Now()`, no
  hardcoded IANA zone, and no UTC-midnight `time.Date(...)` construction (D4's whole
  point). `make boundary-guard` is gateway-only and untouched. `make vehicleref-guard` has
  nothing new to check — this port takes no `vehicleref.Ref` (D-context: its caller is the
  nightly cycle, not a user request; mirrors `SuperchargerSessionAnalyticsReader`'s
  identical no-Ref shape for the same reason). `make delta-guard` has nothing new to check
  — no new column, no `_calc` naming question.
- **Finding:** the Makefile and every guard are unaffected by this change. This is a
  finding, not an assumption — each guard's grep pattern was checked against the new
  code shape above before this line was written.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing
("author their expected values up front, in the change's `design.md`, before the
implementation exists"). **Tests written later must assert THIS contract**, not whatever
the implementation happens to produce.

**Conventions**, following this module's existing ones (`internal/charging/AGENTS.md`
§Testing Notes): assert only against `charging` domain values (a `*float64`, a `bool`) or
direct SQL column values — **never `pgtype`**, in any file. Integration test, `DATABASE_URL`-
gated through the package's existing `testdb_test.go`. Seed `monthly_effective_capacity`
rows with direct `INSERT`s (this module's own precedent for seeding a table with no public
writer shaped for a single row — `MonthlyCapacityCalculator.Calculate` writes a whole
month's pool at once, not one row on demand — mirroring `db_monthly_capacity_integration_test.go`'s
own Group C fixtures).

New file: `internal/charging/db_monthly_capacity_reader_integration_test.go`
(package `charging_test`).

| ID | Setup | Call | Expected `(capacityKWh, found)` | What it proves |
|---|---|---|---|---|
| **T1** | No `monthly_effective_capacity` row for `tesla_id = 777001`, any month | `CapacityForMonth(ctx, 777001, 2026-03-15)` | `(nil, false)` | **No row at all** — the caller's first distinguishable state. |
| **T2** | One row: `tesla_id = 777001`, `effective_period = '2026-03-01'`, `effective_capacity_kwh = NULL`, `candidate_count = 2`, `sample_count = 2` | `CapacityForMonth(ctx, 777001, 2026-03-01)` | `(nil, true)` | **A thin month** — a row exists, but the capacity is genuinely absent (RD4). Distinct from T1 despite both returning a `nil` pointer. |
| **T3** | One row: `tesla_id = 777001`, `effective_period = '2026-03-01'`, `effective_capacity_kwh = 61.8`, `candidate_count = 5`, `sample_count = 4` | `CapacityForMonth(ctx, 777001, 2026-03-01)` | `(ptr(61.8), true)` | **A measured month** — the third state, and the counts are NOT part of the return value (D3) even though they exist on the row. |
| **T4** | Same row as T3 | `CapacityForMonth(ctx, 777001, 2026-03-15)` — a day in the middle of the month, not the 1st | `(ptr(61.8), true)` | **Arbitrary-day normalization** (D4): the caller need not pass the 1st; `date_trunc` in SQL finds the same row T3 found. |
| **T4b** | Same row as T3 | `CapacityForMonth(ctx, 777001, 2026-03-31)` — the last day of the month | `(ptr(61.8), true)` | **Normalization at the far boundary of the month**, not only near its start. |
| **T5** | Row for `tesla_id = 777001`, `effective_period = '2026-03-01'`, measured `61.8`; a second row for `tesla_id = 777002`, same `effective_period`, measured `70.0` | `CapacityForMonth(ctx, 777001, 2026-03-01)` | `(ptr(61.8), true)` — never `70.0` | **A different vehicle's row for the same month does not leak.** |
| **T6** | Row for `tesla_id = 777001`, `effective_period = '2026-03-01'`, measured `61.8`; a second row for the same `tesla_id`, `effective_period = '2026-02-01'`, measured `55.0` | `CapacityForMonth(ctx, 777001, 2026-03-01)` | `(ptr(61.8), true)` — never `55.0` | **A different month's row for the same vehicle does not leak** — the exact-month contract, not "any row for this vehicle" (the property that makes this different from `LatestMeasuredCapacity`). |
| **T7** | Row for `tesla_id = 777001`, `effective_period = '2026-03-01'` | `CapacityForMonth(ctx, 777001, 2026-04-01)` — the adjacent month, no row | `(nil, false)` | **A neighboring month with no row is genuinely "not found"**, not accidentally matched by a loose date comparison. |

## Risks

None identified beyond the ordinary risk of any new read: a future edit to
`monthly_effective_capacity`'s schema (an unlikely column drop, since RM52 tier 1's own
design settled its shape) would need `EffectiveCapacityForPeriod`'s `SELECT` list revisited
alongside `LatestMeasuredCapacity`'s. Both already read the same table and would already need
the same attention. This tier reads one existing, already-indexed column — no new failure
mode is introduced.
