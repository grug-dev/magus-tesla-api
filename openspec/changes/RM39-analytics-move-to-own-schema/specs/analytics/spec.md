## ADDED Requirements

### Requirement: Module-Scoped Database Schema
The analytics module's `vehicle_metrics`, `vehicle_metric_watermarks`, and `charge_gaps` tables SHALL live in a PostgreSQL schema named `analytics`, distinct from the `public` schema and from
every other module's schema. This SHALL be a namespacing change only: it SHALL NOT alter any
stored data, any constraint (primary key, unique, or check), any index, or any behavior of the
module's public interface (`Reader`, `Recalculator`, `GapWriter`). No other module SHALL be
granted access to the `analytics` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before and
after this requirement, now additionally checkable at the database catalog level.

`vehicle_metric_watermarks.source`'s stored vocabulary (`'vehicle_snapshots'`,
`'charge_sessions'`, `'manual_charge_entries'`) and its CHECK constraint SHALL remain unchanged
by this requirement — those values are data naming other modules' tables by convention, not
schema-qualified references, and this schema move SHALL NOT alter, rewrite, or reinterpret them.

#### Scenario: The three tables resolve under the analytics schema
- **GIVEN** the analytics module's migrations have been applied
- **WHEN** the database catalog is queried for `analytics.vehicle_metrics`,
  `analytics.vehicle_metric_watermarks`, and `analytics.charge_gaps`
- **THEN** all three resolve to their table (a non-null relation)
- **AND** none of `public.vehicle_metrics`, `public.vehicle_metric_watermarks`, or
  `public.charge_gaps` resolves to a relation any longer

#### Scenario: Existing metrics, watermarks, and gap rows, constraints, and indexes survive the schema move
- **GIVEN** vehicle metrics, recompute watermarks, and flagged charge gaps already stored for
  one or more accounts
- **WHEN** the schema-move migration is applied
- **THEN** every row in all three tables is preserved unchanged
- **AND** the `UNIQUE (account_id, tesla_id, metric_date)` / `UNIQUE (account_id, tesla_id,
  source)` / `UNIQUE (account_id, tesla_id, gap_date)` constraints, every CHECK constraint
  (including `vehicle_metric_watermarks_source_check`, with its vocabulary byte-identical), and
  the `idx_vehicle_metrics_latest` / `idx_charge_gaps_account` indexes continue to be enforced
  exactly as before

#### Scenario: The module's public interface is unaffected by the schema move
- **GIVEN** a caller of the analytics module's public interface (e.g. `RecentEfficiency`,
  `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsByAccount`,
  `Recalculate`, `Reconcile`, `ReconcileWindow`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned data is identical to what the same call returned before the move
- **AND** no caller (`internal/app`, `internal/gateway`) needs to change to keep working

#### Scenario: The watermark source vocabulary is unaffected by the schema move
- **GIVEN** `vehicle_metric_watermarks` rows whose `source` column holds `'vehicle_snapshots'`,
  `'charge_sessions'`, or `'manual_charge_entries'`
- **WHEN** the schema-move migration is applied
- **THEN** every row's `source` value is byte-identical to its pre-migration value
- **AND** the `vehicle_metric_watermarks_source_check` constraint still accepts exactly the same
  three values and rejects every other value, unchanged
