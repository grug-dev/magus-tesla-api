## ADDED Requirements

### Requirement: Snapshot Last-Updated Timestamp Is Exposed On The Domain Type

The telemetry capability SHALL expose, on the `Snapshot` domain type, the timestamp of when that
row was last written or replaced (the same value the `vehicle_snapshots.updated_at` column
already stores on every row, refreshed by the same-day-replace UPSERT). This field carries no
new database column and no new write-path behavior — it exposes a value telemetry already
persists but did not previously map onto its public domain type.

#### Scenario: A snapshot's last-updated timestamp reflects its most recent write
- **GIVEN** a snapshot row that was first inserted at one instant and later replaced by a
  same-day re-capture at a later instant (the existing "latest capture wins" rule)
- **WHEN** that vehicle's snapshot is read through any of telemetry's read ports
- **THEN** the returned `Snapshot.UpdatedAt` reflects the later, replacing write's instant, not
  the original insert's

### Requirement: Snapshot Updated-Since Read Port

The telemetry capability SHALL expose a read port through which other modules can retrieve every
stored snapshot for a single vehicle whose last-updated timestamp is at or after a
caller-supplied instant, without accessing the telemetry module's database tables directly. The
port SHALL identify the vehicle by its account and its Tesla numeric id, and SHALL return the
existing `Snapshot` domain type. Callers SHALL receive an empty result (not an error) when no
snapshot for that vehicle has been updated at or after the given instant.

#### Scenario: Snapshots updated at or after the given instant are returned
- **GIVEN** a vehicle with snapshots last updated at various instants, some before and some at or
  after a given instant
- **WHEN** the caller requests that vehicle's snapshots updated since that instant
- **THEN** only the snapshots whose `UpdatedAt` is at or after the given instant are returned

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no snapshot updated at or after the requested instant
- **WHEN** the caller requests that vehicle's snapshots updated since that instant
- **THEN** an empty collection is returned, and no error is returned

#### Scenario: Callers never access the telemetry database directly for this port either
- **GIVEN** any caller that needs to detect which of a vehicle's snapshots changed recently
- **WHEN** it obtains that data
- **THEN** it does so exclusively through this read port
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Supercharger Session Updated-Since Read Port

The telemetry capability SHALL expose a read port through which other modules can retrieve every
stored Supercharger session for a single vehicle whose `updated_at` is at or after a
caller-supplied instant, without accessing the telemetry module's database tables directly. The
port SHALL identify the vehicle by its account and its Tesla numeric id, and SHALL return the
existing `SuperchargerSession` domain type. Callers SHALL receive an empty result (not an error)
when no session for that vehicle has been updated at or after the given instant.

#### Scenario: A revised session's billing state is detected by this port
- **GIVEN** a Supercharger session originally stored weeks ago, whose billing fields are revised
  today (its stored `updated_at` refreshes to today)
- **WHEN** the caller requests that vehicle's sessions updated since a recent instant
- **THEN** the revised session is included in the result, even though its `ChargeStartDateTime`/
  `ChargeStopDateTime` are weeks in the past

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no Supercharger session updated at or after the requested instant
- **WHEN** the caller requests that vehicle's sessions updated since that instant
- **THEN** an empty collection is returned, and no error is returned

#### Scenario: Callers never access the telemetry database directly for this port either
- **GIVEN** any caller that needs to detect which of a vehicle's Supercharger sessions changed
  recently
- **WHEN** it obtains that data
- **THEN** it does so exclusively through this read port
- **AND** it imports no package from `internal/telemetry/db`
