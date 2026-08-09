Source: MAG-7 — https://linear.app/magus-monitor/issue/MAG-7/date-filters
Roadmap: openspec/roadmaps/RM8-history-date-range.md
Tier: 2 of 2 (gateway; tier 1 is `RM8-telemetry-between-range-port`, module `telemetry`, archived 2026-08-09)
Depends on:
- Tier 1 `RM8-telemetry-between-range-port` — adds `telemetry.Reader.SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)`, the bounded-window read port this tier consumes. Already archived to `openspec/changes/archive/telemetry/2026-08-09-RM8-telemetry-between-range-port/`.
- RM7 tier 2 `gateway-history-graph-labels-tooltips` — already merged to main; introduced `HistoryBar.Label` + `HistoryChart.LabelVertical` (adaptive MM-DD labels from `EffectiveDate`) and the HTML-grid label row with the `[writing-mode:vertical-rl]` CSS class in `history.templ`. This tier builds on top of that code (same files: `history.go`, `history_vm.go`, `history.templ`) and MUST be applied after it — the same-files ordering is recorded in the RM8 roadmap §"Ordering".

## Why

The dashboard history endpoint today (`internal/gateway/handlers/history.go`) fetches via
`telemetry.Reader.SnapshotsByVehicleSince(ctx, uid, teslaID, since)` and renders from a count
param: `GET /ui/dashboard/history?days=N` clamped by `clampHistoryDays` to one of
`historyDayPresets = [6, 14, 30]`. The two charts derive their bar counts from the snapshot slice
the port returns — the odometer chart takes `days+1` snapshots to produce `days` deltas, the
battery chart takes `days` snapshots for `days` bars — and both label their bars from each
snapshot's `EffectiveDate`. Because the bar count is driven by the *snapshot count the port
happens to return* rather than by a fixed calendar axis, (a) a missing nightly snapshot shifts
every subsequent bar's label by a day and (b) the odometer chart's bars are one snapshot-step
ahead of the battery chart's when the two are laid side by side — the MAG-7 "odometer 08-01 /
battery 08-02" offset the ticket reports.

Linear MAG-7 asks that date filters on the dashboard history endpoint "be handled by start/end
date filters" — a bounded calendar-day window `?start=YYYY-MM-DD&end=YYYY-MM-DD` (`end`
inclusive) instead of the opaque `?days=N` count, with a **fixed `[start..end]` date axis** —
one bar per calendar day, identical labels on both charts, missing days rendered as empty
labeled slots — so the two charts always share an axis and a missing snapshot no longer shifts
the labels. The RM8 roadmap splits the feature across two modules:

- **Tier 1 (archived, `telemetry`):** gave the read port a first-class bounded-window method
  `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)` (no DB change).
- **Tier 2 (this change, `gateway`):** rewrite the HTTP handler to the new param contract, render
  the fixed date axis, rewrite the preset selector to emit absolute `start`/`end`, remove the
  dashboard Refresh button, and record the start/end HTTP convention.

Per the 2026-08-09 grill-me interview with the user (recorded verbatim as Decisions #1-#6 in
`openspec/roadmaps/RM8-history-date-range.md`), the bounded read is a first-class `telemetry.Reader`
method (Decision #1 — tier 1, done) and the **convention covers the HTTP surface**: every
date-filtered gateway HTTP endpoint takes `start`/`end` calendar dates, never a `days` count
(Decision #2). The 6/14/30 preset selector stays (Decision #3) but each button now carries a
server-rendered absolute `?start=<today-N>&end=<today>` href; the axis is fixed (Decision #4);
the dashboard page Refresh button is removed (Decision #5); and the convention is recorded in
`internal/gateway/AGENTS.md` (canonical) with a one-line pointer in `ai/go-conventions.md`
(Decision #6).

## What Changes

- **Switch `DashboardHistoryFragment` from `c.Query("days")` + `clampHistoryDays` to
  `parseHistoryRange(c)` returning a validated `(start, end time.Time)`** (Decision #2):
  ISO date parse (`YYYY-MM-DD`), both whole calendar days, `end` inclusive, default 6-day
  window (`today-6`..`today`) when both params are absent, `400 Bad Request` on malformed
  non-ISO dates / `end < start` / `end > today` / window > 90 days.
- **1-day lookback** `readStart = start.AddDate(0, 0, -1)` (Decision #4) and call
  `h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`. The port is
  a clean `Between(start, end)`; the lookback is purely a gateway concern (tier 1 design D5).
- **Rewrite `buildHistoryView` / `buildOdometerChart` / `buildBatteryChart` to bucket by
  `EffectiveDate` into a fixed `[start..end]` date axis** — one bar per calendar day, identical
  MM-DD labels on both charts. Missing days render as **empty bars that retain their date label**
  (so the axes align even when a night's snapshot is absent). The pre-window `start-1` snapshot
  is consumed solely as the first odometer delta's km basis and is NOT displayed as a bar.
- **Replace `historyDayPresets` / `defaultHistoryDays` / `clampHistoryDays`** with
  `historyRangeWindowDays = 6` (the default window) and `parseHistoryRange(c)` (date parser +
  validation). **Replace, not keep:** a closed vocabulary of *day counts* no longer makes sense
  when the API takes absolute dates; the presets become a UI-only convenience that translates to
  absolute dates server-side at render time.
- **`labelVerticalFor` moves from "days count" to "number of bars in the window"** (6 bars →
  horizontal, ≥14 bars → vertical) — preserving RM7's adaptive-orientation invariant on the new
  axis. RM7 tier 2 set `LabelVertical` from `days`; this tier sets it from `len(bars)` in the
  fixed window so the orientation rule survives the count→date-range rewrite.
- **Preset selector rewrite (`fragments/history.templ`):** each button's `hx-get` emits
  `?start=<today-N>&end=<today>` as server-rendered absolute dates. The `HistoryView` view model
  gains `Presets []RangePreset{Label, Start, End}` (and the active preset is the one whose
  window matches the requested `(start, end)`); the templ renders the absolute hrefs. The
  `#dashboard-history` self-load in `pages/dashboard.templ` switches from `?days=6` to
  `?start=<today-6>&end=<today>` (server-rendered).
- **Remove the dashboard page Refresh button** (`pages/dashboard.templ`, currently the `@ui.Button`
  inside the `@ui.PageHeader` block at lines 24-26) — a vestigial full-page reload already
  superseded by the `#dashboard-content` (`vehicle-changed`) and `#dashboard-history` (preset
  click) htmx self-refresh (Decision #5). The charges-list Refresh (`fragments/charges_list.templ`)
  stays — it belongs to a different feature.
- **Convention doc** in `internal/gateway/AGENTS.md` (canonical — "HTTP date-filter convention"
  section, in the SAME change per CLAUDE.md's "Docs track structural change" rule) + a one-line
  pointer in `ai/go-conventions.md` (Decision #6).
- **Regenerate:** `make generate` (sqlc + templ + css); commit `static/app.css` in the same change
  (gateway AGENTS.md CI guard `make css && git diff --exit-code`).
- **Rewrite history tests** for the new parser (400 cases, default window, lookback bucketing,
  fixed-axis rendering, both charts share dates, presets carry absolute hrefs). Existing `days`-
  param tests are replaced.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `gateway`: the "Dashboard History Charts" requirement (introduced by RM5, modified by RM7 tier 2)
  changes again — the endpoint takes `?start=YYYY-MM-DD&end=YYYY-MM-DD` (inclusive `end`, default
  6-day window, 400 on malformed / `end<start` / `end>today` / window>90d) instead of `?days=N`;
  both charts render a fixed `[start..end]` date axis (one bar/day, missing days = empty labeled
  slots, identical labels on both charts — fixes the MAG-7 odometer/battery offset); the 6/14/30
  preset selector emits server-rendered absolute `start`/`end` hrefs; the dashboard page Refresh
  button is removed. See `specs/gateway/spec.md`.

## Impact

- **Module:** `internal/gateway` only. Reads via the public `telemetry.Reader.SnapshotsByVehicleBetween`
  port (owned by tier 1); no other module is touched, no DB, no Tesla API.
- **Files:**
  - `internal/gateway/handlers/history.go` — replace `clampHistoryDays`/`defaultHistoryDays`/
    `historyDayPresets` with `parseHistoryRange`/`historyRangeWindowDays`; switch the read from
    `SnapshotsByVehicleSince` to `SnapshotsByVehicleBetween` with a 1-day lookback; rewrite
    `buildHistoryView`/`buildOdometerChart`/`buildBatteryChart` to bucket by `EffectiveDate` into a
    fixed `[start..end]` axis; retarget `labelVerticalFor` from days to bar count.
  - `internal/gateway/templates/fragments/history_vm.go` — replace `Days int` / `Presets []int` on
    `HistoryView` with `Start time.Time` / `End time.Time` / `Presets []RangePreset` (struct with
    `Label string`, `Start time.Time`, `End time.Time`, and an `Active bool` flag for rendering).
    `HistoryChart` keeps `Bars`/`Empty`/`LabelVertical` (RM7 tier 2); the bars now always number
    exactly the days in `[start..end]` (one bar/day), with empty-day bars carrying `HeightPct=0`
    plus a `Present bool` flag so the template can render an empty labeled slot.
  - `internal/gateway/templates/fragments/history.templ` — rewrite `historyDaysSelector` to render
    the absolute `?start=...&end=...` hrefs from `RangePreset`; the `historyBarChart` template
    renders empty-day bars as a labeled empty slot (the existing SVG `rect` with `HeightPct=0`,
    plus its `MM-DD` label so the axis stays complete).
  - `internal/gateway/templates/pages/dashboard.templ` — remove the Refresh `@ui.Button` (lines
    24-26); switch the `#dashboard-history` self-load `hx-get` from `?days=6` to a server-rendered
    `?start=<today-6>&end=<today>`.
  - `internal/gateway/handlers/history_test.go` — replace the `days`-param tests with
    `parseHistoryRange` parser tests (malformed / `end<start` / `end>today` / window>90d / default
    window), fixed-axis bucketing tests (missing day → empty labeled bar; pre-window snapshot feeds
    first odometer delta only), and render tests asserting both charts share identical labels.
  - `internal/gateway/static/app.css` — regenerated by `make css` (committed).
  - `internal/gateway/AGENTS.md` — add the "HTTP date-filter convention" section (canonical).
  - `ai/go-conventions.md` — add a one-line pointer to that section.
  - Generated `*_templ.go` files via `make templ` (committed alongside the `.templ` edits).
- **APIs:** `GET /ui/dashboard/history` — the `?days=N` query param is **removed**; the endpoint
  now takes `?start=YYYY-MM-DD&end=YYYY-MM-DD`. This is a **breaking change to the gateway HTTP
  API**. The response HTML shape (two SVG bar charts + a preset selector) is unchanged in element
  structure; the selector's `hx-get` hrefs change from `?days=N` to `?start=...&end=...`.
- **Dependencies:** none added. Consumes `telemetry.Reader.SnapshotsByVehicleBetween` (tier 1).
- **Breaking:** **Yes** — breaking to the gateway HTTP API (`?days=N` → `?start=&end=`). This is
  an internal htmx fragment endpoint (no external JSON consumer; the `/api/{version}` surface is a
  separate adapter that does not expose this route). The only caller is the dashboard's own htmx,
  which this change rewrites in lockstep. Affects module `gateway` only.
- **Performance:** the read path affected — `telemetry.Reader.SnapshotsByVehicleBetween`, the hot
  dashboard history path. The bounded window is *strictly cheaper* than the open `Since` scan
  (tier 1 design D2/D3): an indexed forward range scan over `(account_id, tesla_id, captured_at)`
  with the upper bound pruning the scan in-place, capped by `LIMIT 400` (tier 1) and the 90-day
  HTTP window cap (this tier). Bucketing by `EffectiveDate` into the fixed axis is `O(n)` in the
  snapshot count for the window (≤ 91 rows), a cheap in-handler map. The template stays logic-free
  (all heights/labels pre-computed — invariant preserved). See `design.md` §Read path.
- **Cookbook binding interview:** the proposal rule requires the grill-me skill. The binding
  interview is the 2026-08-09 grill-me with the user, whose outcomes are recorded verbatim as
  Decisions #2, #3, #4, #5, #6 in `openspec/roadmaps/RM8-history-date-range.md` (D1 = bounded port,
  settled in tier 1). Those decisions are NOT re-litigated here — they are applied:
  - D2 = `?start=YYYY-MM-DD&end=YYYY-MM-DD`, UTC-midnight-bounded, `end` inclusive, default 6-day
    window, 400 on malformed / `end<start` / `end>today` / window>90d.
  - D3 = keep 6/14/30 presets, server-render absolute `start`/`end`.
  - D4 = fixed `[start..end]` axis, one bar/day, missing days = empty labeled slots, 1-day lookback
    for first odometer delta only (no display).
  - D5 = remove only the dashboard page Refresh button.
  - D6 = convention canonical in `internal/gateway/AGENTS.md` + one-line pointer in
    `ai/go-conventions.md`.