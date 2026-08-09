## 1. View model

- [x] 1.1 In `internal/gateway/templates/fragments/history_vm.go`, add a `Label string` field to `HistoryBar` (pre-formatted `MM-DD` from `EffectiveDate`) with a doc comment; update the existing `Tooltip` doc to note the date token is now `MM-DD` from `EffectiveDate`.
- [x] 1.2 In the same file, add a `LabelVertical bool` field to `HistoryChart` with a doc comment: set by the handler from the `days` preset (false for 6, true for 14/30); the template reads only this flag.

## 2. Handler (date source + label + orientation)

- [x] 2.1 In `internal/gateway/handlers/history.go`, change `buildOdometerChart` to format the tooltip date as `d.date.Format("01-02")` using the snapshot's `EffectiveDate` (replace the `pts[i].CapturedAt.UTC().Format("2006-01-02")` source with the `EffectiveDate` of the same snapshot; compute the delta's `date` from `EffectiveDate`). Set `Label: <effectiveDate>.Format("01-02")` on each `HistoryBar`. Set the chart's `LabelVertical` from a new helper `labelVerticalFor(days)` (true when `days >= 14`).
- [x] 2.2 In the same file, change `buildBatteryChart` to format the tooltip date as `s.EffectiveDate.Format("01-02")` (replace `s.CapturedAt.UTC().Format("2006-01-02")`) and set `Label: s.EffectiveDate.Format("01-02")` on each `HistoryBar`. Set `LabelVertical` via the same `labelVerticalFor(days)` helper.
- [x] 2.3 Add the `labelVerticalFor(days int) bool` helper next to `clampHistoryDays` (closed-vocabulary lookup: returns true for 14 and 30, false otherwise — one named location for the wide/narrow split). Confirm both charts in one fragment share orientation (both call the same helper with the same `days`).

## 3. Template (render label, switch orientation by flag)

- [x] 3.1 In `internal/gateway/templates/fragments/history.templ` (the `historyBarChart` component), render one SVG `<text class="fill-base-content/60" ...>` per bar positioned under the bar at the bar's x, emitting `bar.Label` verbatim. When `chart.LabelVertical` is true, add `transform="rotate(-90 ...)"` anchoring the text to grow upward from the bar base; when false, render it horizontal and centered. No `time.Format`, no `days` comparison, no rotation math in the markup.
- [x] 3.2 Verify the label sits inside the SVG `viewBox`/`preserveAspectRatio` region (below the bar canvas) so responsive scaling keeps bar↔label alignment; pick a small font size so vertical labels fit at 30-day width. Adjust the chart canvas height / viewBox if needed to leave room for the label row.
- [x] 3.3 Run `make templ` (regenerate `*_templ.go`) and `make css` (regenerate `static/app.css` for any new semantic-token class such as `fill-base-content/60`). Commit `static/app.css` in the same change (gateway AGENTS.md CI guard).

## 4. Tests

- [x] 4.1 Add/extend a handler unit test in `internal/gateway/handlers/` asserting: for a snapshot with `CapturedAt=2026-08-08 03:30 UTC` / `EffectiveDate=2026-08-07`, the built `HistoryBar.Label` == `"08-07"` and the tooltip contains `08-07` (not `2026-08-08`), for both odometer and battery charts.
- [x] 4.2 Add a test asserting `LabelVertical` is false for `days=6` and true for `days=14` and `days=30` (covers `labelVerticalFor`).
- [x] 4.3 Add a test (httptest against `NewEngine` with a fake `telemetry.Reader` returning snapshots with known `EffectiveDate`s) asserting the rendered fragment HTML contains each bar's `MM-DD` label and a rotated `<text>` (i.e. a `transform="rotate(-90` substring) only when the preset is 14 or 30, and not when it is 6.
- [x] 4.4 Assert the template/logic-free invariant holds: the rendered labels match the pre-computed `Label` strings verbatim and there is no `2006-01-02` format string in the response.

## 5. Verification & docs

- [x] 5.1 Ensure tier 1 (`telemetry-add-effective-date`) is applied first (this tier references `Snapshot.EffectiveDate`); `go build ./...` should succeed only after tier 1 lands.
- [x] 5.2 Run `go test ./internal/gateway/...` and `make check` (if available); confirm no live Tesla call and `telemetrydb` is not imported by any gateway file.
- [ ] 5.3 Visually verify the 6/14/30-day presets render labels correctly (horizontal at 6, vertical at 14/30, no overlap, bar↔label aligned) using `make dev` against a vehicle with snapshot history.
- [x] 5.4 Per gateway `AGENTS.md` RD8, confirm no client-side library was added and the SVG rendering approach is unchanged (only a per-bar `<text>` added); no AGENTS.md convention update is required because no rendering approach changed — if any Tailwind class was added, `make css` already ran in 3.3.
## 6. Label rendering fix (design D4-R1 — supersedes the SVG `<text>` approach)

- [ ] 6.1 In `internal/gateway/templates/fragments/history.templ`, wrap the chart in a grid container whose column count equals the bar count (`grid-template-columns: repeat(<numBars>, 1fr)`), with the `<svg>` spanning all columns. Remove the per-bar `<text>` label elements and the label zone from the SVG: restore the bar `viewBox` height to 100 and drop `historyChartViewBoxHeight`/`historyLabelZoneHeight`/`historyLabelHorizontalY`/`historyLabelAnchorY`.
- [ ] 6.2 Render the labels as a sibling HTML row inside the same grid — one cell per bar, emitting `bar.Label` verbatim, centered under its bar. When `chart.LabelVertical` is true the cell gets the vertical CSS class (`[writing-mode:vertical-rl]` + rotation) so the text reads bottom-to-top; when false it is plain horizontal text. The template still branches only on `chart.LabelVertical` — no `days` comparison, no `time.Format`, no rotation math.
- [ ] 6.3 Pick a font size that is legible at the 30-day preset (labels are real DOM text now, so it is a genuine CSS size, not an SVG-scaled one) and ensure the label row does not overflow or wrap.
- [ ] 6.4 Re-run `make templ` and `make css`; leave regenerated `*_templ.go` and `static/app.css` on disk for the leader to commit.
- [ ] 6.5 Update the affected tests: task 4.3's assertion that a rotated label appears only at the 14/30-day presets must now assert the CSS vertical class instead of the `transform="rotate(-90` SVG substring. Keep every other assertion (verbatim labels, no `2006-01-02`, orientation only at 14/30) intact — retarget, never weaken.
