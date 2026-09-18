# Design — RM66-analytics-add-travel-progress-deltas

## Verified against the code — do not trust the roadmap blindly

The roadmap's "Facts verified against the code" section is binding, but this
section re-checks two more claims this tier depends on that the roadmap did not
already verify, following tier 1's own precedent of re-measuring before design.

### 1. `deriveVehicleMetrics` and `consumptionCalc` — confirmed shape

Read `internal/analytics/consumed.go` and `internal/analytics/consumption.go` in
full.

- `deriveVehicleMetrics` (consumed.go) loops `i := 0` to `len(snapshots)-1`,
  builds one `vehicleMetricRow` per iteration, and appends it to `out`. Two
  branches: `prev == nil` (no predecessor anywhere in storage — every derived
  field stays nil) and the normal branch, which calls
  `deriveConsumption(prev, cur, chargePct)` and copies its result onto the row.
- `deriveConsumption` (consumption.go) already computes four wheel deltas
  (`TpmsPressureFLPSICalc` etc.) via a small helper, `tpmsDeltaPSI(prev, cur
  *float64) *float64` — cur minus prev, nil if either operand is nil. This
  helper has **no PSI-specific logic**: it is a generic nil-safe subtraction
  over two `*float64`.
- **`consumptionCalc` and `deriveConsumption` only ever see a `telemetry.Snapshot`
  pair.** They have no access to a previously-computed `vehicleMetricRow` — the
  type that carries `DistanceTraveledKmCalc`, `ConsumedPct`, `KmPerPctCalc`.
  So the three new deltas D-B describes (computed against "the row the loop
  built one step earlier", i.e. `out[len(out)-1]`) **cannot** live inside
  `deriveConsumption`/`consumptionCalc` — `out` does not exist at that scope.
  They must be computed in `deriveVehicleMetrics` itself, after `calc :=
  deriveConsumption(...)`, reading `out[len(out)-1]` directly. This is a
  necessary consequence of D-B's own instruction, not a new judgment call.

### 2. "Reconcile recalculates full history" — no longer true; the roadmap's own justification is stale

The roadmap justifies D-B's "first day of a window is NULL for that pass" cost
with: *"Reconcile recalculates full history, so the next full pass fills it."*
This was checked against `internal/analytics/recalculate.go`'s `Reconcile` and
against the two mirrored write paths it reads from, because the DATABASE GATE
requires an honest backfill answer and this claim is exactly what a backfill
decision would lean on.

**Finding: the claim is stale.** `Reconcile` reads three per-vehicle
watermarks (`vehicle_snapshots`, `supercharger_sessions`,
`manual_charge_entries`), queries each source for rows with `updated_at >=
cursor - 24h`, and calls `Recalculate` only over the **union of the affected
rows' own calendar days, widened by one day on each side** — never the
vehicle's whole history. For this to "recalculate full history" on every
run, at least one source's `updated_at` would have to advance on every
existing row, every night.

That used to be true for `charging.supercharger_sessions` — but
`internal/charging/db/query.sql`'s `UpsertSuperchargerSession` comment says
so directly: *"UPDATED_AT NOW MEANS 'THIS ROW'S DATA CHANGED' (RM44-charging-
add-change-detecting-mirror, MAG-48). An earlier version ... would make
`updated_at` advance every night, forever, for no reason"* — followed by a
`CASE` that only sets `updated_at = now()` when the mirrored columns actually
differ. **RM44 fixed the exact behavior the roadmap's justification depends
on.** `telemetry`'s own `UpsertVehicleSnapshot` only bumps `updated_at` on a
same-day replace (today's row), never on a historical row. So under the
current code, a vehicle's already-stored history is **not** re-touched by
nightly `Reconcile` unless something about that specific historical row
changes (a manual charge-entry edit, a Supercharger session correction). A
project memory note from before RM44 says the opposite; it is superseded by
this reading of the current code and should not be relied on again for this
question.

**Consequence for this design:** the three new columns need an explicit
one-time backfill migration for pre-existing rows. See "Backfill" below. This
does not touch D-B — D-B governs the **live** derivation loop's shape, not how
already-stored history gets the new columns. Ongoing operation is unaffected:
every future `Recalculate`/`Reconcile` pass computes the three new deltas for
the days it (re)processes, per D-B, with no further action needed.

## Database Changes

### New migration file

`internal/analytics/db/migrations/20260918000001_add_travel_progress_deltas.sql`
— a normal migration, not a baseline edit. `internal/analytics/db/migrations/`
holds exactly one file today, `20260917000001_baseline.sql`; that baseline is
applied and frozen (`ai/go-conventions.md` "one baseline per module"), and
production already holds the old `tpms_pressure_*_psi_calc` names, so the
rename must happen in a new file, not by editing the baseline in place.

### Exact schema change

```sql
-- +goose Up

ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN distance_traveled_km_delta_calc double precision,
    ADD COLUMN consumed_pct_delta_calc double precision,
    ADD COLUMN km_per_pct_delta_calc double precision;

ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fl_psi_calc TO tpms_pressure_fl_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fr_psi_calc TO tpms_pressure_fr_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rl_psi_calc TO tpms_pressure_rl_psi_delta_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rr_psi_calc TO tpms_pressure_rr_psi_delta_calc;

COMMENT ON COLUMN analytics.vehicle_metrics.distance_traveled_km_delta_calc IS
    '...'; -- see "Column comments" below
COMMENT ON COLUMN analytics.vehicle_metrics.consumed_pct_delta_calc IS '...';
COMMENT ON COLUMN analytics.vehicle_metrics.km_per_pct_delta_calc IS '...';
-- plus four fresh COMMENT ON COLUMN statements for the renamed tpms columns,
-- under their new names (Postgres keeps a column's comment across a RENAME,
-- but the comment text itself does not update, so it is worth restating
-- under the new name for a reader who greps by new name only).

-- backfill: see "Backfill" below.

-- +goose Down
-- reverses the rename and drops the three new columns; never un-backfills
-- (mirrors 20260908000003's own Down, which only reverses schema, not data).
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fl_psi_delta_calc TO tpms_pressure_fl_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_fr_psi_delta_calc TO tpms_pressure_fr_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rl_psi_delta_calc TO tpms_pressure_rl_psi_calc;
ALTER TABLE analytics.vehicle_metrics
    RENAME COLUMN tpms_pressure_rr_psi_delta_calc TO tpms_pressure_rr_psi_calc;

ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN distance_traveled_km_delta_calc,
    DROP COLUMN consumed_pct_delta_calc,
    DROP COLUMN km_per_pct_delta_calc;
```

**Why `double precision`, nullable, no `DEFAULT`:** matches every sibling
`_calc`/`_delta_calc` column on this table (`distance_traveled_km_calc`,
`km_per_pct_calc`, the four tyre-pressure deltas) — a day-over-day change is
never known at `ADD COLUMN` time for an existing row, and a `0.0` default would
be indistinguishable from a real "no change" reading. Nullable with no default
is the same choice tier 3's tyre-pressure-variance migration made for the
identical reason.

**Why a rename, not a drop-and-add:** the four `tpms_pressure_*_psi_calc`
columns already hold real day-over-day PSI deltas computed by `deriveConsumption`
today; the values are correct, only the name is wrong under D-D. `RENAME
COLUMN` is instant (a catalog-only change, no table rewrite) and keeps every
existing row's data. Dropping and re-adding would lose history for no reason
and would need a new backfill for data the table already has.

### Column comments

Each new/renamed column gets a comment matching this table's existing style
(see `tpms_pressure_fl_psi_delta_calc`'s own precedent from tier 3):

- `distance_traveled_km_delta_calc`: "Day-over-day change in
  `distance_traveled_km_calc`: this row's value minus the value on the row the
  recalculation loop built immediately before it. NULL when this day is the
  first day of a recalculation pass (no in-loop predecessor for that pass), or
  when either day's own `distance_traveled_km_calc` is itself NULL."
- `consumed_pct_delta_calc`: same shape, naming `consumed_pct`.
- `km_per_pct_delta_calc`: same shape, naming `km_per_pct_calc`.
- The four renamed tyre-pressure columns: carry forward the existing comment
  text unchanged (still accurate — it never named the column itself, only the
  raw `tpms_pressure_*_psi` column it subtracts).

### Index plan — proof, not assertion

**Claim:** the three new columns, and the four renamed ones, are projected
only. No index change is needed.

**Read `internal/analytics/db/query.sql` in full to check this, not assert
it.** Five queries touch `analytics.vehicle_metrics`:

| Query | Uses the affected columns how | Index touched |
|---|---|---|
| `UpsertVehicleMetric` | writes them (INSERT/UPDATE SET) | none — a write, not a read |
| `LatestVehicleMetricsByVehicles` | **SELECT list only** — adds the 3 new + 4 renamed columns to the existing `SELECT DISTINCT ON (tesla_id) ... ORDER BY tesla_id, metric_date DESC` | `idx_vehicle_metrics_latest (tesla_id, metric_date DESC)` |
| `DeleteVehicleMetricsInRangeExcept` | does not reference them at all | `(tesla_id, metric_date)` — unrelated |
| `VehicleMetricsConsumedByVehicleBetween` | does not reference them | same, unrelated |
| `VehicleMetricsOdometerByVehicleBetween` | does not reference them | same, unrelated |

`LatestVehicleMetricsByVehicles` is the only read that changes, and it changes
by **widening the `SELECT` list alone** — the `WHERE tesla_id = ANY(...)`
predicate, the `DISTINCT ON (tesla_id)`, and the `ORDER BY tesla_id,
metric_date DESC` are untouched. `idx_vehicle_metrics_latest` is a btree on
`(tesla_id, metric_date DESC)`: it already satisfies the `WHERE`, the
`DISTINCT ON`, and the `ORDER BY` without touching any of the seven affected
columns, so a wider `SELECT` list changes the row width returned per matched
index entry, never which index entries are scanned or their order. This is
the identical reasoning tier 1 (RM50-analytics-add-tire-pressure-columns D3)
and tier 3 (RM50-analytics-add-tire-pressure-variance, "no index changes"
section) already used for the same index and the same query, re-verified here
against the current query text rather than assumed from precedent.

**Decision: no index change.** Adding these columns to `idx_vehicle_metrics_latest`
would cost write time on every nightly UPSERT (this table's write path) for a
read benefit that does not exist, since nothing filters, joins, or orders on
them.

### Backfill

**Needed: yes**, for the three new columns only — not for the four renamed
ones (a rename carries the existing values forward; nothing to compute).

**What it costs:** one `UPDATE ... FROM` self-join over
`analytics.vehicle_metrics`, joined to itself on `(tesla_id, metric_date - 1)`
— the same shape `20260908000003_add_tpms_pressure_variance_columns.sql`
already ran for the four tyre-pressure deltas. The join key is served by the
table's own `vehicle_metrics_tesla_date_unique (tesla_id, metric_date)`
constraint, so this is an index-nested-loop, not a sequential scan, and it
touches each row once. This runs once, inside the migration, at deploy time —
the owner applies it via `make migrate-up`, the same way every other migration
in this repo is applied. No Fleet API call, no re-derivation in Go: the values
it reads (`distance_traveled_km_calc`, `consumed_pct`, `km_per_pct_calc`) are
already stored on every existing row.

**How it runs:**

```sql
UPDATE analytics.vehicle_metrics vm
SET
    distance_traveled_km_delta_calc = vm.distance_traveled_km_calc - prev.distance_traveled_km_calc,
    consumed_pct_delta_calc         = vm.consumed_pct - prev.consumed_pct,
    km_per_pct_delta_calc           = vm.km_per_pct_calc - prev.km_per_pct_calc
FROM analytics.vehicle_metrics prev
WHERE prev.tesla_id    = vm.tesla_id
  AND prev.metric_date = vm.metric_date - 1;
```

This is not a cross-module read: it joins `analytics.vehicle_metrics` to
itself, the same table this migration already owns and alters, exactly like
tier 3's own precedent (its design.md's "This is NOT a boundary deviation"
section, re-verified true here for the identical reason: no other module's
schema is named).

A row whose previous day is missing gets no match on the `FROM ... WHERE`
join, so the `UPDATE` never touches it — its three new columns stay at their
post-`ADD COLUMN` default, `NULL`. A row whose previous day exists but is
itself missing one of the three underlying figures (e.g. `prev`'s own
`km_per_pct_calc` was NULL because that day's `consumed_pct` was `<= 0`)
yields `NULL` automatically — Postgres arithmetic on a `NULL` operand is
always `NULL`, so no `CASE` is needed, matching Go's own nil-safe subtraction
rule for the identical case.

**Difference from the Go loop's own rule, noted honestly:** the backfill
subtracts the row whose `metric_date` is exactly one calendar day earlier. The
live Go loop (D-B) subtracts whatever row it built immediately before the
current one in that pass — normally the same row, since `vehicle_metrics` is
dense (one row per day that has a telemetry snapshot, per the module's own
D9/D10 design), but not guaranteed identical if a vehicle has a genuine
multi-day capture gap in its history. This is not a new risk introduced here:
the four tyre-pressure deltas' own backfill made the identical choice for the
identical reason, and the raw distance/consumption `_calc` columns already
carry the same "usually yesterday, actually whatever the stored predecessor
is" ambiguity, tracked by this table's own `days_spanned_calc` column.

## Go changes

### `internal/analytics/consumed.go`

- `vehicleMetricRow` gains three fields, next to the existing five `_calc`
  fields: `DistanceTraveledKmDeltaCalc *float64`, `ConsumedPctDeltaCalc
  *float64`, `KmPerPctDeltaCalc *float64`.
- Rename the four `TpmsPressure*PSICalc` fields on `vehicleMetricRow` to
  `TpmsPressure*PSIDeltaCalc`, mirroring the SQL rename.
- `deriveVehicleMetrics`: in the `prev != nil` branch, after `calc :=
  deriveConsumption(...)`, compute the three new deltas against `out[len(out)-1]`
  when `out` is non-empty:

  ```go
  var distanceDelta, consumedDelta, kmPerPctDelta *float64
  if len(out) > 0 {
      prevRow := out[len(out)-1]
      distanceDelta = dayOverDayDelta(prevRow.DistanceTraveledKmCalc, calc.DistanceTraveledKmCalc)
      consumedDelta = dayOverDayDelta(prevRow.ConsumedPct, calc.ConsumedPct)
      kmPerPctDelta = dayOverDayDelta(prevRow.KmPerPctCalc, calc.KmPerPctCalc)
  }
  ```

  `len(out) == 0` (this is the first row this pass processed) leaves all three
  `nil` — D-B's accepted cost, made explicit in code rather than left as a
  silent zero-value pointer a reader has to notice.
- The predecessor-less branch (`prev == nil`) needs no new code: its row
  never sets these three fields, so they keep their pointer zero value
  (`nil`), matching every other derived field in that branch.

### `internal/analytics/consumption.go`

- Rename the generic helper `tpmsDeltaPSI(prev, cur *float64) *float64` to
  `dayOverDayDelta(prev, cur *float64) *float64`. Its body does not change —
  it already has no PSI-specific logic (a plain nil-safe `cur - prev`). This
  is a same-file rename of a function whose only four call sites
  (`consumption.go`'s own `deriveConsumption`) this tier is already touching
  to rename the fields it assigns; it is not a separate sweep. Renaming it
  gives `deriveVehicleMetrics` (consumed.go) a correctly-named function to
  reuse for the three new deltas, instead of either duplicating the
  nil-safe-subtraction logic or calling a function named for a unit it no
  longer exclusively serves — a small, closed vocabulary beats two copies of
  the same three lines.
- `consumptionCalc`'s four `TpmsPressure*PSICalc` fields rename to
  `TpmsPressure*PSIDeltaCalc`; `deriveConsumption`'s four calls move from
  `tpmsDeltaPSI(...)` to `dayOverDayDelta(...)`, argument order unchanged.

### `internal/analytics/analytics.go`

- `VehicleStatus` gains three fields: `DistanceTraveledKmDeltaCalc *float64`,
  `ConsumedPctDeltaCalc *float64`, `KmPerPctDeltaCalc *float64`. Same nil rule
  as `DistanceTraveledKmCalc`/`ConsumedPct`/`KmPerPctCalc` already documented
  on this type, plus the new "or the day was the first day of a recalculation
  pass" condition.
- Rename `VehicleStatus`'s four `TpmsPressure*PSICalc` fields to
  `TpmsPressure*PSIDeltaCalc`.

### `internal/analytics/reader.go` and `internal/analytics/recalculate.go`

- `reader.go`'s `LatestMetricsForVehicles` mapping adds three
  `ptrFloat64FromPg(row.DistanceTraveledKmDeltaCalc)`-style lines (exact
  generated field names depend on `sqlc generate`'s output — sqlc title-cases
  each underscore segment, so `distance_traveled_km_delta_calc` becomes
  `DistanceTraveledKmDeltaCalc`, matching the domain field name exactly this
  time, unlike the `TpmsPressureFlPsi...` vs `TpmsPressureFLPSI...` casing
  mismatch that already exists for the tyre columns) and renames the four
  existing `TpmsPressureFlPsiCalc` reads to `TpmsPressureFlPsiDeltaCalc`
  (sqlc's generated casing) mapped onto `TpmsPressureFLPSIDeltaCalc` (the
  domain field).
- `recalculate.go`'s `upsertVehicleMetricParamsFrom` adds three
  `pgFloat8FromPtr(row.DistanceTraveledKmDeltaCalc)`-style lines and renames
  the four existing `TpmsPressureFlPsiCalc: pgFloat8FromPtr(row.TpmsPressureFLPSICalc)`
  lines to their new names on both sides.
- No new `mapping.go` helper needed: `pgFloat8FromPtr`/`ptrFloat64FromPg`
  already handle `*float64` ↔ `pgtype.Float8`, the same type as every sibling
  `_calc` column.

### `internal/analytics/db/query.sql`

- `UpsertVehicleMetric`: add the three new columns and rename the four tyre
  ones, in both the column list and the `VALUES`/`ON CONFLICT ... SET` clauses.
- `LatestVehicleMetricsByVehicles`: add the three new columns and the four
  renamed ones to the `SELECT` list. Update its own doc comment's "PROJECTED
  ONLY" note to cover the three new columns (following the comment style
  already used for the previous two widenings of this same query — state
  plainly that this is a projection, not a new predicate, without pointing at
  any change or roadmap identifier).

### `internal/analytics/db/migrations/` — after `sqlc generate`

Running `sqlc generate` after the migration lands regenerates
`internal/analytics/db/models.go` and `query.sql.go` with the three new
columns and the four renamed ones (`analyticsdb.UpsertVehicleMetricParams`,
`analyticsdb.LatestVehicleMetricsByVehiclesRow`). This is codegen, not
hand-written Go — tasks.md schedules it as its own step, after the migration
exists and before any Go file that references the new generated names.

## Test contract — authored before implementation

Three integration-test fixtures, stated as concrete input/output pairs so a
test written from this contract measures the design, not the implementation
that happens to exist when the test is written.

### Fixture 1 — a day with a predecessor row

Two consecutive `vehicle_metrics` rows for the same vehicle, both already
present with their existing `_calc` figures (as `Recalculate` would have
written them):

| | `metric_date` | `distance_traveled_km_calc` | `consumed_pct` | `km_per_pct_calc` |
|---|---|---|---|---|
| Row A (predecessor) | day 1 | 50.0 | 15.0 | 3.33 |
| Row B (current) | day 2 | 60.0 | 20.0 | 3.0 |

Recalculating day 2, with day 1 available as `out[len(out)-1]` in the same
pass (a window covering both days), must produce:

- `distance_traveled_km_delta_calc` = 60.0 − 50.0 = **10.0**
- `consumed_pct_delta_calc` = 20.0 − 15.0 = **5.0**
- `km_per_pct_delta_calc` = 3.0 − 3.33 = **−0.33** (approximately)

### Fixture 2 — the first day of a recalculation window

A vehicle whose stored history already has a row for day 0 (not itself
predecessor-less — it has its own valid `distance_traveled_km_calc` etc.).
`Recalculate` (or `Reconcile`) is now called for a window that starts at day
1 only (day 0 is outside the window and is not fetched into `snapshots`/`out`
for this pass), and day 1 itself has a real predecessor snapshot (`preceding`
is non-nil, so day 1's own `distance_traveled_km_calc`/`consumed_pct`/
`km_per_pct_calc` ARE computed normally, non-nil).

Because `out` is empty when day 1 is processed (`len(out) == 0`), the three
new deltas for day 1 must be **absent (NULL)**, even though day 1's own
travel-progress figures are present. This is D-B's accepted cost: the delta
comes back once a later pass includes day 1's own real predecessor day in the
same window (e.g. the next `Reconcile` run, whose window widens by one day on
each side of whatever changed).

### Fixture 3 — a day whose predecessor lacks the underlying figure

Two consecutive rows where the predecessor is itself a predecessor-less row
(the vehicle's first-ever tracked day):

| | `metric_date` | `distance_traveled_km_calc` | `consumed_pct` | `km_per_pct_calc` |
|---|---|---|---|---|
| Row A (predecessor, first-ever day) | day 1 | NULL | NULL | NULL |
| Row B (current) | day 2 | 40.0 | 12.0 | 3.33 |

Recalculating day 2 in a pass where day 1 is `out[len(out)-1]` must produce
**all three new deltas absent (NULL)** — never a fabricated `40.0`, `12.0`, or
`3.33` (subtracting from an absent value is not "no change", it is "unknown").
This exercises `dayOverDayDelta`'s nil-safe rule on the predecessor side,
mirroring how the existing four tyre-pressure deltas already handle a missing
wheel reading on either side.

### Where these fixtures live

Wave placement per `ai/go-conventions.md`'s "Authoring order": Fixtures 1–3
are offline, since `deriveVehicleMetrics` and `dayOverDayDelta` take plain
`telemetry.Snapshot`/`vehicleMetricRow` values and return plain structs — no
database needed. They belong in `internal/analytics/consumed_test.go` (or a
sibling `_test.go`, mirroring wherever `deriveVehicleMetrics`'s other
offline table-tests already live), written in the early wave alongside the
Go changes, verified with `go vet` before any migration exists. The
backfill migration's own behaviour (the self-join `UPDATE`) is **not**
tested — `ai/go-conventions.md` "Do not test migrations" — verified by the
owner inspecting the database directly.

`LatestMetricsForVehicles`'s widened projection (the three new fields
reaching `VehicleStatus`) needs a `TEST_DATABASE_URL`-gated integration test,
since it requires the generated `analyticsdb` types and a real migrated
schema — scheduled in the final wave, after the migration and `sqlc generate`
both exist, per this module's own testing convention.

## delta-guard baseline update

Per the tier-1 guard's own `Makefile` target (`delta-guard`), remove exactly
these entries once the rename lands:

**SQL baseline** (`sqlbaseline` regex in `Makefile`'s `delta-guard` target) —
remove:
- `tpms_pressure_fl_psi_calc`
- `tpms_pressure_fr_psi_calc`
- `tpms_pressure_rl_psi_calc`
- `tpms_pressure_rr_psi_calc`

leaving six: `distance_traveled_km_calc`, `battery_used_pct_calc`,
`days_spanned_calc`, `km_per_pct_calc`, `estimated_range_km_calc`,
`inferred_capacity_kwh_calc` — none of these six is touched by this tier
(the three genuinely new columns this tier adds are already correctly named
`_delta_calc`, so they never enter the baseline at all — the guard's own
delta-suffix exclusion filters them out before the baseline split runs).

**Go baseline** (`gobaseline` regex) — remove:
- `TpmsPressureFLPSICalc`, `TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`,
  `TpmsPressureRRPSICalc` (the domain-cased names, on `consumptionCalc`,
  `vehicleMetricRow`, `VehicleStatus`)
- `TpmsPressureFlPsiCalc`, `TpmsPressureFrPsiCalc`, `TpmsPressureRlPsiCalc`,
  `TpmsPressureRrPsiCalc` (the sqlc-generated casing, on
  `analyticsdb.UpsertVehicleMetricParams` / `...Row`)

leaving seven: `DistanceTraveledKmCalc`, `BatteryUsedPctCalc`,
`DaysSpannedCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc`,
`InferredCapacityKWhCalc`, `InferredCapacityKwhCalc`.

This exactly matches tier 1's own design.md prediction ("the baseline shrinks
from 10/15 to 6/7 when tier 2 lands. It does not reach empty."), now confirmed
by re-reading the current tree rather than assumed.

After the rename, `make delta-guard` must still pass (its remaining
baseline entries only warn, never fail) and must print a smaller baseline
count than before. This is a signal this dispatch's implementation worker can
run directly (`Test-Execution-Policy` lists `make delta-guard` as
Claude-runnable).

## Cross-module work — owned by the leader, not this dispatch

Renaming `VehicleStatus`'s four `TpmsPressure*PSICalc` fields breaks:

- `internal/gateway/handlers/handlers.go:580-583` — `mapDashboardSnapshot`'s
  four `dashTireWheel(ctx, vs.TpmsPressureFLPSI, vs.TpmsPressureFLPSICalc)`
  style calls, one per wheel.
- `internal/gateway/handlers/handlers_test.go` — any fixture literal
  constructing `analytics.VehicleStatus{TpmsPressureFLPSICalc: ...}` for the
  four wheels.

Both files are outside `internal/analytics/`; this module's own sandbox rule
forbids editing them here. The roadmap assigns this fix to the leader's wave
commit, and this design does not change that assignment — it only confirms
the exact call sites by re-reading them, since the roadmap's own line numbers
(580-583) were verified to still point at the four `dashTireWheel` calls in
the current tree.
