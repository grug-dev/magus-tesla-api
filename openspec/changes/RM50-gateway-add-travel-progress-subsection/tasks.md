# Tasks — RM50-gateway-add-travel-progress-subsection

All work is inside `internal/gateway`. Full design rationale for D1–D9 lives in
`design.md`; every task below names the decision IDs it implements. Groups T1, T2 and T3
touch disjoint files and have no dependency on each other — separate agents can implement
them in parallel. T4 needs all three (it composes their outputs in the template). T5 and
T6 are sequential after T4.

## T1. `ui/` kit additions (D3, D4, RD11) — no dependencies

- [x] 1.1 In `internal/gateway/templates/ui/icon.templ`: add `case "trending_up"` and
      `case "trending_down"` to `iconMarkup`'s switch, using the path data in design.md
      D4. Update `IconProps.Name`'s doc-comment vocabulary list to include both names.
- [x] 1.2 In `internal/gateway/templates/ui/stat_tile.templ`: add `Trend string` to
      `StatTileProps` (doc comment per design.md D3). Wrap the existing `stat-value` div
      and a new `statTrendIcon(p.Trend)` call in a `flex items-center gap-1` row. Add the
      `statTrendIcon` templ function (design.md D3) — `"up"` → `trending_up` icon,
      `text-success`; `"down"` → `trending_down` icon, `text-error`; default → nothing.
- [x] 1.3 Run `make templ` to regenerate `icon_templ.go` and `stat_tile_templ.go`. Run
      `make css` — no new Tailwind utility is introduced by this task, but the module's
      own regeneration cheatsheet requires it after any `.templ` edit.
- [x] 1.4 `go build ./...` and `go vet ./...` to confirm the new field compiles and every
      existing `ui.StatTile(` call site (grep `internal/gateway/templates` for it) still
      compiles unchanged (they don't set `Trend`, so it defaults to `""` — no rendering
      change, per design.md D3's own verification note).

## T2. i18n catalogue (D6) — no dependencies

- [x] 2.1 In `internal/gateway/i18n/catalog.go`, add six `Key` constants + six `catalog`
      entries (`ES`/`EN` on the same line each, design.md D6's table verbatim) in the
      existing `dashboard.*` namespace, next to the other `KeyDashboard*` entries.
- [x] 2.2 No new test needed — `TestCatalog_AllKeysHaveBothLanguages` covers the six new
      keys automatically. Confirm by inspection that every new line has both languages
      non-empty (mirrors `RM42-gateway-add-theme-selector` T2.2's precedent).

## T3. View model + handler mapping (D2) — no dependencies

- [x] 3.1 In `internal/gateway/templates/fragments/dashboard_vm.go`, add
      `DistanceTraveled string` and `BatteryUsed string` to `DashboardData` (doc comments
      per design.md D2).
- [x] 3.2 In `internal/gateway/handlers/handlers.go`, add `dashDistanceOrDash(v
      *float64) string` and `dashBatteryUsedOrDash(v *float64) string` (design.md D2,
      verbatim). Add the two mapping lines to `mapDashboardSnapshot`.
- [x] 3.3 In `internal/gateway/handlers/handlers_test.go`, extend
      `TestMapDashboardSnapshot_FixtureFull` and `TestMapDashboardSnapshot_FixtureNil`
      per design.md's Test Contract — add `DistanceTraveledKmCalc`/`ConsumedPct` to the
      full fixture, assert `"45 km"`/`"12.3%"`; assert `"—"`/`"—"` on the nil fixture (no
      fixture change needed there, only new assertions). Offline test, no `DATABASE_URL`.
- [x] 3.4 `go build ./...` and `go vet ./...` to confirm the new fields, formatters and
      test assertions compile.

## T4. Panel layout (D1) — depends on T1, T2, T3

- [ ] 4.1 In `internal/gateway/templates/pages/dashboard.templ`, replace the image +
      `grid-cols-2 lg:grid-cols-4` flat tile block inside the `vehicle-status` card with
      the Option B inner grid from design.md D1: left column (image + Odometer +
      MaxRangeCharges, stacked), right column (`travel-progress` section, a `//` comment
      marking the tier-4 Tire-pressure insertion point, `interior-exterior` section).
      Keep the badge row and the "Last updated" footnote exactly where they are today.
- [ ] 4.2 Run `make templ && make css`. Confirm `git diff --stat
      internal/gateway/static/app.css` shows a change (new `md:col-span-4`/`-8`,
      `shrink-0` utilities).
- [ ] 4.3 `go build ./...`, `go vet ./...`, `gofmt -l internal/gateway`.
- [ ] 4.4 `make ui-guard` and `make i18n-guard` — both must pass per design.md D8.

## T5. Docs (D9) — depends on T4

- [ ] 5.1 `internal/gateway/AGENTS.md` — add the short note from design.md D9: the
      Vehicle Status panel's new subsection layout, plus `StatTileProps.Trend` and the
      two new `Icon` glyphs.
- [ ] 5.2 `kkpa/context/use-case/gateway/read-dashboard-bento.md` — correct the stat-row
      description and the "nine pointer fields" count per design.md D9.
- [ ] 5.3 `kkpa/context/input-port/gateway/dashboard.md` — correct the page Description
      row's tile-layout sentence per design.md D9.
- [ ] 5.4 Confirm no other KB file describes this page's old flat tile row: `grep -rl
      "dashboard.*tile\|stat.tile" kkpa/context/` and check any hit.

## T6. Verification (do not run the suite — see Test-Execution-Policy) — depends on T5

- [ ] 6.1 `go build ./...`
- [ ] 6.2 `go vet ./...` (compiles the new/extended `_test.go` files too)
- [ ] 6.3 `gofmt -l internal/gateway` (expect no output)
- [ ] 6.4 `make ui-guard`
- [ ] 6.5 `make i18n-guard`
- [ ] 6.6 `make templ && make css`, confirm no uncommitted diff remains afterward
- [ ] 6.7 Hand back to the owner: `go test ./internal/gateway/...` (the two extended
      fixture tests) and `make check` — Claude does not run either.
