## Context

`internal/telemetry` — `vehicle_snapshots` stores one row per `(account_id, tesla_id,
captured_date)` (superseded to upsert semantics by `telemetry-dedupe-daily-snapshots`,
migration `20260805000001`) with typed columns already in DISPLAY units
(`telemetry-store-display-units`, migration `20260806000001`): `odometer_km DOUBLE
PRECISION NOT NULL`, `battery_level_pct INTEGER NOT NULL`.

MAG-10 (source ticket) asks for three derived metrics computed from consecutive nightly
records: distance traveled, battery consumed, and km-per-percent-battery (from which an
estimated full-charge range is derived). This design implements the five binding decisions
settled with the user in the leader's grill-me interview (DU1–DU5, verbatim from the
dispatch) plus the leader's six schema/read/write constraints (L1–L6), and the
implementation-seam decisions needed to realize them.

Primary and only module: **`internal/telemetry/`**. No other `internal/` module is
touched. `openspec/changes/gateway-battery-consumed-chart/` is explicitly out of scope
(DU5) — not read, not referenced, not touched by this change or this design.

## Goals / Non-Goals

**Goals:**

- Five new nullable columns on `vehicle_snapshots`, all suffixed `_calc`: `distance_traveled_km_calc`,
  `battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc`.
- Computed in Go, at write time, from the incoming snapshot and its predecessor — never
  derived on read (L3).
- Correct across a multi-day gap: the raw multi-day delta is stored, tagged with the
  number of calendar days it spans (DU1) — never silently averaged or dropped.
- Correct when the divisor is zero or negative (charging day, parked day): the two ratio
  columns go NULL, never a stored negative or a division-by-zero (DU2).
- Correct under the table's upsert semantics: a same-day re-capture recomputes and
  refreshes all five columns via `ON CONFLICT ... DO UPDATE`, never leaving stale values
  (L1).
- Correct for the first-ever snapshot of a vehicle: no predecessor exists, all five columns
  are NULL — expected, not an error (L5).
- Existing history is backfilled in the same Up migration via a one-time `LAG()` window
  pass (DU4), producing results identical to what the Go write path would have computed
  for the same consecutive rows.

**Non-Goals:**

- Any gateway/dashboard change. `gateway-battery-consumed-chart` is a separate,
  user-managed change and is explicitly out of scope (DU5).
- Recomputing a snapshot's *successor* when a late-arriving row is inserted after it
  (L6) — named as a known, accepted limitation, not solved here.
- A new database index. The existing `idx_vehicle_snapshots_vehicle_time (account_id,
  tesla_id, captured_at)` is verified to serve the one new query this change adds (D7).

---

## Design Decisions

### D1 (DU1) — Multi-day gaps: store the raw delta, plus a `days_spanned_calc` column

`distance_traveled_km_calc` and `battery_used_pct_calc` always store the true delta across
whatever gap actually exists between the incoming snapshot and its predecessor — a 2-day
gap with 80 km driven stores `distance_traveled_km_calc = 80`, not 40/day. `days_spanned_calc`
records how many calendar days that delta spans (the normal case is `1`), so no consumer
can misread a multi-day figure as a single day's figure without deliberately ignoring an
available column.

**Rejected alternatives:**
- **Store the raw delta with no gap column.** A 2-day bar becomes silently
  indistinguishable from a 1-day bar — the exact ambiguity `days_spanned_calc` exists to
  resolve. Rejected.
- **Normalize to per-day on write** (divide by the gap before storing). Destroys the true
  total irrecoverably — a future consumer that legitimately wants the total (e.g. "km since
  last capture") cannot reconstruct it from an averaged figure. Rejected.
- **Compute only for consecutive days** (gap == 1), leaving a hole otherwise. Creates gaps
  in history the poller cannot control (a missed night is not the vehicle's fault), and
  throws away real, calculable data. Rejected.

This is why the change adds **five** columns, not the four the ticket's arithmetic alone
implies.

### D2 (DU2) — Ratio columns go NULL on a zero or negative divisor

`distance_traveled_km_calc` and `battery_used_pct_calc` are always stored raw, negatives
included, exactly as the ticket specifies (the ticket: "It can be negative. No worries
about that."). `km_per_pct_calc` and `estimated_range_km_calc` are computed **only when
`battery_used_pct_calc > 0`**; otherwise both are NULL.

- **Divisor == 0** (battery level unchanged overnight — e.g. the vehicle was parked and
  plugged in exactly enough to offset idle drain, or genuinely didn't move): a zero divisor
  is a division by zero. NULL is the only truthful result.
- **Divisor < 0** (the car charged overnight, net battery INCREASED): `km_per_pct_calc`
  would be negative — "kilometres per percent of battery consumed" is undefined when no
  battery was net consumed. A negative "estimated range" is meaningless and would corrupt
  any average or chart that reads it.

This follows the module's established D12/DSA3 convention (`internal/telemetry/AGENTS.md`
§DTO/units conventions): NULL means genuinely undefined, and a stored value must always be
a truthful reading — never a stand-in placeholder.

**Rejected alternatives:**
- **Store the raw signed ratio.** Pushes a meaningless negative km-per-percent onto every
  consumer forever, with no signal that it should be excluded from an average. Rejected.
- **Clamp to 0.** A stored `0` would be a lie (it is not "0 km per percent," it is
  "undefined") and would silently drag down any consumer that averages the column,
  exactly the failure mode the D12/DSA3 convention exists to prevent. Rejected.

### D3 (DU3) — Column naming: `<what>_<unit>_calc`, exact schema

The unit suffix stays immediately before `_calc` (CLAUDE.md's non-negotiable unit-suffix
rule is not relaxed by the `_calc` requirement — both apply, in that order), and every
column ends in `_calc` as the ticket asked.

| Column | Type | Nullable | Meaning |
|---|---|---|---|
| `distance_traveled_km_calc` | `DOUBLE PRECISION` | yes | `odometer_km` − previous row's `odometer_km` |
| `battery_used_pct_calc` | `INTEGER` | yes | previous row's `battery_level_pct` − `battery_level_pct` (may be negative) |
| `km_per_pct_calc` | `DOUBLE PRECISION` | yes | `distance_traveled_km_calc` / `battery_used_pct_calc`, only when the divisor > 0 |
| `estimated_range_km_calc` | `DOUBLE PRECISION` | yes | `km_per_pct_calc` × 100, only when the divisor > 0 |
| `days_spanned_calc` | `INTEGER` | yes | calendar days between the previous row and this one (`1` in the normal case) |

Type rationale: `odometer_km` is `DOUBLE PRECISION NOT NULL`, so a difference of two such
values is naturally `DOUBLE PRECISION` — matching the source column's type keeps no
precision lost and no cast needed on either side. `battery_level_pct` is `INTEGER NOT
NULL`, so its difference is naturally `INTEGER` — Tesla reports battery level as a whole
percent, and a whole-percent difference stays a whole integer; there is no fractional
percent anywhere in the source data to justify a wider type. `km_per_pct_calc` and
`estimated_range_km_calc` are ratios/products involving the `DOUBLE PRECISION` distance
value, so they are `DOUBLE PRECISION` too. `days_spanned_calc` is a whole calendar-day
count (`DATE − DATE` in Postgres, or an integer day-count in Go) — `INTEGER`, matching
`battery_used_pct_calc`'s precedent for a small whole-number derived column.

The ticket's `estimated_range_calculated` is renamed to `estimated_range_km_calc` — the
redundant "calculated" is dropped in favor of the project's mandatory unit suffix plus the
`_calc` marker, which together already say everything "calculated" was saying.

No new column is added as `NOT NULL` and none has a `DEFAULT` other than the implicit
`NULL` every newly `ADD COLUMN`ed nullable column gets — every one of the five is
genuinely undefined for a first-ever snapshot (D8/L5) and for pre-migration rows the
backfill cannot reach (there are none — the backfill covers every existing row, D4).

### D4 (DU4) — Backfill existing rows inside the Up migration, via `LAG()`

The Up migration adds the five nullable columns, then fills every existing row in **one**
pass using a `LAG()` window function partitioned by `(account_id, tesla_id)`, ordered by
`captured_at`. This is framed exactly as `20260805000001_dedupe_vehicle_snapshots_daily.sql`
framed its own backfill: a **ONE-TIME, point-in-time conversion**, not an ongoing schema
dependency — it runs once, against rows that already exist, and is retired the instant the
migration completes. The oldest row per vehicle has no predecessor (`LAG()` returns NULL
for the partition's first row) and correctly stays NULL in all five columns — this is the
same "first-ever snapshot" case D8/L5 gives the write path, arrived at for free by the
window function's own semantics, not a special case in the migration SQL.

The backfill SQL is written to produce results **byte-for-byte identical** to what the Go
write path (`deriveConsumption`, D8) computes for the same pair of consecutive rows — same
subtraction order, same `> 0` divisor guard, same `× 100` range formula, same day-count
definition (`captured_date` difference, an exact integer under Postgres `DATE − DATE`,
matching the Go side's `CapturedDate` difference in whole days — see D8). This SQL/Go
duplication is called out here explicitly as a **known, accepted, bounded cost**: it exists
in exactly one place (the migration file), it runs exactly once per deployment, and any
future change to the formula does **not** need to touch this migration (a formula change is
a new migration with a new backfill of its own, following the same one-time-conversion
precedent — it never edits an already-applied migration). Consistency between the two is
maintained by keeping both expressions textually adjacent to their design rationale (this
document) rather than by a shared code path, since Go and SQL cannot share one.

**Rejected alternatives:**
- **A separate one-off `cmd/` runnable.** Avoids the SQL/Go duplication (the runnable could
  call the real `deriveConsumption` function against historical rows), but adds a new
  binary, a `README.md`, and a **manual step** an operator must remember to run after
  `goose up` — the migration alone would leave five NULL columns on every existing row
  until someone remembers. A migration is atomic with the schema change and requires no
  extra operational step. Rejected.
- **Going-forward-only** (no backfill; only rows captured after this migration get the five
  columns populated). Leaves weeks of existing history blank for a metric the user
  explicitly asked "is it possible to apply the formulas to existing records?" about.
  Rejected — the ticket answers its own question "yes."

### D5 (DU5) — `gateway-battery-consumed-chart` is out of scope

Noted for completeness, not a schema decision: this change does not read, reference, or
modify `openspec/changes/gateway-battery-consumed-chart/` in any way. The user is managing
that change themselves. This design and its change are telemetry-only.

### D6 (L1) — `ON CONFLICT ... DO UPDATE` refreshes all five derived columns

`vehicle_snapshots` is upsert-per-day (`20260805000001`): `InsertVehicleSnapshot` is an
`ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE` where the latest capture for
a calendar day replaces that day's row. The five derived columns are computed **in Go,
before the SQL call** (D8), and passed as ordinary bound parameters — exactly like every
other typed column already in this query. They are therefore added to **both** the `INSERT`
column list **and** the `DO UPDATE SET` clause, so a same-day re-capture recomputes and
refreshes them identically to every other typed column. There is no special-case SQL for
these five columns; they ride the same upsert machinery `battery_level_pct`,
`odometer_km`, etc. already use. This is what prevents the correctness bug the dispatch
warns about: a same-day re-capture that left stale derived values.

### D7 (L2) — "Previous record", precisely, and its index

**Definition, refined for correctness under upsert/recapture (see below):** the previous
record for an incoming snapshot at local calendar day `D` (in the poller's configured
`Config.Location`) is the row for the same `(account_id, tesla_id)` with the greatest
`captured_at` strictly earlier than the **start of local calendar day `D`** — not merely
earlier than the incoming snapshot's own `captured_at`.

**Why the refinement is necessary (interaction with D6/L1):** the leader's dispatch defines
"previous" as "the greatest `captured_at` strictly less than the incoming snapshot's
`captured_at`." That definition is correct for a fresh insert (no existing row for today),
because in that case the greatest earlier `captured_at` is exactly yesterday's row. But
under a same-day re-capture (D6/L1: `--once` run twice in one day, or any repeat nightly
run), **today's own row already exists** in the table at the moment the second capture's
"find my previous row" lookup runs — and today's own `captured_at` is earlier than the
second capture's `captured_at`. A literal "captured_at less than mine" lookup would select
**today's own row about to be replaced** as its "previous" row, producing a near-zero
same-day delta instead of the correct day-over-day delta against yesterday. That would
silently corrupt exactly the case D6 exists to get right. Bounding the lookup by the
**start of today's calendar day** instead of by the incoming row's own instant excludes
today's own (about-to-be-replaced) row from candidacy, regardless of whether this is a
fresh insert or a same-day re-capture — and is a strict no-op change for the fresh-insert
case (there, no row exists for today at all, so both definitions select the same
predecessor).

**How it is fetched:** a new pure Go helper, `dayStart(t time.Time, loc *time.Location)
time.Time`, mirrors the existing `dateOnly` helper (`service.go`) but returns the
**local-zone midnight instant** that begins `t`'s calendar day (`time.Date(y, m, d, 0, 0,
0, 0, loc)`), instead of `dateOnly`'s UTC-midnight-normalized calendar-date value. The
collector calls a new store method, `previousSnapshot(ctx, accountID, teslaID,
dayStart(capturedAt, loc))`, which runs:

```sql
SELECT ... FROM vehicle_snapshots
WHERE account_id = @account_id AND tesla_id = @tesla_id AND captured_at < @before
ORDER BY captured_at DESC
LIMIT 1;
```

returning `nil, nil` (no row, no error) when the vehicle has no snapshot before that
instant — the first-ever-snapshot case (D8/L5).

**Index plan verdict: no new index.** The existing `idx_vehicle_snapshots_vehicle_time
(account_id, tesla_id, captured_at)` (ascending B-tree) already serves this query as a
**backward index scan**: the planner seeks to `(account_id, tesla_id, before)` and walks
the index in reverse to satisfy `ORDER BY captured_at DESC`, stopping after the first
matching row because of `LIMIT 1` — a single-row index probe, no sort step, no heap scan of
unrelated rows. This is the same backward-scan technique `SnapshotsByVehicleSince` and
`SnapshotsByVehicleBetween` (both forward range scans on the same index, per their own
design docs) already rely on, applied in the opposite direction; Postgres B-tree indexes
support efficient bidirectional traversal by construction, so no new index, no new
migration object, and no schema change is needed for this lookup. Adding a dedicated
`DESC` index would be pure duplication of an index that already answers this access
pattern in O(log n) plus a 1-row walk.

### D8 (L3) — Derive in Go: a separate `deriveConsumption(prev, cur)` step

`snapshotFrom` (`service.go`) is, and remains, a **pure function of the Tesla DTO** — it has
no access to a previous row and none is added to its signature. Instead, a new, separate,
pure function is introduced:

```go
func deriveConsumption(prev *Snapshot, cur Snapshot) Snapshot
```

Called from `attemptVehicle`, **after** `snapshotFrom` builds `cur` and **before**
`s.store.insertSnapshot(ctx, snap)` is called:

```go
snap := snapshotFrom(v.AccountID, v.TeslaID, s.now(), s.location(), data, raw)
prev, err := s.store.previousSnapshot(ctx, v.AccountID, v.TeslaID, dayStart(s.now(), s.location()))
// a previousSnapshot error is treated as "no predecessor found" (api-error path already
// exists for insertSnapshot failures; see tasks.md for the exact error-handling shape)
snap = deriveConsumption(prev, snap)
if err := s.store.insertSnapshot(ctx, snap); err != nil { ... }
```

**Rationale for this shape over extending `snapshotFrom`'s signature:**
- `snapshotFrom` stays a pure DTO→domain mapper with a single responsibility (the exact
  shape every existing unit test for it — `snapshot_from_test.go` — already assumes);
  widening it to also need a previous-row lookup would mix a pure mapping concern with a DB
  concern in one function, and would force every existing `snapshotFrom` call site and test
  to thread a `*Snapshot`/store argument it doesn't otherwise need.
- `deriveConsumption` is independently and trivially unit-testable: it takes two plain
  `Snapshot` values (or `nil` for `prev`) and returns a `Snapshot` — no DTO, no store, no
  DB, no context. This directly serves the "Unit tests: included" requirement (tasks.md
  T6) and lets every DU2 edge case (zero divisor, negative divisor, multi-day gap,
  nil `prev`) be tested as a pure table-driven test.
- It mirrors the module's own established precedent: `deriveEnergyKWh` /
  `deriveTotalCost` (Source B, `service.go`) are already separate pure derivation functions
  called from `collectChargingHistory` rather than folded into the DTO mapper — this change
  extends the exact same pattern to Source-A-adjacent derivation instead of introducing a
  new one.

`deriveConsumption`'s body (formulas restated for D1/D2 compliance):

```go
func deriveConsumption(prev *Snapshot, cur Snapshot) Snapshot {
	if prev == nil {
		return cur // D8/L5: no predecessor — all five fields stay nil.
	}
	distance := cur.OdometerKm - prev.OdometerKm
	cur.DistanceTraveledKmCalc = &distance

	batteryUsed := prev.BatteryLevelPct - cur.BatteryLevelPct
	cur.BatteryUsedPctCalc = &batteryUsed

	days := int(cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24)
	cur.DaysSpannedCalc = &days

	if batteryUsed > 0 { // D2: only a positive divisor yields a ratio
		kmPerPct := distance / float64(batteryUsed)
		cur.KmPerPctCalc = &kmPerPct
		estRange := kmPerPct * 100
		cur.EstimatedRangeKmCalc = &estRange
	}
	return cur
}
```

`days` uses `CapturedDate` (already UTC-midnight-normalized calendar dates, per the
existing `dateOnly` convention) rather than `CapturedAt`, so the day count is an exact
whole number with no time-of-day noise — matching the backfill's `DATE − DATE` arithmetic
exactly (D4).

### D9 (L4) — Pointer types on `Snapshot`, NULL convention restated

The five new domain fields are pointer types, mirroring the module's existing D12/DSA3
convention (charge-enrichment fields, TPMS fields, `MaxRangeChargeCounter`):

```go
DistanceTraveledKmCalc *float64 // km — nil = no predecessor (first-ever snapshot) or pre-migration row never backfilled
BatteryUsedPctCalc     *int     // pct — nil = same as above; may be *negative* (net charge overnight) — a truthful reading, never clamped
KmPerPctCalc           *float64 // km per 1% battery — nil when no predecessor, OR when BatteryUsedPctCalc <= 0 (D2)
EstimatedRangeKmCalc   *float64 // km — nil under the same conditions as KmPerPctCalc (D2); == KmPerPctCalc * 100 whenever non-nil
DaysSpannedCalc        *int     // whole calendar days between the previous row and this one — nil when no predecessor
```

NULL is reserved for exactly three cases, restated precisely:
1. The first-ever snapshot of a vehicle (no predecessor) — D8/L5.
2. `KmPerPctCalc`/`EstimatedRangeKmCalc` only, when `BatteryUsedPctCalc <= 0` — D2.
3. A pre-migration row the backfill could not reach — does not occur in practice, because
   the backfill (D4) covers every existing row in the same migration; this case is listed
   only for completeness with the module's existing NULL-convention documentation style.

A genuine `0` (e.g. `DistanceTraveledKmCalc = 0` on a day the vehicle never moved, or
`DaysSpannedCalc = 1` in the ordinary case) is always stored non-NULL — the pointer-wrap
convention exists precisely so a truthful zero is distinguishable from "not computed."

### D10 (L5) — First-ever snapshot: all five NULL, expected behavior

Falls directly out of D8/D9: `deriveConsumption(nil, cur)` returns `cur` unchanged, so
every one of the five new fields stays `nil` → stored `NULL`. This is not an error
condition and requires no special-case error handling anywhere in the collector — the
`previousSnapshot` store method's own "no row found" return (`nil, nil`) is the mechanism,
not a sentinel error the caller must branch on.

### D11 (L6) — Late-arriving rows leave a successor's derived values stale

**Named limitation, accepted posture: document, do not recompute-forward.** If a missing
night's data is ever inserted after the following night's row was already written and
derived (e.g. a manual backfill insert, or a delayed `--once` run for a specific past
date), that following row's five derived columns were computed against whatever WAS the
previous row at the time — which is now stale, because a new row has been inserted between
them.

This is accepted, not solved, in this change: recomputing every downstream row's derived
values whenever an out-of-order row is inserted would require either (a) a database
trigger — new kind of DB object, runs on every write including the hot nightly path, and
silently couples correctness to a mechanism invisible from `internal/telemetry`'s Go code
(the exact anti-pattern `ai/go-conventions.md`'s "derive in Go, not SQL" precedent this
design otherwise follows exists to avoid), or (b) an application-level "walk forward and
recompute" pass, which is unbounded work outside this change's scope and has no current
caller (there is no late-arriving-insert code path in this codebase today — `--once` always
writes "now," and no backfill-a-specific-past-date tool exists). Building either mechanism
speculatively, for a scenario this codebase cannot currently trigger, is exactly the kind
of premature abstraction `CLAUDE.md`'s AI-efficiency principle warns against. If a
late-arriving-insert capability is ever added, recompute-forward becomes an in-scope
requirement of *that* change, not this one.

---

## Schema

### DDL (goose migration)

**Filename:** `internal/telemetry/db/migrations/20260814000001_add_derived_consumption_columns_vehicle_snapshots.sql`
(next available slot after `20260806000001`).

```sql
-- +goose Up
-- internal/telemetry — add five derived consumption columns to
-- vehicle_snapshots (MAG-10, telemetry-add-derived-consumption-columns):
-- distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
-- estimated_range_km_calc, days_spanned_calc. All five are nullable and
-- computed from the current row and its predecessor for the same
-- (account_id, tesla_id), ordered by captured_at. NULL means "no
-- predecessor exists" (the vehicle's first-ever snapshot, design D8/D10);
-- km_per_pct_calc and estimated_range_km_calc are additionally NULL
-- whenever battery_used_pct_calc <= 0 — a zero or negative divisor has no
-- truthful ratio (design D2). The write path computes these in Go
-- (deriveConsumption, service.go) at capture time — this migration's
-- backfill below is the ONE-TIME exception, mirroring
-- 20260805000001_dedupe_vehicle_snapshots_daily.sql's own backfill: a
-- point-in-time conversion of already-stored rows, not an ongoing schema
-- dependency (design D4).
ALTER TABLE vehicle_snapshots
    ADD COLUMN distance_traveled_km_calc DOUBLE PRECISION,
    ADD COLUMN battery_used_pct_calc     INTEGER,
    ADD COLUMN km_per_pct_calc           DOUBLE PRECISION,
    ADD COLUMN estimated_range_km_calc   DOUBLE PRECISION,
    ADD COLUMN days_spanned_calc         INTEGER;

-- Backfill every existing row in one pass via LAG() partitioned by vehicle,
-- ordered by capture time (design D4). The oldest row per vehicle has no
-- predecessor: LAG() returns NULL for it, the WHERE clause below excludes
-- it from the UPDATE, and it correctly keeps NULL in all five columns —
-- the same "first-ever snapshot" case the Go write path gives via
-- deriveConsumption(nil, cur) (design D8/D10). The formulas here are
-- written to match deriveConsumption byte-for-byte: same subtraction
-- order, same ">0" divisor guard, same "x100" range formula, same
-- whole-calendar-day count via captured_date (DATE) subtraction.
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

-- +goose Down
-- Full rollback: no row is deleted, only the five added columns. Dividing
-- back out is not needed (nothing here is a unit conversion) — dropping
-- the columns is a complete, lossless-to-everything-else reversal.
ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS days_spanned_calc,
    DROP COLUMN IF EXISTS estimated_range_km_calc,
    DROP COLUMN IF EXISTS km_per_pct_calc,
    DROP COLUMN IF EXISTS battery_used_pct_calc,
    DROP COLUMN IF EXISTS distance_traveled_km_calc;
```

`days_spanned_calc = prev.captured_date - prev.prev_captured_date` relies on Postgres's
built-in `DATE − DATE` arithmetic, which returns a plain `INTEGER` day count — no cast
needed, and it is exactly the same "whole calendar days" quantity `deriveConsumption`
computes from `CapturedDate.Sub(...).Hours() / 24` in Go (D8), because `CapturedDate`
values are UTC-midnight-normalized calendar dates with no time-of-day component, so the
`.Hours()/24` division is always an exact integer with no rounding.

No conversion factor appears anywhere in this migration — `odometer_km` and
`battery_level_pct` are already stored in their final display units (kilometres, whole
percent) by the time this migration's arithmetic runs, since it operates on the typed
columns, not `raw_data`. This is unlike `20260806000001`'s backfill, which multiplied by a
unit-conversion constant; this migration's backfill is pure arithmetic on already-converted
values.

### Index plan

**No new index — the existing `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id,
captured_at)` fully serves the one new query this change adds.** Full justification in D7
above. Restated briefly: `previousSnapshot`'s query (`account_id = $1 AND tesla_id = $2 AND
captured_at < $3 ORDER BY captured_at DESC LIMIT 1`) is a backward scan of the same
ascending B-tree index `SnapshotsByVehicleSince`/`SnapshotsByVehicleBetween` already use
forward — Postgres B-tree indexes are bidirectionally traversable, so the planner seeks to
`(account_id, tesla_id, before)` and walks backward, stopping at the first row because of
`LIMIT 1`. No sort step, no new index maintenance cost on the write path (a cost that would
matter under this project's read-heavy profile only in the sense that an unnecessary index
adds write overhead for zero read benefit — exactly what is being avoided here by not
adding one).

The five new columns themselves are **not indexed** and are not intended to be filtered or
sorted on directly by any query in this change — they ride along on the existing row fetch
of every query that already selects the full `vehicle_snapshots` column list
(`LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`,
`ListSnapshotsByVehicle`), exactly as every prior column addition in this module has done
(TPMS, Source A charge-enrichment, `MaxRangeChargeCounter`).

---

## Write Path

### `InsertVehicleSnapshot` (`internal/telemetry/db/query.sql`)

The five new columns are appended to the `INSERT` column list and `VALUES` (after
`captured_date`, matching the migration's physical column-append order — the module's
existing convention) and to the `ON CONFLICT ... DO UPDATE SET` clause (D6/L1), exactly
like every other typed column:

```sql
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level_pct, @battery_range_km, @charging_state, @charge_limit_soc_pct,
    @odometer_km, @inside_temp_c, @outside_temp_c, @locked, @sentry_mode,
    @car_version,
    @charge_energy_added_kwh, @charger_power_kw, @charger_voltage_v,
    @charger_actual_current_a, @usable_battery_level_pct,
    @max_range_charge_counter,
    @tpms_pressure_fl_psi, @tpms_pressure_fr_psi, @tpms_pressure_rl_psi, @tpms_pressure_rr_psi,
    @captured_date,
    @distance_traveled_km_calc, @battery_used_pct_calc, @km_per_pct_calc,
    @estimated_range_km_calc, @days_spanned_calc
)
ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE SET
    -- ...every existing column exactly as today...
    distance_traveled_km_calc = EXCLUDED.distance_traveled_km_calc,
    battery_used_pct_calc     = EXCLUDED.battery_used_pct_calc,
    km_per_pct_calc            = EXCLUDED.km_per_pct_calc,
    estimated_range_km_calc    = EXCLUDED.estimated_range_km_calc,
    days_spanned_calc          = EXCLUDED.days_spanned_calc,
    updated_at                 = now();
```

### New query: `PreviousSnapshotForVehicle` (`internal/telemetry/db/query.sql`)

```sql
-- name: PreviousSnapshotForVehicle :one
-- Return the single most recent snapshot for a vehicle strictly before the
-- given instant, or pgx.ErrNoRows when none exists (the vehicle's
-- first-ever snapshot — design D8/D10). Callers pass dayStart(capturedAt,
-- loc) as `before` (design D7) — the LOCAL calendar-day start, not the
-- incoming snapshot's own captured_at — so a same-day re-capture cannot
-- select today's own (about-to-be-replaced) row as its own predecessor.
-- Backward scan of the existing idx_vehicle_snapshots_vehicle_time
-- (account_id, tesla_id, captured_at) index (design D7): no new index.
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at,
    distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
    estimated_range_km_calc, days_spanned_calc
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at < @before
ORDER BY captured_at DESC
LIMIT 1;
```

`:one` is chosen over `:many` + `LIMIT 1` because sqlc's `:one` already generates the
`pgx.ErrNoRows`-on-no-match behavior the store method needs to distinguish "no predecessor"
(D8/D10) from a real error — no extra Go-side length check required.

### `Snapshot` struct (`internal/telemetry/telemetry.go`)

Five new pointer fields, documented per D9, placed after the existing TPMS fields (end of
struct, matching the migration's physical column-append order):

```go
DistanceTraveledKmCalc *float64
BatteryUsedPctCalc     *int
KmPerPctCalc           *float64
EstimatedRangeKmCalc   *float64
DaysSpannedCalc        *int
```

### `deriveConsumption` + `dayStart` (`internal/telemetry/service.go`)

Full bodies given in D7 (`dayStart`) and D8 (`deriveConsumption`) above. The `store`
interface (`service.go`) gains one method:

```go
type store interface {
    // ...existing methods...
    previousSnapshot(ctx context.Context, accountID uuid.UUID, teslaID int64, before time.Time) (*Snapshot, error)
}
```

`dbStore.previousSnapshot` calls `q.PreviousSnapshotForVehicle`, maps `pgx.ErrNoRows` to
`(nil, nil)` (D8/D10 — "no predecessor" is not an error), and otherwise maps the row via
the existing shared `rowToSnapshot` mapper (mapping.go) — no new mapper is written.

`dbStore.insertSnapshot` gains five parameters, following the exact same nil→NULL /
non-nil→valid pattern every other nullable column already uses — reusing the **existing**
helpers `float64PtrToPgFloat8` (for the three `DOUBLE PRECISION` columns) and
`intPtrToPgInt4` (for the two `INTEGER` columns), both already defined in `service.go` for
the Source A charge-enrichment columns. No new pgtype-boundary helper is needed.

### Wiring (`internal/telemetry/service.go`, `attemptVehicle`)

```go
snap := snapshotFrom(v.AccountID, v.TeslaID, s.now(), s.location(), data, raw)

prev, err := s.store.previousSnapshot(ctx, v.AccountID, v.TeslaID, dayStart(s.now(), s.location()))
if err != nil {
    // Treated as a transient store failure, same bucket as an insertSnapshot error
    // (ReasonAPIError, retried once by the existing collectVehicle retry loop) — a
    // previousSnapshot lookup failure must not silently proceed as "no predecessor"
    // (that would wrongly NULL out a real vehicle's derived columns on a transient DB hiccup).
    return s.logAPIError(v.TeslaID, "previousSnapshot", err, ReasonAPIError), vehicleConfig{}
}
snap = deriveConsumption(prev, snap)

if err := s.store.insertSnapshot(ctx, snap); err != nil {
    return s.logAPIError(v.TeslaID, "insertSnapshot", err, ReasonAPIError), vehicleConfig{}
}
```

This keeps `attemptVehicle`'s existing error-containment shape: any store failure maps to
`ReasonAPIError` and is retried once by the caller (`collectVehicle`), exactly like the
existing `insertSnapshot` failure path — no new Reason, no new retry mechanism.

---

## Read Path

**No new read method, no plan change.** The five new columns are appended to the end of
the explicit `SELECT` column lists in `LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`,
`SnapshotsByVehicleBetween`, and `ListSnapshotsByVehicle` (matching the migration's
physical column-append order — the same convention every prior column-adding change in
this module has followed, most recently `captured_date, updated_at` in
`20260805000001`). This preserves the single shared `telemetrydb.VehicleSnapshot` struct
across all four query functions, so `rowToSnapshot` (mapping.go) remains the **one** place
that maps a DB row to a domain `Snapshot` — no per-query duplication.

### `rowToSnapshot` (`internal/telemetry/mapping.go`)

```go
DistanceTraveledKmCalc: pgNullableFloat64(r.DistanceTraveledKmCalc),
BatteryUsedPctCalc:     pgNullableInt32AsInt(r.BatteryUsedPctCalc),
KmPerPctCalc:           pgNullableFloat64(r.KmPerPctCalc),
EstimatedRangeKmCalc:   pgNullableFloat64(r.EstimatedRangeKmCalc),
DaysSpannedCalc:        pgNullableInt32AsInt(r.DaysSpannedCalc),
```

Both `pgNullableFloat64` and `pgNullableInt32AsInt` already exist in `mapping.go` (used by
the Source A charge-enrichment fields) — no new mapping helper is needed.

## Read Paths Affected (config.yaml proposal rule)

Every existing consumer of `Reader.LatestSnapshotsByAccount`, `Reader.SnapshotsByVehicleSince`,
and `Reader.SnapshotsByVehicleBetween` gains five additional, always-present (possibly nil)
fields on every returned `Snapshot` — no read path's query shape, index usage, or query
plan changes; the five columns ride along on the same row fetch every one of these queries
already performs. This is the same "precompute at write time, never on read" pattern
`telemetry-store-display-units` used and justified against this project's read-heavy
Performance-Profile: the nightly poller pays the `previousSnapshot` lookup + arithmetic
cost once per vehicle per night; every dashboard render, chart, and API consumer that will
eventually read `DistanceTraveledKmCalc`/`EstimatedRangeKmCalc`/etc. pays zero additional
query or computation cost, ever.

---

## Go-Level Seam Summary (single-source, change-locality)

One new value — the "previous snapshot" — flows through exactly one path:
`attemptVehicle` calls `s.store.previousSnapshot(...)` once, immediately after
`snapshotFrom` and immediately before `deriveConsumption`. `deriveConsumption` is the
**only** place the five derived values are computed; `dayStart` is the **only** place the
same-day-recapture-safe lookup boundary is computed. No second, independent derivation path
exists anywhere in this change (mirrors `20260805000001`'s "Go-Level Seam Summary" for its
own single-source timezone value).

---

## Scope Boundary

This change's entire surface is `internal/telemetry`: one migration, one new query, one
new struct method wiring, five struct fields, two new pure functions
(`deriveConsumption`, `dayStart`), one new store interface method. No file outside
`internal/telemetry/` is touched — unlike `telemetry-dedupe-daily-snapshots`, this change
needs no `cmd/poller/main.go` wiring (no new `Config` field is introduced; `Config.Location`
already exists and is reused as-is for both `dateOnly` and the new `dayStart`).

`openspec/changes/gateway-battery-consumed-chart/` is out of scope (D5/DU5) and is not
touched, read, or referenced by any file this change creates or modifies.

---

## Test Blast Radius

- **New, pure, offline unit tests** for `deriveConsumption` (no DB, no network) — the
  primary test surface for "Unit tests: included." Table-driven, covering: normal driving
  day (positive distance, positive battery-used, both ratios populated); charging day
  (negative `battery_used_pct_calc` → both ratios NULL, D2); parked/zero-delta day (zero
  `battery_used_pct_calc` → both ratios NULL, D2); multi-day gap (`days_spanned_calc > 1`,
  raw multi-day delta stored, D1); first-ever snapshot (`prev == nil` → all five fields
  nil, D8/D10).
- **New, pure, offline unit test** for `dayStart` — mirrors the existing `dateOnly` tests
  in `dedupe_test.go` (a capture near a local-zone day boundary resolves to the correct
  local midnight instant, not the UTC one).
- **Existing DB integration tests** (`fakeStore`-based `service_test.go` tests are
  unaffected — a fake `store` that doesn't implement `previousSnapshot` needs the new
  method added to its fake, returning `nil, nil` by default, matching every other fake
  store method's minimal-implementation precedent).
- **New DB integration test**: same-day re-capture refreshes the five derived columns
  (D6/L1) — insert snapshot A for day 1, insert snapshot B for day 1 (same `captured_date`,
  different `OdometerKm`/`BatteryLevelPct`, later `CapturedAt`) with a distinct day-0
  predecessor already stored; assert the single resulting row's five derived columns
  reflect a recompute against the DAY-0 predecessor (not against snapshot A, which was
  itself replaced) — this is the direct regression test for the D7 refinement.
- **New DB integration test**: `previousSnapshot` round-trips correctly, including
  returning `nil, nil` for a vehicle's first snapshot.
- **Backfill verification** (manual, against a disposable/test DB): after applying the
  migration to a fixture with several consecutive-day rows and one multi-day-gap pair,
  confirm the backfilled values match what `deriveConsumption` would compute for the same
  pairs — exercised as part of T1's acceptance criteria in tasks.md, not a new automated
  Go test (the migration's SQL has no Go test harness of its own, consistent with how
  `20260805000001`'s and `20260806000001`'s backfills were verified — by inspection and
  migration-apply acceptance criteria, not a dedicated test file).

---

## Risks / Trade-offs

- **SQL/Go formula duplication (D4).** Accepted, bounded: the migration runs once and is
  never edited after being applied; a future formula change is a new migration with its
  own backfill, not an edit to this one.
- **A `previousSnapshot` lookup adds one extra query per vehicle per night on the write
  path.** Acceptable under this project's read-heavy Performance-Profile — writes happen
  once per vehicle per night and can afford one extra indexed, `LIMIT 1` query; this is
  the same trade this module already makes for `AccessTokenFor`/`ListVehicles` batching.
- **L6 (late-arriving rows leave a successor stale) is accepted, not solved** (D11). No
  code path in this codebase can currently trigger it; documented so it is visible if one
  is ever added.
- **A same-day re-capture recomputes derived columns against the day-0 predecessor, not
  against the just-replaced same-day row.** This is the deliberate, correct behavior (D7),
  not a limitation — noted here only so a future reader does not mistake it for an
  oversight.

---

## Migration Plan (implementation order for the worker(s))

1. `internal/telemetry/db/migrations/20260814000001_add_derived_consumption_columns_vehicle_snapshots.sql`
   — the DDL above (no dependencies).
2. `internal/telemetry/telemetry.go` — five new `Snapshot` fields; `store` interface gains
   `previousSnapshot` (no dependencies; pure struct/interface additions).
3. `internal/telemetry/service.go` — `dayStart` helper, `deriveConsumption` function,
   `dbStore.previousSnapshot` implementation, `dbStore.insertSnapshot` five new params,
   `attemptVehicle` wiring (depends on step 2).
4. `internal/telemetry/db/query.sql` — `InsertVehicleSnapshot` INSERT/VALUES/ON CONFLICT
   additions; `PreviousSnapshotForVehicle` new query; five-column appends to the four read
   SELECT lists (depends on step 1). Leader runs `make sqlc` after this step.
5. `internal/telemetry/mapping.go` — `rowToSnapshot` five-field mapping additions (depends
   on steps 3, 4).
6. New offline unit tests: `deriveConsumption` table-driven tests, `dayStart` tests
   (depends on step 3).
7. New/updated DB integration tests: same-day re-capture refresh, `previousSnapshot`
   round-trip (depends on steps 1, 4, 5).
8. `internal/telemetry/AGENTS.md` — "Data ownership" / "DTO / units conventions" additions
   documenting the five new columns and their NULL convention (depends on step 1; can run
   any time after the schema is finalized).
9. Verification: `go build ./...`, `go vet ./...`, `go test ./...`,
   `openspec validate telemetry-add-derived-consumption-columns --strict`.
