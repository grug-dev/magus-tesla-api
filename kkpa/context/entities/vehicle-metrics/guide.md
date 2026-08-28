# vehicle_metrics / _calc fields — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct` derived
alongside them.

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Owning module — internal/analytics

| File | Role |
|---|---|
| `internal/analytics/analytics.go` | Port declarations: `Recalculator` interface (`Recalculate`, `Reconcile`), `GapReconciliationWindow` (30d), `Reader` (reads `vehicle_metrics` — `ConsumedByDay`, `OdometerDeltaByDay`). |
| `internal/analytics/recalculate.go` | The ONLY writer to `vehicle_metrics`: `Recalculate` (bounded `[start,end]` window, idempotent UPSERT on `(account_id, tesla_id, metric_date)`) and `Reconcile` (watermark-driven). Also owns `recalcOverlap` (24h commit-skew guard) and the three watermark source labels. |
| `internal/analytics/consumption.go` | Pure math: `deriveConsumption(prev, cur)` → `consumptionCalc` — the per-day deltas behind the five `_calc` fields (divisor guard: `km_per_pct`/`estimated_range` only when `battery_used_pct > 0`). |
| `internal/analytics/consumed.go` | Pure assembly: `deriveVehicleMetrics(preceding, snapshots, sessions, entries, start, end)` builds the full row set, adding supercharger + manual-charge corrections into `consumed_pct`. |
| `internal/analytics/db/query.sql` → `query.sql.go` | sqlc source of truth — the `vehicle_metrics` UPSERT + reads; `db/models.go` mirrors the columns. |
| `internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql` | Table DDL + the `_calc` column NULL-semantics comments (D13). |

### Callers — who triggers the computation

| File | Call | When |
|---|---|---|
| `internal/app/processor.go` (`recalculateAnalytics`, step 3 of `ProcessVehicleData`) | `Reconcile` per vehicle, then gap reconciliation reads the fresh rows | Nightly batch — **the only caller that keeps the table advancing**; driven by `internal/app/scheduler.go`. |
| `internal/gateway/handlers/charges.go` (`recalculateAfterChargeWrite`) | `Recalculate(uid, teslaID, chargedOn, chargedOn)` | After every manual-charge create/update/delete; errors logged and swallowed. |
| `cmd/web/main.go` / `cmd/poller/main.go` | `analytics.NewRecalculator(pool, telemetryReader, superchargerReader, chargingReader)` | Composition roots injecting the port into gateway Deps / the nightly processor. |

### Source data (read-only inputs — owned by other modules)

| File | Role |
|---|---|
| `internal/telemetry` (`Reader`, `SuperchargerReader`) | `vehicle_snapshots` + `supercharger_sessions` reads — see `architecture/telemetry-data-hub.md`. |
| `internal/charging` (`Reader`) | `manual_charge_entries` reads. |

## How maintenance works

- **Add a new `_calc`-style derived column:** migration in `internal/analytics/db/migrations/` → add column + UPSERT to `db/query.sql` → `make sqlc` → extend `consumptionCalc` (consumption.go) / `deriveVehicleMetrics` (consumed.go) → write it in `recalculate.go`. All math stays in the zero-I/O functions; `recalculate.go` only orchestrates reads + the UPSERT.
- **Change the math of an existing field:** edit `deriveConsumption` / `deriveVehicleMetrics` only — the UPSERT is a full-row replace, so the next `Recalculate`/`Reconcile` run self-heals history (idempotent on `(account_id, tesla_id, metric_date)`).
- **Add a new source table:** new watermark source label + `Reconcile` read branch in `recalculate.go`; the source's owning module exposes a bounded read port (never import another module's `db/`).
- **Read the metrics:** `analytics.Reader` (`ConsumedByDay`, `OdometerDeltaByDay`) — the gateway history fragment reads these, never `vehicle_metrics` directly.

## Conventions & gotchas

- **Ordering is a correctness requirement:** nightly `Reconcile` (step 1) MUST run before gap reconciliation (step 2) — `ConsumedByDay` is a plain SELECT over `vehicle_metrics`, and the gap writer DELETES flags for days that no longer flag, so reconciling against stale metrics destroys state. A vehicle whose `Reconcile` fails is skipped for the gap step entirely. _Source: `internal/app/processor.go` `recalculateAnalytics` doc comment._
- **Predecessor-less days have NULL `_calc`s** — the first snapshot of a vehicle's history has no delta to derive; `days_spanned_calc`/`distance_traveled_km_calc`/`battery_used_pct_calc` are NULL, and `km_per_pct_calc`/`estimated_range_km_calc` share the same NULL plus the `battery_used_pct <= 0` divisor guard. _Source: migration `20260821000001` column comments._
- **First `Reconcile` backfills full history** — watermarks start empty, so a new vehicle reads its entire source history once. Subsequent runs are incremental off the three per-source watermarks (`vehicle_metric_watermarks`), each advanced independently. _Source: `internal/analytics/recalculate.go`._
- **24h `recalcOverlap`** — every `Reconcile` read uses `updated_at >= cursor - 24h`, so commit-skew between sources self-heals next run (idempotent UPSERT makes the re-read a no-op). _Source: `recalculate.go` D4._
- **The `_calc` columns' ONLY home is `vehicle_metrics`** — the derived consumption columns were dropped from `vehicle_snapshots` (RM29 tier 4, migration `20260822000001`); never re-add read-time derivation to telemetry or the gateway. _Source: `internal/telemetry/db/migrations/20260822000001…`._
- **`consumed_pct` is charge-corrected** — `battery_used_pct_calc` plus that day's matched supercharger + manual charge deltas; a predecessor-less day's `consumed_pct` has no raw delta to correct. _Source: `internal/analytics/consumed.go` `deriveVehicleMetrics`._
- **"Yesterday" is resolved in the poller's own timezone, not UTC** — `recalculateAnalytics` computes its window with `time.Now().In(p.loc)`; `internal/analytics` itself stays location-free (each row's bucket day travels with it). _Source: `internal/app/processor.go` D-B12 comment._

## Related KB

- Architecture: `architecture/telemetry-data-hub.md` (the snapshot/supercharger sources this table derives from)
- Workflows: `workflows/manual-charge-crud.md` (the post-write `Recalculate` trigger), `workflows/supercharger-stats-read.md`
