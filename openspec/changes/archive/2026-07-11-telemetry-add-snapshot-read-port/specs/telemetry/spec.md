## ADDED Requirements

### Requirement: Latest Snapshot Read Port

The telemetry capability SHALL expose a read port through which other modules (the
gateway in particular) can retrieve the most recently stored snapshot for each vehicle
belonging to a given account, without accessing the telemetry module's database tables
directly. The read port SHALL return the existing `Snapshot` domain type (including all
extracted typed fields and the raw payload). Callers SHALL receive an empty result (not
an error) when the account has no stored snapshots.

#### Scenario: Latest snapshot is returned for each vehicle of an account

- **GIVEN** an account that has stored snapshots for two vehicles, where each vehicle
  has at least two snapshots from different collection runs
- **WHEN** the caller requests the latest snapshots for that account
- **THEN** exactly one snapshot is returned per vehicle
- **AND** the returned snapshot for each vehicle is the one with the most recent
  capture time
- **AND** all extracted fields (battery level, range, charging state, charge limit,
  odometer, temperatures, locked, sentry mode, car version, latitude, longitude) are
  present and match the stored values for that snapshot
- **AND** the distance and range fields are returned in miles (the Tesla-native unit)
  with no kilometre field on the returned type

#### Scenario: Empty result for an account with no snapshots

- **GIVEN** an account that has never had a snapshot stored (e.g. the nightly
  collection has not run yet, or the account just connected)
- **WHEN** the caller requests the latest snapshots for that account
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: Per-account scoping — another account's data is not returned

- **GIVEN** two accounts each owning vehicles with stored snapshots
- **WHEN** the caller requests the latest snapshots for account A
- **THEN** only snapshots belonging to account A are returned
- **AND** no snapshot belonging to account B appears in the result

#### Scenario: Sentry-mode nil fidelity is preserved on read

- **GIVEN** a stored snapshot whose sentry-mode value was recorded as absent (unknown)
  because the vehicle did not report it at capture time
- **WHEN** that snapshot is returned through the read port
- **THEN** the sentry-mode field on the returned snapshot is nil (unknown), distinct
  from a value of off

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs to display or use telemetry snapshot data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the telemetry module's read port interface
- **AND** it imports no package from `internal/telemetry/db`
