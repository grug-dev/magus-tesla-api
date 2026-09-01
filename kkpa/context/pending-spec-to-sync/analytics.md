# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-01
Status: PENDING REVIEW

Derived from the two requirements RM38 tier 1 added to the analytics capability:
**Precomputed Vehicle Status Observations** and **Latest Vehicle Status Per Account**.
Resolved against `INDEX.md`: `vehicle metrics` / `calc fields` already route to
`entities/vehicle-metrics/guide.md`, so this is a delta on that guide — no new guide, no new
INDEX concept row. No `## Component map` block (spec.md carries behavior, not file paths).

---

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsByAccount` returning `analytics.VehicleStatus`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charge_sessions`, and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`. **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port.

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct` derived
alongside them.

The eight status observation columns (RM38): `locked`, `sentry_mode`, `car_version`,
`inside_temp_c`, `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at` —
raw per-day observations, not derived figures.

## [guide] ## Conventions & gotchas — APPEND

- **The eight status observations are populated on EVERY row, including a predecessor-less day — the opposite rule to the `_calc` columns.** They are raw observations copied verbatim from that day's own capture, not deltas, so there is nothing for a missing predecessor to invalidate. Gating them behind the `prev == nil` check that the `_calc` columns use would blank a vehicle's first tracked day. _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **The eight are copied verbatim, never re-derived or converted.** They mirror the day's telemetry capture exactly as reported; adding a computation, a default, or a unit conversion on this path is a defect. _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **Existing rows are never retroactively populated — there was no backfill.** A row written before the status columns existed keeps all eight absent until a new capture triggers a recalculation of that same day. Absence must never be replaced with a fabricated default such as "unlocked" or "sentry off". _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **NULL on `sentry_mode` is ambiguous; NULL on the other seven is not.** For `sentry_mode`, NULL means EITHER "the vehicle did not report sentry" OR "this row predates the status columns". Disambiguate with `captured_at`: NULL sentry with a non-NULL `captured_at` means "not reported"; both NULL means "predates tracking". _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **`LatestMetricsByAccount` returns the capability's own domain type, never another module's capture record.** It yields `analytics.VehicleStatus`, never `telemetry.Snapshot` — the gateway must not receive a telemetry type through this port. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **One result per vehicle, each on that vehicle's OWN latest day — not the account's latest day overall.** With two vehicles whose most recent computed days differ, each entry must describe its own vehicle's latest day. This is what the `DISTINCT ON (tesla_id) … ORDER BY tesla_id, metric_date DESC` shape guarantees; changing the ORDER BY breaks it silently. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **An account with no computed rows returns an empty result and no error** — never an error, and never a nil-versus-empty distinction the caller has to handle. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

## [index] ## Glossary & routing — entities — ADD ROWS

| `vehicle status` | the eight raw status observations on `vehicle_metrics`, read via `analytics.Reader.LatestMetricsByAccount` → `analytics.VehicleStatus` (RM38) | entity | `entities/vehicle-metrics/guide.md` |
| `latest vehicle status` | `analytics.Reader.LatestMetricsByAccount` / `LatestVehicleMetricsByAccount` (`DISTINCT ON (tesla_id)` over `vehicle_metrics`) | entity | `entities/vehicle-metrics/guide.md` |
