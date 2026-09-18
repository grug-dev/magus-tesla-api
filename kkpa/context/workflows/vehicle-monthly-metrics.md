# Vehicle monthly metrics — measured pack capacity — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle monthly metrics`, `monthly effective capacity`, `effective pack capacity`,
  `measured pack capacity`, `monthly capacity`, `pack capacity`, `capacity backfill`
- **Internal name:** table `charging.monthly_effective_capacity`; job
  `charging.MonthlyCapacityCalculator.Calculate`; **two** read sides —
  `packCapacityKWh` (`internal/charging/capacity.go`, module-internal) and the public
  `charging.MonthlyCapacityReader` port (`internal/charging/monthly_capacity_reader.go`)

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
| `internal/charging/db/query.sql` | `ListValidManualEntryCapacitiesForPeriod`, `ListValidSessionCapacitiesForPeriod` (the two inputs), `UpsertMonthlyEffectiveCapacity` (the write), and **two** reads: `LatestMeasuredCapacity` ("what capacity should today's math use?" — newest row that has one, skipping NULL months) and `EffectiveCapacityForPeriod` ("what did this car measure in THIS month?" — that exact month, thin or absent included). Edit here, then `make sqlc`. |
| `internal/charging/monthly_capacity.go` | The job. `estimateEffectiveCapacity` is the pure gate-then-median function; the constants `minSamples` and `minDeltaPct` live here. **Change the estimate here.** |
| `internal/charging/capacity.go` | The read side: `packCapacityKWh`, the `packCapacityLookup` seam, and `defaultPackCapacityKWh = 62.0`. |
| `internal/charging/charging.go` | The public ports: `MonthlyCapacityCalculator`, `MonthlyCapacityReport`, `NewMonthlyCapacityCalculator(pool)`, plus `MonthlyCapacityReader` and `NewMonthlyCapacityReader(pool)`. |
| `internal/charging/monthly_capacity_reader.go` | The `MonthlyCapacityReader` implementation. One method, `CapacityForMonth(ctx, teslaID, month)`. It returns three states, not two: no row, a row with no measured capacity, and a row with one. |
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
| `charging.manual_charge_entries` | `energy_source = 'USER'` **and** `start_battery_source = 'USER'`, with a non-NULL `inferred_capacity_kwh_calc`. Two independent provenances; a row failing either one is not evidence. |
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

The read side is separate and much simpler. There are two reads, and they answer different
questions:

- `packCapacityKWh` (module-internal) takes the newest row for the car that actually has a
  capacity, and falls back to `defaultPackCapacityKWh` when there is none. It answers "what
  number should today's maths use?".
- `MonthlyCapacityReader.CapacityForMonth` (public port) returns the row for one exact month, and
  never looks at another month. It answers "what did this car measure in March?". It reports
  three states: no row at all, a row whose capacity is NULL, and a row with a measured capacity.
  A caller copying the number into its own monthly table needs those apart — an absent month and
  a thin month are not the same fact.

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
  the energy **and** typed the starting percentage. A Supercharger session counts only when it is
  complete and both percentages came from the car. A row whose numbers were themselves derived
  from the 62.0 fallback would feed that fallback back into the answer.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **A manual entry must pass BOTH provenance checks, not one.** The energy must have been typed by
  the person, and so must the starting percentage. The two are recorded and computed separately,
  so a row can fail either on its own.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Why the starting percentage matters here at all.** A derived starting percentage is computed by
  dividing the energy by a pack capacity. The generated `inferred_capacity_kwh_calc` then comes
  back equal to that same capacity, by algebra. Counting such a row would feed the measurement back
  into itself, exactly like a derived energy does.
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
- **The table is still never read directly by another module — only through a port.**
  `MonthlyCapacityReader` exists for a caller outside `charging` (`analytics`, which copies the
  month's capacity into its own monthly row). That caller gets a Go interface, never a query and
  never the table. Add a read here, not a join there.
- **The derivation stays in `charging`, and that is not negotiable.** Every input row already
  belongs to this module, so the job and the table belong here too. An earlier design put the job
  in `analytics` and needed an inverted port to escape an import cycle; that design was withdrawn.
  A cycle here means the work is in the wrong module, not that the wiring needs fixing.

- **A bad month is rejected before the tool touches a database.** The month value is parsed and
  validated first. A month number that does not exist, or a value in the wrong shape, exits with a
  failure status and attempts no measurement. Keep this order if the flag handling is ever
  rewritten — validating after connecting turns a typo into a wasted connection.
  _Source: spec monthly-capacity-cli — Requirement: An Invalid Month Is Rejected Before Any Work Starts._
- **The tool reports four numbers, or it reports a failure — never both.** A successful run names
  the month it measured, how many vehicles it considered, how many got a measured capacity, and how
  many were left thin. Any failure — bad input, a database problem, a measurement error — reports
  the failure, exits non-zero, and never prints a success summary.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Reports What It Measured And Exits Accordingly._
- **The tool needs a database connection and nothing else.** It never calls the Tesla Fleet API, so
  it must never require a Tesla credential to start. This is why it uses `config.LoadDatabase()`
  and not the full `config.Load()`. Without a database connection it fails to start and says so.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Needs Only A Database Connection, Never A Tesla Credential._
- **The tool is local only. It is not in the production image.** It runs from a checkout against a
  database the operator can reach directly. `deploy/docker/Dockerfile` builds only `web`, `poller`
  and `migrate`. Do not add it: it is an operator tool for re-running a month, not a service.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Is Not Part Of The Deployed Production Image._

- **`analytics` imports no `internal/account` symbol at all** — production and test files alike.
  Its reads are scoped by vehicle identifier. Do not add an `account` import to resolve a vehicle
  attribute; that dependency was removed on purpose.
- **`analytics.NewReader` takes the pool and nothing else.** Every surviving `Reader` method reads
  `vehicle_metrics` alone. The sibling ports belong to `NewRecalculator`, which writes those rows.
- **The dashboard efficiency tile is not an `analytics` metric.** It reads
  `vehicle_metrics.km_per_pct_calc` through `LatestMetricsForVehicles`. The Wh/km derivation that
  once shared the word "efficiency" is deleted; searching for it finds nothing.

## Adding a second monthly metric

The plural name is a placeholder, not a promise of a shared table. When a second metric arrives,
decide then — do not pre-build for it:

- If it also derives from charging's own rows, it is another column on this table, or a sibling
  table in the same schema.
- If it derives from another module's rows, it belongs to **that** module, by the same rule that
  put this one in `charging`. A wider cross-module `monthly_metrics` table owned by `analytics`
  was considered and deferred for exactly one reason: one metric does not justify it.

`internal/charging` now holds the platform's **only** pack-capacity definition. `internal/analytics`
used to hold a second one — a model-coarse table keyed on `car_type`, in
`internal/analytics/capacity.go`. That file is gone. It was deleted with the unused efficiency
branch that was its only consumer, so no displayed number changed. A future model-aware capacity
starts from `charging`'s definition, not from a second table.

One limit stays open: `charging`'s `62.0` fallback is model-blind for a vehicle with no measured
value yet. `internal/charging` may not import `internal/account`, so it cannot resolve a `car_type`
by itself. That is a new ticket, not a leftover.

## Related KB

- Workflows: `workflows/manual-charge-crud.md` (one of the two input tables),
  `workflows/supercharger-stats-read.md` (the other one)
- Architecture: `architecture/nightly-cycle.md` (step 4 — when and how the job is triggered)
- Entities: `entities/vehicle-metrics/guide.md` (the unrelated `analytics` metrics table)
