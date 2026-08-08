# Tasks: gateway-add-supercharger-stats

> Backlog item #4. Consumes the existing, unmodified `telemetry.SuperchargerReader` port — no
> telemetry-module task exists in this list. Sub-tasks are grouped so disjoint-file groups can
> run in parallel once their `depends_on` group is done. Verification gate: `make check` (build
> + vet + ui-guard + tests). Keep this file's checkboxes updated live as tasks complete
> (`openspec/config.yaml` rule); update `progress.json` alongside it.

## A. DI wiring — `SuperchargerReader` through `Deps`

*No dependencies — can start immediately, in parallel with group B (disjoint files).*

- [x] A.1 Add `SuperchargerReader telemetry.SuperchargerReader` field to `gateway.Deps` in
  `internal/gateway/gateway.go`, with a doc comment mirroring `TelemetryReader`'s ("NEVER import
  internal/telemetry/db — all access through this interface only").
- [x] A.2 Add the same field to `handlers.Deps` and to the `Handler` struct (unexported
  `superchargerReader`) in `internal/gateway/handlers/handlers.go`; thread it through `New(d
  Deps)`.
- [x] A.3 Thread `d.SuperchargerReader` from `gateway.Deps` into the `handlers.Deps{...}` literal
  inside `gateway.NewEngine`.
- [x] A.4 In `cmd/web/main.go`, add `SuperchargerReader: telemetry.NewSuperchargerReader(pool),`
  to the `gateway.Deps{...}` literal passed to `gateway.NewEngine` (one line, zero business
  logic — the explicitly granted path for this change).

## B. View model types (fragments package)

*No dependencies — can start immediately, in parallel with group A (disjoint files).*

- [x] B.1 Add `SuperchargerStatsView`, `SuperchargerTiles`, `SuperchargerRowVM` types in
  `internal/gateway/templates/fragments/` (design.md "View model" section) — all fields
  pre-computed strings/ints, no domain-type methods reachable from the template. Reuse the
  existing `fragments.HistoryChart`/`fragments.HistoryBar` types for the chart field verbatim —
  do not introduce a new chart-bar type.
- [x] B.2 Add `superchargerMonthPresets = []int{3, 6, 12}` and `defaultSuperchargerMonths = 6` as
  named handler constants, and a `clampSuperchargerMonths(raw string) int` helper, mirroring
  `historyDayPresets`/`defaultHistoryDays`/`clampHistoryDays` exactly (D5).
- [x] B.3 Add `const superchargerReadLimit = 500` as a named handler constant (D6).

## C. Handler logic + unit tests

*Depends on: A (needs `superchargerReader` on `Handler`), B (needs the view-model types and
constants).*

- [ ] C.1 Add `buildSuperchargerStatsView(ctx, uid, teslaID int64, months int, since time.Time)
  fragments.SuperchargerStatsView` in `internal/gateway/handlers/` (mirrors `buildHistoryView`):
  calls `h.superchargerReader.SuperchargerSessionsByVehicle(ctx, uid, teslaID,
  superchargerReadLimit)` — **one read** — then, on success, filters to `ChargeStartDateTime >=
  since` and builds the view model in Go (D5 window math stays in the caller). On a reader
  error, log and return an empty/degraded view (never propagate to a 500).
- [ ] C.2 Implement the tiles computation (D7): Sessions = `len(filtered)`; Energy = sum of
  non-nil `EnergyKWh`; Avg kWh/session = Energy / count(non-nil `EnergyKWh`), zero-guarded;
  Cost = per-currency map built only from sessions with non-nil `TotalCost` AND non-nil
  `Currency` (D4), rendered as one formatted line per currency, sorted deterministically (e.g.
  by currency code) so output order is stable across renders.
- [ ] C.3 Implement the kWh-per-month chart bucketing: group filtered sessions by calendar month
  of `ChargeStartDateTime`, sum non-nil `EnergyKWh` per bucket, compute each bar's `HeightPct`
  relative to the tallest bucket (mirrors `buildOdometerChart`'s max-relative height math), and
  a `<title>`-ready tooltip string per bar (e.g. `"<month> · <kWh> kWh"`). Empty when the
  filtered slice is empty.
- [ ] C.4 Implement the sessions table row mapping: one `SuperchargerRowVM` per filtered session
  (unpaginated, D7) — date label, site label, country code, energy label (`"N.NN kWh"` or
  `"—"`), cost label (`"N.NN <currency>"` or `"—"`), billing type.
- [ ] C.5 Add `SuperchargerStatsPage(c *gin.Context)` and `SuperchargerStatsFragment(c
  *gin.Context)` handlers: auth guard (redirect `/login` for anonymous) →
  `resolveSelectedVehicle` (empty-state render when `false`, no vehicle) →
  `clampSuperchargerMonths(c.Query("months"))` → compute `since` (month-floor, `months` back) →
  `buildSuperchargerStatsView` → render (full page vs. fragment, mirroring
  `DashboardHistoryFragment`'s two-entry-point shape).
- [ ] C.6 Handler tests with a fake `telemetry.SuperchargerReader`: months clamp
  (missing/invalid/out-of-set → 6; 3/6/12 pass); correct `since` computed; tiles math (Sessions,
  Energy skip-nil, Avg divide-by-zero guard); D4 multi-currency cost lines never summed; D4
  nil-cost/nil-currency sessions excluded from Cost but counted in Sessions/Energy; D2
  unattributed sessions (`TeslaID == nil` in the fake data, which the fake's
  `SuperchargerSessionsByVehicle` never returns to a teslaID filter, mirroring the real port's
  contract) never appear; empty-state on zero sessions in window; reader-error degrades (no
  500); anonymous → redirect `/login`; no-selected-vehicle → empty state.

## D. Templates (Templ)

*Depends on: B (needs the view-model field names/types to reference).*

- [ ] D.1 Add a `superchargerMonthsSelector(active int, presets []int)` Templ component — a
  DaisyUI `join` of buttons (mirrors `historyDaysSelector`), each button `hx-get`ting
  `/ui/supercharger-stats?months=N`, `hx-target` the stats region, `hx-swap="innerHTML"`.
- [ ] D.2 Add a `superchargerTiles(t fragments.SuperchargerTiles)` component: a `ui.StatTile` row
  (Sessions, Energy, Cost, Avg kWh/session), composing `ui.StatTile` — never inline DaisyUI
  `stat` classes. The Cost tile renders each `CostLines` entry on its own line inside the
  `Desc`/value area (no template-side currency math — the lines arrive pre-formatted).
- [ ] D.3 Add a `superchargerChart(chart fragments.HistoryChart)` component reusing the existing
  `historyBarChart`-style SVG rendering (or call the existing `historyBarChart` directly if its
  package-visibility allows reuse across files in the same `fragments` package) — one `<rect>`
  per bar at its pre-computed `HeightPct`, DaisyUI semantic fill token, `<title>` tooltip. Falls
  back to the existing `dashHistoryEmpty()`-style placeholder when `chart.Empty`.
- [ ] D.4 Add a `superchargerTable(sessions []fragments.SuperchargerRowVM)` component composing
  `ui.Table` (headers: Date, Site, Country, Energy, Cost, Billing Type) with one `<tr>` per
  `SuperchargerRowVM` — no arithmetic, no conditionals beyond simple presence display.
- [ ] D.5 Add `internal/gateway/templates/pages/supercharger_stats.templ`: `templ
  SuperchargerStatsPage(v fragments.SuperchargerStatsView)` using `layouts.BaseAuth` + `ui.
  PageHeader`, wrapping the month selector + tiles + chart + table inside a
  `#supercharger-stats-content` region marked with `@templ.Fragment("supercharger-stats")`
  (mirrors the `#charges-content` / `ChargePage` shape) so the same tree serves both the full
  page and the fragment route.
- [ ] D.6 Run `make templ` (pinned `go tool templ generate`) so `*_templ.go` regenerates; run
  `make css` if any new DaisyUI/Tailwind class was introduced. Confirm `make check`'s ui-guard
  passes (semantic tokens only, no hardcoded colors).

## E. Routes + nav

*Depends on: C (handlers must exist), D (page component must exist).*

- [ ] E.1 Register `r.GET("/supercharger-stats", h.SuperchargerStatsPage)` and `r.GET(
  "/ui/supercharger-stats", h.SuperchargerStatsFragment)` in `internal/gateway/gateway.go`,
  alongside the other authenticated `/ui/*` routes.
- [ ] E.2 In `internal/gateway/templates/layouts/nav.go`, replace the `{Label: "Supercharger
  Stats", Icon: "analytics", Placeholder: true}` entry with `{Label: "Supercharger Stats", Href:
  "/supercharger-stats", Active: active == "/supercharger-stats", Icon: "analytics"}`. Leave the
  "Settings" placeholder entry untouched.

## F. Tests + verification

*Depends on: C, D, E all complete.*

- [ ] F.1 A render test asserting the fragment contains the responsive `<svg viewBox` + `<title>`
  tooltips, the month selector marks the active preset, and the table row count matches the
  Sessions tile count for a given fake dataset (single-source-of-truth check, D7).
- [ ] F.2 A render test for the D2 scenario: a fake dataset containing a session the fake reader
  would only return for a matching `TeslaID` filter (i.e. simulate the port's own contract —
  the fake's `SuperchargerSessionsByVehicle` never returns an unattributed session to any
  `teslaID` filter) confirms no such session appears in the rendered table or tile counts.
- [ ] F.3 A route test confirming `GET /supercharger-stats` and `GET /ui/supercharger-stats`
  both redirect anonymous callers to `/login`.
- [ ] F.4 `make check` passes (build + vet + ui-guard + tests). No DB, no migration — confirm no
  `internal/telemetry/db` changes were introduced by this change.
