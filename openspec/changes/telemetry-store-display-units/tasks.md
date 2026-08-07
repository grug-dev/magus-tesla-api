# Tasks — telemetry-store-display-units

> RM7 tier 2 of 4. Single module (`internal/telemetry`) plus two repo-root convention docs.
> **Depends on RM7 tier 1** (`tesla-add-tpms-psi-companions`) being merged — group 3 calls the
> `TpmsPressure*PSI()` companions it adds.
>
> **Design gate:** group 1 must not be applied until the user has confirmed the migration and
> index plan in `design.md` D1/D2 (`CLAUDE.md` → Design-Gates: `database`).
>
> **Build expectation:** this tier leaves `internal/battery` and `internal/gateway` not compiling,
> by construction (RM7 Decision 8). Group 7 is scoped accordingly. Do NOT "fix" those modules
> here — they are RM7 tiers 3 and 4 and belong to different module workers.
>
> Groups 1–5 are serial (each depends on the previous). Group 6 (docs) is independent of 2–5 and
> can run in parallel with them; group 7 gates everything.

## 1. Migration (DESIGN GATE — user confirmation required before applying)

- [ ] 1.1 Present `design.md` D1 (full migration SQL) and D2 (index plan) to the user and record
      explicit confirmation in `progress.json` before writing any file in this group.
- [ ] 1.2 Create `internal/telemetry/db/migrations/20260806000001_store_display_units_vehicle_snapshots.sql`
      with the Up/Down exactly as specified in `design.md` D1, including the header comment that
      records what it supersedes and why.
- [ ] 1.3 Verify the Up block renames all 15 columns listed in D1 and that the `UPDATE` rescales
      exactly the 6 value-changing ones — the 9 rename-only columns must NOT appear in the
      `UPDATE`'s SET list.
- [ ] 1.4 Verify the Down block reverses both steps in the opposite order (divide, then rename
      back) and that no column or row is dropped anywhere in the migration.
- [ ] 1.5 `make migrate-up` against a scratch database succeeds.
- [ ] 1.6 `make migrate-down` then `make migrate-up` round-trips cleanly; spot-check one row that
      `odometer_km` ≈ `1.609344 ×` its pre-migration `odometer` and that a row whose TPMS values
      were NULL still has NULL (not 0) in all four `tpms_pressure_*_psi` columns.

## 2. Generated query layer

- [ ] 2.1 Update `internal/telemetry/db/query.sql`: rename every affected column in
      `InsertVehicleSnapshot`'s column list, its `VALUES` parameter names, and its
      `ON CONFLICT ... DO UPDATE SET` block.
- [ ] 2.2 Update the explicit column lists in `ListSnapshotsByVehicle` and
      `SnapshotsByVehicleSince` (and any other query in the file that names an affected column).
- [ ] 2.3 Rewrite the `InsertVehicleSnapshot` header comment at `query.sql:20-21`, which currently
      asserts the opposite rule ("stored API-native (miles); km is derived on read by the domain
      type's Km() companions"), plus the TPMS comment block that says the columns are in bar.
- [ ] 2.4 Run `make sqlc` and confirm `db/query.sql.go` regenerates with the renamed struct fields
      (e.g. `Odometer` → `OdometerKm`, `TpmsPressureFl` → `TpmsPressureFlPsi`).

## 3. Domain type and write path

- [ ] 3.1 In `internal/telemetry/telemetry.go`, rename the `Snapshot` fields to carry their unit:
      `BatteryRange`→`BatteryRangeKm`, `Odometer`→`OdometerKm`, `InsideTemp`→`InsideTempC`,
      `OutsideTemp`→`OutsideTempC`, `BatteryLevel`→`BatteryLevelPct`,
      `UsableBatteryLevel`→`UsableBatteryLevelPct`, `ChargeLimitSoc`→`ChargeLimitSocPct`,
      `ChargeEnergyAdded`→`ChargeEnergyAddedKWh`, `ChargerPower`→`ChargerPowerKW`,
      `ChargerVoltage`→`ChargerVoltageV`, `ChargerActualCurrent`→`ChargerActualCurrentA`,
      `TpmsPressureFL`→`TpmsPressureFLPSI` (and FR/RL/RR). Update each field's unit comment.
- [ ] 3.2 Delete the six companion methods at `telemetry.go:110-155` (`BatteryRangeKm`,
      `OdometerKm`, and the four `TpmsPressure*PSI`). Required for the package to compile —
      the new field names collide with them (design D4).
- [ ] 3.3 Delete the `milesToKm` and `barToPSI` constants from `telemetry.go`. After this the
      module must contain no conversion factor at all.
- [ ] 3.4 In `snapshotFrom` (`service.go:469-503`), populate the converted fields by calling the
      `tesla` adapter's companions — `data.ChargeState.BatteryRangeKm()`,
      `data.VehicleState.OdometerKm()`, and `data.VehicleState.TpmsPressure{FL,FR,RL,RR}PSI()` —
      never by multiplying inline (design D3).
- [ ] 3.5 Confirm the TPMS conversion is applied BEFORE `ptr()` wraps, so a reported zero still
      stores non-NULL and an unreported value still stores NULL (design D3, spec scenario
      "A truthfully reported zero pressure is stored as non-NULL").
- [ ] 3.6 Confirm `InsideTempC`/`OutsideTempC` are assigned straight from the DTO with no
      conversion — the Fleet API already reports Celsius (spec scenario "Temperature is stored as
      reported, without conversion").
- [ ] 3.7 Update `mapping.go:104-118` (row → domain) for both the renamed generated-struct fields
      and the renamed domain fields.

## 4. Reader port

- [ ] 4.1 Verify the `Reader` interface signatures are unchanged — this tier changes field names
      on the returned `Snapshot`, not the port's method set (spec: "No new read method is
      introduced").
- [ ] 4.2 Grep `internal/telemetry` for any remaining in-module caller of a deleted companion and
      convert it to a field read.

## 5. Tests (module-scoped)

- [ ] 5.1 Update every `telemetry.Snapshot` struct literal in the module's tests to the new field
      names, and change the values so they represent the display unit (a test that previously
      meant "12000 miles" now means "12000 km" or the converted equivalent — do not leave a value
      whose unit silently flipped meaning).
- [ ] 5.2 Add a unit test on `snapshotFrom` asserting a known miles odometer and bar pressure DTO
      produces the converted km and PSI values on the resulting `Snapshot`.
- [ ] 5.3 Add a unit test asserting an unreported TPMS reading stays nil through `snapshotFrom`
      and that a reported `0.0` produces a non-nil `0.0` PSI.
- [ ] 5.4 Update the `DATABASE_URL`-gated integration tests (`db_integration_test.go`,
      `db_read_integration_test.go`, `db_sourcea_integration_test.go`) for the renamed columns and
      fields; assert a round-trip write→read returns the converted values.
- [ ] 5.5 Confirm no test in this module calls a `Raw*` method or `cmd/explore-tesla-api` — the
      `tesla-exploration` capability must stay test-free (`CLAUDE.md`).

## 6. Docs — MODULE-LOCAL ONLY (independent of groups 2–5; can run in parallel)

> **Scope narrowed.** The platform-wide convention rewrite (`ai/go-conventions.md`, root
> `CLAUDE.md`) is owned by the separate `platform-unit-of-measure-convention` change, so that one
> rule has one owner and two changes never edit the same lines. This tier touches only its own
> module's doc.

- [ ] 6.1 Update `internal/telemetry/AGENTS.md:107-115` (DTO/units conventions), which currently
      states the reversed rule ("Units are stored API-native (miles)... km is NEVER a stored
      column and never a struct field"). Replace with the display-unit rule and the unit-suffix
      naming convention.
- [ ] 6.2 Do **not** edit `ai/go-conventions.md` or the root `CLAUDE.md` in this change — they
      belong to `platform-unit-of-measure-convention`. If that change has not landed yet, note in
      `progress.json` that those two files still state the superseded read-time-companion rule,
      so a reviewer does not read the mismatch as an incomplete tier.
- [ ] 6.3 Confirm no structural doc needs updating: no module is added, removed, renamed or
      re-scoped, so the root `README.md` "Project Structure"/"Architecture" tables and
      `cmd/README.md` are unaffected. Record that conclusion in `progress.json` rather than
      touching those files.

## 7. Verification gate (module-scoped — see RM7 Decision 8)

- [ ] 7.1 `go build ./internal/telemetry/... ./internal/tesla/...` passes.
- [ ] 7.2 `go vet ./internal/telemetry/... ./internal/tesla/...` passes.
- [ ] 7.3 `go test ./internal/telemetry/... ./internal/tesla/...` passes, including the
      `DATABASE_URL`-gated integration tests against a migrated scratch DB.
- [ ] 7.4 Confirm `go build ./...` FAILS only in `internal/battery` and `internal/gateway`, and
      only on the deleted companions / renamed fields. Any other failure is a real defect in this
      tier. Record the observed failure list in `progress.json` so tiers 3 and 4 have the exact
      call sites.
- [ ] 7.5 `grep -rn "milesToKm\|barToPSI" internal/telemetry` returns nothing, and
      `grep -rn "battery_range\b\|\bodometer\b\|inside_temp\b\|tpms_pressure_fl\b" internal/telemetry`
      returns only historical migration files and `raw_data` JSON paths — never a live query or
      Go identifier.
- [ ] 7.6 `openspec validate telemetry-store-display-units --strict` passes.
