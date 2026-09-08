# RM50-gateway-add-travel-progress-subsection

> Source: MAG-56 — https://linear.app/magus-monitor/issue/MAG-56/vehicle-status-change
> Roadmap: RM50-vehicle-status-subsections, tier 2 of 4. See that file for RD1–RD8 and
> RD-fixed (binding decisions this change does not re-open). Depends on tier 1
> (`RM50-analytics-add-tire-pressure-columns`, archived), which already exposes the two
> fields this tier reads.

## Why

The dashboard "Vehicle Status" panel shows one flat row of four stat tiles today
(Odometer, Interior, Exterior, 100% Charges). MAG-56 wants it grouped into named
subsections instead, plus a new "Travel Progress" subsection showing the latest day's
distance driven and battery used. `/design` already ran for this tier and the user picked
Option B (roadmap RD9) — this change builds that layout and the Travel Progress data.

## What changes

Three parts, all inside `internal/gateway`.

**A. Two `ui/` kit additions (RD11).** `ui.Icon` gains two glyphs, `trending_up` and
`trending_down` (green/red trend arrows). `ui.StatTileProps` gains a `Trend string`
field (`"up"` / `"down"` / `""`) that renders one of those icons beside the tile's
value, coloured by a semantic token (`text-success` / `text-error`, never a raw
colour — RD6). Empty (the zero value) renders no icon, so every existing `StatTile`
call site (three pages) is unaffected.

**B. The panel layout (RD9, Option B) + Travel Progress data.** The Vehicle Status
card's body becomes an inner 12-column grid: a left column (vehicle image + the
Odometer and 100%-Charges tiles, stacked) and a right column of named subsections
(`ui.SectionHeader` + a tile grid each). Two subsections render now — **Travel
Progress** (latest distance travelled, fixed green up icon; latest battery used, fixed
red down icon — RD-fixed, not delta-driven) and **Interior/Exterior** (the existing two
temperature tiles, regrouped). A third subsection, **Tire pressure (PSI)**, is left as
a marked gap for tier 4 — this tier renders nothing there. The two new data points
(`analytics.VehicleStatus.DistanceTraveledKmCalc`, `.ConsumedPct`) already exist on the
read port (tier 1); this tier only maps and formats them.

**C. Docs.** `internal/gateway/AGENTS.md` gains a short note on the new subsections and
the two kit additions. Two KB guides tier 1 already flagged as going stale —
`kkpa/context/use-case/gateway/read-dashboard-bento.md` and
`kkpa/context/input-port/gateway/dashboard.md` — are corrected to describe the new
layout and the two newly-read fields.

## Breaking?

No. `StatTileProps.Trend` is an additive, optional field (zero value unchanged
behaviour). `fragments.DashboardData` gains two new string fields — additive, named-field
construction only (no positional caller exists in this codebase). The Vehicle Status
panel's markup restructures, but every existing element (title, badges, last-updated
footnote, the four pre-existing stat values) still renders, just regrouped.

## Modules affected

- `internal/gateway` — the only module with code changes in this tier.
- `internal/analytics` — read only, through the existing `analytics.Reader` port
  (`LatestMetricsByAccount`). No change requested; tier 1 already exposes both fields.

## Read paths affected

`LatestMetricsByAccount` already feeds `GET /dashboard` and `GET /ui/dashboard`
(`Handler.dashboardFor`) — the same call, no new query. This tier adds no database
read; it reads two pointer fields the query already returns (RD7).

## Non-goals (later tiers)

- No tyre-pressure `_calc` delta columns or maths — tier 3 (`internal/analytics`).
- No Tire pressure (PSI) subsection content — tier 4. This tier only reserves its place
  in the layout.
