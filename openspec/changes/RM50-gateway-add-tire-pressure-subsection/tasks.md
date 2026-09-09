# Tasks — RM50-gateway-add-tire-pressure-subsection

All work is inside `internal/gateway`. Full design rationale for D1–D10 lives in
`design.md`; every task below names the decision IDs it implements. Groups T1 and T2
touch disjoint files and have no dependency on each other — separate agents can
implement them in parallel. T3 needs both (it composes their outputs in the template).
T4 and T5 are sequential after T3.

## T1. Formatters + view model + handler mapping (D2, D3, D4, D6) — no dependencies

- [ ] 1.1 In `internal/gateway/handlers/format.go`, add `formatPSI(psi float64)
      string` and `formatSignedPSI(v float64) string` (design.md D4, verbatim), next
      to the existing `formatKm`/`formatPctRaw`.
- [ ] 1.2 In `internal/gateway/templates/fragments/dashboard_vm.go`, add the
      `TireWheelVM` struct (design.md D2, with its doc comment) and the four fields
      `TirePressureFL`/`FR`/`RL`/`RR` on `DashboardData`.
- [ ] 1.3 In `internal/gateway/handlers/handlers.go`, add `dashPSIOrDash`,
      `dashTireTrend`, `dashTireDelta`, and `dashTireWheel` (design.md D3, verbatim,
      with their doc comments — D5 explains the zero-vs-nil distinction these
      functions must implement correctly). Add the four mapping lines to
      `mapDashboardSnapshot` (design.md D6).
- [ ] 1.4 `go build ./...` and `go vet ./...` to confirm the new struct, fields,
      formatters and mapping lines compile.

## T2. i18n catalogue (D8) — no dependencies

- [ ] 2.1 In `internal/gateway/i18n/catalog.go`, add seven `Key` constants + seven
      `catalog` entries (`ES`/`EN` on the same line each, design.md D8's table
      verbatim) in the existing `dashboard.*` namespace, next to the
      `KeyDashboardTravelProgress*`/`KeyDashboardInteriorExterior*` entries tier 2
      added.
- [ ] 2.2 No new test needed — `TestCatalog_AllKeysHaveBothLanguages` covers the seven
      new keys automatically. Confirm by inspection that every new line has both
      languages non-empty.

## T3. Panel layout (D7) — depends on T1, T2

- [ ] 3.1 In `internal/gateway/templates/pages/dashboard.templ`, replace the tier-4
      placeholder `//` comment with the `tire-pressure` `<section>` block from
      design.md D7 — one `ui.SectionHeader` plus a `grid grid-cols-2 gap-3` of four
      `ui.StatTile`s (FL, FR, RL, RR order), each wired to its `TireWheelVM` field's
      `Value` (through the existing `dashStat(d.HasSnapshot, ...)` wrapper),
      `Trend`, and `Delta` (as `Desc`).
- [ ] 3.2 Run `make templ && make css`. No new Tailwind utility is introduced (the
      tile grid reuses the sibling subsections' existing `grid grid-cols-2 gap-3`
      class), but the module's own regeneration cheatsheet requires the step after
      any `.templ` edit.
- [ ] 3.3 `go build ./...`, `go vet ./...`, `gofmt -l internal/gateway`.
- [ ] 3.4 `make ui-guard` and `make i18n-guard` — both must pass per design.md D9.

## T4. Tests (Test Contract) — depends on T1, T3

- [ ] 4.1 In `internal/gateway/handlers/handlers_test.go`, extend
      `TestMapDashboardSnapshot_FixtureFull` per design.md's Test Contract table —
      add the four wheels' raw/delta values to the fixture (FL positive, FR negative,
      RL exact zero, RR nil/nil) and assert every wheel's `Value`/`Trend`/`Delta`
      against the table's expected values.
- [ ] 4.2 Extend `TestMapDashboardSnapshot_FixtureNil` — no fixture change needed
      (every pointer field is already nil); add assertions that all four
      `TirePressure*` fields equal the zero-value `TireWheelVM{Value: "—"}`.
- [ ] 4.3 `go build ./...` and `go vet ./...` to confirm the extended tests compile.

## T5. Docs (D10) — depends on T3

- [ ] 5.1 `internal/gateway/AGENTS.md` — add the short note from design.md D10: the
      Vehicle Status panel's three named subsections are now all built (Travel
      Progress, Tire pressure, Interior/Exterior) — the tier-4 placeholder this file
      may still reference from tier 2 is superseded.
- [ ] 5.2 `kkpa/context/use-case/gateway/read-dashboard-bento.md` — correct the
      stat-row description (the tier-2 gap is now filled) and the
      `mapDashboardSnapshot` pointer-field count per design.md D10.
- [ ] 5.3 `kkpa/context/input-port/gateway/dashboard.md` — correct the page
      Description row to name all three subsections per design.md D10.
- [ ] 5.4 Confirm no other KB file describes this page's tile layout as having a gap:
      `grep -rl "dashboard.*tile\|stat.tile\|tire.pressure" kkpa/context/` and check
      any hit.

## T6. Verification (do not run the suite — see Test-Execution-Policy) — depends on T4, T5

- [ ] 6.1 `go build ./...`
- [ ] 6.2 `go vet ./...` (compiles the extended `_test.go` file too)
- [ ] 6.3 `gofmt -l internal/gateway` (expect no output)
- [ ] 6.4 `make ui-guard`
- [ ] 6.5 `make i18n-guard`
- [ ] 6.6 `make templ && make css`, confirm no uncommitted diff remains afterward
- [ ] 6.7 Hand back to the owner: `go test ./internal/gateway/...` (the two extended
      fixture tests) and `make check` — Claude does not run either.
