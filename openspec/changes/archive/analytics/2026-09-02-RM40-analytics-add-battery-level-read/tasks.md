# Tasks — RM40-analytics-add-battery-level-read

Ownership legend: **[module: analytics worker]** — inside `internal/analytics/`, the
sandboxed worker's own territory (this tier touches no other module). See design.md
D1–D6/D-index for the rationale behind each task.

**Hard ordering constraints:**
- The `query.sql` edit (1.2) must precede `sqlc generate` (2.1) — sqlc reads the query
  file directly to generate `VehicleMetricsBatteryByVehicleBetween`'s Go types.
- `sqlc generate` (2.1) must precede the `reader.go` work (3.1) — the new
  `vehicleMetricsStore` method signature and `reader.BatteryLevelByDay`'s
  implementation both reference the generated `analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams`/
  `...Row` types, which do not exist until 2.1 runs.
- The `analytics.go` addition (1.1) and the `query.sql` edit (1.2) touch disjoint
  files and have no dependency on each other — parallelizable.
- Documentation (4.1) describes the POST-change state, so it runs after 1.1 and 3.1
  land (content dependency, not a compile dependency).
- Verification (5.1) runs after every other task.

## Wave 1 — port declaration + query (module: analytics worker)

- [x] **1.1** `internal/analytics/analytics.go` — add the `DayBattery` struct
  (`Date time.Time`, `BatteryLevelPct int`, `BatteryRangeKm float64`) immediately
  after `DayDistance`, matching the field types and doc-comment density of
  `DayConsumption`/`DayDistance` (design.md D2). Add `BatteryLevelByDay(ctx
  context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time)
  ([]DayBattery, error)` to the `Reader` interface, with a doc comment matching the
  depth of `ConsumedByDay`/`OdometerDeltaByDay`'s own — covering: the precomputed/
  not-recomputed-on-read contract; the sparse, non-nil-empty-slice contract; that
  `Date` is a FINAL bucket key never re-projected through the gateway's
  `effectiveDayUTC` (design.md D4); and the one explicit contrast with its two
  siblings — **no `IS NOT NULL` filter**, because `battery_level_pct`/
  `battery_range_km` are `NOT NULL` raw observations with no predecessor requirement
  (design.md D3).
  `depends_on`: — · `parallel_ok`: with 1.2

- [x] **1.2** `internal/analytics/db/query.sql` — add
  `VehicleMetricsBatteryByVehicleBetween` (a `:many` query), mirroring
  `VehicleMetricsConsumedByVehicleBetween`/`VehicleMetricsOdometerByVehicleBetween`
  exactly in shape and comment style: `SELECT metric_date, battery_level_pct,
  battery_range_km FROM vehicle_metrics WHERE account_id = @account_id AND tesla_id =
  @tesla_id AND metric_date BETWEEN @start_date AND @end_date ORDER BY metric_date;`
  — deliberately with NO trailing `IS NOT NULL` predicate (design.md D3 — the one
  place this query departs from the pattern it otherwise mirrors; the doc comment
  must state this contrast explicitly, not just omit the clause silently). Doc
  comment cites design.md's Index Plan #1 (served by
  `vehicle_metrics_account_tesla_date_unique`, no new index).
  `depends_on`: — · `parallel_ok`: with 1.1

## Wave 2 — codegen (module: analytics worker)

- [x] **2.1** Run `make sqlc` (or `sqlc generate`; Claude may run — allowed codegen
  command, `CLAUDE.md` "Builds & local checks") to regenerate
  `internal/analytics/db/*.go` from 1.2's new query against the existing (unchanged)
  schema. Confirm `VehicleMetricsBatteryByVehicleBetweenParams` and
  `VehicleMetricsBatteryByVehicleBetweenRow` (with `MetricDate`, `BatteryLevelPct`,
  `BatteryRangeKm` fields) were generated.
  `depends_on`: 1.2 · `parallel_ok`: no

## Wave 3 — reader implementation (module: analytics worker)

- [x] **3.1** `internal/analytics/reader.go` — `vehicleMetricsStore` interface gains
  `VehicleMetricsBatteryByVehicleBetween(ctx context.Context, arg
  analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams)
  ([]analyticsdb.VehicleMetricsBatteryByVehicleBetweenRow, error)` (`*analyticsdb.
  Queries` satisfies it automatically, no adapter needed, mirroring this interface's
  two existing methods). Implement `BatteryLevelByDay` on the concrete `reader`:
  calls the store method with `dateFrom(start)`/`dateFrom(end)` (reusing the existing
  `mapping.go` helpers, no new conversion helper needed), maps each row to a
  `DayBattery` via `dateFromPg(row.MetricDate)` — no derivation logic, pure
  row-to-domain mapping, mirroring `ConsumedByDay`/`OdometerDeltaByDay`'s own "no
  derivation logic here" convention. Returns `make([]DayBattery, 0, len(rows))` on
  the happy path, never a bare `nil` (the non-nil-empty-slice contract, design.md D2).
  `depends_on`: 1.1, 2.1 · `parallel_ok`: no

## Wave 4 — documentation (module: analytics worker)

- [x] **4.1** `internal/analytics/AGENTS.md` — under "Public interface (the port)",
  add the `BatteryLevelByDay`/`DayBattery` bullet immediately after the existing
  `OdometerDeltaByDay`/`DayDistance` bullets, mirroring their exact style: the
  sparse/precomputed contract, the `Date`-is-a-final-bucket-key rule (D4), and the
  one-line explicit note that this method has no `IS NOT NULL` filter unlike its two
  siblings (D3), with the reason stated in one clause. Cross-reference this change's
  design.md for the full rationale rather than restating it, mirroring this file's
  existing citation convention.
  `depends_on`: 1.1, 3.1 · `parallel_ok`: no

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [x] **5.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l`, `make
  build`, `make vet`, `make bins`. Per design.md "Verification signals",
  `make ui-guard`/`make i18n-guard`/`make money-guard`/`make tz-guard`/
  `make boundary-guard`/`make migration-guard` are all no-ops for this tier (no
  gateway code, no user-facing string, no monetary or raw-time-zone code, no
  migration file, and no gateway `telemetry` import touched by a change confined to
  `internal/analytics/`) — running them is optional but harmless. Per the
  Test-Execution-Policy, never run `go test ./...`, `make test`, `make test-with-db`,
  or `make check`.
  `depends_on`: 1.1–4.1 (every prior task) · `parallel_ok`: no

- [x] **5.2** Hand off to the owner the exact command to run and report: `go test
  ./internal/analytics/...` (covers the full existing offline + `DATABASE_URL`-gated
  suite; this tier adds no new test file, so a pass here confirms only that nothing
  existing broke). Until the owner reports a pass, this tier's implementation status
  is **awaiting-user-verification**, never "done" (`CLAUDE.md` "Builds & local
  checks").
  `depends_on`: 5.1 · `parallel_ok`: no
