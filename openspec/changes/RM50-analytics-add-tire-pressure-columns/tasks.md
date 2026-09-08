# Tasks — RM50-analytics-add-tire-pressure-columns

All work is inside `internal/analytics`. Grouped into three sub-tasks that touch mostly
disjoint files — Half A and Half B can be implemented in parallel by separate agents; Half
C (the migration) is a prerequisite file both A's tests and the KB task read, but does not
share Go files with A or B.

## Half A — four raw TPMS columns + backfill migration

Depends on: nothing. Blocks: A's own DB-integration tests need the migration applied.

- [x] 1.1 Write migration
      `internal/analytics/db/migrations/20260908000002_add_tpms_pressure_columns.sql`:
      `ADD COLUMN` for the four nullable `DOUBLE PRECISION` columns plus their
      `COMMENT ON COLUMN` text, then the `UPDATE ... FROM telemetry.vehicle_snapshots`
      backfill, then the `Down` block dropping the four columns. Exact SQL: `design.md`
      Part A "Schema"/"Column comments" and Part C "SQL".
- [x] 1.2 Add `tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi,
      tpms_pressure_rr_psi` to `UpsertVehicleMetric`'s column list, VALUES list, and
      `ON CONFLICT DO UPDATE SET` clause in `internal/analytics/db/query.sql`.
- [x] 1.3 Run `make sqlc` to regenerate `internal/analytics/db/models.go` and
      `query.sql.go`. Confirm the four new columns' `COMMENT ON COLUMN` text lands in
      `models.go`'s `VehicleMetric` struct doc comment (the KB's own documented gotcha —
      easy to miss because the build stays green either way).
- [x] 1.4 Add four fields (`TpmsPressureFLPSI *float64`, …) to `vehicleMetricRow` in
      `internal/analytics/consumed.go`. Populate them from `cur`'s already-public
      `TpmsPressureFLPSI`/`FR`/`RL`/`RR` fields in **both** branches of
      `deriveVehicleMetrics` (the predecessor-less branch and the normal branch) — see
      design.md D2. Direct pointer assignment, no `&cur.Field` address-taking (the source
      fields are already `*float64`, unlike `Locked`/`CarVersion`).
- [x] 1.5 Add the four fields to `upsertVehicleMetricParamsFrom` in
      `internal/analytics/recalculate.go`, using the existing `pgFloat8FromPtr` helper —
      no new mapping code.
- [x] 1.6 Unit test in `internal/analytics/consumed_test.go`: the two `deriveVehicleMetrics`
      cases from design.md's Test Contract (predecessor-less row, row with a
      predecessor) plus the "one wheel absent" case. Offline, no `DATABASE_URL` needed.
- [ ] 1.7 DB-integration test in `internal/analytics/db_integration_test.go`: the
      `Recalculate` round-trip case from design.md's Test Contract. `DATABASE_URL`-gated,
      self-skips per this module's existing convention.
- [ ] 1.8 DB-integration test for the migration's backfill, in a new or existing
      migration-focused test file (mirror this module's existing round-trip migration
      test convention, e.g. `20260828000001`'s): the backfill case and the
      "no matching snapshot stays NULL" case from design.md's Test Contract, plus a
      Down/Up round-trip.

## Half B — expose two existing columns on the read port

Depends on: nothing (both source columns already exist). Can run in parallel with Half A
— touches different lines of the same two files, so coordinate the final merge of
`query.sql`/`analytics.go`/`reader.go` if done by a separate agent in the same wave.

- [x] 2.1 Add `distance_traveled_km_calc, consumed_pct` to `LatestVehicleMetricsByAccount`'s
      SELECT column list in `internal/analytics/db/query.sql`. Update its doc comment to
      note the widened projection and the "no new index" conclusion (design.md D3).
- [x] 2.2 Run `make sqlc` (same regeneration as task 1.3 — coordinate so this does not
      clobber Half A's generated changes; regenerating once after both SQL edits land is
      fine).
- [x] 2.3 Add `DistanceTraveledKmCalc *float64` and `ConsumedPct *float64` to
      `analytics.VehicleStatus` in `internal/analytics/analytics.go`, with a doc comment
      matching the existing pointer-field convention (nil meaning, per design.md).
- [x] 2.4 Add the two fields to `LatestMetricsByAccount`'s mapping loop in
      `internal/analytics/reader.go`, using the existing `ptrFloat64FromPg` helper.
- [ ] 2.5 DB-integration test cases (can extend the same test function/file as task 1.7 or
      1.8): the `LatestMetricsByAccount` case and the "pre-migration row" case from
      design.md's Test Contract.

## Half C — docs

Depends on: 1.1–1.5 and 2.1–2.4 being decided (the exact column/field names), so do this
last.

- [ ] 3.1 Update `internal/analytics/AGENTS.md`: add the four TPMS columns to "Data
      ownership"'s `vehicle_metrics` description (mirroring the existing
      `max_range_charge_counter` entry's shape), and add the six new `VehicleStatus`
      fields to "Public interface (the port)"'s `LatestMetricsByAccount` entry.
- [ ] 3.2 Update `kkpa/context/entities/vehicle-metrics/guide.md`: add the four TPMS
      columns to the column list and the "Conventions & gotchas" section (mirroring the
      existing eight-status-observation bullets), and add the six new fields to
      `VehicleStatus`'s field list. **Do not touch `openspec/changes/archive/`.**
- [ ] 3.3 Confirm no other `kkpa/context/` guide references `VehicleStatus`'s field list or
      `vehicle_metrics`' column list in a way this change invalidates (grep
      `kkpa/context/` for `VehicleStatus` and `vehicle_metrics` — CLAUDE.md's docs-track-
      change rule).

## Verification (do not run the suite — see Test-Execution-Policy)

- [ ] 4.1 `go build ./...`
- [ ] 4.2 `go vet ./...`
- [ ] 4.3 `gofmt -l internal/analytics`
- [ ] 4.4 `make migration-guard` (checks the new migration's version number has no
      collision across module directories)
- [ ] 4.5 `make boundary-guard` (confirms the backfill migration, as expected, does not
      trip it — design.md Part C already verified this by reading the guard's grep
      target; this step re-confirms after the file exists)
- [ ] 4.6 Report the exact suite commands to the owner:
      `go test ./internal/analytics/...` (offline tests must pass with `DATABASE_URL`
      unset; DB-integration tests self-skip in that case and need `DATABASE_URL` set, or
      Docker running, to actually exercise tasks 1.7/1.8/2.5).
