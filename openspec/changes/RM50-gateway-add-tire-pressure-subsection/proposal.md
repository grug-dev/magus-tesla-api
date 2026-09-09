# RM50-gateway-add-tire-pressure-subsection

> Source: MAG-56 — https://linear.app/magus-monitor/issue/MAG-56/vehicle-status-change
> Roadmap: RM50-vehicle-status-subsections, tier 4 of 4. See that file for RD1–RD13
> (binding decisions this change does not re-open). Depends on tier 2
> (`RM50-gateway-add-travel-progress-subsection`, archived — built the panel layout and
> the `ui.StatTileProps.Trend` mechanism) and tier 3
> (`RM50-analytics-add-tire-pressure-variance`, archived — exposes the four `_calc`
> delta fields this tier reads).

## Why

Tier 2 built the Vehicle Status panel's new layout and left a marked gap for the
"Tire pressure (PSI)" subsection. Tier 3 added the four wheel readings and their
day-over-day deltas to the analytics read port. This tier fills the gap: one value per
wheel, each with an up/down trend icon and a numeric delta line, reading data that
already exists on the port — no new database read.

## What changes

All inside `internal/gateway`.

**A. The Tire pressure (PSI) subsection.** A `<section id="tire-pressure">` block
replaces tier 2's placeholder comment, between "Travel Progress" and
"Interior/Exterior". It renders a 2x2 grid (RD9) of four `ui.StatTile`s — front-left,
front-right, rear-left, rear-right — each showing the wheel's current PSI reading, an
up/down trend icon from `ui.StatTileProps.Trend` (added by tier 2, reused verbatim —
no second trend mechanism), and the numeric delta as the tile's `Desc` line (RD10,
e.g. "+0.4 vs prev. day").

**B. View-model + handler mapping.** `fragments.DashboardData` gains a `TireWheelVM`
struct (`Value`, `Trend`, `Delta` — mirrors `SuperchargerTiles`'s "one struct per
repeated shape" pattern) and four named fields, one per wheel. `handlers.go` gains
three pure formatters — `dashPSIOrDash`, `dashTireTrend`, `dashTireDelta` — plus one
small builder, `dashTireWheel`, called four times from `mapDashboardSnapshot`. Two new
formatters in `format.go` — `formatPSI`, `formatSignedPSI`.

**C. i18n.** Seven new catalogue keys: the subsection title + description, the four
wheel labels, and the delta-line format string. All resolve through `i18n.T`, both
`ES` and `EN` non-empty.

**D. Docs.** `internal/gateway/AGENTS.md` gains a short note that the panel's three
named subsections are now all built, plus the wheel-VM shape. The two KB guides tier 2
already corrected get one more line each, since the "reserved gap" they described is
now filled.

## Breaking?

No. `fragments.DashboardData` gains new fields (additive, named-field construction
only — no positional caller exists). `ui.StatTileProps.Trend` and `ui.Icon`'s
`trending_up`/`trending_down` glyphs already exist (tier 2) and are reused, not
changed. The Vehicle Status panel's markup fills a previously-empty gap; every
existing element is unaffected.

## Modules affected

- `internal/gateway` — the only module with code changes in this tier.
- `internal/analytics` — read only, through the existing `analytics.Reader` port
  (`LatestMetricsByAccount`). No change requested; tier 1 and tier 3 already expose
  every field this tier reads.

## Read paths affected

`LatestMetricsByAccount` already feeds `GET /dashboard` and `GET /ui/dashboard`
(`Handler.dashboardFor`) — the same call tier 2 and tier 1 already use. This tier adds
no database read; it reads eight pointer fields (four raw, four `_calc`) the query
already returns (RD7).

## Non-goals

- No dead-zone threshold or target-pressure comparison on the delta (RD3, not
  re-opened).
- No change to `internal/analytics` — every field this tier needs already exists on
  the read port.
- This is the roadmap's last tier — no further gaps are left in the Vehicle Status
  panel after this change.
