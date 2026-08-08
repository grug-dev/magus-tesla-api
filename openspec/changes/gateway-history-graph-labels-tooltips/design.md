## Context

The history charts (`GET /ui/dashboard/history?days=N`) already render two hand-rolled SVG bar
charts from `telemetry.Reader.SnapshotsByVehicleSince` (RM5, design RD5–RD7). The view model is in
`internal/gateway/templates/fragments/history_vm.go` (`HistoryBar`, `HistoryChart`, `HistoryView`);
the builder is `internal/gateway/handlers/history.go` (`buildOdometerChart`, `buildBatteryChart`).
The template (`internal/gateway/templates/fragments/history.templ`) is logic-free: it renders
pre-computed `HeightPct` + `Tooltip` per bar and a `<title>` for the native hover.

Today the tooltip date is `CapturedAt.Format("2006-01-02")` (YYYY-MM-DD), there is no per-bar date
label, and the date shown is the *capture* morning, which is one calendar day later than the day
the data represents. Tier 1 (`telemetry-add-effective-date`) adds `Snapshot.EffectiveDate`
(= `CapturedAt − 1 day`, full `time.Time`). This tier consumes it.

`historyDayPresets` is `[6, 14, 30]`; bars get narrow in the 30-day view.

## Goals / Non-Goals

**Goals:**
- Show the day the data **represents** (`EffectiveDate`), not the capture morning, in both the
  tooltip and a new per-bar date label.
- Format the date `MM-DD` (the user's preferred short format), computed in the handler.
- Render a date label under every bar that stays readable in the narrow 30-day view.
- Keep the template logic-free (handler pre-computes the label string + an orientation flag).

**Non-Goals:**
- NOT changing the endpoint, the days preset set, the SVG approach (RD7), or the empty-state.
- NOT changing the odometer/battery tooltip *content* beyond the date token (km driven +
  cumulative; level % + range km stay).
- NOT touching the telemetry module — `EffectiveDate` is consumed here, owned in tier 1.
- NOT localizing dates or adding a year — `MM-DD` only, as MAG-6 specifies.

## Decisions

### D1 — Both tooltip and label use `EffectiveDate`, formatted `MM-DD` (grill-me outcome)

**Decision:** The tooltip's date token and the per-bar label both come from
`snap.EffectiveDate.Format("01-02")` (MM-DD).

**Rationale (grill-me MAG-6 Q3):** the user confirmed the backend keeps the full `time.Time` (incl.
year) and the MM-DD format is a cosmetic/UI concern owned by the gateway. Showing the label from
`EffectiveDate` and the tooltip from `CapturedAt` would re-introduce the 1-day mismatch MAG-6
specifically fixes — so both use `EffectiveDate` for consistency.

**Alternatives rejected:** tooltip from `CapturedAt` MM-DD (1-day mismatch with label); backend
pre-formatting MM-DD (rejected in tier 1 D3 — keeps the field reusable).

### D2 — Per-bar label, adaptive orientation (grill-me outcome)

**Decision:** Add `Label string` to `HistoryBar` (handler-pre-formatted `MM-DD`). Add
`LabelVertical bool` to `HistoryChart`, set by the handler from the `Days` preset: **false** for
`6` (wide bars → horizontal label), **true** for `14` and `30` (narrow bars → rotated vertical
label). The template renders the label per bar and picks horizontal vs. vertical by reading the
flag — it does NOT compute orientation, compare `Days`, or do rotation math.

**Rationale (grill-me MAG-6 Q1-labels):** the ticket hints "Might be good to put the label in
vertical way somehow". Adaptive (horizontal where there's room, vertical where bars are narrow)
gives the best readability at every preset and keeps the template logic-free via a single boolean.
A chart-level flag (not a per-bar one) is sufficient — all bars in one chart share orientation
because they share a `Days` preset.

**Alternatives rejected:**
- Always vertical for all presets — harder to read in the 6-day view where there is plenty of room.
- Always horizontal, show every Nth label — loses the per-bar date label MAG-6 asks for.
- Always horizontal, full label — overlaps badly in the 30-day view.

### D3 — Orientation is a chart-level bool, computed once in the handler

**Decision:** `HistoryChart.LabelVertical bool` is set in `buildOdometerChart`/`buildBatteryChart`
from the `days` argument (`days >= 14`). The template branches only on this bool.

**Rationale:** one decision per chart, not per bar — both charts in a fragment share the same `days`
so they share orientation; a per-bar flag would be redundant data. Keeps the closed vocabulary
(`historyDayPresets`) the single source of "which presets are wide vs. narrow" (AI-efficiency:
small lookup, one named location).

### D4 — Label rendering: SVG `<text>` with `transform="rotate(-90 ...)"` for vertical

**Decision:** The chart `.templ` renders one `<text class="fill-base-content/60" ...>` per bar
positioned at the bar's x and just below the chart canvas. When `LabelVertical` is true, the text
carries `transform="rotate(-90 x y)"` and is anchored so it grows upward within the bar's width
slot; when false, it is horizontal and centered under the bar. Font size is small (e.g.
`text-[8px]`/`text-[9px]` via the existing SVG sizing). Bar fills stay DaisyUI semantic tokens
(`fill-primary`/`fill-secondary`); the label uses a muted content token (`fill-base-content/60`).

**Rationale:** pure SVG, zero JS, consistent with RD7 (hand-rolled responsive SVG, no chart
library). The `viewBox` + `preserveAspectRatio` already scale the chart; the rotated label rotates
around its bar's x so it never widens the layout. No new client-side dependency (RD8: record that
no rendering approach changes — only a per-bar `<text>` is added to the existing SVG).

**Alternatives rejected:** HTML `<div>` labels under the SVG (breaks the single responsive SVG
contract and the bar↔label alignment under width scaling); a JS library (rejected by RD7).

### D5 — `Label` is pre-formatted in the handler (template stays logic-free)

**Decision:** The handler builds `fragments.HistoryBar{HeightPct, Tooltip, Label}` with `Label =
effectiveDate.Format("01-02")`. The template emits `Label` verbatim and reads `LabelVertical` only
to choose the `<text>` transform.

**Rationale:** mirrors the existing invariant (`Tooltip` is pre-rendered; template does no
formatting). Keeps gateway spec "templates contain no business logic" satisfied — no
`time.Format`, no `CapturedAt` access, no `EffectiveDate` access in the markup.

## Risks / Trade-offs

- **[Risk] Vertical labels overflow the bar width at very small widths.** In a 30-day view each bar
  is ~`100/30 ≈ 3.3%` of canvas width; a rotated `MM-DD` (5 chars) fits vertically because the
  height direction is unconstrained, but the text must be anchored to grow upward from the bar
  base. → **Mitigation:** anchor rotated text at the bar's bottom-center and grow upward
  (`text-anchor: start` after rotate, or compute the anchor so the label sits in the gap below the
  chart); pick a small font size; verify visually in the 30-day preset before closing the change.
- **[Risk] Horizontal 6-day labels overlap the neighboring bar's slot.** `MM-DD` (5 chars) at 6 bars
  is ~16% canvas width each — plenty. → **Mitigation:** center under the bar; if any overlap
  appears at 6, drop font size to 8px; 6-day is the widest case so this is low risk.
- **[Risk] Tier ordering — this tier won't compile without tier 1.** → **Mitigation:** intended.
  The roadmap ships tier 1 then tier 2; the compile error is the ordering signal. Documented in the
  roadmap and in the task list (tier 2 task 1 depends on tier 1 being applied).
- **[Trade-off] A chart-level `LabelVertical` bool means both charts share orientation even if one
  has fewer bars.** Acceptable — both charts always use the same `days` preset by design (RM5
  invariant "both charts render the same number of bars / same window"), so orientation is a
  per-fragment property, not a per-chart one in practice.
- **[Trade-off] New Tailwind class `fill-base-content/60` (or similar) may need `make css`.** Per
  gateway `AGENTS.md`, run `make css` (and `make templ`) after the `.templ` edit and commit
  `static/app.css` in the same change; the CI guard `make css && git diff --exit-code` catches a
  missed regeneration.

## Migration Plan

None at the infrastructure level. Apply tier 1 first (so `Snapshot.EffectiveDate` exists), then
this tier: edit `history_vm.go` + `history.go` + `history.templ`, run `make templ && make css`,
verify the 6/14/30-day presets render correctly, run `go test ./internal/gateway/...`. No DB
migration, no route change, no deploy coordination.

## Open Questions

None. All three presentation questions (D1 EffectiveDate+MM-DD, D2 adaptive orientation, D3
chart-level flag) were resolved in the grill-me interview with the user (Linear MAG-6).