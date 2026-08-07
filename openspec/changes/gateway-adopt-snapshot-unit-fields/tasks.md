# Tasks — gateway-adopt-snapshot-unit-fields

> RM7 tier 4 of 4 — the tier that **restores `go build ./...`**. Single module
> (`internal/gateway`). **Depends on RM7 tier 2** (`telemetry-store-display-units`) being applied.
>
> Independent of RM7 tier 3 (`battery-adopt-snapshot-unit-fields`) — disjoint files, can run in
> parallel — but this tier's group 5 gate cannot pass until tier 3 has also landed, since it
> builds the whole tree.
>
> Groups 1 and 2 touch different files (`handlers.go` vs `history.go`) and can be split across
> parallel workers; group 3 depends on both; groups 4 and 5 gate everything.

## 1. Card and dashboard handlers (`handlers.go`)

- [x] 1.1 In `mapVehicles` (`handlers.go:311,313`), change `snap.BatteryRangeKm()` and
      `snap.OdometerKm()` to the field reads. Leave the `"%.1f km"` format strings exactly as they
      are — the unit literal was already correct (design D1).
- [x] 1.2 In `mapVehicles` (`handlers.go:314-315`), adopt the renamed temperature fields
      (`snap.InsideTemp` → `snap.InsideTempC`, `snap.OutsideTemp` → `snap.OutsideTempC`).
- [x] 1.3 In `mapDashboardSnapshot` (`handlers.go:400,405`), change `formatKm(snap.OdometerKm())`
      and `snap.BatteryRangeKm()` to field reads.
- [x] 1.4 In `mapDashboardSnapshot` (`handlers.go:401-407`), adopt the other renamed fields:
      temperatures, `snap.BatteryLevel` → `snap.BatteryLevelPct`, `snap.ChargeLimitSoc` →
      `snap.ChargeLimitSocPct`.
- [x] 1.5 Grep the whole module for any remaining renamed-field reference this list missed —
      `dashStatus`, `mergeSnapshots`, and the charges handlers also touch `telemetry.Snapshot`.
      Found and fixed one extra spot the list missed: `navHeaderFor` (`handlers.go:659`).
- [x] 1.6 **Keep `formatKm`** (`handlers.go:425`) and its behavior (round to whole, group
      thousands). Rewrite only its doc comment, which currently says the value "arrives in km,
      converted from miles via `Snapshot.OdometerKm()`" — a deleted method (design D2).
- [x] 1.7 Confirm no conversion factor (`1.609344`, `14.503773773`, `milesToKm`, `barToPSI`) is
      introduced anywhere in this module.

## 2. History charts (`history.go`)

- [x] 2.1 In `buildOdometerChart` (`history.go:147`), change
      `pts[i].OdometerKm() - pts[i-1].OdometerKm()` to the field reads.
- [x] 2.2 In `buildOdometerChart` (`history.go:154`), change `odometerKm: pts[i].OdometerKm()` to
      the field read.
- [x] 2.3 In `buildBatteryChart` (`history.go:196`), change `formatKmRaw(s.BatteryRangeKm())` to
      the field read.
- [x] 2.4 Adopt the renamed battery-level field wherever the battery chart reads it.
- [x] 2.5 Confirm the per-day delta arithmetic, the negative-delta-clamps-to-zero rule, the bar
      height percentages, and every tooltip string are unchanged — a delta is chart derivation,
      not unit conversion, and stays in the handler (design D3).
- [x] 2.6 Keep `formatKmRaw` (`history.go:206`) — like `formatKm` it only rounds and never
      converted.

## 3. Tests

- [x] 3.1 Update every `telemetry.Snapshot` literal in `handlers_test.go` and `history_test.go` to
      the renamed fields.
- [x] 3.2 **Fix fixtures whose values are written in miles** (design D-risk):
      `handlers_test.go:231-267` and `:560-609` carry explicit intent comments such as
      `Odometer: 12000.0, // miles → OdometerKm = 12000 * 1.609344` and
      `BatteryRange: 200.0, // miles → ~321.87 km → "322 km"`. Convert each fixture value to the
      kilometre it now represents (or restate the expectation), and delete the stale
      miles→km comments. Converted: 200 mi → 321.8688 km, 12000 mi → 19312.128 km (× 1.609344,
      verified with a throwaway Go program, not by hand).
- [x] 3.3 Replace expectations computed by calling a deleted method — e.g.
      `wantOdometer := fmt.Sprintf("%.1f km", snap.OdometerKm())` at `handlers_test.go:252,259,593`
      — with literal expected strings, so the test asserts an independently-known value rather
      than re-deriving it from the code under test. Removed the now-dead `fmt` import in
      `handlers_test.go` as a result (no remaining `fmt.` call in that file).
- [x] 3.4 Keep the `formatKm` unit tests at `handlers_test.go:677-686` passing unchanged — that
      helper's behavior is not modified by this tier.
- [x] 3.5 Verify the dashboard/vehicle-card assertions still expect the same rendered strings as
      before RM7 (`"19,312 km"`, `"322 km"`, `"22 °C"`). If any expected string changed, tier 2's
      conversion and this tier's field reads disagree — stop and investigate rather than updating
      the expectation. VERIFIED IDENTICAL — see worker final report for the per-assertion table;
      all pass byte-for-byte against pre-RM7 values.

## 4. Docs

- [x] 4.1 Update `internal/gateway/AGENTS.md:188`, which states the template does "no
      `OdometerKm()`/`formatKm`/time calls". The `OdometerKm()` half names a deleted method; the
      `formatKm` half stays true. Restate it as: the template does no arithmetic, no unit
      handling, no formatting and no time calls — the handler has already done all of it.
      Phrased to avoid the literal substring `OdometerKm()` so it doesn't trip task 5.5's grep gate.
- [x] 4.2 Confirm no other gateway doc, template, or view-model comment references a companion
      method or describes a stored value as being in miles. Grepped `internal/gateway` for
      `OdometerKm()|BatteryRangeKm()|TpmsPressure.*PSI()|miles|mph` across `.md`/`.templ`/`.go`
      (excluding generated `_templ.go`); only false positive was "emphasis" (substring `mph`).

## 5. Verification gate (FULL TREE — this is RM7's final gate)

- [x] 5.1 `go build ./...` passes for the whole repository — the build breakage opened by RM7
      tier 2 is closed here (RM7 Decision 8).
- [x] 5.2 `go vet ./...` and `go test ./...` pass.
- [x] 5.3 `make check` passes.
- [ ] 5.4 Run the app against a migrated database and compare the `/dashboard` odometer, range and
      temperature tiles plus both history charts against their pre-RM7 values for the same
      snapshot row. Every number must be identical — RM7 changes where the conversion happens, not
      what the user sees.
- [x] 5.5 `grep -rn "OdometerKm()\|BatteryRangeKm()\|TpmsPressure.*PSI()" internal/gateway internal/battery`
      returns nothing, and the same grep across `internal/` returns only the `internal/tesla`
      adapter definitions.
- [x] 5.6 `openspec validate gateway-adopt-snapshot-unit-fields --strict` passes.
- [ ] 5.7 Mark RM7 complete in `openspec/roadmaps/RM7-store-display-units.progress.json` only
      after 5.1–5.6 all pass.
