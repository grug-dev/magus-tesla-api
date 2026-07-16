## Why

The platform's charging intelligence tier (RM2-charging-stats) captures Tesla-billed Supercharger
sessions and enriches nightly vehicle snapshots with live charge telemetry. Tier 1
(`RM2-tesla-add-charging-history`, archived) already shipped the `tesla.VehicleService.ChargingHistory`
port and the `ChargingSessionTesla` / `ChargingFeeTesla` DTOs. This tier 2 wires those into the
`telemetry` module so the platform can accumulate a Supercharger session ledger and surfacing per-session
cost and energy alongside the nightly snapshot charge state.

Two distinct data sources are combined:

- **Source B — `GET /api/1/dx/charging/history`**: returns Supercharger and DC fast-charging
  sessions only. No home/AC charging, no battery percentage, no single top-level kWh field (energy
  is inside fee tier breakdowns keyed by `uom`). The module will derive `energy_kwh`, `total_cost`,
  `currency`, and `is_paid` from the fee array at write time and store the whole raw session as
  JSONB for lossless history.

- **Source A — `vehicle_data` charge_state enrichment**: the nightly 03:30 snapshot already
  captures a charge-state block, but six live charging fields are not yet extracted into typed
  columns: `charge_energy_added` (kWh), `charger_power` (kW), `charger_voltage` (V),
  `charger_actual_current` (A), `usable_battery_level` (%), and `fast_charger_type` (text). Adding
  these to `vehicle_snapshots` lets a dashboard show the state of a home/overnight charge session
  observed at 03:30 — something Source B cannot provide (it only covers Supercharger/DC fast-
  charging billed by Tesla).

> grill-me was run by the leader (2026-07-16). The binding design decisions for both sources
> are recorded in `design.md` and this proposal captures the agreed scope without reopening them.

## What Changes

**Source B — new `supercharger_sessions` table (module-scoped, telemetry-owned):**

- A new `supercharger_sessions` table (goose migration, next timestamped file in
  `internal/telemetry/db/migrations/`). Unlike `vehicle_snapshots` this table is NOT append-only:
  it uses **UPSERT** on `(session_id)` because `is_paid`, invoices, and fee `status` legitimately
  change after billing, so a nightly re-fetch must refresh mutable columns without duplicating rows.
- The write path folds into the existing `collectAccount` flow: after the per-vehicle snapshot loop
  (valid `creds` already in hand), one `ChargingHistory` call per account fetches all sessions
  (full backfill + nightly re-upsert; no date params — no vehicle wake).
- Derived columns (`energy_kwh`, `total_cost`, `currency`, `is_paid`) are computed from the fee
  array at write time; the whole raw session JSONB is stored losslessly.
- Two new read methods on a new `SuperchargerReader` port (separate from the existing `Reader`):
  sessions by account and sessions by vehicle, both ordered newest-first.
- `CycleReport` gains two new fields: `ChargingSessionsUpserted int` and
  `ChargingFetchFailures int`.

**Source A — six new nullable columns on `vehicle_snapshots`:**

- Six NULLABLE columns added to `vehicle_snapshots` via a new migration:
  `charge_energy_added DOUBLE PRECISION NULL`, `charger_power INTEGER NULL`,
  `charger_voltage INTEGER NULL`, `charger_actual_current INTEGER NULL`,
  `usable_battery_level INTEGER NULL`, `fast_charger_type TEXT NULL`.
- Nullable because pre-enrichment rows predate extraction — old rows retain NULL; data is
  recoverable from `raw_data`. No backfill of existing rows.
- Source A tasks are **blocked until the leader adds the 6 ChargeStateTesla fields**
  (`ChargeEnergyAdded`, `ChargerPower`, `ChargerVoltage`, `ChargerActualCurrent`,
  `UsableBatteryLevel`, `FastChargerType`) to `internal/tesla/types.go`. That is a tesla-module
  edit outside this worker's sandbox; the leader owns it.

**Breaking:** No. Purely additive — new table, new nullable columns (existing rows unaffected),
new port, extended `CycleReport` fields.

**Modules affected:**
- `internal/telemetry` (this worker — owned).
- `internal/tesla/types.go` (leader-owned: 6 new fields on `ChargeStateTesla`).
- `sqlc.yaml` and `Makefile` (leader-integrated: new migrations dir is the same as existing;
  `make sqlc` regenerates `telemetrydb` to pick up new query/columns).

## Read paths affected

- **Per-vehicle Supercharger sessions** — `SuperchargerReader.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, limit)` (new).
- **Account-wide Supercharger sessions** — `SuperchargerReader.SuperchargerSessionsByAccount(ctx, accountID, limit)` (new).
- **Snapshot charge enrichment** — `Reader.LatestSnapshotsByAccount` continues to serve the gateway; after Source A the six new nullable fields are available on each returned `Snapshot` (additive, backwards-compatible with nil for old rows).

## Capabilities

### Modified Capabilities

- `telemetry`: Extended with Supercharger session ingestion, upsert-on-nightly-refetch, and a
  new `SuperchargerReader` port. Also extended with six new nullable charge-enrichment fields on
  `vehicle_snapshots`. Existing behavior (nightly snapshot capture, scheduling, per-vehicle
  isolation, `Reader.LatestSnapshotsByAccount`) is unchanged.

### Added Capabilities

- `telemetry/supercharger-sessions`: A new sub-capability tracking Tesla-billed Supercharger +
  DC fast-charging sessions per account. Sessions are stored with derived cost/energy summaries
  for cheap reads; the full raw JSONB is stored for lossless history. The ledger is refreshed
  nightly via upsert, not accumulated append-only, because billing state changes post-session.

## Deferred / Out of Scope

- **R1 (percent-encode `startTime`/`endTime`)**: the tier-1 review finding that query params
  should be percent-encoded when passed to `GET /api/1/dx/charging/history`. This change passes no
  date params (full fetch, `ChargingHistoryParams{}`), so R1 is not exercised. Tracked in the
  roadmap backlog for when date-ranged fetches are added.
- **Home/AC charging detection**: `charging/history` only covers Tesla-billed Supercharger and DC
  fast-charging sessions. Home/AC sessions appear only as `charge_energy_added` in a vehicle
  snapshot. Correlating snapshot energy-added deltas into synthetic home-charge events is future
  work.
- **Retention / partitioning**: `supercharger_sessions` grows unbounded. A retention policy or
  partitioning is out of scope for this tier.
- **Summary / aggregation tables**: per-month cost/energy rollups are useful for dashboards but
  are deferred until the read port is consumed by the gateway.

## Impact

- **New / changed files inside `internal/telemetry`:**
  - `internal/telemetry/db/migrations/<ts>_add_supercharger_sessions.sql` — new `supercharger_sessions` table + derived-column constraints + indexes.
  - `internal/telemetry/db/migrations/<ts>_enrich_vehicle_snapshots_charge.sql` — 6 nullable columns on `vehicle_snapshots`.
  - `internal/telemetry/db/query.sql` — new queries: `UpsertSuperchargerSession`, `SuperchargerSessionsByAccount`, `SuperchargerSessionsByVehicle`.
  - `internal/telemetry/telemetry.go` — new `SuperchargerSession` domain type; extend `CycleReport`; new `SuperchargerReader` interface + constructor signature.
  - `internal/telemetry/service.go` — fold `ChargingHistory` into `collectAccount`; extend `store` interface with `upsertSuperchargerSession`; implement `dbStore.upsertSuperchargerSession`; extend `snapshotFrom` for 6 new fields.
  - `internal/telemetry/mapping.go` — extend `rowToSnapshot`; new `rowToSuperchargerSession`.
  - `internal/telemetry/reader.go` — new `SuperchargerReader` implementation and `NewSuperchargerReader` constructor.
  - `internal/telemetry/*_test.go` — offline collector tests extended; new `SuperchargerReader` tests.

- **Repo-root (leader-integrated, outside the telemetry sandbox):**
  - `internal/tesla/types.go` — add 6 fields to `ChargeStateTesla` (leader-owned; Source A tasks blocked until done).
  - `sqlc.yaml` / `Makefile` — no structural change needed (the telemetry `sql:` entry already exists; `make sqlc` picks up new queries/columns automatically after the migrations are applied).

- **Dependencies:** no new Go dependencies — existing pgx / pgxpool / sqlc / uuid / stdlib stack.
- **Migrations:** two new goose migrations in `internal/telemetry/db/migrations/`. Applied with `make migrate-up`; never part of a build.
- **Operational:** the nightly cycle gains one `ChargingHistory` call per account (server-side, no vehicle wake). `supercharger_sessions` uses UPSERT on every nightly run (idempotent re-run is safe).
