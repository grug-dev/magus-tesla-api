# Proposal — RM67-analytics-add-monthly-charging-aggregates

> Source: MAG-73 — https://linear.app/magus-monitor/issue/MAG-73/analytics-monthly-metrics-table-distance-efficiency-and-copied

## What

Tier 3 of 4 in roadmap `RM67-vehicle-monthly-metrics-table`. Fill the charging columns
that tier 2 left at zero on `analytics.vehicle_monthly_metrics`:

- `ext_ac_energy_kwh`, `ext_ac_cost`, `ext_ac_entry_count`, `ext_ac_ending_battery_dist`
- `ext_dc_energy_kwh`, `ext_dc_cost`, `ext_dc_entry_count`, `ext_dc_ending_battery_dist`
- `sc_energy_kwh`, `sc_cost`, `sc_session_count`, `sc_ending_battery_dist`

`MonthlySyncer.SyncMonth` keeps its exact signature. This change only widens what it
computes before its existing `UpsertVehicleMonthlyMetric` call — that call's shape does
not change, because tier 2 already built it to always set every column (design.md D8 of
the archived tier 2 change).

No new table, column, index, or migration. No new `charging` port — both reads this
change needs already exist (roadmap RD9).

## Why

Tier 2 built the table and the use case, but left every charging figure at its documented
zero placeholder on purpose, so the whole roadmap did not need two migrations on one new
table (RD1). This tier is the one that replaces the placeholder with a real number, so a
future stats page (MAG-87, out of this roadmap) can read real charging totals per month.

## Breaking change?

No. No existing table, column, port signature, or call site changes shape. The only
widened surface is `NewMonthlySyncer`'s constructor, and it is called from nowhere yet
outside this module — tier 4 (`RM67-app-add-monthly-metrics-step`) is the first real
caller, and it has not started.

## Modules affected

- **`internal/analytics`** (this change) — the only module with file changes. Extends
  `monthlySyncer`'s implementation and its constructor with two more read-only
  dependencies on sibling ports it already imports for other reasons.
- **`internal/charging`** — read-only, through ports that already exist:
  `charging.Reader.ListEntriesByVehicleBetween` and
  `charging.SuperchargerSessionAnalyticsReader.ListSessionsByVehicleBetween`. No
  `charging` file changes.
- **`internal/clock`** — read-only. `clock.Zone()` decides which calendar month a
  Supercharger session belongs to (see D4).

## Read paths affected

None. This is still a write-side use case (`MonthlySyncer.SyncMonth`) with no `Reader`
method over `vehicle_monthly_metrics`. No dashboard, HTTP handler, or existing read port
changes.

## Scope boundary with the rest of the roadmap

- Tier 1 (`RM67-charging-add-monthly-capacity-read`) and tier 2
  (`RM67-analytics-add-vehicle-monthly-metrics`) are archived. Not reopened here — this
  change does not touch the capacity read or the table's shape.
- Tier 4 (`RM67-app-add-monthly-metrics-step`) wires the nightly trigger
  (`internal/app`) that calls `SyncMonth` every night. Not built here — this change only
  makes `SyncMonth` compute real numbers once something does call it.

## Decisions carried over from the roadmap, not reopened here

RD1, RD4, RD8, RD9, RD10, RD11 are settled at the roadmap level
(`openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md`). RD13–RD17, listed below,
were settled with the owner on 2026-09-18, specifically for this tier, before any
artifact for it was written:

- **RD13** — an external charge with no `ChargingType` feeds neither `ext_ac_*` nor
  `ext_dc_*`. It is skipped completely.
- **RD14** — a Supercharger session belongs to the month of its `ChargeStopDateTime`
  read in the platform zone (`internal/clock`), not UTC.
- **RD15** — `currency` is always `"COP"`; every cost is summed whatever the record's
  own currency says.
- **RD16** — every external charge counts, whatever its `Status`.
- **RD17** — a record with no `EndBatteryPct` still counts, but enters no bucket. Bucket
  edges are half-open, the last one closed: `[0,20) [20,40) [40,60) [60,80) [80,100]`.

`design.md` explains how each is honoured; it does not re-argue any of them.
