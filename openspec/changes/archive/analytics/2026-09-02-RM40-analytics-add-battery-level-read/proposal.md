Source: MAG-41 — https://linear.app/magus-monitor/issue/MAG-41/fix-boundary-guard
Roadmap: openspec/roadmaps/RM40-gateway-drop-telemetry-dependency.md
Tier: 1 of 2 (`analytics` gains the read port method; tier 2
`RM40-gateway-drop-telemetry-dependency` depends on this tier and consumes it,
retiring every `internal/telemetry` reference from `internal/gateway/`)
Unit tests: excluded (new); existing tests repaired where the change breaks them. The
owner's standing default is no new unit tests. This tier's implementation is expected
to break no existing analytics test, but design.md still authors the exact expected
values of this change's integration tests up front, per `ai/go-conventions.md`'s
"author expected values first" rule for tests that have no fast feedback loop.

## Why

`RM40-gateway-drop-telemetry-dependency` (roadmap, read in full before this proposal)
wants `make boundary-guard` to pass with zero `// boundary:allow:` escape hatches:
`internal/gateway/` must stop naming `internal/telemetry` at all. The roadmap's own
investigation (its findings table) established that exactly one production call site
remains — `internal/gateway/handlers/history.go:321`'s battery-history read — and that
it consumes only three fields (`EffectiveDate`, `BatteryLevelPct`, `BatteryRangeKm`) off
`telemetry.Snapshot`. `internal/analytics` already owns `vehicle_metrics`, the table
that already stores all three fields as `NOT NULL` original columns, and already serves
the gateway's two sibling history charts (`ConsumedByDay`, `OdometerDeltaByDay`) from
that same table. This tier adds the fourth sibling method, `BatteryLevelByDay`, so tier
2 can retarget the gateway's one remaining call site onto `internal/analytics` instead
of `internal/telemetry`.

This tier touches only `internal/analytics/`. It does not touch the gateway — tier 2,
a separate change depending on this one, is where the call site actually moves and
where `telemetry.Reader`/`telemetry.Snapshot` are removed from gateway code.

Every decision below is carried verbatim from the roadmap (D1–D8, settled with the
owner via `grill-me` before the roadmap was written) — none is re-opened here.

## What Changes

- **New `analytics.Reader` method, `BatteryLevelByDay(ctx, accountID, teslaID, start,
  end) ([]DayBattery, error)`**, and a new domain type `DayBattery` (`Date
  time.Time`, `BatteryLevelPct int`, `BatteryRangeKm float64`) — completing the
  existing closed vocabulary `DayConsumption` / `DayDistance` / `DayBattery` (roadmap
  D2). Mirrors `ConsumedByDay`/`OdometerDeltaByDay` exactly in signature shape,
  precomputed-and-sparse contract, and non-nil-empty-slice rule.
- **One deliberate departure from the pattern it mirrors (roadmap D3):** the new
  query carries NO `IS NOT NULL` filter. Both sibling queries filter because their
  underlying columns (`battery_used_pct_calc`, `distance_traveled_km_calc`) are NULL
  on a predecessor-less day. `battery_level_pct` and `battery_range_km` are `NOT NULL`
  raw per-day observations with no predecessor requirement — filtering would silently
  hide a vehicle's first tracked day from the battery chart for no reason.
- **New query in `internal/analytics/db/query.sql`**, `VehicleMetricsBatteryByVehicleBetween`,
  mirroring `VehicleMetricsConsumedByVehicleBetween`/`VehicleMetricsOdometerByVehicleBetween`
  in shape and comment style, plus the `sqlc generate` step that regenerates
  `query.sql.go`.
- **Store seam + concrete `reader.go` method**, mirroring how `ConsumedByDay` and
  `OdometerDeltaByDay` are built there: `vehicleMetricsStore` gains one more method,
  `*analyticsdb.Queries` satisfies it automatically, `reader.BatteryLevelByDay` maps
  rows to `DayBattery` with no derivation logic.
- **No database object is created or altered.** `vehicle_metrics.metric_date`,
  `.battery_level_pct`, `.battery_range_km` already exist as `NOT NULL` original
  columns (`20260821000001_add_vehicle_metrics.sql:54,56`), and the table's
  `UNIQUE (account_id, tesla_id, metric_date)` constraint already provides the index
  this query needs — the identical index `ConsumedByDay`/`OdometerDeltaByDay` already
  rely on. No migration, no new column, no new index, no new constraint, no view.
- **Docs**: `internal/analytics/AGENTS.md` gains the `BatteryLevelByDay` port entry
  under "Public interface (the port)", completing the `DayBattery` vocabulary
  reference alongside `DayConsumption`/`DayDistance`.

## Breaking

**No — externally.** No HTTP route exists in this module (`ai/architecture.md` §3), no
gateway code changes in this tier (tier 2's job), no i18n key changes. A user sees no
difference from this tier alone.

**No — internally, additively.** `analytics.Reader` gains one new method
(`BatteryLevelByDay`) — every existing method's signature is unchanged.
`analytics.NewReader`'s constructor signature is unchanged; the new method reuses the
`metrics vehicleMetricsStore` dependency `ConsumedByDay`/`OdometerDeltaByDay` already
use, introducing no new dependency.

## Modules Affected

- **`internal/analytics/`** — the only module touched by this tier: `analytics.go`
  (new `DayBattery` type + `Reader` method), `db/query.sql` + regenerated `db/*.go`
  (new query), `reader.go` (store seam + implementation), `AGENTS.md`.
- **`internal/gateway/`** — read, not written. Tier 2 (a separate change, not this
  one) is the only place the gateway's call site moves and `telemetry.Reader`/
  `telemetry.Snapshot` leave gateway code.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal
rule

- **New read path: `BatteryLevelByDay`.** Not yet called by anything in this tier
  (tier 2 is its first caller) — but it is designed and indexed now, since the
  `database` design gate requires the full index plan up front even when (as here) no
  database object changes. Read shape: `WHERE account_id = $1 AND tesla_id = $2 AND
  metric_date BETWEEN $3 AND $4`, no `IS NOT NULL` filter (D3), `ORDER BY
  metric_date`. See design.md "Index Plan" — served entirely by the existing
  `vehicle_metrics_account_tesla_date_unique` index, the same one
  `ConsumedByDay`/`OdometerDeltaByDay` already rely on. No new index needed; no new
  write cost, since no new index is created.
- **`ConsumedByDay`/`OdometerDeltaByDay`/`LatestMetricsByAccount`/`Recalculate`/
  `Reconcile`** — unaffected. None reads or writes the new query; no existing query,
  index, or write path is touched.

## Capabilities

### Added Capabilities

- **`BatteryLevelByDay`** — per-day battery-level-percent and battery-range-km
  observations for a vehicle over a date range, read from the precomputed
  `vehicle_metrics` table, with no predecessor requirement (unlike the module's other
  two day-bucketed reads). See `specs/analytics/spec.md`.

### Out of scope (explicitly deferred)

- **Consuming `BatteryLevelByDay` from the gateway.** Tier 2, a separate change
  (`RM40-gateway-drop-telemetry-dependency`), depends on this tier's artifacts and is
  the only place the gateway's call site, `Deps`/`Handler` fields, and `_test.go`
  fakes change.
- **Removing any `internal/telemetry` reference from `internal/gateway/`.** Entirely
  tier 2's concern — this tier does not touch a single gateway file.
- **Backfilling any historical row.** Not applicable — this tier creates no database
  object and touches no existing row.

## Testing

Per the Test-Execution-Policy: the assistant writes tests and runs `go build ./...`,
`go vet ./...`, `gofmt -l`, and the standalone guards — never `go test ./...`. The
owner runs the suite; until they do, this tier's implementation status is
**awaiting-user-verification**, never "done." See design.md "Test Contract" for the
concrete fixtures and expected values authored before implementation.

## Resolved decisions

Roadmap D1 (`internal/analytics` owns the read — not a gateway-local interface over
telemetry), D2 (one new method on the existing `Reader` port, name and shape fixed),
D3 (no `IS NOT NULL` filter — the one deliberate departure from the sibling pattern),
D4 (`DayBattery.Date` is a final bucket key, never re-projected), D5 (the gateway's
1-day lookback is dropped — tier 2's concern, restated here only because it explains
why `BatteryLevelByDay(start, end)` returns exactly `[start, end]` with no widening),
D6 (the day-coverage difference between `vehicle_metrics` and `vehicle_snapshots` is
accepted and documented, not backfilled), D7 (no new unit tests; existing tests
repaired only if broken — none are expected to break in this tier), D8 (zero escape
hatches — not applicable to this tier's artifacts, since this tier touches no gateway
file) are all carried verbatim from the roadmap and not re-litigated here.

**Database design gate.** This change touches no database object at all — no table,
column, index, constraint, view, or migration. `CLAUDE.md`'s `database` design gate
(built-in, always on) still applies to design.md's requirement to state and justify an
Index Plan; design.md states explicitly that no migration is needed and names the
existing index that serves the new query, for the leader to present to the owner for
explicit confirmation before Apply.
