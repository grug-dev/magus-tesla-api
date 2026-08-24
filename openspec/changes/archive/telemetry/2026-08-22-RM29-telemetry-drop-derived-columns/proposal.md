Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 4 of 8 (`telemetry` drops the five `_calc` columns; depends on tier 3
`RM29-analytics-add-vehicle-metrics`, archived. Tiers 1–3 are archived; tiers 5–7
(`RM29-analytics-own-charge-gaps`, `RM29-charging-add-charge-sessions`,
`RM29-app-add-process-vehicle-data`) are independent of this tier; tier 8 is parked)
Unit tests: characterization only (roadmap D10) — telemetry's existing derivation
tests move to `internal/analytics` and must assert byte-for-byte identical output
for identical inputs. No new unit tests for new code beyond the move and the Test
Contract in design.md.

## Why

The roadmap's one-line scope for this tier — "drop the five `_calc` columns from
`vehicle_snapshots`" — is **misleading if read literally**, and reading it literally
would delete working data. The five values are still needed. What is wrong today is
*who computes them*, not *whether they exist*.

1. **`telemetry` computes a derivation whose only consumer is another module.**
   `deriveConsumption` (`internal/telemetry/service.go:581`) computes
   `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
   `estimated_range_km_calc` and `days_spanned_calc` at capture time, on a table
   whose whole purpose is "what Tesla reported". Nothing in `telemetry` reads them
   back. `internal/analytics` does — and since tier 3 it does not even *derive*
   them, it **copies them verbatim** (`internal/analytics/consumed.go`,
   `deriveVehicleMetrics`' `DistanceTraveledKmCalc: cur.DistanceTraveledKmCalc`
   block) out of `telemetry.Snapshot` into `vehicle_metrics`. A derived-metrics
   module reading another module's derived columns is the exact boundary blur
   MAG-26 exists to fix; tier 3 gave those five values a home analytics owns, and
   this tier finally makes analytics *produce* them.

2. **Today's stored `_calc` values can already be stale, and nothing detects it.**
   `vehicle_snapshots` is no longer append-only: a same-day re-capture REPLACES the
   row via the dedupe UPSERT (`20260805000001`). Telemetry derives `_calc` only for
   the row *being written*, so when day N's row is replaced, day N+1's row — whose
   `_calc` values were computed against the *old* day-N reading — is never
   recomputed. Its `distance_traveled_km_calc` and `battery_used_pct_calc` keep
   describing a predecessor that no longer exists. Moving the derivation into
   `Recalculate` fixes this **by construction**: analytics recomputes from whatever
   the two rows say *now*, every time the watermark says the day was touched. This
   tier is therefore a correctness gain, not only a refactor.

3. **Analytics cannot currently reach the predecessor it needs.** `Recalculate`
   fetches `SnapshotsByVehicleBetween(start-1d, end+1d)` and `Reconcile` widens by a
   further day — a net two-calendar-day lookback. That is enough *only because
   telemetry pre-computed the delta against the true predecessor at write time*. The
   moment analytics derives it itself, a vehicle with a capture gap longer than the
   lookback has no predecessor in the fetched window at all, and today's code path
   for that case emits a row with every derived value NULL — a **silently dropped
   day**. That is why this change adds one new telemetry read port (below) rather
   than simply moving a function.

## What Changes

- **The derivation moves from `telemetry` to `analytics`.** `deriveConsumption`
  moves out of `internal/telemetry/service.go` into a new
  `internal/analytics/consumption.go` as a pure function over two
  `telemetry.Snapshot` values, computing the five figures from raw
  `odometer_km` / `battery_level_pct` / `captured_date` during `Recalculate`.
  Formulas, subtraction order, the `> 0` divisor guard and the `×100` range formula
  are carried over **unchanged** (roadmap D10). See design.md D1, D5.
- **`telemetry.Reader` gains `SnapshotPrecedingDay`** — the single most recent
  snapshot strictly before a given calendar day for one vehicle, or a nil result
  when none exists. This is the exact-predecessor lookup analytics needs; it is
  served by a **backward scan of the existing
  `idx_vehicle_snapshots_vehicle_time`** index, and adds **no new index**. See
  design.md D2 and the Index Plan.
- **`telemetry.Snapshot` loses five fields** (`DistanceTraveledKmCalc`,
  `BatteryUsedPctCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc`,
  `DaysSpannedCalc`), and `vehicle_snapshots` loses the five matching columns
  (migration under `internal/telemetry/db/migrations/`).
- **Telemetry's now-dead write-path plumbing is removed**: `deriveConsumption`,
  `dayStart`, the `previousSnapshot` store seam, its call site in `attemptVehicle`,
  and the private `PreviousSnapshotForVehicle` query — the last of which is
  *replaced by*, not merely deleted alongside, `SnapshotPrecedingDay`'s query.
  See design.md D8.
- **Existing `vehicle_metrics` rows are rebuilt by resetting the watermark.** A
  migration under `internal/analytics/db/migrations/` deletes the
  `vehicle_metric_watermarks` rows for the `vehicle_snapshots` source; the next
  nightly `Reconcile` then treats that source as never-incorporated and backfills
  every vehicle's whole history through the new derivation (`recalculate.go`'s
  own epoch rule, tier 3 design D7). No one-off `cmd/` runner, no half-and-half
  table. See design.md D3.
- **`Recalculate` widens its charge-source fetches when — and only when — the
  predecessor lies outside the fetched snapshot window.** Reaching further back for
  the predecessor without also reaching further back for that span's Supercharger
  sessions and manual entries would compute a *wrong* corrected consumption for
  exactly the gap days this change exists to recover. Not covered by the interview;
  decided here. See design.md D8b.
- **`internal/analytics/AGENTS.md` staleness corrected** — its "Doc-Pack (module)"
  and "Responsibility" sections still describe analytics as "a pure Go derivation
  module with no persistence" whose database arrives "at tier 3 — a fact not yet
  true today". Tier 3 landed; the file's own "Data ownership" and "Testing"
  sections were updated then, but those two earlier sections were not. Correcting
  them is in scope (CLAUDE.md, docs-track-structural-change).

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key changes. Every
chart's displayed value is pinned identical by characterization test (roadmap D10),
with two deliberate, named exceptions that are **bug fixes, not regressions**:
(1) a vehicle-day whose predecessor is older than the fetched window now appears in
the charts instead of silently vanishing (design.md Fixture D); (2) a day whose
predecessor row was replaced by a same-day re-capture now shows a delta against the
replacement instead of a stale one.

**Yes — internally.** `telemetry.Snapshot` loses five public fields; `telemetry.Reader`
gains one method (additive). `vehicle_snapshots` loses five columns — **irreversible
at the data level**; see design.md D9 for exactly what the down migration can and
cannot restore. Every in-repo consumer is re-pointed in this change.

## Modules Affected

- **`internal/telemetry/`** — the owning module. Drops the five columns, the five
  `Snapshot` fields, `deriveConsumption`, `dayStart`, the `previousSnapshot` store
  seam and its call site; adds `Reader.SnapshotPrecedingDay` plus its query. Its two
  derivation test files move out (below).
- **`internal/analytics/`** — gains the derivation (`consumption.go`), the
  `SnapshotPrecedingDay` call inside `Recalculate`, the widened charge-source fetch,
  the watermark-reset migration, and the characterization tests moved from
  telemetry. `vehicle_metrics`' schema is **unchanged** — tier 3 already created
  every column this tier needs.
- **`internal/gateway/`** — not touched. `history_test.go`'s only reference to a
  `_calc` column is a comment naming design.md Fixture B; no gateway code reads a
  `Snapshot` `_calc` field.
- **`cmd/`** — not touched. No composition-root wiring changes: `Recalculate`'s and
  `Reconcile`'s constructor signatures are unchanged.

## Database Changes

**Two migrations, one per module** (roadmap module-ownership rule — a telemetry
migration must never write analytics' table and vice versa):

- `internal/telemetry/db/migrations/20260822000001_drop_derived_consumption_columns_vehicle_snapshots.sql`
  — drops the five columns from `vehicle_snapshots`.
- `internal/analytics/db/migrations/20260822000002_reset_vehicle_metric_watermarks.sql`
  — deletes the `vehicle_metric_watermarks` rows whose `source` is
  `'vehicle_snapshots'`, so the next `Reconcile` rebuilds every vehicle's history.

Full DDL, rationale, rejected alternatives, the down-migration honesty note and the
index plan: design.md "Database Changes". **This change trips the `database` design
gate and must be confirmed by the owner before Apply.**

## Read Paths Affected

- **`GET /ui/dashboard/history`** (odometer + consumed charts) — **unchanged read
  shape**: both charts still read `vehicle_metrics` through
  `ConsumedByDay`/`OdometerDeltaByDay`, served by
  `vehicle_metrics_account_tesla_date_unique`'s own index. Nothing on the hot read
  path moves.
- **Every `vehicle_snapshots` read** (`LatestSnapshotsByAccount`,
  `SnapshotsByVehicleSince`/`Between`/`UpdatedSince`) — same predicates, same index,
  five fewer projected columns. Marginally *narrower* rows; no plan change.
- **New write-path read:** `SnapshotPrecedingDay`, one indexed single-row lookup per
  `Recalculate` call. Off the hot path (nightly batch plus a rare manual-charge
  write), which the read-heavy Performance-Profile explicitly grants latitude for.
- **New write-path read (conditional):** when a gap puts the predecessor outside the
  fetched window, the Supercharger-session and manual-entry fetches widen to cover
  the gap. Fires only on a genuine capture gap; bounded by the gap's own length.

## Capabilities

### Added Capabilities

- **Preceding-snapshot read port** on `telemetry.Reader` — the exact-predecessor
  lookup, index-reusing, nil when none exists. See `specs/telemetry/spec.md`.
- **Analytics-owned derived consumption figures** — the five per-day figures are
  computed by `analytics` from raw observations, including across a capture gap of
  any length. See `specs/analytics/spec.md`.

### Modified Capabilities

- **`telemetry`'s "Derived Consumption Metrics" requirement is REMOVED.** Telemetry
  no longer computes or stores any derived consumption value; the behaviour it
  described is preserved, unchanged in its numbers, by the analytics requirement
  above. See `specs/telemetry/spec.md` `## REMOVED Requirements`.
- **`analytics`' "A Day With No Usable Predecessor Is Skipped, Never Flagged"** — its
  numbers are unchanged, but "no usable predecessor" now means *no earlier snapshot
  exists for this vehicle at all*, not *no earlier snapshot inside the fetched
  window*. See `specs/analytics/spec.md` `## MODIFIED Requirements`.

### Out of scope (explicitly deferred)

- **`charge_gaps` moving into analytics** — tier 5, independent.
- **`charge_sessions`** — tier 6, independent.
- **`internal/app` / `ProcessVehicleData` / `process_runs`** — tier 7.
- **Changing `vehicle_metrics`' schema.** Tier 3 already created all five `_calc`
  columns there; this tier changes only what fills them.
- **`RecentEfficiency`** — untouched, still live-computed from `telemetry`.
- **The `LIMIT 400` on `SnapshotsByVehicleBetween`** — a pre-existing tier-3
  interaction (a >400-row backfill window truncates its newest rows) found while
  writing this design. Recorded in design.md "Risks / Trade-offs" and reported to
  the leader; **not fixed here** (owner's standing minimal-scope preference).

## Testing

Roadmap D10: **characterization tests only.** `internal/telemetry/consumption_test.go`
and `internal/telemetry/db_derived_consumption_integration_test.go` pin today's
behaviour; they move to `internal/analytics` and must assert byte-for-byte identical
output for identical inputs. design.md's "Test Contract" authors five fixtures with
their exact expected values **before implementation** — including the three the
dispatch requires: a multi-day capture gap (Fixture D, the case the new port exists
for), a vehicle's first-ever snapshot (Fixture C, all five values NULL), and the
`battery_used_pct_calc <= 0` divisor guard (Fixtures B and E).

Per the Test-Execution-Policy: the assistant writes these tests and runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`/`vet`/`bins`, the
standalone guards and `make sqlc` — never `go test ./...`. The owner runs the suite;
until they do, this tier's status is **awaiting-user-verification**, never "done".

## Resolved decisions

I1–I4 were settled with the owner via `grill-me` (2026-08-21) and are carried into
design.md as D1–D4, verbatim in substance and not re-litigated. Roadmap decisions D1
(analytics is a precomputed read model), D6 (analytics pulls its inputs through its
own ports) and D10 (characterization tests only) are the ones this tier implements;
D2, D4, D7, D8, D9 bound what it must not do. D5 onward in design.md are the
decisions this artifacts pass had to make to turn I1–I4 into a buildable change —
of which **D8b (widening the charge-source fetch across a gap) is the one the
interview did not cover at all** and is flagged as such.
