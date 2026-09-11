# Vehicle monthly metrics — measured pack capacity — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle monthly metrics`, `monthly effective capacity`, `effective pack capacity`,
  `measured pack capacity`, `monthly capacity`, `pack capacity`, `capacity backfill`
- **Internal name:** table `charging.monthly_effective_capacity`; job
  `charging.MonthlyCapacityCalculator.Calculate`; read side `packCapacityKWh`
  (`internal/charging/capacity.go`)

**Today this workflow holds exactly one metric: the measured pack capacity.** The name is plural
on purpose. More monthly per-vehicle metrics were expected (full-charge count, consumption per
100 km, energy per date), but they are deferred until a second one really exists. See
§"Adding a second monthly metric".

The two numbers stored next to the capacity:

- `candidate_count` — every valid record found that month, counted **before** the delta gate.
- `sample_count` — the subset that survived the gate, the exact slice the median was taken over.

They differ on purpose. Without `candidate_count`, "twenty small top-ups, none big enough" and
"no charging at all" would both read as zero.

## Component map

### Owning module — `internal/charging`

| File | Role |
|---|---|
| `internal/charging/db/migrations/20260909000002_add_monthly_effective_capacity.sql` | Creates `charging.monthly_effective_capacity`. Read its header comment before changing the table — it holds the full rationale. |
| `internal/charging/db/query.sql` | `ListValidManualEntryCapacitiesForPeriod`, `ListValidSessionCapacitiesForPeriod` (the two inputs), `UpsertMonthlyEffectiveCapacity` (the write), `LatestMeasuredCapacity` (the read). Edit here, then `make sqlc`. |
| `internal/charging/monthly_capacity.go` | The job. `estimateEffectiveCapacity` is the pure gate-then-median function; the constants `minSamples` and `minDeltaPct` live here. **Change the estimate here.** |
| `internal/charging/capacity.go` | The read side: `packCapacityKWh`, the `packCapacityLookup` seam, and `defaultPackCapacityKWh = 62.0`. |
| `internal/charging/charging.go` | The public port: `MonthlyCapacityCalculator`, `MonthlyCapacityReport`, `NewMonthlyCapacityCalculator(pool)`. |
| `internal/charging/service.go`, `internal/charging/session_verifier.go` | The two callers of `packCapacityKWh`. Both divide energy by capacity; the seam is never duplicated. |

### Callers — who triggers the job

| File | Role |
|---|---|
| `internal/app/processor.go` | Step 4 of the nightly cycle. `monthlyCapacityPeriod` is the pure day gate; `runMonthlyCapacityStep` reads the clock; `callMonthlyCapacityCalculator` calls the port and logs. |
| `internal/app/app.go` | Holds the port on the processor; documents the four-step shape. |
| `cmd/poller/main.go` | Builds the calculator and passes it in. The only wiring in the running server path. |
| `cmd/monthly-capacity/` | The manual tool: `main.go` (flags, wiring, one report line), `period.go` (the pure period/flag logic), `README.md`. Calls the port directly, bypassing `internal/app`. |
| `Makefile` → `cmd-monthly-capacity` | Builds and runs the tool. `PERIOD=2026-08 TESLA_ID=123`, both optional. |
| `internal/config/config.go` → `LoadDatabase()` | DSN-only config for the tool. It never calls the Fleet API, so it must not need a Tesla credential. |

`cmd/web` never wires the calculator. A web request must never start a month-wide job.

### Source data (read-only inputs — same module)

| Table | Which rows count |
|---|---|
| `charging.manual_charge_entries` | `energy_source = 'USER'` only, with a non-NULL `inferred_capacity_kwh_calc`. |
| `charging.supercharger_sessions` | `status = 'DONE'` only, with a non-NULL `inferred_capacity_kwh_calc` and a non-NULL `tesla_id`. |

## How maintenance works

The job runs for **one period** (a month) and optionally **one vehicle**. In order:

1. Read the month's valid manual entries and Supercharger sessions. Each row yields an implied
   capacity plus the battery-percentage change it was divided by.
2. Group the rows by `tesla_id` in Go, not in SQL. One car is one key, whoever logged the charge.
3. Per vehicle: count the candidates, drop rows whose battery change is under `minDeltaPct`,
   count what survived, and take the median of the survivors.
4. Under `minSamples` survivors, store no capacity — but still store both counts.
5. Upsert one row per vehicle on `(tesla_id, effective_period)`. A re-run of the same month
   overwrites its own row; it never duplicates and never errors.

The read side is separate and much simpler: `packCapacityKWh` takes the newest row for the car
that actually has a capacity, and falls back to `defaultPackCapacityKWh` when there is none.

**To change the estimate**, edit `estimateEffectiveCapacity` in
`internal/charging/monthly_capacity.go`. The gate (which rows count as evidence) and the method
(how survivors become one number) are deliberately separate functions. A new method is a new
sibling function, not an edit to the median.

**To re-run or backfill a month:**

```
make cmd-monthly-capacity PERIOD=2026-08
make cmd-monthly-capacity PERIOD=2026-08 TESLA_ID=123
```

With no arguments it does the previous month, every vehicle. It needs `DATABASE_URL`.

## Conventions & gotchas

- **A stored number always means measured.** Under `minSamples` valid records, the capacity is
  left NULL and only the counts are stored. NULL means "not enough evidence", never "we guessed".
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Only non-derived records count as evidence.** A manual entry counts only when the person gave
  the energy. A Supercharger session counts only when it is complete and both percentages came
  from the car. A row whose numbers were themselves derived from the 62.0 fallback would feed that
  fallback back into the answer.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Small battery changes are dropped, not corrected.** A record under `minDeltaPct` counts toward
  neither the capacity nor `sample_count`. Capacity error grows as the change shrinks, so a small
  charge is noise.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Median, never average.** Taken after the unreliable rows are dropped, so one odd record cannot
  dominate the month — and no outlier maths is needed.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **The vehicle is the key, not the account.** This is the only table in charging's schema with no
  `account_id`, on purpose: it describes a battery pack, not user data. Rows logged by different
  accounts for the same car pool into one measurement. A Supercharger session with no `tesla_id`
  is excluded from every month.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Computing a month never rewrites history.** No charge record's stored energy or capacity
  changes. A monthly measurement is a new, separate fact.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Reads skip unmeasured months.** A newer month with no capacity does not hide an older measured
  one. Pack capacity moves slowly, so the newest measurement is a good answer for a thin month.
  _Source: spec monthly-effective-capacity — Requirement: The Newest Measured Capacity Is Used, Skipping Unmeasured Months._
- **With no measured row at all, everything behaves exactly as before.** `packCapacityKWh` returns
  `defaultPackCapacityKWh`. That constant is named once and read by both its call sites — never
  re-typed as a literal.
  _Source: spec monthly-effective-capacity — Requirement: The Newest Measured Capacity Is Used, Skipping Unmeasured Months._
- **`minSamples` and `minDeltaPct` are Go constants, not database values.** Tuning them is a code
  change plus a re-run, never a migration.
- **`effective_period` is always the first day of its month**, enforced by a CHECK constraint, not
  by caller discipline.
- **No extra index exists, and none is needed.** The `UNIQUE (tesla_id, effective_period)` btree
  serves both the read (equality on `tesla_id`, then a backwards range scan) and the upsert's
  conflict target.
- **The day gate uses `clock.Zone()`, not the poller's `POLLER_TIMEZONE`.** These are two knobs:
  one decides when the nightly cycle fires, the other decides whether today is the 1st. If they
  disagree, step 4 logs one warning and still uses the platform zone.
  _Source: `internal/app/processor.go` `runMonthlyCapacityStep`._
- **The nightly step always passes no vehicle**, so one call measures every car. Scoping to one
  car is the manual tool's job. A per-vehicle loop in the cycle would re-read the same rows once
  per car.
- **No cross-module port exists for this table, and none should.** Every input row already belongs
  to `charging`, so the derivation and the table belong there too. An earlier design put the job in
  `analytics` and needed an inverted port to escape an import cycle; that design was withdrawn. A
  cycle here means the work is in the wrong module.

## Adding a second monthly metric

The plural name is a placeholder, not a promise of a shared table. When a second metric arrives,
decide then — do not pre-build for it:

- If it also derives from charging's own rows, it is another column on this table, or a sibling
  table in the same schema.
- If it derives from another module's rows, it belongs to **that** module, by the same rule that
  put this one in `charging`. A wider cross-module `monthly_metrics` table owned by `analytics`
  was considered and deferred for exactly one reason: one metric does not justify it.

`internal/analytics/capacity.go` holds a **different**, model-coarse capacity table keyed on
`car_type`. This workflow never touched it. Before extending that one, weigh reading the measured
value instead — `analytics` already imports `charging`, so there is no cycle. See
`openspec/roadmaps/backlog.md` item 7.

## Related KB

- Workflows: `workflows/manual-charge-crud.md` (one of the two input tables),
  `workflows/supercharger-stats-read.md` (the other one)
- Architecture: `architecture/nightly-cycle.md` (step 4 — when and how the job is triggered)
- Entities: `entities/vehicle-metrics/guide.md` (the unrelated `analytics` metrics table)
