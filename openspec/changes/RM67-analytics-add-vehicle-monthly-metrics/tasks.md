# Tasks — RM67-analytics-add-vehicle-monthly-metrics

Each task names its file(s), its `depends_on`, and whether it is
`parallel_ok`. A task marked `[leader-owned]` touches a file outside
`internal/analytics/` and needs an explicit grant or the leader's own hand.

## Tasks

- [ ] **TASK-1** `[leader-owned]` — Add one `rename:` entry to the existing
  `analytics` block of the root `sqlc.yaml`:
  `analytics_vehicle_monthly_metric: "VehicleMonthlyMetric"`. Must land
  before TASK-5 runs `make sqlc`, or sqlc names the generated struct with its
  own default guess instead.
  Files: `sqlc.yaml`.
  depends_on: none. parallel_ok: yes.

- [x] **TASK-2** — Create the migration
  `internal/analytics/db/migrations/20260918000002_add_vehicle_monthly_metrics.sql`:
  the full `CREATE TABLE analytics.vehicle_monthly_metrics` from `design.md`
  "Database Changes" (all 32 columns, the `UNIQUE (tesla_id, period)`
  constraint, the month-start `CHECK`), plus `COMMENT ON TABLE`/`COMMENT ON
  COLUMN` statements for every column using the meanings in `design.md`'s
  column table — write the comment text fresh from that table's own
  wording; do not copy a decision ID, tier number, or ticket ID into any SQL
  comment. Also write a real `-- +goose Down` (`DROP TABLE
  analytics.vehicle_monthly_metrics;`) — this is an ordinary migration, not
  a baseline.
  Files: `internal/analytics/db/migrations/20260918000002_add_vehicle_monthly_metrics.sql`.
  depends_on: none. parallel_ok: yes.

- [x] **TASK-3** — Add to `internal/analytics/analytics.go`: the
  `EndingBatteryDist` struct, the `VehicleMonthlyMetrics` struct, the
  `MonthlySyncer` interface, and `NewMonthlySyncer` — exactly as specified
  in `design.md` "The Go Port". `NewMonthlySyncer` takes
  `(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader)` and wires
  `newLoggingMonthlySyncer(newMonthlySyncer(pool, capacity))` — the two
  functions TASK-7 and TASK-8 provide; this task may add the forward
  reference before either exists, since Go resolves it at compile time.
  Files: `internal/analytics/analytics.go`.
  depends_on: none. parallel_ok: yes.

- [x] **TASK-4** — New file `internal/analytics/monthly_figures.go`: the
  `monthDay` type, `bucketAccumulator` (with `add`/`kmPerPct`), `isWeekend`,
  and `deriveMonthlyFigures` — exactly as specified in `design.md` "The Pure
  Derivation". No database import. Plus the offline unit test
  `internal/analytics/monthly_figures_test.go` covering T-PURE-1 through
  T-PURE-3 from `design.md`'s Test Contract.
  Files: `internal/analytics/monthly_figures.go`,
  `internal/analytics/monthly_figures_test.go`.
  depends_on: TASK-3 (needs `VehicleMonthlyMetrics`'s field names).
  parallel_ok: yes (with TASK-1, TASK-2).

- [x] **TASK-5** — Add the two queries (`VehicleMetricsForVehicleAndMonth`,
  `UpsertVehicleMonthlyMetric`) to `internal/analytics/db/query.sql` exactly
  as specified in `design.md` "The SQL", then run `make sqlc` to regenerate
  `internal/analytics/db/{models.go,query.sql.go}`.
  Files: `internal/analytics/db/query.sql`,
  `internal/analytics/db/models.go` (generated),
  `internal/analytics/db/query.sql.go` (generated).
  depends_on: TASK-1, TASK-2. parallel_ok: no.

- [x] **TASK-6** — Add mapping helpers to `internal/analytics/mapping.go`:
  - `jsonFromEndingBatteryDist(EndingBatteryDist) []byte` and
    `endingBatteryDistFromJSON([]byte) (EndingBatteryDist, error)`, using
    `encoding/json`.
  - `pgNumericFromFloat64(float64) pgtype.Numeric` and
    `float64FromPgNumeric(pgtype.Numeric) (float64, error)` — the
    `ext_ac_cost`/`ext_dc_cost`/`sc_cost` conversion pair `design.md` D3
    specifies. `pgtype.Numeric` must not appear anywhere outside this file
    (`internal/analytics/AGENTS.md`); `float64FromPgNumeric` returns an
    error rather than silently truncating a value `float64` cannot
    represent exactly.

  Both pairs mirror this file's existing per-type pg-conversion helper
  convention (one pair of functions per type, not one generic function).
  Files: `internal/analytics/mapping.go`.
  depends_on: TASK-3 (needs `EndingBatteryDist`).
  parallel_ok: yes (with TASK-4, TASK-5).

- [x] **TASK-7** — New file `internal/analytics/monthly_sync.go`:
  `monthlySyncer` struct (`q *analyticsdb.Queries`, `capacity
  charging.MonthlyCapacityReader`), `newMonthlySyncer` constructor, and
  `SyncMonth` implementing `MonthlySyncer` — calls
  `VehicleMetricsForVehicleAndMonth`, runs `deriveMonthlyFigures` over the
  mapped rows, calls `capacity.CapacityForMonth`, sets `CapacityKWh`/
  `CapacityMeasured` only when the port reports a real found+non-nil value,
  sets `Currency = "COP"` and every `Ext*`/`SC*` field to its zero value
  (D8), converts the three cost fields through `pgNumericFromFloat64`
  (TASK-6) when binding `UpsertVehicleMonthlyMetric`'s params, then calls
  it and maps the returned row back into `VehicleMonthlyMetrics` — including
  `Period`, `CreatedAt`, `UpdatedAt` from the row `RETURNING` gave back
  (never recomputed), and `ExtACCost`/`ExtDCCost`/`SCCost` through
  `float64FromPgNumeric` (TASK-6), propagating its error like any other
  mapping failure.
  Files: `internal/analytics/monthly_sync.go`.
  depends_on: TASK-4, TASK-5, TASK-6. parallel_ok: no.

- [x] **TASK-8** — Add `loggingMonthlySyncer` to
  `internal/analytics/query_log.go`, wrapping `MonthlySyncer` and logging
  `SyncMonth` (teslaID, the requested period, and the stored row's
  `AllDayCount`/`CapacityMeasured` — mirroring this file's existing
  decorators' level of detail, e.g. `loggingMonthlyCapacityReader`-style
  from `internal/charging`).
  Files: `internal/analytics/query_log.go`.
  depends_on: TASK-3 (needs the `MonthlySyncer` interface signature).
  parallel_ok: yes (with TASK-4, TASK-5, TASK-6, TASK-7).

- [ ] **TASK-9** — New file
  `internal/analytics/db_monthly_sync_integration_test.go`
  (package `analytics`, `TEST_DATABASE_URL`-gated): implement T1 through T6
  from `design.md`'s Test Contract. Uses this module's existing
  `testdb.ProvisionDirs` harness (must include both `internal/analytics/db/migrations`
  and `internal/charging/db/migrations`, to seed `monthly_effective_capacity`
  directly by `INSERT`). Assert only against `VehicleMonthlyMetrics`'s own
  fields, never `pgtype`.
  Files: `internal/analytics/db_monthly_sync_integration_test.go`.
  depends_on: TASK-5, TASK-7. parallel_ok: no.

- [x] **TASK-10** — Update `internal/analytics/AGENTS.md`:
  - "Allowed / forbidden imports" — add `charging.MonthlyCapacityReader` to
    the list of `internal/charging` ports this module may import.
  - "Public interface (the port)" — add a row (or a new small table) for
    `MonthlySyncer`/`SyncMonth`, following the existing table's style.
  - "Data ownership" — add one sentence naming the new
    `vehicle_monthly_metrics` table alongside the three existing ones.
  Files: `internal/analytics/AGENTS.md`.
  depends_on: TASK-3, TASK-7 (needs the final shape). parallel_ok: yes.

- [ ] **TASK-11** `[leader-owned]` — Check `kkpa/context/` for guides this
  change invalidates or should extend:
  - `kkpa/context/workflows/vehicle-monthly-metrics.md` currently documents
    only tier 1 (the `charging` capacity read). Extend it to also cover this
    tier's table and `MonthlySyncer` port, or split it if the maintainers
    prefer two guides — the leader's call, not scoped here.
  - `kkpa/context/entities/vehicle-metrics/guide.md` — check whether its
    description of `internal/analytics`'s tables/ports needs a mention of
    the new sibling table, so a future reader is not left believing
    `vehicle_metrics` is still the module's only precomputed table.
  Files: under `kkpa/context/` (outside `internal/analytics/`).
  depends_on: TASK-3, TASK-7. parallel_ok: yes.

- [ ] **TASK-12** `[leader-owned]` — Check the root `README.md`'s
  "Project Structure" tree and "Architecture" table for whether either lists
  `internal/analytics`'s ports or tables at a level of detail this change
  would make stale. Based on the existing precedent (these sections list
  modules and paths, not individual ports), this is expected to need no
  edit — but the check itself, and its outcome, must be recorded, not
  assumed (`CLAUDE.md`'s own "docs track structural change" rule).
  Files: `README.md` (outside `internal/analytics/`).
  depends_on: none. parallel_ok: yes.

## Not in this change — do not do these

- **Filling `ext_ac_*`, `ext_dc_*`, `sc_*` with real aggregates.** That is
  `RM67-analytics-add-monthly-charging-aggregates` (tier 3). This change
  writes their documented zero value only (D8).
- **Wiring the nightly trigger.** That is `RM67-app-add-monthly-metrics-step`
  (tier 4), in `internal/app`. Nothing in `internal/app` changes here.
- **Adding a `Reader` method over `vehicle_monthly_metrics`.** No consumer
  exists yet (MAG-87 is out of this roadmap's scope). Do not add a read port
  "to complete the pattern" — `design.md`'s Index Plan explicitly declines a
  speculative index for exactly this reason.
- **Changing `charging.MonthlyCapacityReader`, its query, or its migration.**
  Tier 1 is archived and immutable. This change is a caller of that port
  only.
- **Adding `{bucket}_efficiency_day_count` or `observed_day_count` columns.**
  Considered and rejected in `design.md` D7. Do not add them "for
  completeness" — if a real consumer later needs the finer distinction, that
  is a small, additive migration at that point.
- **Resolving the multi-currency aggregation question `design.md` D3 flags.**
  That is tier 3's problem, when it writes the real `ext_*`/`sc_*`
  aggregation logic. This change only decides the column shape (one shared
  `currency` column).
