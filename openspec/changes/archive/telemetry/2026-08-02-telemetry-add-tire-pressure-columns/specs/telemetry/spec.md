## MODIFIED Requirements

### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture one immutable snapshot of every registered vehicle
across all accounts on each scheduled run. In addition to the fields already required,
the snapshot SHALL also store the tire pressure monitoring system (TPMS) readings for all
four corners of the vehicle (front-left, front-right, rear-left, rear-right) in bar, the
Tesla Fleet API's native unit for tire pressure. These four fields SHALL be stored as
nullable values; a NULL value means the vehicle did not report TPMS at capture (e.g. the
vehicle has no TPMS sensors, or the field was absent in the response) OR the row predates
this extraction, and a NULL value SHALL NOT be interpreted as zero pressure. A truthfully
reported 0.0 bar SHALL be stored as a non-NULL value. All other snapshot requirements
(raw_data, append-only, miles storage, Km-companion derivation, sentry-mode fidelity)
remain unchanged.

#### Scenario: A snapshot captures TPMS pressure for all four corners
- **GIVEN** a registered vehicle that is parked and its vehicle_data reports
  tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, and tpms_pressure_rr values in
  bar when the nightly snapshot is captured
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes non-NULL values for all four TPMS pressure fields
- **AND** each stored value matches the corresponding value reported by the vehicle (in
  bar, the API-native unit)
- **AND** the snapshot's raw_data still contains the full vehicle_data payload

#### Scenario: A snapshot stores NULL TPMS pressure when the vehicle does not report TPMS
- **GIVEN** a registered vehicle whose vehicle_data does not include TPMS pressure values
  at capture time (e.g. the vehicle has no TPMS sensors)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure fields on the snapshot are NULL for all four corners
- **AND** all existing snapshot fields (battery level, range, odometer, etc.) are still
  populated normally

#### Scenario: A truthfully reported zero bar pressure is stored as non-NULL
- **GIVEN** a registered vehicle that reports a TPMS pressure of exactly 0.0 bar for
  one or more corners (e.g. a flat tire)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure value for the affected corner is non-NULL
- **AND** the stored value is 0.0 bar (not NULL — zero is a truthful reading)

#### Scenario: Pre-extraction snapshot rows have NULL for the TPMS pressure fields
- **GIVEN** a vehicle_snapshots row written before the TPMS column migration was applied
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** the four TPMS pressure fields on the Snapshot are NULL
- **AND** the caller can still access the raw_data payload to extract the TPMS values
  retroactively if needed

### Requirement: Latest Snapshot Read Port
The telemetry capability's read port SHALL return snapshots that include the TPMS tire
pressure fields for all four corners alongside the existing snapshot fields. The read port
SHALL also expose four value-receiver companion methods — one per corner — that derive the
tire pressure in PSI (pounds per square inch) from the stored bar value. These companion
methods SHALL return a nil pointer when the stored bar field is nil (preserving the
nil/not-reported distinction through the conversion), and SHALL return a non-nil pointer
to the derived PSI value otherwise. Callers SHALL NOT receive a loss of nil fidelity when
converting from bar to PSI. No new read method is introduced; the four TPMS fields ride on
the existing Snapshot returned by the existing read port methods.

#### Scenario: Nil fidelity is preserved through the bar-to-PSI companion conversion
- **GIVEN** a snapshot row for which TPMS pressure was not reported (NULL in the database)
- **WHEN** a caller retrieves the snapshot through LatestSnapshotsByAccount or
  SnapshotsByVehicleSince and calls any of the four PSI companion methods
- **THEN** each PSI companion method returns nil
- **AND** the caller can distinguish "not reported" from "reported 0.0 PSI"

#### Scenario: Non-nil bar value is correctly converted to PSI by the companion method
- **GIVEN** a snapshot row with a non-nil TPMS pressure value stored in bar
- **WHEN** a caller retrieves the snapshot through the read port and calls the PSI
  companion for that corner
- **THEN** the companion method returns a non-nil PSI value
- **AND** the returned PSI value is the bar value multiplied by the barToPSI conversion
  factor (14.503773773)

#### Scenario: Callers never access the telemetry database directly for TPMS data
- **GIVEN** any caller that needs to display or process TPMS tire pressure data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the Reader port interface (LatestSnapshotsByAccount
  or SnapshotsByVehicleSince), reading the TpmsPressure* fields on the returned Snapshot
- **AND** it imports no package from internal/telemetry/db

#### Scenario: Snapshot struct literals are not broken by the new TPMS fields
- **GIVEN** existing code that constructs a Snapshot struct literal by naming fields
  (not positionally)
- **WHEN** the four new TpmsPressure* fields are added to the Snapshot struct
- **THEN** the existing code continues to compile without modification
- **AND** the new fields default to nil (zero value for *float64)
