# Tasks — RM50-analytics-add-tire-pressure-variance

All work is inside `internal/analytics`. Grouped into two sub-tasks plus a docs task.
Sub-task A (maths) and B (schema + wiring) touch different files and can run in
parallel; both must land before the tests in A can pass against real generated code.

## Sub-task A — delta maths (pure functions)

Depends on: nothing. Implements design D1, D2.

- [x] A.1 Add four fields to `consumptionCalc` in `internal/analytics/consumption.go`:
      `TpmsPressureFLPSICalc`, `TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`,
      `TpmsPressureRRPSICalc`, all `*float64`.
- [x] A.2 Add a `tpmsDeltaPSI(prev, cur *float64) *float64` helper in
      `internal/analytics/consumption.go`: returns `nil` if either argument is `nil`,
      else `cur - prev`. Call it four times inside `deriveConsumption`, once per wheel,
      only in the branch that already runs when `prev != nil`.
- [x] A.3 Add the same four fields to `vehicleMetricRow` in
      `internal/analytics/consumed.go`. Populate them from `calc.TpmsPressureFLPSICalc`
      etc. in the "has a predecessor" branch of `deriveVehicleMetrics`, next to the
      existing `DistanceTraveledKmCalc: calc.DistanceTraveledKmCalc,` line. Leave them
      unset (nil) in the predecessor-less branch.
- [x] A.4 Unit tests in `internal/analytics/consumption_test.go`: extend
      `TestDeriveConsumption_NilPrevReturnsCurUnchanged` with the four new nil-field
      assertions, and add the Fixture 1/3/4 cases from design.md's Test Contract
      (all-present, wheel-absent-on-cur, wheel-absent-on-prev).
- [x] A.5 Unit tests in `internal/analytics/consumed_test.go`: extend
      `TestDeriveVehicleMetrics_TPMS_PredecessorLess_CopiesVerbatim` and
      `TestDeriveVehicleMetrics_TPMS_WithPredecessor_CopiesFromCurNotPrev` with the
      four new `_calc` field assertions (design.md Fixture 5/6). Offline, no
      `DATABASE_URL` needed.

## Sub-task B — schema, wiring, read port

Depends on: nothing (column names and shape are fixed by design.md, independent of A's
implementation). Coordinate with A only at `make sqlc` time, since both touch generated
code paths.

- [x] B.1 Write migration
      `internal/analytics/db/migrations/20260908000003_add_tpms_pressure_variance_columns.sql`:
      `ADD COLUMN` for the four nullable `DOUBLE PRECISION` columns plus their
      `COMMENT ON COLUMN` text, then the self-join backfill `UPDATE`, then the `Down`
      block dropping the four columns. Exact SQL: `design.md` Part A "Schema"/"Column
      comments" and Part B "SQL"/"Down migration".
- [x] B.2 Add `tpms_pressure_fl_psi_calc, tpms_pressure_fr_psi_calc,
      tpms_pressure_rl_psi_calc, tpms_pressure_rr_psi_calc` to `UpsertVehicleMetric`'s
      column list, VALUES list, and `ON CONFLICT DO UPDATE SET` clause in
      `internal/analytics/db/query.sql`.
- [x] B.3 Add the same four columns to `LatestVehicleMetricsByAccount`'s SELECT column
      list in `internal/analytics/db/query.sql`. Update its doc comment to note the
      widened projection and the "no new index" conclusion (design.md D3).
- [x] B.4 Run `make sqlc` to regenerate `internal/analytics/db/models.go` and
      `query.sql.go`. Confirm the four new columns' `COMMENT ON COLUMN` text lands in
      `models.go`'s `VehicleMetric` struct doc comment.
- [x] B.5 Add the four fields to `upsertVehicleMetricParamsFrom` in
      `internal/analytics/recalculate.go`, using the existing `pgFloat8FromPtr` helper —
      no new mapping code.
- [x] B.6 Add four pointer fields (`TpmsPressureFLPSICalc *float64`, …) to
      `analytics.VehicleStatus` in `internal/analytics/analytics.go`, with a doc comment
      matching the existing pointer-field convention (nil meaning, plus the accepted
      temperature-correlation note from design.md).
- [x] B.7 Add the four fields to `LatestMetricsByAccount`'s mapping loop in
      `internal/analytics/reader.go`, using the existing `ptrFloat64FromPg` helper.

- [x] B.8 DB-integration test for the backfill migration, in
      `internal/analytics/db_tpms_migration_integration_test.go`'s sibling
      `db_tpms_variance_migration_integration_test.go`. Mirror tier 1's file exactly:
      a `goose.Provider` scoped to this module's own `db/migrations` only, driven
      through `ApplyVersion` (never `DownTo`/`Up`), no `t.Parallel`, and a `t.Cleanup`
      that re-applies the version so the shared table is left as other tests expect.
      Assert the Test Contract's backfill fixture in design.md.

**No other new test.** Do NOT add a `Recalculate` round-trip test or a
`LatestMetricsByAccount` test. Those paths are field-copy plumbing already covered by the
module's existing DB-integration tests. See design.md "Test scope".

## Docs

Depends on: A.1–A.3 and B.1–B.7 being decided (the exact column/field names), so do
this last.

- [ ] C.1 Update `internal/analytics/AGENTS.md`: add the four `_calc` delta columns to
      "Data ownership"'s `vehicle_metrics` description (mirroring the existing tier-1
      TPMS-columns entry's shape — note this is the opposite NULL rule from the raw
      TPMS columns), and add the four new `VehicleStatus` fields to "Public interface
      (the port)"'s `LatestMetricsByAccount` entry.
- [ ] C.2 Update `kkpa/context/entities/vehicle-metrics/guide.md`: add the four `_calc`
      delta columns to the column list and the "Conventions & gotchas" section
      (mirroring the tier-1 TPMS bullets, but noting this set follows the `_calc` NULL
      rule, not the raw-observation rule), and add the four new fields to
      `VehicleStatus`'s field list. **Do not touch `openspec/changes/archive/`.**
- [ ] C.3 Confirm no other `kkpa/context/` guide references `VehicleStatus`'s field
      list or `vehicle_metrics`' column list in a way this change invalidates (grep
      `kkpa/context/` for `VehicleStatus` and `vehicle_metrics`). A hit under
      `openspec/changes/archive/` is not this task's to fix.

## Verification (do not run the suite — see Test-Execution-Policy)

- [ ] D.1 `go build ./...`
- [ ] D.2 `go vet ./...`
- [ ] D.3 `gofmt -l internal/analytics`
- [ ] D.4 `make migration-guard` (checks the new migration's version number has no
      collision across module directories)
- [ ] D.5 `make boundary-guard` (confirms no `internal/gateway` file was touched — this
      change touches none, so the guard should be a pure no-op pass)
- [ ] D.6 Report the exact suite commands to the owner:
      `go test ./internal/analytics/...` (offline tests, including this change's new
      A.4/A.5 assertions, must pass with `DATABASE_URL` unset; and `make test-with-db` for B.8's new
      migration test, which needs a database and self-skips without one).
