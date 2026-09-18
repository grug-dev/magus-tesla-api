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

- [x] T1.1 Before writing the migration, re-run `grep -n "tpms_pressure.*_calc\|distance_traveled_km_calc\|consumed_pct\|km_per_pct_calc"` against
      `internal/analytics/db/migrations/20260917000001_baseline.sql` and
      `internal/analytics/db/query.sql`. Confirm the four tyre-pressure column
      names and the three underlying travel-progress column names still match
      design.md's schema exactly. If the tree has drifted since this proposal
      was written, update the migration to match reality — do not implement a
      stale name. Record what you found, even if it matches exactly.
      **Found: exact match, no drift.** Both files still use
      `tpms_pressure_{fl,fr,rl,rr}_psi_calc`, `distance_traveled_km_calc`,
      `consumed_pct`, `km_per_pct_calc` verbatim as design.md assumes.
- [x] T1.2 Create
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
      **Done.** File created; read back in full, statements balanced
      (goose Up/Down present, StatementBegin/End wraps only the comments
      containing an internal semicolon, matching the baseline's own
      convention). Not applied — that is the owner's step.
- [x] T1.3 Confirm `Makefile`'s `MIGRATIONS_DIRS`/`MIGRATION_MODULES` need no
      change — this is a new file inside an already-registered module
      directory, not a new module. State this check and its result in the
      report (CLAUDE.md's "verify the Makefile targets" rule).
      **Checked: no change needed.** `MIGRATION_MODULES` already lists
      `analytics`; `MIGRATIONS_DIRS` derives
      `internal/analytics/db/migrations` as a whole directory, so a new file
      inside it needs no Makefile edit.

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

- [x] T2.1 In `internal/analytics/consumption.go`, rename the helper
      `tpmsDeltaPSI(prev, cur *float64) *float64` to `dayOverDayDelta(prev, cur
      *float64) *float64`. Body unchanged. Update its doc comment to describe
      it as the shared nil-safe day-over-day subtraction helper (no PSI
      reference, no change-doc citation), reused by every `_delta_calc`
      field this package computes.
- [x] T2.2 In `consumption.go`, rename `consumptionCalc`'s four
      `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`, and update
      `deriveConsumption`'s four assignments to call `dayOverDayDelta(...)`
      instead of `tpmsDeltaPSI(...)`.
- [x] T2.3 In `internal/analytics/consumed.go`, rename `vehicleMetricRow`'s
      four `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`. Add three new fields next to
      the existing five `_calc` fields: `DistanceTraveledKmDeltaCalc
      *float64`, `ConsumedPctDeltaCalc *float64`, `KmPerPctDeltaCalc
      *float64`.
- [x] T2.4 In `deriveVehicleMetrics` (`consumed.go`), in the `prev != nil`
      branch, after `calc := deriveConsumption(...)`, compute the three new
      deltas against `out[len(out)-1]` per design.md's "Go changes" section —
      `nil` when `len(out) == 0`. Set the three new fields on the appended
      `vehicleMetricRow` literal. The `prev == nil` branch needs no change —
      its row already omits every derived field, including these three, by
      leaving them at their pointer zero value.
      Acceptance: `go build ./internal/analytics/...` compiles with no new
      references to `tpmsDeltaPSI` or the old TPMS field names anywhere in
      this package's non-test files.
      **Note on acceptance wording:** `tpmsDeltaPSI` and the old TPMS field
      names are gone from every file T2 touched. `go build
      ./internal/analytics/...` itself still fails, but only in
      `reader.go`/`recalculate.go` (T4, later wave, depends on T2+T3) and
      the pre-existing `_test.go` files (T5) — neither is in this dispatch's
      scope. See the report for full build output.
- [x] T2.5 In `internal/analytics/analytics.go`, rename `VehicleStatus`'s four
      `TpmsPressure{FL,FR,RL,RR}PSICalc` fields to
      `TpmsPressure{FL,FR,RL,RR}PSIDeltaCalc`, and add three new fields:
      `DistanceTraveledKmDeltaCalc *float64`, `ConsumedPctDeltaCalc
      *float64`, `KmPerPctDeltaCalc *float64`, with doc comments stating the
      absence rule from design.md (absent on a predecessor-less day, or on
      the first day considered within a recalculation pass).

## T3. `query.sql` widen + `sqlc generate` — depends on T1

- [x] T3.1 In `internal/analytics/db/query.sql`, update `UpsertVehicleMetric`:
      add the three new columns and rename the four tyre columns, in the
      `INSERT` column list, the `VALUES` list, and the `ON CONFLICT ... DO
      UPDATE SET` clause.
      **Done.** All three lists updated. The query's own doc comment
      (describing the four tyre delta columns) was also updated to name the
      new `_delta_calc` spelling, since the old text would otherwise
      describe a column name that no longer exists.
- [x] T3.2 In the same file, update `LatestVehicleMetricsByVehicles`'s
      `SELECT` list to add the three new columns and the four renamed ones.
      Update the query's own doc comment to note this widening is
      projection-only (no index change), following the comment style of its
      two previous widenings, without citing any change or roadmap
      identifier.
      **Done.** `SELECT` list widened by seven columns. New comment
      paragraph added, no RM/D-x citation, matching the two prior widenings'
      style.
- [x] T3.3 Run `sqlc generate` (or `make sqlc`). Confirm
      `internal/analytics/db/models.go` and `query.sql.go` now declare
      `DistanceTraveledKmDeltaCalc`, `ConsumedPctDeltaCalc`,
      `KmPerPctDeltaCalc`, and the four `TpmsPressure{Fl,Fr,Rl,Rr}PsiDeltaCalc`
      fields (sqlc's own casing) on `UpsertVehicleMetricParams` and
      `LatestVehicleMetricsByVehiclesRow`.
      Acceptance: `git diff internal/analytics/db/models.go
      internal/analytics/db/query.sql.go` shows only the expected additions
      and renames — no unrelated regeneration drift.
      **Done.** `sqlc generate` ran clean (`sqlc version v1.31.1`). Both
      files now declare all seven fields under sqlc's casing on both
      structs. `git diff --stat`: `models.go` 22 lines changed,
      `query.sql.go` 194 lines changed — every hunk is one of the expected
      additions/renames, nothing else. Full diff pasted in the report.

## T4. `reader.go` / `recalculate.go` mapping — depends on T2, T3

- [x] T4.1 In `internal/analytics/reader.go`'s `LatestMetricsForVehicles`,
      add three `ptrFloat64FromPg(row.<GeneratedName>)` lines mapping onto
      `VehicleStatus`'s three new fields, and rename the four existing
      `TpmsPressureFlPsiCalc`-style reads to their new generated names,
      mapped onto `VehicleStatus`'s renamed fields.
      **Done.**
- [x] T4.2 In `internal/analytics/recalculate.go`'s
      `upsertVehicleMetricParamsFrom`, add three
      `pgFloat8FromPtr(row.<NewFieldName>)` lines and rename the four
      existing TPMS-calc lines on both the generated-param side and the
      `vehicleMetricRow` side.
      Acceptance: `go build ./internal/analytics/...` and `go vet
      ./internal/analytics/...` both pass with zero new findings.
      **Done.** `go build ./internal/analytics/...` passes clean. `go vet
      ./internal/analytics/...` still fails, but only in `consumed_test.go`
      (old `TpmsPressureFLPSICalc` field reference) — that is T5's task,
      not T4's, and is unrelated to reader.go/recalculate.go. See report
      for full vet output.

## T5. Existing test files — rename old field references — depends on T2

- [x] T5.1 `internal/analytics/consumed_test.go` and
      `internal/analytics/consumption_test.go` (2 occurrences each) —
      rename their `TpmsPressure{FL,FR,RL,RR}PSICalc` literals to
      `...PSIDeltaCalc`.
      **Done, count corrected.** The tree had drifted from the task's
      count: `consumed_test.go` had 8 occurrences, `consumption_test.go`
      had 14 — not 2 each. All renamed via a word-boundary sed
      (`TpmsPressure(FL|FR|RL|RR)PSICalc` → `...PSIDeltaCalc`), verified
      by a full-package grep afterward (see T5.2's acceptance run).
- [x] T5.2 `internal/analytics/db_integration_test.go` (26 occurrences of
      `TpmsPressure`, a mix of the four raw `...PSI` names — unchanged — and
      the four `...PSICalc` names — renamed). Read the file and rename only
      the `...PSICalc` occurrences; leave every raw `...PSI` occurrence
      untouched. Also add fixture columns/assertions for the three new
      fields where this test already asserts a full `VehicleStatus` or a full
      `vehicle_metrics` row, so this integration test does not silently stop
      covering the widened row shape.
      **Found: the rename half is void.** All 26 `TpmsPressure` occurrences
      in this file are raw `...PSI`/`...Psi` names (Go domain casing and
      sqlc-generated casing) — none was a `...PSICalc` name. This file never
      asserted the four tyre deltas at all, so there was nothing to rename.
      **Widening done.** Two places assert a "full" row and needed the three
      new fields: (1) `fetchVehicleMetric`, whose own doc comment claims it
      "selects every non-key column" — widened its `SELECT`/`Scan` to add
      `distance_traveled_km_delta_calc`, `consumed_pct_delta_calc`,
      `km_per_pct_delta_calc`. (2)
      `TestReader_LatestMetricsForVehicles_TPMS_And_ExposedCalcColumns` —
      widened its seed `INSERT` and its `VehicleStatus` assertions to cover
      the same three fields. Left the four tyre-delta columns out of
      `fetchVehicleMetric`'s select list: they were already missing there
      before this tier (a tier-3 gap, since the helper's own comment already
      overpromised), and adding them is outside this task's literal "three
      new fields" scope — noted here rather than silently fixed.
      `go vet ./internal/analytics/...` passes clean, and
      `grep -rn "TpmsPressure(FL|FR|RL|RR)PSICalc|TpmsPressureFlPsiCalc|TpmsPressureFrPsiCalc|TpmsPressureRlPsiCalc|TpmsPressureRrPsiCalc" internal/analytics/`
      returns nothing.
      Acceptance: `go vet ./internal/analytics/...` compiles every test file
      with zero references to the four old TPMS-calc names remaining
      anywhere in the package, including tests.

## T6. Offline tests — the three test-contract fixtures — depends on T2

- [x] T6.1 Add three table-driven cases to
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
      **Done — awaiting-user-verification.** Added three tests to
      `consumed_test.go`, named descriptively rather than "Fixture 1/2/3"
      (no design.md/change-ID citation in the code, per this dispatch's
      comment rule): `TestDeriveVehicleMetrics_TravelProgressDelta_
      AgainstPassPredecessor` (fixture 1 — asserts `DistanceTraveledKmDeltaCalc
      = 10.0`, `ConsumedPctDeltaCalc = 5.0`, `KmPerPctDeltaCalc =
      60.0/20.0-50.0/15.0` — the exact same float expression the production
      code evaluates, so the match is exact, not approximate; this equals
      design.md's stated "−0.33 approximately"), `..._FirstRowOfPassIsNil`
      (fixture 2 — own figures non-nil, all three deltas nil), and
      `..._PredecessorLacksOwnDelta_NilPropagates` (fixture 3 — predecessor's
      own figures nil, so subtracting from it yields nil, not a fabricated
      number). `go vet ./internal/analytics/...` compiles clean; not run by
      Claude per `Test-Execution-Policy`.

## T7. `delta-guard` baseline shrink — depends on T1, T2

> **CORRECTION, found while running wave 1 — appended, nothing deleted.** T7.2 as
> written cannot be satisfied, and T7.3's dependency was wrong.
>
> **T7.2 is void.** The four `tpms_pressure_*_psi_calc` names stay in `sqlbaseline`
> forever. `20260917000001_baseline.sql` DEFINES those columns under their old
> names, and that file is an applied baseline nobody may edit. The rename lives in
> a later migration, so the old definition line survives in the tree for good. The
> SQL baseline does not shrink at all: it stays at 10.
>
> **T7.3 now depends on T1, T2, T3 AND T4.** After T2 only the four hand-written Go
> names are gone. The four sqlc-generated `TpmsPressureFlPsiCalc` spellings go with
> T3's regeneration, and `reader.go`/`recalculate.go` with T4. Removing them before
> then makes `make delta-guard` fail.
>
> **Corrected acceptance:** `make delta-guard` passes and prints
> `baseline: 10 SQL / 7 Go legacy name(s)` — not `6 SQL / 7 Go`. Tier 1's design
> predicted a SQL shrink that the frozen baseline file makes impossible.
>
> **Already done in wave 1, by the leader:** the guard's two delta-exclusion
> patterns were anchored with `^`, but they are applied to `grep -rn` output, which
> carries a `path:line:` prefix. The anchor could never match, so a correctly named
> `_delta_calc` / `DeltaCalc` column was reported as a violation. Both patterns now
> accept the prefix. Verified four ways: clean tree passes, a bare `_calc` name
> fails, `delta:allow` forgives, and a compliant `_delta_calc` name is not flagged.

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

- [x] T8.1 Extend (or add to) `internal/analytics/db_integration_test.go`'s
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
      **Done — awaiting-user-verification.** Added
      `TestReader_LatestMetricsForVehicles_TravelProgressDeltas_RoundTripThroughRecalculate`
      in `internal/analytics/db_integration_test.go`. Seeds three consecutive
      snapshots (day0 as a predecessor only, day1, day2) with plain
      distance/battery numbers chosen so the real derivation produces
      fixture 1's exact figures (distance 50.0/60.0, consumed 15.0/20.0,
      km/pct 50/15 and 60/20). Two `Recalculate` calls against the real
      `Recalculator`/`Reader`, one test: pass 1 covers `[day1, day2]` in one
      window, so day2 gets a same-pass predecessor (day1's own freshly-built
      row) and its deltas come back non-nil, asserted equal to fixture 1's
      10.0 / 5.0 / (60/20-50/15) via `approxEqual`. Pass 2 recalculates day2
      alone (`[day2, day2]`) — day1 now falls outside that window, so day2 is
      the first row *this* pass processes; its own `DistanceTraveledKmCalc`
      is asserted unchanged (60.0) while all three deltas are asserted nil,
      matching fixture 2. Both reads go through the real
      `Reader.LatestMetricsForVehicles`, never `fetchVehicleMetric`'s raw
      SQL, so this is genuinely the widened-projection coverage T8 asks for.
      `go build ./...`, `go vet ./...`, `gofmt -l .` all clean (see report).
      Not run by Claude (`Test-Execution-Policy`) —
      awaiting-user-verification.
      **Pre-existing gap noted, not fixed:** `fetchVehicleMetric`'s doc
      comment still claims it selects "every non-key column" but its SELECT
      list omits the four `tpms_pressure_*_psi_delta_calc` columns — a gap
      T5.2 already found and left alone as pre-existing and out of scope.
      Fixing it means widening a `SELECT`, a `Scan` call, and rewording the
      comment, which is more than the "one line" bar this dispatch's brief
      set for opportunistic cleanup, so it is reported here instead of
      touched.

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

- [x] T10.1 (Leader, in this tier's wave commit) Update
      `internal/gateway/handlers/handlers.go:580-583`'s four
      `dashTireWheel(ctx, vs.TpmsPressure*PSI, vs.TpmsPressure*PSICalc)` calls
      to the renamed `vs.TpmsPressure*PSIDeltaCalc` fields, and update the
      matching `analytics.VehicleStatus{...}` literals in
      `internal/gateway/handlers/handlers_test.go`. A module worker for
      `analytics` never edits these files — they live outside
      `internal/analytics/`.
