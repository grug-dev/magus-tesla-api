# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-16
Status: APPLIED 2026-09-16

---

## Why this proposal exists

RM61 tier 3 deleted the `analytics` efficiency branch. Three requirements were REMOVED from the
spec and two MODIFIED. The KB still describes the deleted code as live, in two places. Both say
the platform holds **two** pack-capacity definitions. It now holds **one**.

This is the failure the KB rule warns about: a stale guide makes an agent trust it instead of
reading the code.

What the spec no longer contains:

- `Recent Energy-Per-Kilometre Derivation` — REMOVED
- `Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile` — REMOVED
- `Insufficient Data Returns ok=false, Never a Fabricated Value` — REMOVED
- `No Cross-Module Database Access` — MODIFIED: `internal/account` is now listed among the
  packages `analytics` never imports
- `Module-Scoped Database Schema` — MODIFIED: `RecentEfficiency` dropped from its example list

## [guide] ## Adding a second monthly metric — REPLACE (final paragraph only)

`internal/charging` holds the platform's **only** pack-capacity definition: `packCapacityKWh`,
with its `62.0` fallback, reading the measured monthly value.

`internal/analytics` used to hold a second one — a model-coarse table keyed on `car_type`, in
`internal/analytics/capacity.go`. That file is **gone**. It was deleted together with the
`RecentEfficiency` branch that was its only consumer, because nothing ever called that branch.
No displayed number changed.

So there is nothing left to reconcile here. If a future change needs a model-aware capacity,
it starts from `charging`'s single definition, not from a second table.

One limit stays open: `charging`'s own `62.0` fallback is model-blind for a vehicle with no
measurement yet. `internal/charging` may not import `internal/account`, so it cannot resolve a
`car_type` by itself. That is a new ticket, not a leftover.

## [guide] ## Conventions & gotchas — APPEND

- **`analytics` imports no `internal/account` symbol at all** — production and test files alike.
  Its reads are scoped by vehicle identifier. Do not add an `account` import to resolve a
  vehicle attribute; that dependency was removed on purpose.
  _Source: spec analytics — Requirement: No Cross-Module Database Access._

- **`analytics.NewReader` takes the pool and nothing else.** Every surviving `Reader` method
  reads `vehicle_metrics` alone. The sibling ports belong to `NewRecalculator`, which writes
  those rows. Do not thread a telemetry, charging or account port into the reader.
  _Source: spec analytics — Requirement: Module-Scoped Database Schema._

- **The dashboard efficiency tile is not an `analytics` metric.** It reads
  `vehicle_metrics.KmPerPctCalc` through `LatestMetricsForVehicles`. The Wh/km derivation that
  once shared the word "efficiency" is deleted. Searching for it will find nothing.
  _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._

## [index] ## Architecture — ADD ROWS

| `pack capacity` | `charging`'s `packCapacityKWh` + its `62.0` fallback → `workflows/vehicle-monthly-metrics.md`. The `analytics` `car_type` table was DELETED by RM61; there is only one definition now. |

> **Note for apply-sync:** the `pack capacity` row already exists in `INDEX.md` (line ~146) with a
> now-false parenthetical — "the `analytics` `car_type` table is a different thing — see that
> guide's last section". ADD ROWS skips a row whose first cell exists, so this one must be
> **replaced by hand**, not appended. Flagging it rather than silently widening the block grammar.
