# Tasks — battery-adopt-snapshot-unit-fields

> RM7 tier 3 of 4. Single module (`internal/battery`). **Depends on RM7 tier 2**
> (`telemetry-store-display-units`) being applied — this module does not compile until the
> renamed `telemetry.Snapshot` fields exist.
>
> Independent of RM7 tier 4 (`gateway-adopt-snapshot-unit-fields`): the two touch disjoint files
> and can be implemented in parallel by separate module workers once tier 2 lands.

## 1. Call sites

- [x] 1.1 In `internal/battery/derive.go:32`, change
      `end.OdometerKm() - start.OdometerKm()` to `end.OdometerKm - start.OdometerKm`.
- [x] 1.2 In `internal/battery/derive.go:54-55`, change `FromKm: start.OdometerKm()` and
      `ToKm: end.OdometerKm()` to the field reads.
- [x] 1.3 Update any other `telemetry.Snapshot` field reference in the module that tier 2 renamed
      (e.g. `UsableBatteryLevel` → `UsableBatteryLevelPct`, `BatteryLevel` → `BatteryLevelPct` in
      `socReadings`). Grep the whole module rather than trusting this list.
- [x] 1.4 Confirm no arithmetic constant (`1.609344`, `milesToKm`, or any literal factor) is
      introduced anywhere in this module — after this tier it must contain zero unit conversion.
- [x] 1.5 Confirm the `Efficiency` type is unchanged: `WhPerKm`, `FromKm`, `ToKm`,
      `BatteryDeltaPct`, `Approximate` keep their names, types and meanings (design D3).

## 2. Tests

- [x] 2.1 Update every `telemetry.Snapshot` literal in `derive_test.go` and `reader_test.go` to the
      renamed fields.
- [x] 2.2 **Review each fixture for meaning, not just field name** (design D2): a fixture that
      previously meant "1000 miles" now reads as "1000 km" and feeds a distance 1.609× smaller into
      the formula. For each affected test, either convert the fixture value or recompute the
      expected Wh/km so the test still asserts what it was written to assert. Do NOT accept a green
      run as proof here — a find-and-replace passes while testing the wrong thing.
- [x] 2.3 Add or adapt one test with a hand-computed expected Wh/km from a known kilometre distance
      and known kWh, so the formula is pinned by an independently-derived number rather than by a
      value copied from the previous implementation.
- [x] 2.4 Confirm the existing `ok=false` tests (fewer than two snapshots, no distance moved, net
      charge exceeds consumption) still exercise the same conditions after the fixture edits.

## 3. Docs

- [x] 3.1 Update `internal/battery/AGENTS.md:48-50`, which describes `FromKm`/`ToKm` as
      "already km-native, derived from `telemetry.Snapshot.OdometerKm()`" — that method no longer
      exists. State that the snapshot's kilometre fields are read directly and that this module
      performs no unit conversion.

## 4. Verification gate (module-scoped — see RM7 Decision 8)

- [x] 4.1 `go build ./internal/battery/...` and `go vet ./internal/battery/...` pass, on a tree
      with RM7 tier 2 applied.
- [x] 4.2 `go test ./internal/battery/...` passes.
- [x] 4.3 `grep -rn "OdometerKm()\|BatteryRangeKm()\|1.609344\|milesToKm" internal/battery` returns
      nothing.
- [x] 4.4 Do NOT run `go build ./...` as this tier's gate — `internal/gateway` is still expected to
      fail until RM7 tier 4 lands (RM7 Decision 8).
- [x] 4.5 `openspec validate battery-adopt-snapshot-unit-fields --strict` passes.
