## MODIFIED Requirements

### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture and store the state of every registered vehicle
across all accounts on each scheduled or on-demand run, keyed to the calendar day the
capture happened on in the poller's configured timezone. At most one stored snapshot
SHALL exist per (account, vehicle, calendar day): if a collection run captures a vehicle
for a day that already has a stored snapshot, the existing row SHALL be replaced with the
new capture's values — including its raw payload and captured timestamp — so the most
recent capture for a given day always wins, rather than an additional row being created
for that day. A capture for a calendar day that does not yet have a stored snapshot SHALL
always create a new row; it SHALL NOT replace any other day's row. The snapshot SHALL also
store the tire pressure monitoring system (TPMS) readings for all four corners of the
vehicle (front-left, front-right, rear-left, rear-right) in bar, the Tesla Fleet API's
native unit for tire pressure. These four fields SHALL be stored as nullable values; a
NULL value means the vehicle did not report TPMS at capture (e.g. the vehicle has no TPMS
sensors, or the field was absent in the response) OR the row predates this extraction, and
a NULL value SHALL NOT be interpreted as zero pressure. A truthfully reported 0.0 bar SHALL
be stored as a non-NULL value. This requirement SUPERSEDES the capability's prior
"immutable"/"append-only" snapshot semantics: a stored snapshot row for a given day CAN now
be overwritten by a later same-day capture. The per-attempt collection record (one row per
vehicle per run, success or failure, used for availability/sleep-behavior tracking) is a
separate capability concern and SHALL remain wholly unaffected by this dedupe behavior —
every collection attempt SHALL still be individually recorded regardless of whether it
results in a new snapshot row or a replace. All other snapshot requirements (raw_data,
miles storage, Km-companion derivation, sentry-mode fidelity) remain unchanged.

#### Scenario: A vehicle's first capture on a given day creates a new snapshot row
- **GIVEN** a registered vehicle with no stored snapshot for the current calendar day (in
  the poller's configured timezone)
- **WHEN** a collection run captures the vehicle
- **THEN** a new snapshot row is stored for that vehicle and day
- **AND** the row's fields match the values reported by the vehicle at capture time

#### Scenario: A second capture on the same calendar day replaces the existing row
- **GIVEN** a registered vehicle that already has a stored snapshot for the current
  calendar day (in the poller's configured timezone)
- **WHEN** a new collection run captures the same vehicle again on that same calendar day
- **THEN** exactly one snapshot row exists for that vehicle and day after the run
- **AND** that row's fields reflect the newer capture's values, not the earlier capture's
- **AND** that row's raw_data reflects the newer capture's full payload
- **AND** that row's captured timestamp reflects the newer capture's time, not the earlier one's

#### Scenario: A capture on a new calendar day creates an additional row, never replacing a prior day
- **GIVEN** a registered vehicle with a stored snapshot for a previous calendar day
- **WHEN** a collection run captures the vehicle on a calendar day that does not yet have a
  stored snapshot
- **THEN** a new snapshot row is created for the new day
- **AND** the previous day's snapshot row is left completely unchanged
- **AND** both rows remain independently retrievable

#### Scenario: The calendar day is determined by the poller's configured timezone, not a fixed zone
- **GIVEN** the poller is configured with a specific timezone
- **AND** a vehicle is captured at a moment that falls on different calendar dates
  depending on which timezone is used to interpret it (e.g. shortly before or after
  midnight UTC)
- **WHEN** the capture is stored
- **THEN** the calendar day the snapshot is attributed to is the day in the poller's
  configured timezone, not necessarily the UTC calendar day

#### Scenario: Per-vehicle collection attempts remain individually recorded regardless of the dedupe behavior
- **GIVEN** a registered vehicle that is captured more than once within the same calendar
  day across separate collection runs
- **WHEN** each run completes
- **THEN** each run records its own individual collection attempt (success or failure)
- **AND** the number of recorded attempts for that vehicle equals the number of collection
  runs, even though at most one snapshot row exists for that day

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
