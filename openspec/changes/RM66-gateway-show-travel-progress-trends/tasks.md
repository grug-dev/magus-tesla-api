> **Scope.** Wires the Travel Progress subsection's three tiles (Distance
> travelled, Battery used, Efficiency) to the real day-over-day deltas tier 2
> added, per D-C: all three show a real direction, but only Efficiency is
> coloured. This dispatch wrote the OpenSpec artifacts only; the tasks below
> are for the implementation dispatch that follows. No database object is
> touched by this tier.
>
> **Dependencies / parallelism:**
> - T1 (`ui/stat_tile.templ`) has no dependencies. MAY start immediately.
> - T2 (`fragments/dashboard_vm.go`) has no dependencies. MAY run in parallel
>   with T1.
> - T3 (`handlers/format.go`) has no dependencies. MAY run in parallel with
>   T1/T2.
> - T4 (`i18n/catalog.go` key rename) has no dependencies. MAY run in
>   parallel with T1/T2/T3.
> - T5 (`handlers/handlers.go`) depends on **T2** (needs the `TravelStatVM`
>   type), **T3** (needs `formatSignedKm`/`formatSignedPct`/
>   `formatSignedKmPerPct`), and **T4** (needs the renamed i18n key).
> - T6 (`templates/pages/dashboard.templ`) depends on **T2** (the three
>   `DashboardData` fields it reads change type) and **T1** (so the new
>   `Trend` values it can now pass actually render).
> - T7 (existing test file: `handlers_test.go` fixture fix + extension)
>   depends on **T5**.
> - T8 (new table-driven offline tests — the full test-contract matrix)
>   depends on **T5**. MAY run in parallel with T7.
> - T9 (`ui` package rendering test for the two new `Trend` values) depends
>   on **T1**. MAY run in parallel with T5-T8.
> - T10 (KB guide correction) has no code dependency and MAY run at any
>   point, but do it last so it can describe the real final shape rather
>   than a moving target.
>
> **Wave placement:** T1, T2, T3, T4 first (all four independent, parallel).
> T5 next (needs T2+T3+T4). T6 next (needs T1+T2). T7, T8, T9 next (need T5
> or T1, may run together). T10 last.

## T1. `ui.StatTileProps.Trend` — the two neutral values — no dependencies

Decisions: D1

- [x] T1.1 In `internal/gateway/templates/ui/stat_tile.templ`, update
      `StatTileProps.Trend`'s doc comment to the five-value table in
      design.md ("The third `Trend` value" section): `""`, `"up"`, `"down"`,
      `"up-neutral"`, `"down-neutral"`. Remove the "a third value is not
      modeled" sentence — it now is, twice over, and say why two values were
      needed instead of one (direction and colour are separate facts here).
- [x] T1.2 In the same file, extend `statTrendIcon`'s `switch` with the two
      new cases from design.md, reusing the existing `trending_up`/
      `trending_down` glyph names (`ui/icon.templ` — unchanged, no new
      glyph) with `text-neutral` instead of `text-success`/`text-error`.
- [x] T1.3 Run `make templ` (regenerates `stat_tile_templ.go`).
      Acceptance: `go build ./internal/gateway/...` still compiles (no other
      file references the new cases yet — this task only adds them).

## T2. `fragments.TravelStatVM` and `DashboardData` field types — no dependencies

Decisions: D5

- [x] T2.1 In `internal/gateway/templates/fragments/dashboard_vm.go`, add the
      `TravelStatVM` struct from design.md (same shape as `TireWheelVM`:
      `Value`, `Trend`, `Delta`), placed next to `TireWheelVM`.
- [x] T2.2 In the same file, change `DashboardData.DistanceTraveled`,
      `.BatteryUsed`, `.Efficiency` from `string` to `TravelStatVM`. Keep
      each field's existing doc comment describing what the value means and
      its nil rule; add one sentence noting `.Trend`/`.Delta` follow
      `TireWheelVM`'s own documented rule, so the rule is not restated a
      second time.
      Acceptance: `go build ./internal/gateway/...` now fails exactly where
      expected — `handlers.go` (three assignment lines) and
      `dashboard.templ`/`dashboard_templ.go` (three `Value:` reads) — and
      nowhere else outside this module. Record the exact failing lines in
      the report; they are T5's and T6's targets.

## T3. `handlers/format.go` — three new signed formatters — no dependencies

Decisions: none

- [x] T3.1 Factor `formatSignedPSI`'s existing body into a new
      `formatSigned1(v float64) string` per design.md, and make
      `formatSignedPSI` call it. Behaviour for every existing input is
      unchanged — this is a pure refactor, not a logic change.
- [x] T3.2 Add `formatSignedPct` and `formatSignedKmPerPct`, both calling
      `formatSigned1` (design.md "New format helpers").
- [x] T3.3 Add `formatSignedKm` (whole-km rounding via `math.Round`, mirrors
      `formatKm`'s own rounding — NOT `formatSigned1`, since it needs zero
      decimals and `formatKm`'s exact rounding behaviour, not
      `strconv.FormatFloat`'s).
      Acceptance: `go build ./internal/gateway/...` and `go vet
      ./internal/gateway/...` both pass with zero new findings in this file.
      `gofmt -l internal/gateway/handlers/format.go` prints nothing.

## T4. i18n key rename — no dependencies

Decisions: D3

- [x] T4.1 In `internal/gateway/i18n/catalog.go`, rename
      `KeyDashboardTireDeltaDesc` (`"dashboard.tire_delta_desc"`) to
      `KeyDashboardDeltaDesc` (`"dashboard.delta_desc"`) — both the `Key`
      constant declaration and its one `catalog` map entry. The two
      translation strings (`ES`/`EN`) do not change, only the Go name and
      key string.
      Acceptance: `grep -rn "KeyDashboardTireDeltaDesc"
      internal/gateway/` returns nothing except the one remaining reference
      in `handlers.go` — which is T5's line to update, not this task's.
      `go build ./internal/gateway/i18n/...` passes.

## T5. `handlers/handlers.go` — the trend/delta mechanism — depends on T2, T3, T4

Decisions: D1, D2, D4, D5

- [x] T5.1 Rename `dashTireTrend` to `dashColoredTrend` (body unchanged).
      Update its doc comment per design.md ("shared by every tyre wheel and
      by Efficiency"). Update its four existing call sites inside
      `dashTireWheel`.
- [x] T5.2 Add `dashNeutralTrend` exactly as specified in design.md.
- [x] T5.3 Generalise `dashTireDelta` into `dashDeltaDesc(ctx
      context.Context, v *float64, format func(float64) string) string` per
      design.md, using the renamed `i18n.KeyDashboardDeltaDesc`. Update
      `dashTireWheel` to call `dashColoredTrend(delta)` and
      `dashDeltaDesc(ctx, delta, formatSignedPSI)`.
- [x] T5.4 Add `dashTravelStat` exactly as specified in design.md ("one small
      builder, mirroring `dashTireWheel`'s own shape").
- [x] T5.5 Update `mapDashboardSnapshot`'s three assignment lines (today
      `vm.DistanceTraveled = dashDistanceOrDash(...)` etc.) to the three
      `dashTravelStat(...)` calls in design.md, reading each tile's matching
      `*DeltaCalc` field alongside its existing value field.
      Acceptance: `go build ./internal/gateway/...` passes. `go vet
      ./internal/gateway/...` passes except for the two pre-existing test
      files T7 targets (record which lines fail and why, do not fix them
      here). `gofmt -l internal/gateway/handlers/handlers.go` prints
      nothing.

## T6. `templates/pages/dashboard.templ` — wire the three tiles — depends on T1, T2

Decisions: D1, D5

- [x] T6.1 Replace the three hardcoded-`Trend` `StatTile` calls at
      `dashboard.templ:81-83` with the `.Value`/`.Trend`/`.Delta` reads from
      design.md's "Template wiring" section, mirroring the four tyre tiles
      two sections below in the same file.
- [x] T6.2 Run `make templ && make css` (new class, if any — `text-neutral`
      is already used elsewhere in this module by `ui.Dot`/`ui.Badge`'s
      `"neutral"` variant, so this is likely a no-op for `app.css`, but run
      it regardless per the module's own regeneration cheatsheet).
      Acceptance: `go build ./internal/gateway/...` compiles clean.
      `make ui-guard` and `make i18n-guard` both pass (no raw DaisyUI class,
      no bypassed `i18n.T`).

## T7. Existing test fixture — `handlers_test.go` — depends on T5

Decisions: D2, D4, D5

- [ ] T7.1 In `TestMapDashboardSnapshot_FixtureFull`
      (`internal/gateway/handlers/handlers_test.go:800-910`), add three
      fields to the fixture `analytics.VehicleStatus` literal:
      `DistanceTraveledKmDeltaCalc: ptrF64(5.0)` (positive),
      `ConsumedPctDeltaCalc: ptrF64(-1.2)` (negative),
      `KmPerPctDeltaCalc: ptrF64(0.0)` (exact zero) — one branch per tile,
      mirroring how the four tyre wheels already spread across
      positive/negative/zero/nil in this same fixture.
- [ ] T7.2 Replace the three now-broken string assertions
      (`vm.DistanceTraveled != "45 km"` etc., lines 883-891) with
      `fragments.TravelStatVM` equality checks, one per tile:
      - `DistanceTraveled`: `{Value: "45 km", Trend: "up-neutral", Delta: "+5 vs prev. day"}`
      - `BatteryUsed`: `{Value: "12.3%", Trend: "down-neutral", Delta: "-1.2 vs prev. day"}`
      - `Efficiency`: `{Value: "2.8 km/%", Trend: "", Delta: "0.0 vs prev. day"}`
      Mirror the existing `wantFL`/`wantFR`/`wantRL`/`wantRR` comparison
      style (a `want*` variable plus an equality check with `%+v`).
- [ ] T7.3 In `TestMapDashboardSnapshot_FixtureNil`, replace the three
      now-broken string assertions (lines 952-961) with
      `fragments.TravelStatVM{Value: "—", Trend: "", Delta: ""}` equality
      checks — every pointer field is already nil in this fixture, so no new
      fixture field is needed, only the assertion shape changes.
      Acceptance: `go vet ./internal/gateway/...` compiles both tests with
      zero references to the old string fields anywhere in the file. Not run
      by Claude (`Test-Execution-Policy`) — report as
      awaiting-user-verification with the exact `go test` command to paste.

## T8. New table-driven offline tests — the full test-contract matrix — depends on T5

Decisions: D2, D4

- [ ] T8.1 Add table-driven tests for `dashNeutralTrend` and
      `dashColoredTrend` in `handlers_test.go` (or a new
      `handlers_trend_test.go`), asserting every row of design.md's two
      trend tables (positive / negative / exact-zero / nil, 4 cases each).
- [ ] T8.2 Add table-driven tests for `formatSignedKm`, `formatSignedPct`,
      `formatSignedKmPerPct` in `format_test.go`, asserting every row of
      design.md's three formatter tables, including the `42.6 → "+43"`
      rounding case for `formatSignedKm`.
- [ ] T8.3 Add a table-driven test for `dashDeltaDesc`, asserting design.md's
      four rows (positive / negative / exact-zero / nil) using
      `formatSignedKm` as the example formatter.
      Acceptance: `go vet ./internal/gateway/...` compiles all new tests.
      Not run by Claude — report as awaiting-user-verification.

## T9. `ui` package — the two new `Trend` values render correctly — depends on T1

Decisions: D1

- [ ] T9.1 Add or extend a `stat_tile_test.go` (or wherever this component's
      existing rendering tests live, if any) asserting design.md's
      `statTrendIcon` table: `"up-neutral"`/`"down-neutral"` each render
      exactly one `<svg class="h-5 w-5 text-neutral shrink-0">`, matching
      the assertion style already used (if any) for `"up"`/`"down"`. Assert
      the class list and element count only — never path data, colour hex,
      or element order (`ai/go-conventions.md` §Testing).
      Acceptance: `go vet ./internal/gateway/...` compiles. Not run by
      Claude — report as awaiting-user-verification.

## T10. KB guide correction — no code dependency, do last

Decisions: D6

- [ ] T10.1 In `kkpa/context/use-case/gateway/read-dashboard-bento.md:242`,
      replace the bullet stating "the two Travel Progress trend icons are
      fixed, never computed" with the real behaviour: all three tiles now
      compute a real direction from their own day-over-day delta; only
      Efficiency's is coloured (good/bad), Distance travelled and Battery
      used render the same direction in a shared neutral colour. Keep the
      surrounding bullets' style (short sentence, `_Source: spec gateway —
      ...>` citation line pointing at the "Dashboard Travel Progress
      Subsection" requirement).
      Acceptance: `grep -n "fixed, never computed"
      kkpa/context/use-case/gateway/read-dashboard-bento.md` returns
      nothing.
