# Design — RM50-analytics-add-tire-pressure-columns

Required because this change touches the database (`openspec/config.yaml` design gate).
Roadmap decisions RD1, RD2, RD7, RD8 of `openspec/roadmaps/RM50-vehicle-status-subsections.md`
are binding here and are not re-argued.

## Overview

Three independent pieces of work, all inside `internal/analytics`:

- **A** — four new raw columns on `analytics.vehicle_metrics`, populated by
  `deriveVehicleMetrics` (`consumed.go`).
- **B** — two existing columns (`distance_traveled_km_calc`, `consumed_pct`) added to the
  `LatestMetricsByAccount` read path. No schema change.
- **C** — a migration that backfills part A's columns on rows that already exist.

## Part A — four raw TPMS columns

### Schema

```sql
ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN tpms_pressure_fl_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_fr_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rl_psi DOUBLE PRECISION,
    ADD COLUMN tpms_pressure_rr_psi DOUBLE PRECISION;
```

All four: nullable, no `DEFAULT`. Names match `telemetry.vehicle_snapshots`' own column
names exactly (`fl`/`fr`/`rl`/`rr` = front-left / front-right / rear-left / rear-right),
per RD1's naming convention (unit before any suffix) and roadmap instruction.

### D1 — Column set and naming

**Decision:** four scalar columns, one per wheel, named identically to their
`telemetry.vehicle_snapshots` source columns.

**Rationale:** `telemetry.Snapshot` already exposes `TpmsPressureFLPSI`, `TpmsPressureFRPSI`,
`TpmsPressureRLPSI`, `TpmsPressureRRPSI` — all `*float64`, already in PSI (verified in
`internal/telemetry/telemetry.go`; no telemetry change needed). Matching names removes any
translation table an agent would otherwise have to hold in its head between the two
modules — this is the same "closed, small vocabulary" argument CLAUDE.md's AI-efficiency
rule makes for reusing an existing shape instead of inventing a new one.

**Rejected:** a JSONB blob holding all four values (RD1 already rejected this at the
roadmap level — repeated here because it also settles this column's own DDL). A fixed set
of four wheels needs no schema flexibility, and JSONB would cost the compile-time type
safety `sqlc` gives a scalar column, plus a manual unmarshal step nowhere else in this
module needs.

### D2 — Populated as a raw observation, on every row, never derived

**Decision:** `deriveVehicleMetrics` copies the four values from `cur` (the day's own
`telemetry.Snapshot`) in BOTH code branches — the row with a predecessor and the row
without one. Never re-derived, never converted, never given a fabricated default.

**Rationale:** this is the RM38 status-observation rule, not the `_calc` rule. The KB
guide `kkpa/context/entities/vehicle-metrics/guide.md` states it exactly under
"Conventions & gotchas":

> The eight status observations are populated on EVERY row, including a predecessor-less
> day — the opposite rule to the `_calc` columns. They are raw observations copied
> verbatim from that day's own capture, not deltas, so there is nothing for a missing
> predecessor to invalidate.

TPMS pressure is a raw sensor reading on the day's capture, exactly like
`max_range_charge_counter` (MAG-47) or the eight RM38 columns (`locked`, `inside_temp_c`,
…). It is not a comparison between two days — that comparison is RD3's job, in tier 3,
producing the separate `_calc` delta columns. Gating these four behind the `prev == nil`
check the five `_calc` columns use would blank a vehicle's first tracked day for no
reason: there is no predecessor dependency in a value that is simply read off the current
capture.

**Rejected:** treating these as `_calc`-style (NULL on a predecessor-less day). Rejected
because it would misrepresent a fully-known value as unknown, and because it breaks the
project's own established rule that a raw observation and a derived delta are never
conflated (see the eight RM38 columns' precedent).

### NULL meaning

NULL means one of two things, exactly like the RM38 eight and `max_range_charge_counter`:

1. The vehicle did not report TPMS at that capture — `telemetry.Snapshot`'s own field
   is already nil for this reason (no sensors, absent reading, or the snapshot predates
   telemetry's own TPMS extraction, migration `20260802000001`).
2. This `vehicle_metrics` row predates this migration and was never touched by the
   one-off backfill (Part C) or a later `Recalculate`/`Reconcile` pass.

No disambiguation is needed between these two cases for this change — no consumer needs
to distinguish them yet, exactly as the RM38 precedent states for its own non-ambiguous
seven columns. (Unlike `sentry_mode`/`max_range_charge_counter`, this is not even
ambiguous in practice: `telemetry.Snapshot`'s own TPMS fields have never had a fabricated
non-nil default, so a NULL here reliably means "genuinely not reported or not yet
recalculated" either way — both readings lead to the same "no value" render.)

### Column comments (for `sqlc`)

`sqlc` mirrors `COMMENT ON COLUMN` text into `db/models.go` doc comments (see the KB
guide's own gotcha about this — missing it has already caused two review round-trips on
this table). The migration must carry:

```sql
COMMENT ON COLUMN analytics.vehicle_metrics.tpms_pressure_fl_psi IS
    'Copied verbatim from telemetry.Snapshot.TpmsPressureFLPSI (no re-derivation, no '
    'conversion — already PSI). A raw per-day observation, populated on EVERY row '
    'including a predecessor-less day, exactly like max_range_charge_counter and the '
    'eight RM38 status columns — the opposite rule to the five _calc columns. NULL means '
    'the vehicle did not report TPMS at capture, OR this row predates this migration and '
    'was not touched by the one-off backfill.';
-- (same text, per wheel, for tpms_pressure_fr_psi / _rl_psi / _rr_psi)
```

## Part B — expose two existing columns on the read port

### What changes

- `internal/analytics/db/query.sql`'s `LatestVehicleMetricsByAccount` SELECT gains
  `distance_traveled_km_calc, consumed_pct` in its column list. No new query, no new
  index, no schema change — both columns already exist on `vehicle_metrics`
  (migration `20260821000001`).
- `analytics.VehicleStatus` (in `analytics.go`) gains two pointer fields:
  `DistanceTraveledKmCalc *float64`, `ConsumedPct *float64`. Pointer because both source
  columns are nullable — NULL on a predecessor-less day, exactly the same rule
  `ConsumedByDay`/`OdometerDeltaByDay` already document for these two columns.
- `reader.go`'s `LatestMetricsByAccount` mapping loop gains two lines using the already-existing
  `ptrFloat64FromPg` helper (`mapping.go`) — no new mapping helper needed.

### D3 — No new index

**Decision:** no index changes for either Part A or Part B.

**Analysis against the read pattern:** `LatestMetricsByAccount` is backed by
`LatestVehicleMetricsByAccount`, a `DISTINCT ON (tesla_id) … WHERE account_id = @account_id
ORDER BY tesla_id, metric_date DESC` query, served by `idx_vehicle_metrics_latest
(account_id, tesla_id, metric_date DESC)` (added at the `RM38` design gate specifically to
match this query). Every column this change adds to the SELECT list — the four raw TPMS
columns, plus the two newly-exposed `distance_traveled_km_calc`/`consumed_pct` — is
**projected only**. None appears in a `WHERE`, `JOIN`, or `ORDER BY` clause, in this change
or in any planned future one. Postgres reads a projected-only column straight off the
already-located heap row (or the index-only scan's visibility check) at zero extra cost to
the index scan itself; adding a column to an index buys nothing when nothing ever filters
or sorts on it, and costs write time on every nightly `Recalculate`/`Reconcile` UPSERT
for no read benefit — the same reasoning `20260905000001`'s "no new index" note already
gives for `max_range_charge_counter`. This conclusion covers both Part A and Part B: the
two Part B columns are pre-existing table columns gaining a new consumer, not a new
column, and that consumer is the identical already-indexed query.

**Rejected:** adding any of the six touched columns to `idx_vehicle_metrics_latest` or a
new supporting index. Rejected because the read pattern shows no predicate or order that
would use it — Performance-Profile's "index aggressively" license is bounded by an actual
read pattern that benefits, and none exists here.

## Part C — backfill migration (RD2)

### The deviation, stated explicitly

This migration violates the `analytics` spec's own requirement, "No Cross-Module Database
Access," which otherwise binds this module to reading sibling data only through public Go
ports. The user was shown the boundary-respecting alternative (below) and the fact that
this breaks the written rule, and chose the direct migration anyway. This is recorded here
because CLAUDE.md requires every deviation from a stated architecture rule to be visible
in the change that makes it, not buried in a roadmap file three tiers back.

**Why the deviation is bounded, not a precedent for widening the boundary generally:**

- It is a **one-off data migration**, run once at deploy time by `goose`, never again.
  It is not application code, not a `Go` import, and not a runtime call path.
- **Go code still never reaches across the module boundary.** No `.go` file in
  `internal/analytics` imports `internal/telemetry/db` or any other module's database
  package. The boundary Go code must respect is completely intact after this change.
- **`make boundary-guard` does not catch this and needs no escape-hatch comment.** The
  guard only greps `internal/gateway/**/*.go` for an `internal/telemetry` Go import
  (`ai/architecture.md` §7's "Exception" section). A SQL migration file is neither a
  gateway file nor a Go import, so the guard has nothing to say about it — verified by
  reading the guard's own grep target before writing this migration.
- **It is executable without extra privilege.** `make db-setup` makes the single
  application role the owner of every module's schema (verified: each module's own
  `move_*_to_own_schema` migration runs `CREATE SCHEMA` as that same role, so the role
  already owns `analytics` and `telemetry` both). No `GRANT` statement is needed for this
  `UPDATE ... FROM` to succeed.

**Rejected alternative (the recommended one):** delete the `vehicle_snapshots` watermark
row from `vehicle_metric_watermarks` and let the next nightly `Reconcile` rebuild the
affected history through the normal `Recalculate` path — fully within the module
boundary, self-healing, no cross-schema SQL. This is the pattern the KB guide documents
for retiring a watermark vocabulary value ("Retire a vocabulary value by DELETing its
rows, never by UPDATEing them"). The user considered this and chose the direct migration
instead, for immediacy: a watermark reset would leave every existing row's four new
columns NULL until that vehicle's next nightly cycle, whereas the migration populates them
the moment it runs. **Do not silently substitute this alternative — the user was shown it
and declined it.**

### Migration order check (CLAUDE.md's "verify the Makefile targets" requirement)

`Makefile` line 49: `MIGRATIONS_DIRS ?= internal/account/db/migrations
internal/telemetry/db/migrations internal/charging/db/migrations
internal/analytics/db/migrations`. Goose applies each directory to completion, in this
listed order, before moving to the next (`ai/go-conventions.md` §Testing, "Ordering
between directories matters"). `analytics` is the LAST directory in the list, so every
`telemetry` migration — including `20260802000001_add_tpms_pressure_columns.sql` (adds
the source columns) and `20260806000001_store_display_units_vehicle_snapshots.sql`
(renames them to their current `_psi`-suffixed names) — has already applied by the time
any `analytics` migration runs, on every environment, with no reordering possible short of
editing the Makefile itself. **Finding: no change needed to `MIGRATIONS_DIRS`, no guard
needed beyond what already exists.** This backfill's `FROM telemetry.vehicle_snapshots`
clause is always resolvable.

#### Correction found during wave 1 — the check above covered production only

The check above is correct for every real environment, and it stayed correct. It was
incomplete: it read the Makefile and never read the **test** harness, which builds its own
database and does not use `MIGRATIONS_DIRS` at all.

`internal/analytics/testdb_test.go` listed its own `migrationDirs` with `db/migrations`
(analytics) FIRST, then telemetry, then charging — the opposite of the Makefile's order.
Its comment said "Order is irrelevant today — there are no cross-module foreign keys",
which was true until this change. The backfill is not a foreign key, but it does read
another module's table, so it made order matter for the first time.

The owner's test run caught it:

```
provision: apply migrations: goose up: partial migration error
(type:sql,version:20260908000002):
ERROR: relation "telemetry.vehicle_snapshots" does not exist (SQLSTATE 42P01)
```

**Fix (D4):** reorder `migrationDirs` so analytics is applied LAST, matching
`MIGRATIONS_DIRS`. The migration SQL is unchanged — the user-confirmed design of
D-GATE-1 stands exactly as approved.

**Rejected:** wrapping the backfill in a `to_regclass('telemetry.vehicle_snapshots') IS
NOT NULL` guard so it skips when the table is absent. It would have made the suite pass,
but by making the backfill do nothing in silence. The failure we saw is the only signal
that the apply order is wrong; a guard deletes that signal and would let a real
environment ship with an empty backfill and no error. It would also change SQL the user
confirmed at the design gate.

**Lesson for later tiers:** "which order do migrations apply in" has two answers in this
repo — the Makefile's, and each test package's own `migrationDirs`. A cross-schema
statement must check both.

### SQL

```sql
UPDATE analytics.vehicle_metrics vm
SET
    tpms_pressure_fl_psi = vs.tpms_pressure_fl_psi,
    tpms_pressure_fr_psi = vs.tpms_pressure_fr_psi,
    tpms_pressure_rl_psi = vs.tpms_pressure_rl_psi,
    tpms_pressure_rr_psi = vs.tpms_pressure_rr_psi
FROM telemetry.vehicle_snapshots vs
WHERE vm.account_id  = vs.account_id
  AND vm.tesla_id    = vs.tesla_id
  AND vm.metric_date = vs.captured_date - 1;
```

`vm.metric_date` is the row's *effective* day (`captured_date` minus one calendar day —
`consumed.go`'s `effectiveDay`, unchanged by this migration). `vs.captured_date - 1`
recomputes that same offset in plain SQL to find the matching snapshot. A
`vehicle_metrics` row with no matching snapshot (its source row was deleted, or the
snapshot predates telemetry's own retention) is left with all four columns still NULL —
the correct outcome per the NULL-meaning section above, not an error.

No `to_regclass`-style existence guard: unlike `internal/charging`'s soft cross-module
backfill (`ai/go-conventions.md` §Testing), `telemetry.vehicle_snapshots` is not an
optional, conditionally-present table here — the migration order check above proves it
always exists by the time this migration runs. A guard would hide a real ordering bug
instead of surfacing it.

### Down migration

Only reverses the schema change (drop the four columns) — mirrors
`20260905000001`'s own Down block, which does not attempt to un-backfill data either. A
`Down` that also tried to null out backfilled values would be pure churn: the columns are
about to be dropped anyway.

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing ("author their
expected values up front"). A later worker writes test code against this — it may not
invent expectations by reading the implementation.

### Pure function: `deriveVehicleMetrics` (offline, `consumed_test.go`)

**Input 1 — predecessor-less row.** One `telemetry.Snapshot` with
`TpmsPressureFLPSI = ptr(42.5)`, `TpmsPressureFRPSI = ptr(43.0)`, `TpmsPressureRLPSI = nil`,
`TpmsPressureRRPSI = ptr(41.8)`; `preceding = nil`; this snapshot is `snapshots[0]`.

**Expected output:** the single emitted `vehicleMetricRow` has
`TpmsPressureFLPSI = ptr(42.5)`, `TpmsPressureFRPSI = ptr(43.0)`,
`TpmsPressureRLPSI = nil`, `TpmsPressureRRPSI = ptr(41.8)` — copied verbatim, exactly as
given, in the SAME branch that already leaves `DistanceTraveledKmCalc` nil for this row.
`Flagged` stays `false`, unaffected by TPMS.

**Input 2 — row with a predecessor.** Two chronological snapshots; `cur.TpmsPressureFLPSI
= ptr(40.0)` (any values on `prev` — TPMS never reads `prev`, only `cur`).

**Expected output:** the emitted row for `cur` carries `TpmsPressureFLPSI = ptr(40.0)`
(and the sibling three fields from `cur` verbatim), copied through the SAME derivation
that computes `DistanceTraveledKmCalc` etc. for this row — i.e., TPMS population does not
depend on which branch (predecessor-less or not) the row takes; only its SOURCE value
(`cur`'s own field) differs from `prev`.

### DB integration: `Recalculate` round-trip (`db_integration_test.go`)

**Input:** seed one `telemetry.vehicle_snapshots` row with all four TPMS fields non-NULL
(fixture pattern: direct `INSERT`, per this module's existing D19 convention). Call
`Recalculate` for the day containing that snapshot.

**Expected:** `SELECT tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi,
tpms_pressure_rr_psi FROM analytics.vehicle_metrics WHERE account_id = ? AND tesla_id = ?
AND metric_date = ?` returns the same four values, unconverted.

### DB integration: `LatestMetricsByAccount` (`db_integration_test.go`)

**Input:** one vehicle with one `vehicle_metrics` row carrying non-NULL
`tpms_pressure_fl_psi = 42.5`, `distance_traveled_km_calc = 12.3`, `consumed_pct = 5.0`.

**Expected:** the returned `VehicleStatus` has `TpmsPressureFLPSI != nil && *TpmsPressureFLPSI
== 42.5`, `DistanceTraveledKmCalc != nil && *DistanceTraveledKmCalc == 12.3`,
`ConsumedPct != nil && *ConsumedPct == 5.0`.

**Input — a pre-migration row** (all six new-to-the-port columns NULL, mirroring the
existing "pre-migration row" fixture case already in this file for the RM38 eight):
returned `VehicleStatus` has all six new fields `nil`, no error, no other field affected.

### DB integration: backfill migration round-trip (`migrations_test.go` or equivalent —
see task list)

**Input:** apply migrations up to (not including) this one. Insert one
`telemetry.vehicle_snapshots` row with `captured_date = D`, all four TPMS fields non-NULL.
Insert one `analytics.vehicle_metrics` row with `metric_date = D - 1` (the matching
effective day) and the four new columns left NULL (simulating a pre-existing row). Apply
this migration.

**Expected:** the `vehicle_metrics` row's four TPMS columns now equal the
`vehicle_snapshots` row's four values. A second `vehicle_metrics` row with no matching
`vehicle_snapshots` row (different `metric_date`, no snapshot) keeps all four columns
NULL — not an error, not a fabricated zero.

**Down migration:** re-running Down then Up leaves the table in the same shape (round-trip
test) — mirrors this module's own existing round-trip test convention for schema
migrations (`ai/go-conventions.md`, `20260828000001`'s precedent).

## Files touched (for the implementing worker)

- `internal/analytics/db/migrations/20260908000002_add_tpms_pressure_columns.sql` — new.
- `internal/analytics/db/query.sql` — `UpsertVehicleMetric` gains four params;
  `LatestVehicleMetricsByAccount` gains six columns (four new + two existing).
- `internal/analytics/db/models.go`, `query.sql.go` — regenerated by `make sqlc`, not
  hand-edited.
- `internal/analytics/analytics.go` — `VehicleStatus` gains six pointer fields.
- `internal/analytics/consumed.go` — `vehicleMetricRow` gains four fields; both branches
  of `deriveVehicleMetrics` populate them from `cur`.
- `internal/analytics/recalculate.go` — `upsertVehicleMetricParamsFrom` maps the four new
  fields (reuses the existing `pgFloat8FromPtr` helper — no new mapping code).
- `internal/analytics/reader.go` — `LatestMetricsByAccount`'s mapping loop gains six
  lines (reuses the existing `ptrFloat64FromPg` helper — no new mapping code).
- `internal/analytics/AGENTS.md` — document the four new columns and the two newly
  exposed `VehicleStatus` fields (see tasks.md).
- `kkpa/context/entities/vehicle-metrics/guide.md` — update the column list and
  `VehicleStatus` field list (see tasks.md; not edited in this dispatch).

No file outside `internal/analytics` changes. `internal/telemetry` needs no change at
all — verified fact, not re-derived.
