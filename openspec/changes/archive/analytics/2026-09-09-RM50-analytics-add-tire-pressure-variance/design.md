# Design — RM50-analytics-add-tire-pressure-variance

Required because this change touches the database (`openspec/config.yaml` design gate).
Roadmap decisions RD1, RD3, RD8, RD12 of
`openspec/roadmaps/RM50-vehicle-status-subsections.md` are binding here and are not
re-argued. Tier 1's design (`openspec/changes/archive/analytics/2026-09-08-RM50-analytics-add-tire-pressure-columns/design.md`)
is read-only precedent; this file does not edit it.

## Overview

Two independent pieces of work, all inside `internal/analytics`:

- **A** — four new `_calc` delta columns on `analytics.vehicle_metrics`, computed in the
  pure-maths layer and populated by `Recalculate`.
- **B** — a migration that backfills part A's columns on rows that already exist, by
  joining `vehicle_metrics` against itself.

## Part A — four `_calc` delta columns

### Schema

```sql
ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN tpms_pressure_fl_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_fr_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rl_psi_calc DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rr_psi_calc DOUBLE PRECISION;
```

All four: nullable, no `DEFAULT`. Names match RD1 exactly: unit before `_calc`, mirroring
`distance_traveled_km_calc`.

### D1 — Column set, naming, and where the maths lives

**Decision:** four scalar columns, one per wheel, computed in `consumption.go`'s
`deriveConsumption(prev, cur)` — the same function that already computes
`distance_traveled_km_calc`. `consumptionCalc` gains four more pointer fields:
`TpmsPressureFLPSICalc`, `TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`,
`TpmsPressureRRPSICalc`. `vehicleMetricRow` (`consumed.go`) gains the same four fields,
populated only in the "has a predecessor" branch — left absent (Go zero value, `nil`) in
the predecessor-less branch, exactly like `DistanceTraveledKmCalc`.

**Rationale:** RD3 says "same shape as `distance_traveled_km_calc`". That column is
computed in `deriveConsumption` from the `(prev, cur)` pair — both snapshots already
carry the four raw TPMS fields as public `*float64` (tier 1 verified this; no telemetry
change needed here either). Reusing the same function keeps the change small and puts
the four new fields next to the field they mirror, instead of a second computation site
an agent would have to discover separately.

**Rejected:** computing the deltas in `consumed.go`'s `deriveVehicleMetrics` directly,
bypassing `deriveConsumption`. Rejected because `deriveVehicleMetrics` already delegates
every other `_calc` figure to `deriveConsumption`, and duplicating that split for four
columns would create two derivation homes for the same "prev vs cur" pattern — worse for
an agent reading this module later.

### D2 — Two independent NULL conditions, not one

RD3 states the delta is NULL when the day has no predecessor. That alone is not the
whole rule: each wheel's raw reading (`tpms_pressure_fl_psi` etc., tier 1) is itself a
nullable pointer, independent of whether a predecessor row exists at all. A vehicle can
have a predecessor day, and still be missing one wheel's sensor reading on either day.

**Decision:** a wheel's delta is NULL when EITHER of these is true:

1. The row has no predecessor at all (`prev == nil` in `deriveVehicleMetrics`,
   `consumed.go`) — matches every other `_calc` column's rule.
2. `prev`'s or `cur`'s own raw reading for that specific wheel is nil — there is no
   second operand to subtract.

**Implementation:** a small helper in `consumption.go`,
`tpmsDeltaPSI(prev, cur *float64) *float64`, returning `nil` if either argument is `nil`,
else `*cur - *prev`. Called once per wheel inside `deriveConsumption`, only when `prev`
(the snapshot) is non-nil — condition 1 is already handled by `deriveConsumption`'s
existing `if prev == nil { return consumptionCalc{} }` guard, so the helper itself only
ever needs to handle condition 2.

**Rationale:** "never fabricate" (`CLAUDE.md`, this roadmap's binding rules) — a stored
value must never claim two numbers were compared when one of them was never known. This
is the same reasoning `sumSuperchargerPctBetween` and the other pointer-guarded helpers
in this module already use.

**Rejected:** treating a missing wheel reading as if the wheel simply "did not move"
(defaulting the missing operand to the other day's value, yielding a delta of 0).
Rejected because a `0.0` delta already means something real elsewhere in this schema (no
pressure change) — reusing it for "we don't actually know" would make every `0.0` in
this column ambiguous, exactly the fabrication problem `ai/go-conventions.md`'s
"never fabricate" rule exists to prevent.

### The accepted cost, restated for a future reader

RD3 records this explicitly, and this design repeats it so nobody re-derives it from
first principles later: tyre pressure moves with ambient air temperature, roughly 1 PSI
per 5.5°C. So this delta partly measures the weather, not only a real leak or a real
top-up. **This is accepted, not a defect.** No task in this change, or any later one
touching this column, may add a dead-zone threshold or a comparison against a target
pressure to "correct" it. The roadmap already considered and rejected both (RD3).

### D3 — No new index

**Decision:** no index changes.

**Analysis against the read pattern:** `LatestMetricsByAccount` is backed by
`LatestVehicleMetricsByAccount`, served by `idx_vehicle_metrics_latest (account_id,
tesla_id, metric_date DESC)`. The four new columns are added to the SELECT list only —
never a `WHERE`, `JOIN`, or `ORDER BY` predicate, in this change or any planned one.
Postgres reads a projected-only column at zero extra cost to the index scan itself.
Adding these columns to an index would cost write time on every nightly
`Recalculate`/`Reconcile` UPSERT for no read benefit — identical reasoning to tier 1's D3
and to `20260905000001`'s note for `max_range_charge_counter`.

**Rejected:** adding any of the four columns to `idx_vehicle_metrics_latest` or a new
supporting index. Rejected because no read pattern filters or sorts on them.

## Part B — backfill migration (RD12)

### This is NOT a boundary deviation — say so explicitly

Tier 1's Part C backfill (`RM50-analytics-add-tire-pressure-columns`) crossed schemas —
it read `telemetry.vehicle_snapshots` from an `analytics` migration, and that tier's
design.md records it as a deliberate, user-confirmed deviation from "No Cross-Module
Database Access".

**This migration does not do that.** It reads only `analytics.vehicle_metrics`, joined
against itself. Tier 1 already copied the raw `tpms_pressure_*_psi` values onto every row
of that same table, so the predecessor's raw reading is already sitting in the row this
migration's join reaches — no other module's schema is touched, queried, or named
anywhere in this migration. There is nothing to record as a deviation, and no
`// boundary:allow:` comment is needed (the guard does not grep migration files at all —
verified by reading the guard's own grep target, same check tier 1's design.md already
ran).

### SQL

```sql
UPDATE analytics.vehicle_metrics vm
SET
    tpms_pressure_fl_psi_calc = vm.tpms_pressure_fl_psi - prev.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi_calc = vm.tpms_pressure_fr_psi - prev.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi_calc = vm.tpms_pressure_rl_psi - prev.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi_calc = vm.tpms_pressure_rr_psi - prev.tpms_pressure_rr_psi
FROM analytics.vehicle_metrics prev
WHERE prev.account_id  = vm.account_id
  AND prev.tesla_id    = vm.tesla_id
  AND prev.metric_date = vm.metric_date - 1;
```

**What this does to a row whose previous day is missing:** the `FROM ... WHERE` join
finds no matching `prev` row, so the `UPDATE` never touches that `vm` row at all. Its four
new columns stay at their post-`ADD COLUMN` default, which is `NULL` (no `DEFAULT`
clause). This is the correct outcome per D2, condition 1 — not an error, not a `0`.

**What this does to a row whose previous day exists but is missing one wheel's raw
reading:** Postgres arithmetic on a `NULL` operand always yields `NULL`
(`NULL - x = NULL`), so `vm.tpms_pressure_fl_psi_calc` becomes `NULL` automatically for
that wheel, with no `CASE` needed — matching D2, condition 2, with the exact same rule
the Go implementation uses.

No `to_regclass`-style existence guard: `analytics.vehicle_metrics` is this migration's
own table, always present by construction (every earlier `analytics` migration created
and populated it). There is no ordering question to guard against.

### Migration order check (CLAUDE.md's "verify the Makefile targets" requirement)

`Makefile` line 49 confirms `MIGRATIONS_DIRS ?= internal/account/db/migrations
internal/telemetry/db/migrations internal/charging/db/migrations
internal/analytics/db/migrations` — account → telemetry → charging → analytics,
unchanged since tier 1 checked it. **This migration reads no table outside
`internal/analytics/db/migrations`, so cross-module directory order has no effect on
it at all.** The only ordering requirement is intra-module: this migration must apply
after `20260908000002_add_tpms_pressure_columns.sql` (tier 1), which populates the raw
columns this one reads. Its own filename timestamp
(`20260908000003_add_tpms_pressure_variance_columns.sql`) already sorts after it, and
goose applies one module's directory in filename order.

Tier 1's design.md also records a **test-harness-only** ordering bug it found and fixed:
`internal/analytics/testdb_test.go`'s own `migrationDirs` slice now lists `telemetry`,
then `charging`, then `db/migrations` (this module) last — matching the Makefile. That
fix already stands and this migration needs no further change to it, because — unlike
tier 1's backfill — this migration never reads `telemetry`'s directory at all. Checked
by reading `testdb_test.go` before writing this migration; no edit required there.

### Down migration

Only reverses the schema change (drop the four columns) — mirrors tier 1's own Down
block and `20260905000001`'s, neither of which attempts to un-backfill data.

```sql
ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN tpms_pressure_fl_psi_calc,
    DROP COLUMN tpms_pressure_fr_psi_calc,
    DROP COLUMN tpms_pressure_rl_psi_calc,
    DROP COLUMN tpms_pressure_rr_psi_calc;
```

### Column comments (for `sqlc`)

```sql
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi_calc IS
    'Derived delta: this row''s tpms_pressure_fl_psi minus the previous day''s row for '
    'the same vehicle, in PSI. NULL when this day has no predecessor row, OR when '
    'either day''s own tpms_pressure_fl_psi reading is itself NULL -- never a '
    'fabricated 0. Partly tracks ambient air temperature (about 1 PSI per 5.5 degrees '
    'C) -- this is accepted, not a defect, and must never be "fixed" with a threshold '
    'or a target-pressure comparison (RM50 roadmap RD3).';
-- (same text, per wheel, for tpms_pressure_fr_psi_calc / _rl_psi_calc / _rr_psi_calc)
```

## Makefile and guard check, in both directions (CLAUDE.md requirement)

Checked, not assumed:

- **`db-setup` / `db-reset` role-and-ownership assumptions:** unaffected. This migration
  adds four nullable columns and runs one `UPDATE` inside a schema the app role already
  owns (tier 1 already verified `analytics`'s schema ownership; this change adds no new
  schema, no new role, no new grant).
- **`MIGRATIONS_DIRS`:** unaffected, see the order check above — no directory added,
  removed, or reordered.
- **`make migration-guard`:** the new migration's version prefix
  (`20260908000003`) must be unique across every module's migration directory.
  `20260908000002` (tier 1, `analytics`) is the closest existing number; `20260908000003`
  does not collide with it or with any other module's timestamps (checked:
  `ls internal/*/db/migrations/*.sql` — no other `202609080000*` file exists).
- **`make boundary-guard`:** does not apply — this migration is not a Go import and
  touches no `internal/gateway` file. No `// boundary:allow:` needed (see Part B above).
- **`sqlc`:** `internal/analytics/db/query.sql` changes require `make sqlc` to
  regenerate `models.go` / `query.sql.go`. No new `sqlc.yaml` entry — this module
  already has one (tier 1, RM29).

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing. A later worker
writes test code against this — it may not invent expectations by reading the
implementation.

### Pure function: `deriveConsumption` (offline, `consumption_test.go`)

**Fixture 1 — predecessor exists, all four wheels reported both days.**
`prev.TpmsPressureFLPSI = 35.0`, `cur.TpmsPressureFLPSI = 38.5` (and three more
same-shape pairs, one per wheel, each with a distinct non-zero delta including at least
one negative delta to prove sign is not clamped).

**Expected:** `calc.TpmsPressureFLPSICalc != nil && *calc.TpmsPressureFLPSICalc == 3.5`
(cur minus prev, not prev minus cur), and the matching value for each of the other three
wheels.

**Fixture 2 — `prev == nil`.**

**Expected:** all four fields nil — already covered by the existing
`TestDeriveConsumption_NilPrevReturnsCurUnchanged` fixture; this change only needs to add
the four new field assertions to that existing test, not a new test function.

**Fixture 3 — predecessor exists, one wheel absent on `cur`.**
`prev.TpmsPressureRLPSI = 35.1`, `cur.TpmsPressureRLPSI = nil`.

**Expected:** `calc.TpmsPressureRLPSICalc == nil`. The other three wheels (both readings
present) compute normally.

**Fixture 4 — predecessor exists, one wheel absent on `prev`.**
`prev.TpmsPressureRRPSI = nil`, `cur.TpmsPressureRRPSI = 40.3`.

**Expected:** `calc.TpmsPressureRRPSICalc == nil`. Proves the guard checks BOTH operands,
not only `cur`'s.

### Pure function: `deriveVehicleMetrics` (offline, `consumed_test.go`)

**Fixture 5 — predecessor-less row.** One snapshot, `preceding = nil`.

**Expected:** the emitted row's four `TpmsPressure*PSICalc` fields are all nil — mirrors
the existing `TestDeriveVehicleMetrics_TPMS_PredecessorLess_CopiesVerbatim` fixture's
shape; extend that test (or add a sibling assertion block) rather than write a new
snapshot fixture from scratch.

**Fixture 6 — row with a predecessor.** Reuses
`TestDeriveVehicleMetrics_TPMS_WithPredecessor_CopiesFromCurNotPrev`'s existing snapshot
pair (`prev` PSI values `35.0/35.2/35.1/35.3`, `cur` PSI values
`40.0/40.1/40.2/40.3`).

**Expected:** `entry.TpmsPressureFLPSICalc != nil && *entry.TpmsPressureFLPSICalc == 5.0`
(40.0 − 35.0), and the matching value for the other three wheels
(`4.9`, `5.1`, `5.0`).

### Test scope — one DB-integration test, nothing else new

RD8 bans **cosmetic** tests: tests coupled to markup, CSS classes, ARIA, or element
placement. It does not ban a test of real SQL. So this change adds exactly two things:

1. The pure delta-maths fixtures above (`consumption_test.go`, `consumed_test.go`).
2. **One migration-backfill integration test**, mirroring tier 1's
   `db_tpms_migration_integration_test.go` file for file.

**Why the migration test stays.** Tier 1 wrote the same test and it earned its cost: it
FAILED on first run and exposed a real migration-ordering bug in the test harness. This
migration's self-join is new SQL that no other test executes. Postgres's
`NULL - x = NULL` rule needs no test, but the join predicate does — a wrong
`metric_date - 1`, a missing `tesla_id` in the `WHERE`, or a column typo all compile
fine and all produce silently wrong stored numbers.

**What is still NOT added:** no `Recalculate` round-trip test and no
`LatestMetricsByAccount` test. Those two paths are field-copy plumbing already covered by
the module's existing DB-integration tests, and `go vet` catches signature drift in them.

## Files touched (for the implementing worker)

- `internal/analytics/db/migrations/20260908000003_add_tpms_pressure_variance_columns.sql` — new.
- `internal/analytics/db/query.sql` — `UpsertVehicleMetric` gains four params;
  `LatestVehicleMetricsByAccount` gains four columns.
- `internal/analytics/db/models.go`, `query.sql.go` — regenerated by `make sqlc`, not
  hand-edited.
- `internal/analytics/analytics.go` — `VehicleStatus` gains four pointer fields.
- `internal/analytics/consumption.go` — `consumptionCalc` gains four fields;
  `deriveConsumption` computes them via a new `tpmsDeltaPSI` helper.
- `internal/analytics/consumed.go` — `vehicleMetricRow` gains four fields; the
  "has a predecessor" branch of `deriveVehicleMetrics` populates them from `calc`
  (mirroring `DistanceTraveledKmCalc`'s existing line).
- `internal/analytics/recalculate.go` — `upsertVehicleMetricParamsFrom` maps the four new
  fields (reuses the existing `pgFloat8FromPtr` helper — no new mapping code).
- `internal/analytics/reader.go` — `LatestMetricsByAccount`'s mapping loop gains four
  lines (reuses the existing `ptrFloat64FromPg` helper — no new mapping code).
- `internal/analytics/consumption_test.go`, `internal/analytics/consumed_test.go` — new
  assertions per the Test Contract above. No other test file changes (RD8 — see "No
  DB-integration test for this change" above).
- `internal/analytics/AGENTS.md` — document the four new columns and `VehicleStatus`
  fields (see tasks.md).
- `kkpa/context/entities/vehicle-metrics/guide.md` — update the column list and
  `VehicleStatus` field list (see tasks.md; not edited in this dispatch).

No file outside `internal/analytics` changes. No other module needs a change or a read.
