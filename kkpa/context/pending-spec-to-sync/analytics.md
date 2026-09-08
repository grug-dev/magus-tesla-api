# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-08
Status: PENDING REVIEW

---

## Why this proposal is small

`RM50-analytics-add-tire-pressure-columns` already updated this guide by hand, in the same
change. So most of what the spec now says is already written there. Do not re-add it.

This proposal carries only the delta that is still missing:

1. Two behaviour rules the spec states and the guide does not.
2. One re-sourcing note — an existing bullet cites a change doc that is now archived.
3. The `INDEX.md` rows. This is the important part. Today nothing routes the words
   "tire pressure" or "travel progress" to this guide, so `kkpa-context-fetch` cannot
   resolve them.

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`, `battery level by day`, `per-day battery level`, `battery history`, `tire pressure`, `tyre pressure`, `TPMS`, `travel progress`, `battery drain`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsByAccount` returning `analytics.VehicleStatus`. Read side for the per-day battery history: `analytics.Reader.BatteryLevelByDay` returning `analytics.DayBattery`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over its own, still-`public`, `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')` — later changed again by `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) to `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`, reusing the string that named `telemetry`'s table before RM31 to now name `charging`'s table instead (see that change's `design.md` §6). **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port. **Changed by RM40 tier 1:** a bounded per-day battery-level/range read port was added over the same table — no new column, no migration. **Changed by RM50 tier 1:** `vehicle_metrics` gained four TPMS raw-observation columns (with a one-off backfill migration for pre-existing rows), and `LatestMetricsByAccount`'s projection widened by two more columns that already existed on the table (`distance_traveled_km_calc`, `consumed_pct`) — no new query, no new index. In the UI these two are called **Travel Progress** and **Battery Drain** (RM50 tier 2).

<!--
Only the Known-as line really changed: five aliases added (tire pressure, tyre pressure,
TPMS, travel progress, battery drain). The Internal-name line is reproduced verbatim from
the live guide, plus one closing sentence naming the two UI labels. REPLACE is used because
the template allows no other verb for Glossary.
-->

## [guide] ## Conventions & gotchas — APPEND

- **Each wheel is independent — one absent reading never blanks the other three.** A capture that reports three wheels and not the fourth persists those three exactly as reported and leaves only the fourth absent. Do not treat TPMS as an all-or-nothing group, and never substitute a fabricated reading for the missing wheel. _Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations._
- **On a predecessor-less latest day, the two travel-progress figures are absent while the raw observations are present.** `LatestMetricsByAccount` still returns battery, range, odometer, all eight status observations and all four TPMS readings for that vehicle, but `DistanceTraveledKmCalc` and `ConsumedPct` are nil. This is the same predecessor rule the other per-day reads follow. A caller that renders "0 km" or "0%" there is showing a value the capability never computed. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **A pre-migration latest row reports absent TPMS, never a fabricated pressure.** The same rule that already applies to the eight status observations applies to the four TPMS readings: a row whose day predates tracking, and which the one-off backfill did not match, keeps them absent. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

## [guide] ## Conventions & gotchas — RE-SOURCE (manual edit, not an automatic block)

Two existing bullets cite `openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md`.
That folder is now archived at
`openspec/changes/archive/analytics/2026-09-08-RM50-analytics-add-tire-pressure-columns/`.

The citations are not wrong, and the archive is immutable, so nothing is broken. But the
requirement now lives in the main spec, which is the more durable source. When you next touch
those two bullets, change:

- `_Source: openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md D2._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations._`
- `_Source: design.md Part C._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations._`
  (keep the archive path in the bullet text itself if you want the rationale trail)

This is deliberately NOT an APPEND/REPLACE block. `apply-sync` applies blocks near-verbatim,
and a REPLACE of the whole section would risk clobbering bullets written by hand. Do this one
by hand.

## [index] ## Entities — ADD ROWS

| `tire pressure` | the four `tpms_pressure_*_psi` columns of `vehicle_metrics` (RM50), raw per-day observations read via `analytics.Reader.LatestMetricsByAccount` | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure` | synonym of `tire pressure` | entity | `entities/vehicle-metrics/guide.md` |
| `TPMS` | synonym of `tire pressure` (tire-pressure monitoring system) | entity | `entities/vehicle-metrics/guide.md` |
| `travel progress` | UI name for `vehicle_metrics.distance_traveled_km_calc`, exposed on `analytics.VehicleStatus.DistanceTraveledKmCalc` (RM50) | entity | `entities/vehicle-metrics/guide.md` |
| `battery drain` | UI name for `vehicle_metrics.consumed_pct`, exposed on `analytics.VehicleStatus.ConsumedPct` (RM50) | entity | `entities/vehicle-metrics/guide.md` |

<!--
Five aliases, no row per column. A question about tpms_pressure_rl_psi routes through the
`tire pressure` row to the same guide, which already lists every column by name.

"travel progress" and "battery drain" are the UI labels from Linear MAG-56. They are indexed
now because RM50 tier 2 builds that panel, and an agent working on it will search those words
before it searches `distance_traveled_km_calc`.
-->
