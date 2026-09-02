# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-02
Status: PENDING REVIEW

Derived from the single requirement RM40 (ticket MAG-41) added to this capability:
**"Per-Day Battery Level and Range Read"**. No other requirement in the spec changed.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`, `battery level by day`, `per-day battery level`, `battery history`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsByAccount` returning `analytics.VehicleStatus`. Read side for the per-day battery history: `analytics.Reader.BatteryLevelByDay` returning `analytics.DayBattery`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charge_sessions`, and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`. **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port. **Changed by RM40 tier 1:** a bounded per-day battery-level/range read port was added over the same table — no new column, no migration.

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct` derived
alongside them.

The eight status observation columns (RM38): `locked`, `sentry_mode`, `car_version`,
`inside_temp_c`, `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at` —
raw per-day observations, not derived figures.

The per-day battery read (RM40) serves `battery_level_pct` and `battery_range_km` — both
original `NOT NULL` columns of the table, both raw per-day observations like the RM38 eight,
**not** derived `_calc` figures.

## [guide] ## Conventions & gotchas — APPEND

- **The per-day battery read reports a day even when it has no computable predecessor** — unlike
  distance travelled, battery-percentage-used, and the corrected consumed-percentage figure, which
  all exclude predecessor-less days. Battery level and range are raw observations, not deltas
  against a prior day, so the predecessor question does not apply to them. Filtering them the way
  the sibling reads are filtered would silently hide every vehicle's **first tracked day**.
  _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **A day with no precomputed observation is ABSENT from the result, never zero-valued** — the read
  is sparse. No fabricated or zero entry is substituted, because a stored zero is a real battery
  reading and would be indistinguishable from a missing one.
  _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **An empty range, or a vehicle with no observations, returns an empty result and NO error** —
  the same empty-result contract every other read port on this capability carries. Callers range
  over the result directly; there is no nil case to guard.
  _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **Every per-day battery read is scoped to the requesting account's own vehicle** — two accounts
  whose vehicles share a vehicle identifier never see each other's observations, even on the same
  calendar day. This is the capability's tenant boundary, not an optimization.
  _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **The date carried by a per-day result is a FINAL bucket key** — consumers bucket on it verbatim
  and must never re-project it through a day-normalizing helper of their own. Identical to the rule
  the per-day consumption and distance reads already carry.
  _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._

## [index] ## Glossary & routing — entities — ADD ROWS

| `battery level by day` | `analytics.Reader.BatteryLevelByDay` / `analytics.DayBattery` (`vehicle_metrics.battery_level_pct`, `battery_range_km`) | entity | `entities/vehicle-metrics/guide.md` |
| `per-day battery level` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery history` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
