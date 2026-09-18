Source: MAG-59 — https://linear.app/magus-monitor/issue/MAG-59/travel-progress-drive-the-updown-arrows-from-the-previous-day
Roadmap: openspec/roadmaps/RM66-travel-progress-trends.md
Tier: 2 of 3 (`analytics`, depends on tier 1 `platform` — the delta-guard must
exist before this tier renames the four tyre-pressure columns)

## Why

The dashboard's three Travel Progress tiles (Distance travelled, Battery used,
Efficiency) show a hardcoded up/down arrow. Roadmap decision **D-C** says every
tile must compare against the previous day for real. Today the three figures
behind those tiles — `distance_traveled_km_calc`, `consumed_pct`, `km_per_pct_calc`
— are each a **single day's own value**, not a day-over-day change. There is
nothing to compare against, so the arrow was invented in the template.

This tier adds the three missing day-over-day deltas (**D-B**, **D-D**) so tier 3
can wire real arrows. It also finishes the rename tier 1 prepared for: the four
tyre-pressure delta columns (`tpms_pressure_*_psi_calc`) become
`tpms_pressure_*_psi_delta_calc`, so every delta column in the database now
carries the one clear suffix D-D picked.

This dispatch, like tier 1's, produces **OpenSpec artifacts only** — no
migration file, no Go edit, no `sqlc generate`.

## What Changes

- **One new migration** in `internal/analytics/db/migrations/` (the existing
  `20260917000001_baseline.sql` is a frozen, applied baseline — it is never
  edited):
  - Adds three nullable `double precision` columns to `analytics.vehicle_metrics`:
    `distance_traveled_km_delta_calc`, `consumed_pct_delta_calc`,
    `km_per_pct_delta_calc`.
  - Renames the four existing `tpms_pressure_*_psi_calc` columns to
    `tpms_pressure_*_psi_delta_calc`.
  - Backfills the three new columns on existing rows with a self-join on
    `metric_date - 1`, mirroring the precedent `RM50-analytics-add-tire-pressure-variance`
    already set for the four tyre-pressure deltas. See design.md's "Verified
    against the code" section for why this tier does **not** rely on the
    roadmap's "Reconcile recalculates full history" statement to backfill —
    that claim no longer holds after `RM44-charging-add-change-detecting-mirror`.
- **`deriveVehicleMetrics`** (`internal/analytics/consumed.go`) computes the
  three new deltas from the row it built one loop-iteration earlier, per D-B —
  never a third snapshot, never a second predecessor lookup.
- **`LatestVehicleMetricsByVehicles`** (`internal/analytics/db/query.sql`)
  widens its `SELECT` to project the three new columns and the four renamed
  ones under their new names. No index change — see design.md's proof.
- **`analytics.VehicleStatus`** gains the three new pointer fields and renames
  its four `TpmsPressure*PSICalc` fields to `TpmsPressure*PSIDeltaCalc`.
- **The tier-1 `delta-guard` baseline shrinks**: the four renamed SQL names and
  their eight matching Go names (four domain-cased, four sqlc-cased) are
  removed from both baseline lists in the `Makefile`.
- **Cross-module fallout, owned by the leader, not this dispatch or its
  implementation worker**: renaming `VehicleStatus`'s four PSI-calc fields
  breaks `internal/gateway/handlers/handlers.go:580-583` and
  `handlers_test.go`. The roadmap names this explicitly; this proposal repeats
  it so the pipeline's own tracking does not lose it.

## Breaking / modules affected

- **Breaking for callers of `analytics.VehicleStatus`** — the four
  `TpmsPressure*PSICalc` field names change. The only in-repo caller is
  `internal/gateway`, and the roadmap already assigns that fix to the leader.
- **Modules affected:** `analytics` (schema, port, derivation — this tier);
  `gateway` (leader-integrated fix, tier 2's wave commit); `platform` (baseline
  shrink only, no new guard behavior).
- **Read path affected:** `LatestVehicleMetricsByVehicles`, backing
  `analytics.Reader.LatestMetricsForVehicles` — the dashboard's per-request
  read. Widened by projection only; see design.md's index proof.

## Database gate

This change adds and renames columns on `analytics.vehicle_metrics`. Per
`openspec/config.yaml`'s `design` rules and `CLAUDE.md`'s `Design-Gates`, the
full schema, the rationale, and an index plan justified against the read
pattern are in `design.md`, and require the user's explicit confirmation
before implementation starts.
