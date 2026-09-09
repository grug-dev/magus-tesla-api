# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-09
Status: PENDING REVIEW

---

## Why this proposal replaced an earlier one

An earlier draft of this file was staged on 2026-09-08, after RM50 tier 1. It was never
applied. RM50 tier 3 has since archived, so this run regenerates the whole proposal against
the current spec. It carries the tier 1 delta that is still pending **plus** tier 3's.

The guide itself is in good shape. Tiers 1 and 3 both updated it by hand, in their own
change. So the column lists, the field lists, and most gotchas are already written there.
Do not re-add them.

What is still missing, and what this proposal carries:

1. Aliases on the Glossary line. Eight words a reader would search for do not appear.
2. Four behaviour rules the spec states and the guide does not.
3. A re-sourcing note for four bullets that cite change docs which are now archived.
4. The `INDEX.md` rows. **This is the important part.** Today nothing routes "tire pressure",
   "travel progress", or "tyre pressure variance" to this guide, so `kkpa-context-fetch`
   cannot resolve them at all.

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`, `battery level by day`, `per-day battery level`, `battery history`, `tire pressure`, `tyre pressure`, `TPMS`, `travel progress`, `battery drain`, `tyre pressure delta`, `tyre pressure variance`, `pressure change`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsByAccount` returning `analytics.VehicleStatus`. Read side for the per-day battery history: `analytics.Reader.BatteryLevelByDay` returning `analytics.DayBattery`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over its own, still-`public`, `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')` — later changed again by `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) to `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`, reusing the string that named `telemetry`'s table before RM31 to now name `charging`'s table instead (see that change's `design.md` §6). **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port. **Changed by RM40 tier 1:** a bounded per-day battery-level/range read port was added over the same table — no new column, no migration. **Changed by RM50 tier 1:** `vehicle_metrics` gained four TPMS raw-observation columns (with a one-off backfill migration for pre-existing rows), and `LatestMetricsByAccount`'s projection widened by two more columns that already existed on the table (`distance_traveled_km_calc`, `consumed_pct`) — no new query, no new index. In the UI these two are called **Travel Progress** and **Battery Drain** (RM50 tier 2). **Changed by RM50 tier 3:** `vehicle_metrics` gained four TPMS **delta** (`_calc`) columns, one per wheel, backfilled for pre-existing rows by a self-join migration (not cross-module — the tier 1 raw columns already sit on the same table), and `LatestMetricsByAccount`'s projection widened by these four new columns.

<!--
The Internal-name line is reproduced from the live guide, which tiers 1 and 3 already kept
current, plus one sentence naming the two UI labels. Only the Known-as line really changes:
eight aliases added. REPLACE is used because the template allows no other verb for Glossary.
-->

## [guide] ## Conventions & gotchas — APPEND

- **Each wheel is independent — one absent reading never blanks the other three.** A capture that reports three wheels and not the fourth persists those three exactly as reported and leaves only the fourth absent. The same holds for the four delta columns: a wheel missing on either day makes that wheel's delta absent, and the other three still compute. Do not treat TPMS as an all-or-nothing group, and never substitute a fabricated reading. _Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations; Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._
- **On a predecessor-less latest day, the derived figures are absent while the raw observations are present.** `LatestMetricsByAccount` still returns battery, range, odometer, all eight status observations and all four raw TPMS readings for that vehicle, but `DistanceTraveledKmCalc`, `ConsumedPct` and the four `TpmsPressure*PSICalc` deltas are nil. A caller that renders "0 km", "0%" or a flat arrow there is showing a value the capability never computed. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **A pre-migration latest row reports absent TPMS values, never a fabricated pressure or delta.** The rule that already applies to the eight status observations applies to the four raw TPMS readings and the four deltas: a row whose day predates tracking, and which the one-off backfill did not match, keeps them absent. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **Old rows get their deltas ONLY from the one-time backfill, never from a lazy read.** A row persisted before delta tracking stays absent until either the backfill migration ran, or a new capture triggers a recalculation of that same day. Reading the row does not compute the delta on the fly, and the absence is never filled with a fabricated value. _Source: spec analytics — Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._

## [guide] ## Conventions & gotchas — RE-SOURCE (manual edit, not an automatic block)

Four existing bullets cite change-folder design docs that are now archived:

- two cite `openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md`
  → now `openspec/changes/archive/analytics/2026-09-08-RM50-analytics-add-tire-pressure-columns/`
- two cite `openspec/changes/RM50-analytics-add-tire-pressure-variance/design.md`
  → now `openspec/changes/archive/analytics/2026-09-09-RM50-analytics-add-tire-pressure-variance/`

The citations are not wrong, and the archive is immutable, so nothing is broken. But the
requirements now live in the main spec, which is the more durable source. When you next touch
those bullets, change:

- `_Source: openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md D2._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations._`
- `_Source: design.md Part C._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations._`
- `_Source: openspec/changes/RM50-analytics-add-tire-pressure-variance/design.md D1/D2._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._`
- `_Source: design.md Part B._`
  → `_Source: spec analytics — Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._`
  (keep the archive path in the bullet text itself if you want the rationale trail)

This is deliberately NOT an APPEND/REPLACE block. `apply-sync` applies blocks near-verbatim,
and a REPLACE of the whole section would risk clobbering bullets written by hand. Do this one
by hand.

## [index] ## Entities — ADD ROWS

| `tire pressure` | the four `tpms_pressure_*_psi` columns of `vehicle_metrics` (RM50 tier 1), raw per-day observations read via `analytics.Reader.LatestMetricsByAccount` | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure` | synonym of `tire pressure` | entity | `entities/vehicle-metrics/guide.md` |
| `TPMS` | synonym of `tire pressure` (tire-pressure monitoring system) | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure delta` | the four `tpms_pressure_*_psi_calc` columns of `vehicle_metrics` (RM50 tier 3) — each day's wheel pressure minus the previous day's, NULL without a predecessor or a raw reading | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure variance` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `pressure change` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `travel progress` | UI name for `vehicle_metrics.distance_traveled_km_calc`, exposed on `analytics.VehicleStatus.DistanceTraveledKmCalc` (RM50) | entity | `entities/vehicle-metrics/guide.md` |
| `battery drain` | UI name for `vehicle_metrics.consumed_pct`, exposed on `analytics.VehicleStatus.ConsumedPct` (RM50) | entity | `entities/vehicle-metrics/guide.md` |

<!--
Eight aliases, no row per column. A question about tpms_pressure_rl_psi_calc routes through
the `tyre pressure delta` row to the same guide, which already lists every column by name.

"travel progress" and "battery drain" are the UI labels from Linear MAG-56. RM50 tier 4 builds
the tyre-pressure half of that panel, and an agent working on it will search "tire pressure"
or "pressure change" long before it searches `tpms_pressure_fl_psi_calc`.
-->
