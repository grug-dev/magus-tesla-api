# Battery Consumed Graph — how it works today

> **What this is:** a walkthrough of the pipeline behind the **Battery Consumed** bar chart on
> `/ui/dashboard/history` — where each number is calculated, when it is written, and what does
> (and does not) happen when you edit a charge record.
>
> Shipped by roadmap **RM28** (ticket MAG-15). The binding design decisions are cited below as
> **D1**…**D21** and live in
> [`openspec/roadmaps/archive/RM28-battery-consumed-graph/RM28-battery-consumed-graph.md`](../openspec/roadmaps/archive/RM28-battery-consumed-graph/RM28-battery-consumed-graph.md).
> That file is the authority on *why*; this one describes *what runs*.

**Code references in this document name files and Go symbols, not line numbers** — a symbol
survives refactoring, a line number is stale as soon as anyone edits the file. The only line
numbers here are for migration DDL, which is effectively append-only.

Three facts drive everything below, and all three surprise people:

1. **The consumed percentage is never stored.** It is recomputed on every dashboard request (**D2**).
2. **`charge_gaps` is reconciled — rows are both inserted *and* deleted — but only by the nightly
   poller** (**D7b**). No HTTP request ever touches that table.
3. **`supercharger_sessions.start_battery_pct` / `end_battery_pct` are always NULL.** No code in
   this repository can write them (**D14**), so every Supercharger day flags as a gap.

---

## 1. What the chart shows

`/ui/dashboard/history` renders three bar charts over the same calendar-day axis:

| Card | Question it answers |
|---|---|
| Odometer km/day | how far did the car go? |
| Battery %/day | how did the state of charge move? |
| **Battery consumed %/day** | **how much battery did driving actually use?** |

The third one needs its own pipeline because the stored column can't answer it.
`vehicle_snapshots.battery_used_pct_calc` is a bare night-to-night state-of-charge difference —
yesterday's level minus today's. On any day the car was charged, the battery went *up* overnight
while the car was also driven, so the raw number comes out small, zero, or negative. It is a
truthful reading of two snapshots and a useless answer to "what did I consume".

The correction adds back what the charge put in, using the charge records the platform already
stores.

---

## 2. The tables involved

| Columns | Owning module | Role |
|---|---|---|
| `vehicle_snapshots.battery_used_pct_calc`, `.distance_traveled_km_calc`, `.days_spanned_calc` | `internal/telemetry` | the raw nightly inputs |
| `manual_charge_entries.charged_on`, `.start_battery_pct`, `.end_battery_pct` | `internal/manualcharge` | charges you assert by hand (home / work / 3rd-party) |
| `supercharger_sessions.charge_stop_date_time`, `.start_battery_pct`, `.end_battery_pct` | `internal/telemetry` | sessions Tesla reports |
| `charge_gaps` | `internal/telemetry` | the pipeline's **output** — days whose math doesn't add up |

### The Supercharger percentages are always NULL

This is load-bearing, not a temporary state. `UpsertSuperchargerSession` in
`internal/telemetry/db/query.sql` **deliberately excludes** `start_battery_pct`,
`end_battery_pct`, `battery_pct_source` and the two `*_est` columns from both its INSERT list and
its `ON CONFLICT DO UPDATE SET` clause — there is a `LOAD-BEARING` comment above the query saying
so, mirrored in `upsertSuperchargerSession` in `internal/telemetry/service.go`. RM27 shipped those
five columns as *storage only*: the Fleet API carries no state-of-charge, and no entry UI exists
yet (**D14**, backlog entry 11).

So today the Supercharger side of the formula always contributes **0**. That is exactly the
condition gap detection looks for.

---

## 3. Where `battery_used_pct_calc` is written

**Nightly, on the collection write path — never on a web request.**

```
cmd/poller
└── telemetry.Collector.CollectAll        (internal/telemetry/service.go)
    └── attemptVehicle                    per vehicle
        ├── previousSnapshot              find the predecessor row
        ├── deriveConsumption             compute the five derived columns
        └── insertSnapshot                one row per vehicle per calendar day
```

`deriveConsumption` (`internal/telemetry/service.go`) is where the number comes from:

```go
distance    := cur.OdometerKm - prev.OdometerKm
batteryUsed := prev.BatteryLevelPct - cur.BatteryLevelPct   // yesterday minus today
days        := int(cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24)
```

Notes that matter downstream:

- **Stored raw, never clamped.** A negative `batteryUsed` (net overnight charge) is persisted as-is.
  The two efficiency columns (`km_per_pct_calc`, `estimated_range_km_calc`) are the exception —
  they are computed only when `batteryUsed > 0`, because a zero or negative divisor has no truthful
  ratio.
- **No predecessor ⇒ all five columns stay NULL.** `deriveConsumption(nil, cur)` returns `cur`
  untouched, so a vehicle's first-ever snapshot makes no claim either way.
- **`previousSnapshot` is bounded by *local* midnight** (`dayStart`), so a same-day re-capture can
  never pick the row it is about to replace as its own predecessor.

### The day-attribution rule (D1) — read this before anything else

The poller runs at ≈03:30 and captures the state accumulated over the **prior** day. So:

> The row captured 03:30 on **Aug 14** describes **Aug 13**.

That row carries `battery_used_pct_calc = level(Aug 13 03:30) − level(Aug 14 03:30)` — the delta
across Aug 13 — and `internal/telemetry/mapping.go` derives `EffectiveDate = CapturedAt − 1 day`
to label it. Value and label already agree, so **nothing needs shifting**. RM28 originally planned
a migration to move these columns onto the predecessor row and dropped the whole tier once this was
found (**D1**); don't re-propose it.

---

## 4. Where the consumed figure is derived, and when

**Computed on read. Never stored. There is no cache table** (**D2**).

The window is small — at most 90 days for one account and one vehicle, so roughly 90 snapshot rows
plus a handful of charge rows. Recomputing also makes the invalidation problem disappear: a manual
entry backdated by a year needs no resync, because there is nothing to invalidate.

### The port

`internal/battery/battery.go`:

```go
ConsumedByDay(ctx, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error)
```

The result is **sparse** — a missing day means *no data*, not zero. Each `DayConsumption` carries
`Date`, `ConsumedPct`, `DistanceKm`, `Flagged`, `MissingChargingType`, `DaysSpanned`.

### The I/O

`reader.ConsumedByDay` in `internal/battery/reader.go` makes exactly three port calls — never a
database, `internal/battery` owns no tables at all:

| Port | Window fetched |
|---|---|
| `telemetry.SnapshotsByVehicleBetween` | `[start−1, end+1]` |
| `telemetry.SuperchargerSessionsByVehicleBetween` | `[start−1, end+2]` |
| `manualcharge.ListEntriesByVehicleBetween` | `[start−1, end]` |

The one-day **lookback** is required, not defensive (**D9a**): day `D`'s stored delta was computed
against its predecessor row, so bounding day `D`'s interval needs the `D−1` row. The over-fetch on
the far end covers the timezone shift; the pure derivation re-filters every row against its own
predicate, so reading extra rows can cost time but can never change a result.

### The math

`deriveConsumedByDay` in `internal/battery/consumed.go` — pure, zero I/O:

```
consumed = battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)
```

summed over **all** charge events matched to that day, across **both** tables, neither taking
precedence (**D13**).

Worked example from the ticket — snapshots 22 % → 73 %, one manual charge 18 % → 80 %:

```
battery_used_pct_calc = 22 − 73 = −51
charge                = 80 − 18 = +62
consumed              = −51 + 62 = 11 %
```

The Σ form is required, not merely tidier. Two sessions on one day (20→50 and 60→80) with snapshots
30 % → 75 %:

| Rule | Result |
|---|---|
| Sum every event (**shipped**) | **5** ✓ |
| Only the latest session that day | −25 ✗ — discards the earlier session, invents a gap |
| First start to last end | 15 ✗ — counts the driving *between* sessions as charging |

### Which day a charge belongs to (D12) — source-specific

- **Supercharger sessions** match by *instant*: `sumSuperchargerPctBetween` counts a session whose
  `ChargeStopDateTime` falls in `[prev.CapturedAt, cur.CapturedAt)`. This matters because a session
  ending 01:00 on Aug 14 belongs to Aug 13's 03:00→03:00 window; bucketing it by *date* would file
  it under the wrong day, flagging a false gap on one day and inflating the bar on the next.
- **Manual entries** match by *calendar day*: `sumManualPctBetween` counts entries whose
  `charged_on` falls in `(effectiveDay(prev), day]` — exclusive start, inclusive end. A manual entry
  carries only a date, and you picked it deliberately.
- Either side NULL contributes **0**, never an estimate.

### Which timezone decides a day (D6 / D18)

The **poller's** configured zone, not UTC. `effectiveDay(snapshot)` is the snapshot's own
`CapturedDate` minus one calendar day, and `CapturedDate` was already stamped in that zone on the
write path — so the zone reaches `internal/battery` inside the data and the module needs no
`*time.Location` of its own (**D18a**).

> **Known, accepted mismatch.** The odometer and battery charts still bucket in UTC via
> `effectiveDayUTC` in `internal/gateway/handlers/history.go`. The consumed chart therefore keys on
> `DayConsumption.Date` **verbatim** and must never re-bucket it — there is a `D-G2` comment on
> that line in `buildConsumedChart` saying exactly this.

### Two callers, one function

| Caller | Why |
|---|---|
| `buildHistoryView` (`internal/gateway/handlers/history.go`) | draws the chart |
| `newGapReconciler` (`cmd/poller/main.go`) | harvests only the `Flagged` bit |

The poller writes **no** consumed value anywhere. It runs the same derivation purely to decide
which days belong on the gap worklist.

---

## 5. `charge_gaps` — when a row appears, and when it disappears

### What flags a day (D5)

In `deriveConsumedByDay`:

```go
flagged := consumed < 0 || (consumed == 0 && distanceKm > minFlagDistanceKm)
```

`minFlagDistanceKm` is a named constant in `internal/battery/consumed.go` — **10 km**. The
zero-plus-distance clause catches the silent case where a charge exactly cancels the day's usage;
below 10 km, "zero consumed, barely moved" is a plausible parked day rather than a data gap.

Days whose `battery_used_pct_calc` is NULL — a vehicle's first-ever snapshot — are **skipped, never
flagged** (**D5a**). No predecessor means no claim either way, and flagging would be a fabricated
finding.

### Which source is blamed (D7a)

`inferMissingChargingType` returns `SUPERCHARGER` when a session in the interval has either
percentage NULL (we know exactly which record needs filling), and `MANUAL` otherwise (the car was
charged somewhere the Fleet API doesn't report).

### When rows are written — nightly, last, and only after a successful cycle

```
cmd/poller
└── reconcilingCollector.CollectAll
    ├── inner.CollectAll                  the snapshot poll  ← must succeed first (D4)
    └── reconcile → newGapReconciler
        ├── window = last 30 days, ending YESTERDAY in the poller's zone
        └── per registered vehicle:
            ├── battery.ConsumedByDay(...)
            ├── keep the days where Flagged
            └── telemetry.GapWriter.ReconcileWindow(...)
```

Details worth knowing:

- **Why a decorator.** `reconcilingCollector` wraps the `telemetry.Collector` port rather than
  calling reconciliation beside it. `Scheduler.Run` owns its own loop and calls `CollectAll`
  internally, so there is no seam after a *scheduled* cycle — wrapping the port is what makes the
  scheduled path and `--once` share the step by construction (**D4a**).
- **Order is a dependency.** Detection reads the night's freshly written snapshot, so a failed
  collection cycle returns early and reconciliation does not run at all (**D4**).
- **Window:** `battery.GapReconciliationWindow` = **30 days**, ending **yesterday** in the poller's
  zone (today's data isn't captured until tomorrow's poll).
- **Errors never fatal.** Per-vehicle isolation, logged with a `gap reconciliation:` prefix. A
  missed run self-heals next cycle, because `Flagged` is recomputed from scratch every time rather
  than accumulated.

### `ReconcileWindow` — the insert *and* the delete

`internal/telemetry/gap_writer.go`. One call does the whole reconciliation:

1. **Validate first.** Every entry is checked against the call's `(accountID, teslaID)` scope and
   the `[start, end]` window *before* a transaction is opened — a caller bug rejects the whole call
   with no partial write.
2. **Load** the stored gap dates for that vehicle in the window.
3. **Delete** every stored day that is no longer flagged.
4. **Upsert** every flagged day (the UNIQUE constraint makes this idempotent — a still-flagged day
   is refreshed in place, and `created_at` survives).
5. **Commit.** All-or-nothing.

Contract points: an **empty** flagged list is legal and clears the window; days **outside**
`[start, end]` are never read or touched.

### Table shape

`internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` (DDL at lines 59-71; the file
opens with ~58 lines of rationale worth reading):

- `UNIQUE (account_id, tesla_id, gap_date)` — **one row per vehicle-day**, deliberately *not* per
  day-and-type. One combined shortfall can't be split into a MANUAL and a SUPERCHARGER share, so a
  second row would be speculative.
- `missing_charging_type` — `CHECK IN ('MANUAL','SUPERCHARGER')`, NOT NULL.
- **No `resolved_at`, no soft delete** — this is a *live worklist*, not an audit trail (**D7b**).
  The consuming notification wants "what is outstanding", not a history of resolved days.
- No cross-module foreign keys on `account_id` / `tesla_id`; no `raw_data` JSONB.
- Index `idx_charge_gaps_account (account_id, gap_date DESC)`.

### Nothing reads it yet

In production the only `SELECT` against `charge_gaps` is `ChargeGapDatesByVehicleBetween` — the
write path's own bookkeeping for step 2 above. There is **no public read port**, no notification,
no UI. That was always a separate ticket; `idx_charge_gaps_account` exists for that future
account-wide read.

### `charge_gaps` is a lower bound, not the full list (D14a)

The detection rule only catches days that go **negative**, or sit at **zero with more than 10 km
driven**. A charge that *partially* offsets a day's driving leaves a **positive, plausible-looking,
silently understated** bar that nothing flags:

> Drove ~300 km (true consumption ~50 %), supercharged +30 % with NULL percentages ⇒ `consumed = 20`.
> Not flagged. The bar reads **20** instead of **50**.

This is inherent to the rule, not a bug to fix in code: with the percentages NULL there is no
quantity to add back, and no way to distinguish an understated day from a genuinely frugal one. Two
consequences: don't read a low Supercharger-day bar as fact, and treat `charge_gaps` as a **lower
bound** on days needing attention. The real fix is being able to enter the percentages (backlog
entry 11).

---

## 6. What happens when you create or edit a charge record

### Manual charge entries — the write path

Routes registered in `internal/gateway/gateway.go`:

| Route | Handler (`internal/gateway/handlers/charges.go`) | Service |
|---|---|---|
| `POST /ui/charges/create` | `ChargeCreate` | `manualcharge.Writer.Create` |
| `PUT /ui/charges/row/:id` | `ChargeRowUpdate` | `manualcharge.Writer.Update` |
| `DELETE /ui/charges/row/:id` | `ChargeRowDelete` | `manualcharge.Writer.Delete` |

Guard chain before every write: authenticated user → CSRF compare (`csrf_manualcharge`) →
tenant-ownership check returning 403. `UpdateEntry` and `DeleteEntry` are additionally
double-scoped on `(id, account_id)` in SQL, and `tesla_id` / `vin` / `account_id` are never in the
UPDATE SET list.

### After the write, nothing else happens

This is the direct answer to "shouldn't adding a manual charge clear the gap row?"

| Handler | What it does after the writer returns |
|---|---|
| `ChargeCreate` | discards the returned row, re-reads the page for display, renders the success fragment |
| `ChargeRowUpdate` | renders the row the writer just returned — doesn't even re-read |
| `ChargeRowDelete` | renders an empty row |

No transaction wrapper. No recompute. No cache bust. No event, no `HX-Trigger`. No `charge_gaps`
touch. The service layer is equally bare — one store call, map, return.

It couldn't be otherwise without a boundary change: **`internal/manualcharge` has zero imports of
`internal/telemetry`**, and its `AGENTS.md` forbids them, so it structurally cannot reach
`GapWriter`. The composition root `cmd/poller` is the only place that joins `battery`'s derivation
to `telemetry`'s writer (**D4a**).

### So when does your edit show up?

| What | When it reflects your edit |
|---|---|
| **The chart** | **Immediately** — next page load. `ConsumedPct` is recomputed on read (**D2**), so the bar is correct the moment you refresh. |
| **The `charge_gaps` row** | **Next successful nightly cycle** — *provided the day is still inside the 30-day window.* |

This lag is the design (**D7b**): fixing a charge entry clears the row automatically with no extra
wiring, because the nightly run recomputes the whole window and deletes whatever no longer flags.

> ⚠️ **The 30-day boundary is a real hole.** The reconciliation window ends yesterday and reaches
> back 30 days. Fix a charge for a day *older* than that and `ReconcileWindow` is never asked about
> it again, so the gap row survives permanently. See §8 candidate 1.

### Supercharger sessions — there is no edit path at all

Rows arrive from exactly one place: the nightly `dx/charging/history` import
(`collectChargingHistory` → `upsertSuperchargerSession` in `internal/telemetry/service.go`),
upserted on Tesla's `session_id`.

- The two supercharger routes in `internal/gateway/gateway.go` are **GET only**. No `POST`, `PUT`
  or `PATCH` exists for them anywhere in the repo.
- The only write query for the table is `UpsertSuperchargerSession`, and it deliberately excludes
  the battery-percentage columns (§2).

So a flagged Supercharger day cannot be resolved by editing the session — the only way to clear it
today is to add a **manual** entry for that day, until the verification UI lands (backlog entry 11).

> The `start_battery_pct` field you *can* edit in the UI belongs to `manual_charge_entries`, not to
> a Supercharger session.

---

## 7. How the chart is rendered

```
GET /ui/dashboard/history
└── Handler.DashboardHistoryFragment      (internal/gateway/handlers/history.go)
    ├── parseHistoryRange                 validate the window
    └── buildHistoryView
        ├── SnapshotsByVehicleBetween → buildOdometerChart, buildBatteryChart
        └── battery.ConsumedByDay      → buildConsumedChart
```

**Bar height** is `max(0, ConsumedPct)` scaled **relative to the window maximum** — the tallest bar
is 100 % of the canvas, mirroring the odometer chart rather than the battery chart's absolute
0–100 axis (**D19**). A typical 6–20 %/day range would leave an absolute axis nearly empty. The
trade-off: bar heights are **not comparable between two different windows** — the tooltip carries
the real percentage, so no information is lost.

**Markers are a set, not a single-valued enum** (**D21**) — both can appear on one bar:

| Marker | Meaning | Bar height | Tooltip |
|---|---|---|---|
| `fill-warning` | flagged day — a charge record is missing or incomplete (**D10**) | zero | names the suspected source; **the number is deliberately withheld** |
| `fill-info` | multi-day span, `DaysSpanned > 1` — a missed poll (**D20**) | its **real** value | "covers N days" |

Plotting a number we don't believe reads as a bug, and hiding the day entirely would make it
indistinguishable from having no data — hence the zero-height bar *with* a marker. A spanned day
keeps its real value because the consumption was genuinely observed, just over a longer window;
splitting it evenly across the span would invent a distribution never measured (**D8**).

**Templ:** `historyBarChart` in `internal/gateway/templates/fragments/history.templ` — a CSS grid
plus one `<rect>` per bar with a `<title>` tooltip, markers drawn as a second rect pass. The
consumed card uses `fill-accent`, against odometer's `fill-primary` and battery's `fill-secondary`.
View model: `HistoryBar` / `HistoryChart` in
`internal/gateway/templates/fragments/history_vm.go`.

**Date range** (`parseHistoryRange`):

- No parameters → last **7** calendar days ending **yesterday** (`historyRangeWindowDays = 6`).
- Otherwise both bounds are required, `YYYY-MM-DD`, `end >= start`, `end <= yesterday`, window
  ≤ **90** days (`historyRangeMaxDays`); anything else is HTTP 400 with all three charts empty.
- Both the default and the cap end at yesterday because today's row isn't captured until tomorrow's
  poll — `end = today` could only ever draw an empty trailing bar (**D11**).
- All three charts share one fixed calendar-day axis, so their labels line up by construction.
- Presets (`historyPresetDayCounts = {6, 14, 30}`) are built server-side as absolute hrefs, each
  ending yesterday. Note the label shows the offset `n`, while the window it selects spans `n + 1`
  inclusive days — same arithmetic as the default above.

**Independent failure:** a `ConsumedByDay` error empties **only** the consumed card and leaves
odometer and battery populated — never a 500.

---

## 8. Known gaps — ticket candidates

### 1. Gap rows older than the reconciliation window are orphaned forever

**Problem.** `newGapReconciler` reconciles a rolling 30-day window ending yesterday
(`battery.GapReconciliationWindow`), and `ReconcileWindow`'s contract never touches days outside
`[start, end]`. Once a day falls out of the window it is never re-evaluated, so a gap row for it
can never be deleted.

**Why it matters.** Add a manual charge for a day 35 days back and the chart corrects immediately,
but the gap row survives permanently — the future notification would nag you about a day you
already fixed. `GapReconciliationWindow`'s own comment says the window is "generous enough to catch
a manual-entry backfill days after the fact": true for recent backfills, silent about older ones.

**Where.** `cmd/poller/main.go` (`newGapReconciler`), `internal/battery/battery.go`
(`GapReconciliationWindow`), `internal/telemetry/gap_writer.go` (`ReconcileWindow`).

**Suggested shapes.** Either (a) sweep the stored gap dates unbounded — read all gap dates for a
vehicle, recompute just those days, delete the resolved ones; or (b) extend `ReconcileWindow` with
a variant that also reconciles stored dates found outside the window. (a) is bounded by the number
of *stored gaps* rather than by calendar days, which is the smaller set. Note this touches
`charge_gaps`, so it trips the pipeline's built-in `database` design gate — the schema, rationale
and index plan need confirming before implementation.

### 2. Nothing consumes `charge_gaps` yet

**Problem.** The table is write-only in production: no read port, no notification, no UI. The whole
point of persisting it — per the original ticket, "to notify the user later (In Another ticket)" —
is unbuilt.

**Where.** `internal/telemetry/telemetry.go` exposes only `GapWriter`; the index
`idx_charge_gaps_account (account_id, gap_date DESC)` already exists for the account-wide read.

**Suggested shape.** A `GapReader` port on `internal/telemetry` plus a dashboard surface listing
outstanding days, each linking to the manual-charge form pre-filled with that date. Remember
**D14a**: the list is a lower bound, so the copy shouldn't imply it's exhaustive.

### 3. The flag predicate is computed twice, independently

**Problem.** The gateway derives its warning marker live from `ConsumedByDay` and never reads
`charge_gaps`. Both paths go through the same pure function today, so they agree — but they are two
call sites of one rule, free to drift.

**Where.** `buildConsumedChart` (`markerFlagged: day.Flagged`) vs the persisted rows written by
`newGapReconciler`.

**Suggested shapes.** Either have the gateway read the persisted gaps (couples the UI to the
nightly cadence — the marker would then lag your edit by a day, which is arguably *worse*), or keep
the duplication deliberately and add a test pinning the two together. Leaning toward the second:
recompute-on-read is what makes the chart correct immediately.

### Already recorded in [`openspec/roadmaps/backlog.md`](../openspec/roadmaps/backlog.md)

- **Entry 11** — Supercharger start/end SOC verification UI. Until it lands, *every* Supercharger
  day flags as a gap, and the understatement in **D14a** stays invisible. RM28 makes this more
  valuable than it was.
- **Entry 12** — charging data is split across two modules (`manual_charge_entries` in
  `internal/manualcharge`, `supercharger_sessions` in `internal/telemetry`), so every consumer of
  "how was this car charged" must compose two ports. Scoped out of RM28 deliberately.
- **Entry 13** — the history charts key a `map[time.Time]` without normalizing the lookup side.
  Pre-existing in all three charts and currently unreachable through the UI; raised as a review
  finding on RM28 tier 4 and deferred.
