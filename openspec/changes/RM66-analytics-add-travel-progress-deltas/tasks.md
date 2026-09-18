> **Scope.** Adds three new day-over-day delta columns to `analytics.vehicle_metrics`
> (`distance_traveled_km_delta_calc`, `consumed_pct_delta_calc`,
> `km_per_pct_delta_calc`) and renames the four existing tyre-pressure delta
> columns to match D-D's `_delta_calc` convention — tier 2 of
> `RM66-travel-progress-trends` (D-B, D-D). This dispatch wrote the OpenSpec
> artifacts only; the tasks below are for the implementation dispatch that
> follows.
>
> **Dependencies / parallelism:**
> - T1 (migration file) has no dependencies. MAY start immediately.
> - T2 (Go derivation: `consumed.go`, `consumption.go`, `analytics.go`) has no
>   dependencies on T1 — it is pure Go and compiles standalone. MAY run in
>   parallel with T1.
> - T3 (`query.sql` widen + `sqlc generate`) depends on **T1** — sqlc reads the
>   schema from the migrations directory, so the new/renamed columns must exist
>   there first.
> - T4 (`reader.go` / `recalculate.go` mapping) depends on **T2** (renamed
>   field names) and **T3** (generated types).
> - T5 (existing test files: rename old field references) depends on **T2**.
> - T6 (offline tests — new fixtures) depends on **T2**. MAY run in parallel
>   with T3/T4/T5 once T2 lands.
> - T7 (`delta-guard` baseline shrink) depends on **T1** and **T2** both being
>   complete in the tree (the guard's grep must see the renamed names, not the
>   old ones).
> - T8 (DB-integration test — widened `LatestMetricsForVehicles` projection)
>   depends on **T1, T3, T4** — it needs the real migrated schema and the
>   generated types. Final wave; cannot compile before then.
> - T9 (KB guide update) depends on **T1–T4** being final (needs the true
>   column/field names to document).
> - T10 (leader-integrated) has no dependencies from this module's side, but
>   MUST land in the same wave commit as this tier, since it fixes call sites
>   this tier's own rename breaks.
>
> **Wave placement:** T1, T2 first (parallel). T3 next (needs T1). T4, T5, T6
> next (T4/T5 need T2 landed on disk; T6 can start as soon as T2 lands). T7
> after T1+T2. T8, T9 last — they need the migration and generated types to
> exist. T10 is the leader's own step, run in this tier's wave.

## T1. New migration — schema, comments, backfill — no dependencies

- [ ] T1.1 Before writing the migration, re-run `grep -n "tpms_pressure.*_calc\|distance_traveled_km_calc\|consumed_pct\|km_per_pct_calc"` against
      `internal/analytics/db/migrations/20260917000001_baseline.sql` and
      `internal/analytics/db/query.sql`. Confirm the four tyre-pressure column
      names and the three underlying travel-progress column names still match
      design.md's schema exactly. If the tree has drifted since this proposal
      was written, update the migration to match reality — do not implement a
      stale name. Record what you found, even if it matches exactly.
- [ ] T1.2 Create
      `internal/analytics/db/migrations/20260918000001_add_travel_progress_deltas.sql`
      with the exact `+goose Up`/`+goose Down` SQL in design.md's "Exact schema
      change" section: three `ADD COLUMN`s, four `RENAME COLUMN`s, the
      `COMMENT ON COLUMN` statements from design.md's "Column comments"
      section, and the self-join backfill `UPDATE` from design.md's
      "Backfill" section, in that order (schema first, then comments, then
      backfill, mirroring `20260908000003`'s own file order).
      Acceptance: `goose -dir internal/analytics/db/migrations -table
      analytics.goose_db_version postgres "$DATABASE_URL" up` (or `make
      migrate-up`) applies cleanly on a fresh database — this is the owner's
      step, not Claude's, per `ai/go-conventions.md` "Do not test migrations."
      Claude verifies the file's SQL is syntactically well-formed by reading
      it back, not by running it.
- [ ] T1.3 Confirm `Makefile`'s `MIGRATIONS_DIRS`/`MIGRATION_MODULES` need no
      change — this is a new file inside an already-registered module
      directory, not a new module. State this check and its result in the
      report (CLAUDE.md's "verify the Makefile targets" rule).

## T2. Go derivation — `consumed.go`, `consumption.go`, `analytics.go` — no dependencies, parallel-ok with T1

> **Comment cleanup — binding on this task, opportunistic in scope.** Every
> symbol T2 edits must leave with a doc comment that sends the reader nowhere:
> no `design.md`, no decision IDs (`D1`, `D-B`), no change or roadmap IDs
> (`RM29`, `RM50`), no tier numbers, and no ticket IDs (`MAG-81`). Write the
> reason itself in their place. That is five symbols: `vehicleMetricRow`,
> `deriveVehicleMetrics`, `consumptionCalc`, `deriveConsumption`, and the
> renamed `dayOverDayDelta`. Inline comments inside those function bodies count
> too.
>
> **Do NOT touch anything else.** The two file header comments stay. So do
> `effectiveDay`, `minFlagDistanceKm`, `sumSuperchargerPctBetween`,
> `sumManualPctBetween` and `inferMissingChargingType` — this tier does not edit
> them, and a wider sweep buries the functional change in a large diff. The
> owner ruled on this scope.
>
> Acceptance, runnable:
> `awk '/^func deriveVehicleMetrics|^type vehicleMetricRow/,/^}/' internal/analytics/consumed.go | grep -nE 'design\.md|roadmap D|RM[0-9]|MAG-[0-9]|tier [0-9]|\bD-[A-Z]|\bD[0-9]+\b'`
> returns nothing, and the same grep over `deriveConsumption`, `consumptionCalc`
> and `dayOverDayDelta` in `consumption.go` returns nothing.

- [ ] T2.1 In `internal/analytics/consumption.go`, rename the helper
      `tpmsDeltaPSI(prev, cur *float64) *float64` to `dayOverDayDelta(prev, cur
      *float64) *float64`. Body unchanged. Update its doc comment to describe
      it as the shared nil-safe day-over-day subtraction helper (no PSI
      reference, no change-doc citation), reused by every `_delta_calc`
      field this package computes.
- [ ] T2.2 In `consumption.go`, rename `consumptionCalc`'s four
      `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`, and update
      `deriveConsumption`'s four assignments to call `dayOverDayDelta(...)`
      instead of `tpmsDeltaPSI(...)`.
- [ ] T2.3 In `internal/analytics/consumed.go`, rename `vehicleMetricRow`'s
      four `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`. Add three new fields next to
      the existing five `_calc` fields: `DistanceTraveledKmDeltaCalc
      *float64`, `ConsumedPctDeltaCalc *float64`, `KmPerPctDeltaCalc
      *float64`.
- [ ] T2.4 In `deriveVehicleMetrics` (`consumed.go`), in the `prev != nil`
      branch, after `calc := deriveConsumption(...)`, compute the three new
      deltas against `out[len(out)-1]` per design.md's "Go changes" section —
      `nil` when `len(out) == 0`. Set the three new fields on the appended
      `vehicleMetricRow` literal. The `prev == nil` branch needs no change —
      its row already omits every derived field, including these three, by
      leaving them at their pointer zero value.
      Acceptance: `go build ./internal/analytics/...` compiles with no new
      references to `tpmsDeltaPSI` or the old TPMS field names anywhere in
      this package's non-test files.
- [ ] T2.5 In `internal/analytics/analytics.go`, rename `VehicleStatus`'s four
      `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`, and add three new fields:
      `DistanceTraveledKmDeltaCalc *float64`, `ConsumedPctDeltaCalc
      *float64`, `KmPerPctDeltaCalc *float64`, with doc comments stating the
      absence rule from design.md (absent on a predecessor-less day, or on
      the first day considered within a recalculation pass).

## T3. `query.sql` widen + `sqlc generate` — depends on T1

- [ ] T3.1 In `internal/analytics/db/query.sql`, update `UpsertVehicleMetric`:
      add the three new columns and rename the four tyre columns, in the
      `INSERT` column list, the `VALUES` list, and the `ON CONFLICT ... DO
      UPDATE SET` clause.
- [ ] T3.2 In the same file, update `LatestVehicleMetricsByVehicles`'s
      `SELECT` list to add the three new columns and the four renamed ones.
      Update the query's own doc comment to note this widening is
      projection-only (no index change), following the comment style of its
      two previous widenings, without citing any change or roadmap
      identifier.
- [ ] T3.3 Run `sqlc generate` (or `make sqlc`). Confirm
      `internal/analytics/db/models.go` and `query.sql.go` now declare
      `DistanceTraveledKmDeltaCalc`, `ConsumedPctDeltaCalc`,
      `KmPerPctDeltaCalc`, and the four `TpmsPressure{Fl,Fr,Rl,Rr}PsiDeltaCalc`
      fields (sqlc's own casing) on `UpsertVehicleMetricParams` and
      `LatestVehicleMetricsByVehiclesRow`.
      Acceptance: `git diff internal/analytics/db/models.go
      internal/analytics/db/query.sql.go` shows only the expected additions
      and renames — no unrelated regeneration drift.

## T4. `reader.go` / `recalculate.go` mapping — depends on T2, T3

- [ ] T4.1 In `internal/analytics/reader.go`'s `LatestMetricsForVehicles`,
      add three `ptrFloat64FromPg(row.<GeneratedName>)` lines mapping onto
      `VehicleStatus`'s three new fields, and rename the four existing
      `TpmsPressureFlPsiCalc`-style reads to their new generated names,
      mapped onto `VehicleStatus`'s renamed fields.
- [ ] T4.2 In `internal/analytics/recalculate.go`'s
      `upsertVehicleMetricParamsFrom`, add three
      `pgFloat8FromPtr(row.<NewFieldName>)` lines and rename the four
      existing TPMS-calc lines on both the generated-param side and the
      `vehicleMetricRow` side.
      Acceptance: `go build ./internal/analytics/...` and `go vet
      ./internal/analytics/...` both pass with zero new findings.

## T5. Existing test files — rename old field references — depends on T2

- [ ] T5.1 `internal/analytics/consumed_test.go` and
      `internal/analytics/consumption_test.go` (2 occurrences each) —
      rename their `TpmsPressure{FL,FR,RL,RR}PSICalc` literals to
      `...PSIDeltaCalc`.
- [ ] T5.2 `internal/analytics/db_integration_test.go` (26 occurrences of
      `TpmsPressure`, a mix of the four raw `...PSI` names — unchanged — and
      the four `...PSICalc` names — renamed). Read the file and rename only
      the `...PSICalc` occurrences; leave every raw `...PSI` occurrence
      untouched. Also add fixture columns/assertions for the three new
      fields where this test already asserts a full `VehicleStatus` or a full
      `vehicle_metrics` row, so this integration test does not silently stop
      covering the widened row shape.
      Acceptance: `go vet ./internal/analytics/...` compiles every test file
      with zero references to the four old TPMS-calc names remaining
      anywhere in the package, including tests.

## T6. Offline tests — the three test-contract fixtures — depends on T2

- [ ] T6.1 Add three table-driven cases to
      `internal/analytics/consumed_test.go` (or wherever
      `deriveVehicleMetrics`'s existing offline cases live), implementing
      design.md's "Test contract" fixtures 1–3 verbatim: a day with a
      predecessor row in the same pass, the first day considered within a
      recalculation pass (predecessor exists in storage but is outside the
      pass), and a day whose predecessor is itself predecessor-less.
      Acceptance: each fixture's expected `DistanceTraveledKmDeltaCalc`,
      `ConsumedPctDeltaCalc`, `KmPerPctDeltaCalc` values match design.md's
      worked numbers exactly (10.0 / 5.0 / −0.33 for fixture 1; all three
      nil for fixtures 2 and 3). `go vet` confirms these compile; they are
      not run by Claude (`Test-Execution-Policy`) — reported as
      awaiting-user-verification.

## T7. `delta-guard` baseline shrink — depends on T1, T2

- [ ] T7.1 Before editing the `Makefile`, re-run `delta-guard`'s own two
      greps by hand (or `make delta-guard`) against the tree with T1+T2
      already landed, and confirm the four tyre-pressure names now appear
      only under their new `_delta_calc`/`DeltaCalc` spelling — not the old
      one — everywhere except this change's own `design.md`/`tasks.md`
      (which name the old columns in prose, not as a definition). Record
      what you found.
- [ ] T7.2 Remove `tpms_pressure_fl_psi_calc`, `tpms_pressure_fr_psi_calc`,
      `tpms_pressure_rl_psi_calc`, `tpms_pressure_rr_psi_calc` from the
      `sqlbaseline` regex in the `Makefile`'s `delta-guard` target.
- [ ] T7.3 Remove `TpmsPressureFLPSICalc`, `TpmsPressureFRPSICalc`,
      `TpmsPressureRLPSICalc`, `TpmsPressureRRPSICalc`,
      `TpmsPressureFlPsiCalc`, `TpmsPressureFrPsiCalc`,
      `TpmsPressureRlPsiCalc`, `TpmsPressureRrPsiCalc` from the `gobaseline`
      regex in the same target.
      Acceptance: `make delta-guard` passes, and its printed baseline size
      drops from 10 SQL / 15 Go to 6 SQL / 7 Go.

## T8. DB-integration test — widened latest-status projection — depends on T1, T3, T4

- [ ] T8.1 Extend (or add to) `internal/analytics/db_integration_test.go`'s
      `LatestMetricsForVehicles` coverage with a case asserting the three new
      fields round-trip through a real migrated database: seed two
      consecutive days via `Recalculate`, read back via
      `LatestMetricsForVehicles`, and assert the returned `VehicleStatus`
      carries the expected non-nil deltas for the second day (mirroring
      fixture 1's numbers) and nil deltas for a single-day window (mirroring
      fixture 2). This is `TEST_DATABASE_URL`-gated per this module's
      existing convention and self-skips without one.
      Acceptance: compiles under `go vet` against the real generated types
      from T3; not run by Claude — awaiting-user-verification.

## T9. KB guide update — depends on T1, T2, T3, T4

- [ ] T9.1 Update `kkpa/context/entities/vehicle-metrics/guide.md`: add the
      three new columns and the renamed four to its column list, its
      "Changed by" history line (mirroring the existing "Changed by RM50
      tier 3" entry's style), and its NULL-semantics bullets (the new
      "first day of a recalculation pass" absence condition is a genuinely
      new rule this guide does not yet describe for any column — state it
      once, clearly, since it now applies to seven columns total: the three
      new ones and the four renamed tyre-pressure ones, which already had a
      version of this rule under their old name).
      Acceptance: `grep -n "tpms_pressure_fl_psi_calc\b"
      kkpa/context/entities/vehicle-metrics/guide.md` (the exact old name,
      word-bounded) returns nothing — every mention now uses the new name,
      except where the guide is explicitly narrating history ("renamed
      from...").

## T10. Leader-integrated — gateway call-site fix (NOT this module's task)

- [ ] T10.1 (Leader, in this tier's wave commit) Update
      `internal/gateway/handlers/handlers.go:580-583`'s four
      `dashTireWheel(ctx, vs.TpmsPressure*PSI, vs.TpmsPressure*PSICalc)` calls
      to the renamed `vs.TpmsPressure*PSIDeltaCalc` fields, and update the
      matching `analytics.VehicleStatus{...}` literals in
      `internal/gateway/handlers/handlers_test.go`. A module worker for
      `analytics` never edits these files — they live outside
      `internal/analytics/`.
