# Tasks — RM52-analytics-add-monthly-metrics

All work is inside `internal/analytics` (plus one root-file edit, `sqlc.yaml`, that is this
module's own generator config). Grouped into sub-tasks with explicit dependencies so separate
agents can implement them — in parallel where they touch disjoint files.

Wave rule (`ai/go-conventions.md` §Testing "authoring order"): the pure estimator's tests
(Sub-task A) need no migration and no generated code — they can be written and pass first.
The calculator's fake-backed tests (Sub-task C) need the domain types from Sub-task B's
`analytics.go` edit but no generated `analyticsdb` code. The DB-integration test (Sub-task E)
needs the migration AND `make sqlc` to have run — it is the LAST wave.

## Sub-task A — pure estimator (no dependencies, can start immediately)

Implements design D1, D2, D3.

- [ ] A.1 Create `internal/analytics/monthly_capacity.go`: `minSamples`/`minDeltaPct`
      constants, `capacitySample` struct, `estimateEffectiveCapacityKWh` function. Exact
      body: `design.md` "Pure estimator (RD3)".
- [ ] A.2 Create `internal/analytics/monthly_capacity_test.go`: Fixtures 1–6 from
      `design.md`'s Test Contract, offline, no `DATABASE_URL` needed.

## Sub-task B — schema and sqlc

Depends on: nothing (column names and shape are fixed by `design.md`, independent of A/C's
Go code). Coordinate with D only at `make sqlc` time.

- [ ] B.1 Write migration
      `internal/analytics/db/migrations/20260909000002_add_vehicle_monthly_metrics.sql`:
      the RD11 `CREATE TABLE` verbatim, the four `COMMENT ON` statements, and a `Down` block
      dropping the table. Exact SQL: `design.md` "Schema (RD11)".
- [ ] B.2 Add `UpsertVehicleMonthlyMetric` and `LatestMeasuredPackCapacityKWh` to
      `internal/analytics/db/query.sql`. Exact SQL: `design.md` "sqlc".
- [ ] B.3 Add the `analytics_vehicle_monthly_metric: "VehicleMonthlyMetric"` rename entry to
      `sqlc.yaml`'s existing `analytics` gen block, alongside the module's three existing
      entries.
- [ ] B.4 Run `make sqlc`. Confirm `internal/analytics/db/models.go` gains a
      `VehicleMonthlyMetric` struct (not `AnalyticsVehicleMonthlyMetric`) and
      `query.sql.go` gains `UpsertVehicleMonthlyMetric`/`LatestMeasuredPackCapacityKWh`
      with their generated param/row types.

## Sub-task C — ports, domain types, and the calculator

Depends on: nothing for the interface/type declarations (Sub-task C.1–C.2 can start
immediately, in parallel with A and B). C.3–C.5 (the calculator body and its tests) need
C.1–C.2's types to exist, but not B's generated code — the calculator's write call is the one
place it needs `analyticsdb`, isolated to `Calculate`'s own upsert step.

- [ ] C.1 In `internal/analytics/analytics.go`: fix the package doc comment (it still says
      "owns no database and no store" — false since `RM29-analytics-add-vehicle-metrics`).
      Add the new `MonthlyMetricsCalculator` interface AND the new `PackCapacityReader`
      interface (`PackCapacityKWh(ctx, teslaID, at) (float64, bool, error)`), the
      `MonthlyMetricsReport`/`VehicleMonthlyMetric` domain types, and the two
      forward-declared constructors `NewMonthlyMetricsCalculator` and
      `NewPackCapacityReader`. Exact shapes: `design.md` D7, D8. **Do NOT add any method to
      the existing `Reader` interface — it stays byte-for-byte unchanged** (D7: `Reader`
      already has fakes in `internal/app` and `internal/gateway` that this tier may not
      touch and may not break).
- [ ] C.2 In `internal/analytics/monthly_metrics.go` (new file): the `vehicleLookupAll`
      narrow interface (`AllRegisteredVehicles` only) and the concrete
      `monthlyMetricsCalculator` type's struct fields.
- [ ] C.3 Implement `Calculate` in `monthly_metrics.go`: pool by `tesla_id` (D5), apply the
      `teslaID` filter (D6), read each source via `charging.Reader.ListEntriesByVehicleBetween`
      / `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween` with
      `from = period`, `to = period.AddDate(0, 1, -1)` (D10), filter to valid records (D4),
      call `estimateEffectiveCapacityKWh`, then call `UpsertVehicleMonthlyMetric`. Validate
      `period.Day() != 1` up front and return an error before any read (D9).
- [ ] C.4 Create `internal/analytics/capacity_reader.go` (new file, separate from
      `reader.go`): the `monthlyMetricsStore` narrow interface
      (`LatestMeasuredPackCapacityKWh`), the concrete `packCapacityReader` type, and
      `PackCapacityKWh`'s implementation, mapping `pgx.ErrNoRows` to `(0, false, nil)` per
      `design.md`'s sqlc query comment. **Do not edit `reader.go`, the `reader` struct, or
      the `vehicleMetricsStore` interface for this** (D7).
- [ ] C.5 Unit tests in `internal/analytics/monthly_metrics_test.go`: Fixtures A–E from
      `design.md`'s Test Contract, against hand-written fakes of `charging.Reader`,
      `charging.SuperchargerSessionAnalyticsReader`, and `vehicleLookupAll` — mirrors
      `reader_test.go`'s existing fake-port pattern. Offline, no `DATABASE_URL` needed.

## Sub-task D — DB-integration test (LAST wave)

Depends on: B (migration + generated code must exist) and C (the calculator and
`PackCapacityKWh` must be implemented). Cannot compile before both land.

- [ ] D.1 Create `internal/analytics/db_monthly_metrics_integration_test.go`, provisioned
      with `testdb.ProvisionDirs` (this module's fixtures span `account` and `charging`
      tables, same reason `db_integration_test.go` already needs it over `testdb.Provision`).
      Implement Fixtures F–I from `design.md`'s Test Contract:
      - F: full round trip (seed via `account.NewService(pool).SeedVehicles` and
        `charging.NewWriter(pool).Create`, call `Calculate`, assert the stored row, then
        call `PackCapacityKWh` and assert the read matches).
      - G: RD4 fallback to an earlier measured month.
      - H: no row at all yet ⇒ `(0, false, nil)`.
      - I: a Supercharger `DONE` session seeded via a **direct `INSERT INTO
        charging.supercharger_sessions`** (mirrors `db_integration_test.go`'s existing
        precedent — `SessionWriter.MirrorSessions` carries no battery-percentage fields and
        `SessionStatus` is never settable through any port) contributes through Postgres's
        own generated `inferred_capacity_kwh_calc` column.
      This test self-skips when no Postgres is reachable and no Docker daemon can provision
      one, per this module's existing DB-backed test convention.

## Docs

Depends on: A–D being decided (the exact method/type/column names), so do this last.

- [ ] E.1 Update `internal/analytics/AGENTS.md`: add `vehicle_monthly_metrics` to "Data
      ownership" (table shape, RD5's no-`account_id` rationale, RD4's NULL meaning); add
      `PackCapacityKWh` and `MonthlyMetricsCalculator`/`Calculate` to "Public interface (the
      port)"; note the package-comment fix from C.1.
- [ ] E.2 Grep `kkpa/context/` for `analytics` and confirm no existing guide's consumer map,
      file map, or column list is invalidated by this tier's additions (the new
      `vehicle-monthly-metrics` KB guide itself is tier 4's job, per the roadmap's tier 4
      item 5 — do not create it here). A hit under `openspec/changes/archive/` is not this
      task's to fix.

## Verification (do not run the suite — see Test-Execution-Policy)

- [ ] F.1 `go build ./...`
- [ ] F.2 `go vet ./...`
- [ ] F.3 `gofmt -l internal/analytics`
- [ ] F.4 `make migration-guard` (confirms `20260909000002` collides with no other module's
      migration version)
- [ ] F.5 `make boundary-guard` (confirms no `internal/gateway` file was touched — this
      change touches none, so the guard should be a pure no-op pass)
- [ ] F.6 `grep -rn "analytics.Reader" internal/app internal/gateway` — must show the SAME
      three fakes as before this change (`internal/app/processor_test.go:275`,
      `internal/gateway/handlers/handlers_test.go:219` and `:1162`), unchanged, still
      compiling against the untouched five-method `Reader` interface (D7). A build break in
      either module means `Reader` was edited by mistake — revert that edit before
      proceeding.
- [ ] F.7 Report the exact suite commands to the owner:
      `go test ./internal/analytics/...` (offline tests, including Sub-tasks A/C's new
      assertions, must pass with `DATABASE_URL` unset) and `make test-with-db` for
      Sub-task D's new integration test, which needs a database and self-skips without one.
