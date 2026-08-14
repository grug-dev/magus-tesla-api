Source: MAG-10 — https://linear.app/magus-monitor/issue/MAG-10/battery-consumed-columns-vehicle-snapshots
Unit tests: included

## Why

`vehicle_snapshots` already stores everything needed to compute, per vehicle per night: how
far it drove, how much battery it used, and the resulting efficiency. Today a consumer has
to fetch two consecutive rows and do the arithmetic itself, on every read, forever. The
ticket asks for the arithmetic to be precomputed and stored:

1. Distance traveled: `odometer_km` (today) − `odometer_km` (previous record).
2. Battery consumed: `battery_level_pct` (previous record) − `battery_level_pct` (today) —
   "it can be negative, no worries about that" (a net overnight charge).
3. Efficiency: km per 1% battery consumed, and the equivalent estimated full-charge range
   (× 100) — only meaningful when battery was actually net consumed.

The ticket also asks whether the formulas can be applied to existing rows. Yes — this
change backfills all of history in the same migration that adds the columns.

Two things the ticket's arithmetic alone does not resolve, settled with the user before
this proposal was written (full rationale in design.md):

- The nightly poller can miss a night, so "previous record" may be several days back. The
  raw multi-day delta is stored (not silently averaged into a false "daily" figure), tagged
  with how many days it spans in a new fifth column.
- A zero or negative "battery consumed" divisor (parked day, or a day the car net charged)
  makes "km per percent" and "estimated range" undefined — those two columns go NULL rather
  than storing a division-by-zero placeholder or a meaningless negative.

## What Changes

Primary and only module: **`internal/telemetry/`**. One goose migration on
`vehicle_snapshots` (5 new nullable columns + a one-time `LAG()`-window backfill of every
existing row), one new sqlc query (`PreviousSnapshotForVehicle`), a new pure Go function
(`deriveConsumption`) that computes the five values from a snapshot and its predecessor,
and the `attemptVehicle` wiring that calls it before every insert. `InsertVehicleSnapshot`'s
existing `ON CONFLICT ... DO UPDATE` (from `telemetry-dedupe-daily-snapshots`) is extended
to refresh the five new columns too, so a same-day re-capture never leaves them stale.

### New columns on `vehicle_snapshots` (full schema + rationale in design.md)

| Column | Type | Nullable | Meaning |
|---|---|---|---|
| `distance_traveled_km_calc` | `DOUBLE PRECISION` | yes | `odometer_km` − previous row's `odometer_km` |
| `battery_used_pct_calc` | `INTEGER` | yes | previous row's `battery_level_pct` − `battery_level_pct` (may be negative) |
| `km_per_pct_calc` | `DOUBLE PRECISION` | yes | `distance_traveled_km_calc` / `battery_used_pct_calc`, only when the divisor > 0 |
| `estimated_range_km_calc` | `DOUBLE PRECISION` | yes | `km_per_pct_calc` × 100, only when the divisor > 0 |
| `days_spanned_calc` | `INTEGER` | yes | calendar days between the previous row and this one (`1` normally) |

NULL means: no predecessor exists (first-ever snapshot of the vehicle), or — for the two
ratio columns only — the battery-used divisor was ≤ 0. A genuine `0` (e.g. zero km driven)
is always stored non-NULL.

### Derivation happens in Go, once, at write time — never on read

Following the module's established "derive in Go, not SQL" precedent
(`deriveEnergyKWh`/`deriveTotalCost`), a new `deriveConsumption(prev *Snapshot, cur
Snapshot) Snapshot` pure function computes all five values. `snapshotFrom` stays an
unmodified, pure DTO→domain mapper; the previous row is fetched separately via a new store
method, `previousSnapshot`, and passed into `deriveConsumption` from `attemptVehicle`
before the insert. Full seam design, including why this shape was chosen over widening
`snapshotFrom`'s signature, is in design.md D8.

### Existing history is backfilled in the same migration

The Up migration's second statement is a single `UPDATE ... FROM (LAG() window query)`
pass over the whole table, producing results identical to what the Go write path computes
for the same consecutive rows. This is a one-time, point-in-time conversion — the same
framing `telemetry-dedupe-daily-snapshots`'s own backfill used — not an ongoing schema
dependency. No separate `cmd/` runnable, no manual post-migration step.

## Breaking

**No.** Additive only:

- Five new nullable columns on an existing table — no existing column, constraint, or
  index is altered or removed.
- `Snapshot` gains five new named pointer fields — additive, compile-compatible with every
  existing named-field `Snapshot{...}` struct literal (same precedent as every prior
  extraction change in this module).
- The `Collector` and `Reader` port interface signatures are unchanged. No existing method
  signature changes; `store` (unexported) gains one new method, which is an
  implementation-internal detail with no effect on any caller outside `internal/telemetry`.
- `InsertVehicleSnapshot`'s `ON CONFLICT ... DO UPDATE SET` clause gains five more assigned
  columns — this changes the query's SQL text but not its external contract (same conflict
  target, same param-count-per-column pattern every other column already follows).

## Modules Affected

- **`internal/telemetry/`** — sole module touched: migration, one new query, `Snapshot`
  struct, `store` interface + `dbStore` implementation, `attemptVehicle` wiring,
  `rowToSnapshot` mapping, `AGENTS.md` documentation, new tests.
- No other `internal/` module. No change to `internal/account`, `internal/tesla`, or the
  gateway.
- `openspec/changes/gateway-battery-consumed-chart/` is explicitly **out of scope** — the
  user manages that change separately; it is not read, referenced, or touched by this
  change.

## Database Changes

One migration on `vehicle_snapshots` (a table owned solely by `internal/telemetry/db`):
five new nullable columns, no new constraint, no new index, plus a one-time backfill
`UPDATE` of every existing row via a `LAG()` window function. design.md is REQUIRED (this
change touches the DB) and includes: the full `Up`/`Down` DDL including the backfill, the
rationale and rejected alternatives for every one of the five user-confirmed decisions
(DU1–DU5) plus the leader's six implementation constraints (L1–L6), and an index plan
justified against the one new query this change adds (verdict: no new index — the existing
`idx_vehicle_snapshots_vehicle_time` already serves it as a backward scan). The database
design gate triggers and passes.

## Read Paths Affected

`LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`, and the
test-only `ListSnapshotsByVehicle` all gain five more columns riding along on their existing
row fetch — no predicate, no `ORDER BY`, and no query plan changes for any of them. This is
a precompute-at-write-time change matching this project's read-heavy Performance-Profile:
the nightly poller pays one extra indexed `previousSnapshot` lookup plus in-memory
arithmetic per vehicle per night; every future dashboard render, chart, or API consumer
that reads the five new fields pays zero additional query or computation cost.

## Capabilities

### Added / Modified Capabilities

- **`telemetry`** — extends "Nightly Vehicle Snapshot Capture": each captured snapshot now
  also carries five derived consumption values computed against the vehicle's previous
  snapshot (distance traveled, battery consumed, km-per-percent, estimated range, days
  spanned), following the NULL/undefined conventions above. Existing history is backfilled.
  Extends the "Latest Snapshot Read Port" / "Snapshot History Read Port" requirements: the
  returned `Snapshot` carries the five new fields with no read-time computation and no
  companion conversion method, consistent with every prior field extension.

### Consumed Capabilities (no change to their specs)

- None — this changes telemetry's own write and read semantics only.

## Resolved decisions

All five user-confirmed decisions (DU1–DU5) and the leader's six implementation constraints
(L1–L6) were settled before this proposal was authored (see the dispatch's "BINDING DESIGN
DECISIONS" / "LEADER-SUPPLIED CONSTRAINTS" and the "Why" section above for the source
text). design.md records them as D1–D11 (D1–D5 = DU1–DU5, D6–D11 = L1–L6), including one
necessary refinement to L2's literal wording (D7) required for L1's same-day-recapture
correctness to actually hold — flagged and justified there, not silently substituted.

### Out of scope (explicitly deferred)

- **Gateway/dashboard changes.** No consumer of `Reader.LatestSnapshotsByAccount` /
  `SnapshotsByVehicleSince` / `SnapshotsByVehicleBetween` needs to change: `Snapshot` gains
  fields, nothing is removed. `gateway-battery-consumed-chart` is the user's own, separate,
  in-flight change (DU5) and is untouched here.
- **Recomputing a snapshot's successor when a late-arriving row is inserted after it**
  (L6/D11). Named and accepted as a limitation; no code path in this codebase can currently
  trigger it.
- **A dedicated index on the five new columns.** None of the four existing read queries
  filters or sorts on them; they ride along on the existing row fetch (see Index Plan in
  design.md).
