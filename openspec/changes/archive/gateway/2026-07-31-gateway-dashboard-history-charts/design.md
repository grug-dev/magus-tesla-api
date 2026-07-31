# Design: gateway-dashboard-history-charts

## Context

RM5 tier 2. Tier 1 adds `telemetry.Reader.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)`.
This change consumes it to replace the dashboard's two `dashHistoryEmpty()` placeholders with
responsive SVG bar charts (odometer km/day + battery %) over a user-chosen window, plus the days
selector. Implementation is **not** started; this is the design for review.

## Goals / Non-Goals

**Goals**
- One new fragment endpoint `GET /ui/dashboard/history?days=N` that reads the selected vehicle's
  history and renders the selector + both charts.
- Km-per-day odometer bars and battery-level bars, same bar count, responsive, tooltips, no chart
  library, no template math.
- Days control that re-fetches both charts; auto-loads at default and follows vehicle switches.
- Record the rendering decision + the AGENTS.md convention.

**Non-Goals**
- Any telemetry/DB change (tier 1 owns the port; this change adds no query).
- Touching the base `dashboardFor` VM / `GET /ui/dashboard` render (the history block self-loads).
- Axes, legends, zoom, or JS interactivity beyond native `<title>` hover tooltips.

## Decisions

### RD7 — Hand-rolled responsive SVG, no chart library
Templ emits `<svg viewBox="0 0 W H" preserveAspectRatio="none" class="w-full h-24">` with one
`<rect>` per bar and a child `<title>` per bar for the native hover tooltip. `viewBox` +
`width:100%` makes it scale to any container width (the "responsive" requirement) with zero JS and
no resize handler; a `<canvas>` chart lib would need JS + a resize listener and a vendored/CDN
asset. Bar heights are expressed as a percentage of the chart height, **pre-computed in the
handler** — the template does no arithmetic. Fills use DaisyUI semantic tokens (e.g.
`class="fill-primary"` / `fill-secondary`), never hardcoded hex.

**Rejected: vanilla-JS chart library (uPlot / Chart.js).** Unjustified weight for simple bars in a
Node-less, server-rendered stack; adds a client dependency to keep current and an asset-pinning
concern. Revisit only if a future chart needs axes/interaction beyond hover — and record that
reversal per RD8.

### RD5 — Odometer bars = km driven per day (delta)
Snapshots arrive oldest-first from the port. For `days = N` the handler takes the **N+1** most
recent snapshots and computes `N` consecutive deltas `deltaKm[i] = OdometerKm(s[i]) −
OdometerKm(s[i-1])`, formatted with the existing `formatKm`. This gives varying, meaningful bars
("how far did I drive that day") instead of near-flat cumulative odometer. A negative delta (clock
skew / odometer anomaly) is clamped to 0. Each bar's `<title>` = `"<date> · <km> km driven ·
odometer <cumulative> km"`.

### RD6 — Battery bars = battery level %
Absolute `BatteryLevel` (0–100) for the **N** most recent snapshots — battery level is a level, not
a flow, so no delta. Bar height % = `BatteryLevel` directly (already 0–100). Each bar's `<title>` =
`"<date> · <level>% · <BatteryRangeKm()> km range"`.

### Same bar count (N) for both charts
Battery uses the last N snapshots; odometer uses the last N+1 to produce N deltas — both render N
bars. When fewer than N+1 snapshots exist, both render as many bars as the data allows (odometer =
points−1, battery = points); when a chart would have 0 bars (odometer with <2 points, battery with
0 points) it falls back to the existing `dashHistoryEmpty()` placeholder.

### `days` validation + default
`days` is validated against the allowed set `{6, 14, 30}` (a small closed vocabulary — an agent
looks it up, not re-invents it; also bounds the scan). Missing/invalid/out-of-set → **6**. The
window instant is `since = startOfDay(now).AddDate(0, 0, -days)` (UTC), which includes today plus
`days` prior days → up to `days+1` daily snapshots. The preset set + default live in one named
place in the handler (e.g. `historyDayPresets`, `defaultHistoryDays`), not scattered magic numbers.

### Region wiring — self-loading `#dashboard-history`, event-free for switches
The bento contains a `#dashboard-history` region that **replaces** the two old placeholder cards.
It carries `hx-get="/ui/dashboard/history?days=6"` + `hx-trigger="load"`, so it fetches itself once
the dashboard (full page or `#dashboard-content` fragment) renders. Because `#dashboard-history`
lives **inside** `#dashboard-content` — which already re-renders on `vehicle-changed` (see the
gateway "Vehicle-Scoped Cross-Region Refresh" requirement) — a vehicle switch reissues the shell,
whose `hx-trigger="load"` refetches the charts for the newly-selected vehicle at the default
window. No new event subscription is needed; the switch refresh is inherited.

The endpoint returns the **whole** history block (selector with the active preset highlighted +
both charts). The days buttons target `#dashboard-history` with `hx-swap="innerHTML"`, so a
selector click re-renders the selector (new active state) and both charts together — one template
entry point, mirroring the existing fragment-render pattern (`renderFragment`).

### RD8 — AGENTS.md decision + standing convention
`internal/gateway/AGENTS.md` gains: (1) a short "Charts are hand-rolled SVG — no chart library"
note with the rejected alternative and the reason (Node-less, responsive via `viewBox`,
AI-efficiency); and (2) a standing rule: *any decision to add/replace/drop a client-side library or
change a rendering/architecture approach MUST be recorded in this `AGENTS.md` in the same change.*

## Data flow

```
days-button click / region load / vehicle-changed → GET /ui/dashboard/history?days=N
  handler: currentUID → resolveSelectedVehicle(session) → (accountID, teslaID)
           clamp days → since = startOfDay(now)-days
           telemetry.Reader.SnapshotsByVehicleSince(accountID, teslaID, since)  [1 read]
           build HistoryView{ Days, Presets, Odometer []Bar, Battery []Bar }   [heights+tooltips precomputed, km via *Km()]
  templ:   #dashboard-history → selector(active=Days) + odometerChart(Odometer) + batteryChart(Battery)
           each Bar → <rect height=Bar.HeightPct%> <title>{Bar.Tooltip}</title>
```

## View model (handler-computed, template-dumb)

- `HistoryView{ Days int; Presets []int; Odometer HistoryChart; Battery HistoryChart }`
- `HistoryChart{ Bars []HistoryBar; Empty bool }`
- `HistoryBar{ HeightPct int; Tooltip string }` — every display value already a string/number the
  template renders verbatim (no `OdometerKm()`/`formatKm`/time calls in the template).

## Risks / Trade-offs

- [Odometer delta negative from clock skew or a reset] → clamp to 0; tooltip still shows the raw
  cumulative reading so the anomaly is visible on hover.
- [Sparse data (vehicle newly registered) yields <2 snapshots] → odometer chart shows the
  `dashHistoryEmpty()` placeholder; battery shows whatever points exist (≥1).
- [Days control drift from the port's `LIMIT 400`] → presets max 30 ⇒ ≤31 rows, far under the cap;
  no interaction.
- [A future 4th preset is added in the UI but not validated] → the closed `historyDayPresets` set
  is the single source; validation rejects anything not in it (default 6).

## Migration Plan

None (no DB, no data). Rollback = revert the gateway commit; the placeholders return.

## Open Questions

- Preset values: default is **6** (user-set); the two larger presets are proposed as **14** and
  **30** — confirm at apply.
