## REMOVED Requirements

### Requirement: Recent Energy-Per-Kilometre Derivation

**Reason**: `RecentEfficiency`, the method this requirement describes, has no caller anywhere
in the codebase — no handler, no template, no `cmd`. It was this module's original metric,
superseded by the precomputed `vehicle_metrics` figures (`ConsumedByDay`, `OdometerDeltaByDay`,
`BatteryLevelByDay`, `LatestMetricsForVehicles`) that the dashboard actually reads. Deleted by
`RM61-analytics-remove-dead-efficiency-branch` (roadmap decision RD9). No number any user can
see changes, because nothing called this method.

**Migration**: none. There is no data migration and no caller to update — the whole point of
this deletion is that none exists.

### Requirement: Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile

**Reason**: Describes `Efficiency.Approximate`, a field of the `Efficiency` struct this same
change deletes. It has no meaning once `RecentEfficiency` and `Efficiency` are gone. Deleted by
`RM61-analytics-remove-dead-efficiency-branch` (roadmap decision RD9).

**Migration**: none.

### Requirement: Insufficient Data Returns ok=false, Never a Fabricated Value

**Reason**: Every scenario under this requirement describes `RecentEfficiency`'s own three
`ok=false` cases (fewer than two snapshots in its window, no distance moved, net charge
exceeding consumption). None of the surviving `Reader` methods share this contract in this
shape — `ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay` return a sparse slice (absence
IS the "no data" signal, per their own requirements below, untouched by this change) rather
than an `ok bool`. Deleted by `RM61-analytics-remove-dead-efficiency-branch` (roadmap decision
RD9).

**Migration**: none.

## MODIFIED Requirements

### Requirement: No Cross-Module Database Access

The analytics capability SHALL own no database of its own and SHALL access telemetry and
manual charge data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of
the module boundary.

#### Scenario: The capability owns no database

- **GIVEN** the analytics capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader` interface of `internal/telemetry` and the public
  `Reader` and `SuperchargerSessionAnalyticsReader` interfaces of `internal/charging` — never
  `internal/telemetry/db`, `internal/charging/db`, `internal/account`, or `internal/account/db`

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

(The vocabulary listed above is the one in force when THIS requirement was written. It was
migrated afterwards, by `RM39-analytics-fix-watermark-vocabulary`: `'charge_sessions'` was
retired in favour of `'supercharger_sessions'` once `internal/charging` renamed the table it
names. That later change is governed by "Incremental Recompute Via An Analytics-Owned
Watermark" above; this requirement's own claim — that the *schema move* left the vocabulary
untouched — remains true and is deliberately not rewritten.)

**This requirement and its migration are historical and unaffected by
`RM61-analytics-remove-dead-efficiency-branch`.** Only the "public interface is unaffected"
scenario's example method list is corrected below, because it named `RecentEfficiency`, a
method that change deletes.

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

- **GIVEN** a caller of the analytics module's public interface (e.g. `ConsumedByDay`,
  `OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles`, `Recalculate`,
  `Reconcile`, `ReconcileWindow`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned data is identical to what the same call returned before the move
- **AND** no caller (`internal/app`, `internal/gateway`) needs to change to keep working

#### Scenario: The watermark source vocabulary is unaffected by the schema move
- **GIVEN** `vehicle_metric_watermarks` rows whose `source` column holds `'vehicle_snapshots'`,
  `'charge_sessions'`, or `'manual_charge_entries'` (the vocabulary in force at the time of the
  schema move; `'charge_sessions'` was retired later — see this requirement's note above)
- **WHEN** the schema-move migration is applied
- **THEN** every row's `source` value is byte-identical to its pre-migration value
- **AND** the `vehicle_metric_watermarks_source_check` constraint still accepts exactly the same
  three values and rejects every other value, unchanged
