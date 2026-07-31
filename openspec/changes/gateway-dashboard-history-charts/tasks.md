# Tasks: gateway-dashboard-history-charts

> RM5 tier 2. **Depends on tier 1** (`telemetry.Reader.SnapshotsByVehicleSince` must exist).
> **Not started** — authored for review per the user's "do not implement". Sub-tasks A–D group by
> concern; A (handler/VM) and B (templates) touch disjoint files and can be done in parallel, then
> C wires them and D documents. Verification gate: `make check` (build + vet + ui-guard + tests).

## A. Handler + view model + route

- [x] A.1 Add `HistoryView` / `HistoryChart` / `HistoryBar` view-model types in
  `internal/gateway/templates/fragments/` (all fields pre-computed strings/ints — no domain-type
  methods reachable from the template).
- [x] A.2 Add `historyDayPresets = []int{6, 14, 30}` and `defaultHistoryDays = 6` as named handler
  constants; add a `clampHistoryDays(raw string) int` helper (parse, reject non-preset → default).
- [x] A.3 Add `DashboardHistoryFragment(c *gin.Context)` in `internal/gateway/handlers/`: auth
  guard (redirect `/login` for anonymous), `resolveSelectedVehicle` → `(accountID, teslaID)`,
  `clampHistoryDays(c.Query("days"))`, `since = startOfDay(now).AddDate(0,0,-days)`, call
  `telemetryReader.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)`.
- [x] A.4 Build the view model from the snapshots: **odometer** = last `days+1` points → `days`
  clamped-non-negative km deltas via `OdometerKm()` + `formatKm`, each with the
  date/km-driven/cumulative tooltip; **battery** = last `days` points → level-% bars via
  `BatteryLevel` (+ `BatteryRangeKm()` in the tooltip). Height % pre-computed. Set `Empty` when a
  chart has 0 bars. Degrade to an empty view (not a 500) on a reader error.
- [x] A.5 Register `GET /ui/dashboard/history` in `internal/gateway/gateway.go` behind the same auth
  middleware as the other `/ui/*` fragments.

## B. Templates (Templ)

- [x] B.1 Add a reusable `historyBarChart(chart fragments.HistoryChart, colorClass string)` Templ
  component: responsive `<svg viewBox=... preserveAspectRatio="none" class="w-full h-24">`, one
  `<rect class={colorClass}>` per bar at its `HeightPct`, a child `<title>` per bar; render
  `dashHistoryEmpty()` when `chart.Empty`. No arithmetic, no method calls — layout only. Use DaisyUI
  semantic fill tokens (no hex).
- [x] B.2 Add a `historyDaysSelector(active int, presets []int)` Templ component: a DaisyUI `join`
  of buttons, the active preset visually marked, each button
  `hx-get="/ui/dashboard/history?days=N"`, `hx-target="#dashboard-history"`, `hx-swap="innerHTML"`.
- [x] B.3 Add a `dashboardHistory(v fragments.HistoryView)` block component that renders the
  selector + the two `ui.Card`s ("Odometer history" / "Battery history"), each wrapping a
  `historyBarChart` (odometer=primary, battery=secondary token). This is what the endpoint returns.

## C. Wire into the dashboard bento

- [x] C.1 In `internal/gateway/templates/pages/dashboard.templ`, replace the two
  `@dashHistoryEmpty()` cards with a single `#dashboard-history` region:
  `hx-get="/ui/dashboard/history?days=6"`, `hx-trigger="load"`, `hx-swap="innerHTML"` (inside the
  existing `#dashboard-content` so it inherits the `vehicle-changed` refresh). Keep
  `dashHistoryEmpty()` in place as the per-chart empty state used by `historyBarChart`.
- [x] C.2 Run `make templ` (or the project's Templ generate step) so `*_templ.go` is regenerated;
  confirm `make check`'s ui-guard passes (no hardcoded colours, semantic tokens only).

## D. Docs + AGENTS.md decision (RD8)

- [x] D.1 `internal/gateway/AGENTS.md`: add the "Charts are hand-rolled SVG — no chart library"
  decision (with the rejected vanilla-JS-lib alternative + reason: Node-less, `viewBox`-responsive,
  AI-efficiency) AND the standing convention that client-side-library / rendering-approach decisions
  must be recorded in this `AGENTS.md` in the same change.
- [x] D.2 If `ai/htmx-conventions.md` documents chart/data-viz patterns, add a one-line pointer to
  the SVG-bar-chart pattern (self-loading region + `<title>` tooltips); otherwise skip (no
  invented section).
- [x] D.3 No root README structure-tree change (no module added/removed/renamed). Confirm the
  dashboard description in any gateway `README.md` mentions the history charts if it enumerates the
  bento cards.

## E. Tests + verification

- [x] E.1 Handler tests (fake `telemetry.Reader`): `days` clamp (missing/invalid/out-of-set → 6;
  6/14/30 pass), correct `since` computed, odometer produces N clamped-non-negative deltas from N+1
  points, battery produces N level bars, empty-state when <2 / 0 points, reader error degrades (no
  500), anonymous → redirect `/login`.
- [x] E.2 A render test asserting the fragment contains the responsive `<svg viewBox` + `<title>`
  tooltips and the selector marks the active preset; and a test that `#dashboard-history` in the
  dashboard page carries `hx-trigger="load"` (self-load) and sits inside `#dashboard-content`.
- [x] E.3 `make check` passes (build + vet + ui-guard + tests). No DB, no migration.
