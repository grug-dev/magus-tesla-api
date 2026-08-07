## MODIFIED Requirements

### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture one snapshot of every registered vehicle across all
accounts on each scheduled run, and SHALL store every unit-bearing value in the unit it is
displayed in — distance and range in kilometres, temperature in degrees Celsius, and tire
pressure in PSI — converting at capture time. Every stored unit-bearing value SHALL be identified
by a name that carries its unit, and the capability SHALL NOT store a unit-bearing value under a
name that leaves its unit implicit. In addition to the fields already required, the snapshot SHALL
also store the tire pressure monitoring system (TPMS) readings for all four corners of the vehicle
(front-left, front-right, rear-left, rear-right). These four fields SHALL be stored as nullable
values; a NULL value means the vehicle did not report TPMS at capture (e.g. the vehicle has no
TPMS sensors, or the field was absent in the response) OR the row predates this extraction, and a
NULL value SHALL NOT be interpreted as zero pressure. A truthfully reported zero pressure SHALL be
stored as a non-NULL value. The capability SHALL continue to store the complete raw payload
unconverted, in the units the Fleet API reported, so that no information is lost by the
conversion. All other snapshot requirements (daily-replace upsert semantics, sentry-mode fidelity)
remain unchanged.

#### Scenario: A snapshot captures TPMS pressure for all four corners
- **GIVEN** a registered vehicle that is parked and its vehicle_data reports tire pressure values
  for all four corners when the nightly snapshot is captured
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes non-NULL values for all four TPMS pressure fields
- **AND** each stored value is the corresponding value reported by the vehicle converted to PSI
- **AND** the snapshot's raw_data still contains the full vehicle_data payload with the pressure
  values in the unit the Fleet API reported

#### Scenario: Distance and range are stored in kilometres
- **GIVEN** a registered vehicle whose vehicle_data reports an odometer reading and an estimated
  battery range
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored odometer and battery range are expressed in kilometres
- **AND** each stored value is the reported value converted from the Fleet API's native distance
  unit exactly once, at capture time
- **AND** the snapshot's raw_data still contains the values in the unit the Fleet API reported

#### Scenario: Temperature is stored as reported, without conversion
- **GIVEN** a registered vehicle whose vehicle_data reports inside and outside temperatures
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored inside and outside temperatures are expressed in degrees Celsius
- **AND** the stored values equal the reported values, because the Fleet API already reports
  temperature in degrees Celsius and no conversion is applied

#### Scenario: A snapshot stores NULL TPMS pressure when the vehicle does not report TPMS
- **GIVEN** a registered vehicle whose vehicle_data does not include TPMS pressure values
  at capture time (e.g. the vehicle has no TPMS sensors)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure fields on the snapshot are NULL for all four corners
- **AND** all existing snapshot fields (battery level, range, odometer, etc.) are still
  populated normally

#### Scenario: A truthfully reported zero pressure is stored as non-NULL
- **GIVEN** a registered vehicle that reports a tire pressure of exactly zero for one or more
  corners (e.g. a flat tire)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure value for the affected corner is non-NULL
- **AND** the stored value is 0.0 PSI (not NULL — zero is a truthful reading)

#### Scenario: Pre-extraction snapshot rows have NULL for the TPMS pressure fields
- **GIVEN** a vehicle_snapshots row written before the TPMS column migration was applied
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** the four TPMS pressure fields on the Snapshot are NULL
- **AND** the caller can still access the raw_data payload to extract the TPMS values
  retroactively if needed

#### Scenario: Rows captured before the unit change are converted, not left mixed
- **GIVEN** snapshot rows that were captured while values were stored in the Fleet API's native
  units
- **WHEN** the display-unit change is applied
- **THEN** those rows' distance, range, and tire-pressure values are converted in place to the
  display units
- **AND** no row remains stored in a different unit from any other row
- **AND** rows whose tire-pressure values were NULL remain NULL rather than becoming zero

### Requirement: Latest Snapshot Read Port
The telemetry capability's read port SHALL return snapshots whose values are already expressed in
their display units, and SHALL NOT perform, or require its callers to perform, any unit conversion
on read. The read port SHALL return the TPMS tire pressure fields for all four corners, expressed
in PSI, alongside the existing snapshot fields. The capability SHALL NOT expose companion
conversion methods on the returned snapshot type; every returned field SHALL be directly usable in
the unit its name declares. The pressure fields SHALL remain nullable, preserving the
nil/not-reported distinction from the stored value itself rather than re-deriving it per call. No
new read method is introduced; the four TPMS fields ride on the existing Snapshot returned by the
existing read port methods.

#### Scenario: Values are returned ready to use, with no conversion on read
- **GIVEN** a stored snapshot for a registered vehicle
- **WHEN** a caller retrieves it through LatestSnapshotsByAccount or SnapshotsByVehicleSince
- **THEN** the odometer and battery range fields are expressed in kilometres, the temperature
  fields in degrees Celsius, and the tire pressure fields in PSI
- **AND** the caller performs no arithmetic and calls no conversion method to obtain those units
- **AND** the returned type exposes no companion conversion method for any of those fields

#### Scenario: Nil fidelity is preserved by the stored pressure value
- **GIVEN** a snapshot row for which TPMS pressure was not reported (NULL in the database)
- **WHEN** a caller retrieves the snapshot through LatestSnapshotsByAccount or
  SnapshotsByVehicleSince and reads any of the four pressure fields
- **THEN** each pressure field is nil
- **AND** the caller can distinguish "not reported" from "reported 0.0 PSI"

#### Scenario: Callers never access the telemetry database directly for TPMS data
- **GIVEN** any caller that needs to display or process TPMS tire pressure data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the Reader port interface (LatestSnapshotsByAccount
  or SnapshotsByVehicleSince), reading the pressure fields on the returned Snapshot
- **AND** it imports no package from internal/telemetry/db

### Requirement: Snapshot History Read Port
The telemetry capability SHALL expose a read port through which other modules (the gateway in
particular) can retrieve the **history** of stored snapshots for a single vehicle — all snapshots
captured at or after a caller-supplied instant — without accessing the telemetry module's database
tables directly. The port SHALL identify the vehicle by its account and its Tesla numeric id, and
SHALL return the existing `Snapshot` domain type (including all extracted typed fields and the raw
payload), ordered **oldest-first** by capture time. The window boundary is supplied by the caller
as an absolute instant (`since`, inclusive); the port SHALL NOT compute the window itself. Callers
SHALL receive an empty result (not an error) when the vehicle has no snapshots in the window. The
distance and range fields SHALL be returned in kilometres, already converted at capture time, and
the returned type SHALL carry no companion method for deriving them.

#### Scenario: History is returned oldest-first for a vehicle within its account

- **GIVEN** an account that has stored several snapshots for one vehicle across different
  collection runs, all captured at or after a given instant
- **WHEN** the caller requests that vehicle's snapshot history since that instant (passing the
  account id, the vehicle's Tesla id, and the instant)
- **THEN** every snapshot captured at or after the instant is returned
- **AND** the snapshots are ordered oldest-first by capture time
- **AND** all extracted fields (including odometer, battery level, and battery range) match the
  stored values for each snapshot
- **AND** the distance and range fields are returned in kilometres with no companion conversion
  method on the returned type

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
