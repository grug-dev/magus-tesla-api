## 1. Param parser + view-model changes (handler + history_vm.go)

- [x] 1.1 In `internal/gateway/handlers/history.go`, replace the `historyDayPresets` var, the
      `defaultHistoryDays = 6` const, and the `clampHistoryDays(raw string) int` func with:
      a `historyRangeWindowDays = 6` const (the default window size in days) and a
      `parseHistoryRange(c *gin.Context) (start, end time.Time, ok bool)` helper implementing
      design D1 — ISO `YYYY-MM-DD` parse, both-absent → default 6-day window
      (`end = startOfDay(time.Now())`, `start = end.AddDate(0,0,-historyRangeWindowDays)`),
      missing-partner / malformed / `end.Before(start)` / `end.After(startOfDay(time.Now()))` /
      `window > 90 days` → `ok = false` (400). Keep `startOfDay` (already present). Depends on:
      nothing (first wave; can run in parallel with 1.2 since they touch disjoint symbols).
- [x] 1.2 In `internal/gateway/templates/fragments/history_vm.go`, replace `HistoryView.Days int`
      and `HistoryView.Presets []int` with `HistoryView.Start time.Time`, `HistoryView.End time.Time`
      (the validated requested window, for the template to format the active window's dates), and
      `HistoryView.Presets []RangePreset`. Add a `RangePreset` struct: `Label string` ("6 days" /
      "14 days" / "30 days"), `Start time.Time` (`today-N` UTC midnight), `End time.Time` (today
      UTC midnight), `Active bool` ((Start, End) matches the requested window). Add a `Present bool`
      field to `HistoryBar` (false for a missing-day empty labeled slot; true for a bar backed by a
      snapshot). Update the doc comments to describe the fixed `[start..end]` axis and the
      date-range contract. The `HistoryChart` struct keeps `Bars`/`Empty`/`LabelVertical`
      (unchanged from RM7 tier 2). Depends on: nothing (first wave; disjoint symbols from 1.1).

## 2. Fixed-axis bucketing rewrite (buildHistoryView / buildOdometerChart / buildBatteryChart)

- [x] 2.1 In `internal/gateway/handlers/history.go`, rewrite `DashboardHistoryFragment`: after the
      auth guard and `resolveSelectedVehicle`, call `start, end, ok := parseHistoryRange(c)`; on
      `!ok` return HTTP 400 with the `dashHistoryEmpty()` placeholder rendered into
      `#dashboard-history` (no preset selector on a malformed request). On `ok`, compute
      `readStart := start.AddDate(0, 0, -1)` and call
      `h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`. Build
      `Presets []RangePreset` for `n ∈ {6, 14, 30}` with `End = startOfDay(now)`,
      `Start = End.AddDate(0,0,-n)`, `Active = start.Equal(Start) && end.Equal(End)`. Replace the
      no-vehicle empty-state branch to use the new VM shape (set `Start`/`End` to the default
      window, `Presets` to the absolute-date presets, both charts `Empty = true`). Depends on: 1.1,
      1.2.
- [x] 2.2 Rewrite `buildHistoryView(ctx, uid, teslaID, start, end time.Time)` (drop the `days int`
      and `since time.Time` params): put `Start`, `End`, `Presets` on the returned `HistoryView`;
      fetch via `SnapshotsByVehicleBetween(ctx, uid, teslaID, start.AddDate(0,0,-1), end)` (the
      1-day lookback, design D2); on reader error degrade both charts to `{Empty: true}` (log +
      return, no 500 — preserve existing resilience). Depends on: 2.1.
- [x] 2.3 Rewrite `buildOdometerChart(snaps, start, end time.Time)` (drop the `days int` param) to
      bucket by `EffectiveDate` into a fixed `[start..end]` axis (design D3): allocate `numDays =
      int(end.Sub(start).Hours()/24) + 1` slots (one per calendar day, inclusive `end`), label each
      slot `day.Format("01-02")`; build a `map[time.Time]telemetry.Snapshot` keyed by
      `effectiveDayUTC(startOfDay(s.EffectiveDate))` over the returned snaps; for each day
      `d ∈ [start, end]`, the bar's km delta is `snap[d].OdometerKm - snap[d-1].OdometerKm` when
      both exist (the `start-1` lookback snapshot seeds the first `start` bar's delta), clamped to
      zero on negative; if either `d` or `d-1` has no snapshot, emit an empty labeled bar
      (`Present=false`, `HeightPct=0`, tooltip `"<MM-DD> · no snapshot"`). The odometer chart's
      `Empty` fires only when fewer than 2 snapshots total exist in `[start-1..end]` (no delta
      possible). Set `LabelVertical = labelVerticalFor(numDays)` (retargeted — design D3). Return a
      `HistoryChart` with **exactly `numDays` bars**. Depends on: 2.2.
- [x] 2.4 Rewrite `buildBatteryChart(snaps, start, end time.Time)` (drop the `days int` param) to
      bucket by `EffectiveDate` into the same fixed `[start..end]` axis: one slot per calendar
      day, `HeightPct = snap.BatteryLevelPct` if a snapshot exists for the day else `0` with
      `Present=false`; tooltip for a present bar `"<MM-DD> · <level>% · <range> km range"`, for a
      missing-day bar `"<MM-DD> · no snapshot"`; `Empty` fires only when zero snapshots exist in
      `[start..end]` (a partial axis is NOT empty). Set `LabelVertical = labelVerticalFor(numDays)`.
      Return a `HistoryChart` with **exactly `numDays` bars** so the odometer and battery label
      slices are identical by construction. Depends on: 2.2.
- [x] 2.5 Retarget `labelVerticalFor(numBars int) bool` (design D3): return `true` when
      `numBars >= 14` (the wide/narrow split keyed on the fixed window's bar count, preserving RM7
      tier 2's adaptive-orientation invariant on the new axis). Update its doc comment to say it
      takes the number of bars in the window (6 → horizontal, ≥14 → vertical), not a `days` count.
      Depends on: 2.3, 2.4 (both call it with `numDays`).

## 3. Preset selector + dashboard template (history.templ, dashboard.templ, Refresh removal)

- [x] 3.1 In `internal/gateway/templates/fragments/history.templ`, rewrite `historyDaysSelector`
      to render from `[]RangePreset` (design D4): iterate `v.Presets`; each button's `hx-get` is
      `fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s", p.Start.Format("2006-01-02"),
      p.End.Format("2006-01-02"))` — server-rendered absolute dates; the active preset uses the
      `primary` variant (matching `p.Active`), the rest `ghost`. Keep `hx-target="#dashboard-
      history"` + `hx-swap="innerHTML"`; keep the `ui.Join()` grouping and the button copy
      (`p.Label`). No `days` param anywhere. Depends on: 1.2 (the `RangePreset` struct), 2.1 (the
      handler builds `Presets`).
- [x] 3.2 In the same file, update `historyBarChart` to render a missing-day bar (`Present=false`)
      as a zero-height `<rect>` plus its `MM-DD` label cell in the existing HTML grid (the grid's
      `grid-template-columns: repeat(len(chart.Bars), 1fr)` already derives from the bar count, so
      a `numDays`-cell label row is automatic — no structural change to the grid). Keep the SVG
      `viewBox="0 0 <numBars> 100"` + `preserveAspectRatio="none"` + `w-full h-24` (RD7
      unchanged). Use DaisyUI semantic tokens (`fill-primary`/`fill-secondary`); no hardcoded hex.
      No `time.Format` in the template (the handler pre-formatted every `Label` and every `Tooltip`).
      Depends on: 1.2 (`Present` field), 2.3 (odometer bars), 2.4 (battery bars).
- [x] 3.3 In `internal/gateway/templates/pages/dashboard.templ`, **remove the Refresh button**
      (design D5): delete the `@ui.Button(ui.ButtonProps{Variant: "ghost", Href: "/dashboard",
      Class: "btn-sm"}) { Refresh }` block inside the `@ui.PageHeader` (currently lines 24-26). The
      `@ui.PageHeader` keeps `Title`/`Subtitle` and no trailing button. Switch the `#dashboard-
      history` self-load `hx-get` from `?days=6` to a server-rendered absolute
      `?start=<today-6>&end=<today>` — pass the pre-formatted href string in via
      `fragments.DashboardData` (the dashboard page handler computes it once; the template emits
      it verbatim, preserving the logic-free-template invariant). Do NOT touch the charges-list
      Refresh (`fragments/charges_list.templ`). Depends on: 1.2 (VM shape if the dashboard data
      struct gains the default-history-href field).

## 4. Convention doc (AGENTS.md, go-conventions.md)

- [x] 4.1 In `internal/gateway/AGENTS.md`, add a new "HTTP date-filter convention" section
      (design D6, Decision #6) stating: every date-filtered gateway HTTP endpoint takes
      `?start=YYYY-MM-DD&end=YYYY-MM-DD` (both whole calendar days, UTC-midnight-bounded, `end`
      inclusive), never a `?days=N` count; default window endpoint-specific (dashboard history
      default 6 days → `today-6..today`); validate via a `parseHistoryRange`-style helper — 400
      on malformed / `end<start` / `end>today` / window>90d; the bounded window protects the hot
      read path (read-heavy Performance-Profile); `GET /ui/dashboard/history` is the reference
      implementation (RM8-gateway-history-date-range, Linear MAG-7); every future date-filtered
      endpoint follows the same contract. Place the section adjacent to the "Read-only at request
      time" / "Vehicle-scoped reads" sections. Per CLAUDE.md's "Docs track structural change" rule
      this lands in the SAME change as the code. Depends on: 2.1 (the parser exists to document).
- [x] 4.2 In `ai/go-conventions.md`, add a one-line pointer (design D6): a new "HTTP date-filter
      convention (gateway)" bullet under the Coding Rules / Architecture section pointing to the
      `internal/gateway/AGENTS.md` "HTTP date-filter convention" section. One line, no duplication
      of the canonical text. Depends on: 4.1.

## 5. Codegen (make templ + make css)

- [x] 5.1 Run `make templ` (the pinned `go tool templ generate`) to regenerate
      `internal/gateway/templates/fragments/history_templ.go` and
      `internal/gateway/templates/pages/dashboard_templ.go` from the edited `.templ` files. Confirm
      `git status` shows the generated `*_templ.go` files changed. Depends on: 3.1, 3.2, 3.3.
- [x] 5.2 Run `make css` (Node-less Tailwind CLI) to regenerate `internal/gateway/static/app.css`
      for any new/changed DaisyUI or Tailwind class introduced in 3.1/3.2/3.3. The empty-day bar's
      zero-height `<rect>` adds no new class (it reuses the existing `fill-primary`/
      `fill-secondary`); the only new attribute is the `?start=...&end=...` href (no class). If
      `make css` reports no diff, that is the expected result — still run it to satisfy the CI guard
      `make css && git diff --exit-code internal/gateway/static/app.css`. Commit `static/app.css`
      in the same change. Depends on: 5.1.
- [x] 5.3 `make generate` (runs sqlc + templ + css together) may be used in place of 5.1+5.2 as a
      single step; sqlc is a no-op for the gateway (no `queries.sql` touched) but the combined
      target guarantees nothing is missed. Depends on: 3.3 (all `.templ` edits done).

## 6. Tests (rewrite history_test.go for new parser, 400 cases, fixed-axis, both charts share dates)

- [x] 6.1 Replace the `TestClampHistoryDays_*` tests with `TestParseHistoryRange_*` covering design
      D1: both-absent → default 6-day window (`end == startOfDay(now)`, `start ==
      end.AddDate(0,0,-6)`, `ok == true`); valid `?start=2026-08-03&end=2026-08-07` → `(start, end,
      ok == true)`; malformed `?start=08-07` → `ok == false`; missing partner `?start=2026-08-03`
      → `ok == false`; `end < start` → `ok == false`; `end > today` → `ok == false`; window > 90
      days → `ok == false`. These are pure-function unit tests (no DB, no engine). Depends on: 1.1.
- [x] 6.2 Replace the `fakeHistoryReader` stub's `SnapshotsByVehicleBetween` panic with a real
      capture: record `(gotAccount, gotTeslaID, gotStart, gotEnd)` and return configurable snaps
      for `Between`; keep `SnapshotsByVehicleSince` as a panic (the history handler no longer
      calls it — catch accidental re-wiring). The `Between` stub asserts the 1-day lookback: tests
      can assert `reader.gotStart.Equal(reqStart.AddDate(0,0,-1))` and `reader.gotEnd.Equal(reqEnd)`.
      Depends on: 1.1 (the new call site), 2.1.
- [x] 6.3 Add `TestBuildOdometerChart_FixedAxis_*` unit tests (design D3): (a) a 6-snap fixture for
      a 5-day window `[start..end]` + the `start-1` lookback snap → the chart has exactly 5 bars,
      bars labeled `MM-DD` for `[start..end]`, and the first bar's delta uses the `start-1`
      snap's odometer; (b) a fixture with a missing day inside the window (no snap for
      `start+2`) → the chart still has exactly `numDays` bars, the missing-day bar is
      `Present=false`, `HeightPct=0`, its `Label` is the missing day's `MM-DD`, and the
      surrounding bars' labels do NOT shift to fill it; (c) negative delta clamped to zero
      (preserved from the old test). Depends on: 2.3.
- [x] 6.4 Add `TestBuildBatteryChart_FixedAxis_*` unit tests (design D3): (a) a fixture for a 5-day
      window → exactly 5 bars, `HeightPct == snap.BatteryLevelPct`; (b) a missing-day fixture →
      the missing-day bar is `Present=false`, `HeightPct=0`, tooltip contains "no snapshot", its
      `Label` is the missing day's `MM-DD`; (c) `Empty` fires only when zero snaps exist in the
      window, NOT when only some days are missing. Depends on: 2.4.
- [x] 6.5 Add `TestBuildHistoryView_BothChartsShareFixedAxis` (the MAG-7 fix): for a fixture with
      a missing day, assert `len(v.Odometer.Bars) == len(v.Battery.Bars) == numDays` AND for each
      index `i`, `v.Odometer.Bars[i].Label == v.Battery.Bars[i].Label` (identical labels on both
      charts by construction — the axis offset is gone). Also assert `reader.gotStart ==
      reqStart.AddDate(0,0,-1)` (the lookback was passed to `Between`). Depends on: 2.2, 6.2.
- [x] 6.6 Rewrite the HTTP-level handler tests: `TestDashboardHistoryFragment_Default window`
      (no params → 200, `reader.gotStart == startOfDay(now).AddDate(0,0,-7)` [lookback 1 + default
      6], `reader.gotEnd == startOfDay(now)`); `TestDashboardHistoryFragment_400_*` for each of
      the five 400 cases (malformed, missing-partner, `end<start`, `end>today`, window>90d) — assert
      `w.Code == 400`, the body contains "Awaiting nightly snapshots" (the empty placeholder), and
      `reader` was NOT called (`reader.gotStart.IsZero()`); `TestDashboardHistoryFragment_Presets*`
      replaces the old `days`-preset tests — for a `?start=&end=` request the 6-day preset button
      has `btn-primary` and its href ends in `?start=<today-6>&end=<today>`; the 14/30 preset
      buttons carry their absolute hrefs and are `ghost`. Depends on: 2.1, 3.1, 5.1 (templ
      regenerated so the hrefs render).
- [x] 6.7 Retarget the render/label tests: `TestDashboardHistoryFragment_LabelsMatchViewModelVerbatim`
      (both charts), `TestDashboardHistoryFragment_LabelsRenderedAndVerticalOnlyForNarrowWindows`
      (vertical class present for 14-/30-bar windows, absent for the 6-bar window — the class is
      `[writing-mode:vertical-rl]`, asserted with `strings.Contains`, mirroring RM7 tier 2 task
      6.5's retarget), and `TestDashboardHistoryFragment_ContainsSVGViewBoxAndTitleTooltips`.
      Replace the old `?days=N` request URLs with `?start=&end=` and the absolute-date preset
      URLs. Assert the `?days` param is ignored (a request with `?days=6` and no `start`/`end`
      falls back to the default window — the param is removed, not honored). Depends on: 5.1.
- [x] 6.8 Update `TestDashboard_HistoryRegionInsideDashboardContent` (the structural test rendering
      the full `/ui/dashboard` fragment): assert `#dashboard-history` still carries
      `hx-trigger="load"`; assert the `hx-get` href is a server-rendered `?start=<today-6>&end=<today>`
      (contains `start=` and `end=` and today's `YYYY-MM-DD`), not `?days=6`; assert the dashboard
      page header does NOT contain "Refresh" (the button is gone). Do NOT assert against the
      charges-list Refresh (different page). Depends on: 3.3, 5.1.

## 7. Verification (go test, go vet, git status footprint)

- [x] 7.1 Run `go test ./internal/gateway/...` and confirm the full gateway suite is green,
      including all new `parseHistoryRange` / fixed-axis / preset / 400-case tests and the
      retargeted render tests. No live Tesla call, `telemetrydb` not imported by any gateway file
      (grep `internal/telemetry/db` under `internal/gateway/` — must be empty). Depends on: 6.8.
- [x] 7.2 Run `go vet ./...` and `go build ./...` repo-wide; confirm no other `telemetry.Reader`
      implementer broke (only the gateway's `fakeHistoryReader` / `fakeReader` / `battery`'s
      `fakeTelemetryReader` implement it — the `Since` method is untouched, the `Between` stubs
      added by tier 1 are now real in the gateway's history fake). Depends on: 7.1.
- [x] 7.3 Confirm `git status` shows changes only under `internal/gateway/`,
      `openspec/changes/RM8-gateway-history-date-range/`, and `ai/go-conventions.md` — no migration
      created, no `internal/telemetry/` file touched, no other module touched, no DB object. The
      committed `static/app.css` and the regenerated `*_templ.go` are in `internal/gateway/`.
      Depends on: 7.2.
- [x] 7.4 Run the gateway AGENTS.md CI guard `make css && git diff --exit-code
      internal/gateway/static/app.css` — exit 0 confirms `app.css` is committed and in sync with
      the `.templ` edits (no stale-CSS silent ship). Depends on: 5.2, 7.3.