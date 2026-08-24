Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 3 of 8 (`analytics` gains `vehicle_metrics` — the GOLD STANDARD tier later tiers
mirror; tier 1 `RM29-analytics-rename-from-battery` and tier 2
`RM29-charging-rename-from-manualcharge` are archived; tier 4
`RM29-telemetry-drop-derived-columns` depends on this tier; tiers 5–7 are
`RM29-analytics-own-charge-gaps`, `RM29-charging-add-charge-sessions`,
`RM29-app-add-process-vehicle-data`; tier 8 is parked)
Unit tests: characterization only (roadmap D10) — pin today's `buildOdometerChart`/
`ConsumedByDay` output, assert identical after the move; no new unit tests for new
Recalculate/Reconcile logic beyond that. See design.md "Test Contract" for the
concrete expected values authored before implementation.

## Why

Roadmap D1 declares analytics a **precomputed read model**: duplicating observation
columns (`battery_level`, `odometer`, `battery_range`) from `telemetry` into a new
`analytics.vehicle_metrics` table is intentional, not a smell. Today none of that
exists — `internal/analytics` owns no database at all (confirmed:
`internal/analytics/AGENTS.md` "Data ownership" says "None... no `internal/analytics/db`
package"), and two things fill the gap in the wrong place:

1. **The gateway computes vehicle math it shouldn't.** `buildOdometerChart`
   (`internal/gateway/handlers/history.go`) subtracts consecutive snapshots' odometer
   readings, clamps a negative delta (a clock-skew/anomaly rule), and buckets by
   `effectiveDayUTC` — three vehicle-domain decisions living in the presentation layer,
   violating roadmap D5 ("the gateway computes nothing about the vehicle, only about
   the chart").
2. **`vehicle_snapshots` still carries five columns analytics alone consumes.**
   `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
   `estimated_range_km_calc`, `days_spanned_calc` were added to `telemetry` in MAG-10
   purely so `internal/analytics`'s `ConsumedByDay` could read them — a
   derived-metrics module reading another module's private derived columns is the
   exact boundary blur MAG-26 exists to fix. Tier 4 cannot drop these columns until
   this tier gives them a home analytics owns.

This tier gives them that home. `vehicle_metrics` becomes the single source both the
odometer chart and the consumed chart read from, populated by a new `Recalculate`
write path (incremental, watermark-driven per roadmap D7) instead of computed live on
every request. It is the vertical slice every later tier's read-model pattern mirrors
(roadmap D8), so its shape is deliberately precedent-setting: primary key style,
watermark table shape, and index-plan reasoning are all decided here for the first
time and inherited by tiers 5 and 6 without re-litigation.

Every schema and reconciliation decision below was settled with the owner via
`grill-me` immediately before this proposal was written (recorded here as IO-1
through IO-6; binding, not re-opened).

## What Changes

- **New table `analytics.vehicle_metrics`** — one row per `(account_id, tesla_id,
  metric_date)`, written for **every day that has a snapshot, dense — 1:1 with
  `vehicle_snapshots`' own grain**, whether or not that day has a predecessor
  (revised at the database design gate; the owner's one-item change to an
  otherwise-approved design — see design.md D9). `distance_traveled_km_calc`,
  `battery_used_pct_calc`, `days_spanned_calc` and `consumed_pct` are nullable,
  mirroring `vehicle_snapshots`' own nullability exactly: NULL means "no
  predecessor exists," the identical meaning it carries there today. `flagged` is
  never NULL — it is stored `false` on a predecessor-less row (design.md D9
  explains why a stored `0` would be an active bug, not a simplification).
  `ConsumedByDay`/`OdometerDeltaByDay` filter predecessor-less rows back out on
  read (D13), so their output stays byte-identical to today's live-computed,
  effectively-sparse behavior even though the table itself is dense. Carries: the
  three duplicated raw observations (`battery_level_pct`, `odometer_km`,
  `battery_range_km` — roadmap D1, always present); the five `_calc` columns tier 4
  needs a home for, copied verbatim from `telemetry.Snapshot` (no re-derivation —
  telemetry already computes them); and the corrected
  `consumed_pct`/`flagged`/`missing_charging_type` D13 columns `ConsumedByDay`
  already derives today. Full schema, constraints and index plan: design.md
  "Database Changes" (this change trips the `database` design gate).
- **New table `analytics.vehicle_metric_watermarks`** — one cursor row per `(account_id,
  tesla_id, source)` for the three sources analytics reads (`vehicle_snapshots`,
  `supercharger_sessions`, `manual_charge_entries`), each advancing independently
  (IO-2/IO-3). Read with a 24h safety overlap to absorb commit skew (IO-4).
- **New `analytics.Recalculator` port**: `Recalculate(ctx, accountID, teslaID, start,
  end)` recomputes and UPSERTs `vehicle_metrics` for an explicit date range (idempotent
  — a re-seen input set upserts unchanged rows); `Reconcile(ctx, accountID, teslaID)`
  reads the three watermarks, queries each source for rows updated since `cursor -
  24h`, calls `Recalculate` for the union of affected dates, and advances each
  watermark independently.
- **`analytics.Reader` gains `OdometerDeltaByDay`** — the odometer-chart-shaped
  per-day km-driven delta (already clamped ≥0, roadmap D5), reading `vehicle_metrics`
  and filtering predecessor-less rows out (D13) so its own returned result stays
  sparse — one entry per day with a computable delta — even though the underlying
  table now stores a (NULL-derived-columns) row for every snapshot day.
  **`ConsumedByDay`'s implementation switches** from live-computing over
  `telemetry`/`charging` ports to reading `vehicle_metrics` — its output is
  characterization-pinned identical (D10); its documented "no cache" contract is
  explicitly superseded (design.md D-precompute).
- **`RecentEfficiency` is untouched** — stays live, still reads `telemetry`/
  `charging`/`account` directly. Out of scope for this tier (see Non-Goals).
- **Gateway re-point (D5):** `buildOdometerChart` becomes chart-only — it calls
  `analyticsReader.OdometerDeltaByDay` and does only `HeightPct` scaling, y-axis
  ticks, labels, tooltips and i18n. `buildBatteryChart` is untouched (it renders a raw
  observation with no delta/clamp — never a D5 violation to begin with).
- **Gateway write-path freshness (IO-5):** `charges.go`'s `ChargeCreate` /
  `ChargeRowUpdate` / `ChargeRowDelete` call `Recalculate` for the affected
  `metric_date`(s) after a successful `Writer.Create`/`Update`/`Delete` commit, so the
  Consumed chart keeps its current instant-update behaviour. The interim caller is the
  gateway handler; tier 7's `app.RecalculateVehicleData` relocates the *call*, not the
  logic (IO-5).
- **`cmd/poller` gains a per-vehicle `Reconcile` call** after each successful
  collection cycle, mirroring the existing `newGapReconciler` composition-root
  pattern exactly (same file, same shape, new call).
- **New ports on two sibling modules, added in this change because analytics' watermark
  needs them (IO-4), outside `internal/analytics`'s sandbox:**
  `telemetry.Snapshot` gains an `UpdatedAt` field (the DB column already exists,
  unexposed); `telemetry.Reader` gains `SnapshotsByVehicleUpdatedSince`;
  `telemetry.SuperchargerReader` gains `SuperchargerSessionsByVehicleUpdatedSince`;
  `charging.Reader` gains `ListEntriesByVehicleUpdatedSince`. See "Modules Affected".
- **`internal/analytics/AGENTS.md` corrected** — "Data ownership: None" becomes false
  as of this tier; the file is updated to describe the new `db/` package, in the same
  change (CLAUDE.md docs-track-structural-change).
- **`sqlc.yaml` gains an `analytics` entry**; `internal/analytics/db/migrations/` is
  created.

## Breaking

**No — externally.** No HTTP route, no rendered markup shape and no i18n key changes.
`ConsumedByDay`'s and the odometer chart's OUTPUT is pinned identical by
characterization test (D10) — a user sees no difference.

**Yes — internally.** `analytics.NewReader`'s constructor signature gains a leading
`*pgxpool.Pool` parameter (it now needs a DB connection for the two table-backed
read methods); `analytics.NewReader`'s two callers (`cmd/web`, `cmd/poller`) update in
this change. `analytics.Reader`'s documented "no cache" contract is explicitly
superseded (Test Contract confirms the *values* stay pinned; only the mechanism
producing them changes, per roadmap D1). `telemetry.Reader`, `telemetry.SuperchargerReader`
and `charging.Reader` each gain one new method — additive, no existing method
signature changes.

## Modules Affected

- **`internal/analytics/`** — the primary module. Gains `db/` (migrations + sqlc),
  `vehicle_metrics`/`vehicle_metric_watermarks` schema, `Recalculator` port,
  `OdometerDeltaByDay` on `Reader`, `ConsumedByDay`'s reimplementation. `RecentEfficiency`
  untouched.
- **`internal/telemetry/`** — additive only: `Snapshot.UpdatedAt` field,
  `Reader.SnapshotsByVehicleUpdatedSince`,
  `SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince`. No existing method's
  signature or behavior changes; `vehicle_snapshots`' five `_calc` columns are read,
  never dropped, here (tier 4's job, and only once every reader — this tier's own
  `Recalculate` included — is repointed).
- **`internal/charging/`** — additive only: `Reader.ListEntriesByVehicleUpdatedSince`.
- **`internal/gateway/`** — `history.go`'s `buildOdometerChart` slims to chart-only;
  `charges.go`'s three write handlers gain a post-commit `Recalculate` call;
  `gateway.go`/`handlers.go` `Deps` gain an `AnalyticsRecalculator` field; `AGENTS.md`
  updated if its `Deps.AnalyticsReader` bullet needs the new method noted.
- **`cmd/poller/`, `cmd/web/`** — composition roots: `analytics.NewReader`'s new pool
  parameter threaded through; `cmd/poller` gains the `Reconcile`-per-vehicle call
  beside the existing gap-reconciliation step.
- **`internal/account/`** — not touched. `vehicleLookup`/`AllRegisteredVehicles`
  reused as-is.

## Database Changes

**Two new tables, both owned by `internal/analytics/db`** (the module's first
persistence — the `database` design gate applies in full):

- `vehicle_metrics` — one row per `(account_id, tesla_id, metric_date)`, surrogate
  UUID PK, `UNIQUE (account_id, tesla_id, metric_date)`.
- `vehicle_metric_watermarks` — one row per `(account_id, tesla_id, source)`,
  surrogate UUID PK, `UNIQUE (account_id, tesla_id, source)`.

Full column lists, constraints, rationale, rejected alternatives and index plan (both
tables' sole index is each one's own UNIQUE constraint index — no separate
`CREATE INDEX`, mirroring `charge_gaps`' identical precedent): design.md "Database
Changes". This design was reviewed and confirmed by the owner before implementation
began (design gate).

## Read Paths Affected

- **`GET /ui/dashboard/history`** (odometer + consumed charts) — was two ports
  (`telemetry.Reader.SnapshotsByVehicleBetween` computed live +
  `analytics.Reader.ConsumedByDay` computed live); becomes two `vehicle_metrics`
  SELECTs scoped `account_id, tesla_id, metric_date BETWEEN`, served by the table's
  own UNIQUE-constraint index. Bounded by the existing `historyRangeMaxDays` = 90 cap
  (unchanged), so the read path's worst case is a ≤90-row indexed range scan per
  chart, down from a full snapshot/session/entry re-derivation on every request.
- **`RecentEfficiency`** — unaffected; still the same three live port calls it makes
  today.
- **New write-path reads:** `Recalculate`'s own fetch (telemetry/supercharger/manual,
  1-day lookback — same shape `ConsumedByDay`'s reader.go already used) and
  `Reconcile`'s watermark lookup (single-row, by the watermark table's own unique
  index) — both off the hot path (nightly batch + rare manual-charge writes), per the
  read-heavy Performance-Profile's write-side latitude.

## Capabilities

### Added Capabilities

- **`vehicle_metrics` precomputed read model** — `analytics` now owns a daily,
  per-vehicle metrics table populated incrementally by `Recalculate`/`Reconcile`
  (roadmap D1, D7). See `specs/analytics/spec.md`.
- **`OdometerDeltaByDay`** — the analytics-owned odometer-chart data source,
  replacing `buildOdometerChart`'s in-gateway delta/clamp/day-bucketing (roadmap D5).
  See `specs/analytics/spec.md` and `specs/gateway/spec.md`.
- **Watermark-driven incremental recompute** (`updated_at`-based, per-source, 24h
  overlap) — roadmap D7. See `specs/analytics/spec.md`.
- **`...UpdatedSince` read ports** on `telemetry.Reader`, `telemetry.SuperchargerReader`
  and `charging.Reader` — additive ports analytics' watermark needs. See
  `specs/telemetry/spec.md` and `specs/manual-charge-log/spec.md`.

### Modified Capabilities

- **`ConsumedByDay`'s "no cache" guarantee is superseded.** The existing analytics
  spec's "No Cache — Every Result Is Recomputed On Read" requirement no longer holds
  verbatim; it is replaced by a precomputed-read-model requirement with an equivalent
  freshness guarantee delivered by write-time `Recalculate` instead of read-time
  recomputation (roadmap D1; design.md D-precompute explains why the user-visible
  contract — "editing a charge entry changes what the chart shows, with no separate
  refresh" — is preserved even though the mechanism changes). See
  `specs/analytics/spec.md` `## MODIFIED Requirements`.
- **Gateway's odometer-chart requirement (D5 compliance)** — `buildOdometerChart` no
  longer performs the delta/clamp/day-bucketing itself. See `specs/gateway/spec.md`.

### Out of scope (explicitly deferred)

- **`RecentEfficiency` moving to read `vehicle_metrics`.** Not requested by IO-1..IO-6,
  not a rolling-window-shaped fit for a per-day table, and preserving minimal scope
  (owner's standing preference) — left exactly as-is, live-computed.
  `internal/analytics/AGENTS.md`'s existing `RecentEfficiency` documentation is
  otherwise unchanged.
- **The Supercharger Stats tile math** (`buildSuperchargerTiles` — energy sum, avg
  kWh, cost-by-currency totals, session count). Explicitly deferred to tier 6
  (`RM29-charging-add-charge-sessions`), which relocates the underlying session rows
  into `charging.charge_sessions` anyway — moving this math here would mean writing
  it twice. Not touched by this change.
- **Dropping `vehicle_snapshots`' five `_calc` columns** — tier 4, and only once this
  tier's `Recalculate` has backfilled every vehicle's history (see Testing/rollout
  note in design.md).
- **`internal/app`, `ProcessVehicleData`, `process_runs`** — tier 7. This tier's
  `Recalculate`/`Reconcile` calls are wired directly into `cmd/poller` and
  `internal/gateway/handlers/charges.go` as an interim composition-root arrangement
  tier 7 relocates (IO-5).
- **`charge_gaps` moving into analytics** — tier 5, independent of this tier.
- **Full manual ↔ Supercharger convergence** — never in RM29 (roadmap, rejected
  alternatives).

## Testing

Roadmap D10: **characterization tests only.** design.md's "Test Contract" authors two
concrete fixtures with their exact expected `vehicle_metrics` row values, `DayDistance`
and `DayConsumption` output, **before implementation**, per `ai/go-conventions.md`'s
"author expected values up front" rule for tests with no fast feedback loop:

- **Offline/pure-function tests** (early wave — compile against fixtures, no DB): pin
  today's `buildOdometerChart` output for a fixed snapshot fixture, and today's
  `ConsumedByDay` output for a fixed snapshot+session+entry fixture; assert the new
  `deriveVehicleMetrics` + `OdometerDeltaByDay`/`ConsumedByDay` mapping produce
  identical values.
- **`DATABASE_URL`-gated integration tests** (final wave — cannot compile before the
  migration and sqlc types exist): `Recalculate` writes the exact row design.md's Test
  Contract specifies; `Reconcile` advances watermarks correctly and is a no-op on a
  second run with no source changes (idempotence); `ConsumedByDay`/`OdometerDeltaByDay`
  read back what `Recalculate` wrote.

Per the Test-Execution-Policy: the assistant writes these tests and runs `go build
./...`, `go vet ./...`, `gofmt -l`, and the standalone guards — never `go test ./...`.
The owner runs the suite; until they do, this tier's implementation status is
**awaiting-user-verification**, never "done."

## Resolved decisions

IO-1 through IO-6 were settled with the owner via `grill-me` immediately before this
proposal was written (recorded verbatim in the dispatch that produced this change) and
are carried into design.md as D1–D6 there, plus the additional decisions D7+ this
artifacts pass required to make IO-1..IO-6 fully buildable (the watermark-driven
`Reconcile` orchestration, the `...UpdatedSince` port additions, and the column-list
reconciliation). No IO decision is re-litigated. Roadmap decisions D1 (precomputed read
model), D5 (gateway computes nothing about the vehicle), D6 (analytics pulls its own
inputs), D7 (`updated_at` watermark, not a `processed` flag) and D10 (characterization
tests only) are the ones this tier implements; D2–D4, D8, D9 bound what this tier must
not do (see "Out of scope" above).

**Database design gate — approved with one revision.** The owner reviewed the schema
above and design.md's full "Database Changes" section and approved it, with one
required change: `vehicle_metrics` is dense (one row per vehicle-day with a
snapshot), not sparse (one row only per vehicle-day with a predecessor) — see
design.md D9's "Nullability" subsection for the full rationale (a sparse table would
silently drop a vehicle's first day from any future consumer roadmap D1 points at
`vehicle_metrics`) and D13 for the consequence this change required: `ConsumedByDay`
and `OdometerDeltaByDay` now filter predecessor-less rows out on read, so their own
output is unaffected. No other part of the approved design (D1–D8, D10–D13, the
index plan, the no-FK and no-`raw_data` calls) was reopened.
