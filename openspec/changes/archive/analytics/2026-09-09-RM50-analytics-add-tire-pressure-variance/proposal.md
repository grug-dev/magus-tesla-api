# RM50-analytics-add-tire-pressure-variance

> Source: MAG-56 — https://linear.app/magus-monitor/issue/MAG-56/vehicle-status-change
> Roadmap: RM50-vehicle-status-subsections, tier 3 of 4. See that file for RD1–RD12
> (binding decisions this change does not re-open).

## Why

Tier 4 (gateway, next) will show an up/down arrow on each tyre-pressure tile. It needs a
day-over-day delta value to drive that arrow. Tier 1 already stores the four raw PSI
readings on `vehicle_metrics`. This change adds the four delta columns and their maths.

## What changes

**A. Four `_calc` delta columns.** Add `tpms_pressure_fl_psi_calc`,
`tpms_pressure_fr_psi_calc`, `tpms_pressure_rl_psi_calc`, `tpms_pressure_rr_psi_calc` to
`analytics.vehicle_metrics`. All nullable `double precision`. Each one is today's raw
reading minus yesterday's raw reading, for the same wheel.

**B. Delta maths in the pure layer.** `consumption.go`'s `deriveConsumption` computes the
four deltas from the `(prev, cur)` snapshot pair, the same function that already computes
`distance_traveled_km_calc`. NULL when the day has no predecessor. Also NULL, per wheel,
when either day's own raw reading for that wheel is missing — there is nothing to
subtract.

**C. Backfill migration.** A SQL migration computes the four deltas for every existing row
by joining `analytics.vehicle_metrics` against itself, one row against its own
predecessor row. This stays entirely inside the `analytics` schema — no other module's
table is read.

**D. Expose on the read port.** `LatestMetricsByAccount` / `analytics.VehicleStatus` gain
the four fields, so a future gateway tier can read them.

**E. Unit tests.** The pure delta maths gets a unit test. No test touches markup, CSS, or
element placement (RD8).

## Breaking?

No. All four columns are nullable and additive. `VehicleStatus` gains four new pointer
fields — every call site in this codebase builds it by field name, so no positional
literal breaks (same check tier 1 already ran, unaffected by this change).

## Modules affected

`internal/analytics` only. No other module is touched or read.

## Read paths affected

`LatestMetricsByAccount` feeds `GET /dashboard` / `GET /ui/dashboard`
(`Handler.dashboardFor`) — the same read path tier 1 widened. This change adds four more
projected-only columns to that query's SELECT list. No new `WHERE` or `ORDER BY`, so the
query plan is unchanged — see `design.md` for the index analysis.

## Non-goals (later tiers)

- No gateway UI change — tier 4 reads these fields and renders the arrows.
- No dead-zone threshold and no comparison against a target pressure (RD3 — rejected at
  the roadmap level, not re-opened here).

## Accepted cost — stated up front, not a defect

The delta partly reflects ambient air temperature, roughly 1 PSI per 5.5°C, not only a
real pressure change or leak. The roadmap accepted this (RD3). No task in this change may
add a threshold or a temperature correction to "fix" it.
