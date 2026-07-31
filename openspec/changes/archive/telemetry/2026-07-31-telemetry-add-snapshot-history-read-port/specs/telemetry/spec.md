## ADDED Requirements

### Requirement: Snapshot History Read Port

The telemetry capability SHALL expose a read port through which other modules (the gateway in
particular) can retrieve the **history** of stored snapshots for a single vehicle — all snapshots
captured at or after a caller-supplied instant — without accessing the telemetry module's database
tables directly. The port SHALL identify the vehicle by its account and its Tesla numeric id, and
SHALL return the existing `Snapshot` domain type (including all extracted typed fields and the raw
payload), ordered **oldest-first** by capture time. The window boundary is supplied by the caller
as an absolute instant (`since`, inclusive); the port SHALL NOT compute the window itself. Callers
SHALL receive an empty result (not an error) when the vehicle has no snapshots in the window. The
distance and range fields SHALL be returned in miles (the Tesla-native unit) with no kilometre
field on the returned type.

#### Scenario: History is returned oldest-first for a vehicle within its account

- **GIVEN** an account that has stored several snapshots for one vehicle across different
  collection runs, all captured at or after a given instant
- **WHEN** the caller requests that vehicle's snapshot history since that instant (passing the
  account id, the vehicle's Tesla id, and the instant)
- **THEN** every snapshot captured at or after the instant is returned
- **AND** the snapshots are ordered oldest-first by capture time
- **AND** all extracted fields (including odometer, battery level, and battery range) match the
  stored values for each snapshot
- **AND** the distance and range fields are returned in miles with no kilometre field on the
  returned type

#### Scenario: The `since` boundary is inclusive

- **GIVEN** a vehicle with a snapshot whose capture time is exactly the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** the snapshot captured exactly at `since` is included in the result

#### Scenario: Snapshots older than the window are excluded

- **GIVEN** a vehicle with snapshots both before and after the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** only snapshots captured at or after `since` are returned
- **AND** snapshots captured before `since` do not appear in the result

#### Scenario: Empty result when the vehicle has no snapshots in the window

- **GIVEN** a vehicle that has no stored snapshots at or after the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: Per-account and per-vehicle scoping

- **GIVEN** two accounts that each own vehicles with stored snapshots, and an account that owns two
  vehicles
- **WHEN** the caller requests history for one account and one vehicle
- **THEN** only snapshots belonging to that account AND that vehicle are returned
- **AND** no snapshot belonging to another account, or to another vehicle of the same account,
  appears in the result

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs to display or use a vehicle's snapshot history
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the telemetry module's read port interface
- **AND** it imports no package from `internal/telemetry/db`
