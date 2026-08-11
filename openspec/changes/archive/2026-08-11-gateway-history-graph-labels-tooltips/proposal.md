## Why

The dashboard history charts work, but three rough edges hurt readability (Linear MAG-6):
(1) tooltips and bars show `YYYY-MM-DD` (e.g. `2026-08-07`), which is verbose and not how users
read dates;
(2) there is no per-bar date label, so in the 30-day view you cannot tell which bar is which day
without hovering;
(3) because the nightly batch reads in the morning, the date shown on each bar is *today's*
calendar date for data that really represents *yesterday's* driving — a 1-day mismatch that makes
the "history" graph feel off by one.

Tier 1 of this roadmap adds `Snapshot.EffectiveDate` (the day the snapshot represents =
`CapturedAt − 1`). This tier makes the gateway **use** it: the tooltip and the per-bar label both
show the `EffectiveDate` formatted `MM-DD`, so the graph reads as the day the data describes, and
every bar carries its own date label that fits even in the narrow 30-day view.

## What Changes

- **Tooltip date format → `MM-DD` from `EffectiveDate`.** `buildOdometerChart` and
  `buildBatteryChart` (`internal/gateway/handlers/history.go`) format the date in each tooltip as
  `EffectiveDate.Format("01-02")` instead of `CapturedAt.Format("2006-01-02")`. The rest of each
  tooltip string is unchanged (odometer: km driven + cumulative; battery: level % + range km).
- **Per-bar date label, adaptive orientation.** Add a `Label string` field to `fragments.HistoryBar`
  (pre-formatted `MM-DD` from `EffectiveDate`, computed by the handler — template does no
  formatting). Add a `LabelVertical bool` flag to `fragments.HistoryChart`, set by the handler from
  the `Days` preset: **false** for the 6-day preset (wide bars → horizontal `MM-DD`), **true** for
  the 14- and 30-day presets (narrow bars → rotated vertical `MM-DD`). The SVG template renders the
  label per bar and switches orientation by reading the flag — no arithmetic, no rotation logic, no
  `Days` comparison inside the markup.
- **Use `EffectiveDate` (tier 1) for both tooltip and label.** This is what makes the graph show
  the day the data represents, fixing the off-by-one. The handler reads `snap.EffectiveDate` (added
  by `telemetry-add-effective-date`); if that tier is not yet applied, this change will not compile,
  which is the intended ordering signal.
- **No DB, no Tesla API, no new endpoint, no new module.** Same `GET /ui/dashboard/history?days=N`
  endpoint, same `telemetry.Reader` call, same hand-rolled SVG approach (RD7 unchanged).

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `gateway`: the "Dashboard History Charts" requirement changes — tooltips format the date as
  `MM-DD` from the snapshot's `EffectiveDate`; each bar carries a pre-formatted `MM-DD` date label
  rendered under the bar with adaptive orientation (horizontal for the 6-day preset, vertical for
  the 14- and 30-day presets). The handler computes all label strings and the orientation flag; the
  template stays logic-free.

## Impact

- **Module:** `internal/gateway` only. Depends on `internal/telemetry` exposing
  `Snapshot.EffectiveDate` (tier 1, same roadmap).
- **Files:** `internal/gateway/templates/fragments/history_vm.go` (add `Label` to `HistoryBar`,
  `LabelVertical` to `HistoryChart`), `internal/gateway/handlers/history.go` (format tooltips from
  `EffectiveDate`, build per-bar `Label`, set `LabelVertical` from the preset), the chart `.templ`
  (`templates/fragments/history.templ` — render labels, switch orientation by flag), and the
  handler/template tests.
- **APIs:** `GET /ui/dashboard/history` — no route change, no method-signature change; the response
  HTML gains per-bar labels and `MM-DD` tooltips. **Non-breaking** at the HTTP level.
- **Dependencies:** none added. No chart library (RD7 hand-rolled SVG unchanged).
- **Breaking:** No (HTTP-level). Source-level: requires tier 1's `Snapshot.EffectiveDate` to exist;
  the two tiers ship together via the roadmap.
- **Performance:** the history fragment is a user-initiated htmx refresh, not a per-render hot path.
  Cost is a handful of `time.Format` calls (already happening for tooltips). Negligible. The two hot
  read paths named in tier 1 are not further affected here.
- **Styling:** vertical labels use a `transform="rotate(...)"` on an SVG `<text>`; colors stay
  DaisyUI semantic tokens. Per the gateway `AGENTS.md` RD8 convention, no new client-side library is
  introduced and no rendering approach changes — `make css` runs after any new Tailwind class.