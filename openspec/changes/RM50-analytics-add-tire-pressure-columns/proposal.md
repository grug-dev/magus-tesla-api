# RM50-analytics-add-tire-pressure-columns

> Source: MAG-56 — https://linear.app/magus-monitor/issue/MAG-56/vehicle-status-change
> Roadmap: RM50-vehicle-status-subsections, tier 1 of 4. See that file for RD1–RD8
> (binding decisions this change does not re-open).

## Why

The dashboard "Vehicle Status" panel will soon show a Tire Pressure subsection (tier 4)
and a Travel Progress subsection (tier 2). Both need data that `vehicle_metrics` does not
expose yet. This change is the backend half: it adds the raw tyre-pressure columns and
opens up two fields that already exist but are not on the read port.

## What changes

Three independent parts.

**A. Four raw TPMS columns.** Add `tpms_pressure_fl_psi`, `tpms_pressure_fr_psi`,
`tpms_pressure_rl_psi`, `tpms_pressure_rr_psi` to `analytics.vehicle_metrics`. All
nullable `double precision`. Copied verbatim from `telemetry.Snapshot`'s existing public
`*float64` fields, on every row, including a day with no predecessor. No conversion, no
default value ever written here — `internal/telemetry` already stores these fields in
PSI.

**B. Expose two already-existing columns.** `distance_traveled_km_calc` and
`consumed_pct` already exist on `vehicle_metrics`. Add both to the
`LatestMetricsByAccount` SELECT and to `analytics.VehicleStatus` as pointer fields. No
migration for this half.

**C. Backfill migration.** A SQL migration copies the four new columns' values from
`telemetry.vehicle_snapshots` into existing `analytics.vehicle_metrics` rows, matched by
`(account_id, tesla_id, metric_date)`. This is a one-off cross-schema `UPDATE`, not a new
runtime read path — see `design.md` for the full rationale and the deviation it records.

## Breaking?

No. Every new column is nullable and additive. `VehicleStatus` gains two new pointer
fields — existing callers that build the struct by name are unaffected; a caller that
builds it positionally would break, but no such caller exists in this codebase (checked:
`internal/app`, `cmd/web`, `cmd/poller` all use named fields).

## Modules affected

- `internal/analytics` — the only module with code or schema changes.
- `internal/telemetry` — read only, through its existing public port. No change to this
  module (verified: `TpmsPressureFLPSI`/`FR`/`RL`/`RR` are already public `*float64`
  fields on `telemetry.Snapshot`).

## Read paths affected

`LatestMetricsByAccount` feeds `GET /dashboard` and `GET /ui/dashboard`
(`Handler.dashboardFor`) — the Vehicle Status panel the user sees on every dashboard
page load. This change widens that query's SELECT list by six columns (four new, two
newly exposed). No new WHERE or ORDER BY clause, so the query plan is unchanged; see
`design.md` for the index analysis.

## Non-goals (later tiers)

- No tyre-pressure delta / `_calc` columns — tier 3.
- No gateway UI change — tiers 2 and 4.
