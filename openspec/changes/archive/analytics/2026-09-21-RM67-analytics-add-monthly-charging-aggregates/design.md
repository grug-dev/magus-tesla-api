# Design — RM67-analytics-add-monthly-charging-aggregates

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **RD1–RD17** in
> `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md` and from the archived tier
> 2 change's own **D1–D8** (`openspec/changes/archive/analytics/2026-09-19-RM67-analytics-add-vehicle-monthly-metrics/design.md`).
> All of those are settled and binding; this document does not re-open any of them.

## Context

Five facts shape everything below.

1. **The table already has every column this tier needs, `NOT NULL DEFAULT 0`.** Tier
   2's one migration created `ext_ac_*`, `ext_dc_*`, `sc_*` already (RD1). This tier
   changes no schema — only what Go computes before the existing
   `UpsertVehicleMonthlyMetric` call.
2. **`UpsertVehicleMonthlyMetric` already accepts every parameter this tier needs.**
   Checked against `internal/analytics/db/query.sql` (lines 357–420): its parameter
   list already names `ExtAcEnergyKwh`, `ExtAcCost`, `ExtAcEntryCount`,
   `ExtAcEndingBatteryDist`, and the matching `ExtDc*`/`Sc*` triples. Tier 2's own D8
   built this on purpose, so tier 3 would never need to touch the statement. No sqlc
   query change of any kind is needed by this tier.
3. **Both reads this tier needs already exist** (roadmap RD9, re-checked here):
   `charging.Reader.ListEntriesByVehicleBetween(ctx, teslaID, from, to)` and
   `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween(ctx,
   teslaID, from, to)`. Both are already imported by this module's `recalculator`
   (`internal/analytics/recalculate.go`) for a different use case, so importing them
   into `monthlySyncer` too adds no new dependency edge to the module graph.
4. **`NewMonthlySyncer` is called from nowhere yet.** Tier 4
   (`RM67-app-add-monthly-metrics-step`) is the first real caller and has not started.
   Widening its constructor signature is free — no other file in the repository breaks.
5. **A month-boundary construction in Go is exactly what `make tz-guard` exists to
   catch**, and this tier is the first one that needs Go-side month boundaries at all
   (tier 2's D2 kept month normalization entirely in SQL, because it never needed a
   `time.Time` boundary to hand to another module's port). `charging`'s two range reads
   take `time.Time` bounds, so this tier cannot avoid producing them in Go. D1 below is
   how it does that without hand-rolling a "midnight of some day" construction.

## D1 — Month boundaries via day-count arithmetic on `period`, never `time.Date`

**Decision.** A small unexported helper computes the target month's first and last day
from the `period` parameter `SyncMonth` already receives, using only
`time.Time.AddDate` — never `time.Date` with a zeroed time-of-day, which is the exact
shape `make tz-guard` greps for outside `internal/clock`:

```go
// monthBounds returns the first and last calendar day of the month containing
// period, using only day-count arithmetic on the caller-supplied value -- never a
// fresh "midnight of some day" construction, which only internal/clock may build.
// Only period's year and calendar month matter, matching SyncMonth's own
// any-day-in-month contract.
func monthBounds(period time.Time) (start, end time.Time) {
	start = period.AddDate(0, 0, 1-period.Day())
	end = start.AddDate(0, 1, -1)
	return start, end
}
```

`start.AddDate(0, 1, -1)` is safe from Go's month-length rollover gotcha (e.g. Jan 31
`.AddDate(0,1,0)` lands on March 3, not Feb 28) because `start` is always day 1 of a
month before this call runs — adding one month to a day-1 date always lands on day 1 of
the next month exactly, and stepping back one day from that is always the last day of
the target month.

**Rejected alternative: `time.Date(period.Year(), period.Month(), 1, 0, 0, 0, 0,
period.Location())`.** This is precisely the construction `ai/go-conventions.md` and
`make tz-guard` ban outside `internal/clock` — a locally-built "midnight of a day".
Doing it here would repeat the mistake tier 1 and tier 2 already avoided by keeping
their own month math in SQL (tier 1 design.md D4; tier 2 design.md D2). `monthBounds`
avoids the pattern entirely: it never constructs a new absolute moment from raw
year/month/day components, it only shifts an already-valid one. This mirrors
`monthly_figures.go`'s own `isWeekend`, whose comment already makes the same point for
a `DATE` value: "this needs no `internal/clock` call, unlike bucketing a raw timestamp
would."

**Where this feeds in.** `monthBounds` returns `time.Time` values used only as `from`/
`to` arguments to `charging.Reader.ListEntriesByVehicleBetween` and, widened by one day
each side, to `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween`
(D5). Neither value is ever compared against a raw `time.Now()` or reused for anything
`internal/clock` already owns.

## D2 — `chargeTally`: one small accumulator, shared by all three charging sources

**Decision.** A single unexported type accumulates one charging source's month —
energy, cost, record count, and the ending-battery distribution:

```go
// chargeTally accumulates one charging source's month: total energy, total cost,
// how many records counted, and the ending-battery-percentage distribution across
// those records. A record with no EndBatteryPct still counts (add still increments
// count) but contributes to no bucket -- mirrors monthly_figures.go's
// bucketAccumulator's own "count without contributing to every sum" shape.
type chargeTally struct {
	energyKWh float64
	cost      float64
	count     int
	dist      EndingBatteryDist
}

// add folds one record into the tally. energyKWh and cost are the record's own
// already-dereferenced values -- callers pass 0 for a nil source field (RD13's "a
// nil contributes 0 to its sum, and the record still counts" rule), never a
// fabricated non-zero default.
func (t *chargeTally) add(energyKWh, cost float64, endBatteryPct *int) {
	t.energyKWh += energyKWh
	t.cost += cost
	t.count++
	if endBatteryPct != nil {
		t.dist.addToBucket(*endBatteryPct)
	}
}
```

**Naming, against RD10 and the `golang-naming` skill.** `chargeTally` names the domain
word ("charge") plus a real noun ("tally" — a running count/sum), not a banned generic
suffix (`Processor`, `Manager`, `Handler`, `Helper`, `Data`, `Info`, `Object`, `Thing`).
It does not stutter against the package name (`analytics.chargeTally`, not
`analytics.AnalyticsChargeTally`). It stays unexported: nothing outside
`monthly_charging.go` needs to construct or read one directly — `SyncMonth` reads only
the three tallies' final fields once, to set `VehicleMonthlyMetrics`'s own exported
fields.

**Rejected alternative: three separate `float64`/`int`/`EndingBatteryDist` locals per
source, no shared type.** This would need the same four-way bookkeeping (energy, cost,
count, dist) written out three times inside `aggregateChargingMonth` (D4) — once per
source — instead of one `add` call per record. `chargeTally` is exactly the kind of
small, closed vocabulary the project's AI-efficiency rule favours: one type an agent
looks up instead of re-deriving the same four-field bookkeeping by hand for AC, DC, and
Supercharger.

## D3 — Ending-battery bucketing: `addToBucket`, half-open edges (RD17)

**Decision.** A method on `EndingBatteryDist` decides which one bucket a percentage
falls into:

```go
// addToBucket increments the one bucket pct falls into. Edges are half-open --
// [0,20) [20,40) [40,60) [60,80) -- except the last, [80,100], which is closed so
// a percentage of exactly 100 has a home (RD17).
func (d *EndingBatteryDist) addToBucket(pct int) {
	switch {
	case pct < 20:
		d.Bucket0To20++
	case pct < 40:
		d.Bucket20To40++
	case pct < 60:
		d.Bucket40To60++
	case pct < 80:
		d.Bucket60To80++
	default:
		d.Bucket80To100++
	}
}
```

Both `charging.Entry.EndBatteryPct` and `charging.Session.EndBatteryPct` are already
DB-`CHECK`-constrained to `0`–`100` inclusive when non-nil, so `default` is reached only
by `80` and `100` — never by a value outside the documented range.

## D4 — `aggregateChargingMonth`: the one pure function RD8 asks for

**Decision.** One function, taking already-fetched slices plus the month's bounds,
returns the three tallies. No database access, no port call, no `context.Context` —
this is the "all maths happens in Go, in one place" RD8 already settled for the
`vehicle_metrics` half (`monthly_figures.go`'s `deriveMonthlyFigures`); this is that same
shape for the charging half.

```go
// aggregateChargingMonth folds one month's external charges and Supercharger
// sessions into three tallies: external AC, external DC, Supercharger. entries and
// sessions are assumed already scoped to one vehicle; sessions is assumed already
// fetched with the one-day-each-side widened window D5 requires -- this function
// does the platform-zone filtering that widening exists to make possible.
func aggregateChargingMonth(entries []charging.Entry, sessions []charging.Session, monthStart, monthEnd time.Time) (ac, dc, sc chargeTally) {
	for _, e := range entries {
		if e.ChargingType == nil {
			continue // RD13: no type, no bucket -- skipped entirely, not even counted
		}
		energy := 0.0
		if e.EnergyAddedKWh != nil {
			energy = *e.EnergyAddedKWh
		}
		switch *e.ChargingType {
		case "AC":
			ac.add(energy, e.Price, e.EndBatteryPct)
		case "DC":
			dc.add(energy, e.Price, e.EndBatteryPct)
		}
	}

	for _, s := range sessions {
		day := clock.CalendarDay(s.ChargeStopDateTime, clock.Zone())
		if day.Before(monthStart) || day.After(monthEnd) {
			continue // RD14: the widened fetch returns some neighboring-month sessions on purpose
		}
		energy := 0.0
		if s.EnergyKWh != nil {
			energy = *s.EnergyKWh
		}
		cost := 0.0
		if s.TotalCost != nil {
			cost = *s.TotalCost
		}
		sc.add(energy, cost, s.EndBatteryPct)
	}

	return ac, dc, sc
}
```

**RD16, made concrete.** No `Status` check anywhere in the loop above — every
`charging.Entry` in the fetched window counts, `IN_PROGRESS` and `DONE` alike.

**RD15, made concrete.** No `Currency`/`Currency`-pointer check anywhere — `Price` and
`TotalCost` are summed as-is, whatever currency the record itself carries. The caller
(`SyncMonth`) always writes the stored `currency` column as `"COP"`, unconditionally —
this function never reads or reports a currency at all.

**Rejected alternative: filter to `Status == StatusDone` only.** The ticket's own field
is silent on this, and RD16 settles it explicitly: every entry counts. Filtering would
also disagree with `ext_ac_entry_count`/`ext_dc_entry_count`'s documented meaning
("count of the same entries") — a reader summing entries by hand from
`charging.Reader.ListEntriesByVehicleBetween` and getting a different count than this
column would have no way to know a silent status filter was the reason.

## D5 — Supercharger month membership: widen the fetch, filter by platform-zone day (RD14)

**Decision.** `SyncMonth` fetches Supercharger sessions with the window widened by one
calendar day on each side of the month:

```go
monthStart, monthEnd := monthBounds(period)

sessions, err := s.supercharger.ListSessionsByVehicleBetween(
	ctx, teslaID, monthStart.AddDate(0, 0, -1), monthEnd.AddDate(0, 0, 1),
)
```

`aggregateChargingMonth` (D4) then drops every session whose `ChargeStopDateTime`,
converted to the platform zone via `clock.CalendarDay(t, clock.Zone())`, falls outside
`[monthStart, monthEnd]`.

**Why the widening is necessary, not defensive.** `SessionReader`'s own doc comment
states its `[from, to]` window is **whole UTC calendar days**. The platform zone
(`America/Bogota`) is five hours behind UTC with no DST, so a session whose
`ChargeStopDateTime` is, for example, `2026-04-01T02:00:00Z` has a UTC calendar day of
April 1 but a Bogota calendar day of March 31 (`2026-03-31T21:00:00-05:00`) — the *last*
day of March. Fetching only `[monthStart, monthEnd]` in UTC-day terms would never return
that row at all, because its UTC day sits one day past `monthEnd`. Widening by exactly
one day on each side is enough to guarantee every session whose *platform-zone* day
could fall inside the month is actually fetched — the maximum possible drift between a
UTC calendar day and a Bogota calendar day for the same instant is one day, in either
direction.

**Why the filter is still needed after widening.** The widened window also returns
sessions genuinely outside the month (e.g. a session late on the UTC day before
`monthStart`, whose Bogota day is also outside the month) — RD14 requires those excluded
from this month's tally, and the comment in `aggregateChargingMonth` names this
explicitly: "the widened fetch returns some neighboring-month sessions on purpose."

**External charges need neither widening nor a zone conversion.**
`charging.Entry.ChargedOn` is already a `DATE` column ("midnight UTC of the charge day"
— its own field comment), with no time-of-day to reinterpret in another zone. Fetching
`[monthStart, monthEnd]` exactly and trusting every returned row as belonging to the
month matches this platform's existing rule that a returned `Date` is a final bucket key,
never re-projected through the caller's own day math (roadmap RD6, stated for
`vehicle_metrics.metric_date`; the same reasoning applies here because `ChargedOn` is
the identical kind of value — a bare calendar day with no zone attached).

## D6 — `monthlySyncer` gains two read-only dependencies; `NewMonthlySyncer` widens for free

**Decision.** `monthlySyncer`'s struct and both constructors gain the two ports D4/D5
need, mirroring `recalculator`'s existing field style
(`internal/analytics/recalculate.go`) — the exact instruction this tier's dispatch gave:

```go
type monthlySyncer struct {
	q            *analyticsdb.Queries
	capacity     charging.MonthlyCapacityReader
	charges      charging.Reader
	supercharger charging.SuperchargerSessionAnalyticsReader
}

func newMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader, charges charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader) *monthlySyncer {
	return &monthlySyncer{q: analyticsdb.New(pool), capacity: capacity, charges: charges, supercharger: supercharger}
}
```

`analytics.go`'s public `NewMonthlySyncer` takes the same two extra parameters and
passes them straight through:

```go
func NewMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader, charges charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader) MonthlySyncer {
	return newLoggingMonthlySyncer(newMonthlySyncer(pool, capacity, charges, supercharger))
}
```

**Field naming.** `charges` (not `manual`, `entries`, or `reader`) names what the field
holds without repeating the package name (`charging.Reader` already says "charging");
`supercharger` matches `recalculator`'s own field name for the identical port type,
so a reader who already knows `recalculate.go` recognizes the shape immediately.

**Why this is free.** Fact 4 above: nothing outside this module constructs a
`MonthlySyncer` yet. `grep -rn "NewMonthlySyncer("` finds only `analytics.go`'s own
declaration and `db_monthly_sync_integration_test.go`'s `newTestMonthlySyncer` helper —
both change in this same tier, and no `cmd/` file exists yet that would also need
updating. Tier 4 is the first production call site, and it has not started.

**`SyncMonth`'s own signature does not change** — no `vehicleref.Ref`, no new
parameter. Only the constructor widens; the port's public contract from a caller's
point of view is untouched.

## D7 — No SQL, no migration, no new `charging` port

Restated plainly, because the dispatch instructions require an explicit answer:

- **Database object (table/column/index/constraint/view/migration): none.** Tier 2's
  migration already created every column this tier writes to
  (`20260918000002_add_vehicle_monthly_metrics.sql`). This change adds no migration
  file.
- **sqlc query change: none.** `UpsertVehicleMonthlyMetric` (fact 2) and
  `VehicleMetricsForVehicleAndMonth` already cover every parameter and every read this
  tier needs. Neither statement in `internal/analytics/db/query.sql` changes.
- **New `charging` port: none.** Confirmed again in code (fact 3), not only assumed
  from the roadmap: `internal/charging/charging.go`'s `Reader` and
  `SuperchargerSessionAnalyticsReader` interfaces already export exactly the two
  methods this tier calls, unchanged.

Because none of the three applies, the database design gate (`openspec/config.yaml`
`rules: design:` — "any new/changed database object ... design.md is REQUIRED ... never
skipped") is not triggered by this tier. This section is the record that the check was
made, not skipped.

## D8 — Doc-comment cleanup this tier is responsible for

Three existing doc comments in `internal/analytics/analytics.go` currently say "a later
change computes them" about the exact fields this tier fills. Each is edited in the same
task that touches the surrounding code (never swept file-wide beyond that), replacing
the forward-reference with the real, current state — no decision ID, tier number, or
ticket ID goes into any of them, per this project's comment rule:

- `EndingBatteryDist`'s doc comment (currently: "The zero value ... is what this version
  writes for all three charging sources; a later change computes the real counts.") —
  becomes a comment describing the five fixed buckets only, with no forward reference,
  since a real distribution is now always computed.
- `VehicleMonthlyMetrics`'s doc comment (currently: "ExtAC*, ExtDC*, SC*, and Currency
  hold their documented zero value until a later change computes them ... — every other
  field holds a real figure as of this port.") — becomes a comment saying every field
  holds a real figure, full stop.
- `MonthlySyncer.SyncMonth`'s doc comment (currently: "ExtAC*, ExtDC*, and Currency come
  back at their documented zero value as of this version ... Every other field reflects
  this month's real figures.") — same cleanup: drop the forward reference, state that
  every field reflects the month's real figures.

## D9 — Naming compliance summary (RD10)

Every new identifier this tier introduces, checked against the banned-suffix list
(`Processor`, `Manager`, `Handler`, `Helper`, `Data`, `Info`, `Object`, `Thing`) and
against the `golang-naming` skill's anti-stutter and MixedCaps rules:

| Identifier | Kind | Passes because |
|---|---|---|
| `chargeTally` | unexported struct | domain word ("charge") + real noun ("tally"); no banned suffix |
| `monthBounds` | unexported func | verb-free descriptive name; no banned suffix |
| `aggregateChargingMonth` | unexported func | verb + domain words; no banned suffix |
| `addToBucket` | unexported method on `EndingBatteryDist` | verb + domain word; no banned suffix |
| `charges`, `supercharger` | unexported struct fields | nouns naming what each field holds; no stutter against `charging.Reader`/`charging.SuperchargerSessionAnalyticsReader`'s own names |

No exported identifier is added or renamed by this tier — `NewMonthlySyncer` and
`MonthlySyncer` already existed; only their parameter lists change.

---

## Test Contract (authored before implementation)

Every expected value below is computed by hand, from the fixture rows, before any of
`chargeTally`, `aggregateChargingMonth`, or the widened `SyncMonth` exists. A test
written after reading the implementation would only confirm what the code happens to
do — these numbers are the contract the code must match instead.

### Offline / pure tests — `monthly_charging_test.go` (no database)

`aggregateChargingMonth` takes plain slices and returns plain values — every case below
runs with no port, no fake, and no database, exactly like `monthly_figures_test.go`'s
existing `deriveMonthlyFigures` tests.

**T-CHG-1 — AC/DC split, a nil `ChargingType` skipped entirely, nil pointers contribute
zero but still count.**

Input `entries` (five, `monthStart`/`monthEnd` irrelevant to this case — no sessions):

| # | ChargingType | EnergyAddedKWh | Price | EndBatteryPct |
|---|---|---|---|---|
| 1 | `"AC"` | 10.0 | 15000 | 45 |
| 2 | `"AC"` | nil | 5000 | nil |
| 3 | `"DC"` | 30.0 | 60000 | 85 |
| 4 | nil | 100.0 | 99999 | 50 |
| 5 | `"AC"` | 5.0 | 8000 | 15 |

Expected `ac`: `count = 3` (rows 1, 2, 5), `energyKWh = 15.0` (10.0 + 0 + 5.0),
`cost = 28000` (15000 + 5000 + 8000), `dist = {0-20: 1, 20-40: 0, 40-60: 1, 60-80: 0,
80-100: 0}` (row 1 → 40-60, row 5 → 0-20, row 2 contributes no bucket).

Expected `dc`: `count = 1`, `energyKWh = 30.0`, `cost = 60000`,
`dist = {0-20: 0, 20-40: 0, 40-60: 0, 60-80: 0, 80-100: 1}`.

Row 4 (nil `ChargingType`) must not appear in either tally's count, energy, cost, or
dist — this is the case that proves RD13.

**T-CHG-2 — bucket edges, half-open except the last.**

Input: six `"AC"` entries, `EndBatteryPct` = 0, 20, 40, 60, 80, 100 respectively (energy
and cost irrelevant — assert `dist` only).

Expected `ac.dist = {0-20: 1, 20-40: 1, 40-60: 1, 60-80: 1, 80-100: 2}` — `80` and `100`
both land in the closed last bucket; every other edge value opens its own bucket rather
than falling into the one below it.

**T-CHG-3 — Supercharger sessions: platform-zone month membership, both directions,
across the widened window.**

`monthStart = 2026-03-01` (UTC midnight), `monthEnd = 2026-03-31` (UTC midnight) — the
values `monthBounds` returns for any `period` inside March 2026. Input `sessions`:

| # | ChargeStopDateTime (UTC) | Bogota day | EnergyKWh | TotalCost | EndBatteryPct | Expected |
|---|---|---|---|---|---|---|
| A | `2026-03-01T02:00:00Z` | 2026-02-28 | 50 | 90000 | 90 | excluded |
| B | `2026-04-01T02:00:00Z` | 2026-03-31 | 40 | 70000 | 95 | included |
| C | `2026-03-15T12:00:00Z` | 2026-03-15 | 20 | 30000 | 55 | included |
| D | `2026-03-10T08:00:00Z` | 2026-03-10 | nil | nil | nil | included, no bucket |

Row A's UTC day (March 1) is inside the month, but its Bogota day (Feb 28) is not —
proves the platform-zone conversion, not UTC, decides membership. Row B's UTC day
(April 1) is outside the naive `[monthStart, monthEnd]` window, but its Bogota day
(March 31) is inside — proves the widened fetch is necessary, not merely defensive.

Expected `sc`: `count = 3` (B, C, D), `energyKWh = 60.0` (40 + 20 + 0),
`cost = 100000` (70000 + 30000 + 0),
`dist = {0-20: 0, 20-40: 0, 40-60: 1, 60-80: 0, 80-100: 1}` (C → 40-60, B → 80-100, D
contributes no bucket).

### DB-integration tests — extending `db_monthly_sync_integration_test.go` (real database)

These exercise `SyncMonth` end-to-end: the real widened `ListSessionsByVehicleBetween`
call, the real `ListEntriesByVehicleBetween` call, and the real `UpsertVehicleMonthlyMetric`
write, against a live Postgres. Fixtures use `charging.NewWriter(pool).Create` for
external entries (a public writer exists) and a direct SQL `INSERT` for Supercharger
sessions (no public writer creates a bare session — this module's own established
fixture pattern, `internal/analytics/db_integration_test.go`'s `seedSuperchargerSession`
convention for the identical case).

**T-CHG-INT-1 — external charges, seeded through the real writer, split by type.**

Seed, for one `teslaID`, four `charging.Entry` rows via `charging.NewWriter(pool).Create`,
`ChargedOn` all inside March 2026: two `"AC"` (one `DONE` with `EnergyAddedKWh`/
`EndBatteryPct` set, one `IN_PROGRESS` with neither — RD16 says it still counts), one
`"DC"`, one with `ChargingType` left nil. Call `SyncMonth(ctx, teslaID, <a March 2026
day>)`.

Expected: `ExtACEntryCount = 2`, `ExtDCEntryCount = 1`, the nil-type entry counted in
neither. `ExtACEnergyKWh`/`ExtACCost` equal the sum of the two AC rows' own values (the
`IN_PROGRESS` row contributing 0 energy if `EnergyAddedKWh` was left nil, its `Price`
counted regardless). `ExtACEndingBatteryDist` has exactly one bucket populated (the
`DONE` row's); the `IN_PROGRESS` row contributes to none.

**T-CHG-INT-2 — Supercharger sessions across the month boundary, seeded by direct SQL.**

Seed two `charging.supercharger_sessions` rows directly: one with
`charge_stop_date_time` at `2026-04-01T02:00:00Z` (Bogota March 31 — must be picked up
by March's sync despite its UTC day being in April) and one at `2026-03-01T02:00:00Z`
(Bogota February 28 — must NOT be picked up by March's sync despite its UTC day being in
March). Call `SyncMonth` for March 2026, then again for February 2026.

Expected: March's `SCSessionCount` includes the first row and excludes the second;
February's `SCSessionCount` includes the second row and excludes the first. This is the
one test in this tier that exercises the real widened `ListSessionsByVehicleBetween`
call against actual stored rows, not a hand-built slice.

**T-CHG-INT-3 — re-running `SyncMonth` for the same month is idempotent, mirroring tier
2's own T6.**

Seed one AC entry and one Supercharger session for a month, call `SyncMonth` twice for
the identical `(teslaID, period)`. Expected: both calls return the identical `ExtAC*`/
`SC*` figures — the second call must not double-count, because `UpsertVehicleMonthlyMetric`
overwrites the whole row rather than adding to it (tier 2 design.md D8, still in force).

---

## Not part of this tier's design gate

No database object is new or changed (D7). This change does not go through the
`database` Design-Gate a second time — tier 2 already did, for the table this tier only
writes into.
