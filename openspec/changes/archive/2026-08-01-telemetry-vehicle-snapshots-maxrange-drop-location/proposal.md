# Proposal: telemetry-vehicle-snapshots-maxrange-drop-location

## Why

The `vehicle_snapshots` table currently promotes a handful of typed columns extracted from the
lossless `raw_data` payload so dashboards read cheap without a JSONB probe. Two adjustments
became valuable as the platform's analytical direction hardened:

1. **`max_range_charge_counter`** — Tesla's `vehicle_data` reports a lifetime counter of how
   many times the vehicle has been charged to its true 100 % Maximum-Battery-Range limit. This
   is a first-class signal for **charging-habits health** (one of the `AGENTS.md` Primary
   Objectives: *"How healthy are my charging habits?"* and the Charging metrics list). Today it
   lives **only** inside `raw_data` JSONB, so any dashboard that wants it must run a JSONB
   extraction on every read. Promoting it to a typed, nullable column gives the same cheap,
   index-friendly read path the other charge-enrichment fields (`charge_energy_added`,
   `charger_power`, …) already enjoy (`internal/telemetry/db/migrations/20260716000002_…`).

2. **`latitude` / `longitude`** are columns the nightly snapshot writes from
   `DriveStateTesla.{Latitude,Longitude}`. They are **never consumed by any dashboard or read
   path** — grep across `internal/gateway` finds no handler reads them, and the published
   `telemetry.Reader`/`Snapshot` type exposes them but nothing computes on them. Storing GPS
   per nightly snapshot is also a privacy-sensitive column with zero analytical return. The
   lossless `raw_data` JSONB keeps the value recoverable if a future feature needs it, so
   dropping the typed columns loses no information — only a redundant projection.

3. **`fast_charger_type`** was added by the `telemetry-add-charging-stats` change (RM2) as one
   of six nullable charge-enrichment columns. In practice it has proven low-value as a *typed*
   column: it is a short free-text brand string (`"Tesla"`, `"Combo"`, …) that no dashboard
   filters, sorts, or aggregates on — the charge-brand signal is already captured more
   usefully by the dedicated `supercharger_sessions` ledger (per-session `site_location_name` +
   `billing_type`). Removing it as a promoted column trims an unused typed field while the
   value stays recoverable from `raw_data` (`charge_state.fast_charger_type`) for any future
   need.

Net effect: the snapshot table's typed surface gets **leaner and higher-signal** — one
charging-habits counter added, three low/zero-value columns removed — and read paths get
simpler.

## What Changes

- **ADD** a `max_range_charge_counter INTEGER` (nullable) column to `vehicle_snapshots`,
  extracted from `raw_data`'s `charge_state.max_range_charge_counter` (or the documented
  vehicle-state path — see Design) at capture time, written through the existing
  `insertSnapshot` → `InsertVehicleSnapshot` seam. Nullable for the same reason as the RM2
  charge-enrichment columns: pre-migration rows predating extraction stay NULL rather than
  being misreprerented as `0`.
- **BACKFILL** existing `vehicle_snapshots` rows from their `raw_data` JSONB in a one-time
  migration step, so historical rows carry the counter too (append-only table — no UPDATE in
  the steady state, but a one-shot backfill UPDATE is the established pattern for adding
  extractable columns post-hoc).
- **DROP** the `latitude`, `longitude`, and `fast_charger_type` columns from
  `vehicle_snapshots` via the same migration. The values remain recoverable from `raw_data`
  (drive-state and charge-state objects respectively) — no information is lost.
- **MODIFY** the `telemetry` capability spec:
  - **"Nightly Vehicle Snapshot Capture"** — drop `fast_charger_type` from the enumerated
    charge-telemetry fields (six → five: energy added, charger power, voltage, actual current,
    usable battery level) and **add** `max_range_charge_counter` as a new nullable snapshot
    field with its own backfill/NULL-semantics scenario.
  - **"Latest Snapshot Read Port"** — in the "extracted fields" scenario, remove `latitude,
    longitude` from the enumerated list and add `max_range_charge_counter` (nullable, nil when
    not reported or pre-extraction).
  - **"Snapshot History Read Port"** — no enumerated-field list names the dropped columns, so
    only the shared `Snapshot` type contract carries through; the added counter is covered by
    "all extracted typed fields match the stored values".

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry` — modifies the "Nightly Vehicle Snapshot Capture" requirement (drop
  `fast_charger_type`, add `max_range_charge_counter` + its NULL/backfill semantics) and the
  "Latest Snapshot Read Port" requirement (extracted-fields scenario: drop `latitude`/`longitude`,
  add `max_range_charge_counter`).

## Impact

- **Spec:** `openspec/specs/telemetry/spec.md` (delta only).
- **Schema / migration:** one new goose migration under
  `internal/telemetry/db/migrations/` — `ALTER TABLE vehicle_snapshots
   ADD COLUMN max_range_charge_counter INTEGER`,
  a one-shot `UPDATE … SET max_range_charge_counter =
   (raw_data->'charge_state'->>'max_range_charge_counter')::int WHERE …`
  backfill over existing rows, then `DROP COLUMN latitude, DROP COLUMN longitude,
  DROP COLUMN fast_charger_type` (with their `IF EXISTS` guards in the Down block).
  Pre-migration rows that have no `max_range_charge_counter` in their `raw_data` stay NULL.
- **Generated code:** `internal/telemetry/db/` sqlc output (`models.go`, `query.sql.go`) is
  regenerated after the migration: `VehicleSnapshot` gains `MaxRangeChargeCounter
  pgtype.Int4` and loses `Latitude float64`, `Longitude float64`, `FastChargerType pgtype.Text`;
  `InsertVehicleSnapshotParams` and both read queries (`LatestSnapshotsByAccount`,
  `SnapshotsByVehicleSince`, `ListSnapshotsByVehicle`) SELECT/projection lists update
  correspondingly.
- **Domain write path:** `internal/telemetry/telemetry.go` `Snapshot` struct — add
  `MaxRangeChargeCounter *int` (nullable, same convention as the other charge-enrichment
  pointers; nil = not reported / pre-extraction), drop `Latitude`, `Longitude`,
  `FastChargerType` fields. `internal/telemetry/service.go` `dbStore.insertSnapshot` adds the
  new param and drops the three removed params. `internal/telemetry/mapping.go` drops the
  `Latitude`/`Longitude`/`FastChargerType` rows from the read-side mapping and adds
  `MaxRangeChargeCounter`.
- **Adapter (tesla DTO):** `internal/tesla/types.go` **gains** `MaxRangeChargeCounter int
  json:"max_range_charge_counter"` on the relevant DTO struct (to be confirmed in Design —
  whether it is reported under `charge_state` or `vehicle_state`); `Latitude`/`Longitude`
  **stay** on `DriveStateTesla` and `FastChargerType` **stays** on `ChargeStateTesla` — the
  adapter must keep faithfully decoding every Fleet API field regardless of whether telemetry
  promotes it to a typed column. (Only telemetry drops the typed columns; the anti-corruption
  adapter keeps modeling the source.)
- **Snapshot enrichment:** `internal/telemetry/service.go` `snapshotFrom` extracts
  `MaxRangeChargeCounter` (pointer-wrapped so a reported `0` is a truthful non-NULL reading,
  per the RM2 `D12`/`DSA3` convention) and stops populating `Latitude`/`Longitude`/
  `FastChargerType` on the `Snapshot`.
- **Read/port consumers:** `internal/gateway/handlers` (the `vehicles` card and history
  charts) reference `Snapshot.Latitude/Longitude/FastChargerType` in **zero** handler paths
  today (verified via grep), so no gateway code change is required beyond what the compiler
  flags once the struct fields are removed. Any test fixtures that set the removed fields
  (`internal/telemetry/db_integration_test.go`, `db_read_integration_test.go`,
  `db_sourcea_integration_test.go`, `handlers_test.go`) must drop those assignments and their
  assertions.
- **No API, route, or dependency changes.** The `telemetry.Reader` interface signature is
  unchanged (still returns `[]Snapshot`); only the `Snapshot` struct's fields move.
- **Backfill query:** lives in the migration's Up block as a single `UPDATE` scoped to the
  whole `vehicle_snapshots` table — `UPDATE vehicle_snapshots SET max_range_charge_counter =
   (raw_data->'charge_state'->>'max_range_charge_counter')::int WHERE
   jsonb_typeof(raw_data->'charge_state'->'max_range_charge_counter') = 'number'`. Rows whose
  `raw_data` lacks the path remain NULL. (If Design confirms the path is `vehicle_state.
  max_range_charge_counter`, the path is adjusted accordingly.)
