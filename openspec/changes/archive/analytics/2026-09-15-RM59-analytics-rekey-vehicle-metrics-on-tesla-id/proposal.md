# RM59-analytics-rekey-vehicle-metrics-on-tesla-id

> Source: MAG-69 — https://linear.app/magus-monitor/issue/MAG-69/6-re-key-analyticsvehicle-metrics-and-vehicle-metric-watermarks-on
> Roadmap: `openspec/roadmaps/RM59-rekey-vehicle-metrics-on-tesla-id.md`, tier 1 of 2.
> Parent: MAG-63, step 6 of 7.

## Why

`analytics.vehicle_metrics` holds one row per car per day: battery level, odometer,
distance, consumption. All of that describes a **car**, not an account. But the table
is keyed on `(account_id, tesla_id, metric_date)`. `analytics.vehicle_metric_watermarks`
has the same problem — it is the cursor the nightly cycle uses to know how far it has
already derived, written by the same code in the same transaction.

A car belongs to exactly one account at a time (the MAG-63 decision record). Carrying
`account_id` in the key buys no real isolation on top of that — it just makes every
query and every port method carry a parameter the data does not need.

## What the ticket got wrong, corrected by the roadmap

MAG-69's own text claims MAG-71 already dropped `account_id` from three queries
(`VehicleMetricsConsumedByVehicleBetween`, `VehicleMetricsOdometerByVehicleBetween`,
`VehicleMetricsBatteryByVehicleBetween`) and their `Reader` ports, and that a
`DROP INDEX idx_vehicle_metrics_vehicle_date` belongs in the migration. Both are false,
checked against the live code and database on 2026-09-14: MAG-71 was cancelled and
wrote no code, all three queries still filter on `account_id`
(`internal/analytics/db/query.sql:178,197,226`), all three `Reader` methods still take
it (`internal/analytics/analytics.go:86,101,149`), and `idx_vehicle_metrics_vehicle_date`
does not exist in `pg_indexes` — it was never created. This change folds MAG-71's work
in (roadmap RD2) and does not touch that non-existent index. See the roadmap file for
the full correction record.

## What changes

- **Migration** (`internal/analytics/db/migrations/`): one new file, mirroring
  `internal/telemetry/db/migrations/20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`.
  Collapses any duplicate `(tesla_id, metric_date)` / `(tesla_id, source)` rows (0 today,
  verified), replaces `vehicle_metrics_account_tesla_date_unique` with
  `UNIQUE (tesla_id, metric_date)`, replaces `idx_vehicle_metrics_latest` with
  `(tesla_id, metric_date DESC)`, replaces
  `vehicle_metric_watermarks_account_tesla_source_unique` with
  `UNIQUE (tesla_id, source)`, and drops `account_id` from both tables. No
  `SET NOT NULL` — `tesla_id` is already `NOT NULL` on both.
- **Queries** (`internal/analytics/db/query.sql`): drop `account_id` from
  `UpsertVehicleMetric` (and its `ON CONFLICT` target), `DeleteVehicleMetricsInRangeExcept`,
  `GetVehicleMetricWatermark`, `UpsertVehicleMetricWatermark`, and the three MAG-71 queries
  named above. Rename `LatestVehicleMetricsByAccount` to `LatestVehicleMetricsByVehicles`,
  filtering `tesla_id = ANY(@tesla_ids::bigint[])` instead of `account_id = @account_id`.
  Then `sqlc generate`.
- **Ports** (`internal/analytics/analytics.go`, `reader.go`, `recalculate.go`,
  `consumed.go`, `mapping.go`): `Reader.ConsumedByDay`, `OdometerDeltaByDay`,
  `BatteryLevelByDay` and `Recalculator.Recalculate`, `Reconcile` drop `accountID`.
  `LatestMetricsByAccount` becomes
  `LatestMetricsForVehicles(ctx, refs []vehicleref.Ref)`. `RecentEfficiency` is
  **unchanged** — it keeps `accountID` (it still needs `account.RegisteredVehicles` for
  `CarType` lookup, untouched by this ticket).
- **Leader-owned integration** (not this worker's sandbox, folded into this tier so the
  build stays green): `internal/app/processor.go`'s `recalculateAnalytics` dedupes its
  vehicle loop by `tesla_id` (roadmap RD3); the gateway call sites in
  `internal/gateway/handlers/handlers.go` and `external_charges.go`/`history.go` are
  patched only as far as needed for `go build ./...` to stay green. The full gateway
  authorization rewrite — using the existing `ownedVehicles` helper properly, plus the
  gateway's own test fixups — is tier 2, `RM59-gateway-authorize-metrics-reads`.
- **Docs**: `internal/analytics/AGENTS.md`, this change's `specs/analytics/spec.md`
  delta, and `kkpa/context/entities/vehicle-metrics/guide.md`.

## Breaking?

Internal only — no external API. Five port methods change shape (four drop a
parameter, one is renamed and retypes its argument). Callers: `internal/app`
(one file, leader-owned) and `internal/gateway` (four files, leader-owned stopgap here,
full rewrite in tier 2). No other module imports `internal/analytics`.

## Modules affected

- `internal/analytics` — owns the tables, the queries, the ports, and their
  implementation. This worker's sandbox.
- `internal/app` — one caller (`processor.go`), mechanical consequence of the port
  signature change. Leader-owned.
- `internal/gateway` — four call sites, leader-owned stopgap in this tier; full rewrite
  is tier 2.

## Read paths affected

- The dashboard vehicle cards, the single-vehicle dashboard, the nav header, and the
  external-charges page's battery-consumed suggestion — all four currently read
  `LatestMetricsByAccount`, all four are read-heavy hot paths per this project's
  Performance-Profile. The index serving them (`idx_vehicle_metrics_latest`) is
  recreated in the same shape, minus `account_id`, so no read gets slower.
- The three history-page charts (battery, odometer, consumed) — `ConsumedByDay`,
  `OdometerDeltaByDay`, `BatteryLevelByDay`, all served by the new
  `UNIQUE (tesla_id, metric_date)` index exactly as they were served by the old
  three-column unique index.
- See `design.md`'s Index Plan for the full justification.

## Non-goals

- No table split. By this point every column in `vehicle_metrics` is car data — the
  roadmap explicitly forbids proposing one.
- No change to `RecentEfficiency`'s signature.
- No change to the gateway's real authorization design (tier 2's job).
- `unit tests: excluded` — the ticket says so, the owner confirmed it. Existing tests
  that stop compiling because a signature changed are fixed or deleted, never left red,
  and no new test file is added.
