# Proposal — RM67-analytics-add-vehicle-monthly-metrics

> Source: MAG-73 — https://linear.app/magus-monitor/issue/MAG-73/analytics-monthly-metrics-table-distance-efficiency-and-copied

## What

Tier 2 of 4 in roadmap `RM67-vehicle-monthly-metrics-table`. Add a new table,
`analytics.vehicle_monthly_metrics`, and a new use case that fills it: one row per
vehicle per calendar month, holding that month's distance, battery use and
efficiency — split into all days, weekdays and weekends — plus the pack capacity
copied from `internal/charging`.

One migration creates **every** column the whole roadmap needs, including the
external-charge (`ext_*`), Supercharger (`sc_*`), and JSONB columns tier 3 fills
later. This tier only computes and writes the `vehicle_metrics`-derived figures and
the copied capacity. The `ext_*`/`sc_*` columns are written with their zero value on
every call until tier 3 replaces that zero with a real aggregate — see `design.md`
D8.

## Why

`analytics.vehicle_metrics` holds one row per vehicle per **day**. Nothing
summarises a **month** today. A later stats page (Linear MAG-87, out of this
roadmap's scope) needs monthly totals to avoid summing daily rows on every read —
this table is that precomputed monthly summary, matching the project's
read-heavy Performance-Profile.

## Breaking change?

No. This adds a new table and a new port. No existing table, column, port, or
call site changes.

## Modules affected

- **`internal/analytics`** (this change) — owns the new table, the new port
  (`MonthlySyncer`), the migration, and the sqlc queries.
- **`internal/charging`** — read-only. This change calls the existing
  `charging.MonthlyCapacityReader.CapacityForMonth` port (built by tier 1,
  already archived). No `charging` file changes.
- Root `sqlc.yaml` — one line added to the existing `analytics` block's
  `rename:` map, so sqlc names the new generated struct `VehicleMonthlyMetric`
  instead of its own default guess.

## Read paths affected

None yet. This tier adds a **write-side** use case
(`MonthlySyncer.SyncMonth`) and a table with no `Reader` method over it. No
dashboard, HTTP handler, or existing read port changes. A read port for this
table is future work, once a real consumer (MAG-87) exists — see
`openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md` "Future work".

## Scope boundary with the rest of the roadmap

- Tier 1 (`RM67-charging-add-monthly-capacity-read`, already archived) built
  the one `charging` port this tier reads. Not reopened here.
- Tier 3 (`RM67-analytics-add-monthly-charging-aggregates`) fills the `ext_*`,
  `sc_*` and JSONB columns this tier's migration creates but leaves at zero.
- Tier 4 (`RM67-app-add-monthly-metrics-step`) wires the nightly trigger
  (`internal/app`) that calls this tier's `MonthlySyncer.SyncMonth` every
  night, for the current and the previous month, per vehicle. Not built here.
