# Design — RM29-telemetry-drop-derived-columns

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md` (cited as **roadmap D1**,
> **roadmap D10**, …), from tier 3's archived design decisions (cited as **tier 3
> D9**, **tier 3 D13**, …), and from the binding interview outcomes in the dispatch
> prompt (cited as **I1**..**I4**). D1–D4 each carry exactly one interview outcome,
> named in their heading, verbatim in substance. D5 onward are decisions this
> artifacts pass had to make to turn I1–I4 into a buildable change; **D8b is the one
> the interview did not cover at all** and is called out as such.

## Context

Tier 3 made `internal/analytics` a precomputed read model with its own
`vehicle_metrics` table (roadmap D1). It did **not** make analytics compute the five
per-day consumption figures — it copies them, verbatim, out of `telemetry.Snapshot`:

```go
// internal/analytics/consumed.go, deriveVehicleMetrics (today)
DistanceTraveledKmCalc: cur.DistanceTraveledKmCalc,
BatteryUsedPctCalc:     cur.BatteryUsedPctCalc,
KmPerPctCalc:           cur.KmPerPctCalc,
EstimatedRangeKmCalc:   cur.EstimatedRangeKmCalc,
DaysSpannedCalc:        cur.DaysSpannedCalc,
```

Those five fields are produced by `deriveConsumption`
(`internal/telemetry/service.go:581`), called once per capture in `attemptVehicle`
against a predecessor fetched by the private `previousSnapshot` store seam, and
persisted as five columns on `vehicle_snapshots`. Nothing inside `telemetry` reads
them back.

So the tier's literal scope ("drop five columns") is not executable on its own: drop
them and analytics has nothing to copy. The tier is **"move the derivation from
telemetry to analytics, then drop the columns"** — which is also what makes tier 3's
`vehicle_metrics` the single source of these figures rather than the second one.

Three properties of today's arrangement shape the design:

1. **The derivation's only consumer is another module.** That is the boundary blur
   MAG-26 exists to fix, and the shape roadmap D6 rejects ("analytics pulls its
   inputs through ports it defines").
2. **Stored `_calc` values can already be stale.** `vehicle_snapshots` stopped being
   append-only at `20260805000001`: a same-day re-capture REPLACES the row. Telemetry
   derives `_calc` only for the row being written, so the *successor* row's `_calc`
   is never recomputed when its predecessor changes. Nothing in the repo detects
   this.
3. **The current lookback is only sufficient because telemetry pre-computed at write
   time.** `Reconcile` widens to `[minDay-1, maxDay+1]`
   (`internal/analytics/recalculate.go`), and `Recalculate` fetches
   `SnapshotsByVehicleBetween(start-1d, end+1d)` — a net two-calendar-day lookback
   before the earliest affected day. Once analytics derives the figures itself, a
   vehicle whose capture gap exceeds that window has no predecessor in the fetched
   slice at all.

## Goals / Non-Goals

**Goals**
- `internal/analytics` computes the five per-day consumption figures from raw
  `odometer_km` / `battery_level_pct` / `captured_date`, with output byte-for-byte
  identical to today's telemetry-side derivation for identical inputs (roadmap D10).
- `telemetry.Reader.SnapshotPrecedingDay` — the exact-predecessor lookup, reusing
  the existing index, no new DB object.
- `vehicle_snapshots` loses the five `_calc` columns; `telemetry.Snapshot` loses the
  five matching fields; telemetry loses `deriveConsumption` and its plumbing.
- Existing `vehicle_metrics` rows rebuilt via a watermark reset, with no one-off
  binary and no half-old/half-new table.
- No window in which the five values are unreachable — the task ordering is what
  guarantees this, and it is explicit in tasks.md.

**Non-Goals**
- Changing `vehicle_metrics`' schema. Tier 3 already created every column needed.
- `charge_gaps` (tier 5), `charge_sessions` (tier 6), `internal/app` (tier 7).
- `RecentEfficiency` — untouched, still live-computed.
- Fixing `SnapshotsByVehicleBetween`'s `LIMIT 400` (see Risks).
- Any gateway change. No gateway code reads a `Snapshot` `_calc` field.

## Decisions

### D1 — The derivation MOVES INTO analytics (carries I1)

`deriveConsumption` becomes analytics' code. `internal/analytics/consumption.go`
(new file) holds the pure function; `deriveVehicleMetrics` (`consumed.go`) calls it
during `Recalculate`. `telemetry` loses the five columns, the Go derivation and the
five `Snapshot` port fields.

**Rejected — keep the derivation in telemetry and compute it on read** (a
`Snapshot`-level method, or a derived-column-free `Reader` that joins to the
predecessor). This leaves `telemetry` owning a derivation whose only consumer is
another module — the exact shape roadmap D6 rejects — and it taxes **every** snapshot
read forever with a predecessor lookup, against the read-heavy Performance-Profile.
The whole point of tier 3's read model is that the figures are computed once, on a
write path, and read cheaply.

**Rejected — leave the columns and just re-point analytics.** That preserves the
boundary violation the tier exists to remove, and keeps the stale-successor bug
(Context 2) permanently unaddressed.

### D2 — Analytics gets the exact predecessor via a NEW telemetry port method (carries I2)

Added to `telemetry.Reader`:

```go
// SnapshotPrecedingDay returns the single most recent stored snapshot for one
// vehicle within the given account whose captured_date is strictly before `day`,
// or (nil, nil) when the vehicle has no earlier snapshot at all (its first-ever
// capture). `day` is a bare calendar date, UTC-midnight-normalized — the same
// representation Snapshot.CapturedDate already carries.
SnapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error)
```

**Rejected — widen the existing fixed 1-day/2-day lookback constants to N days.**
A gap longer than N yields a *silently wrong* delta, and a wrong `km_per_pct_calc`
looks like a plausible number on a chart, so nothing would ever flag it. There is no
value of N that is both cheap and correct; the exact lookup is both.

**Rejected — fetch the vehicle's whole history per `Recalculate`.** Correct, but
degrades linearly with history for no benefit over an indexed single-row lookup, and
would collide with `SnapshotsByVehicleBetween`'s `LIMIT 400`.

**Rejected — expose the existing private `previousSnapshot` seam verbatim.** Its
bound is an *instant* (`captured_at < @before`), and its only caller computes that
instant as `dayStart(capturedAt, s.location())` — the poller's configured timezone.
`internal/analytics` deliberately holds no `*time.Location` (tier 3 design D-B12: the
zone arrives already applied, stamped into `captured_date` on the write path), so it
cannot compute that bound. Handing it UTC midnight of `CapturedDate` instead would be
**wrong in any UTC+ poller zone**: a snapshot captured 03:30 local in Asia/Tokyo is
`captured_at` 18:30Z on the *previous* UTC day, so `captured_at < UTC-midnight(day)`
would fail to exclude the row from its own predecessor lookup and it would be
returned as its own predecessor.

**Chosen bound: `captured_date < @day`.** `captured_date` is already the poller-zone
calendar day, computed once on the write path by `dateOnly(capturedAt, loc)`, so the
predicate is zone-free at query time and exactly equivalent to today's
`captured_at < dayStart(cur.capturedAt, loc)` — both say "the row's local calendar
day is strictly before this row's local calendar day". The same-day-recapture guard
that `dayStart` existed to provide (tier `telemetry-add-derived-consumption-columns`
design D7 — a repeat nightly run must not select today's own about-to-be-replaced row
as its own predecessor) therefore **survives the move**, re-expressed in the schema's
own day column instead of a runtime zone computation.

**Return shape:** `(*Snapshot, error)`, nil meaning "no predecessor". Chosen over
`(Snapshot, bool, error)` (`RecentEfficiency`'s shape) because a pointer already
carries absence unambiguously for a struct with no meaningful zero value, and because
this is the shape the module's own `previousSnapshot` seam already uses — the method
is that seam promoted to the public port, not a new idea.

### D3 — Existing `vehicle_metrics` rows are rebuilt by resetting the watermarks (carries I3)

A migration under `internal/analytics/db/migrations/` deletes the
`vehicle_metric_watermarks` rows for the `vehicle_snapshots` source. `recalculate.go`'s
own `watermark` helper already treats an absent row as the epoch (tier 3 design D7), so
the next nightly `Reconcile` queries `SnapshotsByVehicleUpdatedSince(epoch)`, gets every
snapshot the vehicle has, derives the affected span as `[minDay-1, maxDay+1]` (tier 3
D8) and calls `Recalculate` once over the vehicle's whole history — rebuilding every
row through the new derivation. No new Go code, no new query.

**Why only the `vehicle_snapshots` source:** with that one cursor at epoch, the
affected-span union already covers the vehicle's entire history, so the other two
sources' rows are recomputed anyway on the same pass. Deleting all three would be
equally safe but would re-scan two sources for nothing — minimal scope wins.

**Rejected — a one-off `cmd/tmp-backfill-*` runner.** Tier 3 did that and swept a
15 MB binary into the repo. The backfill path already exists and is the same code that
runs nightly; a second entry point would be a second thing to keep correct.

**Rejected — leaving existing rows alone.** The table would be half one formula, half
the other, with no column recording which — undetectable from the data.

**Rejected — a SQL backfill inside the analytics migration** (recomputing
`vehicle_metrics` in `UPDATE ... LAG()` form, the way `20260814000001` backfilled
`vehicle_snapshots`). It would need to read `telemetry`'s tables from an `analytics`
migration — a cross-module write path's mirror image, and exactly the coupling
`ai/architecture.md` §2 forbids.

### D4 — Two migrations, one per module; no sub-task edits both (carries I4)

This change spans two modules by necessity. A `telemetry` migration must never write
`analytics`' table and vice versa, so the work needs two migration files in two
module-owned directories. tasks.md groups every sub-task so that no single one edits
both `internal/telemetry/` and `internal/analytics/`; the wave boundaries are the
handoff points.

### D5 — `deriveConsumption` moves byte-for-byte, returning a value struct

`internal/analytics/consumption.go`:

```go
// consumptionCalc carries the five derived consumption figures for one
// snapshot pair. All five are pointers with the identical NULL convention the
// vehicle_snapshots columns carried: nil means "not computable", never zero.
type consumptionCalc struct {
    DistanceTraveledKmCalc *float64
    BatteryUsedPctCalc     *int
    KmPerPctCalc           *float64
    EstimatedRangeKmCalc   *float64
    DaysSpannedCalc        *int
}

func deriveConsumption(prev *telemetry.Snapshot, cur telemetry.Snapshot) consumptionCalc
```

The body is `internal/telemetry/service.go`'s `deriveConsumption` with one mechanical
change: it populates and returns a `consumptionCalc` instead of mutating and returning
a `Snapshot` (which no longer has the fields). Everything that decides a *number* is
carried over unchanged — same subtraction order (`cur.OdometerKm - prev.OdometerKm`,
`prev.BatteryLevelPct - cur.BatteryLevelPct`), same whole-calendar-day count
(`int(cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24)`), same `batteryUsed > 0`
divisor guard, same `kmPerPct * 100`. `prev == nil` returns the zero
`consumptionCalc` (all five nil).

**Why a struct rather than five return values:** five same-typed pointer returns are
trivially swappable at the call site with no compiler complaint — the exact class of
error a characterization test can miss if its fixture happens to be symmetric. A named
struct makes each assignment self-checking.

### D6 — `prev == nil` becomes the single predecessor signal

`deriveVehicleMetrics` today branches on `i == 0 || cur.BatteryUsedPctCalc == nil` —
the second half being tier 3 D10's deliberate "defense-in-depth: trust telemetry's own
field, not just local array position". **That second check ceases to exist with the
field it reads.** After this change the sole signal is whether a predecessor snapshot
is available:

- for `i >= 1`: `snapshots[i-1]`, guaranteed to be the true immediate predecessor
  because `SnapshotsByVehicleBetween` returns a contiguous window with nothing
  skipped inside it;
- for `i == 0`: the `preceding` snapshot supplied by the caller (D7), which is `nil`
  exactly when the vehicle has no earlier snapshot at all.

This is not a weakening: the removed check and the retained one were expected to
co-occur (tier 3 D10 says so), and the retained one is now the *stronger* of the two —
`preceding` consults the database, where `cur.BatteryUsedPctCalc == nil` only ever
reported what a past write path concluded. The behaviour tier 3's Fixture C pins (a
predecessor-less day gets a row with every derived field NULL and `flagged` forced
`false`, never left to a zero-vs-distance comparison) is **unchanged and still
load-bearing** — see the Test Contract's Fixture C.

### D7 — `Recalculate` calls `SnapshotPrecedingDay` exactly once, for index 0

```go
snapshots, err := r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end.AddDate(0, 0, 1))
// ...
var preceding *telemetry.Snapshot
if len(snapshots) > 0 {
    preceding, err = r.telemetry.SnapshotPrecedingDay(ctx, accountID, teslaID, snapshots[0].CapturedDate)
    if err != nil { return fmt.Errorf("fetching preceding snapshot: %w", err) }
}
// ...
rows := deriveVehicleMetrics(preceding, snapshots, sessions, entries, start, end)
```

`deriveVehicleMetrics` gains a leading `preceding *telemetry.Snapshot` parameter and
uses it as `prev` when `i == 0`. The function stays pure and offline-testable — the
lookup is the caller's I/O, exactly as the three existing fetches are.

**One unconditional call, not a conditional one.** In the common case `snapshots[0]`
is the one-day lookback row, is filtered out of the output by the existing
`day.Before(start)` guard, and its own `preceding` is never used — so the call could
be skipped whenever `effectiveDay(snapshots[0]) < start`. It is not, for two reasons:
the saving is one indexed single-row read on a write path the Performance-Profile
explicitly gives latitude to, and the conditional version has a failure mode
(mis-deriving the guard) whose symptom is a silently-NULLed row rather than an error.
A branch that can only ever save a microsecond and can lose a day of data is not worth
having.

**Error handling:** a `SnapshotPrecedingDay` failure aborts `Recalculate` with a
wrapped error. It is **never** degraded to "no predecessor" — that would silently NULL
out a real vehicle's figures on a transient DB hiccup, which is precisely the
distinction telemetry's own `previousSnapshot` doc comment already draws and which
carries over with the function.

### D8 — Telemetry's now-dead plumbing is removed, not left behind

With `deriveConsumption` gone, `attemptVehicle`'s predecessor lookup has no purpose:

- `deriveConsumption` (`service.go`) — deleted; moved to analytics (D5).
- the `prev, err := s.store.previousSnapshot(...)` call in `attemptVehicle` — deleted,
  along with its error branch. `attemptVehicle` becomes fetch → map → store.
- the `previousSnapshot` method on the `store` interface, on `dbStore`, and on the
  three test fakes (`fakeStore`, `fakeReadStore`, `fakeHistoryStore`) — deleted.
- `dayStart` (`service.go`) — deleted; its only caller was that lookup. Its guarantee
  survives in `SnapshotPrecedingDay`'s `captured_date < @day` predicate (D2).
- `PreviousSnapshotForVehicle` (`db/query.sql`) — **replaced by**
  `SnapshotPrecedingDay`'s query, not deleted alongside it: same table, same index
  strategy, same `LIMIT 1`, new predicate column and new name.

Dead plumbing left behind is a standing invitation for a future agent to re-derive the
figures in the wrong module — the precise mistake this tier exists to undo. Removing
it is part of the change, not a follow-up.

### D8b — `Recalculate` widens its charge-source fetches across a gap (NOT covered by the interview)

**This decision was not covered by I1–I4 and is made here.** Reaching further back for
the *predecessor* without reaching further back for that span's *charge events*
produces a wrong number for exactly the gap days D2 exists to recover.

`deriveVehicleMetrics` corrects each day's raw battery delta by the charge events
matched inside the span:

```go
chargePct := sumSuperchargerPctBetween(sessions, prev.CapturedAt, cur.CapturedAt) +
             sumManualPctBetween(entries, effectiveDay(prev), day)
```

Today `Recalculate` fetches sessions over `[start-1d, end+2d]` and manual entries over
`[start-1d, end]`. When `preceding` sits seven days before `start`, a Supercharger
session or manual entry three days into that gap is **not fetched**, contributes 0, and
the day's `consumed_pct` comes out too low — possibly negative, which the existing
D5/D5a rule then reports as a suspected charge gap. A false "missing charge record"
alarm on a day that was correctly charged is worse than the silent drop it replaced.

So: when `preceding` exists and its effective day precedes the normal lookback start,
both charge-source fetches start from `effectiveDay(*preceding)` instead:

```go
chargeStart := lookbackStart                 // start.AddDate(0, 0, -1), unchanged default
if preceding != nil {
    if d := effectiveDay(*preceding); d.Before(chargeStart) {
        chargeStart = d
    }
}
sessions, err := r.supercharger.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, chargeStart, end.AddDate(0, 0, 2))
entries,  err := r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, chargeStart, end)
```

`effectiveDay(*preceding)` rather than `calendarDay(preceding.CapturedAt)` — one day
more generous than strictly required, matching tier 3 D8's "coarse and generous, not
pixel-exact" precedent. Over-fetching only costs rows read; it can never change a
result, because both `sum...Between` helpers re-filter to the exact interval in Go.

**Correction (leader, wave-2 reconcile).** An earlier draft of this paragraph claimed
that in the steady state `chargeStart == lookbackStart` and the fetches stay
byte-identical to today's. That is arithmetically false, and the code block above is
what governs. `effectiveDay(s)` is `calendarDay(s.CapturedDate).AddDate(0, 0, -1)`, so
the ordinary one-day lookback row at `start-1d` has an effective day of `start-2d`,
which **is** `Before(lookbackStart)`. `chargeStart` therefore drops to `start-2d` on
*every* call, gap or no gap — the charge fetches are permanently one day wider than
before this decision, not conditionally wider.

That is accepted, not a defect: it is the same "one day more generous than strictly
required" the paragraph above already chose deliberately, and it costs one extra day of
charge rows read per `Recalculate` while changing no result, because both
`sum...Between` helpers re-filter to the exact interval in Go. The *gap* widening on top
of it still fires only when a real gap exists and is still bounded by that gap's length.

Any test asserting this must expect `start-2d` in the no-gap case. Task 3.3 was written
against the withdrawn claim and is corrected in tasks.md.

**Rejected — leave the charge fetches narrow.** Trades a silently-dropped day for a
silently-wrong one, plus a false gap alarm. Strictly worse.

**Rejected — always fetch charge sources from the epoch.** Unbounded read on every
`Recalculate`, including the 99% of calls with no gap at all.

### D9 — What the down migrations can and cannot restore

**`vehicle_snapshots`' five columns: the Up is destructive.** `DROP COLUMN` discards
every stored value; nothing in Postgres brings the dropped bytes back.

The Down migration nevertheless does better than re-adding five empty columns, because
these five values are **pure functions of columns that survive** — `odometer_km`,
`battery_level_pct` and `captured_date` are untouched by this change. The Down
therefore re-adds the columns and re-runs `20260814000001`'s own `LAG()` backfill,
verbatim, restoring a value for every row except each vehicle's oldest (which
correctly has no predecessor).

What it **can** restore: every row's five figures, recomputed by the same formulas,
in the same subtraction order, with the same divisor guard.

What it **cannot** restore: the *literal stored bytes*, where those had diverged from
a fresh recomputation. That divergence is real and is Context 2's stale-successor bug:
a row whose predecessor was later replaced by a same-day re-capture carries `_calc`
values describing a reading that no longer exists in the table. Rolling back produces
the *correct* value there rather than the *previously stored* one. Stated plainly:
**a rollback is lossy with respect to a bug, not with respect to correct data.**

It also cannot restore the Go code — the Down migration restores schema and data only;
reverting `telemetry.Snapshot`'s fields, `deriveConsumption` and the analytics changes
is a code revert, not a migration.

**`vehicle_metric_watermarks`' deleted rows: irreversible, and harmless.** The Down is
a no-op (`SELECT 1;`). A deleted cursor row cannot be recovered — but an absent
watermark is defined as "epoch" (tier 3 D7), so the only consequence of rolling back
is that the next `Reconcile` backfills that vehicle's history once more. There is no
state to lose.

### D10 — The correctness gain is a spec change, and is named as one

Two user-visible outputs change, and both are fixes:

1. **A vehicle-day whose predecessor is older than the fetched window now appears.**
   Today `deriveVehicleMetrics` emits a derived-fields-NULL row for it and both Reader
   methods filter it out (tier 3 D13), so the charts silently omit the day. After this
   change the day carries the true multi-day delta and renders. Pinned by Fixture D.
2. **A day whose predecessor row was replaced by a same-day re-capture now shows a
   delta against the replacement**, because `Recalculate` derives from what the two
   rows say at recompute time rather than what a past write path concluded.

Neither can be characterization-pinned as "identical", because identical would mean
preserving the bug. They are recorded here, in the proposal's "Breaking" section, and
in `specs/analytics/spec.md` as explicit requirements — never as an incidental
difference a reviewer has to notice.

### D11 — Ordering: analytics computes before telemetry drops

There must be no window in which the five values are unreachable. The order is:

1. **telemetry adds** `SnapshotPrecedingDay` (additive; nothing breaks).
2. **analytics derives** the five figures itself (still reading a `Snapshot` that
   still carries the old fields — it simply stops copying them). At the end of this
   step the columns are redundant but present, and the system is fully working on
   either source.
3. **telemetry drops** the columns and the `Snapshot` fields, in the same change.
4. **analytics resets** the watermark so existing rows are rebuilt.

Steps 2 and 3 are in separate modules and separate waves, with `depends_on` making the
edge explicit. Step 3 before step 2 would leave analytics reading fields that no longer
compile; step 4 before step 2 would rebuild rows with the old formula.

## Database Changes (design gate — full schema, rationale, index plan)

> **This change trips the `database` design gate.** Both migrations below are
> fully specified; the owner must confirm this section before Apply.

### Migration 1 — `internal/telemetry/db/migrations/20260822000001_drop_derived_consumption_columns_vehicle_snapshots.sql`

```sql
-- +goose Up
-- internal/telemetry — drop the five derived-consumption columns from
-- vehicle_snapshots (MAG-26 RM29 tier 4, RM29-telemetry-drop-derived-columns).
-- They were added by 20260814000001 and computed at capture time by
-- deriveConsumption (service.go). As of this change the derivation is owned by
-- internal/analytics, which computes the same five figures from this table's
-- surviving raw columns (odometer_km, battery_level_pct, captured_date) and
-- persists them on its own vehicle_metrics table (tier 3). Nothing inside
-- telemetry ever read these columns back.
--
-- DESTRUCTIVE: every stored value is discarded. The Down migration below
-- recomputes them rather than restoring them — see the note there.
ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS days_spanned_calc,
    DROP COLUMN IF EXISTS estimated_range_km_calc,
    DROP COLUMN IF EXISTS km_per_pct_calc,
    DROP COLUMN IF EXISTS battery_used_pct_calc,
    DROP COLUMN IF EXISTS distance_traveled_km_calc;

-- +goose Down
-- Re-add the five columns and REPOPULATE them by recomputation. This is
-- possible only because all five are pure functions of columns this change
-- never touched (odometer_km, battery_level_pct, captured_date). The backfill
-- below is 20260814000001's own LAG() pass, verbatim — same subtraction order,
-- same ">0" divisor guard, same "x100" range formula, same whole-calendar-day
-- count via captured_date (DATE) subtraction.
--
-- WHAT THIS RESTORES: a correct value on every row except each vehicle's
-- oldest, which has no predecessor and correctly keeps NULL in all five.
-- WHAT THIS DOES NOT RESTORE: the literal bytes previously stored, where those
-- had diverged from a fresh recomputation. A row whose predecessor was later
-- REPLACED by a same-day re-capture (the dedupe UPSERT, 20260805000001) carried
-- values derived against a reading that no longer exists in the table; this
-- backfill produces the correct figure there, not the stale one. The rollback
-- is lossy with respect to that bug, not with respect to correct data.
ALTER TABLE vehicle_snapshots
    ADD COLUMN distance_traveled_km_calc DOUBLE PRECISION,
    ADD COLUMN battery_used_pct_calc     INTEGER,
    ADD COLUMN km_per_pct_calc           DOUBLE PRECISION,
    ADD COLUMN estimated_range_km_calc   DOUBLE PRECISION,
    ADD COLUMN days_spanned_calc         INTEGER;

WITH prev AS (
    SELECT
        id,
        odometer_km,
        battery_level_pct,
        captured_date,
        LAG(odometer_km)        OVER w AS prev_odometer_km,
        LAG(battery_level_pct)  OVER w AS prev_battery_level_pct,
        LAG(captured_date)      OVER w AS prev_captured_date
    FROM vehicle_snapshots
    WINDOW w AS (PARTITION BY account_id, tesla_id ORDER BY captured_at)
)
UPDATE vehicle_snapshots v
SET
    distance_traveled_km_calc = prev.odometer_km - prev.prev_odometer_km,
    battery_used_pct_calc     = prev.prev_battery_level_pct - prev.battery_level_pct,
    days_spanned_calc         = prev.captured_date - prev.prev_captured_date,
    km_per_pct_calc = CASE
        WHEN (prev.prev_battery_level_pct - prev.battery_level_pct) > 0
        THEN (prev.odometer_km - prev.prev_odometer_km) / (prev.prev_battery_level_pct - prev.battery_level_pct)
        ELSE NULL
    END,
    estimated_range_km_calc = CASE
        WHEN (prev.prev_battery_level_pct - prev.battery_level_pct) > 0
        THEN ((prev.odometer_km - prev.prev_odometer_km) / (prev.prev_battery_level_pct - prev.battery_level_pct)) * 100
        ELSE NULL
    END
FROM prev
WHERE v.id = prev.id
  AND prev.prev_odometer_km IS NOT NULL;
```

**No constraint, index, default or CHECK is dropped by this migration** — none of the
five columns carried one. Verified against every `vehicle_snapshots` DDL statement in
`internal/telemetry/db/migrations/`: the table has exactly three index-backed objects
(the `id` primary key, `idx_vehicle_snapshots_vehicle_time`, and
`vehicle_snapshots_account_tesla_date_unique`), and none of them names a `_calc`
column.

### Migration 2 — `internal/analytics/db/migrations/20260822000002_reset_vehicle_metric_watermarks.sql`

```sql
-- +goose Up
-- internal/analytics — reset the telemetry-snapshot recompute cursor so every
-- vehicle's existing vehicle_metrics rows are rebuilt through the derivation
-- this change moves into this module (MAG-26 RM29 tier 4).
--
-- Recalculator.Reconcile (recalculate.go) treats an ABSENT watermark row as the
-- epoch (tier 3 design D7), so deleting these rows makes the next nightly run
-- query SnapshotsByVehicleUpdatedSince(epoch), see every snapshot the vehicle
-- has, and Recalculate the vehicle's whole history in one pass. No Go code and
-- no one-off binary is involved; the backfill IS the nightly path.
--
-- Only the 'vehicle_snapshots' source is reset. With that cursor at epoch the
-- affected-day span already covers the vehicle's entire history, so the other
-- two sources' contributions are recomputed on the same pass — resetting them
-- too would re-scan two sources for no additional coverage.
--
-- Data-only migration: no schema object is created, altered or dropped.
DELETE FROM vehicle_metric_watermarks
WHERE source = 'vehicle_snapshots';

-- +goose Down
-- Irreversible, and harmless. A deleted cursor row cannot be recovered, but an
-- absent watermark is DEFINED as "epoch" (tier 3 design D7) — the only
-- consequence of a rollback is that the next Reconcile backfills that vehicle's
-- history once more. There is no state to lose and nothing to undo.
SELECT 1;
```

### New query — `internal/telemetry/db/query.sql`

Replaces `PreviousSnapshotForVehicle` (D8). Same table, same index strategy, same
`LIMIT 1`; the bound moves from an instant to a calendar day (D2), and the projection
loses the five dropped columns like every other snapshot query in the file.

```sql
-- name: SnapshotPrecedingDay :one
-- Return the single most recent snapshot for a vehicle whose captured_date is
-- strictly before the given calendar day, or pgx.ErrNoRows when none exists
-- (the vehicle's first-ever snapshot). Backs telemetry.Reader.SnapshotPrecedingDay,
-- whose only consumer is internal/analytics' Recalculate: it needs the EXACT
-- predecessor, however old, because a capture gap longer than its fetch window
-- would otherwise yield a silently wrong (or silently absent) daily delta.
--
-- The bound is captured_date, NOT captured_at: captured_date is already the
-- poller-zone calendar day (stamped once on the write path by dateOnly), so the
-- predicate is zone-free at query time. It is exactly equivalent to the
-- captured_at < dayStart(cur.captured_at, loc) bound this query's predecessor
-- (PreviousSnapshotForVehicle) used, and it preserves that bound's purpose: a
-- same-day re-capture cannot select today's own about-to-be-replaced row as its
-- own predecessor, because that row's captured_date equals @day.
--
-- Index reuse (no new index): the planner seeks the existing
-- idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at) on its
-- two leading equality columns and walks the ascending B-tree BACKWARD to
-- satisfy ORDER BY captured_at DESC, stopping at the first row that also passes
-- the captured_date residual predicate. Because
-- vehicle_snapshots_account_tesla_date_unique allows at most ONE row per
-- (account_id, tesla_id, captured_date), and captured_date is monotone
-- non-decreasing with captured_at for a vehicle, AT MOST ONE row is skipped
-- before the first match. Verified via EXPLAIN in the DB-integration test.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id   = @account_id
  AND tesla_id     = @tesla_id
  AND captured_date < @day
ORDER BY captured_at DESC
LIMIT 1;
```

### Index Plan

**No index is added, and no index is dropped.**

| # | Read pattern | Served by | Change |
|---|---|---|---|
| 1 | **NEW** — `SnapshotPrecedingDay`: `WHERE account_id=$1 AND tesla_id=$2 AND captured_date < $3 ORDER BY captured_at DESC LIMIT 1` | `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)`, **backward** scan off the two leading equality columns; `captured_date` is a residual predicate skipping at most one row (see below) | New pattern, existing index |
| 2 | `LatestSnapshotsByAccount` (`DISTINCT ON (tesla_id) … ORDER BY tesla_id, captured_at DESC`) | Same index | Unchanged predicate; five fewer projected columns |
| 3 | `SnapshotsByVehicleSince` / `SnapshotsByVehicleBetween` (forward range scans, `LIMIT 400`) | Same index | Unchanged predicate; five fewer projected columns |
| 4 | `SnapshotsByVehicleUpdatedSince` (`updated_at` residual within the `(account_id, tesla_id)` prefix) | Same index | Unchanged |
| 5 | `InsertVehicleSnapshot`'s `ON CONFLICT (account_id, tesla_id, captured_date)` | `vehicle_snapshots_account_tesla_date_unique` | Unchanged target; five fewer bound parameters and five fewer `DO UPDATE SET` assignments |
| 6 | Gateway history charts (`vehicle_metrics` range scan, ≤ 90 rows) | `vehicle_metrics_account_tesla_date_unique` (tier 3 Index Plan) | **Completely unchanged** — no hot read path moves in this tier |
| 7 | `Reconcile`'s watermark lookup / the migration's one-time `DELETE … WHERE source = …` | `vehicle_metric_watermarks_account_tesla_source_unique`; the DELETE is a one-time sequential scan of a table holding one row per (vehicle × source) | Unchanged / negligible |

**Why the residual predicate in pattern 1 costs nothing.** The planner cannot use
`captured_date` as an index bound — it is not in the index. It does not need to: the
scan is already pruned to one vehicle's rows by the `(account_id, tesla_id)` prefix,
walks them newest-first, and stops at the first row satisfying `captured_date < @day`.
`vehicle_snapshots_account_tesla_date_unique` guarantees at most one row per vehicle
per calendar day, and `captured_date` is monotone non-decreasing with `captured_at`
within a vehicle (both derive from the same capture instant), so **at most one row is
examined and rejected** before the match. The expected plan is
`Limit → Index Scan Backward using idx_vehicle_snapshots_vehicle_time`, with
`Rows Removed by Filter` ≤ 1. This is asserted by `EXPLAIN` in the DB-integration
test, exactly as tier 3 did for `SnapshotsByVehicleUpdatedSince`.

**Deliberately not added: an index on `(account_id, tesla_id, captured_date)`.** It
would make the residual predicate an index bound — saving the examination of at most
one row, on a write-path query called once per `Recalculate`. Against that, it would
be a fourth index to maintain on the platform's highest-volume table, slowing every
nightly insert and every same-day UPSERT. The read-heavy Performance-Profile licenses
aggressive indexing *for declared read patterns*; this one is already served. Reopen
only if `EXPLAIN` in the integration test shows something other than the plan above.

**Deliberately not added: any index for the watermark DELETE.** It runs once, over a
table with one row per vehicle per source.

**Storage note (not a decision, an observation):** `DROP COLUMN` in Postgres is a
catalog-only operation — it does not reclaim the dropped values' heap space until the
rows are rewritten. No `VACUUM FULL` is prescribed here; the table is append-mostly and
the space is reused by subsequent writes. Recorded so nobody adds a rewrite step
believing it is required.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

Five fixtures with their exact expected values, fixed **before** any implementation
exists. Tests written later must assert THESE values, not whatever the implementation
happens to produce. Fixtures A, B and C are carried over from tier 3's Test Contract
unchanged — their expected rows must stay byte-for-byte identical after the derivation
moves, which is the whole characterization bar (roadmap D10). Fixtures D and E are new
and are the two cases this change's new machinery exists for.

All fixtures use `(accountID = A, teslaID = 42)`. `CapturedDate` values are
UTC-midnight-normalized bare dates, matching `dateOnly`/`rowToSnapshot`.
`effectiveDay(s) = CapturedDate(s) − 1 day`.

### Fixture A — plain day, no charge events (carried from tier 3, must not change)

| | `CapturedDate` | `CapturedAt` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|---|
| predecessor | 2026-08-10 | 2026-08-10T03:30:00Z | 1000.0 | 80 | 300.0 |
| current | 2026-08-11 | 2026-08-11T03:30:00Z | 1050.0 | 65 | 280.0 |

No Supercharger sessions, no manual entries. `metric_date = 2026-08-10`.

**Expected `deriveConsumption(prev, cur)`:**

| Field | Value |
|---|---|
| `DistanceTraveledKmCalc` | `50.0` (`1050.0 − 1000.0`) |
| `BatteryUsedPctCalc` | `15` (`80 − 65`) |
| `KmPerPctCalc` | `3.3333…` (`50.0 / 15`) |
| `EstimatedRangeKmCalc` | `333.333…` (`3.3333… × 100`) |
| `DaysSpannedCalc` | `1` |

**Expected `vehicle_metrics` row** (`Recalculate(A, 42, 2026-08-10, 2026-08-10)`):
`metric_date 2026-08-10`, `battery_level_pct 65`, `odometer_km 1050.0`,
`battery_range_km 280.0`, the five values above, `consumed_pct 15.0`,
`flagged false`, `missing_charging_type NULL`.

**Expected `OdometerDeltaByDay`:** `[{Date: 2026-08-10, KmDriven: 50.0, OdometerKm: 1050.0}]`.
**Expected `ConsumedByDay`:** `[{Date: 2026-08-10, ConsumedPct: 15.0, DistanceKm: 50.0, Flagged: false, MissingChargingType: "", DaysSpanned: 1}]`.

### Fixture B — negative delta, divisor guard, flagged day (carried from tier 3, must not change)

| | `CapturedDate` | `CapturedAt` | `OdometerKm` | `BatteryLevelPct` |
|---|---|---|---|---|
| predecessor | 2026-08-12 | 2026-08-12T03:30:00Z | 2000.0 | 40 |
| current | 2026-08-13 | 2026-08-13T03:30:00Z | 1998.0 | 85 |

(Odometer decreased 2 km — a clock-skew/read anomaly; battery rose 45 points with no
charge event logged in either source.) `metric_date = 2026-08-12`.

**Expected `deriveConsumption`:** `DistanceTraveledKmCalc -2.0` (raw, unclamped),
`BatteryUsedPctCalc -45`, `KmPerPctCalc nil` (**divisor −45 ≤ 0**),
`EstimatedRangeKmCalc nil` (same guard), `DaysSpannedCalc 1`.

**Expected `vehicle_metrics` row:** the above, plus `consumed_pct -45.0`,
`flagged true` (`consumed_pct < 0`), `missing_charging_type 'MANUAL'`.

**Expected `OdometerDeltaByDay`:** `KmDriven 0.0` (clamped on read),
`OdometerKm 1998.0`.
**Expected `ConsumedByDay`:** `ConsumedPct -45.0`, `Flagged true`,
`MissingChargingType telemetry.MissingChargingTypeManual`, `DistanceKm -2.0`
(**unclamped**), `DaysSpanned 1`.

### Fixture C — a vehicle's true first-ever snapshot, no predecessor at all

One snapshot, with nothing earlier for this vehicle anywhere in `vehicle_snapshots`:

| | `CapturedDate` | `CapturedAt` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|---|
| current (first-ever) | 2026-08-05 | 2026-08-05T03:30:00Z | 500.0 | 90 | 320.0 |

`Recalculate(A, 42, 2026-08-04, 2026-08-04)` fetches
`SnapshotsByVehicleBetween(2026-08-04, 2026-08-05)` → exactly this one row, so it is
`snapshots[0]`; `SnapshotPrecedingDay(A, 42, 2026-08-05)` returns **`(nil, nil)`**.
`metric_date = 2026-08-04`.

**Expected `deriveConsumption(nil, cur)`:** all five fields `nil`.

**Expected `vehicle_metrics` row** — a row IS written (dense table, tier 3 D9):
`metric_date 2026-08-04`, `battery_level_pct 90`, `odometer_km 500.0`,
`battery_range_km 320.0`; `distance_traveled_km_calc`, `battery_used_pct_calc`,
`km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc`, `consumed_pct` all
**NULL**; `flagged` **`false`** (asserted as the actual boolean value, not "falsy");
`missing_charging_type` **NULL**.

**Expected `OdometerDeltaByDay(A, 42, 2026-08-04, 2026-08-04)`:** `[]DayDistance{}`
(empty — filtered by `distance_traveled_km_calc IS NOT NULL`, tier 3 D13).
**Expected `ConsumedByDay(A, 42, 2026-08-04, 2026-08-04)`:** `[]DayConsumption{}`
(empty — filtered by `battery_used_pct_calc IS NOT NULL`).

**Direct-read confirmation:** `SELECT * FROM vehicle_metrics WHERE account_id = A AND
tesla_id = 42 AND metric_date = '2026-08-04'` finds the row, with `battery_level_pct
= 90`, `odometer_km = 500.0`, `battery_range_km = 320.0`.

### Fixture D — a multi-day capture gap (**the case `SnapshotPrecedingDay` exists for**)

| | `CapturedDate` | `CapturedAt` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|---|
| predecessor | 2026-08-01 | 2026-08-01T03:30:00Z | 1000.0 | 90 | 350.0 |
| current | 2026-08-08 | 2026-08-08T03:30:00Z | 1210.0 | 55 | 220.0 |

The poller missed six nights. No Supercharger sessions, no manual entries anywhere in
the span. `effectiveDay(current) = 2026-08-07` → `metric_date`.

`Recalculate(A, 42, 2026-08-07, 2026-08-07)` fetches
`SnapshotsByVehicleBetween(2026-08-06, 2026-08-08)`, which returns **only the current
row** — the predecessor's effective day (2026-07-31) is seven days outside the window.
`SnapshotPrecedingDay(A, 42, 2026-08-08)` returns the 2026-08-01 row.
Per D8b, the charge-source fetches widen to start at `effectiveDay(preceding)` =
2026-07-31.

**Expected `deriveConsumption(prev, cur)`:**

| Field | Value | Why it is the assertion that matters |
|---|---|---|
| `DistanceTraveledKmCalc` | `210.0` (`1210.0 − 1000.0`) | the true total across the gap, never averaged per day |
| `BatteryUsedPctCalc` | `35` (`90 − 55`) | |
| `DaysSpannedCalc` | **`7`** | **not `1`** — an implementation that assumed one day, or that took the window's width, fails here |
| `KmPerPctCalc` | `6.0` (`210.0 / 35`) | |
| `EstimatedRangeKmCalc` | `600.0` | |

**Expected `vehicle_metrics` row:** the above, plus `metric_date 2026-08-07`,
`battery_level_pct 55`, `odometer_km 1210.0`, `battery_range_km 220.0`,
`consumed_pct 35.0`, `flagged false`, `missing_charging_type NULL`.

**Expected `OdometerDeltaByDay(A, 42, 2026-08-07, 2026-08-07)`:**
`[{Date: 2026-08-07, KmDriven: 210.0, OdometerKm: 1210.0}]`.
**Expected `ConsumedByDay(A, 42, 2026-08-07, 2026-08-07)`:**
`[{Date: 2026-08-07, ConsumedPct: 35.0, DistanceKm: 210.0, Flagged: false, MissingChargingType: "", DaysSpanned: 7}]`.

**The negative assertion this fixture must also carry:** the results above must be
**non-empty**. An implementation that omits the `SnapshotPrecedingDay` call falls into
the `i == 0` branch, writes a row with all five values NULL, and both Reader methods
return an empty slice — a silently dropped day that compiles, does not error, and
renders as a missing bar nobody investigates. Assert the values, and assert the slices
are length 1.

**Fixture D2 — the same gap, with a charge event inside it (D8b's proof).** Same two
snapshots, plus one manual charge entry on `ChargedOn = 2026-08-04` with a battery
delta of `+20`. The entry's day lies four days before the un-widened fetch start
(2026-08-06), so it is fetched **only** because D8b widened the window.

Expected: `BatteryUsedPctCalc` still `35` (the raw delta is unaffected by charging),
but `consumed_pct = 35 + 20 = 55.0`, `flagged false`, `DistanceKm 210.0`,
`DaysSpanned 7`. An implementation that widened the predecessor lookup but not the
charge fetches produces `consumed_pct 35.0` here — a plausible-looking number that is
wrong by exactly the charge it failed to see.

### Fixture E — the `battery_used_pct_calc == 0` divisor guard (parked day)

| | `CapturedDate` | `CapturedAt` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|---|
| predecessor | 2026-08-15 | 2026-08-15T03:30:00Z | 3000.0 | 70 | 280.0 |
| current | 2026-08-16 | 2026-08-16T03:30:00Z | 3000.0 | 70 | 280.0 |

`metric_date = 2026-08-15`.

**Expected `deriveConsumption`:** `DistanceTraveledKmCalc 0.0` (stored, non-nil — a
truthful zero), `BatteryUsedPctCalc 0` (stored, non-nil), `DaysSpannedCalc 1`,
`KmPerPctCalc **nil**`, `EstimatedRangeKmCalc **nil**` — the guard is `batteryUsed > 0`,
so **zero is excluded exactly like a negative**. This is the boundary Fixture B's `-45`
does not test.

**Expected `vehicle_metrics` row:** the above, plus `consumed_pct 0.0`,
`flagged **false**` (`consumed == 0` but `distanceKm 0.0` is not `> minFlagDistanceKm`
= 10.0), `missing_charging_type NULL`.

**Expected `ConsumedByDay`:** one entry, `ConsumedPct 0.0`, `DistanceKm 0.0`,
`Flagged false`, `DaysSpanned 1` — the day is **not** filtered out, because
`battery_used_pct_calc` is `0`, not NULL. Asserting this is what distinguishes a
correct implementation from one that conflated "zero" with "absent".

### Characterization parity contract (roadmap D10)

`internal/telemetry/consumption_test.go`'s `TestDeriveConsumption` and
`TestDeriveConsumption_NilPrevReturnsCurUnchanged` move to
`internal/analytics/consumption_test.go`. **Every existing case must be ported with
its expected values unchanged** — the assertions move, they are not re-derived. The
`assertFloatPtr`/`assertIntPtr` helpers and the `1e-9` float tolerance move with them.
`TestDayStart_ComfortablyInsideLocalDay` / `TestDayStart_LocalDayBehindUTCDay` test
`dayStart`, which this change deletes (D8); they are removed, and the guarantee they
protected is re-asserted by the DB-integration test that a same-day re-capture's row
does not select itself as its own predecessor (below).

`internal/telemetry/db_derived_consumption_integration_test.go`'s three tests are
re-homed as follows:

| Existing test | Fate |
|---|---|
| `TestStore_PreviousSnapshot_RoundTrips` | **Stays in `internal/telemetry`**, rewritten against `Reader.SnapshotPrecedingDay` (the public port) instead of the deleted `previousSnapshot` seam. Same three cases: a predecessor exists; a single-row vehicle; a vehicle with no snapshots returns nil, nil. |
| `TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns` | **Split.** The same-day-recapture *predecessor-selection* half stays in telemetry, asserting `SnapshotPrecedingDay(day N)` returns the day N−1 row and never the replaced day-N row (this is `dayStart`'s guarantee, re-expressed — D2). The *derived-columns-refreshed* half moves to `internal/analytics` as a `Recalculate`-after-recapture test. |
| `TestStore_DerivedConsumptionColumns_RoundTrip` | **Deleted** — it round-trips columns that no longer exist. Its coverage is replaced by the analytics DB-integration tests asserting the same five figures on `vehicle_metrics`. |

### `EXPLAIN` assertion (Index Plan verification)

The telemetry DB-integration test runs
`EXPLAIN (FORMAT TEXT) SELECT … <SnapshotPrecedingDay's SQL>` against a seeded vehicle
and asserts the plan text contains `Index Scan Backward` and
`idx_vehicle_snapshots_vehicle_time`, and does **not** contain `Seq Scan`. Mirrors
tier 3's identical assertion for `SnapshotsByVehicleUpdatedSince`.

## Risks / Trade-offs

- **Irreversible data at the column level.** `DROP COLUMN` discards the stored values;
  the Down migration recomputes rather than restores (D9). The recomputation is exact
  for every correctly-stored row and *corrects* the stale ones — but a rollback does
  not reproduce the prior bytes, and that is stated in the migration itself, not only
  here.
- **The `preceding` lookup is a new failure point on the write path.** Mitigated by
  never degrading its error to "no predecessor" (D7); a DB failure aborts
  `Recalculate` and the next `Reconcile` retries, since the watermark only advances on
  success.
- **D8b's widened fetch is unbounded by the gap's length.** A vehicle that stopped
  reporting for a year and resumed would, on the run that recovers it, fetch a year of
  Supercharger sessions and manual entries. Accepted: it happens once per gap, on a
  write path, and the alternative (a fixed cap) reintroduces exactly the
  silently-wrong-number failure mode D2 rejects.
- **A previously-hidden day starts rendering.** Fixture D's day appears in the charts
  where it did not before. This is the fix, not a regression (D10) — but it does mean
  a user may see a new bar after this deploys, with a large `KmDriven` covering the
  whole gap. That is the truthful figure; `DaysSpanned` on the consumed chart already
  carries the "this entry spans N days" signal the existing spec requires.
- **PRE-EXISTING, NOT FIXED — `SnapshotsByVehicleBetween`'s `LIMIT 400`.** Found while
  writing this design. `Reconcile`'s epoch backfill can call `Recalculate` over a
  window wider than 400 days; the query is `ORDER BY captured_at ASC LIMIT 400`, so it
  silently truncates the **newest** rows of that window, and `Reconcile` then advances
  the watermark past days it never recomputed. This is a tier-3 behaviour, not
  introduced here, and this change makes it *less* harmful (the `SnapshotPrecedingDay`
  lookup means the first fetched row is no longer predecessor-less). At today's volume
  — one snapshot per vehicle per day — a vehicle needs 400+ days of history for it to
  fire. **Reported to the leader; deliberately out of scope** under the owner's
  standing minimal-scope preference. If the owner wants it in, it is a one-line change
  to that query plus a task.
- **`internal/analytics` now depends on one more `telemetry.Reader` method.** Additive;
  the fakes in `internal/analytics/reader_test.go` and
  `internal/telemetry/reader_test.go` gain a method each. A missing implementation is a
  compile error, so nothing can silently fail.

## Verification signals

Per the Test-Execution-Policy: the assistant runs and reports `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, the standalone
guards (`make ui-guard` / `make i18n-guard` / `make money-guard` — all three expected
to be no-ops here: no gateway markup, no user-facing string and no monetary column is
touched), and `make sqlc` after **each** migration lands. `openspec validate --changes
--strict` is run and reported.

The owner alone runs the suite:

```
go test ./internal/telemetry/... ./internal/analytics/... ./internal/gateway/... ./cmd/...
```

or the full suite:

```
go test ./...
```

Until the owner runs one of these and reports the result, this tier's implementation
status is **awaiting-user-verification**, never "done".
