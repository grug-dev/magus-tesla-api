# Design — telemetry-store-display-units

## Context

`vehicle_snapshots` is written once per vehicle per nightly collection cycle (`service.go`, upserted
on `(account_id, tesla_id, captured_date)`) and read on every dashboard render, every history-chart
request, and every battery-efficiency derivation. It currently stores Fleet API values in their
native units and pushes conversion onto all of those readers, which inverts the project's
read-heavy performance profile.

Relevant current state:

- **Columns** — `battery_range` and `odometer` are `DOUBLE PRECISION NOT NULL` (miles);
  `inside_temp` / `outside_temp` are `DOUBLE PRECISION NOT NULL` (already Celsius);
  `tpms_pressure_{fl,fr,rl,rr}` are nullable `REAL` (bar); `battery_level` and `charge_limit_soc`
  are `INTEGER NOT NULL`; `usable_battery_level`, `charger_power`, `charger_voltage`,
  `charger_actual_current` are nullable `INTEGER`; `charge_energy_added` is nullable
  `DOUBLE PRECISION`.
- **Conversion** — `telemetry.go:24-34` owns `milesToKm = 1.609344` and
  `barToPSI = 14.503773773`; `telemetry.go:110-155` exposes 6 read-time companions.
- **NULL semantics** — for the nullable columns, NULL means "not reported at capture, or the row
  predates this extraction"; a truthfully reported `0` is stored non-NULL via `ptr()` in
  `snapshotFrom` (the D12/DSA3 convention). This invariant must survive unchanged.
- **Indexes** — `vehicle_snapshots_account_tesla_date_unique` UNIQUE `(account_id, tesla_id,
  captured_date)` and `idx_vehicle_snapshots_vehicle_time` `(account_id, tesla_id, captured_at)`.

## Goals / Non-Goals

**Goals:**
- Store km / °C / PSI, converted exactly once, on the write path.
- Make every unit-bearing column self-describing through a name suffix.
- Leave `internal/telemetry` with zero conversion constants and zero conversion methods.
- Preserve NULL-vs-zero fidelity and the lossless `raw_data` payload exactly as they are today.

**Non-Goals:**
- `supercharger_sessions`, `poll_attempts`, `internal/manualcharge` (RM7 Decision 4).
- Changing column *types*, nullability, indexes, or the upsert key.
- Adopting the renamed fields in `internal/battery` / `internal/gateway` — RM7 tiers 3 and 4.
- Rounding or formatting. The stored value stays a plain unformatted number; thousands-grouping
  is presentation and stays in the gateway (RM7 Decision 11).

## Decisions

### D1 — Full migration: rename all 15, convert 6, single pass

`20260806000001_store_display_units_vehicle_snapshots.sql`:

```sql
-- +goose Up
-- internal/telemetry — store DISPLAY units in vehicle_snapshots.
-- SUPERSEDES the "units stay API-native (miles/bar); km/PSI derived on read"
-- invariant from 20260710000002_init_telemetry.sql and 20260802000001_add_tpms_pressure_columns.sql.
-- Rationale: writes happen once per vehicle per night; reads happen on every
-- dashboard render and every history-chart bar. Converting on write and never on
-- read matches CLAUDE.md's read-heavy Performance-Profile (RM7 Decision 1).
-- Every unit-bearing column also gains a unit suffix so the schema is
-- self-describing (RM7 Decision 2). raw_data is UNTOUCHED and still holds the
-- original miles/bar payload losslessly (RM7 Decision 10).

ALTER TABLE vehicle_snapshots RENAME COLUMN battery_range          TO battery_range_km;
ALTER TABLE vehicle_snapshots RENAME COLUMN odometer               TO odometer_km;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fl       TO tpms_pressure_fl_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fr       TO tpms_pressure_fr_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rl       TO tpms_pressure_rl_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rr       TO tpms_pressure_rr_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN inside_temp            TO inside_temp_c;
ALTER TABLE vehicle_snapshots RENAME COLUMN outside_temp           TO outside_temp_c;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_level          TO battery_level_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN usable_battery_level   TO usable_battery_level_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_limit_soc       TO charge_limit_soc_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_energy_added    TO charge_energy_added_kwh;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_power          TO charger_power_kw;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_voltage        TO charger_voltage_v;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_actual_current TO charger_actual_current_a;

-- One-time, in-place unit conversion of the 6 value-changing columns (RM7 Decision 3).
-- The 9 rename-only columns are already in their target unit: Tesla reports
-- temperatures in Celsius, and %, kWh, kW, V and A are API-native.
-- NULL tpms values stay NULL (NULL * factor = NULL), so the "NULL means not
-- reported / pre-migration" invariant survives untouched; a truthful 0.0
-- converts to 0.0 and stays non-NULL.
-- updated_at is deliberately NOT bumped: these rows were not re-captured, and
-- claiming they were would falsify the audit column added in 20260805000001.
UPDATE vehicle_snapshots SET
    battery_range_km     = battery_range_km     * 1.609344,
    odometer_km          = odometer_km          * 1.609344,
    tpms_pressure_fl_psi = tpms_pressure_fl_psi * 14.503773773,
    tpms_pressure_fr_psi = tpms_pressure_fr_psi * 14.503773773,
    tpms_pressure_rl_psi = tpms_pressure_rl_psi * 14.503773773,
    tpms_pressure_rr_psi = tpms_pressure_rr_psi * 14.503773773;

-- +goose Down
-- Reverses both steps. Unlike 20260805000001's dedupe, this Down IS a true
-- rollback: no row is deleted and no column is dropped, so dividing by the same
-- constants restores the original values to within float round-trip tolerance.
UPDATE vehicle_snapshots SET
    battery_range_km     = battery_range_km     / 1.609344,
    odometer_km          = odometer_km          / 1.609344,
    tpms_pressure_fl_psi = tpms_pressure_fl_psi / 14.503773773,
    tpms_pressure_fr_psi = tpms_pressure_fr_psi / 14.503773773,
    tpms_pressure_rl_psi = tpms_pressure_rl_psi / 14.503773773,
    tpms_pressure_rr_psi = tpms_pressure_rr_psi / 14.503773773;

ALTER TABLE vehicle_snapshots RENAME COLUMN charger_actual_current_a TO charger_actual_current;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_voltage_v        TO charger_voltage;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_power_kw         TO charger_power;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_energy_added_kwh  TO charge_energy_added;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_limit_soc_pct     TO charge_limit_soc;
ALTER TABLE vehicle_snapshots RENAME COLUMN usable_battery_level_pct TO usable_battery_level;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_level_pct        TO battery_level;
ALTER TABLE vehicle_snapshots RENAME COLUMN outside_temp_c           TO outside_temp;
ALTER TABLE vehicle_snapshots RENAME COLUMN inside_temp_c            TO inside_temp;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rr_psi     TO tpms_pressure_rr;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rl_psi     TO tpms_pressure_rl;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fr_psi     TO tpms_pressure_fr;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fl_psi     TO tpms_pressure_fl;
ALTER TABLE vehicle_snapshots RENAME COLUMN odometer_km              TO odometer;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_range_km         TO battery_range;
```

**Column types, nullability and constraints are unchanged** — this migration only renames and
rescales. Resulting schema for the affected columns:

| Column | Type | Null | Unit after |
|---|---|---|---|
| `battery_range_km` | `DOUBLE PRECISION` | NOT NULL | km |
| `odometer_km` | `DOUBLE PRECISION` | NOT NULL | km |
| `inside_temp_c` / `outside_temp_c` | `DOUBLE PRECISION` | NOT NULL | °C |
| `battery_level_pct` / `charge_limit_soc_pct` | `INTEGER` | NOT NULL | % |
| `usable_battery_level_pct` | `INTEGER` | NULL | % |
| `charge_energy_added_kwh` | `DOUBLE PRECISION` | NULL | kWh |
| `charger_power_kw` / `charger_voltage_v` / `charger_actual_current_a` | `INTEGER` | NULL | kW / V / A |
| `tpms_pressure_{fl,fr,rl,rr}_psi` | `REAL` | NULL | PSI |

**Rejected alternatives.**
*Add new columns and dual-write, dropping the old ones later* — safer for a live rolling deploy,
but this is a single-instance deployment with one writer (the nightly poller), so it buys nothing
and leaves a window where two columns disagree. *Re-derive every value from `raw_data`* — heavier
(JSONB extraction per row per column) and would write NULL for any row whose payload lacks the
path, losing data the typed column still holds. *Keep the columns and add a generated km column* —
doubles the row width for a value that is always wanted in one unit, and the miles version would
have no remaining reader.

### D2 — Index plan: nothing to do, and that is verified, not assumed

`vehicle_snapshots` has exactly two indexed objects, both created before this change:

| Object | Columns | Affected? |
|---|---|---|
| `vehicle_snapshots_account_tesla_date_unique` (UNIQUE) | `(account_id, tesla_id, captured_date)` | **No** |
| `idx_vehicle_snapshots_vehicle_time` | `(account_id, tesla_id, captured_at)` | **No** |

None of the 15 renamed columns appears in either. `ALTER TABLE ... RENAME COLUMN` is a catalog-only
operation (no table rewrite), and no index references a renamed column, so **no index is
invalidated, rebuilt, or reordered**. The backfill `UPDATE` rewrites every row's heap tuple, which
does cost index maintenance on the two indexes above — but the table holds on the order of one row
per vehicle per day, so this is a trivially small one-shot cost, not a reason to batch.

**No new index is justified.** The renamed columns are projected values on the existing read paths
(`LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`, `ListSnapshotsByVehicle`), all of which
filter on `(account_id, tesla_id)` and order by `captured_at`. This change introduces no new
predicate — there is no `WHERE odometer_km > ?` or `ORDER BY tpms_pressure_fl_psi` in any existing
or planned query — so the existing composite index continues to serve every read as a range scan.
Adding an index on a projected column would cost write time and buy nothing, which is backwards
even under a read-heavy profile.

### D3 — Convert in `snapshotFrom`, by calling the adapter, never by multiplying

`snapshotFrom` (`service.go:469-503`) already receives `*tesla.VehicleDataTesla`. It calls the
adapter's companions rather than doing arithmetic:

```go
BatteryRangeKm: data.ChargeState.BatteryRangeKm(),   // existing, types.go:86
OdometerKm:     data.VehicleState.OdometerKm(),      // existing, types.go:142
InsideTempC:    data.ClimateState.InsideTemp,        // already Celsius — no companion, no conversion
TpmsPressureFLPSI: ptr(data.VehicleState.TpmsPressureFLPSI()), // added in RM7 tier 1
```

The conversion is applied **before** `ptr()` wraps, so the NULL-vs-zero invariant is untouched: a
vehicle that reports `0.0` bar still stores a non-NULL `0.0` PSI, and a pre-migration row stays
NULL. `internal/telemetry` then deletes `milesToKm` and `barToPSI`, leaving the repo with exactly
one definition of each factor, in the module that owns the vendor units.

### D4 — Deleting the six companions is forced, not chosen

Renaming the field `Odometer` → `OdometerKm` collides with the method `OdometerKm()` — Go forbids a
field and method of the same name on one type. The same applies to `BatteryRangeKm` and the four
`TpmsPressure*PSI`. So the methods must go for the package to compile. This is worth stating
explicitly because it is the mechanism that guarantees the goal: after this change it is
*impossible* for a caller to accidentally convert on read, because there is nothing left to call.

### D5 — Precision, and why `REAL` stays `REAL`

The converted value is no longer bit-exactly reversible to the original miles/bar in the typed
column; `raw_data` remains the lossless record (RM7 Decision 10). The TPMS columns keep their
`REAL` (float4, ~7 significant digits) type: bar readings around `2.9` become PSI readings around
`42`, both far inside float4's range and precision. Widening to `DOUBLE PRECISION` would add 4
bytes per row per corner for digits the sensor does not measure.

## Risks / Trade-offs

- **[The tree does not compile after this tier]** → By construction (RM7 Decision 8), because
  `internal/battery` and `internal/gateway` call the deleted companions. Mitigation is procedural,
  not technical: this tier's verification gate is scoped to `./internal/telemetry/...` and
  `./internal/tesla/...`, tiers 3 and 4 land on the same branch, and the roadmap records this so a
  reviewer does not block the tier on a full-tree build that cannot pass yet.
- **[A rename-based migration is invisible to code that queries by string]** → `sqlc` regenerates
  from the schema and the Go compiler catches every generated-struct field change, so the
  type-checked path is safe. The residual risk is hand-written SQL or column names in strings;
  `internal/telemetry/db/query.sql` is the only SQL in the module and is updated here. Task 7.5
  greps for the old column names repo-wide as a cheap backstop.
- **[Down migration restores values, not bit patterns]** → Dividing by the same constant returns
  the original to within float round-trip tolerance, not exactly. Acceptable: `raw_data` is the
  source of truth for exactness, and the Down path exists for local rollback, not for producing an
  audited historical record.
- **[Two migrations now claim opposite unit invariants]** → `20260710000002` and `20260802000001`
  document "API-native storage, derived on read". Their header comments are historical record and
  are not edited; this migration's header states that it supersedes them, matching how
  `20260805000001` supersedes the original append-only invariant. `AGENTS.md` and
  `ai/go-conventions.md` — the docs an agent actually loads — are updated to the new rule.

## Migration Plan

1. `make migrate-up` applies `20260806000001` (renames, then one `UPDATE`). Expected runtime is
   sub-second at this table's size.
2. `make sqlc` regenerates `db/query.sql.go`; the compiler then flags every stale field reference
   inside the module.
3. Deploy is a single unit with RM7 tiers 3 and 4 — do not ship this tier alone.
4. **Rollback:** `make migrate-down` reverses both steps (values divided back, columns renamed
   back). Because no row or column is destroyed, rollback is complete apart from float tolerance.
5. **Design gate:** per `CLAUDE.md` → Design-Gates, present D1's full SQL and D2's index plan to
   the user and get explicit confirmation before Apply.

## Open Questions

None. RM7 Decisions 1–12 settle scope, backfill strategy, naming, conversion location, and the
build-breakage handling.
