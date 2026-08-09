## ADDED Requirements

### Requirement: Snapshot Range Read

The telemetry capability SHALL expose a second read-port method — `SnapshotsByVehicleBetween` —
that returns the **bounded** history of stored snapshots for a single vehicle whose **`EffectiveDate`
calendar day falls in a caller-supplied `[start, end]` window inclusive**. The method SHALL share the
`Snapshot` domain type, the single DB→domain mapper (`rowToSnapshot`), and the
`idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` ascending index used by
`SnapshotsByVehicleSince`; it SHALL NOT introduce any new database object (no migration, no index,
no column). The port SHALL be a **clean bounded window**: it takes `(accountID, teslaID, start, end)`
and returns snapshots with `EffectiveDate` calendar day in `[start, end]` inclusive; it SHALL NOT
take a lookback parameter (the 1-day lookback the gateway needs for the first odometer delta is a
gateway concern, not a port parameter). The window boundaries `start` and `end` are whole calendar
days, UTC-midnight-bounded; `end` is **inclusive**. The returned snapshots SHALL be ordered
**ascending by `EffectiveDate`** (equivalently ascending by `captured_at`, since `EffectiveDate` is
monotonic in `CapturedAt`). Callers SHALL receive an empty (non-nil) slice and a nil error when the
vehicle has no snapshots in the window. The distance and range fields SHALL be returned in
kilometres, already converted at capture time, with no companion conversion method on the returned
type. `SnapshotsByVehicleSince` SHALL remain unchanged and available — this method is additive, not
a replacement.

The implementation SHALL honor `EffectiveDate ∈ [start, end]` by filtering on the `captured_at`
TIMESTAMPTZ column with bounds derived from the window (`start_bound = start + 1 calendar day`,
`end_bound = end + 2 calendar days`, the half-open range `captured_at >= start_bound AND
captured_at < end_bound`), because `EffectiveDate = CapturedAt − 1 calendar day` is derived from the
UTC capture instant. Filtering on the stored `captured_date DATE` column instead is forbidden: that
column is computed in the poller's configured timezone (`POLLER_TIMEZONE`), not UTC, so its
relationship to `EffectiveDate` is deployment-dependent and would couple the port's correctness to
the poller's timezone. The query SHALL be a forward range scan over the existing
`(account_id, tesla_id, captured_at)` ascending index with no sort step, and SHALL carry a `LIMIT
400` safety cap mirroring `SnapshotsByVehicleSince`.

#### Scenario: Bounded window returns snapshots whose EffectiveDate falls in `[start, end]`

- **GIVEN** a vehicle with stored snapshots captured on consecutive nights, whose `EffectiveDate`
  calendar days span a 14-day range
- **WHEN** the caller requests `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)`
  for a 7-day sub-window `[start, end]`
- **THEN** every returned `Snapshot.EffectiveDate` calendar day is within `[start, end]` inclusive
- **AND** no returned `Snapshot.EffectiveDate` calendar day is before `start` or after `end`
- **AND** the snapshots are ordered ascending by `EffectiveDate` (oldest-first)

#### Scenario: The `end` boundary is inclusive

- **GIVEN** a vehicle with a snapshot whose `EffectiveDate` calendar day is exactly `end`
- **WHEN** the caller requests `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)`
- **THEN** that snapshot is included in the result
- **AND** a snapshot whose `EffectiveDate` calendar day is `end + 1 day` is NOT included

#### Scenario: The `start` boundary is inclusive

- **GIVEN** a vehicle with a snapshot whose `EffectiveDate` calendar day is exactly `start`
- **WHEN** the caller requests `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)`
- **THEN** that snapshot is included in the result
- **AND** a snapshot whose `EffectiveDate` calendar day is `start - 1 day` is NOT included

#### Scenario: Empty result when the vehicle has no snapshots in the window

- **GIVEN** a vehicle that has no stored snapshots with `EffectiveDate` calendar day in `[start, end]`
- **WHEN** the caller requests `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)`
- **THEN** an empty (non-nil) slice is returned
- **AND** no error is returned

#### Scenario: Per-account and per-vehicle scoping (tenant isolation)

- **GIVEN** two accounts that each own vehicles with stored snapshots, and an account that owns two
  vehicles with snapshots in the requested window
- **WHEN** the caller requests `SnapshotsByVehicleBetween` for one account and one vehicle
- **THEN** only snapshots belonging to that account AND that vehicle are returned
- **AND** no snapshot belonging to another account, or to another vehicle of the same account,
  appears in the result

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs a vehicle's bounded snapshot history
- **WHEN** it obtains that data
- **THEN** it does so exclusively through `telemetry.Reader.SnapshotsByVehicleBetween`
- **AND** it imports no package from `internal/telemetry/db`

#### Scenario: The port takes no lookback parameter

- **GIVEN** a caller (the gateway in tier 2) that needs a 1-day lookback before `start` for the first
  odometer delta
- **WHEN** it calls `SnapshotsByVehicleBetween`
- **THEN** it supplies the lookback by passing `start - 1 day` as the `start` argument
- **AND** the method signature has no dedicated lookback parameter
- **AND** the port returns the window it was asked for, including any pre-`start` snapshot supplied
  via the shifted `start`

#### Scenario: The query reuses the existing captured-at index and adds no DB object

- **GIVEN** the `vehicle_snapshots` table and the `idx_vehicle_snapshots_vehicle_time
  (account_id, tesla_id, captured_at)` ascending index created by migration
  `20260710000002_init_telemetry.sql`
- **WHEN** `SnapshotsByVehicleBetween` is implemented
- **THEN** its SQL filters on `captured_at` with a half-open `>= start_bound AND < end_bound` range
- **AND** the query is served as a forward range scan over that index with no `Sort` node
- **AND** no new migration, no new index, and no new column is introduced by this change

#### Scenario: `SnapshotsByVehicleSince` remains available unchanged

- **GIVEN** any existing caller of `telemetry.Reader.SnapshotsByVehicleSince`
- **WHEN** this change is applied
- **THEN** the `SnapshotsByVehicleSince` method signature and behavior are unchanged
- **AND** the caller continues to compile and behave identically
- **AND** the method is neither removed nor marked deprecated by this change